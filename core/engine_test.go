package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type offlinePlatform struct {
	DefaultPlatform
	calls   atomic.Int32
	wait    bool
	entered chan struct{}
	once    sync.Once
}

func (p *offlinePlatform) Candidates(ctx context.Context, _ Config, _ int) ([]Candidate, error) {
	p.calls.Add(1)
	if p.entered != nil {
		p.once.Do(func() { close(p.entered) })
	}
	if p.wait {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return nil, nil
}

func engineRequest() Request {
	cfg := validConfig()
	cfg.Password, cfg.Token = "PRIVATE_PASSWORD", "PRIVATE_ROOM_TOKEN"
	return Request{ServerURL: "wss://example.invalid/ws", Config: cfg}
}

func TestEngineConstructionAndOfflineConfig(t *testing.T) {
	platform := &offlinePlatform{}
	e := NewEngine(Options{Platform: platform})
	defer e.Close()
	if s := e.Snapshot(); s.Configured || s.EngineState != "stopped" || s.RunRequested || len(s.Mappings) != 0 {
		t.Fatalf("initial snapshot: %+v", s)
	}
	r := engineRequest()
	if err := e.ApplyConfig(r); err != nil {
		t.Fatal(err)
	}
	r.Config.Provide[0].ID = "changed-by-caller"
	first := e.Snapshot()
	if !first.Configured || first.Mappings[0].ID != "ssh" {
		t.Fatalf("staging leaked mutation: %+v", first)
	}
	first.Mappings[0].ID = "changed-by-observer"
	if e.Snapshot().Mappings[0].ID != "ssh" {
		t.Fatal("snapshot is mutable")
	}
	if err := e.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := e.Stop(); err != nil {
		t.Fatal(err)
	}
	if platform.calls.Load() != 0 {
		t.Fatal("stopped operation contacted network")
	}
}

func TestEngineSignalReconnectIsNotReportedAsRunning(t *testing.T) {
	e := NewEngine(Options{})
	defer e.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &engineRun{ctx: ctx, cancel: func() {}, done: make(chan struct{})}
	request := engineRequest()
	e.mu.Lock()
	e.request = &request
	e.requested = true
	e.run = run
	e.state = "running"
	e.signal = "joined"
	e.mu.Unlock()

	e.agentEvent(run, Event{Kind: "signal", State: "reconnecting"})
	if snapshot := e.Snapshot(); snapshot.EngineState != "recovering" {
		t.Fatalf("reconnect retained ready state: %+v", snapshot)
	}
	e.agentEvent(run, Event{Kind: "signal", State: "joined"})
	if snapshot := e.Snapshot(); snapshot.EngineState != "running" {
		t.Fatalf("join did not restore ready state: %+v", snapshot)
	}
	close(run.done)
}

func TestCoreStrictDocumentAndCandidateValidation(t *testing.T) {
	if _, err := ParseConfig([]byte("room: first\n---\nroom: second\n")); err == nil {
		t.Fatal("accepted a trailing YAML document")
	}
	r := engineRequest()
	r.Config.CandidateAddresses = []string{"not-an-ip"}
	if err := r.Validate(); err == nil {
		t.Fatal("accepted malformed candidate without starting network")
	}
}

func TestEngineStartCancellationAndLiveConfig(t *testing.T) {
	p := &offlinePlatform{wait: true, entered: make(chan struct{})}
	e := NewEngine(Options{Platform: p})
	defer e.Close()
	r := engineRequest()
	if err := e.Start(r); err != nil {
		t.Fatal(err)
	}
	<-p.entered
	if err := e.Start(r); err != nil {
		t.Fatal("identical Start is not idempotent:", err)
	}
	if err := e.ApplyConfig(r); err != nil {
		t.Fatal("live config was rejected:", err)
	}
	if err := e.NetworkChanged(); err != nil {
		t.Fatal("network change rejected", err)
	}
	done := make(chan struct{})
	go func() { _ = e.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not cancel startup")
	}
	if s := e.Snapshot(); s.EngineState != "stopped" || s.RunRequested || s.SignalState != "disconnected" {
		t.Fatalf("after stop: %+v", s)
	}
	if err := e.ApplyConfig(r); err != nil {
		t.Fatal(err)
	}
}

func TestEngineNetworkFailureIsNotConnected(t *testing.T) {
	e := NewEngine(Options{Platform: &offlinePlatform{}})
	defer e.Close()
	if err := e.Start(engineRequest()); err != nil {
		t.Fatal(err)
	}
	if err := e.Wait(context.Background()); err == nil {
		t.Fatal("missing IPv6 not reported")
	}
	s := e.Snapshot()
	if s.EngineState != "error" || s.SignalState != "disconnected" || s.Error == nil || s.Error.Code != "no_ipv6" {
		t.Fatalf("wrong failure snapshot: %+v", s)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if err := e.Start(engineRequest()); !errors.Is(err, ErrClosed) {
		t.Fatal("closed engine started:", err)
	}
}

func TestEngineBoundedEventsAndRedaction(t *testing.T) {
	p := &offlinePlatform{wait: true, entered: make(chan struct{})}
	e := NewEngine(Options{Platform: p, EventCapacity: 2})
	defer e.Close()
	r := engineRequest()
	if err := e.Start(r); err != nil {
		t.Fatal(err)
	}
	<-p.entered
	for i := 0; i < 100; i++ {
		e.agentEvent(e.run, Event{Kind: "log", Message: r.Config.Password + " " + r.Config.Token})
	}
	if e.Snapshot().EventsDropped == 0 {
		t.Fatal("overflow not counted")
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	for event := range e.Events() {
		data, _ := json.Marshal(event)
		if strings.Contains(string(data), r.Config.Password) || strings.Contains(string(data), r.Config.Token) {
			t.Fatal("secret in event")
		}
	}
	data, _ := json.Marshal(e.Snapshot())
	if strings.Contains(string(data), r.Config.Password) || strings.Contains(string(data), "desired_revision") || strings.Contains(string(data), "applied_revision") {
		t.Fatalf("snapshot JSON: %s", data)
	}
}
