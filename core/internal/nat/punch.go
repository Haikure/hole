// SPDX-License-Identifier: LGPL-3.0-only
// See classify.go for provenance. Socket selection, authenticated probes and
// the single-reader QUIC handoff are specific to Hole.
package nat

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"net"
	"net/netip"
	"sync"
	"time"
)

type datagram struct {
	data []byte
	from net.Addr
}
type hit struct {
	socket *Socket
	from   net.Addr
	kind   byte
}

// Socket has exactly one reader for its entire lifetime, including QUIC.
type Socket struct {
	net.PacketConn
	key          []byte
	controlling  bool
	hits         chan<- hit
	packets      chan datagram
	done         chan struct{}
	once         sync.Once
	mu           sync.Mutex
	remote       net.Addr
	readDeadline time.Time
	wake         chan struct{}
}

func Open(pc net.PacketConn, key []byte, controlling bool, hits chan<- hit) *Socket {
	s := &Socket{PacketConn: pc, key: key, controlling: controlling, hits: hits, packets: make(chan datagram, 128), done: make(chan struct{}), wake: make(chan struct{}, 1)}
	go s.read()
	return s
}
func (s *Socket) probe(kind byte, to net.Addr) {
	b := []byte{'H', 'O', 'L', 'E', 'U', 'D', 'P', kind}
	if s.controlling {
		b = append(b, 1)
	} else {
		b = append(b, 0)
	}
	m := hmac.New(sha256.New, s.key)
	_, _ = m.Write(b)
	b = append(b, m.Sum(nil)...)
	_, _ = s.PacketConn.WriteTo(b, to)
}
func (s *Socket) read() {
	defer s.Close()
	b := make([]byte, 65536)
	for {
		n, from, err := s.PacketConn.ReadFrom(b)
		if err != nil {
			return
		}
		if n == 41 && string(b[:7]) == "HOLEUDP" {
			m := hmac.New(sha256.New, s.key)
			_, _ = m.Write(b[:9])
			own := byte(0)
			if s.controlling {
				own = 1
			}
			if b[8] == own || !hmac.Equal(b[9:41], m.Sum(nil)) {
				continue
			}
			kind := b[7]
			if kind == 0 {
				s.probe(1, from)
			}
			if kind == 2 && !s.controlling {
				s.mu.Lock()
				selected := s.remote
				s.mu.Unlock()
				if selected != nil && selected.String() == from.String() {
					s.probe(3, from)
				}
			}
			if kind <= 3 {
				select {
				case s.hits <- hit{s, from, kind}:
				default:
				}
			}
			continue
		}
		s.mu.Lock()
		remote := s.remote
		s.mu.Unlock()
		if remote == nil || from.String() != remote.String() {
			continue
		}
		packet := datagram{append([]byte(nil), b[:n]...), from}
		select {
		case s.packets <- packet:
		default:
		}
	}
}
func (s *Socket) selectRemote(a net.Addr) { s.mu.Lock(); s.remote = a; s.mu.Unlock() }
func (s *Socket) ReadFrom(b []byte) (int, net.Addr, error) {
	for {
		s.mu.Lock()
		deadline := s.readDeadline
		s.mu.Unlock()
		var timer *time.Timer
		var timeout <-chan time.Time
		if !deadline.IsZero() {
			timer = time.NewTimer(time.Until(deadline))
			timeout = timer.C
		}
		select {
		case <-s.done:
			if timer != nil {
				timer.Stop()
			}
			return 0, nil, net.ErrClosed
		case p := <-s.packets:
			if timer != nil {
				timer.Stop()
			}
			return copy(b, p.data), p.from, nil
		case <-timeout:
			return 0, nil, context.DeadlineExceeded
		case <-s.wake:
			if timer != nil {
				timer.Stop()
			}
		}
	}
}
func (s *Socket) SetReadDeadline(t time.Time) error {
	s.mu.Lock()
	s.readDeadline = t
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return nil
}
func (s *Socket) SetDeadline(t time.Time) error {
	_ = s.SetReadDeadline(t)
	return s.SetWriteDeadline(t)
}
func (s *Socket) Close() error {
	var err error
	s.once.Do(func() { close(s.done); err = s.PacketConn.Close() })
	return err
}

