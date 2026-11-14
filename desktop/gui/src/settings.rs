use crate::{
    app::{Desktop, Page},
    motion,
    pages::section,
    ui::{card, col, fact, heading, icon, row},
};
use gpui::{Context, Div, Window, div, prelude::*, px};
use hole_desktop::{config, controller::Command};

impl Desktop {
    pub fn settings(&self, window: &mut Window, cx: &mut Context<Self>) -> Div {
        let p = self.palette(cx);
        let tabs = self.segmented(
            "settings",
            &[
                ("房间与身份", "shield"),
                ("连接方式", "route"),
                ("外观", "sun"),
                ("桌面行为", "devices"),
            ],
            self.settings_tab,
            |app, index| app.settings_tab = index,
            cx,
        );
        let body = match self.settings_tab {
            0 => self.connection_settings(window, cx),
            1 => self.transport_settings(window, cx),
            2 => self.appearance(cx),
            _ => self.behavior(cx),
        };
        col()
            .gap_5()
            .child(tabs)
            .child(motion::enter(
                format!("settings-body-{}", self.settings_tab),
                body,
            ))
            .child(
                card(p)
                    .py_3()
                    .flex_row()
                    .justify_between()
                    .items_center()
                    .child(
                        div()
                            .text_sm()
                            .text_color(if self.dirty() { p.warn } else { p.muted })
                            .child(if self.dirty() {
                                "有尚未保存的修改"
                            } else {
                                "设置已保存"
                            }),
                    )
                    .child(
                        row()
                            .child(self.action(
                                "settings-reset",
                                "还原",
                                "back",
                                false,
                                !self.dirty(),
                                |app, _, _| app.reset_draft(),
                                cx,
                            ))
                            .child(self.action(
                                "settings-save",
                                "保存设置",
                                "check",
                                true,
                                !self.dirty(),
                                |app, _, _| app.save(),
                                cx,
                            )),
                    ),
            )
            .child(
                row()
                    .flex_wrap()
                    .child(self.action(
                        "settings-transfer",
                        "配置交换与备份",
                        "copy",
                        false,
                        false,
                        |app, _, _| app.navigate(Page::Transfer),
                        cx,
                    ))
                    .child(self.action(
                        "settings-nat",
                        "NAT 检测",
                        "network",
                        false,
                        false,
                        |app, _, _| app.navigate(Page::NAT),
                        cx,
                    ))
                    .child(self.action(
                        "settings-diagnostics",
                        "运行诊断",
                        "activity",
                        false,
                        false,
                        |app, _, _| app.navigate(Page::Diagnostics),
                        cx,
                    )),
            )
    }
    fn connection_settings(&self, window: &mut Window, cx: &mut Context<Self>) -> Div {
        let p = self.palette(cx);
        let c = &self.draft.connection;
        let identity = card(p)
            .flex_1()
            .child(section("shield", "房间与身份", p))
            .child(self.field(
                "room-server",
                "信令服务器",
                c.server_url.clone(),
                0,
                |a, v| a.draft.connection.server_url = v,
                window,
                cx,
            ))
            .child(self.field(
                "room-password",
                "信令密码",
                self.draft_secrets.password.clone(),
                1,
                |a, v| a.draft_secrets.password = v,
                window,
                cx,
            ))
            .child(self.field(
                "room-room",
                "房间号",
                c.room.clone(),
                0,
                |a, v| a.draft.connection.room = v,
                window,
                cx,
            ))
            .child(self.field(
                "room-token",
                "房间密码",
                self.draft_secrets.token.clone(),
                1,
                |a, v| a.draft_secrets.token = v,
                window,
                cx,
            ));
        let device = card(p)
            .flex_1()
            .child(section("devices", "本机设备", p))
            .child(self.field(
                "room-device",
                "设备名",
                c.device_name.clone(),
                0,
                |a, v| a.draft.connection.device_name = v,
                window,
                cx,
            ))
            .child(self.field(
                "room-timeout",
                "会话保留期限",
                c.session_timeout.clone(),
                0,
                |a, v| a.draft.connection.session_timeout = v,
                window,
                cx,
            ))
            .child(
                div()
                    .text_xs()
                    .text_color(p.muted)
                    .child("设备名在房间内唯一；会话期限例如 10m、1h。"),
            )
            .child(self.field(
                "room-interfaces",
                "IPv6 候选接口",
                config::join_list(&c.candidate_interfaces),
                0,
                |a, v| a.draft.connection.candidate_interfaces = config::split_list(&v),
                window,
                cx,
            ))
            .child(self.field(
                "room-addresses",
                "手动公网 IPv6（仅 IPv6 模式）",
                config::join_list(&c.candidate_addresses),
                0,
                |a, v| a.draft.connection.candidate_addresses = config::split_list(&v),
                window,
                cx,
            ));
        if window.bounds().size.width >= px(1150.) {
            row().items_start().gap_5().child(identity).child(device)
        } else {
            col().gap_5().child(identity).child(device)
        }
    }
    fn transport_settings(&self, window: &mut Window, cx: &mut Context<Self>) -> Div {
        let p = self.palette(cx);
        let c = &self.draft.connection;
        let mut modes = row().flex_wrap();
        for (mode, label) in [("auto", "自动"), ("ice", "仅 ICE"), ("legacy", "仅 IPv6")] {
            modes = modes.child(self.action(
                format!("mode-{mode}"),
                label,
                "route",
                c.connection_mode == mode,
                false,
                move |a, _, _| a.draft.connection.connection_mode = mode.into(),
                cx,
            ));
        }
        let policy =
            card(p)
                .child(section("route", "连接策略", p))
                .child(modes)
                .child(div().text_sm().text_color(p.muted).child(
                    "自动：ICE 优先，兼容 IPv6；仅 ICE：不回退 IPv6；仅 IPv6：不使用 ICE。",
                ));
        let mut nat_modes = row().flex_wrap();
        for (mode, label) in [(0, "默认分类服务"), (1, "自定义"), (2, "不使用分类 STUN")]
        {
            let selected = match mode {
                0 => c.ice.nat_stun_urls.is_none(),
                1 => c.ice.nat_stun_urls.as_ref().is_some_and(|v| !v.is_empty()),
                _ => c.ice.nat_stun_urls.as_ref().is_some_and(|v| v.is_empty()),
            };
            nat_modes = nat_modes.child(self.action(
                format!("nat-stun-mode-{mode}"),
                label,
                "network",
                selected,
                false,
                move |a, _, _| {
                    a.draft.connection.ice.nat_stun_urls = match mode {
                        0 => None,
                        1 => Some(vec![config::DEFAULT_STUN_URL.into()]),
                        _ => Some(vec![]),
                    };
                    a.form_epoch += 1;
                },
                cx,
            ));
        }
        let mut ice = card(p)
            .flex_1()
            .child(section("network", "NAT 与直连", p))
            .child(self.field(
                "ice-stun",
                "ICE STUN 地址（每行一个）",
                c.ice.stun_urls.join("\n"),
                2,
                |a, v| a.draft.connection.ice.stun_urls = config::split_list(&v),
                window,
                cx,
            ))
            .child(self.toggle(
                "port-mapping",
                "自动 UDP 端口映射",
                "使用路由器的 UPnP / NAT-PMP",
                c.ice.port_mapping != "off",
                |a, v| a.draft.connection.ice.port_mapping = if v { "auto" } else { "off" }.into(),
                cx,
            ))
            .child(div().text_sm().text_color(p.muted).child("NAT 分类服务"))
            .child(nat_modes);
        if c.ice.nat_stun_urls.as_ref().is_some_and(|v| !v.is_empty()) {
            ice = ice.child(self.field(
                "nat-stun-custom",
                "分类 STUN 地址（最多 8 个）",
                c.ice.nat_stun_urls.clone().unwrap_or_default().join("\n"),
                2,
                |a, v| a.draft.connection.ice.nat_stun_urls = Some(config::split_list(&v)),
                window,
                cx,
            ));
        }
        ice = ice
            .child(self.field(
                "ice-interface",
                "ICE 接口白名单",
                config::join_list(&c.ice.interface_allowlist),
                0,
                |a, v| a.draft.connection.ice.interface_allowlist = config::split_list(&v),
                window,
                cx,
            ))
            .child(self.toggle(
                "relay-only",
                "仅使用中继",
                "跳过直连与自动端口映射",
                c.ice.relay_only,
                |a, v| a.draft.connection.ice.relay_only = v,
                cx,
            ))
            .child(self.toggle(
                "loopback",
                "包含回环候选",
                "本地测试使用",
                c.ice.include_loopback,
                |a, v| a.draft.connection.ice.include_loopback = v,
                cx,
            ));
        let mut turn = card(p).flex_1().child(section("share", "中继", p));
        let mut sources = row().flex_wrap();
        for (mode, label) in [
            ("worker", "自动凭据"),
            ("manual", "手动配置"),
            ("off", "关闭 TURN"),
        ] {
            sources = sources.child(self.action(
                format!("turn-{mode}"),
                label,
                "shield",
                c.turn.mode == mode,
                false,
                move |a, _, _| a.draft.connection.turn.mode = mode.into(),
                cx,
            ));
        }
        turn = turn.child(sources).child(self.field(
            "relay-device",
            "Hole 中继设备名",
            c.relay.device.clone(),
            0,
            |a, v| a.draft.connection.relay.device = v,
            window,
            cx,
        ));
        let order = c.relay_order();
        let mut order_rows = col().gap_2();
        for (index, kind) in order.iter().enumerate() {
            let label = kind.to_uppercase();
            order_rows = order_rows.child(
                row()
                    .p_2()
                    .rounded(px(12.))
                    .bg(p.inset)
                    .child(icon("route", p.accent, 16.))
                    .child(div().flex_1().child(label))
                    .child(self.action(
                        format!("relay-up-{index}"),
                        "",
                        "arrow-up",
                        false,
                        index == 0,
                        move |a, _, _| a.reorder_relay(index, -1),
                        cx,
                    ))
                    .child(self.action(
                        format!("relay-down-{index}"),
                        "",
                        "arrow-down",
                        false,
                        index + 1 == order.len(),
                        move |a, _, _| a.reorder_relay(index, 1),
                        cx,
                    ))
                    .child(self.action(
                        format!("relay-remove-{index}"),
                        "",
                        "close",
                        false,
                        false,
                        move |a, _, _| {
                            let mut order = a.draft.connection.relay_order();
                            order.remove(index);
                            a.draft.connection.relay.order = Some(order);
                        },
                        cx,
                    )),
            );
        }
        let mut add = row().flex_wrap();
        for kind in ["hole", "udp", "tcp", "tls"] {
            if !order.iter().any(|v| v == kind) {
                add = add.child(self.action(
                    format!("relay-add-{kind}"),
                    kind.to_uppercase(),
                    "plus",
                    false,
                    false,
                    move |a, _, _| {
                        let mut order = a.draft.connection.relay_order();
                        order.push(kind.into());
                        a.draft.connection.relay.order = Some(order);
                    },
                    cx,
                ));
            }
        }
        turn = turn
            .child(
                div()
                    .text_sm()
                    .text_color(p.muted)
                    .child("统一中继尝试顺序"),
            )
            .child(order_rows)
            .child(add)
            .child(self.action(
                "relay-default",
                "恢复默认顺序",
                "refresh",
                false,
                false,
                |a, _, _| a.draft.connection.relay.order = Some(vec![]),
                cx,
            ));
        if c.turn.mode == "manual" {
            turn = turn
                .child(self.field(
                    "turn-urls",
                    "TURN 地址",
                    c.turn.urls.join("\n"),
                    2,
                    |a, v| a.draft.connection.turn.urls = config::split_list(&v),
                    window,
                    cx,
                ))
                .child(self.field(
                    "turn-user",
                    "TURN 用户名",
                    c.turn.username.clone(),
                    0,
                    |a, v| a.draft.connection.turn.username = v,
                    window,
                    cx,
                ))
                .child(self.field(
                    "turn-password",
                    "TURN 凭据",
                    self.draft_secrets.turn_credential.clone(),
                    1,
                    |a, v| a.draft_secrets.turn_credential = v,
                    window,
                    cx,
                ));
        }
        let pair = if window.bounds().size.width >= px(1180.) {
            row().items_start().gap_5().child(ice).child(turn)
        } else {
            col().gap_5().child(ice).child(turn)
        };
        let timing = card(p)
            .child(section("clock", "超时与高级参数", p))
            .child(
                row()
                    .gap_4()
                    .child(self.field(
                        "direct-timeout",
                        "直连探测",
                        c.ice.direct_probe_timeout.clone(),
                        0,
                        |a, v| a.draft.connection.ice.direct_probe_timeout = v,
                        window,
                        cx,
                    ))
                    .child(self.field(
                        "gather-timeout",
                        "候选收集",
                        c.ice.gather_timeout.clone(),
                        0,
                        |a, v| a.draft.connection.ice.gather_timeout = v,
                        window,
                        cx,
                    )),
            )
            .child(
                row()
                    .gap_4()
                    .child(self.field(
                        "connectivity-timeout",
                        "连通性检查",
                        c.ice.connectivity_timeout.clone(),
                        0,
                        |a, v| a.draft.connection.ice.connectivity_timeout = v,
                        window,
                        cx,
                    ))
                    .child(self.field(
                        "retry-delay",
                        "最大重试间隔",
                        c.ice.retry_max_delay.clone(),
                        0,
                        |a, v| a.draft.connection.ice.retry_max_delay = v,
                        window,
                        cx,
                    )),
            )
            .child(self.field(
                "turn-ttl",
                "TURN 凭据有效期",
                c.turn.ttl.clone(),
                0,
                |a, v| a.draft.connection.turn.ttl = v,
                window,
                cx,
            ))
            .child(self.toggle(
                "insecure-signal",
                "允许明文信令",
                "仅在明确使用本地 ws:// 测试时开启",
                c.allow_insecure_signal,
                |a, v| a.draft.connection.allow_insecure_signal = v,
                cx,
            ));
        col().gap_5().child(policy).child(pair).child(timing)
    }
    fn reorder_relay(&mut self, index: usize, direction: isize) {
        let mut order = self.draft.connection.relay_order();
        let target = index.saturating_add_signed(direction);
        if target < order.len() {
            order.swap(index, target);
        }
        self.draft.connection.relay.order = Some(order);
    }
    fn appearance(&self, cx: &mut Context<Self>) -> Div {
        let p = self.palette(cx);
        let mut modes = row().flex_wrap();
        for (mode, label, name) in [
            ("system", "跟随系统", "devices"),
            ("light", "浅色", "sun"),
            ("dark", "深色", "moon"),
        ] {
            modes = modes.child(self.action(
                format!("theme-{mode}"),
                label,
                name,
                self.draft.prefs.theme_mode == mode,
                false,
                move |a, _, _| a.draft.prefs.theme_mode = mode.into(),
                cx,
            ));
        }
        let mut accents = row().flex_wrap();
        for (color, label) in [
            ("blue", "蓝色"),
            ("teal", "绿色"),
            ("violet", "紫色"),
            ("rose", "玫瑰"),
            ("amber", "琥珀"),
        ] {
            accents = accents.child(self.action(
                format!("accent-{color}"),
                label,
                "sun",
                self.draft.prefs.accent == color,
                false,
                move |a, _, _| a.draft.prefs.accent = color.into(),
                cx,
            ));
        }
        card(p)
            .child(section("sun", "外观", p))
            .child(modes)
            .child(div().text_sm().text_color(p.muted).child("强调色"))
            .child(accents)
            .child(self.toggle(
                "reduce-motion",
                "减少动态效果",
                "立即完成所有界面过渡",
                self.draft.prefs.reduce_motion,
                |a, v| a.draft.prefs.reduce_motion = v,
                cx,
            ))
    }
    fn behavior(&self, cx: &mut Context<Self>) -> Div {
        let p = self.palette(cx);
        card(p)
            .child(section("devices", "桌面与运行", p))
            .child(self.toggle(
                "resume-connect",
                "打开应用时恢复连接",
                "仅在上次仍请求运行时恢复；手动停止后保持停止",
                self.draft.prefs.resume_at_login,
                |a, v| a.draft.prefs.resume_at_login = v,
                cx,
            ))
            .child(fact("桌面版本", env!("CARGO_PKG_VERSION"), p))
            .child(fact("系统", std::env::consts::OS, p))
            .child(fact("核心加载", self.state.host.clone(), p))
            .child(
                div()
                    .text_sm()
                    .text_color(p.muted)
                    .child("关闭窗口会确认退出并停止核心。系统托盘与登录自启尚未接入。"),
            )
    }
    pub fn transfer_page(&self, window: &mut Window, cx: &mut Context<Self>) -> Div {
        let p = self.palette(cx);
        let mut page = col()
            .gap_5()
            .child(heading(
                "配置交换与备份",
                "支持 CLI YAML / JSON、Android 备份与桌面备份；导入前先预览。",
                p,
            ))
            .child(
                card(p)
                    .child(section("copy", "导入 / 导出", p))
                    .child(self.field(
                        &format!("transfer-text-{}", self.transfer_epoch),
                        "配置内容",
                        self.transfer.clone(),
                        2,
                        |a, v| a.transfer = v,
                        window,
                        cx,
                    ))
                    .child(
                        row()
                            .flex_wrap()
                            .child(self.action(
                                "paste-config",
                                "粘贴",
                                "copy",
                                false,
                                false,
                                |a, _, cx| {
                                    if let Some(text) =
                                        cx.read_from_clipboard().and_then(|c| c.text())
                                    {
                                        a.transfer = text;
                                        a.transfer_epoch += 1;
                                    }
                                },
                                cx,
                            ))
                            .child(self.action(
                                "preview-config",
                                "预览导入",
                                "upload",
                                true,
                                self.transfer.trim().is_empty(),
                                |a, _, _| a.controller.send(Command::Import(a.transfer.clone())),
                                cx,
                            ))
                            .child(self.action(
                                "copy-config",
                                "复制内容",
                                "copy",
                                false,
                                self.transfer.is_empty(),
                                |a, _, cx| a.clipboard(a.transfer.clone(), cx),
                                cx,
                            )),
                    )
                    .child(self.field(
                        "document-path",
                        "文件路径（导出留空则生成文本）",
                        self.path.clone(),
                        0,
                        |a, v| a.path = v,
                        window,
                        cx,
                    ))
                    .child(
                        row()
                            .flex_wrap()
                            .child(self.action(
                                "read-config",
                                "读取文件",
                                "upload",
                                false,
                                self.path.trim().is_empty(),
                                |a, _, _| a.load_file(),
                                cx,
                            ))
                            .child(self.action(
                                "export-cli",
                                "导出 CLI YAML",
                                "download",
                                false,
                                false,
                                |a, _, _| {
                                    a.controller.send(Command::Export {
                                        path: a.path.clone(),
                                        backup: false,
                                        secrets: a.include_secrets,
                                    })
                                },
                                cx,
                            ))
                            .child(self.action(
                                "export-backup",
                                "导出完整备份",
                                "download",
                                false,
                                false,
                                |a, _, _| {
                                    a.controller.send(Command::Export {
                                        path: a.path.clone(),
                                        backup: true,
                                        secrets: a.include_secrets,
                                    })
                                },
                                cx,
                            )),
                    )
                    .child(self.toggle(
                        "export-secrets",
                        "导出包含凭据",
                        "默认关闭；包含凭据的文件请妥善保存",
                        self.include_secrets,
                        |a, v| a.include_secrets = v,
                        cx,
                    )),
            );
        if let Some(preview) = &self.preview {
            page = page.child(
                card(p)
                    .child(section("shield", "导入预览", p))
                    .child(
                        row()
                            .gap_7()
                            .flex_wrap()
                            .child(fact("来源", preview.source.clone(), p))
                            .child(fact("房间", preview.config.connection.room.clone(), p))
                            .child(fact(
                                "提供 / 使用服务",
                                format!(
                                    "{} / {}",
                                    preview.config.provide.len(),
                                    preview.config.consume.len()
                                ),
                                p,
                            )),
                    )
                    .child(self.action(
                        "confirm-import",
                        "确认替换配置",
                        "check",
                        true,
                        false,
                        |a, _, _| {
                            a.reset_draft();
                            a.controller.send(Command::ConfirmImport);
                            a.preview = None;
                        },
                        cx,
                    )),
            );
        }
        page
    }
}
