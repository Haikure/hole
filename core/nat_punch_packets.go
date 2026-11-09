package core

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/apernet/quic-go"
)

// Adapted from testnat's dual probes: every field is authenticated and every
// ACK echoes a random challenge. The key is unique to a device generation.
type natPunchPacket struct {
	kind        byte
	round       uint32
	socket      uint16
	sequence    uint32
	nonce, echo [16]byte
}

func (p natPunchPacket) encode(key []byte) []byte {
	b := make([]byte, 47, 79)
	copy(b, "\x00HP1")
	binary.BigEndian.PutUint32(b[4:8], p.round)
	binary.BigEndian.PutUint16(b[8:10], p.socket)
	binary.BigEndian.PutUint32(b[10:14], p.sequence)
	b[14] = p.kind
	copy(b[15:31], p.nonce[:])
	copy(b[31:47], p.echo[:])
	mac := hmac.New(sha256.New, key)
	mac.Write(b)
	return append(b, mac.Sum(nil)...)
}

func decodeNATPunch(b, key []byte, round int) (natPunchPacket, bool) {
	var p natPunchPacket
	if len(b) != 79 || string(b[:4]) != "\x00HP1" {
		return p, false
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(b[:47])
	if !hmac.Equal(b[47:], mac.Sum(nil)) {
		return p, false
	}
	p.round = binary.BigEndian.Uint32(b[4:8])
	p.socket = binary.BigEndian.Uint16(b[8:10])
	p.sequence = binary.BigEndian.Uint32(b[10:14])
	p.kind = b[14]
	copy(p.nonce[:], b[15:31])
	copy(p.echo[:], b[31:47])
	return p, p.round == uint32(round) && p.socket < 64 && p.sequence >= 1 && p.sequence <= 8 && (p.kind == 1 || p.kind == 2)
}

type natPunchAttempt struct {
	sockets    []net.PacketConn
	transports []*quic.Transport
	listeners  []*quic.Listener
	selected   int
	diagnostic NATPunchDiagnostic // populated after run has joined its readers
	observed   []*punchSocketObservation
}

func newNATPunchAttempt(sockets []net.PacketConn) *natPunchAttempt {
	a := &natPunchAttempt{sockets: sockets, selected: -1}
	for _, pc := range sockets {
		conn, observed := observePunchSocket(pc)
		a.transports = append(a.transports, &quic.Transport{Conn: conn})
		a.observed = append(a.observed, observed)
	}
	return a
}

// Keep failed-round local ports reserved until the overall attempt ends.
func (a *natPunchAttempt) close() {
	for _, l := range a.listeners {
		_ = l.Close()
	}
	for _, t := range a.transports {
		_ = t.Close()
	}
}

func (a *natPunchAttempt) run(ctx context.Context, dialer bool, round int, key []byte, targets []*net.UDPAddr, tlsConfig *tls.Config) (*quic.Conn, error) {
	if len(targets) == 0 || len(targets) > 256 || len(a.sockets) == 0 || len(a.sockets) > 64 || len(targets)*len(a.sockets) > 256 {
		return nil, errors.New("invalid punch task count")
	}
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	type result struct {
		conn  *quic.Conn
		index int
		err   error
	}
	completed := make(chan result, len(a.sockets)+1)
	// Each reader owns one element; inspect them only after wg.Wait below.
	reads := make([]NATPunchDiagnostic, len(a.sockets))
	defer func() {
		cancel()
		wg.Wait()
		a.diagnostic.SocketObserved = true
		for _, o := range a.observed {
			if o == nil {
				a.diagnostic.SocketObserved = false
				continue
			}
			a.diagnostic.UDPPackets += int(o.packets.Load())
			a.diagnostic.UDPReadErrors += int(o.errors.Load())
			if first := o.first.Load(); first != nil && a.diagnostic.UDPReadError == "" {
				a.diagnostic.UDPReadError = *first
			}
		}
		for _, r := range reads {
			a.diagnostic.PacketsReceived += r.PacketsReceived
			a.diagnostic.SourceRejected += r.SourceRejected
			a.diagnostic.PacketRejected += r.PacketRejected
			a.diagnostic.ProbesReceived += r.ProbesReceived
			a.diagnostic.ACKsReceived += r.ACKsReceived
			a.diagnostic.ReadErrors += r.ReadErrors
			if a.diagnostic.ReadError == "" {
				a.diagnostic.ReadError = r.ReadError
			}
		}
		for {
			select {
			case r := <-completed:
				if r.conn != nil {
					_ = r.conn.CloseWithError(0, "unselected punch socket")
				}
			default:
				return
			}
		}
	}()
	type event struct {
		index, target int
		packet        natPunchPacket
	}
	incoming := make(chan event, 128)
	for i, t := range a.transports {
		if !dialer {
			l, err := t.Listen(tlsConfig, iceQUICConfig("direct"))
			if err != nil {
				return nil, err
			}
			a.listeners = append(a.listeners, l)
			wg.Go(func() { c, e := l.Accept(ctx); completed <- result{c, i, e} })
		}
		wg.Go(func() {
			stats := &reads[i]
			buf := make([]byte, 2048)
			for {
				n, from, e := t.ReadNonQUICPacket(ctx, buf)
				if e != nil {
					if ctx.Err() == nil {
						stats.ReadErrors++
						stats.ReadError = e.Error()
					}
					return
				}
				stats.PacketsReceived++
				target := -1
				for j, addr := range targets {
					if samePacketAddr(from, addr) {
						target = j
						break
					}
				}
				if target < 0 {
					stats.SourceRejected++
					continue
				}
				packet, ok := decodeNATPunch(buf[:n], key, round)
				if !ok {
					stats.PacketRejected++
					continue
				}
				if packet.kind == 1 {
					stats.ProbesReceived++
				} else {
					stats.ACKsReceived++
				}
				select {
				case incoming <- event{i, target, packet}:
				case <-ctx.Done():
					return
				}
			}
		})
	}
	type slot struct {
		index, target int
		nonce         [16]byte
		sent          uint32
		last          time.Time
		ack           *natPunchPacket
		seen          [64]uint16
	}
	var slots []slot
	for i := range a.sockets {
		for j := range targets {
			s := slot{index: i, target: j}
			if _, e := rand.Read(s.nonce[:]); e != nil {
				return nil, e
			}
			slots = append(slots, s)
		}
	}
	selected := -1
	// 5 ms aggregate pacing includes ACKs. Each candidate is retried no
	// faster than 200 ms; this also bounds single-symmetric scans to 200 pps.
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case r := <-completed:
			if r.err != nil {
				return nil, r.err
			}
			state := r.conn.ConnectionState().SupportsDatagrams
			if !state.Local || !state.Remote {
				_ = r.conn.CloseWithError(1, "DATAGRAM required")
				return nil, errors.New("DATAGRAM required")
			}
			a.selected = r.index
			a.diagnostic.QUICEstablished = true
			cancel()
			for i, t := range a.transports {
				if i != r.index {
					if a.observed[i] != nil {
						a.observed[i].stopping.Store(true)
					}
					_ = t.Close()
				}
			}
			return r.conn, nil
		case ev := <-incoming:
			i := ev.index*len(targets) + ev.target
			s := &slots[i]
			packet := ev.packet
			if packet.kind == 1 {
				bit := uint16(1) << packet.sequence
				if s.seen[packet.socket]&bit != 0 {
					continue
				}
				s.seen[packet.socket] |= bit
				ack := natPunchPacket{kind: 2, round: uint32(round), socket: uint16(ev.index), sequence: packet.sequence, nonce: s.nonce, echo: packet.nonce}
				s.ack = &ack
			} else if dialer && selected < 0 && s.sent > 0 && packet.echo == s.nonce && packet.sequence <= s.sent {
				selected = i
				a.diagnostic.ACKsVerified++
				a.diagnostic.QUICDialStarted = true
				wg.Go(func() {
					c, e := a.transports[ev.index].Dial(ctx, targets[ev.target], tlsConfig, iceQUICConfig("direct"))
					completed <- result{c, ev.index, e}
				})
			}
		case now := <-ticker.C:
			i := -1
			var payload []byte
			for j := range slots {
				if slots[j].ack != nil {
					i = j
					payload = slots[j].ack.encode(key)
					slots[j].ack = nil
					break
				}
			}
			if i < 0 {
				for j, s := range slots {
					if selected >= 0 && j != selected {
						continue
					}
					limit := uint32(4)
					if j == selected || !dialer && s.seen != [64]uint16{} {
						limit = 8
					}
					if s.sent < limit && now.Sub(s.last) >= natPunchInterval && (i < 0 || s.last.Before(slots[i].last)) {
						i = j
					}
				}
				if i >= 0 {
					s := &slots[i]
					s.sent++
					s.last = now
					payload = (natPunchPacket{kind: 1, round: uint32(round), socket: uint16(s.index), sequence: s.sent, nonce: s.nonce}).encode(key)
				}
			}
			if i >= 0 {
				s := slots[i]
				if _, e := a.sockets[s.index].WriteTo(payload, targets[s.target]); e != nil {
					return nil, e
				}
				if payload[14] == 1 {
					a.diagnostic.ProbesSent++
				} else {
					a.diagnostic.ACKsSent++
				}
			}
		}
	}
}
