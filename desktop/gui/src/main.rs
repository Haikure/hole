//! hole 桌面客户端入口：Slint 运行时、回调绑定与应用生命周期。

#![cfg_attr(all(windows, not(debug_assertions)), windows_subsystem = "windows")]

mod config;
mod controller;
mod core_client;
mod report;
mod snapshot;
mod storage;

slint::include_modules!();

use config::{ConsumeEntry, ProvideEntry, Secrets, StoredConfig};
use controller::{Command, Controller, Shared};
use slint::{ComponentHandle, Timer, TimerMode, Weak};
use std::rc::Rc;
use std::sync::{Arc, Mutex};
use std::time::Duration;

fn main() {
    let storage = match storage::Storage::open() {
        Ok(s) => s,
        Err(e) => {
            eprintln!("{e}");
            std::process::exit(2);
        }
    };
    let (config, config_error) = match storage.load_config() {
        Ok(c) => (c, None),
        Err(e) => (StoredConfig::default(), Some(e)),
    };
    let secrets = storage.load_secrets();
    let run_state = storage.load_run_state();
    let shared = Arc::new(Mutex::new(Shared { storage, config, secrets, run_state, config_error, undo: None, pending_import: None }));

    let window = MainWindow::new().expect("创建主窗口失败");
    push_config_forms(&window.as_weak(), &shared);
    {
        let cfg = &shared.lock().unwrap().config;
        window.invoke_apply_theme_mode(cfg.prefs.theme_mode.clone().into());
        window.invoke_apply_accent(cfg.prefs.accent.clone().into());
        window.global::<Theme>().set_reduce_motion(cfg.prefs.reduce_motion);
    }

    let controller = Rc::new(Controller::start(&window, shared.clone()));
    bind_callbacks(&window, controller.clone(), shared.clone());
    {
        let weak = window.as_weak();
        window.window().on_close_requested(move || {
            if let Some(w) = weak.upgrade() {
                w.invoke_handle_close();
            }
            slint::CloseRequestResponse::KeepWindowShown
        });
    }

    if std::env::var_os("HOLE_DESKTOP_SCREENSHOT_DIR").is_some() {
        screenshot_mode(&window);
    }

    window.run().expect("运行事件循环失败");
    // 事件循环结束后确保宿主退出。
    controller.send(Command::Quit);
    std::thread::sleep(Duration::from_millis(200));
}

/// 把已保存配置写回表单初值与偏好；导入替换后也调用。
pub fn push_config_forms(window: &Weak<MainWindow>, shared: &Arc<Mutex<Shared>>) {
    let (cfg, secrets, error) = {
        let s = shared.lock().unwrap();
        (s.config.clone(), s.secrets.clone(), s.config_error.clone())
    };
    let _ = window.upgrade_in_event_loop(move |w| {
        let app = w.global::<App>();
        let c = &cfg.connection;
        app.set_connection(ConnectionForm {
            server_url: c.server_url.clone().into(),
            password: secrets.password.clone().into(),
            room: c.room.clone().into(),
            token: secrets.token.clone().into(),
            device_name: c.device_name.clone().into(),
            session_timeout: c.session_timeout.clone().into(),
            candidate_interfaces: config::join_list(&c.candidate_interfaces).into(),
            candidate_addresses: config::join_list(&c.candidate_addresses).into(),
        });
        app.set_transport(TransportForm {
            mode: c.connection_mode.clone().into(),
            stun_urls: c.ice.stun_urls.join("\n").into(),
            relay_mode: c.turn.mode.clone().into(),
            turn_urls: c.turn.urls.join("\n").into(),
            turn_username: c.turn.username.clone().into(),
            turn_credential: secrets.turn_credential.clone().into(),
            direct_probe_timeout: c.ice.direct_probe_timeout.clone().into(),
            gather_timeout: c.ice.gather_timeout.clone().into(),
            connectivity_timeout: c.ice.connectivity_timeout.clone().into(),
            retry_max_delay: c.ice.retry_max_delay.clone().into(),
            turn_ttl: c.turn.ttl.clone().into(),
            interface_allowlist: config::join_list(&c.ice.interface_allowlist).into(),
            relay_only: c.ice.relay_only,
            allow_insecure_signal: c.allow_insecure_signal,
        });
        app.set_prefs(Prefs {
            theme_mode: cfg.prefs.theme_mode.clone().into(),
            accent: cfg.prefs.accent.clone().into(),
            close_to_tray: cfg.prefs.close_to_tray,
            resume_at_login: cfg.prefs.resume_at_login,
            start_hidden: cfg.prefs.start_hidden,
            reduce_motion: cfg.prefs.reduce_motion,
        });
        app.set_config_loaded(error.is_none());
        w.invoke_apply_theme_mode(cfg.prefs.theme_mode.clone().into());
        w.invoke_apply_accent(cfg.prefs.accent.clone().into());
        w.global::<Theme>().set_reduce_motion(cfg.prefs.reduce_motion);
    });
}

