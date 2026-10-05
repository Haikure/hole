package dev.hole.app.ui

import androidx.compose.material.icons.filled.Check

import androidx.activity.compose.BackHandler
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
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
    onVoiceEnabledChange: (Boolean) -> Unit = {},
    handleBack: Boolean = true,
    onPaletteChange: (dev.hole.app.config.ThemePalette) -> Unit = {},
) {
    val connection = configState.config.connection
    var voiceEnabled by rememberSaveable { mutableStateOf(configState.config.voice.enabled) }
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
    var showAdvanced by rememberSaveable { mutableStateOf(false) }
    var destination by rememberSaveable { mutableStateOf("back") }
    val scope = rememberCoroutineScope()
    val keyboard = LocalSoftwareKeyboardController.current
    val scrollState = rememberScrollState()
    val serverUrlError = validateServerUrl(serverUrl)
    val addressError = candidateAddresses.split(',', '，').map { it.trim() }
        .filter { it.isNotEmpty() }.firstOrNull { !isPublicIpv6(it) }
    val validTimeout = sessionTimeout.isNotBlank() && isValidDuration(sessionTimeout)

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

    fun saveConnection() {
        keyboard?.hide()
        scope.launch {
            val error = onSave(
                serverUrl, password, room, token, deviceName,
                sessionTimeout, candidateInterfaces, candidateAddresses,
            )
            if (error == null) onBack() else saveError = error
        }
    }

    BackHandler(enabled = handleBack) { leave("back") }

    HoleScaffold(
        title = "房间与身份",
        largeTitle = true,
        navigationIcon = { HoleBackButton(onClick = { leave("back") }) },
        floatingActions = {
            FloatingIconAction(androidx.compose.material.icons.Icons.Default.Check, "保存", ::saveConnection,
                enabled = dirty && serverUrlError == null && addressError == null && validTimeout && configState.loaded)
        },
    ) { insets ->
        Column(
            Modifier
                .fillMaxWidth()
                .padding(insets)
                .verticalScroll(scrollState)
                .padding(horizontal = 20.dp),
            verticalArrangement = Arrangement.spacedBy(18.dp),
        ) {
            saveError?.let { Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodyMedium) }
            HoleSettingsGroup("连接") {
                HoleTextField(
                    value = serverUrl,
                    onValueChange = { serverUrl = it; saveError = null },
                    label = "信令服务器",
                    supportingText = { Text(serverUrlError ?: "输入完整 WebSocket 地址，例如 wss://HOST/ws") },
                    isError = serverUrlError != null,
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
                SecretField(
                    label = "信令密码",
                    value = password,
                    onValueChange = { password = it; saveError = null },
                    visible = showPassword,
                    onToggleVisible = { showPassword = !showPassword },
                    supportingText = "用于连接信令服务器",
                )
                HoleTextField(
                    value = room,
                    onValueChange = { room = it; saveError = null },
                    label = "房间号",
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
                SecretField(
                    label = "房间密码",
                    value = token,
                    onValueChange = { token = it; saveError = null },
                    visible = showToken,
                    onToggleVisible = { showToken = !showToken },
                    supportingText = "加入此房间使用，与服务器密码不同",
                )
                HoleTextField(
                    value = deviceName,
                    onValueChange = { deviceName = it; saveError = null },
                    label = "设备名",
                    supportingText = { Text("在房间成员列表中显示") },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
                HoleTextField(
                    value = sessionTimeout,
                    onValueChange = { sessionTimeout = it; saveError = null },
                    label = "会话保留期限",
                    supportingText = {
                        Text(if (validTimeout || sessionTimeout.isBlank()) "例如 10m 或 1h30m" else "格式无效，请使用 10m 或 1h30m")
                    },
                    isError = sessionTimeout.isNotBlank() && !isValidDuration(sessionTimeout),
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
            }

            HoleSettingsGroup("网络高级选项") {
                HoleHelp("网络选项", "可限制探测使用的网卡或指定公网 IPv6 地址；通常保持默认即可。")
                HoleTextButton(if (showAdvanced) "收起网络选项" else "编辑网络选项", { showAdvanced = !showAdvanced })
                AnimatedVisibility(showAdvanced) {
                    Column(verticalArrangement = Arrangement.spacedBy(14.dp)) {
                        HoleTextField(
                            value = candidateInterfaces,
                            onValueChange = { candidateInterfaces = it; saveError = null },
                            label = "候选网卡",
                            supportingText = { Text("网卡名以逗号分隔；留空表示不限制") },
                            singleLine = true,
                            modifier = Modifier.fillMaxWidth(),
                        )
                        HoleTextField(
                            value = candidateAddresses,
                            onValueChange = { candidateAddresses = it; saveError = null },
                            label = "候选地址",
                            supportingText = {
                                Text(if (addressError == null) "公网 IPv6 地址以逗号分隔；留空表示自动探测" else "“$addressError”不是公网 IPv6 地址")
                            },
                            isError = addressError != null,
                            singleLine = true,
                            modifier = Modifier.fillMaxWidth(),
                        )
                    }
                }
            }
            androidx.compose.foundation.layout.Spacer(Modifier.height(96.dp))
        }
    }

    if (showCancelConfirm) {
        Dialog(onDismissRequest = { showCancelConfirm = false }) {
            HoleCard {
                Column(Modifier.padding(22.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    Text("放弃未保存的修改？", style = MaterialTheme.typography.titleLarge)
                    Text("当前连接设置尚未保存。", style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End, verticalAlignment = Alignment.CenterVertically) {
                        HoleTextButton("继续编辑", onClick = { showCancelConfirm = false })
                        HoleTextButton("放弃", onClick = {
                            showCancelConfirm = false
                            when (destination) {
                                "transport" -> onOpenTransport()
                                "background" -> onOpenBackground()
                                "transfer" -> onOpenTransfer()
                                else -> onBack()
                            }
                        })
                    }
                }
            }
        }
    }
}

@Composable
private fun SettingsActionRow(title: String, summary: String, onClick: () -> Unit) {
    Surface(
        modifier = Modifier.fillMaxWidth().clickable(onClickLabel = title, onClick = onClick),
        shape = MaterialTheme.shapes.large,
        color = MaterialTheme.colorScheme.surface,
        border = androidx.compose.foundation.BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.65f)),
    ) {
        Row(Modifier.padding(horizontal = 16.dp, vertical = 14.dp), verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(3.dp)) {
                Text(title, style = MaterialTheme.typography.titleSmall)
                Text(summary, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            Text("›", style = MaterialTheme.typography.headlineSmall, color = MaterialTheme.colorScheme.primary)
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
        trailingIcon = { HoleTextButton(if (visible) "隐藏" else "显示", onClick = onToggleVisible) },
        modifier = Modifier.fillMaxWidth(),
    )
}
