// SPDX-License-Identifier: GPL-3.0-only
package nat

import (
	"context"
	"net"
	"net/netip"
	"strconv"
	"time"

	"github.com/pion/stun/v4"
	"hole/core/natprobe"
)

type Network interface {
	ListenPacket(context.Context, string, string) (net.PacketConn, error)
	ResolveUDP(context.Context, string) (*net.UDPAddr, error)
}

const maxProbeServers = 8

func addrPort(address net.Addr) netip.AddrPort {
	if address == nil {
		return netip.AddrPort{}
	}
	endpoint, _ := netip.ParseAddrPort(address.String())
	return endpoint
}

func Binding(ctx context.Context, pc net.PacketConn, server *net.UDPAddr, change uint32) (Sample, error) {
	return natprobe.Binding(ctx, pc, server, byte(change), 3*time.Second)
}

func Probe(ctx context.Context, network Network, pc net.PacketConn, urls []string) (Snapshot, []netip.AddrPort) {
	capacity := min(len(urls), maxProbeServers)
	endpoints := make([]*net.UDPAddr, 0, capacity)
	seen := make(map[string]struct{}, capacity)
	for _, raw := range urls {
		if ctx.Err() != nil || len(endpoints) == maxProbeServers {
			break
		}
		uri, err := stun.ParseURI(raw)
		if err != nil || uri.Scheme != stun.SchemeTypeSTUN || uri.Proto != stun.ProtoTypeUDP {
			continue
		}
		address, err := network.ResolveUDP(ctx, net.JoinHostPort(uri.Host, strconv.Itoa(uri.Port)))
		if err != nil || address.IP.To4() == nil {
			continue
		}
		key := address.String()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		endpoints = append(endpoints, address)
	}
	if len(endpoints) == 0 {
		snapshot := natprobe.Unknown("没有可用的 IPv4 STUN 地址")
		snapshot.ProbedAt = time.Now().UTC().Format(time.RFC3339Nano)
		return snapshot, nil
	}

	_, snapshot, addresses := natprobe.ProbeSocket(ctx, pc, endpoints, func(networkName, address string) (net.PacketConn, error) {
		return network.ListenPacket(ctx, networkName, address)
	}, selectedLocalEndpoint(ctx, network, pc, endpoints[0]))
	return snapshot, addresses
}

func selectedLocalEndpoint(ctx context.Context, network Network, pc net.PacketConn, remote *net.UDPAddr) netip.AddrPort {
	local := addrPort(pc.LocalAddr())
	if local.IsValid() && !local.Addr().IsUnspecified() {
		return local
	}
	if local.Port() == 0 {
		return netip.AddrPort{}
	}
	dialer, ok := network.(interface {
		DialUDP(context.Context, *net.UDPAddr) (*net.UDPConn, error)
	})
	if !ok {
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
