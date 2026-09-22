package core

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"
)

type providerSessionKey struct {
	mapping     string
	fingerprint string
	id          sessionID
}

type providerTCPEntry struct {
	ready   chan struct{}
	session *tcpSession
	err     error
	cancel  context.CancelFunc
}

// The manager is process-scoped, not signaling- or QUIC-connection-scoped.
// Authentication is still required on every resumed stream. IDs alone grant
// no access, and a new process certificate does not inherit another's sockets.
type sessionManager struct {
	platform        Platform
	logf            logFunc
	ctx             context.Context
	cancel          context.CancelFunc
	timeout         time.Duration
	mu              sync.Mutex
	closed          bool
	clients         map[string]*clientTunnel
	tcp             map[providerSessionKey]*providerTCPEntry
	udp             map[providerSessionKey]*providerUDPGroup
	udpSockets      atomic.Int32
	owners          map[peerMapping]string
	clientTCPBudget *atomic.Int32
	clientUDPBudget *atomic.Int32
	emit            func(Event)
}

type sessionOptions struct {
	platform Platform
	logf     logFunc
	shared   bool
	emit     func(Event)
}

func newSessionManager(timeout time.Duration, options ...sessionOptions) *sessionManager {
	option := sessionOptions{platform: DefaultPlatform{}, logf: discardLog}
	if len(options) > 0 {
		option = options[0]
	}
	if option.platform == nil {
		option.platform = DefaultPlatform{}
	}
	if option.logf == nil {
		option.logf = discardLog
	}
	if timeout == 0 {
		timeout = defaultSessionTimeout
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &sessionManager{
		emit:     option.emit,
		platform: option.platform, logf: option.logf,
		ctx: ctx, cancel: cancel, timeout: timeout,
		clients: make(map[string]*clientTunnel),
		tcp:     make(map[providerSessionKey]*providerTCPEntry),
		udp:     make(map[providerSessionKey]*providerUDPGroup),
		owners:  make(map[peerMapping]string),
	}
	go m.reap()
	if option.shared {
		m.clientTCPBudget = &atomic.Int32{}
		m.clientUDPBudget = &atomic.Int32{}
	}
	return m
}

func (m *sessionManager) close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	m.cancel()
	for _, client := range m.clients {
		client.close()
	}
	for _, entry := range m.tcp {
		if entry.cancel != nil {
			entry.cancel()
		}
		select {
		case <-entry.ready:
			if entry.session != nil {
				entry.session.close()
			}
		default: // DialContext uses m.ctx and will clean up its own result.
		}
	}
	for _, group := range m.udp {
		group.close()
	}
	m.mu.Unlock()
}

func (m *sessionManager) consumer(t tunnel, fingerprint string) (*clientTunnel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, context.Canceled
	}
	if existing := m.clients[t.id]; existing != nil {
		if existing.fingerprint == fingerprint && existing.t == t {
			return existing, nil
		}
		// A provider restart/configuration change is not a network migration.
		existing.close()
		delete(m.clients, t.id)
	}
	client, err := newClientTunnel(m.ctx, t, fingerprint, m.logf, m.clientTCPBudget, m.clientUDPBudget)
	if err != nil {
		return nil, err
	}
	m.clients[t.id] = client
	client.emit = m.emit
	return client, nil
}

func (m *sessionManager) consumerForConnection(t tunnel, fingerprint string) (*clientTunnel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	client := m.clients[t.id]
	if m.closed || client == nil || client.t != t || client.fingerprint != fingerprint {
		return nil, errors.New("stale mapping transport")
	}
	return client, nil
}

