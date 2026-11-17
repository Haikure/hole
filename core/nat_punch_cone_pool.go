package core

import (
	"context"
	"errors"
	"math/rand/v2"
	"net/netip"
)

const natPunchConePool = "retained-v1"
const maxConePoolAttempts = 3

// Only observed exits of a port-preserving cone are eligible. Classification
// and network epochs remain authoritative; a pool alone is not cone evidence.
func conePoolEligible(s NATSnapshot) bool {
	if natGroup(s) != "cone" || s.NoPAT != "suspected" || s.PortPreservation != "preserved" || len(s.IPEvidence) < 2 {
		return false
	}
	for _, e := range s.IPEvidence {
		if e.Ports != 1 || e.Trend != "stable" && e.Trend != "unknown" {
			return false
		}
	}
	return true
}

func conePoolPair(s *natPunchSession) bool {
	if s.mode != "single" && s.mode != "pool" {
		return false
	}
	return conePoolEligible(s.local) && natGroup(s.remote) == "symmetric" ||
		conePoolEligible(s.remote) && natGroup(s.local) == "symmetric"
}

func conePoolCandidates(model NATSnapshot, original string) []string {
	var ips []string
	seen := map[string]bool{original: true}
	for _, e := range model.IPEvidence {
		ip, err := netip.ParseAddr(e.IP)
		if err == nil && ip.Is4() && !ip.IsUnspecified() && !ip.IsMulticast() && !seen[e.IP] {
			seen[e.IP] = true
			ips = append(ips, e.IP)
		}
	}
	rand.Shuffle(len(ips), func(i, j int) { ips[i], ips[j] = ips[j], ips[i] })
	return ips[:min(len(ips), maxConePoolAttempts)]
}

// The cone chooses once per round. The peer validates the unchanged port and
// observed IP before allocating or calibrating anything for that destination.
func (p *icePeer) selectConePoolIP(ctx context.Context, a *iceAttempt, s *natPunchSession, round int, original *natPunchPlan, candidates []string) error {
	if original == nil || !conePoolPair(s) {
		return errors.New("no retained cone mapping")
	}
	plan := *original
	if natGroup(s.local) == "cone" {
		index := round - 4
		if index >= len(candidates) {
			return errors.New("cone address pool exhausted")
		}
		plan.IP = candidates[index]
		if err := p.sendNATPunch(a, natPunchSignal{Kind: "pool_select", Round: round, Mode: s.mode, Plan: &plan}); err != nil {
			return err
		}
	} else {
		m, err := p.waitNATPunch(ctx, a, "pool_select", round)
		if err != nil {
			return err
		}
		if m.Plan == nil || m.Mode != s.mode {
			return errors.New("missing cone pool selection")
		}
		plan.IP = m.Plan.IP
		if plan != *m.Plan || plan.IP == original.IP || s.poolTried[plan.IP] {
			return errors.New("invalid retained cone plan")
		}
		found := false
		for _, e := range s.remote.IPEvidence {
			found = found || e.IP == plan.IP
		}
		if !found {
			return errors.New("cone pool IP was not observed")
		}
	}
	if s.poolTried == nil {
		s.poolTried = make(map[string]bool)
	}
	s.poolTried[plan.IP] = true
	s.conePoolPlan = &plan
	a.process.record(NATPunchProcessEvent{Kind: "pool_selected", Title: "保留锥形 socket 并切换公网候选 IP", Round: round, Mode: s.mode, Plan: &plan})
	return nil
}
