use crate::{
    app::{Desktop, Dialog, Page},
    ui::{self, Palette, badge, card, col, empty, fact, heading, icon, metric, row},
};
use gpui::{AnyElement, Context, Div, Window, div, prelude::*, px};
use hole_desktop::{
    config,
    controller::Command,
    model::NetworkIntent,
    punch::text,
    snapshot::{self, Peer},
};
use yororen_ui::headless::table::{TableColumn, table};

impl Desktop {
    pub fn render_page(&mut self, window: &mut Window, cx: &mut Context<Self>) -> AnyElement {
        match self.page {
            Page::Home => self.home(window, cx),
            Page::Services => self.services(window, cx),
            Page::Devices => self.devices(window, cx),
            Page::Voice => self.voice(window, cx),
            Page::Settings => self.settings(window, cx),
            Page::NAT => self.nat(cx),
            Page::Punch => self.punch_page(window, cx),
            Page::Transfer => self.transfer_page(window, cx),
            Page::Diagnostics => self.diagnostics(cx),
        }
        .into_any_element()
    }
    fn home(&self, window: &mut Window, cx: &mut Context<Self>) -> Div {
        let p = self.palette(cx);
        let s = &self.state.snapshot;
        let running = s.run_requested;
        let configured = config::to_run_request(&self.config, &self.secrets).is_ok();
        let wide = window.bounds().size.width >= px(1150.);
        let kind = if !running {
            "idle"
        } else if s.signal_state == "joined" {
            "good"
        } else if s.engine_state == "error" || s.signal_state == "error" {
            "bad"
        } else {
            "warn"
        };
        let hp = ui::status_palette(p, kind);
        let title = if !configured {
            "尚未配置"
        } else if !running {
            "未连接"
        } else if s.engine_state == "stopping" {
            "正在停止"
        } else if kind == "good" {
            "已连接"
        } else if kind == "bad" {
            "连接异常"
        } else if s.signal_state == "joining" {
            "正在加入房间"
        } else if s.engine_state == "recovering" {
            "等待网络"
        } else {
            "正在连接信令"
        };
        let subtitle = if configured {
            format!(
                "{} · {}",
                self.config.connection.room,
                snapshot::connection_policy_label(&self.config.connection.connection_mode)
            )
        } else {
            "请先在设置中填写服务器、房间和凭据。".into()
        };
        let hero = card(hp)
            .id("home-status")
            .p_7()
            .gap_6()
            .text_color(hp.text)
            .bg(gpui::linear_gradient(
                135.,
                gpui::linear_color_stop(ui::blend(hp.surface, gpui::white(), 0.06), 0.),
                gpui::linear_color_stop(hp.surface, 1.),
            ))
            .shadow(vec![gpui::BoxShadow {
                color: gpui::hsla(
                    hp.surface.hue.into_positive_degrees() / 360.,
                    hp.surface.saturation,
                    hp.surface.lightness,
                    0.22,
                )
                .into(),
                offset: gpui::point(px(0.), px(6.)),
                blur_radius: px(14.),
                spread_radius: px(0.),
                inset: false,
            }])
            .transitions(|t| t.bg(crate::motion::NORMAL))
            .child(
                row()
                    .justify_between()
                    .child(
                        row()
                            .gap_5()
                            .child(
                                div()
                                    .size(px(68.))
                                    .rounded_full()
                                    .bg(hp.soft)
                                    .flex()
                                    .items_center()
                                    .justify_center()
                                    .child(icon(
                                        if kind == "good" {
                                            "check"
                                        } else if running {
                                            "route"
                                        } else {
                                            "link"
                                        },
                                        gpui::white(),
                                        34.,
                                    )),
                            )
                            .child(heading(title, subtitle, hp)),
                    )
                    .child(badge(
                        if running { "运行中" } else { "已停止" },
                        hp.text,
                        hp,
                    )),
            )
            .child(
                row()
                    .justify_between()
                    .flex_wrap()
                    .gap_4()
                    .child(
                        row()
                            .gap_7()
                            .child(fact("核心", snapshot::engine_label(s), hp))
                            .child(fact("信令", snapshot::signal_label(&s.signal_state), hp))
                            .child(fact(
                                "运行时间",
                                snapshot::elapsed_label(&s.started_at, running),
                                hp,
                            )),
                    )
                    .child(self.action(
                        "run-control",
                        if !configured {
                            "配置连接"
                        } else if running {
                            "停止连接"
                        } else {
                            "启动连接"
                        },
                        if running { "stop" } else { "play" },
                        true,
                        self.run_pending
                            || !self.state.ready
                            || matches!(s.engine_state.as_str(), "starting" | "stopping"),
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
                    )),
            );
        let metrics = div()
            .grid()
            .grid_cols(if wide { 4 } else { 2 })
            .gap_4()
            .child(
                metric(
                    "devices",
                    "在线设备",
                    format!(
                        "{} / {}",
                        s.peers
                            .iter()
                            .filter(|p| matches!(p.state.as_str(), "active" | "switching"))
                            .count(),
                        s.peers.len()
                    ),
                    "已连接 / 已发现",
                    p,
                )
                .min_w(px(180.)),
            )
            .child(
                metric(
                    "services",
                    "可用服务",
                    s.mappings
                        .iter()
                        .filter(|m| m.state == "active")
                        .count()
                        .to_string(),
                    "",
                    p,
                )
                .min_w(px(180.)),
            )
            .child(
                metric(
                    "send",
                    "上行速率",
                    snapshot::format_rate(self.state.up_rate),
                    "",
                    p,
                )
                .min_w(px(180.)),
            )
            .child(
                metric(
                    "receive",
                    "下行速率",
                    snapshot::format_rate(self.state.down_rate),
                    "",
                    p,
                )
                .min_w(px(180.)),
            );
        let network = card(p)
            .flex_1()
            .child(
                row()
                    .justify_between()
                    .child(section("activity", "流量与网络", p))
                    .child(self.action(
                        "home-nat",
                        "检测网络",
                        "network",
                        false,
                        false,
                        |app, _, _| app.navigate(Page::NAT),
                        cx,
                    )),
            )
            .child(
                row()
                    .gap_7()
                    .flex_wrap()
                    .child(fact("累计发送", snapshot::format_bytes(s.bytes_sent()), p))
                    .child(fact(
                        "累计接收",
                        snapshot::format_bytes(s.bytes_received()),
                        p,
                    ))
                    .child(fact(
                        "TCP / UDP 会话",
                        format!("{} / {}", s.tcp_sessions(), s.udp_sessions()),
                        p,
                    )),
            )
            .child(div().h(px(1.)).bg(p.inset))
            .child(fact(
                "本机 NAT",
                if s.network.nat.is_empty() {
                    "尚未检测"
                } else {
                    &s.network.nat
                },
                p,
            ))
            .child(
                row()
                    .gap_7()
                    .child(fact("网络切换", s.network_changes.to_string(), p))
                    .child(fact("协调重连", s.reconnects.to_string(), p)),
            );
        let mut activity = card(p).flex_1().child(section("clock", "最近活动", p));
        for event in self.state.events.iter().take(4) {
            activity = activity.child(
                row()
                    .items_start()
                    .child(
                        div()
                            .p_2()
                            .rounded(px(12.))
                            .bg(p.inset)
                            .child(icon("activity", p.muted, 16.)),
                    )
                    .child(
                        col()
                            .gap_1()
                            .flex_1()
                            .child(format!(
                                "{} · {}",
                                text(event, "kind"),
                                text(event, "state")
                            ))
                            .child(
                                div()
                                    .text_xs()
                                    .text_color(p.muted)
                                    .child(text(event, "message").to_string()),
                            ),
                    )
                    .child(
                        div()
                            .text_xs()
                            .text_color(p.muted)
                            .child(snapshot::short_time(text(event, "time"))),
                    ),
            );
        }
        if self.state.events.is_empty() {
            activity = activity.child(empty("clock", "暂无活动", "", p));
        }
        let lower = if wide {
            row().items_stretch().gap_4().child(network).child(activity)
        } else {
            col().child(network).child(activity)
        };
        col().gap_5().child(hero).child(metrics).child(lower)
    }

