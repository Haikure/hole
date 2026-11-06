// SPDX-License-Identifier: GPL-2.0-or-later
//
// natprobe is a standalone UDP STUN NAT diagnostic tool.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/netip"
	"os"
	"sort"
	"strings"

	"github.com/pion/stun/v4"
	"hole/core/natprobe"
)

const (
	defaultTimeout = natprobe.DefaultTimeout
	maxServers     = 8
)

var defaultSTUNServers = natprobe.DefaultServers()

type stringList struct {
	values   []string
	explicit bool
}

func (s *stringList) String() string { return strings.Join(s.values, ",") }

func (s *stringList) Set(value string) error {
	if !s.explicit {
		s.values = nil
		s.explicit = true
	}
	for _, raw := range strings.Split(value, ",") {
		server := strings.TrimSpace(raw)
		if server == "" {
			continue
		}
		if !strings.HasPrefix(server, "stun:") {
			server = "stun:" + server
		}
		s.values = append(s.values, server)
	}
	return nil
}

type natResult = natprobe.Snapshot

type report struct {
	LocalSocket string    `json:"local_socket"`
	STUNServers []string  `json:"stun_servers"`
	NAT         natResult `json:"nat"`
	Mapped      []string  `json:"mapped_addresses"`
}

func main() {
	os.Exit(run())
}

func run() int {
	servers := stringList{values: append([]string(nil), defaultSTUNServers...)}
	flag.Var(&servers, "stun", "UDP STUN 地址，可重复或逗号分隔（默认使用内置列表）")
	timeout := flag.Duration("timeout", defaultTimeout, "DNS 与探测的总超时时间")
	jsonOutput := flag.Bool("json", false, "输出 JSON")
	flag.Parse()

	if *timeout <= 0 {
		fmt.Fprintln(os.Stderr, "-timeout 必须大于零")
		return 2
	}
	if len(servers.values) == 0 {
		fmt.Fprintln(os.Stderr, "至少需要一个 -stun 地址")
		return 2
	}
	if len(servers.values) > maxServers {
		fmt.Fprintf(os.Stderr, "最多支持 %d 个 -stun 地址\n", maxServers)
		return 2
	}
	for _, server := range servers.values {
		if _, err := stun.ParseURI(server); err != nil {
			fmt.Fprintf(os.Stderr, "STUN 地址无效 %q：%v\n", server, err)
			return 2
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	endpoints, resolveErrors := resolveServers(ctx, servers.values)
	local, result, mapped := probe(ctx, endpoints)
	if result.Reason == "" && len(resolveErrors) > 0 {
		result.Reason = strings.Join(resolveErrors, "; ")
	}
	addresses := make([]string, 0, len(mapped))
	for _, address := range mapped {
		addresses = append(addresses, address.String())
	}
	sort.Strings(addresses)
	output := report{LocalSocket: local, STUNServers: servers.values, NAT: result, Mapped: addresses}
	if *jsonOutput {
		if err := json.NewEncoder(os.Stdout).Encode(output); err != nil {
			fmt.Fprintf(os.Stderr, "JSON 编码失败：%v\n", err)
			return 1
		}
		return 0
	}
	printReport(output)
	return 0
}

func resolveServers(ctx context.Context, urls []string) ([]*net.UDPAddr, []string) {
	endpoints := make([]*net.UDPAddr, 0, len(urls))
	seen := make(map[netip.AddrPort]bool)
	var failures []string
	for _, raw := range urls {
		if ctx.Err() != nil {
			break
		}
		uri, err := stun.ParseURI(raw)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", raw, err))
			continue
		}
		port := uri.Port
		if port == 0 {
			port = 3478
		}
		ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", uri.Host)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s DNS: %v", uri.Host, err))
			continue
		}
		if len(ips) == 0 {
			failures = append(failures, fmt.Sprintf("%s has no IPv4 address", uri.Host))
			continue
		}
		address := &net.UDPAddr{IP: ips[0], Port: port}
		key := addrPort(address)
		if key.IsValid() && !seen[key] {
			seen[key] = true
			endpoints = append(endpoints, address)
		}
	}
	return endpoints, failures
}

func probe(ctx context.Context, servers []*net.UDPAddr) (string, natResult, []netip.AddrPort) {
	return probeWithListener(ctx, servers, net.ListenPacket)
}

