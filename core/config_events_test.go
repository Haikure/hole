package core

import (
	"context"
	"testing"
)

func TestConfigAppliedPublishesEngineState(t *testing.T) {
	for _, tc := range []struct {
		name, state, signal string
		stale               bool
		wantRunning         bool
	}{
		{"online", "reconfiguring", "joined", false, true},
		{"already_running", "running", "joined", false, false},
		{"recovering", "recovering", "reconnecting", false, false},
		{"rejoining", "reconfiguring", "connecting", false, false},
		{"superseded", "reconfiguring", "joined", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEngine(Options{})
			r := engineRequest()
			run := &engineRun{ctx: context.Background()}
			e.request, e.run, e.requested = &r, run, true
			e.state, e.signal = tc.state, tc.signal
			defer func() { e.run = nil; _ = e.Close() }()
			applied := r
			if tc.stale {
				applied.Config.DeviceName += "-old"
			}
			e.agentEvent(run, Event{Kind: "config", State: "applied", appliedConfig: &applied})
			var events []Event
			for len(e.events) > 0 {
				events = append(events, <-e.events)
			}
			if tc.stale {
				if len(events) != 0 || e.Snapshot().EngineState != tc.state {
					t.Fatalf("stale completion changed state: %+v", events)
				}
				return
			}
			if tc.wantRunning {
				if len(events) != 2 || events[0].Kind != "engine" || events[0].State != "running" {
					t.Fatalf("missing running notification: %+v", events)
				}
				if e.Snapshot().EngineState != "running" || e.Snapshot().SignalState != "joined" {
					t.Fatal("online completion did not retain joined state")
				}
			} else if len(events) != 1 || e.Snapshot().EngineState != tc.state {
				t.Fatalf("unexpected readiness transition: %+v", events)
			}
			last := events[len(events)-1]
			if last.Kind != "config" || last.State != "applied" || last.appliedConfig != nil {
				t.Fatalf("invalid completion: %+v", last)
			}
		})
	}
}
