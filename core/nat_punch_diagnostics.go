package core

import (
	"net"
	"sync/atomic"

	"github.com/apernet/quic-go"
	"golang.org/x/net/ipv4"
)

// NATPunchDiagnostic describes one IPv4 prediction round. UDP reads and
// authenticated non-QUIC probes are counted separately; kernel drops are unknown.
type NATPunchDiagnostic struct {
	Generation      uint64 `json:"generation,string"`
	Round           int    `json:"round"`
	Mode            string `json:"mode"`
	StartedAt       string `json:"started_at"`
	Stage           string `json:"stage"`
	LocalAddress    string `json:"local_address,omitempty"`
	MappedAddress   string `json:"mapped_address,omitempty"`
	TargetFirst     string `json:"target_first,omitempty"`
	TargetLast      string `json:"target_last,omitempty"`
	Sockets         int    `json:"sockets"`
	Dialer          bool   `json:"dialer"`
	ProbesSent      int    `json:"probes_sent"`
	ACKsSent        int    `json:"acks_sent"`
	PacketsReceived int    `json:"packets_received"`
	SocketObserved  bool   `json:"socket_observed"`
	UDPPackets      int    `json:"udp_packets_received"`
	UDPReadErrors   int    `json:"udp_read_errors"`
	UDPReadError    string `json:"udp_read_error,omitempty"`
	SourceRejected  int    `json:"source_rejected"`
	PacketRejected  int    `json:"packet_rejected"`
	ProbesReceived  int    `json:"probes_received"`
	ACKsReceived    int    `json:"acks_received"`
	ACKsVerified    int    `json:"acks_verified"`
	ReadErrors      int    `json:"read_errors"`
	ReadError       string `json:"read_error,omitempty"`
	QUICDialStarted bool   `json:"quic_dial_started"`
	QUICEstablished bool   `json:"quic_established"`
	Error           string `json:"error,omitempty"`
}

type punchSocketObservation struct {
	packets  atomic.Int64
	errors   atomic.Int64
	first    atomic.Pointer[string]
	stopping atomic.Bool
}

func (o *punchSocketObservation) received(n int, err error) {
	if n > 0 {
		o.packets.Add(int64(n))
	}
	if err != nil && !o.stopping.Load() {
		o.errors.Add(1)
		message := err.Error()
		o.first.CompareAndSwap(nil, &message)
	}
}

// Preserve quic-go's OOB, buffers, raw socket and batch capabilities. Its normal
// *UDPConn path also uses ipv4.NewPacketConn(...).ReadBatch. This observer adds
// no reads and does not change recvmsg/recvmmsg, socket options or send behavior.
type observedPunchUDPConn struct {
	*net.UDPConn
	batch *ipv4.PacketConn
	stats *punchSocketObservation
}

var _ quic.OOBCapablePacketConn = (*observedPunchUDPConn)(nil)
var _ net.Conn = (*observedPunchUDPConn)(nil)

func (c *observedPunchUDPConn) ReadBatch(messages []ipv4.Message, flags int) (int, error) {
	n, err := c.batch.ReadBatch(messages, flags)
	c.stats.received(n, err)
	return n, err
}

type observedPunchPacketConn struct {
	net.PacketConn
	stats *punchSocketObservation
}

type observedPunchRawPacketConn struct {
	*observedPunchPacketConn
	rawSocket
}

func (c *observedPunchPacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	n, from, err := c.PacketConn.ReadFrom(b)
	packets := 0
	if err == nil {
		packets = 1
	}
	c.stats.received(packets, err)
	return n, from, err
}

func observePunchSocket(pc net.PacketConn) (net.PacketConn, *punchSocketObservation) {
	o := &punchSocketObservation{}
	if udp, ok := pc.(*net.UDPConn); ok {
		return &observedPunchUDPConn{UDPConn: udp, batch: ipv4.NewPacketConn(udp), stats: o}, o
	}
	if _, oob := pc.(quic.OOBCapablePacketConn); oob {
		// Do not remove a custom platform's optimized socket capabilities.
		return pc, nil
	}
	basic := &observedPunchPacketConn{PacketConn: pc, stats: o}
	if raw, ok := pc.(rawSocket); ok {
		return &observedPunchRawPacketConn{observedPunchPacketConn: basic, rawSocket: raw}, o
	}
	return basic, o
}

func (p *icePeer) recordPunchDiagnostic(a *iceAttempt, d NATPunchDiagnostic) {
	if a.ctx.Err() != nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.ready.TransportID != a.ready.TransportID || p.ready.PeerRuntimeID != a.ready.PeerRuntimeID {
		return
	}
	// Keep failed direct rounds available after the relay generation starts.
	const limit = 6
	previous := p.stats.PunchDiagnostics
	if len(previous) >= limit {
		previous = previous[len(previous)-limit+1:]
	}
	p.stats.PunchDiagnostics = append(append([]NATPunchDiagnostic(nil), previous...), d)
}
