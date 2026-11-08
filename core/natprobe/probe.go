// SPDX-License-Identifier: GPL-3.0-only
// Package natprobe implements the shared UDP STUN diagnostics used by Hole and its CLI tool.
package natprobe

import (
	"context"
	"github.com/pion/stun/v4"
	"net"
	"net/netip"
	"time"
)

const (
	probeTimeout  = 1200 * time.Millisecond
	probeRounds   = 3
	probeInterval = 100 * time.Millisecond
	probeRTO      = 300 * time.Millisecond
	probeAttempts = 3
	maxPortSpan   = 1024
)

type Sample struct {
	Server, Source, Mapped netip.AddrPort
	Other                  netip.AddrPort
	Change                 byte
}

type Snapshot struct {
	Status            string `json:"status"`
	Type              string `json:"type"`
	MappingBehavior   string `json:"mapping_behavior"`
	FilteringBehavior string `json:"filtering_behavior"`
	FilteringServer   string `json:"filtering_server,omitempty"`
	Predictability    string `json:"predictability"`
	PortStep          int    `json:"port_step,omitempty"`
	PublicIP          string `json:"public_ip,omitempty"`
	LastMappingPort   int    `json:"last_mapping_port,omitempty"`
	AllocationScope   string `json:"allocation_scope,omitempty"`
	PortPreservation  string `json:"port_preservation"`
	NoPAT             string `json:"no_pat"`
	Samples           int    `json:"samples"`
	ProbedAt          string `json:"probed_at"`
	Reason            string `json:"reason,omitempty"`
	Network           string `json:"network,omitempty"`
	Epoch             uint64 `json:"epoch,string"`
}

const Lifetime = 5 * time.Minute

func (s Snapshot) Current() Snapshot {
	if t, err := time.Parse(time.RFC3339Nano, s.ProbedAt); err == nil && time.Since(t) >= Lifetime {
		s.Status = "expired"
	}
	return s
}

func ProbeWithListener(ctx context.Context, servers []*net.UDPAddr, listen func(string, string) (net.PacketConn, error)) (string, Snapshot, []netip.AddrPort) {
	result := Unknown("有效 STUN 样本不足")
	if len(servers) == 0 {
		result.Reason = "没有可用的 IPv4 STUN 地址"
		return "", result, nil
	}
	pc, err := listen("udp4", "0.0.0.0:0")
	if err != nil {
		result.Reason = "创建 UDP socket 失败：" + err.Error()
		return "", result, nil
	}
	defer pc.Close()

	return ProbeSocket(ctx, pc, servers, listen, localSocket(ctx, pc, servers[0]))
}

// ProbeSocket leaves the caller's socket open with its deadlines cleared.
// listen must use the same network as pc; localEndpoint is the selected local IPv4 endpoint.
func ProbeSocket(ctx context.Context, pc net.PacketConn, servers []*net.UDPAddr, listen func(string, string) (net.PacketConn, error), localEndpoint netip.AddrPort) (string, Snapshot, []netip.AddrPort) {
	return ProbeSocketObserved(ctx, pc, servers, listen, localEndpoint, nil)
}

