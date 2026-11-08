package core

import (
	"fmt"
	"net/netip"
	"strings"
	"time"

	"hole/core/internal/nat"
	"hole/core/natprobe"
)

// Shared explanatory text keeps all graphical hosts faithful to the same evidence.
func (d NATDiagnostic) view() NATDiagnostic {
	d.Result.IPEvidence = append([]natprobe.IPEvidence(nil), d.Result.IPEvidence...)
	d.Network = d.Network.clone()
	d.Servers = append([]string{}, d.Servers...)
	d.Mappings = append([]string{}, d.Mappings...)
	d.Targets = append([]nat.ProbeTarget{}, d.Targets...)
	for i := range d.Targets {
		d.Targets[i].Mappings = append([]string{}, d.Targets[i].Mappings...)
	}
	d.Sections = []DiagnosticSection{}
	s := d.Result
	if d.State == "" {
		d.State = "idle"
	}
	if d.State == "complete" || d.State == "insufficient" {
		if s.Current().Status == "expired" {
			d.State = "expired"
		}
	}
	d.Title, d.Summary = "检测当前网络", "检查 IPv4 NAT 的映射与过滤方式，了解 UDP 打洞条件。无需加入房间。"
	kinds := map[string]string{"open": "公网直连", "firewall": "防火墙过滤", "full_cone": "完全锥型", "restricted_cone": "地址受限锥型", "port_restricted_cone": "端口受限锥型", "symmetric": "对称型", "endpoint_independent": "端点无关映射"}
	kinds["address_pool"] = "地址池"
	explanations := map[string]string{
		"open":                 "未观察到地址转换或过滤限制，UDP 直连条件较好。实际连接仍需双方检查可达性。",
		"firewall":             "未观察到地址转换，但存在入站过滤；通常需要双方先发送数据建立放行状态。",
		"full_cone":            "不同目标共用映射，且入站过滤较宽松，通常较容易建立 UDP 直连。",
		"restricted_cone":      "不同目标共用映射，但只接收曾主动联系过的 IP 发来的数据。",
		"port_restricted_cone": "不同目标共用映射，但只接收曾主动联系过的 IP 和端口发来的数据。",
		"symmetric":            "不同目标可能获得不同公网端口，普通打洞难度较高；能否预测取决于端口证据，必要时使用中继。",
		"endpoint_independent": "已确认不同目标共用映射；过滤证据尚不完整，不能进一步判定为哪一种锥型。",
		"address_pool":         "观测到多个出口 IP，组内样本尚不足以区分锥形或对称型；可按本轮出口作有限端口试探。",
	}
	switch d.State {
	case "queued":
		d.Title, d.Summary = "等待检测", "正在等待当前打洞操作释放探测资源，可随时取消。"
	case "probing":
		d.Title, d.Summary = "正在检测网络", "正在向 STUN 服务采集映射及过滤证据。本轮检测最多约 20 秒，结果以实际响应为准。"
	case "complete":
		d.Title, d.Summary = kinds[s.Type], explanations[s.Type]
	case "insufficient":
		d.Title, d.Summary = "检测信息不足", "有效响应或过滤证据不足，暂时无法完整判断。查看下方检测说明后可重试。"
	case "failed":
		d.Title, d.Summary = "检测未完成", "当前网络未能完成检测，请查看检测说明并重试。"
	case "cancelled":
		d.Title, d.Summary = "检测已取消", "本次采集已停止，未完成的数据不能作为当前网络的完整结论。"
	case "network_changed":
		d.Title, d.Summary = "网络已变化", "下方记录属于检测时的网络。请重新检测当前网络。"
	case "expired":
		d.Title, d.Summary = "检测结果已过期", "距检测已超过 5 分钟。路由或端口分配可能变化，建议重新检测。"
	case "not_applicable":
		d.Title, d.Summary = "当前网络不适用", "此检测针对 IPv4 NAT；当前网络没有 IPv4 地址，不能据此判断 IPv6 是否可直连。"
	}
	if d.Title == "" {
		d.Title = "暂无法判定"
	}
	d.Tone, d.Badge = "neutral", "待检测"
	d.Highlights = []DiagnosticTile{}
	if d.State != "idle" {
		d.Badge = "证据待确认"
	}
	if d.busy() {
		d.Badge = "检测中"
	}
	if d.State == "expired" || d.State == "network_changed" {
		d.Badge = "需重新检测"
	}
	if d.State == "complete" {
		switch s.Type {
		case "open", "full_cone":
			d.Tone, d.Badge = "green", "直连条件较好"
		case "restricted_cone", "port_restricted_cone", "firewall":
			d.Tone, d.Badge = "blue", "入站受限"
		case "symmetric", "address_pool":
			switch s.Predictability {
			case "predictable_incremental", "predictable_decremental":
				d.Tone, d.Badge = "yellow", "可预测 · 有限样本"
			case "incremental", "decremental":
				d.Tone, d.Badge = "yellow", "存在端口趋势"
			case "unverified":
				d.Badge = "待验证"
			case "irregular":
				d.Tone, d.Badge = "red", "不可预测 · 本次采样"
			}
		}
	}
	if d.State == "idle" {
		return d
	}
	label := func(value string, values map[string]string) string {
		if text := values[value]; text != "" {
			return text
		}
		return "未知 / 尚未确认"
	}
	field := func(name, value, detail string) DiagnosticField {
		if value == "" {
			value = "暂无"
		}
		return DiagnosticField{name, value, detail}
	}
	behavior := map[string]string{"endpoint_independent": "端点无关", "endpoint_dependent": "端点相关", "address_dependent": "地址相关", "address_port_dependent": "地址与端口相关"}
	var publicIPs []string
	seen := map[string]bool{}
	for _, raw := range d.Mappings {
		if endpoint, err := netip.ParseAddrPort(raw); err == nil && !seen[endpoint.Addr().String()] {
			ip := endpoint.Addr().String()
			publicIPs = append(publicIPs, ip)
			seen[ip] = true
		}
	}
	if len(publicIPs) == 0 && s.PublicIP != "" {
		publicIPs = append(publicIPs, s.PublicIP)
	}
	d.Sections = append(d.Sections, DiagnosticSection{"映射与过滤", []DiagnosticField{
		field("NAT 类型", label(s.Type, kinds), "类型来自本次 IPv4 UDP 检测，不代表 IPv6 路径或实际直连结果。"),
		field("映射行为", label(s.MappingBehavior, behavior), "端点无关表示联系不同目标时仍使用同一公网映射；端点相关表示映射随目标变化。"),
		field("过滤行为", label(s.FilteringBehavior, behavior), "描述哪些来源能向映射发送数据。未知表示缺少可验证的过滤响应，不等于无限制。"),
		field("公网 IPv4", strings.Join(publicIPs, "\n"), "本次实际映射响应中的出口地址；运营商地址池可能提供多个出口。"),
		field("观测到的公网映射", strings.Join(d.Mappings, "\n"), "本轮去重后的 IP:端口；这些是探测连接的映射，不是业务连接的固定入口。"),
		field("预测基准端口", positiveNumber(s.LastMappingPort), "仅在形成递增分配模型时提供；它可能来自后续新 socket 验证，不一定在原 socket 的映射列表中。"),
	}})
	d.Sections = append(d.Sections, DiagnosticSection{"端口特征", []DiagnosticField{
		field("端口规律", label(s.Predictability, map[string]string{"stable": "映射稳定", "incremental": "可能递增", "decremental": "可能递减", "predictable_incremental": "已验证递增趋势", "predictable_decremental": "已验证递减趋势", "irregular": "未观察到规律", "unverified": "待验证"}), "趋势与可预测性是不同证据等级；地址池中的趋势仅属于对应 IP，出现规律也不保证实际打洞成功。"),
		field("端口步长", positiveNumber(s.PortStep), "递增模型采用不同映射端口间最小非零差值，并校验步长余数；没有模型时保留未知。"),
		field("分配范围", label(s.AllocationScope, map[string]string{"same_socket": "同一 socket", "cross_socket": "跨 socket"}), "说明端口规律是否在新建 UDP 连接后仍成立。"),
		field("端口保持", label(s.PortPreservation, map[string]string{"mixed": "部分保持", "translated": "发生端口转换", "preserved": "已观察到保持"}), "检查公网映射端口是否与本地 UDP 端口相同。"),
		field("无端口转换（No-PAT）", label(s.NoPAT, map[string]string{"not_detected": "未检测到", "suspected": "疑似 No-PAT", "not_applicable": "不适用（未发生地址转换）"}), "疑似结论来自映射与端口保持证据，不作为确定的网络类型。"),
	}})
	if len(s.IPEvidence) > 1 {
		for _, evidence := range s.IPEvidence {
			trend := label(evidence.Trend, map[string]string{"stable": "端口稳定", "incremental": "可能递增", "decremental": "可能递减", "unknown": "待验证", "irregular": "未观察到规律"})
			detail := fmt.Sprintf("%d 个不同目标，%d 个不同端口。", evidence.Targets, evidence.Ports)
			if evidence.Step > 0 {
				detail += fmt.Sprintf("观测步长 %d。", evidence.Step)
			}
			detail += "仅用于本轮校准命中该 IP 时的有限试探；样本不足默认递增，已观察到不规则则跳过。"
			d.Sections[1].Fields = append(d.Sections[1].Fields, field(evidence.IP, trend, detail))
		}
	} else if s.PortStep == 0 && !d.busy() {
		detail := "需要同一公网 IP 上至少 3 个不同目标的递增映射、至少 2 个目标 IP，以及稳定的重复响应。类型相同不代表这些条件已满足。"
		if s.Type != "symmetric" && s.Type != "unknown" {
			detail = "当前映射类型不使用对称型 NAT 的递增端口模型，因此不提供预测基准端口、步长和分配范围。"
		}
		d.Sections[1].Fields = append(d.Sections[1].Fields, field("预测模型说明", "未建立递增分配模型", detail))
	}
	d.Sections = append(d.Sections, DiagnosticSection{"检测时的网络", []DiagnosticField{
		field("网络", d.Network.Transport, ""), field("网卡", d.Network.Interface, "桌面端由系统路由选择实际出口；下方列出允许接口的地址。"),
		field("IPv4 / IPv6 地址", strings.Join(d.Network.Addresses, "\n"), "这些是本机地址。存在 IPv6 地址不等于已经验证公网可达。"),
		field("DNS", strings.Join(d.Network.DNS, "\n"), "未提供时使用系统解析器。"),
	}})
	duration := "检测中"
	if start, err := time.Parse(time.RFC3339Nano, d.StartedAt); err == nil && d.FinishedAt != "" {
		if end, err := time.Parse(time.RFC3339Nano, d.FinishedAt); err == nil {
			duration = fmt.Sprintf("%.1f 秒", end.Sub(start).Seconds())
		}
	}
	d.Sections = append(d.Sections, DiagnosticSection{"检测记录", []DiagnosticField{
		field("有效样本", fmt.Sprint(s.Samples), "成功收到的基础映射样本数；不能当作检测进度或直连成功率。"),
		field("开始时间", d.StartedAt, "系统时区"), field("完成时间", d.FinishedAt, "系统时区"), field("总耗时", duration, "含等待资源的时间。"),
		field("检测说明", s.Reason, ""), field("过滤测试服务", s.FilteringServer, "需支持并实际响应过滤测试，才能提供过滤结论。"),
		field("配置的 STUN 服务", strings.Join(d.Servers, "\n"), "列出本轮配置的目标；不表示每个目标都已响应。"),
	}})
	responding := 0
	var targetFields []DiagnosticField
	for _, target := range d.Targets {
		if target.Samples > 0 {
			responding++
		}
		value := fmt.Sprintf("有效样本 %d / 3 · 不同映射 %d", target.Samples, len(target.Mappings))
		if target.Endpoint != "" {
			value = target.Endpoint + "\n" + value
		}
		detail := strings.Join(target.Mappings, "\n")
		if target.Reason != "" {
			detail += "\n" + target.Reason
		}
		targetFields = append(targetFields, field(target.URL, value, strings.TrimSpace(detail)))
	}
	d.Sections[3].Fields = append(d.Sections[3].Fields,
		field("有响应的目标", fmt.Sprint(responding), "按解析后的 IPv4:端口去重；同一目标的 3 轮响应仍只算 1 个目标。"),
		field("不同公网映射", fmt.Sprint(len(d.Mappings)), "原 socket 的 IP:端口去重数量；重复响应增加有效样本数，不增加不同映射数。"))
	if len(targetFields) > 0 {
		d.Sections = append(d.Sections, DiagnosticSection{"STUN 服务明细", targetFields})
	}
	if !d.busy() {
		d.Highlights = []DiagnosticTile{
			{"mapping", "映射行为", d.Sections[0].Fields[1].Value},
			{"shield", "过滤行为", d.Sections[0].Fields[2].Value},
			{"trend", "端口规律", d.Sections[1].Fields[0].Value},
			{"steps", "端口步长", positiveNumber(s.PortStep)},
			{"samples", "有效样本", fmt.Sprint(s.Samples)},
			{"network", "不同公网映射", fmt.Sprint(len(d.Mappings))},
		}
	}
	return d
}

func (d NATDiagnostic) busy() bool { return d.State == "queued" || d.State == "probing" }

func positiveNumber(value int) string {
	if value <= 0 {
		return "未知 / 尚未确认"
	}
	return fmt.Sprint(value)
}
