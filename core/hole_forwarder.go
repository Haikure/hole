package core

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"os"
	"sync"
	"time"
)

const holePairQueue = 16

type holeRoute struct {
	id         string
	generation uint64
	until      int64
	auth       *holeAuth
	grant      holeGrant
	address    [2]netip.AddrPort
	oob        [2][128]byte
	oobn       [2]int
	pending    [2]netip.AddrPort
	challenge  [2][16]byte
	replay     [2]holeReplay
	queue      [holePairQueue]*holeFrame
	head, size int
}

type holeGrantMark struct {
	generation uint64
	until      int64
	revoked    bool
}

// Only this dispatcher reads the socket. Platform wrappers advertise ancillary
// capabilities only when they preserve them through this same receive queue.
type holeForwarder struct {
	conn         net.PacketConn
	backend      forwardingBackend
	ctx          context.Context
	cancel       context.CancelFunc
	mu           sync.Mutex
	routes       map[[16]byte]*holeRoute
	grants       map[string]holeGrantMark
	order        []*holeRoute
	cursor       int
	stats        holeCounters
	bucket       holeBucket
	limit        int
	free         chan *holeFrame
	normal       chan *holeFrame
	control      chan *holeFrame
	readDeadline time.Time
	readChanged  chan struct{}
	wake         chan struct{}
	workers      sync.WaitGroup
	once         sync.Once
	fatalFailure chan error
	fatalOnce    sync.Once
}

func newHoleForwarder(ctx context.Context, conn net.PacketConn, config RelayServeConfig) *holeForwarder {
	ctx, cancel := context.WithCancel(ctx)
	limit := config.MaxPairs
	if limit == 0 {
		limit = 64
	}
	f := &holeForwarder{conn: conn, backend: newForwardingBackend(conn), ctx: ctx, cancel: cancel, limit: limit, routes: map[[16]byte]*holeRoute{}, free: make(chan *holeFrame, limit*holePairQueue+64+3*holeBatchSize), normal: make(chan *holeFrame, 64), wake: make(chan struct{}, 1), fatalFailure: make(chan error, 1)}
	f.grants = make(map[string]holeGrantMark)
	f.control = make(chan *holeFrame, holeBatchSize)
	f.readChanged = make(chan struct{})
	f.bucket = holeBucket{rate: float64(config.BandwidthMbps) * 1_000_000 / 8, last: time.Now()}
	f.bucket.tokens = max(f.bucket.rate/10, float64(holePacketSize))
	f.stats.Backend = f.backend.Name()
	for i := 0; i < cap(f.free); i++ {
		f.free <- new(holeFrame)
	}
	f.workers.Add(2)
	go f.receive()
	go f.forward()
	return f
}

func (f *holeForwarder) reportFatal(err error) {
	if err == nil || f.ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, net.ErrClosed) {
		return
	}
	f.fatalOnce.Do(func() {
		if f.fatalFailure != nil {
			f.fatalFailure <- err
		}
		if f.cancel != nil {
			f.cancel()
		}
	})
}

