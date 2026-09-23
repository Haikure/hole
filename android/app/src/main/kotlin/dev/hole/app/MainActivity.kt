package dev.hole.app

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.material3.SnackbarDuration
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.SnackbarResult
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.saveable.rememberSaveableStateHolder
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalContext
import androidx.core.content.ContextCompat
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.viewmodel.compose.viewModel
import dev.hole.app.ui.ConsumeEditScreen
import dev.hole.app.ui.HomeScreen
import dev.hole.app.ui.HoleTheme
import dev.hole.app.ui.ProvideEditScreen
import dev.hole.app.ui.SettingsScreen
import dev.hole.app.ui.RuntimeDetailsScreen
import dev.hole.app.ui.BackgroundScreen
import dev.hole.app.ui.ConfigTransferScreen
import dev.hole.app.ui.PageMotion
import dev.hole.app.ui.TransportSettingsScreen
import dev.hole.app.config.ThemeMode
import dev.hole.app.config.ThemeStyle
import dev.hole.app.config.usesDynamicColor
import dev.hole.corebridge.CoreSnapshot
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.launch
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

class MainActivity : ComponentActivity() {
    private var detailRequest by mutableStateOf(0)
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        if (intent.getBooleanExtra("open_details", false)) detailRequest++
        setContent {
            val model: MainViewModel = viewModel()
            val lifecycle = LocalLifecycleOwner.current.lifecycle
            DisposableEffect(model, lifecycle) {
                val owner = Any()
                val observer = LifecycleEventObserver { _, _ -> model.setUiVisible(owner, lifecycle.currentState.isAtLeast(Lifecycle.State.STARTED)) }
                lifecycle.addObserver(observer)
                model.setUiVisible(owner, lifecycle.currentState.isAtLeast(Lifecycle.State.STARTED))
                onDispose { lifecycle.removeObserver(observer); model.setUiVisible(owner, false) }
            }
            val snapshot by model.state.collectAsStateWithLifecycle()
            val configState by model.configState.collectAsStateWithLifecycle()
            val commandError by model.commandError.collectAsStateWithLifecycle()
            HoleTheme(
                style = ThemeStyle.fromValue(configState.config.themeStyle),
                mode = ThemeMode.fromValue(configState.config.themeMode),
                dynamic = configState.config.usesDynamicColor(),
            ) {
                AppRoot(model, snapshot, configState, commandError, detailRequest)
            }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        if (intent.getBooleanExtra("open_details", false)) detailRequest++
    }
}

/** 一次可撤销的删除：message 展示在 Snackbar，restore 恢复原条目到原位置。 */
private data class UndoAction(val message: String, val restore: suspend (MainViewModel) -> Unit)

