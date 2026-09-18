package dev.hole.app.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.clickable
import androidx.compose.animation.animateContentSize
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import androidx.compose.ui.text.style.TextAlign
import dev.hole.app.ConfigUiState
import dev.hole.app.config.ConsumeEntry
import dev.hole.app.config.ProvideEntry
import dev.hole.app.config.ThemeStyle
import dev.hole.app.config.composeExpose
import dev.hole.app.config.composeServiceUrl
import dev.hole.corebridge.CoreSnapshot

@Composable
fun HomeScreen(
    snapshot: CoreSnapshot,
    configState: ConfigUiState,
    commandError: String?,
    snackbarHostState: SnackbarHostState,
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
) {
    val listState = rememberLazyListState()
    val mappingStates = remember(snapshot.mappings) { snapshot.mappings.associate { (it.role to it.id) to it.state } }
    val miuix = LocalThemeStyle.current == ThemeStyle.MIUIX
    HoleScaffold(
        title = "hole",
        largeTitle = true,
        modifier = modifier,
        actions = {
            HoleSettingsButton(onClick = onOpenSettings)
        },
        snackbarHost = { HoleSnackbarHost(snackbarHostState) },
    ) { insets ->
        LazyColumn(
            Modifier
                .fillMaxWidth()
                .padding(insets)
                .padding(horizontal = 16.dp),
            state = listState,
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            item { ConnectionCard(snapshot, configState, commandError, onToggleRun, onOpenDetails) }
            item {
                SectionHeader(
                    title = if (miuix) "提供服务" else "provide · 提供服务",
                    subtitle = "把本机或本机可达的服务提供给房间内的设备",
                    addLabel = "新增 provide 配置",
                    onAdd = onAddProvide,
                )
            }
            if (configState.config.provide.isEmpty()) {
                item { EmptyCard("暂无 provide 配置", "点击右上角新增，向远端提供 TCP 或 UDP 服务。") }
            } else {
                items(configState.config.provide, key = { it.entryId }) { entry ->
                    ProvideRow(entry, onEditProvide, onToggleProvide, onDeleteProvide, mappingStates["provide" to entry.id])
                }
            }
            item {
                SectionHeader(
                    title = if (miuix) "使用服务" else "consume · 使用服务",
                    subtitle = "把远端服务映射到本地端口",
                    addLabel = "新增 consume 配置",
                    onAdd = onAddConsume,
                )
            }
            if (configState.config.consume.isEmpty()) {
                item { EmptyCard("暂无 consume 配置", "点击右上角新增，通过本地端口使用远端服务。") }
            } else {
                items(configState.config.consume, key = { it.entryId }) { entry ->
                    ConsumeRow(entry, onEditConsume, onToggleConsume, onDeleteConsume, mappingStates["consume" to entry.id])
                }
            }
            item {
                Text(
                    "转发由前台服务保持；进程被系统回收后按保存的配置重新连接，" +
                        "不恢复原有 TCP socket。停止请使用总开关或通知中的\"停止\"。",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(bottom = 24.dp),
                )
            }
        }
    }
}

