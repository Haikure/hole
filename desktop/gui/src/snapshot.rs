//! 核心快照解析与展示文案；64 位计数保留十进制字符串，只在格式化时转换。
//! 文案与 Android `ConnectionText.kt` / `RuntimeDetailsScreen.kt` 对齐。

use serde_json::Value;
use std::time::{SystemTime, UNIX_EPOCH};

#[derive(Debug, Clone, Default, PartialEq)]
pub struct Fault {
    pub code: String,
    pub message: String,
}

fn fault(v: &Value) -> Option<Fault> {
    v.as_object().map(|o| Fault {
        code: o.get("code").and_then(Value::as_str).unwrap_or_default().into(),
        message: o.get("message").and_then(Value::as_str).unwrap_or_default().into(),
    })
}

#[derive(Debug, Clone, Default, PartialEq)]
pub struct Mapping {
    pub id: String,
    pub role: String,
    pub protocol: String,
    pub state: String,
    pub tcp_sessions: u64,
    pub udp_sessions: u64,
    pub error: Option<Fault>,
    pub endpoint: String,
    pub peer: String,
    pub path: String,
    pub tcp_read_bytes: String,
    pub tcp_written_bytes: String,
    pub read_bytes: String,
    pub written_bytes: String,
    pub replay_bytes: String,
    pub profile: String,
}

#[derive(Debug, Clone, Default, PartialEq)]
pub struct Peer {
    pub peer_id: String,
    pub transport_id: String,
    pub generation: String,
    pub profile: String,
    pub state: String,
    pub phase: String,
    pub pending_phase: String,
    pub path_type: String,
    pub address_family: String,
    pub local_relay_protocol: String,
    pub remote_relay_protocol: String,
    pub relay_side: String,
    pub relay_policy: String,
    pub local_address: String,
    pub remote_address: String,
    pub local_type: String,
    pub remote_type: String,
    pub local_candidates: i64,
    pub remote_candidates: i64,
    pub mapping_count: i64,
    pub active_channels: i64,
    pub connect_ms: String,
    pub rtt_ms: String,
    pub bytes_sent: u64,
    pub bytes_received: u64,
    pub dropped_datagrams: String,
    pub retry_count: String,
    pub relay_state: String,
    pub lease_until: i64,
    pub turn_expires_at: i64,
    pub error: Option<Fault>,
}

#[derive(Debug, Clone, Default, PartialEq)]
pub struct Network {
    pub handle: String,
    pub transport: String,
    pub interface: String,
    pub addresses: Vec<String>,
    pub dns: Vec<String>,
    pub available: bool,
    pub validated: bool,
    pub metered: bool,
}

#[derive(Debug, Clone, Default, PartialEq)]
pub struct Snapshot {
    pub native_ready: bool,
    pub api_version: i64,
    pub session_protocol: i64,
    pub core_version: String,
    pub configured: bool,
    pub run_requested: bool,
    pub engine_state: String,
    pub signal_state: String,
    pub generation: String,
    pub events_dropped: String,
    pub bridge_events_dropped: String,
    pub transport_generation: String,
    pub network_changes: String,
    pub reconnects: String,
    pub started_at: String,
    pub error: Option<Fault>,
    pub mappings: Vec<Mapping>,
    pub peers: Vec<Peer>,
    pub network: Network,
    pub transport_label: String,
    pub network_binding: bool,
}

impl Snapshot {
    pub fn tcp_sessions(&self) -> u64 {
        self.mappings.iter().map(|m| m.tcp_sessions).sum()
    }
    pub fn udp_sessions(&self) -> u64 {
        self.mappings.iter().map(|m| m.udp_sessions).sum()
    }
    pub fn provide_count(&self) -> usize {
        self.mappings.iter().filter(|m| m.role == "provide").count()
    }
    pub fn consume_count(&self) -> usize {
        self.mappings.iter().filter(|m| m.role == "consume").count()
    }
    pub fn active_mappings(&self) -> usize {
        self.mappings.iter().filter(|m| m.state == "active").count()
    }
    pub fn bytes_sent(&self) -> u64 {
        self.peers.iter().map(|p| p.bytes_sent).sum()
    }
    pub fn bytes_received(&self) -> u64 {
        self.peers.iter().map(|p| p.bytes_received).sum()
    }
    pub fn failure(code: &str, message: &str) -> Snapshot {
        Snapshot { engine_state: "error".into(), error: Some(Fault { code: code.into(), message: message.into() }), ..Default::default() }
    }
}