    fn services(&self, window: &mut Window, cx: &mut Context<Self>) -> Div {
        let p = self.palette(cx);
        let wide = window.bounds().size.width >= px(1120.);
        let mut columns = if wide {
            row().items_start().gap_5()
        } else {
            col().gap_5()
        };
        for role in ["provide", "consume"] {
            let provide = role == "provide";
            let label = if provide {
                "提供服务"
            } else {
                "使用服务"
            };
            let count = if provide {
                self.config.provide.len()
            } else {
                self.config.consume.len()
            };
            let mut group = col().flex_1().w_full().child(
                row()
                    .justify_between()
                    .child(section(if provide { "share" } else { "consume" }, label, p))
                    .child(self.action(
                        format!("add-{role}"),
                        "添加",
                        "plus",
                        false,
                        false,
                        move |app, _, _| app.open_mapping(role, None),
                        cx,
                    )),
            );
            for index in 0..count {
                let (id, endpoint, enabled, protocol) = if provide {
                    let e = &self.config.provide[index];
                    (
                        e.id.clone(),
                        config::compose_service(&e.protocol, &e.host, e.port),
                        e.enabled,
                        e.protocol.to_uppercase(),
                    )
                } else {
                    let e = &self.config.consume[index];
                    (
                        e.id.clone(),
                        config::compose_expose(&e.host, e.port),
                        e.enabled,
                        "自动协议".into(),
                    )
                };
                if !self.search.is_empty()
                    && !format!("{id} {endpoint}")
                        .to_lowercase()
                        .contains(&self.search.to_lowercase())
                {
                    continue;
                }
                let runtime = self
                    .state
                    .snapshot
                    .mappings
                    .iter()
                    .find(|m| m.role == role && m.id == id);
                let state = if !enabled {
                    "已停用"
                } else {
                    runtime
                        .map(|m| snapshot::mapping_label(&m.state))
                        .unwrap_or("待启动")
                };
                let mut item = card(p)
                    .gap_4()
                    .child(
                        row()
                            .justify_between()
                            .child(
                                row()
                                    .child(div().p_3().rounded(px(15.)).bg(p.soft).child(icon(
                                        if provide { "share" } else { "consume" },
                                        p.accent,
                                        22.,
                                    )))
                                    .child(
                                        col()
                                            .gap_1()
                                            .flex_1()
                                            .child(
                                                div()
                                                    .font_weight(gpui::FontWeight::SEMIBOLD)
                                                    .child(id.clone()),
                                            )
                                            .child(
                                                div().text_xs().text_color(p.muted).child(protocol),
                                            ),
                                    ),
                            )
                            .child(badge(
                                state,
                                if runtime.is_some_and(|m| m.state == "active") && enabled {
                                    p.good
                                } else {
                                    p.muted
                                },
                                p,
                            )),
                    )
                    .child(
                        div()
                            .px_3()
                            .py_2()
                            .rounded(px(12.))
                            .bg(p.inset)
                            .text_sm()
                            .child(endpoint),
                    )
                    .child(self.toggle(
                        &format!("enable-{role}-{index}"),
                        "启用此服务",
                        "停用后保留服务配置",
                        enabled,
                        move |app, value| {
                            let mut config = app.config.clone();
                            if provide {
                                config.provide[index].enabled = value;
                            } else {
                                config.consume[index].enabled = value;
                            }
                            app.controller
                                .send(Command::Save(Box::new(config), app.secrets.clone()));
                        },
                        cx,
                    ));
                if let Some(runtime) = runtime {
                    item = item.child(
                        row()
                            .gap_5()
                            .child(fact(
                                "对端",
                                if runtime.peer.is_empty() {
                                    "等待匹配"
                                } else {
                                    &runtime.peer
                                },
                                p,
                            ))
                            .child(fact(
                                "会话",
                                format!(
                                    "TCP {} · UDP {}",
                                    runtime.tcp_sessions, runtime.udp_sessions
                                ),
                                p,
                            )),
                    );
                }
                item = item.child(
                    row()
                        .justify_between()
                        .child(self.action(
                            format!("edit-{role}-{index}"),
                            "编辑",
                            "edit",
                            false,
                            false,
                            move |app, _, _| app.open_mapping(role, Some(index)),
                            cx,
                        ))
                        .child(
                            row()
                                .child(self.action(
                                    format!("up-{role}-{index}"),
                                    "",
                                    "arrow-up",
                                    false,
                                    index == 0,
                                    move |app, _, _| app.move_mapping(role, index, -1),
                                    cx,
                                ))
                                .child(self.action(
                                    format!("down-{role}-{index}"),
                                    "",
                                    "arrow-down",
                                    false,
                                    index + 1 == count,
                                    move |app, _, _| app.move_mapping(role, index, 1),
                                    cx,
                                ))
                                .child(self.action(
                                    format!("delete-{role}-{index}"),
                                    "",
                                    "trash",
                                    false,
                                    false,
                                    move |app, _, _| {
                                        app.dialog = Some(Dialog::Delete(role.into(), index))
                                    },
                                    cx,
                                )),
                        ),
                );
                group = group.child(crate::motion::enter(format!("service-{role}-{id}"), item));
            }
            if count == 0 {
                group = group.child(card(p).child(empty(
                    if provide { "share" } else { "consume" },
                    if provide {
                        "暂无提供服务"
                    } else {
                        "暂无使用服务"
                    },
                    "",
                    p,
                )));
            }
            columns = columns.child(group);
        }
        let search = self.field(
            "services-search",
            "搜索服务",
            self.search.clone(),
            0,
            |app, v| app.search = v,
            window,
            cx,
        );
        let mut page = col()
            .gap_5()
            .child(heading("服务映射", "同房间设备使用相同的服务 ID 配对。", p))
            .child(search)
            .child(columns);
        if self.undo.is_some() {
            page = page.child(self.action(
                "undo-delete",
                "撤销删除",
                "back",
                false,
                false,
                |app, _, _| {
                    if let Some((old, _)) = app.undo.take() {
                        let mut cfg = app.config.clone();
                        cfg.provide = old.provide;
                        cfg.consume = old.consume;
                        app.controller
                            .send(Command::Save(Box::new(cfg), app.secrets.clone()));
                    }
                },
                cx,
            ));
        }
        page
    }
    fn move_mapping(&mut self, role: &str, index: usize, direction: isize) {
        let mut cfg = self.config.clone();
        let target = index.saturating_add_signed(direction);
        if role == "provide" {
            if target < cfg.provide.len() {
                cfg.provide.swap(index, target);
            }
        } else if target < cfg.consume.len() {
            cfg.consume.swap(index, target);
        }
        self.controller
            .send(Command::Save(Box::new(cfg), self.secrets.clone()));
    }

