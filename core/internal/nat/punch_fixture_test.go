package nat

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"
)

// Deterministic NAT fixture with endpoint-dependent mapping and filtering.
// Only an outbound packet to the exact source mapping opens a symmetric path.
type virtualRouter struct {
	mu       sync.Mutex
	routes   map[string]*virtualSocket
	next     [2]int
	patterns [2]string
	count    [2]int
	closed   [2]int
}
type virtualNetwork struct {
	r    *virtualRouter
	side int
}
type virtualSocket struct {
	r    *virtualRouter
	side int
	dest map[string]netip.AddrPort
	in   chan datagram
	done chan struct{}
	once sync.Once
	id   int
}

func (n virtualNetwork) ListenPacket(context.Context, string, string) (net.PacketConn, error) {
	n.r.mu.Lock()
	defer n.r.mu.Unlock()
	n.r.count[n.side]++
	return &virtualSocket{r: n.r, side: n.side, dest: map[string]netip.AddrPort{}, in: make(chan datagram, 2048), done: make(chan struct{}), id: n.r.count[n.side]}, nil
}
func (n virtualNetwork) ResolveUDP(context.Context, string) (*net.UDPAddr, error) {
	panic("fixture has no DNS")
}
func (s *virtualSocket) LocalAddr() net.Addr {
	return net.UDPAddrFromAddrPort(netip.AddrPortFrom(netip.MustParseAddr("10.0.0.1"), uint16(10000+s.id)))
}
func (s *virtualSocket) SetDeadline(time.Time) error      { return nil }
func (s *virtualSocket) SetReadDeadline(time.Time) error  { return nil }
func (s *virtualSocket) SetWriteDeadline(time.Time) error { return nil }
func (s *virtualSocket) Close() error {
	s.once.Do(func() { close(s.done); s.r.mu.Lock(); s.r.closed[s.side]++; s.r.mu.Unlock() })
	return nil
}
func (s *virtualSocket) ReadFrom(b []byte) (int, net.Addr, error) {
	select {
	case p := <-s.in:
		return copy(b, p.data), p.from, nil
	case <-s.done:
		return 0, nil, net.ErrClosed
	}
}
func (s *virtualSocket) mapped(destination string) netip.AddrPort {
	key := destination
	if s.r.patterns[s.side] == "cone" {
		key = "*"
	}
	if a, ok := s.dest[key]; ok {
		return a
	}
	p := s.r.next[s.side]
	if s.r.patterns[s.side] == "decremental" {
		s.r.next[s.side]--
	} else {
		s.r.next[s.side]++
	}
	a := netip.AddrPortFrom(netip.MustParseAddr(fmt.Sprintf("198.51.100.%d", s.side+1)), uint16(p))
	s.dest[key] = a
	s.r.routes[a.String()] = s
	return a
}
func (s *virtualSocket) WriteTo(b []byte, destination net.Addr) (int, error) {
	select {
	case <-s.done:
		return 0, net.ErrClosed
	default:
	}
	s.r.mu.Lock()
	defer s.r.mu.Unlock()
	source := s.mapped(destination.String())
	peer := s.r.routes[destination.String()]
	if peer == nil {
		return len(b), nil
	}
	if peer.r.patterns[peer.side] != "cone" {
		out, ok := peer.dest[source.String()]
		if !ok || out.String() != destination.String() {
			return len(b), nil
		}
	}
	select {
	case peer.in <- datagram{append([]byte(nil), b...), net.UDPAddrFromAddrPort(source)}:
	default:
	}
	return len(b), nil
}

func TestPredictionCollisionAndBothPredictableFixtures(t *testing.T) {
	for _, tc := range []struct {
		name, a, b string
		strategy   string
	}{{"prediction", "incremental", "cone", "prediction"}, {"decrement", "decremental", "cone", "prediction"}, {"collision", "hard", "cone", "collision"}, {"both", "incremental", "incremental", "both_predictable"}, {"opposite", "incremental", "decremental", "both_predictable"}} {
		t.Run(tc.name, func(t *testing.T) {
			r := &virtualRouter{routes: map[string]*virtualSocket{}, next: [2]int{40000, 50000}, patterns: [2]string{tc.a, tc.b}}
			networks := []virtualNetwork{{r, 0}, {r, 1}}
			base := make([]net.PacketConn, 2)
			mapped := make([]netip.AddrPort, 2)
			snap := make([]Snapshot, 2)
			for i, n := range networks {
				base[i], _ = n.ListenPacket(context.Background(), "udp4", "")
				r.mu.Lock()
				mapped[i] = base[i].(*virtualSocket).mapped("stun")
				r.mu.Unlock()
				snap[i] = Snapshot{Type: "symmetric", Predictability: n.r.patterns[i]}
				if n.r.patterns[i] == "cone" {
					snap[i] = Snapshot{Type: "full_cone", Predictability: "stable"}
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			key := make([]byte, 32)
			type result struct {
				s      *Socket
				remote net.Addr
				e      error
			}
			done := make(chan result, 1)
			go func() {
				s, a, e := Punch(ctx, networks[1], base[1], snap[1], snap[0], []netip.AddrPort{mapped[0]}, key, false)
				done <- result{s, a, e}
			}()
			left, remote, e := Punch(ctx, networks[0], base[0], snap[0], snap[1], []netip.AddrPort{mapped[1]}, key, true)
			right := <-done
			if e != nil || right.e != nil {
				t.Fatalf("%s: %v %v sockets=%v", tc.strategy, e, right.e, r.count)
			}
			_, _ = left.WriteTo([]byte("business"), remote)
			got := make(chan string, 1)
			go func() { buf := make([]byte, 64); n, _, _ := right.s.ReadFrom(buf); got <- string(buf[:n]) }()
			select {
			case s := <-got:
				if s != "business" {
					t.Fatal(s)
				}
			case <-ctx.Done():
				t.Fatal("handoff failed")
			}
			_ = left.Close()
			_ = right.s.Close()
			r.mu.Lock()
			defer r.mu.Unlock()
			if r.count != r.closed {
				t.Fatalf("leaked sockets opened=%v closed=%v", r.count, r.closed)
			}
		})
	}
}
