package dev.hole.app.config

import kotlin.test.*
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json

class TransportConfigTest {
    @Test fun holeOrderPresenceAndBackupsSurviveRoundTrip() {
        assertEquals(64, RelayServeSettings().maxPairs)
        for (order in listOf(null, emptyList(), listOf("hole", "tls"))) {
            val relay = RelaySettings(device = "relay-node", order = order)
            val config = configured.copy(connection = configured.connection.copy(relay = relay, turn = TurnSettings(mode = "off")))
            assertEquals(relay, toStartRequest(config, "p", "t").config.relay)
            val backup = ConfigExchange.backup(config, "", "", false, false, "")
            assertEquals(relay, ConfigExchange.readBackup(backup).config.connection.relay)
        }
        val serve = RelaySettings(serve = RelayServeSettings(enabled = true))
        assertFailsWith<IllegalArgumentException> {
            toStartRequest(configured.copy(connection = configured.connection.copy(relay = serve)), "p", "t")
        }
        assertNull(holeRelayOrderError(listOf("hole", "udp", "tls")))
        assertNotNull(holeRelayOrderError(listOf("hole", "hole")))
    }
    private val wireJson = Json { encodeDefaults = true }
    private val configured = StoredConfig(connection = ConnectionSettings(serverUrl = "wss://fixture.invalid/ws", room = "room", deviceName = "phone"))
    @Test fun cloudflareIsTheDefaultAndTheWireUsesIce() {
        val request = toStartRequest(configured, "password", "token")
        assertEquals(listOf("stun:stun.cloudflare.com:3478"), request.config.ice.stunUrls)
        assertFalse(wireJson.encodeToString(request).contains("preferred"))
        assertFalse(wireJson.encodeToString(request).contains("allow_legacy"))
        assertTrue(wireJson.encodeToString(request).contains("stun.cloudflare.com"))
        assertEquals(CoreTurn(mode = "worker", ttl = "6h", urls = emptyList(), username = "", credential = ""), request.config.turn)
    }
    @Test fun customTurnOrderReachesTheCoreAndRejectsInvalidValues() {
        val order = listOf("tls", "udp")
        val custom = configured.copy(connection = configured.connection.copy(turn = TurnSettings(order = order)))
        assertEquals(order, toStartRequest(custom, "password", "token").config.turn.order)
        val duplicate = custom.copy(connection = custom.connection.copy(turn = TurnSettings(order = listOf("udp", "udp"))))
        assertFailsWith<IllegalArgumentException> { toStartRequest(duplicate, "password", "token") }
    }
    @Test fun migrationRemovesIpv6TransportAndPreservesCredentials() {
        val pinned = configured.copy(schemaVersion = 1, connection = configured.connection.copy(candidateAddresses = listOf("2001:db8::1"), passwordCipher = "encrypted"))
        val migrated = migrateStoredConfig(pinned, 1)
        assertEquals(2, migrated.schemaVersion)
        assertTrue(migrated.connection.candidateAddresses.isEmpty())
        assertEquals("encrypted", migrated.connection.passwordCipher)
    }
    @Test fun turnCredentialsAreNotPersistedOrExportedByDefault() {
        val source = configured.copy(connection = configured.connection.copy(turn = TurnSettings("manual", "6h", listOf("turn:fixture.invalid:3478?transport=udp"), "user"), turnCredentialCipher = "PRIVATE_CIPHER"))
        val text = ConfigExchange.backup(source, "PRIVATE_PASSWORD", "PRIVATE_TOKEN", false, false, "PRIVATE_TURN")
        assertFalse(text.contains("PRIVATE"))
        val restored = ConfigExchange.readBackup(text)
        assertEquals("", restored.turnCredential)
        assertEquals("", restored.config.connection.turnCredentialCipher)
        val included = ConfigExchange.readBackup(ConfigExchange.backup(source, "", "", true, false, "PRIVATE_TURN"))
        assertEquals("PRIVATE_TURN", included.turnCredential)
    }
    @Test fun iceRejectsAmbiguousLegacyAddressesAndImplicitPlaintext() {
        assertFailsWith<IllegalArgumentException> { toStartRequest(configured.copy(connection = configured.connection.copy(candidateAddresses = listOf("2001:db8::1"))), "p", "t") }
        assertFailsWith<IllegalArgumentException> { toStartRequest(configured.copy(connection = configured.connection.copy(serverUrl = "ws://fixture/ws")), "p", "t") }
    }
    @Test fun backupDropsConnectionModesAndPreservesOtherSettings() {
        for (version in listOf(1, 2)) {
            for (mode in listOf("legacy", "auto", "ice")) {
                val text = """
                    {"format":"hole-android-backup","version":$version,"config":{"schemaVersion":$version,
                    "connection":{"connectionMode":"$mode","room":"room","deviceName":"phone",
                    "turn":{"mode":"manual","urls":["turn:relay.example:3478"],"username":"user"}},
                    "provide":[{"entryId":"service","id":"ssh","host":"127.0.0.1","port":22}]},
                    "password":"password","token":"token","turnCredential":"turn-secret"}
                """.trimIndent()
                val restored = ConfigExchange.readBackup(text)
                assertEquals("phone", restored.config.connection.deviceName)
                assertEquals("user", restored.config.connection.turn.username)
                assertEquals("ssh", restored.config.provide.single().id)
                assertEquals("password", restored.password)
                assertEquals("token", restored.token)
                assertEquals("turn-secret", restored.turnCredential)
                assertFalse(wireJson.encodeToString(restored.config).contains("connectionMode"))
                assertFalse(ConfigExchange.backup(restored.config, "", "", false, false).contains("connectionMode"))
                assertFailsWith<IllegalArgumentException> {
                    ConfigExchange.readBackup(text.replace("\"connectionMode\"", "\"unknownSetting\""))
                }
            }
        }
    }
}