func (m *sessionManager) providerTCP(ctx context.Context, key providerSessionKey, resume bool, endpoint ServiceEndpoint) (*tcpSession, error) {
	m.mu.Lock()
	if m.closed || !m.peerAllowedLocked(key.mapping, key.fingerprint) {
		m.mu.Unlock()
		return nil, context.Canceled
	}
	entry := m.tcp[key]
	creator := entry == nil
	var dialCtx context.Context
	if creator {
		if resume {
			m.mu.Unlock()
			return nil, errors.New("session_expired")
		}
		// Closed entries are not active sockets. A resume to an evicted entry
		// fails rather than silently opening a new service connection.
		for oldKey, old := range m.tcp {
			select {
			case <-old.ready:
				if old.err != nil || old.session.isClosed() {
					delete(m.tcp, oldKey)
				}
			default:
			}
		}
		if len(m.tcp) >= maxTCPSessions {
			m.mu.Unlock()
			return nil, errors.New("session_limit")
		}
		var cancel context.CancelFunc
		dialCtx, cancel = context.WithTimeout(ctx, sessionHandshakeTimeout)
		entry = &providerTCPEntry{ready: make(chan struct{}), cancel: cancel}
		m.tcp[key] = entry
	}
	m.mu.Unlock()
	if creator {
		// Reserve the ID before dialing, so overlapping reconnects do not
		// create two upstream sockets. Do not hold the manager lock on I/O.
		stopCancel := context.AfterFunc(m.ctx, entry.cancel)
		conn, err := m.platform.DialTCP(dialCtx, net.JoinHostPort(endpoint.Addr, fmt.Sprint(endpoint.Port)))
		stopCancel()
		entry.cancel()
		m.mu.Lock()
		if m.closed || m.tcp[key] != entry || !m.peerAllowedLocked(key.mapping, key.fingerprint) {
			if conn != nil {
				conn.Close()
			}
			err = context.Canceled
		}
		entry.err = err
		if err == nil {
			entry.session = newTCPSession(m.ctx, key.id, conn)
			if m.ctx.Err() != nil {
				entry.session.close()
			}
		}
		close(entry.ready)
		if entry.err != nil {
			// No upstream socket was created, so an initial open may retry.
			if m.tcp[key] == entry {
				delete(m.tcp, key)
			}
		}
		m.mu.Unlock()
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-entry.ready:
		return entry.session, entry.err
	}
}

// disconnect retires transport paths but retains local listeners, TCP replay
// buffers and provider UDP sockets. An empty filter matches all mappings/peers.
func (m *sessionManager) disconnect(mapping, fingerprint string) {
	connections := make(map[sessionConnection]bool)
	m.mu.Lock()
	for id, client := range m.clients {
		if (mapping == "" || mapping == id) && (fingerprint == "" || fingerprint == client.fingerprint) {
			client.mu.Lock()
			if client.transport != nil {
				connections[client.transport.conn] = true
			}
			client.mu.Unlock()
		}
	}
	for key, entry := range m.tcp {
		if (mapping != "" && mapping != key.mapping) || (fingerprint != "" && fingerprint != key.fingerprint) {
			continue
		}
		select {
		case <-entry.ready:
			if entry.session != nil {
				entry.session.mu.Lock()
				if entry.session.link != nil {
					connections[entry.session.link.conn] = true
				}
				entry.session.mu.Unlock()
			}
		default:
		}
	}
	for key, group := range m.udp {
		if (mapping == "" || mapping == key.mapping) && (fingerprint == "" || fingerprint == key.fingerprint) {
			group.mu.Lock()
			if group.link != nil {
				connections[group.link.conn] = true
			}
			group.mu.Unlock()
		}
	}
	m.mu.Unlock()
	for conn := range connections {
		_ = conn.CloseWithError(0, "path changed; application sessions retained")
	}
}

func (m *sessionManager) reap() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case now := <-ticker.C:
			m.mu.Lock()
			for _, client := range m.clients {
				client.reap(now, m.timeout)
			}
			for key, entry := range m.tcp {
				select {
				case <-entry.ready:
					if entry.err != nil || entry.session.expired(now, m.timeout) {
						if entry.session != nil {
							entry.session.close()
						}
						delete(m.tcp, key)
					}
				default:
				}
			}
			for key, group := range m.udp {
				if group.reap(now, m.timeout) {
					group.close()
					delete(m.udp, key)
				}
			}
			m.mu.Unlock()
		}
	}
}

type clientTransport struct {
	conn   sessionConnection
	ctx    context.Context
	cancel context.CancelFunc
}

type clientUDPSession struct {
	id       sessionID
	addr     *net.UDPAddr
	lastSeen time.Time
}

