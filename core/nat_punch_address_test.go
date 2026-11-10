package core

import (
	"bytes"
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestNATPunchPathAddressPrefersAuthenticatedPeerObservation(t *testing.T) {
	local := &net.UDPAddr{IP: net.IPv4(10, 0, 0, 7), Port: 23456}
	remote := &net.UDPAddr{IP: net.IPv4(198, 51, 100, 8), Port: 40000}
	platform := punchAddressPlatform{dial: func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("peer reflection must not allocate a route-query socket")
		return nil, errors.New("unexpected dial")
	}}
	for _, tc := range []struct{ observed, want, kind string }{
		{"203.0.113.7:54321", "203.0.113.7:54321", "prflx"},
		{"[::ffff:203.0.113.7]:54321", "203.0.113.7:54321", "prflx"},
		{"10.0.0.7:23456", "10.0.0.7:23456", "prflx"},
		{"", "10.0.0.7:23456", "host"}, // Older mux hello.
		{"0.0.0.0:54321", "10.0.0.7:23456", "host"},
		{"224.0.0.1:54321", "10.0.0.7:23456", "host"},
		{"255.255.255.255:54321", "10.0.0.7:23456", "host"},
		{"[2001:db8::1]:54321", "10.0.0.7:23456", "host"},
		{"example.com:54321", "10.0.0.7:23456", "host"},
		{"203.0.113.7:0", "10.0.0.7:23456", "host"},
		{"203.0.113.7:65536", "10.0.0.7:23456", "host"},
	} {
		t.Run(tc.observed, func(t *testing.T) {
			got, kind := natPunchPathAddress(context.Background(), platform, local, remote, tc.observed)
			if got != tc.want || kind != tc.kind {
				t.Fatalf("address=%q type=%q, want %q %q", got, kind, tc.want, tc.kind)
			}
		})
	}
}

func TestMuxObservedAddressOptionalWireCompatibility(t *testing.T) {
	// Old decoders ignore the optional field; new decoders accept its absence.
	type legacyHello struct {
		Version int `json:"session_version"`
	}
	var wire bytes.Buffer
	if err := writeSessionJSON(&wire, muxHello{Version: sessionProtocol, ObservedAddress: "203.0.113.7:54321"}); err != nil {
		t.Fatal(err)
	}
	var legacy legacyHello
	if err := readSessionJSON(&wire, &legacy); err != nil || legacy.Version != sessionProtocol {
		t.Fatalf("old decoder failed: %+v %v", legacy, err)
	}
	if err := writeSessionJSON(&wire, legacy); err != nil {
		t.Fatal(err)
	}
	var current muxHello
	if err := readSessionJSON(&wire, &current); err != nil || current.Version != sessionProtocol || current.ObservedAddress != "" {
		t.Fatalf("old hello failed: %+v %v", current, err)
	}
}

type punchAddressPlatform struct {
	DefaultPlatform
	dial func(context.Context, string, string) (net.Conn, error)
}

func (p punchAddressPlatform) DialSignal(ctx context.Context, network, address string) (net.Conn, error) {
	return p.dial(ctx, network, address)
}

type punchRouteConn struct {
	net.Conn
	local  net.Addr
	closed bool
}

func (c *punchRouteConn) LocalAddr() net.Addr { return c.local }
func (c *punchRouteConn) Close() error        { c.closed = true; return nil }

func TestNATPunchLocalAddress(t *testing.T) {
	address := func(s string) net.Addr {
		if s == "" {
			return nil
		}
		a, err := net.ResolveUDPAddr("udp", s)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	for _, tc := range []struct {
		name, bound, route, want string
		query, fail              bool
	}{
		{"concrete", "10.0.0.7:23456", "", "10.0.0.7:23456", false, false},
		{"wildcard", "0.0.0.0:23456", "10.0.0.7:54321", "10.0.0.7:23456", true, false},
		{"mapped wildcard", "[::ffff:0.0.0.0]:23456", "10.0.0.7:54321", "10.0.0.7:23456", true, false},
		{"unavailable route", "0.0.0.0:23456", "", "", true, true},
		{"unspecified route", "0.0.0.0:23456", "0.0.0.0:54321", "", true, false},
		{"multicast route", "0.0.0.0:23456", "224.0.0.1:54321", "", true, false},
		{"wrong family", "0.0.0.0:23456", "[2001:db8::1]:54321", "", true, false},
		{"missing route", "0.0.0.0:23456", "", "", true, false},
		{"missing socket", "", "", "", false, false},
		{"missing port", "0.0.0.0:0", "", "", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			queried := false
			route := &punchRouteConn{local: address(tc.route)}
			platform := punchAddressPlatform{dial: func(ctx context.Context, network, remote string) (net.Conn, error) {
				queried = true
				if network != "udp4" || remote != "198.51.100.8:40000" {
					t.Fatalf("route query used %s %s", network, remote)
				}
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > time.Second {
					t.Fatal("route query has no bounded deadline")
				}
				if tc.fail {
					return nil, errors.New("network unavailable")
				}
				return route, nil
			}}
			got := natPunchLocalAddress(context.Background(), platform, address(tc.bound), address("198.51.100.8:40000"))
			if got != tc.want || queried != tc.query {
				t.Fatalf("address=%q query=%v, want %q query=%v", got, queried, tc.want, tc.query)
			}
			if queried && !tc.fail && !route.closed {
				t.Fatal("route socket leaked")
			}
		})
	}
}

func TestNATPunchLocalAddressUsesOriginalPortWithoutSending(t *testing.T) {
	local, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	remote, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	bound := local.LocalAddr().(*net.UDPAddr)
	want := (&net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: bound.Port}).String()
	if got := natPunchLocalAddress(context.Background(), DefaultPlatform{}, bound, remote.LocalAddr()); got != want {
		t.Fatalf("address=%q, want %q", got, want)
	}
	if local.LocalAddr().String() != bound.String() {
		t.Fatal("route query changed the punched socket")
	}
	_ = remote.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	if _, _, err := remote.ReadFrom(make([]byte, 1)); err == nil {
		t.Fatal("route query sent a packet")
	} else if timeout, ok := err.(net.Error); !ok || !timeout.Timeout() {
		t.Fatal(err)
	}
}

// Exercise both mux roles with the wildcard listeners used by real platforms.
// Only UDP route queries are simulated; Worker signaling uses the real socket.
type punchWildcardPlatform struct{ *punchSimPlatform }

type punchWildcardSocket struct{ net.PacketConn }

func (s punchWildcardSocket) LocalAddr() net.Addr {
	bound := *s.PacketConn.LocalAddr().(*net.UDPAddr)
	bound.IP = net.IPv4zero
	return &bound
}

func (p punchWildcardPlatform) ListenPacket(ctx context.Context, network, address string) (net.PacketConn, error) {
	pc, err := p.punchSimPlatform.ListenPacket(ctx, network, address)
	if err != nil {
		return nil, err
	}
	return punchWildcardSocket{pc}, nil
}

func (p punchWildcardPlatform) DialSignal(ctx context.Context, network, address string) (net.Conn, error) {
	if network == "udp4" {
		return &punchRouteConn{local: &net.UDPAddr{IP: net.IPv4(10, 0, byte(p.nat.index), 1), Port: 60000}}, nil
	}
	return p.punchSimPlatform.DialSignal(ctx, network, address)
}
