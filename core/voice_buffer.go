package core

import (
	"context"
	"sync"
	"sync/atomic"
)

const (
	voiceQueueFrames = 3
	voiceJitterLimit = 8
	voiceBridgeBatch = 4
)

// pcmQueue is a bounded newest-first queue for platform bridge frames. It
// drops the oldest frame when a producer outruns the real-time pipeline.
type pcmQueue struct {
	mu     sync.Mutex
	ready  chan struct{}
	frames []PCMFrame
	cap    int
	drops  atomic.Uint64
}

func newPCMQueue(capacity int) *pcmQueue {
	if capacity < 1 {
		capacity = voiceQueueFrames
	}
	return &pcmQueue{ready: make(chan struct{}, 1), frames: make([]PCMFrame, 0, capacity), cap: capacity}
}

func (q *pcmQueue) push(frame PCMFrame) {
	q.mu.Lock()
	if len(q.frames) >= q.cap {
		q.frames[0] = PCMFrame{}
		q.frames = q.frames[1:]
		q.drops.Add(1)
	}
	q.frames = append(q.frames, frame.Clone())
	q.mu.Unlock()
	select {
	case q.ready <- struct{}{}:
	default:
	}
}

func (q *pcmQueue) pop(ctx context.Context) (PCMFrame, error) {
	for {
		q.mu.Lock()
		if len(q.frames) > 0 {
			frame := q.frames[0]
			q.frames[0] = PCMFrame{}
			q.frames = q.frames[1:]
			q.mu.Unlock()
			return frame, nil
		}
		q.mu.Unlock()
		select {
		case <-ctx.Done():
			return PCMFrame{}, ctx.Err()
		case <-q.ready:
		}
	}
}

func (q *pcmQueue) popNow() (PCMFrame, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.frames) == 0 {
		return PCMFrame{}, false
	}
	frame := q.frames[0]
	q.frames[0] = PCMFrame{}
	q.frames = q.frames[1:]
	return frame, true
}

type voicePCMBridge struct {
	input  *pcmQueue
	output *pcmQueue
}

func newVoicePCMBridge() *voicePCMBridge {
	return &voicePCMBridge{input: newPCMQueue(voiceQueueFrames), output: newPCMQueue(voiceQueueFrames)}
}

func (b *voicePCMBridge) ReadPCM(ctx context.Context, frame *PCMFrame) error {
	value, err := b.input.pop(ctx)
	if err != nil {
		return err
	}
	*frame = value
	return nil
}

func (b *voicePCMBridge) WritePCM(_ context.Context, frame PCMFrame) error {
	if err := frame.Validate(); err != nil {
		return err
	}
	b.output.push(frame)
	return nil
}

func (b *voicePCMBridge) push(sequence, timestamp uint64, samples []int16) error {
	if len(samples) == 0 || len(samples)%DefaultPCMFormat.FrameSamples != 0 || len(samples) > voiceBridgeBatch*DefaultPCMFormat.FrameSamples {
		return errInvalidPCMFrame
	}
	for offset := 0; offset < len(samples); offset += DefaultPCMFormat.FrameSamples {
		frame := PCMFrame{Sequence: sequence, Timestamp: timestamp, Samples: samples[offset : offset+DefaultPCMFormat.FrameSamples]}
		b.input.push(frame)
		sequence++
		timestamp += uint64(DefaultPCMFormat.FrameSamples)
	}
	return nil
}

func (b *voicePCMBridge) pull(maxFrames int) []int16 {
	if maxFrames < 1 {
		maxFrames = 1
	}
	if maxFrames > voiceBridgeBatch {
		maxFrames = voiceBridgeBatch
	}
	var result []int16
	for i := 0; i < maxFrames; i++ {
		frame, ok := b.output.popNow()
		if !ok {
			break
		}
		if result == nil {
			result = make([]int16, 0, maxFrames*DefaultPCMFormat.FrameSamples)
		}
		result = append(result, frame.Samples...)
	}
	return result
}

type pcmRing struct {
	mu       sync.Mutex
	frames   []PCMFrame
	capacity int
	drops    uint64
}