func (f *holeForwarder) install(m iceSignalMessage) error {
	now := time.Now()
	if m.Hole == nil || m.LeaseUntil <= now.UnixMilli() || m.LeaseUntil > now.Add(11*time.Minute).UnixMilli() || m.Hole.Generation != m.TransportGeneration {
		return errors.New("expired relay grant")
	}
	auth, err := newHoleAuth(m.Hole)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := 0; i < len(f.order); {
		if f.order[i].until <= now.UnixMilli() {
			f.removeLocked(f.order[i])
		} else {
			i++
		}
	}
	if f.grants == nil {
		f.grants = make(map[string]holeGrantMark)
	}
	mark, known := f.grants[m.TransportID]
	if known && (m.TransportGeneration < mark.generation || (m.TransportGeneration == mark.generation && mark.revoked)) {
		return errors.New("stale or revoked relay grant")
	}
	if r := f.routes[auth.route]; r != nil {
		if r.id != m.TransportID || r.generation != m.TransportGeneration || r.grant != *m.Hole || r.until <= now.UnixMilli() {
			return errors.New("route mismatch")
		}
		r.until = max(r.until, m.LeaseUntil)
		mark.until = max(mark.until, r.until)
		f.grants[m.TransportID] = mark
		return nil
	}
	if known && m.TransportGeneration == mark.generation {
		return errors.New("relay generation cannot change its route")
	}
	// Remember revocations until every grant issued for that generation has
	// expired. Bound historical records independently of the active-pair quota.
	if !known && len(f.grants) >= 4096 {
		for id, old := range f.grants {
			if old.until <= now.UnixMilli() {
				delete(f.grants, id)
			}
		}
		if len(f.grants) >= 4096 {
			return errors.New("relay grant history capacity reached")
		}
	}
	for _, r := range f.order {
		if r.id == m.TransportID {
			f.removeLocked(r)
		}
	}
	if len(f.order) >= f.limit {
		return &Fault{Code: "relay_capacity", Message: "中继设备容量已满"}
	}
	r := &holeRoute{id: m.TransportID, generation: m.TransportGeneration, until: m.LeaseUntil, auth: auth, grant: *m.Hole}
	f.grants[m.TransportID] = holeGrantMark{generation: m.TransportGeneration, until: max(mark.until, m.LeaseUntil)}
	f.routes[auth.route] = r
	f.order = append(f.order, r)
	return nil
}
func (f *holeForwarder) removeLocked(r *holeRoute) {
	mark := f.grants[r.id]
	if mark.generation <= r.generation && f.grants != nil {
		f.grants[r.id] = holeGrantMark{generation: r.generation, until: max(mark.until, r.until), revoked: true}
	}
	delete(f.routes, r.auth.route)
	for r.size > 0 {
		frame := r.queue[r.head]
		r.queue[r.head] = nil
		r.head = (r.head + 1) % holePairQueue
		r.size--
		f.free <- frame
	}
	for i, v := range f.order {
		if v == r {
			copy(f.order[i:], f.order[i+1:])
			f.order = f.order[:len(f.order)-1]
			break
		}
	}
}
func (f *holeForwarder) revoke(id string, generation uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.grants == nil {
		f.grants = make(map[string]holeGrantMark)
	}
	if _, ok := f.grants[id]; !ok {
		now := time.Now()
		if len(f.grants) >= 4096 {
			for key, mark := range f.grants {
				if mark.until <= now.UnixMilli() {
					delete(f.grants, key)
				}
			}
		}
		if len(f.grants) < 4096 {
			f.grants[id] = holeGrantMark{generation: generation, until: now.Add(11 * time.Minute).UnixMilli(), revoked: true}
		}
	}
	if mark, ok := f.grants[id]; ok && generation >= mark.generation {
		mark.generation, mark.revoked = generation, true
		f.grants[id] = mark
	}
	for _, r := range f.order {
		if r.id == id && r.generation <= generation {
			f.removeLocked(r)
			break
		}
	}
}
func (f *holeForwarder) snapshot() holeCounters {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.stats
	s.ActivePairs = len(f.order)
	return s
}
func (f *holeForwarder) revokeAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for len(f.order) > 0 {
		f.removeLocked(f.order[0])
	}
}
func (f *holeForwarder) receive() {
	defer f.workers.Done()
	defer f.cancel()
	var frames [holeBatchSize]*holeFrame
	for f.ctx.Err() == nil {
		for i := range frames {
			select {
			case frames[i] = <-f.free:
			case <-f.ctx.Done():
				for j := 0; j < i; j++ {
					f.free <- frames[j]
				}
				return
			}
		}
		n, err := f.backend.ReadBatch(frames[:])
		if err != nil {
			for _, frame := range frames {
				f.free <- frame
			}
			f.reportFatal(err)
			return
		}
		for i, frame := range frames {
			if i >= n {
				f.free <- frame
				continue
			}
			b := frame.data[:frame.n]
			if len(b) >= 4 && string(b[:4]) == string(holeMagic[:]) {
				if !f.route(frame) {
					f.free <- frame
				}
				continue
			}
			select {
			case f.normal <- frame:
			default:
				f.free <- frame
			}
		}
	}
}

