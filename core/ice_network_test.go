package core

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/pion/turn/v5"
)

type iceDNSFixture struct {
	DefaultPlatform
	ips []net.IP
	err error
}

func (p iceDNSFixture) LookupIP(context.Context, string) ([]net.IP, error) {
	return p.ips, p.err
}

func TestICENetworkResolveAddressFamily(t *testing.T) {
	for _, network := range []string{"ip", "udp", "tcp"} {
		for _, tc := range []struct {
			name, suffix, host, want string
			ips                      []string
		}{
			{name: "dual_stack_ipv6_first", ips: []string{"2001:db8::1", "192.0.2.1"}, want: "192.0.2.1"},
			{name: "dual_stack_ipv4_first", ips: []string{"192.0.2.1", "2001:db8::1"}, want: "192.0.2.1"},
			{name: "first_ipv4_preserved", ips: []string{"2001:db8::1", "192.0.2.2", "192.0.2.1"}, want: "192.0.2.2"},
			{name: "ipv4_only", ips: []string{"192.0.2.1"}, want: "192.0.2.1"},
			{name: "ipv6_only_fallback", ips: []string{"2001:db8::1"}, want: "2001:db8::1"},
			{name: "explicit_ipv4", suffix: "4", ips: []string{"2001:db8::1", "192.0.2.1"}, want: "192.0.2.1"},
			{name: "explicit_ipv6", suffix: "6", ips: []string{"192.0.2.1", "2001:db8::1"}, want: "2001:db8::1"},
			{name: "missing_ipv4", suffix: "4", ips: []string{"2001:db8::1"}},
			{name: "missing_ipv6", suffix: "6", ips: []string{"192.0.2.1"}},
			{name: "empty_answers"},
			{name: "ipv6_literal", host: "2001:db8::1", want: "2001:db8::1"},
			{name: "ipv4_literal", host: "192.0.2.1", want: "192.0.2.1"},
		} {
			t.Run(network+"/"+tc.name, func(t *testing.T) {
				platform := iceDNSFixture{}
				for _, address := range tc.ips {
					platform.ips = append(platform.ips, net.ParseIP(address))
				}
				n, err := newICENetwork(context.Background(), platform)
				if err != nil {
					t.Fatal(err)
				}
				host := tc.host
				if host == "" {
					host = "HOST"
				}
				got, err := n.resolve(network+tc.suffix, host)
				if tc.want == "" {
					var dnsErr *net.DNSError
					if !errors.As(err, &dnsErr) || !dnsErr.IsNotFound {
						t.Fatalf("expected missing-family DNS error, got %v, %v", got, err)
					}
				} else if err != nil || !got.Equal(net.ParseIP(tc.want)) {
					t.Fatalf("got %v, %v; want %s", got, err, tc.want)
				}
			})
		}
	}
}

// Capture the real Pion Allocate request without opening a socket. An IPv4
// PacketConn must not receive an IPv6 destination from generic "udp" lookup.
type turnAllocateDNSProbe struct {
	net.PacketConn
	destination net.Addr
}

func (*turnAllocateDNSProbe) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4(192, 0, 2, 2), Port: 12345}
}

func (p *turnAllocateDNSProbe) WriteTo(_ []byte, addr net.Addr) (int, error) {
	p.destination = addr
	return 0, net.ErrClosed // Stop after capturing the first request.
}

func TestTURNUDPAllocateUsesIPv4WithIPv6FirstDNS(t *testing.T) {
	n, err := newICENetwork(context.Background(), iceDNSFixture{
		ips: []net.IP{net.ParseIP("2001:db8::1"), net.ParseIP("192.0.2.1")},
	})
	if err != nil {
		t.Fatal(err)
	}
	conn := &turnAllocateDNSProbe{}
	client, err := turn.NewClient(&turn.ClientConfig{
		Net: n, Conn: conn, TURNServerAddr: "HOST:3478", Username: "fixture", Password: "fixture",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Allocate(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("expected probe stop, got %v", err)
	}
	addr, ok := conn.destination.(*net.UDPAddr)
	if !ok || !addr.IP.Equal(net.ParseIP("192.0.2.1")) || addr.Port != 3478 {
		t.Fatalf("IPv4 TURN socket received mismatched destination: %v", conn.destination)
	}
}

func TestICENetworkResolvePreservesDNSError(t *testing.T) {
	want := errors.New("fixture DNS failure")
	n, err := newICENetwork(context.Background(), iceDNSFixture{err: want})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := n.ResolveUDPAddr("udp", "HOST:3478"); !errors.Is(err, want) {
		t.Fatalf("DNS error changed: %v", err)
	}
}
