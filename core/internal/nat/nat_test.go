package nat

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
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
		{"stable", []uint16{40000, 40000}, 40000, "stable", "endpoint_independent"},
		{"increment", []uint16{40000, 40001, 40002}, 40003, "predictable_incremental", "symmetric"},
		{"decrement", []uint16{40002, 40001, 40000}, 39999, "predictable_decremental", "symmetric"},
		{"random", []uint16{40000, 50000, 30000}, 45000, "irregular", "symmetric"},
		{"insufficient", []uint16{40000}, 40001, "unknown", "unknown"},
		{"direction_unknown", []uint16{40000, 40001}, 0, "unknown", "symmetric"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			samples := []Sample{}
			for i, p := range tc.ports {
				server := netip.AddrPortFrom(netip.AddrFrom4([4]byte{192, 0, 2, byte(i + 1)}), 3478)
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
	changed.Change = 6
	if s := Classify(ap("10.0.0.1:10000"), []Sample{base, changed}, nil); s.Type != "unknown" || s.Samples != 1 {
		t.Fatal(s)
	}
	changed.Mapped = ap("198.51.100.2:40001")
	if s := Classify(ap("10.0.0.1:10000"), []Sample{base, changed}, &base); s.Status != "insufficient" {
		t.Fatal(s)
	}
}

func TestSnapshotExpiration(t *testing.T) {
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

func startDelayedChangeSTUN(t *testing.T, primary, alternate net.PacketConn, requests *atomic.Int32) {
	t.Helper()
	serve := func(socket net.PacketConn, changedResponse net.PacketConn) {
		go func() {
			buf := make([]byte, 2048)
			for {
				n, from, err := socket.ReadFrom(buf)
				if err != nil {
					return
				}
				request := new(stun.Message)
				request.Raw = append(request.Raw, buf[:n]...)
				if request.Decode() != nil {
					continue
				}
				requests.Add(1)
				responseSocket := socket
				if attr, err := request.Get(stun.AttrChangeRequest); err == nil && len(attr) == 4 && attr[3] != 0 {
					responseSocket = changedResponse
				}
				tid := request.TransactionID
				go func(from net.Addr, responseSocket net.PacketConn, tid [stun.TransactionIDSize]byte) {
					time.Sleep(450 * time.Millisecond)
					reply := stun.MustBuild(stun.NewTransactionIDSetter(tid), stun.BindingSuccess,
						&stun.XORMappedAddress{IP: net.ParseIP("198.51.100.10"), Port: 40000})
					_, _ = responseSocket.WriteTo(reply.Raw, from)
				}(from, responseSocket, tid)
			}
		}()
	}
	serve(primary, alternate)
	serve(alternate, alternate)
}

func TestProbeUsesLongerConcurrentSTUNResponses(t *testing.T) {
	var requests atomic.Int32
	var urls []string
	for range 2 {
		primary, err := net.ListenPacket("udp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		alternate, err := net.ListenPacket("udp4", "127.0.0.1:0")
		if err != nil {
			primary.Close()
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = primary.Close(); _ = alternate.Close() })
		startDelayedChangeSTUN(t, primary, alternate, &requests)
		urls = append(urls, "stun:"+primary.LocalAddr().String())
	}
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	started := time.Now()
	snapshot, addresses := Probe(ctx, &fixtureNetwork{}, pc, urls)
	if snapshot.Status != "insufficient" || snapshot.Type != "unknown" || snapshot.Samples != 6 {
		t.Fatalf("slow valid STUN responses were lost: %+v", snapshot)
	}
	if len(addresses) != 1 || requests.Load() < 12 {
		t.Fatalf("probe did not collect and correlate repeated request samples: addresses=%v requests=%d", addresses, requests.Load())
	}
	if elapsed := time.Since(started); elapsed >= 4*time.Second {
		t.Fatalf("parallel STUN rounds took too long: %s", elapsed)
	}
}

func TestProbeKeepsPortTrendWhenFilteringProbeTimesOut(t *testing.T) {
	type allocationTable struct {
		mu   sync.Mutex
		next uint16
	}
	type mappedServer struct {
		conn     net.PacketConn
		mu       sync.Mutex
		mappings map[string]uint16
	}
	allocations := &allocationTable{next: 40000}
	urls := make([]string, 0, 2)
	for range 2 {
		conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		server := &mappedServer{conn: conn, mappings: map[string]uint16{}}
		t.Cleanup(func() { _ = conn.Close() })
		urls = append(urls, "stun:"+conn.LocalAddr().String())
		go func(server *mappedServer) {
			buf := make([]byte, 2048)
			for {
				n, from, err := server.conn.ReadFrom(buf)
				if err != nil {
					return
				}
				request := new(stun.Message)
				request.Raw = append([]byte(nil), buf[:n]...)
				if request.Decode() != nil {
					continue
				}
				if _, err := request.Get(stun.AttrChangeRequest); err == nil {
					continue
				}
				server.mu.Lock()
				mappedPort, ok := server.mappings[from.String()]
				if !ok {
					allocations.mu.Lock()
					mappedPort = allocations.next
					allocations.next++
					allocations.mu.Unlock()
					server.mappings[from.String()] = mappedPort
				}
				server.mu.Unlock()
				response := stun.MustBuild(stun.NewTransactionIDSetter(request.TransactionID), stun.BindingSuccess,
					&stun.XORMappedAddress{IP: net.ParseIP("198.51.100.10"), Port: int(mappedPort)},
					&stun.OtherAddress{IP: net.ParseIP("8.8.8.8"), Port: 3479})
				_, _ = server.conn.WriteTo(response.Raw, from)
			}
		}(server)
	}
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	snapshot, _ := Probe(ctx, &fixtureNetwork{}, pc, urls)
	if snapshot.Status != "complete" || snapshot.Type != "symmetric" || snapshot.Predictability != "incremental" {
		t.Fatalf("filter timeout discarded available fresh-socket trend evidence: %+v", snapshot)
	}
}
