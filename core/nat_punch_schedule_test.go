package core

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"
)

// Exercise the real reservation, calibration, QUIC and mux paths on three
// APDM/APDF NATs. Each coordinator shares one allocator across both its peers.
func TestNATPunchScheduledMesh(t *testing.T) {
	for _, scenario := range []string{"symmetric", "mixed", "stale-model"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			n := &natNetwork{sides: make([]*simNAT, 3)}
			coords := make([]*iceCoordinator, 3)
			models := make([]NATSnapshot, 3)
			for i := range coords {
				coords[i], _ = lifecycleCoordinator(t)
				coords[i].desired.Config.DeviceName = fmt.Sprintf("node%d", i)
				n.sides[i] = &simNAT{network: n, index: i, ip: net.IPv4(198, 51, 100, byte(i+1)), next: 30001 + i*10000, step: 1, byPort: map[int]*simMapping{}}
				models[i] = NATSnapshot{Status: "complete", Type: "symmetric", MappingBehavior: "endpoint_dependent", Predictability: "predictable_incremental", PublicIP: n.sides[i].ip.String(), PortStep: 1, LastMappingPort: n.sides[i].next - 1, AllocationScope: "cross_socket"}
			}
			if scenario == "mixed" {
				n.sides[2].cone = true
				models[2].Type, models[2].MappingBehavior, models[2].Predictability = "port_restricted_cone", "endpoint_independent", "stable"
			}
			if scenario == "stale-model" {
				// Same actual step, new residue: the old implementation burned
				// three rounds immediately without refreshing this evidence.
				n.sides[0].step, models[0].PortStep = 2, 2
			}
			pairs := [][2]int{{0, 1}, {1, 2}, {0, 2}}
			attempts := make(map[string][2]*iceAttempt)
			peers := make(map[string][2]*icePeer)
			for _, pair := range pairs {
				id := fmt.Sprintf("pair%d%d", pair[0], pair[1])
				var aa [2]*iceAttempt
				var pp [2]*icePeer
				for side, node := range pair {
					other := coords[pair[1-side]]
					ready := iceSignalMessage{SignalMessage: SignalMessage{PeerDevice: other.deviceID(), PeerFingerprint: certFingerprint(other.identity.Certificates[0])}, TransportID: id, TransportGeneration: 1, InitiatorID: coords[pair[0]].deviceID(), Profile: ProfileICE, Phase: "direct", PunchSchedule: natPunchSchedule, PeerRuntimeID: other.runtimeID, LeaseUntil: time.Now().Add(time.Minute).UnixMilli(), Voice: true}
					attemptCtx, stop := context.WithCancel(ctx)
					aa[side] = &iceAttempt{ctx: attemptCtx, cancel: stop, ready: ready, events: make(chan iceSignalMessage, 64), failed: make(chan error, 1)}
					pp[side] = &icePeer{ctx: attemptCtx, coordinator: coords[node], ready: ready, pending: aa[side], online: true}
				}
				attempts[id], peers[id] = aa, pp
			}
			var routers sync.WaitGroup
			for _, c := range coords {
				routers.Go(func() {
					for {
						select {
						case <-ctx.Done():
							return
						case m := <-c.sink.messages:
							for side, p := range peers[m.TransportID] {
								if p.coordinator.deviceID() == m.ToPeerID {
									select {
									case attempts[m.TransportID][side].events <- m:
									case <-ctx.Done():
										return
									}
								}
							}
						}
					}
				})
			}
			defer func() { cancel(); routers.Wait() }()
			type result struct {
				id   string
				side int
				live *liveICETransport
				err  error
			}
			completed := make(chan result, 6)
			config := NATPunchConfig{}
			config.normalize()
			// Queueing is allowed to outlive a round's budget.
			config.Timeout = ConfigDuration(time.Second)
			config.RoundTimeout = ConfigDuration(800 * time.Millisecond)
			cfg := Config{ICE: ICEConfig{NATSTUNURLs: []string{"stun:192.0.2.1:3478", "stun:192.0.2.2:3478", "stun:192.0.2.3:3478"}}}
			var workers sync.WaitGroup
			for _, pair := range pairs {
				id := fmt.Sprintf("pair%d%d", pair[0], pair[1])
				for side, node := range pair {
					workers.Go(func() {
						s := &natPunchSession{local: models[node], remote: models[pair[1-side]], config: config, key: []byte(id), mode: punchMode(models[node], models[pair[1-side]])}
						live, err := peers[id][side].predictNATPunch(attempts[id][side], &punchSimPlatform{nat: n.sides[node]}, cfg, s)
						completed <- result{id, side, live, err}
					})
				}
			}
			workers.Wait()
			lives := make(map[string][2]*liveICETransport)
			for range 6 {
				r := <-completed
				if r.err != nil {
					t.Errorf("%s side %d: %v", r.id, r.side, r.err)
					continue
				}
				t.Cleanup(r.live.close)
				pair := lives[r.id]
				pair[r.side] = r.live
				lives[r.id] = pair
			}
			if t.Failed() {
				return
			}
			// Previously completed pairs must remain alive when later peers
			// allocate their mappings. Verify bidirectional mux datagrams.
			for id, pair := range lives {
				for side, live := range pair {
					packet, _ := EncodeVoiceDatagram(1, 960, []byte(id))
					if err := live.session.SendDatagram(packet); err != nil {
						t.Fatal(err)
					}
					select {
					case got := <-pair[1-side].mux.voiceDatagrams:
						_, _, payload, err := DecodeVoiceDatagram(got)
						if err != nil || string(payload) != id {
							t.Fatalf("%s datagram: %q %v", id, payload, err)
						}
					case <-ctx.Done():
						t.Fatal("existing peer interrupted", id)
					}
				}
			}
			if scenario == "stale-model" && coords[0].localNAT().PortStep != 2 {
				t.Fatal("refreshed evidence was not published")
			}
		})
	}
}

