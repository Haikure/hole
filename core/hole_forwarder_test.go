package core

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"sync"
	"testing"
	"time"
)

func testHoleGrant() *holeGrant {
	return &holeGrant{Route: "01234567890123456789012345678901", Keys: [2]string{"0123456789012345678901234567890101234567890123456789012345678901", "1123456789012345678901234567890101234567890123456789012345678901"}, Generation: 1}
}
func TestHoleReplayAndMAC(t *testing.T) {
	auth, _ := newHoleAuth(testHoleGrant())
	var b [128]byte
	auth.sign(b[:], holeData, 0, 1)
	if !auth.valid(b[:]) {
		t.Fatal("signature")
	}
	b[80] ^= 1
	if auth.valid(b[:]) {
		t.Fatal("tampering accepted")
	}
	var r holeReplay
	for _, tt := range []struct {
		n  uint64
		ok bool
	}{{1, true}, {1, false}, {3, true}, {2, true}, {2, false}, {90, true}, {3, false}, {0, false}} {
		if r.accept(tt.n) != tt.ok {
			t.Fatal(tt)
		}
	}
}
func routeOnlyFixture(t testing.TB) (*holeForwarder, *holeRoute, *holeAuth) {
	t.Helper()
	auth, err := newHoleAuth(testHoleGrant())
	if err != nil {
		t.Fatal(err)
	}
	writer, _ := newHoleAuth(testHoleGrant())
	f := &holeForwarder{ctx: context.Background(), routes: map[[16]byte]*holeRoute{}, free: make(chan *holeFrame, holePairQueue), wake: make(chan struct{}, 1)}
	r := &holeRoute{auth: auth, until: time.Now().Add(time.Hour).UnixMilli(), address: [2]netip.AddrPort{netip.MustParseAddrPort("127.0.0.1:30000"), netip.MustParseAddrPort("127.0.0.1:30001")}}
	f.routes[auth.route] = r
	f.order = []*holeRoute{r}
	return f, r, writer
}
func TestHoleDataCannotRebindReplayOrExceedQueue(t *testing.T) {
	f, r, writer := routeOnlyFixture(t)
	frame := func(n uint64, addr netip.AddrPort) *holeFrame {
		p := &holeFrame{n: 1200, address: addr}
		writer.sign(p.data[:p.n], holeData, 0, n)
		return p
	}
	if f.route(frame(1, netip.MustParseAddrPort("127.0.0.1:39999"))) {
		t.Fatal("data rebound address")
	}
	if r.address[0].Port() != 30000 {
		t.Fatal("address changed")
	}
	for i := 0; i < holePairQueue; i++ {
		if !f.route(frame(uint64(i+2), r.address[0])) {
			t.Fatal("queue rejected", i)
		}
	}
	if f.route(frame(30, r.address[0])) || f.stats.QueueDrops != 1 {
		t.Fatal("unbounded queue")
	}
	if f.route(frame(2, r.address[0])) {
		t.Fatal("replay accepted")
	}
	f.revokeAll()
	if len(f.routes) != 0 || len(f.free) != holePairQueue {
		t.Fatal("buffers leaked")
	}
}
func TestHoleTotalBandwidth(t *testing.T) {
	now := time.Now()
	bucket := holeBucket{rate: 1_000_000 / 8, last: now, tokens: 3000}
	if !bucket.allow(1500, now) || !bucket.allow(1500, now) || bucket.allow(1, now) {
		t.Fatal("aggregate limit")
	}
	if !bucket.allow(1200, now.Add(time.Second)) {
		t.Fatal("refill")
	}
	unlimited := holeBucket{}
	if !unlimited.allow(1<<30, now) {
		t.Fatal("zero must be unlimited")
	}
}
func TestHoleForwardingHotPathAllocations(t *testing.T) {
	f, r, writer := routeOnlyFixture(t)
	frame := &holeFrame{n: 1246, address: r.address[0]}
	seq := uint64(0)
	allocations := testing.AllocsPerRun(1000, func() {
		seq++
		frame.address = r.address[0]
		writer.sign(frame.data[:frame.n], holeData, 0, seq)
		if !f.route(frame) {
			t.Fatal("drop")
		}
		r.queue[r.head] = nil
		r.head = (r.head + 1) % holePairQueue
		r.size--
	})
	if allocations != 0 {
		t.Fatalf("forwarding allocated %.2f per packet", allocations)
	}
}
func BenchmarkHoleForwarding(b *testing.B) {
	for _, size := range []int{64, 160, 1200} {
		b.Run(stringSize(size), func(b *testing.B) {
			f, r, writer := routeOnlyFixture(b)
			frame := &holeFrame{n: holeHeader + size, address: r.address[0]}
			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				frame.address = r.address[0]
				writer.sign(frame.data[:frame.n], holeData, 0, uint64(i+1))
				f.route(frame)
				r.queue[r.head] = nil
				r.head = (r.head + 1) % holePairQueue
				r.size--
			}
		})
	}
}
func stringSize(n int) string {
	switch n {
	case 64:
		return "64"
	case 160:
		return "160"
	default:
		return "1200"
	}
}

type partialHoleBackend struct {
	mu   sync.Mutex
	seen []byte
}

