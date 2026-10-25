package core

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Rejects ICEPlatform collection: ICE candidate gathering would fail. Every
// external socket and DNS operation still goes through a pinned platform.
type fixedTestPlatform struct {
	DefaultPlatform
	resolves   atomic.Int32
	listens    atomic.Int32
	candidates atomic.Int32
	resolved   atomic.Value
}

func (p *fixedTestPlatform) SnapshotNetwork(context.Context) (Platform, NetworkSnapshot, error) {
	return p, NetworkSnapshot{Available: true, Handle: "fixed-test"}, nil
}
func (*fixedTestPlatform) NetworkBinding() bool { return true }
func (p *fixedTestPlatform) InterfaceSnapshots(context.Context) ([]InterfaceSnapshot, error) {
	p.candidates.Add(1)
	return nil, fmt.Errorf("ICE must not run")
}
func (p *fixedTestPlatform) Candidates(context.Context, Config, int) ([]Candidate, error) {
	p.candidates.Add(1)
	return nil, fmt.Errorf("legacy collection must not run")
}
func (p *fixedTestPlatform) ListenPacket(ctx context.Context, network, address string) (net.PacketConn, error) {
	p.listens.Add(1)
	return p.DefaultPlatform.ListenPacket(ctx, network, address)
}
func (p *fixedTestPlatform) ResolveUDP(ctx context.Context, address string) (*net.UDPAddr, error) {
	p.resolves.Add(1)
	host, _, _ := net.SplitHostPort(address)
	if resolved, ok := p.resolved.Load().(string); ok && resolved != "" && host == "gateway.example" {
		address = resolved
	}
	return p.DefaultPlatform.ResolveUDP(ctx, address)
}

func fixedEndpoint(t *testing.T, network string) *FixedEndpointConfig {
	t.Helper()
	host := "127.0.0.1"
	if network == "udp6" {
		host = "::1"
	}
	p, err := net.ListenPacket(network, net.JoinHostPort(host, "0"))
	if err != nil {
		t.Fatal(err)
	}
	address := p.LocalAddr().String()
	_ = p.Close()
	return &FixedEndpointConfig{Listen: address, Advertise: address}
}

// Model UDP port forwarding with a distinct public port and one NAT binding
// per external client; QUIC still runs against the actual shared listener.
func fixedPortMapping(t *testing.T, target string) string {
	t.Helper()
	proxy, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	clients := map[string]net.Conn{}
	closed := false
	t.Cleanup(func() {
		_ = proxy.Close()
		mu.Lock()
		defer mu.Unlock()
		closed = true
		for _, conn := range clients {
			_ = conn.Close()
		}
	})
	go func() {
		buffer := make([]byte, 65535)
		for {
			n, remote, err := proxy.ReadFrom(buffer)
			if err != nil {
				return
			}
			mu.Lock()
			if closed {
				mu.Unlock()
				return
			}
			conn := clients[remote.String()]
			if conn == nil {
				conn, err = net.Dial("udp", target)
				if err != nil {
					mu.Unlock()
					return
				}
				clients[remote.String()] = conn
				go func(upstream net.Conn, external net.Addr) {
					packet := make([]byte, 65535)
					for {
						n, err := upstream.Read(packet)
						if err != nil {
							return
						}
						_, _ = proxy.WriteTo(packet[:n], external)
					}
				}(conn, remote)
			}
			mu.Unlock()
			_, _ = conn.Write(buffer[:n])
		}
	}()
	return proxy.LocalAddr().String()
}

func fixedEchoServices(t *testing.T) (ServiceEndpoint, ServiceEndpoint, *atomic.Int32) {
	t.Helper()
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tcp.Close() })
	accepted := &atomic.Int32{}
	go func() {
		for {
			conn, err := tcp.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			go func() { defer conn.Close(); _, _ = io.Copy(conn, conn) }()
		}
	}()
	udp, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = udp.Close() })
	go func() {
		b := make([]byte, 65535)
		for {
			n, addr, err := udp.ReadFrom(b)
			if err != nil {
				return
			}
			_, _ = udp.WriteTo(b[:n], addr)
		}
	}()
	return ServiceEndpoint{"tcp", "127.0.0.1", tcp.Addr().(*net.TCPAddr).Port}, ServiceEndpoint{"udp", "127.0.0.1", udp.LocalAddr().(*net.UDPAddr).Port}, accepted
}

