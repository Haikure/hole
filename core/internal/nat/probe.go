// SPDX-License-Identifier: LGPL-3.0-only
// See classify.go for provenance and modifications.
package nat

import (
	"context"
	"errors"
	"github.com/pion/stun/v4"
	"hole/core/natprobe"
	"net"
	"net/netip"
	"strconv"
	"time"
)

type Network interface {
	ListenPacket(context.Context, string, string) (net.PacketConn, error)
	ResolveUDP(context.Context, string) (*net.UDPAddr, error)
}

const maxProbeServers = 8

func addrPort(a net.Addr) netip.AddrPort {
	if a == nil {
		return netip.AddrPort{}
	}
	v, _ := netip.ParseAddrPort(a.String())
	return v
}

func Binding(ctx context.Context, pc net.PacketConn, server *net.UDPAddr, change uint32) (Sample, error) {
	return natprobe.Binding(ctx, pc, server, byte(change), 3*time.Second)
}

func Probe(ctx context.Context, network Network, pc net.PacketConn, urls []string) (Snapshot, []netip.AddrPort) {
	var endpoints []*net.UDPAddr
	seen := map[string]bool{}
	for _, raw := range urls {
		if ctx.Err() != nil || len(endpoints) >= maxProbeServers {
			break
		}
		u, err := stun.ParseURI(raw)
		if err != nil {
			continue
		}
		a, err := network.ResolveUDP(ctx, net.JoinHostPort(u.Host, uPort(u.Port)))
		if err != nil || a.IP.To4() == nil || seen[a.String()] {
			continue
		}
		seen[a.String()] = true
		endpoints = append(endpoints, a)
	}
	if len(endpoints) == 0 {
		s := Unknown("没有可用的 IPv4 STUN 地址")
		s.ProbedAt = time.Now().UTC().Format(time.RFC3339Nano)
		return s, nil
	}

	_, snapshot, addresses := natprobe.ProbeSocket(ctx, pc, endpoints, func(n, a string) (net.PacketConn, error) {
		return network.ListenPacket(ctx, n, a)
	}, localEndpoint(ctx, network, pc, endpoints[0]))
	return snapshot, addresses
}

func localEndpoint(ctx context.Context, network Network, pc net.PacketConn, remote *net.UDPAddr) netip.AddrPort {
	local := addrPort(pc.LocalAddr())
	if local.IsValid() && !local.Addr().IsUnspecified() {
		return local
	}
	dialer, ok := network.(interface {
		DialUDP(context.Context, *net.UDPAddr) (*net.UDPConn, error)
	})
	if !ok || local.Port() == 0 {
		return netip.AddrPort{}
	}
	conn, err := dialer.DialUDP(ctx, remote)
	if err != nil {
		return netip.AddrPort{}
	}
	defer conn.Close()
	selected := addrPort(conn.LocalAddr())
	if !selected.IsValid() {
		return netip.AddrPort{}
	}
	return netip.AddrPortFrom(selected.Addr(), local.Port())
}

func uPort(p int) string { return strconv.Itoa(p) }

var ErrNoStrategy = errors.New("当前 NAT 组合没有适用的 UDP 策略")
