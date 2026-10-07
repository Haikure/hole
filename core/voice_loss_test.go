package core

import (
	"testing"
	"time"
)

func TestVoiceReceiveWindowWaitsAcrossFeedbackAndSettlesRealLoss(t *testing.T) {
	now := time.Unix(100, 0)
	var receive voiceReceiveWindow
	var loss voiceLossWindow
	if _, ok := receive.feedback(now); ok {
		t.Fatal("feedback invented traffic before receiving media")
	}
	receive.observe(0, now)
	receive.observe(2, now)
	feedback, _ := receive.feedback(now.Add(voiceReorderWait - time.Millisecond))
	if feedback.HighestSequence != 0 || feedback.Lost != 0 {
		t.Fatalf("pending gap was reported as loss: %+v", feedback)
	}
	loss.observe(feedback)
	receive.observe(1, now.Add(voiceReorderWait-time.Millisecond))
	feedback, _ = receive.feedback(now.Add(voiceReorderWait))
	if rate, advanced := loss.observe(feedback); !advanced || rate != 0 || feedback.HighestSequence != 2 {
		t.Fatalf("recovered gap affected feedback: %+v rate=%v", feedback, rate)
	}
	receive.observe(4, now.Add(time.Second))
	feedback, _ = receive.feedback(now.Add(time.Second + voiceReorderWait))
	if rate, advanced := loss.observe(feedback); !advanced || rate != .5 || feedback.Lost != 1 || feedback.HighestSequence != 4 {
		t.Fatalf("idle tail did not settle real loss: %+v rate=%v", feedback, rate)
	}
	// A duplicate or a packet outside the window cannot subtract confirmed loss.
	receive.observe(3, now.Add(2*time.Second))
	receive.observe(3, now.Add(2*time.Second))
	feedback, _ = receive.feedback(now.Add(2 * time.Second))
	if feedback.Lost != 1 {
		t.Fatalf("late/duplicate packet corrupted cumulative loss: %+v", feedback)
	}
	if _, advanced := loss.observe(feedback); advanced {
		t.Fatal("duplicate feedback counted as new traffic")
	}
}

func TestVoiceReceiveWindowWrapAndBoundedLargeGap(t *testing.T) {
	now := time.Unix(100, 0)
	var receive voiceReceiveWindow
	receive.observe(^uint32(0)-1, now)
	receive.observe(0, now)
	feedback, _ := receive.feedback(now)
	if feedback.HighestSequence != ^uint32(0)-1 || feedback.Lost != 0 {
		t.Fatalf("wrap finalized a pending gap: %+v", feedback)
	}
	receive.observe(^uint32(0), now)
	feedback, _ = receive.feedback(now)
	if feedback.HighestSequence != 0 || feedback.Lost != 0 {
		t.Fatalf("wrap recovery failed: %+v", feedback)
	}
	receive.observe(1_000_000, now)
	feedback, _ = receive.feedback(now)
	if len(receive.missing) != voiceJitterLimit || feedback.Lost != 999_999-voiceJitterLimit || feedback.HighestSequence != 999_999-voiceJitterLimit {
		t.Fatalf("large gap was not bounded: %+v pending=%d", feedback, len(receive.missing))
	}
	feedback, _ = receive.feedback(now.Add(voiceReorderWait))
	if len(receive.missing) != 0 || feedback.Lost != 999_999 || feedback.HighestSequence != 1_000_000 {
		t.Fatalf("large gap tail was not settled: %+v", feedback)
	}
}

func TestVoiceReceiveWindowExpiresOldGapsAsTrafficAdvances(t *testing.T) {
	now := time.Unix(100, 0)
	var receive voiceReceiveWindow
	receive.observe(0, now)
	for sequence := uint32(2); sequence <= 10; sequence++ {
		receive.observe(sequence, now)
	}
	feedback, _ := receive.feedback(now)
	if feedback.Lost != 1 || feedback.HighestSequence != 10 || len(receive.missing) != 0 {
		t.Fatalf("packet horizon did not settle the old gap: %+v", feedback)
	}
}
