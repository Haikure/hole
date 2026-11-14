use crate::{
    app::{Desktop, Page},
    motion,
    pages::section,
    ui::{self, badge, card, col, empty, fact, heading, icon, row},
};
use gpui::{Animation, AnimationExt, Context, Div, Window, div, prelude::*, px};
use hole_desktop::punch::{self, Record, number, text};
use yororen_ui::headless::slider::slider;

impl Desktop {
    pub fn punch_page(&self, window: &mut Window, cx: &mut Context<Self>) -> Div {
        let p = self.palette(cx);
        let mut page = col().gap_5().child(
            row()
                .justify_between()
                .flex_wrap()
                .child(heading("打洞过程", "查看真实连接记录与端口变化", p))
                .child(self.action(
                    "punch-back",
                    "返回连接",
                    "back",
                    false,
                    false,
                    |app, _, _| app.page = Page::Devices,
                    cx,
                )),
        );
        let peers: Vec<_> = self
            .state
            .snapshot
            .peers
            .iter()
            .filter(|peer| !hole_desktop::snapshot::is_fixed_peer(peer))
            .collect();
        if peers.len() > 1 {
            let mut devices = row().flex_wrap();
            for (index, peer) in peers.iter().enumerate() {
                let transport = peer.transport_id.clone();
                devices = devices.child(self.action(
                    format!("punch-peer-{index}"),
                    peer.peer_id.clone(),
                    "devices",
                    self.punch.transport == transport,
                    false,
                    move |app, _, _| app.open_process(transport.clone()),
                    cx,
                ));
            }
            page = page.child(devices);
        }
        if self.punch.attempts.len() > 1 {
            let mut attempts = row().flex_wrap().child(self.action(
                "attempt-latest",
                "最新尝试",
                "refresh",
                self.punch.follow_latest,
                false,
                |app, _, _| {
                    app.punch.select(app.punch.transport.clone(), String::new());
                    app.playing = false;
                    app.expanded_stages.clear();
                },
                cx,
            ));
            for (index, attempt) in self.punch.attempts.iter().enumerate() {
                let generation = text(attempt, "generation").to_string();
                attempts = attempts.child(self.action(
                    format!("attempt-{index}"),
                    format!("第 {} 次 · {}", generation, phase(text(attempt, "phase"))),
                    "clock",
                    self.punch.generation == generation,
                    false,
                    move |app, _, _| {
                        app.punch
                            .select(app.punch.transport.clone(), generation.clone());
                        app.playing = false;
                        app.expanded_stages.clear();
                    },
                    cx,
                ));
            }
            page = page.child(attempts);
        }
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
        let connection = self.punch.record_for("connection").unwrap_or(record);
        let count = if self.punch.stream == "discovery" {
            connection.events.len()
        } else {
            self.punch.count()
        };
        let run = connection.at(count);
        page = page.child(self.activity_card(connection, &run, count, cx));
        let visible: Vec<_> = connection.events.values().take(count).collect();
        let sent = visible
            .iter()
            .filter(|e| text(e, "packet") == "PROBE" && text(e, "direction") == "out")
            .count();
        let ack = visible
            .iter()
            .filter(|e| text(e, "packet") == "ACK" && text(e, "direction") == "in")
            .count();
        let sockets = run["sockets"].as_array().map(|s| s.len()).unwrap_or(0);
        let targets = run["rounds"]
            .as_array()
            .into_iter()
            .flatten()
            .flat_map(|r| r["targets"].as_array().into_iter().flatten())
            .collect::<std::collections::HashSet<_>>()
            .len();
        let columns = if window.bounds().size.width >= px(1100.) {
            4
        } else {
            2
        };
        let mut metrics = div().grid().grid_cols(columns).gap_3();
        for (name, label, value) in [
            ("arrow-up", "PROBE 已发", sent),
            ("check", "ACK 收到", ack),
            ("network", "参与 socket", sockets),
            ("grid", "目标端口", targets),
        ] {
            metrics = metrics.child(
                card(p)
                    .gap_3()
                    .child(
                        row()
                            .gap_2()
                            .child(icon(name, p.accent, 18.))
                            .child(div().text_xs().text_color(p.muted).child(label)),
                    )
                    .child(
                        div()
                            .text_3xl()
                            .font_family("monospace")
                            .child(value.to_string()),
                    ),
            );
        }
        page = page.child(metrics).child(self.replay_card(record, cx));
        page = page.child(self.segmented(
            "punch",
            &[("过程", "activity"), ("端口", "grid"), ("报文", "packet")],
            self.punch.view.min(2),
            |app, index| {
                app.punch.view = index;
                app.punch.stream = "connection".into();
                app.punch.live();
                app.playing = false;
                app.selected_event.clear();
                app.selected_socket.clear();
            },
            cx,
        ));
        if run["rounds"].as_array().is_some_and(|r| r.len() > 1) {
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
        }
        let view = match self.punch.view {
            1 => self.port_matrix(&run, window, cx),
            2 => self.packet_list(window, cx),
            _ => self.timeline(&run, cx),
        };
        page.child(motion::enter(
            format!("punch-body-{}", self.punch.view),
            view,
        ))
    }

