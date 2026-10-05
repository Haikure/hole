package dev.hole.app.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import dev.hole.app.ConfigUiState
import dev.hole.app.config.ConsumeEntry
import dev.hole.app.config.ProvideEntry
import dev.hole.app.config.composeServiceUrl
import dev.hole.corebridge.CoreSnapshot

/** 一级配置入口：集中查看所有映射，编辑动作仍复用主页的稳定 entryId。 */
@Composable
fun ConfigOverviewScreen(
    configState: ConfigUiState,
    snapshot: CoreSnapshot = CoreSnapshot(),
    onAddProvide: () -> Unit,
    onEditProvide: (String) -> Unit,
    onAddConsume: () -> Unit,
    onEditConsume: (String) -> Unit,
    onToggleProvide: (String, Boolean) -> Unit,
    onToggleConsume: (String, Boolean) -> Unit,
    onDeleteProvide: (String) -> Unit,
    onDeleteConsume: (String) -> Unit,
    @Suppress("UNUSED_PARAMETER") onBack: () -> Unit,
    onToggleRun: (Boolean) -> Unit = {},
    snackbarHostState: SnackbarHostState? = null,
) {
    val total = configState.config.provide.size + configState.config.consume.size
    val enabled = configState.config.provide.count { it.enabled } + configState.config.consume.count { it.enabled }
    val floatingInset = LocalFloatingInset.current
    HoleScaffold(
        title = "服务",
        largeTitle = false,
        snackbarHost = { snackbarHostState?.let { HoleSnackbarHost(it) } },
    ) { insets ->
        LazyColumn(
            Modifier.fillMaxWidth().testTag("config-overview").padding(insets).padding(horizontal = 16.dp),
            verticalArrangement = Arrangement.spacedBy(14.dp),
        ) {
            item {
                HoleHelp("$enabled / $total 项已启用", "提供服务供房间成员访问；使用服务在本机创建监听地址。")
            }
            item {
                SectionHeader("提供服务", "房间成员可以访问本机的服务", "新增提供项", onAddProvide)
            }
            if (configState.config.provide.isEmpty()) {
                item { HoleEmptyCard("暂无提供服务") }
            } else {
                items(configState.config.provide, key = { it.entryId }) { entry ->
                    androidx.compose.foundation.layout.Box(Modifier.animateItem()) {
                        ProvideOverviewRow(entry, onEditProvide, onToggleProvide, onDeleteProvide,
                            snapshot.mappings.firstOrNull { it.role == "provide" && it.id == entry.id }?.state)
                    }
                }
            }
            item {
                SectionHeader("使用服务", "本机监听并连接对端提供的服务", "新增使用项", onAddConsume)
            }
            if (configState.config.consume.isEmpty()) {
                item { HoleEmptyCard("暂无使用服务") }
            } else {
                items(configState.config.consume, key = { it.entryId }) { entry ->
                    androidx.compose.foundation.layout.Box(Modifier.animateItem()) {
                        ConsumeOverviewRow(entry, onEditConsume, onToggleConsume, onDeleteConsume,
                            snapshot.mappings.firstOrNull { it.role == "consume" && it.id == entry.id }?.state)
                    }
                }
            }
            item { Spacer(Modifier.height(floatingInset + 12.dp)) }
        }
    }
}

@Composable
private fun ProvideOverviewRow(
    entry: ProvideEntry,
    onEdit: (String) -> Unit,
    onToggle: (String, Boolean) -> Unit,
    onDelete: (String) -> Unit,
    state: String?,
) {
    MappingRow(
        title = entry.id.ifBlank { "未命名" },
        subtitle = composeServiceUrl(entry.protocol, entry.host, entry.port),
        status = if (!entry.enabled) "已停用" else state?.let(::mappingLabel) ?: "已启用",
        direction = "提供",
        enabled = entry.enabled,
        onToggle = { onToggle(entry.entryId, it) },
        onEdit = { onEdit(entry.entryId) },
        onDelete = { onDelete(entry.entryId) },
    )
}

@Composable
private fun ConsumeOverviewRow(
    entry: ConsumeEntry,
    onEdit: (String) -> Unit,
    onToggle: (String, Boolean) -> Unit,
    onDelete: (String) -> Unit,
    state: String?,
) {
    MappingRow(
        title = entry.id.ifBlank { "未命名" },
        subtitle = "监听 ${entry.host}:${entry.port}",
        status = if (!entry.enabled) "已停用" else state?.let(::mappingLabel) ?: "已启用",
        direction = "使用",
        enabled = entry.enabled,
        onToggle = { onToggle(entry.entryId, it) },
        onEdit = { onEdit(entry.entryId) },
        onDelete = { onDelete(entry.entryId) },
    )
}