func TestNATPunchReservationCancellationAndBackgroundProbe(t *testing.T) {
	c, p := lifecycleCoordinator(t)
	release, err := c.natAllocation.acquire(c.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithCancel(c.ctx)
	a := &iceAttempt{ctx: ctx, ready: p.ready, events: make(chan iceSignalMessage, 1), failed: make(chan error, 1)}
	a.ready.InitiatorID = c.deviceID()
	done := make(chan error, 1)
	go func() {
		unlock, err := p.reserveNATPunch(a)
		if unlock != nil {
			unlock()
		}
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled reservation acquired allocator")
		}
	case <-time.After(time.Second):
		t.Fatal("canceled reservation stuck")
	}
	platform := natBudgetPlatform{remaining: make(chan time.Duration, 3)}
	c.platform = platform
	c.desired.Config.ICE.NATSTUNURLs = []string{"stun:192.0.2.1:3478"}
	ctx, cancel = context.WithCancel(c.ctx)
	c.ctx = ctx
	stopped := make(chan struct{})
	go func() { defer close(stopped); c.monitorNAT() }()
	defer func() { cancel(); <-stopped }()
	select {
	case <-platform.remaining:
		t.Fatal("background probe allocated during a reserved punch")
	case <-time.After(50 * time.Millisecond):
	}
	release()
	select {
	case <-platform.remaining:
	case <-time.After(time.Second):
		t.Fatal("background probe stayed blocked after release")
	}
}

