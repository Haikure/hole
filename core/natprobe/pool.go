package natprobe

import (
	"net/netip"
	"sort"
)

// IPEvidence describes the original socket only. It never asserts that a
// subsequent destination or socket will use this public IP or allocator.
type IPEvidence struct {
	IP      string `json:"ip"`
	Targets int    `json:"targets"`
	Ports   int    `json:"ports"`
	Trend   string `json:"trend"`
	Step    int    `json:"step,omitempty"`
}

func poolTrendRank(trend string) int {
	switch trend {
	case "incremental", "decremental":
		return 2
	case "unknown":
		return 1
	}
	return 0
}

func poolEvidence(groups map[netip.Addr][]Sample) []IPEvidence {
	ips := make([]netip.Addr, 0, len(groups))
	for ip := range groups {
		ips = append(ips, ip)
	}
	sort.Slice(ips, func(i, j int) bool { return ips[i].Less(ips[j]) })
	// The normal probe has at most eight destinations and three responses per
	// destination. Bound exported evidence even for callers with longer input.
	if len(ips) > 24 {
		ips = ips[:24]
	}
	result := make([]IPEvidence, 0, len(ips))
	for _, ip := range ips {
		e := IPEvidence{IP: ip.String(), Trend: "unknown"}
		targets := make(map[netip.AddrPort]int)
		unique := make(map[int]bool)
		var ports []int
		unstable := false
		for _, sample := range groups[ip] {
			port := int(sample.Mapped.Port())
			unique[port] = true
			if previous, ok := targets[sample.Server]; ok {
				unstable = unstable || previous != port
				continue
			}
			targets[sample.Server] = port
			ports = append(ports, port)
		}
		e.Targets, e.Ports = len(targets), len(unique)
		switch {
		case unstable:
			e.Trend = "irregular"
		case len(unique) == 1 && len(targets) >= 2:
			e.Trend = "stable"
		case len(unique) >= 3:
			increasing, decreasing := true, true
			lo, hi := ports[0], ports[0]
			for i, port := range ports {
				lo, hi = min(lo, port), max(hi, port)
				if i > 0 {
					increasing = increasing && port >= ports[i-1]
					decreasing = decreasing && port <= ports[i-1]
				}
			}
			e.Trend = "irregular"
			if hi-lo <= maxPortSpan && (increasing || decreasing) {
				e.Trend = "incremental"
				if decreasing {
					e.Trend = "decremental"
				}
				step := MinimumPortStep(ports)
				consistent := true
				for _, port := range ports {
					consistent = consistent && (port-lo)%step == 0
				}
				if consistent {
					e.Step = step
				}
			}
		}
		result = append(result, e)
	}
	return result
}
