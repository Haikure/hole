package dev.hole.app.config

import kotlin.test.*
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json

class TransportConfigTest {
    private val wireJson = Json { encodeDefaults = true }
    private val configured = StoredConfig(connection = ConnectionSettings(serverUrl = "wss://fixture.invalid/ws", room = "room", deviceName = "phone"))
    @Test fun cloudflareIsTheDefaultAndTheWireUsesIce() {
        val request = toStartRequest(configured, "password", "token")
        assertEquals(listOf("stun:stun.cloudflare.com:3478"), request.config.ice.stunUrls)
        assertEquals(PREFERRED_ICE, request.config.transport.preferred)
        assertTrue(request.config.transport.allowLegacy)
        assertTrue(wireJson.encodeToString(request).contains("stun.cloudflare.com"))
        assertEquals(CoreTurn(mode = "worker", ttl = "6h", urls = emptyList(), username = "", credential = ""), request.config.turn)
    }
    @Test fun migrationPreservesExplicitLegacySemanticsAndCredentials() {
        val pinned = configured.copy(schemaVersion = 1, connection = configured.connection.copy(candidateAddresses = listOf("2001:db8::1"), passwordCipher = "encrypted"))
        val migrated = migrateStoredConfig(pinned, 1)
        assertEquals(2, migrated.schemaVersion)
        assertEquals("legacy", migrated.connection.connectionMode)
        assertEquals("encrypted", migrated.connection.passwordCipher)
        assertEquals("auto", migrateStoredConfig(configured.copy(schemaVersion = 1), 1).connection.connectionMode)
        assertEquals("legacy", migrateStoredConfig(configured.copy(connection = configured.connection.copy(serverUrl = "ws://fixture/ws")), 1).connection.connectionMode)
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
    @Test fun ipv6OnlyKeepsItsPersistedValueAndWireProtocol() {
        val connection = configured.connection.copy(connectionMode = "legacy")
        assertEquals(PREFERRED_IPV6, connection.coreTransport().preferred)
        assertFalse(connection.coreTransport().allowLegacy)
    }
}
