package core

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"github.com/gorilla/websocket"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type iceSignalSink struct {
	ctx      context.Context
	messages chan iceSignalMessage
}
type iceCoordinator struct {
	room                     *roomControl
	pendingSignals           map[string]iceSignalMessage
	peerRelayMu              sync.Mutex
	peerRelayGrants          map[sessionID]peerRelayGrant
	peerRelayMarks           map[string]uint64
	peerRelayPaths           map[sessionID]*peerRelayPath
	natAllocation            *natAllocationGate
	natMu                    sync.Mutex
	natSnapshot              NATSnapshot
	natConnected             bool
	natProbeRequested        bool
	natProbeRunning          bool
	natProbeWake             chan struct{}
	natProbeProcess          *natProcessRecorder
	natProbeEpoch            uint64
	natCancel                context.CancelFunc
	ctx                      context.Context
	cancel                   context.CancelFunc
	platform                 Platform
	sessions                 *sessionManager
	identity                 *tls.Config
	runtimeID                string
	fingerprint              string
	emit                     func(Event)
	networkObserved          func(NetworkSnapshot)
	configMu                 sync.RWMutex
	desired                  Request
	acceptedConfig           Request
	configPending            bool
	initial                  Request
	networkEpoch             atomic.Uint64
	reset                    chan struct{}
	updates                  chan struct{}
	mu                       sync.Mutex
	peers                    map[string]*icePeer
	sink                     *iceSignalSink
	online                   bool
	serverV2                 bool
	leaseRenewal             bool
	signalResume             bool
	turnMu                   sync.Mutex
	turnServers              []ICEServer
	turnExpires, turnRefresh int64
	turnState                string
	turnRequestID            string
	turnLast                 time.Time
	turnRetry                time.Time
	turnBackoff              time.Duration
	turnChanged              chan struct{}
	voiceMu                  sync.RWMutex
	voiceMembers             map[string]voiceRoomMember
	voice                    *voiceRuntime
	fixedMu                  sync.Mutex
	fixed                    *fixedListener
}

func (c *iceCoordinator) voiceRuntimePtr() *voiceRuntime {
	c.voiceMu.RLock()
	defer c.voiceMu.RUnlock()
	return c.voice
}

func newICECoordinator(ctx context.Context, r Request, platform Platform, sessions *sessionManager, identity *tls.Config, runtimeID string, epoch uint64, emit func(Event), networkObserved func(NetworkSnapshot)) *iceCoordinator {
	ctx, cancel := context.WithCancel(ctx)
	c := &iceCoordinator{ctx: ctx, cancel: cancel, platform: platform, sessions: sessions, identity: identity, runtimeID: runtimeID, fingerprint: certFingerprint(identity.Certificates[0]), emit: emit, networkObserved: networkObserved, desired: r, reset: make(chan struct{}, 1), updates: make(chan struct{}, 1), peers: map[string]*icePeer{}, turnState: "pending", turnChanged: make(chan struct{}), voiceMembers: map[string]voiceRoomMember{}, natProbeWake: make(chan struct{}, 1)}
	if r.Config.Voice.Enabled {
		c.voice = newConfiguredVoiceRuntime(ctx)
	}
	c.networkEpoch.Store(epoch)
	c.initial = r
	c.natSnapshot = natUnknown()
	c.natAllocation = &natAllocationGate{}
	c.room = newRoomControl(c)
	c.pendingSignals = map[string]iceSignalMessage{}
	return c
}
func (c *iceCoordinator) request() Request {
	c.configMu.RLock()
	defer c.configMu.RUnlock()
	return c.desired
}
func (c *iceCoordinator) deviceID() string   { return c.request().Config.DeviceName }
func (c *iceCoordinator) signalOnline() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.online }
func (c *iceCoordinator) submit(r Request) {
	c.configMu.RLock()
	previous := c.desired
	c.configMu.RUnlock()
	if !reflect.DeepEqual(previous.Config.Transport.FixedEndpoint, r.Config.Transport.FixedEndpoint) {
		c.room.wakeExistingPeers()
	}
	c.configMu.Lock()
	old := c.desired
	pathChanged := pathConfigChanged(old.Config, r.Config)
	if pathChanged {
		c.networkEpoch.Add(1)
	}
	c.desired = r
	c.configPending = true
	c.configMu.Unlock()
	if c.room != nil && !sameSignalingConfig(old, r) {
		c.room.requireSignal(false)
	}
	if !reflect.DeepEqual(old.Config.Transport.FixedEndpoint, r.Config.Transport.FixedEndpoint) {
		for _, p := range c.peerList() {
			p.retireForFixedPolicy()
		}
	}
	if old.Config.Voice != r.Config.Voice {
		c.voiceMu.Lock()
		previous := c.voice
		if r.Config.Voice.Enabled {
			c.voice = newConfiguredVoiceRuntime(c.ctx)
		} else {
			c.voice = nil
		}
		c.voiceMu.Unlock()
		if previous != nil {
			previous.close()
		}
	}
	if r.Config.ICE.RelayOnly && !old.Config.ICE.RelayOnly {
		// Stop old direct data paths immediately, even while signaling is offline.
		for _, p := range c.peerList() {
			p.requireRelay(true)
		}
	}
	if pathChanged {
		c.natMu.Lock()
		if c.natCancel != nil {
			c.natCancel()
		}
		c.natSnapshot = natUnknown()
		c.natConnected = false
		c.natProbeRequested = false
		c.natMu.Unlock()
		c.turnMu.Lock()
		c.turnServers = nil
		c.turnLast = time.Time{}
		c.turnExpires = 0
		c.turnRefresh = 0
		c.turnRetry = time.Time{}
		c.turnBackoff = 0
		c.turnState = "pending"
		c.turnRequestID = ""
		c.notifyTURNLocked()
		c.turnMu.Unlock()
	}
	select {
	case c.updates <- struct{}{}:
	default:
	}
}
func (c *iceCoordinator) networkChanged() {
	c.networkEpoch.Add(1)
	if c.room != nil {
		c.room.requireSignal(false)
	}
	c.natMu.Lock()
	if c.natCancel != nil {
		c.natCancel()
	}
	c.natSnapshot = natUnknown()
	c.natConnected = false
	c.natProbeRequested = false
	c.natMu.Unlock()
	select {
	case c.reset <- struct{}{}:
	default:
	}
}
func (c *iceCoordinator) pin(ctx context.Context) (Platform, error) {
	if source, ok := c.platform.(NetworkSnapshotter); ok {
		p, n, e := source.SnapshotNetwork(ctx)
		c.networkObserved(n)
		return p, e
	}
	return c.platform, nil
}

