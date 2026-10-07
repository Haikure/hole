package core

import (
	"context"
	"errors"
	"fmt"
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
	if n, err := decoder.Decode(nil, make([]int16, DefaultPCMFormat.FrameSamples)); err != nil || n != DefaultPCMFormat.FrameSamples {
		t.Fatalf("Opus PLC samples=%d err=%v", n, err)
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
	peer := newVoicePeer(ctx, "remote", "transport", 1, link, fakeVoiceCodec{})
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
	deadline := time.Now().Add(time.Second)
	for peer.received.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if peer.received.Load() != 3 {
		t.Fatal("voice packets not received")
	}
	samples, ok := peer.decodeFrame()
	if !ok || len(samples) != DefaultPCMFormat.FrameSamples || samples[0] != 100 {
		t.Fatal("voice packet not decoded")
	}
	stats := peer.snapshot()
	if stats.SentFrames != 1 || stats.DecodedFrames != 1 || stats.MediaState != "active" {
		t.Fatalf("media stats=%+v", stats)
	}
}

func TestVoiceSenderPreservesCaptureGapsAndReceiverResumes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	link := &fakeVoiceLink{ctx: ctx, sent: make(chan []byte, 1), recv: make(chan []byte, 1)}
	p := newVoicePeer(ctx, "remote", "transport", 1, link, fakeVoiceCodec{})
	defer p.close()
	for i, timestamp := range []uint64{0, 960, 1920, 3840, 0, 480000} {
		p.enqueue(PCMFrame{Timestamp: timestamp, Samples: make([]int16, 960)})
		select {
		case data := <-link.sent:
			packet, err := parseVoicePacket(data)
			if err != nil {
				t.Fatal(err)
			}
			wantTimestamp := []uint64{0, 960, 1920, 3840, 4800, 484800}[i]
			if packet.Sequence != uint32(i) || packet.Timestamp != wantTimestamp {
				t.Fatalf("packet=%+v want sequence=%d timestamp=%d", packet, i, wantTimestamp)
			}
			if i == 3 {
				p.decodeFrame() // Consume the short mute's missing playback slot.
			}
			if i == 5 {
				for k := 0; k < voiceJitterLimit; k++ {
					p.decodeFrame() // A long mute exhausts PLC and re-buffers.
				}
			}
			link.recv <- data
			waitVoiceReceived(t, p, uint64(i+1))
			if i < 3 {
				if i == 2 {
					for k := 0; k < 3; k++ {
						p.decodeFrame()
					}
				}
			} else {
				if i == 5 {
					p.decodeFrame() // First startup poll waits for reorder.
				}
				if samples, ok := p.decodeFrame(); !ok || samples[0] != 100 {
					t.Fatal("mute or adapter restart rejected resumed audio")
				}
			}
		case <-time.After(time.Second):
			t.Fatal("sender stalled")
		}
	}
	if p.lost.Load() != 0 || p.decoded.Load() != 6 {
		t.Fatalf("capture gaps were network loss or audio was skipped: %+v", p.snapshot())
	}
}

func waitVoiceReceived(t *testing.T, p *voicePeer, count uint64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for p.received.Load() < count && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if p.received.Load() != count {
		t.Fatal("voice receiver stalled")
	}
}

