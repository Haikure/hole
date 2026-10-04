package core

import "time"

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
	min, current, max int
	lastChange        time.Time
	stableSince       time.Time
	largePackets      int
}

func newBitrateController() *bitrateController {
	return &bitrateController{min: voiceMinBitrate, current: voiceStartBitrate, max: voiceMaxBitrate}
}

func (b *bitrateController) bitrate() int { return b.current }

func (b *bitrateController) update(now time.Time, feedback bitrateFeedback) bitrateDecision {
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
