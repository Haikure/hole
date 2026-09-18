package dev.hole.app.config

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue
import kotlin.test.assertFailsWith
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonObject

class ConfigExchangeTest {
    private val original = StoredConfig(themeStyle = "miuix", themeMode = "dark",
        connection = ConnectionSettings(serverUrl = "wss://fixture.invalid/ws", room = "fixture", deviceName = "phone",
            passwordCipher = "PRIVATE_CIPHER_A", tokenCipher = "PRIVATE_CIPHER_B"),
        provide = listOf(ProvideEntry("p", "ssh", "tcp", "127.0.0.1", 22, true), ProvideEntry("off", "draft", "udp", "::1", 53, false)),
        consume = listOf(ConsumeEntry("c", "web", "::1", 8080)))
    @Test fun defaultBackupOmitsAllSecretsAndRetainsDisabledEntriesAndAppearance() {
        val text = ConfigExchange.backup(original, "PRIVATE_PASSWORD", "PRIVATE_TOKEN", false, true)
        assertFalse(text.contains("PRIVATE_"))
        assertFalse(text.contains("passwordCipher"))
        assertTrue(text.contains("hole-android-backup"))
        val restored = ConfigExchange.readBackup(text)
        assertEquals(original.copy(connection = original.connection.copy(passwordCipher = "", tokenCipher = "")), restored.config)
        assertEquals("", restored.password)
        assertTrue(restored.resumeAfterBoot)
    }
    @Test fun credentialsRequireExplicitExportChoice() {
        val restored = ConfigExchange.readBackup(ConfigExchange.backup(original, "PRIVATE_PASSWORD", "PRIVATE_TOKEN", true, false))
        assertEquals("PRIVATE_PASSWORD", restored.password)
        assertEquals("PRIVATE_TOKEN", restored.token)
        assertEquals("", restored.config.connection.passwordCipher)
    }
    @Test fun rejectsUnknownFormatVersionDuplicateIdsAndOversizedFiles() {
        val text = ConfigExchange.backup(original, "", "", false, false)
        for (bad in listOf(text.replace("hole-android-backup", "different"), text.replace("\"version\": 2", "\"version\": 99"),
            text.replace("\"entryId\": \"c\"", "\"entryId\": \"p\""), " ".repeat(128 * 1024 + 1))) {
            assertFailsWith<Exception> { ConfigExchange.readBackup(bad) }
        }
    }
    @Test fun endpointSplittingPreservesIpv6AndCliHostnames() {
        assertEquals("::1" to 8080, ConfigExchange.splitEndpoint("[::1]:8080"))
        assertEquals("service_name.local" to 22, ConfigExchange.splitEndpoint("service_name.local:22"))
    }
    @Test fun goDurationFractionsMicrosecondsAndOverflow() {
        assertTrue(isValidDuration("1.5s"))
        assertTrue(isValidDuration("2h3m4.5s"))
        assertTrue(isValidDuration("100µs"))
        assertFalse(isValidDuration("100000000000000h"))
        assertFalse(isValidDuration("-1s"))
        assertFalse(isValidDuration("1second"))
    }
    @Test fun cliExportAndImportCarryTheConfiguredServer() {
        val text = ConfigExchange.cliJSON(original, "PRIVATE_PASSWORD", "PRIVATE_TOKEN", false)
        assertFalse(text.contains("PRIVATE_"))
        assertTrue(text.contains("\"server_url\":\"wss://fixture.invalid/ws\""))
        val current = original.copy(connection = original.connection.copy(serverUrl = "wss://previous.invalid/ws"))
        val restored = ConfigExchange.readCLIJSON(text, current)
        assertEquals("wss://fixture.invalid/ws", restored.config.connection.serverUrl)
        assertEquals(1, restored.config.provide.size)
        assertEquals("worker", restored.config.connection.turn.mode)
        assertEquals("6h", restored.config.connection.turn.ttl)
        assertEquals("", restored.password)
    }
    @Test fun oldCliDocumentRetainsTheCurrentServerAndDoesNotInventOne() {
        val document = Json.parseToJsonElement(ConfigExchange.cliJSON(original, "", "", false)).jsonObject
        val old = Json.encodeToString(JsonObject(document - "server_url"))
        assertEquals(original.connection.serverUrl, ConfigExchange.readCLIJSON(old, original).config.connection.serverUrl)
        assertEquals("", ConfigExchange.readCLIJSON(old, StoredConfig()).config.connection.serverUrl)
        val blank = ConfigExchange.cliJSON(original.copy(connection = original.connection.copy(serverUrl = "")), "", "", false)
        assertTrue(blank.contains("\"server_url\":\"\""))
        assertFalse(blank.contains("wss://HOST"))
    }
    @Test fun cliPreferredUsesOnlyIceAndIpv6() {
        val text = ConfigExchange.cliJSON(original, "", "", false)
        assertTrue(text.contains("\"preferred\":\"ice\""))
        for (old in listOf("ice-quic-mux-v1", "legacy-ipv6-quic-v2", "legacy", "auto")) {
            assertFailsWith<IllegalArgumentException> { ConfigExchange.readCLIJSON(text.replace("\"preferred\":\"ice\"", "\"preferred\":\"$old\""), original) }
        }
        val ipv6 = original.copy(connection = original.connection.copy(connectionMode = "legacy"))
        val exported = ConfigExchange.cliJSON(ipv6, "", "", false)
        assertTrue(exported.contains("\"preferred\":\"ipv6\""))
        assertEquals("legacy", ConfigExchange.readCLIJSON(exported, original).config.connection.connectionMode)
    }
}
