package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"sync"
	"time"

	"github.com/apernet/quic-go"
)

const peerRelayVersion = "peer-relay-v1"
const maxPeerRelayPaths = 8

var peerRelayMagic = []byte{'H', 'P', 'R', 1}

type peerRelayLink struct {
	ID         string `json:"id"`
	Generation uint64 `json:"generation,string"`
}

// Grants bind both existing direct transports, their runtimes and one opaque
// route to an authorized end-to-end transport generation. No forwarding chains.
type peerRelayGrant struct {
	Route       string           `json:"route"`
	Device      string           `json:"device"`
	Runtime     string           `json:"runtime"`
	Devices     [2]string        `json:"devices"`
	Runtimes    [2]string        `json:"runtimes"`
	Links       [2]peerRelayLink `json:"links"`
	TransportID string           `json:"transport_id"`
	Generation  uint64           `json:"generation,string"`
	LeaseUntil  int64            `json:"lease_until,string"`
}

func (g peerRelayGrant) valid() bool {
	_, err := parseSessionID(g.Route)
	return err == nil && g.Device != "" && g.Runtime != "" && g.Devices[0] != "" && g.Devices[1] != "" &&
		g.Devices[0] != g.Devices[1] && g.Device != g.Devices[0] && g.Device != g.Devices[1] &&
		g.Runtimes[0] != "" && g.Runtimes[1] != "" && g.TransportID != "" && g.Generation > 0 &&
		g.Links[0].ID != "" && g.Links[1].ID != "" && g.Links[0].ID != g.Links[1].ID &&
		g.Links[0].Generation > 0 && g.Links[1].Generation > 0 && g.LeaseUntil > time.Now().UnixMilli()
}

func (c *iceCoordinator) validatePeerRelayReady(m iceSignalMessage) error {
	g := m.PeerRelay
	if g == nil || !g.valid() || m.PeerRelayVersion != peerRelayVersion || m.Profile != ProfileICE || m.RelayRequired ||
		g.TransportID != m.TransportID || g.Generation != m.TransportGeneration || m.RelayRound == nil || *m.RelayRound < 0 || m.RelayPairing != relayPairingAll {
		return &Fault{Code: "protocol_mismatch", Message: "节点中继授权无效"}
	}
	i := 0
	if g.Devices[1] == c.deviceID() {
		i = 1
	}
	if g.Devices[i] != c.deviceID() || g.Runtimes[i] != c.runtimeID || g.Devices[1-i] != m.PeerDevice || g.Runtimes[1-i] != m.PeerRuntimeID {
		return &Fault{Code: "protocol_mismatch", Message: "节点中继身份与设备对不一致"}
	}
	return nil
}

func (c *iceCoordinator) handlePeerRelayGrant(m iceSignalMessage) error {
	g := m.PeerRelay
	if g == nil {
		return nil
	}
	id, err := parseSessionID(g.Route)
	if err != nil {
		return nil
	}
	c.peerRelayMu.Lock()
	defer c.peerRelayMu.Unlock()
	if m.Type == "peer_relay_revoke" {
		delete(c.peerRelayGrants, id)
		return nil
	}
	if m.PeerRelayVersion != peerRelayVersion || !g.valid() || g.Device != c.deviceID() || g.Runtime != c.runtimeID ||
		g.TransportID != m.TransportID || g.Generation != m.TransportGeneration {
		return nil
	}
	if c.peerRelayGrants == nil {
		c.peerRelayGrants = make(map[sessionID]peerRelayGrant)
	}
	for key, grant := range c.peerRelayGrants {
		if grant.LeaseUntil <= time.Now().UnixMilli() || grant.TransportID == g.TransportID {
			delete(c.peerRelayGrants, key)
		}
	}
	if len(c.peerRelayGrants) < maxPeerRelayPaths {
		c.peerRelayGrants[id] = *g
	}
	return nil
}

func (c *iceCoordinator) directRelayMux(link peerRelayLink, device, runtime string) *muxPeer {
	c.mu.Lock()
	p := c.peers[link.ID]
	c.mu.Unlock()
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	a := p.active
	if p.closed || !p.online || p.ready.LeaseUntil <= time.Now().UnixMilli() || a == nil ||
		a.ready.TransportGeneration != link.Generation || p.ready.TransportGeneration != link.Generation ||
		a.ready.PeerDevice != device || a.ready.PeerRuntimeID != runtime || !a.mux.peerRelay || a.mux.ctx.Err() != nil ||
		(p.stats.PathType != "direct" && p.stats.PathType != "fixed") {
		return nil
	}
	return a.mux
}

func (p *muxPeer) queuePeerRelay(data []byte) {
	select {
	case <-p.ctx.Done():
	case p.peerRelaySend <- data:
	default:
		p.dropped.Add(1)
	}
}