fn s(v: &Value, key: &str) -> String {
    v.get(key).and_then(Value::as_str).unwrap_or_default().to_string()
}
fn s_or(v: &Value, key: &str, dflt: &str) -> String {
    let t = s(v, key);
    if t.is_empty() { dflt.into() } else { t }
}
fn u(v: &Value, key: &str) -> u64 {
    match v.get(key) {
        Some(Value::String(t)) => t.parse().unwrap_or(0),
        Some(Value::Number(n)) => n.as_u64().unwrap_or(0),
        _ => 0,
    }
}
fn i(v: &Value, key: &str) -> i64 {
    match v.get(key) {
        Some(Value::String(t)) => t.parse().unwrap_or(0),
        Some(Value::Number(n)) => n.as_i64().unwrap_or(0),
        _ => 0,
    }
}
fn list(v: &Value, key: &str) -> Vec<String> {
    v.get(key).and_then(Value::as_array).map(|a| a.iter().filter_map(|x| x.as_str().map(String::from)).collect()).unwrap_or_default()
}

/// 解析 `snapshot` 方法的 result（含 bridge_events_dropped）。
pub fn parse_snapshot_result(result: &Value) -> Result<Snapshot, String> {
    let snap = result.get("snapshot").ok_or("快照缺少 snapshot 字段")?;
    let mut parsed = parse_snapshot(snap)?;
    parsed.bridge_events_dropped = s_or(result, "bridge_events_dropped", "0");
    Ok(parsed)
}

pub fn parse_snapshot(j: &Value) -> Result<Snapshot, String> {
    if i(j, "api_version") != 1 {
        return Err("桥接 API 版本不匹配".into());
    }
    let mappings = j["mappings"]
        .as_array()
        .map(|a| {
            a.iter()
                .map(|m| Mapping {
                    id: s(m, "id"),
                    role: s(m, "role"),
                    protocol: s(m, "protocol"),
                    state: s(m, "state"),
                    tcp_sessions: u(m, "tcp_sessions"),
                    udp_sessions: u(m, "udp_sessions"),
                    error: m.get("error").and_then(fault),
                    endpoint: s(m, "endpoint"),
                    peer: s(m, "peer"),
                    path: s(m, "path"),
                    tcp_read_bytes: s_or(m, "tcp_read_bytes", "0"),
                    tcp_written_bytes: s_or(m, "tcp_written_bytes", "0"),
                    read_bytes: s_or(m, "read_bytes", "0"),
                    written_bytes: s_or(m, "written_bytes", "0"),
                    replay_bytes: s_or(m, "replay_bytes", "0"),
                    profile: s(m, "profile"),
                })
                .collect()
        })
        .unwrap_or_default();
    let peers = j["peer_transports"]
        .as_array()
        .map(|a| {
            a.iter()
                .map(|p| Peer {
                    peer_id: s(p, "peer_id"),
                    transport_id: s(p, "transport_id"),
                    generation: s_or(p, "generation", "0"),
                    profile: s(p, "profile"),
                    state: s(p, "state"),
                    phase: s(p, "phase"),
                    pending_phase: s(p, "pending_phase"),
                    path_type: s(p, "path_type"),
                    address_family: s(p, "address_family"),
                    local_relay_protocol: { let l = s(p, "local_relay_protocol"); if l.is_empty() { s(p, "relay_protocol") } else { l } },
                    remote_relay_protocol: s(p, "remote_relay_protocol"),
                    relay_side: s(p, "relay_side"),
                    relay_policy: s(p, "relay_policy"),
                    local_address: s(p, "local_address"),
                    remote_address: s(p, "remote_address"),
                    local_type: s(p, "local_type"),
                    remote_type: s(p, "remote_type"),
                    local_candidates: i(p, "local_candidates"),
                    remote_candidates: i(p, "remote_candidates"),
                    mapping_count: i(p, "mapping_count"),
                    active_channels: i(p, "active_channels"),
                    connect_ms: s_or(p, "connect_ms", "0"),
                    rtt_ms: s_or(p, "rtt_ms", "0"),
                    bytes_sent: u(p, "bytes_sent"),
                    bytes_received: u(p, "bytes_received"),
                    dropped_datagrams: s_or(p, "dropped_datagrams", "0"),
                    retry_count: s_or(p, "retry_count", "0"),
                    relay_state: s(p, "relay_state"),
                    lease_until: i(p, "lease_until"),
                    turn_expires_at: i(p, "turn_expires_at"),
                    error: p.get("error").and_then(fault),
                })
                .collect()
        })
        .unwrap_or_default();
    let n = &j["network"];
    let caps = &j["capabilities"];
    Ok(Snapshot {
        native_ready: true,
        api_version: i(j, "api_version"),
        session_protocol: i(j, "session_protocol"),
        core_version: s(j, "core_version"),
        configured: j["configured"].as_bool().unwrap_or(false),
        run_requested: j["run_requested"].as_bool().unwrap_or(false),
        engine_state: s(j, "engine_state"),
        signal_state: s(j, "signal_state"),
        generation: s_or(j, "generation", "0"),
        events_dropped: s_or(j, "events_dropped", "0"),
        bridge_events_dropped: "0".into(),
        transport_generation: s_or(j, "transport_generation", "0"),
        network_changes: s_or(j, "network_changes", "0"),
        reconnects: s_or(j, "reconnects", "0"),
        started_at: s(j, "started_at"),
        error: j.get("error").and_then(fault),
        mappings,
        peers,
        network: Network {
            handle: s(n, "handle"),
            transport: s(n, "transport"),
            interface: s(n, "interface"),
            addresses: list(n, "addresses"),
            dns: list(n, "dns"),
            available: n["available"].as_bool().unwrap_or(false),
            validated: n["validated"].as_bool().unwrap_or(false),
            metered: n["metered"].as_bool().unwrap_or(false),
        },
        transport_label: s_or(caps, "transport", "IPv6 / QUIC / hole-v2"),
        network_binding: caps["network_binding"].as_bool().unwrap_or(false),
    })
}

