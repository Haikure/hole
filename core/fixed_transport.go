package core

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"
)

const fixedAttemptTimeout = 10 * time.Second

type fixedIncoming struct {
	conn     *quic.Conn
	stream   *quic.Stream
	hello    muxHello
	deadline time.Time
}

// One socket and Transport serve every authorized peer of this runtime.
type fixedListener struct {
	relay     *holeForwarder
	serve     RelayServeConfig
	config    FixedEndpointConfig
	epoch     uint64
	ctx       context.Context
	cancel    context.CancelFunc
	packet    net.PacketConn
	transport *quic.Transport
	listener  *quic.EarlyListener
	workers   sync.WaitGroup
	once      sync.Once
}

func (f *fixedListener) close() {
	f.once.Do(func() {
		f.cancel()
		_ = f.listener.Close()
		_ = f.transport.Close()
		_ = f.packet.Close()
		f.workers.Wait()
	})
}

func (c *iceCoordinator) closeFixed() {
	c.fixedMu.Lock()
	f := c.fixed
	c.fixed = nil
	c.fixedMu.Unlock()
	if f != nil {
		f.close()
	}
}

func (c *iceCoordinator) watchFixedRelay(f *fixedListener) {
	if f.relay == nil {
		return
	}
	failure := false
	select {
	case <-f.relay.fatalFailure:
		failure = true
	case <-f.ctx.Done():
		select {
		case <-f.relay.fatalFailure:
			failure = true
		default:
			return
		}
	case <-c.ctx.Done():
		select {
		case <-f.relay.fatalFailure:
			failure = true
		default:
			return
		}
	}
	if !failure {
		return
	}
	c.fixedMu.Lock()
	if c.fixed != f {
		c.fixedMu.Unlock()
		return
	}
	c.fixed = nil
	c.fixedMu.Unlock()

	f.close()
	if c.ctx.Err() != nil {
		return
	}
	if c.emit != nil {
		c.emit(Event{Kind: "relay", State: "restarting", Error: &Fault{
			Code: "hole_forwarder_failed", Message: "Hole 转发器发生不可恢复错误，正在重建共享固定入口",
		}})
	}
	select {
	case c.reset <- struct{}{}:
	default:
	}
}

func (c *iceCoordinator) configureFixed(r Request) error {
	c.fixedMu.Lock()
	f := c.fixed
	epoch := c.networkEpoch.Load()
	endpoint := r.Config.Transport.FixedEndpoint
	if f != nil && endpoint != nil && f.config == *endpoint && f.epoch == epoch && f.serve == r.Config.Relay.Serve {
		c.fixedMu.Unlock()
		return nil
	}
	c.fixed = nil
	c.fixedMu.Unlock()
	if f != nil {
		f.close()
	}
	if endpoint == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(c.ctx)
	platform, err := c.pin(ctx)
	if err != nil {
		cancel()
		return err
	}
	host, _, _ := net.SplitHostPort(endpoint.Listen)
	network := "udp4"
	if net.ParseIP(host).To4() == nil {
		network = "udp6"
	}
	packet, err := platform.ListenPacket(ctx, network, endpoint.Listen)
	if err != nil {
		cancel()
		return err
	}
	var relay *holeForwarder
	if r.Config.Relay.Serve.Enabled {
		relay = newHoleForwarder(ctx, packet, r.Config.Relay.Serve)
		packet = relay.quicPacketConn()
	}
	transport := &quic.Transport{Conn: packet}
	tlsConfig := c.identity.Clone()
	tlsConfig.NextProtos = []string{fixedALPN}
	tlsConfig.ClientAuth = tls.RequireAnyClientCert
	// Accept early only to start the shared handshake budget; routeFixed waits
	// for TLS completion before inspecting a hello. 0-RTT remains disabled.
	listener, err := transport.ListenEarly(tlsConfig, iceQUICConfig("direct"))
	if err != nil {
		cancel()
		_ = transport.Close()
		_ = packet.Close()
		return err
	}
	f = &fixedListener{config: *endpoint, epoch: epoch, ctx: ctx, cancel: cancel, packet: packet, transport: transport, listener: listener}
	f.relay, f.serve = relay, r.Config.Relay.Serve
	c.fixedMu.Lock()
	c.fixed = f
	c.fixedMu.Unlock()
	f.workers.Add(1)
	go func() {
		defer f.workers.Done()
		slots := make(chan struct{}, maxSessionHandshakes)
		for {
			conn, err := listener.Accept(ctx)
			if err != nil {
				return
			}
			select {
			case slots <- struct{}{}:
				f.workers.Add(1)
				go func() {
					defer f.workers.Done()
					defer func() { <-slots }()
					c.routeFixed(f.ctx, conn)
				}()
			default:
				_ = conn.CloseWithError(1, "handshake limit")
			}
		}
	}()
	if relay != nil {
		go c.watchFixedRelay(f)
	}
	return nil
}

