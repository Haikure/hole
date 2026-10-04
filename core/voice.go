package core

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

const (
	voiceProtocolVersion = 2
	voiceSampleRate      = 48000
	voiceChannels        = 1
	voiceFrameSamples    = 960
	voiceFrameMillis     = 20
	voicePeerLimit       = 8
)

// PCMFormat is the only PCM format accepted by the core voice pipeline.
// Platform adapters perform resampling and channel conversion before handing
// frames to the core.
type PCMFormat struct {
	SampleRate   int
	Channels     int
	FrameSamples int
}

var DefaultPCMFormat = PCMFormat{SampleRate: voiceSampleRate, Channels: voiceChannels, FrameSamples: voiceFrameSamples}

func (f PCMFormat) Validate() error {
	if f != DefaultPCMFormat {
		return fmt.Errorf("voice PCM format must be 48 kHz mono with 960 samples per frame")
	}
	return nil
}

// PCMFrame uses a monotonic sampling clock for Timestamp. It deliberately
// carries samples rather than a platform audio handle or byte slice.
type PCMFrame struct {
	Sequence  uint64
	Timestamp uint64
	Samples   []int16
}

func (f PCMFrame) Validate() error {
	if len(f.Samples) != DefaultPCMFormat.FrameSamples {
		return fmt.Errorf("voice PCM frame must contain %d samples", DefaultPCMFormat.FrameSamples)
	}
	return nil
}

func (f PCMFrame) Clone() PCMFrame {
	copySamples := make([]int16, len(f.Samples))
	copy(copySamples, f.Samples)
	f.Samples = copySamples
	return f
}

type PCMSource interface {
	ReadPCM(context.Context, *PCMFrame) error
}

type PCMSink interface {
	WritePCM(context.Context, PCMFrame) error
}

type VoiceMemberSnapshot struct {
	DeviceName     string `json:"device_name"`
	Voice          bool   `json:"voice"`
	SignalState    string `json:"signal_state"`
	TransportState string `json:"transport_state"`
	MediaState     string `json:"media_state"`
	TransportID    string `json:"transport_id,omitempty"`
	TransportGen   uint64 `json:"transport_generation,string,omitempty"`
}

type VoicePeerSnapshot struct {
	PeerID        string `json:"peer_id"`
	TransportID   string `json:"transport_id,omitempty"`
	Generation    uint64 `json:"generation,string,omitempty"`
	State         string `json:"state"`
	Path          string `json:"path,omitempty"`
	RTTMS         int64  `json:"rtt_ms,string,omitempty"`
	Bitrate       int    `json:"bitrate"`
	PacketLoss    uint64 `json:"packet_loss,string"`
	Reordered     uint64 `json:"reordered,string"`
	LateFrames    uint64 `json:"late_frames,string"`
	QueueDepth    int    `json:"queue_depth"`
	QueueDrops    uint64 `json:"queue_drops,string"`
	DatagramDrops uint64 `json:"datagram_drops,string"`
	JitterDepth   int    `json:"jitter_depth"`
	LastFeedback  string `json:"last_feedback,omitempty"`
	Error         *Fault `json:"error,omitempty"`
}

type VoiceSnapshot struct {
	Enabled bool                  `json:"enabled"`
	Muted   bool                  `json:"muted"`
	State   string                `json:"state"`
	Members []VoiceMemberSnapshot `json:"members"`
	Peers   []VoicePeerSnapshot   `json:"peers"`
}

func (s VoiceSnapshot) clone() VoiceSnapshot {
	s.Members = append([]VoiceMemberSnapshot(nil), s.Members...)
	s.Peers = make([]VoicePeerSnapshot, len(s.Peers))
	for i, peer := range s.Peers {
		s.Peers[i] = peer
		if peer.Error != nil {
			copy := *peer.Error
			s.Peers[i].Error = &copy
		}
	}
	return s
}

func sortVoiceSnapshot(s *VoiceSnapshot) {
	sort.Slice(s.Members, func(i, j int) bool { return s.Members[i].DeviceName < s.Members[j].DeviceName })
	sort.Slice(s.Peers, func(i, j int) bool {
		if s.Peers[i].PeerID != s.Peers[j].PeerID {
			return s.Peers[i].PeerID < s.Peers[j].PeerID
		}
		return s.Peers[i].TransportID < s.Peers[j].TransportID
	})
}

var (
	errInvalidPCMFrame = errors.New("invalid voice PCM frame")
)
