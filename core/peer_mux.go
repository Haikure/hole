package core

import (
	"context"
	"encoding/binary"
	"errors"
	"github.com/quic-go/quic-go"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const (
	muxControl byte = 1
	muxSession byte = 2
)

var muxMagic = [4]byte{'H', 'M', 'X', 1}

type muxHello struct {
	Profile            string `json:"profile"`
	TransportID        string `json:"transport_id"`
	Generation         uint64 `json:"transport_generation,string"`
	LocalRuntime       string `json:"local_runtime"`
	RemoteRuntime      string `json:"remote_runtime"`
	Version            int    `json:"session_version"`
	LocalRelayProtocol string `json:"local_relay_protocol,omitempty"`
	// RelayProtocol keeps the handshake compatible with peers predating the
	// explicit local_relay_protocol name.
	RelayProtocol string `json:"relay_protocol,omitempty"`
}
type mappingControl struct {
	Type      string `json:"type"`
	Channel   uint32 `json:"channel_id"`
	Key       string `json:"map_key"`
	MappingID string `json:"mapping_id"`
	Protocol  string `json:"protocol"`
	Status    string `json:"status,omitempty"`
}
type receivedPacket struct {
	id   sessionID
	data []byte
}
type muxPeer struct {
	owner       *icePeer
	conn        *quic.Conn
	ctx         context.Context
	cancel      context.CancelFunc
	control     *quic.Stream
	controlMu   sync.Mutex
	mu          sync.Mutex
	channels    map[uint32]*muxChannel
	byMapping   map[string]*muxChannel
	nextChannel uint32
	lastRemote  uint32
	closing     bool
	workers     sync.WaitGroup
	streamSlots chan struct{}
	queuedBytes atomic.Int64
	dropped     atomic.Uint64
	ready       iceSignalMessage
}
type muxChannel struct {
	peer      *muxPeer
	id        uint32
	mapping   peerMappingRecord
	ctx       context.Context
	cancel    context.CancelFunc
	ack       chan error
	packets   chan receivedPacket
	closeOnce sync.Once
	ready     atomic.Bool
}

func validRelayProtocol(protocol string) bool {
	return protocol == "" || protocol == "udp" || protocol == "tcp" || protocol == "tls"
}

func writeMuxPrefix(stream *quic.Stream, kind byte, id uint32) error {
	var b [9]byte
	copy(b[:4], muxMagic[:])
	b[4] = kind
	binary.BigEndian.PutUint32(b[5:], id)
	return writeFull(stream, b[:])
}
func readMuxPrefix(stream *quic.Stream) (byte, uint32, error) {
	var b [9]byte
	if _, e := io.ReadFull(stream, b[:]); e != nil {
		return 0, 0, e
	}
	if string(b[:4]) != string(muxMagic[:]) {
		return 0, 0, errors.New("invalid mux envelope")
	}
	return b[4], binary.BigEndian.Uint32(b[5:]), nil
}

func newMuxPeer(ctx context.Context, owner *icePeer, conn *quic.Conn, ready iceSignalMessage) (*muxPeer, error) {
	ctx, cancel := context.WithCancel(ctx)
	p := &muxPeer{owner: owner, conn: conn, ctx: ctx, cancel: cancel, channels: map[uint32]*muxChannel{}, byMapping: map[string]*muxChannel{}, streamSlots: make(chan struct{}, 64), ready: ready}
	owner.mu.Lock()
	localRelayProtocol := owner.stats.LocalRelayProtocol
	if localRelayProtocol == "" {
		// A host may have populated only the compatibility field.
		localRelayProtocol = owner.stats.RelayProtocol
	}
	owner.mu.Unlock()
	initiator := ready.InitiatorID == owner.coordinator.deviceID()
	p.nextChannel = 2
	if initiator {
		p.nextChannel = 1
	}
	hsCtx, stop := context.WithTimeout(ctx, sessionHandshakeTimeout)
	defer stop()
	var stream *quic.Stream
	var err error
	if initiator {
		stream, err = conn.OpenStreamSync(hsCtx)
	} else {
		stream, err = conn.AcceptStream(hsCtx)
	}
	if err != nil {
		cancel()
		return nil, err
	}
	p.control = stream
	_ = stream.SetDeadline(time.Now().Add(sessionHandshakeTimeout))
	stopIO := context.AfterFunc(hsCtx, func() { stream.CancelRead(0); stream.CancelWrite(0) })
	defer stopIO()
	hello := muxHello{Profile: ProfileICE, TransportID: ready.TransportID, Generation: ready.TransportGeneration, LocalRuntime: owner.coordinator.runtimeID, RemoteRuntime: ready.PeerRuntimeID, Version: sessionProtocol, LocalRelayProtocol: localRelayProtocol, RelayProtocol: localRelayProtocol}
	var remote muxHello
	if initiator {
		err = writeMuxPrefix(stream, muxControl, 0)
		if err == nil {
			err = writeSessionJSON(stream, hello)
		}
		if err == nil {
			err = readSessionJSON(stream, &remote)
		}
	} else {
		kind, id, e := readMuxPrefix(stream)
		err = e
		if err == nil && (kind != muxControl || id != 0) {
			err = errors.New("first stream is not transport control")
		}
		if err == nil {
			err = readSessionJSON(stream, &remote)
		}
	}
	remoteRelayProtocol := remote.LocalRelayProtocol
	if remoteRelayProtocol == "" {
		remoteRelayProtocol = remote.RelayProtocol
	}
	if err == nil && remote.LocalRelayProtocol != "" && remote.RelayProtocol != "" && remote.LocalRelayProtocol != remote.RelayProtocol {
		err = &Fault{Code: "protocol_mismatch", Message: "对端本机中继接入类型字段不一致"}
	}
	if err == nil && (remote.Profile != ProfileICE || remote.TransportID != ready.TransportID || remote.Generation != ready.TransportGeneration || remote.LocalRuntime != ready.PeerRuntimeID || remote.RemoteRuntime != owner.coordinator.runtimeID || remote.Version != sessionProtocol || !validRelayProtocol(remoteRelayProtocol)) {
		err = &Fault{Code: "protocol_mismatch", Message: "对端传输身份或协议与信令不一致"}
	}
	if err == nil {
		owner.mu.Lock()
		owner.stats.RemoteRelayProtocol = remoteRelayProtocol
		owner.mu.Unlock()
	}
	if err == nil && !initiator {
		err = writeSessionJSON(stream, hello)
	}
	if err != nil {
		cancel()
		stream.CancelRead(0)
		stream.CancelWrite(0)
		return nil, err
	}
	_ = stream.SetDeadline(time.Time{})
	p.spawn(p.readControl)
	p.spawn(p.readStreams)
	p.spawn(p.readDatagrams)
	return p, nil
}
func (p *muxPeer) spawn(fn func()) {
	p.mu.Lock()
	if p.closing {
		p.mu.Unlock()
		return
	}
	p.workers.Add(1)
	p.mu.Unlock()
	go func() { defer p.workers.Done(); fn() }()
}
func (p *muxPeer) stop() {
	p.mu.Lock()
	if p.closing {
		p.mu.Unlock()
		return
	}
	p.closing = true
	list := make([]*muxChannel, 0, len(p.channels))
	for _, c := range p.channels {
		list = append(list, c)
	}
	p.mu.Unlock()
	p.cancel()
	p.control.CancelRead(0)
	p.control.CancelWrite(0)
	_ = p.conn.CloseWithError(0, "peer path replaced")
	for _, c := range list {
		c.close(false)
	}
}
func (p *muxPeer) close() { p.stop(); p.workers.Wait() }
func (p *muxPeer) writeControl(m mappingControl) error {
	p.controlMu.Lock()
	defer p.controlMu.Unlock()
	if p.ctx.Err() != nil {
		return p.ctx.Err()
	}
	_ = p.control.SetWriteDeadline(time.Now().Add(sessionHandshakeTimeout))
	return writeSessionJSON(p.control, m)
}
func (p *muxPeer) mapping(id string) (peerMappingRecord, bool) { return p.owner.mapping(id) }
func (p *muxPeer) channel(record peerMappingRecord, id uint32) *muxChannel {
	ctx, cancel := context.WithCancel(p.ctx)
	return &muxChannel{peer: p, id: id, mapping: record, ctx: ctx, cancel: cancel, ack: make(chan error, 1), packets: make(chan receivedPacket, 8)}
}
func (p *muxPeer) readControl() {
	defer p.stop()
	for {
		var m mappingControl
		if readSessionJSON(p.control, &m) != nil {
			return
		}
		switch m.Type {
		case "OPEN_MAPPING":
			record, ok := p.mapping(m.MappingID)
			valid := ok && record.Key == m.Key && record.Service.Protocol == m.Protocol && record.Provider == p.owner.coordinator.deviceID() && record.Consumer == p.ready.PeerDevice && p.owner.allowNew()
			p.mu.Lock()
			parity := (p.nextChannel + 1) % 2
			if m.Channel == 0 || m.Channel%2 != parity || m.Channel <= p.lastRemote || len(p.channels) >= maxPeerChannels {
				valid = false
			}
			if m.Channel > p.lastRemote {
				p.lastRemote = m.Channel
			}
			status := "mapping_not_authorized"
			var replaced *muxChannel
			if valid {
				if old := p.byMapping[record.ID]; old != nil {
					replaced = old
					old.cancel()
					delete(p.channels, old.id)
				}
				c := p.channel(record, m.Channel)
				c.ready.Store(true)
				p.channels[m.Channel] = c
				p.byMapping[record.ID] = c
				status = "ok"
			}
			p.mu.Unlock()
			if replaced != nil {
				replaced.close(false)
			}
			if valid {
				p.owner.emitMapping(m.MappingID, "active", m.Protocol)
			}
			if p.writeControl(mappingControl{Type: "MAPPING_ACK", Channel: m.Channel, Key: m.Key, MappingID: m.MappingID, Protocol: m.Protocol, Status: status}) != nil {
				return
			}
		case "MAPPING_ACK":
			p.mu.Lock()
			c := p.channels[m.Channel]
			p.mu.Unlock()
			if c == nil || c.mapping.Key != m.Key || c.mapping.ID != m.MappingID || c.mapping.Service.Protocol != m.Protocol {
				return
			}
			var err error
			if m.Status != "ok" {
				err = errors.New(m.Status)
			}
			select {
			case c.ack <- err:
			default:
			}
		case "CLOSE_MAPPING":
			p.mu.Lock()
			c := p.channels[m.Channel]
			p.mu.Unlock()
			if c != nil {
				c.close(false)
			}
		default:
			return
		}
	}
}

func (p *muxPeer) syncMappings() {
	p.mu.Lock()
	list := make([]*muxChannel, 0, len(p.channels))
	for _, c := range p.channels {
		list = append(list, c)
	}
	p.mu.Unlock()
	for _, c := range list {
		record, ok := p.mapping(c.mapping.ID)
		if !ok || record != c.mapping {
			c.close(true)
		}
	}
	for _, record := range p.owner.records() {
		if record.Consumer != p.owner.coordinator.deviceID() || !p.owner.allowNew() {
			continue
		}
		p.mu.Lock()
		if p.closing || p.byMapping[record.ID] != nil || len(p.channels) >= maxPeerChannels || p.nextChannel > ^uint32(0)-2 {
			p.mu.Unlock()
			continue
		}
		c := p.channel(record, p.nextChannel)
		p.nextChannel += 2
		p.channels[c.id] = c
		p.byMapping[record.ID] = c
		p.mu.Unlock()
		p.spawn(func() { p.openMapping(c) })
	}
}
func (p *muxPeer) openMapping(c *muxChannel) {
	record := c.mapping
	defer c.close(true)
	if e := p.writeControl(mappingControl{Type: "OPEN_MAPPING", Channel: c.id, Key: record.Key, MappingID: record.ID, Protocol: record.Service.Protocol}); e != nil {
		return
	}
	timer := time.NewTimer(sessionHandshakeTimeout)
	defer timer.Stop()
	select {
	case <-c.ctx.Done():
		return
	case <-timer.C:
		return
	case err := <-c.ack:
		if err != nil {
			return
		}
	}
	if !p.owner.allowNew() {
		return
	}
	t := tunnel{id: record.ID, protocol: record.Service.Protocol, expose: record.Expose, peer: p.ready.PeerDevice}
	client, err := p.owner.coordinator.sessions.consumer(t, p.ready.PeerFingerprint)
	if err != nil {
		p.owner.mappingError(record.ID, err)
		return
	}
	transport, err := client.bind(c.ctx, c)
	if err != nil {
		return
	}
	defer client.unbind(transport)
	c.ready.Store(true)
	p.owner.emitMapping(record.ID, "active", record.Service.Protocol)
	if record.Service.Protocol == "udp" {
		client.serveUDP(transport)
	} else {
		<-transport.ctx.Done()
	}
}
func (p *muxPeer) readStreams() {
	for {
		stream, err := p.conn.AcceptStream(p.ctx)
		if err != nil {
			return
		}
		select {
		case p.streamSlots <- struct{}{}:
			p.spawn(func() { defer func() { <-p.streamSlots }(); p.serveStream(stream) })
		default:
			stream.CancelRead(1)
			stream.CancelWrite(1)
		}
	}
}
func (p *muxPeer) serveStream(stream *quic.Stream) {
	_ = stream.SetDeadline(time.Now().Add(sessionHandshakeTimeout))
	stop := context.AfterFunc(p.ctx, func() { stream.CancelRead(0); stream.CancelWrite(0) })
	defer stop()
	kind, id, err := readMuxPrefix(stream)
	if err != nil || kind != muxSession {
		stream.CancelRead(1)
		stream.CancelWrite(1)
		return
	}
	p.mu.Lock()
	channel := p.channels[id]
	p.mu.Unlock()
	if channel == nil || channel.ctx.Err() != nil || channel.mapping.Provider != p.owner.coordinator.deviceID() || !p.owner.authorized(channel.mapping) {
		stream.CancelRead(1)
		stream.CancelWrite(1)
		return
	}
	stopChannel := context.AfterFunc(channel.ctx, func() { stream.CancelRead(0); stream.CancelWrite(0) })
	defer stopChannel()
	r := channel.mapping
	facade := &Agent{provide: map[string]Provide{r.ID: {ID: r.ID, Service: r.Service}}, sessions: p.owner.coordinator.sessions, emit: p.owner.coordinator.emit,
		peerFingerprints: map[peerMapping]string{{mappingID: r.ID, device: p.ready.PeerDevice}: p.ready.PeerFingerprint}}
	facade.handleProviderStream(channel.ctx, channel, stream)
}
func (p *muxPeer) readDatagrams() {
	var assembler udpReassembler
	for {
		data, err := p.conn.ReceiveDatagram(p.ctx)
		if err != nil {
			return
		}
		if len(data) < 4 {
			p.dropped.Add(1)
			continue
		}
		id := binary.BigEndian.Uint32(data[:4])
		p.mu.Lock()
		c := p.channels[id]
		p.mu.Unlock()
		if c == nil || c.ctx.Err() != nil || c.mapping.Service.Protocol != "udp" || !p.owner.authorized(c.mapping) {
			p.dropped.Add(1)
			continue
		}
		frame, err := parseUDPFragment(data[4:])
		if err != nil {
			p.dropped.Add(1)
			continue
		}
		frame.key.channel = id
		packet, complete, err := assembler.push(frame, time.Now())
		if err != nil {
			p.dropped.Add(1)
			continue
		}
		if !complete {
			continue
		}
		size := int64(len(packet))
		if p.queuedBytes.Add(size) > udpAssemblyBudget {
			p.queuedBytes.Add(-size)
			p.dropped.Add(1)
			continue
		}
		p.mu.Lock()
		if p.channels[id] != c || c.ctx.Err() != nil {
			p.queuedBytes.Add(-size)
		} else {
			select {
			case c.packets <- receivedPacket{frame.key.session, packet}:
			default:
				p.queuedBytes.Add(-size)
				p.dropped.Add(1)
			}
		}
		p.mu.Unlock()
	}
}
func (c *muxChannel) Context() context.Context              { return c.ctx }
func (c *muxChannel) ConnectionState() quic.ConnectionState { return c.peer.conn.ConnectionState() }
func (c *muxChannel) RemoteAddr() net.Addr                  { return c.peer.conn.RemoteAddr() }
func (c *muxChannel) LocalAddr() net.Addr                   { return c.peer.conn.LocalAddr() }
func (c *muxChannel) CloseWithError(quic.ApplicationErrorCode, string) error {
	c.close(true)
	return nil
}
func (c *muxChannel) close(notify bool) {
	c.closeOnce.Do(func() {
		c.cancel()
		p := c.peer
		p.mu.Lock()
		delete(p.channels, c.id)
		if p.byMapping[c.mapping.ID] == c {
			delete(p.byMapping, c.mapping.ID)
		}
		p.mu.Unlock()
		for {
			select {
			case v := <-c.packets:
				p.queuedBytes.Add(-int64(len(v.data)))
			default:
				goto drained
			}
		}
	drained:
		p.owner.stateChanged()
		if notify && p.ctx.Err() == nil {
			p.spawn(func() {
				_ = p.writeControl(mappingControl{Type: "CLOSE_MAPPING", Channel: c.id, Key: c.mapping.Key, MappingID: c.mapping.ID})
			})
		}
	})
}
func (c *muxChannel) OpenStreamSync(ctx context.Context) (*quic.Stream, error) {
	if c.ctx.Err() != nil {
		return nil, c.ctx.Err()
	}
	stream, err := c.peer.conn.OpenStreamSync(ctx)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { stream.CancelRead(0); stream.CancelWrite(0) })
	defer stop()
	_ = stream.SetDeadline(time.Now().Add(sessionHandshakeTimeout))
	if err := writeMuxPrefix(stream, muxSession, c.id); err != nil {
		stream.CancelRead(0)
		stream.CancelWrite(0)
		return nil, err
	}
	return stream, nil
}
func (c *muxChannel) SendDatagram(data []byte) error {
	if c.ctx.Err() != nil {
		return c.ctx.Err()
	}
	wire := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(wire[:4], c.id)
	copy(wire[4:], data)
	err := c.peer.conn.SendDatagram(wire)
	var large *quic.DatagramTooLargeError
	if errors.As(err, &large) {
		return &quic.DatagramTooLargeError{MaxDatagramPayloadSize: large.MaxDatagramPayloadSize - 4}
	}
	return err
}
func (c *muxChannel) ReceiveDatagram(context.Context) ([]byte, error) {
	return nil, errors.New("multiplexed datagrams require the central dispatcher")
}
func (c *muxChannel) ReceiveSessionPacket(ctx context.Context) (sessionID, []byte, error) {
	select {
	case <-ctx.Done():
		return sessionID{}, nil, ctx.Err()
	case <-c.ctx.Done():
		return sessionID{}, nil, c.ctx.Err()
	case p := <-c.packets:
		c.peer.queuedBytes.Add(-int64(len(p.data)))
		return p.id, p.data, nil
	}
}
