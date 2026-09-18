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

func (c *clientTunnel) readUDP() {
	buffer := make([]byte, maxUDPDatagram)
	for {
		n, addr, err := c.udpListener.ReadFromUDP(buffer)
		if err != nil {
			return
		}
		c.mu.Lock()
		if c.ctx.Err() != nil {
			c.mu.Unlock()
			return
		}
		session := c.udpByAddr[addr.String()]
		if session == nil {
			if len(c.udpByID) >= maxUDPSessions {
				c.mu.Unlock()
				continue
			}
			if c.udpBudget != nil && c.udpBudget.Add(1) > maxUDPSessions {
				c.udpBudget.Add(-1)
				c.mu.Unlock()
				continue
			}
			id, err := newSessionID()
			if err != nil {
				if c.udpBudget != nil {
					c.udpBudget.Add(-1)
				}
				c.mu.Unlock()
				continue
			}
			session = &clientUDPSession{id: id, addr: addr}
			c.udpByAddr[addr.String()] = session
			c.udpByID[id] = session
		}
		session.lastSeen = time.Now()
		link := c.udpLink
		c.mu.Unlock()
		// UDP stays unreliable. During an outage packets may drop, but the
		// listening socket, association ID and upstream source port survive.
		if link != nil {
			if err := link.send(session.id, buffer[:n]); err != nil && link.ctx.Err() == nil {
				c.logf("映射 %s UDP session_id=%s 丢弃数据报：%v", c.t.id, session.id, err)
			}
		}
	}
}

func (c *clientTunnel) serveUDP(transport *clientTransport) {
	conn, ctx := transport.conn, transport.ctx
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return
	}
	defer stream.CancelRead(0)
	defer stream.CancelWrite(0)
	stop := context.AfterFunc(ctx, func() {
		stream.CancelRead(0)
		stream.CancelWrite(0)
	})
	defer stop()
	_ = stream.SetDeadline(time.Now().Add(sessionHandshakeTimeout))
	c.mu.Lock()
	c.generation++
	generation := c.generation
	c.mu.Unlock()
	hello := sessionHello{Version: sessionProtocol, MappingID: c.t.id, Protocol: "udp", SessionID: c.id.String(), Generation: generation}
	if err := writeSessionJSON(stream, hello); err != nil {
		return
	}
	var reply sessionReply
	if err := readSessionJSON(stream, &reply); err != nil {
		c.logf("映射 %s UDP 会话握手失败：%v", c.t.id, err)
		return
	}
	if reply.Version != sessionProtocol || reply.Status != "ok" {
		c.logf("映射 %s UDP 会话未就绪：%s", c.t.id, reply.Status)
		return
	}
	_ = stream.SetDeadline(time.Time{})
	link := newUDPLink(ctx, conn, stream, generation)
	c.mu.Lock()
	if c.transport != transport || ctx.Err() != nil {
		c.mu.Unlock()
		link.close()
		return
	}
	c.udpLink = link
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		if c.udpLink == link {
			c.udpLink = nil
		}
		c.mu.Unlock()
		link.close()
	}()
	c.logf("映射 %s UDP 传输已就绪，现有 session_id 自动恢复", c.t.id)
	serveUDPLink(link, func(id sessionID, packet []byte) {
		c.mu.Lock()
		session := c.udpByID[id]
		current := c.udpLink == link
		c.mu.Unlock()
		if session == nil || !current {
			return
		}
		c.mu.Lock()
		if c.udpByID[session.id] != session || c.udpLink != link {
			c.mu.Unlock()
			return
		}
		session.lastSeen = time.Now()
		c.mu.Unlock()
		if _, err := c.udpListener.WriteToUDP(packet, session.addr); err != nil && ctx.Err() == nil {
			c.logf("映射 %s UDP session_id=%s 回包失败：%v", c.t.id, session.id, err)
		}
	})
}

