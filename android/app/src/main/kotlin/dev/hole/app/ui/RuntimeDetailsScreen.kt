package dev.hole.app.ui

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import dev.hole.app.BuildConfig
import dev.hole.app.ConfigUiState
import dev.hole.corebridge.CoreSnapshot
import dev.hole.corebridge.MappingSnapshot
import dev.hole.corebridge.PeerSnapshot
import java.time.Duration
import java.time.Instant

@OptIn(ExperimentalMaterial3Api::class, ExperimentalLayoutApi::class)
@Composable
fun RuntimeDetailsScreen(
    snapshot: CoreSnapshot, config: ConfigUiState, commandError: String?,
    onBack: () -> Unit, onReconnect: () -> Unit, onExport: () -> Unit, onBackground: () -> Unit,
    snackbarHostState: SnackbarHostState? = null, onTransportSettings: () -> Unit = {},
) {
    var tab by rememberSaveable { mutableStateOf("devices") }
    var query by rememberSaveable { mutableStateOf("") }
    var filter by rememberSaveable { mutableStateOf("all") }
    var selectedPeer by rememberSaveable { mutableStateOf<String?>(null) }
    var selectedMapping by rememberSaveable { mutableStateOf<String?>(null) }
    var diagnostics by rememberSaveable { mutableStateOf(false) }
    var menu by remember { mutableStateOf(false) }
    val floatingInset = LocalFloatingInset.current
    val peers = snapshot.peers.filter {
        (filter == "all" || it.pathType == filter) &&
            listOf(it.peerId, it.localAddress, it.remoteAddress, peerPathLabel(it)).any { value -> value.contains(query, ignoreCase = true) }
    }
    val mappings = snapshot.mappings.filter {
        (filter == "all" || it.protocol == filter) &&
            listOf(it.id, it.endpoint, it.peer).any { value -> value.contains(query, ignoreCase = true) }
    }
    HoleScaffold(title = "连接", actions = {
        HoleIconButton(onReconnect) { Icon(Icons.Default.Refresh, "重选路径") }
        Box {
            HoleIconButton({ menu = true }) { Icon(Icons.Default.MoreVert, "连接选项") }
            DropdownMenu(menu, { menu = false }) {
                DropdownMenuItem(text = { Text("网络诊断") }, onClick = { menu = false; diagnostics = true })
                DropdownMenuItem(text = { Text("连接方式") }, onClick = { menu = false; onTransportSettings() })
                DropdownMenuItem(text = { Text("导出报告") }, onClick = { menu = false; onExport() })
            }
        }
    }, snackbarHost = { snackbarHostState?.let { HoleSnackbarHost(it) } }) { insets ->
        LazyColumn(
            Modifier.fillMaxSize().padding(insets).testTag("connection-details"),
            contentPadding = PaddingValues(start = 20.dp, end = 20.dp, top = 12.dp, bottom = floatingInset + 20.dp),
            verticalArrangement = Arrangement.spacedBy(14.dp),
        ) {
            item {
                HoleSingleChoice(
                    listOf("devices" to "设备 ${snapshot.peers.size}", "services" to "服务 ${snapshot.mappings.size}"),
                    tab, { tab = it; filter = "all" },
                )
            }
            item {
                TextField(query, { query = it }, Modifier.fillMaxWidth(),
                    placeholder = { Text(if (tab == "devices") "搜索设备或地址" else "搜索服务或端口") },
                    leadingIcon = { Icon(Icons.Default.Search, null) },
                    trailingIcon = if (query.isNotEmpty()) ({ IconButton({ query = "" }) { Icon(Icons.Default.Close, "清除搜索") } }) else null,
                    singleLine = true, shape = CircleShape,
                    colors = TextFieldDefaults.colors(
                        focusedContainerColor = MaterialTheme.colorScheme.surfaceContainerLow,
                        unfocusedContainerColor = MaterialTheme.colorScheme.surfaceContainerLow,
                        focusedIndicatorColor = androidx.compose.ui.graphics.Color.Transparent,
                        unfocusedIndicatorColor = androidx.compose.ui.graphics.Color.Transparent,
                    ),
                )
            }
            item {
                FlowRow(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    val filters = if (tab == "devices") listOf("all" to "全部", "direct" to "直连", "relay" to "中继")
                        else listOf("all" to "全部", "tcp" to "TCP", "udp" to "UDP")
                    filters.forEach { (value, label) ->
                        FilterChip(selected = filter == value, onClick = { filter = value }, label = { Text(label) }, shape = CircleShape)
                    }
                }
            }
            item {
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                    Text(if (tab == "devices") "${peers.size} 台设备" else "${mappings.size} 项服务",
                        style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    Text("TCP ${snapshot.tcpSessions}  ·  UDP ${snapshot.udpSessions}",
                        style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
            listOfNotNull(commandError, snapshot.errorMessage).distinct().forEach { error ->
                item { Text(error, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.error) }
            }
            if ((tab == "devices" && peers.isEmpty()) || (tab == "services" && mappings.isEmpty())) item {
                Column(Modifier.fillMaxWidth().padding(vertical = 44.dp), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    Icon(Icons.Default.Search, null, Modifier.size(36.dp), tint = MaterialTheme.colorScheme.outline)
                    Text(if (query.isNotBlank() || filter != "all") "没有匹配的连接" else if (!snapshot.runRequested) "连接已停止" else "等待对端连接",
                        style = MaterialTheme.typography.titleMedium)
                    if (tab == "devices" && config.config.connection.connectionMode == "legacy") Text("IPv6 线路显示在服务中", color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
            if (tab == "devices") items(peers, key = { it.transportId }) { peer ->
                PeerConnectionRow(peer, Modifier.animateItem()) { selectedPeer = peer.transportId }
            } else items(mappings, key = { "${it.role}/${it.id}" }) { mapping ->
                ServiceConnectionRow(mapping, Modifier.animateItem()) { selectedMapping = "${mapping.role}/${mapping.id}" }
            }
        }
    }
    selectedPeer?.let { id ->
        InspectionSheet("设备连接", { selectedPeer = null }) {
            val peer = snapshot.peers.firstOrNull { it.transportId == id }
            if (peer == null) Text("连接已结束") else {
                Text(peer.peerId, style = MaterialTheme.typography.headlineSmall)
                InspectionValue("线路", peerPathLabel(peer))
                InspectionValue("状态", peerStateLabel(peer))
                InspectionValue("延迟", "${peer.rttMs} ms")
                InspectionValue("连接过程", peerPhaseLabel(peer))
                DetailRow("本机连接", selectedConnectionLabel(peer.localType, peer.localAddress))
                DetailRow("对端连接", selectedConnectionLabel(peer.remoteType, peer.remoteAddress))
                InspectionValue("上行 / 下行", "${formatBytes(peer.bytesSent)} / ${formatBytes(peer.bytesReceived)}")
                InspectionValue("服务通道", "${peer.activeChannels} / ${peer.mappingCount}")
                InspectionValue("建连耗时", "${peer.connectMs} ms")
                InspectionValue("重试 / 丢包", "${peer.retryCount} / ${peer.droppedDatagrams}")
                if (peer.pathType == "relay") {
                    InspectionValue("中继接入", relayPathLabel(peer))
                    InspectionValue("本机顺序", relayOrderLabel(peer.relayOrder))
                    InspectionValue("对端顺序", relayOrderLabel(peer.peerRelayOrder))
                    InspectionValue("凭据有效期", remainingTime(peer.turnExpiresAt))
                }
                peer.errorMessage?.let { Text(it, color = MaterialTheme.colorScheme.error) }
            }
        }
    }
    selectedMapping?.let { id ->
        InspectionSheet("服务连接", { selectedMapping = null }) {
            val mapping = snapshot.mappings.firstOrNull { "${it.role}/${it.id}" == id }
            if (mapping == null) Text("连接已结束") else {
                Text(mapping.id, style = MaterialTheme.typography.headlineSmall)
                InspectionValue("状态", mappingLabel(mapping.state))
                DetailRow("监听地址", mapping.endpoint.ifBlank { "等待分配" })
                DetailRow("对端", mapping.peer.ifBlank { "等待匹配" })
                DetailRow("线路", mapping.path.ifBlank { "共享设备线路" })
                InspectionValue("TCP / UDP", "${mapping.tcpSessions} / ${mapping.udpSessions}")
                InspectionValue("上行 / 下行", "${formatBytes(mapping.readBytes)} / ${formatBytes(mapping.writtenBytes)}")
                InspectionValue("待确认数据", formatBytes(mapping.replayBytes))
                mapping.error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
            }
        }
    }
    if (diagnostics) InspectionSheet("网络诊断", { diagnostics = false }) {
        InspectionValue("核心", engineLabel(snapshot))
        InspectionValue("房间", signalLabel(snapshot.signalState))
        InspectionValue("网络", snapshot.network.transport)
        InspectionValue("互联网", if (snapshot.network.validated) "已验证" else "未验证")
        DetailRow("网卡", snapshot.network.interfaceName.ifBlank { "系统选择" })
        DetailRow("地址", snapshot.network.addresses.joinToString("\n").ifBlank { "暂无" })
        DetailRow("DNS", snapshot.network.dns.joinToString("\n").ifBlank { "系统提供" })
        InspectionValue("切网 / 重连", "${snapshot.networkChanges} / ${snapshot.reconnects}")
        InspectionValue("会话保留", config.config.connection.sessionTimeout)
        InspectionValue("运行 / 网络重建编号", "${snapshot.generation} / ${snapshot.transportGeneration}")
        InspectionValue("客户端", BuildConfig.VERSION_NAME)
        DetailRow("核心版本", snapshot.coreVersion.ifBlank { "加载中" })
        HoleButton("导出报告", onExport, Modifier.fillMaxWidth())
        HoleTextButton("后台运行设置", onBackground)
    }
}

@Composable
private fun PeerConnectionRow(peer: PeerSnapshot, modifier: Modifier, onClick: () -> Unit) {
    RaisedPanel(modifier.fillMaxWidth().clickable(onClickLabel = "查看 ${peer.peerId}", onClick = onClick)) {
        Column(Modifier.padding(18.dp), verticalArrangement = Arrangement.spacedBy(14.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                Surface(shape = CircleShape, color = MaterialTheme.colorScheme.primaryContainer) {
                    Icon(Icons.Default.Home, null, Modifier.padding(10.dp).size(22.dp), tint = MaterialTheme.colorScheme.onPrimaryContainer)
                }
                Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(3.dp)) {
                    Text(peer.peerId.ifBlank { "未命名设备" }, style = MaterialTheme.typography.titleMedium)
                    Text(peerPathLabel(peer), style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                Text(peer.rttMs.takeUnless { it == "0" }?.let { "$it ms" } ?: "—", color = MaterialTheme.colorScheme.primary, style = MaterialTheme.typography.titleMedium)
            }
            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant.copy(alpha = .4f))
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                Text("↑ ${formatBytes(peer.bytesSent)}", style = MaterialTheme.typography.bodyMedium)
                Text("↓ ${formatBytes(peer.bytesReceived)}", style = MaterialTheme.typography.bodyMedium)
                Text(peerStateLabel(peer), style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.primary)
            }
        }
    }
}

@Composable
private fun ServiceConnectionRow(mapping: MappingSnapshot, modifier: Modifier, onClick: () -> Unit) {
    RaisedPanel(modifier.fillMaxWidth().clickable(onClickLabel = "查看 ${mapping.id}", onClick = onClick)) {
        Column(Modifier.padding(18.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                Text(mapping.id, style = MaterialTheme.typography.titleMedium)
                Text(mapping.protocol.uppercase(), color = MaterialTheme.colorScheme.primary, style = MaterialTheme.typography.labelLarge)
            }
            Text(mapping.endpoint.ifBlank { "等待匹配" }, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Text("${mappingLabel(mapping.state)} · ${mapping.tcpSessions + mapping.udpSessions} 个会话", style = MaterialTheme.typography.labelMedium)
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun InspectionSheet(title: String, onDismiss: () -> Unit, content: @Composable ColumnScope.() -> Unit) {
    ModalBottomSheet(onDismissRequest = onDismiss, sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)) {
        Column(Modifier.fillMaxWidth().verticalScroll(rememberScrollState()).padding(horizontal = 24.dp).padding(bottom = 32.dp),
            verticalArrangement = Arrangement.spacedBy(18.dp)) {
            Text(title, style = MaterialTheme.typography.titleLarge)
            content()
        }
    }
}

@Composable
private fun InspectionValue(label: String, value: String) {
    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(16.dp)) {
        Text(label, Modifier.weight(1f), color = MaterialTheme.colorScheme.onSurfaceVariant, style = MaterialTheme.typography.bodyMedium)
        SelectionContainer(Modifier.weight(1.4f)) { Text(value, style = MaterialTheme.typography.bodyMedium, textAlign = TextAlign.End) }
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
