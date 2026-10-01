package core

import "testing"

func TestManualRelayOrderUsesOnlyMatchingURLs(t *testing.T) {
	turn := TURNConfig{Mode: "manual", URLs: []string{"turn:relay.invalid:80?transport=tcp"}, Order: []string{"tcp_80", "udp"}, Username: "user", Credential: "secret"}
	order := relayOrderForConfig(RelayPolicyUDPTCPTLS, turn)
	if len(order) != 1 || order[0] != "tcp_80" {
		t.Fatal(order)
	}
	cfg := validConfig()
	cfg.TURN = turn
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg.TURN.URLs = []string{"turn:relay.invalid:3478?transport=tcp"}
	if err := cfg.Validate(); err != nil {
		t.Fatal("TCP standard port should share the TCP type", err)
	}
}

func TestRelayPairingCoversEveryCombinationInBothOrientations(t *testing.T) {
	for a := 0; a <= 5; a++ {
		for b := 0; b <= 5; b++ {
			left, right := orderedRelayTypes[:a], orderedRelayTypes[:b]
			pairs, reverse := relayPairs(left, right, true), relayPairs(right, left, false)
			want := a * b
			if a == 0 || b == 0 {
				want = max(a, b)
			}
			if len(pairs) != want || len(reverse) != want {
				t.Fatalf("%d/%d: incomplete pairing", a, b)
			}
			seen := map[[2]int]bool{}
			for i, pair := range pairs {
				if seen[pair] {
					t.Fatal("duplicate combination")
				}
				seen[pair] = true
				if reverse[i] != [2]int{pair[1], pair[0]} {
					t.Fatal("endpoints disagree on round")
				}
				phase := pairedRelayPhase(i+1, left, right, true)
				if pair[0] < 0 && phase != "relay_wait" || pair[0] >= 0 && phase != relayTypePhase(left[pair[0]]) {
					t.Fatal("wrong phase")
				}
			}
		}
	}
}
