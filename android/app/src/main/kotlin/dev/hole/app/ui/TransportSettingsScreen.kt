package dev.hole.app.ui

import androidx.compose.material.icons.filled.Check

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
    var relayOrderText by rememberSaveable { mutableStateOf(normalizedTurnOrder(current.turn.order).joinToString(", ")) }
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
    var turnOrderDragging by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    val ice = current.ice.copy(stunUrls = splitListField(stun), directProbeTimeout = probe.trim(), gatherTimeout = gather.trim(), connectivityTimeout = check.trim(), retryMaxDelay = retry.trim(), interfaceAllowlist = splitListField(interfaces), relayOnly = relayOnly)
    val relayOrder = splitListField(relayOrderText)
    val turn = TurnSettings(relay, ttl.trim(), splitListField(urls), username.trim(), relayOrder)
    val dirty = mode != current.connectionMode || ice != current.ice || turn != current.turn || credential != configState.turnCredential || insecure != current.allowInsecureSignal
    fun back() { if (busy) return; if (dirty) confirm = true else onBack() }
    val saveTransport: () -> Unit = {
                error = when {
                    mode != "legacy" && current.candidateAddresses.isNotEmpty() -> "请先在连接设置中清空手动 IPv6 候选地址，或选择“仅 IPv6”模式。"
                    listOf(probe, gather, check, retry, ttl).any { !isValidDuration(it) } -> "请检查时间格式，例如 3s、6h。"
                    ice.stunUrls.any { !it.startsWith("stun:") } -> "STUN 地址需以 stun: 开头。"
                    relay == "manual" && (turn.urls.isEmpty() || username.isBlank() || credential.isEmpty()) -> "请填齐 TURN 地址、用户名和凭据。"
                    relay == "manual" && turn.urls.any { !it.startsWith("turn:") && !it.startsWith("turns:") } -> "TURN 地址需以 turn: 或 turns: 开头。"
                    relayOrderError(relayOrder) != null -> relayOrderError(relayOrder)
                    else -> null
                }
                if (error == null) scope.launch { busy = true; error = onSave(mode, ice, turn, credential, insecure); busy = false; if (error == null) onBack() }
    }
    BackHandler(handleBack) { back() }
    HoleScaffold(title = "连接方式", navigationIcon = { HoleBackButton { back() } },
        floatingActions = { FloatingIconAction(androidx.compose.material.icons.Icons.Default.Check, "保存连接方式", saveTransport, enabled = configState.loaded && !busy) }) { insets ->
        Column(Modifier.fillMaxWidth().padding(insets).verticalScroll(rememberScrollState(), enabled = !turnOrderDragging).padding(16.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
            HoleSettingsGroup("连接策略") {
                HoleSingleChoice(listOf("auto", "ice", "legacy").map { it to connectionModeLabel(it) }, mode, { mode = it }, Modifier.fillMaxWidth())
                HoleHelp("策略说明", connectionModeDescription(mode))
                if (mode == "legacy") Text("此模式不使用下方 ICE / TURN 设置。", style = MaterialTheme.typography.bodySmall)
            }
            HoleSettingsGroup("直连探测") {
                HoleTextField(stun, { stun = it }, "STUN 服务器", modifier = Modifier.fillMaxWidth(),
                    supportingText = { Text("默认 Cloudflare：$DEFAULT_STUN_URL；每行一条，留空仅尝试本地地址。") })
                HoleHelp("STUN 说明", "只探测公网映射，不承载业务数据。")
                HoleTextButton("恢复 Cloudflare 默认值", { stun = DEFAULT_STUN_URL })
            }
            HoleSettingsGroup("直连失败后的中继") {
                HoleSingleChoice(listOf("worker" to "协调服务", "manual" to "手动", "off" to "关闭"), relay, { relay = it }, Modifier.fillMaxWidth())
                HoleHelp("凭据来源", when (relay) {
                    "manual" -> "使用自备 TURN；凭据加密保存在本机。"
                    "off" -> "不申请 TURN 凭据，仅尝试直连。"
                    else -> "由 Worker 发放短期凭据；未配置 TURN 时仍可直连。"
                })
                AnimatedVisibility(relay == "manual") {
                    Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                        HoleTextField(urls, { urls = it }, "TURN 地址", modifier = Modifier.fillMaxWidth(), supportingText = { Text("每行一个地址，需含端口；turns: 使用 TLS。") })
                        HoleTextField(username, { username = it }, "TURN 用户名", modifier = Modifier.fillMaxWidth(), singleLine = true)
                        HoleTextField(credential, { credential = it }, "TURN 凭据", modifier = Modifier.fillMaxWidth(), singleLine = true, visualTransformation = PasswordVisualTransformation())
                    }
                }
                HoleHelp("中继说明", "先尝试直连，再按本机顺序尝试 TURN。TLS 保护中继接入，业务数据由 QUIC 端到端加密。")
            }
            HoleSettingsGroup("本机 TURN 类型顺序") {
                HoleHelp("尝试顺序", "顺序仅影响本机；留空使用默认值，未选类型会跳过。")
                TurnOrderBoard(
                    order = relayOrder,
                    onChange = { relayOrderText = it.joinToString(", ") },
                    onDraggingChanged = { turnOrderDragging = it },
                )
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
                    HoleTextButton("恢复默认顺序", { relayOrderText = "" })
                }
                relayOrderError(relayOrder)?.let { Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall) }
            }
            HoleTextButton(if (advanced) "收起高级参数" else "高级连接参数", { advanced = !advanced })
            AnimatedVisibility(advanced) {
                HoleSettingsGroup("高级参数") {
                    HoleTextField(probe, { probe = it }, "优先直连时间", supportingText = { Text("默认 3s；支持 ms、s、m。") }, modifier = Modifier.fillMaxWidth(), singleLine = true)
                    HoleTextField(gather, { gather = it }, "候选收集期限", modifier = Modifier.fillMaxWidth(), singleLine = true)
                    HoleTextField(check, { check = it }, "连通性检查期限", modifier = Modifier.fillMaxWidth(), singleLine = true)
                    HoleTextField(retry, { retry = it }, "最大重试间隔", modifier = Modifier.fillMaxWidth(), singleLine = true)
                    HoleTextField(ttl, { ttl = it }, "请求的中继凭据有效期", supportingText = { Text("默认 6h；以 Worker 签发期限为准。") }, modifier = Modifier.fillMaxWidth(), singleLine = true)
                    HoleTextField(interfaces, { interfaces = it }, "允许使用的网卡", supportingText = { Text("留空自动选择；Android 跟随系统网络。") }, modifier = Modifier.fillMaxWidth())
                    HoleSwitchPreference("仅用中继测试", "跳过直连，强制使用 TURN。", checked = relayOnly, onCheckedChange = { relayOnly = it })
                    HoleSwitchPreference("允许本地 ws 测试", "仅本地测试使用 ws://；生产环境请用 wss://。", checked = insecure, onCheckedChange = { insecure = it })
                }
            }
            error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
            androidx.compose.foundation.layout.Spacer(Modifier.padding(bottom = 96.dp))
        }
    }
    if (confirm) Dialog(onDismissRequest = { confirm = false }) {
        HoleCard { Column(Modifier.padding(20.dp), verticalArrangement = Arrangement.spacedBy(14.dp)) {
            Text("放弃未保存的修改？", style = MaterialTheme.typography.titleMedium)
            Row { HoleTextButton("继续编辑", { confirm = false }); HoleTextButton("放弃", { confirm = false; onBack() }) }
        } }
    }
}
