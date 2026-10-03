package core

import (
	"context"
	"fmt"
	"math"
	"net"
	"time"

	"github.com/pion/ice/v4"
	"github.com/quic-go/quic-go"
)

// Relay phases gather host, server-reflexive and relay candidates together,
// so a pair can be asymmetric: one side on its TURN allocation, the other
// reaching that allocation directly, one relay leg instead of two. RFC 8445
// pair priority already ranks such pairs above relay↔relay, and Pion
// nominates the best succeeded pair once every candidate in it has passed
// its type's minimum wait (2 s for relay by default); the selection is then
// fixed for the generation. A single-leg pair can only succeed after the
// peer has sent a check from its allocation towards our reflexive address,
// which creates the TURN permission, and candidates trickle through the
// Worker, so under signaling lag the double relay is sometimes the only
// succeeded pair at 2 s. Waiting longer before any relay-involving pair may
// be nominated costs at most the difference in connect time in relay phases;
// direct phases gather no relay candidates and are unaffected.
const (
	relayNominationWait = 3500 * time.Millisecond
	maxSnapshotPairs    = maxICECandidates
)

// CandidatePairSnapshot describes one checked pair of an active ICE transport.
// Pion stops checking the other pairs once one is nominated, so their state
// and RTT are those observed during connectivity checks; only the selected
// pair keeps updating.
type CandidatePairSnapshot struct {
	LocalType     string `json:"local_type"`
	RemoteType    string `json:"remote_type"`
	LocalAddress  string `json:"local_address"`
	RemoteAddress string `json:"remote_address"`
	State         string `json:"state"`
	RelayLegs     int    `json:"relay_legs"`
	RTTMS         int64  `json:"rtt_ms,string"`
	Selected      bool   `json:"selected,omitempty"`
}

func candidateAddress(c ice.Candidate) string {
	return net.JoinHostPort(c.Address(), fmt.Sprint(c.Port()))
}

// candidateSocket names the local socket a candidate sends from: its own
// address for a host candidate, the base address for a reflexive one.
func candidateSocket(c ice.Candidate) string {
	if related := c.RelatedAddress(); related != nil && c.Type() != ice.CandidateTypeHost {
		return net.JoinHostPort(related.Address, fmt.Sprint(related.Port))
	}
	return candidateAddress(c)
}

func relayLegs(local, remote ice.Candidate) int {
	legs := 0
	if local.Type() == ice.CandidateTypeRelay {
		legs++
	}
	if remote.Type() == ice.CandidateTypeRelay {
		legs++
	}
	return legs
}

// candidatePairSnapshots joins Pion's pair statistics with the candidates
// they name.
func candidatePairSnapshots(agent *ice.Agent) []CandidatePairSnapshot {
	locals, err := agent.GetLocalCandidates()
	if err != nil {
		return nil
	}
	remotes, err := agent.GetRemoteCandidates()
	if err != nil {
		return nil
	}
	localByID := make(map[string]ice.Candidate, len(locals))
	for _, c := range locals {
		localByID[c.ID()] = c
	}
	remoteByID := make(map[string]ice.Candidate, len(remotes))
	for _, c := range remotes {
		remoteByID[c.ID()] = c
	}
	selected, _ := agent.GetSelectedCandidatePair()
	var out []CandidatePairSnapshot
	for _, st := range agent.GetCandidatePairsStats() {
		if len(out) == maxSnapshotPairs {
			break
		}
		local, remote := localByID[st.LocalCandidateID], remoteByID[st.RemoteCandidateID]
		if local == nil || remote == nil {
			continue
		}
		s := CandidatePairSnapshot{LocalType: local.Type().String(), RemoteType: remote.Type().String(), LocalAddress: candidateAddress(local), RemoteAddress: candidateAddress(remote), State: st.State.String(), RelayLegs: relayLegs(local, remote), RTTMS: int64(st.CurrentRoundTripTime * 1000)}
		s.Selected = selected != nil && selected.Local.Equal(local) && selected.Remote.Equal(remote)
		out = append(out, s)
	}
	return out
}

func iceAttemptTimeout(cfg ICEConfig, phase string) time.Duration {
	if phase == "direct" {
		return cfg.DirectProbeTimeout.Duration()
	}
	return cfg.GatherTimeout.Duration() + cfg.ConnectivityTimeout.Duration()
}

// Candidate arrival triggers extra checks, consuming Pion's default seven
// retries in a burst. Let the bounded time budget control checking instead.
// MaxUint16-1 avoids overflow of Pion's uint16 counter (it tests count > limit).
func iceCheckOptions(timeout time.Duration) []ice.AgentOption {
	return []ice.AgentOption{ice.WithCheckInterval(200 * time.Millisecond),
		ice.WithMaxBindingRequests(math.MaxUint16 - 1), ice.WithFailedTimeout(max(20*time.Second, timeout))}
}

// Remote candidates enter the checklist asynchronously. Only a persistently
// empty list after both end-of-candidates markers warrants early failure.
func watchICEEmptyChecklist(ctx context.Context, agent *ice.Agent, fail func(error)) {
	const interval, required = 250 * time.Millisecond, 4
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	start := time.Now()
	strikes := 0
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if ctx.Err() != nil {
				return
			}
			stats := agent.GetCandidatePairsStats()
			if len(stats) != 0 || now.Sub(start) < time.Second {
				strikes = 0
				continue
			}
			if strikes++; strikes >= required {
				fail(&Fault{Code: "ice_path_failed", Message: "双方候选已收集完毕但没有可配对候选，尝试下一条路径"})
				return
			}
		}
	}
}

// A direct-phase transport only ever holds direct UDP pairs and probes the
// real path MTU (RFC 8899). Relay-phase transports stay at QUIC's 1200-byte
// floor: TURN adds its own encapsulation.
func iceQUICConfig(phase string) *quic.Config {
	return &quic.Config{EnableDatagrams: true, KeepAlivePeriod: 15 * time.Second, MaxIdleTimeout: 45 * time.Second, InitialPacketSize: 1200, DisablePathMTUDiscovery: phase != "direct", MaxIncomingStreams: maxSessionStreams,
		InitialStreamReceiveWindow: 512 * 1024, MaxStreamReceiveWindow: tcpReplayBuffer,
		InitialConnectionReceiveWindow: 2 * 1024 * 1024, MaxConnectionReceiveWindow: 32 * 1024 * 1024}
}
