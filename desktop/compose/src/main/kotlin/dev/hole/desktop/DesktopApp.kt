package dev.hole.desktop

import androidx.compose.foundation.layout.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.key.*
import androidx.compose.ui.unit.dp
import dev.hole.app.BuildConfig
import dev.hole.app.config.*
import dev.hole.app.ui.*
import kotlinx.coroutines.*
import java.awt.FileDialog
import java.nio.file.Path

@Composable
internal fun DesktopApp(model: DesktopModel) {
    val stored by model.config.collectAsState()
    val snapshot by model.snapshot.collectAsState()
    val error by model.error.collectAsState()
    val config = stored.presentation()
    var route by rememberSaveable { mutableStateOf("home") }
    var backwards by remember { mutableStateOf(false) }
    var entryId by rememberSaveable { mutableStateOf<String?>(null) }
    var transportId by rememberSaveable { mutableStateOf("") }
    val snackbar = remember { SnackbarHostState() }
    fun navigate(next: String) { backwards = false; route = next }
    fun goBack() {
        backwards = true
        route = when (route) {
            "provide-edit", "consume-edit" -> "config"
            "punch" -> "details"
            "settings", "details" -> "home"
            else -> "settings"
        }
    }
    fun edit(provide: Boolean, id: String?) { entryId = id; navigate(if (provide) "provide-edit" else "consume-edit") }
    fun mutate(change: (DesktopState) -> DesktopState) = model.action { model.update(change) }
    fun toggleProvide(id: String, enabled: Boolean) = mutate { it.copy(config = it.config.copy(provide = it.config.provide.map { row -> if (row.entryId == id) row.copy(enabled = enabled) else row })) }
    fun toggleConsume(id: String, enabled: Boolean) = mutate { it.copy(config = it.config.copy(consume = it.config.consume.map { row -> if (row.entryId == id) row.copy(enabled = enabled) else row })) }
    fun delete(id: String, provide: Boolean) = mutate { it.copy(config = if (provide) it.config.copy(provide = it.config.provide.filterNot { row -> row.entryId == id })
        else it.config.copy(consume = it.config.consume.filterNot { row -> row.entryId == id })) }
    val toggleRun: (Boolean) -> Unit = { enabled -> model.action { model.toggleRun(enabled) } }
    val voiceEnabled: (Boolean) -> Unit = { enabled -> mutate { it.copy(config = it.config.copy(voice = it.config.voice.copy(enabled = enabled))) } }
    LaunchedEffect(error) { error?.let { snackbar.showSnackbar(it) } }
    PlatformBackHandler(route !in primaryRoutes) { goBack() }

    BoxWithConstraints(Modifier.fillMaxSize().onPreviewKeyEvent {
        if (it.type == KeyEventType.KeyDown && it.isCtrlPressed) {
            val index = listOf(Key.One, Key.Two, Key.Three, Key.Four, Key.Five).indexOf(it.key)
            if (index >= 0) { navigate(destinations[index].route); true } else false
        } else false
    }) {
        val wide = maxWidth >= 1000.dp
        Row(Modifier.fillMaxSize()) {
            if (wide) Column(Modifier.width(220.dp).fillMaxHeight().padding(16.dp), verticalArrangement = Arrangement.spacedBy(20.dp)) {
                Text("hole", style = MaterialTheme.typography.headlineLarge, modifier = Modifier.padding(12.dp))
                destinations.forEach { destination ->
                    NavigationDrawerItem(label = { Text(destination.title) }, selected = route == destination.route,
                        onClick = { navigate(destination.route) }, icon = { Icon(destination.icon, null) })
                }
                Spacer(Modifier.weight(1f))
                FloatingRunControl(snapshot, snapshot.nativeReady && config.loaded, toggleRun)
                Text("${BuildConfig.VERSION_NAME} · ${snapshot.coreVersion}", style = MaterialTheme.typography.labelSmall)
            }
            Box(Modifier.weight(1f).fillMaxHeight()) {
                CompositionLocalProvider(LocalFloatingInset provides if (wide) 0.dp else 92.dp) {
                    val page: @Composable (String) -> Unit = { target ->
                        when (target) {
                            "home" -> HomeScreen(snapshot, config, error, onOpenSettings = { navigate("settings") },
                                onAddProvide = { edit(true, null) }, onEditProvide = { edit(true, it) }, onAddConsume = { edit(false, null) }, onEditConsume = { edit(false, it) },
                                onToggleProvide = ::toggleProvide, onToggleConsume = ::toggleConsume,
                                onDeleteProvide = { delete(it, true) }, onDeleteConsume = { delete(it, false) },
                                onToggleRun = toggleRun, onOpenDetails = { navigate("details") }, onNavigate = ::navigate)
                            "config" -> ConfigOverviewScreen(config, snapshot, onAddProvide = { edit(true, null) }, onEditProvide = { edit(true, it) },
                                onAddConsume = { edit(false, null) }, onEditConsume = { edit(false, it) }, onToggleProvide = ::toggleProvide, onToggleConsume = ::toggleConsume,
                                onDeleteProvide = { delete(it, true) }, onDeleteConsume = { delete(it, false) }, onBack = ::goBack)
                            "provide-edit" -> ProvideEditScreen(entryId, config, onSave = { value -> model.action {
                                model.update { it.copy(config = it.config.copy(provide = it.config.provide.filterNot { row -> row.entryId == value.entryId } + value)) }; withContext(Dispatchers.Main) { goBack() }
                            } }, onDelete = { id -> delete(id, true); goBack() }, onBack = ::goBack)
                            "consume-edit" -> ConsumeEditScreen(entryId, config, onSave = { value -> model.action {
                                model.update { it.copy(config = it.config.copy(consume = it.config.consume.filterNot { row -> row.entryId == value.entryId } + value)) }; withContext(Dispatchers.Main) { goBack() }
                            } }, onDelete = { id -> delete(id, false); goBack() }, onBack = ::goBack)
                            "details" -> RuntimeDetailsScreen(snapshot, config, error, onBack = ::goBack,
                                onReconnect = { model.action { model.core.renominateTransports() } },
                                onExport = { saveDocument(model, "hole-snapshot.json") { model.core.exportSnapshot() } },
                                onBackground = { navigate("desktop-behavior") }, onTransportSettings = { navigate("transport") }, onNATDetection = { navigate("nat-detection") },
                                onNATPunchProcess = { transportId = it; navigate("punch") })
                            "punch" -> NATPunchProcessScreen(snapshot, transportId, model.core::natPunchProcess, ::goBack,
                                onExport = { id -> saveDocument(model, "hole-punch.json") { model.core.exportNATPunchProcess(id) } }, exporting = false)
                            "nat-detection" -> NATDetectionScreen(snapshot.natDiagnostic, snapshot.nativeReady, error,
                                onStart = { model.action { model.core.startNATProbe("{}") } }, onCancel = { model.action { model.core.cancelNATProbe() } },
                                onBack = ::goBack, onSettings = { navigate("transport") })
                            "voice" -> VoiceScreen(snapshot, config, error, microphoneGranted = true, onVoiceEnabledChange = voiceEnabled,
                                onMutedChange = { model.action { model.core.setVoiceMuted(it) } }, onRequestMicrophone = {
                                    if (stored.config.voice.enabled) model.action { model.retryAudio() } else voiceEnabled(true)
                                }, onBack = ::goBack, onToggleRun = toggleRun,
                                inputOptions = listOf("auto" to "系统默认"), inputSummary = "使用系统默认麦克风和扬声器。切换设备后可重新打开音频设备。")
                            "connection-settings" -> SettingsScreen(config,
                                onSave = { server, password, room, token, device, timeout, interfaces, addresses ->
                                    try {
                                        model.update { it.copy(password = password, token = token, config = it.config.copy(connection = it.config.connection.copy(
                                            serverUrl = server.trim(), room = room.trim(), deviceName = device.trim(), sessionTimeout = timeout.trim(),
                                            candidateInterfaces = splitListField(interfaces), candidateAddresses = splitListField(addresses)))) }; null
                                    } catch (failure: Exception) { failure.message ?: "保存失败" }
                                }, onThemeStyleChange = {}, onThemeModeChange = {}, onDynamicColorChange = {}, onBack = ::goBack,
                                onOpenBackground = { navigate("desktop-behavior") }, onOpenTransfer = { navigate("transfer") }, onOpenTransport = { navigate("transport") })
                            "transport" -> TransportSettingsScreen(config, onSave = { ice, turn, credential, insecure, relay ->
                                try {
                                    model.update { it.copy(turnCredential = credential, config = it.config.copy(connection = it.config.connection.copy(
                                        ice = ice, turn = turn, allowInsecureSignal = insecure, relay = relay))) }; null
                                } catch (failure: Exception) { failure.message ?: "保存失败" }
                            }, onBack = ::goBack)
                            "appearance" -> DesktopSettingsPage("外观", ::goBack) {
                                ThemeSection(ThemeStyle.MATERIAL, ThemeMode.fromValue(stored.config.themeMode), false, {},
                                    onModeChange = { mode -> mutate { it.copy(config = it.config.copy(themeMode = mode.value)) } }, onDynamicColorChange = {},
                                    palette = ThemePalette.fromValue(stored.config.materialPalette), onPaletteChange = { palette -> mutate { it.copy(config = it.config.copy(materialPalette = palette.value)) } })
                                HoleSwitchPreference("减少动态效果", "关闭页面切换动画", checked = stored.reduceMotion,
                                    onCheckedChange = { value -> mutate { it.copy(reduceMotion = value) } })
                            }
                            "transfer" -> TransferPage(model, ::goBack)
                            "desktop-behavior", "background" -> DesktopSettingsPage("桌面与音频", ::goBack) {
                                HoleSettingsGroup("窗口与连接") {
                                    HoleSwitchPreference("打开应用时恢复连接", "只在上次仍请求运行时恢复连接", checked = stored.resumeOnOpen,
                                        onCheckedChange = { value -> mutate { it.copy(resumeOnOpen = value) } })
                                    Text("关闭窗口会停止连接并退出。", style = MaterialTheme.typography.bodyMedium,
                                        color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(horizontal = 16.dp))
                                }
                                HoleSettingsGroup("语音设备") {
                                    Text("语音使用系统默认设备，暂不提供应用内回声消除与降噪。", style = MaterialTheme.typography.bodyMedium,
                                        color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(horizontal = 16.dp))
                                    HoleButton("重新打开音频设备", { model.action { model.retryAudio() } },
                                        modifier = Modifier.fillMaxWidth(), enabled = snapshot.runRequested && stored.config.voice.enabled, secondary = true)
                                }
                            }
                            else -> SettingsHub(backgroundTitle = "桌面与音频", backgroundSummary = "恢复连接、窗口与语音设备",
                                showReleaseUpdate = false, onNavigate = ::navigate)
                        }
                    }
                    if (stored.reduceMotion) page(route) else PageMotion(route, backwards, page)
                    if (!wide) Row(
                        Modifier.align(Alignment.BottomCenter).widthIn(max = 650.dp).fillMaxWidth().padding(16.dp),
                        horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Box(Modifier.weight(1f)) { AppNavigation(route, ::navigate) }
                        FloatingRunControl(snapshot, snapshot.nativeReady && config.loaded, toggleRun, docked = true)
                    }
                    HoleSnackbarHost(snackbar, Modifier.align(Alignment.BottomCenter).padding(bottom = if (wide) 16.dp else 100.dp))
                }
            }
        }
    }
}

