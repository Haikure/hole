package core

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestManualNATProbeWithoutRoom(t *testing.T) {
	e := NewEngine(Options{})
	defer e.Close()
	if err := e.StartNATProbe(NATProbeOptions{NATSTUNURLs: natFixtureSTUNURLs(t)}); err != nil {
		t.Fatal(err)
	}
	s := waitSnapshot(t, e, func(s Snapshot) bool { return s.NATDiagnostic.FinishedAt != "" })
	d := s.NATDiagnostic
	if s.Configured || s.RunRequested || d.Result.Samples != 6 || len(d.Sections) != 5 || d.Result.ProbedAt == "" || len(d.Mappings) == 0 {
		t.Fatalf("unexpected standalone result: %+v", d)
	}
	if len(d.Sections[3].Fields) < 7 {
		t.Fatal("missing diagnostic evidence")
	}
	d.Servers[0] = "modified"
	d.Network.Addresses = append(d.Network.Addresses, "modified")
	if e.Snapshot().NATDiagnostic.Servers[0] == "modified" {
		t.Fatal("snapshot aliases live data")
	}
	if err := e.NetworkChanged(); err != nil {
		t.Fatal(err)
	}
	if e.Snapshot().NATDiagnostic.State != "network_changed" {
		t.Fatal("stopped network change retained fresh result")
	}
}

func TestManualNATProbeCancellationAndAllocationExclusion(t *testing.T) {
	e := NewEngine(Options{})
	defer e.Close()
	release, err := e.natAllocation.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	options := NATProbeOptions{NATSTUNURLs: []string{"stun:127.0.0.1:9"}}
	if err := e.StartNATProbe(options); err != nil {
		t.Fatal(err)
	}
	first := e.Snapshot().NATDiagnostic.StartedAt
	if err := e.StartNATProbe(options); err != nil {
		t.Fatal(err)
	}
	if e.Snapshot().NATDiagnostic.StartedAt != first {
		t.Fatal("duplicate detection restarted")
	}
	if e.Snapshot().NATDiagnostic.State != "queued" {
		t.Fatal("probe ignored live allocation lock")
	}
	if err := e.CancelNATProbe(); err != nil {
		t.Fatal(err)
	}
	if e.Snapshot().NATDiagnostic.State != "cancelled" {
		t.Fatal("missing cancelled state")
	}
	if err := e.StartNATProbe(options); err != nil {
		t.Fatal(err)
	}
	if err := e.NetworkChanged(); err != nil {
		t.Fatal(err)
	}
	e.CancelNATProbe()
	if e.Snapshot().NATDiagnostic.State != "network_changed" {
		t.Fatal("cancel overwrote network invalidation")
	}
	if err := e.StartNATProbe(options); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if err := e.StartNATProbe(options); err != ErrClosed {
		t.Fatalf("closed engine accepted probe: %v", err)
	}
}

func TestManualNATOptionsAndEvidence(t *testing.T) {
	for _, text := range []string{"null", "[]", "{}{}", `{"password":"secret"}`} {
		if _, err := ParseNATProbeOptions([]byte(text)); err == nil {
			t.Fatalf("accepted %q", text)
		}
	}
	e := NewEngine(Options{})
	defer e.Close()
	for _, options := range []NATProbeOptions{{NATSTUNURLs: []string{}}, {NATSTUNURLs: []string{"turn:localhost:3478"}}, {STUNURLs: []string{}}} {
		if err := e.StartNATProbe(options); err == nil {
			t.Fatal("disabled/invalid servers accepted")
		}
	}
	d := NATDiagnostic{State: "complete", Result: NATSnapshot{Status: "complete", Type: "symmetric", FilteringBehavior: "address_port_dependent", Predictability: "incremental", PortPreservation: "mixed", NoPAT: "suspected", ProbedAt: time.Now().UTC().Format(time.RFC3339Nano)}}.view()
	if d.Sections[0].Fields[2].Value != "地址与端口相关" {
		t.Fatal("lost confirmed filtering evidence")
	}
	if !strings.Contains(d.Sections[1].Fields[0].Value, "可能") || d.Sections[1].Fields[4].Value != "疑似 No-PAT" {
		t.Fatal("overstated evidence", d.Sections)
	}
	d.Result.ProbedAt = time.Now().Add(-6 * time.Minute).UTC().Format(time.RFC3339Nano)
	if d.view().State != "expired" {
		t.Fatal("old diagnostic remains fresh")
	}
}
