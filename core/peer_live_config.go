package core

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

func mappingToken(record peerMappingRecord) string {
	data, _ := json.Marshal(record)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

var voiceEpochMagic = [4]byte{'H', 'V', 'E', 1}

func (p *muxPeer) receiveVoice(data []byte) bool {
	if len(data) < 4 || (string(data[:4]) != string(voiceMagic[:]) && string(data[:4]) != string(voiceEpochMagic[:])) {
		return false
	}
	p.voiceSyncMu.Lock()
	defer p.voiceSyncMu.Unlock()
	if p.liveConfig {
		if len(data) < 12 || string(data[:4]) != string(voiceEpochMagic[:]) || binary.BigEndian.Uint64(data[4:12]) != p.voiceRevision.Load() {
			p.dropped.Add(1)
			return true
		}
		data = data[12:]
	}
	packet, err := parseVoicePacket(data)
	if err != nil || !p.voiceEnabled.Load() {
		p.dropped.Add(1)
		return true
	}
	queue := p.voiceDatagrams
	if packet.Kind == voiceKindFeedback {
		queue = p.voiceFeedback
	}
	select {
	case queue <- append([]byte(nil), data...):
	default:
		p.dropped.Add(1)
	}
	return true
}

// Both the room authorization and the peer's control state must agree before
// voice can start. The revision also separates media across disable/re-enable.
func (p *muxPeer) syncVoice() {
	p.voiceSyncMu.Lock()
	defer p.voiceSyncMu.Unlock()
	p.owner.mu.Lock()
	ready := p.owner.ready
	active := p.owner.active != nil && p.owner.active.mux == p
	p.owner.mu.Unlock()
	if !active || p.ctx.Err() != nil {
		return
	}
	voice := p.owner.coordinator.voiceRuntimePtr()
	wanted := ready.Voice && voice != nil && ready.LeaseUntil > time.Now().UnixMilli()
	enabled := wanted && p.ready.Voice
	if p.liveConfig {
		if ready.VoiceRevision == 0 {
			return
		}
		if p.voiceSentRevision != ready.VoiceRevision || p.voiceSentEnabled != wanted {
			if err := p.writeControl(mappingControl{Type: "VOICE_STATE", Voice: wanted, VoiceRevision: ready.VoiceRevision}); err != nil {
				p.stop()
				return
			}
			p.voiceSentRevision, p.voiceSentEnabled = ready.VoiceRevision, wanted
		}
		enabled = wanted && p.voiceRemoteEnabled && p.voiceRemoteRevision == ready.VoiceRevision
	}
	// Stop the old codec and drain bounded queues before publishing a new epoch.
	if p.voiceRevision.Load() != ready.VoiceRevision || !enabled {
		p.voiceEnabled.Store(false)
		if voice != nil {
			voice.unbindPeer(p.ready.PeerDevice, p)
		}
		for len(p.voiceDatagrams) > 0 {
			select {
			case <-p.voiceDatagrams:
			default:
			}
		}
		for len(p.voiceFeedback) > 0 {
			select {
			case <-p.voiceFeedback:
			default:
			}
		}
	}
	p.voiceRevision.Store(ready.VoiceRevision)
	p.voiceEnabled.Store(enabled)
	if enabled {
		voice.bindPeer(p.ready.PeerDevice, p.ready.TransportID, p.ready.TransportGeneration, p)
	}
}

type voiceSendRequest struct {
	ctx      context.Context
	data     []byte
	revision uint64
	done     chan error
}

// One bounded sender belongs to the QUIC path, not to a voice activation.
// Cancelling a codec never closes the shared transport or leaks one goroutine
// per packet when the QUIC DATAGRAM queue is congested.
func (p *muxPeer) enqueueVoice(ctx context.Context, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r := voiceSendRequest{ctx: ctx, data: append([]byte(nil), data...), revision: p.voiceRevision.Load(), done: make(chan error, 1)}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.ctx.Done():
		return p.ctx.Err()
	case p.voiceSend <- r:
	default:
		return errors.New("voice send queue full")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.ctx.Done():
		return p.ctx.Err()
	case err := <-r.done:
		return err
	}
}

func (p *muxPeer) writeVoice() {
	p.writeVoiceTo(p.conn)
}

func (p *muxPeer) writeVoiceTo(conn voiceDatagramSender) {
	// Register cancellation once per path, rather than once per audio frame.
	stop := context.AfterFunc(p.ctx, func() { _ = conn.CloseWithError(0, "voice transport cancelled") })
	defer stop()
	for {
		select {
		case <-p.ctx.Done():
			return
		case r := <-p.voiceSend:
			if r.ctx.Err() != nil || !p.voiceEnabled.Load() || r.revision != p.voiceRevision.Load() {
				r.done <- context.Canceled
				continue
			}
			data := r.data
			if p.liveConfig {
				data = make([]byte, 12+len(r.data))
				copy(data, voiceEpochMagic[:])
				binary.BigEndian.PutUint64(data[4:12], r.revision)
				copy(data[12:], r.data)
			}
			r.done <- conn.SendDatagram(data)
		}
	}
}