fn show_toast(window: &MainWindow, text: &str, kind: &str, undo: bool) {
    let app = window.global::<App>();
    app.set_toast(text.into());
    app.set_toast_kind(kind.into());
    app.set_undo_available(undo);
    let weak = window.as_weak();
    let current = text.to_string();
    Timer::single_shot(Duration::from_secs(if undo { 8 } else { 4 }), move || {
        if let Some(w) = weak.upgrade() {
            let app = w.global::<App>();
            if app.get_toast().as_str() == current {
                app.set_toast("".into());
                app.set_undo_available(false);
            }
        }
    });
}

/// 在 UI 线程保存配置：先校验并原子写入，再通知工作线程重配。
fn save_config(shared: &Arc<Mutex<Shared>>, controller: &Controller, mutate: impl FnOnce(&mut StoredConfig, &mut Secrets) -> Result<(), String>) -> Result<(), String> {
    let mut s = shared.lock().unwrap();
    let mut cfg = s.config.clone();
    let mut secrets = s.secrets.clone();
    mutate(&mut cfg, &mut secrets)?;
    config::validate_draft(&cfg)?;
    s.storage.save_config(&cfg)?;
    if secrets != s.secrets {
        s.storage.save_secrets(&secrets)?;
    }
    s.config = cfg;
    s.secrets = secrets;
    s.config_error = None;
    drop(s);
    controller.send(Command::ConfigChanged);
    Ok(())
}

