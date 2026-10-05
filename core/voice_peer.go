package core

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

type voiceLink interface {
	SendVoiceDatagram([]byte) error
	ReceiveVoiceDatagram(context.Context) ([]byte, error)
	SendVoiceFeedback([]byte) error
	ReceiveVoiceFeedback(context.Context) ([]byte, error)
	Context() context.Context
}

// voiceCodec is intentionally small so tests and future platform-neutral
// codecs can be supplied without changing the network or PCM pipeline.
type voiceCodec interface {
	Encode([]int16, int) ([]byte, error)
	Decode([]byte, []int16) (int, error)
}

type voiceCodecFactory func() (voiceCodec, error)

type voiceOutbound struct {
	frame    PCMFrame
	enqueued time.Time
}

type voicePeer struct {
	ctx           context.Context
	cancel        context.CancelFunc
	peerID        string
	transport     string
	generation    uint64
	link          voiceLink
	codec         voiceCodec
	bitrate       *bitrateController
	out           chan voiceOutbound
	jitter        *jitterBuffer
	queueDrops    atomic.Uint64
	datagramDrops atomic.Uint64
	lateFrames    atomic.Uint64
	reordered     atomic.Uint64
	received      atomic.Uint64
	sent          atomic.Uint64
	decoded       atomic.Uint64
	concealed     atomic.Uint64
	lastSent      atomic.Int64
	lastDecoded   atomic.Int64
	lost          atomic.Uint64
	highest       atomic.Uint32
	mu            sync.Mutex
	state         string
	error         *Fault
	workers       sync.WaitGroup
	closeOnce     sync.Once
}

func newVoicePeer(parent context.Context, peerID, transportID string, generation uint64, link voiceLink, codec voiceCodec) *voicePeer {
	ctx, cancel := context.WithCancel(parent)
	p := &voicePeer{ctx: ctx, cancel: cancel, peerID: peerID, transport: transportID, generation: generation, link: link, codec: codec, bitrate: newBitrateController(), out: make(chan voiceOutbound, voiceQueueFrames), jitter: newJitterBuffer(voiceJitterLimit), state: "connecting"}
	p.workers.Add(4)
	go func() { defer p.workers.Done(); p.writeLoop() }()
	go func() { defer p.workers.Done(); p.readLoop() }()
	go func() { defer p.workers.Done(); p.feedbackLoop() }()
	go func() { defer p.workers.Done(); p.feedbackReadLoop() }()
	return p
}

func (p *voicePeer) enqueue(frame PCMFrame) {
	if p.ctx.Err() != nil {
		return
	}
	item := voiceOutbound{frame: frame.Clone(), enqueued: time.Now()}
	select {
	case p.out <- item:
		return
	default:
	}
	select {
	case <-p.out:
		p.queueDrops.Add(1)
	default:
	}
	select {
	case p.out <- item:
	default:
		p.queueDrops.Add(1)
	}
}

func (p *voicePeer) writeLoop() {
	var sequence uint32
	var previousTimestamp uint64
	haveTimestamp := false
	for {
		select {
		case <-p.ctx.Done():
			return
		case item := <-p.out:
			if p.codec == nil {
				p.setError(errors.New("voice Opus encoder is unavailable"))
				continue
			}
			payload, err := p.codec.Encode(item.frame.Samples, p.bitrate.bitrate())
			if err != nil {
				p.setError(err)
				continue
			}
			// Preserve capture gaps (including mute) on the receiver's playout
			// timeline. A restarted adapter may reset its sampling clock to zero.
			if haveTimestamp {
				step := uint64(1)
				if item.frame.Timestamp > previousTimestamp {
					step = max(step, (item.frame.Timestamp-previousTimestamp)/uint64(DefaultPCMFormat.FrameSamples))
				}
				sequence += uint32(step)
			}
			previousTimestamp, haveTimestamp = item.frame.Timestamp, true
			packet, err := marshalVoicePacket(voicePacket{Version: voiceWireVersion, Kind: voiceKindMedia, Sequence: sequence, Timestamp: item.frame.Timestamp, Codec: voiceCodecOpus, Payload: payload})
			if err != nil {
				p.setError(err)
				continue
			}
			if err = p.link.SendVoiceDatagram(packet); err != nil {
				p.datagramDrops.Add(1)
				p.setError(err)
			} else {
				p.sent.Add(1)
				p.lastSent.Store(time.Now().UnixNano())
			}
		}
	}
}

