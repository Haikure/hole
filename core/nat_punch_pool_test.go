package core

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"hole/core/natprobe"
)

func poolTestSnapshot() NATSnapshot {
	return NATSnapshot{Status: "complete", Type: "symmetric", MappingBehavior: "endpoint_dependent", Predictability: "incremental", IPEvidence: []natprobe.IPEvidence{
		{IP: "198.51.100.1", Targets: 3, Ports: 3, Trend: "incremental", Step: 2},
		{IP: "198.51.100.2", Targets: 3, Ports: 3, Trend: "decremental", Step: 1},
		{IP: "198.51.100.3", Targets: 1, Ports: 1, Trend: "unknown"},
		{IP: "198.51.100.4", Targets: 3, Ports: 3, Trend: "irregular"},
		{IP: "198.51.100.5", Targets: 2, Ports: 1, Trend: "stable"},
	}}
}

func TestPoolPlansUseCalibratedIP(t *testing.T) {
	for _, tc := range []struct {
		ip                  string
		port, target, width int
	}{
		{"198.51.100.1", 40000, 40002, 20},
		{"198.51.100.2", 40000, 39980, 20},
		{"198.51.100.3", 40000, 40001, 20},
		{"198.51.100.9", 40000, 40001, 20},
		{"198.51.100.4", 40000, 0, 0},
		{"198.51.100.5", 40000, 40000, 1},
		{"198.51.100.2", 2, 1, 1},
		{"198.51.100.3", 65534, 65535, 1},
		{"198.51.100.2", 1, 0, 0},
		{"198.51.100.1", 65535, 0, 0},
	} {
		plan, err := makeNATPunchPlan(netip.AddrPortFrom(netip.MustParseAddr(tc.ip), uint16(tc.port)), poolTestSnapshot(), "pool", 10, 20)
		if tc.target == 0 {
			if err == nil {
				t.Fatalf("accepted %s:%d: %+v", tc.ip, tc.port, plan)
			}
			continue
		}
		if err != nil || plan.Target != tc.target || plan.Width != tc.width || plan.IP != tc.ip {
			t.Fatalf("%s:%d: %+v %v", tc.ip, tc.port, plan, err)
		}
	}
}

func TestPoolModeRequiresUsableEvidence(t *testing.T) {
	pool := poolTestSnapshot()
	cone := NATSnapshot{Status: "complete", MappingBehavior: "endpoint_independent"}
	sparse := poolTestSnapshot()
	sparse.Type, sparse.MappingBehavior, sparse.Predictability = "address_pool", "unknown", "unverified"
	sparse.IPEvidence = sparse.IPEvidence[2:4]
	irregular := poolTestSnapshot()
	for i := range irregular.IPEvidence {
		irregular.IPEvidence[i].Trend = "irregular"
	}
	stale := poolTestSnapshot()
	stale.ProbedAt = time.Now().Add(-10 * time.Minute).Format(time.RFC3339Nano)
	for _, tc := range []struct {
		a, b NATSnapshot
		want string
	}{
		{pool, cone, "pool"}, {cone, pool, "pool"}, {pool, pool, "pool"}, {sparse, pool, "pool"},
		{irregular, cone, "ice"}, {stale, cone, "ice"}, {pool, NATSnapshot{}, "ice"},
	} {
		if got := punchMode(tc.a, tc.b); got != tc.want {
			t.Fatalf("mode %s, want %s", got, tc.want)
		}
	}
}

func TestPoolPresentationAndCopy(t *testing.T) {
	s := poolTestSnapshot()
	d := NATDiagnostic{State: "complete", Result: s}.view()
	if d.Tone != "yellow" || NATDescription(s) != "对称型（地址池） · 可能递增" {
		t.Fatal(d)
	}
	d.Result.IPEvidence[0].Trend = "modified"
	if s.IPEvidence[0].Trend != "incremental" {
		t.Fatal("diagnostic aliases evidence")
	}
	s.Type, s.Predictability = "address_pool", "unverified"
	d = NATDiagnostic{State: "complete", Result: s}.view()
	if d.Title != "地址池" || d.Badge != "待验证" || d.Tone != "neutral" || NATDescription(s) != "地址池 · 待验证" {
		t.Fatal(d)
	}
}

