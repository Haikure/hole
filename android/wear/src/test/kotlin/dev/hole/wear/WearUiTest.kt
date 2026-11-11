package dev.hole.wear

import android.os.SystemClock
import android.view.InputDevice
import android.view.MotionEvent
import androidx.activity.ComponentActivity
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.unit.Density
import dev.hole.app.ConfigUiState
import dev.hole.app.config.*
import dev.hole.app.ui.*
import dev.hole.corebridge.CoreSnapshot
import dev.hole.corebridge.VoiceSnapshot
import kotlin.test.*
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.LooperMode

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w192dp-h192dp-round-watch-mdpi")
@LooperMode(LooperMode.Mode.PAUSED)
class WearUiTest {
    @get:Rule val compose = createAndroidComposeRule<ComponentActivity>()

    @Test fun drawerRetainsAllDestinationsRunControlAndBackDismissal() {
        var route by mutableStateOf("home")
        var run by mutableStateOf(false)
        compose.setContent {
            HoleTheme(ThemeMode.DARK, false) {
                FloatingAppFrame(route, { route = it }, CoreSnapshot(runRequested = run), true, { run = it }) {
                    HoleScaffold("页面") { HoleScrollColumn { Text("内容") } }
                }
            }
        }
        compose.onNodeWithTag("navigation-track").assertDoesNotExist()
        for ((label, target) in listOf("服务" to "config", "连接" to "details", "语音" to "voice", "设置" to "settings", "首页" to "home")) {
            compose.onNodeWithTag("wear-navigation-handle").performClick()
            compose.onNodeWithText(label).performScrollTo().performClick()
            compose.runOnIdle { assertEquals(target, route) }
            compose.onNodeWithTag("wear-navigation-handle").assertIsDisplayed()
        }
        compose.onNodeWithTag("wear-navigation-handle").performClick()
        compose.onNodeWithText("启动转发").performClick()
        compose.runOnIdle { assertTrue(run) }
        compose.onNodeWithText("停止转发").performClick()
        compose.runOnIdle { assertFalse(run); compose.activity.onBackPressedDispatcher.onBackPressed() }
        compose.onNodeWithTag("wear-navigation").assertDoesNotExist()
    }

    @Test fun handleOpensByPullingAndDrawerClosesByDraggingDown() {
        compose.setContent { HoleTheme(ThemeMode.DARK, false) {
            FloatingAppFrame("home", {}, CoreSnapshot(), true, {}) { Box(Modifier.fillMaxSize()) }
        } }
        compose.onNodeWithTag("wear-navigation-handle").performTouchInput { swipeUp() }
        compose.onNodeWithTag("wear-navigation").assertExists()
        compose.onNodeWithContentDescription("收起导航").performTouchInput { swipeDown() }
        compose.onNodeWithTag("wear-navigation").assertDoesNotExist()
    }

    @Test fun crownScrollsPageAndDrawerWithoutMovingUnderlyingContent() {
        lateinit var page: androidx.compose.foundation.ScrollState
        compose.setContent { HoleTheme(ThemeMode.DARK, false) {
            page = rememberScrollState()
            FloatingAppFrame("home", {}, CoreSnapshot(), true, {}) {
                HoleScrollColumn(Modifier.fillMaxSize(), state = page) { repeat(30) { Text("项目 $it") } }
            }
        } }
        rotate(-3f)
        compose.runOnIdle { assertTrue(page.value > 0, "表冠应滚动当前页面") }
        val position = compose.runOnIdle { page.value }
        compose.onNodeWithTag("wear-navigation-handle").performClick()
        val before = compose.onNodeWithText("首页").getUnclippedBoundsInRoot().left
        rotate(-2f)
        assertTrue(compose.onNodeWithText("首页").getUnclippedBoundsInRoot().left < before)
        compose.runOnIdle { assertEquals(position, page.value); compose.activity.onBackPressedDispatcher.onBackPressed() }
        compose.waitForIdle()
        rotate(2f)
        compose.runOnIdle { assertTrue(page.value < position, "收起后应恢复页面表冠焦点") }
    }

    @Test
    @Config(qualifiers = "w180dp-h180dp-round-watch-mdpi")
    fun serviceFormPreservesEditingValidationAndSaveOnSmallRoundScreen() {
        var saved: ProvideEntry? = null
        compose.setContent { HoleTheme(ThemeMode.DARK, false) {
            ProvideEditScreen(null, ConfigUiState(loaded = true), { saved = it }, {}, {})
        } }
        compose.onNodeWithContentDescription("保存").performScrollTo().performClick()
        compose.runOnIdle { assertNull(saved) }
        for ((label, value) in listOf("映射 ID" to "ssh", "服务地址" to "127.0.0.1", "服务端口" to "22")) {
            compose.onNodeWithContentDescription(label).performScrollTo().performTextReplacement(value)
        }
        compose.onNodeWithContentDescription("保存").performScrollTo().performClick()
        compose.runOnIdle { assertEquals("ssh", saved?.id); assertEquals(22, saved?.port) }
    }

