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
	p := natBudgetPlatform{remaining: make(chan time.Duration, 6)}
	c.desired.Config.ICE.NATSTUNURLs = []string{"stun:192.0.2.1:3478"}
	c.platform = p
	ctx, cancel := context.WithCancel(c.ctx)
	c.ctx = ctx
	done := make(chan struct{})
	go func() { defer close(done); c.monitorNAT() }()
	defer func() { cancel(); <-done }()
	for range 3 {
		select {
		case remaining := <-p.remaining:
			if remaining < natprobe.DefaultTimeout-time.Second || remaining > natprobe.DefaultTimeout {
				t.Fatalf("background probe budget = %s, want %s", remaining, natprobe.DefaultTimeout)
			}
		case <-time.After(time.Second):
			t.Fatal("background probe did not start")
		}
	}
	select {
	case <-p.remaining:
		t.Fatal("more than three discovery attempts")
	default:
	}
	deadline := time.Now().Add(time.Second)
	for {
		c.natMu.Lock()
		running := c.natProbeRunning
		c.natMu.Unlock()
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("initial discovery did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	c.natMu.Lock()
	c.natConnected = true
	c.natSnapshot = NATSnapshot{Status: "complete", Epoch: c.networkEpoch.Load(), ProbedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	c.natMu.Unlock()
	c.requestNATProbeForConnection()
	for range 3 {
		select {
		case <-p.remaining:
		case <-time.After(time.Second):
			t.Fatal("new connection did not trigger NAT discovery")
		}
	}
}

func TestNATProbeScheduleStopsRefreshAfterConnection(t *testing.T) {
	now := time.Now()
	next := now.Add(-time.Second)
	if !natProbeDue(4, 0, false, true, next, now) {
		t.Fatal("initial discovery was skipped after an early connection")
	}
	if natProbeDue(4, 4, true, true, next, now) {
		t.Fatal("connected network kept scheduling periodic discovery")
	}
	if !natProbeDue(5, 4, true, true, next, now) {
		t.Fatal("network epoch change did not schedule discovery")
	}
	if natProbeDue(4, 4, true, false, now.Add(time.Second), now) {
		t.Fatal("discovery ran before its refresh deadline")
	}
	if !natProbeDue(4, 4, true, false, next, now) {
		t.Fatal("discovery did not refresh before any connection was established")
	}
}
