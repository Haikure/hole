// SPDX-License-Identifier: GPL-3.0-only
package nat

import (
	"context"
	"fmt"
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
	snapshot, addresses, _ := ProbeDetailed(ctx, network, pc, urls)
	return snapshot, addresses
}

type ProbeTarget struct {
	URL      string   `json:"url"`
	Endpoint string   `json:"endpoint,omitempty"`
	Samples  int      `json:"samples"`
	Mappings []string `json:"mappings"`
	Reason   string   `json:"reason,omitempty"`
}

// ProbeDetailed retains resolution failures and per-target base samples for hosts.
func ProbeDetailed(ctx context.Context, network Network, pc net.PacketConn, urls []string) (Snapshot, []netip.AddrPort, []ProbeTarget) {
	capacity := min(len(urls), maxProbeServers)
	endpoints := make([]*net.UDPAddr, 0, capacity)
	seen := make(map[string]int, capacity)
	targets := make([]ProbeTarget, capacity)
	resolve := network.ResolveUDP
	if ipv4, ok := network.(interface {
		ResolveUDP4(context.Context, string) (*net.UDPAddr, error)
	}); ok {
		resolve = ipv4.ResolveUDP4
	}
	for i, raw := range urls[:capacity] {
		target := &targets[i]
		target.URL, target.Mappings = raw, []string{}
		if ctx.Err() != nil {
			target.Reason = "未解析：检测已取消或总时限已到"
			continue
		}
		uri, err := stun.ParseURI(raw)
		if err != nil || uri.Scheme != stun.SchemeTypeSTUN || uri.Proto != stun.ProtoTypeUDP {
			target.Reason = "无效的 UDP STUN 地址"
			continue
		}
		// A stalled Android Network lookup must not consume the entire probe.
		resolveCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		address, err := resolve(resolveCtx, net.JoinHostPort(uri.Host, strconv.Itoa(uri.Port)))
		cancel()
		if err != nil {
			target.Reason = "IPv4 解析失败：" + err.Error()
			continue
		}
		if address == nil || address.IP.To4() == nil {
			target.Reason = "DNS 未返回可用的 IPv4 地址"
			continue
		}
		key := address.String()
		target.Endpoint = key
		if previous, ok := seen[key]; ok {
			target.Reason = "重复端点，与 " + targets[previous].URL + " 合并采样"
			continue
		}
		target.Reason = "未采样：检测已取消或总时限已到"
		seen[key] = i
		endpoints = append(endpoints, address)
	}
	if len(endpoints) == 0 {
		snapshot := natprobe.Unknown("没有可用的 IPv4 STUN 地址")
		snapshot.ProbedAt = time.Now().UTC().Format(time.RFC3339Nano)
		return snapshot, nil, targets
	}

	_, snapshot, addresses := natprobe.ProbeSocketObserved(ctx, pc, endpoints, func(networkName, address string) (net.PacketConn, error) {
		return network.ListenPacket(ctx, networkName, address)
	}, selectedLocalEndpoint(ctx, network, pc, endpoints[0]), func(endpoint netip.AddrPort, sample natprobe.Sample, err error) {
		target := &targets[seen[endpoint.String()]]
		if err != nil {
			target.Reason = fmt.Sprintf("基础映射采样中止：%v", err)
			return
		}
		target.Reason = ""
		target.Samples++
		raw := sample.Mapped.String()
		for _, previous := range target.Mappings {
			if previous == raw {
				return
			}
		}
		target.Mappings = append(target.Mappings, raw)
	})
	return snapshot, addresses, targets
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
