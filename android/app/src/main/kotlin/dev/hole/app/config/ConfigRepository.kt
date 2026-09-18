package dev.hole.app.config

import android.content.Context
import java.io.File
import java.io.FileOutputStream
import java.util.concurrent.ConcurrentHashMap
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.intOrNull
import kotlinx.serialization.SerializationException
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.filterNotNull

/**
 * 配置仓库：单个 JSON 文档的等价事务存储。
 * - 每次保存写临时文件、fsync、原子 rename，进程中断不会留下半个配置。
 * - 仓库内串行化，调用方拿到的都是某次完整保存后的快照。
 * - 非秘密字段明文保存；两种密码经 SecretCipher 加密后存放，密文在本文件内。
 * - 首次运行生成默认设备名并立即落盘，保证重开应用名称稳定。
 */
class ConfigRepository(context: Context, private val cipher: SecretCipher = SecretCipher()) {
    private val file = File(context.applicationContext.filesDir, "config/config.json")
    // Service and ViewModel use different repository instances but one file.
    // Share both the transaction lock and change stream per canonical path.
    private val shared = stores.computeIfAbsent(file.canonicalPath) { SharedStore() }
    private val mutex get() = shared.mutex
    val changes = shared.state.filterNotNull()
    private val json = Json { ignoreUnknownKeys = true; encodeDefaults = true }

    /** 读取当前配置；文件不存在或损坏时生成并写入默认配置。 */
    suspend fun load(): StoredConfig = mutex.withLock {
        (readUnlocked() ?: createDefaultUnlocked()).also { shared.state.value = it }
    }

    /** 明文密码（解密失败返回空串，要求用户重新填写）。 */
    suspend fun loadSecrets(config: StoredConfig): Pair<String, String> = withContext(Dispatchers.IO) {
        Pair(decryptOrEmpty(config.connection.passwordCipher), decryptOrEmpty(config.connection.tokenCipher))
    }

    suspend fun loadTurnSecret(config: StoredConfig): String = withContext(Dispatchers.IO) { decryptOrEmpty(config.connection.turnCredentialCipher) }

    /** 事务更新：transform 在锁内对最新快照执行，返回的副本原子落盘。 */
    suspend fun update(transform: (StoredConfig) -> StoredConfig): StoredConfig =
        mutex.withLock {
            val current = readUnlocked() ?: createDefaultUnlocked()
            val next = transform(current)
            writeUnlocked(next)
            shared.state.value = next
            next
        }

    private fun decryptOrEmpty(ciphertext: String): String =
        cipher.decrypt(ciphertext)?.concatToString() ?: ""

    private suspend fun createDefaultUnlocked(): StoredConfig {
        val deviceName = "android-" + buildString {
            val random = java.security.SecureRandom()
            repeat(4) { append(HEX[random.nextInt(HEX.length)]) }
        }
        val config = StoredConfig(connection = ConnectionSettings(deviceName = deviceName))
        writeUnlocked(config)
        return config
    }

    private suspend fun readUnlocked(): StoredConfig? = withContext(Dispatchers.IO) {
        if (!file.exists()) return@withContext null
        try {
            val text = file.readText()
            val document = json.parseToJsonElement(text).jsonObject
            val version = document["schemaVersion"]?.jsonPrimitive?.intOrNull ?: 1
            val hasLegacyRevision = "configRevision" in document
            val stored = json.decodeFromString<StoredConfig>(text)
            migrateStoredConfig(stored, version).also { if (version != 2 || hasLegacyRevision) writeUnlocked(it) }
        } catch (_: SerializationException) {
            quarantineUnlocked()
            null
        }
    }

    /** 配置文件损坏时先隔离保留原文（不静默销毁用户数据），再以默认配置继续。 */
    private fun quarantineUnlocked() {
        val backup = File(file.parentFile, "config.json.corrupt-${System.currentTimeMillis()}")
        if (!file.renameTo(backup)) {
            throw IllegalStateException("配置文件损坏且无法隔离：${file.path}")
        }
    }

    private suspend fun writeUnlocked(config: StoredConfig): Unit = withContext(Dispatchers.IO) {
        file.parentFile?.mkdirs()
        val temp = File(file.parentFile, file.name + ".tmp")
        FileOutputStream(temp).use { stream ->
            stream.write(json.encodeToString(config).toByteArray(Charsets.UTF_8))
            stream.channel.force(true)
        }
        if (!temp.renameTo(file)) {
            temp.delete()
            throw IllegalStateException("配置写入失败：无法原子替换 ${file.path}")
        }
    }

    private companion object {
        const val HEX = "0123456789abcdef"
        val stores = ConcurrentHashMap<String, SharedStore>()
    }
}

private class SharedStore {
    val mutex = Mutex()
    val state = MutableStateFlow<StoredConfig?>(null)
}
