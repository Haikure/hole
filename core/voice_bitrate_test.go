package core

import (
	"sync"
	"testing"
	"time"
)

func TestVoiceBitrateThresholds(t *testing.T) {
	b := newBitrateController()
	start := time.Unix(100, 0)
	decision := b.update(start, bitrateFeedback{LossRate: .10, TargetJitter: 3, JitterDepth: 3})
	if !decision.Changed || decision.Bitrate != 19200 {
		t.Fatalf("loss decision=%+v", decision)
	}
	decision = b.update(start.Add(time.Second), bitrateFeedback{LossRate: .20})
	if decision.Changed {
		t.Fatalf("changed during hold: %+v", decision)
	}
	decision = b.update(start.Add(2*time.Second), bitrateFeedback{LossRate: .20})
	if decision.Bitrate != voiceMinBitrate {
		t.Fatalf("high loss bitrate=%d", decision.Bitrate)
	}
	for i := 0; i < 6; i++ {
		b.update(start.Add(time.Duration(10+i)*time.Second), bitrateFeedback{LossRate: .01, QueueDepth: 1})
	}
	if b.bitrate() <= voiceMinBitrate {
		t.Fatalf("stable network did not increase bitrate=%d", b.bitrate())
	}
}

func TestVoiceBitrateConcurrentReadsAndFeedback(t *testing.T) {
	b := newBitrateController()
	var workers sync.WaitGroup
	start := make(chan struct{})
	for worker := 0; worker < 3; worker++ {
		workers.Go(func() {
			<-start
			for i := 0; i < 2000; i++ {
				if rate := b.bitrate(); rate < voiceMinBitrate || rate > voiceMaxBitrate {
					t.Errorf("invalid bitrate %d", rate)
				}
			}
		})
	}
	close(start)
	for i := 0; i < 2000; i++ {
		loss := 0.0
		if i%4 == 0 {
			loss = .2
		}
		b.update(time.Unix(int64(i)*10, 0), bitrateFeedback{LossRate: loss})
	}
	workers.Wait()
}

func TestVoiceLossWindowRecoversWithoutLifetimeAverage(t *testing.T) {
	var window voiceLossWindow
	b := newBitrateController()
	start := time.Unix(100, 0)
	rate, ok := window.observe(voiceFeedback{HighestSequence: 99, Lost: 30})
	if !ok || rate != .3 {
		t.Fatalf("initial loss=%v advanced=%v", rate, ok)
	}
	b.update(start, bitrateFeedback{LossRate: rate})
	if b.bitrate() != voiceMinBitrate {
		t.Fatal("actual loss did not reduce bitrate")
	}
	for i := uint32(1); i <= 10; i++ {
		feedback := voiceFeedback{HighestSequence: 99 + i*50, Lost: 30}
		rate, ok = window.observe(feedback)
		if !ok || rate != 0 {
			t.Fatal("past loss polluted the healthy interval")
		}
		b.update(start.Add(time.Duration(i)*time.Second), bitrateFeedback{LossRate: rate})
		if _, ok := window.observe(feedback); ok {
			t.Fatal("idle/duplicate report counted as new traffic")
		}
		if _, ok := window.observe(voiceFeedback{HighestSequence: 99, Lost: 30}); ok {
			t.Fatal("reordered feedback changed the baseline")
		}
	}
	if b.bitrate() <= voiceMinBitrate {
		t.Fatal("healthy traffic did not restore bitrate")
	}
}

func TestVoiceLossWindowWrap(t *testing.T) {
	window := voiceLossWindow{started: true, previous: voiceFeedback{HighestSequence: ^uint32(0) - 1, Lost: ^uint32(0)}}
	rate, ok := window.observe(voiceFeedback{HighestSequence: 1, Lost: 0})
	if !ok || rate != 1.0/3 {
		t.Fatalf("wrapped interval loss=%v advanced=%v", rate, ok)
	}
}
