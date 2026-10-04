package core

import "testing"

func TestVoiceBuffersBoundAndClear(t *testing.T) {
	b := newPCMBuffer(3)
	for i := 0; i < 5; i++ {
		b.push(PCMFrame{Sequence: uint64(i), Samples: make([]int16, DefaultPCMFormat.FrameSamples)})
	}
	if b.len() != 3 || b.dropped() != 2 {
		t.Fatalf("len=%d dropped=%d", b.len(), b.dropped())
	}
	first, _ := b.pop()
	if first.Sequence != 2 {
		t.Fatalf("oldest frame=%d", first.Sequence)
	}
	b.clear()
	if b.len() != 0 {
		t.Fatal("buffer not cleared")
	}
	j := newJitterBuffer(2)
	j.push(jitterFrame{Sequence: 3})
	j.push(jitterFrame{Sequence: 1})
	j.push(jitterFrame{Sequence: 2})
	if j.len() != 2 {
		t.Fatalf("jitter depth=%d", j.len())
	}
	j.dropBefore(4)
	if j.len() != 0 {
		t.Fatal("late frames not dropped")
	}
}

func TestJitterBufferWaitsThenSkipsMissingFrames(t *testing.T) {
	j := newJitterBuffer(8)
	j.push(jitterFrame{Sequence: 1})
	j.push(jitterFrame{Sequence: 3})
	if _, ok := j.popForPlayback(); ok {
		t.Fatal("jitter buffer played before its startup target")
	}
	j.push(jitterFrame{Sequence: 4})
	frame, ok := j.popForPlayback()
	if !ok || frame.Sequence != 1 {
		t.Fatalf("first playback frame=%+v ok=%v", frame, ok)
	}
	j.push(jitterFrame{Sequence: 5})
	frame, ok = j.popForPlayback()
	if !ok || frame.Sequence != 3 {
		t.Fatalf("missing frame was not skipped: %+v ok=%v", frame, ok)
	}
}
