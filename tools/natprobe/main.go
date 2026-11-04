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
	"time"

	"github.com/pion/stun/v4"
)

const (
	probeTimeout   = 1200 * time.Millisecond
	defaultTimeout = 20 * time.Second
	maxServers     = 8
	probeRounds    = 3
	probeInterval  = 100 * time.Millisecond
	maxPortSpan    = 1024
)

var defaultSTUNServers = []string{
	"stun:stun.miwifi.com:3478",
	"stun:stun.chat.bilibili.com:3478",
	"stun:stun.hitv.com:3478",
	"stun:stun.voipgate.com:3478",
}

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

type sample struct {
	Server, Source, Mapped netip.AddrPort
	Other                  netip.AddrPort
	Change                 byte
}

type natResult struct {
	Status            string `json:"status"`
	Type              string `json:"type"`
	MappingBehavior   string `json:"mapping_behavior"`
	FilteringBehavior string `json:"filtering_behavior"`
	FilteringServer   string `json:"filtering_server,omitempty"`
	Predictability    string `json:"predictability"`
	Samples           int    `json:"samples"`
	ProbedAt          string `json:"probed_at"`
	Reason            string `json:"reason,omitempty"`
}

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
	result := unknownResult("有效 STUN 样本不足")
	if len(servers) == 0 {
		result.Reason = "没有可用的 IPv4 STUN 地址"
		return "", result, nil
	}
	pc, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		result.Reason = "创建 UDP socket 失败：" + err.Error()
		return "", result, nil
	}
	defer pc.Close()

	localEndpoint := localSocket(ctx, pc, servers[0])
	localText := pc.LocalAddr().String()
	if localEndpoint.IsValid() {
		localText = localEndpoint.String()
	}

	samples := make([]sample, 0, len(servers)*probeRounds+4)
	baseline := make(map[netip.AddrPort]sample, len(servers))
	var lastRequest time.Time
	for _, server := range servers {
		if ctx.Err() != nil {
			break
		}
		for round := 0; round < probeRounds && ctx.Err() == nil; round++ {
			value, err := pacedBinding(ctx, pc, server, 0, &lastRequest)
			if err != nil {
				break
			}
			samples = append(samples, value)
			baseline[value.Server] = value
		}
	}

	var changedBoth, changedPort *sample
	filteringBehavior, filteringServer, filteringReason := "unknown", "", "没有 STUN 服务提供可验证的 RFC 5780 OTHER-ADDRESS"
	for _, server := range servers {
		if ctx.Err() != nil {
			break
		}
		primary := addrPort(server)
		initial, ok := baseline[primary]
		if !ok || !usableOtherAddress(initial.Other, primary) {
			continue
		}
		filteringServer = primary.String()
		var evidence []sample
		filteringBehavior, changedBoth, changedPort, evidence, filteringReason = filteringProbe(ctx, pc, primary, initial.Other, &lastRequest)
		samples = append(samples, evidence...)
		if filteringBehavior != "unknown" {
			break
		}
	}

	var extra *sample
	if ctx.Err() == nil && mappingVaries(samples) && len(servers) > 1 {
		fresh, err := net.ListenPacket("udp4", "0.0.0.0:0")
		if err == nil {
			if value, e := pacedBinding(ctx, fresh, servers[0], 0, &lastRequest); e == nil {
				extra = &value
			}
			_ = fresh.Close()
		}
	}

	result = classify(localEndpoint, samples, changedBoth, changedPort, extra, filteringBehavior, filteringReason)
	result.FilteringServer = filteringServer
	if ctx.Err() != nil && result.Status == "insufficient" {
		result.Reason = "探测总时限已到"
	}
	return localText, result, mappedAddresses(samples)
}

