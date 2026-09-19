//! GUI 配置模型与校验规则；语义对齐 Android `ConfigRules.kt`，最终仍以核心校验为准。
//! 凭据不放在本结构里，由 `storage::Secrets` 单独持有。

use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use std::net::{IpAddr, Ipv6Addr};

pub const DEFAULT_STUN_URL: &str = "stun:stun.cloudflare.com:3478";
pub const SCHEMA_VERSION: u32 = 1;

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct ProvideEntry {
    pub entry_id: String,
    #[serde(default)]
    pub id: String,
    #[serde(default = "tcp")]
    pub protocol: String,
    #[serde(default)]
    pub host: String,
    #[serde(default)]
    pub port: u16,
    #[serde(default = "yes")]
    pub enabled: bool,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct ConsumeEntry {
    pub entry_id: String,
    #[serde(default)]
    pub id: String,
    #[serde(default = "loopback")]
    pub host: String,
    #[serde(default)]
    pub port: u16,
    #[serde(default = "yes")]
    pub enabled: bool,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct IceSettings {
    pub stun_urls: Vec<String>,
    pub direct_probe_timeout: String,
    pub gather_timeout: String,
    pub connectivity_timeout: String,
    pub retry_max_delay: String,
    pub interface_allowlist: Vec<String>,
    pub include_loopback: bool,
    pub relay_only: bool,
}

impl Default for IceSettings {
    fn default() -> Self {
        IceSettings {
            stun_urls: vec![DEFAULT_STUN_URL.into()],
            direct_probe_timeout: "3s".into(),
            gather_timeout: "6s".into(),
            connectivity_timeout: "10s".into(),
            retry_max_delay: "15s".into(),
            interface_allowlist: vec![],
            include_loopback: false,
            relay_only: false,
        }
    }
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct TurnSettings {
    pub mode: String,
    pub ttl: String,
    pub urls: Vec<String>,
    pub username: String,
}

impl Default for TurnSettings {
    fn default() -> Self {
        TurnSettings { mode: "worker".into(), ttl: "6h".into(), urls: vec![], username: String::new() }
    }
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ConnectionSettings {
    pub server_url: String,
    pub room: String,
    pub device_name: String,
    pub session_timeout: String,
    pub candidate_interfaces: Vec<String>,
    pub candidate_addresses: Vec<String>,
    /// auto | ice | legacy
    pub connection_mode: String,
    pub allow_insecure_signal: bool,
    pub ice: IceSettings,
    pub turn: TurnSettings,
}

impl Default for ConnectionSettings {
    fn default() -> Self {
        ConnectionSettings {
            server_url: String::new(),
            room: String::new(),
            device_name: default_device_name(),
            session_timeout: "10m".into(),
            candidate_interfaces: vec![],
            candidate_addresses: vec![],
            connection_mode: "auto".into(),
            allow_insecure_signal: false,
            ice: IceSettings::default(),
            turn: TurnSettings::default(),
        }
    }
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Prefs {
    /// system | light | dark
    pub theme_mode: String,
    pub accent: String,
    pub close_to_tray: bool,
    pub resume_at_login: bool,
    pub start_hidden: bool,
    pub reduce_motion: bool,
}

impl Default for Prefs {
    fn default() -> Self {
        Prefs { theme_mode: "system".into(), accent: "blue".into(), close_to_tray: true, resume_at_login: false, start_hidden: false, reduce_motion: false }
    }
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct StoredConfig {
    pub schema_version: u32,
    pub prefs: Prefs,
    pub connection: ConnectionSettings,
    pub provide: Vec<ProvideEntry>,
    pub consume: Vec<ConsumeEntry>,
}

impl Default for StoredConfig {
    fn default() -> Self {
        StoredConfig { schema_version: SCHEMA_VERSION, prefs: Prefs::default(), connection: ConnectionSettings::default(), provide: vec![], consume: vec![] }
    }
}

/// 明文凭据，只存在于内存与受保护的独立文件中。
#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Secrets {
    pub password: String,
    pub token: String,
    pub turn_credential: String,
}

fn tcp() -> String { "tcp".into() }
fn yes() -> bool { true }
fn loopback() -> String { "127.0.0.1".into() }

pub fn default_device_name() -> String {
    std::env::var("HOSTNAME")
        .ok()
        .or_else(|| std::env::var("COMPUTERNAME").ok())
        .or_else(|| std::fs::read_to_string("/etc/hostname").ok().map(|s| s.trim().to_string()))
        .filter(|s| !s.is_empty())
        .unwrap_or_else(|| "desktop".into())
}

pub fn new_entry_id() -> String {
    uuid::Uuid::new_v4().to_string()
}

// ---------- 校验规则 ----------

pub fn parse_literal_ip(text: &str) -> Option<IpAddr> {
    let value = text.trim().trim_start_matches('[').trim_end_matches(']');
    value.parse::<IpAddr>().ok()
}

pub fn is_literal_ip(text: &str) -> bool {
    parse_literal_ip(text).is_some()
}

/// 与 core 的 isPublicIPv6 一致：全局单播、非 ULA、非链路本地、非回环、非组播。
pub fn is_public_ipv6(text: &str) -> bool {
    let Some(IpAddr::V6(ip)) = parse_literal_ip(text) else { return false };
    if ip.is_unspecified() || ip.is_loopback() || ip.is_multicast() {
        return false;
    }
    let first = ip.octets()[0];
    if first & 0xFE == 0xFC {
        return false; // fc00::/7
    }
    if ip.segments()[0] & 0xFFC0 == 0xFE80 {
        return false; // fe80::/10
    }
    ip != Ipv6Addr::UNSPECIFIED
}

pub fn normalize_host(text: &str) -> String {
    text.trim().trim_start_matches('[').trim_end_matches(']').trim().to_string()
}

pub fn wrap_ipv6(host: &str) -> String {
    let value = normalize_host(host);
    if matches!(parse_literal_ip(&value), Some(IpAddr::V6(_))) {
        format!("[{value}]")
    } else {
        value
    }
}

pub fn compose_service(protocol: &str, host: &str, port: u16) -> String {
    format!("{}://{}:{}", protocol.to_lowercase(), wrap_ipv6(host), port)
}

pub fn compose_expose(host: &str, port: u16) -> String {
    format!("{}:{}", wrap_ipv6(host), port)
}

pub fn parse_port(text: &str) -> Option<u16> {
    text.trim().parse::<u16>().ok().filter(|p| *p >= 1)
}

/// Go time.ParseDuration 子集：形如 3s、1h30m、500ms。
pub fn is_valid_duration(text: &str) -> bool {
    let value = text.trim();
    if value.is_empty() || value.len() > 128 {
        return false;
    }
    let mut rest = value;
    let mut total_ns: f64 = 0.0;
    let mut parts = 0;
    while !rest.is_empty() {
        let num_len = rest.chars().take_while(|c| c.is_ascii_digit() || *c == '.').map(char::len_utf8).sum::<usize>();
        if num_len == 0 {
            return false;
        }
        let number: f64 = match rest[..num_len].parse() {
            Ok(n) => n,
            Err(_) => return false,
        };
        rest = &rest[num_len..];
        let (unit, factor) = ["ns", "us", "µs", "μs", "ms", "s", "m", "h"]
            .iter()
            .filter(|u| rest.starts_with(**u))
            .max_by_key(|u| u.len())
            .map(|u| {
                let f = match *u {
                    "ns" => 1.0,
                    "us" | "µs" | "μs" => 1e3,
                    "ms" => 1e6,
                    "s" => 1e9,
                    "m" => 60e9,
                    _ => 3600e9,
                };
                (*u, f)
            })
            .unwrap_or(("", 0.0));
        if unit.is_empty() {
            return false;
        }
        rest = &rest[unit.len()..];
        total_ns += number * factor;
        parts += 1;
    }
    parts > 0 && total_ns <= i64::MAX as f64
}

pub fn is_valid_mapping_id(id: &str) -> bool {
    let value = id.trim();
    !value.is_empty() && value.len() <= 64 && !value.chars().any(|c| c.is_whitespace() || c.is_control())
}

/// 信令入口：默认 wss://；明文 ws:// 保留为显式开发配置。
pub fn validate_server_url(text: &str) -> Option<String> {
    let value = text.trim();
    if value.is_empty() {
        return Some("信令服务器不能为空".into());
    }
    let (scheme, rest) = match value.split_once("://") {
        Some(p) => p,
        None => return Some("必须以 wss:// 或 ws:// 开头".into()),
    };
    if !matches!(scheme.to_ascii_lowercase().as_str(), "ws" | "wss") {
        return Some("必须以 wss:// 或 ws:// 开头".into());
    }
    if rest.contains('#') {
        return Some("凭据使用独立字段，URL 不包含用户信息或片段".into());
    }
    let authority = rest.split(['/', '?']).next().unwrap_or("");
    if authority.contains('@') {
        return Some("凭据使用独立字段，URL 不包含用户信息或片段".into());
    }
    let host_port = authority;
    let (host, port) = if let Some(end) = host_port.find(']') {
        let host = &host_port[..=end];
        let port = host_port[end + 1..].strip_prefix(':');
        (host, port)
    } else if let Some((h, p)) = host_port.rsplit_once(':') {
        (h, Some(p))
    } else {
        (host_port, None)
    };
    if host.is_empty() {
        return Some("缺少主机名".into());
    }
    if let Some(p) = port {
        if p.parse::<u16>().ok().filter(|v| *v >= 1).is_none() {
            return Some("服务器端口无效".into());
        }
    }
    None
}

pub fn split_list(text: &str) -> Vec<String> {
    text.split(|c: char| c == ',' || c == '，' || c == ';' || c == '；' || c.is_whitespace())
        .map(str::trim)
        .filter(|s| !s.is_empty())
        .map(String::from)
        .collect()
}

pub fn join_list(values: &[String]) -> String {
    values.join(", ")
}

pub fn split_lines(text: &str) -> Vec<String> {
    text.lines().map(str::trim).filter(|s| !s.is_empty()).map(String::from).collect()
}

/// 同类列表内不重复；启用的 provide 与 consume 不得共用 id。
pub fn find_id_conflict(provide: &[ProvideEntry], consume: &[ConsumeEntry]) -> Option<String> {
    let mut seen = std::collections::HashSet::new();
    for e in provide.iter().filter(|e| e.enabled) {
        if !seen.insert(e.id.trim().to_string()) {
            return Some(format!("provide 中存在重复的映射 ID \"{}\"", e.id));
        }
    }
    for e in consume.iter().filter(|e| e.enabled) {
        let id = e.id.trim().to_string();
        if seen.contains(&id) {
            return Some(format!("映射 ID \"{}\" 不能同时出现在启用的 provide 和 consume 中", e.id));
        }
        seen.insert(id);
    }
    None
}

pub fn find_connection_error(c: &ConnectionSettings, secrets: &Secrets) -> Option<String> {
    if let Some(e) = validate_server_url(&c.server_url) {
        return Some(e);
    }
    if c.room.trim().is_empty() {
        return Some("房间号不能为空".into());
    }
    if secrets.password.is_empty() {
        return Some("信令密码不能为空".into());
    }
    if secrets.token.is_empty() {
        return Some("房间密码不能为空".into());
    }
    if c.device_name.trim().is_empty() {
        return Some("设备名不能为空".into());
    }
    if !is_valid_duration(&c.session_timeout) {
        return Some("会话保留期限格式无效，例如 10m".into());
    }
    None
}

/// 只把启用项映射为核心运行请求；返回可展示的错误。
pub fn to_run_request(cfg: &StoredConfig, secrets: &Secrets) -> Result<Value, String> {
    if let Some(e) = find_connection_error(&cfg.connection, secrets) {
        return Err(e);
    }
    if let Some(e) = find_id_conflict(&cfg.provide, &cfg.consume) {
        return Err(e);
    }
    let c = &cfg.connection;
    if !matches!(c.connection_mode.as_str(), "auto" | "ice" | "legacy") {
        return Err("连接方式无效".into());
    }
    if c.connection_mode != "legacy" {
        if !c.candidate_addresses.is_empty() {
            return Err("手动 IPv6 候选地址只用于“仅 IPv6”模式；使用 ICE 前请清空此项".into());
        }
        if c.server_url.trim().starts_with("ws://") && !c.allow_insecure_signal {
            return Err("ICE 默认使用 wss://；本地 ws:// 测试需在连接方式中显式开启".into());
        }
    }
    if !matches!(c.turn.mode.as_str(), "worker" | "manual" | "off") {
        return Err("中继来源无效".into());
    }
    if c.turn.mode == "manual" && (c.turn.urls.is_empty() || c.turn.username.trim().is_empty() || secrets.turn_credential.is_empty()) {
        return Err("手动中继需要服务器、用户名和凭据".into());
    }
    for a in &c.candidate_addresses {
        if !is_public_ipv6(a) {
            return Err(format!("候选地址 \"{a}\" 需要公网 IPv6 字面地址"));
        }
    }
    for d in [&c.ice.direct_probe_timeout, &c.ice.gather_timeout, &c.ice.connectivity_timeout, &c.ice.retry_max_delay, &c.turn.ttl] {
        if !is_valid_duration(d) {
            return Err("请检查连接方式中的时间格式，例如 3s、6h".into());
        }
    }
    let mut provide = Vec::new();
    for e in cfg.provide.iter().filter(|e| e.enabled) {
        if !is_valid_mapping_id(&e.id) {
            return Err("provide 项缺少有效的映射 ID".into());
        }
        if e.port == 0 {
            return Err(format!("provide \"{}\" 的端口无效", e.id));
        }
        if e.host.trim().is_empty() {
            return Err(format!("provide \"{}\" 缺少服务地址", e.id));
        }
        if !matches!(e.protocol.as_str(), "tcp" | "udp") {
            return Err("仅支持 TCP 或 UDP".into());
        }
        provide.push(json!({ "id": e.id.trim(), "service": compose_service(&e.protocol, &e.host, e.port) }));
    }
    let mut consume = Vec::new();
    for e in cfg.consume.iter().filter(|e| e.enabled) {
        if !is_valid_mapping_id(&e.id) {
            return Err("consume 项缺少有效的映射 ID".into());
        }
        if e.port == 0 {
            return Err(format!("consume \"{}\" 的端口无效", e.id));
        }
        if !is_literal_ip(&e.host) {
            return Err(format!("consume \"{}\" 的监听地址必须是字面 IP，例如 127.0.0.1", e.id));
        }
        consume.push(json!({ "id": e.id.trim(), "expose": compose_expose(&e.host, e.port) }));
    }
    let transport = json!({
        "preferred": if c.connection_mode == "legacy" { "ipv6" } else { "ice" },
        "allow_legacy": c.connection_mode == "auto",
        "allow_insecure_signal": c.allow_insecure_signal,
    });
    let turn = if c.turn.mode == "manual" {
        json!({ "mode": "manual", "ttl": c.turn.ttl, "urls": c.turn.urls, "username": c.turn.username, "credential": secrets.turn_credential })
    } else {
        json!({ "mode": c.turn.mode, "ttl": c.turn.ttl, "urls": [], "username": "", "credential": "" })
    };
    Ok(json!({
        "api_version": 1,
        "server_url": c.server_url.trim(),
        "config": {
            "room": c.room.trim(),
            "password": secrets.password,
            "token": secrets.token,
            "device_name": c.device_name.trim(),
            "session_timeout": c.session_timeout.trim(),
            "candidate_interfaces": c.candidate_interfaces,
            "candidate_addresses": c.candidate_addresses,
            "provide": provide,
            "consume": consume,
            "transport": transport,
            "ice": {
                "stun_urls": c.ice.stun_urls,
                "direct_probe_timeout": c.ice.direct_probe_timeout,
                "gather_timeout": c.ice.gather_timeout,
                "connectivity_timeout": c.ice.connectivity_timeout,
                "retry_max_delay": c.ice.retry_max_delay,
                "interface_allowlist": c.ice.interface_allowlist,
                "include_loopback": c.ice.include_loopback,
                "relay_only": c.ice.relay_only,
            },
            "turn": turn,
        }
    }))
}

/// 便携文档（decode_cli_config 结果 / encode_cli_config 输入），顶层含 server_url。
pub fn to_portable_document(cfg: &StoredConfig, secrets: &Secrets, include_secrets: bool) -> Result<Value, String> {
    // 用占位值走一遍严格校验，只为提前发现映射/传输问题；占位值不写入文档。
    let mut draft = cfg.clone();
    if validate_server_url(&draft.connection.server_url).is_some() {
        draft.connection.server_url = "wss://HOST/ws".into();
    }
    if draft.connection.room.trim().is_empty() {
        draft.connection.room = "pending".into();
    }
    if draft.connection.device_name.trim().is_empty() {
        draft.connection.device_name = "pending".into();
    }
    let placeholder = Secrets {
        password: if secrets.password.is_empty() { "pending".into() } else { secrets.password.clone() },
        token: if secrets.token.is_empty() { "pending".into() } else { secrets.token.clone() },
        turn_credential: if secrets.turn_credential.is_empty() { "pending".into() } else { secrets.turn_credential.clone() },
    };
    let mut request = to_run_request(&draft, &placeholder)?;
    let mut config = request["config"].take();
    config["room"] = json!(cfg.connection.room.trim());
    config["device_name"] = json!(cfg.connection.device_name.trim());
    config["password"] = json!(if include_secrets { secrets.password.as_str() } else { "" });
    config["token"] = json!(if include_secrets { secrets.token.as_str() } else { "" });
    if cfg.connection.turn.mode == "manual" {
        config["turn"]["credential"] = json!(if include_secrets { secrets.turn_credential.as_str() } else { "" });
    }
    config["server_url"] = json!(cfg.connection.server_url.trim());
    Ok(config)
}

/// 从便携文档生成停止态草稿；缺少服务器/设备名时沿用本机值，新条目分配独立 ID。
pub fn from_portable_document(doc: &Value, current: &StoredConfig) -> Result<(StoredConfig, Secrets), String> {
    let s = |v: &Value| v.as_str().unwrap_or_default().trim().to_string();
    let list = |v: &Value| v.as_array().map(|a| a.iter().filter_map(|x| x.as_str().map(|s| s.trim().to_string())).collect::<Vec<_>>()).unwrap_or_default();
    let transport = &doc["transport"];
    let preferred = s(&transport["preferred"]);
    if !matches!(preferred.as_str(), "ice" | "ipv6" | "") {
        return Err("transport.preferred 只接受 ice 或 ipv6".into());
    }
    let connection_mode = if preferred == "ipv6" {
        "legacy"
    } else if transport["allow_legacy"].as_bool().unwrap_or(true) {
        "auto"
    } else {
        "ice"
    };
    let ice = &doc["ice"];
    let turn = &doc["turn"];
    let dur = |v: &Value, dflt: &str| {
        let t = s(v);
        if t.is_empty() { dflt.to_string() } else { t }
    };
    let ice_defaults = IceSettings::default();
    let server_url = s(&doc["server_url"]);
    let device_name = s(&doc["device_name"]);
    let connection = ConnectionSettings {
        server_url: if server_url.is_empty() { current.connection.server_url.clone() } else { server_url },
        room: s(&doc["room"]),
        device_name: if device_name.is_empty() { current.connection.device_name.clone() } else { device_name },
        session_timeout: dur(&doc["session_timeout"], "10m"),
        candidate_interfaces: list(&doc["candidate_interfaces"]),
        candidate_addresses: list(&doc["candidate_addresses"]),
        connection_mode: connection_mode.into(),
        allow_insecure_signal: transport["allow_insecure_signal"].as_bool().unwrap_or(false),
        ice: IceSettings {
            stun_urls: if ice["stun_urls"].is_array() { list(&ice["stun_urls"]) } else { ice_defaults.stun_urls.clone() },
            direct_probe_timeout: dur(&ice["direct_probe_timeout"], &ice_defaults.direct_probe_timeout),
            gather_timeout: dur(&ice["gather_timeout"], &ice_defaults.gather_timeout),
            connectivity_timeout: dur(&ice["connectivity_timeout"], &ice_defaults.connectivity_timeout),
            retry_max_delay: dur(&ice["retry_max_delay"], &ice_defaults.retry_max_delay),
            interface_allowlist: list(&ice["interface_allowlist"]),
            include_loopback: ice["include_loopback"].as_bool().unwrap_or(false),
            relay_only: ice["relay_only"].as_bool().unwrap_or(false),
        },
        turn: TurnSettings {
            mode: { let m = s(&turn["mode"]); if m.is_empty() { "worker".into() } else { m } },
            ttl: dur(&turn["ttl"], "6h"),
            urls: list(&turn["urls"]),
            username: s(&turn["username"]),
        },
    };
    let mut provide = Vec::new();
    for p in doc["provide"].as_array().cloned().unwrap_or_default() {
        let service = s(&p["service"]);
        let (protocol, endpoint) = service.split_once("://").ok_or("provide 的 service 缺少协议")?;
        let (host, port) = split_endpoint(endpoint)?;
        provide.push(ProvideEntry { entry_id: new_entry_id(), id: s(&p["id"]), protocol: protocol.to_lowercase(), host, port, enabled: true });
    }
    let mut consume = Vec::new();
    for c in doc["consume"].as_array().cloned().unwrap_or_default() {
        let (host, port) = split_endpoint(&s(&c["expose"]))?;
        consume.push(ConsumeEntry { entry_id: new_entry_id(), id: s(&c["id"]), host, port, enabled: true });
    }
    let config = StoredConfig { schema_version: SCHEMA_VERSION, prefs: current.prefs.clone(), connection, provide, consume };
    let secrets = Secrets { password: s(&doc["password"]), token: s(&doc["token"]), turn_credential: s(&turn["credential"]) };
    validate_draft(&config)?;
    Ok((config, secrets))
}

pub fn split_endpoint(value: &str) -> Result<(String, u16), String> {
    let sep = value.rfind(':').ok_or("端点缺少地址或端口")?;
    if sep == 0 {
        return Err("端点缺少地址或端口".into());
    }
    let host = normalize_host(&value[..sep]);
    let port = parse_port(&value[sep + 1..]).ok_or("端口无效")?;
    if host.is_empty() {
        return Err("地址为空".into());
    }
    Ok((host, port))
}

/// 停止态草稿校验：允许缺少服务器与凭据，其余约束不变。
pub fn validate_draft(cfg: &StoredConfig) -> Result<(), String> {
    to_portable_document(cfg, &Secrets::default(), false).map(|_| ())
}

// ---------- 备份（桌面版本化格式 + Android v1/v2 读取）----------

pub const DESKTOP_BACKUP_FORMAT: &str = "hole-desktop-backup";

pub fn backup_document(cfg: &StoredConfig, secrets: &Secrets, include_secrets: bool) -> Value {
    json!({
        "format": DESKTOP_BACKUP_FORMAT,
        "version": 1,
        "config": cfg,
        "password": if include_secrets { Some(&secrets.password) } else { None },
        "token": if include_secrets { Some(&secrets.token) } else { None },
        "turn_credential": if include_secrets { Some(&secrets.turn_credential) } else { None },
    })
}

pub struct BackupPreview {
    pub config: StoredConfig,
    pub secrets: Secrets,
    pub resume_at_login: bool,
    pub source: &'static str,
}

pub fn read_backup(text: &str, current: &StoredConfig) -> Result<BackupPreview, String> {
    let doc: Value = serde_json::from_str(text).map_err(|_| "不是合法的 JSON 备份".to_string())?;
    let format = doc["format"].as_str().unwrap_or_default();
    let version = doc["version"].as_i64().unwrap_or_default();
    let s = |v: &Value| v.as_str().unwrap_or_default().to_string();
    let (mut config, source) = match (format, version) {
        (DESKTOP_BACKUP_FORMAT, 1) => {
            let cfg: StoredConfig = serde_json::from_value(doc["config"].clone()).map_err(|e| format!("桌面备份格式无效：{e}"))?;
            (cfg, "桌面完整备份")
        }
        ("hole-android-backup", 1 | 2) => (android_backup(&doc["config"], version, current)?, "Android 配置备份"),
        _ => return Err("请选择 hole 桌面或 Android 配置备份".into()),
    };
    config.schema_version = SCHEMA_VERSION;
    let ids: Vec<&String> = config.provide.iter().map(|e| &e.entry_id).chain(config.consume.iter().map(|e| &e.entry_id)).collect();
    let unique: std::collections::HashSet<_> = ids.iter().collect();
    if ids.iter().any(|i| i.trim().is_empty()) || unique.len() != ids.len() {
        return Err("备份包含重复或空的条目标识".into());
    }
    validate_draft(&config)?;
    let secrets = Secrets { password: s(&doc["password"]), token: s(&doc["token"]), turn_credential: s(&doc["turn_credential"]).clone() };
    let secrets = Secrets { turn_credential: if secrets.turn_credential.is_empty() { s(&doc["turnCredential"]) } else { secrets.turn_credential }, ..secrets };
    let resume = doc["resume_at_login"].as_bool().or_else(|| doc["resumeAfterBoot"].as_bool()).unwrap_or(false);
    Ok(BackupPreview { config, secrets, resume_at_login: resume, source })
}

/// Android 备份使用 camelCase 与 Material/Miuix 主题字段；只保留可移植部分。
fn android_backup(config: &Value, version: i64, current: &StoredConfig) -> Result<StoredConfig, String> {
    let s = |v: &Value| v.as_str().unwrap_or_default().trim().to_string();
    let list = |v: &Value| v.as_array().map(|a| a.iter().filter_map(|x| x.as_str().map(|s| s.trim().to_string())).collect::<Vec<_>>()).unwrap_or_default();
    let conn = &config["connection"];
    let ice = &conn["ice"];
    let turn = &conn["turn"];
    let d = IceSettings::default();
    let or = |v: &Value, dflt: &str| { let t = s(v); if t.is_empty() { dflt.to_string() } else { t } };
    let server_url = s(&conn["serverUrl"]);
    let candidate_addresses = list(&conn["candidateAddresses"]);
    let schema = config["schemaVersion"].as_i64().unwrap_or(if version == 1 { 1 } else { 2 });
    let connection_mode = if schema >= 2 {
        or(&conn["connectionMode"], "auto")
    } else if !candidate_addresses.is_empty() || server_url.starts_with("ws://") {
        "legacy".into()
    } else {
        "auto".into()
    };
    let mut prefs = current.prefs.clone();
    let mode = s(&config["themeMode"]);
    if matches!(mode.as_str(), "system" | "light" | "dark") {
        prefs.theme_mode = mode;
    }
    let mut cfg = StoredConfig {
        schema_version: SCHEMA_VERSION,
        prefs,
        connection: ConnectionSettings {
            server_url,
            room: s(&conn["room"]),
            device_name: or(&conn["deviceName"], &current.connection.device_name),
            session_timeout: or(&conn["sessionTimeout"], "10m"),
            candidate_interfaces: list(&conn["candidateInterfaces"]),
            candidate_addresses,
            connection_mode,
            allow_insecure_signal: conn["allowInsecureSignal"].as_bool().unwrap_or(false),
            ice: IceSettings {
                stun_urls: if ice["stun_urls"].is_array() { list(&ice["stun_urls"]) } else { d.stun_urls.clone() },
                direct_probe_timeout: or(&ice["direct_probe_timeout"], &d.direct_probe_timeout),
                gather_timeout: or(&ice["gather_timeout"], &d.gather_timeout),
                connectivity_timeout: or(&ice["connectivity_timeout"], &d.connectivity_timeout),
                retry_max_delay: or(&ice["retry_max_delay"], &d.retry_max_delay),
                interface_allowlist: list(&ice["interface_allowlist"]),
                include_loopback: ice["include_loopback"].as_bool().unwrap_or(false),
                relay_only: ice["relay_only"].as_bool().unwrap_or(false),
            },
            turn: TurnSettings { mode: or(&turn["mode"], "worker"), ttl: or(&turn["ttl"], "6h"), urls: list(&turn["urls"]), username: s(&turn["username"]) },
        },
        provide: vec![],
        consume: vec![],
    };
    for p in config["provide"].as_array().cloned().unwrap_or_default() {
        cfg.provide.push(ProvideEntry {
            entry_id: s(&p["entryId"]),
            id: s(&p["id"]),
            protocol: or(&p["protocol"], "tcp"),
            host: s(&p["host"]),
            port: p["port"].as_u64().and_then(|v| u16::try_from(v).ok()).unwrap_or(0),
            enabled: p["enabled"].as_bool().unwrap_or(true),
        });
    }
    for c in config["consume"].as_array().cloned().unwrap_or_default() {
        cfg.consume.push(ConsumeEntry {
            entry_id: s(&c["entryId"]),
            id: s(&c["id"]),
            host: or(&c["host"], "127.0.0.1"),
            port: c["port"].as_u64().and_then(|v| u16::try_from(v).ok()).unwrap_or(0),
            enabled: c["enabled"].as_bool().unwrap_or(true),
        });
    }
    Ok(cfg)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn durations() {
        for ok in ["10m", "1h30m", "500ms", "3s", "0.5s"] {
            assert!(is_valid_duration(ok), "{ok}");
        }
        for bad in ["", "10", "m", "10x", "1h 30m"] {
            assert!(!is_valid_duration(bad), "{bad}");
        }
    }

    #[test]
    fn server_urls() {
        assert_eq!(validate_server_url("wss://example.com/ws"), None);
        assert_eq!(validate_server_url("wss://[::1]:8443/ws"), None);
        assert!(validate_server_url("https://x").is_some());
        assert!(validate_server_url("wss://u:p@x/ws").is_some());
        assert!(validate_server_url("wss://x:70000").is_some());
    }

    #[test]
    fn public_ipv6() {
        assert!(is_public_ipv6("2001:db8::1"));
        assert!(!is_public_ipv6("fe80::1"));
        assert!(!is_public_ipv6("fd00::1"));
        assert!(!is_public_ipv6("::1"));
        assert!(!is_public_ipv6("127.0.0.1"));
    }

    fn sample() -> (StoredConfig, Secrets) {
        let mut cfg = StoredConfig::default();
        cfg.connection.server_url = "wss://host/ws".into();
        cfg.connection.room = "r".into();
        cfg.connection.device_name = "desktop".into();
        cfg.provide.push(ProvideEntry { entry_id: "a".into(), id: "ssh".into(), protocol: "tcp".into(), host: "::1".into(), port: 22, enabled: true });
        cfg.provide.push(ProvideEntry { entry_id: "b".into(), id: "off".into(), protocol: "udp".into(), host: "x".into(), port: 1, enabled: false });
        cfg.consume.push(ConsumeEntry { entry_id: "c".into(), id: "web".into(), host: "127.0.0.1".into(), port: 8080, enabled: true });
        (cfg, Secrets { password: "p".into(), token: "t".into(), turn_credential: String::new() })
    }

    #[test]
    fn run_request_only_enabled() {
        let (cfg, secrets) = sample();
        let req = to_run_request(&cfg, &secrets).unwrap();
        assert_eq!(req["server_url"], "wss://host/ws");
        assert_eq!(req["config"]["provide"].as_array().unwrap().len(), 1);
        assert_eq!(req["config"]["provide"][0]["service"], "tcp://[::1]:22");
        assert_eq!(req["config"]["consume"][0]["expose"], "127.0.0.1:8080");
        assert_eq!(req["config"]["transport"]["preferred"], "ice");
        assert_eq!(req["config"]["transport"]["allow_legacy"], true);
        assert!(req["config"].get("server_url").is_none());
    }

    #[test]
    fn portable_roundtrip() {
        let (cfg, secrets) = sample();
        let doc = to_portable_document(&cfg, &secrets, false).unwrap();
        assert_eq!(doc["password"], "");
        assert_eq!(doc["server_url"], "wss://host/ws");
        let (back, back_secrets) = from_portable_document(&doc, &StoredConfig::default()).unwrap();
        assert_eq!(back.provide.len(), 1);
        assert_eq!(back.provide[0].host, "::1");
        assert_eq!(back.consume[0].port, 8080);
        assert_eq!(back.connection.connection_mode, "auto");
        assert_eq!(back_secrets.password, "");
    }

    #[test]
    fn android_backup_reads() {
        let text = r#"{"format":"hole-android-backup","version":2,"config":{"schemaVersion":2,"themeStyle":"miuix","themeMode":"dark","connection":{"serverUrl":"wss://a/ws","room":"r","deviceName":"phone","sessionTimeout":"10m","connectionMode":"ice"},"provide":[{"entryId":"e1","id":"ssh","protocol":"tcp","host":"127.0.0.1","port":22,"enabled":false}],"consume":[]},"password":"p","token":"t","resumeAfterBoot":true}"#;
        let p = read_backup(text, &StoredConfig::default()).unwrap();
        assert_eq!(p.config.prefs.theme_mode, "dark");
        assert_eq!(p.config.connection.connection_mode, "ice");
        assert!(!p.config.provide[0].enabled);
        assert!(p.resume_at_login);
        assert_eq!(p.secrets.token, "t");
    }

    #[test]
    fn conflicts() {
        let (mut cfg, _) = sample();
        cfg.consume[0].id = "ssh".into();
        assert!(find_id_conflict(&cfg.provide, &cfg.consume).is_some());
        cfg.consume[0].enabled = false;
        assert!(find_id_conflict(&cfg.provide, &cfg.consume).is_none());
    }
}
