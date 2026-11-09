package core

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

type punchSimPlatform struct {
	DefaultPlatform
	nat *simNAT
}

func TestNATPunchThreeFailedRoundsBeforeFallback(t *testing.T) {
	n, _, _ := simulatedNATs([2]int{1, 1}, [2]int{}, 0)
	n.blockOneWay = true
	ca, pa := lifecycleCoordinator(t)
	cb, pb := lifecycleCoordinator(t)
	ca.desired.Config.DeviceName = "alpha"
	cb.desired.Config.DeviceName = "beta"
	pa.ready.InitiatorID = "alpha"
	pb.ready.InitiatorID = "alpha"
	pa.ready.PeerFingerprint = certFingerprint(cb.identity.Certificates[0])
	pb.ready.PeerFingerprint = certFingerprint(ca.identity.Certificates[0])
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	aa := &iceAttempt{ctx: ctx, ready: pa.ready, events: make(chan iceSignalMessage, 32), failed: make(chan error, 1)}
	ab := &iceAttempt{ctx: ctx, ready: pb.ready, events: make(chan iceSignalMessage, 32), failed: make(chan error, 1)}
	var wg sync.WaitGroup
	var rounds [2][]int
	for side, c := range []*iceCoordinator{ca, cb} {
		target := ab
		if side == 1 {
			target = aa
		}
		wg.Go(func() {
			for {
				select {
				case <-ctx.Done():
					return
				case m := <-c.sink.messages:
					if m.Punch.Kind == "ready" {
						rounds[side] = append(rounds[side], m.Punch.Round)
					}
					select {
					case target.events <- m:
					case <-ctx.Done():
						return
					}
				}
			}
		})
	}
	model := func(ip string, port int) NATSnapshot {
		return NATSnapshot{Status: "complete", Type: "symmetric", MappingBehavior: "endpoint_dependent", PublicIP: ip, PortStep: 1, LastMappingPort: port, AllocationScope: "cross_socket"}
	}
	models := [2]NATSnapshot{model("198.51.100.1", 40000), model("198.51.100.2", 50000)}
	config := NATPunchConfig{Sockets: 10, ScanWidth: 20, DualSockets: 2, DualRetrySockets: 4, Timeout: ConfigDuration(3 * time.Second), RoundTimeout: ConfigDuration(500 * time.Millisecond)}
	completed := make(chan error, 2)
	for side, p := range []*icePeer{pa, pb} {
		a := aa
		if side == 1 {
			a = ab
		}
		go func() {
			_, err := p.predictNATPunch(a, &punchSimPlatform{nat: n.sides[side]}, Config{ICE: ICEConfig{NATSTUNURLs: []string{"stun:192.0.2.1:3478"}}}, &natPunchSession{local: models[side], remote: models[1-side], mode: "fixed", key: []byte("failed-rounds"), config: config})
			completed <- err
		}()
	}
	for range 2 {
		if err := <-completed; err == nil {
			t.Fatal("one-way network established")
		}
	}
	cancel()
	wg.Wait()
	for _, got := range rounds {
		if len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
			t.Fatalf("rounds %v, want [1 2 3]", got)
		}
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, side := range n.sides {
		for _, mapping := range side.byPort {
			select {
			case <-mapping.socket.closed:
			default:
				t.Fatal("failed round leaked socket")
			}
		}
	}
}

func (p *punchSimPlatform) ListenPacket(ctx context.Context, _, _ string) (net.PacketConn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.nat.network.mu.Lock()
	defer p.nat.network.mu.Unlock()
	p.nat.nextLocal++
	return &simSocket{nat: p.nat, port: 20000 + p.nat.nextLocal, mappings: map[string]*simMapping{}, incoming: make(chan datagram, 512), closed: make(chan struct{}), changed: make(chan struct{})}, nil
}

func (p *punchSimPlatform) ResolveUDP(ctx context.Context, address string) (*net.UDPAddr, error) {
	return net.ResolveUDPAddr("udp4", address)
}

// Model Android's selected-Network resolver: DNS can return AAAA first even
// though this simulator exposes only IPv4 interfaces and IPv4 NAT mappings.
type punchIPv6FirstPlatform struct{ *punchSimPlatform }

func (p *punchIPv6FirstPlatform) InterfaceSnapshots(ctx context.Context) ([]InterfaceSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []InterfaceSnapshot{{Name: "wlan0", Up: true, Addresses: []string{"10.0.0.1"}}}, nil
}

func (p *punchIPv6FirstPlatform) LookupIP(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(strings.TrimSuffix(host, ".stun.test")); ip != nil {
		return []net.IP{net.ParseIP("2001:db8::53"), ip}, nil
	}
	return p.DefaultPlatform.LookupIP(ctx, host)
}

func (p *punchIPv6FirstPlatform) ResolveUDP(ctx context.Context, address string) (*net.UDPAddr, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := p.LookupIP(ctx, host)
	if err != nil {
		return nil, err
	}
	return net.ResolveUDPAddr("udp", net.JoinHostPort(ips[0].String(), port))
}