func (p *muxPeer) writePeerRelay() {
	for {
		select {
		case <-p.ctx.Done():
			return
		case data := <-p.peerRelaySend:
			if p.conn.SendDatagram(data) != nil {
				p.dropped.Add(1)
			}
		}
	}
}

func (p *muxPeer) receivePeerRelay(data []byte) bool {
	if !bytes.HasPrefix(data, peerRelayMagic) {
		return false
	}
	if !p.peerRelay || len(data) > udpInitialFrameSize {
		return true
	}
	f, err := parseUDPFragment(data[4:])
	if err != nil || f.total > 1200 {
		return true
	}
	c := p.owner.coordinator
	c.peerRelayMu.Lock()
	path := c.peerRelayPaths[f.key.session]
	grant, granted := c.peerRelayGrants[f.key.session]
	c.peerRelayMu.Unlock()
	if path != nil {
		if path.mux == p && c.directRelayMux(path.link, path.grant.Device, path.grant.Runtime) == p {
			path.receive(f)
		}
		return true
	}
	if !granted || grant.LeaseUntil <= time.Now().UnixMilli() {
		return true
	}
	for i := range grant.Devices {
		if c.directRelayMux(grant.Links[i], grant.Devices[i], grant.Runtimes[i]) != p {
			continue
		}
		other := c.directRelayMux(grant.Links[1-i], grant.Devices[1-i], grant.Runtimes[1-i])
		if other != nil {
			other.queuePeerRelay(data)
		} else {
			// Report once per grant; the Worker advances both endpoints together.
			c.peerRelayMu.Lock()
			delete(c.peerRelayGrants, f.key.session)
			c.peerRelayMu.Unlock()
			m := signalMessage("peer_relay_failed")
			m.TransportID, m.TransportGeneration = grant.TransportID, grant.Generation
			_ = c.send(m)
		}
		break
	}
	return true
}

type peerRelayPath struct {
	ctx                         context.Context
	cancel                      context.CancelFunc
	owner                       *icePeer
	grant                       peerRelayGrant
	id                          sessionID
	link                        peerRelayLink
	mux                         *muxPeer
	packets                     chan []byte
	mu                          sync.Mutex
	assembler                   udpReassembler
	seq                         uint64
	readDeadline, writeDeadline time.Time
	changed                     chan struct{}
}

func (p *peerRelayPath) receive(f udpFragment) {
	p.mu.Lock()
	packet, complete, err := p.assembler.push(f, time.Now())
	p.mu.Unlock()
	if complete && err == nil {
		select {
		case p.packets <- packet:
		default:
			p.mux.dropped.Add(1)
		}
	}
}

func (p *peerRelayPath) ReadFrom(b []byte) (int, net.Addr, error) {
	for {
		p.mu.Lock()
		deadline, changed := p.readDeadline, p.changed
		p.mu.Unlock()
		var timeout <-chan time.Time
		var timer *time.Timer
		if !deadline.IsZero() {
			if !time.Now().Before(deadline) {
				return 0, nil, os.ErrDeadlineExceeded
			}
			timer = time.NewTimer(time.Until(deadline))
			timeout = timer.C
		}
		select {
		case <-p.ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return 0, nil, net.ErrClosed
		case <-timeout:
			return 0, nil, os.ErrDeadlineExceeded
		case <-changed:
			if timer != nil {
				timer.Stop()
			}
		case packet := <-p.packets:
			if timer != nil {
				timer.Stop()
			}
			return copy(b, packet), p.PeerAddr(), nil
		}
	}
}

func (p *peerRelayPath) WriteTo(b []byte, _ net.Addr) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ctx.Err() != nil {
		return 0, net.ErrClosed
	}
	if !p.writeDeadline.IsZero() && !time.Now().Before(p.writeDeadline) {
		return 0, os.ErrDeadlineExceeded
	}
	if len(b) > 1200 {
		return 0, errors.New("peer relay packet exceeds MTU")
	}
	p.seq++
	frameSize := udpInitialFrameSize - len(peerRelayMagic)
	err := sendUDPFragments(p.ctx, func(frame []byte) error {
		p.mux.queuePeerRelay(append(append([]byte{}, peerRelayMagic...), frame...))
		return nil
	}, p.id, p.seq, &frameSize, b)
	if err != nil {
		return 0, err
	}
	return len(b), nil
}

func (p *peerRelayPath) PeerAddr() net.Addr { return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 2), Port: 1} }
func (p *peerRelayPath) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}
}
func (p *peerRelayPath) Close() error {
	p.cancel()
	c := p.owner.coordinator
	c.peerRelayMu.Lock()
	if c.peerRelayPaths[p.id] == p {
		delete(c.peerRelayPaths, p.id)
	}
	c.peerRelayMu.Unlock()
	return nil
}
func (p *peerRelayPath) setDeadline(t time.Time, read, write bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if read {
		p.readDeadline = t
	}
	if write {
		p.writeDeadline = t
	}
	close(p.changed)
	p.changed = make(chan struct{})
	return nil
}
func (p *peerRelayPath) SetDeadline(t time.Time) error      { return p.setDeadline(t, true, true) }
func (p *peerRelayPath) SetReadDeadline(t time.Time) error  { return p.setDeadline(t, true, false) }
func (p *peerRelayPath) SetWriteDeadline(t time.Time) error { return p.setDeadline(t, false, true) }

