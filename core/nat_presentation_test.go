package core

import (
	"testing"
	"time"
)

func TestNATDiagnosticColorsRespectEvidenceAndFreshness(t *testing.T) {
	for _, tc := range []struct{ kind, trend, tone string }{
		{"open", "stable", "green"}, {"full_cone", "stable", "green"},
		{"restricted_cone", "stable", "blue"}, {"port_restricted_cone", "stable", "blue"},
		{"symmetric", "predictable_incremental", "yellow"}, {"symmetric", "predictable_decremental", "yellow"},
		{"symmetric", "incremental", "yellow"}, {"symmetric", "decremental", "yellow"},
		{"symmetric", "irregular", "red"}, {"symmetric", "unknown", "neutral"},
		{"endpoint_independent", "stable", "neutral"},
	} {
		t.Run(tc.kind+"/"+tc.trend, func(t *testing.T) {
			d := NATDiagnostic{State: "complete", Result: NATSnapshot{Status: "complete", Type: tc.kind, Predictability: tc.trend,
				Samples: 18, ProbedAt: time.Now().UTC().Format(time.RFC3339Nano)}}
			view := d.view()
			if view.Tone != tc.tone || view.Badge == "" || len(view.Highlights) != 6 || view.Highlights[4].Value != "18" {
				t.Fatalf("unexpected presentation: %+v", view)
			}
			for _, state := range []string{"queued", "probing", "failed", "cancelled", "network_changed", "insufficient"} {
				d.State = state
				if got := d.view(); got.Tone != "neutral" {
					t.Fatalf("%s retained stale color: %+v", state, got)
				}
			}
			d.State = "complete"
			d.Result.ProbedAt = time.Now().Add(-6 * time.Minute).UTC().Format(time.RFC3339Nano)
			if got := d.view(); got.Tone != "neutral" || got.Badge != "需重新检测" {
				t.Fatal(got)
			}
		})
	}
}
