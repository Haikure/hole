package core

import (
	"context"
	"gopkg.in/yaml.v3"
	"reflect"
	"testing"
	"time"
)

func TestNATBudgetIncludesSemaphoreWaitAndOldNetworkCannotPublish(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	c := &iceCoordinator{natSlots: make(chan struct{}, 1)}
	c.natSlots <- struct{}{}
	a := &iceAttempt{ctx: ctx, cancel: cancel, natTried: true}
	p := &icePeer{coordinator: c, pending: a}
	start := time.Now()
	p.attemptNAT(a)
	if time.Since(start) > time.Second {
		t.Fatal("queue exceeded parent deadline")
	}
	if len(c.natSlots) != 1 {
		t.Fatal("queue consumed another task's slot")
	}
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
	}{{"ice: {stun_urls: []}", 0}, {"ice: {stun_urls: [], nat_stun_urls: ['stun:127.0.0.1:3478']}", 1}, {"ice: {nat_stun_urls: []}", 0}, {"ice: {}", 4}} {
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
