package dev.hole.app.ui

import androidx.compose.runtime.*
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.v2.createComposeRule
import dev.hole.app.config.ThemeMode
import dev.hole.corebridge.CoreSnapshot
import dev.hole.corebridge.MappingSnapshot
import kotlin.test.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.LooperMode

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [26])
@LooperMode(LooperMode.Mode.PAUSED)
class HomeConnectionStatusTest {
    @get:Rule val compose = createComposeRule()

    @Test fun signalStateIsNotOverriddenByRetainedServiceStatistics() {
        val running = CoreSnapshot(runRequested = true, engineState = "running", signalState = "joined",
            mappings = listOf(MappingSnapshot("ssh", "provide", "tcp", "active")))
        assertEquals(HomeConnectionPhase.CONNECTED, homeConnectionPhase(running))
        assertEquals(HomeConnectionPhase.SIGNAL, homeConnectionPhase(running.copy(signalState = "connecting")))
        assertEquals(HomeConnectionPhase.JOINING, homeConnectionPhase(running.copy(signalState = "joining")))
        assertEquals(HomeConnectionPhase.RECONNECTING, homeConnectionPhase(running.copy(signalState = "reconnecting")))
        assertEquals(HomeConnectionPhase.OFFLINE, homeConnectionPhase(running.copy(runRequested = false)))
        assertEquals(HomeConnectionPhase.ERROR, homeConnectionPhase(running.copy(signalState = "error")))
        assertEquals(HomeConnectionPhase.WAITING_NETWORK, homeConnectionPhase(running.copy(engineState = "recovering")))
        assertEquals(HomeConnectionPhase.STOPPING, homeConnectionPhase(running.copy(runRequested = false, engineState = "stopping")))
    }

    @Test fun statusChangesAreVisibleAndAnnouncedWithoutAServiceConnectionClaim() {
        var snapshot by mutableStateOf(CoreSnapshot(engineState = "stopped"))
        compose.setContent { HoleTheme(ThemeMode.LIGHT, false) { HomeConnectionStatus(snapshot) } }
        compose.onNodeWithText("未连接").assertIsDisplayed()
        compose.runOnIdle { snapshot = CoreSnapshot(runRequested = true, engineState = "running", signalState = "connecting") }
        compose.onNodeWithText("正在连接信令").assertIsDisplayed()
        compose.onNodeWithText("已连接").assertDoesNotExist()
        compose.runOnIdle { snapshot = snapshot.copy(signalState = "joined") }
        compose.onNodeWithText("已连接").assertIsDisplayed()
        compose.onNodeWithText("已加入房间 · 等待对端").assertIsDisplayed()
        compose.onNodeWithTag("home-connection-status").assert(SemanticsMatcher.expectValue(SemanticsProperties.StateDescription, "已连接"))
    }
}
