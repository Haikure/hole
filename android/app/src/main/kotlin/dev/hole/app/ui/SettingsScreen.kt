package dev.hole.app.ui

import androidx.activity.compose.BackHandler

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
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
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import dev.hole.app.ConfigUiState
import dev.hole.app.config.ThemeMode
import dev.hole.app.config.ThemeStyle
import dev.hole.app.config.usesDynamicColor
import dev.hole.app.config.isPublicIpv6
import dev.hole.app.config.isValidDuration
import dev.hole.app.config.joinListField
import dev.hole.app.config.validateServerUrl
import kotlinx.coroutines.launch

@Composable
fun SettingsScreen(
    configState: ConfigUiState,
    onSave: suspend (
        serverUrl: String,
        password: String,
        room: String,
        token: String,
        deviceName: String,
        sessionTimeout: String,
        candidateInterfacesText: String,
        candidateAddressesText: String,
    ) -> String?,
    onThemeStyleChange: (ThemeStyle) -> Unit,
    onThemeModeChange: (ThemeMode) -> Unit,
    onDynamicColorChange: (Boolean) -> Unit,
    onBack: () -> Unit,
    onOpenBackground: () -> Unit = {},
    onOpenTransfer: () -> Unit = {},
    onOpenTransport: () -> Unit = {},
    handleBack: Boolean = true,
) {
    val connection = configState.config.connection
    var serverUrl by rememberSaveable { mutableStateOf(connection.serverUrl) }
    var password by remember { mutableStateOf(configState.password) }
    var room by rememberSaveable { mutableStateOf(connection.room) }
    var token by remember { mutableStateOf(configState.token) }
    var deviceName by rememberSaveable { mutableStateOf(connection.deviceName) }
    var sessionTimeout by rememberSaveable { mutableStateOf(connection.sessionTimeout) }
    var candidateInterfaces by rememberSaveable { mutableStateOf(joinListField(connection.candidateInterfaces)) }
    var candidateAddresses by rememberSaveable { mutableStateOf(joinListField(connection.candidateAddresses)) }
    var showPassword by rememberSaveable { mutableStateOf(false) }
    var showToken by rememberSaveable { mutableStateOf(false) }
    var saveError by rememberSaveable { mutableStateOf<String?>(null) }
    var showCancelConfirm by rememberSaveable { mutableStateOf(false) }
    var destination by rememberSaveable { mutableStateOf("back") }
    val scope = rememberCoroutineScope()
    val keyboard = LocalSoftwareKeyboardController.current
    val scrollState = rememberScrollState()
    val miuix = LocalThemeStyle.current == ThemeStyle.MIUIX
    val serverUrlError = validateServerUrl(serverUrl)
    val addressError = candidateAddresses.split(',', '，').map { it.trim() }
        .filter { it.isNotEmpty() }.firstOrNull { !isPublicIpv6(it) }

    val dirty = serverUrl != connection.serverUrl || password != configState.password ||
        room != connection.room || token != configState.token ||
        deviceName != connection.deviceName || sessionTimeout != connection.sessionTimeout ||
        candidateInterfaces != joinListField(connection.candidateInterfaces) ||
        candidateAddresses != joinListField(connection.candidateAddresses)

    fun leave(target: String) {
        destination = target
        if (dirty) showCancelConfirm = true else when (target) {
            "background" -> onOpenBackground()
            "transport" -> onOpenTransport()
            "transfer" -> onOpenTransfer()
            else -> onBack()
        }
    }
    BackHandler(enabled = handleBack) { leave("back") }

    HoleScaffold(
        title = "设置",
        largeTitle = true,
        navigationIcon = {
            HoleBackButton(onClick = { leave("back") })
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
            if (!miuix) HoleSectionTitle("外观")
            if (configState.loaded) {
                ThemeSection(
                    themeStyle = ThemeStyle.fromValue(configState.config.themeStyle),
                    themeMode = ThemeMode.fromValue(configState.config.themeMode),
                    dynamicColor = configState.config.usesDynamicColor(),
                    onStyleChange = { keyboard?.hide(); onThemeStyleChange(it) },
                    onModeChange = onThemeModeChange,
                    onDynamicColorChange = onDynamicColorChange,
                )
            } else {
                Text("正在读取外观设置…", style = MaterialTheme.typography.bodySmall)
            }
            configState.lastError?.let {
                Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
            }

            HoleSettingsGroup("运行与数据") {
                HoleButton("连接方式 · ICE 与中继", { leave("transport") }, secondary = true, modifier = Modifier.fillMaxWidth())
                HoleButton("后台保持与自动恢复", { leave("background") }, secondary = true, modifier = Modifier.fillMaxWidth())
                HoleButton("导入与导出配置", { leave("transfer") }, secondary = true, modifier = Modifier.fillMaxWidth())
            }

            HoleSettingsGroup("连接") {
                HoleTextField(
                    value = serverUrl,
                    onValueChange = { serverUrl = it },
                    label = "信令服务器",
                    supportingText = { Text(serverUrlError ?: "完整 WebSocket URL，例如 wss://HOST/ws；明文 ws:// 仅用于开发") },
                    isError = serverUrlError != null,
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
                SecretField(
                    label = "信令密码",
                    value = password,
                    onValueChange = { password = it },
                    visible = showPassword,
                    onToggleVisible = { showPassword = !showPassword },
                    supportingText = "Worker 部署密码，经 Authorization: Bearer 发送",
                )
                HoleTextField(
                    value = room,
                    onValueChange = { room = it },
                    label = "房间号",
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
                SecretField(
                    label = "房间密码",
                    value = token,
                    onValueChange = { token = it },
                    visible = showToken,
                    onToggleVisible = { showToken = !showToken },
                    supportingText = "加入房间使用的共享 token，与信令密码是两个凭据",
                )
                HoleTextField(
                    value = deviceName,
                    onValueChange = { deviceName = it },
                    label = "设备名",
                    supportingText = { Text("在房间内展示的成员名称") },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
                HoleTextField(
                    value = sessionTimeout,
                    onValueChange = { sessionTimeout = it },
                    label = "会话保留期限",
                    supportingText = {
                        Text(
                            if (isValidDuration(sessionTimeout) || sessionTimeout.isBlank()) {
                                "例如 10m；网络中断后 TCP 在此期限内恢复，UDP 为空闲期限"
                            } else {
                                "格式无效，例如 10m、1h30m"
                            },
                        )
                    },
                    isError = sessionTimeout.isNotBlank() && !isValidDuration(sessionTimeout),
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
            }

            HoleSettingsGroup("高级") {
                HoleTextField(
                    value = candidateInterfaces,
                    onValueChange = { candidateInterfaces = it },
                    label = "候选网卡",
                    supportingText = { Text("逗号分隔的网卡名，留空表示不限制") },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
                HoleTextField(
                    value = candidateAddresses,
                    onValueChange = { candidateAddresses = it },
                    label = "候选地址",
                    supportingText = {
                        Text(
                            if (addressError == null) "逗号分隔的公网 IPv6 字面地址，留空表示不使用"
                            else "\"$addressError\" 不是公网 IPv6 字面地址",
                        )
                    },
                    isError = addressError != null,
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
            }

            saveError?.let {
                Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
            }
            val canSave = serverUrlError == null && addressError == null &&
                sessionTimeout.isNotBlank() && isValidDuration(sessionTimeout)
            Row(Modifier.fillMaxWidth().padding(vertical = 16.dp), horizontalArrangement = Arrangement.End) {
                HoleButton(
                    text = "保存连接设置",
                    enabled = canSave && configState.loaded,
                    onClick = {
                        keyboard?.hide()
                        scope.launch {
                            val error = onSave(
                                serverUrl, password, room, token, deviceName,
                                sessionTimeout, candidateInterfaces, candidateAddresses,
                            )
                            if (error == null) onBack() else saveError = error
                        }
                    },
                )
            }
        }
    }

    if (showCancelConfirm) {
        Dialog(onDismissRequest = { showCancelConfirm = false }) {
            HoleCard {
                Column(Modifier.padding(20.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    Text("放弃未保存的修改？", style = MaterialTheme.typography.titleMedium)
                    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End, verticalAlignment = Alignment.CenterVertically) {
                        HoleTextButton("继续编辑", onClick = { showCancelConfirm = false })
                        HoleTextButton("放弃", onClick = {
                            showCancelConfirm = false
                            when (destination) { "transport" -> onOpenTransport(); "background" -> onOpenBackground(); "transfer" -> onOpenTransfer(); else -> onBack() }
                        })
                    }
                }
            }
        }
    }
}

@Composable
private fun SecretField(
    label: String,
    value: String,
    onValueChange: (String) -> Unit,
    visible: Boolean,
    onToggleVisible: () -> Unit,
    supportingText: String,
) {
    HoleTextField(
        value = value,
        onValueChange = onValueChange,
        label = label,
        supportingText = { Text(supportingText) },
        singleLine = true,
        visualTransformation = if (visible) VisualTransformation.None else PasswordVisualTransformation(),
        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password),
        trailingIcon = {
            // 核心图标集中没有可见性图标，用文字切换，同样支持读屏。
            HoleTextButton(if (visible) "隐藏" else "显示", onClick = onToggleVisible)
        },
        modifier = Modifier.fillMaxWidth(),
    )
}