// ---------- 文案 ----------

pub fn engine_label(s: &Snapshot) -> String {
    match s.engine_state.as_str() {
        "loading" => "正在加载核心",
        "stopped" => if s.configured { "已停止" } else { "未配置 · 已停止" },
        "starting" => "正在启动",
        "running" => "核心运行中",
        "stopping" => "正在停止",
        "reconfiguring" => "正在应用配置",
        "recovering" => "等待网络 / 恢复连接",
        "error" => "核心异常",
        other => return other.to_string(),
    }
    .to_string()
}

pub fn signal_label(state: &str) -> &'static str {
    match state {
        "disconnected" => "未连接",
        "connecting" => "连接中",
        "joining" => "正在加入房间",
        "joined" => "已加入房间",
        "reconnecting" => "等待重连",
        "error" => "异常",
        _ => "未知",
    }
}

pub fn mapping_label(state: &str) -> &'static str {
    match state {
        "active" => "通道可用",
        "waiting_peer" => "等待对端",
        "connecting" => "连接中",
        "stopped" => "已停止",
        "error" => "需要检查",
        _ => "准备中",
    }
}

pub fn mapping_kind(state: &str) -> &'static str {
    match state {
        "active" => "good",
        "waiting_peer" | "connecting" => "warn",
        "error" => "bad",
        "stopped" => "idle",
        _ => "info",
    }
}

pub fn connection_mode_label(mode: &str) -> &'static str {
    match mode {
        "legacy" => "仅 IPv6",
        "ice" => "仅 ICE",
        _ => "自动",
    }
}

pub fn connection_policy_label(mode: &str) -> &'static str {
    match mode {
        "legacy" => "仅 IPv6 · 公网直连",
        "ice" => "仅 ICE · IPv4 / IPv6 与中继",
        _ => "自动 · ICE 优先，兼容 IPv6 协议",
    }
}

const ACTIONABLE: [&str; 6] = ["signal_auth_failed", "invalid_token", "protocol_mismatch", "worker_upgrade_required", "invalid_config", "insecure_signal"];

pub fn connection_title(s: &Snapshot) -> &'static str {
    let code = s.error.as_ref().map(|e| e.code.as_str()).unwrap_or("");
    if !s.run_requested {
        "连接已停止"
    } else if s.mappings.iter().any(|m| m.state == "active") || s.peers.iter().any(|p| matches!(p.state.as_str(), "active" | "switching")) {
        "转发连接正常"
    } else if ACTIONABLE.contains(&code) {
        "连接需要处理"
    } else if s.signal_state == "joined" && s.mappings.is_empty() {
        "已就绪，尚未启用服务"
    } else if s.signal_state == "joined" && s.peers.is_empty() {
        "等待对端设备"
    } else if s.peers.iter().any(|p| p.state == "reconnecting") || s.engine_state == "recovering" {
        "正在恢复连接"
    } else {
        "正在建立连接"
    }
}

