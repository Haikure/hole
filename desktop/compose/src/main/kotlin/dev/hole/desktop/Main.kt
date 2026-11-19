package dev.hole.desktop

import androidx.compose.foundation.layout.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.key.*
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.*
import dev.hole.app.config.*
import dev.hole.app.ui.*
import dev.hole.desktop.bridge.DesktopCoreClient
import kotlinx.coroutines.*
import java.awt.Dimension

fun main() = application {
    var model by remember { mutableStateOf<DesktopModel?>(null) }
    var startupError by remember { mutableStateOf<String?>(null) }
    var closing by remember { mutableStateOf(false) }
    var confirmClose by remember { mutableStateOf(false) }
    var startup by remember { mutableStateOf<Job?>(null) }
    val scope = rememberCoroutineScope()
    LaunchedEffect(Unit) {
        startup = currentCoroutineContext()[Job]
        try {
            model = withContext(Dispatchers.IO) {
                val storage = DesktopStorage()
                var core: DesktopCoreClient? = null
                try { core = DesktopCoreClient(); DesktopModel(storage, core) }
                catch (failure: Throwable) { core?.close(); storage.close(); throw failure }
            }
        } catch (failure: Throwable) { startupError = failure.message ?: failure.javaClass.simpleName }
    }
    fun exit() {
        if (closing) return
        closing = true
        scope.launch {
            try { startup?.join(); model?.shutdown() } finally { exitApplication() }
        }
    }
    val state = rememberWindowState(width = 1180.dp, height = 820.dp)
    Window(title = "Hole", state = state, icon = painterResource("app-icon.png"), onCloseRequest = {
        if (model?.snapshot?.value?.runRequested == true) confirmClose = true else exit()
    }) {
        LaunchedEffect(Unit) { window.minimumSize = Dimension(760, 560) }
        val current = model
        val config = current?.config?.collectAsState()?.value ?: DesktopState()
        val back = remember { DesktopBackDispatcher() }
        DisposableEffect(current) {
            current?.core?.setTelemetryActive(true)
            onDispose { current?.core?.setTelemetryActive(false) }
        }
        HoleTheme(ThemeMode.fromValue(config.config.themeMode), palette = ThemePalette.fromValue(config.config.materialPalette)) {
            CompositionLocalProvider(LocalDesktopBack provides back) {
                Surface(Modifier.fillMaxSize().onPreviewKeyEvent {
                    it.type == KeyEventType.KeyDown && it.key == Key.Escape && back.back()
                }) {
                    when {
                        closing -> Box(Modifier.padding(32.dp)) { Text("正在停止核心并退出…") }
                        startupError != null -> Column(Modifier.padding(32.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
                            Text("无法启动 Hole", style = MaterialTheme.typography.headlineMedium)
                            Text(startupError!!, color = MaterialTheme.colorScheme.error)
                            HoleButton("退出", ::exit)
                        }
                        current == null -> Box(Modifier.padding(32.dp)) { CircularProgressIndicator() }
                        else -> DesktopApp(current)
                    }
                    if (confirmClose) AlertDialog(onDismissRequest = { confirmClose = false },
                        title = { Text("停止连接并退出？") }, text = { Text("退出会关闭当前转发和语音。") },
                        confirmButton = { TextButton(onClick = { confirmClose = false; exit() }) { Text("退出") } },
                        dismissButton = { TextButton(onClick = { confirmClose = false }) { Text("取消") } })
                }
            }
        }
    }
}
