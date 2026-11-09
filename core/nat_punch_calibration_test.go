package core

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

type calibrationDNSPlatform struct {
	DefaultPlatform
	resolve func(context.Context, string) (*net.UDPAddr, error)
}

func (p calibrationDNSPlatform) ResolveUDP4(ctx context.Context, address string) (*net.UDPAddr, error) {
	return p.resolve(ctx, address)
}

func TestPunchSTUNRotationDeduplicatesAndPrefersAnotherIP(t *testing.T) {
	p := calibrationDNSPlatform{resolve: func(ctx context.Context, address string) (*net.UDPAddr, error) {
		address = strings.ReplaceAll(address, "alias.test", "192.0.2.1")
		return net.ResolveUDPAddr("udp4", address)
	}}
	urls := []string{"stun:192.0.2.1:3478", "stun:alias.test:3478", "stun:192.0.2.1:3479", "stun:192.0.2.2:3478", "stun:192.0.2.3:3478", "turn:192.0.2.4:3478"}
	previous := "192.0.2.1:3478"
	for range 12 {
		servers, err := punchSTUNServers(context.Background(), p, urls, previous)
		if err != nil || len(servers) != 4 {
			t.Fatalf("servers=%v err=%v", servers, err)
		}
		lastIP, _, _ := net.SplitHostPort(previous)
		if servers[0].IP.String() == lastIP || servers[len(servers)-1].String() != previous {
			t.Fatalf("reused previous server despite alternatives: %s -> %v", previous, servers)
		}
		previous = servers[0].String()
	}
}

func TestPunchSTUNResolutionCancellation(t *testing.T) {
	started := make(chan struct{}, 2)
	p := calibrationDNSPlatform{resolve: func(ctx context.Context, _ string) (*net.UDPAddr, error) {
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := punchSTUNServers(ctx, p, []string{"stun:one.test:3478", "stun:two.test:3478"}, "")
		done <- err
	}()
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("DNS preparation did not start concurrently")
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("DNS preparation ignored cancellation")
	}
}

type calibrationWrite struct {
	local, server string
}

type calibrationSocket struct {
	net.PacketConn
	writes chan<- calibrationWrite
	fail   string
}

func (s *calibrationSocket) WriteTo(b []byte, to net.Addr) (int, error) {
	s.writes <- calibrationWrite{s.LocalAddr().String(), to.String()}
	if s.fail == "*" || to.String() == s.fail {
		return 0, errors.New("fixture STUN unreachable")
	}
	return s.PacketConn.WriteTo(b, to)
}