// ProbeSocketObserved reports each base mapping response/error synchronously.
// Extra sockets validate the model and are deliberately excluded from base counts.
func ProbeSocketObserved(ctx context.Context, pc net.PacketConn, servers []*net.UDPAddr, listen func(string, string) (net.PacketConn, error), localEndpoint netip.AddrPort, observe func(netip.AddrPort, Sample, error)) (string, Snapshot, []netip.AddrPort) {
	localText := pc.LocalAddr().String()
	if localEndpoint.IsValid() {
		localText = localEndpoint.String()
	}

	samples := make([]Sample, 0, len(servers)*probeRounds+4)
	baseline := make(map[netip.AddrPort]Sample, len(servers))
	var lastRequest time.Time
	for _, server := range servers {
		if ctx.Err() != nil {
			break
		}
		for round := 0; round < probeRounds && ctx.Err() == nil; round++ {
			value, err := pacedBinding(ctx, pc, server, 0, &lastRequest)
			if observe != nil {
				observe(addrPort(server), value, err)
			}
			if err != nil {
				break
			}
			samples = append(samples, value)
			baseline[value.Server] = value
		}
	}

	var extra *Sample
	if ctx.Err() == nil && mappingVaries(samples) && len(servers) > 1 {
		fresh, err := listen("udp4", "0.0.0.0:0")
		if err == nil {
			defer fresh.Close()
			// Verify against a responding destination; the first configured
			// server may have failed while later servers supplied the trend.
			server := net.UDPAddrFromAddrPort(samples[0].Server)
			if value, e := pacedBinding(ctx, fresh, server, 0, &lastRequest); e == nil {
				extra = &value
			}
		}
	}

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
		filteringBehavior, _, _, filteringReason = probeFiltering(ctx, server, listen, &lastRequest)
		if filteringBehavior != "unknown" {
			break
		}
	}

	result := Classify(localEndpoint, samples, extra, filteringBehavior, filteringReason)
	// Validate on additional, held-out sockets and different destinations.
	// Keep their local ports reserved until discovery finishes.
	if result.AllocationScope == "cross_socket" {
		var held []net.PacketConn
		defer func() {
			for _, conn := range held {
				_ = conn.Close()
			}
		}()
		for i := 0; i < 3; i++ {
			fresh, err := listen("udp4", "0.0.0.0:0")
			if err != nil {
				result.AllocationScope = "same_socket"
				break
			}
			held = append(held, fresh)
			server := net.UDPAddrFromAddrPort(samples[(i+1)*probeRounds%len(samples)].Server)
			value, err := pacedBinding(ctx, fresh, server, 0, &lastRequest)
			if err != nil || !advanceModel(&result, value) {
				result.AllocationScope = "same_socket"
				break
			}
			reused, err := pacedBinding(ctx, fresh, server, 0, &lastRequest)
			if err != nil || reused.Mapped != value.Mapped {
				result.AllocationScope = "unknown"
				break
			}
		}
		if result.AllocationScope != "cross_socket" {
			result.Reason += "；额外跨 socket 验证未通过，禁用固定目标多 socket 阶段"
		}
	}
	result.FilteringServer = filteringServer
	if ctx.Err() != nil && result.Status == "insufficient" {
		result.Reason = "探测总时限已到"
	}
	return localText, result, mappedAddresses(samples)
}

func Binding(ctx context.Context, pc net.PacketConn, server *net.UDPAddr, change byte, timeout time.Duration) (Sample, error) {
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
	buf := make([]byte, 2048)
	rto := probeRTO
	for attempt := 0; attempt < probeAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return Sample{}, err
		}
		readDeadline := deadline
		if retry := time.Now().Add(rto); attempt < probeAttempts-1 && retry.Before(deadline) {
			readDeadline = retry
		}
		_ = pc.SetReadDeadline(readDeadline)
		if err := ctx.Err(); err != nil {
			return Sample{}, err
		}
		if _, err := pc.WriteTo(request.Raw, server); err != nil {
			return Sample{}, err
		}
		for ctx.Err() == nil {
			n, source, err := pc.ReadFrom(buf)
			if err != nil {
				if ctx.Err() != nil {
					return Sample{}, ctx.Err()
				}
				if isTimeout(err) && readDeadline.Before(deadline) {
					break
				}
				return Sample{}, err
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
			return Sample{Server: to, Source: from, Mapped: mapped, Other: otherAddress(message), Change: change}, nil
		}
		rto *= 2
	}
	return Sample{}, context.DeadlineExceeded
}

func isTimeout(err error) bool {
	timeout, ok := err.(net.Error)
	return ok && timeout.Timeout()
}

func pacedBinding(ctx context.Context, pc net.PacketConn, server *net.UDPAddr, change byte, lastRequest *time.Time) (Sample, error) {
	if !lastRequest.IsZero() {
		if wait := time.Until(lastRequest.Add(probeInterval)); wait > 0 {
			timer := time.NewTimer(wait)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return Sample{}, ctx.Err()
			case <-timer.C:
			}
		}
	}
	value, err := Binding(ctx, pc, server, change, probeTimeout)
	*lastRequest = time.Now()
	return value, err
}