func TestPoolPunchRejectsExcessTasks(t *testing.T) {
	a := &natPunchAttempt{sockets: make([]net.PacketConn, 2)}
	_, err := a.run(context.Background(), true, 1, nil, make([]*net.UDPAddr, 129), nil)
	if err == nil || !strings.Contains(err.Error(), "task count") {
		t.Fatalf("oversized socket/target product accepted: %v", err)
	}
}

func TestPoolPunchEstablishesQUICWithCalibration(t *testing.T) {
	for _, tc := range []struct {
		name         string
		step         int
		sparse, cone bool
		width        int
	}{
		{"cone increasing", 1, false, true, 20},
		{"cone decreasing", -1, false, true, 20},
		{"sparse defaults upward", 1, true, true, 20},
		{"dual opposite directions", -1, false, false, 1},
		{"dual scan opposite directions", -1, false, false, 20},
		{"dual maximum window", -1, false, false, 256},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, _, _ := simulatedNATs([2]int{tc.step, 1}, [2]int{}, 0)
			n.sides[1].cone = tc.cone
			ca, pa := lifecycleCoordinator(t)
			cb, pb := lifecycleCoordinator(t)
			ca.desired.Config.DeviceName, cb.desired.Config.DeviceName = "alpha", "beta"
			pa.ready.InitiatorID, pb.ready.InitiatorID = "alpha", "alpha"
			pa.ready.PeerFingerprint = certFingerprint(cb.identity.Certificates[0])
			pb.ready.PeerFingerprint = certFingerprint(ca.identity.Certificates[0])
			pa.ready.PeerRuntimeID, pb.ready.PeerRuntimeID = cb.runtimeID, ca.runtimeID
			pa.ready.Profile, pb.ready.Profile = ProfileICE, ProfileICE
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			defer cancel()
			attempts := []*iceAttempt{
				{ctx: ctx, cancel: cancel, ready: pa.ready, events: make(chan iceSignalMessage, 32), failed: make(chan error, 1)},
				{ctx: ctx, cancel: cancel, ready: pb.ready, events: make(chan iceSignalMessage, 32), failed: make(chan error, 1)},
			}
			var forwarding sync.WaitGroup
			for side, c := range []*iceCoordinator{ca, cb} {
				forwarding.Go(func() {
					for {
						select {
						case <-ctx.Done():
							return
						case m := <-c.sink.messages:
							select {
							case attempts[1-side].events <- m:
							case <-ctx.Done():
								return
							}
						}
					}
				})
			}
			defer func() { cancel(); forwarding.Wait() }()
			models := [2]NATSnapshot{}
			for side := range models {
				trend := "incremental"
				if side == 0 && tc.step < 0 {
					trend = "decremental"
				}
				targets, ports := 3, 3
				if tc.sparse {
					trend, targets, ports = "unknown", 1, 1
				}
				models[side] = NATSnapshot{Status: "complete", Type: "address_pool", Predictability: "unverified", IPEvidence: []natprobe.IPEvidence{
					{IP: n.sides[side].ip.String(), Targets: targets, Ports: ports, Trend: trend},
					{IP: fmt.Sprintf("203.0.113.%d", side+1), Targets: 1, Ports: 1, Trend: "unknown"},
				}}
			}
			if tc.cone {
				models[1] = NATSnapshot{Status: "complete", Type: "port_restricted_cone", MappingBehavior: "endpoint_independent", Predictability: "stable"}
			}
			config := NATPunchConfig{Sockets: 10, ScanWidth: tc.width, Timeout: ConfigDuration(3 * time.Second), RoundTimeout: ConfigDuration(2 * time.Second)}
			type outcome struct {
				live *liveICETransport
				err  error
			}
			out := make(chan outcome, 2)
			for side, peer := range []*icePeer{pa, pb} {
				go func() {
					live, err := peer.predictNATPunch(attempts[side], &punchSimPlatform{nat: n.sides[side]}, Config{ICE: ICEConfig{NATSTUNURLs: []string{"stun:192.0.2.1:3478"}}}, &natPunchSession{local: models[side], remote: models[1-side], mode: "pool", poolPrediction: true, config: config, key: []byte("pool-calibration")})
					out <- outcome{live, err}
				}()
			}
			for range 2 {
				got := <-out
				if got.live != nil {
					defer got.live.close()
				}
				if got.err != nil {
					t.Error(got.err)
				}
			}
		})
	}
}
