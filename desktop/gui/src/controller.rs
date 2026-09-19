//! 应用控制器：用户意图、配置应用与核心状态同步。
//!
//! 线程模型：
//! - UI 线程：Slint 回调；本地校验 + 原子保存配置，再向工作线程投递命令。
//! - 工作线程：持有 `CoreClient`，执行 RPC、采样快照、处理事件，并通过
//!   `invoke_from_event_loop` 把视图模型写回 UI。
//!
//! 采样策略对齐 Android `SnapshotSampler`：运行且窗口可见 1500ms、隐藏 15000ms、
//! 停止态只由事件/命令唤醒；事件触发的读取最短间隔 100ms。

use crate::config::{self, Secrets, StoredConfig};
use crate::core_client::{self, CoreClient, Inbound, RpcError};
use crate::report;
use crate::snapshot::{self as snap, Snapshot};
use crate::storage::{RunState, Storage};
use serde_json::Value;
use slint::{ComponentHandle, Model, ModelRc, VecModel, Weak};
use std::collections::VecDeque;
use std::sync::mpsc::{self, Receiver, RecvTimeoutError, Sender};
use std::sync::{Arc, Mutex};
use std::thread;
use std::time::{Duration, Instant};

use crate::{App, EventRow, HostInfo, ImportPreview, MainWindow, MappingRow, PeerRow, Status, KV};

/// 线程安全的行数据（纯字段）；到 UI 线程再转换成带 ModelRc 的 Slint 结构。
#[derive(Clone, Default)]
struct MappingData {
    entry_id: String,
    id: String,
    role: String,
    role_label: String,
    protocol: String,
    endpoint: String,
    enabled: bool,
    state: String,
    state_label: String,
    kind: String,
    sessions: String,
    peer: String,
    details: Vec<KV>,
}

#[derive(Clone, Default)]
struct PeerData {
    peer_id: String,
    transport_id: String,
    state_label: String,
    path_label: String,
    kind: String,
    channels: String,
    rtt: String,
    traffic: String,
    note: String,
    error: String,
    details: Vec<KV>,
}

impl MappingData {
    fn into_row(self) -> MappingRow {
        MappingRow {
            entry_id: self.entry_id.into(),
            id: self.id.into(),
            role: self.role.into(),
            role_label: self.role_label.into(),
            protocol: self.protocol.into(),
            endpoint: self.endpoint.into(),
            enabled: self.enabled,
            state: self.state.into(),
            state_label: self.state_label.into(),
            kind: self.kind.into(),
            sessions: self.sessions.into(),
            peer: self.peer.into(),
            details: ModelRc::new(VecModel::from(self.details)),
        }
    }
}

impl PeerData {
    fn into_row(self) -> PeerRow {
        PeerRow {
            peer_id: self.peer_id.into(),
            transport_id: self.transport_id.into(),
            state_label: self.state_label.into(),
            path_label: self.path_label.into(),
            kind: self.kind.into(),
            channels: self.channels.into(),
            rtt: self.rtt.into(),
            traffic: self.traffic.into(),
            note: self.note.into(),
            error: self.error.into(),
            details: ModelRc::new(VecModel::from(self.details)),
        }
    }
}

pub const GUI_VERSION: &str = env!("CARGO_PKG_VERSION");
const FOREGROUND_INTERVAL: Duration = Duration::from_millis(1500);
const BACKGROUND_INTERVAL: Duration = Duration::from_millis(15000);
const MIN_INTERVAL: Duration = Duration::from_millis(100);
const MAX_EVENTS: usize = 200;
const HISTORY_LEN: usize = 60;

/// UI 线程与工作线程共享的持久状态。
pub struct Shared {
    pub storage: Storage,
    pub config: StoredConfig,
    pub secrets: Secrets,
    pub run_state: RunState,
    pub config_error: Option<String>,
    /// 删除撤销缓存：(角色, 位置, 条目 JSON)
    pub undo: Option<(String, usize, Value)>,
    pub pending_import: Option<PendingImport>,
}

pub struct PendingImport {
    pub config: StoredConfig,
    pub secrets: Secrets,
    pub resume_at_login: bool,
    pub source: String,
}

pub enum Command {
    Start,
    Stop,
    Reconnect,
    Refresh,
    /// 配置已变更；运行中则重配核心。
    ConfigChanged,
    Visible(bool),
    ImportText { text: String, kind: String },
    ConfirmImport,
    Export { path: String, kind: String, include_secrets: bool },
    BuildReport,
    Quit,
}

pub struct Controller {
    pub shared: Arc<Mutex<Shared>>,
    pub tx: Sender<Command>,
}

struct TrafficSample {
    at: Instant,
    sent: u64,
    received: u64,
}

struct Worker {
    window: Weak<MainWindow>,
    shared: Arc<Mutex<Shared>>,
    rx: Receiver<Command>,
    wake_rx: Receiver<Inbound>,
    client: Option<Arc<CoreClient>>,
    hello: core_client::Hello,
    bridge_error: String,
    snapshot: Snapshot,
    visible: bool,
    busy: bool,
    command_error: String,
    events: VecDeque<EventRow>,
    last_generation: u64,
    last_sequence: u64,
    history: VecDeque<TrafficSample>,
    last_read: Instant,
    bridge_path: String,
}