func (m iceSignalMessage) wakesSignal() bool {
	switch m.Type {
	case "room_graph", "transport_established", "peer_relay_failed", "nat_status":
		// Losing the signaling socket must not turn routine reports or relay
		// cleanup into a request for a new direct connection.
		return false
	default:
		return true
	}
}

func (c *iceCoordinator) send(m iceSignalMessage) error {
	c.mu.Lock()
	sink := c.sink
	c.mu.Unlock()
	if sink == nil {
		if c.room != nil && c.room.enabled() {
			if m.Type == "transport_restart" && (m.Reason == "path_lost" || m.Reason == "retry" || m.Reason == "peer_relay_failed") {
				c.mu.Lock()
				peer := c.peers[m.TransportID]
				c.mu.Unlock()
				if peer == nil {
					return errors.New("signal disconnected")
				}
				peer.mu.Lock()
				ready := peer.ready
				stale := peer.closed || !peer.online || ready.TransportGeneration != m.ExpectedGeneration
				peer.mu.Unlock()
				if stale || c.room.deferPeerRecovery(ready) {
					return errors.New("signal disconnected")
				}
			}
			queued := false
			switch m.Type {
			case "transport_restart", "transport_established", "transport_failed", "hole_failed", "peer_relay_failed", "nat_status":
				c.mu.Lock()
				if c.pendingSignals == nil {
					c.pendingSignals = map[string]iceSignalMessage{}
				}
				key := m.Type + "/" + m.TransportID
				if _, exists := c.pendingSignals[key]; exists || len(c.pendingSignals) < 128 {
					c.pendingSignals[key] = m
					queued = true
				}
				c.mu.Unlock()
			}
			if m.wakesSignal() {
				c.room.requireSignal(false)
			} else if queued {
				return nil
			}
		}
		return errors.New("signal disconnected")
	}
	if m.RequestID == "" {
		id, _ := newSessionID()
		m.RequestID = id.String()
	}
	select {
	case <-sink.ctx.Done():
		return sink.ctx.Err()
	case sink.messages <- m:
		return nil
	default:
		return &Fault{Code: "signal_backpressure", Message: "协调消息暂时拥塞，正在恢复连接"}
	}
}
func (c *iceCoordinator) run() error {
	roomDone := make(chan struct{})
	if c.room != nil {
		go func() { defer close(roomDone); c.room.run(c.ctx) }()
	} else {
		close(roomDone)
	}
	defer func() { c.cancel(); <-roomDone }()
	natDone := make(chan struct{})
	go func() { defer close(natDone); c.monitorNAT() }()
	defer func() { c.cancel(); <-natDone }()
	monitorDone := make(chan struct{})
	if _, bound := c.platform.(NetworkSnapshotter); bound {
		close(monitorDone)
	} else {
		go func() { defer close(monitorDone); c.watchNetwork() }()
	}
	defer func() { c.cancel(); <-monitorDone; c.close() }()
	last := c.initial
	backoff := time.Second
	soloRecovery := false
	for c.ctx.Err() == nil {
		c.configMu.RLock()
		current, epoch := c.desired, c.networkEpoch.Load()
		c.configMu.RUnlock()
		if err := c.applyLocal(&last, current); err != nil {
			return err
		}
		select {
		case <-c.reset:
			// A reset may arrive just after configureFixed inspected the current
			// listener. Re-enter the loop so a fatal fixed relay is rebuilt before
			// the next signaling connection starts.
			continue
		default:
		}
		if c.room != nil && c.room.enabled() && c.room.waitForSignal() && !c.signalNeeded() {
			c.configApplied(current)
			c.emit(Event{Kind: "signal", State: "delegated"})
			select {
			case <-c.ctx.Done():
				return c.ctx.Err()
			case <-c.room.wake:
			case <-c.room.changed:
			case <-c.updates:
			case <-c.reset:
			}
			continue
		}
		started := time.Now()
		err, changed := c.connect(current, &last, epoch)
		if c.ctx.Err() != nil {
			return c.ctx.Err()
		}
		c.mu.Lock()
		hadOnline := c.online
		c.online = false
		resume := c.signalResume
		c.sink = nil
		c.mu.Unlock()
		if c.room != nil {
			c.room.signalClosed(hadOnline && !c.room.hasSignalVia())
		}
		c.fixedMu.Lock()
		if !resume && c.fixed != nil && c.fixed.relay != nil {
			c.fixed.relay.revokeAll()
		}
		c.fixedMu.Unlock()
		for _, peer := range c.peerList() {
			peer.mu.Lock()
			// A queued restart may have been lost with the old WebSocket.
			// Keep healthy media paths, but let the next replay request recovery.
			peer.requestedGeneration = 0
			pending := peer.pending
			if pending != nil && c.room != nil && c.room.enabled() {
				peer.pending = nil
				pending.cancel()
			}
			retire := !resume && (peer.ready.Voice || peer.ready.HoleVersion == HoleRelayVersion)
			peer.mu.Unlock()
			if pending != nil && c.room != nil && c.room.enabled() {
				peer.restartFrom(pending.ready, pending.ready.Phase, "signal_interrupted")
			}
			if retire {
				peer.offline("signal_offline")
			}
		}
		c.turnMu.Lock()
		c.turnRequestID = ""
		if c.turnState == "requesting" {
			c.turnState = "pending"
			c.turnLast = time.Time{}
		}
		c.notifyTURNLocked()
		c.turnMu.Unlock()
		if changed {
			backoff = time.Second
			continue
		}
		// Report the original cause even when room delegation or an immediate
		// recovery attempt skips the backoff below. Planned changes stay quiet.
		fault := classifyError(err)
		fault.Message = redact(fault.Message, current.Config)
		c.emit(Event{Kind: "signal", State: "reconnecting", Error: fault})
		if c.room != nil && c.room.enabled() {
			if c.room.hasSignalVia() && !c.signalNeeded() {
				soloRecovery = false
				continue
			}
			if hadOnline {
				soloRecovery = true
				continue
			}
			if soloRecovery {
				c.room.recoverTogether()
				soloRecovery = false
			}
		}
		if time.Since(started) > 30*time.Second {
			backoff = time.Second
		}
		timer := time.NewTimer(backoff)
		select {
		case <-c.ctx.Done():
			timer.Stop()
			return c.ctx.Err()
		case <-c.reset:
			timer.Stop()
		case <-c.updates:
			timer.Stop()
		case <-timer.C:
		}
		backoff = min(current.Config.ICE.RetryMaxDelay.Duration(), backoff*2)
	}
	return c.ctx.Err()
}
func (c *iceCoordinator) connect(r Request, applied *Request, epoch uint64) (error, bool) {
	ctx, cancel := context.WithCancel(c.ctx)
	defer cancel()
	platform, err := c.pin(ctx)
	if err != nil {
		return err, false
	}
	u, err := url.Parse(r.ServerURL)
	if err != nil {
		return err, false
	}
	q := u.Query()
	q.Set("room", r.Config.Room)
	u.RawQuery = q.Encode()
	dialer := *websocket.DefaultDialer
	dialer.NetDialContext = platform.DialSignal
	dialer.HandshakeTimeout = 10 * time.Second
	if roots := loadTrustRoots(); roots != nil {
		dialer.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	}
	c.emit(Event{Kind: "signal", State: "connecting"})
	conn, response, err := dialer.DialContext(ctx, u.String(), http.Header{"Authorization": []string{"Bearer " + r.Config.Password}})
	if err != nil {
		if response != nil {
			if response.Body != nil {
				response.Body.Close()
			}
			if response.StatusCode == 401 || response.StatusCode == 403 {
				return &Fault{Code: "signal_auth_failed", Message: "协调服务密码不匹配"}, false
			}
		}
		return err, false
	}
	defer conn.Close()
	conn.SetReadLimit(256 * 1024)
	_ = conn.SetReadDeadline(time.Now().Add(signalPongWait))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(signalPongWait)) })
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	join := c.memberDeclaration(ctx, platform, r)
	join.TransportEpoch = epoch
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err = conn.WriteJSON(join); err != nil {
		return err, false
	}
	return c.serveSignal(ctx, cancel, conn, platform, r, applied)
}

