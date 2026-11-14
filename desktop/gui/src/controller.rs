//! 单一工作线程拥有核心客户端；页面只发送用户意图，刷新只读快照。
use crate::{
    config::{self, Secrets, StoredConfig},
    core_client::{CoreClient, Hello, Inbound},
    model::NetworkIntent,
    report,
    snapshot::{self, Snapshot},
    storage::{RunState, Storage},
    voice_audio::VoiceAudio,
};
use serde_json::{Value, json};
use std::{
    collections::VecDeque,
    sync::{
        Arc,
        mpsc::{self, Receiver, RecvTimeoutError, Sender},
    },
    thread,
    time::{Duration, Instant},
};

pub const GUI_VERSION: &str = env!("CARGO_PKG_VERSION");

#[derive(Clone, Default)]
pub struct State {
    pub snapshot: Snapshot,
    pub ready: bool,
    pub error: String,
    pub message: String,
    pub host: String,
    pub voice_error: String,
    pub voice_route: String,
    pub voice_running: bool,
    pub events: Vec<Value>,
    pub up_rate: f64,
    pub down_rate: f64,
}

#[cfg(all(test, feature = "embedded-core"))]
mod tests {
    use super::*;
    fn state(controller: &Controller, until: impl Fn(&State) -> bool) -> State {
        let deadline = Instant::now() + Duration::from_secs(10);
        loop {
            let update = controller
                .updates
                .recv_timeout(deadline.saturating_duration_since(Instant::now()))
                .expect("等待核心状态超时");
            if let Update::State(state) = update {
                if until(&state) {
                    return *state;
                }
            }
        }
    }
    #[test]
    fn real_core_keeps_run_intent_and_generation_when_only_preferences_change() {
        let directory =
            std::env::temp_dir().join(format!("hole-controller-{}", uuid::Uuid::new_v4()));
        std::fs::create_dir_all(&directory).unwrap();
        let mut config = StoredConfig::default();
        config.connection.server_url = "wss://127.0.0.1:9/ws".into();
        config.connection.room = "controller-contract".into();
        config.connection.device_name = "desktop".into();
        config.connection.ice.stun_urls.clear();
        config.connection.ice.nat_stun_urls = Some(vec![]);
        config.connection.ice.port_mapping = "off".into();
        config.connection.turn.mode = "off".into();
        let secrets = Secrets {
            password: "fixture".into(),
            token: "fixture".into(),
            ..Default::default()
        };
        let mut controller = Controller::with_host(
            Storage {
                dir: directory.clone(),
            },
            config.clone(),
            secrets.clone(),
            RunState::default(),
            String::new(),
            crate::core_host::embedded,
        );
        let ready = state(&controller, |s| s.ready);
        assert!(!ready.snapshot.run_requested);
        assert_eq!(ready.snapshot.generation, "0");
        controller.send(Command::Start);
        let started = state(&controller, |s| s.snapshot.run_requested);
        let generation = started.snapshot.generation;
        assert!(generation.parse::<u64>().unwrap() > 0);
        config.prefs.theme_mode = "dark".into();
        controller.send(Command::Save(Box::new(config), secrets));
        let saved = state(&controller, |s| s.message == "已保存");
        assert!(saved.snapshot.run_requested);
        assert_eq!(saved.snapshot.generation, generation);
        controller.send(Command::Network(NetworkIntent::ReselectPath));
        controller.send(Command::Refresh);
        let reselected = state(&controller, |s| s.snapshot.run_requested);
        assert_eq!(
            reselected.snapshot.network_changes,
            started.snapshot.network_changes
        );
        controller.send(Command::Stop);
        let stopped = state(&controller, |s| !s.snapshot.run_requested);
        assert!(!stopped.snapshot.run_requested);
        controller.finish();
        let run: RunState = serde_json::from_str(
            &std::fs::read_to_string(directory.join("run-state.json")).unwrap(),
        )
        .unwrap();
        assert!(!run.run_requested);
        std::fs::remove_dir_all(directory).unwrap();
    }
}

