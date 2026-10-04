package core

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPCMFormatAndFrameValidation(t *testing.T) {
	if err := DefaultPCMFormat.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (PCMFormat{SampleRate: 16000, Channels: 1, FrameSamples: 320}).Validate(); err == nil {
		t.Fatal("unexpectedly accepted unsupported format")
	}
	frame := PCMFrame{Samples: make([]int16, DefaultPCMFormat.FrameSamples)}
	if err := frame.Validate(); err != nil {
		t.Fatal(err)
	}
	frame.Samples = frame.Samples[:10]
	if err := frame.Validate(); err == nil {
		t.Fatal("unexpectedly accepted short frame")
	}
}

func TestPureGoOpusCodecUsesFixedPCMFormat(t *testing.T) {
	decoder, err := newPureGoOpusCodec()
	if err != nil {
		t.Fatal(err)
	}
	packet, err := decoder.Encode(make([]int16, DefaultPCMFormat.FrameSamples), voiceStartBitrate)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := decoder.Decode(packet, make([]int16, DefaultPCMFormat.FrameSamples)); err != nil || n != DefaultPCMFormat.FrameSamples {
		t.Fatalf("Opus round trip samples=%d err=%v", n, err)
	}
}

type fakeVoiceCodec struct{}

func (fakeVoiceCodec) Encode(samples []int16, _ int) ([]byte, error) {
	if len(samples) != DefaultPCMFormat.FrameSamples {
		return nil, errors.New("bad samples")
	}
	return []byte{1, 2, 3}, nil
}
func (fakeVoiceCodec) Decode(payload []byte, out []int16) (int, error) {
	if len(payload) == 0 {
		return 0, errors.New("empty")
	}
	for i := range out {
		out[i] = 100
	}
	return len(out), nil
}

type fakeVoiceLink struct {
	ctx  context.Context
	sent chan []byte
	recv chan []byte
}

func (l *fakeVoiceLink) SendVoiceDatagram(data []byte) error {
	select {
	case l.sent <- data:
		return nil
	case <-l.ctx.Done():
		return l.ctx.Err()
	}
}
func (l *fakeVoiceLink) ReceiveVoiceDatagram(ctx context.Context) ([]byte, error) {
	select {
	case data := <-l.recv:
		return data, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (l *fakeVoiceLink) SendVoiceFeedback([]byte) error { return nil }
func (l *fakeVoiceLink) ReceiveVoiceFeedback(ctx context.Context) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (l *fakeVoiceLink) Context() context.Context { return l.ctx }

func TestVoicePeerFakePCMPath(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	link := &fakeVoiceLink{ctx: ctx, sent: make(chan []byte, 2), recv: make(chan []byte, 4)}
	decoded := make(chan voiceDecoded, 1)
	peer := newVoicePeer(ctx, "remote", "transport", 1, link, fakeVoiceCodec{}, decoded)
	defer peer.close()
	peer.enqueue(PCMFrame{Timestamp: 960, Samples: make([]int16, DefaultPCMFormat.FrameSamples)})
	select {
	case data := <-link.sent:
		if _, _, _, err := DecodeVoiceDatagram(data); err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("voice packet not sent")
	}
	for sequence := uint32(1); sequence <= 3; sequence++ {
		packet, err := EncodeVoiceDatagram(sequence, uint64(sequence)*960, []byte{4, 5, 6})
		if err != nil {
			t.Fatal(err)
		}
		link.recv <- packet
	}
	select {
	case got := <-decoded:
		if len(got.frame.Samples) != DefaultPCMFormat.FrameSamples {
			t.Fatalf("decoded samples=%d", len(got.frame.Samples))
		}
	case <-time.After(time.Second):
		t.Fatal("voice packet not decoded")
	}
}