func fixedActive(channels int) func(Snapshot) bool {
	return func(s Snapshot) bool {
		return len(s.PeerTransports) == 1 && s.PeerTransports[0].State == "active" && s.PeerTransports[0].ActiveChannels == channels
	}
}
func fixedTCP(t *testing.T, port int) net.Conn {
	t.Helper()
	c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	fixedTransfer(t, c)
	return c
}
func fixedTransfer(t *testing.T, c net.Conn) {
	t.Helper()
	data := bytes.Repeat([]byte("fixed tunnel"), 10000)
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	go func() { _, _ = c.Write(data) }()
	got := make([]byte, len(data))
	if _, err := io.ReadFull(c, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("TCP bytes changed")
	}
}
func fixedUDP(t *testing.T, port int) {
	t.Helper()
	c, err := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	data := bytes.Repeat([]byte("udp"), 2000)
	got := make([]byte, 65535)
	for until := time.Now().Add(5 * time.Second); time.Now().Before(until); {
		_ = c.SetDeadline(time.Now().Add(200 * time.Millisecond))
		_, _ = c.Write(data)
		n, err := c.Read(got)
		if err == nil && bytes.Equal(data, got[:n]) {
			return
		}
	}
	t.Fatal("UDP forwarding did not converge")
}

func TestFixedActualWorkerBidirectionalMappingsRecoveryAndSharedListener(t *testing.T) {
	server, hibernate := actualWorkerFixture(t)
	tcp, udp, accepted := fixedEchoServices(t)
	platforms := []*fixedTestPlatform{{}, {}, {}}
	engines := []*Engine{}
	for _, p := range platforms {
		e := NewEngine(Options{Platform: p})
		engines = append(engines, e)
		t.Cleanup(func() { _ = e.Close() })
	}
	a, b, c := engines[0], engines[1], engines[2]
	ra, rb, rc := iceFixtureRequest(server, "alpha"), iceFixtureRequest(server, "beta"), iceFixtureRequest(server, "charlie")
	ra.Config.Transport.FixedEndpoint = fixedEndpoint(t, "udp4")
	ra.Config.Transport.AllowLegacy = true
	// Exercise DNS on the pinned dialer and real UDP port forwarding.
	local := ra.Config.Transport.FixedEndpoint.Listen
	public := fixedPortMapping(t, local)
	_, publicPort, _ := net.SplitHostPort(public)
	ra.Config.Transport.FixedEndpoint.Advertise = net.JoinHostPort("gateway.example", publicPort)
	platforms[1].resolved.Store(public)
	platforms[2].resolved.Store(public)
	ta, tb, tc := unusedTCPPort(t), unusedTCPPort(t), unusedTCPPort(t)
	ua, ub := unusedUDPPort(t), unusedUDPPort(t)
	ra.Config.Provide = []Provide{{"tcp-a", tcp}, {"udp-a", udp}}
	ra.Config.Consume = []Consume{{"tcp-b", HostPort{"127.0.0.1", ta}}, {"udp-b", HostPort{"127.0.0.1", ua}}}
	rb.Config.Provide = []Provide{{"tcp-b", tcp}, {"udp-b", udp}}
	rb.Config.Consume = []Consume{{"tcp-a", HostPort{"127.0.0.1", tb}}, {"udp-a", HostPort{"127.0.0.1", ub}}}
	rc.Config.Consume = []Consume{{"tcp-a", HostPort{"127.0.0.1", tc}}}
	// Even with Worker TURN enabled neither fixed-only runtime may request it.
	for _, r := range []*Request{&ra, &rb, &rc} {
		r.Config.TURN.Mode = "worker"
	}
	if err := a.Start(ra); err != nil {
		t.Fatal(err)
	}
	if err := b.Start(rb); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, a, fixedActive(4))
	waitSnapshot(t, b, fixedActive(4))
	first, second := fixedTCP(t, ta), fixedTCP(t, tb)
	fixedUDP(t, ua)
	fixedUDP(t, ub)
	if err := c.Start(rc); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, c, fixedActive(1))
	waitSnapshot(t, a, func(s Snapshot) bool {
		return len(s.PeerTransports) == 2 && s.PeerTransports[0].State == "active" && s.PeerTransports[1].State == "active"
	})
	third := fixedTCP(t, tc)
	if platforms[0].listens.Load() != 1 {
		t.Fatalf("fixed listener duplicated: %d", platforms[0].listens.Load())
	}
	// Losing one peer connection preserves the shared listener and other peer.
	a.mu.Lock()
	coord := a.run.ice
	a.mu.Unlock()
	peers := coord.peerList()
	var beta *icePeer
	for _, p := range peers {
		if p.peerID() == "beta" {
			beta = p
		}
	}
	beta.mu.Lock()
	live := beta.active
	gen := live.ready.TransportGeneration
	beta.mu.Unlock()
	_ = live.session.CloseWithError(0, "test disconnect")
	waitSnapshotWithin(t, b, 15*time.Second, func(s Snapshot) bool { return fixedActive(4)(s) && s.PeerTransports[0].Generation > gen })
	fixedTransfer(t, first)
	fixedTransfer(t, second)
	fixedTransfer(t, third)
	if platforms[0].listens.Load() != 1 {
		t.Fatal("connection loss replaced listener")
	}
	hibernate()
	_ = coord.send(signalMessage("transport_sync"))
	fixedTransfer(t, second)
	beforeDNS := platforms[1].resolves.Load()
	if err := b.NetworkChanged(); err != nil {
		t.Fatal(err)
	}
	waitSnapshotWithin(t, b, 15*time.Second, func(s Snapshot) bool { return fixedActive(4)(s) && platforms[1].resolves.Load() > beforeDNS })
	fixedTransfer(t, second)
	// Reconfigure the listener while keeping application sockets and mappings.
	newEndpoint := fixedEndpoint(t, "udp4")
	ra.Config.Transport.FixedEndpoint = newEndpoint
	platforms[1].resolved.Store("")
	platforms[2].resolved.Store("")
	if err := a.ApplyConfig(ra); err != nil {
		t.Fatal(err)
	}
	waitSnapshotWithin(t, b, 15*time.Second, func(s Snapshot) bool {
		return fixedActive(4)(s) && s.PeerTransports[0].FixedEndpoint == newEndpoint.Advertise
	})
	waitSnapshotWithin(t, c, 15*time.Second, func(s Snapshot) bool {
		return fixedActive(1)(s) && s.PeerTransports[0].FixedEndpoint == newEndpoint.Advertise
	})
	fixedTransfer(t, first)
	fixedTransfer(t, second)
	fixedTransfer(t, third)
	fixedUDP(t, ua)
	fixedUDP(t, ub)
	if accepted.Load() != 3 {
		t.Fatalf("upstream application sockets were replaced: %d", accepted.Load())
	}
	for _, e := range engines {
		for _, s := range e.Snapshot().PeerTransports {
			if s.Profile != ProfileFixed || s.PathType != "fixed" || s.RemoteAddress == "" || s.LocalCandidates != 0 || s.RemoteCandidates != 0 || s.TURNExpiresAt != 0 || s.RelayState != "" {
				t.Fatalf("unexpected fixed snapshot: %+v", s)
			}
		}
	}
	for _, p := range platforms {
		if p.candidates.Load() != 0 {
			t.Fatal("fixed path gathered ICE/legacy candidates")
		}
	}
	coord.turnMu.Lock()
	turnRequested := !coord.turnLast.IsZero()
	coord.turnMu.Unlock()
	if turnRequested {
		t.Fatal("fixed path requested TURN")
	}
	_ = a.Stop()
	packet, err := net.ListenPacket("udp4", newEndpoint.Listen)
	if err != nil {
		t.Fatalf("Stop leaked fixed UDP socket: %v", err)
	}
	_ = packet.Close()
	packet, err = net.ListenPacket("udp4", local)
	if err != nil {
		t.Fatalf("reconfiguration leaked old socket: %v", err)
	}
	_ = packet.Close()
}

