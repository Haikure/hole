package dev.hole.app.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import dev.hole.app.ConfigUiState
import dev.hole.app.config.findIdConflict
import dev.hole.app.config.isLiteralIp
import dev.hole.app.config.isValidMappingId
import dev.hole.app.config.normalizeHostInput
import dev.hole.app.config.parsePort
import dev.hole.app.config.ConsumeEntry
import dev.hole.app.config.ProvideEntry
import dev.hole.app.config.ThemeStyle
import java.util.UUID

@Composable
fun ProvideEditScreen(
    entryId: String?,
    configState: ConfigUiState,
    onSave: (ProvideEntry) -> Unit,
    onDelete: (String) -> Unit,
    onBack: () -> Unit,
) {
    val existing = entryId?.let { id -> configState.config.provide.find { it.entryId == id } }
    var stableId by rememberSaveable { mutableStateOf(existing?.entryId ?: UUID.randomUUID().toString()) }
    var id by rememberSaveable { mutableStateOf(existing?.id ?: "") }
    var protocol by rememberSaveable { mutableStateOf(existing?.protocol ?: "tcp") }
    var host by rememberSaveable { mutableStateOf(existing?.host ?: "") }
    var port by rememberSaveable { mutableStateOf(existing?.let { it.port.toString() } ?: "") }
    var enabled by rememberSaveable { mutableStateOf(existing?.enabled ?: true) }
    var attempted by rememberSaveable { mutableStateOf(false) }

    val idError = if (attempted && !isValidMappingId(id)) "映射 ID 不能为空，且不能包含空白字符" else null
    val hostError = if (attempted && host.isBlank()) "服务地址不能为空" else null
    val portValue = parsePort(port)
    val portError = if (attempted && portValue == null) "端口必须是 1–65535 的数字" else null
    val candidate = ProvideEntry(stableId, id.trim(), protocol, normalizeHostInput(host), portValue ?: 0, enabled)
    val otherProvide = configState.config.provide.filter { it.entryId != stableId }
    val conflict = if (attempted) findIdConflict(otherProvide + candidate, configState.config.consume) else null

    EditScaffold(
        title = if (existing == null) "新增 provide" else "编辑 provide",
        onBack = onBack,
        attempted = attempted,
        hasErrors = idError != null || hostError != null || portError != null || conflict != null,
        onDelete = if (existing == null) null else ({ onDelete(stableId) }),
        fields = {
            HoleTextField(
                value = id,
                onValueChange = { id = it },
                label = "映射 ID",
                supportingText = { Text(idError ?: "与远端 consume 配置配对的标识，同类内不重复") },
                isError = idError != null,
                singleLine = true,
                modifier = Modifier.fillMaxWidth(),
            )
            Text("协议", style = MaterialTheme.typography.bodyMedium)
            HoleSingleChoice(
                options = listOf("tcp" to "TCP", "udp" to "UDP"),
                selectedValue = protocol,
                onSelect = { protocol = it },
                modifier = Modifier.fillMaxWidth(),
            )
            HoleTextField(
                value = host,
                onValueChange = { host = it },
                label = "服务地址",
                supportingText = { Text(hostError ?: "允许域名、IPv4、IPv6；例如 127.0.0.1 或 ssh.example.com") },
                isError = hostError != null,
                singleLine = true,
                modifier = Modifier.fillMaxWidth(),
            )
            HoleTextField(
                value = port,
                onValueChange = { port = it },
                label = "服务端口",
                supportingText = { Text(portError ?: "1–65535") },
                isError = portError != null,
                singleLine = true,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
                modifier = Modifier.fillMaxWidth(),
            )
            conflict?.let {
                Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
            }
            EnabledRow(enabled) { enabled = it }
        },
        onSave = {
            attempted = true
            if (isValidMappingId(id) && normalizeHostInput(host).isNotBlank() && portValue != null &&
                findIdConflict(otherProvide + candidate, configState.config.consume) == null) {
                onSave(candidate)
            }
        },
    )
}