fn bind_callbacks(window: &MainWindow, controller: Rc<Controller>, shared: Arc<Mutex<Shared>>) {
    let app = window.global::<App>();

    // ---- 运行控制 ----
    {
        let c = controller.clone();
        let shared = shared.clone();
        let weak = window.as_weak();
        app.on_toggle_run(move |on| {
            if on {
                let s = shared.lock().unwrap();
                if let Some(e) = config::find_connection_error(&s.config.connection, &s.secrets) {
                    drop(s);
                    if let Some(w) = weak.upgrade() {
                        show_toast(&w, &format!("无法开启：{e}"), "warn", false);
                        // 开关回弹由下一次状态推送完成
                        w.global::<App>().set_status({ let mut st = w.global::<App>().get_status(); st.run_requested = false; st });
                    }
                    return;
                }
                c.send(Command::Start);
            } else {
                c.send(Command::Stop);
            }
        });
    }
    {
        let c = controller.clone();
        app.on_reconnect(move || c.send(Command::Reconnect));
    }
    {
        let c = controller.clone();
        app.on_refresh(move || c.send(Command::Refresh));
    }
    {
        let c = controller.clone();
        app.on_quit_and_stop(move || c.send(Command::Quit));
    }
    {
        let weak = window.as_weak();
        window.on_hide_to_tray(move || {
            if let Some(w) = weak.upgrade() {
                let _ = w.hide();
            }
        });
    }

    // ---- 连接设置 ----
    {
        let c = controller.clone();
        let shared = shared.clone();
        let weak = window.as_weak();
        app.on_save_connection(move |form| {
            let result = save_config(&shared, &c, |cfg, secrets| {
                if let Some(e) = config::validate_server_url(&form.server_url) {
                    return Err(e);
                }
                if !config::is_valid_duration(&form.session_timeout) {
                    return Err("会话保留期限格式无效，例如 10m、1h30m".into());
                }
                let addresses = config::split_list(&form.candidate_addresses);
                if let Some(bad) = addresses.iter().find(|a| !config::is_public_ipv6(a)) {
                    return Err(format!("\"{bad}\" 不是公网 IPv6 字面地址"));
                }
                let conn = &mut cfg.connection;
                conn.server_url = form.server_url.trim().into();
                conn.room = form.room.trim().into();
                conn.device_name = form.device_name.trim().into();
                conn.session_timeout = form.session_timeout.trim().into();
                conn.candidate_interfaces = config::split_list(&form.candidate_interfaces);
                conn.candidate_addresses = addresses;
                secrets.password = form.password.to_string();
                secrets.token = form.token.to_string();
                Ok(())
            });
            match result {
                Ok(()) => {
                    push_config_forms(&weak, &shared);
                    if let Some(w) = weak.upgrade() {
                        show_toast(&w, "连接设置已保存", "good", false);
                    }
                    "".into()
                }
                Err(e) => e.into(),
            }
        });
    }

    // ---- 连接方式 ----
    {
        let c = controller.clone();
        let shared = shared.clone();
        let weak = window.as_weak();
        app.on_save_transport(move |form| {
            let result = save_config(&shared, &c, |cfg, secrets| {
                let mode = form.mode.to_string();
                if mode != "legacy" && !cfg.connection.candidate_addresses.is_empty() {
                    return Err("请先在设置中清空手动 IPv6 候选地址，或选择“仅 IPv6”模式。".into());
                }
                for d in [&form.direct_probe_timeout, &form.gather_timeout, &form.connectivity_timeout, &form.retry_max_delay, &form.turn_ttl] {
                    if !config::is_valid_duration(d) {
                        return Err("请检查时间格式，例如 3s、6h。".into());
                    }
                }
                let stun = config::split_lines(&form.stun_urls);
                if stun.iter().any(|u| !u.starts_with("stun:")) {
                    return Err("STUN 地址需以 stun: 开头。".into());
                }
                let relay = form.relay_mode.to_string();
                let turn_urls = config::split_lines(&form.turn_urls);
                if relay == "manual" {
                    if turn_urls.is_empty() || form.turn_username.trim().is_empty() || form.turn_credential.is_empty() {
                        return Err("请填齐 TURN 地址、用户名和凭据。".into());
                    }
                    if turn_urls.iter().any(|u| !u.starts_with("turn:") && !u.starts_with("turns:")) {
                        return Err("TURN 地址需以 turn: 或 turns: 开头。".into());
                    }
                }
                let conn = &mut cfg.connection;
                conn.connection_mode = mode;
                conn.allow_insecure_signal = form.allow_insecure_signal;
                conn.ice.stun_urls = stun;
                conn.ice.direct_probe_timeout = form.direct_probe_timeout.trim().into();
                conn.ice.gather_timeout = form.gather_timeout.trim().into();
                conn.ice.connectivity_timeout = form.connectivity_timeout.trim().into();
                conn.ice.retry_max_delay = form.retry_max_delay.trim().into();
                conn.ice.interface_allowlist = config::split_list(&form.interface_allowlist);
                conn.ice.relay_only = form.relay_only;
                conn.turn.mode = relay;
                conn.turn.ttl = form.turn_ttl.trim().into();
                conn.turn.urls = turn_urls;
                conn.turn.username = form.turn_username.trim().into();
                secrets.turn_credential = form.turn_credential.to_string();
                Ok(())
            });
            match result {
                Ok(()) => {
                    push_config_forms(&weak, &shared);
                    if let Some(w) = weak.upgrade() {
                        show_toast(&w, "连接方式已保存", "good", false);
                    }
                    "".into()
                }
                Err(e) => e.into(),
            }
        });
    }

    // ---- 偏好 ----
    {
        let c = controller.clone();
        let shared = shared.clone();
        let weak = window.as_weak();
        app.on_save_prefs(move |prefs| {
            let result = save_config(&shared, &c, |cfg, _| {
                cfg.prefs = config::Prefs {
                    theme_mode: prefs.theme_mode.to_string(),
                    accent: prefs.accent.to_string(),
                    close_to_tray: prefs.close_to_tray,
                    resume_at_login: prefs.resume_at_login,
                    start_hidden: prefs.start_hidden,
                    reduce_motion: prefs.reduce_motion,
                };
                Ok(())
            });
            push_config_forms(&weak, &shared);
            if let (Err(e), Some(w)) = (result, weak.upgrade()) {
                show_toast(&w, &format!("偏好保存失败：{e}"), "bad", false);
            }
        });
    }

    // ---- 映射 ----
    {
        let c = controller.clone();
        let shared = shared.clone();
        let weak = window.as_weak();
        app.on_save_mapping(move |form| {
            let result = save_config(&shared, &c, |cfg, _| {
                let id = form.id.trim().to_string();
                if !config::is_valid_mapping_id(&id) {
                    return Err("映射 ID 不能为空，且不能包含空白字符".into());
                }
                let host = config::normalize_host(&form.host);
                let port = config::parse_port(&form.port).ok_or("端口必须是 1–65535 的数字")?;
                let entry_id = if form.entry_id.is_empty() { config::new_entry_id() } else { form.entry_id.to_string() };
                if form.role == "provide" {
                    if host.is_empty() {
                        return Err("服务地址不能为空".into());
                    }
                    let entry = ProvideEntry { entry_id: entry_id.clone(), id, protocol: form.protocol.to_string(), host, port, enabled: form.enabled };
                    let mut others: Vec<ProvideEntry> = cfg.provide.iter().filter(|e| e.entry_id != entry_id).cloned().collect();
                    others.push(entry.clone());
                    if let Some(e) = config::find_id_conflict(&others, &cfg.consume) {
                        return Err(e);
                    }
                    match cfg.provide.iter_mut().find(|e| e.entry_id == entry_id) {
                        Some(slot) => *slot = entry,
                        None => cfg.provide.push(entry),
                    }
                } else {
                    if host.is_empty() {
                        return Err("本地监听地址不能为空".into());
                    }
                    if !config::is_literal_ip(&host) {
                        return Err("必须是字面 IP 地址（例如 127.0.0.1 或 ::1），不解析域名".into());
                    }
                    let entry = ConsumeEntry { entry_id: entry_id.clone(), id, host: host.clone(), port, enabled: form.enabled };
                    let mut others: Vec<ConsumeEntry> = cfg.consume.iter().filter(|e| e.entry_id != entry_id).cloned().collect();
                    if let Some(dup) = others.iter().find(|e| e.enabled && form.enabled && e.host == host && e.port == port) {
                        return Err(format!("与已启用的映射 \"{}\" 监听相同地址和端口，运行时会绑定失败", dup.id));
                    }
                    others.push(entry.clone());
                    if let Some(e) = config::find_id_conflict(&cfg.provide, &others) {
                        return Err(e);
                    }
                    match cfg.consume.iter_mut().find(|e| e.entry_id == entry_id) {
                        Some(slot) => *slot = entry,
                        None => cfg.consume.push(entry),
                    }
                }
                Ok(())
            });
            match result {
                Ok(()) => {
                    if let Some(w) = weak.upgrade() {
                        show_toast(&w, "映射已保存", "good", false);
                    }
                    "".into()
                }
                Err(e) => e.into(),
            }
        });
    }
    {
        let c = controller.clone();
        let shared = shared.clone();
        let weak = window.as_weak();
        app.on_delete_mapping(move |role, entry_id| {
            let removed = save_config(&shared, &c, |cfg, _| {
                let value = if role == "provide" {
                    let idx = cfg.provide.iter().position(|e| e.entry_id.as_str() == entry_id.as_str()).ok_or("条目不存在")?;
                    let e = cfg.provide.remove(idx);
                    (idx, serde_json::to_value(&e).map_err(|e| e.to_string())?)
                } else {
                    let idx = cfg.consume.iter().position(|e| e.entry_id.as_str() == entry_id.as_str()).ok_or("条目不存在")?;
                    let e = cfg.consume.remove(idx);
                    (idx, serde_json::to_value(&e).map_err(|e| e.to_string())?)
                };
                UNDO.with(|u| *u.borrow_mut() = Some((role.to_string(), value.0, value.1)));
                Ok(())
            });
            match removed {
                Ok(()) => {
                    UNDO.with(|u| {
                        let taken = u.borrow_mut().take();
                        shared.lock().unwrap().undo = taken;
                    });
                    if let Some(w) = weak.upgrade() {
                        show_toast(&w, "映射已删除", "info", true);
                    }
                }
                Err(e) => {
                    if let Some(w) = weak.upgrade() {
                        show_toast(&w, &format!("删除失败：{e}"), "bad", false);
                    }
                }
            }
        });
    }
    {
        let c = controller.clone();
        let shared = shared.clone();
        let weak = window.as_weak();
        app.on_undo_delete(move || {
            let undo = shared.lock().unwrap().undo.take();
            let Some((role, index, value)) = undo else { return };
            let result = save_config(&shared, &c, |cfg, _| {
                if role == "provide" {
                    let e: ProvideEntry = serde_json::from_value(value).map_err(|e| e.to_string())?;
                    let at = index.min(cfg.provide.len());
                    cfg.provide.insert(at, e);
                } else {
                    let e: ConsumeEntry = serde_json::from_value(value).map_err(|e| e.to_string())?;
                    let at = index.min(cfg.consume.len());
                    cfg.consume.insert(at, e);
                }
                Ok(())
            });
            if let Some(w) = weak.upgrade() {
                match result {
                    Ok(()) => show_toast(&w, "已恢复映射", "good", false),
                    Err(e) => show_toast(&w, &format!("撤销失败：{e}"), "bad", false),
                }
            }
        });
    }
    {
        let c = controller.clone();
        let shared = shared.clone();
        let weak = window.as_weak();
        app.on_toggle_mapping(move |role, entry_id, enabled| {
            let result = save_config(&shared, &c, |cfg, _| {
                if role == "provide" {
                    if let Some(e) = cfg.provide.iter_mut().find(|e| e.entry_id.as_str() == entry_id.as_str()) {
                        e.enabled = enabled;
                    }
                } else if let Some(e) = cfg.consume.iter_mut().find(|e| e.entry_id.as_str() == entry_id.as_str()) {
                    e.enabled = enabled;
                }
                Ok(())
            });
            if let (Err(e), Some(w)) = (result, weak.upgrade()) {
                show_toast(&w, &format!("无法切换：{e}"), "bad", false);
                c.send(Command::Refresh);
            }
        });
    }
    {
        let c = controller.clone();
        let shared = shared.clone();
        app.on_move_mapping(move |role, entry_id, delta| {
            let _ = save_config(&shared, &c, |cfg, _| {
                fn shift<T>(list: &mut Vec<T>, idx: usize, delta: i32) {
                    let target = idx as i64 + delta as i64;
                    if target >= 0 && (target as usize) < list.len() {
                        list.swap(idx, target as usize);
                    }
                }
                if role == "provide" {
                    if let Some(i) = cfg.provide.iter().position(|e| e.entry_id.as_str() == entry_id.as_str()) {
                        shift(&mut cfg.provide, i, delta);
                    }
                } else if let Some(i) = cfg.consume.iter().position(|e| e.entry_id.as_str() == entry_id.as_str()) {
                    shift(&mut cfg.consume, i, delta);
                }
                Ok(())
            });
        });
    }

    // ---- 交换 ----
    {
        let c = controller.clone();
        let weak = window.as_weak();
        app.on_import_file(move |path, kind| {
            let Some(w) = weak.upgrade() else { return };
            match storage::Storage::read_document(std::path::Path::new(path.as_str().trim())) {
                Ok(text) => {
                    w.global::<App>().set_exchange_busy(true);
                    c.send(Command::ImportText { text, kind: kind.to_string() });
                }
                Err(e) => w.global::<App>().set_exchange_message(format!("导入失败：{e}").into()),
            }
        });
    }
    {
        let c = controller.clone();
        let weak = window.as_weak();
        app.on_import_text(move |text, kind| {
            if text.len() > storage::MAX_DOCUMENT_BYTES {
                if let Some(w) = weak.upgrade() {
                    w.global::<App>().set_exchange_message("文档超过 128 KiB".into());
                }
                return;
            }
            if let Some(w) = weak.upgrade() {
                w.global::<App>().set_exchange_busy(true);
            }
            c.send(Command::ImportText { text: text.to_string(), kind: kind.to_string() });
        });
    }
    {
        let c = controller.clone();
        let weak = window.as_weak();
        app.on_confirm_import(move || {
            if let Some(w) = weak.upgrade() {
                w.global::<App>().set_exchange_busy(true);
            }
            c.send(Command::ConfirmImport);
        });
    }
    {
        let shared = shared.clone();
        let weak = window.as_weak();
        app.on_cancel_import(move || {
            shared.lock().unwrap().pending_import = None;
            if let Some(w) = weak.upgrade() {
                let app = w.global::<App>();
                let mut p = app.get_import_preview();
                p.visible = false;
                app.set_import_preview(p);
            }
        });
    }
    {
        let c = controller.clone();
        let weak = window.as_weak();
        app.on_export_file(move |path, kind, include_secrets| {
            if let Some(w) = weak.upgrade() {
                w.global::<App>().set_exchange_busy(true);
            }
            c.send(Command::Export { path: path.to_string(), kind: kind.to_string(), include_secrets });
        });
    }

    // ---- 诊断 ----
    {
        let c = controller.clone();
        app.on_build_report(move || c.send(Command::BuildReport));
    }
    {
        let weak = window.as_weak();
        app.on_copy_text(move |text| {
            let Some(w) = weak.upgrade() else { return };
            match arboard::Clipboard::new().and_then(|mut cb| cb.set_text(text.to_string())) {
                Ok(()) => show_toast(&w, "已复制到剪贴板", "good", false),
                Err(e) => show_toast(&w, &format!("复制失败：{e}"), "bad", false),
            }
        });
    }
    {
        let weak = window.as_weak();
        app.on_dismiss_toast(move || {
            if let Some(w) = weak.upgrade() {
                let app = w.global::<App>();
                app.set_toast("".into());
                app.set_undo_available(false);
            }
        });
    }
}

