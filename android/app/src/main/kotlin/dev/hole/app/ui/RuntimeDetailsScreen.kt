package dev.hole.app.ui

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.animateContentSize
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import dev.hole.app.BuildConfig
import dev.hole.app.ConfigUiState
import dev.hole.corebridge.CoreSnapshot
import dev.hole.corebridge.MappingSnapshot
import dev.hole.corebridge.PeerSnapshot
import java.time.Duration
import java.time.Instant
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.flow

@Composable
fun RuntimeDetailsScreen(
    snapshot: CoreSnapshot, config: ConfigUiState, commandError: String?,
    onBack: () -> Unit, onReconnect: () -> Unit, onExport: () -> Unit, onBackground: () -> Unit,
    snackbarHostState: SnackbarHostState? = null, onTransportSettings: () -> Unit = {},
) {
    val uptimeFlow = remember(snapshot.startedAt, snapshot.runRequested) {
        flow {
            while (true) {
                emit(elapsedLabel(snapshot.startedAt, snapshot.runRequested))
                if (!snapshot.runRequested) break
                delay(1000)
            }
        }
    }
    val uptime by uptimeFlow.collectAsStateWithLifecycle(initialValue = elapsedLabel(snapshot.startedAt, snapshot.runRequested))
    val active = snapshot.mappings.count { it.state == "active" }
    HoleScaffold(title = "连接详情", navigationIcon = { HoleBackButton(onBack) },
        snackbarHost = { snackbarHostState?.let { HoleSnackbarHost(it) } }) { insets ->
        LazyColumn(Modifier.fillMaxWidth().padding(insets).padding(horizontal = 16.dp).testTag("connection-details"),
            verticalArrangement = Arrangement.spacedBy(14.dp)) {
            item {
                HoleCard(Modifier.fillMaxWidth(), containerColor = MaterialTheme.colorScheme.secondaryContainer) {
                    Column(Modifier.padding(20.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                        Text(connectionTitle(snapshot), style = MaterialTheme.typography.headlineSmall)
                        Text(if (snapshot.runRequested) "当前 $active / ${snapshot.mappings.size} 个服务通道可用。退出此页面不会停止转发。" else "设置与服务条目仍保留，开启首页开关后重新连接。",
                            style = MaterialTheme.typography.bodyMedium)
                        CompactDetail("运行时间", uptime)
                        CompactDetail("业务连接", "TCP ${snapshot.tcpSessions} · UDP ${snapshot.udpSessions}")
                        snapshot.errorCode?.let { Text(connectionIssue(it), color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodyMedium) }
                        commandError?.let { Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall) }
                        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                            HoleButton("重选路径", onReconnect, enabled = snapshot.runRequested, secondary = true)
                            HoleTextButton("连接方式", onTransportSettings)
                        }
                    }
                }
            }
            item { HoleSectionTitle("设备与线路") }
            if (snapshot.peers.isEmpty()) item {
                DetailCard {
                    val ipv6 = config.config.connection.connectionMode == "legacy" || snapshot.mappings.any { it.profile == "legacy-ipv6-quic-v2" }
                    Text(when {
                        !snapshot.runRequested -> "连接尚未开启"
                        ipv6 && snapshot.mappings.any { it.state == "active" } -> "正在使用 IPv6 直连"
                        ipv6 -> "等待 IPv6 对端连接"
                        else -> "暂无已配对的 ICE 设备"
                    }, style = MaterialTheme.typography.titleMedium)
                    Text(if (ipv6) "IPv6 直连的对端与路径地址见下方服务明细；此方式不使用 ICE 或 TURN 中继。"
                        else if (snapshot.runRequested) "双方使用相同房间，并启用匹配的提供 / 使用服务后，这里会显示直连或中继、延迟与流量。"
                        else "连接开启后显示每台对端设备实际使用的线路。", style = MaterialTheme.typography.bodyMedium)
                }
            }
            items(snapshot.peers, key = { "peer/${it.transportId}" }) { peer -> PeerDetail(peer) }
            item { HoleSectionTitle("服务明细 · ${snapshot.mappings.size}") }
            if (snapshot.mappings.isEmpty()) item {
                Text("尚未启用服务。可以返回首页添加提供服务或使用服务。", style = MaterialTheme.typography.bodyMedium)
            }
            items(snapshot.mappings, key = { "mapping/${it.role}/${it.id}" }) { mapping -> MappingDetail(mapping) }
            item {
                DetailCard {
                    Text("设置与恢复", style = MaterialTheme.typography.titleMedium)
                    Text(configurationText(snapshot, config), color = MaterialTheme.colorScheme.primary, style = MaterialTheme.typography.bodyLarge)
                    CompactDetail("协调服务", signalLabel(snapshot.signalState))
                    CompactDetail("恢复记录", "切网 ${snapshot.networkChanges} 次 · 协调重连 ${snapshot.reconnects} 次")
                    CompactDetail("断线保留", config.config.connection.sessionTimeout)
                    Text("底层路径断开时，已有 TCP 会话在保留期限内尝试续接；正常转发不会到点停止。UDP 期间可能丢包。删除服务、变更身份或停止运行会结束对应会话。",
                        style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
            item {
                DetailCard {
                    Text("本机网络与后台", style = MaterialTheme.typography.titleMedium)
                    CompactDetail("网络", snapshot.network.transport.takeUnless { it == "none" || it.isBlank() } ?: "等待可用网络")
                    CompactDetail("网络状态", when { !snapshot.runRequested -> "显示最近使用的网络"; snapshot.network.validated -> "系统已确认互联网可用"; snapshot.network.available -> "网络已连接，等待互联网验证"; else -> "尚无可用网络" })
                    CompactDetail("连接策略", connectionPolicyLabel(config.config.connection.connectionMode))
                    Disclosure("网络地址与探测服务", "收起网络信息") {
                        DetailRow("网卡", snapshot.network.interfaceName.ifEmpty { "由系统选择" })
                        DetailRow("本机地址", snapshot.network.addresses.joinToString("\n").ifEmpty { "暂无地址" })
                        DetailRow("DNS", snapshot.network.dns.joinToString("\n").ifEmpty { "由当前网络提供" })
                        val ipv6Only = config.config.connection.connectionMode == "legacy"
                        DetailRow("STUN", if (ipv6Only) "未使用（仅 IPv6 模式）" else config.config.connection.ice.stunUrls.joinToString("\n").ifEmpty { "未启用，仅探测本地地址" })
                        Text(if (ipv6Only) "当前转发路径仅使用公网 IPv6；STUN 与 TURN 设置不会用于此模式。"
                            else "STUN 用于发现可直连的公网映射，不转发业务数据。ICE 同时尝试 IPv4 与 IPv6；直连不通时按设置尝试中继。", style = MaterialTheme.typography.bodySmall)
                        CompactDetail("计费网络", if (snapshot.network.metered) "是" else "否")
                        CompactDetail("网络绑定", if (snapshot.networkBinding) "外部连接与 DNS 跟随所选网络" else "使用系统网络")
                    }
                    HoleTextButton("后台保持设置", onBackground)
                }
            }
            item {
                DetailCard {
                    Text("连接报告", style = MaterialTheme.typography.titleMedium)
                    Text("导出当前状态、线路和服务统计，便于排查问题。不包含密码、TURN 凭据或 Go 原始日志。", style = MaterialTheme.typography.bodyMedium)
                    HoleButton("导出连接报告", onExport, secondary = true)
                    Disclosure("版本与技术信息", "收起技术信息") {
                        CompactDetail("客户端", "${BuildConfig.VERSION_NAME} (${BuildConfig.VERSION_CODE})")
                        DetailRow("共享组件", snapshot.coreVersion.ifEmpty { "加载中" })
                        CompactDetail("运行 / 网络重建编号", "${snapshot.generation} / ${snapshot.transportGeneration}")
                        CompactDetail("桥接 / 会话协议", "${snapshot.apiVersion} / ${snapshot.sessionProtocol}")
                        snapshot.errorMessage?.let { DetailRow("当前错误详情", "${snapshot.errorCode}: $it") }
                    }
                }
            }
            item { Text("统计来自当前连接状态；线路流量包含协议开销，不作为中继账单。", Modifier.padding(bottom = 28.dp), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
        }
    }
}

@Composable
private fun PeerDetail(peer: PeerSnapshot) {
    DetailCard {
        Text(peer.peerId, style = MaterialTheme.typography.titleLarge)
        Text(peerStateLabel(peer), style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.primary)
        Text(peerPathLabel(peer), style = MaterialTheme.typography.bodyLarge)
        CompactDetail("共享服务", "${peer.activeChannels} / ${peer.mappingCount}")
        CompactDetail("往返延迟", peer.rttMs.takeUnless { it == "0" }?.let { "$it ms" } ?: "正在测量")
        CompactDetail("线路流量 ↑ / ↓", "${formatBytes(peer.bytesSent)} / ${formatBytes(peer.bytesReceived)}")
        if (peer.pendingPhase.isNotEmpty()) Text("同时${phaseLabel(peer.pendingPhase)}，新路径就绪后切换。", style = MaterialTheme.typography.bodySmall)
        if (peer.errorCode != null && peer.state !in setOf("active", "switching")) Text(connectionIssue(peer.errorCode), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
        Disclosure("查看线路细节", "收起线路细节") {
            CompactDetail("连接过程", peerPhaseLabel(peer))
            CompactDetail("中继顺序", if (peer.relayPolicy == "udp-tcp-tls-v1") "UDP → TCP（80、3478）→ TLS（443、5349）" else "兼容阶段 · 对端或 Worker 尚未支持新顺序")
            if (peer.pathType == "relay") {
                CompactDetail("中继使用方", when (peer.relaySide) { "both" -> "本机与对端"; "local" -> "本机"; "remote" -> "对端"; else -> "等待确认" })
                CompactDetail("中继接入", relayPathLabel(peer))
                Text("接入类型按实际选中的 TURN 连接显示，不按候选地址端口猜测；仅对端使用中继时，其接入协议可能未上报。", style = MaterialTheme.typography.bodySmall)
            }
            DetailRow("本机连接", selectedConnectionLabel(peer.localType, peer.localAddress))
            DetailRow("对端连接", selectedConnectionLabel(peer.remoteType, peer.remoteAddress))
            CompactDetail("已发现候选地址", "本机 ${peer.localCandidates} · 对端 ${peer.remoteCandidates}")
            CompactDetail("建连耗时", peer.connectMs.takeUnless { it == "0" }?.let { "$it ms" } ?: "尚未完成")
            CompactDetail("路径重试", "${peer.retryCount} 次")
            CompactDetail("丢弃的 UDP 报文", peer.droppedDatagrams)
            CompactDetail("本机中继来源", when (peer.relayState) { "ready" -> "短期凭据已就绪"; "manual" -> "手动配置"; "off" -> "不申请本机中继"; "unavailable", "expired" -> "暂不可用，直连仍可使用"; else -> "准备中" })
            if (peer.turnExpiresAt != "0") CompactDetail("中继凭据剩余时间", remainingTime(peer.turnExpiresAt))
            CompactDetail("授权确认剩余时间", remainingTime(peer.leaseUntil))
            DetailRow("传输标识", "${peer.transportId} · 第 ${peer.generation} 次路径")
        }
    }
}

@Composable
private fun MappingDetail(mapping: MappingSnapshot) {
    DetailCard {
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            Text(mapping.id, style = MaterialTheme.typography.titleMedium, modifier = Modifier.weight(1f))
            Text(mappingLabel(mapping.state), style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.primary)
        }
        Text("${if (mapping.role == "provide") "提供服务" else "使用服务"} · ${mapping.protocol.uppercase().ifBlank { "等待匹配协议" }}", style = MaterialTheme.typography.labelLarge)
        Text(mapping.endpoint.ifBlank { "等待分配监听地址" }, style = MaterialTheme.typography.bodyMedium)
        CompactDetail("业务连接", "TCP ${mapping.tcpSessions} · UDP ${mapping.udpSessions}")
        Disclosure("服务地址与数据详情", "收起服务详情") {
            CompactDetail("传输方式", when (mapping.profile) { "legacy-ipv6-quic-v2" -> "IPv6 直连"; "ice-quic-mux-v1" -> "ICE · 多服务共享 QUIC"; else -> "等待协商" })
            DetailRow("对端设备", mapping.peer.ifBlank { "尚未匹配" })
            DetailRow("线路地址", mapping.path.ifBlank { "由上方设备线路统一承载" })
            CompactDetail("TCP 应用上行 / 下行", "${formatBytes(mapping.tcpReadBytes)} / ${formatBytes(mapping.tcpWrittenBytes)}")
            CompactDetail("TCP 待确认数据", formatBytes(mapping.replayBytes))
            Text("TCP 统计当前存活的业务连接和应用数据；UDP 按本地来源计数。确认前的 TCP 缓存可用于断线续接。", style = MaterialTheme.typography.bodySmall)
            mapping.error?.let { DetailRow("当前服务提示", it) }
        }
    }
}

@Composable
private fun DetailCard(content: @Composable () -> Unit) {
    HoleCard(Modifier.fillMaxWidth().animateContentSize()) {
        Column(Modifier.padding(18.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) { content() }
    }
}
@Composable
private fun Disclosure(openLabel: String, closeLabel: String, content: @Composable () -> Unit) {
    var expanded by rememberSaveable { mutableStateOf(false) }
    HoleTextButton(if (expanded) closeLabel else openLabel, { expanded = !expanded })
    AnimatedVisibility(expanded) { Column(verticalArrangement = Arrangement.spacedBy(10.dp)) { content() } }
}
@Composable
private fun CompactDetail(label: String, value: String) {
    if (LocalDensity.current.fontScale > 1.3f) {
        Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Text(label, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Text(value, style = MaterialTheme.typography.bodyMedium)
        }
        return
    }
    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(16.dp)) {
        Text(label, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.weight(0.9f))
        Text(value, style = MaterialTheme.typography.bodyMedium, textAlign = TextAlign.End, modifier = Modifier.weight(1.1f))
    }
}
@Composable
fun DetailRow(label: String, value: String) {
    Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(3.dp)) {
        Text(label, style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
        SelectionContainer { Text(value, style = MaterialTheme.typography.bodyMedium) }
    }
}
fun mappingLabel(state: String): String = when (state) {
    "active" -> "通道可用"
    "waiting_peer" -> "等待对端"
    "connecting" -> "连接中"
    "stopped" -> "已停止"
    "error" -> "需要检查"
    else -> "准备中"
}
private fun elapsedLabel(startedAt: String, running: Boolean): String {
    if (!running) return "已停止"
    if (startedAt.isBlank()) return "启动中"
    return runCatching {
        val seconds = Duration.between(Instant.parse(startedAt), Instant.now()).seconds.coerceAtLeast(0)
        if (seconds >= 3600) "${seconds / 3600} 小时 ${seconds / 60 % 60} 分" else "${seconds / 60} 分 ${seconds % 60} 秒"
    }.getOrDefault("运行中")
}
private fun remainingTime(value: String): String {
    val seconds = ((value.toLongOrNull() ?: 0) - System.currentTimeMillis()) / 1000
    return if (seconds <= 0) "等待刷新" else if (seconds >= 3600) "约 ${seconds / 3600} 小时" else "约 ${(seconds + 59) / 60} 分钟"
}
fun formatBytes(value: String): String {
    val size = value.toDoubleOrNull() ?: return value
    return when {
        size >= 1024 * 1024 * 1024 -> "%.2f GiB".format(size / 1024 / 1024 / 1024)
        size >= 1024 * 1024 -> "%.2f MiB".format(size / 1024 / 1024)
        size >= 1024 -> "%.1f KiB".format(size / 1024)
        else -> "$value B"
    }
}
