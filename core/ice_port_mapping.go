package core

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/pion/ice/v4"
	"hole/core/internal/nat"
)

// A mapped candidate is another address for an existing ICE host socket.
// Pion still authenticates and nominates the pair; no separate packet reader
// or transport is created. In particular, NAT-PMP may change the external port.
type icePortMapper struct {
	ctx       context.Context
	cancel    context.CancelFunc
	platform  Platform
	mu        sync.Mutex
	closed    bool
	attempted bool
	workers   sync.WaitGroup
	status    func(nat.MappingSnapshot)
	changed   func()
}

func newICEPortMapper(ctx context.Context, platform Platform, status func(nat.MappingSnapshot), changed func()) *icePortMapper {
	ctx, cancel := context.WithCancel(ctx)
	return &icePortMapper{ctx: ctx, cancel: cancel, platform: platform, status: status, changed: changed}
}

func (m *icePortMapper) candidate(host ice.Candidate) ice.Candidate {
	if host == nil || host.Type() != ice.CandidateTypeHost || host.NetworkType() != ice.NetworkTypeUDP4 {
		return nil
	}
	ip, err := netip.ParseAddr(host.Address())
	if err != nil || !ip.Is4() || ip.IsUnspecified() || ip.IsMulticast() {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.attempted || m.ctx.Err() != nil {
		return nil
	}
	source, ok := m.platform.(GatewayPlatform)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(m.ctx, 1500*time.Millisecond)
	defer cancel()
	gateway, err := source.DefaultGateway(ctx)
	if err != nil {
		m.attempted = true
		m.status(nat.MappingSnapshot{Status: "unavailable"})
		return nil
	}
	// Only map the interface selected by the pinned network's default route.
	conn, err := m.platform.DialUDP(ctx, net.UDPAddrFromAddrPort(netip.AddrPortFrom(gateway, 1900)))
	if err != nil {
		return nil
	}
	local, err := netip.ParseAddrPort(conn.LocalAddr().String())
	_ = conn.Close()
	if err != nil || local.Addr().Unmap() != ip {
		return nil
	}
	m.attempted = true
	mapping, err := nat.MapPort(ctx, m.platform, gateway, uint16(host.Port()))
	if err != nil {
		m.status(nat.MappingSnapshot{Status: "unavailable"})
		return nil
	}
	// Own the lease even if cancellation races with creation or validation.
	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		mapping.Maintain(m.ctx, m.changed)
	}()
	address, err := netip.ParseAddrPort(mapping.Snapshot.Address)
	if err != nil || !address.Addr().Is4() || address.Addr().IsUnspecified() || address.Addr().IsMulticast() || address.Port() == 0 {
		m.cancel()
		m.status(nat.MappingSnapshot{Status: "unavailable"})
		return nil
	}
	candidate, err := ice.NewCandidateServerReflexive(&ice.CandidateServerReflexiveConfig{
		Network: "udp", Address: address.Addr().String(), Port: int(address.Port()),
		Component: host.Component(), RelAddr: host.Address(), RelPort: host.Port(),
	})
	if err != nil {
		m.cancel()
		return nil
	}
	m.status(mapping.Snapshot)
	return candidate
}

func (m *icePortMapper) close() {
	m.cancel()
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	m.workers.Wait()
}