// A single receive loop owns each connection's datagram queue. The control
// stream is a lifetime signal: either direction closing it retires the link.
func serveUDPLink(link *udpLink, receive func(sessionID, []byte)) {
	stop := context.AfterFunc(link.ctx, func() {
		link.control.CancelRead(0)
		link.control.CancelWrite(0)
	})
	defer stop()
	controlDone := make(chan struct{})
	go func() {
		defer close(controlDone)
		var extra [1]byte
		_, _ = link.control.Read(extra[:])
		link.cancel()
	}()
	defer func() {
		link.cancel()
		link.control.CancelRead(0)
		<-controlDone
	}()
	var assembler udpReassembler
	for {
		if shared, ok := link.conn.(sessionPacketReceiver); ok {
			id, packet, err := shared.ReceiveSessionPacket(link.ctx)
			if err != nil {
				return
			}
			receive(id, packet)
			continue
		}
		data, err := link.conn.ReceiveDatagram(link.ctx)
		if err != nil {
			return
		}
		frame, err := parseUDPFragment(data)
		if err != nil {
			continue
		}
		packet, complete, err := assembler.push(frame, time.Now())
		if err == nil && complete {
			receive(frame.key.session, packet)
		}
	}
}

type providerUDPSession struct {
	id       sessionID
	socket   *net.UDPConn
	lastSeen time.Time
}

type providerUDPGroup struct {
	platform   Platform
	logf       logFunc
	ctx        context.Context
	key        providerSessionKey
	endpoint   *net.UDPAddr
	budget     *atomic.Int32
	mu         sync.Mutex
	closed     bool
	link       *udpLink
	generation uint64
	lastSeen   time.Time
	sessions   map[sessionID]*providerUDPSession
}

func (m *sessionManager) providerUDP(key providerSessionKey, endpoint ServiceEndpoint) (*providerUDPGroup, error) {
	return m.providerUDPContext(m.ctx, key, endpoint)
}

func (m *sessionManager) providerUDPContext(ctx context.Context, key providerSessionKey, endpoint ServiceEndpoint) (*providerUDPGroup, error) {
	m.mu.Lock()
	group := m.udp[key]
	m.mu.Unlock()
	if group != nil {
		return group, nil
	}
	ctx, cancel := context.WithTimeout(ctx, sessionHandshakeTimeout)
	defer cancel()
	stopCancel := context.AfterFunc(m.ctx, cancel)
	defer stopCancel()
	addr, err := m.platform.ResolveUDP(ctx, net.JoinHostPort(endpoint.Addr, fmt.Sprint(endpoint.Port)))
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || !m.peerAllowedLocked(key.mapping, key.fingerprint) {
		return nil, context.Canceled
	}
	if group = m.udp[key]; group != nil {
		return group, nil
	}
	if len(m.udp) >= maxUDPSessions {
		return nil, errors.New("UDP tunnel limit")
	}
	group = &providerUDPGroup{
		platform: m.platform, logf: m.logf,
		ctx: m.ctx, key: key, endpoint: addr, budget: &m.udpSockets,
		lastSeen: time.Now(), sessions: make(map[sessionID]*providerUDPSession),
	}
	m.udp[key] = group
	return group, nil
}

func (g *providerUDPGroup) attach(link *udpLink) error {
	g.mu.Lock()
	if g.closed || link.generation <= g.generation {
		g.mu.Unlock()
		return errors.New("UDP session closed or stale generation")
	}
	old := g.link
	g.link = link
	g.generation = link.generation
	g.lastSeen = time.Now()
	g.mu.Unlock()
	if old != nil {
		old.close()
	}
	return nil
}

func (g *providerUDPGroup) close() {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return
	}
	g.closed = true
	link := g.link
	g.link = nil
	for id, session := range g.sessions {
		_ = session.socket.Close()
		g.budget.Add(-1)
		delete(g.sessions, id)
	}
	g.mu.Unlock()
	if link != nil {
		link.close()
	}
}

