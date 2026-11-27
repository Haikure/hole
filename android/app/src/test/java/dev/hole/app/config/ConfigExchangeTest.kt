package dev.hole.app.config

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue
import kotlin.test.assertFailsWith
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
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
        val order = listOf("tls", "udp")
        val configured = original.copy(connection = original.connection.copy(turn = TurnSettings(order = order)))
        val text = ConfigExchange.cliJSON(configured, "PRIVATE_PASSWORD", "PRIVATE_TOKEN", false)
        assertFalse(text.contains("PRIVATE_"))
        assertTrue(text.contains("\"server_url\":\"wss://fixture.invalid/ws\""))
        assertTrue(text.contains("\"order\":[\"tls\",\"udp\"]"))
        val current = original.copy(connection = original.connection.copy(serverUrl = "wss://previous.invalid/ws"))
        val restored = ConfigExchange.readCLIJSON(text, current)
        assertEquals("wss://fixture.invalid/ws", restored.config.connection.serverUrl)
        assertEquals(1, restored.config.provide.size)
        assertEquals("worker", restored.config.connection.turn.mode)
        assertEquals("6h", restored.config.connection.turn.ttl)
        assertEquals(order, restored.config.connection.turn.order)
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
    @Test fun cliRejectsRemovedStrategyField() {
        val text = ConfigExchange.cliJSON(original, "", "", false)
        assertFalse(text.contains("preferred"))
        for (old in listOf("auto", "ice", "ice-quic-mux-v1", "legacy-ipv6-quic-v2", "legacy", "ipv6")) {
            val document = Json.parseToJsonElement(text).jsonObject
            val transport = document.getValue("transport").jsonObject
            val oldDocument = JsonObject(document + ("transport" to JsonObject(transport + ("preferred" to JsonPrimitive(old)))))
            assertFailsWith<IllegalArgumentException> { ConfigExchange.readCLIJSON(oldDocument.toString(), original) }
        }
    }
    @Test fun minecraftDiscoveryAndVoiceGroupSurviveImportBackupAndRuntimeConversion() {
        val metadata = MinecraftMetadata("海岛世界", "1.20.1", 2, 20)
        val group = "a".repeat(64)
        val configured = original.copy(
            voice = VoiceSettings(true, MICROPHONE_SOURCE_HEADSET, group),
            connection = original.connection.copy(ice = IceSettings(punch = NatPunchSettings(sockets = 12))),
            provide = listOf(original.provide.first().copy(discovery = "minecraft", minecraft = metadata)),
        )
        val text = ConfigExchange.cliJSON(configured, "password", "token", true)
        val imported = ConfigExchange.readCLIJSON(text, original.copy(voice = configured.voice))
        assertEquals(metadata, imported.config.provide.single().minecraft)
        assertEquals("minecraft", imported.config.provide.single().discovery)
        assertEquals(configured.voice, imported.config.voice)
        assertEquals(configured.connection.ice.punch, imported.config.connection.ice.punch)
        val backup = ConfigExchange.backup(imported.config, imported.password, imported.token, true, false)
        val restored = ConfigExchange.readBackup(backup)
        assertEquals(imported.config, restored.config)
        val runtime = toStartRequest(restored.config, restored.password, restored.token)
        assertEquals(metadata, runtime.config.provide.single().minecraft)
        assertEquals("minecraft", runtime.config.provide.single().discovery)
        assertEquals(group, runtime.config.voice.group)
        assertEquals(text, ConfigExchange.cliJSON(restored.config, restored.password, restored.token, true))
    }
    @Test fun optionalDiscoveryAndGroupAreOmittedForOrdinaryConfigurations() {
        val text = ConfigExchange.cliJSON(original, "", "", false)
        assertFalse(text.contains("\"discovery\""))
        assertFalse(text.contains("\"minecraft\""))
        assertFalse(text.contains("\"group\""))
    }
    @Test fun importedWorldFieldsAreValidatedBeforeSaving() {
        val good = original.copy(provide = listOf(original.provide.first().copy(
            discovery = "minecraft", minecraft = MinecraftMetadata("世界", "1.20.1", 1, 20))))
        val entry = good.provide.single()
        val metadata = requireNotNull(entry.minecraft)
        for (invalid in listOf(
            good.copy(voice = VoiceSettings(group = "invalid")),
            good.copy(provide = listOf(entry.copy(protocol = "udp"))),
            good.copy(provide = listOf(entry.copy(discovery = "unknown"))),
            good.copy(provide = listOf(entry.copy(discovery = ""))),
            good.copy(provide = listOf(entry.copy(minecraft = metadata.copy(description = "界".repeat(65))))),
            good.copy(provide = listOf(entry.copy(minecraft = metadata.copy(description = "a\nb")))),
            good.copy(provide = listOf(entry.copy(minecraft = metadata.copy(version = "")))),
            good.copy(provide = listOf(entry.copy(minecraft = metadata.copy(online = -1)))),
        )) assertFailsWith<IllegalArgumentException> { ConfigExchange.validateDraft(invalid) }
    }
}