    fn activity_card(
        &self,
        record: &Record,
        run: &serde_json::Value,
        count: usize,
        cx: &mut Context<Self>,
    ) -> Div {
        let p = self.palette(cx);
        let hp = ui::tone_palette(
            p,
            match text(run, "stage") {
                "connected" => "good",
                _ if text(run, "state") == "failed" => "bad",
                _ => "info",
            },
        );
        let peer = self
            .state
            .snapshot
            .peers
            .iter()
            .find(|peer| peer.transport_id == self.punch.transport);
        let events: Vec<_> = record.events.values().take(count).collect();
        let latest = events.last().copied();
        let endpoint_event = events.iter().rev().copied().find(|e| {
            !text(e, "local_address").is_empty() || !text(e, "remote_address").is_empty()
        });
        let selected = run["sockets"]
            .as_array()
            .into_iter()
            .flatten()
            .find(|s| text(s, "state") == "selected");
        let local = selected
            .map(|s| text(s, "local_address"))
            .filter(|v| !v.is_empty())
            .or_else(|| {
                endpoint_event
                    .map(|e| text(e, "local_address"))
                    .filter(|v| !v.is_empty())
            })
            .or_else(|| endpoint_event.map(|e| text(e, "bound_address")))
            .unwrap_or("");
        let remote = selected
            .map(|s| text(s, "target"))
            .filter(|v| !v.is_empty())
            .or_else(|| endpoint_event.map(|e| text(e, "remote_address")))
            .unwrap_or("");
        let state = if self.punch.paused {
            "回放节点"
        } else {
            match text(run, "state") {
                "connected" | "active" | "succeeded" if text(run, "stage") == "connected" => {
                    "已连接"
                }
                "failed" => "已结束",
                "complete" => "采样完成",
                "cancelled" => "已取消",
                _ => "进行中",
            }
        };
        let mut hero = card(hp)
            .gap_5()
            .child(
                row()
                    .justify_between()
                    .child(
                        row()
                            .gap_4()
                            .child(
                                div()
                                    .p_4()
                                    .rounded_full()
                                    .bg(hp.soft)
                                    .child(icon("route", hp.accent, 26.)),
                            )
                            .child(
                                col()
                                    .gap_1()
                                    .child(
                                        div()
                                            .text_2xl()
                                            .font_weight(gpui::FontWeight::SEMIBOLD)
                                            .child(
                                                peer.map(|p| p.peer_id.clone())
                                                    .unwrap_or_else(|| "设备打洞过程".into()),
                                            ),
                                    )
                                    .child(
                                        div()
                                            .text_sm()
                                            .text_color(hp.accent)
                                            .child(phase(text(run, "phase")).to_string()),
                                    ),
                            ),
                    )
                    .child(
                        col()
                            .gap_1()
                            .items_end()
                            .child(
                                div()
                                    .text_lg()
                                    .font_family("monospace")
                                    .text_color(hp.accent)
                                    .child(format!("{} ms", number(run, "elapsed_ms"))),
                            )
                            .child(div().text_xs().text_color(hp.accent).child(state)),
                    ),
            )
            .child(
                row()
                    .items_stretch()
                    .gap_4()
                    .child(self.process_endpoint(
                        "local",
                        "本机 UDP",
                        local,
                        if selected.is_some() {
                            "选中业务 socket"
                        } else {
                            "socket 绑定 / 实际观测"
                        },
                        cx,
                    ))
                    .child(self.process_endpoint(
                        "remote",
                        if selected.is_none()
                            && endpoint_event.is_some_and(|e| text(e, "packet") == "STUN")
                        {
                            "STUN 服务"
                        } else {
                            "打洞目标"
                        },
                        remote,
                        if selected.is_some() {
                            "选中线路"
                        } else {
                            "实际收发"
                        },
                        cx,
                    )),
            );
        let mut flow = div()
            .relative()
            .h(px(16.))
            .mx_1()
            .child(
                div()
                    .absolute()
                    .top(px(7.))
                    .h(px(2.))
                    .w_full()
                    .bg(ui::blend(hp.surface, hp.accent, 0.2)),
            )
            .child(
                div()
                    .absolute()
                    .top(px(5.))
                    .left_0()
                    .size(px(6.))
                    .rounded_full()
                    .bg(ui::blend(hp.surface, hp.accent, 0.4)),
            )
            .child(
                div()
                    .absolute()
                    .top(px(5.))
                    .right_0()
                    .size(px(6.))
                    .rounded_full()
                    .bg(ui::blend(hp.surface, hp.accent, 0.4)),
            );
        if let Some(event) =
            latest.filter(|e| !text(e, "direction").is_empty() && !text(e, "packet").is_empty())
        {
            let incoming = text(event, "direction") == "in";
            flow = flow.child(
                div().absolute().left_0().right(px(10.)).h_full().child(
                    div()
                        .absolute()
                        .top(px(3.))
                        .size(px(10.))
                        .rounded_full()
                        .bg(hp.accent)
                        .with_animation(
                            format!(
                                "flow-{}-{}-{}-{}",
                                self.punch.transport,
                                self.punch.generation,
                                self.punch.stream,
                                text(event, "sequence")
                            ),
                            Animation::new(std::time::Duration::from_millis(550)),
                            move |el, t| el.left(gpui::relative(if incoming { 1. - t } else { t })),
                        ),
                ),
            );
        }
        hero = hero.child(flow).child(
            row()
                .gap_2()
                .child(icon(
                    if text(run, "stage") == "connected" {
                        "check"
                    } else {
                        "activity"
                    },
                    hp.accent,
                    22.,
                ))
                .child(
                    div()
                        .font_weight(gpui::FontWeight::MEDIUM)
                        .child(punch::stage(text(run, "stage")).to_string()),
                ),
        );
        if let Some(event) = latest {
            hero = hero.child(div().text_xs().text_color(hp.accent).child(format!(
                "{} · +{} ms",
                text(event, "title"),
                number(event, "elapsed_ms")
            )));
        }
        hero.child(
            row()
                .flex_wrap()
                .child(badge(
                    format!("第 {} 次尝试", self.punch.generation),
                    p.accent,
                    p,
                ))
                .children(
                    latest
                        .filter(|e| !text(e, "socket_id").is_empty())
                        .map(|e| badge(socket_label(text(e, "socket_id")), p.accent, p)),
                ),
        )
    }

