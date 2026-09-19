//! Go 宿主子进程与 JSON-RPC/NDJSON 客户端。
//!
//! - 以绝对路径直接启动宿主，不经过 shell，不搜索 PATH。
//! - stdout 按 LF 分帧并兼容 CRLF、半行与一读多行；stderr 单独读取并丢弃到诊断缓冲。
//! - 请求由字符串 ID 关联；事件通知经回调交给控制器；宿主退出时清空在途请求。

use serde::de::DeserializeOwned;
use serde_json::{json, Value};
use std::collections::HashMap;
use std::io::{BufRead, BufReader, Write};
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::mpsc::{self, Sender};
use std::sync::{Arc, Mutex};
use std::thread;
use std::time::{Duration, Instant};

pub const BRIDGE_VERSION: i64 = 1;
pub const API_VERSION: i64 = 1;
/// 单输出消息上限（与宿主 output_bytes 对齐），超出视为协议损坏。
const MAX_LINE_BYTES: usize = 2 * 1024 * 1024 + 16;
const REQUEST_TIMEOUT: Duration = Duration::from_secs(20);
const MAX_INFLIGHT: usize = 32;

#[derive(Debug, Clone)]
pub struct RpcError {
    pub code: i64,
    pub message: String,
    pub fault_code: Option<String>,
    pub fault_message: Option<String>,
}

impl RpcError {
    fn local(message: impl Into<String>) -> Self {
        RpcError { code: 0, message: message.into(), fault_code: None, fault_message: None }
    }
    /// 可直接展示的文本；核心 Fault 已由宿主脱敏。
    pub fn display(&self) -> String {
        match (&self.fault_code, &self.fault_message) {
            (Some(code), Some(msg)) => format!("{msg}（{code}）"),
            _ => self.message.clone(),
        }
    }
}

impl std::fmt::Display for RpcError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(&self.display())
    }
}

/// 从宿主流出的消息，交给控制器线程处理。
#[derive(Debug)]
pub enum Inbound {
    Event(Value),
    /// 宿主进程结束；附退出说明。
    Exited(String),
}

#[derive(Debug, Clone, Default)]
pub struct Hello {
    pub bridge_version: i64,
    pub api_version: i64,
    pub core_version: String,
    pub methods: Vec<String>,
}

struct Pending {
    tx: Sender<Result<Value, RpcError>>,
    since: Instant,
}

struct Shared {
    pending: Mutex<HashMap<String, Pending>>,
    stdin: Mutex<Option<std::process::ChildStdin>>,
    alive: Mutex<bool>,
}

pub struct CoreClient {
    shared: Arc<Shared>,
    child: Mutex<Option<Child>>,
    seq: AtomicU64,
    pub path: PathBuf,
}

impl CoreClient {
    /// 启动宿主并开始读取两条流；事件与退出通过 `on_inbound` 交付。
    pub fn spawn(path: &Path, on_inbound: impl Fn(Inbound) + Send + Sync + 'static) -> Result<Arc<Self>, String> {
        let inbound: Arc<dyn Fn(Inbound) + Send + Sync> = Arc::new(on_inbound);
        if !path.is_absolute() {
            return Err(format!("宿主路径必须是绝对路径：{}", path.display()));
        }
        let mut command = Command::new(path);
        command.stdin(Stdio::piped()).stdout(Stdio::piped()).stderr(Stdio::piped());
        #[cfg(windows)]
        {
            use std::os::windows::process::CommandExt;
            const CREATE_NO_WINDOW: u32 = 0x0800_0000;
            command.creation_flags(CREATE_NO_WINDOW);
        }
        let mut child = command.spawn().map_err(|e| format!("无法启动核心宿主 {}：{e}", path.display()))?;
        let stdin = child.stdin.take().ok_or("宿主 stdin 不可用")?;
        let stdout = child.stdout.take().ok_or("宿主 stdout 不可用")?;
        let stderr = child.stderr.take().ok_or("宿主 stderr 不可用")?;

        let shared = Arc::new(Shared {
            pending: Mutex::new(HashMap::new()),
            stdin: Mutex::new(Some(stdin)),
            alive: Mutex::new(true),
        });

        // stderr：诊断输出，读到 EOF 为止；不进入 UI 日志，避免夹带凭据。
        thread::Builder::new()
            .name("hole-core-stderr".into())
            .spawn(move || {
                let reader = BufReader::new(stderr);
                for line in reader.split(b'\n') {
                    if line.is_err() {
                        break;
                    }
                }
            })
            .map_err(|e| e.to_string())?;

        let reader_shared = shared.clone();
        let reader_inbound = inbound.clone();
        thread::Builder::new()
            .name("hole-core-stdout".into())
            .spawn(move || {
                let mut reader = BufReader::with_capacity(64 * 1024, stdout);
                let mut buffer: Vec<u8> = Vec::with_capacity(64 * 1024);
                let mut reason = String::from("核心宿主已退出");
                loop {
                    buffer.clear();
                    match reader.read_until(b'\n', &mut buffer) {
                        Ok(0) => break,
                        Ok(_) => {}
                        Err(e) => {
                            reason = format!("读取核心宿主输出失败：{e}");
                            break;
                        }
                    }
                    if buffer.len() > MAX_LINE_BYTES {
                        reason = "核心宿主输出超过上限，已断开".into();
                        break;
                    }
                    while matches!(buffer.last(), Some(b'\n') | Some(b'\r')) {
                        buffer.pop();
                    }
                    if buffer.iter().all(|b| b.is_ascii_whitespace()) {
                        continue;
                    }
                    let value: Value = match serde_json::from_slice(&buffer) {
                        Ok(v) => v,
                        Err(_) => continue, // 非法行只影响自身；宿主保证输出合法 JSON
                    };
                    dispatch(&reader_shared, &reader_inbound, value);
                }
                *reader_shared.alive.lock().unwrap() = false;
                fail_all(&reader_shared, &reason);
                reader_inbound(Inbound::Exited(reason));
            })
            .map_err(|e| e.to_string())?;

        Ok(Arc::new(CoreClient {
            shared,
            child: Mutex::new(Some(child)),
            seq: AtomicU64::new(1),
            path: path.to_path_buf(),
        }))
    }

