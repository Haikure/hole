// The APDM/APDF simulator and scenarios are adapted from ../testnat/dual_test.go.
package core

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/apernet/quic-go"
	"github.com/pion/stun/v4"
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"
)

type natNetwork struct {
	mu          sync.Mutex
	sides       []*simNAT
	drop        int
	duplicate   bool
	writes      int
	blockOneWay bool
}
type simNAT struct {
	network    *natNetwork
	index      int
	ip         net.IP
	next, step int
	occupied   int
	cone       bool
	pool       bool
	nextLocal  int
	byPort     map[int]*simMapping
}
type simMapping struct {
	socket *simSocket
	target *net.UDPAddr
	port   int
}
type datagram struct {
	data []byte
	from net.Addr
}
type simSocket struct {
	nat      *simNAT
	port     int
	mappings map[string]*simMapping // guarded by network.mu
	incoming chan datagram
	closed   chan struct{}
	once     sync.Once
	mu       sync.Mutex
	deadline time.Time
	changed  chan struct{}
}

func (s *simSocket) WriteTo(p []byte, addr net.Addr) (int, error) {
	select {
	case <-s.closed:
		return 0, net.ErrClosed
	default:
	}
	target := addr.(*net.UDPAddr)
	n := s.nat.network
	n.mu.Lock()
	defer n.mu.Unlock()
	n.writes++
	m := s.mappings[target.String()]
	if m == nil {
		port := s.nat.next
		s.nat.next += s.nat.step
		if s.nat.cone {
			for _, old := range s.mappings {
				port = old.port
				break
			}
		}
		if port == s.nat.occupied {
			port = s.nat.next
			s.nat.next += s.nat.step
		}
		m = &simMapping{socket: s, target: target, port: port}
		s.mappings[target.String()] = m
		s.nat.byPort[port] = m
	}
	if target.IP.To4() != nil && target.IP.To4()[0] == 192 && stun.IsMessage(p) {
		request := &stun.Message{Raw: p}
		if request.Decode() == nil && request.Type == stun.BindingRequest {
			ip := s.nat.ip
			if s.nat.pool && target.IP.Equal(net.IPv4(192, 0, 2, 4)) {
				ip = net.IPv4(203, 0, 113, byte(s.nat.index+1))
			}
			response := stun.MustBuild(stun.NewTransactionIDSetter(request.TransactionID), stun.BindingSuccess, &stun.XORMappedAddress{IP: ip, Port: m.port})
			s.incoming <- datagram{append([]byte(nil), response.Raw...), target}
			return len(p), nil
		}
	}
	var peer *simNAT
	for _, candidate := range n.sides {
		if target.IP.Equal(candidate.ip) {
			peer = candidate
			break
		}
	}
	if peer == nil {
		return len(p), nil
	}
	remote := peer.byPort[target.Port]
	if remote != nil && peer.cone {
		remote = remote.socket.mappings[(&net.UDPAddr{IP: s.nat.ip, Port: m.port}).String()]
	}
	if !target.IP.Equal(peer.ip) || remote == nil || !remote.target.IP.Equal(s.nat.ip) || remote.target.Port != m.port {
		return len(p), nil
	}
	if n.drop > 0 {
		n.drop--
		return len(p), nil
	}
	if n.blockOneWay && s.nat.index == 0 {
		return len(p), nil
	}
	d := datagram{append([]byte(nil), p...), &net.UDPAddr{IP: s.nat.ip, Port: m.port}}
	select {
	case remote.socket.incoming <- d:
	default:
	}
	if n.duplicate {
		select {
		case remote.socket.incoming <- d:
		default:
		}
	}
	return len(p), nil
}
func (s *simSocket) ReadFrom(p []byte) (int, net.Addr, error) {
	for {
		s.mu.Lock()
		deadline, changed := s.deadline, s.changed
		s.mu.Unlock()
		var timer *time.Timer
		var timeout <-chan time.Time
		if !deadline.IsZero() {
			timer = time.NewTimer(time.Until(deadline))
			timeout = timer.C
		}
		select {
		case d := <-s.incoming:
			if timer != nil {
				timer.Stop()
			}
			return copy(p, d.data), d.from, nil
		case <-s.closed:
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
		}
	}
}
func (s *simSocket) Close() error { s.once.Do(func() { close(s.closed) }); return nil }
func (s *simSocket) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4(10, 0, byte(s.nat.index), 1), Port: s.port}
}
func (s *simSocket) SetDeadline(t time.Time) error { return s.SetReadDeadline(t) }
func (s *simSocket) SetReadDeadline(t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deadline = t
	close(s.changed)
	s.changed = make(chan struct{})
	return nil
}
func (s *simSocket) SetWriteDeadline(time.Time) error { return nil }

