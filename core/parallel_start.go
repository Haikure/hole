package core

import (
	"reflect"
	"sync"
)

// The relay owns an ordinary icePeer lifecycle but is not another device pair.
// Its attempts and statistics are isolated; only the parent chooses a sender.
func (c *iceCoordinator) validateParallelReady(m iceSignalMessage) error {
	r := m.ParallelRelay
	if r == nil {
		return nil
	}
	bad := &Fault{Code: "protocol_mismatch", Message: "并发中继计划与设备传输身份不一致"}
	if !m.ParallelStart || !m.PathUpgrade || !m.UpgradeProbe || !m.localAuthorization() || m.Profile != ProfileICE || m.RelayRequired || m.RoomControl ||
		r.ParallelRelay != nil || r.UpgradeProbe || r.UpgradeIdle || r.UpgradeCommit || r.RoomControl || r.RelayRequired ||
		r.TransportGeneration == 0 || r.TransportGeneration == m.TransportGeneration || r.TransportID != m.TransportID ||
		r.Profile != m.Profile || r.PeerDevice != m.PeerDevice || r.PeerRuntimeID != m.PeerRuntimeID || r.RuntimeID != m.RuntimeID ||
		r.PeerFingerprint != m.PeerFingerprint || r.InitiatorID != m.InitiatorID || r.CoreVersion != m.CoreVersion ||
		r.PeerCoreVersion != m.PeerCoreVersion || r.WorkerVersion != m.WorkerVersion || r.SessionVersion != m.SessionVersion ||
		r.Voice != m.Voice || r.VoiceRevision != m.VoiceRevision || r.VoiceGroup != m.VoiceGroup || r.LiveConfig != m.LiveConfig ||
		!reflect.DeepEqual(r.Mappings, m.Mappings) || r.RelayRound == nil || *r.RelayRound <= 0 ||
		!reflect.DeepEqual(r.RelayOrder, m.RelayOrder) || !reflect.DeepEqual(r.PeerRelayOrder, m.PeerRelayOrder) ||
		!reflect.DeepEqual(r.RelayPlan, m.RelayPlan) || r.PeerHoleDevice != m.PeerHoleDevice || r.HoleVersion != m.HoleVersion ||
		r.RelayOnly || r.PeerRelayOnly || r.RelayPairing != m.RelayPairing || r.RelayPolicy != m.RelayPolicy ||
		r.LeaseUntil != m.LeaseUntil || r.RetryAfterMS < 0 || r.RetryAfterMS > 15000 || r.PeerRelay != nil {
		return bad
	}
	if err := c.validateHoleReady(*r); err != nil {
		return err
	}
	return nil
}

func (p *icePeer) acceptsGenerationLocked(generation uint64) bool {
	return generation == p.ready.TransportGeneration || generation > max(p.ready.TransportGeneration, p.generationFloor)
}

func (p *icePeer) syncParallelRelay(m iceSignalMessage) {
	if p.parallelParent.Load() != nil {
		return
	}
	p.mu.Lock()
	if p.closed || !p.acceptsGenerationLocked(m.TransportGeneration) {
		p.mu.Unlock()
		return
	}
	relay := p.parallelRelay
	if m.ParallelRelay == nil {
		p.parallelRelay = nil
	} else if relay == nil {
		relay = newICEPeerWithParent(p.coordinator, *m.ParallelRelay, p)
		p.parallelRelay = relay
	}
	p.mu.Unlock()
	if relay != nil {
		if m.ParallelRelay == nil {
			relay.close()
		} else {
			// First availability takes precedence over background classification.
			// Prediction still reserves the allocator before sampling its model.
			relay.mu.Lock()
			allocating := relay.ready.TransportGeneration != m.ParallelRelay.TransportGeneration
			relay.mu.Unlock()
			if allocating {
				p.coordinator.natMu.Lock()
				if p.coordinator.natCancel != nil && p.coordinator.natProbeRunning {
					p.coordinator.natCancel()
				}
				p.coordinator.natMu.Unlock()
			}
			relay.update(*m.ParallelRelay)
		}
	}
}