func (c *iceCoordinator) memberDeclaration(ctx context.Context, platform Platform, r Request) iceSignalMessage {
	profiles := []string{ProfileICE, ProfileFixed}
	relayEnabled := r.Config.TURN.Mode != "off"
	order := relayOrderForConfig(RelayPolicyUDPTCPTLS, r.Config.TURN)
	// Older round-capable Workers require a present relay_order when disabled.
	if !relayEnabled {
		order = relayOrder(RelayPolicyUDPTCPTLS, nil)
	}
	join := iceSignalMessage{SignalMessage: SignalMessage{Type: "join", Room: r.Config.Room, Token: r.Config.Token, DeviceID: r.Config.DeviceName, DeviceName: r.Config.DeviceName, Provide: r.Config.Provide, Consume: r.Config.Consume, CertFingerprint: c.fingerprint},
		SignalVersion: 2, AuthMode: "shared-secret", RuntimeID: c.runtimeID, TransportEpoch: c.networkEpoch.Load(), TransportProfiles: profiles, SessionVersions: []int{sessionProtocol}, RelayPolicy: RelayPolicyUDPTCPTLS,
		RelayEnabled: &relayEnabled, RelayOrder: order, RelayPairing: relayPairingAll, RelayOnly: r.Config.ICE.RelayOnly}
	join.Voice = r.Config.Voice.Enabled
	join.VoiceGroup = r.Config.Voice.Group
	join.CoreVersion = CoreVersion
	join.Certificate = base64.StdEncoding.EncodeToString(c.identity.Certificates[0].Certificate[0])
	if c.room != nil {
		c.room.mu.Lock()
		join.WorkerVersion = c.room.workerVersion
		c.room.mu.Unlock()
	}
	join.LiveConfig = true
	join.PeerRelayVersion = peerRelayVersion
	join.PathUpgrade = true
	join.PunchVersion = natPunchVersion
	join.DirectOrder = directOrderICEFirst
	join.PunchSchedule = natPunchSchedule
	join.PunchCalibration = natPunchCalibration
	join.PunchConePool = natPunchConePool
	join.HoleVersion = HoleRelayVersion
	join.HoleDevice = r.Config.Relay.Device
	// Keep the legacy relay-disabled wire contract unless Hole is requested.
	if r.Config.holeConfigured() || r.Config.Relay.Order != nil {
		join.RelayOrder = r.Config.deviceRelayOrder(RelayPolicyUDPTCPTLS)
	}
	if r.Config.Relay.Serve.Enabled {
		serve := r.Config.Relay.Serve
		serve.MaxPairs = r.Config.relayServeLimit()
		join.HoleServe = &serve
	}
	if endpoint := r.Config.Transport.FixedEndpoint; endpoint != nil {
		join.FixedEndpoint = endpoint.Advertise
	}
	return join
}