type clientTunnel struct {
	logf        logFunc
	t           tunnel
	fingerprint string
	id          sessionID
	ctx         context.Context
	cancel      context.CancelFunc
	tcpListener *net.TCPListener
	udpListener *net.UDPConn

	mu              sync.Mutex
	changed         chan struct{}
	transport       *clientTransport
	tcp             map[sessionID]*tcpSession
	udpByAddr       map[string]*clientUDPSession
	udpByID         map[sessionID]*clientUDPSession
	udpLink         *udpLink
	udpReadBytes    atomic.Uint64
	udpWrittenBytes atomic.Uint64
	generation      uint64
	tcpBudget       *atomic.Int32
	udpBudget       *atomic.Int32
	emit            func(Event)
}

func newClientTunnel(ctx context.Context, t tunnel, fingerprint string, logf logFunc, budgets ...*atomic.Int32) (*clientTunnel, error) {
	id, err := newSessionID()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	c := &clientTunnel{
		logf: logf,
		t:    t, fingerprint: fingerprint, id: id, ctx: ctx, cancel: cancel,
		changed: make(chan struct{}), tcp: make(map[sessionID]*tcpSession),
		udpByAddr: make(map[string]*clientUDPSession), udpByID: make(map[sessionID]*clientUDPSession),
	}
	if len(budgets) == 2 {
		c.tcpBudget, c.udpBudget = budgets[0], budgets[1]
	}
	if t.protocol == "tcp" {
		bind := tcpAddr(t.expose)
		c.tcpListener, err = net.ListenTCP(exposeNetwork("tcp", bind.IP), bind)
	} else {
		bind := udpAddr(t.expose)
		c.udpListener, err = net.ListenUDP(exposeNetwork("udp", bind.IP), bind)
	}
	if err != nil {
		cancel()
		return nil, err
	}
	if t.protocol == "tcp" {
		go c.acceptTCP()
	} else {
		go c.readUDP()
	}
	c.logf("映射 %s 本地监听已启动：%s %s（会话跨重连保留）", t.id, t.protocol, net.JoinHostPort(t.expose.Addr, fmt.Sprint(t.expose.Port)))
	return c, nil
}

func (c *clientTunnel) close() {
	c.cancel()
	if c.tcpListener != nil {
		_ = c.tcpListener.Close()
	}
	if c.udpListener != nil {
		_ = c.udpListener.Close()
	}
	c.mu.Lock()
	if c.udpBudget != nil {
		c.udpBudget.Add(-int32(len(c.udpByID)))
	}
	clear(c.udpByID)
	clear(c.udpByAddr)
	if c.transport != nil {
		c.transport.cancel()
		_ = c.transport.conn.CloseWithError(0, "mapping stopped")
	}
	for _, session := range c.tcp {
		session.close()
	}
	c.mu.Unlock()
}

func (c *clientTunnel) bind(ctx context.Context, conn sessionConnection) (*clientTransport, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ctx.Err() != nil || c.ctx.Err() != nil {
		return nil, context.Canceled
	}
	if c.transport != nil {
		c.transport.cancel()
		_ = c.transport.conn.CloseWithError(0, "transport replaced")
	}
	ctx, cancel := context.WithCancel(ctx)
	transport := &clientTransport{conn: conn, ctx: ctx, cancel: cancel}
	c.transport = transport
	close(c.changed)
	c.changed = make(chan struct{})
	return transport, nil
}

func (c *clientTunnel) unbind(transport *clientTransport) {
	transport.cancel()
	c.mu.Lock()
	if c.transport == transport {
		c.transport = nil
		close(c.changed)
		c.changed = make(chan struct{})
	}
	c.mu.Unlock()
}

func (c *clientTunnel) acceptTCP() {
	for {
		socket, err := c.tcpListener.AcceptTCP()
		if err != nil {
			return
		}
		id, err := newSessionID()
		if err != nil {
			_ = socket.Close()
			continue
		}
		c.mu.Lock()
		if c.ctx.Err() != nil || len(c.tcp) >= maxTCPSessions {
			c.mu.Unlock()
			_ = socket.Close()
			continue
		}
		if c.tcpBudget != nil && c.tcpBudget.Add(1) > maxTCPSessions {
			c.tcpBudget.Add(-1)
			c.mu.Unlock()
			_ = socket.Close()
			continue
		}
		session := newTCPSession(c.ctx, id, socket)
		c.tcp[id] = session
		c.mu.Unlock()
		go c.runTCP(session)
	}
}

