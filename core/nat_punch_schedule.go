package core

import (
	"context"
	"errors"
	"sync"
	"time"

	"hole/core/natprobe"
)

const natPunchSchedule = "ordered-v1"

// A coordinator owns one allocator even when several device pairs use it.
// Existing connections retain their sockets and never take this gate.
type natAllocationGate struct {
	once  sync.Once
	token chan struct{}
}

func (g *natAllocationGate) acquire(ctx context.Context) (func(), error) {
	g.once.Do(func() { g.token = make(chan struct{}, 1) })
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case g.token <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-g.token
			return nil, err
		}
		var once sync.Once
		return func() { once.Do(func() { <-g.token }) }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// The lower device name acquires its own allocator before requesting the
// higher one's. All edges follow the same order, including triangles, so a
// device never holds the higher allocator while waiting for the lower one.
func (p *icePeer) reserveNATPunch(a *iceAttempt) (func(), error) {
	ctx, cancel := context.WithDeadline(a.ctx, time.UnixMilli(a.ready.LeaseUntil))
	defer cancel()
	initiator := a.ready.InitiatorID == p.coordinator.deviceID()
	if !initiator {
		if _, err := p.waitNATPunch(ctx, a, "reserve", 0); err != nil {
			return nil, err
		}
	}
	release, err := p.coordinator.natAllocation.acquire(ctx)
	if err != nil {
		return nil, err
	}
	kind := "reserved"
	if initiator {
		kind = "reserve"
	}
	err = p.sendNATPunch(a, natPunchSignal{Kind: kind})
	if err == nil && initiator {
		_, err = p.waitNATPunch(ctx, a, "reserved", 0)
	}
	if err != nil {
		release()
		return nil, err
	}
	return release, nil
}

// Refresh under the allocator reservation, then exchange the new evidence
// before choosing this round's algorithm. Both ends derive the same mode.
func (p *icePeer) refreshNATPunchModel(a *iceAttempt, platform Platform, cfg Config, s *natPunchSession, round int, refresh bool) error {
	ctx, cancel := context.WithTimeout(a.ctx, natprobe.DefaultTimeout+5*time.Second)
	defer cancel()
	if refresh || s.local.Current().Status != "complete" {
		epoch := p.coordinator.networkEpoch.Load()
		probeCtx, stop := context.WithTimeout(ctx, natprobe.DefaultTimeout)
		s.local = probeNATWithRetries(probeCtx, platform, natURLs(cfg.ICE))
		stop()
		s.local.Epoch = epoch
		if source, ok := platform.(interface{ NATNetworkIdentity() string }); ok {
			s.local.Network = source.NATNetworkIdentity()
		}
		p.coordinator.natMu.Lock()
		if epoch == p.coordinator.networkEpoch.Load() && a.ctx.Err() == nil {
			p.coordinator.natSnapshot = s.local
		}
		p.coordinator.natMu.Unlock()
	}
	if err := p.sendNATPunch(a, natPunchSignal{Kind: "model", Round: round, Snapshot: &s.local}); err != nil {
		return err
	}
	peer, err := p.waitNATPunch(ctx, a, "model", round)
	if err != nil {
		return err
	}
	if peer.Snapshot == nil {
		return errors.New("missing refreshed NAT model")
	}
	s.remote = *peer.Snapshot
	p.mu.Lock()
	p.stats.PeerNAT = s.remote.Current()
	p.mu.Unlock()
	s.mode = punchMode(s.local, s.remote)
	if s.mode == "pool" && s.poolPrediction {
		return nil
	}
	if s.mode != "single" && s.mode != "fixed" && s.mode != "next" {
		return errors.New("refreshed NAT model does not support prediction")
	}
	return nil
}
