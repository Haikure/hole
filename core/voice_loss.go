package core

import "time"

const voiceReorderWait = voiceJitterLimit * voiceFrameMillis * time.Millisecond

// voiceReceiveWindow settles packet IDs independently of sampling/playout slots.
// Only settled losses are cumulative; pending gaps may still be reordered packets.
// Callers serialize access with voicePeer.mu.
type voiceReceiveWindow struct {
	started   bool
	highest   uint32
	lost      uint64
	reordered uint64
	missing   map[uint32]time.Time
}

func (w *voiceReceiveWindow) expire(now time.Time) {
	for sequence, deadline := range w.missing {
		if !now.Before(deadline) {
			delete(w.missing, sequence)
			w.lost++
		}
	}
}

func (w *voiceReceiveWindow) observe(sequence uint32, now time.Time) {
	w.expire(now)
	if !w.started {
		w.started, w.highest = true, sequence
		w.missing = make(map[uint32]time.Time)
		return
	}
	if !voiceSequenceBefore(w.highest, sequence) {
		w.reordered++
		delete(w.missing, sequence)
		return
	}
	// A gap more than eight packets behind the newest packet is outside the
	// reorder window. Large jumps take bounded work and bounded memory.
	for pending := range w.missing {
		if sequence-pending > voiceJitterLimit {
			delete(w.missing, pending)
			w.lost++
		}
	}
	gap := sequence - w.highest - 1
	retained := min(gap, uint32(voiceJitterLimit))
	w.lost += uint64(gap - retained)
	for offset := uint32(1); offset <= retained; offset++ {
		w.missing[sequence-offset] = now.Add(voiceReorderWait)
	}
	w.highest = sequence
}

// Feedback's highest ID covers only settled packets. Freezing it before a
// pending gap keeps the numerator and denominator in the same loss interval,
// even when feedback is sent before the missing packet arrives or during mute.
func (w *voiceReceiveWindow) feedback(now time.Time) (voiceFeedback, bool) {
	w.expire(now)
	if !w.started {
		return voiceFeedback{}, false
	}
	highest := w.highest
	for sequence := range w.missing {
		if voiceSequenceBefore(sequence-1, highest) {
			highest = sequence - 1
		}
	}
	return voiceFeedback{HighestSequence: highest, Lost: uint32(w.lost), Reordered: uint32(w.reordered)}, true
}