func binding(ctx context.Context, pc net.PacketConn, server *net.UDPAddr, change byte, timeout time.Duration) (sample, error) {
	request := stun.MustBuild(stun.TransactionID, stun.BindingRequest)
	if change != 0 {
		request.Add(stun.AttrChangeRequest, []byte{0, 0, 0, change})
	}
	deadline := time.Now().Add(timeout)
	if limit, ok := ctx.Deadline(); ok && limit.Before(deadline) {
		deadline = limit
	}
	_ = pc.SetDeadline(deadline)
	interrupted := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = pc.SetReadDeadline(time.Now())
		close(interrupted)
	})
	defer func() {
		if !stop() {
			<-interrupted
		}
		_ = pc.SetDeadline(time.Time{})
	}()
	if _, err := pc.WriteTo(request.Raw, server); err != nil {
		return sample{}, err
	}
	buf := make([]byte, 2048)
	for ctx.Err() == nil {
		n, source, err := pc.ReadFrom(buf)
		if err != nil {
			return sample{}, err
		}
		message := new(stun.Message)
		message.Raw = append(message.Raw, buf[:n]...)
		if message.Decode() != nil || message.TransactionID != request.TransactionID || message.Type != stun.BindingSuccess {
			continue
		}
		from := addrPort(source)
		to := addrPort(server)
		if change == 0 && from != to {
			continue
		}
		mapped, ok := mappedAddress(message)
		if !ok {
			continue
		}
		return sample{Server: to, Source: from, Mapped: mapped, Other: otherAddress(message), Change: change}, nil
	}
	return sample{}, ctx.Err()
}

func pacedBinding(ctx context.Context, pc net.PacketConn, server *net.UDPAddr, change byte, lastRequest *time.Time) (sample, error) {
	if !lastRequest.IsZero() {
		if wait := time.Until(lastRequest.Add(probeInterval)); wait > 0 {
			timer := time.NewTimer(wait)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return sample{}, ctx.Err()
			case <-timer.C:
			}
		}
	}
	value, err := binding(ctx, pc, server, change, probeTimeout)
	*lastRequest = time.Now()
	return value, err
}

func filteringProbe(ctx context.Context, pc net.PacketConn, primary, other netip.AddrPort, lastRequest *time.Time) (string, *sample, *sample, []sample, string) {
	var evidence []sample
	if changed, err := pacedBinding(ctx, pc, net.UDPAddrFromAddrPort(primary), 6, lastRequest); err == nil && changed.Source == other {
		return "endpoint_independent", &changed, nil, append(evidence, changed), ""
	}
	portChanged := netip.AddrPortFrom(primary.Addr(), other.Port())
	if changed, err := pacedBinding(ctx, pc, net.UDPAddrFromAddrPort(primary), 2, lastRequest); err == nil && changed.Source == portChanged {
		return "address_dependent", nil, &changed, append(evidence, changed), ""
	}
	// RFC 5780's alternate-address check distinguishes restrictive filtering
	// from a path that cannot reach the server's alternate IP at all.
	alternateIP := netip.AddrPortFrom(other.Addr(), primary.Port())
	if check, err := pacedBinding(ctx, pc, net.UDPAddrFromAddrPort(alternateIP), 0, lastRequest); err == nil && check.Source == alternateIP {
		return "address_port_dependent", nil, nil, append(evidence, check), ""
	}
	return "unknown", nil, nil, evidence, "STUN 服务支持 OTHER-ADDRESS，但备用地址连通性未能确认，过滤行为无法判定"
}

func otherAddress(message *stun.Message) netip.AddrPort {
	var other stun.OtherAddress
	if err := other.GetFrom(message); err != nil || other.Port < 1 || other.Port > 65535 {
		return netip.AddrPort{}
	}
	address, ok := netip.AddrFromSlice(other.IP)
	if !ok {
		return netip.AddrPort{}
	}
	address = address.Unmap()
	if !address.Is4() {
		return netip.AddrPort{}
	}
	return netip.AddrPortFrom(address, uint16(other.Port))
}

func usableOtherAddress(other, primary netip.AddrPort) bool {
	return other.IsValid() && other.Addr().IsGlobalUnicast() && !other.Addr().IsPrivate() &&
		!other.Addr().IsLoopback() && !other.Addr().IsUnspecified() &&
		other.Addr().Is4() && other != primary && other.Addr() != primary.Addr()
}