impl Controller {
    pub fn start(window: &MainWindow, shared: Arc<Mutex<Shared>>) -> Controller {
        let (tx, rx) = mpsc::channel();
        let (wake_tx, wake_rx) = mpsc::channel();
        let weak = window.as_weak();
        let shared_worker = shared.clone();
        thread::Builder::new()
            .name("hole-controller".into())
            .spawn(move || {
                let mut worker = Worker {
                    window: weak,
                    shared: shared_worker,
                    rx,
                    wake_rx,
                    client: None,
                    hello: Default::default(),
                    bridge_error: String::new(),
                    snapshot: Snapshot::default(),
                    visible: true,
                    busy: false,
                    command_error: String::new(),
                    events: VecDeque::new(),
                    last_generation: 0,
                    last_sequence: 0,
                    history: VecDeque::new(),
                    last_read: Instant::now() - MIN_INTERVAL,
                    bridge_path: String::new(),
                };
                worker.connect_bridge(wake_tx.clone());
                worker.run(wake_tx);
            })
            .expect("控制器线程启动失败");
        Controller { shared, tx }
    }

    pub fn send(&self, command: Command) {
        let _ = self.tx.send(command);
    }
}

impl Worker {
    fn connect_bridge(&mut self, wake_tx: Sender<Inbound>) {
        match core_client::locate_bridge() {
            Ok(path) => {
                self.bridge_path = path.display().to_string();
                let tx = wake_tx;
                match CoreClient::spawn(&path, move |inbound| {
                    let _ = tx.send(inbound);
                }) {
                    Ok(client) => match client.hello() {
                        Ok(hello) if hello.bridge_version == core_client::BRIDGE_VERSION && hello.api_version == core_client::API_VERSION => {
                            self.hello = hello;
                            self.client = Some(client);
                            self.bridge_error.clear();
                            self.read_snapshot();
                        }
                        Ok(hello) => {
                            self.bridge_error = format!("核心宿主协议不匹配：桥接 {} / API {}，GUI 需要 {} / {}", hello.bridge_version, hello.api_version, core_client::BRIDGE_VERSION, core_client::API_VERSION);
                            client.shutdown(Duration::from_secs(3));
                        }
                        Err(e) => self.bridge_error = format!("核心宿主握手失败：{e}"),
                    },
                    Err(e) => self.bridge_error = e,
                }
            }
            Err(e) => self.bridge_error = e,
        }
        self.push_ui();
        self.maybe_resume();
    }

    /// 登录恢复：只有用户开启且上次仍请求运行时，执行一次明确的新启动。
    fn maybe_resume(&mut self) {
        let (resume, requested) = {
            let s = self.shared.lock().unwrap();
            (s.config.prefs.resume_at_login, s.run_state.run_requested)
        };
        if resume && requested && self.client.is_some() {
            if let Err(e) = self.start() {
                let mut s = self.shared.lock().unwrap();
                s.run_state.last_resume_error = format!("登录恢复失败：{e}");
                let _ = s.storage.save_run_state(&s.run_state);
            }
            self.push_ui();
        }
    }

    fn run(mut self, _wake_tx: Sender<Inbound>) {
        loop {
            let timeout = if self.snapshot.run_requested && self.client.is_some() {
                if self.visible { FOREGROUND_INTERVAL } else { BACKGROUND_INTERVAL }
            } else {
                Duration::from_secs(3600)
            };
            // 先排空事件通道，再等待命令；两者都能唤醒采样。
            let mut wake = false;
            while let Ok(inbound) = self.wake_rx.try_recv() {
                wake |= self.handle_inbound(inbound);
            }
            if wake {
                self.throttled_read();
                continue;
            }
            match self.rx.recv_timeout(timeout) {
                Ok(Command::Quit) => {
                    self.quit();
                    return;
                }
                Ok(command) => self.handle_command(command),
                Err(RecvTimeoutError::Timeout) => {
                    if self.snapshot.run_requested {
                        self.read_snapshot();
                        self.push_ui();
                    }
                }
                Err(RecvTimeoutError::Disconnected) => {
                    self.quit();
                    return;
                }
            }
        }
    }

    fn handle_inbound(&mut self, inbound: Inbound) -> bool {
        match inbound {
            Inbound::Event(params) => self.handle_event(&params),
            Inbound::Exited(reason) => {
                self.client = None;
                self.bridge_error = format!("{reason}；转发已中断。重新打开应用以重新启动核心宿主。");
                self.snapshot = Snapshot::failure("bridge_exited", &reason);
                self.push_ui();
                false
            }
        }
    }

