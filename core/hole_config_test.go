package core

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestHoleConfigurationMigration(t *testing.T) {
	c := engineRequest().Config
	c.TURN = TURNConfig{Mode: "worker", Order: []string{"tls", "udp"}}
	c.Relay.Device = "relay"
	for _, tt := range []struct {
		order []string
		want  []string
	}{
		{nil, []string{"hole", "tls", "tls_443", "udp"}},
		{[]string{}, []string{"hole", "udp", "tcp", "tcp_80", "tls", "tls_443"}},
		{[]string{"udp", "hole"}, []string{"udp", "hole"}},
	} {
		c.Relay.Order = tt.order
		if got := c.deviceRelayOrder(RelayPolicyUDPTCPTLS); !reflect.DeepEqual(got, tt.want) {
			t.Fatalf("%v: %v", tt.order, got)
		}
		data, err := yaml.Marshal(c.Relay)
		if err != nil {
			t.Fatal(err)
		}
		var back RelayConfig
		if err = yaml.Unmarshal(data, &back); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(c.Relay.Order, back.Order) {
			t.Fatalf("nil/empty migration lost: %s", data)
		}
	}
	c.TURN.Mode = "off"
	c.Relay.Order = []string{"hole", "tls"}
	if got := c.deviceRelayOrder(RelayPolicyUDPTCPTLS); !reflect.DeepEqual(got, []string{"hole"}) {
		t.Fatal(got)
	}
	c.Relay.Device = ""
	if len(c.deviceRelayOrder(RelayPolicyUDPTCPTLS)) != 0 {
		t.Fatal("device-less endpoint proposed Hole")
	}
	for _, order := range [][]string{{"hole", "hole"}, {"none"}, {"udp", "udp"}} {
		c.Relay.Order = order
		if c.validateHoleRelay() == nil {
			t.Fatal("invalid order accepted", order)
		}
	}
}
func TestHoleServerCapabilityRequiresCLI(t *testing.T) {
	r := iceFixtureRequest("ws://127.0.0.1/ws", "relay")
	r.Config.Transport.FixedEndpoint = &FixedEndpointConfig{Listen: "0.0.0.0:40000", Advertise: "relay.example:40000"}
	r.Config.Relay.Serve.Enabled = true
	e := NewEngine(Options{})
	defer e.Close()
	for _, operation := range []func(Request) error{e.Start, e.ApplyConfig} {
		if err := operation(r); err == nil || !strings.Contains(err.Error(), "仅 CLI 支持") {
			t.Fatal(err)
		}
	}
}
func TestHolePlan(t *testing.T) {
	p := deviceRelayPlan([]string{"hole", "udp"}, []string{"hole", "tls"}, "z-relay", "a-relay")
	want := []relayPathStep{{Device: "a-relay", A: "hole", B: "hole"}, {Device: "z-relay", A: "hole", B: "hole"}, {A: "relay_udp", B: "relay_tls"}}
	if !reflect.DeepEqual(p, want) {
		t.Fatal(p)
	}
	p = deviceRelayPlan([]string{"hole", "udp"}, []string{"hole", "udp"}, "same", "same")
	if len(p) != 2 {
		t.Fatal(p)
	}
}

func TestHoleDoesNotHideUnusableManualTURNWhitelist(t *testing.T) {
	c := iceFixtureRequest("wss://relay.example/ws", "client").Config
	c.Relay.Device = "relay-node"
	c.TURN = TURNConfig{Mode: "manual", URLs: []string{"turn:turn.example:3478?transport=udp"}, Username: "user", Credential: "secret"}
	c.Relay.Order = []string{"hole", "tls"}
	if c.validateTransport() == nil {
		t.Fatal("Hole masked unusable explicit TURN whitelist")
	}
	c.Relay.Order = []string{"hole"}
	if err := c.validateTransport(); err != nil {
		t.Fatal("Hole-only intentionally excludes all TURN", err)
	}
}