func (p *voicePeer) readLoop() {
	for {
		data, err := p.link.ReceiveVoiceDatagram(p.ctx)
		if err != nil {
			return
		}
		packet, err := parseVoicePacket(data)
		if err != nil || packet.Kind != voiceKindMedia {
			p.datagramDrops.Add(1)
			continue
		}
		previous := p.highest.Load()
		if p.received.Load() > 0 && packet.Sequence > previous+1 {
			p.lost.Add(uint64(packet.Sequence - previous - 1))
		}
		if p.received.Load() > 0 && packet.Sequence <= previous {
			p.reordered.Add(1)
		}
		if packet.Sequence > previous {
			p.highest.Store(packet.Sequence)
		}
		p.received.Add(1)
		p.jitter.push(jitterFrame{Sequence: packet.Sequence, Timestamp: packet.Timestamp, Payload: packet.Payload})
	}
}

// Only the runtime playback clock calls decodeFrame, so each decoder advances
// once per output period regardless of packet arrival order or peer count.
func (p *voicePeer) decodeFrame() ([]int16, bool) {
	if p.ctx.Err() != nil {
		return nil, false
	}
	frame, ok := p.jitter.popForPlayback()
	if !ok {
		return nil, false
	}
	samples := make([]int16, DefaultPCMFormat.FrameSamples)
	if p.codec == nil {
		p.setError(errors.New("voice Opus decoder is unavailable"))
		return samples, true
	}
	n, err := p.codec.Decode(frame.Payload, samples)
	if err != nil || n != len(samples) {
		p.datagramDrops.Add(1)
		if err != nil && len(frame.Payload) > 0 {
			p.setError(err)
		}
		clear(samples)
		p.concealed.Add(1)
	} else if len(frame.Payload) == 0 {
		p.concealed.Add(1)
	} else {
		p.decoded.Add(1)
		p.lastDecoded.Store(time.Now().UnixNano())
		p.mu.Lock()
		p.state, p.error = "active", nil
		p.mu.Unlock()
	}
	return samples, true
}

func (p *voicePeer) feedbackLoop() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			feedback := voiceFeedback{HighestSequence: p.highest.Load(), Lost: uint32(p.lost.Load()), Reordered: uint32(p.reordered.Load()), JitterDepth: uint16(p.jitter.len()), LateFrames: uint32(p.lateFrames.Load())}
			packet, err := EncodeVoiceFeedback(feedback.HighestSequence, uint64(time.Now().UnixMilli()), marshalVoiceFeedback(feedback))
			if err != nil {
				continue
			}
			if err := p.link.SendVoiceFeedback(packet); err != nil {
				p.datagramDrops.Add(1)
				return
			}
		}
	}
}

func (p *voicePeer) feedbackReadLoop() {
	for {
		data, err := p.link.ReceiveVoiceFeedback(p.ctx)
		if err != nil {
			return
		}
		_, _, payload, err := DecodeVoiceFeedback(data)
		if err != nil {
			p.datagramDrops.Add(1)
			continue
		}
		feedback, err := parseVoiceFeedback(payload)
		if err != nil {
			p.datagramDrops.Add(1)
			continue
		}
		decision := p.bitrate.update(time.Now(), bitrateFeedback{
			LossRate: lossRate(feedback), JitterDepth: int(feedback.JitterDepth),
			TargetJitter: 4, QueueDepth: len(p.out),
		})
		if decision.Changed {
			p.mu.Lock()
			p.state = "active"
			p.mu.Unlock()
		}
	}
}