    fn handle_event(&mut self, e: &Value) -> bool {
        let kind = e["kind"].as_str().unwrap_or_default();
        if kind == "log" {
            return false;
        }
        let generation: u64 = e["generation"].as_str().and_then(|s| s.parse().ok()).unwrap_or(0);
        let sequence: u64 = e["sequence"].as_str().and_then(|s| s.parse().ok()).unwrap_or(0);
        if generation < self.last_generation || (generation == self.last_generation && sequence <= self.last_sequence && sequence != 0) {
            return false; // 旧代次或重复
        }
        self.last_generation = generation;
        self.last_sequence = sequence;
        let state = e["state"].as_str().unwrap_or_default();
        let message = e["message"].as_str().unwrap_or_default();
        let error = e.get("error").and_then(|f| f.get("code")).and_then(Value::as_str).unwrap_or_default();
        let (level, summary) = match kind {
            "engine" => (if state == "error" { "bad" } else if state == "running" { "good" } else { "info" }, format!("核心 {}", snap::engine_label(&Snapshot { engine_state: state.into(), configured: true, ..Default::default() }))),
            "signal" => (if state == "error" { "bad" } else if state == "joined" { "good" } else { "info" }, format!("信令 {}", snap::signal_label(state))),
            "mapping" => (snap::mapping_kind(state), format!("映射 {} {}", e["mapping_id"].as_str().unwrap_or_default(), snap::mapping_label(state))),
            "peer" => (snap::peer_kind(state), format!("设备 {} {}", e["peer"].as_str().unwrap_or_default(), snap::peer_state_label(state))),
            "network" => ("warn", "网络变化，正在重建路径".to_string()),
            "session" => ("info", format!("会话 {} {}", e["mapping_id"].as_str().unwrap_or_default(), e["stage"].as_str().unwrap_or_default())),
            other => ("info", format!("{other} {state}")),
        };
        let level = if !error.is_empty() { "bad" } else { level };
        let mut detail = String::new();
        if !message.is_empty() {
            detail.push_str(message);
        }
        if !error.is_empty() {
            if !detail.is_empty() {
                detail.push_str(" · ");
            }
            detail.push_str(snap::connection_issue(error));
        }
        for key in ["phase", "path", "profile", "target"] {
            if let Some(v) = e[key].as_str().filter(|v| !v.is_empty()) {
                if !detail.is_empty() {
                    detail.push_str(" · ");
                }
                detail.push_str(v);
            }
        }
        self.events.push_front(EventRow {
            time: snap::short_time(e["time"].as_str().unwrap_or_default()).into(),
            kind: kind.into(),
            level: level.into(),
            summary: summary.into(),
            detail: detail.into(),
        });
        while self.events.len() > MAX_EVENTS {
            self.events.pop_back();
        }
        true
    }

    fn throttled_read(&mut self) {
        let since = self.last_read.elapsed();
        if since < MIN_INTERVAL {
            thread::sleep(MIN_INTERVAL - since);
        }
        // 合并这段时间内到达的事件，而不是持续延后。
        while let Ok(inbound) = self.wake_rx.try_recv() {
            self.handle_inbound(inbound);
        }
        self.read_snapshot();
        self.push_ui();
    }

    fn read_snapshot(&mut self) {
        self.last_read = Instant::now();
        let Some(client) = self.client.clone() else { return };
        match client.call("snapshot", None).and_then(|v| snap::parse_snapshot_result(&v).map_err(|e| RpcError { code: 0, message: e, fault_code: None, fault_message: None })) {
            Ok(s) => {
                if s.run_requested {
                    self.history.push_back(TrafficSample { at: Instant::now(), sent: s.bytes_sent(), received: s.bytes_received() });
                    while self.history.len() > HISTORY_LEN {
                        self.history.pop_front();
                    }
                } else {
                    self.history.clear();
                }
                self.snapshot = s;
            }
            Err(e) => self.command_error = format!("读取快照失败：{e}"),
        }
    }

    fn handle_command(&mut self, command: Command) {
        match command {
            Command::Start => {
                self.set_busy(true);
                match self.start() {
                    Ok(()) => self.command_error.clear(),
                    Err(e) => self.command_error = e,
                }
                self.set_busy(false);
            }
            Command::Stop => {
                self.set_busy(true);
                self.stop();
                self.set_busy(false);
            }
            Command::Reconnect => {
                if let Some(c) = &self.client {
                    match c.call("network_changed", None) {
                        Ok(_) => self.command_error.clear(),
                        Err(e) => self.command_error = format!("重新连接失败：{e}"),
                    }
                }
                self.read_snapshot();
                self.push_ui();
            }
            Command::Refresh => {
                self.read_snapshot();
                self.push_ui();
            }
            Command::ConfigChanged => {
                if self.snapshot.run_requested {
                    self.apply_config();
                }
                self.push_ui();
            }
            Command::Visible(v) => {
                self.visible = v;
            }
            Command::ImportText { text, kind } => self.import(text, kind),
            Command::ConfirmImport => self.confirm_import(),
            Command::Export { path, kind, include_secrets } => self.export(path, kind, include_secrets),
            Command::BuildReport => self.build_report(),
            Command::Quit => {}
        }
    }

    fn set_busy(&mut self, busy: bool) {
        self.busy = busy;
        self.push_ui();
    }

    fn build_request(&self) -> Result<Value, String> {
        let s = self.shared.lock().unwrap();
        config::to_run_request(&s.config, &s.secrets)
    }

