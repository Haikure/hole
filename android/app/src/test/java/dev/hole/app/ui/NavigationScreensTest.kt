package dev.hole.app.ui

import androidx.compose.material3.SnackbarHostState
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onAllNodesWithContentDescription
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.performScrollToNode
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import dev.hole.app.ConfigUiState
import dev.hole.app.config.ConnectionSettings
import dev.hole.app.config.ConsumeEntry
import dev.hole.app.config.ProvideEntry
import dev.hole.app.config.StoredConfig
import dev.hole.app.config.ThemeMode
import dev.hole.corebridge.CoreSnapshot
import dev.hole.corebridge.VoiceMemberSnapshot
import dev.hole.corebridge.VoicePeerSnapshot
import dev.hole.corebridge.VoiceSnapshot
import kotlin.test.assertEquals
import kotlin.test.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.LooperMode

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [26])
@LooperMode(LooperMode.Mode.PAUSED)
class NavigationScreensTest {
    @get:Rule val compose = createComposeRule()

    @Test
    fun configOverviewEditsAndTogglesStableEntries() {
        val provide = ProvideEntry("provide-id", "ssh", "tcp", "127.0.0.1", 22)
        val consume = ConsumeEntry("consume-id", "ssh", "127.0.0.1", 2200, enabled = false)
        var edited: String? = null
        var toggled: Pair<String, Boolean>? = null
        compose.setContent {
            HoleTheme(ThemeMode.LIGHT, dynamic = false) {
                ConfigOverviewScreen(
                    configState = ConfigUiState(loaded = true, config = StoredConfig(
                        connection = ConnectionSettings(serverUrl = "wss://example.test/ws"),
                        provide = listOf(provide), consume = listOf(consume),
                    )),
                    onAddProvide = {}, onEditProvide = { edited = it }, onAddConsume = {}, onEditConsume = {},
                    onToggleProvide = { id, enabled -> toggled = id to enabled }, onToggleConsume = { _, _ -> },
                    onDeleteProvide = {}, onDeleteConsume = {}, onBack = {},
                )
            }
        }
        compose.onAllNodesWithText("ssh")[0].performClick()
        compose.onNodeWithText("使").assertDoesNotExist()
        compose.onNodeWithContentDescription("使用服务", useUnmergedTree = true).assertExists()
        compose.runOnIdle { assertEquals("provide-id", edited) }
        compose.onAllNodesWithContentDescription("启用 ssh")[0].performClick()
        compose.runOnIdle { assertEquals("provide-id" to false, toggled) }
        compose.onNodeWithTag("config-overview").performScrollToNode(hasText("监听 127.0.0.1:2200"))
        compose.onNodeWithTag("config-overview").performScrollToNode(hasText("已停用"))
        compose.onNodeWithText("监听 127.0.0.1:2200").assertExists()
        compose.onNodeWithText("已停用").assertExists()
    }

    @Test
    fun voiceScreenShowsSnapshotAndRoutesMuteAction() {
        var muted: Boolean? = null
        val snapshot = CoreSnapshot(
            runRequested = true,
            voice = VoiceSnapshot(
                enabled = true,
                muted = false,
                state = "active",
                members = listOf(VoiceMemberSnapshot("laptop", true, transportState = "connected", mediaState = "active")),
                peers = listOf(VoicePeerSnapshot(peerId = "laptop", state = "active", path = "direct", bitrate = 24000, packetLoss = "0")),
            ),
        )
        compose.setContent {
            HoleTheme(ThemeMode.LIGHT, dynamic = false) {
                VoiceScreen(
                    snapshot = snapshot,
                    configState = ConfigUiState(loaded = true, config = StoredConfig(voice = dev.hole.app.config.VoiceSettings(enabled = true))),
                    microphoneGranted = true,
                    onVoiceEnabledChange = {}, onMutedChange = { muted = it }, onRequestMicrophone = {}, onBack = {},
                )
            }
        }
        compose.onNodeWithText("语音混音运行中").assertExists()
        compose.onNodeWithText("静音").performClick()
        compose.runOnIdle { assertEquals(true, muted) }
        compose.onNodeWithTag("voice-screen").performScrollToNode(hasText("laptop"))
        compose.runOnIdle { assertTrue(compose.onAllNodesWithText("laptop").fetchSemanticsNodes().isNotEmpty()) }
        compose.onNodeWithTag("voice-screen").performScrollToNode(hasText("已连接 · direct · 24000 bit/s"))
        compose.onNodeWithText("已连接 · direct · 24000 bit/s").assertExists()
    }
}
