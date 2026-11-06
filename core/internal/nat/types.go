// SPDX-License-Identifier: GPL-3.0-only
package nat

import (
	"net/netip"

	"hole/core/natprobe"
)

const Lifetime = natprobe.Lifetime

type Snapshot = natprobe.Snapshot
type Sample = natprobe.Sample

func Unknown(reason string) Snapshot { return natprobe.Unknown(reason) }

func Classify(local netip.AddrPort, samples []Sample, extra *Sample) Snapshot {
	return natprobe.Classify(local, samples, extra, "unknown", "")
}
