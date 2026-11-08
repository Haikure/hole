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