// Punch transfers the chosen socket to its caller; every other socket is closed.
// Selection is proposed by the QUIC dialer and acknowledged on the exact socket
// pair before QUIC starts, preventing opposite sides choosing different hits.
func Punch(ctx context.Context, network Network, base net.PacketConn, local, remote Snapshot, addresses []netip.AddrPort, key []byte, controlling bool) (*Socket, net.Addr, error) {
	strategy := Strategy(local, remote)
	if strategy == "none" || len(addresses) == 0 || len(key) < 32 {
		_ = base.Close()
		return nil, nil, ErrNoStrategy
	}
	hits := make(chan hit, 256)
	all := []*Socket{Open(base, key, controlling, hits)}
	if deadline, ok := ctx.Deadline(); ok {
		_ = base.SetWriteDeadline(deadline)
	}
	var chosen *Socket
	defer func() {
		for _, s := range all {
			if s != chosen {
				_ = s.Close()
			}
		}
	}()
	count := 1
	if strategy == "collision" && local.Type == "symmetric" {
		count = HardSockets
	}
	if strategy == "both_predictable" {
		count = BothSockets
	}
	for i := 1; i < count; i++ {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		pc, err := network.ListenPacket(ctx, "udp4", "0.0.0.0:0")
		if err != nil {
			return nil, nil, err
		}
		all = append(all, Open(pc, key, controlling, hits))
		if deadline, ok := ctx.Deadline(); ok {
			_ = pc.SetWriteDeadline(deadline)
		}
	}
	targets := []net.Addr{}
	for _, a := range addresses {
		if strategy == "both_predictable" {
			port := int(a.Port()) + 20
			if remote.Predictability == "decremental" || remote.Predictability == "predictable_decremental" {
				port = int(a.Port()) - 20
			}
			port = max(1, min(65535, port))
			targets = append(targets, net.UDPAddrFromAddrPort(netip.AddrPortFrom(a.Addr(), uint16(port))))
			continue
		}
		targets = append(targets, net.UDPAddrFromAddrPort(a))
		if predictable(remote) {
			for _, p := range Predict(a.Port(), remote.Predictability, 50) {
				targets = append(targets, net.UDPAddrFromAddrPort(netip.AddrPortFrom(a.Addr(), p)))
			}
		}
	}
	// The cone listener scans a shuffled 600..799 port window each round;
	// symmetric clients keep their 84 destination mappings alive concurrently.
	var shuffled []uint16
	if strategy == "collision" && remote.Type == "symmetric" {
		shuffled = make([]uint16, 65535)
		for i := range shuffled {
			shuffled[i] = uint16(i + 1)
		}
		for i := len(shuffled) - 1; i > 0; i-- {
			var b [2]byte
			_, _ = rand.Read(b[:])
			j := int(uint16(b[0])<<8|uint16(b[1])) % (i + 1)
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		}
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var candidate *hit
	offset := 0
	for {
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case h := <-hits:
			if controlling {
				if candidate == nil && h.kind == 1 {
					candidate = &h
					h.socket.selectRemote(h.from)
					h.socket.probe(2, h.from)
				}
				if candidate != nil && h.kind == 3 && h.socket == candidate.socket && h.from.String() == candidate.from.String() {
					chosen = h.socket
					_ = chosen.SetWriteDeadline(time.Time{})
					return chosen, h.from, nil
				}
			} else if h.kind == 2 {
				h.socket.selectRemote(h.from)
				h.socket.probe(3, h.from)
				// Repeated selection packets are acknowledged by the reader even
				// after ownership is handed to QUIC.
				chosen = h.socket
				_ = chosen.SetWriteDeadline(time.Time{})
				return chosen, h.from, nil
			}
		case <-ticker.C:
			if candidate != nil {
				candidate.socket.probe(2, candidate.from)
				continue
			}
			for _, s := range all {
				for _, a := range targets {
					if ctx.Err() != nil {
						return nil, nil, ctx.Err()
					}
					s.probe(0, a)
				}
			}
			if len(shuffled) > 0 {
				var random [1]byte
				_, _ = rand.Read(random[:])
				window := 600 + int(random[0])%200
				for i := 0; i < max(1, window/len(addresses)); i++ {
					p := shuffled[(offset+i)%len(shuffled)]
					for _, a := range addresses {
						all[0].probe(0, net.UDPAddrFromAddrPort(netip.AddrPortFrom(a.Addr(), p)))
					}
				}
				offset = (offset + max(1, window/len(addresses))) % len(shuffled)
			}
		}
	}
}

var _ net.PacketConn = (*Socket)(nil)
