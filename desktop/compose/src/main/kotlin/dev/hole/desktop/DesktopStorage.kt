package dev.hole.desktop

import dev.hole.app.ConfigUiState
import dev.hole.app.config.*
import java.nio.channels.FileChannel
import java.nio.file.*
import java.nio.file.attribute.*
import java.util.UUID
import kotlinx.serialization.Serializable
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.*
import org.json.JSONObject

@Serializable
internal data class DesktopState(
    val version: Int = 1,
    val config: StoredConfig = StoredConfig(dynamicColor = false),
    val password: String = "",
    val token: String = "",
    val turnCredential: String = "",
    val resumeOnOpen: Boolean = false,
    val runRequested: Boolean = false,
    val reduceMotion: Boolean = false,
) {
    fun presentation() = ConfigUiState(true, config, password, token, turnCredential = turnCredential)
}

internal class DesktopStorage(configDirectory: Path? = null) : AutoCloseable {
    private val windows = System.getProperty("os.name").startsWith("Windows")
    private val json = Json { encodeDefaults = true; ignoreUnknownKeys = true; prettyPrint = true }
    val directory: Path = (configDirectory ?: Path.of(System.getenv("HOLE_DESKTOP_CONFIG_DIR") ?: if (windows) {
        (System.getenv("APPDATA") ?: error("缺少 APPDATA")) + "/hole-desktop"
    } else {
        (System.getenv("XDG_CONFIG_HOME") ?: (System.getProperty("user.home") + "/.config")) + "/hole-desktop"
    })).toAbsolutePath()
    private val file = directory.resolve("compose-state.json")
    private val channel: FileChannel
    private val lease: java.nio.channels.FileLock

    init {
        Files.createDirectories(directory)
        channel = FileChannel.open(directory.resolve("compose.lock"), StandardOpenOption.CREATE, StandardOpenOption.WRITE)
        lease = try { channel.tryLock() ?: error("另一个 Kotlin 桌面实例正在使用此配置目录") }
            catch (failure: Exception) { channel.close(); throw failure }
    }

    fun load(): DesktopState {
        if (Files.exists(file)) {
            val text = read(file)
            val hasConnectionMode = json.parseToJsonElement(text).jsonObject["config"]?.jsonObject
                ?.get("connection")?.jsonObject?.containsKey("connectionMode") == true
            val value = json.decodeFromString<DesktopState>(text)
            require(value.version == 1) { "桌面配置版本不受支持，原文件已保留" }
            val migrated = value.copy(config = migrateStoredConfig(value.config, value.config.schemaVersion))
            if (migrated != value || hasConnectionMode) save(migrated)
            return migrated
        }
        val legacy = directory.resolve("config.json")
        val state = if (Files.exists(legacy)) {
            val config = JSONObject(read(legacy))
            val secrets = directory.resolve("secrets.json").takeIf { Files.exists(it) }?.let { JSONObject(read(it)) } ?: JSONObject()
            val running = directory.resolve("run-state.json").takeIf { Files.exists(it) }?.let { JSONObject(read(it)) }
            legacyState(config, secrets).copy(runRequested = running?.optBoolean("run_requested") ?: false)
        } else DesktopState(config = StoredConfig(dynamicColor = false, connection = ConnectionSettings(
            deviceName = System.getenv("COMPUTERNAME") ?: System.getenv("HOSTNAME") ?: "desktop-${UUID.randomUUID().toString().take(8)}")))
        save(state)
        return state
    }

    // Legacy files are read only. A single protected document commits config and credentials together.
    fun save(state: DesktopState) {
        val temp = Files.createTempFile(directory, ".compose-", ".tmp")
        try {
            protect(temp)
            val clean = state.copy(config = state.config.copy(connection = state.config.connection.copy(
                passwordCipher = "", tokenCipher = "", turnCredentialCipher = "")))
            val encoded = json.encodeToString(clean).toByteArray(Charsets.UTF_8)
            require(encoded.size <= 128 * 1024) { "配置文件超过 128 KiB" }
            FileChannel.open(temp, StandardOpenOption.WRITE).use { stream ->
                val data = java.nio.ByteBuffer.wrap(encoded)
                while (data.hasRemaining()) stream.write(data)
                stream.force(true)
            }
            Files.move(temp, file, StandardCopyOption.ATOMIC_MOVE, StandardCopyOption.REPLACE_EXISTING)
        } finally { Files.deleteIfExists(temp) }
    }

