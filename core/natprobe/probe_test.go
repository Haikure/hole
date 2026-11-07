// SPDX-License-Identifier: GPL-3.0-only
package natprobe

import (
	"context"
	"errors"
	"github.com/pion/stun/v4"
	"net"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"
)

func mapping(server, mapped string) Sample {
	endpoint := netip.MustParseAddrPort(server)
	return Sample{Server: endpoint, Source: endpoint, Mapped: netip.MustParseAddrPort(mapped)}
}

func TestClassifyMappingEvidence(t *testing.T) {
	a := mapping("192.0.2.1:3478", "198.51.100.1:40000")
	b := mapping("192.0.2.2:3478", "198.51.100.1:40000")
	changed := a
	changed.Change = 6
	changed.Source = netip.MustParseAddrPort("192.0.2.3:3479")
	foreign := a
	foreign.Source = changed.Source
	extra := mapping(a.Server.String(), "198.51.100.1:41000")
	for _, tc := range []struct {
		name                                   string
		samples                                []Sample
		extra                                  *Sample
		status, kind, behavior, predictability string
	}{
		{"stable", []Sample{a, a, b, b}, nil, "complete", "endpoint_independent", "endpoint_independent", "stable"},
		{"unstable target", []Sample{a, mapping(a.Server.String(), "198.51.100.1:40001"), b}, nil, "insufficient", "unknown", "unknown", "unknown"},
		{"changed source is not a second destination", []Sample{a, changed}, nil, "insufficient", "unknown", "unknown", "unknown"},
		{"unexpected ordinary source", []Sample{a, foreign}, nil, "insufficient", "unknown", "unknown", "unknown"},
		{"only one destination IP", []Sample{a, mapping("192.0.2.1:3479", a.Mapped.String())}, nil, "insufficient", "unknown", "unknown", "unknown"},
		{"multiple public IPs without enough targets per IP", []Sample{a, mapping(b.Server.String(), "198.51.100.2:40000")}, &extra, "insufficient", "unknown", "unknown", "stable"},
		{"fresh socket does not affect base mapping", []Sample{a, b}, &extra, "complete", "endpoint_independent", "endpoint_independent", "stable"},
		{"symmetric without fresh socket", []Sample{a, mapping(b.Server.String(), "198.51.100.1:45000")}, nil, "complete", "symmetric", "endpoint_dependent", "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(netip.AddrPort{}, tc.samples, tc.extra, "unknown", "filter unavailable")
			if got.Status != tc.status || got.Type != tc.kind || got.MappingBehavior != tc.behavior || got.Predictability != tc.predictability {
				t.Fatalf("classification = %+v", got)
			}
		})
	}
	got := Classify(netip.AddrPort{}, []Sample{a, changed}, nil, "endpoint_independent", "")
	if got.Status != "insufficient" || got.Type != "unknown" || got.FilteringBehavior != "endpoint_independent" || got.Samples != 1 {
		t.Fatalf("single-target filtering evidence = %+v", got)
	}
}

