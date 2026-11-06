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