func mappedAddress(message *stun.Message) (netip.AddrPort, bool) {
	var ip net.IP
	var port int
	var xor stun.XORMappedAddress
	if err := xor.GetFrom(message); err == nil {
		ip, port = xor.IP, xor.Port
	} else {
		var mapped stun.MappedAddress
		if err := mapped.GetFrom(message); err != nil {
			return netip.AddrPort{}, false
		}
		ip, port = mapped.IP, mapped.Port
	}
	address, ok := netip.AddrFromSlice(ip)
	if !ok || !address.Unmap().Is4() || port < 1 || port > 65535 {
		return netip.AddrPort{}, false
	}
	return netip.AddrPortFrom(address.Unmap(), uint16(port)), true
}

func addrPort(address net.Addr) netip.AddrPort {
	if address == nil {
		return netip.AddrPort{}
	}
	value, err := netip.ParseAddrPort(address.String())
	if err != nil || !value.Addr().Is4() {
		return netip.AddrPort{}
	}
	return netip.AddrPortFrom(value.Addr().Unmap(), value.Port())
}

func localSocket(ctx context.Context, pc net.PacketConn, remote *net.UDPAddr) netip.AddrPort {
	local, ok := pc.LocalAddr().(*net.UDPAddr)
	if !ok || local.Port < 1 || local.Port > 65535 {
		return netip.AddrPort{}
	}
	connection, err := (&net.Dialer{}).DialContext(ctx, "udp4", remote.String())
	if err != nil {
		return netip.AddrPort{}
	}
	defer connection.Close()
	address := addrPort(connection.LocalAddr())
	if !address.IsValid() {
		return netip.AddrPort{}
	}
	return netip.AddrPortFrom(address.Addr(), uint16(local.Port))
}

func classify(local netip.AddrPort, samples []sample, changedBoth, changedPort, extra *sample, filteringBehavior, filteringReason string) natResult {
	result := unknownResult("有效响应地址不足，无法确定 NAT 映射行为")
	result.Samples = len(samples)
	result.FilteringBehavior = filteringBehavior
	servers := make(map[netip.AddrPort]bool)
	mappings := make(map[netip.AddrPort]bool)
	publicIPs := make(map[netip.Addr]bool)
	for _, value := range samples {
		if !value.Source.IsValid() || !value.Mapped.IsValid() {
			continue
		}
		servers[value.Source] = true
		mappings[value.Mapped] = true
		publicIPs[value.Mapped.Addr()] = true
	}
	if len(servers) < 2 || len(mappings) == 0 {
		return result
	}

	result.Status = "complete"
	if len(mappings) == 1 {
		result.MappingBehavior = "endpoint_independent"
		result.Predictability = "stable"
		if local.IsValid() && mappings[local] {
			result.Type = "open"
			result.FilteringBehavior = "none"
			result.Reason = ""
			return result
		}
		if filteringBehavior == "endpoint_independent" && changedBoth != nil {
			result.Type = "full_cone"
			result.Reason = ""
			return result
		}
		if filteringBehavior == "address_dependent" && changedPort != nil {
			result.Type = "restricted_cone"
			result.Reason = ""
			return result
		}
		if filteringBehavior == "address_port_dependent" {
			result.Type = "port_restricted_cone"
			result.Reason = ""
			return result
		}
		result.Type = "endpoint_independent"
		result.Reason = filteringReason
		return result
	}

	result.Type = "symmetric"
	result.MappingBehavior = "endpoint_dependent"
	result.Predictability = "irregular"
	result.Reason = "不同 STUN 目标得到不同公网映射"
	if filteringReason != "" {
		result.Reason += "；" + filteringReason
	}
	if len(publicIPs) != 1 || extra == nil || !extra.Mapped.IsValid() || !publicIPs[extra.Mapped.Addr()] {
		return result
	}
	switch portTrend(samples, extra) {
	case "incremental":
		result.Predictability = "incremental"
		result.Reason = "端口样本整体递增，允许间隔和少量波动；仅为有限样本推断"
	case "decremental":
		result.Predictability = "decremental"
		result.Reason = "端口样本整体递减，允许间隔和少量波动；仅为有限样本推断"
	}
	if result.Predictability != "irregular" && filteringBehavior == "unknown" && filteringReason != "" {
		result.Reason += "；" + filteringReason
	}
	return result
}

