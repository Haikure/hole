package core

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"net/netip"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"
	"hole/core/internal/nat"
)

func natUnknown() NATSnapshot { return nat.Unknown("尚未探测") }

func (p *icePeer) publishNAT() {
	s := p.coordinator.localNAT()
	if s.ProbedAt == "" {
		return
	}
	p.mu.Lock()
	ready := p.ready
	published := p.natPublished
	p.mu.Unlock()
	key := ready.PunchKey + s.ProbedAt
	if ready.UDPPunchVersion != udpPunchVersion || key == published {
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
			ctx, cancel := context.WithTimeout(c.ctx, nat.ProbeTimeout)
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
	return append(out, "stun:stun.miwifi.com:3478", "stun:stun.chat.bilibili.com:3478", "stun:stun.hitv.com:3478")
}

func (p *icePeer) attemptNAT(a *iceAttempt) {
	a.supplement = false
	started := time.Now()
	ctx, cancel := context.WithTimeout(a.ctx, nat.Budget)
	defer cancel()
	fail := func(err error) { p.failed(a, err); a.cancel() }
	c := p.coordinator
	select {
	case c.natSlots <- struct{}{}:
	case <-ctx.Done():
		fail(ctx.Err())
		return
	}
	held := true
	defer func() {
		if held {
			<-c.natSlots
		}
	}()
	for {
		select {
		case <-a.failed:
			continue
		default:
		}
		break
	}
	epoch := c.networkEpoch.Load()
	platform, err := c.pin(ctx)
	if err != nil {
		fail(err)
		return
	}
	pc, err := platform.ListenPacket(ctx, "udp4", "0.0.0.0:0")
	if err != nil {
		fail(err)
		return
	}
	defer func() {
		if pc != nil {
			_ = pc.Close()
		}
	}()
	p.transportEvent(a, "checking", nil)
	probeCtx, stopProbe := context.WithTimeout(ctx, nat.ProbeTimeout)
	local, addresses := nat.Probe(probeCtx, platform, pc, natURLs(c.request().Config.ICE))
	stopProbe()
	local.Epoch = epoch
	var mapping *nat.Mapping
	if c.request().Config.ICE.PortMapping != "off" {
		if source, ok := platform.(GatewayPlatform); ok {
			mappingCtx, stop := context.WithTimeout(ctx, 1500*time.Millisecond)
			gateway, e := source.DefaultGateway(mappingCtx)
			if e == nil {
				if port, e := netip.ParseAddrPort(pc.LocalAddr().String()); e == nil {
					mapping, e = nat.MapPort(mappingCtx, platform, gateway, port.Port())
				}
			}
			stop()
			p.mu.Lock()
			if mapping != nil {
				p.stats.PortMapping = mapping.Snapshot
			} else {
				p.stats.PortMapping = nat.MappingSnapshot{Status: "unavailable"}
			}
			p.mu.Unlock()
		}
	}
	if mapping != nil {
		mapped, e := netip.ParseAddrPort(mapping.Snapshot.Address)
		if e == nil {
			addresses = append(addresses, mapped)
		}
		mappingCtx, stop := context.WithCancel(a.ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			mapping.Maintain(mappingCtx, func() { p.restart("direct", "mapping_changed") })
		}()
		defer func() { stop(); <-done }()
	}
	if epoch != c.networkEpoch.Load() {
		fail(errors.New("网络代次已改变"))
		return
	}
	c.natMu.Lock()
	c.natSnapshot = local
	c.natMu.Unlock()
	p.mu.Lock()
	if a.natEvents == nil {
		a.natEvents = make(chan iceSignalMessage, 4)
	}
	ch := a.natEvents
	p.stats.LocalNAT = local
	p.mu.Unlock()
	plan := &natPlan{Snapshot: local, Addresses: []string{}}
	for _, v := range addresses {
		if len(plan.Addresses) < 8 {
			plan.Addresses = append(plan.Addresses, v.String())
		}
	}
	report := signalMessage("nat_report")
	report.TransportID = a.ready.TransportID
	report.TransportGeneration = a.ready.TransportGeneration
	report.ToPeerID = a.ready.PeerDevice
	report.NAT = plan
	if err = c.send(report); err != nil {
		fail(err)
		return
	}
	var peer *natPlan
	select {
	case m := <-ch:
		peer = m.NAT
	case <-ctx.Done():
		fail(ctx.Err())
		return
	}
	if peer == nil {
		fail(errors.New("对端 NAT 报告缺失"))
		return
	}
	remoteAddresses := []netip.AddrPort{}
	for _, raw := range peer.Addresses {
		v, e := netip.ParseAddrPort(raw)
		if e == nil && v.Addr().Is4() && !v.Addr().IsUnspecified() && !v.Addr().IsMulticast() {
			remoteAddresses = append(remoteAddresses, v)
		}
	}
	p.mu.Lock()
	p.stats.PeerNAT = peer.Snapshot
	p.stats.DirectStrategy = nat.Strategy(local, peer.Snapshot)
	p.mu.Unlock()
	key, err := hex.DecodeString(a.ready.PunchKey)
	if err != nil {
		fail(err)
		return
	}
	controlling := a.ready.InitiatorID == c.deviceID()
	socket, remote, err := nat.Punch(ctx, platform, pc, local, peer.Snapshot, remoteAddresses, key, controlling)
	pc = nil // Punch owns all sockets, including failure paths.
	if err != nil {
		fail(err)
		return
	}
	transport := &quic.Transport{Conn: socket}
	owned := false
	defer func() {
		if !owned {
			_ = transport.Close()
			_ = socket.Close()
		}
	}()
	tlsConfig := c.identity.Clone()
	tlsConfig.NextProtos = []string{iceALPN}
	tlsConfig.InsecureSkipVerify = true
	tlsConfig.ClientAuth = tls.RequireAnyClientCert
	var identityMismatch atomic.Bool
	tlsConfig.VerifyConnection = func(state tls.ConnectionState) error {
		if len(state.PeerCertificates) > 0 {
			sum := sha256.Sum256(state.PeerCertificates[0].Raw)
			if hex.EncodeToString(sum[:]) == a.ready.PeerFingerprint {
				return nil
			}
		}
		identityMismatch.Store(true)
		return &Fault{Code: "peer_identity_mismatch", Message: "对端身份与协调服务记录不一致"}
	}
	var conn *quic.Conn
	if controlling {
		conn, err = transport.Dial(ctx, remote, tlsConfig, iceQUICConfig("direct"))
	} else {
		var listener *quic.Listener
		listener, err = transport.Listen(tlsConfig, iceQUICConfig("direct"))
		if err == nil {
			conn, err = listener.Accept(ctx)
		}
	}
	if err != nil {
		if identityMismatch.Load() {
			err = &Fault{Code: "peer_identity_mismatch", Message: "对端身份校验未通过"}
		}
		fail(err)
		return
	}
	state := conn.ConnectionState().SupportsDatagrams
	if !state.Local || !state.Remote {
		_ = conn.CloseWithError(1, "DATAGRAM required")
		fail(&Fault{Code: "protocol_mismatch", Message: "对端未启用 QUIC 数据报"})
		return
	}
	// Bound mux negotiation by the remaining budget without making a successful
	// transport inherit the preparation deadline.
	stopMux := context.AfterFunc(ctx, func() { _ = conn.CloseWithError(1, "直连预算已用尽") })
	mux, err := newMuxPeer(a.ctx, p, conn, a.ready)
	stopped := stopMux()
	if err != nil || !stopped || ctx.Err() != nil {
		if err == nil {
			err = context.DeadlineExceeded
		}
		fail(err)
		return
	}
	live := &liveICETransport{ready: a.ready, mux: mux, session: conn, cancel: a.cancel, fixedCleanup: func() { _ = transport.Close(); _ = socket.Close() }}
	p.mu.Lock()
	if p.pending != a || p.closed || epoch != c.networkEpoch.Load() {
		p.mu.Unlock()
		live.close()
		return
	}
	old := p.active
	p.active = live
	p.pending = nil
	p.stats.State = "active"
	p.stats.PathType = "direct"
	p.stats.LocalType = "srflx"
	p.stats.RemoteType = "srflx"
	p.stats.LocalAddress = socket.LocalAddr().String()
	p.stats.RemoteAddress = remote.String()
	p.stats.Error = nil
	p.stats.ConnectMS = time.Since(started).Milliseconds()
	p.mu.Unlock()
	owned = true
	// The semaphore covers preparation only, never the lifetime of the tunnel.
	<-c.natSlots
	held = false
	cancel()
	if a.ready.Voice {
		if v := c.voiceRuntimePtr(); v != nil {
			v.bindPeer(a.ready.PeerDevice, a.ready.TransportID, a.ready.TransportGeneration, mux)
			defer v.unbindPeer(a.ready.PeerDevice, mux)
		}
	}
	mux.syncMappings()
	if old != nil {
		old.close()
	}
	p.stateChanged()
	status := signalMessage("transport_established")
	status.TransportID = a.ready.TransportID
	status.TransportGeneration = a.ready.TransportGeneration
	_ = c.send(status)
	select {
	case <-mux.ctx.Done():
	case <-a.ctx.Done():
	case <-a.failed:
	}
	live.close()
	p.mu.Lock()
	current := p.active == live
	if current {
		p.active = nil
		p.stats.State = "reconnecting"
	}
	p.mu.Unlock()
	if current && p.ctx.Err() == nil {
		p.stateChanged()
		p.restart("direct", "path_lost")
	}
}
