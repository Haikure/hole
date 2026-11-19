package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestPeerRelayHandshakePreservesSignalFailure(t *testing.T) {
	for _, code := range []string{"peer_identity_mismatch", "protocol_mismatch", "peer_upgrade_required", "network_error"} {
		t.Run(code, func(t *testing.T) {
			c, p := lifecycleCoordinator(t)
			legReady := iceSignalMessage{SignalMessage: SignalMessage{PeerDevice: "relay"},
				TransportID: "leg", TransportGeneration: 3, PeerRuntimeID: "relay-runtime", LeaseUntil: p.ready.LeaseUntil}
			outer := &muxPeer{ctx: c.ctx, peerRelay: true}
			c.peers["leg"] = &icePeer{online: true, ready: legReady, stats: PeerTransportSnapshot{PathType: "direct"},
				active: &liveICETransport{ready: legReady, mux: outer}}
			round := 1
			ready := p.ready
			ready.Profile, ready.Phase, ready.RelayPairing, ready.RelayRound = ProfileICE, "peer_relay", relayPairingAll, &round
			// Accept without a remote packet: only the signaling failure should
			// terminate the handshake, preserving its cause through cancellation.
			ready.InitiatorID = "peer"
			ready.PeerRelay = &peerRelayGrant{Route: strings.Repeat("1", 32), Device: "relay", Runtime: "relay-runtime",
				Devices: [2]string{c.deviceID(), "peer"}, Links: [2]peerRelayLink{{ID: "leg", Generation: 3}, {ID: "other-leg", Generation: 4}}}
			ctx, cancel := context.WithCancel(c.ctx)
			defer cancel()
			a := &iceAttempt{ctx: ctx, cancel: cancel, ready: ready, failed: make(chan error, 1)}
			p.ready, p.pending = ready, a
			failure := &Fault{Code: code, Message: "fixture signaling failure"}
			a.failed <- failure
			p.attemptPeerRelay(a)
			fatal := code != "network_error"
			if p.pending != nil || p.terminal != fatal || p.stats.Error == nil {
				t.Fatalf("pending=%v terminal=%v error=%v", p.pending, p.terminal, p.stats.Error)
			}
			if fatal && (p.stats.State != "error" || p.stats.Error.Code != code || p.stats.Error.Message != failure.Message) {
				t.Fatalf("lost signaling fault: state=%s error=%v", p.stats.State, p.stats.Error)
			}
			select {
			case report := <-c.sink.messages:
				if fatal {
					if report.Type != "transport_failed" || report.Code != code {
						t.Fatalf("unexpected report: %+v", report)
					}
				} else if report.Type != "transport_restart" || report.RelayRound == nil || *report.RelayRound != round || report.Reason != "peer_relay_failed" {
					t.Fatalf("fallback skipped held round: %+v", report)
				}
			default:
				t.Fatal("missing failure report or fallback")
			}
			if len(c.peerRelayPaths) != 0 || !errors.Is(a.ctx.Err(), context.Canceled) {
				t.Fatal("handshake did not release its route")
			}
		})
	}
}

