// SPDX-License-Identifier: LGPL-3.0-only
// UDP strategy adapted from EasyTier, commit 24e0303fc55e4e645a11818954dd5b712fb94510.
// Copyright EasyTier contributors. Go implementation and conservative evidence
// handling added by Hole contributors. See THIRD_PARTY_NOTICES.md.
package nat

import (
	"net/netip"
	"time"
)

const Lifetime = 5 * time.Minute
const Budget = 15 * time.Second
const HardSockets = 84
const BothSockets = 25

type Snapshot struct {
	Status         string `json:"status"`
	Type           string `json:"type"`
	Predictability string `json:"predictability"`
	Samples        int    `json:"samples"`
	ProbedAt       string `json:"probed_at,omitempty"`
	Reason         string `json:"reason,omitempty"`
	Network        string `json:"network,omitempty"`
	Epoch          uint64 `json:"epoch,string"`
}

func Unknown(reason string) Snapshot {
	return Snapshot{Status: "insufficient", Type: "unknown", Predictability: "unknown", Reason: reason}
}
func (s Snapshot) Current() Snapshot {
	if t, err := time.Parse(time.RFC3339Nano, s.ProbedAt); err == nil && time.Since(t) >= Lifetime {
		s.Status = "expired"
	}
	return s
}

type Sample struct {
	Server, Source, Mapped netip.AddrPort
	ChangeIP, ChangePort   bool
}

// Classify never treats missing CHANGE-REQUEST responses as proof of filtering.
// Restricted classifications require successful replies from the alternate
// endpoint as well as explicit filtering evidence supplied by a fixture/server.
func Classify(local netip.AddrPort, samples []Sample, extra *Sample) Snapshot {
	s := Unknown("有效样本不足")
	s.ProbedAt = time.Now().UTC().Format(time.RFC3339Nano)
	servers, mappings, ips := map[netip.AddrPort]bool{}, map[netip.AddrPort]bool{}, map[netip.Addr]bool{}
	var ports []int
	changedIP, changedPort := false, false
	for _, v := range samples {
		if !v.Mapped.IsValid() || !v.Mapped.Addr().Is4() || !v.Source.IsValid() {
			continue
		}
		servers[v.Source] = true
		mappings[v.Mapped] = true
		ips[v.Mapped.Addr()] = true
		ports = append(ports, int(v.Mapped.Port()))
		changedIP = changedIP || (v.ChangeIP && v.Source.Addr() != v.Server.Addr())
		changedPort = changedPort || (v.ChangePort && v.Source.Addr() == v.Server.Addr() && v.Source.Port() != v.Server.Port())
	}
	s.Samples = len(ports)
	if len(servers) < 2 {
		return s
	}
	if len(mappings) == 1 {
		s.Predictability = "stable"
		if changedIP {
			s.Type, s.Status, s.Reason = "full_cone", "complete", ""
			if mappings[local] {
				s.Type = "open"
			}
		} else if changedPort {
			s.Type, s.Status, s.Reason = "restricted_cone", "complete", ""
		}
		return s
	}
	s.Type, s.Status, s.Predictability, s.Reason = "symmetric", "complete", "irregular", ""
	if len(ips) != 1 || len(ports) < 2 || extra == nil || !ips[extra.Mapped.Addr()] {
		return s
	}
	lo, hi := ports[0], ports[0]
	for _, p := range ports {
		lo = min(lo, p)
		hi = max(hi, p)
	}
	if hi-lo > 15 {
		return s
	}
	p := int(extra.Mapped.Port())
	if p > hi && p-hi < 100 {
		s.Predictability = "incremental"
	}
	if p < lo && lo-p < 100 {
		s.Predictability = "decremental"
	}
	return s
}

func predictable(s Snapshot) bool {
	return s.Predictability == "incremental" || s.Predictability == "decremental"
}
func Strategy(a, b Snapshot) string {
	if a.Status == "expired" || b.Status == "expired" {
		return "none"
	}
	as, bs := a.Type == "symmetric", b.Type == "symmetric"
	if as && bs {
		if predictable(a) && predictable(b) {
			return "both_predictable"
		}
		return "none"
	}
	if as || bs {
		if predictable(a) || predictable(b) {
			return "prediction"
		}
		return "collision"
	}
	return "bidirectional"
}

func Predict(base uint16, direction string, count int) []uint16 {
	out := []uint16{}
	for i := 1; i <= count; i++ {
		p := int(base) + i
		if direction == "decremental" {
			p = int(base) - i
		}
		if p > 0 && p <= 65535 {
			out = append(out, uint16(p))
		}
	}
	return out
}
