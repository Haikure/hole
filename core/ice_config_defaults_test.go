package core

import (
	"reflect"
	"testing"
	"time"
)

func TestParseTransportDefaultsAndExplicitOverrides(t *testing.T) {
	for _, text := range []string{"{}", "turn: {}", "turn:\n  mode: worker\n", "turn:\n  ttl: 6h\n"} {
		cfg, err := ParseConfig([]byte(text))
		if err != nil {
			t.Fatal(err)
		}
		want := TURNConfig{Mode: "worker", TTL: ConfigDuration(6 * time.Hour), URLs: []string{}, Order: []string{}}
		if !reflect.DeepEqual(cfg.TURN, want) {
			t.Fatalf("%q has unexpected TURN defaults: %+v", text, cfg.TURN)
		}
	}
	for _, text := range []string{
		"turn:\n  mode: off\n  ttl: 2h\nice:\n  stun_urls: []\n",
		`{"turn":{"mode":"off","ttl":"2h"},"ice":{"stun_urls":[]}}`,
	} {
		cfg, err := ParseConfig([]byte(text))
		if err != nil || cfg.TURN.Mode != "off" || cfg.TURN.TTL.Duration() != 2*time.Hour || cfg.ICE.STUNURLs == nil || len(cfg.ICE.STUNURLs) != 0 {
			t.Fatal("explicit settings overwritten", cfg, err)
		}
	}
}

func TestRemovedTransportPreferredIsRejected(t *testing.T) {
	for _, preferred := range []string{"auto", "ice", ProfileICE, ProfileFixed, "legacy-ipv6-quic-v2", "legacy", "ipv6"} {
		for _, text := range []string{
			"transport:\n  preferred: " + preferred + "\n",
			`{"transport":{"preferred":"` + preferred + `"}}`,
		} {
			if _, err := ParseConfig([]byte(text)); err == nil {
				t.Fatalf("accepted removed preferred field %q", preferred)
			}
		}
	}
	if ProfileICE != "ice-quic-mux-v1" || ProfileFixed != "fixed-quic-mux-v1" {
		t.Fatal("configuration change changed a wire protocol")
	}
}