@Composable
fun ConsumeEditScreen(
    entryId: String?,
    configState: ConfigUiState,
    onSave: (ConsumeEntry) -> Unit,
    onDelete: (String) -> Unit,
    onBack: () -> Unit,
) {
    val existing = entryId?.let { id -> configState.config.consume.find { it.entryId == id } }
    var stableId by rememberSaveable { mutableStateOf(existing?.entryId ?: UUID.randomUUID().toString()) }
    var id by rememberSaveable { mutableStateOf(existing?.id ?: "") }
    var host by rememberSaveable { mutableStateOf(existing?.host ?: "127.0.0.1") }
    var port by rememberSaveable { mutableStateOf(existing?.let { it.port.toString() } ?: "") }
    var enabled by rememberSaveable { mutableStateOf(existing?.enabled ?: true) }
    var attempted by rememberSaveable { mutableStateOf(false) }

    val idError = if (attempted && !isValidMappingId(id)) "映射 ID 不能为空，且不能包含空白字符" else null
    val hostValue = normalizeHostInput(host)
    val hostError = when {
        attempted && hostValue.isBlank() -> "本地监听地址不能为空"
        attempted && !isLiteralIp(hostValue) -> "必须是字面 IP 地址（例如 127.0.0.1 或 ::1），不解析域名"
        else -> null
    }
    val portValue = parsePort(port)
    val portError = if (attempted && portValue == null) "端口必须是 1–65535 的数字" else null
    val portWarning = if (!attempted || portValue == null) null else configState.config.consume
        .filter { it.entryId != stableId && it.enabled && it.host == hostValue && it.port == portValue }
        .firstOrNull()
        ?.let { "与已启用的映射 \"${it.id}\" 监听相同地址和端口，运行时可能绑定失败" }
    val candidate = ConsumeEntry(stableId, id.trim(), hostValue, portValue ?: 0, enabled)
    val otherConsume = configState.config.consume.filter { it.entryId != stableId }
    val conflict = if (attempted) findIdConflict(configState.config.provide, otherConsume + candidate) else null

    EditScaffold(
        title = if (existing == null) "新增 consume" else "编辑 consume",
        onBack = onBack,
        attempted = attempted,
        hasErrors = idError != null || hostError != null || portError != null || conflict != null,
        onDelete = if (existing == null) null else ({ onDelete(stableId) }),
        fields = {
            HoleTextField(
                value = id,
                onValueChange = { id = it },
                label = "映射 ID",
                supportingText = { Text(idError ?: "匹配远端 provide 的映射 ID，同类内不重复") },
                isError = idError != null,
                singleLine = true,
                modifier = Modifier.fillMaxWidth(),
            )
            HoleTextField(
                value = host,
                onValueChange = { host = it },
                label = "本地监听地址",
                supportingText = {
                    Text(
                        hostError
                            ?: "必须是字面 IP；127.0.0.1 仅本机可访问，0.0.0.0 或 :: 表示所有接口",
                    )
                },
                isError = hostError != null,
                singleLine = true,
                modifier = Modifier.fillMaxWidth(),
            )
            HoleTextField(
                value = port,
                onValueChange = { port = it },
                label = "本地监听端口",
                supportingText = {
                    Text(
                        when {
                            portError != null -> portError
                            portWarning != null -> portWarning
                            else -> "1–65535；建议使用高位端口"
                        },
                    )
                },
                isError = portError != null,
                singleLine = true,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
                modifier = Modifier.fillMaxWidth(),
            )
            Text(
                "协议由匹配到的远端 provide 决定；尚未匹配时显示“等待远端服务”。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            conflict?.let {
                Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
            }
            EnabledRow(enabled) { enabled = it }
        },
        onSave = {
            attempted = true
            if (isValidMappingId(id) && isLiteralIp(hostValue) && portValue != null &&
                findIdConflict(configState.config.provide, otherConsume + candidate) == null) {
                onSave(candidate)
            }
        },
    )
}

@Composable
private fun EnabledRow(enabled: Boolean, onChange: (Boolean) -> Unit) {
    if (LocalThemeStyle.current == ThemeStyle.MIUIX) {
        HoleSwitchPreference(
            title = "保存后启用", summary = "停用时保留配置，不加入运行集合", checked = enabled,
            insideMargin = PaddingValues(0.dp), onCheckedChange = onChange,
        )
        return
    }
    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        Column(Modifier.weight(1f)) {
            Text("保存后启用")
            Text(
                "停用的条目保留全部字段，不进入有效配置",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        HoleSwitch(checked = enabled, onCheckedChange = onChange, label = "保存后启用")
    }
}

@Composable
private fun EditScaffold(
    title: String,
    onBack: () -> Unit,
    attempted: Boolean,
    hasErrors: Boolean,
    onDelete: (() -> Unit)?,
    fields: @Composable () -> Unit,
    onSave: () -> Unit,
) {
    val scrollState = rememberScrollState()
    HoleScaffold(
        title = title,
        navigationIcon = {
            HoleBackButton(onClick = onBack)
        },
        bottomBar = {
            Row(
                Modifier
                    .fillMaxWidth()
                    .navigationBarsPadding()
                    .padding(horizontal = 16.dp, vertical = 12.dp),
                horizontalArrangement = Arrangement.spacedBy(12.dp, Alignment.End),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                if (onDelete != null) {
                    // 读屏或不便滑动的用户从编辑页删除；滑动删除在列表页仍可用。
                    HoleButton("删除", onClick = onDelete, secondary = true)
                }
                HoleButton(
                    text = "保存",
                    enabled = !attempted || !hasErrors,
                    onClick = onSave,
                )
            }
        },
    ) { insets ->
        Column(
            Modifier
                .fillMaxWidth()
                .padding(insets)
                .verticalScroll(scrollState)
                .padding(horizontal = 16.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            HoleSettingsGroup("服务配置") { fields() }
        }
    }
}
