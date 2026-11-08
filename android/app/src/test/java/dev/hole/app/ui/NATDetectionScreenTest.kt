package dev.hole.app.ui

import android.graphics.Bitmap
import android.graphics.HardwareRenderer
import android.graphics.RenderNode
import androidx.activity.ComponentActivity
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.unit.dp
import dev.hole.app.config.ThemeMode
import dev.hole.corebridge.DiagnosticField
import dev.hole.corebridge.DiagnosticSection
import dev.hole.corebridge.NATDiagnostic
import dev.hole.corebridge.DiagnosticTile
import java.io.File
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import org.robolectric.util.ReflectionHelpers
import org.robolectric.util.ReflectionHelpers.ClassParameter

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], qualifiers = "w393dp-h851dp-xhdpi")
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class NATDetectionScreenTest {
    @get:Rule val compose = createAndroidComposeRule<ComponentActivity>()

    @Test fun detectionActionsDetailedEvidenceAndSmallScreens() {
        var report by mutableStateOf(NATDiagnostic())
        var mode by mutableStateOf(ThemeMode.LIGHT)
        var narrow by mutableStateOf(false)
        var starts = 0
        var cancels = 0
        compose.setContent {
            HoleTheme(mode, dynamic = false) {
                Box(if (narrow) Modifier.width(220.dp) else Modifier) {
                    NATDetectionScreen(report, true, null, { starts++ }, { cancels++ }, {}, {})
                }
            }
        }
        compose.onNodeWithTag("nat-start").performClick()
        assertEquals(1, starts)
        compose.runOnIdle { report = report.copy(state = "probing", title = "正在检测网络") }
        compose.onNodeWithTag("nat-cancel").performClick()
        assertEquals(1, cancels)
        val result = NATDiagnostic("complete", "对称型", "不同目标可能获得不同公网端口，普通打洞难度较高；能否预测取决于端口证据。", listOf(
            DiagnosticSection("映射与过滤", listOf(
                DiagnosticField("映射行为", "端点相关", "映射随目标地址和端口变化。"),
                DiagnosticField("过滤行为", "未知 / 尚未确认", "未知不等于无限制。"),
                DiagnosticField("公网 IPv4", "203.0.113.8"))),
            DiagnosticSection("端口特征", listOf(DiagnosticField("端口规律", "可能递增", "出现规律不保证打洞成功。"))),
            DiagnosticSection("检测时的网络", listOf(DiagnosticField("IPv6 地址", "2001:db8:1234:5678:abcd:ef01:2345:6789"))),
            DiagnosticSection("检测记录", listOf(DiagnosticField("有效样本", "18"))),
        ), tone = "yellow", badge = "可能可预测", highlights = listOf(
            DiagnosticTile("mapping", "映射行为", "端点相关"), DiagnosticTile("shield", "过滤行为", "地址与端口相关"),
            DiagnosticTile("trend", "端口规律", "可能递增"), DiagnosticTile("steps", "端口步长", "2"),
            DiagnosticTile("samples", "有效样本", "18"), DiagnosticTile("network", "不同公网映射", "6")))
        compose.runOnIdle { report = result }
        compose.onNodeWithTag("nat-detection").performScrollToNode(hasText("端口特征"))
        compose.onNodeWithText("端口特征").assertIsDisplayed()
        compose.onNodeWithTag("nat-detection").performScrollToNode(hasTestTag("nat-tile-mapping"))
        val left = compose.onNodeWithTag("nat-tile-mapping").fetchSemanticsNode().boundsInRoot
        val right = compose.onNodeWithTag("nat-tile-shield").fetchSemanticsNode().boundsInRoot
        assertEquals(left.top, right.top, 1f)
        assertTrue(left.right < right.left)
        capture("nat-tiles.png")
        for (appearance in listOf(ThemeMode.LIGHT, ThemeMode.DARK)) {
            compose.runOnIdle { mode = appearance }
            compose.onNodeWithTag("nat-detection").performScrollToIndex(0)
            capture("nat-${appearance.value}.png")
        }
        for ((tone, title) in listOf("green" to "完全锥型", "blue" to "端口受限锥型", "red" to "对称型")) {
            val cone = tone != "red"
            val mapping = if (cone) "端点无关" else "端点相关"
            val filtering = if (tone == "green") "端点无关" else "地址与端口相关"
            val trend = if (cone) "映射稳定" else "未观察到规律"
            compose.runOnIdle { mode = ThemeMode.LIGHT; report = result.copy(tone = tone, title = title,
                summary = if (tone == "green") "不同目标共用映射，且入站过滤较宽松，通常较容易建立 UDP 直连。"
                    else if (tone == "blue") "不同目标共用映射，但只接收曾主动联系过的 IP 和端口发来的数据。"
                    else "不同目标获得不同公网端口，本次采样未观察到可预测的分配规律。",
                badge = if (tone == "green") "直连条件较好" else if (tone == "blue") "入站受限" else "不可预测 · 本次采样",
                highlights = result.highlights.map { tile -> tile.copy(value = when(tile.icon) {
                    "mapping" -> mapping; "shield" -> filtering; "trend" -> trend
                    "steps" -> "未知 / 尚未确认"; "network" -> if (cone) "1" else "6"; else -> tile.value
                }) },
                sections = result.sections.map { section -> section.copy(fields = section.fields.map { field ->
                    when(field.label) {
                        "映射行为" -> field.copy(value = mapping, detail = "不同目标的映射行为。")
                        "过滤行为" -> field.copy(value = filtering, detail = "本次采样确认的入站限制。")
                        "端口规律" -> field.copy(value = trend)
                        else -> field
                    }
                }) }) }
            capture("nat-$tone.png")
        }
        compose.runOnIdle { report = result }
        compose.runOnIdle { narrow = true }
        compose.onNodeWithTag("nat-detection").performScrollToNode(hasText("2001:db8:1234:5678:abcd:ef01:2345:6789"))
        compose.onNodeWithText("2001:db8:1234:5678:abcd:ef01:2345:6789").assertIsDisplayed()
        capture("nat-small-screen.png")
        compose.runOnIdle { report = result.copy(state = "network_changed", title = "网络已变化", tone = "neutral", badge = "需重新检测") }
        compose.onNodeWithTag("nat-detection").performScrollToIndex(0)
        compose.onNodeWithText("网络已变化").assertIsDisplayed()
    }

    private fun capture(name: String) {
        compose.mainClock.advanceTimeBy(600)
        compose.waitForIdle()
        compose.runOnIdle {
            val view = compose.activity.window.decorView
            val node = RenderNode("nat-preview").apply { setPosition(0, 0, view.width, view.height) }
            view.draw(node.beginRecording(view.width, view.height))
            node.endRecording()
            val hardware = ReflectionHelpers.callStaticMethod<Bitmap>(HardwareRenderer::class.java, "createHardwareBitmap",
                ClassParameter.from(RenderNode::class.java, node), ClassParameter.from(Int::class.javaPrimitiveType, view.width), ClassParameter.from(Int::class.javaPrimitiveType, view.height))
            val bitmap = requireNotNull(hardware.copy(Bitmap.Config.ARGB_8888, false))
            val directory = File("build/reports/nat-preview").apply { mkdirs() }
            File(directory, name).outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
            bitmap.recycle(); hardware.recycle(); node.discardDisplayList()
        }
    }
}
