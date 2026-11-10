package dev.hole.app.ui

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.Text
import androidx.compose.material3.SnackbarHostState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.v2.createComposeRule
import dev.hole.app.ConfigUiState
import dev.hole.app.config.StoredConfig
import dev.hole.app.config.ThemeMode
import dev.hole.corebridge.CoreSnapshot
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
class RuntimeInteractionTest {
    @get:Rule val compose = createComposeRule()
    @Test fun dashboardStartsServiceWithoutOpeningDetails() {
        var toggles = 0
        var opened = 0
        var destination = ""
        compose.setContent {
            HoleTheme(mode = ThemeMode.LIGHT, dynamic = false) {
              FloatingAppFrame("home", {}, CoreSnapshot(runRequested = true), true, { toggles++ }) {
                HomeScreen(
                    snapshot = CoreSnapshot(nativeReady = true, runRequested = true, engineState = "running", signalState = "joined"),
                    configState = ConfigUiState(loaded = true), commandError = null, snackbarHostState = remember { SnackbarHostState() },
                    onOpenSettings = {}, onAddProvide = {}, onEditProvide = {}, onAddConsume = {}, onEditConsume = {},
                    onToggleProvide = { _, _ -> }, onToggleConsume = { _, _ -> }, onDeleteProvide = {}, onDeleteConsume = {},
                    onToggleRun = { toggles++ }, onOpenDetails = { opened++ },
                    onNavigate = { destination = it },
                )
              }
            }
        }
        compose.onNodeWithText("已确认配置").assertDoesNotExist()
        compose.onNodeWithText("#123456").assertDoesNotExist()
        compose.onNodeWithTag("run-control").performClick()
        compose.runOnIdle { assertEquals(1, toggles); assertEquals(0, opened) }
        compose.onNodeWithText("查看运行详情 ›").assertDoesNotExist()
        compose.onNodeWithText("TCP 会话").assertExists()
        compose.onNodeWithText("NAT 检测").performClick()
        compose.runOnIdle { assertEquals("nat-detection", destination) }
    }
    @Test fun firstSaveValidatesRatherThanSavingAnInvalidDraft() {
        var saved = 0
        compose.setContent { HoleTheme(mode = ThemeMode.LIGHT, dynamic = false) {
            ProvideEditScreen(null, ConfigUiState(loaded = true, config = StoredConfig()), onSave = { saved++ }, onDelete = {}, onBack = {})
        } }
        compose.onNodeWithContentDescription("保存").performClick()
        compose.runOnIdle { assertEquals(0, saved) }
        compose.onNode(hasSetTextAction() and hasText("映射 ID")).performScrollTo().performTextReplacement("ssh")
        compose.onNode(hasSetTextAction() and hasText("服务地址")).performScrollTo().performTextReplacement("127.0.0.1")
        compose.onNode(hasSetTextAction() and hasText("服务端口")).performScrollTo().performTextReplacement("22")
        compose.onNodeWithContentDescription("保存").performClick()
        compose.runOnIdle { assertEquals(1, saved) }
    }
    @Test fun enteringAndReturningUseOppositeMotionAndRemoveOldPage() {
        var route by mutableStateOf("home")
        var back by mutableStateOf(false)
        compose.setContent { PageMotion(route, back) { page -> Box(Modifier.fillMaxSize().testTag(page)) { Text(page) } } }
        compose.mainClock.autoAdvance = false
        compose.runOnIdle { route = "provide/" }
        compose.mainClock.advanceTimeByFrame()
        compose.waitForIdle()
        compose.mainClock.advanceTimeBy(160)
        assertTrue(compose.onNodeWithTag("provide/").getUnclippedBoundsInRoot().left.value > 0)
        compose.mainClock.advanceTimeBy(400)
        assertEquals(0f, compose.onNodeWithTag("provide/").getUnclippedBoundsInRoot().left.value)
        compose.runOnIdle { back = true; route = "home" }
        compose.mainClock.advanceTimeByFrame()
        compose.waitForIdle()
        compose.mainClock.advanceTimeBy(160)
        assertTrue(compose.onNodeWithTag("home").getUnclippedBoundsInRoot().left.value < 0)
        compose.mainClock.advanceTimeBy(400)
        compose.onNodeWithTag("provide/").assertDoesNotExist()
        assertEquals(0f, compose.onNodeWithTag("home").getUnclippedBoundsInRoot().left.value)
    }
    @Test fun rapidPrimaryPageSwitchesSettleOnTheLatestDestination() {
        var route by mutableStateOf("home")
        compose.setContent { PageMotion(route, false) { page -> Box(Modifier.fillMaxSize().testTag(page)) { Text(page) } } }
        compose.mainClock.autoAdvance = false
        for (destination in listOf("details", "settings", "config", "voice", "home")) {
            compose.runOnIdle { route = destination }
            compose.mainClock.advanceTimeByFrame()
            compose.mainClock.advanceTimeBy(48)
        }
        compose.mainClock.advanceTimeBy(400)
        compose.onNodeWithTag("home").assertIsDisplayed()
        for (destination in primaryRoutes - "home") compose.onNodeWithTag(destination).assertDoesNotExist()
    }
}