    @Test fun voiceControlsAndChoicesStayUsableWithLargerText() {
        var source = ""
        var muted = false
        var enabled = true
        compose.setContent { HoleTheme(ThemeMode.DARK, false) {
            CompositionLocalProvider(LocalDensity provides Density(LocalDensity.current.density, 1.3f)) {
                VoiceScreen(CoreSnapshot(runRequested = true, voice = VoiceSnapshot(enabled = true)),
                    ConfigUiState(loaded = true, config = StoredConfig(voice = VoiceSettings(enabled = true))),
                    microphoneGranted = true, onVoiceEnabledChange = { enabled = it },
                    onMicrophoneSourceChange = { source = it }, onMutedChange = { muted = it }, onRequestMicrophone = {}, onBack = {})
            }
        } }
        compose.onNodeWithContentDescription("启用房间语音").performScrollTo().performClick()
        compose.onNodeWithTag("microphone-source").performScrollTo()
        compose.onNodeWithText("耳机").performScrollTo().assertIsDisplayed().performClick()
        compose.onNodeWithText("静音").performScrollTo().performClick()
        compose.runOnIdle { assertFalse(enabled); assertEquals("headset", source); assertTrue(muted) }
        val mute = compose.onNodeWithText("静音").assertIsDisplayed().fetchSemanticsNode()
        val screenWidth = compose.onRoot().fetchSemanticsNode().size.width
        assertTrue(mute.positionInRoot.x >= 0 && mute.positionInRoot.x + mute.size.width <= screenWidth)
    }

    @Test fun relayOrderCanBeReorderedDisabledAndEnabled() {
        var order by mutableStateOf(listOf("hole", "udp", "tcp", "tls"))
        compose.setContent { HoleTheme(ThemeMode.DARK, false) {
            HoleScrollColumn { TurnOrderBoard(order, { order = it }) }
        } }
        compose.onNodeWithText("上移 UDP").performScrollTo().performClick()
        compose.runOnIdle { assertEquals(listOf("udp", "hole", "tcp", "tls"), order) }
        compose.onNodeWithText("下移 UDP").performScrollTo().performClick()
        compose.onNodeWithText("停用 TLS").performScrollTo().performClick()
        compose.runOnIdle { assertEquals(listOf("hole", "udp", "tcp"), order) }
        compose.onNodeWithText("启用 TLS").performScrollTo().performClick()
        compose.runOnIdle { assertEquals(listOf("hole", "udp", "tcp", "tls"), order) }
    }

    @Test fun longHelpRemainsScrollableAndDismissible() {
        compose.setContent { HoleTheme(ThemeMode.DARK, false) {
            HoleScrollColumn { HoleInfoButton("说明") { repeat(20) { Text("说明段落 $it") } } }
        } }
        compose.onNodeWithContentDescription("说明 说明").performScrollTo().performClick()
        compose.onNodeWithText("说明段落 19").performScrollTo().assertIsDisplayed()
        compose.onNodeWithText("知道了").performScrollTo().performClick()
        compose.onNodeWithText("说明段落 19").assertDoesNotExist()
    }

    @Test fun deviceSearchCanFilterAndClearOnWatch() {
        val peers = listOf(
            dev.hole.corebridge.PeerSnapshot(peerId = "watch-peer", transportId = "one"),
            dev.hole.corebridge.PeerSnapshot(peerId = "other", transportId = "two"),
        )
        compose.setContent { HoleTheme(ThemeMode.DARK, false) {
            RuntimeDetailsScreen(CoreSnapshot(peers = peers), ConfigUiState(loaded = true), null,
                onBack = {}, onReconnect = {}, onExport = {}, onBackground = {})
        } }
        compose.onNodeWithTag("connection-details").performScrollToNode(hasContentDescription("搜索设备或地址"))
        compose.onNodeWithContentDescription("搜索设备或地址").performTextReplacement("watch")
        compose.onNodeWithTag("connection-details").performScrollToNode(hasText("1 台设备"))
        compose.onNodeWithText("1 台设备").assertIsDisplayed()
        compose.onNodeWithTag("connection-details").performScrollToNode(hasText("清除搜索"))
        compose.onNodeWithText("清除搜索").performClick()
        compose.onNodeWithTag("connection-details").performScrollToNode(hasText("2 台设备"))
        compose.onNodeWithText("2 台设备").assertIsDisplayed()
    }

    private fun rotate(amount: Float) {
        compose.runOnIdle {
            val now = SystemClock.uptimeMillis()
            val event = MotionEvent.obtain(now, now, MotionEvent.ACTION_SCROLL, 1,
                arrayOf(MotionEvent.PointerProperties().apply { id = 0 }),
                arrayOf(MotionEvent.PointerCoords().apply { setAxisValue(MotionEvent.AXIS_SCROLL, amount) }),
                0, 0, 1f, 1f, 0, 0, InputDevice.SOURCE_ROTARY_ENCODER, 0)
            try { compose.activity.dispatchGenericMotionEvent(event) } finally { event.recycle() }
        }
        compose.waitForIdle()
    }
}
