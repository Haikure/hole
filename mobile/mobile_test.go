package mobile

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hole/core"
)

const requestJSON = `{"api_version":1,"server_url":"wss://example.invalid/ws","config":{"room":"fixture","password":"PRIVATE_PASSWORD","token":"PRIVATE_TOKEN","device_name":"fixture-device","provide":[],"consume":[]}}`

func TestFacadeStoppedLifecycle(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	var s map[string]any
	if err := json.Unmarshal([]byte(e.SnapshotJSON()), &s); err != nil {
		t.Fatal(err)
	}
	if s["engine_state"] != "stopped" || s["configured"] != false || Version() != 1 {
		t.Fatalf("initial: %+v", s)
	}
	if err := e.ApplyConfig(requestJSON); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(e.SnapshotJSON(), "PRIVATE_") {
		t.Fatal("snapshot contains credentials")
	}
	if err := e.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := e.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if err := e.Start(requestJSON); err == nil {
		t.Fatal("closed facade accepted start")
	}
}

type heldPlatform struct{ core.DefaultPlatform }

func (heldPlatform) Candidates(ctx context.Context, _ core.Config, _ int) ([]core.Candidate, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

type heldSink struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	count   atomic.Int32
}

func (s *heldSink) OnEvent(string) {
	s.count.Add(1)
	s.once.Do(func() { close(s.entered) })
	<-s.release
}

func TestFacadeStopQuiescesCallbacks(t *testing.T) {
	sink := &heldSink{entered: make(chan struct{}), release: make(chan struct{})}
	e := &Engine{engine: core.NewEngine(core.Options{Platform: heldPlatform{}}), sink: sink, pumpDone: make(chan struct{})}
	go e.pump()
	defer func() {
		select {
		case <-sink.release:
		default:
			close(sink.release)
		}
		_ = e.Close()
	}()
	if err := e.Start(requestJSON); err != nil {
		t.Fatal(err)
	}
	select {
	case <-sink.entered:
	case <-time.After(time.Second):
		t.Fatal("no callback")
	}
	stopped := make(chan struct{})
	go func() { _ = e.Stop(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("Stop returned during active callback")
	case <-time.After(20 * time.Millisecond):
	}
	close(sink.release)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not finish")
	}
	count := sink.count.Load()
	time.Sleep(20 * time.Millisecond)
	if sink.count.Load() != count {
		t.Fatal("callback arrived after Stop")
	}
}

func TestFacadeStrictRequests(t *testing.T) {
	if err := Validate(requestJSON); err != nil {
		t.Fatal(err)
	}
	legacy := strings.Replace(requestJSON, `,"server_url"`, `,"config_revision":"1","server_url"`, 1)
	if err := Validate(legacy); err != nil {
		t.Fatal("legacy config_revision was not accepted:", err)
	}
	for _, bad := range []string{
		strings.Replace(requestJSON, `"api_version":1`, `"api_version":2`, 1),
		strings.Replace(requestJSON, `"consume":[]`, `"consume":[],"enabled":true`, 1),
		strings.Replace(requestJSON, `"api_version":1`, `"api_version":1,"unknown":true`, 1),
		requestJSON + `{}`,
	} {
		err := Validate(bad)
		if err == nil {
			t.Fatal("accepted invalid request:", bad)
		}
		if !json.Valid([]byte(err.Error())) {
			t.Fatal("exception is not structured JSON:", err)
		}
	}
}
