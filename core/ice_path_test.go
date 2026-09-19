package core

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"syscall"
	"testing"
	"time"

	"github.com/pion/ice/v4"
	"github.com/quic-go/quic-go"
)

func TestRelayLegsAndCandidateSocket(t *testing.T) {
	host, err := ice.NewCandidateHost(&ice.CandidateHostConfig{Network: "udp", Address: "192.0.2.1", Port: 10000, Component: 1})
	if err != nil {
		t.Fatal(err)
	}
	srflx, err := ice.NewCandidateServerReflexive(&ice.CandidateServerReflexiveConfig{Network: "udp", Address: "203.0.113.1", Port: 20000, Component: 1, RelAddr: "192.0.2.1", RelPort: 10000})
	if err != nil {
		t.Fatal(err)
	}
	relay, err := ice.NewCandidateRelay(&ice.CandidateRelayConfig{Network: "udp", Address: "198.51.100.1", Port: 40000, Component: 1, RelayProtocol: "udp"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		local, remote ice.Candidate
		legs          int
	}{{host, host, 0}, {srflx, relay, 1}, {relay, srflx, 1}, {relay, relay, 2}} {
		if got := relayLegs(tc.local, tc.remote); got != tc.legs {
			t.Fatalf("%s ↔ %s: %d legs, want %d", tc.local.Type(), tc.remote.Type(), got, tc.legs)
		}
	}
	// A reflexive candidate sends from its base socket; that is the socket
	// a direct-phase QUIC connection must tune for MTU discovery.
	if candidateSocket(host) != "192.0.2.1:10000" || candidateSocket(srflx) != "192.0.2.1:10000" || candidateAddress(srflx) != "203.0.113.1:20000" {
		t.Fatal(candidateSocket(host), candidateSocket(srflx), candidateAddress(srflx))
	}
	if relayNominationWait <= 2*time.Second {
		t.Fatal("relay nomination wait does not exceed Pion's default")
	}
}

func TestICEChecksExhaustedNeedsEveryPairFailed(t *testing.T) {
	failed := ice.CandidatePairStats{State: ice.CandidatePairStateFailed}
	if iceChecksExhausted([]ice.CandidatePairStats{failed, {State: ice.CandidatePairStateInProgress}}) {
		t.Fatal("in-progress pair reported as exhausted")
	}
	if !iceChecksExhausted([]ice.CandidatePairStats{failed, failed}) || !iceChecksExhausted(nil) {
		t.Fatal("all-failed checklist not reported")
	}
}

func TestQUICConfigProbesMTUOnlyOnDirectPaths(t *testing.T) {
	if iceQUICConfig("direct").DisablePathMTUDiscovery {
		t.Fatal("direct path keeps the 1200-byte floor")
	}
	for _, phase := range []string{"relay_udp", "relay_tcp_80", "relay_tcp", "relay_tls_443", "relay_tls"} {
		if cfg := iceQUICConfig(phase); !cfg.DisablePathMTUDiscovery || cfg.InitialPacketSize != 1200 || !cfg.EnableDatagrams {
			t.Fatalf("%s: %+v", phase, cfg)
		}
	}
}

type recordingRawConn struct{ calls *int }

func (r recordingRawConn) Control(f func(fd uintptr)) error {
	*r.calls++
	f(0)
	return nil
}
func (recordingRawConn) Read(func(fd uintptr) bool) error  { return errors.ErrUnsupported }
func (recordingRawConn) Write(func(fd uintptr) bool) error { return errors.ErrUnsupported }

type recordingSocket struct{ calls *int }

func (s recordingSocket) SyscallConn() (syscall.RawConn, error) { return recordingRawConn(s), nil }
func (s recordingSocket) SetReadBuffer(int) error               { *s.calls++; return nil }
func (s recordingSocket) SetWriteBuffer(int) error              { *s.calls++; return nil }

func TestMTUProbingConnAppliesSocketOptionsToEveryGatheredSocket(t *testing.T) {
	platform := DefaultPlatform{}
	network, err := newICENetwork(context.Background(), platform)
	if err != nil {
		t.Fatal(err)
	}
	first, err := network.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := network.CreateListenConfig(nil).ListenPacket(context.Background(), "udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if got := len(network.socketsFor("")); got != 2 {
		t.Fatalf("tracked %d sockets, want 2", got)
	}
	// The socket carrying the selected pair is applied last so quic-go's
	// verdicts come from it. SyscallConn hands out a fresh wrapper each call,
	// so compare descriptors.
	fdOf := func(socket rawSocket) (fd uintptr) {
		raw, err := socket.SyscallConn()
		if err != nil {
			t.Fatal(err)
		}
		if err = raw.Control(func(f uintptr) { fd = f }); err != nil {
			t.Fatal(err)
		}
		return fd
	}
	ordered := network.socketsFor(first.LocalAddr().String())
	if len(ordered) != 2 || fdOf(ordered[1]) != fdOf(first.(rawSocket)) || fdOf(ordered[0]) != fdOf(second.(rawSocket)) {
		t.Fatal("selected socket was not moved last")
	}
	receiver, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	sender, err := net.DialUDP("udp4", nil, receiver.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	calls := 0
	probing := &mtuProbingConn{icePacketConn: newICEPacketConn(sender), sockets: []rawSocket{recordingSocket{&calls}, recordingSocket{&calls}}}
	raw, err := probing.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	touched := 0
	if err = raw.Control(func(uintptr) { touched++ }); err != nil || touched != 2 || calls != 2 {
		t.Fatalf("control fan-out: err=%v touched=%d calls=%d", err, touched, calls)
	}
	if err = probing.SetReadBuffer(1); err != nil || calls != 4 {
		t.Fatalf("buffer fan-out: err=%v calls=%d", err, calls)
	}
	if _, err = (&mtuProbingConn{icePacketConn: newICEPacketConn(sender)}).SyscallConn(); err == nil {
		t.Fatal("socket-less transport offered a raw connection")
	}
	// quic-go only starts discovery when the wrapped connection accepts DF;
	// real loopback sockets do.
	wrapped := &mtuProbingConn{icePacketConn: newICEPacketConn(sender), sockets: network.socketsFor("")}
	transport := &quic.Transport{Conn: wrapped}
	defer transport.Close()
	if _, err = transport.Listen(mustTLS(t), iceQUICConfig("direct")); err != nil {
		t.Fatal(err)
	}
}

func mustTLS(t *testing.T) *tls.Config {
	t.Helper()
	cfg, err := makeTLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.NextProtos = []string{iceALPN}
	return cfg
}

type fixedLimitConn struct{ limit int }

func (c fixedLimitConn) SendDatagram(p []byte) error {
	if len(p) > c.limit {
		return &quic.DatagramTooLargeError{MaxDatagramPayloadSize: int64(c.limit)}
	}
	return nil
}

func TestUDPFrameGrowsWithDiscoveredMTUButRateLimited(t *testing.T) {
	frame, probeAt, now := udpInitialFrameSize, time.Time{}, time.Now()
	growUDPFrame(fixedLimitConn{1400}, &frame, &probeAt, now)
	if frame != 1400-udpFrameMargin {
		t.Fatal("frame did not grow with the datagram limit", frame)
	}
	growUDPFrame(fixedLimitConn{3000}, &frame, &probeAt, now.Add(time.Second))
	if frame != 1400-udpFrameMargin {
		t.Fatal("probe ignored its rate limit", frame)
	}
	growUDPFrame(fixedLimitConn{1000}, &frame, &probeAt, now.Add(udpFrameProbe))
	if frame != 1400-udpFrameMargin {
		t.Fatal("probe shrank the frame; DatagramTooLargeError owns shrinking", frame)
	}
	if datagramLimit(nil) != 0 || datagramLimit(fixedLimitConn{1200}) != 1200 {
		t.Fatal("datagram limit probe")
	}
}