@Composable
private fun ConnectionCard(
    snapshot: CoreSnapshot,
    configState: ConfigUiState,
    commandError: String?,
    onToggleRun: (Boolean) -> Unit,
    onOpenDetails: () -> Unit,
) {
    val miuix = LocalThemeStyle.current == ThemeStyle.MIUIX
    val provideEnabled = configState.config.provide.count { it.enabled }
    val consumeEnabled = configState.config.consume.count { it.enabled }
    HoleCard(
        Modifier.fillMaxWidth().animateContentSize().clickable(
            enabled = snapshot.nativeReady || snapshot.runRequested,
            onClickLabel = "查看运行详情", onClick = onOpenDetails,
        ),
        containerColor = MaterialTheme.colorScheme.secondaryContainer,
    ) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            run {
                Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                    Column(Modifier.weight(1f)) {
                        Text("连接", style = MaterialTheme.typography.titleMedium, modifier = Modifier.semantics { heading() })
                        Text(
                            if (snapshot.runRequested) "转发由前台服务保持，退出页面不停止" else "开启后由前台服务保持转发",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                    // 开关状态取核心快照的 run_requested：表达"用户希望运行"，
                    // 与信令是否已加入、映射是否 active 是分开的状态。
                    HoleSwitch(
                        checked = snapshot.runRequested,
                        onCheckedChange = onToggleRun,
                        label = "运行转发",
                        enabled = configState.loaded,
                    )
                }
                StatusRow("核心", engineLabel(snapshot))
            }
            StatusRow("信令", signalLabel(snapshot.signalState))
            StatusRow(
                "配置",
                if (!configState.loaded) "读取中"
                else if (miuix) "提供 $provideEnabled 项 · 使用 $consumeEnabled 项"
                else "已保存 · provide $provideEnabled 项 / consume $consumeEnabled 项启用",
            )
            if (snapshot.runRequested) {
                StatusRow(
                    "运行映射",
                    "提供 ${snapshot.provideCount} 项 · 使用 ${snapshot.consumeCount} 项",
                )
            }
            if (snapshot.runRequested || snapshot.nativeReady) {
                StatusRow("应用会话", "${snapshot.tcpSessions} TCP · ${snapshot.udpSessions} UDP")
                Text("查看运行详情 ›", style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.primary,
                    modifier = Modifier.padding(top = 4.dp))
            }
            snapshot.errorMessage?.let {
                Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
            }
            configState.lastError?.let {
                Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
            }
            commandError?.let {
                Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
            }
            if (snapshot.engineState == "loading") LinearProgressIndicator(Modifier.fillMaxWidth())
        }
    }
}

@Composable
private fun StatusRow(label: String, value: String) {
    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
        Text(
            label,
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Text(value, style = MaterialTheme.typography.bodyMedium, modifier = Modifier.weight(1f), textAlign = TextAlign.End)
    }
}

@Composable
private fun ProvideRow(
    entry: ProvideEntry,
    onEdit: (String) -> Unit,
    onToggle: (String, Boolean) -> Unit,
    onDelete: (String) -> Unit,
    state: String?,
) {
    MappingRow(
        title = entry.id.ifBlank { "（未命名）" },
        subtitle = composeServiceUrl(entry.protocol, entry.host, entry.port),
        status = if (!entry.enabled) "已停用" else state?.let(::mappingLabel),
        enabled = entry.enabled,
        onToggle = { onToggle(entry.entryId, it) },
        onEdit = { onEdit(entry.entryId) },
        onDelete = { onDelete(entry.entryId) },
    )
}

@Composable
private fun ConsumeRow(
    entry: ConsumeEntry,
    onEdit: (String) -> Unit,
    onToggle: (String, Boolean) -> Unit,
    onDelete: (String) -> Unit,
    state: String?,
) {
    MappingRow(
        title = entry.id.ifBlank { "（未命名）" },
        subtitle = composeExpose(entry.host, entry.port),
        status = if (!entry.enabled) "已停用" else state?.let(::mappingLabel)
            ?: "协议由远端 provide 决定，当前等待匹配",
        enabled = entry.enabled,
        onToggle = { onToggle(entry.entryId, it) },
        onEdit = { onEdit(entry.entryId) },
        onDelete = { onDelete(entry.entryId) },
    )
}

@Composable
private fun EmptyCard(title: String, description: String) {
    HoleCard(Modifier.fillMaxWidth(), outlined = true) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Text(title, style = MaterialTheme.typography.bodyLarge)
            Text(
                description,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

fun engineLabel(snapshot: CoreSnapshot): String = when (snapshot.engineState) {
    "loading" -> "正在加载核心"
    "stopped" -> if (snapshot.configured) "已停止" else "未配置 · 已停止"
    "starting" -> "正在启动"
    "running" -> "核心运行中"
    "stopping" -> "正在停止"
    "reconfiguring" -> "正在应用配置"
    "recovering" -> "等待网络 / 恢复连接"
    "error" -> "核心异常"
    else -> snapshot.engineState
}

fun signalLabel(state: String): String = when (state) {
    "disconnected" -> "未连接"
    "connecting" -> "连接中"
    "joining" -> "正在加入房间"
    "joined" -> "已加入房间"
    "reconnecting" -> "等待重连"
    "error" -> "异常"
    else -> state
}