func fixedFingerprint(conn *quic.Conn) string {
	certs := conn.ConnectionState().TLS.PeerCertificates
	if len(certs) == 0 {
		return ""
	}
	sum := sha256.Sum256(certs[0].Raw)
	return hex.EncodeToString(sum[:])
}

// Only the first control hello is read before selecting a peer. No mux or
// business socket is created until the signal record and certificate match.
func (c *iceCoordinator) routeFixed(ctx context.Context, conn *quic.Conn) {
	delivered := false
	defer func() {
		if !delivered {
			_ = conn.CloseWithError(1, "fixed transport not authorized")
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, fixedAttemptTimeout)
	defer cancel()
	select {
	case <-ctx.Done():
		return
	case <-conn.Context().Done():
		return
	case <-conn.HandshakeComplete():
	}
	stream, err := conn.AcceptStream(ctx)
	if err != nil {
		return
	}
	deadline, _ := ctx.Deadline()
	_ = stream.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { stream.CancelRead(0); stream.CancelWrite(0) })
	defer stop()
	kind, id, err := readMuxPrefix(stream)
	if err != nil || kind != muxControl || id != 0 {
		return
	}
	var hello muxHello
	if readSessionJSON(stream, &hello) != nil {
		return
	}
	c.mu.Lock()
	p := c.peers[hello.TransportID]
	c.mu.Unlock()
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	a := p.pending
	if a == nil || a.ctx.Err() != nil || a.fixedClaimed || p.closed || !p.online {
		return
	}
	m := a.ready
	if m.Profile != ProfileFixed || m.ListenerID != c.deviceID() || m.LeaseUntil <= time.Now().UnixMilli() || hello.Profile != ProfileFixed || hello.Version != sessionProtocol || hello.Generation != m.TransportGeneration || hello.LocalRuntime != m.PeerRuntimeID || hello.RemoteRuntime != c.runtimeID || fixedFingerprint(conn) != m.PeerFingerprint {
		return
	}
	select {
	case a.incoming <- &fixedIncoming{conn: conn, stream: stream, hello: hello, deadline: deadline}:
		a.fixedClaimed = true
		delivered = true
	default:
	}
}

