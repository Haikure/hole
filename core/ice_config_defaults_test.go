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
		want := TURNConfig{Mode: "worker", TTL: ConfigDuration(6 * time.Hour), URLs: []string{}}
		if !reflect.DeepEqual(cfg.TURN, want) {
			t.Fatalf("%q has unexpected TURN defaults: %+v", text, cfg.TURN)
		}
		if cfg.Transport.Preferred != PreferredIPv6 {
			t.Fatal("URL-less mobile/old transport policy changed")
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

func TestPreferredNamesAreStrictAndSeparateFromWireProfiles(t *testing.T) {
	for _, preferred := range []string{PreferredICE, PreferredIPv6} {
		cfg := validConfig()
		cfg.Transport.Preferred = preferred
		if err := cfg.Validate(); err != nil {
			t.Fatal(preferred, err)
		}
	}
	for _, old := range []string{ProfileICE, ProfileLegacy, "auto", "legacy", "ICE", "IPv6"} {
		cfg := validConfig()
		cfg.Transport.Preferred = old
		if err := cfg.Validate(); err == nil {
			t.Fatalf("accepted obsolete name %q", old)
		}
	}
	if ProfileICE != "ice-quic-mux-v1" || ProfileLegacy != "legacy-ipv6-quic-v2" {
		t.Fatal("configuration rename changed a wire protocol")
	}
}