func TestPunchCalibrationBarrierAndBilateralRetry(t *testing.T) {
	for _, mode := range []string{"next", "fixed"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			n, _, _ := simulatedNATs([2]int{1, 1}, [2]int{}, 0)
			var peers [2]*icePeer
			var attempts [2]*iceAttempt
			var held [2][]net.PacketConn
			var writes [2]chan calibrationWrite
			var sockets [2][]net.PacketConn
			var opens [2]func() (net.PacketConn, error)
			for side := range 2 {
				_, peers[side] = lifecycleCoordinator(t)
				attempts[side] = &iceAttempt{ctx: ctx, ready: peers[side].ready, events: make(chan iceSignalMessage, 32), failed: make(chan error, 1)}
				attempts[side].ready.PunchCalibration = natPunchCalibration
				writes[side] = make(chan calibrationWrite, 16)
				platform := &punchSimPlatform{nat: n.sides[side]}
				opens[side] = func() (net.PacketConn, error) {
					pc, err := platform.ListenPacket(ctx, "udp4", "0.0.0.0:0")
					if err != nil {
						return nil, err
					}
					wrapped := &calibrationSocket{PacketConn: pc, writes: writes[side]}
					if side == 0 {
						wrapped.fail = "192.0.2.1:3478"
					}
					held[side] = append(held[side], wrapped)
					return wrapped, nil
				}
				pc, err := opens[side]()
				if err != nil {
					t.Fatal(err)
				}
				sockets[side] = []net.PacketConn{pc}
			}
			var routers sync.WaitGroup
			prepared := make(chan struct{}, 1)
			for side, p := range peers {
				routers.Go(func() {
					for {
						select {
						case <-ctx.Done():
							return
						case m := <-p.coordinator.sink.messages:
							if side == 0 && m.Punch.Kind == "calibrate" && m.Punch.Trial == 1 {
								prepared <- struct{}{}
							}
							select {
							case attempts[1-side].events <- m:
							case <-ctx.Done():
								return
							}
						}
					}
				})
			}
			type result struct {
				plans      *punchCalibrationPlans
				err        error
				diagnostic NATPunchDiagnostic
			}
			completed := [2]chan result{make(chan result, 1), make(chan result, 1)}
			var workers sync.WaitGroup
			defer func() {
				cancel()
				workers.Wait()
				routers.Wait()
				for _, side := range held {
					for _, pc := range side {
						_ = pc.Close()
					}
				}
			}()
			run := func(side int) {
				workers.Go(func() {
					model := NATSnapshot{Status: "complete", Type: "symmetric", MappingBehavior: "endpoint_dependent", PublicIP: n.sides[side].ip.String(), PortStep: 1, LastMappingPort: 40000 + side*10000, AllocationScope: "cross_socket"}
					s := &natPunchSession{local: model, mode: mode, config: NATPunchConfig{ScanWidth: 20}}
					servers := []*net.UDPAddr{{IP: net.IPv4(192, 0, 2, 1), Port: 3478}, {IP: net.IPv4(192, 0, 2, 2), Port: 3478}}
					var d NATPunchDiagnostic
					previous := ""
					plans, err := peers[side].calibrateNATPunch(ctx, attempts[side], s, 1, servers, sockets[side], opens[side], &previous, &d)
					completed[side] <- result{plans, err, d}
				})
			}
			run(0)
			select {
			case <-prepared:
			case <-ctx.Done():
				t.Fatal("missing preparation barrier")
			}
			// The peer is still doing DNS/socket preparation: no Binding yet.
			select {
			case w := <-writes[0]:
				t.Fatalf("Binding before peer preparation: %+v", w)
			case <-time.After(30 * time.Millisecond):
			}
			run(1)
			var results [2]result
			for side := range 2 {
				results[side] = <-completed[side]
				r := results[side]
				if r.err != nil || r.diagnostic.CalibrationAttempts != 2 {
					t.Fatalf("side %d: %+v", side, r)
				}
				first, second := <-writes[side], <-writes[side]
				if first.server == second.server || first.local == second.local {
					t.Fatalf("retry reused server or calibration socket: %+v %+v", first, second)
				}
				if mode == "next" && sockets[side][0].LocalAddr().String() != second.local {
					t.Fatal("next mode did not retain the successful calibration socket")
				}
				if mode == "fixed" && sockets[side][0] != held[side][0] {
					t.Fatal("unused business socket was replaced")
				}
			}
			if results[1].plans.local.Port != 50002 || *results[0].plans.remote != *results[1].plans.local || *results[1].plans.remote != *results[0].plans.local {
				t.Fatal("successful peer kept the first trial's stale mapping")
			}
		})
	}
}

