package core

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"
)

// A device-pair datagram path is independent of ICE or a concrete forwarder.
// Future backends must preserve PacketConn address/deadline semantics.
type deviceDatagramPath interface {
	net.PacketConn
	PeerAddr() net.Addr
}
type holeClientPath struct {
	conn                net.PacketConn
	remote              *net.UDPAddr
	readAuth, writeAuth *holeAuth
	side                int
	replay              holeReplay
	writeMu             sync.Mutex
	seq                 uint64
	writeBuffer         [holePacketSize]byte
	readBuffer          [holePacketSize]byte
}

func (p *holeClientPath) send(kind byte, data []byte) (int, error) {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	if len(data) > holePacketSize-holeHeader {
		return 0, errors.New("Hole datagram too large")
	}
	b := p.writeBuffer[:holeHeader+len(data)]
	copy(b[holeHeader:], data)
	p.seq++
	p.writeAuth.sign(b, kind, p.side, p.seq)
	_, err := p.conn.WriteTo(b, p.remote)
	if err != nil {
		return 0, err
	}
	return len(data), nil
}
func (p *holeClientPath) ReadFrom(b []byte) (int, net.Addr, error) {
	for {
		n, a, err := p.conn.ReadFrom(p.readBuffer[:])
		if err != nil {
			return 0, nil, err
		}
		if a.String() != p.remote.String() {
			continue
		}
		packet := p.readBuffer[:n]
		if !p.readAuth.valid(packet) {
			continue
		}
		switch packet[4] {
		case holeChallenge:
			if int(packet[5]) == p.side && n == holeHeader+16 {
				_, _ = p.send(holeConfirm, packet[holeHeader:])
			}
		case holeData:
			if int(packet[5]) != p.side && p.replay.accept(binarySequence(packet)) {
				return copy(b, packet[holeHeader:]), p.remote, nil
			}
		}
	}
}
func binarySequence(b []byte) uint64 {
	return uint64(b[22])<<56 | uint64(b[23])<<48 | uint64(b[24])<<40 | uint64(b[25])<<32 | uint64(b[26])<<24 | uint64(b[27])<<16 | uint64(b[28])<<8 | uint64(b[29])
}
func (p *holeClientPath) WriteTo(b []byte, _ net.Addr) (int, error) { return p.send(holeData, b) }
func (p *holeClientPath) PeerAddr() net.Addr                        { return p.remote }
func (p *holeClientPath) LocalAddr() net.Addr                       { return p.conn.LocalAddr() }
func (p *holeClientPath) SetReadBuffer(n int) error                 { return holeReadBuffer(p.conn, n) }
func (p *holeClientPath) SetWriteBuffer(n int) error                { return holeWriteBuffer(p.conn, n) }
func (p *holeClientPath) SetDeadline(t time.Time) error             { return p.conn.SetDeadline(t) }
func (p *holeClientPath) SetReadDeadline(t time.Time) error         { return p.conn.SetReadDeadline(t) }
func (p *holeClientPath) SetWriteDeadline(t time.Time) error        { return p.conn.SetWriteDeadline(t) }
func (p *holeClientPath) Close() error                              { return p.conn.Close() }

func openHolePath(ctx context.Context, platform Platform, g *holeGrant, side int) (*holeClientPath, error) {
	address, err := platform.ResolveUDP(ctx, g.Endpoint)
	if err != nil {
		return nil, err
	}
	network, local := "udp4", "0.0.0.0:0"
	if address.IP.To4() == nil {
		network, local = "udp6", "[::]:0"
	}
	conn, err := platform.ListenPacket(ctx, network, local)
	if err != nil {
		return nil, err
	}
	auth, err := newHoleAuth(g)
	if err != nil {
		conn.Close()
		return nil, err
	}
	other, _ := newHoleAuth(g)
	p := &holeClientPath{conn: conn, remote: address, side: side, readAuth: auth, writeAuth: other}
	stop := context.AfterFunc(ctx, func() { _ = conn.SetReadDeadline(time.Now()) })
	defer stop()
	for ctx.Err() == nil {
		if _, err = p.send(holeRegister, nil); err != nil {
			break
		}
		_ = conn.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
		n, a, e := conn.ReadFrom(p.readBuffer[:])
		if e != nil {
			if timeout, ok := e.(net.Error); ok && timeout.Timeout() {
				continue
			}
			err = e
			break
		}
		b := p.readBuffer[:n]
		if a.String() != address.String() || !auth.valid(b) || int(b[5]) != side {
			continue
		}
		if b[4] == holeChallenge && n == holeHeader+16 {
			_, err = p.send(holeConfirm, b[holeHeader:])
			if err != nil {
				break
			}
		}
		if b[4] == holeAck {
			_ = conn.SetReadDeadline(time.Time{})
			return p, nil
		}
	}
	conn.Close()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return nil, err
}