/// 总体状态语义色：good / warn / bad / idle。
pub fn connection_kind(s: &Snapshot) -> &'static str {
    let code = s.error.as_ref().map(|e| e.code.as_str()).unwrap_or("");
    if !s.run_requested {
        "idle"
    } else if s.engine_state == "error" || ACTIONABLE.contains(&code) {
        "bad"
    } else if s.mappings.iter().any(|m| m.state == "active") || s.peers.iter().any(|p| matches!(p.state.as_str(), "active" | "switching")) {
        "good"
    } else {
        "warn"
    }
}

pub fn configuration_text(s: &Snapshot) -> &'static str {
    if !s.run_requested {
        "设置已保存，开启连接后使用"
    } else if s.error.is_some() {
        "设置尚未生效，请检查连接提示"
    } else if s.signal_state == "joined" {
        "当前启用的设置已生效"
    } else {
        "正在应用设置并加入房间"
    }
}

pub fn phase_label(phase: &str) -> &'static str {
    match phase {
        "direct" => "探测直连路径",
        "relay_udp" => "尝试 UDP 中继",
        "relay_tls_443" => "尝试 TLS 中继 · 443 端口",
        "relay_tls" => "尝试 TLS 中继 · 5349 / 自定义端口",
        "relay_tcp_80" => "尝试 TCP 中继 · 80 端口",
        "relay_tcp" => "尝试 TCP 中继 · 3478 / 自定义端口",
        _ => "正在选择连接路径",
    }
}

pub fn peer_phase_label(p: &Peer) -> String {
    if p.state == "waiting_credentials" { "等待中继凭据，尚未开始探测".into() } else { phase_label(&p.phase).into() }
}

pub fn peer_path_label(p: &Peer) -> String {
    let family = if p.address_family.is_empty() { String::new() } else { format!(" · {}", p.address_family) };
    match (p.state.as_str(), p.path_type.as_str()) {
        ("waiting_credentials", _) => peer_phase_label(p),
        (_, "relay") => format!("中继{family}"),
        (_, "direct") => format!("直连{family}"),
        _ => phase_label(&p.phase).into(),
    }
}

pub fn peer_state_label(state: &str) -> &'static str {
    match state {
        "waiting_credentials" => "等待中继凭据",
        "active" => "已连接",
        "switching" => "正在切换，现有路径继续工作",
        "paused" => "对端暂时离线",
        "error" => "连接需要处理",
        "reconnecting" => "正在重试",
        _ => "连接中",
    }
}

pub fn peer_kind(state: &str) -> &'static str {
    match state {
        "active" | "switching" => "good",
        "error" => "bad",
        "paused" | "reconnecting" | "waiting_credentials" => "warn",
        _ => "info",
    }
}

pub fn relay_access_label(protocol: &str, side: &str) -> String {
    match protocol {
        "udp" => "UDP 中继".into(),
        "tls" => "TLS 中继".into(),
        "tcp" => "TCP 中继".into(),
        _ => if side == "remote" { "对端中继 · 接入协议未上报".into() } else { "中继连接 · 接入协议待确认".into() },
    }
}

pub fn relay_path_label(p: &Peer) -> String {
    let local = &p.local_relay_protocol;
    match p.relay_side.as_str() {
        "remote" => relay_access_label(&p.remote_relay_protocol, "remote"),
        "both" => {
            if !local.is_empty() && !p.remote_relay_protocol.is_empty() && local != &p.remote_relay_protocol {
                format!("{} / {}", relay_access_label(local, ""), relay_access_label(&p.remote_relay_protocol, ""))
            } else {
                relay_access_label(if local.is_empty() { &p.remote_relay_protocol } else { local }, "both")
            }
        }
        side => relay_access_label(local, side),
    }
}

pub fn selected_connection_label(kind: &str, address: &str) -> String {
    let path = match kind {
        "relay" => "中继",
        "host" | "srflx" | "prflx" => "直连",
        _ => "路径未确认",
    };
    format!("{path} · {}", if address.is_empty() { "探测中" } else { address })
}

