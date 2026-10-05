package dev.hole.app.ui

import androidx.compose.animation.AnimatedContent
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.dp
import dev.hole.app.ConfigUiState
import dev.hole.corebridge.CoreSnapshot

@Composable
fun HomeScreen(
    snapshot: CoreSnapshot,
    configState: ConfigUiState,
    commandError: String?,
    snackbarHostState: SnackbarHostState? = null,
    onOpenSettings: () -> Unit,
    onAddProvide: () -> Unit,
    onEditProvide: (String) -> Unit,
    onAddConsume: () -> Unit,
    onEditConsume: (String) -> Unit,
    onToggleProvide: (String, Boolean) -> Unit,
    onToggleConsume: (String, Boolean) -> Unit,
    onDeleteProvide: (String) -> Unit,
    onDeleteConsume: (String) -> Unit,
    onToggleRun: (Boolean) -> Unit,
    modifier: Modifier = Modifier,
    onOpenDetails: () -> Unit = {},
    onNavigate: (String) -> Unit = {},
) {
    val floatingInset = LocalFloatingInset.current
    HoleScaffold(title = "hole", modifier = modifier, actions = { HoleSettingsButton(onOpenSettings) },
        snackbarHost = { snackbarHostState?.let { HoleSnackbarHost(it) } }) { insets ->
        LazyColumn(Modifier.fillMaxSize().padding(insets).testTag("dashboard"),
            contentPadding = PaddingValues(start = 16.dp, end = 16.dp, top = 16.dp, bottom = floatingInset + 16.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
            item { HomeConnectionStatus(snapshot) }
            item { DashboardPair("TCP 会话", snapshot.tcpSessions.toString(), "UDP 会话", snapshot.udpSessions.toString()) }
            item { DashboardPair(
                "上行流量", formatBytes(snapshot.peers.sumOf { it.bytesSent.toLongOrNull() ?: 0L }.toString()),
                "下行流量", formatBytes(snapshot.peers.sumOf { it.bytesReceived.toLongOrNull() ?: 0L }.toString()),
            ) }
            item {
                HoleSettingsGroup("当前连接") {
                    DashboardLine("房间", configState.config.connection.room.ifBlank { "未设置" })
                    HorizontalDivider(color = MaterialTheme.colorScheme.surfaceContainerHighest)
                    DashboardLine("设备", configState.config.connection.deviceName.ifBlank { "未设置" })
                    HorizontalDivider(color = MaterialTheme.colorScheme.surfaceContainerHighest)
                    DashboardLine("连接方式", connectionModeLabel(configState.config.connection.connectionMode))
                }
            }
            item {
                HoleSettingsGroup("运行状态") {
                    DashboardLine("核心", engineLabel(snapshot))
                    DashboardLine("设备线路", snapshot.peers.size.toString())
                    DashboardLine("可用服务", snapshot.mappings.count { it.state == "active" }.toString())
                    DashboardLine("网络", snapshot.network.transport.takeUnless { it == "none" || it.isBlank() } ?: "未连接")
                }
            }
            if (configState.config.connection.serverUrl.isBlank()) item {
                HoleButton("配置连接", { onNavigate("connection-settings") }, Modifier.fillMaxWidth())
            }
            listOfNotNull(snapshot.errorMessage, configState.lastError, commandError).distinct().forEach { error ->
                item { Text(error, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodyMedium) }
            }
        }
    }
}

@Composable
private fun DashboardPair(first: String, firstValue: String, second: String, secondValue: String) {
    BoxWithConstraints {
        if (maxWidth < 280.dp || androidx.compose.ui.platform.LocalDensity.current.fontScale > 1.4f) {
            Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                DashboardMetric(first, firstValue, Modifier.fillMaxWidth())
                DashboardMetric(second, secondValue, Modifier.fillMaxWidth())
            }
        } else {
            Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                DashboardMetric(first, firstValue, Modifier.weight(1f))
                DashboardMetric(second, secondValue, Modifier.weight(1f))
            }
        }
    }
}

@Composable
private fun DashboardMetric(title: String, value: String, modifier: Modifier) {
    HoleCard(modifier) {
        Column(Modifier.padding(18.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Text(title, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
            AnimatedMetric(value)
        }
    }
}

@Composable
private fun DashboardLine(title: String, value: String) {
    Row(Modifier.fillMaxWidth().heightIn(min = 36.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(16.dp)) {
        Text(title, Modifier.weight(1f), style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Text(value, Modifier.weight(1.2f), style = MaterialTheme.typography.bodyLarge, textAlign = androidx.compose.ui.text.style.TextAlign.End)
    }
}

fun engineLabel(snapshot: CoreSnapshot): String = when (snapshot.engineState) {
    "loading" -> "正在加载"
    "stopped" -> if (snapshot.configured) "已停止" else "未配置"
    "starting" -> "正在启动"
    "running" -> "运行中"
    "stopping" -> "正在停止"
    "reconfiguring" -> "应用配置中"
    "recovering" -> "等待网络"
    "error" -> "核心异常"
    else -> snapshot.engineState
}

fun signalLabel(state: String): String = when (state) {
    "disconnected" -> "未连接"
    "connecting" -> "连接中"
    "joining" -> "加入房间中"
    "joined" -> "已加入"
    "reconnecting" -> "等待重连"
    "error" -> "连接异常"
    else -> state
}