    fn devices(&self, window: &mut Window, cx: &mut Context<Self>) -> Div {
        let p = self.palette(cx);
        let wide = window.bounds().size.width >= px(1120.);
        let mut list = col().gap_3().min_w(px(240.));
        for (index, peer) in self.state.snapshot.peers.iter().enumerate() {
            let id = peer.transport_id.clone();
            let selected = self.selected_peer == id || self.selected_peer.is_empty() && index == 0;
            list = list.child(
                card(p)
                    .id(format!("peer-card-{index}"))
                    .p_3()
                    .bg(if selected { p.soft } else { p.surface })
                    .transitions(|t| t.bg(crate::motion::NORMAL))
                    .child(self.action(
                        format!("select-peer-{index}"),
                        peer.peer_id.clone(),
                        "devices",
                        selected,
                        false,
                        move |app, _, _| app.selected_peer = id.clone(),
                        cx,
                    ))
                    .child(
                        row()
                            .justify_between()
                            .child(
                                div()
                                    .text_xs()
                                    .text_color(p.muted)
                                    .child(snapshot::peer_state_label(&peer.state)),
                            )
                            .child(
                                div()
                                    .text_xs()
                                    .text_color(p.muted)
                                    .child(format!("{} ms", peer.rtt_ms)),
                            ),
                    ),
            );
        }
        let selected = self
            .state
            .snapshot
            .peers
            .iter()
            .find(|p| p.transport_id == self.selected_peer)
            .or_else(|| self.state.snapshot.peers.first());
        let details = selected
            .map(|peer| {
                col().child(crate::motion::enter(
                    format!("peer-details-{}", peer.transport_id),
                    self.peer_details(peer, cx),
                ))
            })
            .unwrap_or_else(|| card(p).child(empty("devices", "暂无连接设备", "", p)));
        let layout = if wide {
            row()
                .items_start()
                .gap_5()
                .child(list.w(px(250.)).flex_shrink_0())
                .child(details.flex_1())
        } else {
            col().child(list).child(details)
        };
        col()
            .gap_5()
            .child(
                row()
                    .justify_between()
                    .flex_wrap()
                    .gap_3()
                    .child(heading("设备与线路", "", p))
                    .child(
                        row()
                            .child(self.action(
                                "device-nat",
                                "NAT 检测",
                                "network",
                                false,
                                false,
                                |app, _, _| app.navigate(Page::NAT),
                                cx,
                            ))
                            .child(self.action(
                                "device-diagnostics",
                                "诊断",
                                "activity",
                                false,
                                false,
                                |app, _, _| app.navigate(Page::Diagnostics),
                                cx,
                            )),
                    ),
            )
            .child(layout)
    }
    fn peer_details(&self, peer: &Peer, cx: &mut Context<Self>) -> Div {
        let p = self.palette(cx);
        let fixed = snapshot::is_fixed_peer(peer);
        let transport = peer.transport_id.clone();
        let identity = row()
            .gap_4()
            .child(
                div()
                    .p_4()
                    .rounded_full()
                    .bg(p.soft)
                    .child(icon("devices", p.accent, 30.)),
            )
            .child(heading(
                peer.peer_id.clone(),
                snapshot::peer_path_label(peer),
                p,
            ));
        let mut top = card(p)
            .child(
                row()
                    .justify_between()
                    .child(section("check", "连接概览", p))
                    .child(badge(
                        snapshot::peer_state_label(&peer.state),
                        if matches!(peer.state.as_str(), "active" | "switching") {
                            p.good
                        } else {
                            p.warn
                        },
                        p,
                    )),
            )
            .child(
                row()
                    .gap_7()
                    .flex_wrap()
                    .child(fact("当前线路", snapshot::peer_path_label(peer), p))
                    .child(fact("往返延迟", format!("{} ms", peer.rtt_ms), p))
                    .child(fact(
                        "可用通道",
                        format!("{} / {}", peer.active_channels, peer.mapping_count),
                        p,
                    )),
            )
            .child(fact(
                "连接过程",
                if peer.state == "active" {
                    "线路已建立"
                } else {
                    snapshot::peer_state_label(&peer.state)
                },
                p,
            ))
            .child(row().child(self.action(
                "reselect-path",
                "重选路径",
                "refresh",
                false,
                self.run_pending || !self.state.snapshot.run_requested,
                |app, _, _| app.run_command(Command::Network(NetworkIntent::ReselectPath)),
                cx,
            )));
        if !fixed {
            top = top.child(self.action(
                "open-punch",
                "查看打洞过程",
                "route",
                true,
                false,
                move |app, _, _| app.open_process(transport.clone()),
                cx,
            ));
        }
        if let Some(error) = &peer.error {
            top = top.child(div().text_sm().text_color(p.bad).child(format!(
                "{} · {}",
                snapshot::connection_issue(&error.code),
                error.message
            )));
        }
        let mut addresses = card(p).child(section("network", "连接地址", p));
        if fixed {
            for (key, value) in snapshot::fixed_peer_details(peer) {
                addresses = addresses.child(fact(key, value, p));
            }
        } else {
            addresses = addresses
                .child(fact(
                    "本机连接",
                    snapshot::selected_connection_label(&peer.local_type, &peer.local_address),
                    p,
                ))
                .child(fact(
                    "对端连接",
                    snapshot::selected_connection_label(&peer.remote_type, &peer.remote_address),
                    p,
                ))
                .child(
                    row()
                        .gap_6()
                        .child(fact(
                            "本机中继顺序",
                            snapshot::relay_order_label(&peer.relay_order),
                            p,
                        ))
                        .child(fact(
                            "对端中继顺序",
                            snapshot::relay_order_label(&peer.peer_relay_order),
                            p,
                        )),
                );
            if !peer.relay_device.is_empty() {
                addresses = addresses
                    .child(fact("Hole 中继设备", &peer.relay_device, p))
                    .child(fact("公布入口", &peer.relay_endpoint, p))
                    .child(fact("实际接入", &peer.relay_access_address, p));
            }
        }
        let rate = |loss: Option<&snapshot::SendLoss>| {
            loss.map(|l| {
                l.rate_percent
                    .map(|r| format!("{r:.2}%"))
                    .unwrap_or("暂无样本".into())
            })
            .unwrap_or("暂无反馈".into())
        };
        let bandwidth = |rates: Option<&snapshot::BandwidthRates>, peak: bool| {
            rates
                .map(|r| {
                    format!(
                        "{:.2} Mbps",
                        if peak { r.peak_bps } else { r.average_bps } as f64 / 1_000_000.
                    )
                })
                .unwrap_or("—".into())
        };
        let stats = table("peer-quic", cx)
            .columns([
                TableColumn::new("metric", "QUIC 统计"),
                TableColumn::new("total", "合计"),
                TableColumn::new("send", "上行"),
                TableColumn::new("receive", "下行"),
            ])
            .rows([
                vec![
                    "丢包率".into(),
                    rate(peer.quic_loss.as_ref()).into(),
                    rate(peer.send_loss.as_ref()).into(),
                    rate(peer.receive_loss.as_ref()).into(),
                ],
                vec![
                    "峰值速率".into(),
                    bandwidth(peer.bandwidth.as_ref().map(|b| &b.total), true).into(),
                    bandwidth(peer.bandwidth.as_ref().map(|b| &b.send), true).into(),
                    bandwidth(peer.bandwidth.as_ref().map(|b| &b.receive), true).into(),
                ],
                vec![
                    "平均速率".into(),
                    bandwidth(peer.bandwidth.as_ref().map(|b| &b.total), false).into(),
                    bandwidth(peer.bandwidth.as_ref().map(|b| &b.send), false).into(),
                    bandwidth(peer.bandwidth.as_ref().map(|b| &b.receive), false).into(),
                ],
            ])
            .render(cx);
        let mut nat_details = col().gap_4();
        if !fixed {
            nat_details = nat_details.child(section("route", "NAT 与端口映射", p));
            for (title, value, name) in [
                ("本机 NAT", &peer.local_nat, "devices"),
                ("对端 NAT", &peer.peer_nat, "network"),
            ] {
                let tone = ui::tone_palette(
                    p,
                    if value.contains("对称") {
                        "warn"
                    } else {
                        "neutral"
                    },
                );
                nat_details = nat_details.child(
                    card(tone)
                        .gap_3()
                        .child(section(name, title, tone))
                        .child(div().text_sm().child(value.clone())),
                );
            }
        }
        col()
            .gap_5()
            .child(identity)
            .child(top)
            .child(addresses)
            .child(nat_details)
            .child(
                card(p)
                    .child(section("activity", "传输质量", p))
                    .child(stats)
                    .child(
                        div()
                            .text_xs()
                            .text_color(p.muted)
                            .child("丢包率统计本次 QUIC 连接；平均速率按有流量的采样时长计算。"),
                    )
                    .child(
                        row()
                            .flex_wrap()
                            .gap_7()
                            .child(fact(
                                "发送 / 接收",
                                format!(
                                    "{} / {}",
                                    snapshot::format_bytes(peer.bytes_sent),
                                    snapshot::format_bytes(peer.bytes_received)
                                ),
                                p,
                            ))
                            .child(fact("路径重试", &peer.retry_count, p))
                            .child(fact("本地丢弃", &peer.dropped_datagrams, p)),
                    ),
            )
    }

