package core

import (
	"context"
	"crypto/tls"
	"errors"
	"github.com/gorilla/websocket"
	"net/http"
	"net/url"
	"reflect"
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
	networkEpoch             atomic.Uint64
	reset                    chan struct{}
	mu                       sync.Mutex
	peers                    map[string]*icePeer
	sink                     *iceSignalSink
	online                   bool
	serverV2                 bool
	leaseRenewal             bool
	legacy                   *Agent
	turnMu                   sync.Mutex
	turnServers              []ICEServer
	turnExpires, turnRefresh int64
	turnState                string
	turnRequestID            string
	turnLast                 time.Time
	turnRetry                time.Time
	turnBackoff              time.Duration
	turnChanged              chan struct{}
}

func newICECoordinator(ctx context.Context, r Request, platform Platform, sessions *sessionManager, identity *tls.Config, runtimeID string, epoch uint64, emit func(Event), networkObserved func(NetworkSnapshot)) *iceCoordinator {
	ctx, cancel := context.WithCancel(ctx)
	c := &iceCoordinator{ctx: ctx, cancel: cancel, platform: platform, sessions: sessions, identity: identity, runtimeID: runtimeID, fingerprint: certFingerprint(identity.Certificates[0]), emit: emit, networkObserved: networkObserved, desired: r, reset: make(chan struct{}, 1), peers: map[string]*icePeer{}, turnState: "pending", turnChanged: make(chan struct{})}
	c.networkEpoch.Store(epoch)
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
	c.configMu.Lock()
	old := c.desired
	c.desired = r
	c.configMu.Unlock()
	if !reflect.DeepEqual(old.Config.ICE, r.Config.ICE) || !reflect.DeepEqual(old.Config.TURN, r.Config.TURN) || old.Config.Transport != r.Config.Transport {
		c.networkEpoch.Add(1)
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
	case c.reset <- struct{}{}:
	default:
	}
}
func (c *iceCoordinator) networkChanged() {
	c.networkEpoch.Add(1)
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
func (c *iceCoordinator) send(m iceSignalMessage) error {
	c.mu.Lock()
	sink := c.sink
	c.mu.Unlock()
	if sink == nil {
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
	monitorDone := make(chan struct{})
	if _, bound := c.platform.(NetworkSnapshotter); bound {
		close(monitorDone)
	} else {
		go func() { defer close(monitorDone); c.watchNetwork() }()
	}
	defer func() { c.cancel(); <-monitorDone; c.close() }()
	var last Request
	lastSet := false
	backoff := time.Second
	for c.ctx.Err() == nil {
		current := c.request()
		if lastSet && !sameEffective(last, current) {
			c.closeLegacy()
			c.sessions.reconfigure(last.Config, current.Config)
			for _, p := range c.peerList() {
				p.refreshPolicy()
			}
		}
		last = current
		lastSet = true
		select {
		case <-c.reset:
		default:
		}
		started := time.Now()
		err, changed := c.connect(current)
		if c.ctx.Err() != nil {
			return c.ctx.Err()
		}
		c.mu.Lock()
		c.online = false
		c.sink = nil
		c.mu.Unlock()
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
		fault := classifyError(err)
		fault.Message = redact(fault.Message, current.Config)
		c.emit(Event{Kind: "signal", State: "reconnecting", Error: fault})
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
		case <-timer.C:
		}
		backoff = min(current.Config.ICE.RetryMaxDelay.Duration(), backoff*2)
	}
	return c.ctx.Err()
}
func (c *iceCoordinator) connect(r Request) (error, bool) {
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
	conn.SetReadLimit(64 * 1024)
	_ = conn.SetReadDeadline(time.Now().Add(signalPongWait))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(signalPongWait)) })
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	profiles := []string{ProfileICE}
	var candidates []Candidate
	if r.Config.Transport.AllowLegacy {
		profiles = append(profiles, ProfileLegacy)
		candidates, _ = platform.Candidates(ctx, r.Config, holePort)
	}
	join := iceSignalMessage{SignalMessage: SignalMessage{Type: "join", Room: r.Config.Room, Token: r.Config.Token, DeviceID: r.Config.DeviceName, DeviceName: r.Config.DeviceName, Provide: r.Config.Provide, Consume: r.Config.Consume, Candidates: candidates, CertFingerprint: c.fingerprint},
		SignalVersion: 2, AuthMode: "shared-secret", RuntimeID: c.runtimeID, TransportEpoch: c.networkEpoch.Load(), TransportProfiles: profiles, SessionVersions: []int{sessionProtocol}, RelayPolicy: RelayPolicyUDPTCPTLS}
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err = conn.WriteJSON(join); err != nil {
		return err, false
	}
	sink := &iceSignalSink{ctx: ctx, messages: make(chan iceSignalMessage, 256)}
	c.mu.Lock()
	c.sink = sink
	c.mu.Unlock()
	c.emit(Event{Kind: "signal", State: "joining"})
	results := make(chan error, 2)
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
			if err = c.handle(message, r, platform); err != nil {
				results <- err
				return
			}
		}
	}()
	go func() {
		defer workers.Done()
		ticker := time.NewTicker(25 * time.Second)
		defer ticker.Stop()
		leases := time.NewTicker(60 * time.Second)
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
	changed := false
	select {
	case <-c.ctx.Done():
		err = c.ctx.Err()
	case <-c.reset:
		changed = true
	case err = <-results:
	}
	cancel()
	conn.Close()
	workers.Wait()
	return err, changed
}
func (c *iceCoordinator) handle(m iceSignalMessage, sessionRequest Request, platform Platform) error {
	switch m.Type {
	case "joined":
		if !sameEffective(sessionRequest, c.request()) {
			return nil
		}
		if m.SignalVersion < 2 && !sessionRequest.Config.Transport.AllowLegacy {
			return &Fault{Code: "worker_upgrade_required", Message: "协调服务尚未支持 ICE，请先更新 Worker"}
		}
		c.mu.Lock()
		c.online = true
		c.serverV2 = m.SignalVersion >= 2
		c.leaseRenewal = m.LeaseRenewal
		c.mu.Unlock()
		c.emit(Event{Kind: "signal", State: "joined"})
		if m.SignalVersion >= 2 {
			// Full state is requested once on join/rejoin, never as a heartbeat.
			_ = c.send(signalMessage("transport_sync"))
		}
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
		if !validRelayPhase(m.RelayPolicy, m.Phase) {
			return &Fault{Code: "protocol_mismatch", Message: "协调服务返回的中继策略或路径阶段不受支持"}
		}
		if m.Profile != ProfileICE || m.SessionVersion != sessionProtocol || m.TransportID == "" || m.TransportGeneration == 0 || m.PeerRuntimeID == "" || len(m.PeerFingerprint) != 64 || m.PeerDevice == "" || len(m.Mappings) > maxPeerChannels {
			return &Fault{Code: "protocol_mismatch", Message: "协调服务返回的 ICE 能力或传输信息无效"}
		}
		expected := c.deviceID()
		if m.PeerDevice < expected {
			expected = m.PeerDevice
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
		c.requestTURN()
	case "ice_description", "ice_candidate":
		c.mu.Lock()
		p := c.peers[m.TransportID]
		c.mu.Unlock()
		if p != nil {
			p.receive(m)
		}
	case "mapping_ready":
		if m.Profile != "" && m.Profile != ProfileLegacy {
			return nil
		}
		if !sessionRequest.Config.Transport.AllowLegacy {
			return &Fault{Code: "protocol_mismatch", Message: "对端仅支持 IPv6 直连协议，当前配置未启用兼容模式"}
		}
		if err := c.legacyMapping(m.SignalMessage, platform); err != nil {
			c.emit(Event{Kind: "mapping", MappingID: m.MappingID, State: "error", Error: classifyError(err)})
		}
	case "peer_candidates":
		c.mu.Lock()
		a := c.legacy
		c.mu.Unlock()
		if a != nil {
			a.updateSessionCandidates(m.MappingID, m.PeerDevice, m.Candidates)
		}
	case "mapping_closed":
		c.mu.Lock()
		a := c.legacy
		c.mu.Unlock()
		if a != nil {
			a.stopMapping(m.MappingID, m.PeerDevice)
		}
		c.emit(Event{Kind: "mapping", MappingID: m.MappingID, State: "waiting_peer"})
	case "peer_state":
		for _, p := range c.peerList() {
			if p.peerID() == m.PeerDevice {
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
func (c *iceCoordinator) close() {
	c.cancel()
	c.mu.Lock()
	c.online = false
	c.sink = nil
	c.mu.Unlock()
	for _, p := range c.peerList() {
		p.close()
	}
	c.closeLegacy()
}
func (c *iceCoordinator) legacyMapping(msg SignalMessage, p Platform) error {
	c.mu.Lock()
	a := c.legacy
	c.mu.Unlock()
	if a == nil {
		var err error
		a, err = newAgentWithState(c.ctx, c.request().Config, p, func(event Event) { event.Profile = ProfileLegacy; c.emit(event) }, c.sessions, c.identity)
		if err != nil {
			return err
		}
		if err = a.ensureProviderListener(a.lifecycleContext); err != nil {
			a.shutdown()
			return err
		}
		a.spawn(func() { a.runPunchReceiver(a.lifecycleContext) })
		c.mu.Lock()
		c.legacy = a
		c.mu.Unlock()
	}
	a.setPeerFingerprint(msg.MappingID, msg.PeerDevice, msg.PeerFingerprint)
	a.startMapping(a.lifecycleContext, msg)
	return nil
}
func (c *iceCoordinator) closeLegacy() {
	c.mu.Lock()
	a := c.legacy
	c.legacy = nil
	c.mu.Unlock()
	if a != nil {
		a.shutdown()
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
	c.mu.Lock()
	ready, light := c.online && c.serverV2, c.leaseRenewal
	c.mu.Unlock()
	if ready {
		for _, p := range c.peerList() {
			p.mu.Lock()
			needed := p.online && !p.closed && len(p.ready.Mappings) > 0
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
		needed := p.online && !p.closed && ((p.pending != nil && p.pending.ready.Phase != "direct") || (p.active != nil && p.active.expires > 0))
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
		if active == nil || active.mux.ctx.Err() != nil || lease <= time.Now().UnixMilli() {
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