func (*partialHoleBackend) Name() string                        { return "partial-test" }
func (*partialHoleBackend) ReadBatch([]*holeFrame) (int, error) { return 0, net.ErrClosed }
func (p *partialHoleBackend) WriteBatch(frames []*holeFrame) (int, error) {
	p.mu.Lock()
	p.seen = append(p.seen, frames[0].data[0])
	p.mu.Unlock()
	return 1, nil
}
func TestHolePartialBatchAndFairness(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend := new(partialHoleBackend)
	f := &holeForwarder{ctx: ctx, backend: backend, routes: map[[16]byte]*holeRoute{}, free: make(chan *holeFrame, 20), wake: make(chan struct{}, 1)}
	for pair := 0; pair < 2; pair++ {
		g := testHoleGrant()
		auth, _ := newHoleAuth(g)
		auth.route[0] = byte(pair)
		r := &holeRoute{auth: auth, until: time.Now().Add(time.Minute).UnixMilli(), size: 4}
		for i := 0; i < 4; i++ {
			r.queue[i] = &holeFrame{n: 100}
			r.queue[i].data[0] = byte(pair)
		}
		f.routes[auth.route] = r
		f.order = append(f.order, r)
	}
	f.workers.Add(1)
	go f.forward()
	f.wake <- struct{}{}
	deadline := time.Now().Add(time.Second)
	for len(f.free) < 8 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	f.workers.Wait()
	if len(f.free) != 8 {
		t.Fatal("partial sends leaked buffers")
	}
	for i, v := range backend.seen {
		if int(v) != i%2 {
			t.Fatal("unfair schedule", backend.seen)
		}
	}
}
func TestHoleInvalidExpiredAndReplacedGrant(t *testing.T) {
	socket, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := newHoleForwarder(context.Background(), socket, RelayServeConfig{MaxPairs: 1})
	defer f.Close()
	g := testHoleGrant()
	m := iceSignalMessage{Hole: g, TransportID: "pair", TransportGeneration: 1, LeaseUntil: time.Now().Add(time.Minute).UnixMilli()}
	if err = f.install(m); err != nil {
		t.Fatal(err)
	}
	g2 := *g
	g2.Route = "11234567890123456789012345678901"
	g2.Generation = 2
	m.Hole = &g2
	m.TransportGeneration = 2
	if err = f.install(m); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	if len(f.routes) != 1 {
		t.Fatal("old grant retained")
	}
	f.mu.Unlock()
	f.revoke("pair", 1)
	if f.snapshot().ActivePairs != 1 {
		t.Fatal("stale revoke removed newer route")
	}
	m.LeaseUntil = time.Now().Add(-time.Second).UnixMilli()
	if f.install(m) == nil {
		t.Fatal("expired grant accepted")
	}
}

func TestHoleGrantCannotRegressOrRevive(t *testing.T) {
	f, _, _ := routeOnlyFixture(t)
	f.revokeAll()
	f.limit = 2
	g := testHoleGrant()
	m := iceSignalMessage{TransportID: "pair", TransportGeneration: 1, Hole: g, LeaseUntil: time.Now().Add(time.Minute).UnixMilli()}
	if err := f.install(m); err != nil {
		t.Fatal(err)
	}
	old := m
	f.revoke("uninstalled", 1)
	uninstalled := m
	uninstalled.TransportID = "uninstalled"
	if f.install(uninstalled) == nil {
		t.Fatal("revoke before install was forgotten")
	}
	newGrant := *g
	newGrant.Generation = 2
	newGrant.Route = "11234567890123456789012345678901"
	m.Hole, m.TransportGeneration = &newGrant, 2
	if err := f.install(m); err != nil {
		t.Fatal(err)
	}
	if f.install(old) == nil {
		t.Fatal("older generation replaced current grant")
	}
	changed := newGrant
	changed.Keys[0] = g.Keys[1]
	bad := m
	bad.Hole = &changed
	if f.install(bad) == nil {
		t.Fatal("renewal changed key")
	}
	f.revoke("pair", 2)
	if f.install(m) == nil {
		t.Fatal("revoked generation revived")
	}
	newGrant.Generation = 3
	m.TransportGeneration = 3
	if err := f.install(m); err != nil {
		t.Fatal(err)
	}
	f.revokeAll()
	if f.install(m) == nil {
		t.Fatal("disconnect revived old grant")
	}
}

func TestHoleReadDeadlineCanBeReset(t *testing.T) {
	socket, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := newHoleForwarder(context.Background(), socket, RelayServeConfig{MaxPairs: 1})
	defer f.Close()
	_ = f.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
	buf := make([]byte, 100)
	if _, _, err = f.ReadFrom(buf); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatal(err)
	}
	_ = f.SetReadDeadline(time.Time{})
	sender, err := net.Dial("udp4", socket.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	_, _ = sender.Write([]byte("ordinary QUIC"))
	_ = f.SetReadDeadline(time.Now().Add(time.Second))
	n, _, err := f.ReadFrom(buf)
	if err != nil || string(buf[:n]) != "ordinary QUIC" {
		t.Fatal("read dispatcher stopped after deadline", err)
	}
}

type blockedHoleBackend struct{ started, release chan struct{} }

func (*blockedHoleBackend) Name() string                        { return "blocked-test" }
func (*blockedHoleBackend) ReadBatch([]*holeFrame) (int, error) { return 0, net.ErrClosed }
func (b *blockedHoleBackend) WriteBatch(frames []*holeFrame) (int, error) {
	close(b.started)
	<-b.release
	return len(frames), nil
}
func TestHoleBlockedSendDoesNotBlockRevocation(t *testing.T) {
	f, r, _ := routeOnlyFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.ctx = ctx
	b := &blockedHoleBackend{make(chan struct{}), make(chan struct{})}
	f.backend = b
	r.queue[0], r.size = &holeFrame{n: 100}, 1
	f.workers.Add(1)
	go f.forward()
	f.wake <- struct{}{}
	select {
	case <-b.started:
	case <-time.After(time.Second):
		t.Fatal("no send")
	}
	done := make(chan struct{})
	go func() { f.revokeAll(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		close(b.release)
		t.Fatal("socket send held authorization lock")
	}
	cancel()
	close(b.release)
	f.workers.Wait()
}