func (c *clientTunnel) runTCP(session *tcpSession) {
	defer session.close()
	defer func() {
		if c.tcpBudget != nil {
			c.tcpBudget.Add(-1)
		}
		c.mu.Lock()
		delete(c.tcp, session.id)
		c.mu.Unlock()
	}()
	openDeadline := time.Now().Add(tcpInitialOpenTimeout)
	lastError := ""
	for session.ctx.Err() == nil {
		if !session.wasOpened() && !time.Now().Before(openDeadline) {
			message := "首次应用会话建立超时，已结束本次连接"
			if lastError != "" {
				message += "：" + lastError
			}
			c.sessionEvent(session, "error", "open", &Fault{Code: "session_open_timeout", Message: message})
			return
		}
		c.mu.Lock()
		transport, changed := c.transport, c.changed
		c.mu.Unlock()
		if transport == nil {
			var deadline <-chan time.Time
			var timer *time.Timer
			if !session.wasOpened() {
				timer = time.NewTimer(time.Until(openDeadline))
				deadline = timer.C
			}
			select {
			case <-session.ctx.Done():
				if timer != nil {
					timer.Stop()
				}
				return
			case <-changed:
			case <-deadline:
			}
			if timer != nil {
				timer.Stop()
			}
			continue
		}
		err := c.runTCPStream(session, transport, openDeadline)
		if session.ctx.Err() != nil {
			return
		}
		if err != nil && transport.ctx.Err() == nil && transport.conn.Context().Err() == nil {
			if session.wasOpened() && lastError != err.Error() {
				c.sessionEvent(session, "recovering", "resume", &Fault{Code: "session_transport_interrupted", Message: err.Error()})
			}
			lastError = err.Error()
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-session.ctx.Done():
			timer.Stop()
			return
		case <-changed:
			timer.Stop()
		case <-timer.C:
		}
	}
}

func (c *clientTunnel) runTCPStream(session *tcpSession, transport *clientTransport, openDeadline time.Time) error {
	ctx, cancel := context.WithCancel(session.ctx)
	stop := context.AfterFunc(transport.ctx, cancel)
	defer stop()
	defer cancel()
	deadline := time.Now().Add(sessionHandshakeTimeout)
	if !session.wasOpened() && openDeadline.Before(deadline) {
		deadline = openDeadline
	}
	handshakeCtx, cancelHandshake := context.WithDeadline(ctx, deadline)
	defer cancelHandshake()
	stream, err := transport.conn.OpenStreamSync(handshakeCtx)
	if err != nil {
		return err
	}
	defer stream.CancelRead(0)
	defer stream.CancelWrite(0)
	stopStream := context.AfterFunc(ctx, func() {
		stream.CancelRead(0)
		stream.CancelWrite(0)
	})
	defer stopStream()
	stopHandshake := context.AfterFunc(handshakeCtx, func() { stream.CancelRead(0); stream.CancelWrite(0) })
	defer stopHandshake()
	_ = stream.SetDeadline(deadline)
	hello := session.hello(c.t.id)
	if err := writeSessionJSON(stream, hello); err != nil {
		return err
	}
	var reply sessionReply
	if err := readSessionJSON(stream, &reply); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if reply.Version != sessionProtocol {
		session.close()
		return errors.New("incompatible TCP session protocol")
	}
	if reply.Status != "ok" {
		message := reply.Message
		if message == "" {
			message = "提供端未能建立应用会话（" + reply.Status + "）"
		}
		stage := "open"
		if hello.Resume {
			stage = "resume"
		}
		c.sessionEvent(session, "error", stage, &Fault{Code: reply.Status, Message: message})
		switch reply.Status {
		case "mapping_not_authorized", "stale_generation":
			// mapping_ready may reach the consumer before the provider. Wait
			// for its signaling state rather than closing an established socket.
		default:
			session.close()
		}
		return fmt.Errorf("TCP session not ready: %s", reply.Status)
	}
	link, _, err := session.attach(transport.conn, stream, hello.Generation, reply.ReceiveOffset, reply.ReceiveFIN)
	if err != nil {
		session.close()
		return err
	}
	if !stopHandshake() && handshakeCtx.Err() != nil {
		session.detach(link)
		return handshakeCtx.Err()
	}
	cancelHandshake()
	_ = stream.SetDeadline(time.Time{})
	if c.emit != nil {
		c.emit(Event{Kind: "session", MappingID: c.t.id, Protocol: "tcp", Peer: c.t.peer, SessionID: session.id.String(), Resume: hello.Resume, State: "active"})
	}
	return session.runLink(ctx, link, reply.ReceiveOffset)
}