func (p *icePeer) attemptPeerRelay(a *iceAttempt) {
	defer a.cancel()
	g := *a.ready.PeerRelay
	i := 0
	if g.Devices[1] == p.coordinator.deviceID() {
		i = 1
	}
	c := p.coordinator
	mux := c.directRelayMux(g.Links[i], g.Device, g.Runtime)
	if mux == nil {
		p.failed(a, errors.New("peer relay direct link unavailable"))
		return
	}
	id, _ := parseSessionID(g.Route)
	ctx, cancel := context.WithCancel(a.ctx)
	packet := &peerRelayPath{ctx: ctx, cancel: cancel, owner: p, grant: g, id: id, link: g.Links[i], mux: mux, packets: make(chan []byte, 64), changed: make(chan struct{})}
	c.peerRelayMu.Lock()
	if c.peerRelayPaths == nil {
		c.peerRelayPaths = make(map[sessionID]*peerRelayPath)
	}
	if len(c.peerRelayPaths) >= maxPeerRelayPaths {
		c.peerRelayMu.Unlock()
		cancel()
		p.failed(a, errors.New("peer relay path limit"))
		return
	}
	c.peerRelayPaths[id] = packet
	c.peerRelayMu.Unlock()
	defer packet.Close()
	// Interrupt handshakes and active sessions as soon as their outer leg or
	// authorization disappears. Shutdown also unblocks PacketConn.ReadFrom.
	done := make(chan struct{})
	defer func() { cancel(); <-done }()
	go func() {
		defer close(done)
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-a.failed:
				cancel()
				return
			case <-mux.ctx.Done():
				cancel()
				return
			case <-ticker.C:
				if c.directRelayMux(packet.link, g.Device, g.Runtime) != mux || !p.allowRelayPath(a) {
					cancel()
					return
				}
			}
		}
	}()
	started := time.Now()
	transport := &quic.Transport{Conn: packet}
	defer transport.Close()
	tlsConfig := c.identity.Clone()
	tlsConfig.NextProtos, tlsConfig.ClientAuth, tlsConfig.InsecureSkipVerify = []string{iceALPN}, tls.RequireAnyClientCert, true
	tlsConfig.VerifyConnection = func(state tls.ConnectionState) error {
		if len(state.PeerCertificates) > 0 {
			sum := sha256.Sum256(state.PeerCertificates[0].Raw)
			if hex.EncodeToString(sum[:]) == a.ready.PeerFingerprint {
				return nil
			}
		}
		return errors.New("peer relay endpoint certificate mismatch")
	}
	hs, stop := context.WithTimeout(ctx, 8*time.Second)
	defer stop()
	var conn *quic.Conn
	var err error
	if c.deviceID() == a.ready.InitiatorID {
		conn, err = transport.Dial(hs, packet.PeerAddr(), tlsConfig, iceQUICConfig("peer_relay"))
	} else {
		var listener *quic.Listener
		listener, err = transport.Listen(tlsConfig, iceQUICConfig("peer_relay"))
		if err == nil {
			defer listener.Close()
			conn, err = listener.Accept(hs)
		}
	}
	if err != nil {
		p.failed(a, err)
		return
	}
	defer conn.CloseWithError(0, "peer relay ended")
	datagrams := conn.ConnectionState().SupportsDatagrams
	if !datagrams.Local || !datagrams.Remote {
		p.failed(a, errors.New("peer relay requires QUIC DATAGRAM"))
		return
	}
	inner, err := newMuxPeer(ctx, p, conn, a.ready)
	if err != nil {
		p.failed(a, err)
		return
	}
	live := &liveICETransport{ready: a.ready, mux: inner, session: conn, cancel: a.cancel, fixedCleanup: func() { _ = transport.Close(); _ = packet.Close() }}
	p.mu.Lock()
	if p.pending != a || p.closed || a.ctx.Err() != nil {
		p.mu.Unlock()
		live.close()
		return
	}
	p.stats.PathType, p.stats.LocalType, p.stats.RemoteType = "peer_relay", "peer_relay", "peer_relay"
	p.stats.RelayDevice = g.Device
	p.stats.LocalAddress, p.stats.RemoteAddress = "", ""
	p.stats.AddressFamily = ""
	p.mu.Unlock()
	p.serveICETransport(a, live, started)
}

func (p *icePeer) allowRelayPath(a *iceAttempt) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.closed && p.online && p.ready.TransportGeneration == a.ready.TransportGeneration && p.ready.LeaseUntil > time.Now().UnixMilli()
}