func lossRate(feedback voiceFeedback) float64 {
	total := uint64(feedback.HighestSequence) + 1
	if total == 0 {
		return 0
	}
	return float64(feedback.Lost) / float64(total)
}

func (p *voicePeer) setError(err error) {
	if err == nil {
		return
	}
	p.mu.Lock()
	p.state = "paused"
	p.error = classifyError(err)
	p.mu.Unlock()
}

func (p *voicePeer) snapshot() VoicePeerSnapshot {
	p.mu.Lock()
	state, fault := p.state, p.error
	p.mu.Unlock()
	media := "waiting"
	sending, receiving := voiceRecently(p.lastSent.Load()), voiceRecently(p.lastDecoded.Load())
	switch {
	case fault != nil:
		media = "paused"
	case sending && receiving:
		media = "active"
	case receiving:
		media = "receiving"
	case sending:
		media = "sending"
	}
	s := VoicePeerSnapshot{PeerID: p.peerID, TransportID: p.transport, Generation: p.generation, State: state, MediaState: media, SentFrames: p.sent.Load(), ReceivedFrames: p.received.Load(), DecodedFrames: p.decoded.Load(), ConcealedFrames: p.concealed.Load(), PacketLoss: p.lost.Load(), Reordered: p.reordered.Load(), Bitrate: p.bitrate.bitrate(), QueueDepth: len(p.out), QueueDrops: p.queueDrops.Load(), DatagramDrops: p.datagramDrops.Load(), LateFrames: p.lateFrames.Load(), JitterDepth: p.jitter.len()}
	if fault != nil {
		copy := *fault
		s.Error = &copy
	}
	return s
}

func voiceRecently(timestamp int64) bool {
	return timestamp > 0 && time.Since(time.Unix(0, timestamp)) < 2*time.Second
}

func (p *voicePeer) close() {
	p.closeOnce.Do(func() { p.cancel(); p.jitter.clear(); p.workers.Wait() })
}

type voiceRuntime struct {
	ctx          context.Context
	cancel       context.CancelFunc
	source       PCMSource
	sink         PCMSink
	codec        voiceCodec
	codecFactory voiceCodecFactory
	bridge       *voicePCMBridge
	muted        atomic.Bool
	mu           sync.Mutex
	peers        map[string]*voicePeer
	captured     atomic.Uint64
	lastCaptured atomic.Int64
	mixed        atomic.Uint64
	workers      sync.WaitGroup
	closed       bool
}

func newConfiguredVoiceRuntime(parent context.Context) *voiceRuntime {
	bridge := newVoicePCMBridge()
	runtime := newVoiceRuntimeWithFactory(parent, bridge, bridge, func() (voiceCodec, error) {
		return newPureGoOpusCodec()
	})
	runtime.bridge = bridge
	return runtime
}

func newVoiceRuntime(parent context.Context, source PCMSource, sink PCMSink, codec voiceCodec) *voiceRuntime {
	return newVoiceRuntimeWithFactory(parent, source, sink, func() (voiceCodec, error) {
		if codec == nil {
			return nil, errors.New("voice codec is unavailable")
		}
		return codec, nil
	})
}

func newVoiceRuntimeWithFactory(parent context.Context, source PCMSource, sink PCMSink, factory voiceCodecFactory) *voiceRuntime {
	ctx, cancel := context.WithCancel(parent)
	r := &voiceRuntime{ctx: ctx, cancel: cancel, source: source, sink: sink, codecFactory: factory, peers: map[string]*voicePeer{}}
	if source != nil {
		r.workers.Add(1)
		go func() { defer r.workers.Done(); r.captureLoop() }()
	}
	if sink != nil {
		r.workers.Add(1)
		go func() { defer r.workers.Done(); r.mixLoop() }()
	}
	return r
}

func (r *voiceRuntime) setMuted(muted bool) { r.muted.Store(muted) }

func (r *voiceRuntime) pushPCM(sequence, timestamp uint64, samples []int16) error {
	if r.bridge == nil {
		return errors.New("voice PCM bridge is unavailable")
	}
	return r.bridge.push(sequence, timestamp, samples)
}