func TestClassifyAddressPool(t *testing.T) {
	a := mapping("192.0.2.1:3478", "198.51.100.1:40000")
	b := mapping("192.0.2.2:3478", a.Mapped.String())
	c := mapping("192.0.2.3:3478", "198.51.100.2:40000")
	d := mapping("192.0.2.4:3478", c.Mapped.String())
	changed := b
	changed.Change = 6
	foreign := b
	foreign.Source = c.Server
	extra := mapping(a.Server.String(), "198.51.100.3:40003")
	for _, tc := range []struct {
		name                               string
		samples                            []Sample
		extra                              *Sample
		kind, predictability, preservation string
		noPAT                              string
		validSamples                       int
	}{
		{"one classifiable IP", []Sample{a, b, c}, nil, "cone", "stable", "preserved", "unknown", 3},
		{"two classifiable IPs", []Sample{a, b, c, d}, nil, "cone", "stable", "preserved", "unknown", 4},
		{"fresh socket does not change type", []Sample{a, b, c}, &extra, "cone", "stable", "preserved", "unknown", 3},
		{"different stable ports per IP", []Sample{a, b, mapping(c.Server.String(), "198.51.100.2:45000")}, nil, "cone", "unknown", "mixed", "not_detected", 3},
		{"different stable ports with fresh socket", []Sample{a, b, mapping(c.Server.String(), "198.51.100.2:45000")}, &extra, "cone", "irregular", "mixed", "not_detected", 3},
		{"same target rotates IP", []Sample{a, mapping(a.Server.String(), c.Mapped.String()), b, c}, nil, "cone", "stable", "preserved", "unknown", 4},
		{"unstable group does not hide valid group", []Sample{a, mapping(a.Server.String(), "198.51.100.1:40001"), b, c, d}, nil, "cone", "unknown", "mixed", "not_detected", 5},
		{"symmetric group takes priority", []Sample{a, b, c, mapping(d.Server.String(), "198.51.100.2:40001")}, nil, "symmetric", "unknown", "mixed", "not_detected", 4},
		{"symmetric group and singleton", []Sample{a, mapping(b.Server.String(), "198.51.100.1:40001"), c}, &extra, "symmetric", "irregular", "mixed", "not_detected", 3},
		{"same target IP different ports can be symmetric", []Sample{a, mapping("192.0.2.1:3479", "198.51.100.1:40001"), c}, nil, "symmetric", "unknown", "mixed", "not_detected", 3},
		{"singletons cannot establish a cone", []Sample{a, c}, nil, "unknown", "stable", "preserved", "unknown", 2},
		{"singletons cannot establish symmetric NAT", []Sample{a, mapping(c.Server.String(), "198.51.100.2:45000")}, nil, "unknown", "unknown", "mixed", "not_detected", 2},
		{"repeated responses do not add targets", []Sample{a, a, c, c}, nil, "unknown", "stable", "preserved", "unknown", 4},
		{"only one target IP per group", []Sample{a, mapping("192.0.2.1:3479", a.Mapped.String()), c}, nil, "unknown", "stable", "preserved", "unknown", 3},
		{"unstable group and singleton", []Sample{a, mapping(a.Server.String(), "198.51.100.1:40001"), b, c}, nil, "unknown", "unknown", "mixed", "not_detected", 4},
		{"changed response excluded", []Sample{a, changed, c}, nil, "unknown", "stable", "preserved", "unknown", 2},
		{"foreign response excluded", []Sample{a, foreign, c}, nil, "unknown", "stable", "preserved", "unknown", 2},
		{"invalid mapping excluded", []Sample{a, mapping(b.Server.String(), "198.51.100.1:0"), c}, nil, "unknown", "stable", "preserved", "unknown", 2},
	} {
		for _, filter := range []struct{ behavior, cone string }{
			{"endpoint_independent", "full_cone"},
			{"address_dependent", "restricted_cone"},
			{"address_port_dependent", "port_restricted_cone"},
			{"unknown", "endpoint_independent"},
		} {
			t.Run(tc.name+"/"+filter.behavior, func(t *testing.T) {
				kind, behavior, status := tc.kind, "endpoint_independent", "complete"
				switch kind {
				case "cone":
					kind = filter.cone
				case "symmetric":
					behavior = "endpoint_dependent"
				case "unknown":
					behavior, status = "unknown", "insufficient"
				}
				// A pool must never report the whole path as open or No-PAT,
				// even when the local endpoint happens to match one pool entry.
				for _, local := range []netip.AddrPort{netip.MustParseAddrPort("10.0.0.1:40000"), a.Mapped} {
					samples := append([]Sample(nil), tc.samples...)
					var reason string
					for order := 0; order < 2; order++ {
						got := Classify(local, samples, tc.extra, filter.behavior, "filter detail")
						if got.Type != kind || got.MappingBehavior != behavior || got.Status != status || got.FilteringBehavior != filter.behavior {
							t.Fatalf("classification = %+v, want %s / %s / %s", got, kind, behavior, status)
						}
						if got.Predictability != tc.predictability || got.PortPreservation != tc.preservation || got.NoPAT != tc.noPAT || got.Samples != tc.validSamples {
							t.Fatalf("aggregate observations changed: %+v", got)
						}
						if !strings.Contains(got.Reason, "运营商地址池") || !strings.Contains(got.Reason, "filter detail") {
							t.Fatalf("missing pool or filtering explanation: %+v", got)
						}
						if order == 1 && got.Reason != reason {
							t.Fatalf("classification depends on sample order: %q vs %q", got.Reason, reason)
						}
						reason = got.Reason
						for i, j := 0, len(samples)-1; i < j; i, j = i+1, j-1 {
							samples[i], samples[j] = samples[j], samples[i]
						}
					}
				}
			})
		}
	}
}

