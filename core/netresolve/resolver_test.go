package netresolve

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestLookupSystemFallbackAndLiteral(t *testing.T) {
	for _, scenario := range []string{"system", "fallback", "empty", "literal", "wrong-family", "both-fail", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			calls := []string{}
			failure := errors.New("DNS unavailable")
			lookup := func(name string) lookupFunc {
				return func(attempt context.Context, network, host string) ([]net.IP, error) {
					calls = append(calls, name)
					deadline, ok := attempt.Deadline()
					if !ok || time.Until(deadline) > time.Second || network != "ip4" || host != "stun.invalid" {
						t.Fatalf("unbounded/wrong lookup: %v %s %s", deadline, network, host)
					}
					if scenario == "canceled" {
						cancel()
						return nil, context.Canceled
					}
					if scenario == "both-fail" || name == "system" && scenario == "fallback" {
						return nil, failure
					}
					if name == "system" && scenario == "empty" {
						return nil, nil
					}
					return []net.IP{net.ParseIP("192.0.2.1")}, nil
				}
			}
			host := "stun.invalid"
			if scenario == "literal" {
				host = "192.0.2.1"
			}
			if scenario == "wrong-family" {
				host = "2001:db8::1"
			}
			ips, err := lookupIP(ctx, "ip4", host, lookup("system"), lookup("fallback"))
			switch scenario {
			case "both-fail":
				if !errors.Is(err, failure) || len(calls) != 2 {
					t.Fatal(calls, err)
				}
			case "canceled":
				if !errors.Is(err, context.Canceled) || len(calls) != 1 {
					t.Fatal(calls, err)
				}
			case "wrong-family":
				if err == nil || len(calls) != 0 {
					t.Fatal(calls, err)
				}
			default:
				if err != nil || len(ips) != 1 || ips[0].String() != "192.0.2.1" {
					t.Fatal(ips, err)
				}
				want := map[string]int{"system": 1, "fallback": 2, "empty": 2, "literal": 0}[scenario]
				if len(calls) != want {
					t.Fatalf("calls=%v want %d", calls, want)
				}
			}
		})
	}
}

func TestFallbackHonorsCancellationWithoutSystemDNS(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	resolver := NewFallbackResolver()
	if !resolver.PreferGo || resolver.Dial == nil {
		t.Fatal("fallback depends on system DNS")
	}
	for _, network := range []string{"udp", "tcp"} {
		conn, err := resolver.Dial(ctx, network, "127.0.0.1:53")
		if conn != nil {
			conn.Close()
			t.Fatal("canceled DNS dial succeeded")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal(network, err)
		}
	}
}

func TestSlowSystemLookupLeavesTimeForFallback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ips, err := lookupIP(ctx, "ip4", "stun.invalid", func(attempt context.Context, _, _ string) ([]net.IP, error) {
		<-attempt.Done()
		return nil, attempt.Err()
	}, func(attempt context.Context, _, _ string) ([]net.IP, error) {
		if attempt.Err() != nil {
			t.Fatal("system resolver consumed the entire budget")
		}
		return []net.IP{net.ParseIP("192.0.2.1")}, nil
	})
	if err != nil || len(ips) != 1 {
		t.Fatal(ips, err)
	}
}
