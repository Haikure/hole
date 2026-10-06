package desktop

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"math"
	"testing"

	"hole/core"
)

type fakeVoiceEngine struct {
	*fakeEngine
	pcm                 []byte
	sequence, timestamp int64
	muted               bool
	captures            int
}

func (e *fakeVoiceEngine) PushVoicePCM(sequence, timestamp int64, pcm []byte) error {
	e.pcm, e.sequence, e.timestamp = bytes.Clone(pcm), sequence, timestamp
	e.captures++
	return nil
}
func (e *fakeVoiceEngine) PullVoicePCM(int) []byte        { pcm := e.pcm; e.pcm = nil; return pcm }
func (e *fakeVoiceEngine) SetVoiceMuted(muted bool) error { e.muted = muted; return nil }

func TestVoiceFramingPreservesBinaryAndJSONAcrossShortReads(t *testing.T) {
	pcm := make([]byte, 1920)
	for i := range pcm {
		pcm[i] = byte(i)
	}
	packet := voicePacket(7, 23, pcm)
	control := []byte(`{"jsonrpc":"2.0","id":"1","method":"snapshot"}`)
	input := append(bytes.Clone(control), '\n')
	input = append(input, packet...)
	input = append(input, control...) // Complete final JSON without LF is valid.
	s := bufio.NewScanner(shortReader{bytes.NewReader(input)})
	s.Split(splitFrames)
	for i, expected := range [][]byte{control, packet, control} {
		if !s.Scan() || !bytes.Equal(s.Bytes(), expected) {
			t.Fatalf("frame %d corrupted: %v", i, s.Err())
		}
	}
	if s.Scan() || s.Err() != nil {
		t.Fatalf("unexpected trailing data: %v", s.Err())
	}
	s = bufio.NewScanner(bytes.NewReader(packet[:len(packet)-1]))
	s.Split(splitFrames)
	if s.Scan() || s.Err() != io.ErrUnexpectedEOF {
		t.Fatalf("truncated PCM accepted: %v", s.Err())
	}
}

func TestVoiceStreamLifecycleAndMute(t *testing.T) {
	e := &fakeVoiceEngine{fakeEngine: newFakeEngine()}
	e.state.RunRequested = true
	e.state.Voice = core.VoiceSnapshot{Enabled: true}
	h := host{engine: e}
	call := func(method, params string) {
		t.Helper()
		_, fault, _ := h.dispatch(request{Method: method, Params: json.RawMessage(params)})
		if fault != nil {
			t.Fatalf("%s: %+v", method, fault)
		}
	}
	call("set_voice_audio", `{"enabled":true}`)
	id := h.audioID
	pcm := bytes.Repeat([]byte{10, 255}, 960)
	h.capture(voicePacket(id, 20, pcm))
	if e.captures != 1 || e.timestamp != 19200 || !bytes.Equal(e.pcm, pcm) {
		t.Fatal("PCM did not reach core")
	}
	for _, seq := range []uint64{19, 20, math.MaxUint64} {
		h.capture(voicePacket(id, seq, pcm))
	}
	if e.captures != 1 {
		t.Fatal("stale/overflow sequence accepted")
	}
	if p := h.playback(); !bytes.Equal(p, voicePacket(id, 0, pcm)) {
		t.Fatal("playback changed PCM")
	}
	call("set_voice_muted", `{"muted":true}`)
	if !e.muted {
		t.Fatal("mute not applied")
	}
	call("set_voice_audio", `{"enabled":false}`)
	h.capture(voicePacket(id, 21, pcm))
	if e.captures != 1 || h.playback() != nil {
		t.Fatal("disabled stream remained active")
	}
	call("set_voice_audio", `{"enabled":true}`)
	if h.audioID == id {
		t.Fatal("stream ID reused")
	}
	h.capture(voicePacket(id, 22, pcm))
	h.capture(voicePacket(h.audioID, 0, pcm))
	if e.captures != 2 {
		t.Fatal("new stream failed to reject old frames or reset sequence")
	}
	call("stop", `{}`)
	if h.audioID != 0 {
		t.Fatal("stop did not invalidate audio")
	}
}

func TestVoiceControlsRequireExactBooleanFields(t *testing.T) {
	e := &fakeVoiceEngine{fakeEngine: newFakeEngine()}
	h := host{engine: e}
	for _, params := range []string{`{}`, `null`, `{"enabled":null}`, `{"enabled":1}`, `{"Enabled":true}`, `{"enabled":true,"extra":1}`} {
		_, fault, _ := h.dispatch(request{Method: "set_voice_audio", Params: json.RawMessage(params)})
		if fault == nil || fault.Code != invalidParams {
			t.Errorf("accepted %s", params)
		}
	}
	_, fault, _ := h.dispatch(request{Method: "set_voice_audio", Params: json.RawMessage(`{"enabled":true}`)})
	if fault == nil || fault.Data.Code != "voice_unavailable" {
		t.Fatal("audio attached to stopped engine")
	}
}