func filteringProbe(ctx context.Context, pc net.PacketConn, primary, other netip.AddrPort, lastRequest *time.Time) (string, *Sample, *Sample, string) {
	unknown := func(reason string) (string, *Sample, *Sample, string) {
		return "unknown", nil, nil, reason
	}
	if !usableOtherAddress(other, primary) {
		return unknown("STUN 服务未提供有效的 RFC 5780 OTHER-ADDRESS")
	}
	changed, err := pacedBinding(ctx, pc, net.UDPAddrFromAddrPort(primary), 6, lastRequest)
	if ctx.Err() != nil {
		return unknown("过滤探测总时限已到")
	}
	if err == nil {
		if changed.Source != other {
			return unknown("STUN 服务未按 CHANGE-REQUEST 变更响应来源，过滤行为无法判定")
		}
		return "endpoint_independent", &changed, nil, ""
	}
	if !isTimeout(err) {
		return unknown("过滤探测失败：" + err.Error())
	}
	portChanged := netip.AddrPortFrom(primary.Addr(), other.Port())
	changed, err = pacedBinding(ctx, pc, net.UDPAddrFromAddrPort(primary), 2, lastRequest)
	if ctx.Err() != nil {
		return unknown("过滤探测总时限已到")
	}
	if err == nil {
		if changed.Source != portChanged {
			return unknown("STUN 服务未按 CHANGE-REQUEST 变更响应来源，过滤行为无法判定")
		}
		return "address_dependent", nil, &changed, ""
	}
	if !isTimeout(err) {
		return unknown("过滤探测失败：" + err.Error())
	}
	// RFC 5780's alternate-address check distinguishes restrictive filtering
	// from a path that cannot reach the server's alternate IP at all.
	alternateIP := netip.AddrPortFrom(other.Addr(), primary.Port())
	if check, err := pacedBinding(ctx, pc, net.UDPAddrFromAddrPort(alternateIP), 0, lastRequest); err == nil && ctx.Err() == nil && check.Source == alternateIP {
		return "address_port_dependent", nil, nil, ""
	}
	return unknown("STUN 服务支持 OTHER-ADDRESS，但备用地址连通性未能确认，过滤行为无法判定")
}