    fn start(&mut self) -> Result<(), String> {
        let client = self.client.clone().ok_or_else(|| self.bridge_error.clone())?;
        let request = self.build_request()?;
        client.call("validate", Some(request.clone())).map_err(|e| e.display())?;
        client.call("start", Some(request)).map_err(|e| e.display())?;
        {
            let mut s = self.shared.lock().unwrap();
            s.run_state.run_requested = true;
            s.run_state.last_resume_error.clear();
            let _ = s.storage.save_run_state(&s.run_state);
        }
        self.read_snapshot();
        Ok(())
    }

    fn stop(&mut self) {
        {
            let mut s = self.shared.lock().unwrap();
            s.run_state.run_requested = false;
            let _ = s.storage.save_run_state(&s.run_state);
        }
        if let Some(c) = &self.client {
            match c.call("stop", None) {
                Ok(_) => self.command_error.clear(),
                Err(e) => self.command_error = format!("停止失败：{e}"),
            }
        }
        self.history.clear();
        self.read_snapshot();
        self.push_ui();
    }

    fn apply_config(&mut self) {
        let Some(client) = self.client.clone() else { return };
        match self.build_request() {
            Ok(request) => match client.call("apply_config", Some(request)) {
                Ok(_) => self.command_error.clear(),
                Err(e) => self.command_error = format!("应用配置失败：{e}"),
            },
            Err(e) => self.command_error = format!("配置未应用：{e}"),
        }
        self.read_snapshot();
    }

    fn import(&mut self, text: String, kind: String) {
        let result = (|| -> Result<PendingImport, String> {
            let current = self.shared.lock().unwrap().config.clone();
            if kind == "backup" {
                let p = config::read_backup(&text, &current)?;
                Ok(PendingImport { config: p.config, secrets: p.secrets, resume_at_login: p.resume_at_login, source: p.source.into() })
            } else {
                let client = self.client.clone().ok_or("核心宿主未运行，无法解析 CLI 配置")?;
                let value = client.call("decode_cli_config", Some(serde_json::json!({ "text": text }))).map_err(|e| e.display())?;
                let doc = value.get("config").ok_or("转换结果缺少 config")?;
                let (cfg, secrets) = config::from_portable_document(doc, &current)?;
                Ok(PendingImport { config: cfg, secrets, resume_at_login: false, source: "CLI YAML / JSON".into() })
            }
        })();
        let preview = match result {
            Ok(p) => {
                let preview = ImportPreview {
                    visible: true,
                    kind_label: p.source.clone().into(),
                    provide_count: p.config.provide.len().to_string().into(),
                    consume_count: p.config.consume.len().to_string().into(),
                    enabled_count: (p.config.provide.iter().filter(|e| e.enabled).count() + p.config.consume.iter().filter(|e| e.enabled).count()).to_string().into(),
                    server_url: p.config.connection.server_url.clone().into(),
                    room: p.config.connection.room.clone().into(),
                    secrets_note: if !p.secrets.password.is_empty() && !p.secrets.token.is_empty() { "文件含凭据，将重新加密保存" } else { "凭据不完整，导入后在设置中填写" }.into(),
                    resume_note: if p.source.starts_with("Android") || p.source.starts_with("桌面") { if p.resume_at_login { "备份中开启；导入后需在设置中确认系统集成" } else { "关闭" } } else { "" }.into(),
                };
                self.shared.lock().unwrap().pending_import = Some(p);
                Some(preview)
            }
            Err(e) => {
                self.set_exchange_message(format!("导入检查未通过：{e}。原配置保持不变。"));
                None
            }
        };
        if let Some(preview) = preview {
            let _ = self.window.upgrade_in_event_loop(move |w| {
                let app = w.global::<App>();
                app.set_import_preview(preview);
                app.set_exchange_message("".into());
            });
        }
        self.set_exchange_busy(false);
    }

    /// 与 Android 顺序一致：清除运行意图 → 等待 stop → 原子保存 → 保持停止。
    fn confirm_import(&mut self) {
        let pending = self.shared.lock().unwrap().pending_import.take();
        let Some(p) = pending else {
            self.set_exchange_busy(false);
            return;
        };
        self.stop();
        let result = {
            let mut s = self.shared.lock().unwrap();
            let mut cfg = p.config;
            cfg.prefs.resume_at_login = false; // 系统集成由用户显式确认
            s.storage.save_config(&cfg).and_then(|_| s.storage.save_secrets(&p.secrets)).map(|_| {
                s.config = cfg;
                s.secrets = p.secrets;
                s.config_error = None;
            })
        };
        match result {
            Ok(()) => self.set_exchange_message("配置已替换，连接保持停止。".into()),
            Err(e) => self.set_exchange_message(format!("配置保存失败：{e}")),
        }
        let _ = self.window.upgrade_in_event_loop(|w| {
            let app = w.global::<App>();
            app.set_import_preview(ImportPreview::default());
        });
        self.set_exchange_busy(false);
        self.push_ui();
        crate::push_config_forms(&self.window, &self.shared);
    }

