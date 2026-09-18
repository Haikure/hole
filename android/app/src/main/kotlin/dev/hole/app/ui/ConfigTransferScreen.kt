package dev.hole.app.ui

import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import dev.hole.app.ConfigUiState
import dev.hole.app.RunStateStore
import dev.hole.app.config.ConfigExchange
import dev.hole.app.config.ImportPreview
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

@Composable
fun ConfigTransferScreen(config: ConfigUiState, onImport: suspend (ImportPreview) -> Unit, onBack: () -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var includeSecrets by remember { mutableStateOf(false) }
    var pendingExport by remember { mutableStateOf("") }
    var importingBackup by remember { mutableStateOf(false) }
    var preview by remember { mutableStateOf<ImportPreview?>(null) }
    var message by remember { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf(false) }
    val export = rememberLauncherForActivityResult(ActivityResultContracts.CreateDocument("application/octet-stream")) { uri ->
        val document = pendingExport
        pendingExport = ""
        if (uri != null && document.isNotEmpty()) scope.launch {
            busy = true
            try {
                withContext(Dispatchers.IO) {
                    requireNotNull(context.contentResolver.openOutputStream(uri, "wt")) { "文件尚未打开" }
                        .bufferedWriter().use { it.write(document) }
                }
                message = "已导出"
            } catch (_: Exception) { message = "文件写入失败，请重新选择位置" }
            finally { busy = false }
        }
    }
    val picker = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        if (uri != null) scope.launch {
            busy = true
            try {
                preview = withContext(Dispatchers.IO) {
                    val bytes = requireNotNull(context.contentResolver.openInputStream(uri)).use {
                        val buffer = ByteArray(ConfigExchange.MAX_DOCUMENT_BYTES + 1)
                        var count = 0
                        while (count < buffer.size) { val n = it.read(buffer, count, buffer.size - count); if (n < 0) break; count += n }
                        require(count <= ConfigExchange.MAX_DOCUMENT_BYTES) { "文件超过 128 KiB" }
                        buffer.copyOf(count)
                    }
                    val text = bytes.toString(Charsets.UTF_8)
                    if (importingBackup) ConfigExchange.readBackup(text) else ConfigExchange.readCLI(text, config.config)
                }
                message = null
            } catch (_: Exception) { message = "导入检查未通过：请检查文件格式、大小、映射 ID 与端口；原配置保持不变。" }
            finally { busy = false }
        }
    }
    fun exportDocument(backup: Boolean) {
        scope.launch {
            busy = true
            try {
                pendingExport = withContext(Dispatchers.IO) {
                    if (backup) ConfigExchange.backup(config.config, config.password, config.token, includeSecrets, RunStateStore(context).resumeAfterBoot(), config.turnCredential)
                    else ConfigExchange.cli(config.config, config.password, config.token, includeSecrets, config.turnCredential)
                }
                export.launch(if (backup) "hole-android-backup.json" else "hole-config.yaml")
            } catch (_: Exception) { message = "导出失败，请先检查启用项的地址、协议和端口" }
            finally { busy = false }
        }
    }
    HoleScaffold(title = "导入与导出", navigationIcon = { HoleBackButton(onBack) }) { insets ->
        Column(Modifier.fillMaxWidth().padding(insets).verticalScroll(rememberScrollState()).padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp)) {
            HoleSettingsGroup("导出内容") {
                HoleSwitchPreference("包含连接凭据", "默认关闭。开启后文件包含明文信令密码、房间密码和手动 TURN 凭据。", checked = includeSecrets, onCheckedChange = { includeSecrets = it })
                Text("CLI YAML 包含信令服务器 server_url 和当前启用项；未包含的凭据需在使用前补齐。导入旧版未填写服务器的 YAML 时保留本机服务器设置。Android 备份同时保留停用项、列表顺序、外观和开机恢复选项。")
                HoleButton("导出 CLI YAML", { exportDocument(false) }, enabled = config.loaded && !busy)
                HoleButton("导出 Android 完整配置", { exportDocument(true) }, enabled = config.loaded && !busy, secondary = true)
            }
            HoleSettingsGroup("导入配置") {
                Text("先检查导入预览，再确认替换。确认后停止当前连接；检查服务器与凭据后手动开启，不自动连接新配置。")
                HoleButton("导入 CLI YAML / JSON", { importingBackup = false; picker.launch(arrayOf("*/*")) }, enabled = !busy, secondary = true)
                HoleButton("恢复 Android 配置备份", { importingBackup = true; picker.launch(arrayOf("application/json", "text/plain", "application/octet-stream")) }, enabled = !busy, secondary = true)
            }
            if (busy) Text("正在处理文件…", style = MaterialTheme.typography.bodyMedium)
            message?.let { Text(it, style = MaterialTheme.typography.bodyMedium) }
        }
    }
    preview?.let { draft ->
        Dialog(onDismissRequest = { if (!busy) preview = null }) {
            HoleCard {
                Column(Modifier.padding(20.dp).verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    Text("确认替换配置", style = MaterialTheme.typography.titleLarge)
                    DetailRow("提供 / 使用", "${draft.config.provide.size} / ${draft.config.consume.size} 项")
                    DetailRow("启用项", "${draft.config.provide.count { it.enabled } + draft.config.consume.count { it.enabled }} 项")
                    DetailRow("信令服务器", draft.config.connection.serverUrl.ifEmpty { "待填写" })
                    DetailRow("房间", draft.config.connection.room.ifEmpty { "待填写" })
                    DetailRow("凭据", if (draft.password.isNotEmpty() && draft.token.isNotEmpty()) "文件含凭据，将重新加密保存" else "凭据不完整，导入后在设置中填写")
                    HoleButton("停止连接并替换", onClick = {
                        scope.launch {
                            busy = true
                            try { onImport(draft); preview = null; message = "配置已替换，连接保持停止。" }
                            catch (_: Exception) { message = "配置保存失败，请检查应用存储与凭据设置"; preview = null }
                            finally { busy = false }
                        }
                    }, enabled = !busy)
                    HoleTextButton("取消", { if (!busy) preview = null })
                }
            }
        }
    }
}