func (c *iceCoordinator) serveSignal(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, platform Platform, r Request, applied *Request) (error, bool) {
	sink := &iceSignalSink{ctx: ctx, messages: make(chan iceSignalMessage, 256)}
	c.mu.Lock()
	c.sink = sink
	c.mu.Unlock()
	c.emit(Event{Kind: "signal", State: "joining"})
	results := make(chan error, 2)
	incoming := make(chan iceSignalMessage, 64)
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		for {
			var message iceSignalMessage
			err := conn.ReadJSON(&message)
			if err != nil {
				results <- err
				return
			}
			if ctx.Err() != nil {
				return
			}
			select {
			case incoming <- message:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		defer workers.Done()
		ticker := time.NewTicker(25 * time.Second)
		defer ticker.Stop()
		leases := time.NewTicker(5 * time.Minute)
		defer leases.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case m := <-sink.messages:
				_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if e := conn.WriteJSON(m); e != nil {
					results <- e
					return
				}
			case <-ticker.C:
				if e := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); e != nil {
					results <- e
					return
				}
				// Protocol Ping is handled by the edge without waking the DO.
				c.requestTURN()
			case <-leases.C:
				message, needed := c.leaseRequest()
				if !needed {
					continue
				}
				_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if e := conn.WriteJSON(message); e != nil {
					results <- e
					return
				}
			}
		}
	}()
	err, changed := c.signalUpdates(ctx, incoming, results, platform, r, applied)
	cancel()
	conn.Close()
	workers.Wait()
	return err, changed
}
func (c *iceCoordinator) handle(m iceSignalMessage, sessionRequest Request, platform Platform) error {
	switch m.Type {
	case "joined":
		if c.room != nil {
			if err := c.room.joined(m); err != nil {
				return err
			}
		}
		if sessionRequest.Config.holeConfigured() && m.HoleVersion != HoleRelayVersion {
			return &Fault{Code: "worker_upgrade_required", Message: "Hole 中继需要先更新 Worker"}
		}
		if m.SignalVersion < 2 {
			return &Fault{Code: "worker_upgrade_required", Message: "协调服务尚未支持 ICE，请先更新 Worker"}
		}
		if sessionRequest.Config.ICE.RelayOnly && m.RelayOnlyPolicy != relayOnlyPolicy {
			return &Fault{Code: "worker_upgrade_required", Message: "仅中继模式需要更新 Worker 以协商双方连接策略"}
		}
		c.mu.Lock()
		c.online = true
		c.serverV2 = m.SignalVersion >= 2
		c.leaseRenewal = m.LeaseRenewal
		c.signalResume = m.SignalResume
		c.mu.Unlock()
		if c.room != nil {
			c.room.uploadGraph()
		}
		c.emit(Event{Kind: "signal", State: "joined"})
		if m.SignalVersion >= 2 {
			// Full state is requested once on join/rejoin, never as a heartbeat.
			_ = c.send(signalMessage("transport_sync"))
		}
		c.mu.Lock()
		pending := c.pendingSignals
		c.pendingSignals = map[string]iceSignalMessage{}
		c.mu.Unlock()
		for _, message := range pending {
			_ = c.send(message)
		}
	case "transport_synced":
		if c.room != nil {
			c.room.synchronized()
		}
	case "room_frame":
		if c.room != nil && m.RoomFrame != nil {
			c.room.accept(*m.RoomFrame, nil)
		}
	case "room_state":
		if err := c.updateVoiceMembers(m.RoomState); err != nil {
			return &Fault{Code: "protocol_mismatch", Message: err.Error()}
		}
	case "peer_relay_grant", "peer_relay_revoke":
		return c.handlePeerRelayGrant(m)
	case "hole_grant", "hole_revoke":
		return c.handleHoleGrant(m)
	case "transport_lease":
		for _, lease := range m.Leases {
			c.mu.Lock()
			p := c.peers[lease.TransportID]
			c.mu.Unlock()
			if p != nil {
				p.renewLease(lease)
			}
		}
	case "transport_ready":
		if !sameSignalingConfig(sessionRequest, c.request()) {
			return nil
		}
		cfg := c.request().Config
		if (m.UpgradeProbe || m.UpgradeIdle || m.UpgradeCommit) && (!m.PathUpgrade || !m.localAuthorization() || m.Profile != ProfileICE || m.RelayRequired) {
			return &Fault{Code: "protocol_mismatch", Message: "后台换路能力或路径策略无效"}
		}
		if m.Phase == "peer_relay" {
			if err := c.validatePeerRelayReady(m); err != nil {
				return err
			}
		}
		if m.HoleVersion != "" || (cfg.Relay.Device != "" && m.Profile != ProfileFixed) {
			if err := c.validateHoleReady(m); err != nil {
				return err
			}
		}
		if m.Profile == ProfileFixed {
			if err := c.validateFixedReady(m); err != nil {
				return err
			}
		} else {
			if cfg.Transport.FixedEndpoint != nil {
				return &Fault{Code: "protocol_mismatch", Message: "已启用固定入口，协调服务不能选择其他连接方式"}
			}
			if cfg.ICE.RelayOnly && m.RelayOnlyPolicy != relayOnlyPolicy {
				return &Fault{Code: "worker_upgrade_required", Message: "仅中继模式需要更新 Worker 以协商双方连接策略"}
			}
			if m.RelayOnlyPolicy != "" && (m.RelayOnlyPolicy != relayOnlyPolicy || m.RelayOnly != cfg.ICE.RelayOnly || m.RelayRequired != (m.RelayOnly || m.PeerRelayOnly)) {
				return &Fault{Code: "protocol_mismatch", Message: "协调服务返回的仅中继约束与双方策略不一致"}
			}
			if m.RelayRequired && (m.Phase == "direct" || (m.RelayRound != nil && *m.RelayRound == 0)) {
				return &Fault{Code: "protocol_mismatch", Message: "仅中继连接不能进入直连轮次"}
			}
			if m.RelayPairing != "" && m.RelayPairing != relayPairingAll {
				return &Fault{Code: "protocol_mismatch", Message: "协调服务返回了不支持的中继配对策略"}
			}
			if m.DirectOrder != "" && (m.DirectOrder != directOrderICEFirst || m.PunchVersion != natPunchVersion || (m.DirectStage != "ice" && m.DirectStage != "punch") || m.RelayRequired) {
				return &Fault{Code: "protocol_mismatch", Message: "协调服务返回了无效的直连尝试顺序"}
			}
			if m.PunchCalibration != "" && (m.PunchCalibration != natPunchCalibration || m.PunchSchedule != natPunchSchedule || m.DirectOrder != directOrderICEFirst) {
				return &Fault{Code: "protocol_mismatch", Message: "协调服务返回了无效的打洞校准能力"}
			}
			if m.PunchConePool != "" && (m.PunchConePool != natPunchConePool || m.PunchCalibration != natPunchCalibration) {
				return &Fault{Code: "protocol_mismatch", Message: "协调服务返回了无效的锥形地址池能力"}
			}
			if m.RelayPolicy != "" && m.RelayPolicy != RelayPolicyUDPTCPTLS {
				return &Fault{Code: "protocol_mismatch", Message: "协调服务返回了不受支持的中继策略"}
			}
			if m.Phase == "peer_relay" {
				// The relay round is held while the optional peer path is tried.
			} else if m.HoleVersion == HoleRelayVersion {
				if m.RelayRound == nil || plannedPhase(m, *m.RelayRound, cfg.DeviceName) != m.Phase {
					return &Fault{Code: "protocol_mismatch", Message: "Hole 路径轮次无效"}
				}
			} else if m.RelayPairing == relayPairingAll {
				if m.RelayRound == nil || !validRelayOrder(m.RelayOrder) || !validRelayOrder(m.PeerRelayOrder) ||
					m.Phase == "" || m.Phase != pairedRelayPhase(*m.RelayRound, m.RelayOrder, m.PeerRelayOrder, cfg.DeviceName < m.PeerDevice) {
					return &Fault{Code: "protocol_mismatch", Message: "协调服务返回的中继配对轮次无效"}
				}
			} else if m.RelayRound != nil {
				if !validRelayRound(*m.RelayRound, m.Phase, m.RelayOrder, m.PeerRelayOrder) {
					return &Fault{Code: "protocol_mismatch", Message: "协调服务返回的中继轮次或本机路径阶段无效"}
				}
			} else if !validRelayPhase(m.RelayPolicy, m.Phase) {
				return &Fault{Code: "protocol_mismatch", Message: "协调服务返回的中继策略或路径阶段不受支持"}
			}
			if len(m.RelayOrder) > 6 || len(m.PeerRelayOrder) > 6 {
				return &Fault{Code: "protocol_mismatch", Message: "协调服务返回的中继顺序超出限制"}
			}
			if m.RelayRound == nil {
				// Old workers and clients communicate named phases. Their shared
				// policy remains authoritative, so custom local orders are ignored.
				fallbackOrder := relayOrder(m.RelayPolicy, nil)
				m.RelayOrder = fallbackOrder
				m.PeerRelayOrder = slices.Clone(fallbackOrder)
			} else {
				localOrder := cfg.deviceRelayOrder(m.RelayPolicy)
				if !slices.Equal(localOrder, m.RelayOrder) {
					return &Fault{Code: "protocol_mismatch", Message: "协调服务返回的本机中继顺序与配置不一致"}
				}
			}
		}
		if (m.Profile != ProfileICE && m.Profile != ProfileFixed) || m.SessionVersion != sessionProtocol || m.TransportID == "" || m.TransportGeneration == 0 || m.PeerRuntimeID == "" || len(m.PeerFingerprint) != 64 || m.PeerDevice == "" || len(m.Mappings) > maxPeerChannels {
			return &Fault{Code: "protocol_mismatch", Message: "协调服务返回的 ICE 能力或传输信息无效"}
		}
		if m.Voice && !cfg.Voice.Enabled {
			return &Fault{Code: "protocol_mismatch", Message: "未启用语音的配置收到语音传输"}
		}
		if m.LiveConfig && m.VoiceRevision == 0 {
			return &Fault{Code: "protocol_mismatch", Message: "在线语音授权缺少修订号"}
		}
		expected := c.deviceID()
		if m.PeerDevice < expected {
			expected = m.PeerDevice
		}
		if m.Profile == ProfileFixed {
			expected = m.DialerID
		}
		if m.InitiatorID != expected {
			return &Fault{Code: "protocol_mismatch", Message: "双方连接角色不一致"}
		}
		// Offline records can legitimately expire. A replacement transport ID
		// for the same device must retire the old coordinator entry, not leak a
		// peer slot on every long offline/rejoin cycle.
		for _, old := range c.peerList() {
			if old.peerID() != m.PeerDevice {
				continue
			}
			c.mu.Lock()
			retired := false
			for id, current := range c.peers {
				if current == old && id != m.TransportID {
					delete(c.peers, id)
					retired = true
				}
			}
			c.mu.Unlock()
			if retired {
				old.close()
			}
		}
		c.mu.Lock()
		p := c.peers[m.TransportID]
		if p == nil && m.RoomControl {
			c.mu.Unlock()
			return nil
		}
		if p == nil {
			if len(c.peers) >= maxPeerTransports {
				c.mu.Unlock()
				return &Fault{Code: "transport_limit", Message: "对端连接数量达到上限"}
			}
			p = newICEPeer(c, m)
			c.peers[m.TransportID] = p
		}
		c.mu.Unlock()
		p.update(m)
		if m.Profile != ProfileFixed {
			c.requestTURN()
		}
	case "ice_description", "ice_candidate", "nat_status", "nat_punch":
		c.mu.Lock()
		p := c.peers[m.TransportID]
		c.mu.Unlock()
		if p != nil {
			p.receive(m)
		}
	case "mapping_ready", "peer_candidates", "mapping_closed":
		return &Fault{Code: "protocol_mismatch", Message: "旧 IPv6 传输协议已移除，请更新对端和 Worker"}
	case "peer_state":
		if c.room != nil && c.room.enabled() && m.Reason == "peer_offline" {
			break
		}
		for _, p := range c.peerList() {
			p.mu.Lock()
			matches := p.ready.PeerDevice == m.PeerDevice && (m.TransportID == "" || p.ready.TransportID == m.TransportID) && (m.TransportGeneration == 0 || p.ready.TransportGeneration <= m.TransportGeneration)
			p.mu.Unlock()
			if matches {
				if m.Reason == "mapping_removed" || m.Reason == "revoked" {
					c.mu.Lock()
					for id, current := range c.peers {
						if current == p {
							delete(c.peers, id)
						}
					}
					c.mu.Unlock()
					p.close()
					p.offline(m.Reason)
				} else {
					p.offline(m.Reason)
				}
			}
		}
	case "turn_servers":
		accepted := false
		c.turnMu.Lock()
		if m.RequestID != "" && m.RequestID == c.turnRequestID {
			accepted = true
			c.turnServers = m.ICEServers
			c.turnExpires = m.ExpiresAt
			c.turnRefresh = m.RefreshAt
			c.turnState = "ready"
			c.turnRequestID = ""
			c.turnRetry = time.Time{}
			c.turnBackoff = 0
			c.notifyTURNLocked()
		}
		c.turnMu.Unlock()
		if accepted {
			c.emit(Event{Kind: "turn", State: "ready"})
		}
	case "error":
		// Complete every failed credential request, not just selected error
		// codes. Otherwise peer removal can leave requesting stuck forever.
		if c.failTURNRequest(m) {
			return nil
		}
		if m.Code == "punch_calibration_rejected" {
			c.mu.Lock()
			p := c.peers[m.TransportID]
			c.mu.Unlock()
			if p != nil {
				p.mu.Lock()
				a := p.pending
				p.mu.Unlock()
				if a != nil && a.ready.TransportGeneration == m.TransportGeneration && m.Punch != nil {
					// Keep refusal evidence in the trace. A delayed error for an
					// earlier round must not abort a newer calibration via a.failed.
					a.process.record(NATPunchProcessEvent{Kind: "signal_error", Title: "Worker 未转发校准结果", Direction: "out", Packet: m.Punch.Kind,
						Round: m.Punch.Round, Trial: m.Punch.Trial, Validation: "rejected", Detail: m.Message})
				}
			}
			return nil
		}
		if m.TransportID != "" && m.Code != "stale_generation" {
			c.mu.Lock()
			p := c.peers[m.TransportID]
			c.mu.Unlock()
			if p == nil {
				return nil
			} // queued old-generation messages after last mapping removal
			p.mu.Lock()
			pending := p.pending
			p.stats.Error = &Fault{Code: m.Code, Message: m.Message}
			p.mu.Unlock()
			if pending != nil {
				select {
				case pending.failed <- &Fault{Code: m.Code, Message: m.Message}:
				default:
				}
			}
			return nil
		}
		if m.Code == "stale_generation" {
			_ = c.send(iceSignalMessage{SignalMessage: SignalMessage{Type: "transport_sync"}})
			return nil
		}
		if m.MappingID != "" {
			c.emit(Event{Kind: "mapping", MappingID: m.MappingID, State: "error", Error: &Fault{Code: m.Code, Message: m.Message}})
		} else {
			c.emit(Event{Kind: "signal", State: "error", Error: &Fault{Code: m.Code, Message: m.Message}})
		}
	}
	return nil
}
func (c *iceCoordinator) allowed(r peerMappingRecord) bool {
	cfg := c.request().Config
	id := cfg.DeviceName
	if r.Provider == id {
		for _, p := range cfg.Provide {
			if p.ID == r.ID && p.Service == r.Service {
				return true
			}
		}
	}
	if r.Consumer == id {
		for _, p := range cfg.Consume {
			if p.ID == r.ID && p.Expose == r.Expose {
				return true
			}
		}
	}
	return false
}
func (c *iceCoordinator) peerList() []*icePeer {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]*icePeer, 0, len(c.peers))
	for _, p := range c.peers {
		result = append(result, p)
	}
	return result
}

