package dev.hole.app.ui

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ShowChart
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import dev.hole.corebridge.NATSnapshot
import dev.hole.corebridge.PeerSnapshot
import java.time.Duration
import java.time.Instant

internal fun currentNATDisplay(snapshot: NATSnapshot, now: Instant = Instant.now()): NATSnapshot {
    val expired = runCatching { Duration.between(Instant.parse(snapshot.probedAt), now).seconds >= 300 }.getOrDefault(false)
    return if (snapshot.status == "complete" && expired) snapshot.copy(status = "expired") else snapshot
}

internal fun peerNATTone(snapshot: NATSnapshot): String {
    if (snapshot.status != "complete") return "neutral"
    return when (snapshot.type) {
        "open", "full_cone" -> "green"
        "firewall", "restricted_cone", "port_restricted_cone" -> "blue"
        "symmetric", "address_pool" -> when (snapshot.predictability) {
            "incremental", "decremental", "predictable_incremental", "predictable_decremental" -> "yellow"
            "irregular" -> "red"
            else -> "neutral"
        }
        else -> "neutral"
    }
}

@Composable
internal fun PeerDetailsContent(peer: PeerSnapshot) {
    val active = peer.state in setOf("active", "switching")
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
        Surface(shape = CircleShape, color = MaterialTheme.colorScheme.primaryContainer) {
            Icon(Icons.Default.Devices, null, Modifier.padding(14.dp).size(28.dp), tint = MaterialTheme.colorScheme.onPrimaryContainer)
        }
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            SelectionContainer { Text(peer.peerId.ifBlank { "未命名设备" }, style = MaterialTheme.typography.headlineSmall) }
            Text(peerPathLabel(peer), style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
    PeerDetailSection("连接概览", Icons.Default.Hub) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Icon(if (active) Icons.Default.CheckCircle else Icons.Default.Sync, null,
                Modifier.size(20.dp), tint = MaterialTheme.colorScheme.primary)
            Text(peerStateLabel(peer), style = MaterialTheme.typography.titleMedium)
        }
        PeerMetricGrid(listOf(
            PeerMetric("延迟", peer.rttMs.takeUnless { it == "0" }?.let { "$it ms" } ?: "—", Icons.Default.Speed),
            PeerMetric("服务通道", "${peer.activeChannels} / ${peer.mappingCount}", Icons.Default.AccountTree),
        ))
        DetailRow("连接过程", if (peer.state == "active") "线路已建立" else peerPhaseLabel(peer))
    }
    PeerDetailSection(if (isFixedPeer(peer)) "固定入口" else "连接地址", Icons.Default.Route) {
        if (isFixedPeer(peer)) {
            fixedPeerDetails(peer).forEach { (label, value) -> DetailRow(label, value) }
        } else {
            DetailRow("本机连接", selectedConnectionLabel(peer.localType, peer.localAddress))
            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant.copy(alpha = .5f))
            DetailRow("对端连接", selectedConnectionLabel(peer.remoteType, peer.remoteAddress))
        }
    }
    if (!isFixedPeer(peer)) {
        PeerSectionHeading("NAT 与端口映射", Icons.Default.NetworkCheck)
        PeerNATCard("本机 NAT", peer.localNAT, local = true)
        PeerNATCard("对端 NAT", peer.peerNAT, local = false)
        PeerDetailSection("端口映射", Icons.Default.SwapHoriz) {
            SelectionContainer { Text(peer.portMapping.description(), style = MaterialTheme.typography.bodyMedium) }
            if (peer.portMapping.reason.isNotBlank()) DetailRow("映射说明", peer.portMapping.reason)
        }
    }
    PeerDetailSection("传输统计", Icons.AutoMirrored.Filled.ShowChart) {
        PeerMetricGrid(listOf(
            PeerMetric("上行", formatBytes(peer.bytesSent), Icons.Default.NorthEast),
            PeerMetric("下行", formatBytes(peer.bytesReceived), Icons.Default.SouthWest),
            PeerMetric("建连耗时", "${peer.connectMs} ms", Icons.Default.Timer),
            PeerMetric("连接重试", "${peer.retryCount} 次", Icons.Default.Refresh),
            PeerMetric("本地丢弃", "${peer.droppedDatagrams} 个报文", Icons.Default.DeleteOutline),
        ))
    }
    PeerDetailSection("QUIC 统计", Icons.AutoMirrored.Filled.ShowChart,
        info = "丢包率统计最近约 1 分钟的数据，合计按双向包数加权。下行与合计需要有效的对端反馈。\n\n" +
            "速率统计本次连接：峰值取约 1 秒采样的最大值，平均包含空闲时间。数值包含重传和控制开销，不代表线路容量；切换连接后重新统计。") {
        PeerQuicMetric("丢包率", "合计 ${peer.quicLoss?.rateLabel() ?: "暂无反馈"} · 上行 ${peer.sendLoss?.rateLabel() ?: "—"} · 下行 ${peer.receiveLoss?.rateLabel() ?: "暂无反馈"}")
        PeerQuicMetric("峰值速率", peer.bandwidth?.description(peak = true, concise = true) ?: "—")
        PeerQuicMetric("平均速率", peer.bandwidth?.description(peak = false, concise = true) ?: "—")
        Text("丢包率近 1 分钟\n速率本次连接", style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
    if (!isFixedPeer(peer) && (peer.pathType in setOf("relay", "hole") || peer.relayDevice.isNotBlank())) {
        PeerDetailSection("中继信息", Icons.Default.Dns) {
            if (peer.relayDevice.isNotBlank()) {
                DetailRow("Hole 中继设备", peer.relayDevice)
                DetailRow("公布入口", peer.relayEndpoint)
                DetailRow("实际接入地址", peer.relayAccessAddress)
            }
            DetailRow("中继接入", if (peer.pathType == "hole") "Hole · UDP" else relayPathLabel(peer))
            DetailRow("本机顺序", relayOrderLabel(peer.relayOrder))
            DetailRow("对端顺序", relayOrderLabel(peer.peerRelayOrder))
            if (peer.pathType == "relay") DetailRow("凭据有效期", remainingTime(peer.turnExpiresAt))
        }
    }
    val issue = fixedConnectionIssue(peer.errorCode) ?: peer.errorMessage
    if (!issue.isNullOrBlank()) {
        HoleCard(containerColor = MaterialTheme.colorScheme.errorContainer) {
            Row(Modifier.padding(16.dp), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                Icon(Icons.Default.ErrorOutline, null, tint = MaterialTheme.colorScheme.onErrorContainer)
                Text(issue, color = MaterialTheme.colorScheme.onErrorContainer, style = MaterialTheme.typography.bodyMedium)
            }
        }
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun PeerQuicMetric(label: String, summary: String) {
    Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(5.dp)) {
        Text(label, style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
        SelectionContainer {
            FlowRow(horizontalArrangement = Arrangement.spacedBy(16.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                summary.split(" · ").forEach { Text(it, style = MaterialTheme.typography.bodyMedium) }
            }
        }
    }
}

@Composable
private fun PeerSectionHeading(title: String, icon: ImageVector, info: String? = null) {
    Row(Modifier.padding(horizontal = 4.dp).semantics { heading() }, verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        Icon(icon, null, Modifier.size(20.dp), tint = MaterialTheme.colorScheme.primary)
        Text(title, style = MaterialTheme.typography.titleSmall, color = MaterialTheme.colorScheme.primary,
            modifier = Modifier.weight(1f))
        if (info != null) HoleInfoButton(title) { Text(info) }
    }
}

@Composable
private fun PeerDetailSection(title: String, icon: ImageVector, info: String? = null, content: @Composable ColumnScope.() -> Unit) {
    Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
        PeerSectionHeading(title, icon, info)
        HoleCard(Modifier.fillMaxWidth()) {
            Column(Modifier.fillMaxWidth().padding(16.dp), verticalArrangement = Arrangement.spacedBy(14.dp), content = content)
        }
    }
}

@Composable
private fun PeerNATCard(title: String, snapshot: NATSnapshot, local: Boolean) {
    val current = currentNATDisplay(snapshot)
    val tone = peerNATTone(current)
    val colors = natColors(tone)
    HoleCard(Modifier.fillMaxWidth().testTag(if (local) "peer-nat-local" else "peer-nat-remote"), containerColor = colors.surface) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Icon(if (local) Icons.Default.PhoneAndroid else Icons.Default.DevicesOther, null, Modifier.size(20.dp), tint = colors.ink)
                Text(title, style = MaterialTheme.typography.labelLarge, color = colors.ink, modifier = Modifier.weight(1f))
                if (current.reason.isNotBlank()) HoleInfoButton(title) { Text(current.reason) }
            }
            SelectionContainer { Text(current.description(local), style = MaterialTheme.typography.titleMedium, color = colors.ink) }
            Text("${if (local) "本机探测时间" else "对端探测时间"}：${current.probedAtDisplay.ifBlank { "暂无" }}",
                style = MaterialTheme.typography.bodySmall, color = colors.ink)
        }
    }
}

private data class PeerMetric(val label: String, val value: String, val icon: ImageVector)

@Composable
private fun PeerMetricGrid(metrics: List<PeerMetric>) {
    BoxWithConstraints(Modifier.fillMaxWidth()) {
        val columns = if (maxWidth.value >= 260 * LocalDensity.current.fontScale) 2 else 1
        Column(verticalArrangement = Arrangement.spacedBy(14.dp)) {
            metrics.chunked(columns).forEach { row ->
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    row.forEach { metric ->
                        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(5.dp)) {
                            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                                Icon(metric.icon, null, Modifier.size(16.dp), tint = MaterialTheme.colorScheme.onSurfaceVariant)
                                Text(metric.label, style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                            }
                            SelectionContainer { Text(metric.value, style = MaterialTheme.typography.titleMedium) }
                        }
                    }
                }
            }
        }
    }
}