    fn voice(&self, _: &mut Window, cx: &mut Context<Self>) -> Div {
        let p = self.palette(cx);
        let voice = &self.state.snapshot.voice;
        let mut page = col().gap_5().child(
            card(p)
                .child(section("voice", "房间语音", p))
                .child(self.toggle(
                    "voice-enable",
                    "启用房间语音",
                    "需要自动或 ICE 连接方式",
                    self.config.voice.enabled,
                    |app, value| {
                        if value && app.config.connection.connection_mode == "legacy" {
                            app.notice("仅 IPv6 模式不支持房间语音");
                            return;
                        }
                        let mut cfg = app.config.clone();
                        cfg.voice.enabled = value;
                        app.controller
                            .send(Command::Save(Box::new(cfg), app.secrets.clone()));
                    },
                    cx,
                ))
                .child(
                    row()
                        .justify_between()
                        .child(fact(
                            "采集 / 播放设备",
                            if self.state.voice_route.is_empty() {
                                "等待音频设备"
                            } else {
                                &self.state.voice_route
                            },
                            p,
                        ))
                        .child(self.action(
                            "voice-retry",
                            "重新打开设备",
                            "refresh",
                            false,
                            !self.config.voice.enabled,
                            |app, _, _| app.controller.send(Command::RetryVoiceAudio),
                            cx,
                        )),
                )
                .child(
                    row()
                        .gap_7()
                        .child(fact(
                            "采集状态",
                            snapshot::media_label(&voice.capture_state),
                            p,
                        ))
                        .child(fact(
                            "采集 / 混音帧",
                            format!("{} / {}", voice.captured_frames, voice.mixed_frames),
                            p,
                        )),
                )
                .child(self.action(
                    "voice-mute",
                    if voice.muted {
                        "取消静音"
                    } else {
                        "静音麦克风"
                    },
                    "voice",
                    voice.muted,
                    !self.state.voice_running,
                    move |app, _, _| {
                        app.controller
                            .send(Command::VoiceMuted(!app.state.snapshot.voice.muted))
                    },
                    cx,
                ))
                .child(
                    div()
                        .text_xs()
                        .text_color(p.muted)
                        .child("使用系统默认音频设备。建议佩戴耳机。"),
                ),
        );
        if !self.state.voice_error.is_empty() {
            page = page.child(
                card(p).child(
                    div()
                        .text_color(p.bad)
                        .child(self.state.voice_error.clone()),
                ),
            );
        }
        let mut members = card(p).child(section("devices", "房间成员", p));
        for member in &voice.members {
            members = members.child(
                row()
                    .justify_between()
                    .child(row().child(icon("voice", p.accent, 20.)).child(format!(
                        "{}{}",
                        member.name,
                        if member.local { " · 本机" } else { "" }
                    )))
                    .child(
                        div()
                            .text_sm()
                            .text_color(p.muted)
                            .child(format!("{} · {}", member.state, member.media)),
                    ),
            );
        }
        if voice.members.is_empty() {
            members = members.child(empty("voice", "暂无语音成员", "", p));
        }
        page = page.child(members);
        for peer in &voice.peers {
            page = page.child(
                card(p)
                    .child(section("route", &peer.name, p))
                    .child(
                        row()
                            .gap_6()
                            .flex_wrap()
                            .child(fact("线路", &peer.path, p))
                            .child(fact("状态", &peer.state, p))
                            .child(fact("码率", &peer.bitrate, p)),
                    )
                    .child(
                        div()
                            .text_sm()
                            .text_color(p.muted)
                            .child(format!("{} · {}", peer.stats, peer.buffering)),
                    ),
            );
        }
        page
    }

