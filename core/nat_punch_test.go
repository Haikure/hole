package core

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestNATPunchWaitReportsMissingConfirmation(t *testing.T) {
	_, p := lifecycleCoordinator(t)
	a := &iceAttempt{events: make(chan iceSignalMessage)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := p.waitNATPunchTrial(ctx, a, "calibrate", 2, 1)
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "calibrate") || !strings.Contains(err.Error(), "轮次 2") || !strings.Contains(err.Error(), "校准序号 1") {
		t.Fatalf("confirmation failure lost its phase or cause: %v", err)
	}
}

func TestNATPunchRejectedCalibrationKeepsEvidenceWithoutAbortingNextRound(t *testing.T) {
	c, p := lifecycleCoordinator(t)
	a := &iceAttempt{ready: p.ready, failed: make(chan error, 1), process: p.process.begin(p.ready)}
	p.pending = a
	m := signalMessage("error")
	m.TransportID, m.TransportGeneration = p.ready.TransportID, p.ready.TransportGeneration
	m.Code, m.Message = "punch_calibration_rejected", "第 1 轮校准 1 结果未转发：未收到双方同轮同序号的准备确认"
	m.Punch = &natPunchSignal{Kind: "calibrated", Round: 1, Trial: 1}
	if err := c.handle(m, c.request(), DefaultPlatform{}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-a.failed:
		t.Fatal("late refusal aborted another round", err)
	default:
	}
	trace := p.process.snapshot(0)
	last := trace.Events[len(trace.Events)-1]
	if last.Kind != "signal_error" || last.Round != 1 || last.Trial != 1 || last.Detail != m.Message {
		t.Fatalf("missing calibration refusal evidence: %+v", last)
	}
	m.TransportGeneration++
	_ = c.handle(m, c.request(), DefaultPlatform{})
	if len(p.process.snapshot(0).Events) != len(trace.Events) {
		t.Fatal("stale generation contaminated the current trace")
	}
}

type punchTimeoutPlatform struct {
	DefaultPlatform
	calls int
}

func TestNATPunchEarlyFailedResultReachesRoundBarrier(t *testing.T) {
	for _, buffered := range []bool{false, true} {
		_, p := lifecycleCoordinator(t)
		a := &iceAttempt{events: make(chan iceSignalMessage, 1)}
		failure := natPunchSignal{Kind: "failed", Round: 2, Result: "unreachable"}
		if buffered {
			a.punchBuffered = []natPunchSignal{failure}
		} else {
			a.events <- iceSignalMessage{Punch: &failure}
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if _, err := p.waitNATPunch(ctx, a, "ready", 2); err == nil {
			t.Fatal("peer failure did not end coordination")
		}
		got, err := p.waitNATPunch(ctx, a, "failed", 2)
		cancel()
		if err != nil || got == nil || got.Result != "unreachable" || len(a.punchBuffered) != 0 {
			t.Fatalf("buffered=%v: barrier lost failure: %+v %v", buffered, got, err)
		}
	}
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

func TestNATPunchPacketSequenceRange(t *testing.T) {
	key := []byte("continuous-probing")
	for _, kind := range []byte{1, 2} {
		for _, sequence := range []uint32{0, 1, 8, 9, 64, 65, 151, ^uint32(0)} {
			p := natPunchPacket{kind: kind, round: 1, socket: 63, sequence: sequence}
			got, ok := decodeNATPunch(p.encode(key), key, 1)
			if ok != (sequence != 0) || got != p {
				t.Fatalf("kind=%d sequence=%d: packet=%+v valid=%v", kind, sequence, got, ok)
			}
		}
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