pub struct ImportPreview {
    pub config: StoredConfig,
    pub secrets: Secrets,
    pub source: String,
}
pub enum Update {
    State(Box<State>),
    Saved(Box<StoredConfig>, Secrets),
    Preview(Box<ImportPreview>),
    Document(String),
    Process {
        token: u64,
        result: Result<Value, String>,
    },
    RunFinished,
}

pub enum Command {
    Start,
    Stop,
    Network(NetworkIntent),
    Refresh,
    Visible(bool),
    Save(Box<StoredConfig>, Secrets),
    NATProbe,
    CancelNATProbe,
    VoiceMuted(bool),
    RetryVoiceAudio,
    Import(String),
    ConfirmImport,
    Export {
        path: String,
        backup: bool,
        secrets: bool,
    },
    Report,
    Process {
        token: u64,
        transport: String,
        after: String,
        generation: String,
        stream: String,
    },
    Quit,
}
enum Message {
    Command(Command),
    Inbound(Inbound),
}
pub struct Controller {
    tx: Sender<Message>,
    pub updates: Receiver<Update>,
    thread: Option<thread::JoinHandle<()>>,
}
impl Controller {
    pub fn start(
        storage: Storage,
        config: StoredConfig,
        secrets: Secrets,
        run_state: RunState,
        error: String,
    ) -> Self {
        Self::with_host(
            storage,
            config,
            secrets,
            run_state,
            error,
            crate::core_host::prepare,
        )
    }
    fn with_host(
        storage: Storage,
        config: StoredConfig,
        secrets: Secrets,
        run_state: RunState,
        error: String,
        prepare: impl FnOnce() -> Result<crate::core_host::PreparedCore, String> + Send + 'static,
    ) -> Self {
        let (tx, rx) = mpsc::channel();
        let (updates, update_rx) = mpsc::channel();
        let wake = tx.clone();
        let thread = thread::Builder::new()
            .name("hole-controller".into())
            .spawn(move || {
                Worker {
                    storage,
                    config,
                    secrets,
                    run_state,
                    rx,
                    updates,
                    client: None,
                    hello: Hello::default(),
                    state: State {
                        error,
                        ..Default::default()
                    },
                    events: VecDeque::new(),
                    visible: true,
                    muted: false,
                    audio: None,
                    preview: None,
                    last_event: (0, 0),
                    last_read: Instant::now() - Duration::from_secs(1),
                    sample: None,
                }
                .run(wake, prepare);
            })
            .expect("控制器线程启动失败");
        Self {
            tx,
            updates: update_rx,
            thread: Some(thread),
        }
    }
    pub fn send(&self, command: Command) {
        let _ = self.tx.send(Message::Command(command));
    }
    pub fn finish(&mut self) {
        self.send(Command::Quit);
        if let Some(thread) = self.thread.take() {
            let _ = thread.join();
        }
    }
}
impl Drop for Controller {
    fn drop(&mut self) {
        self.finish();
    }
}

