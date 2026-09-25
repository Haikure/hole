package core

import (
	"reflect"
	"testing"

	"github.com/pion/ice/v4"
)

func TestRelayPolicyOrderAndCompatibility(t *testing.T) {
	for policy, order := range map[string][]string{
		RelayPolicyUDPTCPTLS: {"direct", "relay_udp", "relay_tcp_80", "relay_tcp", "relay_tls_443", "relay_tls", "direct"},
		"":                   {"direct", "relay_udp", "relay_tls", "relay_tls_443", "direct"},
	} {
		for i := 0; i < len(order)-1; i++ {
			if !validRelayPhase(policy, order[i]) || nextICEPhase(order[i], policy) != order[i+1] {
				t.Fatalf("policy %q: unexpected next phase for %q", policy, order[i])
			}
		}
	}
	if validRelayPhase("unknown", "direct") || validRelayPhase("", "relay_tcp") || validRelayPhase(RelayPolicyUDPTCPTLS, "unknown") {
		t.Fatal("unsupported phase accepted")
	}
}

func TestCustomRelayRoundOrderAndRelayWait(t *testing.T) {
	local := []string{"udp"}
	remote := []string{"tls_443", "udp"}
	if got := relayOrder(RelayPolicyUDPTCPTLS, nil); !reflect.DeepEqual(got, orderedRelayTypes) {
		t.Fatalf("default order changed: %v", got)
	}
	if relayRoundLimit(local, remote) != 2 || !validRelayRound(1, "relay_udp", local, remote) || !validRelayRound(2, "relay_wait", local, remote) {
		t.Fatal("per-end round translation failed")
	}
	if validRelayRound(2, "relay_udp", local, remote) || validRelayRound(3, "relay_wait", local, remote) {
		t.Fatal("accepted a phase or round that does not match this member")
	}
	if nextICERound(1, 2) != 2 || nextICERound(2, 2) != 0 {
		t.Fatal("round progression did not wrap to direct")
	}
	if round, phase := nextRelayRound(1, local, remote); round != 2 || phase != "relay_wait" {
		t.Fatalf("exhausted local order must wait for peer relay, got round=%d phase=%q", round, phase)
	}
	disabled := relayOrderForConfig(RelayPolicyUDPTCPTLS, TURNConfig{Mode: "off", Order: local})
	if len(disabled) != 0 {
		t.Fatalf("disabled TURN must contribute no relay rounds, got %v", disabled)
	}
	if round, phase := nextRelayRound(0, disabled, disabled); round != 0 || phase != "direct" {
		t.Fatalf("two disabled TURN endpoints must retry direct, got round=%d phase=%q", round, phase)
	}
	if got := relayOrderForConfig(RelayPolicyUDPTCPTLS, TURNConfig{Mode: "worker"}); !reflect.DeepEqual(got, orderedRelayTypes) {
		t.Fatalf("enabled TURN must retain the default order, got %v", got)
	}
}

func TestTURNOrderValidation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		order []string
		valid bool
	}{
		{name: "omitted uses default", valid: true},
		{name: "custom", order: []string{"tls_443", "udp", "tcp"}, valid: true},
		{name: "all types", order: []string{"udp", "tcp_80", "tcp", "tls_443", "tls"}, valid: true},
		{name: "unknown", order: []string{"direct"}},
		{name: "duplicate", order: []string{"udp", "udp"}},
		{name: "too many", order: []string{"udp", "tcp_80", "tcp", "tls_443", "tls", "udp"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{TURN: TURNConfig{Mode: "off", Order: tc.order}}
			err := cfg.validateTransport()
			if (err == nil) != tc.valid {
				t.Fatalf("validateTransport() error = %v, valid=%v", err, tc.valid)
			}
		})
	}
}

func TestRelayURLsAreSeparatedByActualSchemeAndPort(t *testing.T) {
	servers := []ICEServer{{URLs: []string{
		"turn:HOST:3478?transport=tcp", "turns:HOST:5349?transport=tcp", "turn:HOST:80?transport=tcp",
		"turns:HOST:443?transport=tcp", "turn:HOST:3478?transport=udp", "turns:HOST:1443?transport=tcp", "turn:HOST:18080?transport=tcp",
	}, Username: "user", Credential: "credential"}}
	for phase, expected := range map[string][]int{
		"direct": {}, "relay_wait": {}, "relay_udp": {3478}, "relay_tls_443": {443}, "relay_tls": {5349, 1443}, "relay_tcp_80": {80}, "relay_tcp": {3478, 18080},
	} {
		ports := []int{}
		for _, u := range selectRelayURLs(servers, phase) {
			ports = append(ports, u.Port)
			if u.Username != "user" || u.Password != "credential" {
				t.Fatal("credentials lost")
			}
		}
		if !reflect.DeepEqual(ports, expected) {
			t.Fatalf("%s: got %v want %v", phase, ports, expected)
		}
	}
}

func TestSelectedPairReportsRealRelayProtocolNotCandidateUDPOrPhase(t *testing.T) {
	host, err := ice.NewCandidateHost(&ice.CandidateHostConfig{Network: "udp", Address: "192.0.2.1", Port: 10000, Component: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, protocol := range []string{"udp", "tcp", "tls"} {
		relay, err := ice.NewCandidateRelay(&ice.CandidateRelayConfig{Network: "udp", Address: "198.51.100.1", Port: 40000, Component: 1, RelayProtocol: protocol})
		if err != nil {
			t.Fatal(err)
		}
		s := PeerTransportSnapshot{Phase: "relay_tls_443"}
		selectedICEPair(&s, relay, host)
		if s.PathType != "relay" || s.LocalRelayProtocol != protocol || s.RelayProtocol != protocol || s.RelaySide != "local" {
			t.Fatal(s)
		}
		selectedICEPair(&s, host, relay)
		if s.PathType != "relay" || s.LocalRelayProtocol != "" || s.RelayProtocol != "" || s.RelaySide != "remote" {
			t.Fatal("guessed remote TURN access", s)
		}
		selectedICEPair(&s, host, host)
		if s.PathType != "direct" || s.RelayProtocol != "" || s.RelaySide != "" {
			t.Fatal("retained a stale relay label", s)
		}
	}
}
