package core

import (
	"context"
	"fmt"
	"time"

	"hole/core/internal/nat"
	"hole/core/natprobe"
)

func natUnknown() NATSnapshot {
	s := nat.Unknown("尚未探测")
	s.ProbedAt = ""
	return s
}

func (p *icePeer) publishNAT() {
	s := p.coordinator.localNAT()
	if s.ProbedAt == "" {
		return
	}
	p.mu.Lock()
	ready := p.ready
	published := p.natPublished
	lastPublished := p.natPublishedAt
	online := p.online && !p.closed
	p.mu.Unlock()
	key := fmt.Sprintf("%s/%d/%s", ready.TransportID, ready.TransportGeneration, s.ProbedAt)
	if !online || ready.Profile != ProfileICE || ready.RelayRequired || ready.LeaseUntil <= time.Now().UnixMilli() || key == published || time.Since(lastPublished) < 30*time.Second {
		return
	}
	m := signalMessage("nat_status")
	m.TransportID = ready.TransportID
	m.TransportGeneration = ready.TransportGeneration
	m.ToPeerID = ready.PeerDevice
	m.NAT = &natPlan{Snapshot: s, Addresses: []string{}}
	if p.coordinator.send(m) == nil {
		p.mu.Lock()
		p.natPublished = key
		p.natPublishedAt = time.Now()
		p.mu.Unlock()
	}
}

func (c *iceCoordinator) localNAT() NATSnapshot {
	c.natMu.Lock()
	defer c.natMu.Unlock()
	if c.natSnapshot.Epoch != c.networkEpoch.Load() {
		return natUnknown()
	}
	return c.natSnapshot.Current()
}

func (c *iceCoordinator) monitorNAT() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var lastEpoch uint64
	var next time.Time
	for {
		cfg := c.request().Config
		epoch := c.networkEpoch.Load()
		if !cfg.ICE.RelayOnly && cfg.Transport.FixedEndpoint == nil && (epoch != lastEpoch || time.Now().After(next)) {
			lastEpoch = epoch
			next = time.Now().Add(nat.Lifetime)
			ctx, cancel := context.WithTimeout(c.ctx, natprobe.DefaultTimeout)
			platform, err := c.pin(ctx)
			if err == nil {
				if v, ok := platform.(interface{ NATIPv4Available() bool }); ok && !v.NATIPv4Available() {
					c.natMu.Lock()
					if epoch == c.networkEpoch.Load() {
						c.natSnapshot = NATSnapshot{Status: "not_applicable", Type: "unknown", Predictability: "unknown", Epoch: epoch, ProbedAt: time.Now().UTC().Format(time.RFC3339Nano)}
					}
					c.natMu.Unlock()
					cancel()
					select {
					case <-c.ctx.Done():
						return
					case <-ticker.C:
						continue
					}
				}
			}
			if err == nil {
				pc, e := platform.ListenPacket(ctx, "udp4", "0.0.0.0:0")
				if e == nil {
					c.natMu.Lock()
					c.natSnapshot = NATSnapshot{Status: "probing", Type: "unknown", Predictability: "unknown", Epoch: epoch}
					c.natMu.Unlock()
					s, _ := nat.Probe(ctx, platform, pc, natURLs(cfg.ICE))
					_ = pc.Close()
					s.Epoch = epoch
					if source, ok := platform.(interface{ NATNetworkIdentity() string }); ok {
						s.Network = source.NATNetworkIdentity()
					}
					c.natMu.Lock()
					if epoch == c.networkEpoch.Load() {
						c.natSnapshot = s
					}
					c.natMu.Unlock()
				}
			}
			cancel()
		}
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Explicitly disabling ordinary STUN also disables implicit external probes.
func natURLs(cfg ICEConfig) []string {
	if cfg.NATSTUNURLs != nil {
		return append([]string{}, cfg.NATSTUNURLs...)
	}
	if len(cfg.STUNURLs) == 0 {
		return nil
	}
	out := append([]string{}, cfg.STUNURLs...)
	seen := make(map[string]bool, len(out))
	for _, raw := range out {
		seen[raw] = true
	}
	for _, raw := range natprobe.DefaultServers() {
		if !seen[raw] {
			out = append(out, raw)
			seen[raw] = true
		}
	}
	return out
}