    fn nat(&self, cx: &mut Context<Self>) -> Div {
        let p = self.palette(cx);
        let nat = &self.state.snapshot.nat_diagnostic;
        let running = matches!(text(nat, "state"), "running" | "queued" | "probing");
        let tone = ui::tone_palette(
            p,
            match text(nat, "tone") {
                "green" => "good",
                "yellow" => "warn",
                "red" => "bad",
                "blue" => "info",
                _ => "neutral",
            },
        );
        let mut page = col()
            .gap_5()
            .child(
                row()
                    .justify_between()
                    .flex_wrap()
                    .child(heading("NAT 检测", "无需加入房间", p))
                    .child(
                        row()
                            .child(self.action(
                                "nat-copy",
                                "复制结果",
                                "copy",
                                false,
                                nat.is_null(),
                                |app, _, cx| {
                                    app.clipboard(
                                        serde_json::to_string_pretty(
                                            &app.state.snapshot.nat_diagnostic,
                                        )
                                        .unwrap_or_default(),
                                        cx,
                                    );
                                },
                                cx,
                            ))
                            .child(self.action(
                                "nat-probe",
                                if running {
                                    "取消检测"
                                } else {
                                    "开始检测"
                                },
                                if running { "stop" } else { "network" },
                                true,
                                !self.state.ready,
                                move |app, _, _| {
                                    app.controller.send(if running {
                                        Command::CancelNATProbe
                                    } else {
                                        Command::NATProbe
                                    })
                                },
                                cx,
                            )),
                    ),
            )
            .child(
                card(tone).child(
                    row()
                        .gap_5()
                        .child(
                            div()
                                .p_5()
                                .rounded(px(22.))
                                .bg(p.soft)
                                .child(icon("network", p.accent, 36.)),
                        )
                        .child(heading(
                            if text(nat, "title").is_empty() {
                                "检测当前网络"
                            } else {
                                text(nat, "title")
                            },
                            text(nat, "summary").to_string(),
                            p,
                        )),
                ),
            );
        let mut tiles = row().items_stretch().flex_wrap().gap_4();
        for tile in nat["highlights"].as_array().into_iter().flatten() {
            tiles = tiles.child(
                metric(
                    "network",
                    text(tile, "label"),
                    text(tile, "value").to_string(),
                    "",
                    p,
                )
                .min_w(px(200.)),
            );
        }
        page = page.child(tiles);
        for section_data in nat["sections"].as_array().into_iter().flatten() {
            let mut section_card =
                card(p)
                    .gap_4()
                    .child(section("info", text(section_data, "title"), tone));
            for (index, field) in section_data["fields"]
                .as_array()
                .into_iter()
                .flatten()
                .enumerate()
            {
                if index > 0 {
                    section_card = section_card.child(div().h(px(1.)).bg(p.inset));
                }
                section_card = section_card.child(
                    col()
                        .gap_1()
                        .child(fact(
                            text(field, "label"),
                            snapshot::nat_time(text(field, "value")),
                            p,
                        ))
                        .child(
                            div()
                                .text_xs()
                                .text_color(p.muted)
                                .child(text(field, "detail").to_string()),
                        ),
                );
            }
            page = page.child(section_card);
        }
        page
    }

