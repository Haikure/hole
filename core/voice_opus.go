package core

import (
	"hole/core/opus"
)

type pureGoOpusCodec struct {
	codec opus.Codec
}

func newPureGoOpusCodec() (*pureGoOpusCodec, error) {
	codec, err := opus.New()
	if err != nil {
		return nil, err
	}
	return &pureGoOpusCodec{codec: codec}, nil
}

func (c *pureGoOpusCodec) Encode(samples []int16, bitrate int) ([]byte, error) {
	return c.codec.Encode(samples, bitrate)
}

func (c *pureGoOpusCodec) Decode(payload []byte, output []int16) (int, error) {
	return c.codec.Decode(payload, output)
}
