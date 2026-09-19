//! 结构化连接报告：对齐 Android `Diagnostics.kt` 的字段，加上桌面 OS / GUI / 桥接版本；
//! 删除 URL 查询参数、脱敏信令密码、房间密码、TURN 凭据及其 URL 编码形式，上限 128 KiB。

use crate::config::{Secrets, StoredConfig};
use crate::snapshot::{peer_path_label, phase_label, Snapshot};

const MAX_REPORT_BYTES: usize = 128 * 1024;

pub fn url_encode(text: &str) -> String {
    let mut out = String::with_capacity(text.len());
    for b in text.bytes() {
        match b {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'*' => out.push(b as char),
            b' ' => out.push('+'),
            _ => out.push_str(&format!("%{b:02X}")),
        }
    }
    out
}

pub fn redact(text: &str, secrets: &[&str]) -> String {
    let mut result = text.to_string();
    let mut ordered: Vec<&str> = secrets.iter().copied().filter(|s| !s.is_empty()).collect();
    ordered.sort_by_key(|s| std::cmp::Reverse(s.len()));
    for secret in ordered {
        for spelling in [secret.to_string(), url_encode(secret)] {
            result = result.replace(&spelling, "[redacted]");
        }
    }
    // 删除 ws(s):// URL 中的查询串，避免残留凭据。
    let mut cleaned = String::with_capacity(result.len());
    let mut rest = result.as_str();
    while let Some(pos) = find_ws_url(rest) {
        cleaned.push_str(&rest[..pos]);
        let tail = &rest[pos..];
        let end = tail.find(char::is_whitespace).unwrap_or(tail.len());
        let url = &tail[..end];
        match url.find('?') {
            Some(q) => {
                cleaned.push_str(&url[..q]);
                cleaned.push_str("?[redacted]");
            }
            None => cleaned.push_str(url),
        }
        rest = &tail[end..];
    }
    cleaned.push_str(rest);
    truncate_utf8(cleaned, MAX_REPORT_BYTES)
}

fn find_ws_url(text: &str) -> Option<usize> {
    let lower = text.to_ascii_lowercase();
    let a = lower.find("wss://");
    let b = lower.find("ws://");
    match (a, b) {
        (Some(x), Some(y)) => Some(x.min(y)),
        (Some(x), None) | (None, Some(x)) => Some(x),
        _ => None,
    }
}

fn truncate_utf8(mut text: String, max: usize) -> String {
    if text.len() <= max {
        return text;
    }
    let mut cut = max;
    while !text.is_char_boundary(cut) {
        cut -= 1;
    }
    text.truncate(cut);
    text
}

pub struct ReportContext<'a> {
    pub snapshot: &'a Snapshot,
    pub config: &'a StoredConfig,
    pub secrets: &'a Secrets,
    pub gui_version: &'a str,
    pub bridge_version: i64,
    pub os_label: &'a str,
    pub resume_at_login: bool,
    pub last_resume_error: &'a str,
    pub now_rfc3339: &'a str,
}

