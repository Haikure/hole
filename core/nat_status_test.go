package core

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestNATStatusPublishedAfterGenerationChange(t *testing.T) {
	c, p := lifecycleCoordinator(t)
	p.ready.Profile = ProfileICE
	p.ready.Mappings = nil
	c.natSnapshot = NATSnapshot{Status: "complete", Type: "endpoint_independent", Predictability: "stable", Epoch: c.networkEpoch.Load(), ProbedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	p.publishNAT()
	first := <-c.sink.messages
	if first.Type != "nat_status" || first.NAT.Snapshot != c.natSnapshot || first.TransportGeneration != 1 {
		t.Fatalf("initial status = %+v", first)
	}
	p.publishNAT()
	if len(c.sink.messages) != 0 {
		t.Fatal("unchanged status was sent twice")
	}
	// Relay generations need the cached network result as soon as authorized.
	ready := p.ready
	ready.TransportGeneration++
	ready.Phase = "relay_udp"
	ready.RetryAfterMS = 60_000
	ctx, cancel := context.WithCancel(p.ctx)
	p.ctx = ctx
	t.Cleanup(func() { cancel(); p.workers.Wait() })
	p.update(ready)
	p.publishNAT()
	second := <-c.sink.messages
	if second.Type != "nat_status" || second.TransportGeneration != 2 {
		t.Fatalf("new generation status = %+v", second)
	}
	// A new probe result inside the Worker rate limit stays pending locally.
	c.natSnapshot.ProbedAt = time.Now().Add(time.Second).UTC().Format(time.RFC3339Nano)
	p.publishNAT()
	if len(c.sink.messages) != 0 {
		t.Fatal("status exceeded the Worker rate limit")
	}
	p.natPublishedAt = time.Now().Add(-31 * time.Second)
	p.publishNAT()
	if len(c.sink.messages) != 1 {
		t.Fatal("new probe result was lost during rate limiting")
	}
}

func TestNATStatusUpdatesDisplayWithoutPendingAttempt(t *testing.T) {
	_, p := lifecycleCoordinator(t)
	want := NATSnapshot{Status: "complete", Type: "firewall", Predictability: "stable", MappingBehavior: "endpoint_independent", FilteringBehavior: "address_dependent", PortPreservation: "preserved", NoPAT: "not_applicable"}
	m := signalMessage("nat_status")
	m.TransportID, m.TransportGeneration = p.ready.TransportID, p.ready.TransportGeneration
	m.NAT = &natPlan{Snapshot: want}
	p.receive(m)
	if !reflect.DeepEqual(p.stats.PeerNAT, want) {
		t.Fatalf("peer NAT = %+v", p.stats.PeerNAT)
	}
	for _, kind := range []string{"nat_status"} {
		m.Type = kind
		m.TransportGeneration--
		m.NAT.Snapshot.Type = "symmetric"
		p.receive(m)
		m.TransportGeneration++
		m.TransportID = "other"
		p.receive(m)
		m.TransportID = p.ready.TransportID
		if p.stats.PeerNAT != want {
			t.Fatalf("stale %s changed the display", kind)
		}
	}
}

func TestNATDescriptionSharedProbeValues(t *testing.T) {
	for _, tc := range []struct{ kind, trend, want string }{
		{"firewall", "stable", "防火墙过滤 · 映射稳定"},
		{"symmetric", "predictable_incremental", "对称型 · 可预测（递增）"},
		{"symmetric", "predictable_decremental", "对称型 · 可预测（递减）"},
	} {
		if got := NATDescription(NATSnapshot{Status: "complete", Type: tc.kind, Predictability: tc.trend}); got != tc.want {
			t.Fatalf("description = %q, want %q", got, tc.want)
		}
	}
}
