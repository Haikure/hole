package dev.hole.app.ui

import androidx.compose.runtime.*
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.clickable
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.unit.dp
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.v2.createComposeRule
import dev.hole.app.config.ThemeMode
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
class AppNavigationTest {
    @get:Rule val compose = createComposeRule()

    @Test fun allPrimaryPagesAreDirectDestinations() {
        var route by mutableStateOf("home")
        compose.setContent { HoleTheme(ThemeMode.LIGHT, false) { AppNavigation(route) { route = it } } }
        listOf("服务" to "config", "连接" to "details", "语音" to "voice", "设置" to "settings", "首页" to "home").forEach { (label, target) ->
            compose.onNodeWithText(label).performClick().assertIsSelected()
            compose.runOnIdle { assertEquals(target, route) }
        }
    }

    @Test fun draggingTheLensCommitsDestinationOnRelease() {
        var route by mutableStateOf("home")
        compose.setContent { HoleTheme(ThemeMode.LIGHT, false) {
            Box(Modifier.width(360.dp)) { AppNavigation(route) { route = it } }
        } }
        compose.onNodeWithTag("navigation-track").performTouchInput {
            down(Offset(width * .1f, centerY))
            moveTo(Offset(width * .7f, centerY), 240)
        }
        compose.runOnIdle { assertEquals("home", route) }
        compose.onNodeWithTag("navigation-track").performTouchInput { advanceEventTime(200); moveTo(Offset(width * .7f, centerY)); up() }
        compose.onNodeWithText("语音").assertIsSelected()
        compose.runOnIdle { assertEquals("voice", route) }
    }

    @Test fun cancelledDragReturnsToTheCurrentPage() {
        var route by mutableStateOf("home")
        compose.setContent { HoleTheme(ThemeMode.LIGHT, false) { AppNavigation(route) { route = it } } }
        compose.onNodeWithTag("navigation-track").performTouchInput {
            down(Offset(width * .1f, centerY))
            moveTo(Offset(width * .9f, centerY), 240)
            cancel()
        }
        compose.runOnIdle { assertEquals("home", route) }
        compose.onNodeWithText("首页").assertIsSelected()
    }

    @Test fun protocolSegmentsFillTheAvailableWidth() {
        compose.setContent { HoleTheme(ThemeMode.LIGHT, false) {
            Box(Modifier.width(320.dp)) { HoleSingleChoice(listOf("tcp" to "TCP", "udp" to "UDP"), "tcp", {}) }
        } }
        val track = compose.onNodeWithTag("segmented-control").getUnclippedBoundsInRoot()
        val tcp = compose.onNodeWithText("TCP").getUnclippedBoundsInRoot()
        val udp = compose.onNodeWithText("UDP").getUnclippedBoundsInRoot()
        assertEquals((track.right - track.left).value, (tcp.right - tcp.left + udp.right - udp.left).value, 1f)
        assertEquals((tcp.right - tcp.left).value, (udp.right - udp.left).value, 1f)
    }

    @Test fun floatingControlsDoNotReserveAnOpaqueBottomStrip() {
        var backgroundTaps = 0
        compose.setContent { HoleTheme(ThemeMode.LIGHT, false) {
            FloatingAppFrame("home", {}, dev.hole.corebridge.CoreSnapshot(), true, {}) {
                Box(Modifier.fillMaxSize().testTag("page-under-dock").clickable { backgroundTaps++ })
            }
        } }
        val page = compose.onNodeWithTag("page-under-dock").getUnclippedBoundsInRoot()
        val dock = compose.onNodeWithTag("navigation-track").getUnclippedBoundsInRoot()
        kotlin.test.assertTrue(page.bottom > dock.bottom)
        compose.onNodeWithTag("page-under-dock").performTouchInput { click(Offset(2f, height - 2f)) }
        compose.runOnIdle { assertEquals(1, backgroundTaps) }
    }

    @Test fun fieldHelpOpensOnTapAndErrorsRemainInline() {
        var invalid by mutableStateOf(false)
        compose.setContent { HoleTheme(ThemeMode.LIGHT, false) {
            HoleTextField("", {}, "服务器", supportingText = {
                androidx.compose.material3.Text(if (invalid) "地址无效" else "输入完整 WebSocket 地址")
            }, isError = invalid)
        } }
        compose.onNodeWithText("输入完整 WebSocket 地址").assertDoesNotExist()
        compose.onNodeWithContentDescription("服务器 说明").performClick()
        compose.onNodeWithText("输入完整 WebSocket 地址").assertIsDisplayed()
        compose.onNodeWithText("知道了").performClick()
        compose.onNodeWithText("输入完整 WebSocket 地址").assertDoesNotExist()
        compose.runOnIdle { invalid = true }
        compose.onNodeWithText("地址无效").assertIsDisplayed()
    }

    @Test fun runButtonOnlyAppearsAtHomeAndItsSlotAnimates() {
        var route by mutableStateOf("home")
        var toggles = 0
        compose.setContent { HoleTheme(ThemeMode.LIGHT, false) {
            FloatingAppFrame(route, { route = it }, dev.hole.corebridge.CoreSnapshot(runRequested = true), true, { toggles++ }) {
                Box(Modifier.fillMaxSize())
            }
        } }
        val homeWidth = compose.onNodeWithTag("navigation-track").getUnclippedBoundsInRoot().let { it.right - it.left }
        compose.onNodeWithTag("run-control").assertExists()
        compose.mainClock.autoAdvance = false
        compose.runOnIdle { route = "config" }
        compose.mainClock.advanceTimeByFrame()
        compose.mainClock.advanceTimeBy(100)
        compose.onNodeWithTag("run-control").assertExists()
        compose.mainClock.advanceTimeBy(500)
        compose.onNodeWithTag("run-control").assertDoesNotExist()
        val awayWidth = compose.onNodeWithTag("navigation-track").getUnclippedBoundsInRoot().let { it.right - it.left }
        kotlin.test.assertTrue(awayWidth > homeWidth)
        for (destination in listOf("details", "voice", "settings", "provide/")) {
            compose.runOnIdle { route = destination }
            compose.mainClock.advanceTimeBy(600)
            compose.onNodeWithTag("run-control").assertDoesNotExist()
        }
        compose.runOnIdle { route = "home" }
        compose.mainClock.advanceTimeByFrame()
        compose.waitForIdle()
        compose.mainClock.advanceTimeBy(600)
        compose.onNodeWithTag("run-control").assertExists().performClick()
        compose.runOnIdle { assertEquals(1, toggles) }
        compose.mainClock.autoAdvance = true
    }
}