func (p *icePeer) activationMutex() *sync.Mutex {
	if p.activation != nil {
		return p.activation
	}
	return &p.activationMu
}

func (p *icePeer) relayPeer() *icePeer {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.parallelRelay
}

func (p *icePeer) signalPeer(generation uint64) *icePeer {
	if relay := p.relayPeer(); relay != nil {
		relay.mu.Lock()
		matches := relay.ready.TransportGeneration == generation
		relay.mu.Unlock()
		if matches {
			return relay
		}
	}
	return p
}

func (p *icePeer) selectedMux(mux *muxPeer) bool {
	owner := p
	if parent := p.parallelParent.Load(); parent != nil {
		owner = parent
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	return !owner.closed && owner.active != nil && owner.active.mux == mux
}

// A late relay handshake can serve the endpoint that has not received commit,
// but cannot replace a direct path or bind application senders again.
func (p *icePeer) installParallelRelay(live *liveICETransport) bool {
	owner := p.parallelParent.Load()
	if owner == nil {
		return true
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.closed || owner.parallelRelay != p {
		return false
	}
	if owner.ready.UpgradeCommit || owner.active != nil && owner.active.mux.owner != p {
		return false
	}
	owner.active = live
	return true
}

func (p *icePeer) lostParallelRelay(live *liveICETransport) bool {
	owner := p.parallelParent.Load()
	if owner == nil {
		return true
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.active == live {
		owner.active = nil
	}
	return !owner.closed && owner.parallelRelay == p && !owner.ready.UpgradeCommit
}

func (p *icePeer) processRecorder() *natProcessRecorder {
	if p.recorder != nil {
		return p.recorder
	}
	return &p.process
}

// The Worker may select the startup TURN branch after both sides report that
// it actually became direct. Keep its authenticated mux and sender bindings;
// retiring the old primary must only cancel the other startup candidate.
func (c *iceCoordinator) promoteParallelDirect(p *icePeer, m iceSignalMessage) *icePeer {
	if !m.ParallelStart || m.Profile != ProfileICE || m.RoomControl || m.RelayRequired || m.ParallelRelay != nil ||
		m.UpgradeProbe || m.UpgradeIdle || m.UpgradeCommit || m.Phase != "direct" {
		return p
	}
	activation := p.activationMutex()
	activation.Lock()
	c.mu.Lock()
	p.mu.Lock()
	relay := p.parallelRelay
	if p.closed || p.ready.UpgradeCommit || relay == nil || c.peers[m.TransportID] != p {
		p.mu.Unlock()
		c.mu.Unlock()
		activation.Unlock()
		return p
	}
	relay.mu.Lock()
	ready := relay.ready
	if relay.closed || ready.TransportGeneration != m.TransportGeneration || ready.PeerRuntimeID != m.PeerRuntimeID ||
		ready.PeerFingerprint != m.PeerFingerprint || ready.RuntimeID != m.RuntimeID ||
		(relay.active != nil && relay.active.pathType != "direct") {
		relay.mu.Unlock()
		p.mu.Unlock()
		c.mu.Unlock()
		activation.Unlock()
		return p
	}
	p.parallelRelay = nil
	if p.active != nil && p.active.mux.owner == relay {
		p.active = nil
	}
	if p.retiring != nil && p.retiring.mux.owner == relay {
		p.retiring = nil
	}
	relay.generationFloor = max(relay.generationFloor, p.generationFloor, p.ready.TransportGeneration)
	relay.stats.PeerNAT = p.stats.PeerNAT
	relay.stats.PunchDiagnostics = append([]NATPunchDiagnostic(nil), p.stats.PunchDiagnostics...)
	relay.parallelParent.Store(nil)
	c.peers[m.TransportID] = relay
	relay.mu.Unlock()
	p.mu.Unlock()
	c.mu.Unlock()
	activation.Unlock()
	p.close()
	return relay
}

func (c *iceCoordinator) transportPeers() []*icePeer {
	peers := c.peerList()
	for _, p := range append([]*icePeer(nil), peers...) {
		if relay := p.relayPeer(); relay != nil {
			peers = append(peers, relay)
		}
	}
	return peers
}