@Composable
private fun AppRoot(
    model: MainViewModel,
    snapshot: CoreSnapshot,
    configState: ConfigUiState,
    commandError: String?,
    detailRequest: Int,
) {
    var backStack by rememberSaveable { mutableStateOf(listOf("home")) }
    val route = backStack.last()
    var popping by remember { mutableStateOf(false) }
    val pageState = rememberSaveableStateHolder()
    val context = LocalContext.current
    val snackbarHostState = remember { SnackbarHostState() }
    val undoQueue = remember { Channel<UndoAction>(Channel.UNLIMITED) }
    val scope = rememberCoroutineScope()

    fun showNotice(message: String) {
        scope.launch { snackbarHostState.showSnackbar(message) }
    }

    fun discardFormState() {
        if ((route == "settings" || route == "transport") || route.startsWith("provide/") || route.startsWith("consume/")) pageState.removeState(route)
    }
    fun navigate(target: String) {
        if (target == route) return
        discardFormState()
        popping = false
        backStack = backStack + target
    }
    fun goBack() {
        if (backStack.size > 1) {
            discardFormState()
            popping = true
            backStack = backStack.dropLast(1)
        }
    }
    fun goHome() { discardFormState(); popping = true; backStack = listOf("home") }
    BackHandler(enabled = route != "home" && route != "settings" && route != "transport") { goBack() }
    LaunchedEffect(detailRequest) { if (detailRequest > 0) navigate("details") }

    var report by remember { mutableStateOf("") }
    val exportReport = rememberLauncherForActivityResult(ActivityResultContracts.CreateDocument("text/plain")) { uri ->
        val document = report
        report = ""
        if (uri != null && document.isNotEmpty()) scope.launch {
            try {
                withContext(Dispatchers.IO) {
                    requireNotNull(context.contentResolver.openOutputStream(uri, "wt")).bufferedWriter().use { it.write(document) }
                }
                showNotice("脱敏诊断已导出")
            } catch (_: Exception) { showNotice("诊断写入失败，请重新选择位置") }
        }
    }

    // 串行处理撤销队列：每条 Snackbar 等待结束（超时、操作或被替换）再处理下一条。
    LaunchedEffect(Unit) {
        for (undo in undoQueue) {
            val result = snackbarHostState.showSnackbar(undo.message, actionLabel = "撤销", duration = SnackbarDuration.Long)
            if (result == SnackbarResult.ActionPerformed) undo.restore(model)
        }
    }

    // 通知权限只影响通知可见性，不影响前台服务运行；拒绝后照常启动并提示。
    var startAfterPermission by remember { mutableStateOf(false) }
    val permissionLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestPermission(),
    ) { granted ->
        if (startAfterPermission) {
            startAfterPermission = false
            if (!granted) showNotice("未授予通知权限，运行状态通知可能不显示")
            model.startRun()
        }
    }

    val onToggleRun: (Boolean) -> Unit = { wantOn ->
        if (!wantOn) {
            model.stopRun()
        } else {
            val connectionError = model.connectionError()
            if (connectionError != null) {
                showNotice("连接设置不完整：$connectionError")
            } else {
                val granted = Build.VERSION.SDK_INT < 33 || ContextCompat.checkSelfPermission(
                    context, Manifest.permission.POST_NOTIFICATIONS,
                ) == PackageManager.PERMISSION_GRANTED
                if (granted) {
                    model.startRun()
                } else {
                    startAfterPermission = true
                    permissionLauncher.launch(Manifest.permission.POST_NOTIFICATIONS)
                }
            }
        }
    }

    fun deleteProvideWithUndo(entryId: String) {
        scope.launch {
            val removed = model.deleteProvide(entryId)
            if (removed != null) {
                undoQueue.trySend(UndoAction("已删除 provide \"${removed.first.id}\"") { vm ->
                    vm.upsertProvide(removed.first, removed.second)
                })
            }
        }
    }

    fun deleteConsumeWithUndo(entryId: String) {
        scope.launch {
            val removed = model.deleteConsume(entryId)
            if (removed != null) {
                undoQueue.trySend(UndoAction("已删除 consume \"${removed.first.id}\"") { vm ->
                    vm.upsertConsume(removed.first, removed.second)
                })
            }
        }
    }

    PageMotion(route, popping) { page ->
      pageState.SaveableStateProvider(page) {
       when {
        page == "settings" -> SettingsScreen(
            configState = configState,
            onSave = { url, password, room, token, deviceName, timeout, interfaces, addresses ->
                model.saveConnectionSettings(url, password, room, token, deviceName, timeout, interfaces, addresses)
            },
            onThemeStyleChange = model::setThemeStyle,
            onThemeModeChange = model::setThemeMode,
            onDynamicColorChange = { model.setDynamicColor(ThemeStyle.fromValue(configState.config.themeStyle), it) },
            onBack = { goBack() },
            onOpenBackground = { navigate("background") },
            onOpenTransfer = { navigate("transfer") },
            onOpenTransport = { navigate("transport") },
            handleBack = route == page,
        )
        page == "transport" -> TransportSettingsScreen(configState, model::saveTransportSettings, { goBack() }, handleBack = route == page)
        page == "background" -> BackgroundScreen(onBack = { goBack() })
        page == "transfer" -> ConfigTransferScreen(configState, onImport = model::importConfig, onBack = { goBack() })
        page == "details" -> RuntimeDetailsScreen(
            snapshot, configState, commandError,
            onBack = { goBack() }, onReconnect = { model.renominateTransports() },
            onExport = {
                report = diagnosticReport(snapshot, configState, readBackgroundInfo(context))
                exportReport.launch("hole-diagnostics.txt")
            }, onBackground = { navigate("background") }, snackbarHostState = snackbarHostState, onTransportSettings = { navigate("transport") },
        )
        page.startsWith("provide") -> ProvideEditScreen(
            entryId = page.removePrefix("provide/").ifEmpty { null },
            configState = configState,
            onSave = { entry ->
                scope.launch {
                    model.upsertProvide(entry).join()
                    if (model.configState.value.lastError == null) goHome() else showNotice("配置保存失败，请重试")
                }
            },
            onDelete = { entryId ->
                deleteProvideWithUndo(entryId)
                goHome()
            },
            onBack = { goBack() },
        )
        page.startsWith("consume") -> ConsumeEditScreen(
            entryId = page.removePrefix("consume/").ifEmpty { null },
            configState = configState,
            onSave = { entry ->
                scope.launch {
                    model.upsertConsume(entry).join()
                    if (model.configState.value.lastError == null) goHome() else showNotice("配置保存失败，请重试")
                }
            },
            onDelete = { entryId ->
                deleteConsumeWithUndo(entryId)
                goHome()
            },
            onBack = { goBack() },
        )
        else -> HomeScreen(
            snapshot = snapshot,
            configState = configState,
            commandError = commandError,
            snackbarHostState = snackbarHostState,
            onOpenSettings = { navigate("settings") },
            onOpenDetails = { navigate("details") },
            onAddProvide = { navigate("provide/") },
            onEditProvide = { entryId -> navigate("provide/$entryId") },
            onAddConsume = { navigate("consume/") },
            onEditConsume = { entryId -> navigate("consume/$entryId") },
            onToggleProvide = { entryId, enabled -> model.toggleProvide(entryId, enabled) },
            onToggleConsume = { entryId, enabled -> model.toggleConsume(entryId, enabled) },
            onDeleteProvide = { entryId -> deleteProvideWithUndo(entryId) },
            onDeleteConsume = { entryId -> deleteConsumeWithUndo(entryId) },
            onToggleRun = onToggleRun,
        )
       }
      }
    }
}