func (c *iceCoordinator) validateFixedReady(m iceSignalMessage) error {
	cfg := c.request().Config
	bad := func() error {
		return &Fault{Code: "protocol_mismatch", Message: "协调服务返回的固定入口角色、地址或授权无效"}
	}
	if cfg.ICE.RelayOnly || m.RelayOnly || m.PeerRelayOnly || m.RelayRequired || m.Phase != "fixed" || m.RelayRound != nil || m.RelayPairing != "" || m.RelayPolicy != "" || m.RuntimeID != c.runtimeID || m.RetryAfterMS < 0 || m.RetryAfterMS > 15000 || validateFixedAddress(m.FixedEndpoint, false) != nil {
		return bad()
	}
	id := cfg.DeviceName
	if !(m.ListenerID == id && m.DialerID == m.PeerDevice || m.DialerID == id && m.ListenerID == m.PeerDevice) || m.ListenerID == m.DialerID {
		return bad()
	}
	if m.ListenerID == id && (cfg.Transport.FixedEndpoint == nil || cfg.Transport.FixedEndpoint.Advertise != m.FixedEndpoint) {
		return bad()
	}
	keys := map[string]bool{}
	for _, record := range m.Mappings {
		if record.Key == "" || keys[record.ID] || !c.allowed(record) || !(record.Provider == id && record.Consumer == m.PeerDevice || record.Consumer == id && record.Provider == m.PeerDevice) {
			return bad()
		}
		keys[record.ID] = true
	}
	if len(m.Mappings) == 0 && !m.Voice {
		return bad()
	}
	return nil
}

func (p *icePeer) retireForFixedPolicy() {
	p.mu.Lock()
	if p.pending != nil {
		p.pending.cancel()
		p.pending = nil
	}
	active := p.active
	p.active = nil
	p.stats.RTTMS = 0
	p.online = false
	p.stats.State = "reconnecting"
	p.mu.Unlock()
	if active != nil {
		active.cancel()
		_ = active.session.CloseWithError(0, "fixed endpoint policy changed")
	}
}