    pub fn is_alive(&self) -> bool {
        *self.shared.alive.lock().unwrap()
    }

    fn next_id(&self, method: &str) -> String {
        let n = self.seq.fetch_add(1, Ordering::Relaxed);
        format!("{method}-{n}")
    }

    /// 同步请求；在控制器线程调用，不在 UI 线程调用。
    pub fn call(&self, method: &str, params: Option<Value>) -> Result<Value, RpcError> {
        if !self.is_alive() {
            return Err(RpcError::local("核心宿主未运行"));
        }
        let id = self.next_id(method);
        let mut message = json!({ "jsonrpc": "2.0", "id": id, "method": method });
        if let Some(p) = params {
            message["params"] = p;
        }
        let mut line = serde_json::to_vec(&message).map_err(|e| RpcError::local(e.to_string()))?;
        line.push(b'\n');

        let (tx, rx) = mpsc::channel();
        {
            let mut pending = self.shared.pending.lock().unwrap();
            pending.retain(|_, p| p.since.elapsed() < REQUEST_TIMEOUT * 3);
            if pending.len() >= MAX_INFLIGHT {
                return Err(RpcError::local("在途请求过多，请稍后再试"));
            }
            pending.insert(id.clone(), Pending { tx, since: Instant::now() });
        }
        {
            let mut guard = self.shared.stdin.lock().unwrap();
            let Some(stdin) = guard.as_mut() else {
                self.shared.pending.lock().unwrap().remove(&id);
                return Err(RpcError::local("核心宿主输入已关闭"));
            };
            if let Err(e) = stdin.write_all(&line).and_then(|_| stdin.flush()) {
                self.shared.pending.lock().unwrap().remove(&id);
                return Err(RpcError::local(format!("向核心宿主写入失败：{e}")));
            }
        }
        match rx.recv_timeout(REQUEST_TIMEOUT) {
            Ok(result) => result,
            Err(_) => {
                self.shared.pending.lock().unwrap().remove(&id);
                Err(RpcError::local(format!("请求 {method} 超时；状态以后续快照为准")))
            }
        }
    }

    pub fn call_typed<T: DeserializeOwned>(&self, method: &str, params: Option<Value>) -> Result<T, RpcError> {
        let value = self.call(method, params)?;
        serde_json::from_value(value).map_err(|e| RpcError::local(format!("解析 {method} 响应失败：{e}")))
    }

    pub fn hello(&self) -> Result<Hello, RpcError> {
        let v = self.call("hello", None)?;
        Ok(Hello {
            bridge_version: v["bridge_version"].as_i64().unwrap_or_default(),
            api_version: v["api_version"].as_i64().unwrap_or_default(),
            core_version: v["core_version"].as_str().unwrap_or_default().to_string(),
            methods: v["methods"]
                .as_array()
                .map(|a| a.iter().filter_map(|m| m.as_str().map(String::from)).collect())
                .unwrap_or_default(),
        })
    }

