package core

import (
	"fmt"
	"net"
	"slices"

	"github.com/pion/ice/v4"
	"github.com/pion/stun/v4"
)

var orderedRelayPhases = []string{"direct", "relay_udp", "relay_tcp_80", "relay_tcp", "relay_tls_443", "relay_tls"}
var compatibleRelayPhases = []string{"direct", "relay_udp", "relay_tls", "relay_tls_443"}

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