func (c *iceCoordinator) renominate() {
	for _, p := range c.peerList() {
		p.renominate()
	}
}
func (c *iceCoordinator) snapshot() []PeerTransportSnapshot {
	result := []PeerTransportSnapshot{}
	for _, p := range c.peerList() {
		result = append(result, p.snapshot())
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].PeerID != result[j].PeerID {
			return result[i].PeerID < result[j].PeerID
		}
		return result[i].TransportID < result[j].TransportID
	})
	return result
}

func (c *iceCoordinator) updateVoiceMembers(members []voiceRoomMember) error {
	seen := make(map[string]bool, len(members))
	for _, member := range members {
		if member.DeviceName == "" || len(member.DeviceName) > 128 || seen[member.DeviceName] {
			return errors.New("room_state 成员无效")
		}
		seen[member.DeviceName] = true
	}
	c.voiceMu.Lock()
	c.voiceMembers = make(map[string]voiceRoomMember, len(members))
	for _, member := range members {
		if member.Voice {
			c.voiceMembers[member.DeviceName] = member
		}
	}
	c.voiceMu.Unlock()
	c.emit(Event{Kind: "room", VoiceState: "changed"})
	return nil
}

func (c *iceCoordinator) voiceSnapshot() VoiceSnapshot {
	cfg := c.request().Config
	snapshot := VoiceSnapshot{Enabled: cfg.Voice.Enabled, State: "disabled", Members: []VoiceMemberSnapshot{}, Peers: []VoicePeerSnapshot{}}
	if !cfg.Voice.Enabled {
		return snapshot
	}
	snapshot.State = "waiting"
	runtimePeers := map[string]VoicePeerSnapshot{}
	if voice := c.voiceRuntimePtr(); voice != nil {
		snapshot.Muted = voice.muted.Load()
		snapshot.CaptureState = voice.captureState()
		snapshot.CapturedFrames = voice.captured.Load()
		snapshot.MixedFrames = voice.mixed.Load()
		for _, peer := range voice.snapshot() {
			runtimePeers[peer.PeerID] = peer
		}
	}
	c.voiceMu.RLock()
	signalState := "online"
	if !c.signalOnline() && (c.room == nil || !c.room.hasSignalVia()) {
		signalState = "offline"
	}
	for _, member := range c.voiceMembers {
		value := VoiceMemberSnapshot{DeviceName: member.DeviceName, Voice: true, SignalState: signalState, TransportState: "offline", MediaState: "offline"}
		if member.DeviceName == cfg.DeviceName {
			value.Local = true
			value.TransportState = "local"
			value.MediaState = snapshot.CaptureState
		}
		snapshot.Members = append(snapshot.Members, value)
	}
	c.voiceMu.RUnlock()
	for _, peer := range c.peerList() {
		stats := peer.snapshot()
		if !stats.Voice {
			continue
		}
		voicePeer := runtimePeers[stats.PeerID]
		if voicePeer.TransportID != stats.TransportID || voicePeer.Generation != stats.Generation {
			voicePeer = VoicePeerSnapshot{MediaState: "waiting"}
		}
		voicePeer.PeerID = stats.PeerID
		voicePeer.TransportID = stats.TransportID
		voicePeer.Generation = stats.Generation
		voicePeer.State = stats.State
		voicePeer.Path = stats.PathType
		voicePeer.RTTMS = stats.RTTMS
		snapshot.Peers = append(snapshot.Peers, voicePeer)
		for i := range snapshot.Members {
			if snapshot.Members[i].DeviceName == stats.PeerID {
				snapshot.Members[i].TransportID = stats.TransportID
				snapshot.Members[i].TransportGen = stats.Generation
				snapshot.Members[i].TransportState = stats.State
				if stats.State == "active" {
					snapshot.Members[i].MediaState = voicePeer.MediaState
				}
			}
		}
	}
	for _, peer := range snapshot.Peers {
		if peer.State != "active" {
			if snapshot.State == "waiting" {
				snapshot.State = "connecting"
			}
			continue
		}
		if peer.MediaState == "active" || peer.MediaState == "sending" || peer.MediaState == "receiving" {
			snapshot.State = "active"
			break
		}
		snapshot.State = "ready"
	}
	sortVoiceSnapshot(&snapshot)
	return snapshot
}
func (c *iceCoordinator) close() {
	c.cancel()
	c.mu.Lock()
	c.online = false
	c.sink = nil
	c.mu.Unlock()
	for _, p := range c.peerList() {
		p.close()
	}
	c.closeFixed()
	if voice := c.voiceRuntimePtr(); voice != nil {
		voice.close()
	}
}
func (c *iceCoordinator) requestTURN() {
	cfg := c.request().Config
	if cfg.TURN.Mode != "worker" || !c.signalOnline() || !c.needsTURN() {
		return
	}
	c.turnMu.Lock()
	now := time.Now()
	timedOut := false
	if c.turnRequestID != "" && now.Sub(c.turnLast) >= 15*time.Second {
		c.turnFailedLocked(now)
		timedOut = true
	}
	if c.turnRequestID != "" || now.Before(c.turnRetry) || (c.turnRefresh > now.UnixMilli() && c.turnExpires > now.UnixMilli()) {
		c.turnMu.Unlock()
		if timedOut {
			c.emit(Event{Kind: "turn", State: "unavailable", Error: &Fault{Code: "turn_request_timeout", Message: "获取中继凭据超时，已进入退避重试"}})
		}
		return
	}
	id, _ := newSessionID()
	c.turnRequestID = id.String()
	c.turnLast = now
	c.turnState = "requesting"
	c.notifyTURNLocked()
	c.turnMu.Unlock()
	c.emit(Event{Kind: "turn", State: "requesting"})
	if err := c.send(iceSignalMessage{SignalMessage: SignalMessage{Type: "turn_request"}, RequestID: id.String(), TTLSecs: int(cfg.TURN.TTL.Duration() / time.Second)}); err != nil {
		c.turnMu.Lock()
		c.turnRequestID = ""
		c.turnState = "pending"
		c.turnRetry = time.Now().Add(5 * time.Second)
		c.notifyTURNLocked()
		c.turnMu.Unlock()
		c.emit(Event{Kind: "turn", State: "pending", Error: classifyError(err)})
	}
}

