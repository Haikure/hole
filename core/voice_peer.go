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

type voiceDecoded struct {
	peer  string
	frame PCMFrame
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
	decoded       chan<- voiceDecoded
	jitter        *jitterBuffer
	sequence      atomic.Uint32
	queueDrops    atomic.Uint64
	datagramDrops atomic.Uint64
	lateFrames    atomic.Uint64
	reordered     atomic.Uint64
	received      atomic.Uint64
	lost          atomic.Uint64
	highest       atomic.Uint32
	mu            sync.Mutex
	state         string
	error         *Fault
	workers       sync.WaitGroup
	closeOnce     sync.Once
}

func newVoicePeer(parent context.Context, peerID, transportID string, generation uint64, link voiceLink, codec voiceCodec, decoded chan<- voiceDecoded) *voicePeer {
	ctx, cancel := context.WithCancel(parent)
	p := &voicePeer{ctx: ctx, cancel: cancel, peerID: peerID, transport: transportID, generation: generation, link: link, codec: codec, bitrate: newBitrateController(), out: make(chan voiceOutbound, voiceQueueFrames), decoded: decoded, jitter: newJitterBuffer(voiceJitterLimit), state: "connecting"}
	p.workers.Add(5)
	go func() { defer p.workers.Done(); p.writeLoop() }()
	go func() { defer p.workers.Done(); p.readLoop() }()
	go func() { defer p.workers.Done(); p.feedbackLoop() }()
	go func() { defer p.workers.Done(); p.feedbackReadLoop() }()
	go func() { defer p.workers.Done(); p.playLoop() }()
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
			sequence := p.sequence.Add(1) - 1
			packet, err := marshalVoicePacket(voicePacket{Version: voiceWireVersion, Kind: voiceKindMedia, Sequence: sequence, Timestamp: item.frame.Timestamp, Codec: voiceCodecOpus, Payload: payload})
			if err != nil {
				p.setError(err)
				continue
			}
			if err = p.link.SendVoiceDatagram(packet); err != nil {
				p.datagramDrops.Add(1)
				p.setError(err)
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

func (p *voicePeer) playLoop() {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			frame, ok := p.jitter.popForPlayback()
			if !ok {
				continue
			}
			if p.codec == nil {
				p.setError(errors.New("voice Opus decoder is unavailable"))
				continue
			}
			samples := make([]int16, DefaultPCMFormat.FrameSamples)
			n, err := p.codec.Decode(frame.Payload, samples)
			if err != nil || n != len(samples) {
				p.datagramDrops.Add(1)
				if err != nil {
					p.setError(err)
				}
				continue
			}
			select {
			case p.decoded <- voiceDecoded{peer: p.peerID, frame: PCMFrame{Sequence: uint64(frame.Sequence), Timestamp: frame.Timestamp, Samples: samples}}:
			case <-p.ctx.Done():
				return
			default:
				p.lateFrames.Add(1)
			}
		}
	}
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
	s := VoicePeerSnapshot{PeerID: p.peerID, TransportID: p.transport, Generation: p.generation, State: state, Bitrate: p.bitrate.bitrate(), QueueDepth: len(p.out), QueueDrops: p.queueDrops.Load(), DatagramDrops: p.datagramDrops.Load(), LateFrames: p.lateFrames.Load(), JitterDepth: p.jitter.len()}
	if fault != nil {
		copy := *fault
		s.Error = &copy
	}
	return s
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
	decoded      chan voiceDecoded
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
	r := &voiceRuntime{ctx: ctx, cancel: cancel, source: source, sink: sink, codecFactory: factory, peers: map[string]*voicePeer{}, decoded: make(chan voiceDecoded, voiceJitterLimit)}
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
	p := newVoicePeer(r.ctx, peerID, transportID, generation, link, codec, r.decoded)
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
	var previous PCMFrame
	havePrevious := false
	for {
		var frame PCMFrame
		if err := r.source.ReadPCM(r.ctx, &frame); err != nil {
			return
		}
		if frame.Validate() != nil || (havePrevious && (frame.Sequence != previous.Sequence+1 || frame.Timestamp != previous.Timestamp+uint64(DefaultPCMFormat.FrameSamples))) || r.muted.Load() {
			if frame.Validate() == nil {
				previous = frame
				havePrevious = true
			}
			continue
		}
		previous, havePrevious = frame, true
		r.mu.Lock()
		for _, peer := range r.peers {
			peer.enqueue(frame)
		}
		r.mu.Unlock()
	}
}

func (r *voiceRuntime) mixLoop() {
	frames := make(map[string]PCMFrame, voicePeerLimit)
	for {
		select {
		case <-r.ctx.Done():
			return
		case input := <-r.decoded:
			for peer, frame := range frames {
				window := uint64(2 * DefaultPCMFormat.FrameSamples)
				if input.frame.Timestamp > frame.Timestamp+window || frame.Timestamp > input.frame.Timestamp+window {
					delete(frames, peer)
				}
			}
			frames[input.peer] = input.frame
			if len(frames) == 0 {
				continue
			}
			mixed := make([]int16, DefaultPCMFormat.FrameSamples)
			for _, frame := range frames {
				for i, sample := range frame.Samples {
					value := int32(mixed[i]) + int32(sample)
					if value > 32767 {
						value = 32767
					}
					if value < -32768 {
						value = -32768
					}
					mixed[i] = int16(value)
				}
			}
			if err := r.sink.WritePCM(r.ctx, PCMFrame{Timestamp: input.frame.Timestamp, Sequence: input.frame.Sequence, Samples: mixed}); err != nil {
				return
			}
		}
	}
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
