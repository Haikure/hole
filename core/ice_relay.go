package core

import (
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"

	"github.com/pion/ice/v4"
	"github.com/pion/stun/v4"
)

var orderedRelayPhases = []string{"direct", "relay_udp", "relay_tcp_80", "relay_tcp", "relay_tls_443", "relay_tls"}
var compatibleRelayPhases = []string{"direct", "relay_udp", "relay_tls", "relay_tls_443"}
var orderedRelayTypes = []string{"udp", "tcp_80", "tcp", "tls_443", "tls"}
var compatibleRelayTypes = []string{"udp", "tls", "tls_443"}

const relayPairingAll = "all-pairs-v1"

// Expand by preference depth, trying each diagonal first, then its cross pairs.
// Device-name order fixes orientation so both endpoints derive the same plan.
// An endpoint with TURN disabled contributes host/srflx in every peer round.
func relayPairs(local, remote []string, localFirst bool) [][2]int {
	a, b := len(local), len(remote)
	if !localFirst {
		a, b = b, a
	}
	var pairs [][2]int
	add := func(i, j int) {
		if !localFirst {
			i, j = j, i
		}
		pairs = append(pairs, [2]int{i, j})
	}
	if a == 0 || b == 0 {
		for i := 0; i < max(a, b); i++ {
			if a == 0 {
				add(-1, i)
			} else {
				add(i, -1)
			}
		}
		return pairs
	}
	for depth := 0; depth < max(a, b); depth++ {
		if depth < a && depth < b {
			add(depth, depth)
		}
		for i := 0; i < depth; i++ {
			if i < a && depth < b {
				add(i, depth)
			}
			if depth < a && i < b {
				add(depth, i)
			}
		}
	}
	return pairs
}

func pairedRelayPhase(round int, local, remote []string, localFirst bool) string {
	if round == 0 {
		return "direct"
	}
	pairs := relayPairs(local, remote, localFirst)
	if round < 1 || round > len(pairs) {
		return ""
	}
	index := pairs[round-1][0]
	if index < 0 {
		return "relay_wait"
	}
	return relayTypePhase(local[index])
}

func relayOrder(policy string, custom []string) []string {
	if len(custom) > 0 {
		return slices.Clone(custom)
	}
	if policy == RelayPolicyUDPTCPTLS {
		return slices.Clone(orderedRelayTypes)
	}
	return slices.Clone(compatibleRelayTypes)
}

func relayOrderForConfig(policy string, turn TURNConfig) []string {
	if turn.Mode == "off" {
		return []string{}
	}
	order := normalizedRelayOrder(turn.Order)
	if len(order) == 0 {
		order = []string{"udp", "tcp", "tls"}
	}
	available := []string{}
	servers := []ICEServer{{URLs: turn.URLs}}
	for _, token := range order {
		stages := []string{token}
		switch token {
		case "tcp":
			stages = []string{"tcp", "tcp_80"}
		case "tls":
			stages = []string{"tls", "tls_443"}
		}
		for _, stage := range stages {
			if turn.Mode != "manual" || len(selectRelayURLs(servers, relayTypePhase(stage))) > 0 {
				available = append(available, stage)
			}
		}
	}
	return available
}

// Port-specific tokens are only retained on the compatibility wire. Older
// configuration documents migrate to protocol names while genuine duplicates
// still fail validation.
func normalizedRelayOrder(order []string) []string {
	result := make([]string, 0, len(order))
	seenRaw, seenType := map[string]bool{}, map[string]bool{}
	for _, raw := range order {
		token := raw
		if token == "tcp_80" {
			token = "tcp"
		}
		if token == "tls_443" {
			token = "tls"
		}
		if !seenType[token] || seenRaw[raw] {
			result = append(result, token)
		}
		seenRaw[raw], seenType[token] = true, true
	}
	return result
}

func relayTypePhase(token string) string {
	switch token {
	case "udp":
		return "relay_udp"
	case "tcp_80":
		return "relay_tcp_80"
	case "tcp":
		return "relay_tcp"
	case "tls_443":
		return "relay_tls_443"
	case "tls":
		return "relay_tls"
	default:
		return ""
	}
}

func relayRoundPhase(round int, order []string) string {
	if round == 0 {
		return "direct"
	}
	if round < 0 || round > len(order) {
		return ""
	}
	return relayTypePhase(order[round-1])
}

func relayRoundLimit(local, remote []string) int {
	if len(remote) > len(local) {
		return len(remote)
	}
	return len(local)
}

func validRelayOrder(order []string) bool {
	if len(order) > 5 {
		return false
	}
	seen := map[string]bool{}
	for _, token := range order {
		if !slices.Contains(orderedRelayTypes, token) || seen[token] {
			return false
		}
		seen[token] = true
	}
	return true
}