func TestPortTrendUsesStrictAndLimitedEvidence(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ports []uint16
		extra uint16
		want  string
	}{
		{"strict increasing needs fresh socket", []uint16{40000, 40001, 40002}, 0, "unknown"},
		{"strict decreasing needs fresh socket", []uint16{40002, 40001, 40000}, 0, "unknown"},
		{"increasing gaps", []uint16{40000, 40004, 40009}, 0, "unknown"},
		{"decreasing gaps", []uint16{40009, 40004, 40000}, 0, "unknown"},
		{"increasing gaps with extra", []uint16{40000, 40004, 40009}, 40010, "incremental"},
		{"decreasing gaps with extra", []uint16{40009, 40004, 40000}, 39999, "decremental"},
		{"constant step greater than one", []uint16{40000, 40002, 40004}, 40006, "incremental"},
		{"gap after consecutive run", []uint16{40000, 40001, 40002, 40004}, 40005, "incremental"},
		{"fresh socket cannot supply third increasing sample", []uint16{40000, 40001}, 40002, "incremental"},
		{"fresh socket cannot supply third decreasing sample", []uint16{40002, 40001}, 40000, "decremental"},
		{"fresh socket confirms increasing", []uint16{40000, 40001, 40002}, 40003, "predictable_incremental"},
		{"fresh socket confirms decreasing", []uint16{40002, 40001, 40000}, 39999, "predictable_decremental"},
		{"fresh socket skips increasing port", []uint16{40000, 40001, 40002}, 40004, "incremental"},
		{"fresh socket skips decreasing port", []uint16{40002, 40001, 40000}, 39998, "decremental"},
		{"fresh socket contradicts", []uint16{40000, 40001, 40002}, 39999, "irregular"},
		{"fresh socket contradicts decreasing", []uint16{40002, 40001, 40000}, 40003, "irregular"},
		{"fresh socket repeats last port", []uint16{40000, 40001, 40002}, 40002, "irregular"},
		{"fresh socket repeats decreasing port", []uint16{40002, 40001, 40000}, 40000, "irregular"},
		{"fresh socket resets to first port", []uint16{40000, 40001, 40002}, 40000, "irregular"},
		{"two samples", []uint16{40000, 40001}, 0, "unknown"},
		{"equal adjacent ports", []uint16{40000, 40000, 40004}, 0, "unknown"},
		{"equal adjacent ports with extra", []uint16{40000, 40000, 40004}, 40009, "incremental"},
		{"wide span", []uint16{40000, 42000, 43000}, 43001, "irregular"},
		{"fresh socket exceeds span", []uint16{40000, 40001, 40002}, 42000, "irregular"},
		{"reversal without extra", []uint16{40000, 40009, 40004}, 0, "unknown"},
		{"limited fluctuation", []uint16{40000, 40003, 40002, 40005}, 40010, "incremental"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var samples []Sample
			for i, port := range tc.ports {
				endpoint := netip.AddrPortFrom(netip.AddrFrom4([4]byte{192, 0, 2, byte(i + 1)}), 3478)
				// Repeated responses from one destination must not increase the
				// number of ports available to establish a strict trend.
				for round := 0; round < probeRounds; round++ {
					samples = append(samples, Sample{Server: endpoint, Source: endpoint, Mapped: netip.AddrPortFrom(netip.MustParseAddr("198.51.100.1"), port)})
				}
			}
			var extra *Sample
			if tc.extra != 0 {
				extra = &Sample{Mapped: netip.AddrPortFrom(netip.MustParseAddr("198.51.100.1"), tc.extra)}
			}
			got := Classify(netip.AddrPort{}, samples, extra, "unknown", "")
			if got.Predictability != tc.want {
				t.Fatalf("classification = %+v, want %s", got, tc.want)
			}
			if !strings.Contains(got.Reason, "socket") {
				t.Fatalf("classification does not explain cross-socket evidence: %+v", got)
			}
		})
	}
}