    fn export(&mut self, path: String, kind: String, include_secrets: bool) {
        let result = (|| -> Result<String, String> {
            let (cfg, secrets, dir) = {
                let s = self.shared.lock().unwrap();
                (s.config.clone(), s.secrets.clone(), s.storage.dir.clone())
            };
            let (text, default_name) = if kind == "backup" {
                (serde_json::to_string_pretty(&config::backup_document(&cfg, &secrets, include_secrets)).map_err(|e| e.to_string())?, "hole-desktop-backup.json")
            } else {
                let client = self.client.clone().ok_or("核心宿主未运行，无法生成 CLI YAML")?;
                let doc = config::to_portable_document(&cfg, &secrets, include_secrets)?;
                let value = client.call("encode_cli_config", Some(serde_json::json!({ "config": doc, "include_secrets": include_secrets }))).map_err(|e| e.display())?;
                (value["text"].as_str().ok_or("转换结果缺少 text")?.to_string(), "hole-config.yaml")
            };
            let target = if path.trim().is_empty() { dir.join(default_name) } else { std::path::PathBuf::from(path.trim()) };
            Storage::write_document(&target, &text, include_secrets)?;
            Ok(target.display().to_string())
        })();
        match result {
            Ok(p) => self.set_exchange_message(format!("已导出到 {p}")),
            Err(e) => self.set_exchange_message(format!("导出失败：{e}")),
        }
        self.set_exchange_busy(false);
    }

    fn set_exchange_message(&self, text: String) {
        let _ = self.window.upgrade_in_event_loop(move |w| w.global::<App>().set_exchange_message(text.into()));
    }

    fn set_exchange_busy(&self, busy: bool) {
        let _ = self.window.upgrade_in_event_loop(move |w| w.global::<App>().set_exchange_busy(busy));
    }

    fn build_report(&mut self) {
        let text = {
            let s = self.shared.lock().unwrap();
            report::diagnostic_report(&report::ReportContext {
                snapshot: &self.snapshot,
                config: &s.config,
                secrets: &s.secrets,
                gui_version: GUI_VERSION,
                bridge_version: self.hello.bridge_version,
                os_label: &os_label(),
                resume_at_login: s.config.prefs.resume_at_login,
                last_resume_error: &s.run_state.last_resume_error,
                now_rfc3339: &now_rfc3339(),
            })
        };
        let _ = self.window.upgrade_in_event_loop(move |w| w.global::<App>().set_report_text(text.into()));
    }

    fn quit(&mut self) {
        if let Some(c) = self.client.take() {
            if self.snapshot.run_requested {
                let _ = c.call("stop", None);
            }
            c.shutdown(Duration::from_secs(5));
        }
        let _ = slint::quit_event_loop();
    }

    // ---------- 视图模型 ----------

    fn push_ui(&mut self) {
        let status = self.build_status();
        let (provides, consumes, runtime) = self.build_mappings();
        let peers = self.build_peers();
        let events: Vec<EventRow> = self.events.iter().cloned().collect();
        let network = self.build_network_info();
        let versions = self.build_version_info();
        let host = self.build_host_info();
        let _ = self.window.upgrade_in_event_loop(move |w| {
            let app = w.global::<App>();
            app.set_status(status);
            app.set_provides(ModelRc::new(VecModel::from(provides.into_iter().map(MappingData::into_row).collect::<Vec<_>>())));
            app.set_consumes(ModelRc::new(VecModel::from(consumes.into_iter().map(MappingData::into_row).collect::<Vec<_>>())));
            app.set_runtime_mappings(ModelRc::new(VecModel::from(runtime.into_iter().map(MappingData::into_row).collect::<Vec<_>>())));
            app.set_peers(ModelRc::new(VecModel::from(peers.into_iter().map(PeerData::into_row).collect::<Vec<_>>())));
            app.set_events(ModelRc::new(VecModel::from(events)));
            app.set_network_info(ModelRc::new(VecModel::from(network)));
            app.set_version_info(ModelRc::new(VecModel::from(versions)));
            app.set_host(host);
        });
    }