    fn process_endpoint(
        &self,
        id: &str,
        title: &str,
        value: &str,
        source: &str,
        cx: &mut Context<Self>,
    ) -> Div {
        let p = self.palette(cx);
        let (host, port) = punch::endpoint(value);
        let copy = value.to_string();
        card(p)
            .flex_1()
            .gap_2()
            .child(
                row()
                    .justify_between()
                    .child(
                        row()
                            .gap_2()
                            .child(icon(
                                if id == "local" { "devices" } else { "grid" },
                                p.accent,
                                18.,
                            ))
                            .child(div().text_sm().child(title.to_string())),
                    )
                    .child(self.action(
                        format!("copy-endpoint-{id}"),
                        "",
                        "copy",
                        false,
                        value.is_empty(),
                        move |app, _, cx| app.clipboard(copy.clone(), cx),
                        cx,
                    )),
            )
            .child(
                div()
                    .text_sm()
                    .font_family("monospace")
                    .child(if host.is_empty() { "待观测" } else { host }.to_string()),
            )
            .child(
                div()
                    .text_3xl()
                    .font_family("monospace")
                    .text_color(p.accent)
                    .child(if port.is_empty() { "—" } else { port }.to_string()),
            )
            .child(
                div()
                    .text_xs()
                    .text_color(p.muted)
                    .child(source.to_string()),
            )
    }