pub fn connection_issue(code: &str) -> &'static str {
    match code {
        "signal_auth_failed" => "协调服务密码不匹配，请检查“信令密码”。",
        "invalid_token" => "房间密码不匹配，请与对端使用相同的房间密码。",
        "no_network" => "当前没有可用网络，恢复网络后会自动重试。",
        "no_ipv6" => "IPv6 直连需要双方具有可用的公网 IPv6；“自动”和“仅 ICE”支持 IPv4 / IPv6 探测与中继，需协调服务及对端支持 ICE。",
        "network_dns_failed" => "当前网络未能解析服务器地址，请检查 DNS 或切换网络。",
        "address_in_use" => "本地监听端口已被占用，请修改该服务的端口。",
        "worker_upgrade_required" => "协调服务尚未支持 ICE，请更新 Worker，或选择“仅 IPv6”并确认双方公网 IPv6 可用。",
        "protocol_mismatch" => "双方传输协议不一致，请核对“自动”“仅 ICE”或“仅 IPv6”的选择，并检查对端版本。",
        "peer_identity_mismatch" => "对端身份校验没有通过，请核对设备与协调服务配置。",
        "relay_unavailable" | "turn_rate_limited" => "中继暂未就绪；设备仍会优先尝试直连。",
        "service_unavailable" => "目标服务未响应，请检查提供端的服务地址、端口及运行状态。",
        "insecure_signal" => "ICE 默认使用 wss://。本地 ws:// 测试需在连接方式中显式开启。",
        "ice_path_failed" => "此路径未连通，正在尝试其他直连或中继路径。",
        "invalid_config" => "当前设置未通过检查，请核对连接方式、映射与地址。",
        _ => "连接暂未就绪，请检查网络、对端和连接设置。",
    }
}

pub fn format_bytes(value: u64) -> String {
    let v = value as f64;
    const G: f64 = 1024.0 * 1024.0 * 1024.0;
    const M: f64 = 1024.0 * 1024.0;
    if v >= G {
        format!("{:.2} GiB", v / G)
    } else if v >= M {
        format!("{:.2} MiB", v / M)
    } else if v >= 1024.0 {
        format!("{:.1} KiB", v / 1024.0)
    } else {
        format!("{value} B")
    }
}

pub fn format_bytes_str(value: &str) -> String {
    value.parse::<u64>().map(format_bytes).unwrap_or_else(|_| value.to_string())
}

pub fn format_rate(bytes_per_sec: f64) -> String {
    if bytes_per_sec >= 1024.0 * 1024.0 {
        format!("{:.2} MiB/s", bytes_per_sec / 1024.0 / 1024.0)
    } else if bytes_per_sec >= 1024.0 {
        format!("{:.1} KiB/s", bytes_per_sec / 1024.0)
    } else {
        format!("{:.0} B/s", bytes_per_sec)
    }
}

pub fn now_millis() -> i64 {
    SystemTime::now().duration_since(UNIX_EPOCH).map(|d| d.as_millis() as i64).unwrap_or(0)
}

pub fn remaining_time(until_ms: i64) -> String {
    let seconds = (until_ms - now_millis()) / 1000;
    if seconds <= 0 {
        "等待刷新".into()
    } else if seconds >= 3600 {
        format!("约 {} 小时", seconds / 3600)
    } else {
        format!("约 {} 分钟", (seconds + 59) / 60)
    }
}

/// 解析 RFC 3339 时间为 Unix 秒；仅支持核心输出的 `YYYY-MM-DDTHH:MM:SS(.fff)Z` 及带偏移形式。
pub fn parse_rfc3339_secs(text: &str) -> Option<i64> {
    let t = text.trim();
    if t.len() < 19 {
        return None;
    }
    let (date, rest) = t.split_at(10);
    let rest = rest.strip_prefix('T').or_else(|| rest.strip_prefix(' '))?;
    let mut parts = date.split('-');
    let y: i64 = parts.next()?.parse().ok()?;
    let m: i64 = parts.next()?.parse().ok()?;
    let d: i64 = parts.next()?.parse().ok()?;
    let hh: i64 = rest.get(0..2)?.parse().ok()?;
    let mm: i64 = rest.get(3..5)?.parse().ok()?;
    let ss: i64 = rest.get(6..8)?.parse().ok()?;
    let tail = &rest[8..];
    let tail = tail.strip_prefix('.').map(|f| f.trim_start_matches(|c: char| c.is_ascii_digit())).unwrap_or(tail);
    let offset = if tail == "Z" || tail.is_empty() {
        0
    } else {
        let sign = if tail.starts_with('-') { -1 } else { 1 };
        let oh: i64 = tail.get(1..3)?.parse().ok()?;
        let om: i64 = tail.get(4..6)?.parse().ok()?;
        sign * (oh * 3600 + om * 60)
    };
    // days from civil (Howard Hinnant)
    let (y2, m2) = if m <= 2 { (y - 1, m + 9) } else { (y, m - 3) };
    let era = if y2 >= 0 { y2 } else { y2 - 399 } / 400;
    let yoe = y2 - era * 400;
    let doy = (153 * m2 + 2) / 5 + d - 1;
    let doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
    let days = era * 146097 + doe - 719468;
    Some(days * 86400 + hh * 3600 + mm * 60 + ss - offset)
}