    fn build_status(&self) -> Status {
        let s = &self.snapshot;
        let shared = self.shared.lock().unwrap();
        let cfg = &shared.config;
        let mode = cfg.connection.connection_mode.as_str();
        let (rate_up, rate_down, spark_up, spark_down) = self.traffic_series();
        let network_label = if s.network.transport.is_empty() || s.network.transport == "none" {
            if s.run_requested { "等待可用网络" } else { "未提供" }.to_string()
        } else {
            s.network.transport.clone()
        };
        let network_state = if !s.run_requested {
            "显示最近使用的网络"
        } else if s.network.validated {
            "系统已确认互联网可用"
        } else if s.network.available {
            "网络已连接，等待互联网验证"
        } else {
            "桌面平台未提供网络状态"
        };
        let error = s.error.as_ref().map(|e| format!("{} {}", snap::connection_issue(&e.code), if e.message.is_empty() { String::new() } else { format!("（{}）", e.message) })).unwrap_or_default();
        Status {
            bridge_ready: self.client.is_some(),
            bridge_error: self.bridge_error.clone().into(),
            run_requested: s.run_requested,
            configured: s.configured,
            busy: self.busy,
            engine_state: s.engine_state.clone().into(),
            engine_label: snap::engine_label(s).into(),
            signal_state: s.signal_state.clone().into(),
            signal_label: snap::signal_label(&s.signal_state).into(),
            title: snap::connection_title(s).into(),
            subtitle: if s.run_requested { format!("{} · {}", snap::engine_label(s), snap::signal_label(&s.signal_state)) } else { "开启总开关后连接房间并建立映射".into() }.into(),
            kind: snap::connection_kind(s).into(),
            error: error.trim().to_string().into(),
            command_error: self.command_error.clone().into(),
            config_error: shared.config_error.clone().unwrap_or_default().into(),
            uptime: snap::elapsed_label(&s.started_at, s.run_requested).into(),
            tcp_sessions: s.tcp_sessions().to_string().into(),
            udp_sessions: s.udp_sessions().to_string().into(),
            provide_enabled: cfg.provide.iter().filter(|e| e.enabled).count().to_string().into(),
            consume_enabled: cfg.consume.iter().filter(|e| e.enabled).count().to_string().into(),
            runtime_provide: s.provide_count().to_string().into(),
            runtime_consume: s.consume_count().to_string().into(),
            active_channels: s.active_mappings().to_string().into(),
            total_channels: s.mappings.len().to_string().into(),
            peers_active: s.peers.iter().filter(|p| matches!(p.state.as_str(), "active" | "switching")).count().to_string().into(),
            peers_total: s.peers.len().to_string().into(),
            network_label: network_label.into(),
            network_state: network_state.into(),
            policy_label: snap::connection_policy_label(mode).into(),
            configuration_text: snap::configuration_text(s).into(),
            recovery_text: format!("切网 {} 次 · 协调重连 {} 次", s.network_changes, s.reconnects).into(),
            session_timeout: cfg.connection.session_timeout.clone().into(),
            core_version: s.core_version.clone().into(),
            generation: s.generation.clone().into(),
            transport_generation: s.transport_generation.clone().into(),
            protocol_versions: format!("{} / {}", s.api_version, s.session_protocol).into(),
            traffic_up: snap::format_bytes(s.bytes_sent()).into(),
            traffic_down: snap::format_bytes(s.bytes_received()).into(),
            traffic_rate_up: rate_up.into(),
            traffic_rate_down: rate_down.into(),
            sparkline_up: spark_up.into(),
            sparkline_down: spark_down.into(),
            events_dropped: format!("{} / 桥接 {}", s.events_dropped, s.bridge_events_dropped).into(),
        }
    }

    /// 由累计字节差分出速率序列，生成 viewbox 100x32 的折线命令。
    fn traffic_series(&self) -> (String, String, String, String) {
        let samples: Vec<&TrafficSample> = self.history.iter().collect();
        if samples.len() < 2 {
            return (snap::format_rate(0.0), snap::format_rate(0.0), String::new(), String::new());
        }
        let mut up = Vec::with_capacity(samples.len());
        let mut down = Vec::with_capacity(samples.len());
        for pair in samples.windows(2) {
            let dt = pair[1].at.duration_since(pair[0].at).as_secs_f64().max(0.001);
            up.push(pair[1].sent.saturating_sub(pair[0].sent) as f64 / dt);
            down.push(pair[1].received.saturating_sub(pair[0].received) as f64 / dt);
        }
        let max = up.iter().chain(down.iter()).cloned().fold(1.0_f64, f64::max);
        let path = |series: &[f64]| -> String {
            let n = HISTORY_LEN.max(2) - 1;
            let mut out = String::new();
            let offset = n.saturating_sub(series.len());
            for (i, v) in series.iter().enumerate() {
                let x = (offset + i) as f64 / n as f64 * 100.0;
                let y = 30.0 - (v / max) * 28.0;
                out.push_str(if i == 0 { "M" } else { " L" });
                out.push_str(&format!(" {x:.1} {y:.1}"));
            }
            out
        };
        (snap::format_rate(*up.last().unwrap()), snap::format_rate(*down.last().unwrap()), path(&up), path(&down))
    }

