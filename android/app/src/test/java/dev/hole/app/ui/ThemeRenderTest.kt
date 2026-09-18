package dev.hole.app.ui

import android.graphics.Bitmap
import android.graphics.HardwareRenderer
import android.graphics.RenderNode
import androidx.activity.ComponentActivity
import androidx.test.filters.SdkSuppress
import androidx.compose.material3.SnackbarHostState
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.unit.Density
import dev.hole.app.ConfigUiState
import dev.hole.app.config.ConnectionSettings
import dev.hole.app.config.ConsumeEntry
import dev.hole.app.config.ProvideEntry
import dev.hole.app.config.StoredConfig
import dev.hole.app.config.ThemeMode
import dev.hole.app.config.ThemeStyle
import dev.hole.corebridge.CoreSnapshot
import dev.hole.corebridge.PeerSnapshot
import dev.hole.corebridge.MappingSnapshot
import dev.hole.corebridge.NetworkSnapshot
import java.io.File
import kotlin.test.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import org.robolectric.annotation.LooperMode
import org.robolectric.util.ReflectionHelpers
import org.robolectric.util.ReflectionHelpers.ClassParameter

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w393dp-h851dp-xhdpi")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
class ThemeRenderTest {
    @get:Rule val compose = createAndroidComposeRule<ComponentActivity>()

