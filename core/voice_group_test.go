package core

import (
	"strings"
	"testing"
	"time"
)

func TestVoiceWorldGroupsSeparateRoomAndSwitch(t *testing.T) {
	url, _ := actualWorkerFixture(t)
	groupA, groupB := strings.Repeat("a", 64), strings.Repeat("b", 64)
	engines := make([]*Engine, 4)
	requests := make([]Request, 4)
	for i, name := range []string{"world-a-host", "world-a-guest", "world-b-host", "ordinary"} {
		engines[i] = NewEngine(Options{RetryNetwork: true})
		t.Cleanup(func() { _ = engines[i].Close() })
		request := iceFixtureRequest(url, name)
		request.Config.Provide = nil
		request.Config.Consume = nil
		request.Config.Voice.Enabled = true
		if i < 2 {
			request.Config.Voice.Group = groupA
		} else if i == 2 {
			request.Config.Voice.Group = groupB
		}
		requests[i] = request
		if err := engines[i].Start(request); err != nil {
			t.Fatal(err)
		}
	}
	waitSnapshot(t, engines[0], func(s Snapshot) bool { return len(s.Voice.Peers) == 1 && s.Voice.Peers[0].State == "active" })
	for i := 2; i < 4; i++ {
		s := waitSnapshot(t, engines[i], func(s Snapshot) bool { return s.SignalState == "joined" })
		if len(s.PeerTransports) != 0 {
			t.Fatal("cross-world voice transport", s.PeerTransports)
		}
	}
	requests[1].Config.Voice.Group = groupB
	if err := engines[1].ApplyConfig(requests[1]); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, engines[1], func(s Snapshot) bool {
		found := false
		for _, p := range s.Voice.Peers {
			if p.PeerID == "world-a-host" && p.State == "active" {
				return false
			}
			if p.PeerID == "world-b-host" && p.State == "active" {
				found = true
			}
		}
		return found
	})
	pcm := make([]byte, 1920)
	for i := range pcm {
		pcm[i] = 42
	}
	for seq := int64(0); seq < 30; seq++ {
		_ = engines[0].PushVoicePCM(seq, seq*960, pcm)
		_ = engines[2].PushVoicePCM(seq, seq*960, pcm)
		time.Sleep(20 * time.Millisecond)
	}
	var received uint64
	for _, p := range engines[1].Snapshot().Voice.Peers {
		if p.PeerID == "world-a-host" && p.ReceivedFrames > 0 {
			t.Fatal("old world audio leaked")
		}
		if p.PeerID == "world-b-host" {
			received = p.ReceivedFrames
		}
	}
	if received == 0 {
		t.Fatal("new world audio was not received")
	}
	if len(engines[3].Snapshot().Voice.Peers) != 0 {
		t.Fatal("ordinary room voice joined an MC world")
	}
}

func TestVoiceGroupValidation(t *testing.T) {
	for _, group := range []string{"", strings.Repeat("a", 64), "world", strings.Repeat("g", 64)} {
		cfg := validConfig()
		cfg.Voice.Group = group
		err := cfg.Validate()
		if (err == nil) != (group == "" || group == strings.Repeat("a", 64)) {
			t.Fatal(group, err)
		}
	}
}
