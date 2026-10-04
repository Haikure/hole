package core

import (
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