    @Test
    @SdkSuppress(minSdkVersion = 35)
    fun renderBothThemesAndEditScreens() {
        var style by mutableStateOf(ThemeStyle.MATERIAL)
        var mode by mutableStateOf(ThemeMode.LIGHT)
        var screen by mutableStateOf("settings")
        var fontScale by mutableStateOf(1f)
        val base = StoredConfig(
            connection = ConnectionSettings(serverUrl = "wss://example.test/ws", deviceName = "android-preview"),
            provide = listOf(ProvideEntry("provide-preview", "ssh", "tcp", "127.0.0.1", 22)),
            consume = listOf(ConsumeEntry("consume-preview", "web", "127.0.0.1", 8080)),
        )
        compose.setContent {
            HoleTheme(mode, dynamic = false, style = style) {
                CompositionLocalProvider(LocalDensity provides Density(LocalDensity.current.density, fontScale)) {
                    val configState = ConfigUiState(
                        loaded = true,
                        config = base.copy(themeStyle = style.value, themeMode = mode.value, dynamicColor = false),
                    )
                    when (screen) {
                        "home" -> HomeScreen(
                            snapshot = CoreSnapshot(nativeReady = true, configured = true, runRequested = true, engineState = "running", signalState = "joined",
                                provideCount = 1, consumeCount = 1,
                                mappings = listOf(MappingSnapshot("ssh", "provide", "tcp", "active", tcpSessions = 3), MappingSnapshot("web", "consume", "udp", "active", udpSessions = 2))),
                            configState = configState, commandError = null,
                            snackbarHostState = remember { SnackbarHostState() },
                            onOpenSettings = {}, onAddProvide = {}, onEditProvide = {},
                            onAddConsume = {}, onEditConsume = {}, onToggleProvide = { _, _ -> },
                            onToggleConsume = { _, _ -> }, onDeleteProvide = {}, onDeleteConsume = {}, onToggleRun = {},
                        )
                        "provide" -> ProvideEditScreen("provide-preview", configState, onSave = {}, onDelete = {}, onBack = {})
                        "consume" -> ConsumeEditScreen("consume-preview", configState, onSave = {}, onDelete = {}, onBack = {})
                        "details" -> RuntimeDetailsScreen(
                            snapshot = CoreSnapshot(nativeReady = true, configured = true, runRequested = true, engineState = "running", signalState = "joined", startedAt = java.time.Instant.now().minusSeconds(240).toString(), coreVersion = "fixture-core-version", apiVersion = 1, sessionProtocol = 2,
                                networkChanges = "2", networkBinding = true,
                                peers = listOf(PeerSnapshot(peerId="desktop",transportId="preview-peer",generation="3",profile="ice-quic-mux-v1",state="active",phase="direct",pathType="direct",addressFamily="IPv4",rttMs="12",mappingCount=1,activeChannels=1,bytesSent="2097152",bytesReceived="10485760",localAddress="192.168.1.20:40000",remoteAddress="192.168.1.21:50000")),
                                network = NetworkSnapshot("4294967397", "Wi-Fi", "wlan0", listOf("2001:db8::1234", "192.168.1.22"), listOf("2001:db8::53"), true, true, false),
                                mappings = listOf(MappingSnapshot("ssh", "provide", "tcp", "active", "127.0.0.1:22", "desktop", "[2001:db8::1234]:55140"))),
                            config = configState, commandError = null, onBack = {}, onReconnect = {}, onExport = {}, onBackground = {},
                        )
                        "transport" -> TransportSettingsScreen(configState,onSave={_,_,_,_,_->null},onBack={})
                        "transport-ipv6" -> TransportSettingsScreen(configState.copy(config=configState.config.copy(connection=configState.config.connection.copy(connectionMode="legacy"))),onSave={_,_,_,_,_->null},onBack={})
                        "background" -> BackgroundScreen(onBack = {})
                        "transfer" -> ConfigTransferScreen(configState, onImport = {}, onBack = {})
                        else -> SettingsScreen(
                            configState = configState, onSave = { _, _, _, _, _, _, _, _ -> null },
                            onThemeStyleChange = { style = it }, onThemeModeChange = { mode = it },
                            onDynamicColorChange = {}, onBack = {},
                        )
                    }
                }
            }
        }
        val directory = File("build/outputs/theme-previews").apply { mkdirs() }
        fun capture(name: String) {
            compose.runOnIdle { compose.activity.window.decorView.invalidate() }
            compose.mainClock.advanceTimeByFrame()
            compose.waitForIdle()
            compose.runOnIdle {
                val view = compose.activity.window.decorView
                // Miuix 在 API 33+ 使用 RuntimeShader；记录到硬件 Canvas，避免软件 Canvas 跳过或拒绝着色器。
                val node = RenderNode("theme-preview").apply { setPosition(0, 0, view.width, view.height) }
                view.draw(node.beginRecording(view.width, view.height))
                node.endRecording()
                val hardware = ReflectionHelpers.callStaticMethod<Bitmap>(
                    HardwareRenderer::class.java,
                    "createHardwareBitmap",
                    ClassParameter.from(RenderNode::class.java, node),
                    ClassParameter.from(Int::class.javaPrimitiveType, view.width),
                    ClassParameter.from(Int::class.javaPrimitiveType, view.height),
                )
                val bitmap = requireNotNull(hardware.copy(Bitmap.Config.ARGB_8888, false))
                val pixels = IntArray(bitmap.width * bitmap.height)
                bitmap.getPixels(pixels, 0, bitmap.width, 0, 0, bitmap.width, bitmap.height)
                File(directory, name).outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
                bitmap.recycle()
                hardware.recycle()
                node.discardDisplayList()
                assertTrue(pixels.asSequence().distinct().take(33).count() > 32, "$name 应包含完整页面，而非空白截图")
            }
        }
        for (theme in ThemeStyle.entries) {
            for (appearance in listOf(ThemeMode.LIGHT, ThemeMode.DARK)) {
                for (page in listOf("settings", "home", "provide", "consume", "details", "background", "transfer", "transport", "transport-ipv6")) {
                    compose.runOnIdle { style = theme; mode = appearance; screen = page }
                    compose.waitForIdle()
                    capture("${theme.value}-${appearance.value}-$page.png")
                }
            }
        }
        compose.runOnIdle { style = ThemeStyle.MIUIX; mode = ThemeMode.LIGHT; screen = "settings"; fontScale = 2f }
        compose.waitForIdle()
        capture("miuix-large-font-settings.png")
        compose.runOnIdle { screen = "details" }
        compose.waitForIdle()
        capture("miuix-large-font-details.png")
        compose.runOnIdle { screen = "transport" }
        compose.waitForIdle()
        capture("miuix-large-font-transport.png")
        compose.runOnIdle { screen = "home" }
        compose.waitForIdle()
        capture("miuix-large-font-home.png")
    }
}