func (p *icePeer) attemptFixed(a *iceAttempt) {
	defer a.cancel()
	defer func() {
		// routeFixed sends under p.mu and checks cancellation before delivery.
		a.cancel()
		p.mu.Lock()
		defer p.mu.Unlock()
		select {
		case incoming := <-a.incoming:
			_ = incoming.conn.CloseWithError(0, "attempt retired")
		default:
		}
	}()
	if a.ready.RetryAfterMS > 0 {
		timer := time.NewTimer(time.Duration(a.ready.RetryAfterMS) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-a.ctx.Done():
			return
		case <-timer.C:
		}
	}
	started := time.Now()
	hsCtx, cancel := context.WithTimeout(a.ctx, fixedAttemptTimeout)
	defer cancel()
	deadline, _ := hsCtx.Deadline()
	var conn *quic.Conn
	var incoming *fixedIncoming
	cleanup := func() {}
	defer func() { cleanup() }()
	if a.ready.DialerID == p.coordinator.deviceID() {
		platform, err := p.coordinator.pin(hsCtx)
		if err != nil {
			p.failed(a, err)
			return
		}
		address, err := platform.ResolveUDP(hsCtx, a.ready.FixedEndpoint)
		if err != nil {
			p.failed(a, err)
			return
		}
		var transport *quic.Transport
		// Reuse the fixed socket when the destination has the same IP family.
		p.coordinator.fixedMu.Lock()
		f := p.coordinator.fixed
		if f != nil {
			local := f.packet.LocalAddr().(*net.UDPAddr)
			if (local.IP.To4() != nil) == (address.IP.To4() != nil) {
				transport = f.transport
			}
		}
		p.coordinator.fixedMu.Unlock()
		if transport == nil {
			network, local := "udp4", "0.0.0.0:0"
			if address.IP.To4() == nil {
				network, local = "udp6", "[::]:0"
			}
			packet, err := platform.ListenPacket(hsCtx, network, local)
			if err != nil {
				p.failed(a, err)
				return
			}
			transport = &quic.Transport{Conn: packet}
			cleanup = func() { _ = transport.Close(); _ = packet.Close() }
		}
		tlsConfig := p.coordinator.identity.Clone()
		tlsConfig.NextProtos = []string{fixedALPN}
		tlsConfig.InsecureSkipVerify = true
		var identityMismatch atomic.Bool
		tlsConfig.VerifyConnection = func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) > 0 {
				sum := sha256.Sum256(state.PeerCertificates[0].Raw)
				if hex.EncodeToString(sum[:]) == a.ready.PeerFingerprint {
					return nil
				}
			}
			identityMismatch.Store(true)
			return &Fault{Code: "peer_identity_mismatch", Message: "固定入口证书指纹与协调服务不一致"}
		}
		conn, err = transport.Dial(hsCtx, address, tlsConfig, iceQUICConfig("direct"))
		if err != nil {
			if identityMismatch.Load() {
				err = &Fault{Code: "peer_identity_mismatch", Message: "固定入口证书指纹与协调服务不一致"}
			}
			p.failed(a, err)
			return
		}
	} else {
		select {
		case <-hsCtx.Done():
			p.failed(a, hsCtx.Err())
			return
		case err := <-a.failed:
			p.failed(a, err)
			return
		case incoming = <-a.incoming:
			conn = incoming.conn
		}
		if incoming.deadline.Before(deadline) {
			deadline = incoming.deadline
		}
	}
	defer conn.CloseWithError(0, "fixed path ended")
	datagrams := conn.ConnectionState().SupportsDatagrams
	if !datagrams.Local || !datagrams.Remote {
		p.failed(a, &Fault{Code: "protocol_mismatch", Message: "固定入口需要 QUIC DATAGRAM"})
		return
	}
	mux, err := newMuxPeerPrepared(a.ctx, p, conn, a.ready, incoming, deadline)
	if err != nil {
		p.failed(a, err)
		return
	}
	cleanup = sync.OnceFunc(cleanup)
	live := &liveICETransport{ready: a.ready, mux: mux, session: conn, cancel: a.cancel, fixedCleanup: cleanup}
	p.mu.Lock()
	if p.pending != a || p.closed || a.ctx.Err() != nil {
		p.mu.Unlock()
		live.close()
		return
	}
	old := p.active
	p.active, p.pending = live, nil
	p.stats.State, p.stats.Error = "active", nil
	p.stats.PathType, p.stats.LocalType, p.stats.RemoteType = "fixed", "fixed", "fixed"
	p.stats.LocalAddress, p.stats.RemoteAddress = conn.LocalAddr().String(), conn.RemoteAddr().String()
	p.stats.AddressFamily = "IPv4"
	if host, _, err := net.SplitHostPort(conn.RemoteAddr().String()); err == nil && net.ParseIP(host).To4() == nil {
		p.stats.AddressFamily = "IPv6"
	}
	p.stats.ConnectMS = time.Since(started).Milliseconds()
	p.stats.RelayState, p.stats.TURNExpiresAt = "", 0
	p.mu.Unlock()
	if a.ready.Voice {
		if voice := p.coordinator.voiceRuntimePtr(); voice != nil {
			voice.bindPeer(a.ready.PeerDevice, a.ready.TransportID, a.ready.TransportGeneration, mux)
		}
	}
	mux.syncMappings()
	if old != nil {
		old.close()
	}
	p.stateChanged()
	status := signalMessage("transport_established")
	status.TransportID, status.TransportGeneration = a.ready.TransportID, a.ready.TransportGeneration
	_ = p.coordinator.send(status)
	select {
	case <-mux.ctx.Done():
	case <-a.ctx.Done():
	case <-a.failed:
	}
	live.close()
	if a.ready.Voice {
		if voice := p.coordinator.voiceRuntimePtr(); voice != nil {
			voice.unbindPeer(a.ready.PeerDevice, mux)
		}
	}
	p.mu.Lock()
	current := p.active == live
	if current {
		p.active = nil
		p.stats.State = "reconnecting"
		p.stats.RTTMS = 0
		p.stats.Error = &Fault{Code: "fixed_path_failed", Message: "固定入口连接已断开，正在重试"}
	}
	p.mu.Unlock()
	if current && p.ctx.Err() == nil {
		p.stateChanged()
		p.restart("fixed", "path_lost")
	}
}
