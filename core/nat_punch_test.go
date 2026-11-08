package core

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"
)

type punchTimeoutPlatform struct {
	DefaultPlatform
	calls int
}

func (p *punchTimeoutPlatform) ListenPacket(ctx context.Context, _, _ string) (net.PacketConn, error) {
	p.calls++
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestNATPunchCalibrationTimeoutRetriesThreeRounds(t *testing.T) {
	_, p := lifecycleCoordinator(t)
	ctx, cancel := context.WithTimeout(p.ctx, 4*time.Second)
	defer cancel()
	a := &iceAttempt{ctx: ctx, ready: p.ready, events: make(chan iceSignalMessage, 4), failed: make(chan error, 1)}
	platform := &punchTimeoutPlatform{}
	config := NATPunchConfig{Timeout: ConfigDuration(time.Second), RoundTimeout: ConfigDuration(500 * time.Millisecond)}
	config.normalize()
	_, err := p.predictNATPunch(a, platform, Config{ICE: ICEConfig{NATSTUNURLs: []string{"stun:192.0.2.1:3478"}}}, &natPunchSession{mode: "next", config: config})
	if err == nil || platform.calls != 3 || ctx.Err() != nil {
		t.Fatalf("attempts=%d err=%v parent=%v", platform.calls, err, ctx.Err())
	}
}

func TestNATPunchGroupsAndEvidence(t *testing.T) {
	cone := NATSnapshot{Status: "complete", Type: "port_restricted_cone", MappingBehavior: "endpoint_independent", Predictability: "stable"}
	sym := NATSnapshot{Status: "complete", Type: "symmetric", MappingBehavior: "endpoint_dependent", Predictability: "predictable_incremental", PortStep: 2, AllocationScope: "cross_socket", PublicIP: "198.51.100.1", LastMappingPort: 40000}
	same := sym
	same.AllocationScope = "same_socket"
	irregular := sym
	irregular.Predictability = "irregular"
	irregular.AllocationScope = ""
	irregular.PortStep = 0
	stale := cone
	stale.ProbedAt = time.Now().Add(-10 * time.Minute).Format(time.RFC3339Nano)
	for _, tc := range []struct {
		a, b NATSnapshot
		want string
	}{
		{cone, cone, "cone"}, {cone, sym, "single"}, {sym, cone, "single"}, {sym, sym, "fixed"}, {same, sym, "next"}, {irregular, cone, "ice"}, {irregular, sym, "ice"}, {stale, cone, "ice"}, {NATSnapshot{}, cone, "ice"},
	} {
		if got := punchMode(tc.a, tc.b); got != tc.want {
			t.Fatalf("%+v %+v: %s want %s", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestNATPunchPlansAndNoPortWrap(t *testing.T) {
	model := NATSnapshot{Status: "complete", Type: "symmetric", MappingBehavior: "endpoint_dependent", PublicIP: "198.51.100.1", PortStep: 2, LastMappingPort: 40000, AllocationScope: "cross_socket", Predictability: "predictable_incremental"}
	mapped := netip.MustParseAddrPort("198.51.100.1:40002")
	for _, tc := range []struct {
		mode            string
		sockets, target int
	}{{"fixed", 32, 40034}, {"fixed", 64, 40066}, {"next", 1, 40004}, {"single", 10, 40003}} {
		plan, e := makeNATPunchPlan(mapped, model, tc.mode, tc.sockets, 20)
		if e != nil || plan.Target != tc.target {
			t.Fatal(plan, e)
		}
	}
	model.Predictability = "predictable_decremental"
	plan, e := makeNATPunchPlan(mapped, model, "single", 10, 20)
	if e != nil || plan.Target != 39982 || plan.Width != 20 {
		t.Fatal(plan, e)
	}
	for _, addr := range []string{"198.51.100.1:65534", "198.51.100.2:40002", "198.51.100.1:39998", "198.51.100.1:40003"} {
		if _, e := makeNATPunchPlan(netip.MustParseAddrPort(addr), model, "fixed", 32, 20); e == nil {
			t.Fatal("accepted invalid calibration", addr)
		}
	}
}

func TestNATPunchAuthenticatedPackets(t *testing.T) {
	p := natPunchPacket{kind: 1, round: 2, socket: 31, sequence: 4, nonce: [16]byte{1}, echo: [16]byte{2}}
	key := []byte(strings.Repeat("k", 32))
	b := p.encode(key)
	if got, ok := decodeNATPunch(b, key, 2); !ok || got != p {
		t.Fatal(got, ok)
	}
	if _, ok := decodeNATPunch(b, key, 1); ok {
		t.Fatal("old round accepted")
	}
	if _, ok := decodeNATPunch(b, []byte("other generation"), 2); ok {
		t.Fatal("wrong key accepted")
	}
	for i := range b {
		b[i] ^= 1
		if _, ok := decodeNATPunch(b, key, 2); ok {
			t.Fatal("tampering accepted", i)
		}
		b[i] ^= 1
	}
}

func TestNATPunchRoundSignalsKeepFutureReadiness(t *testing.T) {
	_, p := lifecycleCoordinator(t)
	a := &iceAttempt{events: make(chan iceSignalMessage, 4), failed: make(chan error, 1)}
	for _, v := range []natPunchSignal{{Kind: "ready", Round: 2}, {Kind: "failed", Round: 1}} {
		v := v
		a.events <- iceSignalMessage{Punch: &v}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := p.waitNATPunch(ctx, a, "ready", 1); err == nil {
		t.Fatal("failed round accepted")
	}
	if m, err := p.waitNATPunch(ctx, a, "ready", 2); err != nil || m.Round != 2 {
		t.Fatal(m, err)
	}
}

func TestNATPunchSignalsKeepSameRoundReservationAndModel(t *testing.T) {
	_, p := lifecycleCoordinator(t)
	a := &iceAttempt{events: make(chan iceSignalMessage, 8), failed: make(chan error, 1)}
	for _, v := range []natPunchSignal{{Kind: "reserve"}, {Kind: "offer"}, {Kind: "ready", Round: 1}, {Kind: "model", Round: 1}} {
		v := v
		a.events <- iceSignalMessage{Punch: &v}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for _, want := range []natPunchSignal{{Kind: "offer"}, {Kind: "reserve"}, {Kind: "model", Round: 1}, {Kind: "ready", Round: 1}} {
		m, err := p.waitNATPunch(ctx, a, want.Kind, want.Round)
		if err != nil || m.Kind != want.Kind || m.Round != want.Round {
			t.Fatalf("want %+v: %v %v", want, m, err)
		}
	}
}
