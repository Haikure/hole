package core

import "testing"

func TestVoiceWireValidation(t *testing.T) {
	packet, err := EncodeVoiceDatagram(7, 960, []byte{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	seq, ts, payload, err := DecodeVoiceDatagram(packet)
	if err != nil || seq != 7 || ts != 960 || string(payload) != "\x01\x02\x03" {
		t.Fatalf("decoded packet %d/%d/%v/%v", seq, ts, payload, err)
	}
	longTimestamp, err := EncodeVoiceDatagram(8, uint64(^uint32(0))+960, []byte{1})
	if err != nil {
		t.Fatal(err)
	}
	_, decodedTimestamp, _, err := DecodeVoiceDatagram(longTimestamp)
	if err != nil || decodedTimestamp <= uint64(^uint32(0)) {
		t.Fatalf("64-bit timestamp was not preserved: %d/%v", decodedTimestamp, err)
	}
	for _, mutate := range []func([]byte){func(p []byte) { p[0] = 'X' }, func(p []byte) { p[4] = 3 }, func(p []byte) { p[19] = 0; p[20] = 9 }, func(p []byte) { p[5] = 9 }} {
		copyPacket := append([]byte(nil), packet...)
		mutate(copyPacket)
		if _, _, _, err := DecodeVoiceDatagram(copyPacket); err == nil {
			t.Fatal("invalid packet accepted")
		}
	}
	if _, err := EncodeVoiceDatagram(1, 1, make([]byte, voiceDatagramLimit)); err == nil {
		t.Fatal("oversized payload accepted")
	}
}