    fn replay_card(&self, record: &Record, cx: &mut Context<Self>) -> Div {
        let p = self.palette(cx);
        let mut controls = card(p)
            .gap_3()
            .child(
                row()
                    .justify_between()
                    .flex_wrap()
                    .child(section("activity", "实时过程", p))
                    .child(badge(
                        if self.punch.paused {
                            "暂停浏览"
                        } else {
                            "实时"
                        },
                        p.accent,
                        p,
                    )),
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
                        "punch-play",
                        if self.playing {
                            "停止回放"
                        } else {
                            "连续回放"
                        },
                        if self.playing { "pause" } else { "play" },
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
                    )),
            );
        if self.punch.paused {
            let length = record.events.len().max(1);
            let weak = cx.entity().downgrade();
            controls = controls.child(
                row()
                    .child(
                        slider("punch-replay-position", cx)
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
                            .render(cx),
                    )
                    .child(
                        div()
                            .text_xs()
                            .child(format!("{} / {length}", self.punch.position)),
                    ),
            );
        }
        controls.child(div().text_xs().text_color(p.muted).child(format!(
            "已读取 {} 条 · 移出保留窗口 {} · 采集时未记录 {} · 暂停浏览不影响实际连接",
            record.events.len(),
            record.evicted,
            record.dropped
        )))
    }

    fn timeline(&self, _: &serde_json::Value, cx: &mut Context<Self>) -> Div {
        let p = self.palette(cx);
        let events: Vec<_> = self
            .punch
            .record()
            .into_iter()
            .flat_map(|r| r.events.values().take(self.punch.count()))
            .filter(|e| {
                self.punch
                    .selected_round
                    .is_none_or(|(r, t)| number(e, "round") == r && number(e, "trial") == t)
            })
            .collect();
        let groups = punch::stage_groups(&events);
        let mut content = col().gap_4();
        if let Some(discovery) = self.punch.record_for("discovery") {
            content = content.child(self.nat_samples(discovery, cx));
        }
        for (index, group) in groups
            .iter()
            .enumerate()
            .skip(self.punch.page * 64)
            .take(64)
        {
            let first = group.events[0];
            let last = group.events.last().unwrap();
            let key = format!(
                "{}-{}-{}",
                self.punch.generation,
                group.stage,
                text(first, "sequence")
            );
            let expanded = self.expanded_stages.contains(&key);
            let toggle = key.clone();
            let mut details = col().gap_3().pt_3();
            for event in group
                .events
                .iter()
                .filter(|e| text(e, "direction").is_empty() || text(e, "packet").is_empty())
                .take(16)
            {
                let raw = serde_json::to_string_pretty(event).unwrap_or_default();
                details = details.child(
                    row()
                        .items_start()
                        .child(
                            div()
                                .w(px(80.))
                                .flex_shrink_0()
                                .text_xs()
                                .font_family("monospace")
                                .text_color(p.muted)
                                .child(format!("+{} ms", number(event, "elapsed_ms"))),
                        )
                        .child(
                            col()
                                .flex_1()
                                .gap_1()
                                .child(text(event, "title").to_string())
                                .child(
                                    div()
                                        .text_xs()
                                        .text_color(p.muted)
                                        .child(text(event, "detail").to_string()),
                                ),
                        )
                        .child(self.action(
                            format!("stage-detail-{}", text(event, "sequence")),
                            "",
                            "info",
                            false,
                            !expanded,
                            move |app, _, _| {
                                app.selected_event = raw.clone();
                                app.expanded_raw = false;
                            },
                            cx,
                        )),
                );
            }
            let round = number(first, "round");
            let trial = number(first, "trial");
            details = details.child(self.action(
                format!("stage-packets-{index}"),
                "查看这一阶段的收发记录",
                "packet",
                false,
                !expanded,
                move |app, _, _| {
                    app.punch.view = 2;
                    app.punch.selected_round = (round > 0).then_some((round, trial));
                    app.punch.page = 0;
                },
                cx,
            ));
            let panel = card(p)
                .gap_0()
                .child(
                    row()
                        .gap_4()
                        .child(
                            div()
                                .size(px(44.))
                                .flex_shrink_0()
                                .flex()
                                .items_center()
                                .justify_center()
                                .rounded_full()
                                .bg(p.inset)
                                .text_color(p.muted)
                                .font_family("monospace")
                                .child(format!("{:02}", index + 1)),
                        )
                        .child(
                            col()
                                .gap_1()
                                .flex_1()
                                .child(
                                    div()
                                        .font_weight(gpui::FontWeight::MEDIUM)
                                        .child(punch::stage(group.stage).to_string()),
                                )
                                .child(
                                    div()
                                        .text_xs()
                                        .text_color(p.muted)
                                        .font_family("monospace")
                                        .child(format!(
                                            "+{} ms → {} ms · {} 条记录",
                                            number(first, "elapsed_ms"),
                                            number(last, "elapsed_ms"),
                                            group.events.len()
                                        )),
                                ),
                        )
                        .child(self.action(
                            format!("stage-toggle-{key}"),
                            "",
                            if expanded { "collapse" } else { "expand" },
                            false,
                            false,
                            move |app, _, _| {
                                if !app.expanded_stages.remove(&toggle) {
                                    app.expanded_stages.insert(toggle.clone());
                                }
                            },
                            cx,
                        )),
                )
                .child(
                    div()
                        .id(format!("stage-body-{key}"))
                        .overflow_hidden()
                        .h(if expanded {
                            gpui::Length::Auto
                        } else {
                            px(0.).into()
                        })
                        .opacity(if expanded { 1. } else { 0. })
                        .transitions(|t| t.h(motion::NORMAL).opacity(motion::NORMAL))
                        .child(details),
                );
            content = content.child(panel);
        }
        if groups.is_empty() {
            content = content.child(card(p).child(empty("clock", "暂无阶段记录", "", p)));
        }
        content = content.child(self.process_pager(groups.len(), cx));
        content
    }
    fn port_matrix(&self, run: &serde_json::Value, window: &Window, cx: &mut Context<Self>) -> Div {
        let p = self.palette(cx);
        let events = self.punch.events();
        let network = &run["network"];
        let mut network_card = card(p)
            .gap_4()
            .child(section("network", "使用的网络", p))
            .child(
                row()
                    .flex_wrap()
                    .child(badge(
                        if text(network, "transport").is_empty() {
                            "未记录网络类型"
                        } else {
                            text(network, "transport")
                        }
                        .to_string(),
                        p.accent,
                        p,
                    ))
                    .children(
                        (!text(network, "interface").is_empty())
                            .then(|| badge(text(network, "interface").to_string(), p.accent, p)),
                    ),
            );
        if !text(network, "handle").is_empty() {
            network_card =
                network_card.child(fact("网络编号", text(network, "handle").to_string(), p));
        }
        for address in network["addresses"]
            .as_array()
            .into_iter()
            .flatten()
            .filter_map(|v| v.as_str())
        {
            network_card = network_card.child(
                row()
                    .items_start()
                    .child(div().w(px(72.)).text_sm().text_color(p.muted).child(
                        if address.contains(':') {
                            "IPv6"
                        } else {
                            "IPv4"
                        },
                    ))
                    .child(
                        div()
                            .flex_1()
                            .text_sm()
                            .font_family("monospace")
                            .child(address.to_string()),
                    ),
            );
        }
        if let Some(dns) = network["dns"].as_array() {
            network_card = network_card.child(fact(
                "DNS",
                dns.iter()
                    .filter_map(|v| v.as_str())
                    .collect::<Vec<_>>()
                    .join(" · "),
                p,
            ));
        }
        let mut socket_grid = div()
            .grid()
            .grid_cols(if window.bounds().size.width >= px(1120.) {
                3
            } else {
                2
            })
            .gap_4();
        for socket in run["sockets"].as_array().into_iter().flatten().filter(|s| {
            self.punch
                .selected_round
                .is_none_or(|(r, t)| number(s, "round") == r && number(s, "trial") == t)
        }) {
            let id = text(socket, "id").to_string();
            let selected = text(socket, "state") == "selected";
            let address = if text(socket, "local_address").is_empty() {
                text(socket, "bound_address")
            } else {
                text(socket, "local_address")
            };
            let (host, port) = punch::endpoint(address);
            let show = self.selected_socket == id;
            let mut tile = card(p)
                .gap_3()
                .child(
                    row()
                        .justify_between()
                        .child(
                            row()
                                .gap_2()
                                .child(
                                    div()
                                        .p_2()
                                        .rounded(px(12.))
                                        .bg(p.soft)
                                        .child(icon("network", p.accent, 20.)),
                                )
                                .child(
                                    col().gap_1().child(socket_label(&id)).child(
                                        div()
                                            .text_xs()
                                            .text_color(p.muted)
                                            .child(socket_role(text(socket, "role")).to_string()),
                                    ),
                                ),
                        )
                        .child(self.action(
                            format!("socket-inspect-{id}"),
                            "",
                            if show { "collapse" } else { "info" },
                            false,
                            false,
                            move |app, _, _| {
                                app.selected_socket = if app.selected_socket == id {
                                    String::new()
                                } else {
                                    id.clone()
                                };
                            },
                            cx,
                        )),
                )
                .child(
                    div()
                        .text_3xl()
                        .text_color(p.accent)
                        .font_family("monospace")
                        .child(if port.is_empty() { "—" } else { port }.to_string()),
                )
                .child(
                    div()
                        .text_xs()
                        .font_family("monospace")
                        .child(if host.is_empty() { "待观测" } else { host }.to_string()),
                )
                .child(badge(
                    socket_state(text(socket, "state")).to_string(),
                    if selected { p.good } else { p.accent },
                    p,
                ))
                .child(div().text_xs().text_color(p.muted).child(format!(
                    "PROBE ↑{} ↓{} · ACK ↓{}",
                    number(socket, "probes_sent"),
                    number(socket, "probes_received"),
                    number(socket, "acks_received")
                )))
                .child(fact(
                    "公网映射",
                    if text(socket, "mapped_address").is_empty() {
                        "待观测"
                    } else {
                        text(socket, "mapped_address")
                    }
                    .to_string(),
                    p,
                ));
            let socket_id = text(socket, "id").to_string();
            tile = tile.child(
                div()
                    .id(format!("socket-body-{socket_id}"))
                    .overflow_hidden()
                    .h(if show {
                        gpui::Length::Auto
                    } else {
                        px(0.).into()
                    })
                    .opacity(if show { 1. } else { 0. })
                    .transitions(|t| t.h(motion::NORMAL).opacity(motion::NORMAL))
                    .child(
                        col()
                            .gap_3()
                            .pt_3()
                            .child(fact(
                                "绑定地址",
                                text(socket, "bound_address").to_string(),
                                p,
                            ))
                            .child(fact("目标", text(socket, "target").to_string(), p))
                            .child(self.action(
                                format!("socket-packets-{socket_id}"),
                                "查看报文",
                                "packet",
                                false,
                                !show,
                                move |app, _, _| {
                                    app.punch.filter = socket_id.clone();
                                    app.punch.transaction.clear();
                                    app.punch.packet.clear();
                                    app.punch.direction.clear();
                                    app.punch.view = 2;
                                    app.punch.stream = "connection".into();
                                    app.punch.page = 0;
                                    app.form_epoch += 1;
                                },
                                cx,
                            )),
                    ),
            );
            socket_grid = socket_grid.child(tile);
        }
        let mut content = col().gap_5().child(network_card).child(socket_grid);
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
                            app.punch.transaction.clear();
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
                        move |app, _, _| {
                            app.selected_event = raw.clone();
                            app.expanded_raw = true;
                        },
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
        let mut source = row().flex_wrap();
        for (key, label, stream) in [
            ("connection", "连接收发", "connection"),
            ("discovery", "模型采样", "discovery"),
        ] {
            source = source.child(self.action(
                format!("packet-source-{key}"),
                label,
                if key == "connection" {
                    "route"
                } else {
                    "network"
                },
                self.punch.stream == stream,
                false,
                move |app, _, _| {
                    app.punch.live();
                    app.punch.stream = stream.into();
                    app.punch.selected_round = None;
                    app.punch.filter.clear();
                    app.punch.transaction.clear();
                    app.punch.packet.clear();
                    app.punch.direction.clear();
                    app.form_epoch += 1;
                    app.playing = false;
                    app.selected_event.clear();
                },
                cx,
            ));
        }
        let selected_direction = match self.punch.direction.as_str() {
            "out" => 1,
            "in" => 2,
            _ => 0,
        };
        let filters = card(p)
            .gap_4()
            .child(section("filter", "报文筛选", p))
            .child(self.field(
                "packet-filter",
                "搜索端点、socket、事务或记录编号",
                self.punch.filter.clone(),
                0,
                |app, value| {
                    app.punch.filter = value;
                    app.punch.transaction.clear();
                    app.punch.page = 0;
                },
                window,
                cx,
            ))
            .child(row().flex_wrap().gap_2().children(
                ["", "PROBE", "ACK", "STUN"].into_iter().map(|kind| {
                    self.action(
                        format!("packet-kind-{kind}"),
                        if kind.is_empty() { "全部" } else { kind },
                        "",
                        self.punch.packet == kind,
                        false,
                        move |app, _, _| {
                            app.punch.packet = kind.into();
                            app.punch.page = 0;
                        },
                        cx,
                    )
                }),
            ))
            .child(self.segmented(
                "packet-direction",
                &[("双向", ""), ("发送", "arrow-up"), ("接收", "arrow-down")],
                selected_direction,
                |app, index| {
                    app.punch.direction = match index {
                        1 => "out",
                        2 => "in",
                        _ => "",
                    }
                    .into();
                    app.punch.page = 0;
                },
                cx,
            ));
        let wide = window.bounds().size.width >= px(1120.);
        let mut packets = div().grid().grid_cols(if wide { 2 } else { 1 }).gap_4();
        for event in events.iter().skip(self.punch.page * 64).take(64) {
            let raw = serde_json::to_string_pretty(event).unwrap_or_default();
            let incoming = text(event, "direction") == "in";
            let tone = ui::tone_palette(p, if incoming { "good" } else { "info" });
            let local = text(event, "local_address");
            let remote = text(event, "remote_address");
            let endpoint = |value: &str, color| {
                let (host, port) = punch::endpoint(value);
                col()
                    .gap_1()
                    .flex_1()
                    .child(
                        div()
                            .text_xs()
                            .font_family("monospace")
                            .child(if host.is_empty() { "待观测" } else { host }.to_string()),
                    )
                    .child(
                        div()
                            .text_2xl()
                            .font_family("monospace")
                            .text_color(color)
                            .child(if port.is_empty() { "—" } else { port }.to_string()),
                    )
            };
            let rejected = matches!(
                text(event, "validation"),
                "packet_rejected" | "source_rejected" | "send_failed"
            );
            let event_id = format!("packet-detail-{}", text(event, "sequence"));
            packets = packets.child(
                card(p)
                    .gap_4()
                    .child(
                        row()
                            .justify_between()
                            .child(
                                row()
                                    .gap_3()
                                    .child(div().p_3().rounded(px(14.)).bg(tone.soft).child(icon(
                                        if incoming { "arrow-down" } else { "arrow-up" },
                                        tone.accent,
                                        22.,
                                    )))
                                    .child(
                                        col()
                                            .gap_1()
                                            .flex_1()
                                            .child(format!("{} 报文", text(event, "packet")))
                                            .child(
                                                div()
                                                    .text_xs()
                                                    .text_color(tone.accent)
                                                    .child(text(event, "title").to_string()),
                                            ),
                                    ),
                            )
                            .child(
                                div()
                                    .text_xs()
                                    .font_family("monospace")
                                    .text_color(p.muted)
                                    .child(format!("+{} ms", number(event, "elapsed_ms"))),
                            ),
                    )
                    .child(
                        row()
                            .items_center()
                            .child(endpoint(local, p.text))
                            .child(icon(
                                if incoming { "back" } else { "chevron" },
                                tone.accent,
                                24.,
                            ))
                            .child(endpoint(remote, tone.accent)),
                    )
                    .child(
                        row()
                            .flex_wrap()
                            .gap_2()
                            .children((!text(event, "socket_id").is_empty()).then(|| {
                                badge(socket_label(text(event, "socket_id")), p.accent, p)
                            }))
                            .child(badge(format!("{} B", number(event, "bytes")), p.accent, p))
                            .child(badge(
                                validation(text(event, "validation")).to_string(),
                                if rejected { p.bad } else { tone.accent },
                                p,
                            )),
                    )
                    .children((!text(event, "transaction_id").is_empty()).then(|| {
                        div()
                            .text_xs()
                            .font_family("monospace")
                            .text_color(p.muted)
                            .child(format!("TX {}", text(event, "transaction_id")))
                    }))
                    .child(self.action(
                        event_id,
                        "查看完整记录",
                        "info",
                        false,
                        false,
                        move |app, _, _| {
                            app.selected_event = raw.clone();
                            app.expanded_raw = false;
                        },
                        cx,
                    )),
            );
        }
        let mut content = col()
            .gap_4()
            .child(source)
            .child(filters)
            .child(div().text_xs().text_color(p.muted).child(format!(
                "保留 {} 条收发记录 · 端口和方向均来自核心实际记录",
                events.len()
            )))
            .child(packets);
        if events.is_empty() {
            content =
                content.child(card(p).child(empty("packet", "当前筛选下暂无收发记录", "", p)));
        }
        content = content.child(self.process_pager(events.len(), cx));
        content
    }

    fn nat_samples(&self, record: &Record, cx: &mut Context<Self>) -> Div {
        let p = self.palette(cx);
        let mut panel = card(p)
            .gap_4()
            .child(section("activity", "模型生成时的 NAT 采样", p));
        let mut filters = row().flex_wrap().gap_2();
        for (key, title) in [
            ("", "全部"),
            ("nat_base", "基础映射"),
            ("nat_validation", "跨 socket"),
            ("nat_filtering", "过滤"),
        ] {
            filters = filters.child(self.action(
                format!("sample-role-{key}"),
                title,
                "",
                self.sample_filter == key,
                false,
                move |app, _, _| {
                    app.sample_filter = key.into();
                    app.sample_ip.clear();
                },
                cx,
            ));
        }
        let samples: Vec<_> = record
            .events
            .values()
            .filter(|e| {
                !text(e, "mapped_address").is_empty()
                    && (self.sample_filter.is_empty()
                        || text(e, "socket_role") == self.sample_filter)
            })
            .collect();
        let mut ips: Vec<_> = samples
            .iter()
            .map(|e| punch::endpoint(text(e, "mapped_address")).0)
            .collect();
        ips.sort_unstable();
        ips.dedup();
        let ip = ips
            .iter()
            .copied()
            .find(|ip| *ip == self.sample_ip)
            .or_else(|| ips.first().copied())
            .unwrap_or("")
            .to_string();
        let points: Vec<_> = samples
            .iter()
            .copied()
            .filter(|e| punch::endpoint(text(e, "mapped_address")).0 == ip)
            .collect();
        let ports: Vec<f32> = points
            .iter()
            .filter_map(|e| {
                punch::endpoint(text(e, "mapped_address"))
                    .1
                    .parse::<u16>()
                    .ok()
                    .map(|p| p as f32)
            })
            .collect();
        panel = panel
            .child(
                row()
                    .flex_wrap()
                    .child(badge(
                        if text(&record.run, "state") == "running" {
                            "采样中"
                        } else {
                            "采样记录"
                        },
                        p.accent,
                        p,
                    ))
                    .child(div().text_xs().text_color(p.muted).child(format!(
                        "{} 个映射响应 · {} ms",
                        samples.len(),
                        number(&record.run, "elapsed_ms")
                    ))),
            )
            .child(filters);
        if !ips.is_empty() {
            let mut choices = row().flex_wrap();
            for address in ips {
                let address = address.to_string();
                let selected = address == ip;
                choices = choices.child(self.action(
                    format!("sample-ip-{address}"),
                    address.clone(),
                    "",
                    selected,
                    false,
                    move |app, _, _| app.sample_ip = address.clone(),
                    cx,
                ));
            }
            panel = panel.child(choices);
        }
        if ports.is_empty() {
            panel = panel.child(
                div()
                    .text_sm()
                    .text_color(p.muted)
                    .child("当前分类暂无可用的映射响应。"),
            );
        } else {
            let min = ports.iter().copied().fold(f32::INFINITY, f32::min);
            let max = ports.iter().copied().fold(0., f32::max);
            let chart = gpui::canvas(
                |_, _, _| {},
                move |bounds, _, window, _| {
                    let plot = bounds.inset(px(8.));
                    for step in 0..=2 {
                        let y = plot.origin.y + plot.size.height * (step as f32 / 2.);
                        window.paint_quad(
                            gpui::PaintQuad::new(gpui::Bounds::new(
                                gpui::point(plot.origin.x, y),
                                gpui::size(plot.size.width, px(1.)),
                            ))
                            .background(p.inset),
                        );
                    }
                    let mut path = gpui::PathBuilder::stroke(px(2.5));
                    for (index, port) in ports.iter().enumerate() {
                        let point = gpui::point(
                            plot.origin.x
                                + plot.size.width
                                    * (index as f32
                                        / (ports.len().saturating_sub(1).max(1)) as f32),
                            plot.origin.y
                                + plot.size.height
                                    * (0.9 - 0.8 * (port - min) / (max - min).max(1.)),
                        );
                        if index == 0 {
                            path.move_to(point);
                        } else {
                            path.line_to(point);
                        }
                        window.paint_quad(
                            gpui::PaintQuad::new(gpui::Bounds::centered_at(
                                point,
                                gpui::size(px(6.), px(6.)),
                            ))
                            .background(p.accent)
                            .corner_radii(px(3.)),
                        );
                    }
                    if let Ok(path) = path.build() {
                        window.paint_path(path, p.accent);
                    }
                },
            )
            .w_full()
            .h(px(170.));
            panel = panel.child(chart).child(
                row()
                    .justify_between()
                    .child(
                        div()
                            .text_xs()
                            .font_family("monospace")
                            .child(format!("端口 {}", min as u16)),
                    )
                    .child(
                        div()
                            .text_xs()
                            .font_family("monospace")
                            .child(format!("端口 {}", max as u16)),
                    ),
            );
            let mut preview = div().grid().grid_cols(6).gap_2();
            for (index, event) in points.iter().take(6).enumerate() {
                preview =
                    preview.child(
                        col()
                            .gap_2()
                            .p_3()
                            .rounded(px(16.))
                            .bg(p.inset)
                            .child(
                                div()
                                    .text_xs()
                                    .text_color(p.muted)
                                    .child(format!("样本 {}", index + 1)),
                            )
                            .child(div().font_family("monospace").text_color(p.accent).child(
                                punch::endpoint(text(event, "mapped_address")).1.to_string(),
                            )),
                    );
            }
            panel = panel.child(preview);
        }
        let mut steps = col().gap_3().pt_3();
        for event in samples.iter().take(32) {
            steps = steps.child(
                row()
                    .items_start()
                    .justify_between()
                    .child(
                        div()
                            .text_xs()
                            .font_family("monospace")
                            .flex_1()
                            .child(text(event, "server_address").to_string()),
                    )
                    .child(
                        div()
                            .text_xs()
                            .font_family("monospace")
                            .flex_1()
                            .child(text(event, "mapped_address").to_string()),
                    ),
            );
        }
        panel
            .child(
                row()
                    .justify_between()
                    .child(
                        div()
                            .text_sm()
                            .child(format!("采样步骤 · {}", samples.len())),
                    )
                    .child(self.action(
                        "sample-expand",
                        "",
                        if self.expanded_samples {
                            "collapse"
                        } else {
                            "expand"
                        },
                        false,
                        false,
                        |app, _, _| app.expanded_samples = !app.expanded_samples,
                        cx,
                    )),
            )
            .child(
                div()
                    .id("sample-steps")
                    .overflow_hidden()
                    .h(if self.expanded_samples {
                        gpui::Length::Auto
                    } else {
                        px(0.).into()
                    })
                    .opacity(if self.expanded_samples { 1. } else { 0. })
                    .transitions(|t| t.h(motion::NORMAL).opacity(motion::NORMAL))
                    .child(steps),
            )
            .child(self.action(
                "sample-packets",
                "查看采样请求、响应与过滤探测",
                "packet",
                false,
                false,
                |app, _, _| {
                    app.punch.live();
                    app.punch.stream = "discovery".into();
                    app.punch.view = 2;
                    app.punch.selected_round = None;
                    app.punch.filter.clear();
                    app.punch.transaction.clear();
                    app.punch.packet.clear();
                    app.punch.direction.clear();
                    app.form_epoch += 1;
                    app.playing = false;
                },
                cx,
            ))
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
    pub fn process_inspection(&self, cx: &mut Context<Self>) -> Div {
        let p = self.palette(cx);
        let reveal = self.event_presence.value();
        div()
            .occlude()
            .absolute()
            .inset_0()
            .flex()
            .justify_end()
            .bg(gpui::Hsla {
                alpha: 0.28 * reveal,
                ..gpui::rgb_to_hsla(gpui::rgb(0x12182A))
            })
            .on_mouse_down(
                gpui::MouseButton::Left,
                cx.listener(|app, _, _, cx| {
                    app.selected_event.clear();
                    cx.notify();
                }),
            )
            .child(
                col()
                    .relative()
                    .left(px(540. * (1. - reveal)))
                    .w(px(540.))
                    .h_full()
                    .bg(p.bg)
                    .p_5()
                    .on_mouse_down(gpui::MouseButton::Left, |_, _, cx| cx.stop_propagation())
                    .child(
                        div()
                            .id("process-inspection")
                            .flex_1()
                            .min_h_0()
                            .overflow_y_scroll()
                            .child(self.event_detail(cx)),
                    ),
            )
    }
    fn event_detail(&self, cx: &mut Context<Self>) -> Div {
        let p = self.palette(cx);
        let raw = self
            .event_presence
            .content
            .as_deref()
            .unwrap_or(&self.selected_event);
        let event: serde_json::Value = serde_json::from_str(raw).unwrap_or_default();
        let mut detail = card(p).gap_4().child(
            row()
                .justify_between()
                .child(section(
                    "info",
                    if text(&event, "title").is_empty() {
                        if event.get("plan").is_some() {
                            "模型与计划"
                        } else {
                            "记录详情"
                        }
                    } else {
                        text(&event, "title")
                    },
                    p,
                ))
                .child(
                    row()
                        .child(self.action(
                            "copy-event",
                            "复制",
                            "copy",
                            false,
                            false,
                            |app, _, cx| app.clipboard(app.selected_event.clone(), cx),
                            cx,
                        ))
                        .child(self.action(
                            "close-event",
                            "",
                            "close",
                            false,
                            false,
                            |app, _, _| {
                                app.selected_event.clear();
                                app.expanded_raw = false;
                            },
                            cx,
                        )),
                ),
        );
        for (label, key) in [
            ("记录编号", "sequence"),
            ("代次", "generation"),
            ("绝对时间", "time"),
            ("本机 socket", "socket_id"),
            ("本机端点", "local_address"),
            ("绑定端点", "bound_address"),
            ("远端端点", "remote_address"),
            ("观测映射", "mapped_address"),
            ("STUN 事务", "transaction_id"),
            ("请求的 STUN 服务", "server_address"),
            ("OTHER-ADDRESS", "other_address"),
            ("地址来源", "address_source"),
        ] {
            if !text(&event, key).is_empty() {
                detail = detail.child(
                    row()
                        .items_start()
                        .child(
                            div()
                                .w(px(120.))
                                .flex_shrink_0()
                                .text_sm()
                                .text_color(p.muted)
                                .child(label),
                        )
                        .child(
                            div()
                                .flex_1()
                                .text_sm()
                                .font_family("monospace")
                                .child(text(&event, key).to_string()),
                        ),
                );
            }
        }
        if event.get("elapsed_ms").is_some()
            || event.get("round").is_some()
            || event.get("trial").is_some()
        {
            let mut metadata = row().flex_wrap().gap_6();
            if event.get("elapsed_ms").is_some() {
                metadata = metadata.child(fact(
                    "相对时间",
                    format!("+{} ms", number(&event, "elapsed_ms")),
                    p,
                ));
            }
            if event.get("round").is_some() || event.get("trial").is_some() {
                metadata = metadata.child(fact(
                    "轮次 / 校准",
                    format!("{} / {}", number(&event, "round"), number(&event, "trial")),
                    p,
                ));
            }
            detail = detail.child(metadata);
        }
        if !text(&event, "validation").is_empty() {
            detail = detail.child(fact(
                "验证状态",
                validation(text(&event, "validation")).to_string(),
                p,
            ));
        }
        if !text(&event, "detail").is_empty() {
            detail = detail.child(div().text_sm().child(text(&event, "detail").to_string()));
        }
        if !text(&event, "transaction_id").is_empty() {
            let transaction = text(&event, "transaction_id").to_string();
            detail = detail.child(self.action(
                "event-transaction",
                "查看此事务的请求与响应",
                "packet",
                false,
                false,
                move |app, _, _| {
                    app.punch.filter = transaction.clone();
                    app.punch.transaction = transaction.clone();
                    app.punch.packet.clear();
                    app.punch.direction.clear();
                    app.punch.selected_round = None;
                    app.punch.page = 0;
                    app.punch.view = 2;
                    app.form_epoch += 1;
                    app.selected_event.clear();
                },
                cx,
            ));
        }
        detail
            .child(self.action(
                "event-raw-toggle",
                if self.expanded_raw {
                    "收起原始记录"
                } else {
                    "原始记录"
                },
                if self.expanded_raw {
                    "collapse"
                } else {
                    "expand"
                },
                false,
                false,
                |app, _, _| app.expanded_raw = !app.expanded_raw,
                cx,
            ))
            .child(
                div()
                    .id("event-raw")
                    .overflow_hidden()
                    .h(if self.expanded_raw {
                        gpui::Length::Auto
                    } else {
                        px(0.).into()
                    })
                    .opacity(if self.expanded_raw { 1. } else { 0. })
                    .transitions(|t| t.h(motion::NORMAL).opacity(motion::NORMAL))
                    .child(
                        div()
                            .text_sm()
                            .font_family("monospace")
                            .child(raw.to_string()),
                    ),
            )
    }
}

