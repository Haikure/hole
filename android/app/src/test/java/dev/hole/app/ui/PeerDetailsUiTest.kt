package dev.hole.app.ui

import android.graphics.Bitmap
import android.graphics.HardwareRenderer
import android.graphics.RenderNode
import androidx.activity.ComponentActivity
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.unit.dp
import dev.hole.app.config.ThemeMode
import dev.hole.corebridge.NATSnapshot
import dev.hole.corebridge.PeerSnapshot
import dev.hole.corebridge.SendLossSnapshot
import dev.hole.corebridge.BandwidthRates
import dev.hole.corebridge.BandwidthSnapshot
import java.io.File
import java.time.Instant
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
class PeerDetailsUiTest {
    @get:Rule val compose = createAndroidComposeRule<ComponentActivity>()

    @Test fun natColorsFollowEvidenceAndExpiredResultsStayNeutral() {
        for ((type, trend, tone) in listOf(
            Triple("full_cone", "stable", "green"), Triple("port_restricted_cone", "stable", "blue"),
            Triple("symmetric", "incremental", "yellow"), Triple("symmetric", "irregular", "red"),
            Triple("address_pool", "unverified", "neutral"), Triple("unknown", "unknown", "neutral"))) {
            val snapshot = NATSnapshot(status = "complete", type = type, predictability = trend)
            assertEquals(tone, peerNATTone(snapshot))
            assertEquals("neutral", peerNATTone(snapshot.copy(status = "probing")))
            val expired = currentNATDisplay(snapshot.copy(probedAt = "2026-11-08T00:00:00Z"), Instant.parse("2026-11-08T00:05:00Z"))
            assertEquals("neutral", peerNATTone(expired))
            assertTrue(expired.description().startsWith("已过期"))
        }
    }

    @Test fun sectionsSupportThemesLongAddressesAndNarrowScreens() {
        var mode by mutableStateOf(ThemeMode.LIGHT)
        var narrow by mutableStateOf(false)
        val peer = PeerSnapshot(peerId = "工作站", state = "active", pathType = "direct", addressFamily = "IPv6", rttMs = "12",
            localType = "host", remoteType = "srflx", localAddress = "[2001:db8:1234:5678:abcd:ef01:2345:6789]:40000",
            remoteAddress = "[2001:db8::2]:40001", activeChannels = 2, mappingCount = 3, bytesSent = "12582912", bytesReceived = "5242880",
            localNAT = NATSnapshot(status = "complete", type = "port_restricted_cone", predictability = "stable"),
            peerNAT = NATSnapshot(status = "complete", type = "symmetric", predictability = "incremental", addressPool = true))
        compose.setContent { HoleTheme(mode, false) {
            Surface(color = MaterialTheme.colorScheme.background) {
                Column((if (narrow) Modifier.width(220.dp) else Modifier.fillMaxWidth()).verticalScroll(rememberScrollState()).padding(16.dp),
                    verticalArrangement = Arrangement.spacedBy(18.dp)) { PeerDetailsContent(peer) }
            }
        } }
        compose.onNodeWithText("连接概览").assertExists()
        capture("peer-details-light.png")
        for (theme in listOf(ThemeMode.LIGHT, ThemeMode.DARK)) {
            compose.runOnIdle { mode = theme }
            compose.onNodeWithTag("peer-nat-local").performScrollTo()
            compose.onNodeWithText("对称型（地址池） · 可能递增").assertExists()
            capture("peer-nat-${theme.value}.png")
        }
        compose.runOnIdle { narrow = true }
        compose.onNodeWithText("对端连接").performScrollTo().assertIsDisplayed()
        capture("peer-details-narrow.png")
        compose.onNodeWithText("本地丢弃").performScrollTo().assertIsDisplayed()
    }