func (p *punchIPv6FirstPlatform) ResolveUDP4(ctx context.Context, address string) (*net.UDPAddr, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := p.LookupIP(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, ip := range ips {
		if ip.To4() != nil {
			return net.ResolveUDPAddr("udp4", net.JoinHostPort(ip.String(), port))
		}
	}
	return nil, &net.DNSError{Name: host, Err: "no IPv4 address", IsNotFound: true}
}

func TestNATPunchActualWorkerAndSharedMux(t *testing.T) {
	for _, tc := range []struct {
		name       string
		cone, pool bool
		step       int
		dnsSide    string
	}{
		{"cone-symmetric", true, false, 1, ""}, {"dual-symmetric", false, false, 1, ""},
		{"cone-pool-increasing", true, true, 1, ""}, {"cone-pool-decreasing", true, true, -1, ""},
		{"cone-symmetric-ipv6-first-symmetric", true, false, 1, "alpha"},
		{"cone-symmetric-ipv6-first-cone", true, false, 1, "beta"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, _ := actualWorkerFixture(t)
			n, _, _ := simulatedNATs([2]int{tc.step, 1}, [2]int{}, 0)
			n.sides[1].cone = tc.cone // The cone can be the non-initiator in mux signaling.
			n.sides[0].pool = tc.pool
			platforms := [2]Platform{&punchSimPlatform{nat: n.sides[0]}, &punchSimPlatform{nat: n.sides[1]}}
			for side, name := range []string{"alpha", "beta"} {
				if tc.dnsSide == name {
					platforms[side] = &punchIPv6FirstPlatform{platforms[side].(*punchSimPlatform)}
				}
			}
			a, b := NewEngine(Options{Platform: platforms[0]}), NewEngine(Options{Platform: platforms[1]})
			defer a.Close()
			defer b.Close()
			ra, rb := iceFixtureRequest(server, "alpha"), iceFixtureRequest(server, "beta")
			for _, r := range []*Request{&ra, &rb} {
				r.Config.ICE.NATSTUNURLs = []string{"stun:192.0.2.1:3478", "stun:192.0.2.2:3478", "stun:192.0.2.3:3478"}
				if r.Config.DeviceName == tc.dnsSide {
					r.Config.ICE.NATSTUNURLs = []string{"stun:192.0.2.1.stun.test:3478", "stun:192.0.2.2.stun.test:3478", "stun:192.0.2.3.stun.test:3478"}
				}
				if tc.pool {
					r.Config.ICE.NATSTUNURLs = append(r.Config.ICE.NATSTUNURLs, "stun:192.0.2.4:3478")
				}
				r.Config.ICE.InterfaceAllowlist = nil
				r.Config.ICE.PortMapping = "off"
				r.Config.ICE.DirectProbeTimeout = ConfigDuration(time.Second)
			}
			for _, id := range []string{"one", "two"} {
				ra.Config.Provide = append(ra.Config.Provide, Provide{ID: id, Service: ServiceEndpoint{"tcp", "127.0.0.1", unusedTCPPort(t)}})
				rb.Config.Consume = append(rb.Config.Consume, Consume{ID: id, Expose: HostPort{"127.0.0.1", unusedTCPPort(t)}})
			}
			if err := a.Start(ra); err != nil {
				t.Fatal(err)
			}
			if err := b.Start(rb); err != nil {
				t.Fatal(err)
			}
			active := func(s Snapshot) bool {
				return len(s.PeerTransports) == 1 && s.PeerTransports[0].State == "active" && s.PeerTransports[0].MappingCount == 2 && s.PeerTransports[0].PathType == "direct"
			}
			left := waitSnapshotWithin(t, a, 12*time.Second, active)
			right := waitSnapshotWithin(t, b, 12*time.Second, active)
			if left.PeerTransports[0].TransportID != right.PeerTransports[0].TransportID {
				t.Fatal("device pair did not share a transport")
			}
			if tc.pool && (len(left.PeerTransports[0].LocalNAT.IPEvidence) != 2 || len(right.PeerTransports[0].PeerNAT.IPEvidence) != 2) {
				t.Fatal("pool evidence lost between probe, Worker and peer")
			}
			for _, engine := range []*Engine{a, b} {
				s := engine.Snapshot().PeerTransports[0]
				if s.LocalType != "srflx" || s.LocalCandidates != 0 || s.Generation != 2 {
					t.Fatalf("prediction unexpectedly fell back: %+v", s)
				}
				engine.mu.Lock()
				coordinator := engine.run.ice
				engine.mu.Unlock()
				for _, peer := range coordinator.peerList() {
					peer.mu.Lock()
					schedule := peer.ready.PunchSchedule
					calibration := peer.ready.PunchCalibration
					peer.mu.Unlock()
					if schedule != natPunchSchedule {
						t.Fatalf("Worker did not negotiate scheduling: %q", schedule)
					}
					if calibration != natPunchCalibration {
						t.Fatalf("Worker did not negotiate calibration barrier: %q", calibration)
					}
				}
			}
		})
	}
}
