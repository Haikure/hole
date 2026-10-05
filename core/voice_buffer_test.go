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

func TestJitterBufferWaitsThenConcealsMissingFrames(t *testing.T) {
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
	if !ok || frame.Sequence != 2 || frame.Payload != nil {
		t.Fatalf("missing frame was not concealed: %+v ok=%v", frame, ok)
	}
	frame, ok = j.popForPlayback()
	if !ok || frame.Sequence != 3 {
		t.Fatalf("frame after loss was skipped: %+v ok=%v", frame, ok)
	}
}

func TestVoiceBridgeRetainsFullBatch(t *testing.T) {
	b := newVoicePCMBridge()
	if err := b.push(5, 4800, make([]int16, voiceBridgeBatch*DefaultPCMFormat.FrameSamples)); err != nil {
		t.Fatal(err)
	}
	for i := uint64(0); i < voiceBridgeBatch; i++ {
		frame, ok := b.input.popNow()
		if !ok || frame.Sequence != 5+i || frame.Timestamp != 4800+i*960 {
			t.Fatalf("batch frame=%+v ok=%v", frame, ok)
		}
	}
	if b.input.drops.Load() != 0 {
		t.Fatal("one bridge batch overflowed the queue")
	}
}

func TestJitterBufferRejectsLateFramesAndRestartsAfterUnderrun(t *testing.T) {
	j := newJitterBuffer(voiceJitterLimit)
	for i := uint32(0); i < 3; i++ {
		j.push(jitterFrame{Sequence: i, Payload: []byte{1}})
	}
	for i := 0; i < 3; i++ {
		j.popForPlayback()
	}
	j.push(jitterFrame{Sequence: 1, Payload: []byte{1}})
	if j.len() != 0 {
		t.Fatal("played frame reentered the jitter buffer")
	}
	for i := 0; i < voiceJitterLimit; i++ {
		frame, ok := j.popForPlayback()
		if !ok || len(frame.Payload) != 0 {
			t.Fatal("underrun did not produce a PLC slot")
		}
	}
	if _, ok := j.popForPlayback(); ok {
		t.Fatal("PLC continued without bound")
	}
	j.push(jitterFrame{Sequence: 500, Payload: []byte{1}})
	if _, ok := j.popForPlayback(); ok {
		t.Fatal("resumed stream skipped rebuffering")
	}
	if frame, ok := j.popForPlayback(); !ok || frame.Sequence != 500 {
		t.Fatal("resumed stream failed to resync")
	}
}

func TestJitterBufferSequenceWrapAndLargeJump(t *testing.T) {
	j := newJitterBuffer(voiceJitterLimit)
	for _, sequence := range []uint32{^uint32(0) - 1, ^uint32(0), 0} {
		j.push(jitterFrame{Sequence: sequence, Payload: []byte{1}})
	}
	for _, sequence := range []uint32{^uint32(0) - 1, ^uint32(0), 0} {
		if frame, ok := j.popForPlayback(); !ok || frame.Sequence != sequence {
			t.Fatal("sequence wrap reordered playback")
		}
	}
	j.push(jitterFrame{Sequence: 100, Payload: []byte{1}})
	if frame, ok := j.popForPlayback(); !ok || frame.Sequence != 100 {
		t.Fatal("large jump accumulated unbounded latency")
	}
}