func TestPortTrendRequiresValidFreshSocketMapping(t *testing.T) {
	samples := []Sample{
		mapping("192.0.2.1:3478", "198.51.100.1:40000"),
		mapping("192.0.2.2:3478", "198.51.100.1:40001"),
		mapping("192.0.2.3:3478", "198.51.100.1:40002"),
	}
	for _, tc := range []struct {
		name, mapped, want string
	}{
		{"invalid mapping", "", "unknown"},
		{"zero port", "198.51.100.1:0", "unknown"},
		{"IPv6 mapping", "[2001:db8::1]:40003", "unknown"},
		{"different public IP", "198.51.100.2:40003", "irregular"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			extra := &Sample{}
			if tc.mapped != "" {
				extra.Mapped = netip.MustParseAddrPort(tc.mapped)
			}
			got := Classify(netip.AddrPort{}, samples, extra, "unknown", "filter unavailable")
			if got.Type != "symmetric" || got.Samples != len(samples) || got.Predictability != tc.want {
				t.Fatalf("classification = %+v, want predictability %s", got, tc.want)
			}
			if !strings.Contains(got.Reason, "filter unavailable") {
				t.Fatalf("classification lost filtering explanation: %+v", got)
			}
		})
	}
}

func TestClassifyPortPreservationAndNoPAT(t *testing.T) {
	local := netip.MustParseAddrPort("10.0.0.1:40000")
	a := mapping("192.0.2.1:3478", "198.51.100.1:40000")
	b := mapping("192.0.2.2:3478", a.Mapped.String())
	rewritten := mapping(b.Server.String(), "198.51.100.1:45000")
	otherIP := mapping(b.Server.String(), "198.51.100.2:40000")
	changed := rewritten
	changed.Change = 6
	foreign := rewritten
	foreign.Source = a.Server
	extra := mapping(a.Server.String(), rewritten.Mapped.String())
	for _, tc := range []struct {
		name                string
		local               netip.AddrPort
		samples             []Sample
		extra               *Sample
		preservation, noPAT string
	}{
		{"IP only translation", local, []Sample{a, a, b, b}, nil, "preserved", "suspected"},
		{"stable rewritten port", netip.MustParseAddrPort("10.0.0.1:12345"), []Sample{a, b}, nil, "translated", "not_detected"},
		{"mixed preservation", local, []Sample{a, rewritten}, nil, "mixed", "not_detected"},
		{"no address translation", a.Mapped, []Sample{a, b}, nil, "preserved", "not_applicable"},
		{"multiple mapped IPs preserve port", local, []Sample{a, otherIP}, nil, "preserved", "unknown"},
		{"multiple mapped IPs rewrite port", netip.MustParseAddrPort("10.0.0.1:12345"), []Sample{a, otherIP}, nil, "translated", "not_detected"},
		{"single target preserves port", local, []Sample{a, a}, nil, "preserved", "unknown"},
		{"single target rewrites port", netip.MustParseAddrPort("10.0.0.1:12345"), []Sample{a}, nil, "translated", "not_detected"},
		{"only one target IP", local, []Sample{a, mapping("192.0.2.1:3479", a.Mapped.String())}, nil, "preserved", "unknown"},
		{"unstable mapped IP", local, []Sample{a, mapping(a.Server.String(), otherIP.Mapped.String()), b}, nil, "preserved", "unknown"},
		{"unstable mapped port", local, []Sample{a, extra, b}, nil, "mixed", "not_detected"},
		{"unknown local endpoint", netip.AddrPort{}, []Sample{a, b}, nil, "unknown", "unknown"},
		{"zero local port", netip.MustParseAddrPort("10.0.0.1:0"), []Sample{a, b}, nil, "unknown", "unknown"},
		{"unspecified local IP", netip.MustParseAddrPort("0.0.0.0:40000"), []Sample{a, b}, nil, "unknown", "unknown"},
		{"no samples", local, nil, nil, "unknown", "unknown"},
		{"change responses excluded", local, []Sample{a, b, changed}, nil, "preserved", "suspected"},
		{"unexpected source excluded", local, []Sample{a, b, foreign}, nil, "preserved", "suspected"},
		{"invalid mapped port excluded", local, []Sample{a, b, mapping("192.0.2.3:3478", "198.51.100.1:0")}, nil, "preserved", "suspected"},
		{"fresh socket excluded", local, []Sample{a, b}, &extra, "preserved", "suspected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(tc.local, tc.samples, tc.extra, "unknown", "filter unavailable")
			if got.PortPreservation != tc.preservation || got.NoPAT != tc.noPAT {
				t.Fatalf("classification = %+v, want port preservation %q, No-PAT %q", got, tc.preservation, tc.noPAT)
			}
		})
	}
}