func TestNATPunchReconnectRefreshesBothModelsBeforeStrategy(t *testing.T) {
	n, _, _ := simulatedNATs([2]int{1, 1}, [2]int{}, 0)
	ca, pa := lifecycleCoordinator(t)
	cb, pb := lifecycleCoordinator(t)
	ca.desired.Config.DeviceName, cb.desired.Config.DeviceName = "alpha", "beta"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	coords, peers := []*iceCoordinator{ca, cb}, []*icePeer{pa, pb}
	cfg := Config{ICE: ICEConfig{NATSTUNURLs: []string{"stun:192.0.2.1:3478", "stun:192.0.2.2:3478", "stun:192.0.2.3:3478"}}}
	// Repeat quickly without another successful connection in between: the
	// previous conclusive irregular result must never suppress reclassification.
	for generation := uint64(2); generation <= 3; generation++ {
		stale := time.Now().UTC().Format(time.RFC3339Nano)
		attempts := make([]*iceAttempt, 2)
		for side, c := range coords {
			c.natMu.Lock()
			c.natConnected = false
			c.natSnapshot = NATSnapshot{Status: "complete", Type: "symmetric", MappingBehavior: "endpoint_dependent", Predictability: "irregular", Epoch: c.networkEpoch.Load(), ProbedAt: stale}
			c.natMu.Unlock()
			ready := peers[side].ready
			ready.TransportGeneration, ready.PunchSchedule = generation, natPunchSchedule
			attempts[side] = &iceAttempt{ctx: ctx, ready: ready, events: make(chan iceSignalMessage, 8), failed: make(chan error, 1)}
		}
		type result struct {
			side    int
			session *natPunchSession
			err     error
		}
		out := make(chan result, 2)
		for side, p := range peers {
			go func() {
				s, err := p.prepareNATPunch(attempts[side], &punchSimPlatform{nat: n.sides[side]}, cfg)
				out <- result{side, s, err}
			}()
		}
		// Hold both offers until each contains a fresh sample. Local allocator
		// must already be free while waiting for the remote offer (mesh safety).
		for side, c := range coords {
			select {
			case m := <-c.sink.messages:
				if m.Punch == nil || m.Punch.Kind != "offer" || m.Punch.Snapshot.ProbedAt == stale || m.Punch.Snapshot.Predictability == "irregular" {
					t.Fatalf("side %d reused cached model: %+v", side, m.Punch)
				}
				gateCtx, stop := context.WithTimeout(ctx, time.Second)
				release, err := c.natAllocation.acquire(gateCtx)
				stop()
				if err != nil {
					t.Fatal("offer wait held device allocator", err)
				}
				release()
				attempts[1-side].events <- m
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
		for range 2 {
			got := <-out
			if got.err != nil || got.session == nil || got.session.mode != "fixed" {
				t.Fatalf("side %d: session=%+v err=%v", got.side, got.session, got.err)
			}
			if got.session.local.ProbedAt == stale || got.session.remote.ProbedAt == stale || coords[got.side].localNAT().ProbedAt != got.session.local.ProbedAt {
				t.Fatal("fresh bilateral evidence was not published")
			}
		}
	}
}

func TestNATPunchRoundWaitsForPeerBeforeNextModel(t *testing.T) {
	c, p := lifecycleCoordinator(t)
	ctx, cancel := context.WithTimeout(c.ctx, 2*time.Second)
	defer cancel()
	ready := p.ready
	ready.InitiatorID, ready.PunchSchedule = c.deviceID(), natPunchSchedule
	a := &iceAttempt{ctx: ctx, ready: ready, events: make(chan iceSignalMessage, 8), failed: make(chan error, 1)}
	model := NATSnapshot{Status: "complete", Type: "symmetric", MappingBehavior: "endpoint_dependent", PublicIP: "198.51.100.1", PortStep: 1, AllocationScope: "cross_socket"}
	config := NATPunchConfig{}
	config.normalize()
	done := make(chan error, 1)
	go func() {
		// No STUN servers: fail before calibration while the peer is still
		// working on this round. No real socket or network is required.
		_, err := p.predictNATPunch(a, DefaultPlatform{}, Config{ICE: ICEConfig{NATSTUNURLs: []string{}}}, &natPunchSession{local: model, remote: model, mode: "fixed", config: config})
		done <- err
	}()
	defer func() { cancel(); <-done }()
	next := func(kind string, round int) {
		t.Helper()
		select {
		case m := <-c.sink.messages:
			if m.Punch == nil || m.Punch.Kind != kind || m.Punch.Round != round {
				t.Fatalf("want %s round %d, got %+v", kind, round, m.Punch)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	next("reserve", 0)
	a.events <- iceSignalMessage{Punch: &natPunchSignal{Kind: "reserved"}}
	next("model", 1)
	a.events <- iceSignalMessage{Punch: &natPunchSignal{Kind: "model", Round: 1, Snapshot: &model}}
	next("failed", 1)
	select {
	case m := <-c.sink.messages:
		t.Fatalf("advanced before peer finished first round: %+v", m.Punch)
	case <-time.After(30 * time.Millisecond):
	}
	a.events <- iceSignalMessage{Punch: &natPunchSignal{Kind: "failed", Round: 1}}
	next("model", 2)
}