func TestVoiceReplacementSurvivesOldLinkCleanup(t *testing.T) {
	for _, generation := range []uint64{1, 2} {
		t.Run(fmt.Sprint(generation), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := newVoiceRuntime(ctx, nil, nil, fakeVoiceCodec{})
			defer r.close()
			old := &fakeVoiceLink{ctx: ctx, sent: make(chan []byte, 1), recv: make(chan []byte)}
			next := &fakeVoiceLink{ctx: ctx, sent: make(chan []byte, 1), recv: make(chan []byte, 3)}
			r.bindPeer("remote", "transport", 1, old)
			r.bindPeer("remote", "transport", generation, next)
			r.unbindPeer("remote", old)
			if generation > 1 {
				r.bindPeer("remote", "transport", 1, old) // A delayed old attempt cannot replace the new binding.
			}
			peers := r.snapshot()
			if len(peers) != 1 || peers[0].Generation != generation {
				t.Fatalf("replacement retired by old link: %+v", peers)
			}
			p := r.peers["remote"]
			p.enqueue(PCMFrame{Samples: make([]int16, 960)})
			select {
			case <-next.sent:
			case <-time.After(time.Second):
				t.Fatal("replacement stopped sending")
			}
			for i := uint32(0); i < 3; i++ {
				packet, err := EncodeVoiceDatagram(i, uint64(i)*960, []byte{1})
				if err != nil {
					t.Fatal(err)
				}
				next.recv <- packet
			}
			waitVoiceReceived(t, p, 3)
			if frame := r.mixFrame(0); frame.Samples[0] != 100 {
				t.Fatal("replacement stopped receiving")
			}
			r.unbindPeer("remote", next)
			if len(r.snapshot()) != 0 {
				t.Fatal("owning link could not detach")
			}
		})
	}
}

func TestVoicePacketLossRemainsSeparateFromPlaybackSlots(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	link := &fakeVoiceLink{ctx: ctx, sent: make(chan []byte), recv: make(chan []byte, 3)}
	p := newVoicePeer(ctx, "remote", "transport", 1, link, fakeVoiceCodec{})
	defer p.close()
	for _, packet := range []struct {
		sequence  uint32
		timestamp uint64
	}{
		{^uint32(0) - 1, 0}, {0, 1920}, {1, 480000},
	} {
		data, err := EncodeVoiceDatagram(packet.sequence, packet.timestamp, []byte{1})
		if err != nil {
			t.Fatal(err)
		}
		link.recv <- data
	}
	waitVoiceReceived(t, p, 3)
	p.receiveFeedback(time.Now().Add(voiceReorderWait))
	if p.lost.Load() != 1 || p.highest.Load() != 1 {
		t.Fatalf("wrap or capture gap corrupted network loss: %+v", p.snapshot())
	}
}

func TestVoiceReorderedPacketsPlayWithoutReducingBitrate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	link := &fakeVoiceLink{ctx: ctx, sent: make(chan []byte, 1), recv: make(chan []byte, 6)}
	peer := newVoicePeer(ctx, "remote", "transport", 1, link, fakeVoiceCodec{})
	defer peer.close()
	for _, sequence := range []uint32{0, 2, 1, 3, 5, 4} {
		packet, err := EncodeVoiceDatagram(sequence, uint64(sequence)*960, []byte{1})
		if err != nil {
			t.Fatal(err)
		}
		link.recv <- packet
	}
	waitVoiceReceived(t, peer, 6)
	for i := 0; i < 6; i++ {
		if samples, ok := peer.decodeFrame(); !ok || len(samples) != 960 || samples[0] != 100 {
			t.Fatalf("playback slot %d failed", i)
		}
	}
	feedback, _ := peer.receiveFeedback(time.Now().Add(voiceReorderWait))
	var loss voiceLossWindow
	rate, advanced := loss.observe(feedback)
	decision := peer.bitrate.update(time.Now(), bitrateFeedback{LossRate: rate})
	if !advanced || rate != 0 || feedback.Lost != 0 || feedback.Reordered != 2 || decision.Bitrate != voiceStartBitrate {
		t.Fatalf("reordering reduced bitrate: feedback=%+v rate=%v decision=%+v", feedback, rate, decision)
	}
	if peer.decoded.Load() != 6 || peer.concealed.Load() != 0 {
		t.Fatalf("reordered media was lost: %+v", peer.snapshot())
	}
}

type constantVoiceCodec int16

func (c constantVoiceCodec) Encode(samples []int16, bitrate int) ([]byte, error) {
	return fakeVoiceCodec{}.Encode(samples, bitrate)
}
func (c constantVoiceCodec) Decode(payload []byte, out []int16) (int, error) {
	if len(payload) > 0 {
		for i := range out {
			out[i] = int16(c)
		}
	}
	return len(out), nil
}