func (g *providerUDPGroup) reap(now time.Time, timeout time.Duration) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for id, session := range g.sessions {
		if now.Sub(session.lastSeen) >= timeout {
			_ = session.socket.Close()
			g.budget.Add(-1)
			delete(g.sessions, id)
		}
	}
	return g.link == nil && len(g.sessions) == 0 && now.Sub(g.lastSeen) >= timeout
}

func (g *providerUDPGroup) deliver(link *udpLink, id sessionID, packet []byte) {
	g.mu.Lock()
	if g.closed || g.link != link || link.ctx.Err() != nil {
		g.mu.Unlock()
		return
	}
	session := g.sessions[id]
	if session == nil {
		if g.budget.Add(1) > maxUDPSessions {
			g.budget.Add(-1)
			g.mu.Unlock()
			return
		}
		socket, err := g.platform.DialUDP(g.ctx, g.endpoint)
		if err != nil {
			g.budget.Add(-1)
			g.mu.Unlock()
			g.logf("映射 %s 创建 UDP 会话失败：%v", g.key.mapping, err)
			return
		}
		session = &providerUDPSession{id: id, socket: socket}
		g.sessions[id] = session
		go g.readReplies(session)
	}
	session.lastSeen = time.Now()
	g.lastSeen = session.lastSeen
	g.mu.Unlock()
	_ = session.socket.SetWriteDeadline(time.Now().Add(time.Second))
	if _, err := session.socket.Write(packet); err != nil && link.ctx.Err() == nil {
		g.logf("映射 %s UDP session_id=%s 写入服务失败：%v", g.key.mapping, id, err)
	}
}

func (g *providerUDPGroup) readReplies(session *providerUDPSession) {
	defer func() {
		g.mu.Lock()
		if g.sessions[session.id] == session {
			delete(g.sessions, session.id)
			g.budget.Add(-1)
		}
		g.mu.Unlock()
		_ = session.socket.Close()
	}()
	buffer := make([]byte, maxUDPDatagram)
	for {
		n, err := session.socket.Read(buffer)
		if err != nil {
			return
		}
		g.mu.Lock()
		if g.closed || g.sessions[session.id] != session {
			g.mu.Unlock()
			return
		}
		session.lastSeen = time.Now()
		g.lastSeen = session.lastSeen
		link := g.link
		g.mu.Unlock()
		if link != nil {
			if err := link.send(session.id, buffer[:n]); err != nil && link.ctx.Err() == nil {
				g.logf("映射 %s UDP session_id=%s 丢弃回包：%v", g.key.mapping, session.id, err)
			}
		}
	}
}

func (a *Agent) handleProviderUDP(ctx context.Context, conn sessionConnection, stream *quic.Stream, hello sessionHello, key providerSessionKey, endpoint ServiceEndpoint) {
	group, err := a.sessions.providerUDPContext(ctx, key, endpoint)
	if err != nil {
		_ = writeSessionJSON(stream, sessionReply{Version: sessionProtocol, Status: "UDP_session_unavailable"})
		return
	}
	if conn.Context().Err() != nil || !a.verifyPeerCert(hello.MappingID, conn) {
		_ = writeSessionJSON(stream, sessionReply{Version: sessionProtocol, Status: "mapping_not_authorized"})
		return
	}
	link := newUDPLink(ctx, conn, stream, hello.Generation)
	if err := group.attach(link); err != nil {
		_ = writeSessionJSON(stream, sessionReply{Version: sessionProtocol, Status: "stale_generation"})
		link.close()
		return
	}
	defer func() {
		group.mu.Lock()
		if group.link == link {
			group.link = nil
			group.lastSeen = time.Now()
		}
		group.mu.Unlock()
		link.close()
	}()
	if err := writeSessionJSON(stream, sessionReply{Version: sessionProtocol, Status: "ok"}); err != nil {
		return
	}
	_ = stream.SetDeadline(time.Time{})
	serveUDPLink(link, func(id sessionID, packet []byte) { group.deliver(link, id, packet) })
}
