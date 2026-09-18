package dev.hole.app

import dev.hole.app.config.ConnectionSettings
import dev.hole.app.config.StoredConfig
import dev.hole.corebridge.CoreSnapshot
import dev.hole.corebridge.PeerSnapshot
import kotlin.test.assertFalse
import kotlin.test.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [26])
class DiagnosticsTest {
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