pub fn diagnostic_report(ctx: &ReportContext<'_>) -> String {
    let s = ctx.snapshot;
    let c = &ctx.config.connection;
    let server = c.server_url.split('?').next().unwrap_or_default();
    let mut out = String::new();
    let line = |out: &mut String, text: String| {
        out.push_str(&text);
        out.push('\n');
    };
    line(&mut out, format!("hole 诊断 · {}", ctx.now_rfc3339));
    line(&mut out, format!("桌面 GUI {} · {} · 桥接协议 {}", ctx.gui_version, ctx.os_label, ctx.bridge_version));
    line(&mut out, format!("Core {} · API {} · 会话协议 {} · {}", s.core_version, s.api_version, s.session_protocol, s.transport_label));
    line(&mut out, format!("用户运行意图 {} · 核心 {} · 信令 {}", s.run_requested, s.engine_state, s.signal_state));
    line(&mut out, format!("启动 {} · 运行代次 {} · 传输代次 {}", s.started_at, s.generation, s.transport_generation));
    line(&mut out, format!("服务器 {} · 房间 {} · 设备 {}", server, c.room, c.device_name));
    line(&mut out, format!("连接方式 {} · 中继 {} · STUN {}", c.connection_mode, c.turn.mode, c.ice.stun_urls.join(", ")));
    line(&mut out, format!("重连 {} · 切网 {} · 丢弃事件 {} · 桥接丢弃 {}", s.reconnects, s.network_changes, s.events_dropped, s.bridge_events_dropped));
    line(&mut out, format!("网络 {} / {} / handle={}", s.network.transport, s.network.interface, s.network.handle));
    line(&mut out, format!("可用 {} · 验证 {} · 计费 {}", s.network.available, s.network.validated, s.network.metered));
    line(&mut out, format!("地址 {} · DNS {}", s.network.addresses.join(", "), s.network.dns.join(", ")));
    line(&mut out, format!("登录恢复 {} · {}", ctx.resume_at_login, ctx.last_resume_error));
    if let Some(e) = &s.error {
        line(&mut out, format!("错误 {}: {}", e.code, e.message));
    }
    for m in &s.mappings {
        line(&mut out, format!("映射 {}: {} / {} / {} / {}", m.id, m.role, m.protocol, m.state, m.endpoint));
        line(&mut out, format!("  对端 {} · 路径 {} · TCP {} / UDP {}", m.peer, m.path, m.tcp_sessions, m.udp_sessions));
        line(&mut out, format!("  活跃 TCP 会话累计读取 {} / 写入 {} / 待确认 {} 字节", m.tcp_read_bytes, m.tcp_written_bytes, m.replay_bytes));
        if let Some(e) = &m.error {
            line(&mut out, format!("  错误 {}: {}", e.code, e.message));
        }
    }
    line(&mut out, "设备线路（结构化状态，不含原始日志或候选凭据）".into());
    for p in &s.peers {
        line(&mut out, format!("设备 {}：{} / {}", p.peer_id, p.state, peer_path_label(p)));
        let local = if p.local_relay_protocol.is_empty() { "非本机中继" } else { &p.local_relay_protocol };
        let remote = if p.remote_relay_protocol.is_empty() { "未上报" } else { &p.remote_relay_protocol };
        let policy = if p.relay_policy.is_empty() { "兼容阶段" } else { &p.relay_policy };
        line(&mut out, format!("  本机接入 {local} · 对端接入 {remote} · 中继使用方 {} · 策略 {policy}", p.relay_side));
        line(&mut out, format!("  本地 {} ({}) · 对端 {} ({})", p.local_address, p.local_type, p.remote_address, p.remote_type));
        line(&mut out, format!("  通道 {}/{} · RTT {} ms · 建连 {} ms", p.active_channels, p.mapping_count, p.rtt_ms, p.connect_ms));
        line(&mut out, format!("  线路发送 {} / 接收 {} 字节 · UDP 丢弃 {}", p.bytes_sent, p.bytes_received, p.dropped_datagrams));
        line(&mut out, format!("  {} / generation {} / phase {}（{}） / retries {}", p.transport_id, p.generation, p.phase, phase_label(&p.phase), p.retry_count));
        line(&mut out, format!("  中继状态 {} · 凭据到期 {} · 授权到期 {}", p.relay_state, p.turn_expires_at, p.lease_until));
        if let Some(e) = &p.error {
            line(&mut out, format!("  当前问题 {}: {}", e.code, e.message));
        }
    }
    redact(&out, &[&ctx.secrets.password, &ctx.secrets.token, &ctx.secrets.turn_credential, &c.turn.username])
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn redacts_secrets_and_queries() {
        let text = "pw=s3cr et token=abc wss://h/ws?token=abc more";
        let out = redact(text, &["s3cr et", "abc"]);
        assert!(!out.contains("s3cr et"));
        assert!(!out.contains("s3cr+et"));
        assert!(out.contains("wss://h/ws?[redacted]"));
        assert!(!out.contains("token=abc"));
    }

    #[test]
    fn truncates() {
        let big = "x".repeat(MAX_REPORT_BYTES + 10);
        assert_eq!(redact(&big, &[]).len(), MAX_REPORT_BYTES);
    }
}