func TestFixedBothEndpointsTryOtherDirectionIPv6(t *testing.T) {
	probe, err := net.ListenPacket("udp6", "[::1]:0")
	if err != nil {
		t.Skip("host has no IPv6 loopback")
	}
	_ = probe.Close()
	server, _ := actualWorkerFixture(t)
	tcp, _, _ := fixedEchoServices(t)
	a, b := NewEngine(Options{}), NewEngine(Options{})
	defer a.Close()
	defer b.Close()
	ra, rb := iceFixtureRequest(server, "alpha"), iceFixtureRequest(server, "beta")
	ra.Config.Transport.FixedEndpoint = fixedEndpoint(t, "udp6")
	rb.Config.Transport.FixedEndpoint = fixedEndpoint(t, "udp6")
	// The first listener (alpha) advertises an unreachable port. On the next
	// generation alpha must dial beta's reachable IPv6 endpoint.
	ra.Config.Transport.FixedEndpoint.Advertise = fixedEndpoint(t, "udp6").Advertise
	port := unusedTCPPort(t)
	ra.Config.Provide = []Provide{{"tcp", tcp}}
	rb.Config.Consume = []Consume{{"tcp", HostPort{"127.0.0.1", port}}}
	if err := a.Start(ra); err != nil {
		t.Fatal(err)
	}
	if err := b.Start(rb); err != nil {
		t.Fatal(err)
	}
	s := waitSnapshotWithin(t, b, 25*time.Second, fixedActive(1)).PeerTransports[0]
	if s.ListenerID != "beta" || s.DialerID != "alpha" || s.Generation < 2 || s.AddressFamily != "IPv6" {
		t.Fatalf("direction did not change: %+v", s)
	}
	fixedTCP(t, port)
}