@Composable
private fun DesktopSettingsPage(title: String, onBack: () -> Unit, content: @Composable ColumnScope.() -> Unit) {
    val inset = LocalFloatingInset.current
    HoleScaffold(title = title, navigationIcon = { HoleBackButton(onBack) }) { padding ->
        HoleScrollColumn(Modifier.fillMaxSize().padding(padding), contentPadding = PaddingValues(20.dp, 16.dp, 20.dp, inset + 24.dp),
            verticalArrangement = Arrangement.spacedBy(24.dp), content = content)
    }
}

private suspend fun chooseFile(save: Boolean, suggested: String? = null): Path? = withContext(Dispatchers.Main) {
    val dialog = FileDialog(null as java.awt.Frame?, if (save) "导出配置" else "导入配置", if (save) FileDialog.SAVE else FileDialog.LOAD)
    try {
        if (suggested != null) dialog.file = suggested
        dialog.isVisible = true
        dialog.file?.let { Path.of(dialog.directory, it) }
    } finally { dialog.dispose() }
}

private fun saveDocument(model: DesktopModel, name: String, document: suspend () -> String) = model.action {
    val path = chooseFile(true, name) ?: return@action
    val text = document()
    withContext(Dispatchers.IO) { model.storage.writeDocument(path, text) }
}

