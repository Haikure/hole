package natprobe

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"reflect"
	"testing"
)

func TestPoolTrendsKeepIndependentEvidence(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ports []int
		trend string
		step  int
	}{
		{"increasing", []int{40000, 40001, 40002}, "incremental", 1},
		{"decreasing", []int{40004, 40002, 40000}, "decremental", 2},
		{"too few", []int{40000, 40001}, "unknown", 0},
		{"irregular", []int{40000, 40002, 40001}, "irregular", 0},
		{"large span", []int{40000, 41000, 42000}, "irregular", 0},
		{"port wrap", []int{65534, 65535, 1}, "irregular", 0},
		{"unequal gaps", []int{40000, 40003, 40005}, "incremental", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var samples []Sample
			for i, port := range tc.ports {
				s := mapping(fmt.Sprintf("192.0.2.%d:3478", i+1), fmt.Sprintf("198.51.100.2:%d", port))
				samples = append(samples, s, s, mapping("192.0.2.8:3478", "198.51.100.1:50000"))
			}
			// A fresh socket on an unrelated IP cannot invalidate a local trend.
			extra := mapping("192.0.2.1:3478", "198.51.100.3:20000")
			got := Classify(netip.AddrPort{}, samples, &extra, "unknown", "")
			want := tc.trend
			if want == "unknown" || want == "irregular" {
				want = "unverified" // The singleton still permits a trial.
			}
			if got.Type != "symmetric" || got.Predictability != want || got.Samples != len(samples) || len(got.IPEvidence) != 2 {
				t.Fatalf("classification: %+v", got)
			}
			e := got.IPEvidence[1]
			if e.Trend != tc.trend || e.Step != tc.step || e.Targets != len(tc.ports) || e.Ports != len(tc.ports) {
				t.Fatalf("IP evidence: %+v", e)
			}
			if got.PortStep != 0 || got.PublicIP != "" || got.AllocationScope != "" {
				t.Fatalf("pool promoted to a global allocation model: %+v", got)
			}
			copy := got.Current()
			copy.IPEvidence[0].Trend = "modified"
			if got.IPEvidence[0].Trend == "modified" {
				t.Fatal("snapshot aliases mutable evidence")
			}
		})
	}
}

func TestPoolUnstableTargetIsNotSparse(t *testing.T) {
	samples := []Sample{
		mapping("192.0.2.1:3478", "198.51.100.1:40000"),
		mapping("192.0.2.1:3478", "198.51.100.1:40001"),
		mapping("192.0.2.1:3478", "198.51.100.1:40002"),
		mapping("192.0.2.2:3478", "198.51.100.2:50000"),
	}
	got := Classify(netip.AddrPort{}, samples, nil, "unknown", "")
	if got.Type != "address_pool" || got.IPEvidence[0].Trend != "irregular" || got.IPEvidence[0].Targets != 1 {
		t.Fatal(got)
	}
}

func TestProbePoolPreservesOppositeTrends(t *testing.T) {
	f := newFixture("endpoint_independent")
	f.other = f.primary
	f.mappedPorts = map[uint16]map[netip.AddrPort]uint16{10000: {}}
	f.mappedIPs = make(map[netip.AddrPort]netip.Addr)
	var servers []*net.UDPAddr
	for i := 0; i < 6; i++ {
		server := netip.MustParseAddrPort(fmt.Sprintf("192.0.2.%d:3478", i+1))
		servers = append(servers, net.UDPAddrFromAddrPort(server))
		port, ip := 40000+i/2, "198.51.100.1"
		if i%2 == 1 {
			port, ip = 50000-i/2, "198.51.100.2"
		}
		f.mappedPorts[10000][server] = uint16(port)
		f.mappedIPs[server] = netip.MustParseAddr(ip)
	}
	_, got, addresses := ProbeWithListener(context.Background(), servers, f.listen)
	want := []IPEvidence{{IP: "198.51.100.1", Targets: 3, Ports: 3, Trend: "incremental", Step: 1}, {IP: "198.51.100.2", Targets: 3, Ports: 3, Trend: "decremental", Step: 1}}
	if got.Type != "symmetric" || got.Predictability != "incremental" || got.Samples != 18 || len(addresses) != 6 || !reflect.DeepEqual(got.IPEvidence, want) {
		t.Fatalf("probe: %+v, addresses: %v", got, addresses)
	}
	for _, conn := range f.connections {
		if !conn.closed {
			t.Fatal("probe leaked socket")
		}
	}
}
