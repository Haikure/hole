use crate::{
    motion::{self, Presence, Spring},
    ui::{self, Palette, card, col, row},
};
use gpui::{AnyElement, App, Context, Div, ElementId, Render, Window, div, prelude::*, px};
use hole_desktop::{
    config::{self, ConsumeEntry, ProvideEntry, Secrets, StoredConfig},
    controller::{Command, Controller, ImportPreview, State, Update},
    punch::Process,
    snapshot,
    storage::Storage,
};
use std::{
    cell::RefCell,
    collections::HashMap,
    sync::Arc,
    time::{Duration, Instant},
};
use yororen_ui::{
    ActionVariantKind,
    headless::{
        button::button,
        icon::IconSource,
        password_input::password_input,
        switch::switch,
        text_area::text_area,
        text_input::{TextInputState, text_input},
    },
};

#[derive(Clone, Copy, PartialEq, Eq)]
pub enum Page {
    Home,
    Services,
    Devices,
    Voice,
    Settings,
    NAT,
    Punch,
    Transfer,
    Diagnostics,
}
impl Page {
    pub fn title(self) -> &'static str {
        match self {
            Self::Home => "首页",
            Self::Services => "服务",
            Self::Devices => "连接",
            Self::Voice => "语音",
            Self::Settings => "设置",
            Self::NAT => "NAT 检测",
            Self::Punch => "打洞过程",
            Self::Transfer => "配置文件",
            Self::Diagnostics => "运行诊断",
        }
    }
    pub fn icon(self) -> &'static str {
        match self {
            Self::Home => "home",
            Self::Services => "services",
            Self::Devices => "devices",
            Self::Voice => "voice",
            Self::Settings => "settings",
            Self::NAT => "network",
            Self::Punch => "route",
            Self::Transfer => "copy",
            Self::Diagnostics => "activity",
        }
    }
}
#[derive(Clone)]
pub struct MappingDraft {
    pub entry: String,
    pub role: String,
    pub id: String,
    pub protocol: String,
    pub host: String,
    pub port: String,
    pub enabled: bool,
}
#[derive(Clone)]
pub enum Dialog {
    Close,
    Delete(String, usize),
    DiscardMapping,
    Navigate(Page),
}
pub struct Desktop {
    pub controller: Controller,
    pub state: State,
    pub config: StoredConfig,
    pub secrets: Secrets,
    pub draft: StoredConfig,
    pub draft_secrets: Secrets,
    pub form_epoch: u64,
    pub page: Page,
    pub mapping: Option<MappingDraft>,
    pub undo: Option<(StoredConfig, Secrets)>,
    pub selected_peer: String,
    pub search: String,
    pub settings_tab: usize,
    pub dialog: Option<Dialog>,
    pub toast: String,
    pub toast_at: Instant,
    pub preview: Option<ImportPreview>,
    pub transfer: String,
    pub transfer_epoch: u64,
    pub path: String,
    pub include_secrets: bool,
    pub punch: Process,
    pub process_at: Instant,
    pub playing: bool,
    pub selected_event: String,
    pub visible: bool,
    pub fixture: bool,
    pub run_pending: bool,
    pub root_focus: gpui::FocusHandle,
    pub focus: RefCell<HashMap<ElementId, gpui::FocusHandle>>,
    pub theme_key: Option<(bool, String, bool)>,
    pub nav_lens: Spring,
    pub frame_at: Instant,
    pub replay_ms: f64,
    pub was_playing: bool,
    pub mapping_presence: Presence<MappingDraft>,
    pub dialog_presence: Presence<Dialog>,
    pub toast_presence: Presence<String>,
    pub event_presence: Presence<String>,
    pub expanded_stages: std::collections::HashSet<String>,
    pub expanded_samples: bool,
    pub selected_socket: String,
    pub sample_filter: String,
    pub sample_ip: String,
    pub expanded_raw: bool,
    rendered_view: Option<(Page, u64, usize, usize, bool, bool, bool)>,
}
impl Desktop {
    pub fn new(
        controller: Controller,
        config: StoredConfig,
        secrets: Secrets,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) -> Self {
        let entity = cx.entity().downgrade();
        let root_focus = cx.focus_handle();
        window.focus(&root_focus, cx);
        window.on_window_should_close(cx, move |_, cx| {
            entity
                .update(cx, |app, cx| {
                    app.dialog = Some(Dialog::Close);
                    cx.notify();
                })
                .ok();
            false
        });
        cx.spawn(async |weak, cx| {
            loop {
                cx.background_executor()
                    .timer(Duration::from_millis(16))
                    .await;
                if weak.update(cx, |app, cx| app.poll(cx)).is_err() {
                    break;
                }
            }
        })
        .detach();
        Self {
            controller,
            state: State::default(),
            draft: config.clone(),
            draft_secrets: secrets.clone(),
            config,
            secrets,
            form_epoch: 0,
            page: Page::Home,
            mapping: None,
            undo: None,
            selected_peer: String::new(),
            search: String::new(),
            settings_tab: 0,
            dialog: None,
            toast: String::new(),
            toast_at: Instant::now(),
            preview: None,
            transfer: String::new(),
            transfer_epoch: 0,
            path: String::new(),
            include_secrets: false,
            punch: Process::default(),
            process_at: Instant::now() - Duration::from_secs(1),
            playing: false,
            selected_event: String::new(),
            visible: true,
            fixture: false,
            run_pending: false,
            root_focus,
            focus: RefCell::new(HashMap::new()),
            theme_key: None,
            nav_lens: Spring::new(0.),
            frame_at: Instant::now(),
            replay_ms: 0.,
            was_playing: false,
            mapping_presence: Presence::new(),
            dialog_presence: Presence::new(),
            toast_presence: Presence::new(),
            event_presence: Presence::new(),
            expanded_stages: Default::default(),
            expanded_samples: false,
            selected_socket: String::new(),
            sample_filter: String::new(),
            sample_ip: String::new(),
            expanded_raw: false,
            rendered_view: None,
        }
    }
    fn poll(&mut self, cx: &mut Context<Self>) {
        let mut changed = false;
        let now = Instant::now();
        let dt = now.duration_since(self.frame_at).as_secs_f32().min(0.064);
        self.frame_at = now;
        self.nav_lens.target = match self.page {
            Page::Home => 0.,
            Page::Services => 1.,
            Page::Devices | Page::NAT | Page::Punch | Page::Diagnostics => 2.,
            Page::Voice => 3.,
            _ => 4.,
        };
        changed |= self.nav_lens.step(dt, self.draft.prefs.reduce_motion);
        changed |= self
            .mapping_presence
            .step(dt, self.draft.prefs.reduce_motion);
        changed |= self
            .dialog_presence
            .step(dt, self.draft.prefs.reduce_motion);
        changed |= self.toast_presence.step(dt, self.draft.prefs.reduce_motion);
        changed |= self.event_presence.step(dt, self.draft.prefs.reduce_motion);
        while let Ok(update) = self.controller.updates.try_recv() {
            changed = true;
            match update {
                Update::State(state) => {
                    if state.message != self.state.message && !state.message.is_empty() {
                        self.notice(state.message.clone());
                    }
                    self.state = *state;
                }
                Update::Saved(config, secrets) => {
                    let clean = self.draft == self.config && self.draft_secrets == self.secrets;
                    self.config = *config;
                    self.secrets = secrets;
                    if clean {
                        self.reset_draft();
                    } else {
                        self.draft.provide = self.config.provide.clone();
                        self.draft.consume = self.config.consume.clone();
                        self.draft.voice = self.config.voice.clone();
                    }
                    self.notice("配置已保存");
                }
                Update::Preview(preview) => self.preview = Some(*preview),
                Update::Document(text) => {
                    self.transfer = text;
                    self.transfer_epoch += 1;
                    self.page = Page::Transfer;
                }
                Update::Process { token, result } => {
                    self.punch.accept(token, result);
                }
                Update::RunFinished => self.run_pending = false,
            }
        }
        if self.page == Page::Punch
            && self.visible
            && !self.fixture
            && self.process_at.elapsed() >= Duration::from_millis(500)
        {
            if let Some((token, transport, after, generation, stream)) = self.punch.request() {
                self.controller.send(Command::Process {
                    token,
                    transport,
                    after,
                    generation,
                    stream,
                });
                self.process_at = Instant::now();
            }
        }
        if self.playing && self.punch.paused && self.page == Page::Punch && self.visible {
            let length = self.punch.record().map(|r| r.events.len()).unwrap_or(0);
            if !self.was_playing {
                self.replay_ms = self
                    .punch
                    .record()
                    .and_then(|r| r.events.values().nth(self.punch.position.saturating_sub(1)))
                    .map(|e| hole_desktop::punch::number(e, "elapsed_ms") as f64)
                    .unwrap_or(0.);
            }
            self.replay_ms += dt as f64 * 1000.;
            self.punch.position = self
                .punch
                .record()
                .map(|r| {
                    r.events
                        .values()
                        .take_while(|e| {
                            hole_desktop::punch::number(e, "elapsed_ms") as f64 <= self.replay_ms
                        })
                        .count()
                })
                .unwrap_or(0);
            self.playing = self.punch.position < length;
            changed = true;
        }
        self.was_playing = self.playing;
        if !self.toast.is_empty() && self.toast_at.elapsed() > Duration::from_secs(5) {
            self.toast.clear();
            changed = true;
        }
        if changed {
            cx.notify();
        }
    }
    pub fn notice(&mut self, text: impl Into<String>) {
        self.toast = text.into();
        self.toast_at = Instant::now();
    }
    pub fn dirty(&self) -> bool {
        self.draft != self.config || self.draft_secrets != self.secrets
    }
    pub fn reset_draft(&mut self) {
        self.draft = self.config.clone();
        self.draft_secrets = self.secrets.clone();
        self.form_epoch += 1;
    }
    pub fn save(&mut self) {
        self.controller.send(Command::Save(
            Box::new(self.draft.clone()),
            self.draft_secrets.clone(),
        ));
    }
    pub fn run_command(&mut self, command: Command) {
        if !self.run_pending {
            self.run_pending = true;
            self.controller.send(command);
        }
    }
    pub fn navigate(&mut self, page: Page) {
        if self.mapping.is_some() {
            self.dialog = Some(Dialog::Navigate(page));
            return;
        }
        self.page = page;
    }
    pub fn palette(&self, cx: &App) -> Palette {
        let dark = match self.draft.prefs.theme_mode.as_str() {
            "dark" => true,
            "light" => false,
            _ => matches!(
                cx.window_appearance(),
                gpui::WindowAppearance::Dark | gpui::WindowAppearance::VibrantDark
            ),
        };
        Palette::new(dark, &self.draft.prefs.accent)
    }
    pub fn action(
        &self,
        id: impl Into<ElementId>,
        caption: impl Into<gpui::SharedString>,
        icon: &str,
        primary: bool,
        disabled: bool,
        handler: impl Fn(&mut Self, &mut Window, &mut Context<Self>) + Send + Sync + 'static,
        cx: &mut Context<Self>,
    ) -> AnyElement {
        let weak = cx.entity().downgrade();
        let id = id.into();
        let caption = caption.into();
        let p = self.palette(cx);
        let name = match &id {
            ElementId::Name(name) => name.as_ref(),
            _ => "",
        };
        let nav = name.starts_with("nav-");
        let segment = name.starts_with("segment-");
        let choice = [
            "segment-",
            "settings-tab-",
            "punch-tab-",
            "theme-",
            "accent-",
            "mode-",
            "nat-stun-mode-",
            "attempt-",
            "packet-",
            "punch-round-",
            "punch-peer-",
            "sample-",
            "mapping-tcp",
            "mapping-udp",
            "select-peer-",
        ]
        .iter()
        .any(|prefix| name.starts_with(prefix));
        let focus = self
            .focus
            .borrow_mut()
            .entry(id.clone())
            .or_insert_with(|| cx.focus_handle())
            .clone();
        let mut props = button(id, cx).caption(caption.clone());
        props.focus_handle = focus.clone().tab_stop(!disabled);
        let handler = Arc::new(handler);
        let click_handler = handler.clone();
        if !icon.is_empty() {
            props = props.icon(match ui::builtin_icon(icon) {
                Some(name) => IconSource::Builtin(name.into()),
                None => IconSource::Resource(ui::icon_path(icon)),
            });
        }
        let props = props
            .variant(if primary {
                ActionVariantKind::Primary
            } else {
                ActionVariantKind::Neutral
            })
            .disabled(disabled)
            .on_click(move |_, window, cx| {
                weak.update(cx, |app, cx| {
                    click_handler(app, window, cx);
                    cx.notify();
                })
                .ok();
            });
        let element = if !nav && !choice && caption.is_empty() && !icon.is_empty() {
            let color = if primary { p.on_accent } else { p.text };
            props
                .apply(
                    div()
                        .size(px(40.))
                        .flex_shrink_0()
                        .flex()
                        .items_center()
                        .justify_center()
                        .rounded_full()
                        .bg(if primary { p.accent } else { p.inset })
                        .opacity(if disabled { 0.4 } else { 1. })
                        .child(ui::icon(icon, color, 20.)),
                )
                .hover(|style| {
                    if disabled {
                        style
                    } else {
                        style.bg(if primary { p.accent } else { p.soft })
                    }
                })
                .active(|style| style.opacity(if disabled { 0.4 } else { 0.72 }))
                .when(!disabled, |button| button.cursor_pointer())
        } else if nav || choice {
            let color = if primary { p.accent } else { p.muted };
            let background = if nav || segment {
                gpui::transparent_black()
            } else if primary {
                p.soft
            } else {
                gpui::transparent_black()
            };
            props
                .apply(
                    div()
                        .flex()
                        .items_center()
                        .justify_center()
                        .gap_2()
                        .min_w_0()
                        .h(px(if nav { 52. } else { 40. }))
                        .px_4()
                        .rounded_full()
                        .bg(background)
                        .text_color(color)
                        .children(
                            (!icon.is_empty())
                                .then(|| ui::icon(icon, color, if nav { 22. } else { 18. })),
                        )
                        .children((!caption.is_empty()).then_some(caption)),
                )
                .when(segment, |el| el.w_full().rounded(px(14.)))
                .hover(|style| style.bg(if primary { p.soft } else { p.inset }))
                .active(|style| style.opacity(0.72))
                .cursor_pointer()
        } else {
            props
                .apply(
                    row()
                        .h(px(40.))
                        .px_4()
                        .gap_2()
                        .justify_center()
                        .rounded_full()
                        .bg(if primary { p.accent } else { p.inset })
                        .text_color(if primary { p.on_accent } else { p.text })
                        .opacity(if disabled { 0.4 } else { 1. })
                        .children((!icon.is_empty()).then(|| {
                            ui::icon(icon, if primary { p.on_accent } else { p.text }, 18.)
                        }))
                        .children((!caption.is_empty()).then_some(caption)),
                )
                .when(!disabled, |el| {
                    el.cursor_pointer().hover(|s| {
                        s.bg(if primary {
                            ui::blend(p.accent, p.text, 0.12)
                        } else {
                            p.soft
                        })
                    })
                })
        };
        element
            .relative()
            .top(px(0.))
            .when(!disabled, |el| {
                el.hover(|s| {
                    s.top(px(-1.)).bg(if nav || choice {
                        if primary { p.soft } else { p.inset }
                    } else if primary {
                        ui::blend(p.accent, p.text, 0.12)
                    } else {
                        p.soft
                    })
                })
                .active(|s| s.top(px(1.)).opacity(0.72))
            })
            .transitions(|t| {
                t.bg(motion::FAST)
                    .text_color(motion::FAST)
                    .opacity(motion::FAST)
                    .top(motion::FAST)
            })
            .focus_visible(|style| {
                style.shadow(vec![gpui::BoxShadow {
                    color: self.palette(cx).accent.into(),
                    offset: gpui::point(px(0.), px(0.)),
                    blur_radius: px(0.),
                    spread_radius: px(2.),
                    inset: false,
                }])
            })
            .on_key_down({
                let weak = cx.entity().downgrade();
                move |event, window, cx| {
                    if !disabled
                        && focus.is_focused(window)
                        && matches!(event.keystroke.key.as_str(), "enter" | "space")
                    {
                        weak.update(cx, |app, cx| {
                            handler(app, window, cx);
                            cx.notify();
                        })
                        .ok();
                        cx.stop_propagation();
                    }
                }
            })
            .into_any_element()
    }
    pub fn segmented(
        &self,
        id: &str,
        items: &[(&str, &str)],
        selected: usize,
        setter: impl Fn(&mut Self, usize) + Send + Sync + 'static,
        cx: &mut Context<Self>,
    ) -> Div {
        let p = self.palette(cx);
        let setter = Arc::new(setter);
        let mut track = row().relative().gap_0().child(
            div()
                .id(format!("{id}-lens"))
                .absolute()
                .top_0()
                .left(gpui::relative(selected as f32 / items.len() as f32))
                .w(gpui::relative(1. / items.len() as f32))
                .h(px(40.))
                .rounded(px(14.))
                .bg(ui::surface_gradient(Palette {
                    surface: ui::blend(p.surface, p.soft, 0.45),
                    ..p
                }))
                .shadow(ui::dock_shadow(p))
                .transitions(|t| t.left(motion::NORMAL).bg(motion::NORMAL)),
        );
        for (index, (title, icon)) in items.iter().enumerate() {
            let setter = setter.clone();
            track = track.child(div().flex_1().min_w_0().child(self.action(
                format!("segment-{id}-{index}"),
                *title,
                icon,
                selected == index,
                false,
                move |app, _, _| setter(app, index),
                cx,
            )));
        }
        col()
            .gap_0()
            .p_1()
            .rounded(px(18.))
            .bg(p.inset)
            .child(track)
    }
    pub fn field(
        &self,
        id: &str,
        label: &str,
        value: String,
        kind: u8,
        setter: impl Fn(&mut Self, String) + Send + Sync + 'static,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) -> Div {
        let key: ElementId = format!("{}:{}", self.form_epoch, id).into();
        let weak = cx.entity().downgrade();
        let on_change = move |value: &str, _: &mut Window, cx: &mut App| {
            weak.update(cx, |app, cx| {
                setter(app, value.into());
                cx.notify();
            })
            .ok();
        };
        // 所有输入使用组件本身的 keyed state；刷新不会覆盖正在编辑的文本。
        let state = window.use_keyed_state(key.clone(), cx, move |_, cx| {
            let mut state = TextInputState::new(cx);
            state.set_value(value);
            state
        });
        state.read(cx).focus_handle().tab_stop(true);
        let input = match kind {
            1 => password_input(key).on_change(on_change).render(cx, window),
            2 => text_area(key).on_change(on_change).render(cx, window),
            _ => text_input(key).on_change(on_change).render(cx, window),
        };
        col()
            .gap_2()
            .w_full()
            .child(
                div()
                    .text_sm()
                    .text_color(self.palette(cx).muted)
                    .child(label.to_string()),
            )
            .child(input)
    }
    pub fn toggle(
        &self,
        id: &str,
        title: &str,
        detail: &str,
        checked: bool,
        setter: impl Fn(&mut Self, bool) + Send + Sync + 'static,
        cx: &mut Context<Self>,
    ) -> Div {
        let p = self.palette(cx);
        let weak = cx.entity().downgrade();
        let key: ElementId = gpui::SharedString::from(id.to_string()).into();
        let focus = self
            .focus
            .borrow_mut()
            .entry(key.clone())
            .or_insert_with(|| cx.focus_handle())
            .clone();
        let mut props = switch(key, cx).checked(checked);
        props.focus_handle = focus.clone().tab_stop(true);
        let setter = Arc::new(setter);
        let click_setter = setter.clone();
        let control = props
            .on_toggle(move |v, _, _, cx| {
                weak.update(cx, |app, cx| {
                    click_setter(app, v);
                    cx.notify();
                })
                .ok();
            })
            .apply(
                div()
                    .relative()
                    .w(px(48.))
                    .h(px(28.))
                    .flex_shrink_0()
                    .rounded_full()
                    .bg(if checked { p.accent } else { p.inset })
                    .cursor_pointer()
                    .child(
                        div()
                            .id("switch-knob")
                            .absolute()
                            .top(px(3.))
                            .left(px(if checked { 23. } else { 3. }))
                            .size(px(22.))
                            .rounded_full()
                            .bg(if checked { p.on_accent } else { p.muted })
                            .transitions(|t| t.left(motion::NORMAL).bg(motion::FAST)),
                    ),
            )
            .hover(|s| s.opacity(0.88))
            .transitions(|t| t.bg(motion::NORMAL).opacity(motion::FAST))
            .focus_visible(|style| {
                style.shadow(vec![gpui::BoxShadow {
                    color: p.accent.into(),
                    offset: gpui::point(px(0.), px(0.)),
                    blur_radius: px(0.),
                    spread_radius: px(2.),
                    inset: false,
                }])
            })
            .on_key_down({
                let weak = cx.entity().downgrade();
                move |event, window, cx| {
                    if focus.is_focused(window)
                        && matches!(event.keystroke.key.as_str(), "enter" | "space")
                    {
                        weak.update(cx, |app, cx| {
                            setter(app, !checked);
                            cx.notify();
                        })
                        .ok();
                        cx.stop_propagation();
                    }
                }
            });
        row()
            .justify_between()
            .gap_6()
            .py_2()
            .child(col().gap_1().flex_1().child(title.to_string()).children(
                (!detail.is_empty()).then(|| {
                    div()
                        .text_xs()
                        .text_color(p.muted)
                        .child(detail.to_string())
                }),
            ))
            .child(control)
    }
    pub fn open_mapping(&mut self, role: &str, index: Option<usize>) {
        self.mapping = Some(if let Some(index) = index {
            if role == "provide" {
                let e = &self.config.provide[index];
                MappingDraft {
                    entry: e.entry_id.clone(),
                    role: role.into(),
                    id: e.id.clone(),
                    protocol: e.protocol.clone(),
                    host: e.host.clone(),
                    port: e.port.to_string(),
                    enabled: e.enabled,
                }
            } else {
                let e = &self.config.consume[index];
                MappingDraft {
                    entry: e.entry_id.clone(),
                    role: role.into(),
                    id: e.id.clone(),
                    protocol: "tcp".into(),
                    host: e.host.clone(),
                    port: e.port.to_string(),
                    enabled: e.enabled,
                }
            }
        } else {
            MappingDraft {
                entry: config::new_entry_id(),
                role: role.into(),
                id: String::new(),
                protocol: "tcp".into(),
                host: "127.0.0.1".into(),
                port: String::new(),
                enabled: true,
            }
        });
        self.form_epoch += 1;
    }
    pub fn save_mapping(&mut self) {
        let Some(edit) = self.mapping.clone() else {
            return;
        };
        let port = match edit.port.parse::<u16>() {
            Ok(port) if port > 0 => port,
            _ => {
                self.notice("端口需要为 1–65535");
                return;
            }
        };
        if !config::is_valid_mapping_id(&edit.id) || edit.host.trim().is_empty() {
            self.notice("请填写有效的服务 ID 和地址");
            return;
        }
        let mut cfg = self.config.clone();
        if edit.role == "provide" {
            let entry = ProvideEntry {
                entry_id: edit.entry.clone(),
                id: edit.id.trim().into(),
                protocol: edit.protocol,
                host: edit.host.trim().into(),
                port,
                enabled: edit.enabled,
            };
            if let Some(old) = cfg.provide.iter_mut().find(|e| e.entry_id == edit.entry) {
                *old = entry;
            } else {
                cfg.provide.push(entry);
            }
        } else {
            if !config::is_literal_ip(&edit.host) {
                self.notice("监听地址需要为字面 IP，例如 127.0.0.1");
                return;
            }
            let entry = ConsumeEntry {
                entry_id: edit.entry.clone(),
                id: edit.id.trim().into(),
                host: edit.host.trim().into(),
                port,
                enabled: edit.enabled,
            };
            if let Some(old) = cfg.consume.iter_mut().find(|e| e.entry_id == edit.entry) {
                *old = entry;
            } else {
                cfg.consume.push(entry);
            }
        }
        if let Some(error) = config::find_id_conflict(&cfg.provide, &cfg.consume) {
            self.notice(error);
            return;
        }
        self.controller
            .send(Command::Save(Box::new(cfg), self.secrets.clone()));
        self.mapping = None;
    }
    pub fn clipboard(&mut self, text: String, cx: &mut Context<Self>) {
        cx.write_to_clipboard(gpui::ClipboardItem::new_string(text));
        self.notice("已复制");
    }
    pub fn open_process(&mut self, transport: String) {
        self.punch.select(transport, String::new());
        self.selected_event.clear();
        self.page = Page::Punch;
        self.playing = false;
        self.expanded_stages.clear();
        self.selected_socket.clear();
    }
    pub fn load_file(&mut self) {
        match Storage::read_document(std::path::Path::new(self.path.trim())) {
            Ok(text) => {
                self.transfer = text;
                self.transfer_epoch += 1;
            }
            Err(error) => self.notice(error),
        }
    }
}

