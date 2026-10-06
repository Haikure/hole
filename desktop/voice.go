package desktop

import (
	"encoding/binary"
	"encoding/json"
	"math"

	"hole/core"
)

// PCM is an opt-in binary extension of the private stdio transport. A NUL
// marker cannot begin a JSON message. Fixed-size packets bound allocations and
// preserve arbitrary PCM bytes (including LF) without JSON/base64 conversion.
const voicePacketBytes = 1 + 8 + 8 + 960*2

type voiceEngine interface {
	PushVoicePCM(int64, int64, []byte) error
	PullVoicePCM(int) []byte
	SetVoiceMuted(bool) error
}

func voicePacket(stream, sequence uint64, pcm []byte) []byte {
	packet := make([]byte, voicePacketBytes)
	binary.LittleEndian.PutUint64(packet[1:9], stream)
	binary.LittleEndian.PutUint64(packet[9:17], sequence)
	copy(packet[17:], pcm)
	return packet
}

func (h *host) capture(packet []byte) {
	if len(packet) != voicePacketBytes || h.audioID == 0 || binary.LittleEndian.Uint64(packet[1:9]) != h.audioID {
		return
	}
	sequence := binary.LittleEndian.Uint64(packet[9:17])
	if sequence > math.MaxInt64/960 || (h.audioCaptured && sequence <= h.audioSequence) {
		return
	}
	h.audioCaptured, h.audioSequence = true, sequence
	if audio, ok := h.engine.(voiceEngine); ok {
		_ = audio.PushVoicePCM(int64(sequence), int64(sequence*960), packet[17:])
	}
}

func (h *host) playback() []byte {
	if h.audioID == 0 {
		return nil
	}
	if audio, ok := h.engine.(voiceEngine); ok {
		if pcm := audio.PullVoicePCM(1); len(pcm) == core.DefaultPCMFormat.FrameSamples*2 {
			return voicePacket(h.audioID, 0, pcm)
		}
	}
	return nil
}

func (h *host) voiceControl(method string, raw json.RawMessage) (any, *rpcError, bool) {
	audio, ok := h.engine.(voiceEngine)
	if !ok {
		return nil, rpcFault(coreError, "voice_unavailable", "核心宿主不支持语音"), false
	}
	if method == "set_voice_muted" {
		var params struct {
			Muted *bool `json:"muted"`
		}
		if !onlyFields(raw, "muted") || decodeStrict(raw, &params) != nil || params.Muted == nil {
			return nil, rpcFault(invalidParams, "invalid_params", "需要 muted 布尔值"), false
		}
		if err := audio.SetVoiceMuted(*params.Muted); err != nil {
			return nil, operationFault(err), false
		}
	} else {
		var params struct {
			Enabled *bool `json:"enabled"`
		}
		if !onlyFields(raw, "enabled") || decodeStrict(raw, &params) != nil || params.Enabled == nil {
			return nil, rpcFault(invalidParams, "invalid_params", "需要 enabled 布尔值"), false
		}
		h.audioID = 0
		if *params.Enabled {
			snapshot := h.engine.Snapshot()
			if !snapshot.RunRequested || !snapshot.Voice.Enabled {
				return nil, rpcFault(coreError, "voice_unavailable", "请先启动连接并启用语音"), false
			}
			h.audioNext++
			h.audioID = h.audioNext
			h.audioCaptured = false
			// Do not play audio left over from an earlier device/session.
			_ = audio.PullVoicePCM(4)
		}
		return struct {
			StreamID uint64 `json:"stream_id,string"`
		}{h.audioID}, nil, false
	}
	return struct {
		Accepted bool `json:"accepted"`
	}{true}, nil, false
}