func simulatedNATs(steps, background [2]int, count int) (*natNetwork, [2][]net.PacketConn, [2]*net.UDPAddr) {
	n := &natNetwork{sides: make([]*simNAT, 2)}
	var sockets [2][]net.PacketConn
	var targets [2]*net.UDPAddr
	for side := range 2 {
		base := 40000 + side*10000
		nat := &simNAT{network: n, index: side, ip: net.IPv4(198, 51, 100, byte(side+1)), next: base + steps[side]*(background[side]+1), step: steps[side], byPort: make(map[int]*simMapping)}
		n.sides[side] = nat
		targets[side] = &net.UDPAddr{IP: nat.ip, Port: base + steps[side]*max(1, count/2)}
		for i := range count {
			sockets[side] = append(sockets[side], &simSocket{nat: nat, port: 10000 + i, mappings: make(map[string]*simMapping), incoming: make(chan datagram, 512), closed: make(chan struct{}), changed: make(chan struct{})})
		}
	}
	return n, sockets, targets
}

func dualTLS(t *testing.T) [2]*tls.Config {
	a, b := mustTLS(t), mustTLS(t)
	configs := [2]*tls.Config{a, b}
	for side, c := range configs {
		c.NextProtos = []string{iceALPN}
		c.InsecureSkipVerify = true
		c.ClientAuth = tls.RequireAnyClientCert
		fingerprint := certFingerprint(configs[1-side].Certificates[0])
		c.VerifyConnection = func(s tls.ConnectionState) error {
			if len(s.PeerCertificates) == 0 {
				return errors.New("missing certificate")
			}
			sum := sha256.Sum256(s.PeerCertificates[0].Raw)
			if hex.EncodeToString(sum[:]) != fingerprint {
				return errors.New("certificate mismatch")
			}
			return nil
		}
	}
	return configs
}

func TestNATPunchConeSymmetricScansFromRetainedSocket(t *testing.T) {
	for _, step := range []int{1, -1} {
		t.Run(fmt.Sprint(step), func(t *testing.T) {
			n, sockets, targets := simulatedNATs([2]int{1, step}, [2]int{}, 10)
			n.sides[0].cone = true
			// The cone's STUN mapping already exists and must be reused by all scans.
			cone := sockets[0][0].(*simSocket)
			stun := &net.UDPAddr{IP: net.IPv4(192, 0, 2, 1), Port: 3478}
			_, _ = cone.WriteTo([]byte("stun"), stun)
			targets[0].Port = 40001
			sockets[0] = sockets[0][:1]
			a, b := newNATPunchAttempt(sockets[0]), newNATPunchAttempt(sockets[1])
			defer func() {
				a.close()
				b.close()
				for _, side := range sockets {
					for _, pc := range side {
						_ = pc.Close()
					}
				}
			}()
			var scan []*net.UDPAddr
			for i := 1; i <= 20; i++ {
				scan = append(scan, &net.UDPAddr{IP: n.sides[1].ip, Port: 50000 + step*i})
			}
			configs := dualTLS(t)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			type result struct {
				c *quic.Conn
				e error
			}
			done := make(chan result, 2)
			go func() { c, e := a.run(ctx, true, 1, []byte("single"), scan, configs[0]); done <- result{c, e} }()
			go func() {
				c, e := b.run(ctx, false, 1, []byte("single"), []*net.UDPAddr{targets[0]}, configs[1])
				done <- result{c, e}
			}()
			for range 2 {
				r := <-done
				if r.e != nil {
					t.Fatal(r.e)
				}
				defer r.c.CloseWithError(0, "done")
			}
			if a.selected != 0 {
				t.Fatal("cone replaced socket")
			}
			if !a.diagnostic.QUICDialStarted || !a.diagnostic.QUICEstablished || a.diagnostic.ACKsVerified != 1 || a.diagnostic.PacketsReceived == 0 || !b.diagnostic.QUICEstablished {
				t.Fatalf("successful path missing probe/QUIC evidence: %+v / %+v", a.diagnostic, b.diagnostic)
			}
			if a.diagnostic.UDPReadErrors != 0 || b.diagnostic.UDPReadErrors != 0 || a.diagnostic.ReadErrors != 0 || b.diagnostic.ReadErrors != 0 {
				t.Fatalf("closing unused sockets reported a receive failure: %+v / %+v", a.diagnostic, b.diagnostic)
			}
			n.mu.Lock()
			defer n.mu.Unlock()
			for _, m := range cone.mappings {
				if m.port != 40001 {
					t.Fatal("cone mapping changed")
				}
			}
		})
	}
}