func TestPunchCalibrationOldPeerFailoverAndPoolIP(t *testing.T) {
	n, _, _ := simulatedNATs([2]int{1, 1}, [2]int{}, 0)
	n.sides[0].pool = true
	_, p := lifecycleCoordinator(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	a := &iceAttempt{ctx: ctx, ready: p.ready}
	writes := make(chan calibrationWrite, 10)
	platform := &punchSimPlatform{nat: n.sides[0]}
	open := func() (net.PacketConn, error) {
		pc, err := platform.ListenPacket(ctx, "udp4", "0.0.0.0:0")
		if err != nil {
			return nil, err
		}
		t.Cleanup(func() { _ = pc.Close() })
		return &calibrationSocket{PacketConn: pc, writes: writes, fail: "192.0.2.1:3478"}, nil
	}
	pc, err := open()
	if err != nil {
		t.Fatal(err)
	}
	s := &natPunchSession{local: poolTestSnapshot(), mode: "pool", config: NATPunchConfig{ScanWidth: 20}}
	previous := ""
	var d NATPunchDiagnostic
	servers := []*net.UDPAddr{{IP: net.IPv4(192, 0, 2, 1), Port: 3478}, {IP: net.IPv4(192, 0, 2, 4), Port: 3478}}
	plans, err := p.calibrateNATPunch(ctx, a, s, 1, servers, []net.PacketConn{pc}, open, &previous, &d)
	if err != nil || plans.local.IP != "203.0.113.1" || plans.local.Target != plans.local.Port+1 || d.CalibrationAttempts != 2 {
		t.Fatalf("plans=%+v diagnostic=%+v err=%v", plans, d, err)
	}
	select {
	case m := <-p.coordinator.sink.messages:
		t.Fatalf("new calibration message sent without negotiation: %+v", m)
	default:
	}
}

func TestPunchCalibrationWaitIgnoresOldTrial(t *testing.T) {
	_, p := lifecycleCoordinator(t)
	a := &iceAttempt{events: make(chan iceSignalMessage, 8), failed: make(chan error, 1)}
	for _, m := range []natPunchSignal{{Kind: "calibrated", Round: 1, Trial: 1, Result: "ok"}, {Kind: "calibrate", Round: 1, Trial: 3}, {Kind: "calibrated", Round: 1, Trial: 2, Result: "retry"}} {
		a.events <- iceSignalMessage{Punch: &m}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	m, err := p.waitNATPunchTrial(ctx, a, "calibrated", 1, 2)
	if err != nil || m.Result != "retry" {
		t.Fatal(m, err)
	}
	m, err = p.waitNATPunchTrial(ctx, a, "calibrate", 1, 3)
	if err != nil || m.Trial != 3 {
		t.Fatal(m, err)
	}
	if slices.ContainsFunc(a.punchBuffered, func(m natPunchSignal) bool { return m.Trial == 1 }) {
		t.Fatal("stale trial retained")
	}
}

func TestPunchCalibrationFailureBudget(t *testing.T) {
	for _, serverCount := range []int{1, 2, 4} {
		t.Run(string(rune('0'+serverCount)), func(t *testing.T) {
			n, _, _ := simulatedNATs([2]int{1, 1}, [2]int{}, 0)
			_, p := lifecycleCoordinator(t)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			a := &iceAttempt{ctx: ctx, ready: p.ready}
			writes := make(chan calibrationWrite, 10)
			platform := &punchSimPlatform{nat: n.sides[0]}
			open := func() (net.PacketConn, error) {
				pc, err := platform.ListenPacket(ctx, "udp4", "0.0.0.0:0")
				if err != nil {
					return nil, err
				}
				t.Cleanup(func() { _ = pc.Close() })
				return &calibrationSocket{PacketConn: pc, writes: writes, fail: "*"}, nil
			}
			pc, err := open()
			if err != nil {
				t.Fatal(err)
			}
			var servers []*net.UDPAddr
			for i := range serverCount {
				servers = append(servers, &net.UDPAddr{IP: net.IPv4(192, 0, 2, byte(i+1)), Port: 3478})
			}
			previous := ""
			var d NATPunchDiagnostic
			_, err = p.calibrateNATPunch(ctx, a, &natPunchSession{mode: "next"}, 1, servers, []net.PacketConn{pc}, open, &previous, &d)
			want := min(serverCount, 3)
			if err == nil || d.CalibrationAttempts != want || len(writes) != want {
				t.Fatalf("attempts=%d writes=%d want=%d err=%v", d.CalibrationAttempts, len(writes), want, err)
			}
			seen := map[string]bool{}
			for range want {
				w := <-writes
				if seen[w.server] {
					t.Fatal("failed endpoint reused", w.server)
				}
				seen[w.server] = true
			}
		})
	}
}

func TestPunchCalibrationBarrierCancellation(t *testing.T) {
	n, _, _ := simulatedNATs([2]int{1, 1}, [2]int{}, 0)
	_, p := lifecycleCoordinator(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &iceAttempt{ctx: ctx, ready: p.ready, events: make(chan iceSignalMessage), failed: make(chan error, 1)}
	a.ready.PunchCalibration = natPunchCalibration
	platform := &punchSimPlatform{nat: n.sides[0]}
	pc, err := platform.ListenPacket(ctx, "udp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	writes := make(chan calibrationWrite, 10)
	wrapped := &calibrationSocket{PacketConn: pc, writes: writes}
	done := make(chan error, 1)
	go func() {
		previous := ""
		_, err := p.calibrateNATPunch(ctx, a, &natPunchSession{mode: "next"}, 1, []*net.UDPAddr{{IP: net.IPv4(192, 0, 2, 1), Port: 3478}}, []net.PacketConn{wrapped}, nil, &previous, &NATPunchDiagnostic{})
		done <- err
	}()
	select {
	case <-p.coordinator.sink.messages:
	case <-time.After(time.Second):
		t.Fatal("barrier not reached")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("barrier ignored cancellation")
	}
	if len(writes) != 0 {
		t.Fatal("Binding started without peer")
	}
}
