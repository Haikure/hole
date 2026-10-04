package core

import (
	"encoding/binary"
	"errors"
	"fmt"
)

type voiceFeedback struct {
	HighestSequence uint32
	Lost            uint32
	Reordered       uint32
	JitterDepth     uint16
	LateFrames      uint32
}

const (
	voiceWireVersion    byte = 2
	voiceKindMedia      byte = 1
	voiceKindFeedback   byte = 2
	voiceCodecOpus      byte = 1
	voiceWireHeaderSize      = 21
	voiceDatagramLimit       = 1200
)

var voiceMagic = [4]byte{'H', 'V', 'C', 2}

type voicePacket struct {
	Version   byte
	Kind      byte
	Sequence  uint32
	Timestamp uint64
	Codec     byte
	Payload   []byte
}

func (p voicePacket) validate() error {
	if p.Version != voiceWireVersion {
		return fmt.Errorf("unsupported voice wire version %d", p.Version)
	}
	if p.Kind != voiceKindMedia && p.Kind != voiceKindFeedback {
		return errors.New("invalid voice packet kind")
	}
	if p.Kind == voiceKindMedia && p.Codec != voiceCodecOpus {
		return errors.New("unsupported voice codec")
	}
	if p.Kind == voiceKindFeedback && p.Codec != 0 {
		return errors.New("feedback packet must not carry a codec")
	}
	if len(p.Payload) == 0 || len(p.Payload)+voiceWireHeaderSize > voiceDatagramLimit {
		return errors.New("voice packet payload exceeds datagram limit")
	}
	return nil
}

func marshalVoicePacket(p voicePacket) ([]byte, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	data := make([]byte, voiceWireHeaderSize+len(p.Payload))
	copy(data[:4], voiceMagic[:])
	data[4] = p.Version
	data[5] = p.Kind
	binary.BigEndian.PutUint32(data[6:10], p.Sequence)
	binary.BigEndian.PutUint64(data[10:18], p.Timestamp)
	data[18] = p.Codec
	binary.BigEndian.PutUint16(data[19:21], uint16(len(p.Payload)))
	copy(data[21:], p.Payload)
	return data, nil
}

func parseVoicePacket(data []byte) (voicePacket, error) {
	if len(data) < voiceWireHeaderSize || len(data) > voiceDatagramLimit {
		return voicePacket{}, errors.New("invalid voice packet length")
	}
	if string(data[:4]) != string(voiceMagic[:]) {
		return voicePacket{}, errors.New("invalid voice packet magic")
	}
	p := voicePacket{Version: data[4], Kind: data[5], Sequence: binary.BigEndian.Uint32(data[6:10]), Timestamp: binary.BigEndian.Uint64(data[10:18]), Codec: data[18]}
	n := int(binary.BigEndian.Uint16(data[19:21]))
	if n != len(data)-voiceWireHeaderSize {
		return voicePacket{}, errors.New("voice packet payload length mismatch")
	}
	p.Payload = append([]byte(nil), data[voiceWireHeaderSize:]...)
	if err := p.validate(); err != nil {
		return voicePacket{}, err
	}
	return p, nil
}

// EncodeVoiceDatagram and DecodeVoiceDatagram keep the binary protocol usable
// by focused tests without exposing the QUIC connection to voiceRuntime.
func EncodeVoiceDatagram(sequence uint32, timestamp uint64, opus []byte) ([]byte, error) {
	return marshalVoicePacket(voicePacket{Version: voiceWireVersion, Kind: voiceKindMedia, Sequence: sequence, Timestamp: timestamp, Codec: voiceCodecOpus, Payload: opus})
}

func DecodeVoiceDatagram(data []byte) (sequence uint32, timestamp uint64, opus []byte, err error) {
	p, err := parseVoicePacket(data)
	if err != nil {
		return 0, 0, nil, err
	}
	if p.Kind != voiceKindMedia {
		return 0, 0, nil, errors.New("voice datagram is not media")
	}
	return p.Sequence, p.Timestamp, p.Payload, nil
}

func EncodeVoiceFeedback(sequence uint32, timestamp uint64, payload []byte) ([]byte, error) {
	return marshalVoicePacket(voicePacket{Version: voiceWireVersion, Kind: voiceKindFeedback, Sequence: sequence, Timestamp: timestamp, Payload: payload})
}

func DecodeVoiceFeedback(data []byte) (sequence uint32, timestamp uint64, payload []byte, err error) {
	p, err := parseVoicePacket(data)
	if err != nil {
		return 0, 0, nil, err
	}
	if p.Kind != voiceKindFeedback {
		return 0, 0, nil, errors.New("voice datagram is not feedback")
	}
	return p.Sequence, p.Timestamp, p.Payload, nil
}

func marshalVoiceFeedback(feedback voiceFeedback) []byte {
	payload := make([]byte, 18)
	binary.BigEndian.PutUint32(payload[0:4], feedback.HighestSequence)
	binary.BigEndian.PutUint32(payload[4:8], feedback.Lost)
	binary.BigEndian.PutUint32(payload[8:12], feedback.Reordered)
	binary.BigEndian.PutUint16(payload[12:14], feedback.JitterDepth)
	binary.BigEndian.PutUint32(payload[14:18], feedback.LateFrames)
	return payload
}

func parseVoiceFeedback(payload []byte) (voiceFeedback, error) {
	if len(payload) != 18 {
		return voiceFeedback{}, errors.New("invalid voice feedback length")
	}
	return voiceFeedback{HighestSequence: binary.BigEndian.Uint32(payload[0:4]), Lost: binary.BigEndian.Uint32(payload[4:8]), Reordered: binary.BigEndian.Uint32(payload[8:12]), JitterDepth: binary.BigEndian.Uint16(payload[12:14]), LateFrames: binary.BigEndian.Uint32(payload[14:18])}, nil
}
