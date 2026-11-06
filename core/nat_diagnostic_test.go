package core

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"hole/core/natprobe"
)

type natBudgetPlatform struct {
	DefaultPlatform
	remaining chan time.Duration
}

func (p natBudgetPlatform) ListenPacket(ctx context.Context, _, _ string) (net.PacketConn, error) {
	deadline, _ := ctx.Deadline()
	p.remaining <- time.Until(deadline)
	return nil, errors.New("fixture: no socket needed")
}

func TestNATMonitorUsesToolBudget(t *testing.T) {
	c, _ := lifecycleCoordinator(t)
	p := natBudgetPlatform{remaining: make(chan time.Duration, 1)}
	c.platform = p
	ctx, cancel := context.WithCancel(c.ctx)
	c.ctx = ctx
	done := make(chan struct{})
	go func() { defer close(done); c.monitorNAT() }()
	defer func() { cancel(); <-done }()
	select {
	case remaining := <-p.remaining:
		if remaining < natprobe.DefaultTimeout-time.Second || remaining > natprobe.DefaultTimeout {
			t.Fatalf("background probe budget = %s, want %s", remaining, natprobe.DefaultTimeout)
		}
	case <-time.After(time.Second):
		t.Fatal("background probe did not start")
	}
}

func TestNATPreparationDoesNotOverwriteDiagnosticTrend(t *testing.T) {
	c, p := lifecycleCoordinator(t)
	c.desired.Config.ICE.NATSTUNURLs = natFixtureSTUNURLs(t)
	c.desired.Config.ICE.PortMapping = "off"
	want := NATSnapshot{Status: "complete", Type: "symmetric", Predictability: "predictable_decremental", Epoch: c.networkEpoch.Load(), ProbedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	c.natSnapshot = want
	ctx, cancel := context.WithCancel(c.ctx)
	a := &iceAttempt{ctx: ctx, cancel: cancel, ready: p.ready}
	p.pending = a
	done := make(chan struct{})
	go func() { defer close(done); p.attemptNAT(a) }()
	defer func() { cancel(); <-done }()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case m := <-c.sink.messages:
			if m.Type != "nat_report" {
				continue
			}
			if got := c.localNAT(); got != want {
				t.Fatalf("preparation overwrote diagnostic trend: %+v", got)
			}
			return
		case <-deadline.C:
			t.Fatal("preparation did not report its socket")
		}
	}
}