pub fn elapsed_label(started_at: &str, running: bool) -> String {
    if !running {
        return "已停止".into();
    }
    if started_at.is_empty() {
        return "启动中".into();
    }
    let Some(start) = parse_rfc3339_secs(started_at) else { return "运行中".into() };
    let now = SystemTime::now().duration_since(UNIX_EPOCH).map(|d| d.as_secs() as i64).unwrap_or(start);
    let seconds = (now - start).max(0);
    if seconds >= 3600 {
        format!("{} 小时 {} 分", seconds / 3600, seconds / 60 % 60)
    } else {
        format!("{} 分 {} 秒", seconds / 60, seconds % 60)
    }
}

/// 只保留时间部分 HH:MM:SS（本地时区未知时显示 UTC）。
pub fn short_time(rfc3339: &str) -> String {
    rfc3339.get(11..19).map(String::from).unwrap_or_default()
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn parse_stopped_snapshot() {
        let result = json!({"snapshot":{"api_version":1,"session_protocol":2,"core_version":"x","configured":false,"run_requested":false,"engine_state":"stopped","signal_state":"disconnected","generation":"0","events_dropped":"0","mappings":[],"capabilities":{"transport":"IPv6 / QUIC / hole-v2","requires_ipv6":true,"live_configuration":true,"network_binding":false},"transport_generation":"0","network_changes":"0","reconnects":"0","network":{"handle":"","transport":"","interface":"","addresses":[],"dns":[],"available":false,"validated":false,"metered":false},"peer_transports":[]},"bridge_events_dropped":"3"});
        let s = parse_snapshot_result(&result).unwrap();
        assert_eq!(s.engine_state, "stopped");
        assert_eq!(s.bridge_events_dropped, "3");
        assert_eq!(connection_title(&s), "连接已停止");
        assert_eq!(engine_label(&s), "未配置 · 已停止");
    }

    #[test]
    fn big_counters_survive() {
        let m = json!({"id":"a","role":"provide","state":"active","tcp_sessions":"3","udp_sessions":"0","tcp_read_bytes":"18446744073709551615","tcp_written_bytes":"0","replay_bytes":"0"});
        let snap = json!({"api_version":1,"session_protocol":2,"core_version":"","configured":true,"run_requested":true,"engine_state":"running","signal_state":"joined","mappings":[m],"peer_transports":[{"peer_id":"p","transport_id":"t","state":"active","bytes_sent":"18446744073709551615","bytes_received":"1"}],"network":{}});
        let s = parse_snapshot(&snap).unwrap();
        assert_eq!(s.mappings[0].tcp_read_bytes, "18446744073709551615");
        assert_eq!(s.peers[0].bytes_sent, u64::MAX);
        assert_eq!(connection_title(&s), "转发连接正常");
        assert_eq!(connection_kind(&s), "good");
    }

    #[test]
    fn rfc3339() {
        assert_eq!(parse_rfc3339_secs("1970-01-01T00:00:00Z"), Some(0));
        assert_eq!(parse_rfc3339_secs("2026-09-14T00:00:00Z"), Some(1789344000));
        assert_eq!(parse_rfc3339_secs("2026-09-14T08:00:00.123+08:00"), Some(1789344000));
        assert_eq!(short_time("2026-09-14T08:01:02.5Z"), "08:01:02");
    }

    #[test]
    fn bytes() {
        assert_eq!(format_bytes(512), "512 B");
        assert_eq!(format_bytes(2048), "2.0 KiB");
        assert_eq!(format_bytes(3 * 1024 * 1024), "3.00 MiB");
    }
}