    /// 请求 `shutdown`，等待进程退出；超时则关闭 stdin，再有界终止。
    pub fn shutdown(&self, wait: Duration) {
        let _ = self.call("shutdown", None);
        self.close_stdin();
        let deadline = Instant::now() + wait;
        let mut child = self.child.lock().unwrap();
        if let Some(c) = child.as_mut() {
            loop {
                match c.try_wait() {
                    Ok(Some(_)) => break,
                    Ok(None) if Instant::now() < deadline => thread::sleep(Duration::from_millis(50)),
                    _ => {
                        let _ = c.kill();
                        let _ = c.wait();
                        break;
                    }
                }
            }
        }
        *child = None;
    }

    pub fn close_stdin(&self) {
        *self.shared.stdin.lock().unwrap() = None;
    }
}

impl Drop for CoreClient {
    fn drop(&mut self) {
        self.close_stdin();
        if let Some(mut c) = self.child.lock().unwrap().take() {
            let deadline = Instant::now() + Duration::from_secs(3);
            while Instant::now() < deadline {
                if matches!(c.try_wait(), Ok(Some(_))) {
                    return;
                }
                thread::sleep(Duration::from_millis(50));
            }
            let _ = c.kill();
            let _ = c.wait();
        }
    }
}

fn dispatch(shared: &Shared, inbound: &Arc<dyn Fn(Inbound) + Send + Sync>, value: Value) {
    if value.get("method").and_then(Value::as_str) == Some("event") {
        if let Some(params) = value.get("params") {
            inbound(Inbound::Event(params.clone()));
        }
        return;
    }
    let Some(id) = value.get("id").and_then(Value::as_str) else {
        return; // 无 ID 的错误信封只对应非法请求，本地不会产生
    };
    let pending = shared.pending.lock().unwrap().remove(id);
    let Some(pending) = pending else { return };
    let result = if let Some(err) = value.get("error") {
        Err(RpcError {
            code: err["code"].as_i64().unwrap_or_default(),
            message: err["message"].as_str().unwrap_or("核心宿主返回错误").to_string(),
            fault_code: err["data"]["code"].as_str().map(String::from),
            fault_message: err["data"]["message"].as_str().map(String::from),
        })
    } else {
        Ok(value.get("result").cloned().unwrap_or(Value::Null))
    };
    let _ = pending.tx.send(result);
}

fn fail_all(shared: &Shared, reason: &str) {
    let drained: Vec<Pending> = shared.pending.lock().unwrap().drain().map(|(_, p)| p).collect();
    for p in drained {
        let _ = p.tx.send(Err(RpcError::local(reason.to_string())));
    }
}

/// 定位与 GUI 同目录、匹配当前系统与架构的宿主可执行文件。
pub fn locate_bridge() -> Result<PathBuf, String> {
    let exe = std::env::current_exe().map_err(|e| format!("无法确定 GUI 路径：{e}"))?;
    let dir = exe.parent().ok_or("GUI 所在目录不可用")?.to_path_buf();
    let os = match std::env::consts::OS {
        "windows" => "windows",
        "linux" => "linux",
        other => other,
    };
    let arch = match std::env::consts::ARCH {
        "x86_64" => "amd64",
        "aarch64" => "arm64",
        other => other,
    };
    let ext = if cfg!(windows) { ".exe" } else { "" };
    let name = format!("hole-desktop-core-{os}-{arch}{ext}");
    let mut candidates = vec![dir.join(&name), dir.join(format!("hole-desktop-core{ext}"))];
    if let Ok(custom) = std::env::var("HOLE_DESKTOP_CORE") {
        candidates.insert(0, PathBuf::from(custom));
    }
    // 开发期：仓库 dist/desktop-core/ 下的产物。
    if let Ok(manifest) = std::env::var("CARGO_MANIFEST_DIR") {
        candidates.push(PathBuf::from(manifest).join("../../dist/desktop-core").join(&name));
    }
    if let Some(repo) = dir.ancestors().find(|a| a.join("dist/desktop-core").is_dir()) {
        candidates.push(repo.join("dist/desktop-core").join(&name));
    }
    for c in candidates {
        if c.is_file() {
            return c.canonicalize().map_err(|e| e.to_string());
        }
    }
    Err(format!("未找到核心宿主 {name}；请先执行 ./build.sh desktop-core 或设置 HOLE_DESKTOP_CORE"))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rpc_error_prefers_fault() {
        let e = RpcError { code: -32000, message: "核心操作未完成".into(), fault_code: Some("network_error".into()), fault_message: Some("网络操作失败".into()) };
        assert_eq!(e.display(), "网络操作失败（network_error）");
        let e = RpcError::local("x");
        assert_eq!(e.display(), "x");
    }
}