    fn build_mappings(&self) -> (Vec<MappingData>, Vec<MappingData>, Vec<MappingData>) {
        let s = &self.snapshot;
        let shared = self.shared.lock().unwrap();
        let cfg = &shared.config;
        let runtime_state = |role: &str, id: &str| s.mappings.iter().find(|m| m.role == role && m.id == id);
        let provides = cfg
            .provide
            .iter()
            .map(|e| {
                let rt = runtime_state("provide", &e.id);
                let (state, label, kind) = match (e.enabled, rt) {
                    (false, _) => ("disabled", "已停用".to_string(), "idle"),
                    (true, Some(m)) => (m.state.as_str(), snap::mapping_label(&m.state).to_string(), snap::mapping_kind(&m.state)),
                    (true, None) => ("saved", if s.run_requested { "准备中".into() } else { "已启用".into() }, "idle"),
                };
                MappingData { details: vec![kv("host", &e.host), kv("port", &e.port.to_string())],
                    entry_id: e.entry_id.clone().into(),
                    id: e.id.clone().into(),
                    role: "provide".into(),
                    role_label: "提供服务".into(),
                    protocol: e.protocol.to_uppercase().into(),
                    endpoint: config::compose_service(&e.protocol, &e.host, e.port).into(),
                    enabled: e.enabled,
                    state: state.into(),
                    state_label: label.into(),
                    kind: kind.into(),
                    sessions: rt.map(|m| format!("TCP {} · UDP {}", m.tcp_sessions, m.udp_sessions)).unwrap_or_default().into(),
                    peer: rt.map(|m| m.peer.clone()).unwrap_or_default().into(),
                }
            })
            .collect();
        let consumes = cfg
            .consume
            .iter()
            .map(|e| {
                let rt = runtime_state("consume", &e.id);
                let (state, label, kind) = match (e.enabled, rt) {
                    (false, _) => ("disabled", "已停用".to_string(), "idle"),
                    (true, Some(m)) => (m.state.as_str(), snap::mapping_label(&m.state).to_string(), snap::mapping_kind(&m.state)),
                    (true, None) => ("saved", if s.run_requested { "等待匹配".into() } else { "已启用".into() }, "idle"),
                };
                MappingData { details: vec![kv("host", &e.host), kv("port", &e.port.to_string())],
                    entry_id: e.entry_id.clone().into(),
                    id: e.id.clone().into(),
                    role: "consume".into(),
                    role_label: "使用服务".into(),
                    protocol: rt.map(|m| m.protocol.to_uppercase()).filter(|p| !p.is_empty()).unwrap_or_default().into(),
                    endpoint: config::compose_expose(&e.host, e.port).into(),
                    enabled: e.enabled,
                    state: state.into(),
                    state_label: label.into(),
                    kind: kind.into(),
                    sessions: rt.map(|m| format!("TCP {} · UDP {}", m.tcp_sessions, m.udp_sessions)).unwrap_or_default().into(),
                    peer: rt.map(|m| m.peer.clone()).unwrap_or_default().into(),
                }
            })
            .collect();
        let runtime = s
            .mappings
            .iter()
            .map(|m| MappingData { details: vec![
                    kv("传输方式", match m.profile.as_str() { "legacy-ipv6-quic-v2" => "IPv6 直连", "ice-quic-mux-v1" => "ICE · 多服务共享 QUIC", _ => "等待协商" }),
                    kv("线路地址", if m.path.is_empty() { "由设备线路统一承载" } else { &m.path }),
                    kv("TCP 应用上行 / 下行", &format!("{} / {}", snap::format_bytes_str(&m.tcp_read_bytes), snap::format_bytes_str(&m.tcp_written_bytes))),
                    kv("TCP 待确认数据", &snap::format_bytes_str(&m.replay_bytes)),
                    kv("当前服务提示", &m.error.as_ref().map(|e| format!("{}（{}）", e.message, e.code)).unwrap_or_else(|| "无".into())),
                ],
                entry_id: "".into(),
                id: m.id.clone().into(),
                role: m.role.clone().into(),
                role_label: if m.role == "provide" { "提供服务" } else { "使用服务" }.into(),
                protocol: m.protocol.to_uppercase().into(),
                endpoint: m.endpoint.clone().into(),
                enabled: true,
                state: m.state.clone().into(),
                state_label: snap::mapping_label(&m.state).into(),
                kind: snap::mapping_kind(&m.state).into(),
                sessions: format!("TCP {} · UDP {}", m.tcp_sessions, m.udp_sessions).into(),
                peer: m.peer.clone().into(),
            })
            .collect();
        (provides, consumes, runtime)
    }

    fn build_peers(&self) -> Vec<PeerData> {
        self.snapshot
            .peers
            .iter()
            .map(|p| {
                let mut details = vec![
                    kv("连接过程", &snap::peer_phase_label(p)),
                    kv("中继顺序", if p.relay_policy == "udp-tcp-tls-v1" { "UDP → TCP（80、3478）→ TLS（443、5349）" } else { "兼容阶段 · 对端或 Worker 尚未支持新顺序" }),
                ];
                if p.path_type == "relay" {
                    details.push(kv("中继使用方", match p.relay_side.as_str() { "both" => "本机与对端", "local" => "本机", "remote" => "对端", _ => "等待确认" }));
                    details.push(kv("中继接入", &snap::relay_path_label(p)));
                }
                details.extend([
                    kv("本机连接", &snap::selected_connection_label(&p.local_type, &p.local_address)),
                    kv("对端连接", &snap::selected_connection_label(&p.remote_type, &p.remote_address)),
                    kv("已发现候选地址", &format!("本机 {} · 对端 {}", p.local_candidates, p.remote_candidates)),
                    kv("建连耗时", &if p.connect_ms == "0" { "尚未完成".to_string() } else { format!("{} ms", p.connect_ms) }),
                    kv("路径重试", &format!("{} 次", p.retry_count)),
                    kv("丢弃的 UDP 报文", &p.dropped_datagrams),
                    kv("本机中继来源", match p.relay_state.as_str() { "ready" => "短期凭据已就绪", "manual" => "手动配置", "off" => "不申请本机中继", "unavailable" | "expired" => "暂不可用，直连仍可使用", _ => "准备中" }),
                ]);
                if p.turn_expires_at != 0 {
                    details.push(kv("中继凭据剩余时间", &snap::remaining_time(p.turn_expires_at)));
                }
                details.push(kv("授权确认剩余时间", &snap::remaining_time(p.lease_until)));
                details.push(kv("传输标识", &format!("{} · 第 {} 次路径", p.transport_id, p.generation)));
                PeerData { details,
                    peer_id: p.peer_id.clone().into(),
                    transport_id: p.transport_id.clone().into(),
                    state_label: snap::peer_state_label(&p.state).into(),
                    path_label: snap::peer_path_label(p).into(),
                    kind: snap::peer_kind(&p.state).into(),
                    channels: format!("{} / {}", p.active_channels, p.mapping_count).into(),
                    rtt: if p.rtt_ms == "0" { "正在测量".to_string() } else { format!("{} ms", p.rtt_ms) }.into(),
                    traffic: format!("{} / {}", snap::format_bytes(p.bytes_sent), snap::format_bytes(p.bytes_received)).into(),
                    note: if p.pending_phase.is_empty() { String::new() } else { format!("同时{}，新路径就绪后切换。", snap::phase_label(&p.pending_phase)) }.into(),
                    error: p.error.as_ref().filter(|_| !matches!(p.state.as_str(), "active" | "switching")).map(|e| snap::connection_issue(&e.code).to_string()).unwrap_or_default().into(),
                }
            })
            .collect()
    }

