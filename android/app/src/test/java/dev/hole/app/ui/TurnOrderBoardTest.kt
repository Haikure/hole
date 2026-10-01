package dev.hole.app.ui

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTouchInput
import dev.hole.app.config.ThemeMode
import dev.hole.app.config.ThemeStyle
import androidx.compose.ui.unit.dp
import kotlin.test.assertEquals
import kotlin.test.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.LooperMode

class TurnOrderBoardTest {
    private fun chip(cx: Float, cy: Float) = Rect(cx - 100f, cy - 20f, cx + 100f, cy + 20f)

    @Test
    fun labelsMatchTokensWithoutCustomPortSuffix() {
        assertEquals("UDP", turnOrderTypeLabel("udp"))
        assertEquals("TCP", turnOrderTypeLabel("tcp_80"))
        assertEquals("TCP", turnOrderTypeLabel("tcp"))
        assertEquals("TLS", turnOrderTypeLabel("tls_443"))
        assertEquals("TLS", turnOrderTypeLabel("tls"))
        assertEquals("unknown", turnOrderTypeLabel("unknown"))
        assertTrue(turnOrderTypes.toList() == listOf("udp", "tcp", "tls"))
    }

    @Test
    fun orderSummaryKeepsShortPortLabels() {
        assertEquals("默认：UDP → TCP → TLS", relayOrderLabel(emptyList()))
        assertEquals("TLS → UDP", relayOrderLabel(listOf("tls", "udp")))
    }

    @Test
    fun insertIndexFollowsRowThenColumnOrder() {
        val bounds = mapOf("a" to chip(100f, 50f), "b" to chip(300f, 50f), "c" to chip(500f, 50f))
        assertEquals(0, turnOrderInsertIndex(listOf("a", "b", "c"), "x", bounds, Offset(0f, 50f), 20f))
        assertEquals(1, turnOrderInsertIndex(listOf("a", "b", "c"), "x", bounds, Offset(200f, 50f), 20f))
        assertEquals(3, turnOrderInsertIndex(listOf("a", "b", "c"), "x", bounds, Offset(600f, 50f), 20f))
        // 同行判定允许半行高以内的纵向误差。
        assertEquals(0, turnOrderInsertIndex(listOf("a", "b", "c"), "x", bounds, Offset(0f, 60f), 20f))
        // 落点在整行上下时按行比较。
        assertEquals(3, turnOrderInsertIndex(listOf("a", "b", "c"), "x", bounds, Offset(0f, 200f), 20f))
        assertEquals(0, turnOrderInsertIndex(listOf("a", "b", "c"), "x", bounds, Offset(0f, -100f), 20f))
    }

    @Test
    fun insertIndexSkipsTheDraggedToken() {
        val bounds = mapOf("a" to chip(100f, 50f), "b" to chip(300f, 50f), "c" to chip(500f, 50f))
        assertEquals(0, turnOrderInsertIndex(listOf("a", "b", "c"), "b", bounds, Offset(50f, 50f), 20f))
        assertEquals(1, turnOrderInsertIndex(listOf("a", "b", "c"), "b", bounds, Offset(400f, 50f), 20f))
    }

    @Test
    fun insertIndexCountsEarlierRowsFully() {
        val bounds = mapOf("a" to chip(100f, 50f), "b" to chip(300f, 50f), "c" to chip(100f, 150f), "d" to chip(300f, 150f))
        // 两行之间：第一行整体在前，第二行不参与列比较。
        assertEquals(2, turnOrderInsertIndex(listOf("a", "b", "c", "d"), "x", bounds, Offset(200f, 100f), 20f))
        assertEquals(2, turnOrderInsertIndex(listOf("a", "b", "c", "d"), "x", bounds, Offset(0f, 150f), 20f))
    }
}

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [26])
@LooperMode(LooperMode.Mode.PAUSED)
class TurnOrderBoardInteractionTest {
    @get:Rule val compose = createComposeRule()