func (c *clientTunnel) reap(now time.Time, timeout time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, session := range c.tcp {
		if session.expired(now, timeout) {
			session.close()
		}
	}
	for key, session := range c.udpByAddr {
		if now.Sub(session.lastSeen) >= timeout {
			if c.udpBudget != nil {
				c.udpBudget.Add(-1)
			}
			delete(c.udpByAddr, key)
			delete(c.udpByID, session.id)
		}
	}
}

func (a *Agent) handleProviderStream(ctx context.Context, conn sessionConnection, stream *quic.Stream) {
	defer stream.Close()
	defer stream.CancelRead(0)
	_ = stream.SetDeadline(time.Now().Add(sessionHandshakeTimeout))
	var hello sessionHello
	if err := readSessionJSON(stream, &hello); err != nil {
		return
	}
	respond := func(status string) {
		_ = writeSessionJSON(stream, sessionReply{Version: sessionProtocol, Status: status})
	}
	id, err := parseSessionID(hello.SessionID)
	if err != nil || hello.Version != sessionProtocol || hello.Generation == 0 {
		respond("invalid_handshake")
		return
	}
	provide, ok := a.provide[hello.MappingID]
	if !ok || provide.Service.Protocol != hello.Protocol || !a.verifyPeerCert(hello.MappingID, conn) {
		respond("mapping_not_authorized")
		return
	}
	key := providerSessionKey{mapping: hello.MappingID, fingerprint: connectionFingerprint(conn), id: id}
	if hello.Protocol == "udp" {
		a.handleProviderUDP(ctx, conn, stream, hello, key, provide.Service)
		return
	}
	target := net.JoinHostPort(provide.Service.Addr, fmt.Sprint(provide.Service.Port))
	session, err := a.sessions.providerTCP(ctx, key, hello.Resume, provide.Service)
	if err != nil {
		if ctx.Err() != nil || conn.Context().Err() != nil {
			return
		}
		fault := serviceSessionFault(err, target, hello.Resume)
		stage := "dial"
		if hello.Resume {
			stage = "resume"
		}
		a.sessionEvent(conn, hello, target, "error", stage, fault)
		_ = writeSessionJSON(stream, sessionReply{Version: sessionProtocol, Status: fault.Code, Message: fault.Message})
		return
	}
	if conn.Context().Err() != nil || !a.verifyPeerCert(hello.MappingID, conn) {
		respond("mapping_not_authorized")
		return
	}
	link, reply, err := session.attach(conn, stream, hello.Generation, hello.ReceiveOffset, hello.ReceiveFIN)
	if err != nil {
		switch {
		case session.isClosed():
			respond("session_closed")
		case errors.Is(err, errSessionProtocol):
			respond("invalid_session_state")
		default:
			respond("stale_generation")
		}
		return
	}
	if err := writeSessionJSON(stream, reply); err != nil {
		session.detach(link)
		return
	}
	_ = stream.SetDeadline(time.Time{})
	a.sessionEvent(conn, hello, target, "active", "", nil)
	_ = session.runLink(ctx, link, hello.ReceiveOffset)
}

func (a *Agent) runConsumer(ctx context.Context, conn sessionConnection, t tunnel) {
	if ctx.Err() != nil {
		return
	}
	client, err := a.sessions.consumerForConnection(t, connectionFingerprint(conn))
	if err != nil {
		a.logf("映射 %s 初始化会话失败：%v", t.id, err)
		return
	}
	transport, err := client.bind(ctx, conn)
	if err != nil {
		return
	}
	defer client.unbind(transport)
	if t.protocol == "udp" {
		client.serveUDP(transport)
		return
	}
	a.logf("映射 %s TCP 传输已就绪，现有 session_id 自动恢复", t.id)
	select {
	case <-ctx.Done():
	case <-conn.Context().Done():
	}
}
