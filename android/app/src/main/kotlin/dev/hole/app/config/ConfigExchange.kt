package dev.hole.app.config

import dev.hole.core.mobile.Mobile
import java.util.UUID
import kotlinx.serialization.Serializable
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.encodeToJsonElement
import kotlinx.serialization.json.decodeFromJsonElement

@Serializable
data class PortableBackup(
    val format: String = "hole-android-backup",
    val version: Int = 2,
    val config: StoredConfig,
    val password: String? = null,
    val token: String? = null,
    val resumeAfterBoot: Boolean = false,
    val turnCredential: String? = null,
)

/** Plaintext exists only in memory while the user previews/exports a document. */
data class ImportPreview(val config: StoredConfig, val password: String, val token: String, val resumeAfterBoot: Boolean = false, val turnCredential: String = "")

object ConfigExchange {
    private val json = Json { prettyPrint = true; ignoreUnknownKeys = false }
    private val coreJson = Json { encodeDefaults = true }
    const val MAX_DOCUMENT_BYTES = 128 * 1024

    fun backup(config: StoredConfig, password: String, token: String, includeSecrets: Boolean, resumeAfterBoot: Boolean, turnCredential: String = ""): String {
        val document = json.encodeToJsonElement(PortableBackup(
            config = config.copy(connection = config.connection.copy(passwordCipher = "", tokenCipher = "", turnCredentialCipher = "")),
            password = password.takeIf { includeSecrets }, token = token.takeIf { includeSecrets }, resumeAfterBoot = resumeAfterBoot, turnCredential = turnCredential.takeIf { includeSecrets },
        )).jsonObject
        return json.encodeToString(JsonObject(document + mapOf("format" to JsonPrimitive("hole-android-backup"), "version" to JsonPrimitive(2))))
    }

    fun readBackup(text: String): ImportPreview {
        require(text.toByteArray().size <= MAX_DOCUMENT_BYTES) { "文件超过 128 KiB" }
        val envelope = json.parseToJsonElement(text).jsonObject
        require(envelope["format"]?.jsonPrimitive?.content == "hole-android-backup" && envelope["version"]?.jsonPrimitive?.content in setOf("1", "2")) { "请选择 Android 配置备份" }
        val backup = json.decodeFromString<PortableBackup>(text)
        require(backup.format == "hole-android-backup" && backup.version in 1..2) { "备份格式或版本不受支持" }
        val migrated = migrateStoredConfig(backup.config, if (backup.version == 1) 1 else backup.config.schemaVersion)
        val config = migrated.copy(connection = migrated.connection.copy(passwordCipher = "", tokenCipher = "", turnCredentialCipher = ""))
        val ids = config.provide.map { it.entryId } + config.consume.map { it.entryId }
        require(ids.all { it.isNotBlank() } && ids.distinct().size == ids.size) { "备份包含重复或空的条目标识" }
        validateDraft(config)
        return ImportPreview(config, backup.password.orEmpty(), backup.token.orEmpty(), backup.resumeAfterBoot, backup.turnCredential.orEmpty())
    }

    fun readCLI(text: String, current: StoredConfig): ImportPreview {
        require(text.toByteArray().size <= MAX_DOCUMENT_BYTES) { "文件超过 128 KiB" }
        return readCLIJSON(Mobile.decodeCLIConfig(text), current)
    }

    // Keep document metadata out of the runtime's nested CoreConfig. The mobile
    // request continues to have one authoritative server_url in its envelope.
    internal fun readCLIJSON(text: String, current: StoredConfig): ImportPreview {
        val document = coreJson.parseToJsonElement(text).jsonObject
        val serverUrl = document["server_url"]?.jsonPrimitive?.content.orEmpty().trim()
        val cli = coreJson.decodeFromJsonElement<CoreConfig>(JsonObject(document - "server_url"))
        require(cli.transport.preferred in setOf(PREFERRED_ICE, PREFERRED_IPV6)) { "transport.preferred 只接受 ice 或 ipv6" }
        val config = current.copy(
            connection = ConnectionSettings(
                serverUrl = serverUrl.ifBlank { current.connection.serverUrl }, room = cli.room,
                deviceName = cli.deviceName.ifBlank { current.connection.deviceName },
                sessionTimeout = cli.sessionTimeout, candidateInterfaces = cli.candidateInterfaces, candidateAddresses = cli.candidateAddresses,
                connectionMode = if (cli.transport.preferred == PREFERRED_IPV6) "legacy" else if (cli.transport.allowLegacy) "auto" else "ice",
                allowInsecureSignal = cli.transport.allowInsecureSignal, ice = cli.ice, turn = TurnSettings(cli.turn.mode, cli.turn.ttl, cli.turn.urls, cli.turn.username),
            ),
            provide = cli.provide.map {
                val (host, port) = splitEndpoint(it.service.substringAfter("://"))
                ProvideEntry(UUID.randomUUID().toString(), it.id, it.service.substringBefore("://"), host, port)
            },
            consume = cli.consume.map {
                val (host, port) = splitEndpoint(it.expose)
                ConsumeEntry(UUID.randomUUID().toString(), it.id, host, port)
            },
        )
        validateDraft(config)
        return ImportPreview(config, cli.password, cli.token, turnCredential = cli.turn.credential)
    }

    fun cli(config: StoredConfig, password: String, token: String, includeSecrets: Boolean, turnCredential: String = ""): String {
        return Mobile.encodeCLIConfig(cliJSON(config, password, token, includeSecrets, turnCredential), includeSecrets)
    }

    internal fun cliJSON(config: StoredConfig, password: String, token: String, includeSecrets: Boolean, turnCredential: String = ""): String {
        val request = validatedDraftRequest(config, password, token)
        // Validation helpers use temporary required values; never export those.
        val effective = request.config.copy(room = config.connection.room, deviceName = config.connection.deviceName,
            password = if (includeSecrets) password else "", token = if (includeSecrets) token else "", turn = config.connection.coreTurn(if (includeSecrets) turnCredential else ""))
        val document = coreJson.encodeToJsonElement(effective).jsonObject
        return coreJson.encodeToString(JsonObject(document + ("server_url" to JsonPrimitive(config.connection.serverUrl.trim()))))
    }

    fun validateDraft(config: StoredConfig) { validatedDraftRequest(config, "", "") }

    internal fun splitEndpoint(value: String): Pair<String, Int> {
        val separator = value.lastIndexOf(':')
        require(separator > 0) { "端点缺少地址或端口" }
        val host = normalizeHostInput(value.substring(0, separator))
        val port = requireNotNull(parsePort(value.substring(separator + 1))) { "端口无效" }
        require(host.isNotBlank()) { "地址为空" }
        return host to port
    }

    private fun validatedDraftRequest(config: StoredConfig, password: String, token: String): StartRequest = toStartRequest(
        config.copy(connection = config.connection.copy(
            serverUrl = config.connection.serverUrl.ifBlank { "wss://HOST/ws" },
            room = config.connection.room.ifBlank { "pending" }, deviceName = config.connection.deviceName.ifBlank { "pending" },
        )), password.ifBlank { "pending" }, token.ifBlank { "pending" }, turnCredential = "pending",
    )
}
