package dev.hole.app

import dev.hole.app.config.ConnectionSettings
import dev.hole.app.config.StoredConfig
import dev.hole.corebridge.CoreSnapshot
import dev.hole.corebridge.PeerSnapshot
import dev.hole.corebridge.SendLossSnapshot
import kotlin.test.assertFalse
import kotlin.test.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [26])
class DiagnosticsTest {
    @Test fun failedPunchEvidenceIsExportedAfterRelayFallbackAndRedacted() {
        val round = """{"generation":"3","round":1,"probes_sent":80,"packets_received":0,"error":"fixture-secret"}"""
        val peer = PeerSnapshot(phase = "relay_tcp", pathType = "relay", punchDiagnostics = listOf(round))
        val report = diagnosticReport(CoreSnapshot(peers = listOf(peer)), ConfigUiState(password = "fixture-secret"), BackgroundInfo())
        assertTrue(report.contains("IPv4 打洞轮次"))
        assertTrue(report.contains("\"probes_sent\":80"))
        assertTrue(report.contains("\"packets_received\":0"))
        assertFalse(report.contains("fixture-secret"))
    }
    @Test fun quicLossIsReportedForEveryPathWithoutUsingLocalDrops() {
        for (path in listOf("direct", "relay", "hole", "fixed")) {
            val peer = PeerSnapshot(pathType = path, retryCount = "99", droppedDatagrams = "999", sendLoss = SendLossSnapshot(60, "80", "1", 1.25))
            val report = diagnosticReport(CoreSnapshot(peers = listOf(peer, peer.copy(sendLoss = null),
                peer.copy(sendLoss = SendLossSnapshot(60, "0", "0", null)))), ConfigUiState(), BackgroundInfo())
            assertTrue(report.contains("QUIC 上行丢包率 1.25% · 本机 → 对端 · 近 1 分钟 · 丢失 1 / 已判定 80 包"))
            assertTrue(report.contains("QUIC 上行丢包率 未上报"))
            assertTrue(report.contains("QUIC 上行丢包率 暂无样本"))
            assertTrue(report.contains("本地丢弃 999"))
        }
    }
    @Test fun fixedReportKeepsReportedRolesAndAddressesWithoutIceFields() {
        val peer = PeerSnapshot(profile="fixed-quic-mux-v1", state="reconnecting", phase="fixed", pathType="fixed",
            fixedEndpoint="gateway.example:40000", listenerId="phone", dialerId="server", localType="fixed", remoteType="fixed",
            localAddress="[2001:db8::1]:50000", remoteAddress="[2001:db8::2]:40000", errorCode="fixed_path_failed")
        val report = diagnosticReport(CoreSnapshot(peers=listOf(peer, PeerSnapshot(phase="fixed", state="connecting"))), ConfigUiState(), BackgroundInfo())
        for (text in listOf("公布入口 gateway.example:40000", "监听方 phone", "拨号方 server", "对端连接 固定入口直连 · [2001:db8::2]:40000", "仅重试固定入口", "reconnecting", "RTT")) assertTrue(report.contains(text), text)
        for (text in listOf("TURN 顺序", "中继状态", "凭据到期", "非本机中继", "探测中")) assertFalse(report.contains(text), text)
    }
    @Test fun diagnosticsStripPlainEncodedAndUrlCredentials() {
        val state = ConfigUiState(true, StoredConfig(connection = ConnectionSettings(serverUrl = "wss://userinfo@fixture.invalid/ws?arbitrary=hidden")), "PASS WORD", "ROOM_SECRET")
        val snapshot = CoreSnapshot(coreVersion = "fixture-core", errorMessage = "PASS WORD ROOM_SECRET PASS+WORD at wss://fixture.invalid/ws?not_a_standard_key=extra")
        val report = diagnosticReport(snapshot, state, BackgroundInfo())
        for (secret in listOf("PASS WORD", "PASS+WORD", "ROOM_SECRET", "userinfo", "arbitrary=hidden", "extra")) assertFalse(report.contains(secret), secret)
        assertTrue(report.contains("fixture-core"))
        assertFalse(report.contains("配置 Saved"))
        assertTrue(report.contains("[redacted]"))
    }
    @Test fun reportUsesActualTcpRelayLabel() {
        val report = diagnosticReport(CoreSnapshot(peers=listOf(PeerSnapshot(peerId="desktop",pathType="relay",relayProtocol="tcp",relaySide="local",relayPolicy="udp-tcp-tls-v1",phase="relay_tcp_80"))), ConfigUiState(), BackgroundInfo())
        assertTrue(report.contains("TCP 中继"))
        assertFalse(report.contains("TLS 中继"))
        assertTrue(report.contains("80 端口"))
    }
}
