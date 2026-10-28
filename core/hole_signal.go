package core

import (
	"reflect"
	"strconv"
	"time"
)

func (c *iceCoordinator) validateHoleReady(m iceSignalMessage) error {
	cfg := c.request().Config
	bad := func() error {
		return &Fault{Code: "protocol_mismatch", Message: "Hole 路径计划或三方授权无效"}
	}
	if m.HoleVersion != HoleRelayVersion {
		return &Fault{Code: "peer_upgrade_required", Message: "Hole 中继需要更新 Worker 和对端客户端"}
	}
	if m.Profile != ProfileICE || m.RelayRound == nil || m.RelayPairing != relayPairingAll || m.HoleDevice != cfg.Relay.Device {
		return bad()
	}
	local := cfg.DeviceName
	validOrder := func(order []string) bool {
		if len(order) > 6 {
			return false
		}
		seen := map[string]bool{}
		for _, v := range order {
			if v != "hole" && !validRelayOrder([]string{v}) || seen[v] {
				return false
			}
			seen[v] = true
		}
		return true
	}
	if !validOrder(m.RelayOrder) || !validOrder(m.PeerRelayOrder) {
		return bad()
	}
	first := local < m.PeerDevice
	a, b, ad, bd := m.RelayOrder, m.PeerRelayOrder, m.HoleDevice, m.PeerHoleDevice
	if !first {
		a, b, ad, bd = b, a, bd, ad
	}
	if ad == local || ad == m.PeerDevice || bd == local || bd == m.PeerDevice {
		return bad()
	}
	want := deviceRelayPlan(a, b, ad, bd)
	if !reflect.DeepEqual(want, m.RelayPlan) || len(want) > 36 || plannedPhase(m, *m.RelayRound, local) != m.Phase {
		return bad()
	}
	if m.Phase != "hole" {
		if m.Hole != nil {
			return bad()
		}
		return nil
	}
	g := m.Hole
	if *m.RelayRound < 1 || g == nil || g.Device != want[*m.RelayRound-1].Device || g.Generation != m.TransportGeneration {
		return bad()
	}
	if g.Error != "" {
		return nil
	}
	side := 0
	if !first {
		side = 1
	}
	if g.Devices[side] != local || g.Devices[1-side] != m.PeerDevice || g.Runtimes[side] != c.runtimeID || g.Runtimes[1-side] != m.PeerRuntimeID || g.Epochs[side] != strconv.FormatUint(c.networkEpoch.Load(), 10) || g.RelayRuntime == "" || validateFixedAddress(g.Endpoint, false) != nil || m.LeaseUntil <= time.Now().UnixMilli() {
		return bad()
	}
	_, err := newHoleAuth(g)
	return err
}
func (c *iceCoordinator) handleHoleGrant(m iceSignalMessage) error {
	cfg := c.request().Config
	c.fixedMu.Lock()
	defer c.fixedMu.Unlock()
	f := c.fixed
	if !cfg.Relay.Serve.Enabled || f == nil || f.relay == nil {
		return nil
	}
	if m.Type == "hole_revoke" {
		f.relay.revoke(m.TransportID, m.TransportGeneration)
		return nil
	}
	if m.HoleVersion != HoleRelayVersion || m.RuntimeID != c.runtimeID || m.TransportEpoch != c.networkEpoch.Load() || m.Hole == nil || m.Hole.Device != cfg.DeviceName || m.Hole.RelayRuntime != c.runtimeID || m.Hole.RelayEpoch != c.networkEpoch.Load() || m.Hole.Endpoint != cfg.Transport.FixedEndpoint.Advertise {
		return &Fault{Code: "protocol_mismatch", Message: "中继服务授权与本机运行实例不一致"}
	}
	if err := f.relay.install(m); err != nil {
		failure := signalMessage("hole_failed")
		failure.TransportID = m.TransportID
		failure.TransportGeneration = m.TransportGeneration
		_ = c.send(failure)
		c.emit(Event{Kind: "relay", State: "unavailable", Error: &Fault{Code: "hole_unavailable", Message: "中继路由授权未能启用"}})
	} else {
		c.emit(Event{Kind: "relay", State: "changed"})
	}
	return nil
}
func (c *iceCoordinator) relaySnapshot() *holeCounters {
	c.fixedMu.Lock()
	defer c.fixedMu.Unlock()
	if c.fixed == nil || c.fixed.relay == nil {
		return nil
	}
	s := c.fixed.relay.snapshot()
	return &s
}