func bufferedVoicePeer(value int16, timestamp uint64) *voicePeer {
	p := &voicePeer{ctx: context.Background(), codec: constantVoiceCodec(value), jitter: newJitterBuffer(voiceJitterLimit), bitrate: newBitrateController()}
	for sequence := uint32(0); sequence < 3; sequence++ {
		p.jitter.push(jitterFrame{Sequence: sequence, Timestamp: timestamp + uint64(sequence)*960, Payload: []byte{1}})
	}
	return p
}

func TestVoiceMixUsesLocalClockAndConsumesEachPeerOnce(t *testing.T) {
	a, b := bufferedVoicePeer(100, 0), bufferedVoicePeer(200, 48_000*3600)
	r := &voiceRuntime{peers: map[string]*voicePeer{"a": a, "b": b}}
	for sequence := uint64(0); sequence < 3; sequence++ {
		frame := r.mixFrame(sequence)
		if frame.Sequence != sequence || frame.Timestamp != sequence*960 || frame.Samples[0] != 300 {
			t.Fatalf("mixed frame seq=%d timestamp=%d sample=%d", frame.Sequence, frame.Timestamp, frame.Samples[0])
		}
		if a.jitter.len() != 2-int(sequence) || b.jitter.len() != 2-int(sequence) {
			t.Fatal("mix did not consume exactly one frame per peer")
		}
	}
	if frame := r.mixFrame(3); frame.Samples[0] != 0 {
		t.Fatal("underrun replayed a stale frame")
	}
	if a.concealed.Load() != 1 || b.concealed.Load() != 1 {
		t.Fatal("underrun did not invoke concealment")
	}
}

func TestVoiceMixLimitsOverlapWithoutOrderDependentClipping(t *testing.T) {
	r := &voiceRuntime{peers: map[string]*voicePeer{
		"a": bufferedVoicePeer(30_000, 0), "b": bufferedVoicePeer(30_000, 0), "c": bufferedVoicePeer(-30_000, 0),
	}}
	if sample := r.mixFrame(0).Samples[0]; sample != 30_000 {
		t.Fatalf("cancellation clipped early: %d", sample)
	}
	delete(r.peers, "c")
	if sample := r.mixFrame(1).Samples[0]; sample != 32767 {
		t.Fatalf("overlap limiter sample=%d", sample)
	}
}

type voiceTestSink chan PCMFrame

