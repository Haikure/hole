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
import kotlin.test.assertFalse

@RunWith(RobolectricTestRunner::class)
@Config(sdk=[26])
@LooperMode(LooperMode.Mode.PAUSED)
class ConnectionDetailsTest {
    @get:Rule val compose = createComposeRule()
    @Test fun fixedLabelsKeepStateAndOmitMissingAddresses() {
        for ((state, label) in listOf("connecting" to "连接中", "active" to "已连接", "reconnecting" to "正在重试", "error" to "连接需要处理")) {
            val peer = PeerSnapshot(profile="fixed-quic-mux-v1", state=state)
            assertTrue(isFixedPeer(peer))
            assertEquals("固定入口直连", peerPathLabel(peer))
            assertEquals(label, peerStateLabel(peer))
            assertTrue(fixedPeerDetails(peer).isEmpty())
        }
        assertTrue(isFixedPeer(PeerSnapshot(phase="fixed")))
        assertTrue(isFixedPeer(PeerSnapshot(pathType="fixed")))
        assertEquals("固定入口直连 · IPv4", peerPathLabel(PeerSnapshot(pathType="fixed", addressFamily="IPv4")))
        assertEquals("固定入口直连 · IPv6", peerPathLabel(PeerSnapshot(pathType="fixed", addressFamily="IPv6")))
        assertEquals("固定入口直连 · 192.0.2.1:40000", selectedConnectionLabel("fixed", "192.0.2.1:40000"))
        assertEquals("固定入口直连", voicePathLabel("fixed"))
        assertEquals("direct", voicePathLabel("direct"))
        assertFalse(isFixedPeer(PeerSnapshot(pathType="direct", phase="direct")))
        assertTrue(connectionIssue("fixed_path_failed").contains("仅重试固定入口"))
        assertTrue(connectionIssue("fixed_relay_conflict").contains("仅中继"))
    }

    @Test fun fixedFilterIncludesConnectingPeersAndSearchesAdvertisedEntrance() {
        val peers = listOf(
            PeerSnapshot(peerId="fixed-pending", transportId="fixed-pending", profile="fixed-quic-mux-v1", state="connecting", fixedEndpoint="waiting.example:40000", listenerId="phone", dialerId="server"),
            PeerSnapshot(peerId="fixed-retrying", transportId="fixed-retrying", phase="fixed", state="reconnecting"),
            PeerSnapshot(peerId="ice-peer", transportId="ice-peer", pathType="direct"),
        )
        compose.setContent { HoleTheme(ThemeMode.LIGHT, false) {
            RuntimeDetailsScreen(CoreSnapshot(peers=peers), ConfigUiState(loaded=true), null,
                onBack={}, onReconnect={}, onExport={}, onBackground={})
        } }
        compose.onNodeWithText("固定入口").performClick()
        compose.onNodeWithText("fixed-pending").assertExists()
        compose.onNodeWithText("fixed-retrying").assertExists()
        compose.onNodeWithText("ice-peer").assertDoesNotExist()
        compose.onNode(hasSetTextAction()).performTextInput("waiting.example")
        compose.onNodeWithText("fixed-pending").performClick()
        compose.onNodeWithText("waiting.example:40000").assertExists()
        compose.onNodeWithText("监听方").assertExists()
        compose.onNodeWithText("拨号方").assertExists()
        compose.onNodeWithText("本机连接").assertDoesNotExist()
        compose.onNodeWithText("对端连接").assertDoesNotExist()
        compose.onNodeWithText("凭据有效期").assertDoesNotExist()
    }

    @Test fun fixedDetailsSeparateEntranceAndActualAddresses() {
        val peer = PeerSnapshot(peerId="fixed-connected", transportId="fixed-connected", state="active", pathType="fixed", phase="fixed", addressFamily="IPv6",
            fixedEndpoint="gateway.example:40000", listenerId="server", dialerId="phone", localType="fixed", remoteType="fixed",
            localAddress="[2001:db8::1]:50000", remoteAddress="[2001:db8::2]:40000")
        assertEquals("phone", fixedPeerDetails(peer.copy(listenerId="phone", dialerId="server")).toMap()["监听方"])
        compose.setContent { HoleTheme(ThemeMode.LIGHT, false) {
            RuntimeDetailsScreen(CoreSnapshot(peers=listOf(peer)), ConfigUiState(loaded=true), null,
                onBack={}, onReconnect={}, onExport={}, onBackground={})
        } }
        compose.onNodeWithText("fixed-connected").performClick()
        compose.onNodeWithText("公布入口").assertExists()
        compose.onNodeWithText("gateway.example:40000").assertExists()
        compose.onNodeWithText("固定入口直连 · [2001:db8::2]:40000").assertExists()
        compose.onNodeWithText("服务通道").assertExists()
        compose.onNodeWithText("本机顺序").assertDoesNotExist()
    }
    @Test fun defaultDetailsAreReadableAndTechnicalFieldsAreExpandable() {
        val snapshot = CoreSnapshot(nativeReady=true,runRequested=true,engineState="running",signalState="joined",
            peers=listOf(PeerSnapshot(peerId="desktop",transportId="internal-transport-id",generation="17",state="active",pathType="direct",addressFamily="IPv4",rttMs="12",mappingCount=2,activeChannels=2,localType="host",remoteType="srflx",localAddress="192.168.1.20:40000",remoteAddress="192.168.1.21:40001")))
        compose.setContent { HoleTheme(ThemeMode.LIGHT,false) {
            RuntimeDetailsScreen(snapshot,ConfigUiState(loaded=true),null,onBack={},onReconnect={},onExport={},onBackground={})
        } }
        compose.onNodeWithText("连接").assertExists()
        compose.onNodeWithText("配置与会话边界").assertDoesNotExist()
        compose.onNodeWithText("最近事件").assertDoesNotExist()
        compose.onNodeWithText("保存版本").assertDoesNotExist()
        compose.onNodeWithText("直连 · IPv4").assertExists()
        compose.onNodeWithText("12 ms").assertExists()
        compose.onNodeWithText("192.168.1.20:40000").assertDoesNotExist()
        compose.onNodeWithText("desktop").performClick()
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
                onSave={_,_,_,_,_,_->null}, onBack={})
        } }
        compose.onNodeWithText("仅 IPv6").assertExists()
        compose.onNodeWithText("旧 IPv6").assertDoesNotExist()
        compose.onNodeWithText(connectionModeDescription("legacy")).assertDoesNotExist()
        compose.onNodeWithContentDescription("策略说明 说明").performClick()
        compose.onNodeWithText(connectionModeDescription("legacy")).assertIsDisplayed()
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
        compose.onNodeWithContentDescription("连接选项").performClick()
        compose.onNodeWithText("网络诊断").performClick()
        compose.onNodeWithText("运行 / 网络重建编号").performScrollTo().assertIsDisplayed()
        compose.onNodeWithText("配置版本：保存 / 提交 / 确认").assertDoesNotExist()
    }
}