    fn diagnostics(&self, cx: &mut Context<Self>) -> Div {
        let p = self.palette(cx);
        let s = &self.state.snapshot;
        let mut page = col()
            .gap_5()
            .child(
                row()
                    .justify_between()
                    .child(heading("运行诊断", "报告会隐藏凭据。", p))
                    .child(self.action(
                        "diagnostic-report",
                        "生成报告",
                        "download",
                        true,
                        false,
                        |app, _, _| app.controller.send(Command::Report),
                        cx,
                    )),
            )
            .child(
                card(p)
                    .child(section("shield", "核心与运行", p))
                    .child(
                        row()
                            .gap_8()
                            .flex_wrap()
                            .child(fact("核心版本", &s.core_version, p))
                            .child(fact("运行代次", s.generation.to_string(), p))
                            .child(fact("传输代次", s.transport_generation.to_string(), p))
                            .child(fact("运行状态", snapshot::engine_label(s), p)),
                    )
                    .child(fact("加载方式", self.state.host.clone(), p))
                    .child(
                        row()
                            .gap_8()
                            .child(fact("核心丢弃事件", s.events_dropped.to_string(), p))
                            .child(fact("桥接丢弃事件", s.bridge_events_dropped.to_string(), p)),
                    ),
            )
            .child(
                card(p)
                    .child(section("network", "网络与恢复", p))
                    .child(fact("接口", &s.network.interface, p))
                    .child(fact("地址", s.network.addresses.join(" · "), p))
                    .child(fact("DNS", s.network.dns.join(" · "), p))
                    .child(fact("本机 NAT", &s.network.nat, p)),
            );
        for mapping in &s.mappings {
            page = page.child(
                card(p)
                    .child(section("services", &mapping.id, p))
                    .child(
                        row()
                            .flex_wrap()
                            .gap_8()
                            .child(fact("状态", snapshot::mapping_label(&mapping.state), p))
                            .child(fact("端点", &mapping.endpoint, p))
                            .child(fact("对端", &mapping.peer, p)),
                    )
                    .child(fact(
                        "会话",
                        format!(
                            "TCP {} · UDP {} · 待确认 {} 字节",
                            mapping.tcp_sessions, mapping.udp_sessions, mapping.replay_bytes
                        ),
                        p,
                    )),
            );
        }
        page
    }