func newPCMBuffer(capacity int) *pcmRing {
	if capacity < 1 {
		capacity = voiceQueueFrames
	}
	return &pcmRing{capacity: capacity, frames: make([]PCMFrame, 0, capacity)}
}

func (r *pcmRing) push(frame PCMFrame) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.frames) >= r.capacity {
		r.frames[0] = PCMFrame{}
		r.frames = r.frames[1:]
		r.drops++
	}
	r.frames = append(r.frames, frame)
}

func (r *pcmRing) pop() (PCMFrame, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.frames) == 0 {
		return PCMFrame{}, false
	}
	frame := r.frames[0]
	r.frames[0] = PCMFrame{}
	r.frames = r.frames[1:]
	return frame, true
}

func (r *pcmRing) clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.frames {
		r.frames[i] = PCMFrame{}
	}
	r.frames = r.frames[:0]
}

func (r *pcmRing) len() int        { r.mu.Lock(); defer r.mu.Unlock(); return len(r.frames) }
func (r *pcmRing) dropped() uint64 { r.mu.Lock(); defer r.mu.Unlock(); return r.drops }

type jitterFrame struct {
	Sequence  uint32
	Timestamp uint64
	Payload   []byte
}

type jitterBuffer struct {
	mu           sync.Mutex
	frames       map[uint32]jitterFrame
	capacity     int
	dropped      uint64
	reordered    uint64
	next         uint32
	haveNext     bool
	missingPolls uint8
	startupPolls uint8
}

func newJitterBuffer(capacity int) *jitterBuffer {
	if capacity < 1 || capacity > voiceJitterLimit {
		capacity = voiceJitterLimit
	}
	return &jitterBuffer{frames: make(map[uint32]jitterFrame), capacity: capacity}
}

func (b *jitterBuffer) push(frame jitterFrame) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.frames[frame.Sequence]; exists {
		b.reordered++
		return
	}
	if len(b.frames) >= b.capacity {
		var oldest uint32
		first := true
		for sequence := range b.frames {
			if first || sequence < oldest {
				oldest, first = sequence, false
			}
		}
		delete(b.frames, oldest)
		b.dropped++
	}
	b.frames[frame.Sequence] = frame
}

func (b *jitterBuffer) pop(sequence uint32) (jitterFrame, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	frame, ok := b.frames[sequence]
	if ok {
		delete(b.frames, sequence)
	}
	return frame, ok
}

// popForPlayback keeps a small startup delay for reordering, then advances
// over a missing sequence once the bounded buffer is full.
func (b *jitterBuffer) popForPlayback() (jitterFrame, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.frames) == 0 {
		return jitterFrame{}, false
	}
	if !b.haveNext {
		b.startupPolls++
		if len(b.frames) < 3 && b.startupPolls < 2 {
			return jitterFrame{}, false
		}
		for sequence := range b.frames {
			if !b.haveNext || sequence < b.next {
				b.next, b.haveNext = sequence, true
			}
		}
		b.startupPolls = 0
	}
	if frame, ok := b.frames[b.next]; ok {
		delete(b.frames, b.next)
		b.next++
		b.missingPolls = 0
		return frame, true
	}
	b.missingPolls++
	if len(b.frames) < 3 && b.missingPolls < 2 {
		return jitterFrame{}, false
	}
	var oldest uint32
	first := true
	for sequence := range b.frames {
		if first || sequence < oldest {
			oldest, first = sequence, false
		}
	}
	if oldest >= b.next {
		b.dropped += uint64(oldest - b.next)
	}
	b.next = oldest + 1
	b.missingPolls = 0
	frame := b.frames[oldest]
	delete(b.frames, oldest)
	return frame, true
}

func (b *jitterBuffer) dropBefore(sequence uint32) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for current := range b.frames {
		if current < sequence {
			delete(b.frames, current)
			b.dropped++
		}
	}
}

func (b *jitterBuffer) clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.frames = make(map[uint32]jitterFrame)
	b.haveNext = false
	b.missingPolls = 0
	b.startupPolls = 0
}

func (b *jitterBuffer) len() int { b.mu.Lock(); defer b.mu.Unlock(); return len(b.frames) }