// Returns true exactly when the pair queue takes ownership of frame.
func (f *holeForwarder) route(frame *holeFrame) bool {
	b := frame.data[:frame.n]
	if len(b) < holeHeader || b[5] > 1 {
		return false
	}
	var id [16]byte
	copy(id[:], b[6:22])
	side := int(b[5])
	now := time.Now()
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.routes[id]
	if r == nil || r.until <= now.UnixMilli() || !r.auth.valid(b) || !r.replay[side].accept(binary.BigEndian.Uint64(b[22:30])) {
		return false
	}
	addr := canonicalHoleAddress(frame.address)
	switch b[4] {
	case holeRegister:
		r.pending[side] = addr
		_, _ = rand.Read(r.challenge[side][:])
		copy(frame.data[holeHeader:], r.challenge[side][:])
		frame.n = holeHeader + 16
		r.auth.sign(frame.data[:frame.n], holeChallenge, side, binary.BigEndian.Uint64(b[22:30]))
		return f.sendControl(frame)
	case holeConfirm:
		if frame.n != holeHeader+16 || r.pending[side] != addr || string(b[holeHeader:]) != string(r.challenge[side][:]) {
			return false
		}
		r.address[side] = addr
		r.oob[side], r.oobn[side] = frame.oob, frame.oobn
		r.pending[side] = netip.AddrPort{}
		frame.n = holeHeader
		r.auth.sign(frame.data[:frame.n], holeAck, side, binary.BigEndian.Uint64(b[22:30]))
		return f.sendControl(frame)
	case holeData:
		if r.address[side] != addr || !r.address[1-side].IsValid() {
			return false
		}
		if r.size == holePairQueue {
			f.stats.QueueDrops++
			return false
		}
		frame.address = r.address[1-side]
		frame.oob, frame.oobn = r.oob[1-side], r.oobn[1-side]
		r.queue[(r.head+r.size)%holePairQueue] = frame
		r.size++
		select {
		case f.wake <- struct{}{}:
		default:
		}
		return true
	}
	return false
}
func (f *holeForwarder) sendControl(frame *holeFrame) bool {
	select {
	case f.control <- frame:
		select {
		case f.wake <- struct{}{}:
		default:
		}
		return true
	default:
		return false
	}
}
func (f *holeForwarder) forward() {
	defer f.workers.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var batch [holeBatchSize]*holeFrame
	for {
		select {
		case <-f.ctx.Done():
			return
		case <-ticker.C:
		case <-f.wake:
		}
		for f.ctx.Err() == nil {
			f.mu.Lock()
			now := time.Now().UnixMilli()
			for i := 0; i < len(f.order); {
				r := f.order[i]
				if r.until <= now {
					f.removeLocked(r)
				} else {
					i++
				}
			}
			n, empty := 0, 0
			control := 0
			for control < holeBatchSize/2 {
				select {
				case frame := <-f.control:
					batch[n] = frame
					n++
					control++
				default:
					control = holeBatchSize / 2
				}
			}
			control = n
			for n < len(batch) && len(f.order) > 0 && empty < len(f.order) {
				f.cursor %= len(f.order)
				r := f.order[f.cursor]
				f.cursor++
				if r.size == 0 {
					empty++
					continue
				}
				empty = 0
				frame := r.queue[r.head]
				r.queue[r.head] = nil
				r.head = (r.head + 1) % holePairQueue
				r.size--
				if !f.bucket.allow(frame.n, time.Now()) {
					f.stats.RateDrops++
					f.free <- frame
					continue
				}
				batch[n] = frame
				n++
			}
			// Dequeued frames are already submitted. A revoke drains queued frames;
			// socket backpressure must never hold up routing or authorization.
			f.mu.Unlock()
			sent := 0
			for sent < n {
				count, err := f.backend.WriteBatch(batch[sent:n])
				f.mu.Lock()
				for _, frame := range batch[max(sent, control):max(sent+count, control)] {
					f.stats.ForwardedBytes += uint64(frame.n)
				}
				sent += count
				if err != nil || count == 0 {
					f.stats.QueueDrops += uint64(n - sent)
					f.mu.Unlock()
					if err != nil {
						f.reportFatal(err)
					}
					break
				}
				f.mu.Unlock()
			}
			for i := 0; i < n; i++ {
				f.free <- batch[i]
				batch[i] = nil
			}
			if n == 0 {
				break
			}
		}
	}
}
func (f *holeForwarder) takeNormal() (*holeFrame, error) {
	for {
		f.mu.Lock()
		deadline, changed := f.readDeadline, f.readChanged
		f.mu.Unlock()
		var timer *time.Timer
		var timeout <-chan time.Time
		if !deadline.IsZero() {
			if !time.Now().Before(deadline) {
				return nil, os.ErrDeadlineExceeded
			}
			timer = time.NewTimer(time.Until(deadline))
			timeout = timer.C
		}
		select {
		case <-f.ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return nil, net.ErrClosed
		case <-changed:
			if timer != nil {
				timer.Stop()
			}
			continue
		case <-timeout:
			return nil, os.ErrDeadlineExceeded
		case frame := <-f.normal:
			if timer != nil {
				timer.Stop()
			}
			return frame, nil
		}
	}
}
func (f *holeForwarder) ReadFrom(b []byte) (int, net.Addr, error) {
	frame, err := f.takeNormal()
	if err != nil {
		return 0, nil, err
	}
	n := copy(b, frame.data[:frame.n])
	addr := net.UDPAddrFromAddrPort(frame.address)
	f.free <- frame
	return n, addr, nil
}
func (f *holeForwarder) WriteTo(b []byte, a net.Addr) (int, error) { return f.conn.WriteTo(b, a) }
func (f *holeForwarder) LocalAddr() net.Addr                       { return f.conn.LocalAddr() }
func (f *holeForwarder) SetReadBuffer(n int) error                 { return holeReadBuffer(f.conn, n) }
func (f *holeForwarder) SetWriteBuffer(n int) error                { return holeWriteBuffer(f.conn, n) }
func (f *holeForwarder) SetDeadline(t time.Time) error {
	_ = f.SetReadDeadline(t)
	return f.SetWriteDeadline(t)
}
func (f *holeForwarder) SetReadDeadline(t time.Time) error {
	f.mu.Lock()
	f.readDeadline = t
	if f.readChanged != nil {
		close(f.readChanged)
	}
	f.readChanged = make(chan struct{})
	f.mu.Unlock()
	return nil
}
func (f *holeForwarder) SetWriteDeadline(t time.Time) error { return f.conn.SetWriteDeadline(t) }
func (f *holeForwarder) Close() error {
	var err error
	f.once.Do(func() {
		f.cancel()
		err = f.conn.Close()
		f.workers.Wait()
		f.mu.Lock()
		for len(f.order) > 0 {
			f.removeLocked(f.order[0])
		}
		f.mu.Unlock()
		for len(f.control) > 0 {
			f.free <- <-f.control
		}
		for {
			select {
			case frame := <-f.normal:
				f.free <- frame
			default:
				return
			}
		}
	})
	return err
}

var _ net.PacketConn = (*holeForwarder)(nil)