    fun readDocument(path: Path) = read(path)
    private fun read(path: Path): String {
        require(Files.size(path) <= 128 * 1024) { "文件超过 128 KiB" }
        Files.newInputStream(path).use { stream ->
            val bytes = stream.readNBytes(128 * 1024 + 1)
            require(bytes.size <= 128 * 1024) { "文件超过 128 KiB" }
            return Charsets.UTF_8.newDecoder().decode(java.nio.ByteBuffer.wrap(bytes)).toString()
        }
    }
    fun writeDocument(path: Path, text: String) {
        Files.newByteChannel(path, setOf(StandardOpenOption.CREATE_NEW, StandardOpenOption.WRITE)).use { stream ->
            protect(path)
            val data = java.nio.ByteBuffer.wrap(text.toByteArray(Charsets.UTF_8))
            while (data.hasRemaining()) stream.write(data)
        }
    }
    private fun protect(path: Path) {
        if (!windows) Files.setPosixFilePermissions(path, PosixFilePermissions.fromString("rw-------"))
        else {
            val view = Files.getFileAttributeView(path, AclFileAttributeView::class.java)
                ?: error("配置目录不支持 Windows ACL")
            view.acl = listOf(AclEntry.newBuilder().setType(AclEntryType.ALLOW).setPrincipal(view.owner)
                .setPermissions(*AclEntryPermission.values()).build())
        }
    }

    fun importDocument(text: String, current: DesktopState): DesktopState {
        val root = runCatching { JSONObject(text) }.getOrNull()
        if (root?.optString("format") == "hole-desktop-backup") {
            require(root.getInt("version") == 1) { "桌面备份版本不受支持" }
            val imported = legacyState(root.getJSONObject("config"), root.optJSONObject("secrets") ?: root)
            ConfigExchange.validateDraft(imported.config)
            val ids = imported.config.provide.map { it.entryId } + imported.config.consume.map { it.entryId }
            require(ids.all { it.isNotBlank() } && ids.distinct().size == ids.size) { "备份包含重复或空的条目标识" }
            return imported.copy(runRequested = false)
        }
        val preview = ConfigExchange.readPasted(text, current.config)
        return current.copy(config = preview.config.copy(dynamicColor = false), password = preview.password,
            token = preview.token, turnCredential = preview.turnCredential, runRequested = false)
    }

    private fun legacyState(root: JSONObject, secrets: JSONObject): DesktopState {
        require(root.optInt("schema_version", 1) == 1) { "旧桌面配置版本不受支持" }
        val connection = JSONObject(root.getJSONObject("connection").toString())
        mapOf("server_url" to "serverUrl", "device_name" to "deviceName", "session_timeout" to "sessionTimeout",
            "candidate_interfaces" to "candidateInterfaces", "candidate_addresses" to "candidateAddresses",
            "allow_insecure_signal" to "allowInsecureSignal").forEach { (old, new) ->
            if (connection.has(old)) connection.put(new, connection.remove(old))
        }
        val prefs = root.optJSONObject("prefs") ?: JSONObject()
        fun entries(name: String): JsonElement {
            val array = root.optJSONArray(name) ?: org.json.JSONArray()
            for (index in 0 until array.length()) {
                val item = array.getJSONObject(index)
                item.put("entryId", item.optString("entry_id").ifBlank { UUID.randomUUID().toString() })
                item.remove("entry_id")
            }
            return json.parseToJsonElement(array.toString())
        }
        val config = buildJsonObject {
            put("connection", json.parseToJsonElement(connection.toString()))
            put("voice", json.parseToJsonElement((root.optJSONObject("voice") ?: JSONObject()).toString()))
            put("provide", entries("provide")); put("consume", entries("consume"))
            put("dynamicColor", false); put("themeMode", prefs.optString("theme_mode", "system"))
            put("materialPalette", when (val accent = prefs.optString("accent", "blue")) { "teal" -> "green"; "violet" -> "purple"; else -> accent })
        }
        fun secret(name: String) = if (secrets.isNull(name)) "" else secrets.optString(name)
        val stored = json.decodeFromJsonElement<StoredConfig>(config)
        return DesktopState(config = migrateStoredConfig(stored, stored.schemaVersion), password = secret("password"),
            token = secret("token"), turnCredential = secret("turn_credential"),
            resumeOnOpen = prefs.optBoolean("resume_at_login"), reduceMotion = prefs.optBoolean("reduce_motion"))
    }

    override fun close() { lease.release(); channel.close() }
}