// One local three-device scenario covers the real Worker, both QUIC layers,
// fragmented datagrams and fallback without depending on Internet NAT behavior.
func TestPeerRelayThreeDevicesAndFallback(t *testing.T) {
	server, hibernate := actualWorkerFixture(t)
	turnURL, stopTURN := startTURNRelay(t, "udp")
	defer stopTURN()
	tcp, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	go func() {
		for {
			conn, err := tcp.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); _, _ = io.Copy(conn, conn) }()
		}
	}()
	udp, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	go func() {
		buf := make([]byte, 65535)
		for {
			n, addr, err := udp.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = udp.WriteTo(buf[:n], addr)
		}
	}()
	a, b, relay := NewEngine(Options{}), NewEngine(Options{}), NewEngine(Options{})
	defer a.Close()
	defer b.Close()
	defer relay.Close()
	ra, rb, rr := iceFixtureRequest(server, "alpha"), iceFixtureRequest(server, "beta"), iceFixtureRequest(server, "relay")
	for _, r := range []*Request{&ra, &rb, &rr} {
		// Voice relationships provide the two already-established relay legs.
		r.Config.Voice.Enabled = true
		r.Config.ICE.PortMapping = "off"
		r.Config.ICE.NATSTUNURLs = []string{}
		r.Config.TURN = TURNConfig{Mode: "manual", URLs: []string{turnURL}, Username: "user", Credential: "secret", Order: []string{"udp"}}
	}
	tcpPort, udpPort := unusedTCPPort(t), unusedTCPPort(t)
	ra.Config.Provide = []Provide{{ID: "tcp", Service: ServiceEndpoint{"tcp", "127.0.0.1", tcp.Addr().(*net.TCPAddr).Port}}, {ID: "udp", Service: ServiceEndpoint{"udp", "127.0.0.1", udp.LocalAddr().(*net.UDPAddr).Port}}}
	rb.Config.Consume = []Consume{{ID: "tcp", Expose: HostPort{"127.0.0.1", tcpPort}}, {ID: "udp", Expose: HostPort{"127.0.0.1", udpPort}}}
	for _, item := range []struct {
		e *Engine
		r Request
	}{{a, ra}, {b, rb}, {relay, rr}} {
		if err := item.e.Start(item.r); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range []*Engine{a, b, relay} {
		waitSnapshotWithin(t, e, 8*time.Second, func(s Snapshot) bool {
			if len(s.PeerTransports) != 2 {
				return false
			}
			for _, p := range s.PeerTransports {
				if p.State != "active" || p.PathType != "direct" {
					return false
				}
			}
			return true
		})
	}
	a.mu.Lock()
	coordinator := a.run.ice
	a.mu.Unlock()
	var pair *icePeer
	for _, p := range coordinator.peerList() {
		if p.peerID() == "beta" {
			pair = p
		}
	}
	if pair == nil {
		t.Fatal("missing pair")
	}
	// Force only this pair past its direct budget; both relay legs stay real.
	pair.restartRound(1, "", "direct_exhausted")
	peerActive := func(s Snapshot) bool {
		for _, p := range s.PeerTransports {
			if p.PeerID == "alpha" || p.PeerID == "beta" {
				return p.State == "active" && p.PathType == "peer_relay" && p.RelayDevice == "relay" && p.ActiveChannels == 2
			}
		}
		return false
	}
	for _, e := range []*Engine{a, b} {
		waitSnapshotWithin(t, e, 12*time.Second, peerActive)
	}
	conn, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", tcpPort), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	checkTCP := func(text string) {
		t.Helper()
		_ = conn.SetDeadline(time.Now().Add(8 * time.Second))
		if _, err := conn.Write([]byte(text)); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len(text))
		if _, err := io.ReadFull(conn, got); err != nil || string(got) != text {
			t.Fatalf("TCP: %q %v", got, err)
		}
	}
	checkTCP("peer relay TCP")
	u, err := net.Dial("udp4", fmt.Sprintf("127.0.0.1:%d", udpPort))
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	_ = u.SetDeadline(time.Now().Add(8 * time.Second))
	payload := bytes.Repeat([]byte("fragmented"), 300)
	if _, err = u.Write(payload); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 65535)
	if n, err := u.Read(got); err != nil || !bytes.Equal(got[:n], payload) {
		t.Fatalf("UDP: %v", err)
	}
	hibernate()
	// Renew after hibernation to verify the persisted route can be replayed.
	_ = coordinator.send(signalMessage("transport_sync"))
	checkTCP("after Worker hibernation")
	if err := relay.Stop(); err != nil {
		t.Fatal(err)
	}
	// A failed outer leg must enter the held ordinary relay round, not skip it.
	waitSnapshotWithin(t, a, 12*time.Second, func(s Snapshot) bool {
		for _, p := range s.PeerTransports {
			if p.PeerID == "beta" {
				return p.State == "active" && p.Phase == "relay_udp" && p.PathType != "peer_relay"
			}
		}
		return false
	})
	checkTCP("same TCP socket after fallback")
}