    @Test fun quicLossUpdatesOnEveryPathAndWrapsOnWearWidth() {
        var peer by mutableStateOf(PeerSnapshot(state = "active", pathType = "direct", retryCount = "99", droppedDatagrams = "999"))
        compose.setContent { HoleTheme(ThemeMode.DARK, false) {
            Surface {
                Column(Modifier.width(220.dp).verticalScroll(rememberScrollState()).padding(12.dp),
                    verticalArrangement = Arrangement.spacedBy(18.dp)) { PeerDetailsContent(peer) }
            }
        } }
        compose.onNodeWithText("丢包率").performScrollTo().assertIsDisplayed()
        compose.onNodeWithText("上行 —").assertExists()
        compose.onNodeWithText("合计 暂无反馈").assertExists()
        for (path in listOf("direct", "relay", "hole", "fixed")) {
            compose.runOnIdle { peer = peer.copy(pathType = path, sendLoss = SendLossSnapshot("本连接", "80", "1", 1.25),
                quicLoss = SendLossSnapshot("本连接", "1000", "19", 1.9), receiveLoss = SendLossSnapshot("本连接", "900", "9", 1.0),
                bandwidth = BandwidthSnapshot(BandwidthRates(8e6, 4e6), BandwidthRates(16e6, 8e6), BandwidthRates(16e6, 12e6))) }
            for (value in listOf("合计 1.90%", "上行 1.25%", "下行 1.00%", "合计 16 Mbps", "上行 8 Mbps", "下行 16 Mbps",
                "合计 12 Mbps", "上行 4 Mbps", "下行 8 Mbps")) {
                compose.onNodeWithText(value).performScrollTo().assertIsDisplayed()
            }
        }
        capture("peer-quic-loss-narrow.png")
        compose.runOnIdle { peer = peer.copy(sendLoss = SendLossSnapshot("本连接", "80", "0", 0.0)) }
        compose.onNodeWithText("上行 0.00%").assertExists()
        compose.runOnIdle { peer = peer.copy(sendLoss = SendLossSnapshot("本连接", "0", "0", null)) }
        compose.onNodeWithText("上行 暂无样本").assertExists()
        compose.runOnIdle { peer = peer.copy(sendLoss = null) }
        compose.onNodeWithText("上行 —").assertExists()
        compose.onNodeWithContentDescription("QUIC 统计 说明").performScrollTo().performClick()
        compose.onNodeWithText("速率统计本次连接", substring = true).assertIsDisplayed()
    }

    private fun capture(name: String) {
        compose.waitForIdle()
        compose.runOnIdle {
            val view = compose.activity.window.decorView
            val node = RenderNode("peer-preview").apply { setPosition(0, 0, view.width, view.height) }
            view.draw(node.beginRecording(view.width, view.height)); node.endRecording()
            val hardware = ReflectionHelpers.callStaticMethod<Bitmap>(HardwareRenderer::class.java, "createHardwareBitmap",
                ClassParameter.from(RenderNode::class.java, node), ClassParameter.from(Int::class.javaPrimitiveType, view.width), ClassParameter.from(Int::class.javaPrimitiveType, view.height))
            val bitmap = requireNotNull(hardware.copy(Bitmap.Config.ARGB_8888, false))
            val directory = File("build/reports/connection-preview").apply { mkdirs() }
            File(directory, name).outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
            bitmap.recycle(); hardware.recycle(); node.discardDisplayList()
        }
    }

    @Test fun releaseNotesFollowThemeAndFitNarrowWidth() {
        var mode by mutableStateOf(ThemeMode.LIGHT)
        var narrow by mutableStateOf(false)
        val markdown = """
            # 更新日志

            ## 网络连接与诊断

            - **地址池识别**：保留每个公网 IP 的端口趋势。
            - 最新版也能重新获取安装包。

            > 连接详情可查看本机与对端 NAT 状态。

            使用 `hole --help` 查看参数。

            ```text
            hole --config ./config.json
            ```

            | 类型 | 趋势 |
            | --- | --- |
            | 对称型 | 可能递增 |
            | 地址池 | 待验证 |

            [完整变更](https://github.com/Haikure/hole/releases)
        """.trimIndent()
        compose.setContent { HoleTheme(mode, false) {
            Surface(color = MaterialTheme.colorScheme.background) {
                Column((if (narrow) Modifier.width(220.dp) else Modifier.fillMaxWidth()).verticalScroll(rememberScrollState()).padding(16.dp)) {
                    HoleSettingsGroup("发行说明") { ReleaseNotes(markdown, "https://github.com/Haikure/hole/releases/tag/v1.0.6") }
                }
            }
        } }
        for (theme in listOf(ThemeMode.LIGHT, ThemeMode.DARK)) {
            compose.runOnIdle { mode = theme }
            capture("release-notes-${theme.value}.png")
        }
        compose.runOnIdle { narrow = true }
        capture("release-notes-narrow.png")
    }
}
