package main

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"hole/core"
)

const (
	logLevelInfo  = "INFO  "
	logLevelWarn  = "WARN  "
	logLevelError = "ERROR "
	logLevelDebug = "DEBUG "
)

func logServer(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "[invalid-server]"
	}
	return u.Scheme + "://" + u.Host + u.EscapedPath()
}

// Escape terminal control characters while preserving readable Chinese.
func logText(value string) string {
	quoted := strconv.Quote(value)
	return quoted[1 : len(quoted)-1]
}

func logIdent(value string) string {
	if value == "" || strings.ContainsAny(value, " \t\r\n:()") {
		return strconv.Quote(value)
	}
	return logText(value)
}

func logValue(value string) string {
	if strings.ContainsAny(value, " \t\r\n=()") {
		return strconv.Quote(value)
	}
	return logText(value)
}

func logDuration(milliseconds int64) string {
	if milliseconds < 1000 {
		return fmt.Sprintf("%dms", milliseconds)
	}
	return fmt.Sprintf("%.1fs", float64(milliseconds)/1000)
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
		return ""
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
		return ""
	}
}

func logRoute(p core.PeerTransportSnapshot) string {
	path := logPath(p)
	if path == "" {
		return ""
	}
	if p.AddressFamily != "" {
		return path + "/" + logText(p.AddressFamily)
	}
	return path
}

func logCause(f *core.Fault) string {
	if f == nil {
		return ""
	}
	causes := map[string]string{
		"member_limit": "房间设备数已达上限", "duplicate_provide_id": "服务 ID 重复",
		"stale_client_epoch": "设备状态已更新，重新同步", "not_joined": "尚未加入房间",
		"invalid_join": "加入房间的参数无效", "server_error": "信令服务器内部错误",
		"turn_rate_limited": "中继申请过于频繁", "transport_not_authorized": "对端连接未获确认",
		"network_error": "网络错误", "address_in_use": "本地端口被占用",
		"signal_auth_failed": "信令密码错误，检查 password", "auth_failed": "房间认证失败",
		"invalid_token": "房间 token 错误，检查 token", "signal_backpressure": "信令消息拥塞",
		"worker_upgrade_required": "信令服务器版本过旧，需更新 Worker",
		"peer_identity_mismatch":  "对端身份校验失败", "protocol_mismatch": "连接协议不兼容",
		"transport_limit": "对端连接数已达上限", "no_ipv6": "没有可用的 IPv6 地址",
		"ice_network_adapter_missing": "缺少网络适配", "ice_path_failed": "路径未连通",
		"relay_unavailable": "没有可用的中继服务器", "relay_credentials_unavailable": "中继凭据未就绪",
		"turn_request_timeout": "申请中继凭据超时", "service_refused": "拒绝连接",
		"service_timeout": "连接超时", "service_unavailable": "不可用",
		"session_open_timeout": "建立会话超时", "session_expired": "会话已过期",
		"session_limit": "会话数已达上限", "session_closed": "会话已关闭",
		"session_canceled": "会话已取消", "session_transport_interrupted": "会话传输中断",
		"mapping_not_authorized": "映射未获确认", "stale_generation": "连接状态已更新，等待同步",
		"invalid_handshake": "握手失败", "invalid_session_state": "会话状态不一致",
	}
	return causes[f.Code]
}

func logDetail(f *core.Fault) string {
	if f == nil {
		return "原因未知"
	}
	message := logText(f.Message)
	if message == "" || message == logText(f.Code) {
		return logText(f.Code)
	}
	return logText(f.Code) + ": " + message
}

func logFault(f *core.Fault) string {
	return logDetail(f)
}

func logAppendField(line, name, value string) string {
	if value == "" {
		return line
	}
	return line + " " + name + "=" + logValue(value)
}

func (r *cliReporter) debugEvent(e core.Event) {
	if !r.debug {
		return
	}
	if e.Kind == "log" {
		r.debugf("core %s", logText(e.Message))
		return
	}
	line := logText(e.Kind) + " " + logValue(e.State)
	line = logAppendField(line, "mapping", e.MappingID)
	line = logAppendField(line, "proto", e.Protocol)
	line = logAppendField(line, "peer", e.Peer)
	line = logAppendField(line, "target", e.Target)
	line = logAppendField(line, "session", e.SessionID)
	line = logAppendField(line, "stage", e.Stage)
	line = logAppendField(line, "path", e.Path)
	line = logAppendField(line, "phase", e.Phase)
	if e.TransportGeneration > 0 {
		line += fmt.Sprintf(" gen=%d", e.TransportGeneration)
	}
	if e.Resume {
		line += " resume=true"
	}
	if e.Error != nil {
		line += " err=" + logValue(e.Error.Code) + " msg=" + strconv.Quote(logText(e.Error.Message))
	}
	line = logAppendField(line, "message", e.Message)
	r.debugf("%s", line)
}

func (r *cliReporter) debugPeer(p core.PeerTransportSnapshot) {
	if !r.debug {
		return
	}
	line := logText(p.PeerID) + " state=" + logValue(p.State) + " phase=" + logValue(p.Phase)
	line += fmt.Sprintf(" gen=%d channels=%d/%d", p.Generation, p.ActiveChannels, p.MappingCount)
	line = logAppendField(line, "pending", p.PendingPhase)
	if p.PathType != "" {
		line += " path=" + logValue(logRoute(p))
		if p.RelaySide != "" {
			line += " relay_side=" + logValue(p.RelaySide)
			if p.RelaySide == "remote" && p.RemoteRelayProtocol == "" {
				line += " remote_relay_proto=unreported"
			} else if p.RelaySide == "remote" {
				line += " remote_relay_proto=" + logValue(p.RemoteRelayProtocol)
			} else if p.RelayProtocol != "" {
				line += " relay_proto=" + logValue(p.RelayProtocol)
			}
		}
		if p.AddressFamily != "" {
			line += " family=" + logValue(p.AddressFamily)
		}
		if p.LocalAddress != "" {
			line += " local=" + logValue(p.LocalAddress)
		}
		if p.RemoteAddress != "" {
			line += " remote=" + logValue(p.RemoteAddress)
		}
		if p.ConnectMS > 0 {
			line += " connect=" + logDuration(p.ConnectMS)
		}
	}
	if p.DatagramLimit > 0 {
		line += fmt.Sprintf(" datagram_limit=%d", p.DatagramLimit)
	}
	if p.RetryCount > 0 {
		line += fmt.Sprintf(" retries=%d", p.RetryCount)
	}
	if p.Error != nil {
		line += " err=" + logValue(p.Error.Code) + " msg=" + strconv.Quote(logText(p.Error.Message))
	}
	r.debugf("%s", line)
	for _, pair := range p.CandidatePairs {
		line := "pair " + logValue(pair.LocalType) + " " + logValue(pair.LocalAddress) + " -> " + logValue(pair.RemoteType) + " " + logValue(pair.RemoteAddress)
		line += " " + logValue(pair.State) + fmt.Sprintf(" legs=%d", pair.RelayLegs)
		if pair.RTTMS > 0 {
			line += " rtt=" + logDuration(pair.RTTMS)
		}
		if pair.Selected {
			line += " selected"
		}
		r.debugf("%s", line)
	}
}