func (p *icePeer) attemptHole(a *iceAttempt) {
	defer a.cancel()
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
	g := a.ready.Hole
	if g == nil || g.Error != "" {
		message := "Hole 中继路径不可用"
		code := "hole_unavailable"
		if g != nil {
			message = g.Error
			if g.ErrorCode != "" {
				code = g.ErrorCode
			}
		}
		p.failed(a, &Fault{Code: code, Message: message})
		return
	}
	// Signaling failures must interrupt registration and the QUIC handshake too.
	failureDone := make(chan struct{})
	go func() {
		defer close(failureDone)
		select {
		case err := <-a.failed:
			p.failed(a, err)
			a.cancel()
		case <-a.ctx.Done():
		}
	}()
	defer func() { a.cancel(); <-failureDone }()
	platform, err := p.coordinator.pin(a.ctx)
	if err != nil {
		p.failed(a, err)
		return
	}
	handshakeCtx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
	defer cancel()
	side := 0
	if p.coordinator.deviceID() == g.Devices[1] {
		side = 1
	}
	packet, err := openHolePath(handshakeCtx, platform, g, side)
	if err != nil {
		p.failed(a, err)
		return
	}
	defer packet.Close()
	transport := &quic.Transport{Conn: packet}
	defer transport.Close()
	heartbeatCtx, stopHeartbeat := context.WithCancel(a.ctx)
	defer stopHeartbeat()
	done := make(chan struct{})
	defer func() { stopHeartbeat(); <-done }()
	go func() {
		defer close(done)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				_, _ = packet.send(holeRegister, nil)
			}
		}
	}()
	tlsConfig := p.coordinator.identity.Clone()
	tlsConfig.NextProtos = []string{iceALPN}
	tlsConfig.ClientAuth = tls.RequireAnyClientCert
	tlsConfig.InsecureSkipVerify = true
	var mismatch atomic.Bool
	tlsConfig.VerifyConnection = func(state tls.ConnectionState) error {
		if len(state.PeerCertificates) > 0 {
			sum := sha256.Sum256(state.PeerCertificates[0].Raw)
			if hex.EncodeToString(sum[:]) == a.ready.PeerFingerprint {
				return nil
			}
		}
		mismatch.Store(true)
		return &Fault{Code: "peer_identity_mismatch", Message: "Hole 路径端到端证书校验失败"}
	}
	var conn *quic.Conn
	if p.coordinator.deviceID() == a.ready.InitiatorID {
		conn, err = transport.Dial(handshakeCtx, packet.PeerAddr(), tlsConfig, iceQUICConfig("hole"))
	} else {
		var listener *quic.Listener
		listener, err = transport.Listen(tlsConfig, iceQUICConfig("hole"))
		if err == nil {
			defer listener.Close()
			conn, err = listener.Accept(handshakeCtx)
		}
	}
	if err != nil {
		if mismatch.Load() {
			err = &Fault{Code: "peer_identity_mismatch", Message: "Hole 路径端到端证书校验失败"}
		}
		p.failed(a, err)
		return
	}
	defer conn.CloseWithError(0, "Hole path ended")
	datagrams := conn.ConnectionState().SupportsDatagrams
	if !datagrams.Local || !datagrams.Remote {
		p.failed(a, &Fault{Code: "protocol_mismatch", Message: "Hole 路径需要 QUIC DATAGRAM"})
		return
	}
	mux, err := newMuxPeer(a.ctx, p, conn, a.ready)
	if err != nil {
		p.failed(a, err)
		return
	}
	cleanup := sync.OnceFunc(func() { _ = transport.Close(); _ = packet.Close() })
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
	p.stats.PathType, p.stats.LocalType, p.stats.RemoteType = "hole", "hole", "hole"
	p.stats.RTTMS = 0
	p.stats.RelayDevice, p.stats.RelayEndpoint, p.stats.RelayAccessAddress = g.Device, g.Endpoint, packet.remote.String()
	p.stats.LocalAddress, p.stats.RemoteAddress = packet.LocalAddr().String(), packet.remote.String()
	p.stats.LocalRelayProtocol, p.stats.RelayProtocol = "udp", "udp"
	p.stats.AddressFamily = "IPv4"
	if packet.remote.IP.To4() == nil {
		p.stats.AddressFamily = "IPv6"
	}
	p.stats.ConnectMS = time.Since(started).Milliseconds()
	p.stats.RelayState = "active"
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
	case <-a.ctx.Done():
	case <-mux.ctx.Done():
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
	}
	p.mu.Unlock()
	if current && p.ctx.Err() == nil {
		p.stateChanged()
		p.restart("direct", "path_lost")
	}
}