func TestNoPATPreservesFiltering(t *testing.T) {
	local := netip.MustParseAddrPort("10.0.0.1:40000")
	samples := []Sample{mapping("192.0.2.1:3478", "198.51.100.1:40000"), mapping("192.0.2.2:3478", "198.51.100.1:40000")}
	for _, tc := range []struct{ filtering, kind string }{
		{"unknown", "endpoint_independent"},
		{"endpoint_independent", "full_cone"},
		{"address_dependent", "restricted_cone"},
		{"address_port_dependent", "port_restricted_cone"},
	} {
		t.Run(tc.filtering, func(t *testing.T) {
			got := Classify(local, samples, nil, tc.filtering, "")
			if got.NoPAT != "suspected" || got.Type != tc.kind || got.FilteringBehavior != tc.filtering || got.Predictability != "stable" {
				t.Fatalf("No-PAT changed mapping or filtering classification: %+v", got)
			}
		})
	}
}

func TestClassifyPreservesFiltering(t *testing.T) {
	local := netip.MustParseAddrPort("198.51.100.1:40000")
	samples := []Sample{mapping("192.0.2.1:3478", local.String()), mapping("192.0.2.2:3478", local.String())}
	for _, tc := range []struct {
		translated      bool
		filtering, kind string
	}{
		{false, "unknown", "endpoint_independent"},
		{false, "endpoint_independent", "open"},
		{false, "address_dependent", "firewall"},
		{false, "address_port_dependent", "firewall"},
		{true, "unknown", "endpoint_independent"},
		{true, "endpoint_independent", "full_cone"},
		{true, "address_dependent", "restricted_cone"},
		{true, "address_port_dependent", "port_restricted_cone"},
	} {
		t.Run(tc.kind+"/"+tc.filtering, func(t *testing.T) {
			endpoint := local
			if tc.translated {
				endpoint = netip.MustParseAddrPort("10.0.0.1:12345")
			}
			got := Classify(endpoint, samples, nil, tc.filtering, "unavailable")
			if got.Type != tc.kind || got.FilteringBehavior != tc.filtering {
				t.Fatalf("classification = %+v", got)
			}
			preservation, noPAT := "preserved", "not_applicable"
			if tc.translated {
				preservation, noPAT = "translated", "not_detected"
			}
			if got.PortPreservation != preservation || got.NoPAT != noPAT {
				t.Fatalf("port translation classification = %+v", got)
			}
		})
	}
}

// The fixture tracks NAT permissions per socket. A timeout represents a dropped
// datagram; returning it immediately keeps loss and filtering tests deterministic.
type stunFixture struct {
	primary, other                 netip.AddrPort
	filtering                      string
	drops                          map[byte]int
	ignoreChange, unreachableOther bool
	preservePort                   bool
	mappedPorts                    map[uint16]map[netip.AddrPort]uint16
	mappedIPs                      map[netip.AddrPort]netip.Addr
	connections                    []*fixtureConn
}

type fixtureConn struct {
	fixture     *stunFixture
	allowed     map[netip.AddrPort]bool
	requests    map[byte]int
	transaction map[byte][12]byte
	response    []byte
	source      netip.AddrPort
	closed      bool
	port        uint16
}

