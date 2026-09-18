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

func TestRelayURLsAreSeparatedByActualSchemeAndPort(t *testing.T) {
	servers := []ICEServer{{URLs: []string{
		"turn:HOST:3478?transport=tcp", "turns:HOST:5349?transport=tcp", "turn:HOST:80?transport=tcp",
		"turns:HOST:443?transport=tcp", "turn:HOST:3478?transport=udp", "turns:HOST:1443?transport=tcp", "turn:HOST:18080?transport=tcp",
	}, Username: "user", Credential: "credential"}}
	for phase, expected := range map[string][]int{
		"direct": {}, "relay_udp": {3478}, "relay_tls_443": {443}, "relay_tls": {5349, 1443}, "relay_tcp_80": {80}, "relay_tcp": {3478, 18080},
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