func (c *iceCoordinator) leaseRequest() (iceSignalMessage, bool) {
	if c.room != nil && c.room.enabled() {
		return iceSignalMessage{}, false
	}
	c.mu.Lock()
	ready, light := c.online && c.serverV2, c.leaseRenewal
	c.mu.Unlock()
	if ready {
		for _, p := range c.peerList() {
			p.mu.Lock()
			needed := p.online && !p.closed && (p.ready.Voice || len(p.ready.Mappings) > 0)
			p.mu.Unlock()
			if needed {
				kind := "transport_sync" // Compatibility with older Workers.
				if light {
					kind = "transport_renew"
				}
				return signalMessage(kind), true
			}
		}
	}
	return iceSignalMessage{}, false
}

func (c *iceCoordinator) needsTURN() bool {
	for _, p := range c.peerList() {
		p.mu.Lock()
		needed := p.online && !p.closed && p.ready.Profile != ProfileFixed && ((p.pending != nil && p.pending.ready.Phase != "direct" && p.pending.ready.Phase != "hole" && p.pending.ready.Phase != "peer_relay" && p.pending.ready.Phase != "relay_wait") || (p.active != nil && p.active.expires > 0))
		p.mu.Unlock()
		if needed {
			return true
		}
	}
	return false
}

func (c *iceCoordinator) notifyTURNLocked() {
	if c.turnChanged != nil {
		close(c.turnChanged)
	}
	c.turnChanged = make(chan struct{})
}

