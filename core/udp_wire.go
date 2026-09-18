package core

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
)

const (
	udpHeaderSize       = 32
	maxUDPDatagram      = 65535
	udpInitialFrameSize = 1100 // fits QUIC's initial 1200-byte path MTU
	udpFragmentTimeout  = 5 * time.Second
	udpAssemblyBudget   = 4 * 1024 * 1024
	maxUDPAssemblies    = 128
	maxUDPSessions      = 256
)

var udpWireMagic = [4]byte{'H', 'U', 'D', 2}

type udpLink struct {
	conn       sessionConnection
	control    *quic.Stream
	generation uint64
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	packetID   uint64
	frameSize  int
}

func newUDPLink(ctx context.Context, conn sessionConnection, control *quic.Stream, generation uint64) *udpLink {
	ctx, cancel := context.WithCancel(ctx)
	return &udpLink{conn: conn, control: control, generation: generation, ctx: ctx, cancel: cancel, frameSize: udpInitialFrameSize}
}

func (l *udpLink) close() {
	l.cancel()
	l.control.CancelRead(0)
	l.control.CancelWrite(0)
	_ = l.conn.CloseWithError(0, "UDP transport replaced; application sessions retained")
}

// Large UDP messages are split into bounded QUIC DATAGRAM frames. Offsets,
// rather than a fixed fragment count, allow the path MTU to shrink mid-message.
// Failure drops this message only; no application socket is torn down.
func (l *udpLink) send(id sessionID, data []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.packetID++
	return sendUDPFragments(l.ctx, l.conn.SendDatagram, id, l.packetID, &l.frameSize, data)
}

func sendUDPFragments(ctx context.Context, send func([]byte) error, id sessionID, packetID uint64, frameSize *int, data []byte) error {
	if len(data) > maxUDPDatagram || *frameSize <= udpHeaderSize {
		return errors.New("invalid UDP message or frame size")
	}
	for offset := 0; ; {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := min(len(data)-offset, *frameSize-udpHeaderSize)
		frame := make([]byte, udpHeaderSize+n)
		copy(frame[:4], udpWireMagic[:])
		copy(frame[4:20], id[:])
		binary.BigEndian.PutUint64(frame[20:28], packetID)
		binary.BigEndian.PutUint16(frame[28:30], uint16(len(data)))
		binary.BigEndian.PutUint16(frame[30:32], uint16(offset))
		copy(frame[32:], data[offset:offset+n])
		if err := send(frame); err != nil {
			var tooLarge *quic.DatagramTooLargeError
			if errors.As(err, &tooLarge) && tooLarge.MaxDatagramPayloadSize > udpHeaderSize && tooLarge.MaxDatagramPayloadSize < int64(*frameSize) {
				*frameSize = int(tooLarge.MaxDatagramPayloadSize)
				continue
			}
			return err
		}
		offset += n
		if offset == len(data) {
			return nil // also sends zero-length UDP messages
		}
	}
}

type udpPacketKey struct {
	channel uint32 // local reassembly namespace; not part of the inner v2 wire

	session sessionID
	packet  uint64
}

type udpFragment struct {
	key    udpPacketKey
	total  int
	offset int
	data   []byte
}

func parseUDPFragment(data []byte) (udpFragment, error) {
	var f udpFragment
	if len(data) < udpHeaderSize || !bytes.Equal(data[:4], udpWireMagic[:]) {
		return f, errors.New("invalid UDP frame header")
	}
	copy(f.key.session[:], data[4:20])
	f.key.packet = binary.BigEndian.Uint64(data[20:28])
	f.total = int(binary.BigEndian.Uint16(data[28:30]))
	f.offset = int(binary.BigEndian.Uint16(data[30:32]))
	f.data = data[32:]
	if f.key.session == (sessionID{}) || f.key.packet == 0 || f.offset+len(f.data) > f.total || (f.total > 0 && len(f.data) == 0) {
		return f, errors.New("invalid UDP fragment bounds")
	}
	return f, nil
}

type udpAssembly struct {
	data     []byte
	bits     []byte
	received int
	expires  time.Time
}

// One reassembler belongs to one authenticated QUIC transport and has one
// reader. Old-path fragments never combine with a new path's packet counters.
type udpReassembler struct {
	pending map[udpPacketKey]*udpAssembly
	seen    map[udpPacketKey]time.Time
	bytes   int
}

func (r *udpReassembler) prune(now time.Time) {
	for key, a := range r.pending {
		if !now.Before(a.expires) {
			r.bytes -= len(a.data) + len(a.bits)
			delete(r.pending, key)
		}
	}
	for key, expires := range r.seen {
		if !now.Before(expires) {
			delete(r.seen, key)
		}
	}
}

func (r *udpReassembler) push(f udpFragment, now time.Time) ([]byte, bool, error) {
	if r.pending == nil {
		r.pending = make(map[udpPacketKey]*udpAssembly)
		r.seen = make(map[udpPacketKey]time.Time)
	}
	r.prune(now)
	if _, exists := r.seen[f.key]; exists {
		return nil, false, nil
	}
	a := r.pending[f.key]
	if a == nil {
		cost := f.total + (f.total+7)/8
		if len(r.pending) >= maxUDPAssemblies || r.bytes+cost > udpAssemblyBudget {
			return nil, false, errors.New("UDP reassembly budget exhausted")
		}
		a = &udpAssembly{data: make([]byte, f.total), bits: make([]byte, (f.total+7)/8), expires: now.Add(udpFragmentTimeout)}
		r.pending[f.key] = a
		r.bytes += cost
	}
	if len(a.data) != f.total {
		return nil, false, errors.New("inconsistent UDP fragment length")
	}
	for i, value := range f.data {
		pos := f.offset + i
		mask := byte(1 << uint(pos%8))
		if a.bits[pos/8]&mask == 0 {
			a.bits[pos/8] |= mask
			a.data[pos] = value
			a.received++
		} else if a.data[pos] != value {
			return nil, false, fmt.Errorf("conflicting UDP fragment at %d", pos)
		}
	}
	if a.received != len(a.data) {
		return nil, false, nil
	}
	delete(r.pending, f.key)
	r.bytes -= len(a.data) + len(a.bits)
	if len(r.seen) >= 2048 {
		// Bounded duplicate cache. QUIC already removes packet-level duplicates.
		for key := range r.seen {
			delete(r.seen, key)
			break
		}
	}
	r.seen[f.key] = now.Add(udpFragmentTimeout)
	return a.data, true, nil
}