fn phase(value: &str) -> &str {
    match value {
        "ice" | "direct" => "ICE",
        "prediction" | "punch" => "预测打洞",
        "hole" => "Hole 中继",
        "relay" | "relay_udp" => "UDP 中继",
        "relay_tcp" => "TCP 中继",
        "nat" => "NAT 采样",
        "" => "连接记录",
        _ => value,
    }
}
fn socket_label(value: &str) -> String {
    let suffix = value.rsplit(['/', '-']).next().unwrap_or(value);
    suffix
        .parse::<u64>()
        .map(|n| format!("S{n:02}"))
        .unwrap_or_else(|_| value.to_string())
}
fn socket_role(value: &str) -> &str {
    match value {
        "ice" => "ICE 候选",
        "business" => "打洞业务",
        "calibration" => "STUN 校准",
        "nat_base" => "基础映射采样",
        "nat_validation" => "跨 socket 验证",
        "nat_filtering" => "过滤探测",
        "dialer" => "连接发起端",
        "listener" => "连接接收端",
        _ => value,
    }
}
fn socket_state(value: &str) -> &str {
    match value {
        "bound" | "socket_bound" => "已绑定",
        "probing" => "探测中",
        "verified" => "ACK 已验证",
        "quic" => "QUIC 握手",
        "selected" | "socket_selected" => "已选中",
        "retained" => "保留占用",
        "closed" => "已关闭",
        _ => value,
    }
}
fn validation(value: &str) -> &str {
    match value {
        "authenticated" => "报文认证通过",
        "transaction_matched" => "STUN 事务匹配",
        "echo_verified" => "挑战回显匹配",
        "source_rejected" => "来源不在范围内",
        "packet_rejected" => "报文认证未通过",
        "sent" => "发送成功",
        "queued" => "信令已提交",
        "send_failed" => "提交未完成",
        "" => "未提供认证结果",
        _ => value,
    }
}
