package core

import (
	"context"
	"fmt"
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

func iceChecksExhausted(stats []ice.CandidatePairStats) bool {
	for _, s := range stats {
		if s.State != ice.CandidatePairStateFailed {
			return false
		}
	}
	return true
}

// watchICEExhaustion ends an attempt once every pair the two gathered
// candidate sets can form has failed, instead of spending the rest of the
// phase budget. Remote candidates reach the checklist asynchronously and a
// pair Pion gave up on can still be revived by the peer's own check, so the
// verdict must hold for a full second, and an empty checklist only counts
// after a grace period.
func watchICEExhaustion(ctx context.Context, agent *ice.Agent, fail func(error)) {
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
			if !iceChecksExhausted(stats) || (len(stats) == 0 && now.Sub(start) < time.Second) {
				strikes = 0
				continue
			}
			if strikes++; strikes >= required {
				fail(&Fault{Code: "ice_path_failed", Message: "双方候选已收集完毕且所有候选对均未连通，提前尝试下一条路径"})
				return
			}
		}
	}
}

// A direct-phase transport only ever holds direct UDP pairs and probes the
// real path MTU (RFC 8899). Relay-phase transports stay at QUIC's 1200-byte
// floor: TURN adds its own encapsulation.
func iceQUICConfig(phase string) *quic.Config {
	return &quic.Config{EnableDatagrams: true, KeepAlivePeriod: 15 * time.Second, MaxIdleTimeout: 45 * time.Second, InitialPacketSize: 1200, DisablePathMTUDiscovery: phase != "direct", MaxIncomingStreams: 128}
}