struct Worker {
    storage: Storage,
    config: StoredConfig,
    secrets: Secrets,
    run_state: RunState,
    rx: Receiver<Message>,
    updates: Sender<Update>,
    client: Option<Arc<CoreClient>>,
    hello: Hello,
    state: State,
    events: VecDeque<Value>,
    visible: bool,
    muted: bool,
    audio: Option<VoiceAudio>,
    preview: Option<ImportPreview>,
    last_event: (u64, u64),
    last_read: Instant,
    sample: Option<(Instant, u64, u64)>,
}
impl Worker {
    fn run(
        mut self,
        wake: Sender<Message>,
        prepare: impl FnOnce() -> Result<crate::core_host::PreparedCore, String>,
    ) {
        let connected = (|| {
            let host = prepare()?;
            self.state.host = host.description.clone();
            let client = CoreClient::spawn(host, move |inbound| {
                let _ = wake.send(Message::Inbound(inbound));
            })?;
            let hello = client.hello().map_err(|e| e.display())?;
            if hello.bridge_version != crate::core_client::BRIDGE_VERSION
                || hello.api_version != crate::core_client::API_VERSION
            {
                client.shutdown(Duration::from_secs(3));
                return Err("核心宿主协议不匹配，请更新配套核心".into());
            }
            self.hello = hello;
            self.client = Some(client);
            self.state.ready = true;
            self.refresh()?;
            Ok::<(), String>(())
        })();
        if let Err(error) = connected {
            self.state.error = error;
        }
        if self.state.ready && self.config.prefs.resume_at_login && self.run_state.run_requested {
            if let Err(error) = self.start() {
                self.state.error = error.clone();
                self.run_state.last_resume_error = error;
                let _ = self.storage.save_run_state(&self.run_state);
            }
        }
        self.publish();
        loop {
            let probe = matches!(
                self.state.snapshot.nat_diagnostic["state"].as_str(),
                Some("running" | "queued" | "probing" | "complete" | "insufficient")
            );
            let interval = if self.state.snapshot.voice.enabled {
                Duration::from_millis(500)
            } else if self.visible {
                Duration::from_secs(2)
            } else {
                Duration::from_secs(15)
            };
            let interval = if self.state.snapshot.run_requested || probe {
                interval
            } else {
                Duration::from_secs(3600)
            };
            if self.state.ready && self.last_read.elapsed() >= interval {
                if let Err(error) = self.refresh() {
                    self.state.error = error;
                    self.last_read = Instant::now();
                }
                self.publish();
            }
            let wait = if self.state.ready {
                interval.saturating_sub(self.last_read.elapsed())
            } else {
                Duration::from_secs(3600)
            };
            match self.rx.recv_timeout(wait) {
                Ok(Message::Command(Command::Quit)) | Err(RecvTimeoutError::Disconnected) => break,
                Ok(Message::Command(command)) => {
                    let run_command = matches!(
                        command,
                        Command::Start | Command::Stop | Command::Network(_)
                    );
                    if let Err(error) = self.command(command) {
                        self.state.error = error;
                    }
                    self.publish();
                    if run_command {
                        self.updates.send(Update::RunFinished).ok();
                    }
                }
                Ok(Message::Inbound(Inbound::Exited(reason))) => {
                    self.audio.take();
                    self.client.take();
                    self.state.ready = false;
                    self.state.error = reason.clone();
                    self.state.snapshot = Snapshot::failure("bridge_exited", &reason);
                    self.publish();
                }
                Ok(Message::Inbound(Inbound::Event(event))) => {
                    if event["kind"] == "log" {
                        continue;
                    }
                    let number = |key: &str| {
                        event[key]
                            .as_str()
                            .and_then(|s| s.parse::<u64>().ok())
                            .unwrap_or(0)
                    };
                    let order = (number("generation"), number("sequence"));
                    if order.1 != 0 && order <= self.last_event {
                        continue;
                    }
                    self.last_event = order;
                    self.events.push_front(event);
                    self.events.truncate(200);
                    if self.last_read.elapsed() >= Duration::from_millis(100) {
                        if let Err(error) = self.refresh() {
                            self.state.error = error;
                        }
                        self.publish();
                    }
                }
                Err(RecvTimeoutError::Timeout) => {
                    if let Err(error) = self.refresh() {
                        self.state.error = error;
                        self.last_read = Instant::now();
                    }
                    self.publish();
                }
            }
        }
        self.stop_audio();
        if let Some(client) = self.client.take() {
            client.shutdown(Duration::from_secs(5));
        }
    }
    fn rpc(&self, method: &str, params: Option<Value>) -> Result<Value, String> {
        self.client
            .as_ref()
            .ok_or("核心宿主未运行")?
            .call(method, params)
            .map_err(|e| e.display())
    }
    fn start(&mut self) -> Result<(), String> {
        let request = config::to_run_request(&self.config, &self.secrets)?;
        self.rpc("start", Some(request))?;
        self.run_state.run_requested = true;
        self.storage.save_run_state(&self.run_state)?;
        self.refresh()
    }
    fn command(&mut self, command: Command) -> Result<(), String> {
        self.state.error.clear();
        match command {
            Command::Start => self.start()?,
            Command::Stop => {
                self.stop_audio();
                self.rpc("stop", None)?;
                self.run_state.run_requested = false;
                self.storage.save_run_state(&self.run_state)?;
                self.refresh()?;
            }
            Command::Network(intent) => {
                self.rpc(intent.method(), None)?;
                self.refresh()?;
            }
            Command::Refresh => self.refresh()?,
            Command::Visible(visible) => {
                self.visible = visible;
                if visible {
                    self.refresh()?;
                }
            }
            Command::Save(config, secrets) => self.save(*config, secrets)?,
            Command::NATProbe => {
                let ice = &self.config.connection.ice;
                self.rpc("start_nat_probe", Some(json!({"stun_urls":ice.stun_urls,"nat_stun_urls":ice.nat_stun_urls,"interface_allowlist":ice.interface_allowlist})))?;
                self.refresh()?;
            }
            Command::CancelNATProbe => {
                self.rpc("cancel_nat_probe", None)?;
                self.refresh()?;
            }
            Command::VoiceMuted(muted) => {
                self.rpc("set_voice_muted", Some(json!({"muted":muted})))?;
                self.muted = muted;
                self.refresh()?;
            }
            Command::RetryVoiceAudio => {
                self.stop_audio();
                self.state.voice_error.clear();
                self.refresh()?;
            }
            Command::Import(text) => {
                if text.len() > crate::storage::MAX_DOCUMENT_BYTES {
                    return Err("导入内容超过 128 KiB".into());
                }
                let preview = if let Ok(backup) = config::read_backup(&text, &self.config) {
                    ImportPreview {
                        config: backup.config,
                        secrets: backup.secrets,
                        source: backup.source.into(),
                    }
                } else {
                    let decoded = self.rpc("decode_cli_config", Some(json!({"text":text})))?;
                    let (config, secrets) =
                        config::from_portable_document(&decoded["config"], &self.config)?;
                    ImportPreview {
                        config,
                        secrets,
                        source: "CLI YAML / JSON".into(),
                    }
                };
                self.updates
                    .send(Update::Preview(Box::new(ImportPreview {
                        config: preview.config.clone(),
                        secrets: preview.secrets.clone(),
                        source: preview.source.clone(),
                    })))
                    .ok();
                self.preview = Some(preview);
            }
            Command::ConfirmImport => {
                let mut preview = self.preview.take().ok_or("请先预览导入内容")?;
                preview.config.prefs.resume_at_login = false;
                self.save(preview.config, preview.secrets)?;
            }
            Command::Export {
                path,
                backup,
                secrets: include_secrets,
            } => {
                let text = if backup {
                    serde_json::to_string_pretty(&config::backup_document(
                        &self.config,
                        &self.secrets,
                        include_secrets,
                    ))
                    .map_err(|e| e.to_string())?
                } else {
                    let document =
                        config::to_portable_document(&self.config, &self.secrets, include_secrets)?;
                    self.rpc(
                        "encode_cli_config",
                        Some(json!({"config":document,"include_secrets":include_secrets})),
                    )?["text"]
                        .as_str()
                        .ok_or("转换结果缺少 text")?
                        .to_string()
                };
                if path.trim().is_empty() {
                    self.updates.send(Update::Document(text)).ok();
                } else {
                    Storage::write_document(
                        std::path::Path::new(path.trim()),
                        &text,
                        include_secrets,
                    )?;
                    self.state.message = format!("已导出到 {}", path.trim());
                }
            }
            Command::Report => {
                let text = report::diagnostic_report(&report::ReportContext {
                    snapshot: &self.state.snapshot,
                    config: &self.config,
                    secrets: &self.secrets,
                    gui_version: GUI_VERSION,
                    bridge_version: self.hello.bridge_version,
                    os_label: std::env::consts::OS,
                    resume_at_login: self.config.prefs.resume_at_login,
                    last_resume_error: &self.run_state.last_resume_error,
                    now_rfc3339: &chrono::Utc::now().to_rfc3339(),
                });
                self.updates.send(Update::Document(text)).ok();
            }
            Command::Process {
                token,
                transport,
                after,
                generation,
                stream,
            } => {
                let result = if self.hello.methods.iter().any(|m| m == "nat_punch_process") {
                    self.rpc("nat_punch_process", Some(json!({"transport_id":transport,"after_sequence":after,"generation":generation,"stream":stream,"limit":256})))
                } else {
                    Err("当前核心未提供打洞过程，请更新配套核心".into())
                };
                self.updates.send(Update::Process { token, result }).ok();
            }
            Command::Quit => {}
        }
        Ok(())
    }
    fn save(&mut self, config: StoredConfig, secrets: Secrets) -> Result<(), String> {
        config::validate_draft(&config)?;
        let request = config::to_run_request(&config, &secrets);
        let changed = config::to_run_request(&self.config, &self.secrets).ok()
            != request.as_ref().ok().cloned();
        self.storage.save_secrets(&secrets)?;
        self.storage.save_config(&config)?;
        self.config = config;
        self.secrets = secrets;
        self.updates
            .send(Update::Saved(
                Box::new(self.config.clone()),
                self.secrets.clone(),
            ))
            .ok();
        self.state.message = "已保存".into();
        if self.state.snapshot.run_requested && changed {
            match request {
                Ok(request) => {
                    self.stop_audio();
                    self.state.voice_error.clear();
                    self.rpc("apply_config", Some(request))?;
                    self.state.message = "已保存，已提交核心应用".into();
                    self.refresh()?;
                }
                Err(error) => self.state.message = format!("已保存，暂未应用：{error}"),
            }
        }
        Ok(())
    }
    fn refresh(&mut self) -> Result<(), String> {
        let snapshot = snapshot::parse_snapshot_result(&self.rpc("snapshot", None)?)?;
        let now = Instant::now();
        if let Some((at, up, down)) = self.sample {
            let elapsed = now.duration_since(at).as_secs_f64().max(0.001);
            self.state.up_rate = snapshot.bytes_sent().saturating_sub(up) as f64 / elapsed;
            self.state.down_rate = snapshot.bytes_received().saturating_sub(down) as f64 / elapsed;
        }
        self.sample = Some((now, snapshot.bytes_sent(), snapshot.bytes_received()));
        self.state.snapshot = snapshot;
        self.last_read = now;
        self.sync_audio();
        Ok(())
    }
    fn stop_audio(&mut self) {
        if self.audio.take().is_some() {
            let _ = self.rpc("set_voice_audio", Some(json!({"enabled":false})));
        }
        self.state.voice_running = false;
        self.state.voice_route.clear();
    }
    fn sync_audio(&mut self) {
        if !self.config.voice.enabled
            || !self.state.snapshot.run_requested
            || !self.state.snapshot.voice.enabled
        {
            self.stop_audio();
            return;
        }
        if let Some(error) = self.audio.as_ref().and_then(|audio| audio.error()) {
            self.stop_audio();
            self.state.voice_error = error;
        }
        if self.audio.is_some()
            || !self.state.voice_error.is_empty()
            || self.state.snapshot.engine_state != "running"
        {
            return;
        }
        let result = (|| {
            self.rpc("set_voice_muted", Some(json!({"muted":self.muted})))?;
            let response = self.rpc("set_voice_audio", Some(json!({"enabled":true})))?;
            let id = response["stream_id"]
                .as_str()
                .and_then(|id| id.parse::<u64>().ok())
                .filter(|id| *id != 0)
                .ok_or("语音流标识无效")?;
            VoiceAudio::open(self.client.clone().ok_or("核心未运行")?, id)
        })();
        match result {
            Ok(audio) => {
                self.state.voice_route = audio.route.clone();
                self.state.voice_running = true;
                self.audio = Some(audio);
            }
            Err(error) => {
                let _ = self.rpc("set_voice_audio", Some(json!({"enabled":false})));
                self.state.voice_error = error;
            }
        }
    }
    fn publish(&mut self) {
        self.state.events = self.events.iter().cloned().collect();
        self.updates
            .send(Update::State(Box::new(self.state.clone())))
            .ok();
    }
}