func probeWithListener(ctx context.Context, servers []*net.UDPAddr, listen func(string, string) (net.PacketConn, error)) (string, natResult, []netip.AddrPort) {
	return natprobe.ProbeWithListener(ctx, servers, listen)
}

func addrPort(address net.Addr) netip.AddrPort {
	if address == nil {
		return netip.AddrPort{}
	}
	value, err := netip.ParseAddrPort(address.String())
	if err != nil || !value.Addr().Is4() {
		return netip.AddrPort{}
	}
	return value
}

func printReport(result report) {
	fmt.Printf("NAT 类型：%s (%s)\n", natTypeLabel(result.NAT.Type), result.NAT.Type)
	fmt.Printf("映射行为：%s\n", behaviorLabel(result.NAT.MappingBehavior))
	fmt.Printf("过滤行为：%s\n", behaviorLabel(result.NAT.FilteringBehavior))
	if result.NAT.FilteringServer != "" {
		fmt.Printf("过滤测试 STUN：%s\n", result.NAT.FilteringServer)
	}
	fmt.Printf("端口变化可预测性：%s\n", predictabilityLabel(result.NAT.Predictability))
	fmt.Printf("端口保持：%s\n", portPreservationLabel(result.NAT.PortPreservation))
	fmt.Printf("No-PAT 判断：%s\n", noPATLabel(result.NAT.NoPAT))
	fmt.Printf("探测状态：%s，样本数：%d\n", statusLabel(result.NAT.Status), result.NAT.Samples)
	fmt.Printf("本地 UDP socket：%s\n", result.LocalSocket)
	if len(result.Mapped) == 0 {
		fmt.Println("映射地址：没有收到有效的 STUN 映射响应")
	} else {
		fmt.Println("映射地址：")
		for _, address := range result.Mapped {
			fmt.Printf("  %s\n", address)
		}
	}
	if result.NAT.Reason != "" {
		fmt.Printf("说明：%s\n", result.NAT.Reason)
	}
}

func natTypeLabel(value string) string {
	labels := map[string]string{
		"open":                 "开放网络",
		"firewall":             "防火墙过滤（未检测到地址转换）",
		"full_cone":            "全锥型",
		"restricted_cone":      "地址受限型",
		"port_restricted_cone": "端口受限型",
		"symmetric":            "对称型",
		"endpoint_independent": "端点无关映射",
		"unknown":              "未知",
	}
	if label, ok := labels[value]; ok {
		return label
	}
	return value
}

func behaviorLabel(value string) string {
	labels := map[string]string{
		"endpoint_independent":   "端点无关",
		"endpoint_dependent":     "端点相关",
		"address_dependent":      "允许同 IP 的变更端口响应",
		"address_port_dependent": "地址与端口均受限",
		"none":                   "不适用（开放网络）",
		"unknown":                "未知",
	}
	if label, ok := labels[value]; ok {
		return label
	}
	return value
}

func predictabilityLabel(value string) string {
	switch value {
	case "stable":
		return "稳定（不同目标间映射端口未变化）"
	case "predictable_incremental":
		return "可预测，端口严格递增"
	case "predictable_decremental":
		return "可预测，端口严格递减"
	case "incremental":
		return "可能可预测，端口递增"
	case "decremental":
		return "可能可预测，端口递减"
	case "irregular":
		return "不可预测，端口变化不规则"
	default:
		return "未知，样本不足或 STUN 服务不支持所需探测"
	}
}

func portPreservationLabel(value string) string {
	switch value {
	case "preserved":
		return "保持（所有有效样本的公网端口均等于本地端口）"
	case "translated":
		return "未保持（所有有效样本均发生端口转换）"
	case "mixed":
		return "部分保持（同时观测到端口保持和端口转换）"
	default:
		return "未知（缺少有效样本或本地端点）"
	}
}

func noPATLabel(value string) string {
	switch value {
	case "suspected":
		return "疑似 No-PAT（仅 IP 转换、端口保持；也可能是保留端口的 PAT）"
	case "not_detected":
		return "不符合 No-PAT（已观测到端口转换）"
	case "not_applicable":
		return "不适用（未检测到地址转换）"
	default:
		return "未知（样本不足、本地地址未知或公网映射不一致）"
	}
}

func statusLabel(value string) string {
	if value == "complete" {
		return "完成"
	}
	return "样本不足"
}