func (r *voiceRuntime) pullPCM(maxFrames int) []int16 {
	if r.bridge == nil {
		return nil
	}
	return r.bridge.pull(maxFrames)
}

func (r *voiceRuntime) bindPeer(peerID, transportID string, generation uint64, link voiceLink) {
	r.mu.Lock()
	old := r.peers[peerID]
	if old != nil && old.transport == transportID && old.generation == generation {
		r.mu.Unlock()
		return
	}
	if old != nil {
		delete(r.peers, peerID)
	}
	var codec voiceCodec
	if r.codecFactory != nil {
		codec, _ = r.codecFactory()
	}
	p := newVoicePeer(r.ctx, peerID, transportID, generation, link, codec)
	r.peers[peerID] = p
	r.mu.Unlock()
	if old != nil {
		old.close()
	}
}

func (r *voiceRuntime) unbindPeer(peerID string) {
	r.mu.Lock()
	p := r.peers[peerID]
	delete(r.peers, peerID)
	r.mu.Unlock()
	if p != nil {
		p.close()
	}
}

func (r *voiceRuntime) captureLoop() {
	for {
		var frame PCMFrame
		if err := r.source.ReadPCM(r.ctx, &frame); err != nil {
			return
		}
		if frame.Validate() != nil {
			continue
		}
		r.captured.Add(1)
		r.lastCaptured.Store(time.Now().UnixNano())
		if r.muted.Load() {
			continue
		}
		r.mu.Lock()
		for _, peer := range r.peers {
			peer.enqueue(frame)
		}
		r.mu.Unlock()
	}
}

func (r *voiceRuntime) mixLoop() {
	ticker := time.NewTicker(voiceFrameMillis * time.Millisecond)
	defer ticker.Stop()
	var sequence uint64
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			if err := r.sink.WritePCM(r.ctx, r.mixFrame(sequence)); err != nil {
				return
			}
			r.mixed.Add(1)
			sequence++
		}
	}
}

func (r *voiceRuntime) mixFrame(sequence uint64) PCMFrame {
	r.mu.Lock()
	peers := make([]*voicePeer, 0, len(r.peers))
	for _, peer := range r.peers {
		peers = append(peers, peer)
	}
	r.mu.Unlock()
	sums := make([]int32, DefaultPCMFormat.FrameSamples)
	for _, peer := range peers {
		samples, ok := peer.decodeFrame()
		if !ok {
			continue
		}
		for i, sample := range samples {
			sums[i] += int32(sample)
		}
	}
	// Apply one gain to the whole frame when voices overlap beyond PCM16 range.
	// Summing in int32 before limiting also keeps cancellation order-independent.
	peak := int32(32767)
	for _, sum := range sums {
		if sum > peak {
			peak = sum
		}
		if -sum > peak {
			peak = -sum
		}
	}
	samples := make([]int16, len(sums))
	for i, sum := range sums {
		samples[i] = int16(int64(sum) * 32767 / int64(peak))
	}
	return PCMFrame{Sequence: sequence, Timestamp: sequence * uint64(DefaultPCMFormat.FrameSamples), Samples: samples}
}

func (r *voiceRuntime) captureState() string {
	if !voiceRecently(r.lastCaptured.Load()) {
		return "waiting"
	}
	if r.muted.Load() {
		return "muted"
	}
	return "capturing"
}

func (r *voiceRuntime) snapshot() []VoicePeerSnapshot {
	r.mu.Lock()
	list := make([]VoicePeerSnapshot, 0, len(r.peers))
	for _, peer := range r.peers {
		list = append(list, peer.snapshot())
	}
	r.mu.Unlock()
	return list
}

func (r *voiceRuntime) close() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	peers := make([]*voicePeer, 0, len(r.peers))
	for _, p := range r.peers {
		peers = append(peers, p)
	}
	r.peers = map[string]*voicePeer{}
	r.mu.Unlock()
	r.cancel()
	for _, p := range peers {
		p.close()
	}
	r.workers.Wait()
}
