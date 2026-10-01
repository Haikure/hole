package core

import (
	"bytes"
	"context"
	"github.com/quic-go/quic-go"
	"io"
	"testing"
	"time"
)

type delayedChunk struct {
	data  []byte
	ready time.Time
}
type delayedSessionStream struct {
	ctx       context.Context
	cancel    context.CancelFunc
	in        <-chan delayedChunk
	out       chan<- delayedChunk
	remaining []byte
	delay     time.Duration
}

func (s *delayedSessionStream) Write(data []byte) (int, error) {
	chunk := delayedChunk{append([]byte(nil), data...), time.Now().Add(s.delay)}
	select {
	case s.out <- chunk:
		return len(data), nil
	case <-s.ctx.Done():
		return 0, s.ctx.Err()
	}
}
func (s *delayedSessionStream) Read(data []byte) (int, error) {
	if len(s.remaining) == 0 {
		var chunk delayedChunk
		select {
		case chunk = <-s.in:
		case <-s.ctx.Done():
			return 0, io.EOF
		}
		timer := time.NewTimer(max(0, time.Until(chunk.ready)))
		select {
		case <-timer.C:
		case <-s.ctx.Done():
			timer.Stop()
			return 0, io.EOF
		}
		s.remaining = chunk.data
	}
	n := copy(data, s.remaining)
	s.remaining = s.remaining[n:]
	return n, nil
}
func (s *delayedSessionStream) CancelRead(quic.StreamErrorCode)  { s.cancel() }
func (s *delayedSessionStream) CancelWrite(quic.StreamErrorCode) { s.cancel() }

// Measures the application replay protocol with a pipelined 100ms RTT and no
// imposed bandwidth ceiling. It isolates window/CPU costs, not physical QUIC,
// TURN or Wi-Fi throughput. Use the same benchmark with a 256KiB window for the
// former-window comparison.
func BenchmarkTCPReplayRTT100ms(b *testing.B) {
	input := bytes.Repeat([]byte("bandwidth"), 4*1024*1024)
	b.SetBytes(int64(len(input)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		wireCtx, wireCancel := context.WithCancel(ctx)
		ab, ba := make(chan delayedChunk, 4096), make(chan delayedChunk, 4096)
		left := &delayedSessionStream{ctx: wireCtx, cancel: wireCancel, in: ba, out: ab, delay: 50 * time.Millisecond}
		right := &delayedSessionStream{ctx: wireCtx, cancel: wireCancel, in: ab, out: ba, delay: 50 * time.Millisecond}
		appA, appB := newMemoryTCPSocket(input), newMemoryTCPSocket(nil)
		appA.finish()
		appB.finish()
		id := sessionID{1}
		a, c := newTCPSession(ctx, id, appA), newTCPSession(ctx, id, appB)
		la, _, err := a.attach(nil, left, 1, 0, false)
		if err != nil {
			b.Fatal(err)
		}
		lb, _, err := c.attach(nil, right, 1, 0, false)
		if err != nil {
			b.Fatal(err)
		}
		done := make(chan error, 2)
		go func() { done <- a.runLink(ctx, la, 0) }()
		go func() { done <- c.runLink(ctx, lb, 0) }()
		for range 2 {
			select {
			case <-done:
			case <-ctx.Done():
				b.Fatal("replay transfer stalled")
			}
		}
		a.close()
		c.close()
		wireCancel()
		cancel()
		if !bytes.Equal(appB.received(), input) {
			b.Fatal("transfer corrupted")
		}
	}
}