@Composable
private fun TransferPage(model: DesktopModel, onBack: () -> Unit) {
    var includeSecrets by remember { mutableStateOf(false) }
    var pending by remember { mutableStateOf<Pair<Path, String>?>(null) }
    DesktopSettingsPage("配置文件", onBack) {
        Text("支持 CLI YAML/JSON、Android 备份及旧桌面备份。导入会先停止连接。")
        HoleSwitchPreference("包含凭据", "启用后导出文件包含密码与 TURN 凭据", checked = includeSecrets, onCheckedChange = { includeSecrets = it })
        HoleButton("导入配置", { model.action {
            val path = chooseFile(false) ?: return@action
            val text = withContext(Dispatchers.IO) { model.storage.readDocument(path) }
            // Validate and preview before replacing the active configuration.
            withContext(Dispatchers.IO) { model.storage.importDocument(text, model.config.value) }
            withContext(Dispatchers.Main) { pending = path to text }
        } })
        HoleButton("导出 CLI 配置", { saveDocument(model, "hole.yaml") { model.export(includeSecrets, false) } })
        HoleButton("导出配置备份", { saveDocument(model, "hole-backup.json") { model.export(includeSecrets, true) } })
        Text("保存时不覆盖已有文件；请选择新文件名。备份不包含系统登录启动设置。", style = MaterialTheme.typography.bodySmall)
    }
    pending?.let { (path, text) -> AlertDialog(onDismissRequest = { pending = null }, title = { Text("导入配置？") },
        text = { Text("将从 ${path.fileName} 导入配置，并停止当前连接。") },
        confirmButton = { TextButton(onClick = { pending = null; model.action { model.import(text) } }) { Text("导入") } },
        dismissButton = { TextButton(onClick = { pending = null }) { Text("取消") } }) }
}