func (s voiceTestSink) WritePCM(ctx context.Context, frame PCMFrame) error {
	select {
	case s <- frame:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestVoiceRuntimeWritesOneFramePerPlaybackTick(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sink := make(voiceTestSink, 32)
	r := newVoiceRuntimeWithFactory(ctx, nil, nil, nil)
	r.sink = sink
	r.peers["a"] = bufferedVoicePeer(100, 0)
	r.peers["b"] = bufferedVoicePeer(200, 48_000*60)
	done := make(chan struct{})
	go func() { r.mixLoop(); close(done) }()
	defer func() { cancel(); <-done }()
	start := time.Now()
	for sequence := uint64(0); sequence < 4; sequence++ {
		select {
		case frame := <-sink:
			if frame.Sequence != sequence || frame.Timestamp != sequence*960 {
				t.Fatalf("output clock=%+v", frame)
			}
		case <-time.After(time.Second):
			t.Fatal("playback clock stalled")
		}
	}
	if time.Since(start) < 60*time.Millisecond {
		t.Fatal("multiple peers accelerated output")
	}
}

func TestVoiceCaptureKeepsValidFramesAcrossDropsAndAdapterRestarts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := newVoicePCMBridge()
	r := newVoiceRuntimeWithFactory(ctx, nil, nil, nil)
	r.source = source
	p := &voicePeer{ctx: ctx, out: make(chan voiceOutbound, 8)}
	r.peers["remote"] = p
	r.workers.Add(1)
	go func() { defer r.workers.Done(); r.captureLoop() }()
	defer r.close()
	for _, sequence := range []uint64{1, 4, 0} {
		if err := source.push(sequence, sequence*960, make([]int16, 960)); err != nil {
			t.Fatal(err)
		}
		select {
		case item := <-p.out:
			if item.frame.Sequence != sequence {
				t.Fatalf("capture sequence=%d want=%d", item.frame.Sequence, sequence)
			}
		case <-time.After(time.Second):
			t.Fatal("valid frame discarded after a discontinuity")
		}
	}
	if r.captureState() != "capturing" || r.captured.Load() != 3 {
		t.Fatal("capture telemetry not updated")
	}
	r.setMuted(true)
	if r.captureState() != "muted" {
		t.Fatal("muted capture state not reflected")
	}
	// This manually constructed peer owns no workers or cancellation function.
	r.mu.Lock()
	delete(r.peers, "remote")
	r.mu.Unlock()
}

func TestVoiceSnapshotSeparatesLocalMemberTransportAndMedia(t *testing.T) {
	r := &voiceRuntime{peers: map[string]*voicePeer{}}
	c := &iceCoordinator{desired: Request{Config: Config{DeviceName: "self", Voice: VoiceConfig{Enabled: true}}}, online: true, voice: r,
		voiceMembers: map[string]voiceRoomMember{"self": {DeviceName: "self", Voice: true}, "remote": {DeviceName: "remote", Voice: true}}, peers: map[string]*icePeer{}}
	c.peers["remote"] = &icePeer{coordinator: c, stats: PeerTransportSnapshot{PeerID: "remote", TransportID: "t", Generation: 1, Voice: true, State: "active"}}
	peer := bufferedVoicePeer(100, 0)
	peer.peerID, peer.transport, peer.generation = "remote", "t", 1
	r.peers["remote"] = peer
	s := c.voiceSnapshot()
	if s.State != "ready" {
		t.Fatalf("transport readiness invented media activity: %+v", s)
	}
	for _, member := range s.Members {
		if member.DeviceName == "self" && (!member.Local || member.TransportState != "local" || member.MediaState != "waiting") {
			t.Fatalf("local member=%+v", member)
		}
		if member.DeviceName == "remote" && member.MediaState != "waiting" {
			t.Fatalf("remote media=%+v", member)
		}
	}
	peer.decodeFrame()
	r.lastCaptured.Store(time.Now().UnixNano())
	r.captured.Add(1)
	s = c.voiceSnapshot()
	if s.State != "active" || s.CaptureState != "capturing" || s.Peers[0].MediaState != "receiving" || s.Peers[0].DecodedFrames != 1 {
		t.Fatalf("actual media activity missing: %+v", s)
	}
	peer.generation = 2
	if s = c.voiceSnapshot(); s.State != "ready" || s.Peers[0].DecodedFrames != 0 {
		t.Fatalf("different generation leaked stale media: %+v", s)
	}
}

func TestVoiceSnapshotClonePreservesAndIsolatesMedia(t *testing.T) {
	original := VoiceSnapshot{Members: []VoiceMemberSnapshot{{DeviceName: "self", Local: true}},
		Peers: []VoicePeerSnapshot{{PeerID: "remote", MediaState: "receiving", DecodedFrames: 42, Error: &Fault{Message: "decode failed"}}}}
	cloned := original.clone()
	if cloned.Peers[0].PeerID != "remote" || cloned.Peers[0].DecodedFrames != 42 {
		t.Fatal("clone lost peer media statistics")
	}
	cloned.Members[0].DeviceName = "changed"
	cloned.Peers[0].Error.Message = "changed"
	if original.Members[0].DeviceName != "self" || original.Peers[0].Error.Message != "decode failed" {
		t.Fatal("clone shares mutable snapshot data")
	}
}

func TestVoiceMediaErrorsAndExpiredActivityAreVisible(t *testing.T) {
	p := bufferedVoicePeer(100, 0)
	p.lastSent.Store(time.Now().UnixNano())
	p.lastDecoded.Store(time.Now().UnixNano())
	p.setError(errors.New("decode failed"))
	if p.snapshot().MediaState != "paused" {
		t.Fatal("recent activity hid a media failure")
	}
	p.mu.Lock()
	p.error = nil
	p.mu.Unlock()
	p.lastSent.Store(time.Now().Add(-3 * time.Second).UnixNano())
	p.lastDecoded.Store(time.Now().Add(-3 * time.Second).UnixNano())
	if p.snapshot().MediaState != "waiting" {
		t.Fatal("stalled media stayed active")
	}
}
