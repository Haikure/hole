// SPDX-License-Identifier: GPL-3.0-only
package natprobe

import (
	"context"
	"net"
	"net/netip"
	"testing"
)

func TestMinimumPortStep(t *testing.T) {
	for _, tc := range []struct {
		ports []int
		want  int
	}{
		{nil, 0}, {[]int{40000, 40000}, 0}, {[]int{40000, 40006, 40002, 40002}, 2},
		{[]int{40000, 40003, 40005}, 2}, {[]int{40002, 40001, 40000}, 1},
	} {
		if got := MinimumPortStep(tc.ports); got != tc.want {
			t.Fatalf("%v: got %d want %d", tc.ports, got, tc.want)
		}
	}
}

func TestAllocationModelRejectsUnstableSequences(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ports []int
		extra int
		scope string
		step  int
	}{
		{"step two", []int{40000, 40002, 40004}, 40006, "cross_socket", 2},
		{"background allocations", []int{40000, 40006, 40008}, 40012, "cross_socket", 2},
		{"minimum from fresh socket", []int{40000, 40004, 40008}, 40009, "cross_socket", 1},
		{"random fresh socket", []int{40000, 40002, 40004}, 39000, "same_socket", 2},
		{"no cross evidence", []int{40000, 40002, 40004}, 0, "same_socket", 2},
		{"inconsistent residue", []int{40000, 40003, 40005}, 40009, "unknown", 2},
		{"reversal", []int{40000, 40004, 40002}, 40006, "", 0},
		{"wrap", []int{65530, 65534, 1024}, 1026, "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var samples []Sample
			for i, p := range tc.ports {
				s := Sample{Server: netip.AddrPortFrom(netip.AddrFrom4([4]byte{192, 0, 2, byte(i + 1)}), 3478), Mapped: netip.AddrPortFrom(netip.MustParseAddr("198.51.100.1"), uint16(p))}
				s.Source = s.Server
				samples = append(samples, s, s)
			}
			var extra *Sample
			if tc.extra > 0 {
				extra = &Sample{Mapped: netip.AddrPortFrom(netip.MustParseAddr("198.51.100.1"), uint16(tc.extra))}
			}
			var model Snapshot
			allocationModel(&model, samples, extra)
			if model.PortStep != tc.step || model.AllocationScope != tc.scope {
				t.Fatalf("%+v", model)
			}
		})
	}
}

func TestProbeValidatesCrossSocketOnHeldOutMappings(t *testing.T) {
	for _, random := range []bool{false, true} {
		f := newFixture("endpoint_independent")
		f.other = f.primary
		f.mappedPorts = make(map[uint16]map[netip.AddrPort]uint16)
		servers := []*net.UDPAddr{net.UDPAddrFromAddrPort(f.primary), net.UDPAddrFromAddrPort(netip.MustParseAddrPort("192.0.2.2:3478")), net.UDPAddrFromAddrPort(netip.MustParseAddrPort("192.0.2.3:3478"))}
		f.mappedPorts[10000] = map[netip.AddrPort]uint16{}
		for i, s := range servers {
			f.mappedPorts[10000][addrPort(s)] = uint16(40000 + i*2)
		}
		f.mappedPorts[10001] = map[netip.AddrPort]uint16{f.primary: 40006}
		for i := 0; i < 3; i++ {
			p := uint16(40008 + i*2)
			if random {
				p = 39000
			}
			f.mappedPorts[uint16(10002+i)] = map[netip.AddrPort]uint16{addrPort(servers[(i+1)%3]): p}
		}
		_, model, _ := ProbeWithListener(context.Background(), servers, f.listen)
		want := "cross_socket"
		if random {
			want = "same_socket"
		}
		if model.PortStep != 2 || model.AllocationScope != want {
			t.Fatalf("random=%v: %+v", random, model)
		}
		for _, c := range f.connections {
			if !c.closed {
				t.Fatal("probe leaked socket")
			}
		}
	}
}
