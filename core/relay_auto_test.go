package core

import (
	"reflect"
	"testing"

	"github.com/pion/stun/v4"
)

func TestRelayAutomaticPortsAndImplicitTransport(t *testing.T) {
	worker := TURNConfig{Mode: "worker", Order: []string{"tls", "tcp", "udp"}}
	if got := relayOrderForConfig(RelayPolicyUDPTCPTLS, worker); !reflect.DeepEqual(got, []string{"tls", "tls_443", "tcp", "tcp_80", "udp"}) {
		t.Fatal(got)
	}
	for _, tc := range []struct {
		url   string
		order []string
	}{
		{"turn:relay.invalid:3478", []string{"udp", "tcp"}},
		{"turn:relay.invalid:80", []string{"udp", "tcp_80"}},
		{"turn:relay.invalid:5350?transport=tcp", []string{"tcp"}},
		{"turn:relay.invalid:5350?transport=udp", []string{"udp"}},
		{"turns:relay.invalid:5349", []string{"tls"}},
		{"turns:relay.invalid:443", []string{"tls_443"}},
	} {
		t.Run(tc.url, func(t *testing.T) {
			turn := TURNConfig{Mode: "manual", URLs: []string{tc.url}, Username: "user", Credential: "secret"}
			cfg := validConfig()
			cfg.TURN = turn
			if err := cfg.Validate(); err != nil {
				t.Fatal(err)
			}
			if got := relayOrderForConfig(RelayPolicyUDPTCPTLS, turn); !reflect.DeepEqual(got, tc.order) {
				t.Fatal(got, tc.order)
			}
			for _, kind := range tc.order {
				urls := selectRelayURLs([]ICEServer{{URLs: turn.URLs}}, relayTypePhase(kind))
				if len(urls) != 1 {
					t.Fatal(urls)
				}
				if kind != "udp" && urls[0].Proto != stun.ProtoTypeTCP {
					t.Fatal("transport not inferred")
				}
			}
		})
	}
	for _, raw := range []string{"turn:relay.invalid", "turns:relay.invalid", "turn:relay.invalid:0", "turns:relay.invalid:443?transport=udp"} {
		cfg := validConfig()
		cfg.TURN = TURNConfig{Mode: "manual", URLs: []string{raw}, Username: "user", Credential: "secret"}
		if err := cfg.Validate(); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	cfg := validConfig()
	cfg.TURN.Order = []string{"tcp_80", "udp", "tcp", "tls_443", "tls"}
	if got := cfg.Normalized().TURN.Order; !reflect.DeepEqual(got, []string{"tcp", "udp", "tls"}) {
		t.Fatal("legacy order not migrated", got)
	}
}

func TestReplayCacheRemainsBoundedAndReleasesIdleBlocks(t *testing.T) {
	budget := &replayBudget{limit: 2 * tcpFramePayload}
	r := tcpReplay{budget: budget}
	r.append(make([]byte, 2*tcpFramePayload))
	r.clear()
	if budget.used+len(budget.free)*tcpFramePayload > budget.limit {
		t.Fatal("cache escaped memory limit")
	}
	block := &budget.free[0][:cap(budget.free[0])][0]
	r.append([]byte("new"))
	r.clear()
	if len(budget.free) != 2 || &budget.free[0][:cap(budget.free[0])][0] != block {
		t.Fatal("cache not reused")
	}
	budget.trim(budget.lastUse.Add(tcpReplayCacheIdle))
	if len(budget.free) != 0 || budget.used != 0 {
		t.Fatal("idle cache retained")
	}
}
