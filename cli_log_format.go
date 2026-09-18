package main

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"hole/core"
)

func logServer(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "[无效地址]"
	}
	return u.Scheme + "://" + u.Host + u.EscapedPath()
}

// Escape terminal control characters while preserving readable Chinese.
func logText(value string) string {
	quoted := strconv.Quote(value)
	return quoted[1 : len(quoted)-1]
}

var cliStates = map[string]string{
	"starting": "启动中", "running": "运行中", "stopped": "已停止", "stopping": "正在停止", "closed": "已关闭",
	"connecting": "连接中", "checking": "探测中", "joining": "正在加入房间", "joined": "已加入房间", "disconnected": "未连接",
	"reconnecting": "正在重连", "recovering": "正在恢复", "waiting_peer": "等待对端", "waiting_network": "等待网络",
	"active": "已就绪", "ready": "已就绪", "switching": "正在切换", "paused": "对端离线",
	"pending": "等待中", "requesting": "正在申请中继凭据", "unavailable": "暂不可用", "off": "已关闭",
	"waiting_credentials": "等待中继凭据（尚未开始探测）", "retrying": "准备重试", "error": "异常", "changed": "已变化",
	"configured": "配置已加载", "applied": "配置已生效", "applying": "正在应用配置", "staged": "配置待应用", "unchanged": "配置未变化",
}

func logState(state string) string {
	if text, ok := cliStates[state]; ok {
		return text
	}
	return logText(state)
}

func logPhase(phase string) string {
	switch phase {
	case "direct":
		return "直连"
	case "relay_udp":
		return "UDP 中继"
	case "relay_tcp", "relay_tcp_80":
		return "TCP 中继"
	case "relay_tls", "relay_tls_443":
		return "TLS 中继"
	default:
		return "可用路径"
	}
}

func logPath(p core.PeerTransportSnapshot) string {
	switch p.PathType {
	case "direct":
		return "直连"
	case "relay":
		if p.RelaySide == "remote" {
			if p.RemoteRelayProtocol != "" {
				return strings.ToUpper(logText(p.RemoteRelayProtocol)) + " 对端中继"
			}
			return "对端中继"
		}
		if p.RelaySide == "both" && p.RelayProtocol != "" && p.RemoteRelayProtocol != "" && p.RelayProtocol != p.RemoteRelayProtocol {
			return strings.ToUpper(logText(p.RelayProtocol)) + "/" + strings.ToUpper(logText(p.RemoteRelayProtocol)) + " 中继"
		}
		if p.RelayProtocol != "" {
			return strings.ToUpper(logText(p.RelayProtocol)) + " 中继"
		}
		return "中继"
	default:
		return "已建立的路径"
	}
}

// Codes determine user-facing causes. Raw messages (which may contain socket
// internals or protocol identifiers) remain available in debug output.
func logFault(f *core.Fault) string {
	if f == nil {
		return "原因尚未确认，可加 -debug 查看详情"
	}
	causes := map[string]string{
		"member_limit": "房间在线设备数量已达上限", "duplicate_provide_id": "服务名称重复，请修改共享服务的 ID",
		"stale_client_epoch": "设备连接状态已更新，正在重新同步", "not_joined": "尚未加入房间",
		"invalid_join": "加入房间的配置无效，请检查设备名称和服务配置", "server_error": "协调服务器处理请求失败",
		"turn_rate_limited": "中继申请过于频繁", "transport_not_authorized": "对端连接尚未获确认",
		"network_error": "网络连接异常", "address_in_use": "本地端口已被占用，请关闭占用程序或修改端口",
		"signal_auth_failed": "协调服务器密码不匹配，请检查 password 配置",
		"auth_failed":        "房间认证失败，请检查房间凭据", "invalid_token": "房间凭据不匹配，请检查 token 配置",
		"signal_backpressure": "协调消息暂时拥塞", "worker_upgrade_required": "协调服务器版本过旧，请更新 Worker",
		"peer_identity_mismatch": "对端身份校验未通过，请检查设备配置",
		"protocol_mismatch":      "双方连接协议不兼容，请检查客户端和协调服务器版本",
		"transport_limit":        "对端连接数量已达上限", "no_ipv6": "未找到可用的 IPv6 地址，请检查网络或改用自动连接",
		"ice_network_adapter_missing": "当前平台缺少所需的网络适配",
		"ice_path_failed":             "当前路径暂未连通", "relay_unavailable": "当前没有可用的中继服务器",
		"relay_credentials_unavailable": "中继连接信息尚未就绪", "turn_request_timeout": "获取中继连接信息超时",
		"service_refused":      "目标服务拒绝连接，请检查服务是否启动",
		"service_timeout":      "连接目标服务超时，请检查服务地址和网络",
		"service_unavailable":  "目标服务暂不可用，请检查服务状态",
		"session_open_timeout": "建立服务连接超时，本次连接已结束",
		"session_expired":      "会话已过期，请重新连接服务", "session_limit": "服务会话数量已达上限",
		"session_closed": "会话已关闭，请重新连接服务", "session_canceled": "本次服务连接已取消",
		"session_transport_interrupted": "会话传输中断，正在尝试续接",
		"mapping_not_authorized":        "服务映射未获确认，请检查双方服务配置",
		"stale_generation":              "会话连接状态已更新，等待重新同步",
		"invalid_handshake":             "服务连接握手未通过，请检查客户端版本",
		"invalid_session_state":         "服务会话状态不一致，请重新连接服务",
	}
	if text, ok := causes[f.Code]; ok {
		return text
	}
	return "操作未成功，可加 -debug 查看具体原因"
}