func TestNATPunchDualSymmetricEstablishesRealQUICOnOriginalSocket(t *testing.T) {
	for _, tc := range []struct {
		name              string
		steps, background [2]int
		drop              int
		duplicate         bool
		delay             time.Duration
		count             int
	}{
		{"strict step one", [2]int{1, 1}, [2]int{}, 0, false, 0, 32},
		{"different steps and allocation offsets", [2]int{1, 2}, [2]int{4, 7}, 2, true, 90 * time.Millisecond, 32},
		{"next port prediction", [2]int{2, 1}, [2]int{}, 1, false, 40 * time.Millisecond, 1},
		{"expanded batch covers larger drift", [2]int{2, 1}, [2]int{20, 24}, 1, false, 40 * time.Millisecond, 64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, sockets, targets := simulatedNATs(tc.steps, tc.background, tc.count)
			n.drop = tc.drop
			n.duplicate = tc.duplicate
			attempts := [2]*natPunchAttempt{newNATPunchAttempt(sockets[0]), newNATPunchAttempt(sockets[1])}
			defer func() {
				for i, a := range attempts {
					a.close()
					for _, s := range sockets[i] {
						s.Close()
					}
				}
			}()
			configs := dualTLS(t)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			type outcome struct {
				side int
				conn *quic.Conn
				err  error
			}
			done := make(chan outcome, 2)
			for side := range 2 {
				go func() {
					if side == 1 {
						timer := time.NewTimer(tc.delay)
						defer timer.Stop()
						select {
						case <-timer.C:
						case <-ctx.Done():
							done <- outcome{side, nil, ctx.Err()}
							return
						}
					}
					conn, err := attempts[side].run(ctx, side == 1, 1, []byte("test-session"), []*net.UDPAddr{targets[1-side]}, configs[side])
					done <- outcome{side, conn, err}
				}()
			}
			var conns [2]*quic.Conn
			for range 2 {
				r := <-done
				if r.err != nil {
					t.Fatalf("side %d: %v", r.side, r.err)
				}
				conns[r.side] = r.conn
				defer r.conn.CloseWithError(0, "test done")
			}
			for side, a := range attempts {
				want := max(1, tc.count/2) - tc.background[side] - 1
				if a.selected != want {
					t.Fatalf("side %d selected socket %d, want %d", side, a.selected, want)
				}
				if conns[side].LocalAddr().String() != sockets[side][a.selected].LocalAddr().String() {
					t.Fatal("QUIC replaced punched socket")
				}
			}
			stream, err := conns[1].OpenStreamSync(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = stream.Write([]byte("data after punch")); err != nil {
				t.Fatal(err)
			}
			received, err := conns[0].AcceptStream(ctx)
			if err != nil {
				t.Fatal(err)
			}
			_ = received.SetReadDeadline(time.Now().Add(time.Second))
			buf := make([]byte, 16)
			if _, err = io.ReadFull(received, buf); err != nil || string(buf) != "data after punch" {
				t.Fatalf("%q %v", buf, err)
			}
			n.mu.Lock()
			defer n.mu.Unlock()
			for side := range 2 {
				for _, s := range sockets[side] {
					for dest := range s.(*simSocket).mappings {
						if dest != targets[1-side].String() {
							t.Fatal("target changed during round")
						}
					}
				}
			}
		})
	}
}

func TestNATPunchDualSymmetricOccupiedTargetCannotReportSuccess(t *testing.T) {
	n, sockets, targets := simulatedNATs([2]int{1, 2}, [2]int{}, 8)
	n.sides[0].occupied = targets[0].Port
	configs := dualTLS(t)
	ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer cancel()
	done := make(chan error, 2)
	for side := range 2 {
		go func() {
			a := newNATPunchAttempt(sockets[side])
			defer a.close()
			defer func() {
				for _, s := range sockets[side] {
					s.Close()
				}
			}()
			_, err := a.run(ctx, side == 1, 1, []byte("key"), []*net.UDPAddr{targets[1-side]}, configs[side])
			done <- err
		}()
	}
	for range 2 {
		if err := <-done; !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected timeout, got %v", err)
		}
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.writes > 8*4*2 {
		t.Fatal(fmt.Sprintf("packet budget exceeded: %d", n.writes))
	}
}

func TestNATPunchDualSymmetricOneWayTrafficNeverEstablishes(t *testing.T) {
	n, sockets, targets := simulatedNATs([2]int{1, 1}, [2]int{}, 1)
	n.blockOneWay = true
	configs := dualTLS(t)
	ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer cancel()
	done := make(chan error, 2)
	for side := range 2 {
		go func() {
			a := newNATPunchAttempt(sockets[side])
			defer a.close()
			defer sockets[side][0].Close()
			_, err := a.run(ctx, side == 1, 1, []byte("key"), []*net.UDPAddr{targets[1-side]}, configs[side])
			done <- err
		}()
	}
	for range 2 {
		if err := <-done; !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("single direction counted as success: %v", err)
		}
	}
}