func (c *iceCoordinator) turnFailedLocked(now time.Time) {
	c.turnRequestID = ""
	c.turnState = "unavailable"
	c.turnBackoff = min(5*time.Minute, max(30*time.Second, c.turnBackoff*2))
	c.turnRetry = now.Add(c.turnBackoff)
	c.notifyTURNLocked()
}

func (c *iceCoordinator) failTURNRequest(m iceSignalMessage) bool {
	c.turnMu.Lock()
	matched := m.RequestID != "" && m.RequestID == c.turnRequestID
	if matched {
		c.turnFailedLocked(time.Now())
	}
	c.turnMu.Unlock()
	if matched {
		c.emit(Event{Kind: "turn", State: "unavailable", Error: &Fault{Code: m.Code, Message: m.Message}})
	}
	return matched
}

// Wait locally for on-demand credentials. Waiting here does not keep the DO
// awake, and avoids racing through every relay phase while one API call runs.
func (c *iceCoordinator) relayServers(ctx context.Context) ([]ICEServer, int64, string, error) {
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	waitError := func() error {
		if parent.Err() != nil {
			return parent.Err()
		}
		return &Fault{Code: "relay_credentials_unavailable", Message: "中继凭据暂未就绪，稍后重试直连；凭据申请继续按退避策略进行"}
	}
	for {
		servers, expires, state := c.servers()
		if c.request().Config.TURN.Mode != "worker" || len(servers) > 0 {
			return servers, expires, state, nil
		}
		if ctx.Err() != nil {
			return nil, 0, state, waitError()
		}
		c.requestTURN()
		c.turnMu.Lock()
		changed := c.turnChanged
		wait := time.Second
		if c.turnRequestID != "" {
			wait = max(time.Millisecond, time.Until(c.turnLast.Add(15*time.Second)))
		} else if time.Now().Before(c.turnRetry) {
			wait = time.Until(c.turnRetry)
		}
		// Recheck while holding the same lock as the notification to avoid a
		// response arriving between servers() and capturing the change channel.
		available := len(c.turnServers) > 0 && c.turnExpires > time.Now().UnixMilli()
		c.turnMu.Unlock()
		if available {
			continue
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, 0, state, waitError()
		case <-changed:
			timer.Stop()
		case <-timer.C:
		}
	}
}
func (c *iceCoordinator) servers() ([]ICEServer, int64, string) {
	cfg := c.request().Config
	if cfg.TURN.Mode == "off" {
		return nil, 0, "off"
	}
	if cfg.TURN.Mode == "manual" {
		return []ICEServer{{URLs: cfg.TURN.URLs, Username: cfg.TURN.Username, Credential: cfg.TURN.Credential}}, 0, "manual"
	}
	c.turnMu.Lock()
	defer c.turnMu.Unlock()
	if c.turnExpires > 0 && c.turnExpires <= time.Now().UnixMilli() {
		return nil, c.turnExpires, "expired"
	}
	return append([]ICEServer(nil), c.turnServers...), c.turnExpires, c.turnState
}
func signalMessage(kind string) iceSignalMessage {
	id, _ := newSessionID()
	return iceSignalMessage{SignalMessage: SignalMessage{Type: kind}, RequestID: id.String()}
}

