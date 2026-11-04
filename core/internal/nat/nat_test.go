package nat

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/pion/stun/v4"
)

func ap(raw string) netip.AddrPort { return netip.MustParseAddrPort(raw) }
func TestClassificationEvidence(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ports []uint16
		extra uint16
		want  string
		kind  string
	}{
		{"stable", []uint16{40000, 40000}, 40000, "stable", "unknown"},
		{"increment", []uint16{40000, 40001, 40002}, 40003, "incremental", "symmetric"},
		{"decrement", []uint16{40002, 40001, 40000}, 39999, "decremental", "symmetric"},
		{"random", []uint16{40000, 50000, 30000}, 45000, "irregular", "symmetric"},
		{"insufficient", []uint16{40000}, 40001, "unknown", "unknown"},
		{"direction_unknown", []uint16{40000, 40001}, 0, "irregular", "symmetric"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			samples := []Sample{}
			for i, p := range tc.ports {
				server := netip.AddrPortFrom(netip.MustParseAddr("192.0.2.1"), uint16(3478+i))
				samples = append(samples, Sample{Server: server, Source: server, Mapped: netip.AddrPortFrom(netip.MustParseAddr("198.51.100.1"), p)})
			}
			var extra *Sample
			if tc.extra != 0 {
				extra = &Sample{Mapped: netip.AddrPortFrom(netip.MustParseAddr("198.51.100.1"), tc.extra)}
			}
			s := Classify(ap("10.0.0.1:10000"), samples, extra)
			if s.Type != tc.kind || s.Predictability != tc.want {
				t.Fatalf("%+v", s)
			}
		})
	}
	base := Sample{Server: ap("192.0.2.1:3478"), Source: ap("192.0.2.1:3478"), Mapped: ap("198.51.100.1:40000")}
	changed := base
	changed.Source = ap("192.0.2.2:3479")
	changed.ChangeIP = true
	if s := Classify(ap("10.0.0.1:10000"), []Sample{base, changed}, nil); s.Type != "full_cone" {
		t.Fatal(s)
	}
	changed.Mapped = ap("198.51.100.2:40001")
	if s := Classify(ap("10.0.0.1:10000"), []Sample{base, changed}, &base); s.Predictability != "irregular" {
		t.Fatal(s)
	}
}

func TestStrategyMatrixAndPortBounds(t *testing.T) {
	cone := Snapshot{Type: "full_cone", Predictability: "stable"}
	hard := Snapshot{Type: "symmetric", Predictability: "irregular"}
	easy := Snapshot{Type: "symmetric", Predictability: "incremental"}
	for _, tc := range []struct {
		a, b Snapshot
		want string
	}{{cone, cone, "bidirectional"}, {hard, cone, "collision"}, {cone, hard, "collision"}, {easy, cone, "prediction"}, {easy, easy, "both_predictable"}, {hard, easy, "none"}, {hard, hard, "none"}} {
		if got := Strategy(tc.a, tc.b); got != tc.want {
			t.Fatalf("%+v %+v: %s", tc.a, tc.b, got)
		}
	}
	if len(Predict(65534, "incremental", 50)) != 1 || len(Predict(1, "decremental", 50)) != 0 {
		t.Fatal("invalid port range")
	}
	old := Snapshot{ProbedAt: time.Now().Add(-Lifetime).Format(time.RFC3339Nano)}
	if old.Current().Status != "expired" {
		t.Fatal(old)
	}
}

type fixtureNetwork struct {
	mu      sync.Mutex
	sockets []net.PacketConn
}

func (n *fixtureNetwork) ListenPacket(ctx context.Context, network, address string) (net.PacketConn, error) {
	pc, e := (&net.ListenConfig{}).ListenPacket(ctx, network, address)
	if e == nil {
		n.mu.Lock()
		n.sockets = append(n.sockets, pc)
		n.mu.Unlock()
	}
	return pc, e
}
func (n *fixtureNetwork) ResolveUDP(_ context.Context, a string) (*net.UDPAddr, error) {
	return net.ResolveUDPAddr("udp4", a)
}

func TestAuthenticatedPunchHandoff(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	n := new(fixtureNetwork)
	a, e := n.ListenPacket(ctx, "udp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	b, e := n.ListenPacket(ctx, "udp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	cone := Snapshot{Type: "full_cone", Predictability: "stable"}
	key := make([]byte, 32)
	type result struct {
		s      *Socket
		remote net.Addr
		err    error
	}
	results := make(chan result, 1)
	go func() {
		s, r, e := Punch(ctx, n, b, cone, cone, []netip.AddrPort{addrPort(a.LocalAddr())}, key, false)
		results <- result{s, r, e}
	}()
	left, remote, e := Punch(ctx, n, a, cone, cone, []netip.AddrPort{addrPort(b.LocalAddr())}, key, true)
	if e != nil {
		t.Fatal(e)
	}
	defer left.Close()
	right := <-results
	if right.err != nil {
		t.Fatal(right.err)
	}
	defer right.s.Close()
	if _, e = left.WriteTo([]byte("quic-payload"), remote); e != nil {
		t.Fatal(e)
	}
	_ = right.s.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 64)
	count, _, e := right.s.ReadFrom(buf)
	if e != nil || string(buf[:count]) != "quic-payload" {
		t.Fatalf("%q %v", buf[:count], e)
	}
	// Forged probes cannot select a socket, and cancelling closes every socket.
	ctx2, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer stop()
	pc, _ := n.ListenPacket(ctx2, "udp4", "127.0.0.1:0")
	if _, _, e = Punch(ctx2, n, pc, cone, cone, []netip.AddrPort{addrPort(a.LocalAddr())}, []byte("wrong key, at least thirty two bytes!"), true); e == nil {
		t.Fatal("forged credential accepted")
	}
	if _, e = pc.WriteTo([]byte{1}, a.LocalAddr()); e == nil {
		t.Fatal("cancelled socket leaked")
	}
}

func TestBindingDiscardsWrongTransactionAndLateSource(t *testing.T) {
	server, e := net.ListenPacket("udp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer server.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 2048)
		n, from, e := server.ReadFrom(buf)
		if e != nil {
			return
		}
		request := new(stun.Message)
		request.Raw = buf[:n]
		_ = request.Decode()
		wrong := stun.MustBuild(stun.TransactionID, stun.BindingSuccess, &stun.XORMappedAddress{IP: net.ParseIP("198.51.100.1"), Port: 1234})
		_, _ = server.WriteTo(wrong.Raw, from)
		good := stun.MustBuild(stun.NewTransactionIDSetter(request.TransactionID), stun.BindingSuccess, &stun.XORMappedAddress{IP: net.ParseIP("198.51.100.1"), Port: 40000})
		_, _ = server.WriteTo(good.Raw, from)
	}()
	pc, e := net.ListenPacket("udp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer pc.Close()
	s, e := Binding(context.Background(), pc, server.LocalAddr().(*net.UDPAddr), 0)
	if e != nil || s.Mapped.Port() != 40000 {
		t.Fatalf("%+v %v", s, e)
	}
	<-done
}
