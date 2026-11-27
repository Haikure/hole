package core

import (
	"context"
	"errors"
	"time"
)

const upgradeInitialDelay = 15 * time.Second

// Only one endpoint schedules probes. The Worker makes topology-triggered
// Peer Relay attempts immediately, independently of this direct retry timer.
func (p *icePeer) probeUpgrade(now time.Time) {
	p.mu.Lock()
	m, active := p.ready, p.active
	if p.closed || !p.online || p.terminal || !m.PathUpgrade || m.RoomControl || m.RelayRequired || m.Profile != ProfileICE ||
		m.InitiatorID != p.coordinator.deviceID() || active == nil || active.mux.ctx.Err() != nil || active.pathType == "direct" ||
		p.pending != nil || p.retiring != nil || p.requestedGeneration != 0 || now.Before(p.nextUpgrade) {
		p.mu.Unlock()
		return
	}
	if p.nextUpgrade.IsZero() {
		p.nextUpgrade = now.Add(upgradeInitialDelay)
		p.mu.Unlock()
		return
	}
	p.requestedGeneration = m.TransportGeneration
	p.upgradeRequested = true
	p.nextUpgrade = now.Add(upgradeInitialDelay)
	p.mu.Unlock()
	request := signalMessage("transport_restart")
	request.TransportID, request.ExpectedGeneration, request.Reason = m.TransportID, m.TransportGeneration, "auto_upgrade"
	if m.UpgradeProbe && !m.UpgradeIdle && active.ready.TransportGeneration != m.TransportGeneration {
		request.Reason = "upgrade_failed"
	}
	if err := p.coordinator.send(request); err != nil {
		p.mu.Lock()
		if p.requestedGeneration == m.TransportGeneration {
			p.requestedGeneration = 0
			p.upgradeRequested = false
		}
		p.mu.Unlock()
	}
}

func (p *icePeer) deferUpgradeLocked(now time.Time) {
	p.upgradeFailures = min(p.upgradeFailures+1, 5)
	p.nextUpgrade = now.Add(min(5*time.Minute, upgradeInitialDelay*time.Duration(1<<p.upgradeFailures)))
}

func (p *icePeer) commitUpgrade(generation uint64) {
	p.mu.Lock()
	if p.closed || !p.online || p.ready.TransportGeneration != generation {
		p.mu.Unlock()
		return
	}
	a := p.pending
	if a != nil && a.ready.TransportGeneration == generation && a.upgradeCommit != nil {
		a.commitOnce.Do(func() { close(a.upgradeCommit) })
	}
	live := p.active
	installed := live != nil && live.ready.TransportGeneration == generation && live.mux.ctx.Err() == nil
	p.mu.Unlock()
	if installed {
		// A report queued on the old signaling socket may have been lost.
		// Replayed commits must acknowledge an already-installed path too.
		status := signalMessage("transport_established")
		status.TransportID, status.TransportGeneration, status.PathType = live.ready.TransportID, generation, live.pathType
		_ = p.coordinator.send(status)
	}
}

// QUIC and mux identity checks have finished, but the current relay remains
// active until both endpoints have prepared this exact candidate generation.
func (p *icePeer) prepareUpgrade(a *iceAttempt, live *liveICETransport) error {
	p.mu.Lock()
	current := p.pending == a && !p.closed
	path := p.stats.PathType
	p.mu.Unlock()
	if !current {
		return context.Canceled
	}
	if path != "direct" && path != "peer_relay" {
		return errors.New("upgrade candidate is not a preferred path")
	}
	request := signalMessage("transport_upgrade_prepared")
	request.TransportID, request.TransportGeneration, request.PathType = a.ready.TransportID, a.ready.TransportGeneration, path
	if err := p.coordinator.send(request); err != nil {
		return err
	}
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	select {
	case <-a.upgradeCommit:
		return nil
	case <-a.ctx.Done():
		return a.ctx.Err()
	case <-live.mux.ctx.Done():
		return context.Cause(live.mux.ctx)
	case err := <-a.failed:
		return err
	case <-timer.C:
		return errors.New("timed out waiting for bilateral path upgrade")
	}
}

func (p *icePeer) failedUpgrade(a *iceAttempt, err error) {
	p.mu.Lock()
	if p.pending != a || p.closed {
		p.mu.Unlock()
		return
	}
	p.pending = nil
	p.stats.State = "active"
	p.nextUpgrade = time.Now().Add(upgradeInitialDelay)
	p.mu.Unlock()
	fault := classifyError(err)
	request := signalMessage("transport_restart")
	request.TransportID, request.ExpectedGeneration = a.ready.TransportID, a.ready.TransportGeneration
	request.Reason = "upgrade_failed"
	if a.ready.Phase == "direct" && a.ready.DirectOrder == directOrderICEFirst && a.ready.DirectStage == "ice" && !terminalTransportFault(fault) {
		request.Reason = "ice_failed"
	}
	// Candidate authentication errors never replace or terminate the already
	// authenticated active path. This candidate is discarded on both endpoints.
	_ = p.coordinator.send(request)
	a.cancel()
	p.stateChanged()
}