func newFixture(filtering string) *stunFixture {
	return &stunFixture{
		primary:   netip.MustParseAddrPort("192.0.2.1:3478"),
		other:     netip.MustParseAddrPort("192.0.2.2:3479"),
		filtering: filtering,
		drops:     make(map[byte]int),
	}
}

func (f *stunFixture) listen(_, _ string) (net.PacketConn, error) {
	c := &fixtureConn{fixture: f, allowed: make(map[netip.AddrPort]bool), requests: make(map[byte]int), transaction: make(map[byte][12]byte), port: uint16(10000 + len(f.connections))}
	f.connections = append(f.connections, c)
	return c, nil
}

func (c *fixtureConn) WriteTo(raw []byte, destination net.Addr) (int, error) {
	f := c.fixture
	request := &stun.Message{Raw: append([]byte(nil), raw...)}
	if err := request.Decode(); err != nil {
		return 0, err
	}
	to := addrPort(destination)
	c.allowed[to] = true
	change := byte(0)
	if attr, err := request.Get(stun.AttrChangeRequest); err == nil {
		change = attr[3]
	}
	c.requests[change]++
	if previous, ok := c.transaction[change]; ok && change != 0 && previous != request.TransactionID {
		return 0, errors.New("retransmission changed transaction ID")
	}
	c.transaction[change] = request.TransactionID
	c.source = to
	if !f.ignoreChange {
		if change == 6 {
			c.source = f.other
		} else if change == 2 {
			c.source = netip.AddrPortFrom(to.Addr(), f.other.Port())
		}
	}
	c.response = nil
	if f.drops[change] > 0 {
		f.drops[change]--
		return len(raw), nil
	}
	if f.unreachableOther && c.source.Addr() == f.other.Addr() {
		return len(raw), nil
	}
	allowed := f.filtering == "endpoint_independent" || c.allowed[c.source]
	if f.filtering == "address_dependent" {
		for endpoint := range c.allowed {
			allowed = allowed || endpoint.Addr() == c.source.Addr()
		}
	}
	if !allowed {
		return len(raw), nil
	}
	other := f.other
	if to.Addr() == f.other.Addr() {
		other = f.primary
	}
	mappedPort := int(c.port) + 30000
	if f.preservePort {
		mappedPort = int(c.port)
	}
	if f.mappedPorts != nil {
		mappedPort = int(f.mappedPorts[c.port][to])
		if mappedPort == 0 {
			return len(raw), nil
		}
	}
	mappedIP := netip.MustParseAddr("198.51.100.1")
	if ip, ok := f.mappedIPs[to]; ok {
		mappedIP = ip
	}
	response := stun.MustBuild(stun.NewTransactionIDSetter(request.TransactionID), stun.BindingSuccess,
		&stun.XORMappedAddress{IP: net.IP(mappedIP.AsSlice()), Port: mappedPort},
		&stun.OtherAddress{IP: net.IP(other.Addr().AsSlice()), Port: int(other.Port())})
	c.response = response.Raw
	return len(raw), nil
}

func (c *fixtureConn) ReadFrom(p []byte) (int, net.Addr, error) {
	if c.response == nil {
		return 0, nil, os.ErrDeadlineExceeded
	}
	n := copy(p, c.response)
	c.response = nil
	return n, net.UDPAddrFromAddrPort(c.source), nil
}

func (c *fixtureConn) Close() error { c.closed = true; return nil }
func (c *fixtureConn) LocalAddr() net.Addr {
	return net.UDPAddrFromAddrPort(netip.AddrPortFrom(netip.MustParseAddr("10.0.0.1"), c.port))
}
func (c *fixtureConn) SetDeadline(time.Time) error      { return nil }
func (c *fixtureConn) SetReadDeadline(time.Time) error  { return nil }
func (c *fixtureConn) SetWriteDeadline(time.Time) error { return nil }

