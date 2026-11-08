package desktop

import (
	"encoding/json"
	"testing"

	"hole/core"
)

func TestNATDiagnosticRPCWithoutStart(t *testing.T) {
	e := core.NewEngine(core.Options{})
	defer e.Close()
	h := host{engine: e}
	for _, params := range []string{`{"room":"unexpected"}`, `{"nat_stun_urls":[]}`} {
		if _, fault, _ := h.dispatch(request{Method: "start_nat_probe", Params: json.RawMessage(params)}); fault == nil {
			t.Fatal("accepted invalid diagnostic request")
		}
	}
	if _, fault, _ := h.dispatch(request{Method: "start_nat_probe", Params: json.RawMessage(`{"nat_stun_urls":["stun:127.0.0.1:9"]}`)}); fault != nil {
		t.Fatal(fault)
	}
	if _, fault, _ := h.dispatch(request{Method: "cancel_nat_probe"}); fault != nil {
		t.Fatal(fault)
	}
	if s := e.Snapshot(); s.RunRequested || s.Configured || s.NATDiagnostic.State != "cancelled" {
		t.Fatalf("diagnostic changed run state: %+v", s)
	}
}