    fn build_network_info(&self) -> Vec<KV> {
        let s = &self.snapshot;
        let shared = self.shared.lock().unwrap();
        let c = &shared.config.connection;
        let ipv6_only = c.connection_mode == "legacy";
        vec![
            kv("网卡", if s.network.interface.is_empty() { "由系统选择" } else { &s.network.interface }),
            kv("本机地址", &if s.network.addresses.is_empty() { "桌面平台未提供".to_string() } else { s.network.addresses.join("\n") }),
            kv("DNS", &if s.network.dns.is_empty() { "由当前网络提供".to_string() } else { s.network.dns.join("\n") }),
            kv("STUN", &if ipv6_only { "未使用（仅 IPv6 模式）".to_string() } else if c.ice.stun_urls.is_empty() { "未启用，仅探测本地地址".to_string() } else { c.ice.stun_urls.join("\n") }),
            kv("计费网络", if s.network.metered { "是" } else { "未提供" }),
            kv("网络绑定", if s.network_binding { "外部连接与 DNS 跟随所选网络" } else { "使用系统网络" }),
        ]
    }

    fn build_version_info(&self) -> Vec<KV> {
        let s = &self.snapshot;
        let mut items = vec![
            kv("GUI", &format!("{GUI_VERSION} · Slint {}", slint_version())),
            kv("共享核心", if s.core_version.is_empty() { &self.hello.core_version } else { &s.core_version }),
            kv("桥接协议 / API", &format!("{} / {}", self.hello.bridge_version, self.hello.api_version)),
            kv("会话协议", &s.session_protocol.to_string()),
            kv("运行 / 网络重建编号", &format!("{} / {}", s.generation, s.transport_generation)),
            kv("传输能力", &s.transport_label),
        ];
        if let Some(e) = &s.error {
            items.push(kv("当前错误详情", &format!("{}: {}", e.code, e.message)));
        }
        items
    }

    fn build_host_info(&self) -> HostInfo {
        let shared = self.shared.lock().unwrap();
        HostInfo {
            os_label: os_label().into(),
            gui_version: GUI_VERSION.into(),
            bridge_path: if self.bridge_path.is_empty() { "未找到".into() } else { self.bridge_path.clone().into() },
            config_path: shared.storage.dir.display().to_string().into(),
            tray_available: false,
            tray_note: "系统托盘尚未接入；隐藏窗口时转发继续由本进程保持。".into(),
            autostart_note: "登录自启动尚未接入系统；开启后仅在本应用启动时按运行意图恢复。".into(),
            last_resume_error: shared.run_state.last_resume_error.clone().into(),
        }
    }
}

fn kv(key: &str, value: &str) -> KV {
    KV { key: key.into(), value: value.into() }
}

fn slint_version() -> &'static str {
    "1.18"
}

pub fn os_label() -> String {
    format!("{} {}", std::env::consts::OS, std::env::consts::ARCH)
}

pub fn now_rfc3339() -> String {
    let secs = std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).map(|d| d.as_secs() as i64).unwrap_or(0);
    // civil from days (Howard Hinnant)
    let days = secs.div_euclid(86400);
    let rem = secs.rem_euclid(86400);
    let z = days + 719468;
    let era = z.div_euclid(146097);
    let doe = z - era * 146097;
    let yoe = (doe - doe / 1460 + doe / 36524 - doe / 146096) / 365;
    let y = yoe + era * 400;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = doy - (153 * mp + 2) / 5 + 1;
    let m = if mp < 10 { mp + 3 } else { mp - 9 };
    let y = if m <= 2 { y + 1 } else { y };
    format!("{y:04}-{m:02}-{d:02}T{:02}:{:02}:{:02}Z", rem / 3600, rem / 60 % 60, rem % 60)
}

#[allow(dead_code)]
fn model_len<T: Clone + 'static>(m: &ModelRc<T>) -> usize {
    m.row_count()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rfc3339_now_shape() {
        let t = now_rfc3339();
        assert_eq!(t.len(), 20);
        assert!(t.ends_with('Z'));
        assert_eq!(snap::parse_rfc3339_secs(&t).is_some(), true);
    }
}
