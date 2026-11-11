package dev.hole.wear

import android.graphics.Bitmap
import android.graphics.HardwareRenderer
import android.graphics.RenderNode
import androidx.activity.ComponentActivity
import androidx.activity.enableEdgeToEdge
import androidx.compose.runtime.*
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import dev.hole.app.ConfigUiState
import dev.hole.app.config.*
import dev.hole.app.ui.*
import dev.hole.corebridge.CoreSnapshot
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
@Config(sdk = [35], qualifiers = "w192dp-h192dp-round-watch-xhdpi")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@LooperMode(LooperMode.Mode.PAUSED)
class WearRenderTest {
    @get:Rule val compose = createAndroidComposeRule<ComponentActivity>()

    @Test fun renderRoundWatchPages() {
        var route by mutableStateOf("home")
        val state = ConfigUiState(loaded = true, config = StoredConfig(
            connection = ConnectionSettings(serverUrl = "wss://example.test/ws", room = "测试房间", deviceName = "手表"),
            provide = listOf(ProvideEntry("ssh", "ssh", "tcp", "127.0.0.1", 22))))
        compose.activity.enableEdgeToEdge()
        compose.setContent { HoleTheme(ThemeMode.DARK, false) {
            FloatingAppFrame(route, { route = it }, CoreSnapshot(), true, {}) {
                when (route) {
                    "home" -> HomeScreen(CoreSnapshot(), state, null, onOpenSettings = {}, onAddProvide = {}, onEditProvide = {},
                        onAddConsume = {}, onEditConsume = {}, onToggleProvide = { _, _ -> }, onToggleConsume = { _, _ -> },
                        onDeleteProvide = {}, onDeleteConsume = {}, onToggleRun = {})
                    "settings" -> SettingsHub {}
                    "voice" -> VoiceScreen(CoreSnapshot(), state, onVoiceEnabledChange = {}, onMutedChange = {}, onRequestMicrophone = {}, onBack = {})
                    "config" -> ConfigOverviewScreen(state, onAddProvide = {}, onEditProvide = {}, onAddConsume = {}, onEditConsume = {},
                        onToggleProvide = { _, _ -> }, onToggleConsume = { _, _ -> }, onDeleteProvide = {}, onDeleteConsume = {}, onBack = {})
                    else -> ProvideEditScreen("ssh", state, {}, {}, {})
                }
            }
        } }
        capture("home")
        compose.mainClock.autoAdvance = false
        compose.onNodeWithTag("wear-navigation-handle").performClick()
        compose.mainClock.advanceTimeByFrame()
        repeat(10) { frame ->
            compose.mainClock.advanceTimeBy(40)
            capture("navigation-opening-%02d".format(frame), settleMillis = 0)
        }
        compose.mainClock.autoAdvance = true
        capture("navigation")
        compose.onNodeWithText("设置").performScrollTo().performClick()
        capture("settings")
        compose.runOnIdle { route = "voice" }
        capture("voice")
        compose.runOnIdle { route = "config" }
        capture("services")
        compose.onNodeWithTag("config-overview").performScrollToNode(hasText("ssh"))
        capture("service-card")
        compose.runOnIdle { route = "provide/ssh" }
        capture("edit")
        compose.onNodeWithContentDescription("保存").performScrollTo()
        capture("save")
    }

    private fun capture(name: String, settleMillis: Long = 500) {
        compose.waitForIdle()
        if (settleMillis > 0) compose.mainClock.advanceTimeBy(settleMillis)
        compose.runOnIdle { compose.activity.window.decorView.invalidate() }
        compose.mainClock.advanceTimeByFrame()
        compose.waitForIdle()
        compose.runOnIdle {
            val view = compose.activity.window.decorView
            val node = RenderNode("wear-preview").apply { setPosition(0, 0, view.width, view.height) }
            view.draw(node.beginRecording(view.width, view.height))
            node.endRecording()
            val hardware = ReflectionHelpers.callStaticMethod<Bitmap>(HardwareRenderer::class.java, "createHardwareBitmap",
                ClassParameter.from(RenderNode::class.java, node),
                ClassParameter.from(Int::class.javaPrimitiveType, view.width),
                ClassParameter.from(Int::class.javaPrimitiveType, view.height))
            val bitmap = requireNotNull(hardware.copy(Bitmap.Config.ARGB_8888, false))
            val directory = File("build/reports/wear-previews").apply { mkdirs() }
            File(directory, "$name.png").outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
            val pixels = IntArray(bitmap.width * bitmap.height)
            bitmap.getPixels(pixels, 0, bitmap.width, 0, 0, bitmap.width, bitmap.height)
            assertTrue(pixels.asSequence().distinct().take(33).count() > 32, "$name should contain a rendered page")
            bitmap.recycle(); hardware.recycle(); node.discardDisplayList()
        }
    }
}
