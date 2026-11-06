// SPDX-License-Identifier: LGPL-3.0-only
// UDP strategy adapted from EasyTier, commit 24e0303fc55e4e645a11818954dd5b712fb94510.
// Copyright EasyTier contributors. Go implementation and conservative evidence
// handling added by Hole contributors. See THIRD_PARTY_NOTICES.md.
package nat

import (
	"net/netip"
	"time"

	"hole/core/natprobe"
)

const Lifetime = natprobe.Lifetime
const Budget = 15 * time.Second
const ProbeTimeout = 10 * time.Second
const HardSockets = 84
const BothSockets = 25

type Snapshot = natprobe.Snapshot
type Sample = natprobe.Sample

func Unknown(reason string) Snapshot { return natprobe.Unknown(reason) }
func Classify(local netip.AddrPort, samples []Sample, extra *Sample) Snapshot {
	return natprobe.Classify(local, samples, extra, "unknown", "")
}

func predictable(s Snapshot) bool {
	return s.Predictability == "incremental" || s.Predictability == "decremental" || s.Predictability == "predictable_incremental" || s.Predictability == "predictable_decremental"
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
		if direction == "decremental" || direction == "predictable_decremental" {
			p = int(base) - i
		}
		if p > 0 && p <= 65535 {
			out = append(out, uint16(p))
		}
	}
	return out
}