func nextICERound(round, limit int) int {
	if round < limit {
		return round + 1
	}
	return 0
}

func nextRelayRound(round int, local, remote []string) (int, string) {
	next := nextICERound(round, relayRoundLimit(local, remote))
	if next > len(local) {
		return next, "relay_wait"
	}
	return next, relayRoundPhase(next, local)
}

func validRelayRound(round int, phase string, local, remote []string) bool {
	if !validRelayOrder(local) || !validRelayOrder(remote) {
		return false
	}
	limit := relayRoundLimit(local, remote)
	if round < 0 || round > limit {
		return false
	}
	want := "relay_wait"
	if round <= len(local) {
		want = relayRoundPhase(round, local)
	}
	return phase == want
}

func relayPhases(policy string) []string {
	if policy == RelayPolicyUDPTCPTLS {
		return orderedRelayPhases
	}
	return compatibleRelayPhases
}

func validRelayPhase(policy, phase string) bool {
	return (policy == "" || policy == RelayPolicyUDPTCPTLS) && slices.Contains(relayPhases(policy), phase)
}

func nextICEPhase(phase, policy string) string {
	order := relayPhases(policy)
	i := slices.Index(order, phase)
	if i < 0 || i == len(order)-1 {
		return "direct"
	}
	return order[i+1]
}

// Each phase owns a separate ICE generation. Preferred and fallback ports are
// never gathered together, so this is a real ordering, not just URL sorting.
func relayURLMatches(phase string, u *stun.URI) bool {
	switch phase {
	case "relay_udp":
		return u.Scheme == stun.SchemeTypeTURN && u.Proto == stun.ProtoTypeUDP
	case "relay_tls_443":
		return u.Scheme == stun.SchemeTypeTURNS && u.Proto == stun.ProtoTypeTCP && u.Port == 443
	case "relay_tls":
		return u.Scheme == stun.SchemeTypeTURNS && u.Proto == stun.ProtoTypeTCP && u.Port != 443
	case "relay_tcp_80":
		return u.Scheme == stun.SchemeTypeTURN && u.Proto == stun.ProtoTypeTCP && u.Port == 80
	case "relay_tcp":
		return u.Scheme == stun.SchemeTypeTURN && u.Proto == stun.ProtoTypeTCP && u.Port != 80
	default:
		return false
	}
}

func selectRelayURLs(servers []ICEServer, phase string) []*stun.URI {
	urls := []*stun.URI{}
	for _, server := range servers {
		for _, raw := range server.URLs {
			u, err := stun.ParseURI(raw)
			if err == nil && u.Scheme == stun.SchemeTypeTURN {
				_, query, _ := strings.Cut(raw, "?")
				values, _ := url.ParseQuery(query)
				if !values.Has("transport") && (phase == "relay_tcp" || phase == "relay_tcp_80") {
					u.Proto = stun.ProtoTypeTCP
				}
			}
			if err == nil && relayURLMatches(phase, u) {
				u.Username, u.Password = server.Username, server.Credential
				urls = append(urls, u)
			}
		}
	}
	return urls
}

func selectedICEPair(s *PeerTransportSnapshot, local, remote ice.Candidate) {
	s.LocalType, s.RemoteType = local.Type().String(), remote.Type().String()
	s.LocalAddress = net.JoinHostPort(local.Address(), fmt.Sprint(local.Port()))
	s.RemoteAddress = net.JoinHostPort(remote.Address(), fmt.Sprint(remote.Port()))
	s.PathType, s.LocalRelayProtocol, s.RelayProtocol, s.RelaySide = "direct", "", "", ""
	localRelay, remoteRelay := local.Type() == ice.CandidateTypeRelay, remote.Type() == ice.CandidateTypeRelay
	if localRelay || remoteRelay {
		s.PathType = "relay"
		s.RelaySide = "remote"
		if localRelay {
			s.RelaySide = "local"
			if remoteRelay {
				s.RelaySide = "both"
			}
			// The candidate knows the actual TURN access protocol. ICE relay
			// candidates themselves remain UDP even for TURN-over-TCP/TLS.
			if relay, ok := local.(interface{ RelayProtocol() string }); ok {
				s.LocalRelayProtocol = relay.RelayProtocol()
				s.RelayProtocol = s.LocalRelayProtocol
			}
		}
		// Remote SDP candidates do not carry their TURN access protocol.
		// Leave it unknown instead of inferring TLS/TCP from a phase or port.
	}
	s.AddressFamily = "IPv4"
	if net.ParseIP(local.Address()).To4() == nil {
		s.AddressFamily = "IPv6"
	}
}