func logSessionFault(e core.Event) string {
	message := logFault(e.Error)
	if e.Target != "" {
		message = "目标 " + logText(e.Target) + "：" + message
	}
	return message
}

func (r *cliReporter) debugEvent(e core.Event) {
	if !r.debug {
		return
	}
	if e.Kind == "log" {
		r.debugf("%s", logText(e.Message))
		return
	}
	kind := map[string]string{"engine": "引擎", "signal": "协调连接", "network": "网络", "mapping": "映射", "session": "会话", "turn": "中继", "config": "配置", "transport": "传输"}[e.Kind]
	if kind == "" {
		kind = logText(e.Kind)
	}
	line := kind + "：" + logState(e.State)
	for _, field := range []struct{ label, value string }{
		{"映射", e.MappingID}, {"协议", e.Protocol}, {"对端", e.Peer}, {"目标", e.Target}, {"会话", e.SessionID}, {"阶段", e.Stage}, {"路径", e.Path}, {"探测阶段", e.Phase},
	} {
		if field.value != "" {
			line += " · " + field.label + " " + logText(field.value)
		}
	}
	if e.TransportGeneration > 0 {
		line += fmt.Sprintf(" · 代次 %d", e.TransportGeneration)
	}
	if e.Resume {
		line += " · 续接"
	}
	if e.Error != nil {
		line += " · 原因：" + logText(e.Error.Message) + " [" + logText(e.Error.Code) + "]"
	}
	if e.Message != "" {
		line += " · " + logText(e.Message)
	}
	r.debugf("%s", line)
}

func (r *cliReporter) debugPeer(p core.PeerTransportSnapshot) {
	if !r.debug {
		return
	}
	line := fmt.Sprintf("传输：对端「%s」 · %s · 探测阶段 %s · 代次 %d · 通道 %d/%d", logText(p.PeerID), logState(p.State), logText(p.Phase), p.Generation, p.ActiveChannels, p.MappingCount)
	if p.PendingPhase != "" {
		line += " · 正在尝试 " + logText(p.PendingPhase)
	}
	if p.PathType != "" {
		line += fmt.Sprintf(" · 实际路径 %s/%s · %s ↔ %s · 建连耗时 %d 毫秒", logPath(p), logText(p.AddressFamily), logText(p.LocalAddress), logText(p.RemoteAddress), p.ConnectMS)
		if p.RelaySide == "remote" && p.RemoteRelayProtocol == "" {
			line += " · 对端中继接入协议未上报"
		}
	}
	if p.RetryCount > 0 {
		line += fmt.Sprintf(" · 累计重试 %d 次", p.RetryCount)
	}
	if p.Error != nil {
		line += " · 原因：" + logText(p.Error.Message) + " [" + logText(p.Error.Code) + "]"
	}
	r.debugf("%s", line)
}
