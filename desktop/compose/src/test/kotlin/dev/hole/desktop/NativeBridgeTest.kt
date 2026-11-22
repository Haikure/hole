package dev.hole.desktop

import dev.hole.app.config.*
import dev.hole.desktop.bridge.NativeCore
import java.nio.file.Files
import org.json.JSONObject
import kotlin.test.*

/** Runs against the current shared library, never an old AAR or stdio executable. */
class NativeBridgeTest {
    @Test fun savedAndImportedIpv6SettingsMigrateWithoutLosingServicesOrSecrets() {
        val directory = Files.createTempDirectory("hole-desktop-migration")
        try {
            DesktopStorage(directory).use { storage ->
                val old = DesktopState(
                    config = StoredConfig(connection = ConnectionSettings(
                        serverUrl = "wss://example.invalid/ws", room = "room", deviceName = "desktop-test",
                        candidateAddresses = listOf("2001:db8::1")),
                        provide = listOf(ProvideEntry("enabled", "ssh", "tcp", "127.0.0.1", 22),
                            ProvideEntry("disabled", "draft", "udp", "127.0.0.1", 53, false))),
                    password = "password", token = "token", turnCredential = "turn-secret", runRequested = true,
                )
                storage.save(old)
                val path = directory.resolve("compose-state.json")
                val legacyDocument = JSONObject(Files.readString(path))
                legacyDocument.getJSONObject("config").getJSONObject("connection").put("connectionMode", "legacy")
                Files.writeString(path, legacyDocument.toString())
                val loaded = storage.load()
                val expected = old.copy(config = old.config.copy(connection = old.config.connection.copy(
                    candidateAddresses = emptyList())))
                assertEquals(expected, loaded)
                assertEquals(loaded, storage.load())
                assertEquals(listOf("ssh"), toStartRequest(loaded.config, loaded.password, loaded.token).config.provide.map { it.id })
                val persisted = JSONObject(Files.readString(directory.resolve("compose-state.json")))
                    .getJSONObject("config").getJSONObject("connection")
                assertFalse(persisted.has("connectionMode"))
                assertEquals(0, persisted.getJSONArray("candidateAddresses").length())
                for (mode in listOf("auto", "ice")) {
                    val document = JSONObject(Files.readString(path))
                    document.getJSONObject("config").getJSONObject("connection").put("connectionMode", mode)
                    Files.writeString(path, document.toString())
                    assertEquals(loaded, storage.load())
                    assertFalse(Files.readString(path).contains("connectionMode"))
                }

                val backup = """
                    {"format":"hole-desktop-backup","version":1,"config":{"schema_version":1,
                    "connection":{"server_url":"wss://example.invalid/ws","room":"room","device_name":"imported","connection_mode":"legacy","candidate_addresses":["2001:db8::1"]},
                    "provide":[{"entry_id":"service","id":"ssh","protocol":"tcp","host":"127.0.0.1","port":22}]},
                    "secrets":{"password":"password","token":"token","turn_credential":"turn-secret"}}
                """.trimIndent()
                val imported = storage.importDocument(backup, loaded)
                assertTrue(imported.config.connection.candidateAddresses.isEmpty())
                assertEquals("imported", imported.config.connection.deviceName)
                assertEquals("ssh", imported.config.provide.single().id)
                assertEquals("password", imported.password)
                assertEquals("token", imported.token)
                assertEquals("turn-secret", imported.turnCredential)
            }
        } finally {
            Files.walk(directory).use { paths -> paths.sorted(Comparator.reverseOrder()).forEach { Files.deleteIfExists(it) } }
        }
    }

    @Test fun nativeLifecycleAndUtf8ConfigExchange() {
        val handle = NativeCore.open()
        try {
            assertEquals("1", NativeCore.invoke(handle, "version"))
            val snapshot = JSONObject(NativeCore.invoke(handle, "snapshot"))
            assertFalse(snapshot.getBoolean("run_requested"))
            assertEquals(1, snapshot.getInt("api_version"))
            val document = """
                server_url: wss://example.invalid/ws
                room: 中文房间
                password: 密码
                token: 房间密码
                device_name: desktop-test
                provide: []
                consume: []
            """.trimIndent()
            val normalized = NativeCore.invoke(0, "decode", document)
            assertEquals("中文房间", JSONObject(normalized).getString("room"))
            val yaml = NativeCore.invoke(0, "encode", normalized)
            assertFalse(yaml.contains("密码"))
            NativeCore.invoke(handle, "stop")
            NativeCore.invoke(handle, "stop")
        } finally { NativeCore.close(handle) }
        NativeCore.close(handle)
        assertFailsWith<IllegalStateException> { NativeCore.invoke(handle, "snapshot") }
    }

    @Test fun malformedRequestsAndMissingHandlesAreRejected() {
        assertFailsWith<IllegalStateException> { NativeCore.invoke(-1, "snapshot") }
        assertFailsWith<IllegalStateException> { NativeCore.invoke(0, "decode", "x".repeat(128 * 1024 + 1)) }
        assertFailsWith<IllegalStateException> { NativeCore.invoke(0, "decode", "unknown: true") }
    }

    @Test fun sharedBackupAndRuntimeModelsKeepDisabledMappings() {
        val config = StoredConfig(connection = ConnectionSettings(serverUrl = "wss://example.invalid/ws", room = "room", deviceName = "desktop-test"),
            provide = listOf(ProvideEntry("enabled", "ssh", "tcp", "127.0.0.1", 22),
                ProvideEntry("disabled", "draft", "udp", "127.0.0.1", 53, false)))
        val backup = ConfigExchange.backup(config, "password", "token", true, false)
        val restored = ConfigExchange.readBackup(backup)
        assertEquals(config, restored.config)
        assertEquals("password", restored.password)
        val request = toStartRequest(restored.config, restored.password, restored.token)
        assertEquals(listOf("ssh"), request.config.provide.map { it.id })
    }
}