// Each filtering attempt starts with a new socket and contacts only the primary
// endpoint before CHANGE-REQUEST, so mapping probes cannot pre-open permissions.
func probeFiltering(ctx context.Context, server *net.UDPAddr, listen func(string, string) (net.PacketConn, error), lastRequest *time.Time) (string, *Sample, *Sample, string) {
	pc, err := listen("udp4", "0.0.0.0:0")
	if err != nil {
		return "unknown", nil, nil, "创建过滤探测 UDP socket 失败：" + err.Error()
	}
	defer pc.Close()
	initial, err := pacedBinding(ctx, pc, server, 0, lastRequest)
	if err != nil {
		return "unknown", nil, nil, "过滤探测基础请求失败：" + err.Error()
	}
	return filteringProbe(ctx, pc, addrPort(server), initial.Other, lastRequest)
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
		other.Addr().Is4() && other.Addr() != primary.Addr() && other.Port() != primary.Port()
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
	if address := addrPort(local); address.IsValid() && !address.Addr().IsUnspecified() {
		return address
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

func Classify(local netip.AddrPort, samples []Sample, extra *Sample, filteringBehavior, filteringReason string) Snapshot {
	result := Unknown("有效 STUN 目标不足，无法确定 NAT 映射行为")
	result.FilteringBehavior = filteringBehavior
	servers := make(map[netip.AddrPort]netip.AddrPort)
	serverIPs := make(map[netip.Addr]bool)
	mappings := make(map[netip.AddrPort]bool)
	publicIPs := make(map[netip.Addr][]Sample)
	ports := make(map[uint16]bool)
	unstable := false
	localKnown := local.IsValid() && local.Addr().Is4() && !local.Addr().IsUnspecified() && local.Port() != 0
	preserved, translated := false, false
	for _, value := range samples {
		if !mappingSample(value) {
			continue
		}
		result.Samples++
		if previous, ok := servers[value.Server]; ok && previous != value.Mapped {
			unstable = true
		}
		servers[value.Server] = value.Mapped
		serverIPs[value.Server.Addr()] = true
		mappings[value.Mapped] = true
		publicIPs[value.Mapped.Addr()] = append(publicIPs[value.Mapped.Addr()], value)
		ports[value.Mapped.Port()] = true
		if localKnown {
			if value.Mapped.Port() == local.Port() {
				preserved = true
			} else {
				translated = true
			}
		}
	}
	// Port preservation is an observation even when mapping classification is
	// inconclusive. A single translated port rules out pure No-PAT for this path.
	switch {
	case preserved && translated:
		result.PortPreservation = "mixed"
	case translated:
		result.PortPreservation = "translated"
	case preserved:
		result.PortPreservation = "preserved"
	}
	if translated {
		result.NoPAT = "not_detected"
	}
	if len(publicIPs) > 1 {
		// An address pool can rotate IPs even for the same destination. Apply
		// the usual evidence requirements within each IP before considering
		// that rotation unstable. Keep aggregate observations on the result.
		var selected Snapshot
		var selectedIP netip.Addr
		for ip, group := range publicIPs {
			// Multiple external IPs rule out calling the whole path open or
			// untranslated, even if one of them matches the local endpoint.
			candidate := Classify(netip.AddrPort{}, group, nil, filteringBehavior, "")
			if candidate.Status != "complete" {
				continue
			}
			// Positive evidence of destination-dependent ports takes priority
			// over stable groups. Break ties by IP for deterministic reporting.
			if !selectedIP.IsValid() || (candidate.Type == "symmetric" && selected.Type != "symmetric") ||
				(candidate.Type == selected.Type && ip.Less(selectedIP)) {
				selected, selectedIP = candidate, ip
			}
		}
		result.Reason = "探测到多个公网 IP，可能来自运营商地址池"
		if selectedIP.IsValid() {
			result.Status = "complete"
			result.Type = selected.Type
			result.MappingBehavior = selected.MappingBehavior
			result.Reason += "；根据公网 IP " + selectedIP.String() + " 的组内映射行为分类"
		} else {
			result.Reason += "；各 IP 组内样本不足或映射不稳定，无法确定 NAT 类型"
		}
		if len(ports) == 1 {
			result.Predictability = "stable"
		} else {
			if portTrend(samples, extra) != "unknown" {
				result.Predictability = "irregular"
			}
			result.Reason += "；跨公网 IP 的端口变化无法确认可预测趋势"
		}
		if filteringReason != "" {
			result.Reason += "；" + filteringReason
		}
		return result
	}
	if unstable {
		result.Reason = "同一 STUN 目标的映射响应不稳定，无法确定映射行为"
		return result
	}
	if len(servers) < 2 || len(mappings) == 0 {
		return result
	}
	if len(mappings) == 1 && len(serverIPs) < 2 {
		result.Reason = "STUN 目标只有一个 IP，无法确认端点无关映射"
		return result
	}

	result.Status = "complete"
	if len(mappings) == 1 {
		result.MappingBehavior = "endpoint_independent"
		result.Predictability = "stable"
		if preserved && !translated {
			// A stable IP-only mapping is consistent with Basic NAT, but a PAT
			// that preserves ports produces the same limited STUN evidence.
			result.NoPAT = "suspected"
			if mappings[local] {
				result.NoPAT = "not_applicable"
			}
		}
		if local.IsValid() && mappings[local] {
			switch filteringBehavior {
			case "endpoint_independent":
				result.Type = "open"
				result.Reason = ""
			case "address_dependent", "address_port_dependent":
				result.Type = "firewall"
				result.Reason = "未检测到地址转换，但观测到防火墙过滤"
			default:
				result.Type = "endpoint_independent"
				result.Reason = "未检测到地址转换，过滤行为未知"
				if filteringReason != "" {
					result.Reason += "；" + filteringReason
				}
			}
			return result
		}
		if filteringBehavior == "endpoint_independent" {
			result.Type = "full_cone"
			result.Reason = ""
			return result
		}
		if filteringBehavior == "address_dependent" {
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
	allocationModel(&result, samples, extra)
	result.Predictability = "irregular"
	result.Reason = "不同 STUN 目标得到不同公网映射，跨 socket 样本未能确认可预测的端口趋势"
	if filteringReason != "" {
		result.Reason += "；" + filteringReason
	}
	switch portTrend(samples, extra) {
	case "predictable_incremental":
		result.Predictability = "predictable_incremental"
		result.Reason = "原 socket 至少 3 个不同目标的映射端口按探测顺序依次加 1，新 socket 在同一公网 IP 上继续加 1；据此判为可预测（有限样本）"
	case "predictable_decremental":
		result.Predictability = "predictable_decremental"
		result.Reason = "原 socket 至少 3 个不同目标的映射端口按探测顺序依次减 1，新 socket 在同一公网 IP 上继续减 1；据此判为可预测（有限样本）"
	case "incremental":
		result.Predictability = "incremental"
		result.Reason = "不同目标的映射端口整体递增，新 socket 在同一公网 IP 上延续递增趋势；仅为有限样本推断"
	case "decremental":
		result.Predictability = "decremental"
		result.Reason = "不同目标的映射端口整体递减，新 socket 在同一公网 IP 上延续递减趋势；仅为有限样本推断"
	case "unknown":
		result.Predictability = "unknown"
		result.Reason = "未取得有效的新 socket 映射样本，无法验证跨 socket 的端口变化趋势"
	}
	if result.Predictability != "irregular" && filteringBehavior == "unknown" && filteringReason != "" {
		result.Reason += "；" + filteringReason
	}
	return result
}

func portTrend(samples []Sample, extra *Sample) string {
	if extra == nil || !extra.Mapped.IsValid() || !extra.Mapped.Addr().Is4() || extra.Mapped.Port() == 0 {
		return "unknown"
	}
	targetPorts := make(map[netip.AddrPort]int)
	ordered := make([]int, 0, len(samples)+1)
	publicIP := netip.Addr{}
	for _, value := range samples {
		if !mappingSample(value) {
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

	baseMinimum, baseMaximum := ordered[0], ordered[0]
	for _, port := range ordered[1:] {
		baseMinimum = min(baseMinimum, port)
		baseMaximum = max(baseMaximum, port)
	}
	extraPort := int(extra.Mapped.Port())
	baseCount := len(ordered)
	ordered = append(ordered, extraPort)
	minimum, maximum := min(baseMinimum, extraPort), max(baseMaximum, extraPort)
	if maximum-minimum > maxPortSpan {
		return ""
	}
	// The fresh socket must confirm a strict trend, but cannot supply the
	// third destination needed to establish it on the original socket.
	if strict := strictPortTrend(ordered); baseCount >= 3 && strict != "" {
		return strict
	}
	increasing := extraPort > baseMaximum
	decreasing := extraPort < baseMinimum
	if !increasing && !decreasing {
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

func strictPortTrend(ports []int) string {
	if len(ports) < 3 {
		return ""
	}
	step := ports[1] - ports[0]
	if step != 1 && step != -1 {
		return ""
	}
	for i := 2; i < len(ports); i++ {
		if ports[i]-ports[i-1] != step {
			return ""
		}
	}
	if step == 1 {
		return "predictable_incremental"
	}
	return "predictable_decremental"
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

func mappingVaries(samples []Sample) bool {
	servers := make(map[netip.AddrPort]netip.AddrPort)
	mappings := make(map[netip.AddrPort]bool)
	for _, value := range samples {
		if !mappingSample(value) {
			continue
		}
		if previous, ok := servers[value.Server]; ok && previous != value.Mapped {
			return false
		}
		servers[value.Server] = value.Mapped
		mappings[value.Mapped] = true
	}
	return len(servers) > 1 && len(mappings) > 1
}

func mappingSample(value Sample) bool {
	return value.Change == 0 && value.Server.IsValid() && value.Server.Addr().Is4() &&
		value.Source == value.Server && value.Mapped.IsValid() && value.Mapped.Addr().Is4() && value.Mapped.Port() != 0
}

func mappedAddresses(samples []Sample) []netip.AddrPort {
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

func Unknown(reason string) Snapshot {
	return Snapshot{
		Status:            "insufficient",
		Type:              "unknown",
		MappingBehavior:   "unknown",
		FilteringBehavior: "unknown",
		Predictability:    "unknown",
		PortPreservation:  "unknown",
		NoPAT:             "unknown",
		ProbedAt:          time.Now().UTC().Format(time.RFC3339Nano),
		Reason:            reason,
	}
}