    @Test
    fun tapTogglesTypesInBothThemes() {
        var theme by mutableStateOf(ThemeStyle.MATERIAL)
        val order = mutableStateOf(emptyList<String>())
        compose.setContent {
            HoleTheme(mode = ThemeMode.LIGHT, style = theme, dynamic = false) {
                TurnOrderBoard(order = order.value, onChange = { order.value = it })
            }
        }
        for (style in ThemeStyle.entries) {
            compose.runOnIdle { theme = style }
            compose.waitForIdle()
            compose.onNodeWithText("TLS").performClick()
            compose.runOnIdle { assertEquals(listOf("tls"), order.value) }
            compose.onNodeWithText("TLS").performClick()
            compose.runOnIdle { assertEquals(emptyList<String>(), order.value) }
        }
    }

    @Test
    fun longPressDragMovesPoolTypeAboveDivider() {
        val order = mutableStateOf(listOf("udp", "tcp"))
        compose.setContent {
            HoleTheme(mode = ThemeMode.LIGHT, dynamic = false) {
                TurnOrderBoard(order = order.value, onChange = { order.value = it })
            }
        }
        compose.waitForIdle()
        compose.onNodeWithText("TLS").performTouchInput {
            down(center)
            advanceEventTime(viewConfiguration.longPressTimeoutMillis + 100)
            moveBy(Offset(0f, -1200f), delayMillis = 80)
            up()
        }
        compose.waitForIdle()
        assertEquals(listOf("tls", "udp", "tcp"), order.value)
    }

    @Test
    fun longPressDragBelowDividerRemovesType() {
        val order = mutableStateOf(listOf("udp", "tls"))
        compose.setContent {
            HoleTheme(mode = ThemeMode.LIGHT, dynamic = false) {
                TurnOrderBoard(order = order.value, onChange = { order.value = it })
            }
        }
        compose.waitForIdle()
        compose.onNodeWithText("TLS").performTouchInput {
            down(center)
            advanceEventTime(viewConfiguration.longPressTimeoutMillis + 100)
            moveBy(Offset(0f, 1200f), delayMillis = 80)
            up()
        }
        compose.waitForIdle()
        assertEquals(listOf("udp"), order.value)
    }

    @Test
    fun longPressHorizontalDragKeepsTheNewOrder() {
        val order = mutableStateOf(listOf("udp", "tls"))
        compose.setContent {
            HoleTheme(mode = ThemeMode.LIGHT, dynamic = false) {
                TurnOrderBoard(order = order.value, onChange = { order.value = it })
            }
        }
        compose.waitForIdle()
        compose.onNodeWithText("UDP").performTouchInput {
            down(center)
            advanceEventTime(viewConfiguration.longPressTimeoutMillis + 100)
            moveBy(Offset(400f, 0f), delayMillis = 80)
            up()
        }
        compose.waitForIdle()
        assertEquals(listOf("tls", "udp"), order.value)
    }

    @Test
    fun draggingLocksTheContainingPageScrollUntilDrop() {
        val order = mutableStateOf(listOf("udp"))
        val dragging = mutableStateOf(false)
        val pageOffset = mutableStateOf(0)
        compose.setContent {
            val scroll = rememberScrollState(initial = 80)
            androidx.compose.runtime.SideEffect { pageOffset.value = scroll.value }
            HoleTheme(mode = ThemeMode.LIGHT, dynamic = false) {
                Column(Modifier.height(260.dp).verticalScroll(scroll, enabled = !dragging.value)) {
                    TurnOrderBoard(
                        order = order.value,
                        onChange = { order.value = it },
                        onDraggingChanged = { dragging.value = it },
                    )
                    Spacer(Modifier.height(600.dp))
                }
            }
        }
        compose.waitForIdle()
        compose.onNodeWithText("TLS").performTouchInput {
            down(center)
            advanceEventTime(viewConfiguration.longPressTimeoutMillis + 100)
            moveBy(Offset(0f, -180f), delayMillis = 80)
            up()
        }
        compose.waitForIdle()
        compose.runOnIdle {
            assertEquals(80, pageOffset.value)
            assertEquals(listOf("tls", "udp"), order.value)
            assertEquals(false, dragging.value)
        }
    }
}
