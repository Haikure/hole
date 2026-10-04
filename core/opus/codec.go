package opus

import (
	"encoding/binary"
	"errors"

	pionopus "github.com/pion/opus"
)

const (
	voiceSampleRate = 48000
	voiceChannels   = 1
	voiceFrameBytes = 960 * 2
	maxFrameBytes   = 1275
)

// Codec wraps Pion's pure-Go RFC 6716 encoder and decoder. The fixed voice
// format uses one 48 kHz mono 20 ms frame per packet.
type Codec struct {
	encoder *pionopus.Encoder
	decoder pionopus.Decoder
}

func New() (Codec, error) {
	encoder, err := pionopus.NewEncoder(
		pionopus.WithSampleRate(voiceSampleRate),
		pionopus.WithChannels(voiceChannels),
		pionopus.WithApplication(pionopus.ApplicationVoIP),
		pionopus.WithBandwidth(pionopus.BandwidthFullband),
		pionopus.WithVBR(false),
		pionopus.WithConstrainedVBR(true),
	)
	if err != nil {
		return Codec{}, err
	}
	decoder, err := pionopus.NewDecoderWithOutput(48000, 1)
	if err != nil {
		return Codec{}, err
	}
	return Codec{encoder: encoder, decoder: decoder}, nil
}

func (c *Codec) Encode(samples []int16, bitrate int) ([]byte, error) {
	if len(samples) != 960 {
		return nil, errors.New("Opus encoder requires one 20 ms mono frame")
	}
	if bitrate < 12000 {
		bitrate = 12000
	}
	if bitrate > 32000 {
		bitrate = 32000
	}
	if err := c.encoder.SetBitrate(bitrate); err != nil {
		return nil, err
	}
	pcm := make([]byte, voiceFrameBytes)
	for i, sample := range samples {
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(sample))
	}
	out := make([]byte, maxFrameBytes+1)
	n, err := c.encoder.Encode(pcm, out)
	if err != nil {
		return nil, err
	}
	if n < 1 || n > len(out) {
		return nil, errors.New("Opus encoder returned an invalid packet length")
	}
	return append([]byte(nil), out[:n]...), nil
}

func (c *Codec) Decode(payload []byte, samples []int16) (int, error) {
	return c.decoder.DecodeToInt16(payload, samples)
}