    pub fn mapping_panel(&self, window: &mut Window, cx: &mut Context<Self>) -> Div {
        let p = self.palette(cx);
        let e = self.mapping_presence.content.as_ref().unwrap();
        let reveal = self.mapping_presence.value();
        let provide = e.role == "provide";
        let mut form = col()
            .gap_5()
            .child(self.field(
                "mapping-id",
                "服务 ID",
                e.id.clone(),
                0,
                |app, v| {
                    if let Some(e) = &mut app.mapping {
                        e.id = v;
                    }
                },
                window,
                cx,
            ))
            .child(self.field(
                "mapping-host",
                if provide {
                    "目标主机"
                } else {
                    "本机监听 IP"
                },
                e.host.clone(),
                0,
                |app, v| {
                    if let Some(e) = &mut app.mapping {
                        e.host = v;
                    }
                },
                window,
                cx,
            ))
            .child(self.field(
                "mapping-port",
                "端口",
                e.port.clone(),
                0,
                |app, v| {
                    if let Some(e) = &mut app.mapping {
                        e.port = v;
                    }
                },
                window,
                cx,
            ));
        if provide {
            form = form.child(
                row()
                    .child(self.action(
                        "mapping-tcp",
                        "TCP",
                        "terminal",
                        e.protocol == "tcp",
                        false,
                        |app, _, _| {
                            if let Some(e) = &mut app.mapping {
                                e.protocol = "tcp".into();
                            }
                        },
                        cx,
                    ))
                    .child(self.action(
                        "mapping-udp",
                        "UDP",
                        "packet",
                        e.protocol == "udp",
                        false,
                        |app, _, _| {
                            if let Some(e) = &mut app.mapping {
                                e.protocol = "udp".into();
                            }
                        },
                        cx,
                    )),
            );
        }
        form = form.child(self.toggle(
            "mapping-enabled",
            "启用服务",
            "保存后提交给核心",
            e.enabled,
            |app, v| {
                if let Some(e) = &mut app.mapping {
                    e.enabled = v;
                }
            },
            cx,
        ));
        div()
            .occlude()
            .absolute()
            .inset_0()
            .bg(gpui::Hsla {
                alpha: 0.28 * reveal,
                ..gpui::rgb_to_hsla(gpui::rgb(0x12182A))
            })
            .flex()
            .justify_end()
            .child(
                col()
                    .relative()
                    .left(px(440. * (1. - reveal)))
                    .w(px(440.))
                    .h_full()
                    .p_6()
                    .bg(p.surface)
                    .shadow_lg()
                    .child(
                        row()
                            .justify_between()
                            .child(section(
                                if provide { "share" } else { "consume" },
                                if provide {
                                    "编辑提供服务"
                                } else {
                                    "编辑使用服务"
                                },
                                p,
                            ))
                            .child(self.action(
                                "mapping-close",
                                "",
                                "close",
                                false,
                                false,
                                |app, _, _| app.dialog = Some(Dialog::DiscardMapping),
                                cx,
                            )),
                    )
                    .child(
                        div()
                            .id("mapping-form")
                            .flex_1()
                            .min_h_0()
                            .overflow_y_scroll()
                            .child(form),
                    )
                    .child(
                        row()
                            .justify_end()
                            .child(self.action(
                                "mapping-cancel",
                                "取消",
                                "close",
                                false,
                                false,
                                |app, _, _| app.dialog = Some(Dialog::DiscardMapping),
                                cx,
                            ))
                            .child(self.action(
                                "mapping-save",
                                "保存服务",
                                "check",
                                true,
                                false,
                                |app, _, _| app.save_mapping(),
                                cx,
                            )),
                    ),
            )
    }
    pub fn dialog_view(&self, cx: &mut Context<Self>) -> Div {
        let p = self.palette(cx);
        let reveal = self.dialog_presence.value();
        let (title, detail, confirm) = match self.dialog_presence.content.as_ref().unwrap() {
            Dialog::Close => (
                "退出 Hole",
                if self.state.snapshot.run_requested {
                    "退出会停止连接。"
                } else if self.dirty() {
                    "设置尚未保存，退出会放弃草稿。"
                } else {
                    "关闭桌面客户端。"
                },
                "退出并停止",
            ),
            Dialog::Delete(_, _) => ("删除此服务", "删除会停止此服务，可以撤销。", "删除服务"),
            Dialog::DiscardMapping | Dialog::Navigate(_) => {
                ("放弃编辑", "尚未保存的服务编辑将被丢弃。", "放弃编辑")
            }
        };
        div()
            .occlude()
            .absolute()
            .inset_0()
            .bg(gpui::Hsla {
                alpha: 0.4 * reveal,
                ..gpui::rgb_to_hsla(gpui::rgb(0x12182A))
            })
            .flex()
            .items_center()
            .justify_center()
            .child(
                card(p)
                    .relative()
                    .top(px(20. * (1. - reveal)))
                    .opacity(reveal)
                    .w(px(440.))
                    .p_7()
                    .gap_6()
                    .child(heading(title, detail, p))
                    .child(
                        row()
                            .justify_end()
                            .child(self.action(
                                "dialog-cancel",
                                "取消",
                                "back",
                                false,
                                false,
                                |app, _, _| app.dialog = None,
                                cx,
                            ))
                            .child(self.action(
                                "dialog-confirm",
                                confirm,
                                "check",
                                true,
                                false,
                                |app, _, cx| match app.dialog.take() {
                                    Some(Dialog::Close) => {
                                        app.controller.finish();
                                        cx.quit();
                                    }
                                    Some(Dialog::Delete(role, index)) => {
                                        let mut cfg = app.config.clone();
                                        app.undo = Some((cfg.clone(), app.secrets.clone()));
                                        if role == "provide" {
                                            if index < cfg.provide.len() {
                                                cfg.provide.remove(index);
                                            }
                                        } else if index < cfg.consume.len() {
                                            cfg.consume.remove(index);
                                        }
                                        app.controller.send(Command::Save(
                                            Box::new(cfg),
                                            app.secrets.clone(),
                                        ));
                                        app.notice("服务已删除，可撤销");
                                    }
                                    Some(Dialog::DiscardMapping) => app.mapping = None,
                                    Some(Dialog::Navigate(page)) => {
                                        app.mapping = None;
                                        app.page = page;
                                    }
                                    None => {}
                                },
                                cx,
                            )),
                    ),
            )
    }
}
pub fn section(name: &str, title: &str, p: Palette) -> Div {
    row().child(icon(name, p.accent, 20.)).child(
        div()
            .font_weight(gpui::FontWeight::SEMIBOLD)
            .child(title.to_string()),
    )
}
