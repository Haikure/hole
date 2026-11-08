// SPDX-License-Identifier: GPL-3.0-only
package natprobe

import (
	"net/netip"
	"sort"
)

// MinimumPortStep ignores reused ports. Sorting makes this the smallest
// nonzero difference between ANY two observed ports, rather than their mean.
func MinimumPortStep(ports []int) int {
	ordered := append([]int(nil), ports...)
	sort.Ints(ordered)
	step := 0
	for i := 1; i < len(ordered); i++ {
		delta := ordered[i] - ordered[i-1]
		if delta > 0 && (step == 0 || delta < step) {
			step = delta
		}
	}
	return step
}

func allocationModel(result *Snapshot, samples []Sample, extra *Sample) {
	var ports []int
	var ip netip.Addr
	targets := make(map[netip.AddrPort]int)
	ips := make(map[netip.Addr]bool)
	for _, sample := range samples {
		if !mappingSample(sample) {
			continue
		}
		if ip.IsValid() && sample.Mapped.Addr() != ip {
			return
		}
		ip = sample.Mapped.Addr()
		port := int(sample.Mapped.Port())
		if previous, ok := targets[sample.Server]; ok {
			if previous != port {
				return
			}
			continue
		}
		targets[sample.Server] = port
		ips[sample.Server.Addr()] = true
		// ADM may reuse the mapping for another port on the same server IP.
		if len(ports) == 0 || ports[len(ports)-1] != port {
			ports = append(ports, port)
		}
	}
	if len(ports) < 3 || len(ips) < 2 {
		return
	}
	step := MinimumPortStep(ports)
	if step == 0 || ports[len(ports)-1]-ports[0] > maxPortSpan {
		return
	}
	for i := 1; i < len(ports); i++ {
		if ports[i] <= ports[i-1] {
			return
		}
	}
	result.PortStep, result.PublicIP = step, ip.String()
	result.LastMappingPort = ports[len(ports)-1]
	result.AllocationScope = "same_socket"
	// A fresh mapping may reveal a smaller step than the fitting samples.
	if extra != nil && extra.Mapped.IsValid() && extra.Mapped.Addr() == ip && int(extra.Mapped.Port()) > result.LastMappingPort {
		step = MinimumPortStep(append(ports, int(extra.Mapped.Port())))
		result.PortStep = step
		if advanceModel(result, *extra) {
			result.AllocationScope = "cross_socket"
		}
	}
	// All observed allocations must remain on this step's residue class.
	for _, port := range ports {
		if (port-ports[0])%step != 0 {
			result.AllocationScope = "unknown"
			return
		}
	}
}

func advanceModel(model *Snapshot, sample Sample) bool {
	if !sample.Mapped.IsValid() || sample.Mapped.Addr().String() != model.PublicIP || model.PortStep <= 0 {
		return false
	}
	delta := int(sample.Mapped.Port()) - model.LastMappingPort
	if delta <= 0 || delta > maxPortSpan || delta%model.PortStep != 0 {
		return false
	}
	model.LastMappingPort = int(sample.Mapped.Port())
	return true
}
