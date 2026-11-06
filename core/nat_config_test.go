package core

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	"hole/core/natprobe"
)

func TestNATOldNetworkCannotPublish(t *testing.T) {
	c := &iceCoordinator{}
	c.natSnapshot = NATSnapshot{Status: "complete", Type: "symmetric", Epoch: 1}
	c.networkEpoch.Store(2)
	if s := c.localNAT(); s.Type != "unknown" || s.Status != "insufficient" {
		t.Fatal("stale network snapshot visible", s)
	}
}

func TestNATConfigProbeConsentAndRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want int
	}{{"ice: {stun_urls: []}", 0}, {"ice: {stun_urls: [], nat_stun_urls: ['stun:127.0.0.1:3478']}", 1}, {"ice: {nat_stun_urls: []}", 0}, {"ice: {}", 6}} {
		cfg, e := ParseConfig([]byte(tc.raw))
		if e != nil {
			t.Fatal(e)
		}
		cfg = cfg.Normalized()
		if n := len(natURLs(cfg.ICE)); n != tc.want {
			t.Fatalf("%s: %d", tc.raw, n)
		}
		data, e := yaml.Marshal(cfg.ICE)
		if e != nil {
			t.Fatal(e)
		}
		var restored ICEConfig
		if e = yaml.Unmarshal(data, &restored); e != nil {
			t.Fatal(e)
		}
		if !reflect.DeepEqual(restored.NATSTUNURLs, cfg.ICE.NATSTUNURLs) {
			t.Fatalf("NAT service presence lost: %s", data)
		}
	}
}

func TestNATProbeDefaultServersMatchNatprobe(t *testing.T) {
	cfg, err := ParseConfig([]byte("ice: {}"))
	if err != nil {
		t.Fatal(err)
	}
	want := natprobe.DefaultServers()
	if got := natURLs(cfg.Normalized().ICE); !reflect.DeepEqual(got, want) {
		t.Fatalf("default NAT STUN servers: %v, want %v", got, want)
	}
}

func TestNATConfigRetainsMappingAndDropsRemovedOption(t *testing.T) {
	if _, err := ParseConfig([]byte("ice: {udp_punching: off}")); err == nil {
		t.Fatal("removed option was accepted")
	}
	cfg, err := ParseConfig([]byte("ice: {port_mapping: off, nat_stun_urls: []}"))
	if err != nil {
		t.Fatal(err)
	}
	cfg = cfg.Normalized()
	if cfg.ICE.PortMapping != "off" || cfg.ICE.NATSTUNURLs == nil {
		t.Fatal(cfg.ICE)
	}
	for _, marshal := range []func(any) ([]byte, error){json.Marshal, yaml.Marshal} {
		data, err := marshal(cfg.ICE)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "udp_punching") {
			t.Fatalf("removed option serialized: %s", data)
		}
	}
	if cfg := (Config{}).Normalized(); cfg.ICE.PortMapping != "auto" {
		t.Fatal(cfg.ICE)
	}
}