func TestProbeVerifiesTrendAcrossSockets(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		ports                       []uint16
		extra                       uint16
		unreachableFirst, failFresh bool
		want                        string
	}{
		{"increasing", []uint16{40000, 40001, 40002}, 40003, false, false, "predictable_incremental"},
		{"decreasing", []uint16{40002, 40001, 40000}, 39999, false, false, "predictable_decremental"},
		{"same direction with gap", []uint16{40000, 40001, 40002}, 40004, false, false, "incremental"},
		{"resets on fresh socket", []uint16{40000, 40001, 40002}, 40000, false, false, "irregular"},
		{"fresh socket cannot listen", []uint16{40000, 40001, 40002}, 40003, false, true, "unknown"},
		{"fresh socket gets no response", []uint16{40000, 40001, 40002}, 0, false, false, "unknown"},
		{"first configured server unavailable", []uint16{40000, 40001, 40002}, 40003, true, false, "predictable_incremental"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture("endpoint_independent")
			// An OTHER-ADDRESS with the same port disables unrelated filtering
			// probes so this fixture isolates the original and fresh sockets.
			f.other = f.primary
			f.mappedPorts = map[uint16]map[netip.AddrPort]uint16{10000: {}, 10001: {}}
			var servers []*net.UDPAddr
			if tc.unreachableFirst {
				servers = append(servers, net.UDPAddrFromAddrPort(f.primary))
			}
			var firstResponding netip.AddrPort
			for i, port := range tc.ports {
				endpoint := netip.AddrPortFrom(netip.AddrFrom4([4]byte{192, 0, 2, byte(i + 2)}), 3478)
				if i == 0 {
					firstResponding = endpoint
				}
				servers = append(servers, net.UDPAddrFromAddrPort(endpoint))
				f.mappedPorts[10000][endpoint] = port
			}
			f.mappedPorts[10001][firstResponding] = tc.extra
			pc, err := f.listen("udp4", "0.0.0.0:0")
			if err != nil {
				t.Fatal(err)
			}
			defer pc.Close()
			listen := func(network, address string) (net.PacketConn, error) {
				if tc.failFresh {
					return nil, errors.New("fresh socket unavailable")
				}
				return f.listen(network, address)
			}
			_, got, mapped := ProbeSocket(context.Background(), pc, servers, listen, addrPort(pc.LocalAddr()))
			if got.Type != "symmetric" || got.Predictability != tc.want || got.Samples != len(tc.ports)*probeRounds {
				t.Fatalf("probe = %+v, want predictability %s", got, tc.want)
			}
			if len(mapped) != len(tc.ports) {
				t.Fatalf("fresh socket changed original mapping addresses: %v", mapped)
			}
			for i, port := range tc.ports {
				if mapped[i].Port() != port {
					t.Fatalf("mapping %d = %v, want original port %d", i, mapped[i], port)
				}
			}
			wantSockets := 2
			if tc.failFresh {
				wantSockets = 1
			}
			if len(f.connections) != wantSockets || f.connections[0].closed {
				t.Fatalf("unexpected socket count or original socket was closed: %+v", f.connections)
			}
			if !tc.failFresh {
				fresh := f.connections[1]
				if !fresh.closed || len(fresh.allowed) != 1 || !fresh.allowed[firstResponding] {
					t.Fatalf("fresh socket did not verify a responding target and close: %+v", fresh)
				}
			}
		})
	}
}

func TestProbeIsolatesFilteringSocket(t *testing.T) {
	f := newFixture("address_port_dependent")
	servers := []*net.UDPAddr{net.UDPAddrFromAddrPort(f.primary), net.UDPAddrFromAddrPort(f.other)}
	_, got, mapped := ProbeWithListener(context.Background(), servers, f.listen)
	if got.Type != "port_restricted_cone" || got.FilteringBehavior != f.filtering || got.Samples != 2*probeRounds {
		t.Fatalf("probe = %+v", got)
	}
	if len(mapped) != 1 || mapped[0].Port() != 40000 {
		t.Fatalf("filtering socket contaminated mapping output: %v", mapped)
	}
	if len(f.connections) != 2 {
		t.Fatalf("created %d sockets, want mapping + filtering", len(f.connections))
	}
	for _, conn := range f.connections {
		if !conn.closed {
			t.Fatal("probe left a socket open")
		}
	}
}

