package dev.hole.desktop

import dev.hole.app.config.*
import dev.hole.desktop.bridge.NativeCore
import org.json.JSONObject
import kotlin.test.*

/** Runs against the current shared library, never an old AAR or stdio executable. */
class NativeBridgeTest {
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
