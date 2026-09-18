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
import dev.hole.app.config.ThemeStyle
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
    @Test fun switchAndDetailsHaveIndependentClickActionsInBothThemes() {
        var theme by mutableStateOf(ThemeStyle.MATERIAL)
        var toggles = 0
        var opened = 0
        compose.setContent {
            HoleTheme(mode = ThemeMode.LIGHT, style = theme, dynamic = false) {
                HomeScreen(
                    snapshot = CoreSnapshot(nativeReady = true, runRequested = true, engineState = "running", signalState = "joined"),
                    configState = ConfigUiState(loaded = true), commandError = null, snackbarHostState = remember { SnackbarHostState() },
                    onOpenSettings = {}, onAddProvide = {}, onEditProvide = {}, onAddConsume = {}, onEditConsume = {},
                    onToggleProvide = { _, _ -> }, onToggleConsume = { _, _ -> }, onDeleteProvide = {}, onDeleteConsume = {},
                    onToggleRun = { toggles++ }, onOpenDetails = { opened++ },
                )
            }
        }
        for ((index, style) in ThemeStyle.entries.withIndex()) {
            compose.runOnIdle { theme = style }
            compose.onNodeWithText("已确认配置").assertDoesNotExist()
            compose.onNodeWithText("#123456").assertDoesNotExist()
            compose.onNodeWithContentDescription("运行转发").performClick()
            compose.runOnIdle { assertEquals(index + 1, toggles); assertEquals(index, opened) }
            compose.onNodeWithText("查看运行详情 ›").performClick()
            compose.runOnIdle { assertEquals(index + 1, toggles); assertEquals(index + 1, opened) }
        }
    }
    @Test fun firstSaveValidatesRatherThanSavingAnInvalidDraft() {
        var saved = 0
        compose.setContent { HoleTheme(mode = ThemeMode.LIGHT, dynamic = false) {
            ProvideEditScreen(null, ConfigUiState(loaded = true, config = StoredConfig()), onSave = { saved++ }, onDelete = {}, onBack = {})
        } }
        compose.onNodeWithText("保存").performClick()
        compose.runOnIdle { assertEquals(0, saved) }
        compose.onNode(hasSetTextAction() and hasText("映射 ID")).performScrollTo().performTextReplacement("ssh")
        compose.onNode(hasSetTextAction() and hasText("服务地址")).performScrollTo().performTextReplacement("127.0.0.1")
        compose.onNode(hasSetTextAction() and hasText("服务端口")).performScrollTo().performTextReplacement("22")
        compose.onNodeWithText("保存").performClick()
        compose.runOnIdle { assertEquals(1, saved) }
    }
    @Test fun enteringAndReturningUseOppositeMotionAndRemoveOldPage() {
        var route by mutableStateOf("home")
        var back by mutableStateOf(false)
        compose.setContent { PageMotion(route, back) { page -> Box(Modifier.fillMaxSize().testTag(page)) { Text(page) } } }
        compose.mainClock.autoAdvance = false
        compose.runOnIdle { route = "details" }
        compose.mainClock.advanceTimeByFrame()
        compose.waitForIdle()
        compose.mainClock.advanceTimeBy(160)
        assertTrue(compose.onNodeWithTag("details").getUnclippedBoundsInRoot().left.value > 0)
        compose.mainClock.advanceTimeBy(400)
        assertEquals(0f, compose.onNodeWithTag("details").getUnclippedBoundsInRoot().left.value)
        compose.runOnIdle { back = true; route = "home" }
        compose.mainClock.advanceTimeByFrame()
        compose.waitForIdle()
        compose.mainClock.advanceTimeBy(160)
        assertTrue(compose.onNodeWithTag("home").getUnclippedBoundsInRoot().left.value < 0)
        compose.mainClock.advanceTimeBy(400)
        compose.onNodeWithTag("details").assertDoesNotExist()
        assertEquals(0f, compose.onNodeWithTag("home").getUnclippedBoundsInRoot().left.value)
    }
}