func portTrend(samples []sample, extra *sample) string {
	if extra == nil || !extra.Mapped.IsValid() {
		return ""
	}
	targetPorts := make(map[netip.AddrPort]int)
	ordered := make([]int, 0, len(samples)+1)
	publicIP := netip.Addr{}
	for _, value := range samples {
		if value.Change != 0 || !value.Server.IsValid() || !value.Mapped.IsValid() {
			continue
		}
		if !publicIP.IsValid() {
			publicIP = value.Mapped.Addr()
		} else if value.Mapped.Addr() != publicIP {
			return ""
		}
		port := int(value.Mapped.Port())
		if previous, exists := targetPorts[value.Server]; exists {
			if previous != port {
				return ""
			}
			continue
		}
		targetPorts[value.Server] = port
		ordered = append(ordered, port)
	}
	if len(ordered) < 2 || !publicIP.IsValid() || extra.Mapped.Addr() != publicIP {
		return ""
	}

	minimum, maximum := ordered[0], ordered[0]
	for _, port := range ordered[1:] {
		minimum = min(minimum, port)
		maximum = max(maximum, port)
	}
	extraPort := int(extra.Mapped.Port())
	increasing := extraPort > maximum
	decreasing := extraPort < minimum
	if !increasing && !decreasing {
		return ""
	}
	ordered = append(ordered, extraPort)
	minimum = min(minimum, extraPort)
	maximum = max(maximum, extraPort)
	if maximum-minimum > maxPortSpan {
		return ""
	}
	uniquePorts := make(map[int]bool, len(ordered))
	for _, port := range ordered {
		uniquePorts[port] = true
	}
	if len(uniquePorts) < 3 || trendScore(ordered, increasing) < 0.8 {
		return ""
	}
	if increasing {
		return "incremental"
	}
	return "decremental"
}

func trendScore(ports []int, increasing bool) float64 {
	concordant, discordant := 0, 0
	for i := 0; i < len(ports); i++ {
		for j := i + 1; j < len(ports); j++ {
			if ports[i] == ports[j] {
				continue
			}
			if (ports[j] > ports[i]) == increasing {
				concordant++
			} else {
				discordant++
			}
		}
	}
	if concordant+discordant == 0 {
		return 0
	}
	return float64(concordant) / float64(concordant+discordant)
}

func mappingVaries(samples []sample) bool {
	if len(samples) < 2 {
		return false
	}
	first := samples[0].Mapped
	for _, value := range samples[1:] {
		if value.Mapped != first {
			return true
		}
	}
	return false
}

func mappedAddresses(samples []sample) []netip.AddrPort {
	seen := make(map[netip.AddrPort]bool)
	addresses := make([]netip.AddrPort, 0, len(samples))
	for _, value := range samples {
		if value.Mapped.IsValid() && !seen[value.Mapped] {
			seen[value.Mapped] = true
			addresses = append(addresses, value.Mapped)
		}
	}
	return addresses
}

func unknownResult(reason string) natResult {
	return natResult{
		Status:            "insufficient",
		Type:              "unknown",
		MappingBehavior:   "unknown",
		FilteringBehavior: "unknown",
		Predictability:    "unknown",
		ProbedAt:          time.Now().UTC().Format(time.RFC3339Nano),
		Reason:            reason,
	}
}

func printReport(result report) {
	fmt.Printf("NAT 类型：%s (%s)\n", natTypeLabel(result.NAT.Type), result.NAT.Type)
	fmt.Printf("映射行为：%s\n", behaviorLabel(result.NAT.MappingBehavior))
	fmt.Printf("过滤行为：%s\n", behaviorLabel(result.NAT.FilteringBehavior))
	if result.NAT.FilteringServer != "" {
		fmt.Printf("过滤测试 STUN：%s\n", result.NAT.FilteringServer)
	}
	fmt.Printf("端口变化可预测性：%s\n", predictabilityLabel(result.NAT.Predictability))
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

func statusLabel(value string) string {
	if value == "complete" {
		return "完成"
	}
	return "样本不足"
}
