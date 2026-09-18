package dev.hole.app.ui

import androidx.activity.compose.BackHandler
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
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
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import dev.hole.app.ConfigUiState
import dev.hole.app.config.*
import kotlinx.coroutines.launch

@Composable
fun TransportSettingsScreen(
    configState: ConfigUiState,
    onSave: suspend (String, IceSettings, TurnSettings, String, Boolean) -> String?,
    onBack: () -> Unit,
    handleBack: Boolean = true,
) {
    val current = configState.config.connection
    var mode by rememberSaveable { mutableStateOf(current.connectionMode) }
    var stun by rememberSaveable { mutableStateOf(current.ice.stunUrls.joinToString("\n")) }
    var relay by rememberSaveable { mutableStateOf(current.turn.mode) }
    var urls by rememberSaveable { mutableStateOf(current.turn.urls.joinToString("\n")) }
    var username by rememberSaveable { mutableStateOf(current.turn.username) }
    var credential by remember { mutableStateOf(configState.turnCredential) }
    var probe by rememberSaveable { mutableStateOf(current.ice.directProbeTimeout) }
    var gather by rememberSaveable { mutableStateOf(current.ice.gatherTimeout) }
    var check by rememberSaveable { mutableStateOf(current.ice.connectivityTimeout) }
    var retry by rememberSaveable { mutableStateOf(current.ice.retryMaxDelay) }
    var ttl by rememberSaveable { mutableStateOf(current.turn.ttl) }
    var interfaces by rememberSaveable { mutableStateOf(current.ice.interfaceAllowlist.joinToString(", ")) }
    var insecure by rememberSaveable { mutableStateOf(current.allowInsecureSignal) }
    var relayOnly by rememberSaveable { mutableStateOf(current.ice.relayOnly) }
    var advanced by rememberSaveable { mutableStateOf(false) }
    var confirm by rememberSaveable { mutableStateOf(false) }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    val scope = rememberCoroutineScope()
    val ice = current.ice.copy(stunUrls = splitListField(stun), directProbeTimeout = probe.trim(), gatherTimeout = gather.trim(), connectivityTimeout = check.trim(), retryMaxDelay = retry.trim(), interfaceAllowlist = splitListField(interfaces), relayOnly = relayOnly)
    val turn = TurnSettings(relay, ttl.trim(), splitListField(urls), username.trim())
    val dirty = mode != current.connectionMode || ice != current.ice || turn != current.turn || credential != configState.turnCredential || insecure != current.allowInsecureSignal
    fun back() { if (busy) return; if (dirty) confirm = true else onBack() }
    BackHandler(handleBack) { back() }
    HoleScaffold(title = "连接方式", navigationIcon = { HoleBackButton { back() } }) { insets ->
        Column(Modifier.fillMaxWidth().padding(insets).verticalScroll(rememberScrollState()).padding(16.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
            HoleSettingsGroup("选择连接策略") {
                HoleSingleChoice(listOf("auto", "ice", "legacy").map { it to connectionModeLabel(it) }, mode, { mode = it }, Modifier.fillMaxWidth())
                Text(connectionModeDescription(mode), style = MaterialTheme.typography.bodyMedium)
                if (mode == "legacy") Text("下方 ICE 的探测和中继设置仅在“自动”或“仅 ICE”模式下生效。", style = MaterialTheme.typography.bodySmall)
            }
            HoleSettingsGroup("直连探测") {
                HoleTextField(stun, { stun = it }, "STUN 服务器", modifier = Modifier.fillMaxWidth(),
                    supportingText = { Text("默认 Cloudflare：$DEFAULT_STUN_URL。每行一条；留空时仅尝试本地地址。") })
                Text("STUN 只帮助发现公网映射，不转发业务数据。服务之间仍通过加密的 QUIC 通道传输。", style = MaterialTheme.typography.bodySmall)
                HoleTextButton("恢复 Cloudflare 默认值", { stun = DEFAULT_STUN_URL })
            }
            HoleSettingsGroup("直连失败后的中继") {
                HoleSingleChoice(listOf("worker" to "协调服务", "manual" to "手动", "off" to "关闭"), relay, { relay = it }, Modifier.fillMaxWidth())
                Text(when (relay) {
                    "manual" -> "填写自己的 TURN 服务器。凭据在本机加密保存。"
                    "off" -> "不申请本机 TURN 中继，继续尝试直连。"
                    else -> "由 Worker 签发 Cloudflare 短期凭据；主密钥留在部署侧。未配置中继时，直连仍可使用。"
                }, style = MaterialTheme.typography.bodyMedium)
                AnimatedVisibility(relay == "manual") {
                    Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                        HoleTextField(urls, { urls = it }, "TURN 地址", modifier = Modifier.fillMaxWidth(), supportingText = { Text("每行一条：UDP 用 turn:HOST:3478?transport=udp；TCP 用 turn:HOST:80?transport=tcp；TLS 用 turns:HOST:443?transport=tcp。也支持 3478、5349 及自定义端口。") })
                        HoleTextField(username, { username = it }, "TURN 用户名", modifier = Modifier.fillMaxWidth(), singleLine = true)
                        HoleTextField(credential, { credential = it }, "TURN 凭据", modifier = Modifier.fillMaxWidth(), singleLine = true, visualTransformation = PasswordVisualTransformation())
                    }
                }
                Text("尝试顺序：直连 → UDP 中继 → TCP 中继（80 → 3478）→ TLS 中继（443 → 5349）。同一对设备的服务共享连接。", style = MaterialTheme.typography.bodySmall)
                Text("TLS 加密本机到 TURN 的接入；普通 TCP 接入不使用 TLS。业务数据始终由 QUIC 端到端加密。新顺序需双方客户端与 Worker 均支持。", style = MaterialTheme.typography.bodySmall)
            }
            HoleTextButton(if (advanced) "收起高级参数" else "高级连接参数", { advanced = !advanced })
            AnimatedVisibility(advanced) {
                HoleSettingsGroup("高级参数") {
                    HoleTextField(probe, { probe = it }, "优先直连时间", supportingText = { Text("默认 3s；时间格式可用 ms、s、m。") }, modifier = Modifier.fillMaxWidth(), singleLine = true)
                    HoleTextField(gather, { gather = it }, "候选收集期限", modifier = Modifier.fillMaxWidth(), singleLine = true)
                    HoleTextField(check, { check = it }, "连通性检查期限", modifier = Modifier.fillMaxWidth(), singleLine = true)
                    HoleTextField(retry, { retry = it }, "最大重试间隔", modifier = Modifier.fillMaxWidth(), singleLine = true)
                    HoleTextField(ttl, { ttl = it }, "请求的中继凭据有效期", supportingText = { Text("默认 6h；Worker 最终决定签发期限。") }, modifier = Modifier.fillMaxWidth(), singleLine = true)
                    HoleTextField(interfaces, { interfaces = it }, "允许使用的网卡", supportingText = { Text("留空表示自动选择；Android 仍使用系统选定的网络。") }, modifier = Modifier.fillMaxWidth())
                    HoleSwitchPreference("仅用中继测试", "用于核对 TURN；日常使用保持关闭以优先直连。", checked = relayOnly, onCheckedChange = { relayOnly = it })
                    HoleSwitchPreference("允许本地 ws 测试", "默认使用 wss://，此项只为显式配置的本地测试入口启用。", checked = insecure, onCheckedChange = { insecure = it })
                }
            }
            error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
            HoleButton("保存连接方式", enabled = configState.loaded && !busy, modifier = Modifier.fillMaxWidth(), onClick = {
                error = when {
                    mode != "legacy" && current.candidateAddresses.isNotEmpty() -> "请先在连接设置中清空手动 IPv6 候选地址，或选择“仅 IPv6”模式。"
                    listOf(probe, gather, check, retry, ttl).any { !isValidDuration(it) } -> "请检查时间格式，例如 3s、6h。"
                    ice.stunUrls.any { !it.startsWith("stun:") } -> "STUN 地址需以 stun: 开头。"
                    relay == "manual" && (turn.urls.isEmpty() || username.isBlank() || credential.isEmpty()) -> "请填齐 TURN 地址、用户名和凭据。"
                    relay == "manual" && turn.urls.any { !it.startsWith("turn:") && !it.startsWith("turns:") } -> "TURN 地址需以 turn: 或 turns: 开头。"
                    else -> null
                }
                if (error == null) scope.launch { busy = true; error = onSave(mode, ice, turn, credential, insecure); busy = false; if (error == null) onBack() }
            })
        }
    }
    if (confirm) Dialog(onDismissRequest = { confirm = false }) {
        HoleCard { Column(Modifier.padding(20.dp), verticalArrangement = Arrangement.spacedBy(14.dp)) {
            Text("放弃未保存的修改？", style = MaterialTheme.typography.titleMedium)
            Row { HoleTextButton("继续编辑", { confirm = false }); HoleTextButton("放弃", { confirm = false; onBack() }) }
        } }
    }
}
