package core

import (
	"sync"
	"time"
)

const (
	voiceMinBitrate   = 12000
	voiceStartBitrate = 24000
	voiceMaxBitrate   = 32000
)

type bitrateFeedback struct {
	LossRate         float64
	JitterDepth      int
	TargetJitter     int
	QueueDepth       int
	DatagramTooLarge int
}

type bitrateDecision struct {
	Bitrate int
	Changed bool
	Reason  string
}

type bitrateController struct {
	mu                sync.RWMutex
	min, current, max int
	lastChange        time.Time
	stableSince       time.Time
	largePackets      int
}

func newBitrateController() *bitrateController {
	return &bitrateController{min: voiceMinBitrate, current: voiceStartBitrate, max: voiceMaxBitrate}
}

func (b *bitrateController) bitrate() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.current
}

func (b *bitrateController) update(now time.Time, feedback bitrateFeedback) bitrateDecision {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.lastChange.IsZero() {
		b.lastChange = now.Add(-2 * time.Second)
	}
	if feedback.DatagramTooLarge > 0 {
		b.largePackets += feedback.DatagramTooLarge
	} else {
		b.largePackets = 0
	}
	if now.Sub(b.lastChange) < 2*time.Second {
		return bitrateDecision{Bitrate: b.current}
	}
	if feedback.LossRate > 0.15 || b.largePackets >= 2 {
		next := b.current
		if next < b.min {
			next = b.min
		}
		if next > b.min {
			next = b.min
		}
		return b.change(now, next, "high_loss_or_datagram_too_large")
	}
	if feedback.LossRate > 0.08 || (feedback.TargetJitter > 0 && feedback.JitterDepth > feedback.TargetJitter) {
		return b.change(now, max(b.min, int(float64(b.current)*0.8)), "loss_or_jitter")
	}
	if feedback.LossRate < 0.02 && feedback.QueueDepth <= 3 && feedback.DatagramTooLarge == 0 {
		if b.stableSince.IsZero() {
			b.stableSince = now
		}
		if now.Sub(b.stableSince) >= 5*time.Second {
			return b.change(now, min(b.max, int(float64(b.current)*1.1)), "stable_network")
		}
	} else {
		b.stableSince = time.Time{}
	}
	return bitrateDecision{Bitrate: b.current}
}

// Feedback counters are cumulative, but adaptation must describe the interval
// since the last report. Ignore idle, duplicate and reordered reports, including
// across the uint32 sequence wrap, rather than treating silence as good traffic.
type voiceLossWindow struct {
	previous voiceFeedback
	started  bool
}

func (w *voiceLossWindow) observe(feedback voiceFeedback) (float64, bool) {
	total := uint64(feedback.HighestSequence) + 1
	lost := uint64(feedback.Lost)
	if w.started {
		if !voiceSequenceBefore(w.previous.HighestSequence, feedback.HighestSequence) {
			return 0, false
		}
		total = uint64(feedback.HighestSequence - w.previous.HighestSequence)
		lost = uint64(feedback.Lost - w.previous.Lost)
	}
	w.previous, w.started = feedback, true
	return float64(min(lost, total)) / float64(total), true
}

func (b *bitrateController) change(now time.Time, next int, reason string) bitrateDecision {
	if next < b.min {
		next = b.min
	}
	if next > b.max {
		next = b.max
	}
	if next == b.current {
		return bitrateDecision{Bitrate: b.current, Reason: reason}
	}
	b.current, b.lastChange, b.stableSince = next, now, time.Time{}
	return bitrateDecision{Bitrate: next, Changed: true, Reason: reason}
}
