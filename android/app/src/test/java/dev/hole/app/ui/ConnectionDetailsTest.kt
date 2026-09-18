package dev.hole.app.ui

import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.v2.createComposeRule
import dev.hole.app.ConfigUiState
import dev.hole.app.config.ThemeMode
import dev.hole.app.config.ConnectionSettings
import dev.hole.app.config.StoredConfig
import dev.hole.corebridge.CoreSnapshot
import dev.hole.corebridge.PeerSnapshot
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import org.robolectric.annotation.LooperMode
import kotlin.test.assertEquals
import kotlin.test.assertTrue

@RunWith(RobolectricTestRunner::class)
@Config(sdk=[26])
@LooperMode(LooperMode.Mode.PAUSED)
class ConnectionDetailsTest {
    @get:Rule val compose = createComposeRule()
    @Test fun defaultDetailsAreReadableAndTechnicalFieldsAreExpandable() {
        val snapshot = CoreSnapshot(nativeReady=true,runRequested=true,engineState="running",signalState="joined",
            peers=listOf(PeerSnapshot(peerId="desktop",transportId="internal-transport-id",generation="17",state="active",pathType="direct",addressFamily="IPv4",rttMs="12",mappingCount=2,activeChannels=2,localType="host",remoteType="srflx",localAddress="192.168.1.20:40000",remoteAddress="192.168.1.21:40001")))
        compose.setContent { HoleTheme(ThemeMode.LIGHT,false) {
            RuntimeDetailsScreen(snapshot,ConfigUiState(loaded=true),null,onBack={},onReconnect={},onExport={},onBackground={})
        } }
        compose.onNodeWithText("连接详情").assertExists()
        compose.onNodeWithText("配置与会话边界").assertDoesNotExist()
        compose.onNodeWithText("最近事件").assertDoesNotExist()
        compose.onNodeWithText("保存版本").assertDoesNotExist()
        compose.onNodeWithTag("connection-details").performScrollToNode(hasText("查看线路细节"))
        compose.onNodeWithText("直连 · IPv4").assertExists()
        compose.onNodeWithText("12 ms").assertExists()
        compose.onNodeWithText("192.168.1.20:40000").assertDoesNotExist()
        compose.onNodeWithText("查看线路细节").performClick()
        compose.onNodeWithTag("connection-details").performScrollToNode(hasText("本机连接"))
        compose.onNodeWithText("本机连接").assertExists()
        compose.onNodeWithText("直连 · 192.168.1.20:40000").assertExists()
        compose.onNodeWithText("对端连接").assertExists()
        compose.onNodeWithText("连接过程").assertExists()
    }
    @Test fun statusTextDistinguishesConfiguredFromActuallyConnected() {
        assertEquals("等待对端设备",connectionTitle(CoreSnapshot(runRequested=true,signalState="joined",mappings=listOf(dev.hole.corebridge.MappingSnapshot("ssh","provide","tcp","waiting_peer")))))
        assertTrue(connectionIssue("worker_upgrade_required").contains("更新 Worker"))
        assertEquals("中继 · IPv4", peerPathLabel(PeerSnapshot(pathType="relay",relayProtocol="tls",addressFamily="IPv4")))
        assertEquals("直连 · 192.0.2.1:40000", selectedConnectionLabel("srflx", "192.0.2.1:40000"))
        assertEquals("中继 · [2001:db8::1]:3478", selectedConnectionLabel("relay", "[2001:db8::1]:3478"))
        assertEquals("仅 IPv6", connectionModeLabel("legacy"))
        assertTrue(connectionModeDescription("legacy").contains("不使用 ICE、STUN 或 TURN"))
        assertTrue(connectionModeDescription("auto").contains("不因探测失败而切换协议"))
        assertTrue(connectionPolicyLabel("legacy").contains("公网直连"))
    }
    @Test fun ipv6ModeUsesTheNewLabelAndExplainsTheActualBehavior() {
        compose.setContent { HoleTheme(ThemeMode.LIGHT, false) {
            TransportSettingsScreen(ConfigUiState(loaded=true, config=StoredConfig(connection=ConnectionSettings(connectionMode="legacy"))),
                onSave={_,_,_,_,_->null}, onBack={})
        } }
        compose.onNodeWithText("仅 IPv6").assertExists()
        compose.onNodeWithText("旧 IPv6").assertDoesNotExist()
        compose.onNodeWithText(connectionModeDescription("legacy")).assertExists()
    }
    @Test fun relayLabelsDistinguishTcpTlsAndUnreportedRemoteAccess() {
        assertEquals("中继 · IPv4", peerPathLabel(PeerSnapshot(pathType="relay",relayProtocol="tcp",relaySide="local",addressFamily="IPv4")))
        assertEquals("TCP 中继", relayPathLabel(PeerSnapshot(pathType="relay",relayProtocol="tcp",relaySide="local")))
        assertEquals("TLS 中继", relayPathLabel(PeerSnapshot(pathType="relay",relayProtocol="tls",relaySide="local")))
        assertEquals("对端中继 · 接入协议未上报", relayPathLabel(PeerSnapshot(pathType="relay",relaySide="remote")))
        assertEquals("尝试 TCP 中继 · 80 端口", phaseLabel("relay_tcp_80"))
        assertEquals("尝试 TLS 中继 · 443 端口", phaseLabel("relay_tls_443"))
        assertTrue(phaseLabel("relay_tls").contains("TLS"))
        assertTrue(phaseLabel("relay_tcp").contains("TCP"))
    }
    @Test fun credentialWaitIsNotShownAsAnActiveUdpProbe() {
        val waiting = PeerSnapshot(state="waiting_credentials",phase="relay_udp",relayState="requesting")
        assertEquals("等待中继凭据", peerStateLabel(waiting))
        assertEquals("等待中继凭据，尚未开始探测", peerPathLabel(waiting))
        assertEquals("等待中继凭据，尚未开始探测", peerPhaseLabel(waiting))
        assertEquals("尝试 UDP 中继", peerPhaseLabel(waiting.copy(state="connecting",relayState="ready")))
    }
    @Test fun technicalDetailsDoNotExposeConfigurationRevisionCounters() {
        compose.setContent { HoleTheme(ThemeMode.LIGHT, false) {
            RuntimeDetailsScreen(CoreSnapshot(runRequested=true), ConfigUiState(loaded=true), null,
                onBack={},onReconnect={},onExport={},onBackground={})
        } }
        compose.onNodeWithTag("connection-details").performScrollToNode(hasText("版本与技术信息"))
        compose.onNodeWithText("版本与技术信息").performClick()
        compose.onNodeWithTag("connection-details").performScrollToNode(hasText("运行 / 网络重建编号"))
        compose.onNodeWithText("配置版本：保存 / 提交 / 确认").assertDoesNotExist()
    }
}