thread_local! {
    static UNDO: std::cell::RefCell<Option<(String, usize, serde_json::Value)>> = const { std::cell::RefCell::new(None) };
}

/// 开发验证：按页面切换并用渲染器快照写出 PPM；`HOLE_DESKTOP_SCREENSHOT_DIR` 指定输出目录。
fn screenshot_mode(window: &MainWindow) {
    let pages = ["overview", "mappings", "devices", "diagnostics", "settings", "transport", "exchange"];
    let dir = std::env::var("HOLE_DESKTOP_SCREENSHOT_DIR").unwrap_or_default();
    let weak = window.as_weak();
    let index = Rc::new(std::cell::Cell::new(0usize));
    let timer = Rc::new(Timer::default());
    let timer_clone = timer.clone();
    timer.start(TimerMode::Repeated, Duration::from_millis(1500), move || {
        let Some(w) = weak.upgrade() else { return };
        let i = index.get();
        if i > 0 {
            let name = pages[i - 1];
            match w.window().take_snapshot() {
                Ok(buffer) => {
                    let (width, height) = (buffer.width(), buffer.height());
                    let mut ppm = format!("P6\n{width} {height}\n255\n").into_bytes();
                    for px in buffer.as_slice() {
                        ppm.extend_from_slice(&[px.r, px.g, px.b]);
                    }
                    let _ = std::fs::write(format!("{dir}/{name}.ppm"), ppm);
                }
                Err(e) => eprintln!("截图 {name} 失败：{e}"),
            }
        }
        if i >= pages.len() {
            timer_clone.stop();
            let _ = slint::quit_event_loop();
            return;
        }
        w.set_page(pages[i].into());
        index.set(i + 1);
    });
    std::mem::forget(timer);
}
