use crate::{
    app::{Desktop, Page},
    pages::section,
    ui::{badge, card, col, empty, fact, heading, icon, row},
};
use gpui::{Context, Div, Window, div, prelude::*, px};
use hole_desktop::punch::{self, number, text};
use yororen_ui::headless::{
    slider::slider,
    table::{TableColumn, table},
};

impl Desktop {
    pub fn punch_page(&self, window: &mut Window, cx: &mut Context<Self>) -> Div {
        let p = self.palette(cx);
        let mut devices = row().flex_wrap();
        for (index, peer) in self.state.snapshot.peers.iter().enumerate() {
            if hole_desktop::snapshot::is_fixed_peer(peer) {
                continue;
            }
            let transport = peer.transport_id.clone();
            let selected = self.punch.transport == transport;
            devices = devices.child(self.action(
                format!("punch-peer-{index}"),
                peer.peer_id.clone(),
                "devices",
                selected,
                false,
                move |app, _, _| app.open_process(transport.clone()),
                cx,
            ));
        }
        let mut attempts = row().flex_wrap().child(self.action(
            "attempt-latest",
            "最新尝试",
            "refresh",
            self.punch.follow_latest,
            false,
            |app, _, _| {
                let transport = app.punch.transport.clone();
                app.punch.select(transport, String::new());
                app.playing = false;
            },
            cx,
        ));
        for (index, attempt) in self.punch.attempts.iter().enumerate() {
            let generation = text(attempt, "generation").to_string();
            let selected = self.punch.generation == generation;
            attempts = attempts.child(self.action(
                format!("attempt-{index}"),
                format!("第 {} 次 · {}", generation, text(attempt, "phase")),
                "clock",
                selected,
                false,
                move |app, _, _| {
                    let transport = app.punch.transport.clone();
                    app.punch.select(transport, generation.clone());
                    app.playing = false;
                },
                cx,
            ));
        }
        let mut tabs = row().flex_wrap();
        for (index, (title, name)) in [
            ("过程", "activity"),
            ("端口", "grid"),
            ("报文", "packet"),
            ("NAT 采样", "network"),
        ]
        .iter()
        .enumerate()
        {
            tabs = tabs.child(self.action(
                format!("punch-tab-{index}"),
                *title,
                name,
                self.punch.view == index,
                false,
                move |app, _, _| {
                    app.punch.view = index;
                    app.selected_event.clear();
                    app.punch.stream = if index == 3 {
                        "discovery"
                    } else {
                        "connection"
                    }
                    .into();
                    app.punch.live();
                    app.playing = false;
                    app.punch.page = 0;
                },
                cx,
            ));
        }
        let mut page = col()
            .gap_5()
            .child(
                row()
                    .justify_between()
                    .child(heading("打洞过程", "暂停浏览不影响实际连接。", p))
                    .child(self.action(
                        "punch-back",
                        "返回连接",
                        "back",
                        false,
                        false,
                        |app, _, _| app.page = Page::Devices,
                        cx,
                    )),
            )
            .child(devices)
            .child(attempts)
            .child(tabs);
        if !self.punch.error.is_empty() {
            page =
                page.child(card(p).child(div().text_color(p.bad).child(self.punch.error.clone())));
        }
        let Some(record) = self.punch.record() else {
            return page.child(card(p).child(empty(
                "route",
                "暂无过程记录",
                "选择设备并启动连接后查看。",
                p,
            )));
        };
        let run = record.at(self.punch.count());
        let stage = punch::stage(text(&run, "stage"));
        let mut hero = card(p)
            .child(
                row()
                    .justify_between()
                    .child(
                        row()
                            .gap_4()
                            .child(
                                div()
                                    .p_4()
                                    .rounded(px(20.))
                                    .bg(p.soft)
                                    .child(icon("route", p.accent, 30.)),
                            )
                            .child(heading(
                                stage,
                                format!(
                                    "第 {} 次尝试 · {} ms",
                                    self.punch.generation,
                                    number(&run, "elapsed_ms")
                                ),
                                p,
                            )),
                    )
                    .child(badge(
                        if self.punch.paused {
                            "暂停浏览"
                        } else {
                            "实时记录"
                        },
                        if self.punch.paused { p.warn } else { p.good },
                        p,
                    )),
            )
            .child(
                row()
                    .gap_7()
                    .flex_wrap()
                    .child(fact("已读取", format!("{} 条", record.events.len()), p))
                    .child(fact("移出保留窗口", &record.evicted, p))
                    .child(fact("采集时未记录", &record.dropped, p)),
            )
            .child(
                row()
                    .flex_wrap()
                    .child(self.action(
                        "punch-pause",
                        if self.punch.paused {
                            "回到实时"
                        } else {
                            "暂停浏览"
                        },
                        if self.punch.paused {
                            "refresh"
                        } else {
                            "pause"
                        },
                        true,
                        false,
                        |app, _, _| {
                            if app.punch.paused {
                                app.punch.live();
                                app.playing = false;
                            } else if !app.punch.pause() {
                                app.notice("正在读取完整分页，请稍后暂停");
                            }
                        },
                        cx,
                    ))
                    .child(self.action(
                        "punch-step-back",
                        "上一条",
                        "back",
                        false,
                        !self.punch.paused,
                        |app, _, _| {
                            app.punch.position = app.punch.position.saturating_sub(1);
                            app.playing = false;
                        },
                        cx,
                    ))
                    .child(self.action(
                        "punch-step-next",
                        "下一条",
                        "chevron",
                        false,
                        !self.punch.paused,
                        |app, _, _| {
                            app.punch.position = (app.punch.position + 1)
                                .min(app.punch.record().map(|r| r.events.len()).unwrap_or(0));
                            app.playing = false;
                        },
                        cx,
                    ))
                    .child(self.action(
                        "punch-play",
                        if self.playing {
                            "停止回放"
                        } else {
                            "连续回放"
                        },
                        "play",
                        false,
                        !self.punch.paused,
                        |app, _, _| {
                            if app.punch.position
                                >= app.punch.record().map(|r| r.events.len()).unwrap_or(0)
                            {
                                app.punch.position = 0;
                            }
                            app.playing = !app.playing;
                        },
                        cx,
                    )),
            );
        if self.punch.paused {
            let length = record.events.len().max(1);
            let weak = cx.entity().downgrade();
            let control = slider("punch-replay-position", cx)
                .range(0., length as f32)
                .step(1.)
                .value(self.punch.position as f32)
                .on_change(move |value, _, cx| {
                    weak.update(cx, |app, cx| {
                        app.punch.position = value as usize;
                        app.playing = false;
                        cx.notify();
                    })
                    .ok();
                })
                .render(cx);
            hero = hero.child(
                row().child(control).child(
                    div()
                        .text_xs()
                        .text_color(p.muted)
                        .child(format!("{} / {length}", self.punch.position)),
                ),
            );
        }
        page = page.child(hero);
        let mut rounds = row().flex_wrap().child(self.action(
            "punch-round-all",
            "全部轮次",
            "filter",
            self.punch.selected_round.is_none(),
            false,
            |app, _, _| {
                app.punch.selected_round = None;
                app.punch.page = 0;
            },
            cx,
        ));
        for (index, round) in run["rounds"].as_array().into_iter().flatten().enumerate() {
            let round_no = number(round, "round");
            let trial = number(round, "trial");
            rounds = rounds.child(self.action(
                format!("punch-round-{index}"),
                format!("轮次 {round_no} · 校准 {trial}"),
                "route",
                self.punch.selected_round == Some((round_no, trial)),
                false,
                move |app, _, _| {
                    app.punch.selected_round = Some((round_no, trial));
                    app.punch.page = 0;
                },
                cx,
            ));
        }
        page = page.child(rounds);
        let view = match self.punch.view {
            1 => self.port_matrix(&run, cx),
            2 => self.packet_list(window, cx),
            3 => self.nat_samples(&run, cx),
            _ => self.timeline(&run, cx),
        };
        page.child(view)
    }
    fn timeline(&self, run: &serde_json::Value, cx: &mut Context<Self>) -> Div {
        let p = self.palette(cx);
        let events = self.punch.events();
        let stages: Vec<_> = events
            .into_iter()
            .filter(|event| text(event, "direction").is_empty() || text(event, "packet").is_empty())
            .collect();
        let mut timeline = card(p).child(section("activity", "阶段时间线", p));
        for (index, event) in stages
            .iter()
            .enumerate()
            .skip(self.punch.page * 64)
            .take(64)
        {
            timeline = timeline.child(
                row()
                    .items_start()
                    .gap_4()
                    .child(
                        col()
                            .w(px(84.))
                            .flex_shrink_0()
                            .gap_1()
                            .child(
                                div()
                                    .text_xs()
                                    .text_color(p.muted)
                                    .child(format!("{} ms", number(event, "elapsed_ms"))),
                            )
                            .child(
                                div()
                                    .text_xs()
                                    .text_color(p.muted)
                                    .child(format!("#{}", text(event, "sequence"))),
                            ),
                    )
                    .child(div().p_2().rounded_full().bg(p.soft).child(icon(
                        match text(event, "stage") {
                            "connected" => "check",
                            "quic" => "shield",
                            "udp" => "packet",
                            _ => "route",
                        },
                        p.accent,
                        18.,
                    )))
                    .child(
                        col()
                            .gap_1()
                            .flex_1()
                            .child(
                                div()
                                    .font_weight(gpui::FontWeight::MEDIUM)
                                    .child(text(event, "title").to_string()),
                            )
                            .child(div().text_xs().text_color(p.muted).child(format!(
                                "{} {}",
                                punch::stage(text(event, "stage")),
                                text(event, "detail")
                            ))),
                    )
                    .child(self.action(
                        format!("stage-detail-{index}"),
                        "详情",
                        "info",
                        false,
                        false,
                        {
                            let json = serde_json::to_string_pretty(event).unwrap_or_default();
                            move |app, _, _| {
                                app.selected_event = json.clone();
                            }
                        },
                        cx,
                    )),
            );
        }
        if stages.is_empty() {
            timeline = timeline.child(empty("clock", "暂无阶段记录", "", p));
        }
        let mut sockets = card(p).child(section("network", "Socket 状态", p));
        for socket in run["sockets"].as_array().into_iter().flatten().take(32) {
            sockets = sockets.child(
                col()
                    .gap_2()
                    .p_3()
                    .rounded(px(14.))
                    .bg(p.inset)
                    .child(
                        row()
                            .justify_between()
                            .child(text(socket, "id").to_string())
                            .child(
                                div()
                                    .text_xs()
                                    .text_color(p.muted)
                                    .child(text(socket, "role").to_string()),
                            ),
                    )
                    .child(fact(
                        "绑定地址",
                        text(socket, "bound_address").to_string(),
                        p,
                    ))
                    .child(fact(
                        "实际本机端点",
                        text(socket, "local_address").to_string(),
                        p,
                    ))
                    .child(fact(
                        "STUN 返回",
                        text(socket, "mapped_address").to_string(),
                        p,
                    ))
                    .child(fact("目标", text(socket, "target").to_string(), p)),
            );
        }
        let mut content = col()
            .gap_5()
            .child(timeline)
            .child(self.process_pager(stages.len(), cx))
            .child(sockets);
        if !self.selected_event.is_empty() {
            content = content.child(self.event_detail(cx));
        }
        content
    }
    fn port_matrix(&self, run: &serde_json::Value, cx: &mut Context<Self>) -> Div {
        let p = self.palette(cx);
        let events = self.punch.events();
        let mut content = col().gap_5();
        for (index, round) in run["rounds"].as_array().into_iter().flatten().enumerate() {
            let r = number(round, "round");
            let t = number(round, "trial");
            if self
                .punch
                .selected_round
                .is_some_and(|selected| selected != (r, t))
            {
                continue;
            }
            let mut group = card(p)
                .child(section("grid", &format!("轮次 {r} · 校准 {t}"), p))
                .child(
                    row()
                        .gap_5()
                        .flex_wrap()
                        .items_start()
                        .child(self.plan_card(
                            format!("plan-local-{index}"),
                            "本机计划",
                            &round["local_plan"],
                            &round["local_model"],
                            cx,
                        ))
                        .child(self.plan_card(
                            format!("plan-peer-{index}"),
                            "对端计划",
                            &round["peer_plan"],
                            &round["peer_model"],
                            cx,
                        )),
                );
            let targets: Vec<_> = round["targets"]
                .as_array()
                .into_iter()
                .flatten()
                .filter_map(|v| v.as_str())
                .collect();
            let mut matrix = col().gap_2();
            let mut header = row().gap_2().child(
                div()
                    .w(px(104.))
                    .flex_shrink_0()
                    .text_xs()
                    .text_color(p.muted)
                    .child("Socket / 目标端口"),
            );
            for target in targets.iter().skip(self.punch.target_page * 64).take(64) {
                header = header.child(
                    div()
                        .w(px(76.))
                        .flex_shrink_0()
                        .text_xs()
                        .text_color(p.muted)
                        .child(target.rsplit(':').next().unwrap_or(target).to_string()),
                );
            }
            matrix = matrix.child(header);
            let sockets: Vec<_> = run["sockets"]
                .as_array()
                .into_iter()
                .flatten()
                .filter(|s| number(s, "round") == r && number(s, "trial") == t)
                .collect();
            for (si, socket) in sockets
                .iter()
                .skip(self.punch.socket_page * 32)
                .take(32)
                .enumerate()
            {
                let mut cells = row().gap_2().child(
                    div()
                        .w(px(104.))
                        .flex_shrink_0()
                        .text_xs()
                        .child(text(socket, "id").to_string()),
                );
                for (ti, target) in targets
                    .iter()
                    .skip(self.punch.target_page * 64)
                    .take(64)
                    .enumerate()
                {
                    let matching: Vec<_> = events
                        .iter()
                        .copied()
                        .filter(|e| {
                            text(e, "socket_id") == text(socket, "id")
                                && text(e, "remote_address") == *target
                        })
                        .collect();
                    let status = if matching
                        .iter()
                        .any(|e| text(e, "validation") == "echo_verified")
                    {
                        "已验证"
                    } else if matching.iter().any(|e| text(e, "direction") == "in") {
                        "已回应"
                    } else if matching.iter().any(|e| text(e, "direction") == "out") {
                        "已探测"
                    } else {
                        "未发送"
                    };
                    let filter = target.to_string();
                    cells = cells.child(div().w(px(76.)).flex_shrink_0().child(self.action(
                        format!("matrix-{index}-{si}-{ti}"),
                        status,
                        "",
                        status == "已验证",
                        false,
                        move |app, _, _| {
                            app.punch.filter = filter.clone();
                            app.punch.view = 2;
                            app.punch.page = 0;
                            app.form_epoch += 1;
                        },
                        cx,
                    )));
                }
                matrix = matrix.child(cells);
            }
            group = group
                .child(
                    div()
                        .id(format!("port-grid-{index}"))
                        .overflow_x_scroll()
                        .child(matrix),
                )
                .child(
                    row()
                        .flex_wrap()
                        .child(self.action(
                            format!("matrix-sockets-prev-{index}"),
                            "上一组 Socket",
                            "back",
                            false,
                            self.punch.socket_page == 0,
                            |a, _, _| a.punch.socket_page = a.punch.socket_page.saturating_sub(1),
                            cx,
                        ))
                        .child(self.action(
                            format!("matrix-sockets-next-{index}"),
                            "下一组 Socket",
                            "chevron",
                            false,
                            (self.punch.socket_page + 1) * 32 >= sockets.len(),
                            |a, _, _| a.punch.socket_page += 1,
                            cx,
                        ))
                        .child(self.action(
                            format!("matrix-targets-prev-{index}"),
                            "上一组端口",
                            "back",
                            false,
                            self.punch.target_page == 0,
                            |a, _, _| a.punch.target_page = a.punch.target_page.saturating_sub(1),
                            cx,
                        ))
                        .child(self.action(
                            format!("matrix-targets-next-{index}"),
                            "下一组端口",
                            "chevron",
                            false,
                            (self.punch.target_page + 1) * 64 >= targets.len(),
                            |a, _, _| a.punch.target_page += 1,
                            cx,
                        )),
                )
                .child(div().text_xs().text_color(p.muted).child(format!(
                    "Socket 第 {} 组 · 端口第 {} 组 · 每组 32 × 64；点击格子筛选报文。",
                    self.punch.socket_page + 1,
                    self.punch.target_page + 1
                )));
            content = content.child(group);
        }
        if run["rounds"].as_array().is_none_or(|r| r.is_empty()) {
            content = content.child(card(p).child(empty(
                "grid",
                "尚无预测端口计划",
                "普通 ICE 成功时可能无需执行预测打洞。",
                p,
            )));
        }
        if !self.selected_event.is_empty() {
            content = content.child(self.event_detail(cx));
        }
        content
    }
    fn plan_card(
        &self,
        id: String,
        title: &str,
        plan: &serde_json::Value,
        model: &serde_json::Value,
        cx: &mut Context<Self>,
    ) -> Div {
        let p = self.palette(cx);
        let kind = match text(model, "type") {
            "open" => "公网直连",
            "firewall" => "防火墙过滤",
            "full_cone" => "完全锥型",
            "restricted_cone" => "地址受限锥型",
            "port_restricted_cone" => "端口受限锥型",
            "symmetric" => "对称型",
            "endpoint_independent" => "端点无关映射",
            "address_pool" => "地址池",
            _ => "待识别",
        };
        let raw = serde_json::to_string_pretty(&serde_json::json!({
            "plan": plan,
            "model": model,
        }))
        .unwrap_or_default();
        let mut content = col()
            .flex_1()
            .min_w(px(230.))
            .gap_3()
            .p_4()
            .rounded(px(16.))
            .bg(p.inset)
            .child(
                row()
                    .justify_between()
                    .child(section("route", title, p))
                    .child(self.action(
                        id,
                        "原始记录",
                        "",
                        false,
                        false,
                        move |app, _, _| app.selected_event = raw.clone(),
                        cx,
                    )),
            )
            .child(fact("NAT 类型", kind, p));
        if !plan.is_object() {
            return content.child(div().text_sm().text_color(p.muted).child("等待计划交换"));
        }
        let port = number(plan, "port");
        let ip = text(plan, "ip");
        let mapped = if !ip.is_empty() && port > 0 {
            if ip.contains(':') {
                format!("[{ip}]:{port}")
            } else {
                format!("{ip}:{port}")
            }
        } else {
            "待校准".into()
        };
        let start = number(plan, "target");
        let width = number(plan, "width");
        let ports = if start == 0 || width == 0 {
            "待交换".into()
        } else if width == 1 {
            start.to_string()
        } else {
            format!("{start}–{}", start.saturating_add(width - 1))
        };
        let count = |key| {
            plan.get(key)
                .filter(|v| !v.is_null())
                .map(|_| number(plan, key).to_string())
                .unwrap_or_else(|| "待交换".into())
        };
        content = content
            .child(fact("校准映射", mapped, p))
            .child(fact("预测端口", ports, p))
            .child(
                row()
                    .child(fact("窗口宽度", count("width"), p).flex_1())
                    .child(fact("Socket 数", count("sockets"), p).flex_1()),
            );
        content
    }
    fn packet_list(&self, window: &mut Window, cx: &mut Context<Self>) -> Div {
        let p = self.palette(cx);
        let events: Vec<_> = self
            .punch
            .events()
            .into_iter()
            .filter(|e| !text(e, "packet").is_empty())
            .collect();
        let filters = card(p)
            .child(self.field(
                "packet-filter",
                "筛选 IP、端口、socket、事务编号",
                self.punch.filter.clone(),
                0,
                |a, v| {
                    a.punch.filter = v;
                    a.punch.page = 0;
                },
                window,
                cx,
            ))
            .child(
                row()
                    .flex_wrap()
                    .child(self.action(
                        "packet-all",
                        "全部方向",
                        "filter",
                        self.punch.direction.is_empty(),
                        false,
                        |a, _, _| {
                            a.punch.direction.clear();
                            a.punch.page = 0;
                        },
                        cx,
                    ))
                    .child(self.action(
                        "packet-send",
                        "发送",
                        "arrow-up",
                        self.punch.direction == "out",
                        false,
                        |a, _, _| {
                            a.punch.direction = "out".into();
                            a.punch.page = 0;
                        },
                        cx,
                    ))
                    .child(self.action(
                        "packet-recv",
                        "接收",
                        "arrow-down",
                        self.punch.direction == "in",
                        false,
                        |a, _, _| {
                            a.punch.direction = "in".into();
                            a.punch.page = 0;
                        },
                        cx,
                    )),
            )
            .child(
                row()
                    .flex_wrap()
                    .children(["", "STUN", "PROBE", "ACK"].into_iter().map(|kind| {
                        self.action(
                            format!("packet-kind-{kind}"),
                            if kind.is_empty() {
                                "全部类型"
                            } else {
                                kind
                            },
                            "packet",
                            self.punch.packet == kind,
                            false,
                            move |a, _, _| {
                                a.punch.packet = kind.into();
                                a.punch.page = 0;
                            },
                            cx,
                        )
                    })),
            );
        let mut packets =
            card(p).child(section("packet", &format!("报文 · {} 条", events.len()), p));
        for (index, event) in events
            .iter()
            .enumerate()
            .skip(self.punch.page * 64)
            .take(64)
        {
            let event_json = serde_json::to_string_pretty(event).unwrap_or_default();
            packets = packets.child(
                row()
                    .items_start()
                    .gap_4()
                    .child(div().p_2().rounded(px(12.)).bg(p.inset).child(icon(
                        if text(event, "direction") == "out" {
                            "arrow-up"
                        } else {
                            "arrow-down"
                        },
                        p.accent,
                        18.,
                    )))
                    .child(
                        col()
                            .gap_1()
                            .flex_1()
                            .child(
                                row()
                                    .justify_between()
                                    .child(format!(
                                        "{} · {}",
                                        text(event, "packet"),
                                        text(event, "title")
                                    ))
                                    .child(
                                        div()
                                            .text_xs()
                                            .text_color(p.muted)
                                            .child(format!("{} ms", number(event, "elapsed_ms"))),
                                    ),
                            )
                            .child(div().text_xs().text_color(p.muted).child(format!(
                                "{} → {}",
                                text(event, "local_address"),
                                text(event, "remote_address")
                            )))
                            .child(div().text_xs().text_color(p.muted).child(format!(
                                "{} · {} B · {}",
                                text(event, "socket_id"),
                                number(event, "bytes"),
                                text(event, "validation")
                            ))),
                    )
                    .child(self.action(
                        format!("packet-detail-{index}"),
                        "详情",
                        "info",
                        false,
                        false,
                        move |a, _, _| a.selected_event = event_json.clone(),
                        cx,
                    )),
            );
        }
        let mut content = col()
            .gap_5()
            .child(filters)
            .child(packets)
            .child(self.process_pager(events.len(), cx));
        if !self.selected_event.is_empty() {
            content = content.child(self.event_detail(cx));
        }
        content
    }
    fn nat_samples(&self, run: &serde_json::Value, cx: &mut Context<Self>) -> Div {
        let p = self.palette(cx);
        let mut samples = table("nat-samples", cx).columns([
            TableColumn::new("time", "时间 / ms"),
            TableColumn::new("socket", "Socket"),
            TableColumn::new("mapped", "STUN 映射"),
            TableColumn::new("server", "STUN 服务"),
            TableColumn::new("source", "观测来源"),
        ]);
        for event in self
            .punch
            .events()
            .into_iter()
            .filter(|e| !text(e, "mapped_address").is_empty())
            .skip(self.punch.page * 64)
            .take(64)
        {
            samples = samples.row(vec![
                number(event, "elapsed_ms").to_string().into(),
                text(event, "socket_id").to_string().into(),
                text(event, "mapped_address").to_string().into(),
                text(event, "server_address").to_string().into(),
                text(event, "address_source").to_string().into(),
            ]);
        }
        col()
            .gap_5()
            .child(
                card(p)
                    .child(section("network", "NAT 采样轨道", p))
                    .child(samples.render(cx)),
            )
            .child(self.timeline(run, cx))
    }
    fn process_pager(&self, count: usize, cx: &mut Context<Self>) -> Div {
        let p = self.palette(cx);
        row()
            .justify_between()
            .child(div().text_xs().text_color(p.muted).child(format!(
                "第 {} 页 · 每页 64 条 · 共 {count} 条",
                self.punch.page + 1
            )))
            .child(
                row()
                    .child(self.action(
                        "punch-page-prev",
                        "上一页",
                        "back",
                        false,
                        self.punch.page == 0,
                        |a, _, _| a.punch.page = a.punch.page.saturating_sub(1),
                        cx,
                    ))
                    .child(self.action(
                        "punch-page-next",
                        "下一页",
                        "chevron",
                        false,
                        (self.punch.page + 1) * 64 >= count,
                        |a, _, _| a.punch.page += 1,
                        cx,
                    )),
            )
    }
    fn event_detail(&self, cx: &mut Context<Self>) -> Div {
        let p = self.palette(cx);
        card(p)
            .child(
                row()
                    .justify_between()
                    .child(section("info", "原始记录详情", p))
                    .child(
                        row()
                            .child(self.action(
                                "copy-event",
                                "复制",
                                "copy",
                                false,
                                false,
                                |a, _, cx| a.clipboard(a.selected_event.clone(), cx),
                                cx,
                            ))
                            .child(self.action(
                                "close-event",
                                "",
                                "close",
                                false,
                                false,
                                |a, _, _| a.selected_event.clear(),
                                cx,
                            )),
                    ),
            )
            .child(
                div()
                    .text_sm()
                    .font_family("monospace")
                    .child(self.selected_event.clone()),
            )
    }
}