func TestProbeAddressPool(t *testing.T) {
	for _, tc := range []struct{ filtering, kind string }{
		{"endpoint_independent", "full_cone"},
		{"address_dependent", "restricted_cone"},
		{"address_port_dependent", "port_restricted_cone"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			t.Parallel()
			f := newFixture(tc.filtering)
			third := netip.MustParseAddrPort("192.0.2.3:3478")
			f.mappedIPs = map[netip.AddrPort]netip.Addr{third: netip.MustParseAddr("198.51.100.2")}
			servers := []*net.UDPAddr{net.UDPAddrFromAddrPort(f.primary), net.UDPAddrFromAddrPort(f.other), net.UDPAddrFromAddrPort(third)}
			_, got, mapped := ProbeWithListener(context.Background(), servers, f.listen)
			if got.Type != tc.kind || got.MappingBehavior != "endpoint_independent" || got.FilteringBehavior != tc.filtering || got.Predictability != "stable" {
				t.Fatalf("address pool probe = %+v", got)
			}
			if got.Status != "complete" || got.Samples != 3*probeRounds || got.PortPreservation != "translated" || got.NoPAT != "not_detected" {
				t.Fatalf("address pool observations = %+v", got)
			}
			if len(mapped) != 2 || mapped[0] != netip.MustParseAddrPort("198.51.100.1:40000") || mapped[1] != netip.MustParseAddrPort("198.51.100.2:40000") {
				t.Fatalf("address pool mappings = %v", mapped)
			}
			if got.FilteringServer != f.primary.String() || len(f.connections) != 3 {
				t.Fatalf("unexpected filtering endpoint or socket count: %+v, %d sockets", got, len(f.connections))
			}
			for _, conn := range f.connections {
				if !conn.closed {
					t.Fatal("address pool probe left a socket open")
				}
			}
		})
	}
}

func TestProbePortPreservationAndNoPAT(t *testing.T) {
	f := newFixture("address_port_dependent")
	f.preservePort = true
	servers := []*net.UDPAddr{net.UDPAddrFromAddrPort(f.primary), net.UDPAddrFromAddrPort(f.other)}
	_, got, mapped := ProbeWithListener(context.Background(), servers, f.listen)
	if got.PortPreservation != "preserved" || got.NoPAT != "suspected" || got.Type != "port_restricted_cone" {
		t.Fatalf("probe = %+v", got)
	}
	if got.Samples != 2*probeRounds || len(mapped) != 1 || mapped[0].Port() != 10000 {
		t.Fatalf("non-base socket contaminated No-PAT evidence: %+v, mapped %v", got, mapped)
	}
}

func TestFilteringRetriesAndUnknownEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, filtering, want string
		dropChange            byte
		ignore, unreachable   bool
	}{
		{"full cone", "endpoint_independent", "endpoint_independent", 0, false, false},
		{"lost change both", "endpoint_independent", "endpoint_independent", 6, false, false},
		{"lost change port", "address_dependent", "address_dependent", 2, false, false},
		{"port restricted", "address_port_dependent", "address_port_dependent", 0, false, false},
		{"extension ignored", "endpoint_independent", "unknown", 0, true, false},
		{"alternate path unreachable", "address_port_dependent", "unknown", 0, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(tc.filtering)
			f.ignoreChange, f.unreachableOther = tc.ignore, tc.unreachable
			if tc.dropChange != 0 {
				f.drops[tc.dropChange] = 1
			}
			var last time.Time
			got, _, _, reason := probeFiltering(context.Background(), net.UDPAddrFromAddrPort(f.primary), f.listen, &last)
			if got != tc.want || (got == "unknown" && reason == "") {
				t.Fatalf("filtering = %q (%s), want %q", got, reason, tc.want)
			}
			conn := f.connections[0]
			if tc.dropChange != 0 && conn.requests[tc.dropChange] < 2 {
				t.Fatal("lost change request was not retried")
			}
			if !conn.closed {
				t.Fatal("filtering socket was not closed")
			}
		})
	}
}