impl Render for Desktop {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let view = (
            self.page,
            self.form_epoch,
            self.settings_tab,
            self.punch.view,
            self.mapping.is_some(),
            self.dialog.is_some(),
            !self.selected_event.is_empty(),
        );
        if self.rendered_view != Some(view) {
            // 离开页面后释放旧控件焦点，让快捷键和 Tab 始终从当前页面继续。
            window.focus(&self.root_focus, cx);
            self.rendered_view = Some(view);
        }
        let visible = window.is_visible();
        if visible != self.visible {
            self.visible = visible;
            self.controller.send(Command::Visible(visible));
        }
        let p = self.palette(cx);
        let theme_key = (
            p.dark,
            self.draft.prefs.accent.clone(),
            self.draft.prefs.reduce_motion,
        );
        if self.theme_key.as_ref() != Some(&theme_key) {
            p.install(cx, self.draft.prefs.reduce_motion);
            self.theme_key = Some(theme_key);
        }
        let compact = window.bounds().size.width < px(1000.);
        let reduce = self.draft.prefs.reduce_motion;
        self.mapping_presence.sync(self.mapping.clone(), reduce);
        self.dialog_presence.sync(self.dialog.clone(), reduce);
        self.toast_presence
            .sync((!self.toast.is_empty()).then(|| self.toast.clone()), reduce);
        self.event_presence.sync(
            (self.page == Page::Punch && !self.selected_event.is_empty())
                .then(|| self.selected_event.clone()),
            reduce,
        );
        let content = self.render_page(window, cx);
        let mut nav = col()
            .h_full()
            .w(px(if compact { 100. } else { 216. }))
            .flex_shrink_0()
            .p_4()
            .gap_3()
            .bg(p.sidebar)
            .child(
                row()
                    .h(px(64.))
                    .px_2()
                    .child(gpui::img(ui::app_icon()).size(px(44.)).rounded(px(14.)))
                    .children((!compact).then(|| {
                        col().gap_0().child(
                            div()
                                .text_xl()
                                .font_weight(gpui::FontWeight::BOLD)
                                .child("Hole"),
                        )
                    })),
            )
            .child(div().h_3());
        let mut track = col()
            .relative()
            .gap_0()
            .p_2()
            .rounded(px(34.))
            .bg(p.dock)
            .shadow(ui::dock_shadow(p))
            .child(
                div()
                    .absolute()
                    .left_2()
                    .right_2()
                    .top(px(8. + self.nav_lens.value * 52.))
                    .h(px(52.))
                    .rounded_full()
                    .bg(gpui::linear_gradient(
                        155.,
                        gpui::linear_color_stop(
                            ui::blend(p.soft, gpui::white(), if p.dark { 0.06 } else { 0.45 }),
                            0.,
                        ),
                        gpui::linear_color_stop(p.soft, 1.),
                    ))
                    .shadow(vec![gpui::BoxShadow {
                        color: gpui::rgba(0x00000012).into(),
                        offset: gpui::point(px(0.), px(1.)),
                        blur_radius: px(3.),
                        spread_radius: px(0.),
                        inset: false,
                    }]),
            );
        for page in [
            Page::Home,
            Page::Services,
            Page::Devices,
            Page::Voice,
            Page::Settings,
        ] {
            let selected = self.page == page
                || match page {
                    Page::Devices => {
                        matches!(self.page, Page::NAT | Page::Punch | Page::Diagnostics)
                    }
                    Page::Settings => self.page == Page::Transfer,
                    _ => false,
                };
            let item = self.action(
                format!("nav-{}", page.title()),
                if compact {
                    String::new()
                } else {
                    page.title().into()
                },
                page.icon(),
                selected,
                false,
                move |app, _, _| app.navigate(page),
                cx,
            );
            track = track.child(div().w_full().child(item));
        }
        nav = nav.child(track);
        let running = self.state.snapshot.run_requested;
        let configured = config::to_run_request(&self.config, &self.secrets).is_ok();
        nav = nav
            .child(div().flex_1())
            .child(self.action(
                "sidebar-run",
                if compact {
                    ""
                } else if !configured {
                    "配置连接"
                } else if running {
                    "停止连接"
                } else {
                    "启动连接"
                },
                if running { "stop" } else { "play" },
                true,
                self.run_pending || !self.state.ready,
                move |app, _, _| {
                    if !configured {
                        app.navigate(Page::Settings);
                    } else {
                        app.run_command(if running {
                            Command::Stop
                        } else {
                            Command::Start
                        });
                    }
                },
                cx,
            ))
            .child(
                col()
                    .gap_2()
                    .p_3()
                    .rounded(px(18.))
                    .bg(p.surface)
                    .child(
                        row()
                            .child(ui::icon(
                                "shield",
                                if self.state.ready { p.good } else { p.muted },
                                18.,
                            ))
                            .children((!compact).then(|| {
                                div().text_xs().child(if self.state.ready {
                                    "核心已就绪"
                                } else {
                                    "准备核心"
                                })
                            })),
                    )
                    .children((!compact).then(|| {
                        div()
                            .text_xs()
                            .text_color(p.muted)
                            .child(format!("桌面 {}", env!("CARGO_PKG_VERSION")))
                    })),
            );
        let status = &self.state.snapshot;
        let top = row()
            .h(px(74.))
            .px_7()
            .justify_between()
            .child(
                row()
                    .child(ui::icon(self.page.icon(), p.accent, 22.))
                    .child(
                        div()
                            .font_weight(gpui::FontWeight::SEMIBOLD)
                            .child(self.page.title()),
                    )
                    .child(ui::badge(
                        snapshot::signal_label(&status.signal_state),
                        if status.signal_state == "joined" {
                            p.good
                        } else {
                            p.muted
                        },
                        p,
                    )),
            )
            .child(
                row()
                    .child(
                        div()
                            .text_xs()
                            .text_color(p.muted)
                            .child(self.config.connection.device_name.clone()),
                    )
                    .child(self.action(
                        "top-refresh",
                        "",
                        "refresh",
                        false,
                        !self.state.ready,
                        |app, _, _| app.controller.send(Command::Refresh),
                        cx,
                    )),
            );
        let mut body = col().gap_0().flex_1().min_w_0().h_full().child(top).child(
            div()
                .id(format!("page-{}", self.page.title()))
                .flex_1()
                .min_h_0()
                .overflow_y_scroll()
                .px_7()
                .pb_7()
                .child(motion::enter(
                    format!("page-enter-{}", self.page.title()),
                    div().w_full().child(content),
                )),
        );
        if !self.state.error.is_empty() {
            body = body.child(
                row()
                    .px_7()
                    .py_3()
                    .bg(p.surface)
                    .text_color(p.bad)
                    .child(ui::icon("info", p.bad, 18.))
                    .child(self.state.error.clone()),
            );
        }
        let mut root = div()
            .id("hole-desktop")
            .size_full()
            .relative()
            .flex()
            .bg(p.bg)
            .text_color(p.text)
            .text_base()
            .font_family("sans-serif")
            .transitions(|t| t.bg(motion::NORMAL).text_color(motion::NORMAL))
            .track_focus(&self.root_focus)
            .child(nav)
            .child(body)
            .capture_key_down(cx.listener(|app, event: &gpui::KeyDownEvent, window, cx| {
                if event.keystroke.key == "tab" {
                    if event.keystroke.modifiers.shift {
                        window.focus_prev(cx);
                    } else {
                        window.focus_next(cx);
                    }
                    cx.stop_propagation();
                    return;
                }
                if event.keystroke.modifiers.control {
                    let page = match event.keystroke.key.as_str() {
                        "1" => Some(Page::Home),
                        "2" => Some(Page::Services),
                        "3" => Some(Page::Devices),
                        "4" => Some(Page::Voice),
                        "5" => Some(Page::Settings),
                        _ => None,
                    };
                    if let Some(page) = page {
                        app.navigate(page);
                        cx.notify();
                    }
                }
                if event.keystroke.key == "escape" {
                    if app.dialog.is_some() {
                        app.dialog = None;
                    } else if app.mapping.is_some() {
                        app.dialog = Some(Dialog::DiscardMapping);
                    } else if !app.selected_event.is_empty() {
                        app.selected_event.clear();
                    } else {
                        app.page = Page::Home;
                    }
                    cx.notify();
                }
            }));
        if let Some(message) = &self.toast_presence.content {
            let reveal = self.toast_presence.value();
            root = root.child(
                div()
                    .absolute()
                    .top(px(82. - 12. * (1. - reveal)))
                    .opacity(reveal)
                    .w(px(if compact { 360. } else { 420. }))
                    .right_7()
                    .child(
                        card(p)
                            .py_3()
                            .flex_row()
                            .items_center()
                            .justify_between()
                            .child(
                                row()
                                    .child(ui::icon("check", p.good, 18.))
                                    .child(message.clone()),
                            )
                            .child(self.action(
                                "dismiss-toast",
                                "",
                                "close",
                                false,
                                false,
                                |app, _, _| app.toast.clear(),
                                cx,
                            )),
                    ),
            );
        }
        if self.event_presence.content.is_some() {
            root = root.child(self.process_inspection(cx));
        }
        if self.mapping_presence.content.is_some() {
            root = root.child(self.mapping_panel(window, cx));
        }
        if self.dialog_presence.content.is_some() {
            root = root.child(self.dialog_view(cx));
        }
        root
    }
}
