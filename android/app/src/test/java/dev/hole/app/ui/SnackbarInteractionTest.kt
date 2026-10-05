package dev.hole.app.ui

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.SnackbarResult
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.MutableState
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.platform.LocalAccessibilityManager
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.v2.createComposeRule
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
class SnackbarInteractionTest {
    @get:Rule val compose = createComposeRule()

    private fun showPrompts(
        messages: List<String>,
        results: MutableList<SnackbarResult>,
        route: MutableState<String> = mutableStateOf("config"),
    ) {
        val host = SnackbarHostState()
        compose.setContent {
            CompositionLocalProvider(LocalAccessibilityManager provides null) {
                HoleTheme(ThemeMode.LIGHT, dynamic = false) {
                    FloatingAppFrame(route.value, { route.value = it }, CoreSnapshot(), true, {}, snackbarHostState = host) {
                        PageMotion(route.value, false) { page ->
                            HoleScaffold(title = "page-$page") { insets ->
                                Box(Modifier.fillMaxSize().padding(insets))
                            }
                        }
                    }
                    LaunchedEffect(Unit) {
                        for (message in messages) results += host.showUndoSnackbar(message)
                    }
                }
            }
        }
        compose.onNodeWithText(messages.first()).assertIsDisplayed()
        compose.mainClock.autoAdvance = false
    }

    private fun assertAboveDock() {
        val prompt = compose.onNodeWithTag("snackbar-swipe").getUnclippedBoundsInRoot()
        val dock = compose.onNodeWithTag("navigation-track").getUnclippedBoundsInRoot()
        assertTrue(prompt.bottom < dock.top, "撤销提示应位于悬浮导航栏上方")
    }

    @Test fun promptStaysAboveDockAndUndoWorksAcrossPageChanges() {
        val route = mutableStateOf("config")
        val results = mutableListOf<SnackbarResult>()
        showPrompts(listOf("已删除「ssh」"), results, route)
        assertAboveDock()
        for (page in listOf("home", "provide/example", "config")) {
            compose.runOnIdle { route.value = page }
            compose.mainClock.advanceTimeByFrame()
            compose.waitForIdle()
            compose.mainClock.advanceTimeBy(600)
            compose.waitForIdle()
            compose.onNodeWithText("page-$page").assertIsDisplayed()
            compose.onNodeWithText("已删除「ssh」").assertIsDisplayed()
            if (page in primaryRoutes) assertAboveDock()
        }
        compose.onNodeWithText("撤销").performClick()
        compose.mainClock.advanceTimeBy(600)
        compose.onNodeWithText("已删除「ssh」").assertDoesNotExist()
        compose.runOnIdle { assertEquals(listOf(SnackbarResult.ActionPerformed), results) }
    }

    @Test fun swipingEitherWayDismissesAndTheNextPromptCanStillUndo() {
        val results = mutableListOf<SnackbarResult>()
        showPrompts(listOf("已删除「ssh」", "已删除「web」", "已删除「dns」"), results)
        compose.onNodeWithTag("snackbar-swipe").performTouchInput { swipeLeft() }
        compose.mainClock.advanceTimeBy(700)
        compose.onNodeWithText("已删除「ssh」").assertDoesNotExist()
        compose.onNodeWithText("已删除「web」").assertIsDisplayed()
        compose.onNodeWithTag("snackbar-swipe").performTouchInput { swipeRight() }
        compose.mainClock.advanceTimeBy(700)
        compose.onNodeWithText("已删除「web」").assertDoesNotExist()
        compose.onNodeWithText("已删除「dns」").assertIsDisplayed()
        compose.onNodeWithText("撤销").performClick()
        compose.mainClock.advanceTimeBy(600)
        compose.runOnIdle {
            assertEquals(listOf(SnackbarResult.Dismissed, SnackbarResult.Dismissed, SnackbarResult.ActionPerformed), results)
        }
    }

    @Test fun shortPromptExpiresWithoutPerformingUndo() {
        val results = mutableListOf<SnackbarResult>()
        showPrompts(listOf("已删除「ssh」"), results)
        compose.mainClock.advanceTimeBy(2500)
        compose.onNodeWithText("已删除「ssh」").assertIsDisplayed()
        compose.runOnIdle { assertTrue(results.isEmpty()) }
        compose.mainClock.advanceTimeBy(2000)
        compose.onNodeWithText("已删除「ssh」").assertDoesNotExist()
        compose.runOnIdle { assertEquals(listOf(SnackbarResult.Dismissed), results) }
    }

    @Test fun aShortDragReturnsThePromptAndLeavesUndoAvailable() {
        val results = mutableListOf<SnackbarResult>()
        showPrompts(listOf("已删除「ssh」"), results)
        compose.onNodeWithTag("snackbar-swipe").performTouchInput {
            val start = center
            val end = start + Offset(width * .08f, 0f)
            down(start)
            moveTo(end, 200)
            advanceEventTime(240)
            moveTo(end)
            up()
        }
        compose.mainClock.advanceTimeBy(700)
        compose.onNodeWithText("已删除「ssh」").assertIsDisplayed()
        compose.runOnIdle { assertTrue(results.isEmpty()) }
        compose.onNodeWithText("撤销").performClick()
        compose.runOnIdle { assertEquals(listOf(SnackbarResult.ActionPerformed), results) }
    }
}
