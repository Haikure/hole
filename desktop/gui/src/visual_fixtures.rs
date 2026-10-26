//! 仅由显式截图模式调用的确定性巡检数据，不向核心发送命令或修改配置。
use crate::{App, MainWindow, MappingRow, PeerRow, VoiceMemberRow, VoicePeerRow, KV};
use slint::{ComponentHandle, ModelRc, VecModel};

fn model<T: Clone + 'static>(values: Vec<T>) -> ModelRc<T> { ModelRc::new(VecModel::from(values)) }

pub fn apply(window: &MainWindow, scenario: &str) {
    if scenario == "fixed" {
        apply(window, "running");
        let snapshot = fixed_snapshot();
        let app = window.global::<App>();
        let mut status = app.get_status();
        status.peers_active = "1".into(); status.peers_total = snapshot.peers.len().to_string().into();
        status.active_channels = "1".into(); status.total_channels = "1".into();
        app.set_status(status);
        app.set_peers(model(snapshot.peers.iter().map(|p| crate::controller::PeerData::from_peer(p).into_row()).collect()));
        app.set_runtime_mappings(model(snapshot.mappings.iter().map(|m| MappingRow {
            id: m.id.clone().into(), role: m.role.clone().into(), role_label: "使用服务".into(),
            protocol: m.protocol.to_uppercase().into(), state: m.state.clone().into(),
            state_label: crate::snapshot::mapping_label(&m.state).into(), kind: "good".into(),
            endpoint: m.endpoint.clone().into(), peer: m.peer.clone().into(), enabled: true,
            details: model(vec![KV { key: "传输方式".into(), value: crate::snapshot::profile_label(&m.profile).into() }]),
            ..Default::default()
        }).collect()));
        app.set_voice_peers(model(snapshot.voice.peers));
        return;
    }
    if !["empty", "running", "reconnecting", "error", "long-list"].contains(&scenario) { return; }
    let app = window.global::<App>();
    let running = scenario != "empty";
    let mut status = app.get_status();
    status.bridge_ready = true;
    status.busy = false;
    status.run_requested = running;
    status.configured = running;
    status.engine_state = if running { "running" } else { "stopped" }.into();
    status.engine_label = if running { "核心运行中" } else { "已停止" }.into();
    status.signal_state = if scenario == "reconnecting" { "reconnecting" } else if running { "joined" } else { "disconnected" }.into();
    status.signal_label = if scenario == "reconnecting" { "等待重连" } else if running { "已加入房间" } else { "未连接" }.into();
    status.title = match scenario { "empty" => "连接已停止", "reconnecting" => "正在恢复连接", "error" => "连接需要处理", _ => "转发连接正常" }.into();
    status.kind = match scenario { "empty" => "idle", "reconnecting" => "warn", "error" => "bad", _ => "good" }.into();
    status.error = if scenario == "error" { "巡检数据：信令鉴权失败，请检查房间与身份设置。" } else { "" }.into();
    status.command_error = "".into(); status.config_error = "".into(); status.bridge_error = "".into();
    status.tcp_sessions = if running { "12" } else { "0" }.into();
    status.udp_sessions = if running { "3" } else { "0" }.into();
    status.peers_active = if running && scenario != "reconnecting" { "2" } else { "0" }.into();
    status.peers_total = if running { "2" } else { "0" }.into();
    status.active_channels = if running { "4" } else { "0" }.into();
    status.total_channels = if running { "4" } else { "0" }.into();
    status.uptime = if running { "01:23:45" } else { "—" }.into();
    status.traffic_rate_up = if running { "128 KiB/s" } else { "0 B/s" }.into();
    status.traffic_rate_down = if running { "2.4 MiB/s" } else { "0 B/s" }.into();
    status.traffic_up = "42.6 MiB".into(); status.traffic_down = "1.8 GiB".into();
    status.sparkline_up = "M0 26L10 22L20 25L30 12L40 18L50 10L60 14L70 8L80 13L90 4L100 10".into();
    status.sparkline_down = "M0 30L10 20L20 24L30 8L40 14L50 2L60 12L70 6L80 10L90 0L100 5".into();
    status.network_label = "Wi-Fi · 巡检网络".into(); status.network_state = "元数据未提供".into();
    status.policy_label = "自动 · ICE 优先".into(); status.recovery_text = "重连 2 次 · 网络切换 1 次".into();
    app.set_status(status);
    if running {
        let mut connection = app.get_connection();
        connection.room = "design-preview-room".into();
        connection.device_name = "桌面巡检设备".into();
        app.set_connection(connection);
    }
    let count = if scenario == "long-list" { 40 } else if running { 2 } else { 0 };
    let provides: Vec<MappingRow> = (0..count).map(|i| MappingRow {
        entry_id: format!("fixture-{i}").into(),
        id: if i % 3 == 0 { format!("很长的服务名称-ssh-proxy-production-{i:02}") } else { format!("网页服务-{i:02}") }.into(),
        role: "provide".into(), role_label: "提供".into(), protocol: if i % 2 == 0 { "TCP" } else { "UDP" }.into(),
        endpoint: format!("[2001:db8::1]:{}", 8000 + i).into(), enabled: i % 4 != 3,
        state: "active".into(), state_label: if i % 4 == 3 { "已停用" } else { "通道可用" }.into(),
        kind: if i % 4 == 3 { "idle" } else { "good" }.into(), sessions: "TCP 3 · UDP 1".into(),
        peer: "巡检笔记本".into(),
        details: model(vec![KV {key: "地址".into(), value: "127.0.0.1".into()}, KV {key: "端口".into(), value: "8080".into()}]),
    }).collect();
    app.set_provides(model(provides.clone())); app.set_consumes(model(vec![])); app.set_runtime_mappings(model(provides));
    app.set_peers(model((0..if running { 2 } else { 0 }).map(|i| PeerRow {
        peer_id: format!("巡检设备-{i}").into(), transport_id: format!("fixture-peer-{i}").into(),
        state_label: if scenario == "reconnecting" { "正在重连" } else { "已连接" }.into(),
        path_label: if i == 0 { "IPv6 直连" } else { "TURN · TLS" }.into(),
        kind: if scenario == "reconnecting" { "warn" } else { "good" }.into(),
        channels: "2".into(), rtt: "24 ms".into(), traffic: "128 KiB / 2.4 MiB".into(),
        note: "截图巡检数据".into(), error: "".into(),
        details: model(vec![KV{key:"选中地址".into(),value:"[2001:db8::2]:3478".into()}]),
    }).collect()));
    let mut voice = app.get_voice();
    voice.enabled = running; voice.available = true; voice.running = running; voice.muted = false;
    voice.label = if running { "语音已连接" } else { "语音未开启" }.into();
    voice.capture = "系统默认麦克风".into(); voice.route = "48 kHz · 单声道".into();
    voice.error = if scenario == "error" { "巡检数据：音频设备暂不可用，请重试。" } else { "" }.into();
    app.set_voice(voice);
    app.set_voice_members(model((0..if running { 3 } else { 0 }).map(|i| VoiceMemberRow {
        name: format!("语音成员-{i}").into(), local:i==0, enabled:true, state:"已加入".into(), media:"Opus · 48 kHz".into(),
    }).collect()));
    app.set_voice_peers(model(if running { vec![VoicePeerRow {
        name:"巡检笔记本".into(),state:"已连接".into(),media:"Opus".into(),path:"IPv6".into(),
        bitrate:"32 kbit/s".into(),stats:"发送 1200 · 接收 1250".into(),buffering:"抖动缓冲 40ms".into(),error:"".into(),
    }] } else { vec![] }));
}

pub(crate) fn fixed_snapshot() -> crate::snapshot::Snapshot {
    crate::snapshot::parse_snapshot(&serde_json::from_str(include_str!("../tests/fixtures/fixed_snapshot.json")).unwrap()).unwrap()
}