func (c *iceCoordinator) activeMappings() map[string]bool {
	result := map[string]bool{}
	for _, p := range c.peerList() {
		p.mu.Lock()
		active := p.active
		lease := p.ready.LeaseUntil
		p.mu.Unlock()
		if active == nil || active.mux.ctx.Err() != nil || (!active.ready.localAuthorization() && lease <= time.Now().UnixMilli()) {
			continue
		}
		active.mux.mu.Lock()
		for _, channel := range active.mux.channels {
			if channel.ready.Load() && channel.ctx.Err() == nil {
				result[channel.mapping.ID] = true
			}
		}
		active.mux.mu.Unlock()
	}
	return result
}

func (c *iceCoordinator) watchNetwork() {
	source, ok := c.platform.(ICEPlatform)
	if !ok {
		return
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	last := ""
	for {
		facts, err := source.InterfaceSnapshots(c.ctx)
		if err == nil {
			keys := []string{}
			addresses := []string{}
			for _, f := range facts {
				if !f.Up || f.Loopback || isDefaultExcludedInterface(f.Name) {
					continue
				}
				for _, addr := range f.Addresses {
					keys = append(keys, f.Name+"/"+addr)
					addresses = append(addresses, addr)
				}
			}
			sort.Strings(keys)
			current := strings.Join(keys, "|")
			c.networkObserved(NetworkSnapshot{Handle: "system", Transport: "系统网络", Addresses: addresses, Available: len(keys) > 0, Validated: false})
			if last != "" && last != current {
				c.emit(Event{Kind: "network", State: "changed"})
				c.networkChanged()
			}
			last = current
		}
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
