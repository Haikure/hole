package dev.hole.app.ui

import dev.hole.app.ConfigUiState
import dev.hole.corebridge.CoreSnapshot
import dev.hole.corebridge.PeerSnapshot

fun connectionModeLabel(mode: String): String = when (mode) {
    "legacy" -> "仅 IPv6"
    "ice" -> "仅 ICE"
    else -> "自动"
}
fun connectionModeDescription(mode: String): String = when (mode) {
    "legacy" -> "转发路径仅使用公网 IPv6 直连，双方都需要可用的公网 IPv6。不使用 ICE、STUN 或 TURN 中继。"
    "ice" -> "使用 ICE 探测 IPv4 / IPv6 直连，直连不通时按设置尝试 TURN 中继。协调服务和对端都需要支持 ICE。"
    else -> "优先使用 ICE；对端仅支持 IPv6 传输协议时，使用 IPv6 直连。双方都支持 ICE 时，按直连与中继策略连接，不因探测失败而切换协议。"
}
fun connectionPolicyLabel(mode: String): String = when (mode) {
    "legacy" -> "仅 IPv6 · 公网直连"
    "ice" -> "仅 ICE · IPv4 / IPv6 与中继"
    else -> "自动 · ICE 优先，兼容 IPv6 协议"
}

fun connectionTitle(s: CoreSnapshot): String = when {
    !s.runRequested -> "连接已停止"
    s.mappings.any { it.state == "active" } || s.peers.any { it.state in setOf("active", "switching") } -> "转发连接正常"
    s.errorCode in setOf("signal_auth_failed", "invalid_token", "protocol_mismatch", "worker_upgrade_required", "invalid_config", "insecure_signal") -> "连接需要处理"
    s.signalState == "joined" && s.mappings.isEmpty() -> "已就绪，尚未启用服务"
    s.signalState == "joined" && s.peers.isEmpty() -> "等待对端设备"
    s.peers.any { it.state == "reconnecting" } || s.engineState == "recovering" -> "正在恢复连接"
    else -> "正在建立连接"
}
fun configurationText(s: CoreSnapshot, config: ConfigUiState): String = when {
    !s.runRequested -> "设置已保存，开启连接后使用"
    s.errorCode != null -> "设置尚未生效，请检查连接提示"
    s.signalState == "joined" -> "当前启用的设置已生效"
    else -> "正在应用设置并加入房间"
}
fun phaseLabel(phase: String): String = when (phase) {
    "direct" -> "探测直连路径"
    "relay_udp" -> "尝试 UDP 中继"
    "relay_tls_443" -> "尝试 TLS 中继 · 443 端口"
    "relay_tls" -> "尝试 TLS 中继 · 5349 / 自定义端口"
    "relay_tcp_80" -> "尝试 TCP 中继 · 80 端口"
    "relay_tcp" -> "尝试 TCP 中继 · 3478 / 自定义端口"
    else -> "正在选择连接路径"
}
fun peerPhaseLabel(p: PeerSnapshot): String = if (p.state == "waiting_credentials")
    "等待中继凭据，尚未开始探测" else phaseLabel(p.phase)
fun peerPathLabel(p: PeerSnapshot): String = when {
    p.state == "waiting_credentials" -> peerPhaseLabel(p)
    p.pathType == "relay" -> "中继${p.addressFamily.takeIf { it.isNotBlank() }?.let { " · $it" }.orEmpty()}"
    p.pathType == "direct" -> "直连${p.addressFamily.takeIf { it.isNotBlank() }?.let { " · $it" }.orEmpty()}"
    else -> phaseLabel(p.phase)
}
fun relayAccessLabel(protocol: String, side: String = ""): String = when (protocol) {
    "udp" -> "UDP 中继"
    "tls" -> "TLS 中继"
    "tcp" -> "TCP 中继"
    else -> if (side == "remote") "对端中继 · 接入协议未上报" else "中继连接 · 接入协议待确认"
}
fun relayPathLabel(p: PeerSnapshot): String = when (p.relaySide) {
    "remote" -> relayAccessLabel(p.remoteRelayProtocol, "remote")
    "both" -> if (p.localRelayProtocol.ifBlank { p.relayProtocol }.isNotBlank() && p.remoteRelayProtocol.isNotBlank() && p.localRelayProtocol.ifBlank { p.relayProtocol } != p.remoteRelayProtocol)
        "${relayAccessLabel(p.localRelayProtocol.ifBlank { p.relayProtocol })} / ${relayAccessLabel(p.remoteRelayProtocol)}"
    else relayAccessLabel(p.localRelayProtocol.ifBlank { p.relayProtocol }.ifBlank { p.remoteRelayProtocol }, "both")
    else -> relayAccessLabel(p.localRelayProtocol.ifBlank { p.relayProtocol }, p.relaySide)
}
fun selectedConnectionLabel(type: String, address: String): String {
    val path = when (type) {
        "relay" -> "中继"
        "host", "srflx", "prflx" -> "直连"
        else -> "路径未确认"
    }
    return "$path · ${address.ifBlank { "探测中" }}"
}
fun peerStateLabel(p: PeerSnapshot): String = when (p.state) {
    "waiting_credentials" -> "等待中继凭据"
    "active" -> "已连接"
    "switching" -> "正在切换，现有路径继续工作"
    "paused" -> "对端暂时离线"
    "error" -> "连接需要处理"
    "reconnecting" -> "正在重试"
    else -> "连接中"
}
fun candidateLabel(type: String): String = when (type) {
    "host" -> "设备本地地址"
    "srflx" -> "STUN 探测的公网映射"
    "prflx" -> "连通性检查发现的地址"
    "relay" -> "TURN 中继地址"
    else -> "尚未选定"
}
fun connectionIssue(code: String?): String = when (code) {
    "signal_auth_failed" -> "协调服务密码不匹配，请检查“信令密码”。"
    "invalid_token" -> "房间密码不匹配，请与对端使用相同的房间密码。"
    "no_network" -> "当前没有可用网络，恢复网络后会自动重试。"
    "no_ipv6" -> "IPv6 直连需要双方具有可用的公网 IPv6；“自动”和“仅 ICE”支持 IPv4 / IPv6 探测与中继，需协调服务及对端支持 ICE。"
    "network_dns_failed" -> "当前网络未能解析服务器地址，请检查 DNS 或切换网络。"
    "address_in_use" -> "本地监听端口已被占用，请修改该服务的端口。"
    "worker_upgrade_required" -> "协调服务尚未支持 ICE，请更新 Worker，或选择“仅 IPv6”并确认双方公网 IPv6 可用。"
    "protocol_mismatch" -> "双方传输协议不一致，请核对“自动”“仅 ICE”或“仅 IPv6”的选择，并检查对端版本。"
    "peer_identity_mismatch" -> "对端身份校验没有通过，请核对设备与协调服务配置。"
    "relay_unavailable", "turn_rate_limited" -> "中继暂未就绪；设备仍会优先尝试直连。"
    "service_unavailable" -> "目标服务未响应，请检查提供端的服务地址、端口及运行状态。"
    "insecure_signal" -> "ICE 默认使用 wss://。本地 ws:// 测试需在连接方式中显式开启。"
    "ice_path_failed" -> "此路径未连通，正在尝试其他直连或中继路径。"
    "invalid_config" -> "当前设置未通过检查，请核对连接方式、映射与地址。"
    else -> "连接暂未就绪，请检查网络、对端和连接设置。"
}
