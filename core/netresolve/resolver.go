// Package netresolve provides the system-first DNS policy shared by the
// standalone clients and diagnostic tools. Android Network adapters keep
// their own pinned resolver and do not use this fallback.
package netresolve

import (
	"context"
	"fmt"
	"net"
	"time"
)

const lookupTimeout = 2 * time.Second

// NewFallbackResolver works without libc or resolv.conf (e.g. static binaries
// running in Termux). Preserve TCP retries requested for truncated DNS replies.
func NewFallbackResolver() *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: lookupTimeout}).DialContext(ctx, network, "223.5.5.5:53")
		},
	}
}

func LookupIP(ctx context.Context, network, host string) ([]net.IP, error) {
	return lookupIP(ctx, network, host, net.DefaultResolver.LookupIP, NewFallbackResolver().LookupIP)
}

type lookupFunc func(context.Context, string, string) ([]net.IP, error)

func lookupIP(ctx context.Context, network, host string, system, fallback lookupFunc) ([]net.IP, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if ip := net.ParseIP(host); ip != nil {
		if network == "ip" || network == "ip4" && ip.To4() != nil || network == "ip6" && ip.To4() == nil {
			return []net.IP{ip}, nil
		}
		return nil, &net.DNSError{Name: host, Err: "no address in selected family", IsNotFound: true}
	}
	resolve := func(lookup lookupFunc) ([]net.IP, error) {
		attempt, cancel := context.WithTimeout(ctx, lookupTimeout)
		defer cancel()
		ips, err := lookup(attempt, network, host)
		if err == nil && len(ips) == 0 {
			err = &net.DNSError{Name: host, Err: "no addresses", IsNotFound: true}
		}
		return ips, err
	}
	ips, err := resolve(system)
	if err == nil {
		return ips, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	ips, fallbackErr := resolve(fallback)
	if fallbackErr != nil {
		return nil, fmt.Errorf("系统 DNS 失败（%v），备用 DNS 也失败：%w", err, fallbackErr)
	}
	return ips, nil
}
