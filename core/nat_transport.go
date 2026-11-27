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
	connected := p.active != nil
	published := p.natPublished
	lastPublished := p.natPublishedAt
	online := p.online && !p.closed
	p.mu.Unlock()
	key := fmt.Sprintf("%s/%d/%s", ready.TransportID, ready.TransportGeneration, s.ProbedAt)
	if !online || !connected || ready.Profile != ProfileICE || ready.RelayRequired || (!ready.localAuthorization() && ready.LeaseUntil <= time.Now().UnixMilli()) || key == published || time.Since(lastPublished) < 30*time.Second {
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

func (c *iceCoordinator) localNATProcess() *natProcessRecorder {
	c.natMu.Lock()
	defer c.natMu.Unlock()
	if c.natProbeEpoch != c.networkEpoch.Load() {
		return nil
	}
	return c.natProbeProcess
}

func (c *iceCoordinator) natConnectionEstablished() {
	c.natMu.Lock()
	c.natConnected = true
	c.natMu.Unlock()
}

func (c *iceCoordinator) requestNATProbeForConnection() {
	c.natMu.Lock()
	snapshot := c.natSnapshot.Current()
	needed := c.natConnected || snapshot.ProbedAt == "" || snapshot.Status == "expired"
	if needed && c.natProbeRunning {
		// The in-flight probe started this discovery cycle and will publish the
		// fresh result that the connection attempt is waiting for.
		c.natConnected = false
		needed = false
	}
	if needed {
		c.natConnected = false
		c.natSnapshot = natUnknown()
		c.natProbeRequested = true
	}
	c.natMu.Unlock()
	if needed {
		select {
		case c.natProbeWake <- struct{}{}:
		default:
		}
	}
}

func natProbeDue(epoch, lastEpoch uint64, initialized, connected bool, next, now time.Time) bool {
	return !initialized || epoch != lastEpoch || (!connected && now.After(next))
}

func (c *iceCoordinator) monitorNAT() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var lastEpoch uint64
	var initialized bool
	var next time.Time
	for {
		cfg := c.request().Config
		epoch := c.networkEpoch.Load()
		c.natMu.Lock()
		connected := c.natConnected
		requested := c.natProbeRequested
		c.natMu.Unlock()
		if !cfg.ICE.RelayOnly && cfg.Transport.FixedEndpoint == nil && (requested || natProbeDue(epoch, lastEpoch, initialized, connected, next, time.Now())) {
			lastEpoch = epoch
			initialized = true
			next = time.Now().Add(nat.Lifetime)
			ctx, cancel := context.WithCancel(c.ctx)
			c.natMu.Lock()
			c.natProbeRequested = false
			c.natProbeRunning = true
			c.natCancel = cancel
			if epoch != c.networkEpoch.Load() {
				cancel()
			} else {
				c.natSnapshot = NATSnapshot{Status: "probing", Type: "unknown", Predictability: "unknown", Epoch: epoch}
			}
			c.natMu.Unlock()
			// Queue before starting the probe budget. Do not consume mappings
			// while another device pair is using its calibrated prediction.
			release, lockErr := c.natAllocation.acquire(ctx)
			if lockErr != nil {
				cancel()
				if c.ctx.Err() != nil {
					return
				}
				c.natMu.Lock()
				c.natProbeRunning = false
				c.natMu.Unlock()
				continue
			}
			probeCtx, stopProbe := context.WithTimeout(ctx, 3*natprobe.DefaultTimeout)
			ctx = probeCtx
			platform, err := c.pin(ctx)
			if err == nil {
				if v, ok := platform.(interface{ NATIPv4Available() bool }); ok && !v.NATIPv4Available() {
					c.natMu.Lock()
					if epoch == c.networkEpoch.Load() {
						c.natSnapshot = natUnknown()
						c.natSnapshot.Status, c.natSnapshot.Epoch, c.natSnapshot.ProbedAt = "not_applicable", epoch, time.Now().UTC().Format(time.RFC3339Nano)
					}
					c.natMu.Unlock()
					stopProbe()
					release()
					cancel()
					c.natMu.Lock()
					c.natProbeRunning = false
					c.natMu.Unlock()
					select {
					case <-c.ctx.Done():
						return
					case <-ticker.C:
						continue
					}
				}
			}
			if err == nil {
				process := &natProcessRecorder{}
				trace := process.begin(iceSignalMessage{TransportID: "nat-discovery", TransportGeneration: epoch, Phase: "nat"})
				if source, ok := platform.(interface{ NATNetworkSnapshot() NetworkSnapshot }); ok {
					trace.network(source.NATNetworkSnapshot())
				}
				c.natMu.Lock()
				if epoch == c.networkEpoch.Load() {
					c.natProbeProcess, c.natProbeEpoch = process, epoch
				}
				c.natMu.Unlock()
				s := probeNATWithRetriesObserved(ctx, platform, natURLs(cfg.ICE), trace, 0)
				trace.models(0, "discovery", s, NATSnapshot{})
				trace.finish(s.Status, s.Reason)
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
			stopProbe()
			release()
			cancel()
			// A failed socket/DNS/platform operation must also finish discovery;
			// consumers may wait for this bounded classification pass.
			c.natMu.Lock()
			if epoch == c.networkEpoch.Load() && c.natSnapshot.ProbedAt == "" {
				c.natSnapshot = nat.Unknown("NAT 探测未取得足够证据")
				c.natSnapshot.Epoch = epoch
			}
			c.natMu.Unlock()
			c.natMu.Lock()
			c.natProbeRunning = false
			c.natMu.Unlock()
		}
		select {
		case <-c.ctx.Done():
			return
		case <-c.natProbeWake:
			continue
		case <-ticker.C:
		}
	}
}

// Repeat a full discovery, with fresh sockets, only while classification is
// insufficient. A known irregular symmetric NAT is a conclusive classification.
func probeNATWithRetries(ctx context.Context, network nat.Network, urls []string) NATSnapshot {
	return probeNATWithRetriesObserved(ctx, network, urls, nil, 0)
}

func probeNATWithRetriesObserved(ctx context.Context, network nat.Network, urls []string, process *natProcessRun, round int) NATSnapshot {
	s := nat.Unknown("NAT 探测未取得足够证据")
	if len(urls) == 0 {
		return s
	}
	for attempt := 0; attempt < 3 && ctx.Err() == nil; attempt++ {
		probeCtx, stop := context.WithTimeout(ctx, natprobe.DefaultTimeout)
		probeNetwork := network
		if process != nil {
			probeNetwork = &natProcessNetwork{Network: network, process: process, round: round, trial: attempt + 1}
			process.record(NATPunchProcessEvent{Kind: "stage", Stage: "nat_sampling", Title: "开始 NAT 映射与过滤采样", Round: round, Trial: attempt + 1})
		}
		pc, err := probeNetwork.ListenPacket(probeCtx, "udp4", "0.0.0.0:0")
		if err == nil {
			s, _ = nat.Probe(probeCtx, probeNetwork, pc, urls)
			_ = pc.Close()
		}
		stop()
		if s.Status == "complete" {
			break
		}
	}
	return s
}

// An empty classification list uses built-ins, supplementing ordinary STUN.
func natURLs(cfg ICEConfig) []string {
	if len(cfg.NATSTUNURLs) > 0 {
		return append([]string{}, cfg.NATSTUNURLs...)
	}
	out := append([]string{}, cfg.STUNURLs...)
	seen := make(map[string]bool, len(out))
	for _, raw := range out {
		seen[raw] = true
	}
	for _, raw := range natprobe.DefaultServers() {
		if len(out) >= natprobe.MaxServers {
			break
		}
		if !seen[raw] {
			out = append(out, raw)
			seen[raw] = true
		}
	}
	return out
}