func TestFixedUnreachableKeepsRetryingWithoutICEOrTURN(t *testing.T) {
	server, _ := actualWorkerFixture(t)
	platforms := []*fixedTestPlatform{{}, {}}
	a, b := NewEngine(Options{Platform: platforms[0]}), NewEngine(Options{Platform: platforms[1]})
	defer a.Close()
	defer b.Close()
	ra, rb := iceFixtureRequest(server, "alpha"), iceFixtureRequest(server, "beta")
	ra.Config.Transport.FixedEndpoint = fixedEndpoint(t, "udp4")
	// Keep the target port bound without answering QUIC. No ICMP or other
	// transport can make this endpoint appear reachable.
	blackhole, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer blackhole.Close()
	ra.Config.Transport.FixedEndpoint.Advertise = blackhole.LocalAddr().String()
	ra.Config.Provide = []Provide{{"one", ServiceEndpoint{"tcp", "127.0.0.1", 22}}}
	rb.Config.Consume = []Consume{{"one", HostPort{"127.0.0.1", unusedTCPPort(t)}}}
	ra.Config.TURN.Mode, rb.Config.TURN.Mode = "worker", "worker"
	if err := a.Start(ra); err != nil {
		t.Fatal(err)
	}
	if err := b.Start(rb); err != nil {
		t.Fatal(err)
	}
	s := waitSnapshotWithin(t, b, 35*time.Second, func(s Snapshot) bool { return len(s.PeerTransports) == 1 && s.PeerTransports[0].Generation >= 3 }).PeerTransports[0]
	if s.Profile != ProfileFixed || s.Phase != "fixed" || s.State == "active" || s.Error == nil || s.Error.Code != "fixed_path_failed" {
		t.Fatalf("fixed failure not visible: %+v", s)
	}
	for i, e := range []*Engine{a, b} {
		e.mu.Lock()
		coord := e.run.ice
		e.mu.Unlock()
		coord.turnMu.Lock()
		requested := !coord.turnLast.IsZero()
		coord.turnMu.Unlock()
		if requested || platforms[i].candidates.Load() != 0 {
			t.Fatal("unreachable fixed path fell back to ICE/TURN")
		}
	}
}

func TestFixedOccupiedListenerFailsAndReleasesEngine(t *testing.T) {
	packet, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer packet.Close()
	r := iceFixtureRequest("ws://127.0.0.1:1", "occupied")
	r.Config.Transport.FixedEndpoint = &FixedEndpointConfig{packet.LocalAddr().String(), packet.LocalAddr().String()}
	e := NewEngine(Options{})
	defer e.Close()
	if err := e.Start(r); err != nil {
		t.Fatal(err)
	}
	s := waitSnapshot(t, e, func(s Snapshot) bool { return s.EngineState == "error" })
	if s.Error == nil || s.Error.Code != "address_in_use" {
		t.Fatalf("missing bind failure: %+v", s)
	}
}

func TestFixedEnableRetiresICEAndPreservesApplicationSession(t *testing.T) {
	server, _ := actualWorkerFixture(t)
	tcp, _, accepted := fixedEchoServices(t)
	a, b := NewEngine(Options{}), NewEngine(Options{})
	defer a.Close()
	defer b.Close()
	ra, rb := iceFixtureRequest(server, "alpha"), iceFixtureRequest(server, "beta")
	port := unusedTCPPort(t)
	ra.Config.Provide = []Provide{{"one", tcp}}
	rb.Config.Consume = []Consume{{"one", HostPort{"127.0.0.1", port}}}
	if err := a.Start(ra); err != nil {
		t.Fatal(err)
	}
	if err := b.Start(rb); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, a, fixedActive(1))
	waitSnapshot(t, b, fixedActive(1))
	client := fixedTCP(t, port)
	a.mu.Lock()
	coord := a.run.ice
	a.mu.Unlock()
	peer := coord.peerList()[0]
	peer.mu.Lock()
	old := peer.active
	peer.mu.Unlock()
	ra.Config.Transport.FixedEndpoint = fixedEndpoint(t, "udp4")
	if err := a.ApplyConfig(ra); err != nil {
		t.Fatal(err)
	}
	if old.session.Context().Err() == nil {
		t.Fatal("enabling fixed endpoint kept old ICE path alive")
	}
	waitSnapshot(t, b, func(s Snapshot) bool { return fixedActive(1)(s) && s.PeerTransports[0].Profile == ProfileFixed })
	fixedTransfer(t, client)
	ra.Config.Transport.FixedEndpoint = nil
	if err := a.ApplyConfig(ra); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, b, func(s Snapshot) bool { return fixedActive(1)(s) && s.PeerTransports[0].Profile == ProfileICE })
	fixedTransfer(t, client)
	if accepted.Load() != 1 {
		t.Fatal("policy change replaced application session")
	}
}
