package core

import (
	"sync"
	"time"
)

// Payload storage is charged in blocks, including unused capacity. All mappings
// and both roles of an Engine share this budget; waiting readers hold at most
// one additional tcpFramePayload-sized scratch buffer each.
const tcpReplayBudgetBytes = 128 * 1024 * 1024
const tcpReplayCachedBlocks = tcpReplayBuffer / tcpFramePayload
const tcpReplayCacheIdle = 5 * time.Second

type replayBudget struct {
	mu      sync.Mutex
	used    int
	limit   int
	changed chan struct{}
	free    [][]byte
	lastUse time.Time
}

func (b *replayBudget) acquire(blocks [][]byte) (bool, <-chan struct{}) {
	n := len(blocks) * tcpFramePayload
	if n == 0 {
		return true, nil
	}
	b.mu.Lock()
	limit := b.limit
	if limit == 0 {
		limit = tcpReplayBudgetBytes
	}
	if b.used+n <= limit {
		b.used += n
		b.lastUse = time.Now()
		for i := range blocks {
			if last := len(b.free) - 1; last >= 0 {
				blocks[i] = b.free[last]
				b.free[last] = nil
				b.free = b.free[:last]
			}
		}
		b.mu.Unlock()
		for i := range blocks {
			if blocks[i] == nil {
				blocks[i] = make([]byte, 0, tcpFramePayload)
			}
		}
		return true, nil
	}
	if b.changed == nil {
		b.changed = make(chan struct{})
	}
	changed := b.changed
	b.mu.Unlock()
	return false, changed
}

func (b *replayBudget) recycle(blocks [][]byte) {
	if len(blocks) == 0 {
		return
	}
	b.mu.Lock()
	b.used -= len(blocks) * tcpFramePayload
	for i, block := range blocks {
		if len(b.free) < tcpReplayCachedBlocks {
			b.free = append(b.free, block[:0])
		}
		blocks[i] = nil
	}
	if b.changed != nil {
		close(b.changed)
		b.changed = nil
	}
	b.mu.Unlock()
}

// Idle cached blocks share the 128 MiB budget with live replay data. The manager
// periodically releases them after bulk traffic stops, and flushes them on Stop.
func (b *replayBudget) trim(now time.Time) {
	b.mu.Lock()
	if now.Sub(b.lastUse) >= tcpReplayCacheIdle {
		b.free = nil
	}
	b.mu.Unlock()
}

// Blocks are filled in order. ACKs release complete blocks without copying the
// unacknowledged suffix. A sender copies into its reusable frame buffer under
// the session lock, so old and new stream generations never borrow freed data.
type tcpReplay struct {
	blocks [][]byte
	head   int
	size   int
	budget *replayBudget
}

func (r *tcpReplay) append(data []byte) (bool, <-chan struct{}) {
	if len(data) == 0 {
		return true, nil
	}
	if r.budget == nil {
		r.budget = &replayBudget{}
	}
	space := 0
	if len(r.blocks) > 0 {
		space = tcpFramePayload - len(r.blocks[len(r.blocks)-1])
	}
	count := (max(0, len(data)-space) + tcpFramePayload - 1) / tcpFramePayload
	var one [1][]byte
	allocated := one[:min(count, 1)]
	if count > 1 {
		allocated = make([][]byte, count)
	}
	if ok, changed := r.budget.acquire(allocated); !ok {
		return false, changed
	}
	for len(data) > 0 {
		if len(r.blocks) == 0 || len(r.blocks[len(r.blocks)-1]) == tcpFramePayload {
			r.blocks = append(r.blocks, allocated[0])
			allocated = allocated[1:]
		}
		i := len(r.blocks) - 1
		n := min(len(data), tcpFramePayload-len(r.blocks[i]))
		r.blocks[i] = append(r.blocks[i], data[:n]...)
		r.size += n
		data = data[n:]
	}
	return true, nil
}

func (r *tcpReplay) discard(n int) {
	r.size -= n
	freed := 0
	for n > 0 {
		available := len(r.blocks[freed]) - r.head
		if n < available {
			r.head += n
			break
		}
		n -= available
		r.head = 0
		freed++
	}
	if r.budget != nil {
		r.budget.recycle(r.blocks[:freed])
	}
	r.blocks = r.blocks[freed:]
	if len(r.blocks) == 0 {
		r.blocks = nil
	}
}

func (r *tcpReplay) clear() { r.discard(r.size) }

func (r *tcpReplay) copyAt(dst []byte, offset int) int {
	want := min(len(dst), r.size-offset)
	position := r.head + offset
	written := 0
	for written < want {
		index, start := position/tcpFramePayload, position%tcpFramePayload
		n := copy(dst[written:want], r.blocks[index][start:])
		written += n
		position += n
	}
	return written
}
