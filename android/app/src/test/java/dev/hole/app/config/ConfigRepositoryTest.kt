package dev.hole.app.config

import android.content.Context
import java.io.File
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.launch
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.robolectric.annotation.Config

/**
 * 配置仓库的持久化行为，SDK 与 minSdk 一致（26）。
 * 只使用 Application Context 与文件系统，不触碰 Keystore。
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [26])
class ConfigRepositoryTest {

    private val context: Context get() = RuntimeEnvironment.getApplication()
    private val configDir get() = File(context.filesDir, "config")

    @Test
    fun defaultDeviceNamePersistsAcrossReloads() = runBlocking {
        val first = ConfigRepository(context).load()
        assertTrue(
            first.connection.deviceName.startsWith("android-"),
            "首次运行应生成 android-xxxx 设备名，实际为 ${first.connection.deviceName}",
        )
        // 重新构造仓库（等价于重开应用）读取到同一份配置。
        val second = ConfigRepository(context).load()
        assertEquals(first.connection.deviceName, second.connection.deviceName)
    }

    @Test
    fun corruptConfigIsQuarantinedInsteadOfDiscarded() = runBlocking {
        configDir.mkdirs()
        val file = File(configDir, "config.json")
        file.writeText("{ 这不是合法配置")

        val loaded = ConfigRepository(context).load()

        assertTrue(loaded.connection.deviceName.startsWith("android-"))
        val backups = configDir.listFiles { f -> f.name.startsWith("config.json.corrupt-") }
        assertTrue(!backups.isNullOrEmpty(), "损坏的配置应先隔离保留，而不是被直接覆盖")
        assertEquals("{ 这不是合法配置", backups[0].readText())
    }

    @Test
    fun updatesPersistConfigurationAndAppearance() = runBlocking {
        val repository = ConfigRepository(context)
        val initial = repository.load()

        val themed = repository.update { it.copy(themeMode = "dark") }
        assertEquals("dark", themed.themeMode)

        val appearance = repository.update { it.copy(themeMode = "light") }
        assertEquals("light", appearance.themeMode)
        val changed = repository.update { it.copy(connection = it.connection.copy(room = "new-room")) }
        assertEquals("new-room", changed.connection.room)
    }

    @Test
    fun disabledEntriesCanBeEditedAndEnabledLater() = runBlocking {
        val repository = ConfigRepository(context)
        val first = repository.update { it.copy(provide = listOf(ProvideEntry("one", "ssh", "tcp", "127.0.0.1", 22, false))) }
        val edited = repository.update { it.copy(provide = it.provide.map { entry -> entry.copy(port = 2222) }) }
        assertEquals(2222, edited.provide.single().port)
        val enabled = repository.update { it.copy(provide = it.provide.map { entry -> entry.copy(enabled = true) }) }
        assertTrue(enabled.provide.single().enabled)
    }

    @Test
    fun deleteAndUndoPreserveStableSavedIdentity() = runBlocking {
        val repository = ConfigRepository(context)
        val entry = ProvideEntry("stable-id", "ssh", "tcp", "127.0.0.1", 22)
        val before = repository.update { it.copy(provide = listOf(entry)) }
        val deleted = repository.update { it.copy(provide = emptyList()) }
        val restored = repository.update { it.copy(provide = listOf(entry)) }
        assertTrue(deleted.provide.isEmpty())
        assertEquals(before.provide, restored.provide)
    }

    @Test
    fun serviceAndViewModelRepositoriesShareOneTransactionLock() = runBlocking {
        val one = ConfigRepository(context)
        val two = ConfigRepository(context)
        one.load()
        coroutineScope {
            launch { repeat(15) { index -> one.update { config -> config.copy(provide = config.provide + ProvideEntry("p$index", "p$index", "tcp", "127.0.0.1", 10000 + index)) } } }
            launch { repeat(15) { index -> two.update { config -> config.copy(consume = config.consume + ConsumeEntry("c$index", "c$index", "127.0.0.1", 20000 + index)) } } }
        }
        val stored = one.load()
        assertEquals(15, stored.provide.size)
        assertEquals(15, stored.consume.size)
        assertEquals(15, stored.provide.size)
    }

    @Test
    fun futureSchemaIsNotOverwrittenByAnOlderApp() = runBlocking {
        configDir.mkdirs()
        val file = File(configDir, "config.json")
        val data = """{"schemaVersion":999,"connection":{"deviceName":"future"}}"""
        file.writeText(data)
        kotlin.test.assertFailsWith<IllegalArgumentException> { ConfigRepository(context).load() }
        assertEquals(data, file.readText())
    }

    @Test
    fun legacyCoreRevisionIsDroppedWhenConfigIsLoaded() = runBlocking {
        configDir.mkdirs()
        val file = File(configDir, "config.json")
        file.writeText("""{"configRevision":9,"connection":{"deviceName":"saved-device"}}""")

        ConfigRepository(context).load()

        assertTrue(!file.readText().contains("configRevision"))
    }

    @Test
    fun themeSelectionPersistsAndPreservesLegacyConnection() = runBlocking {
        configDir.mkdirs()
        File(configDir, "config.json").writeText(
            """{"themeMode":"dark","dynamicColor":false,"connection":{"serverUrl":"wss://example.test/ws","deviceName":"saved-device","passwordCipher":"saved-cipher"}}""",
        )
        val repository = ConfigRepository(context)
        val legacy = repository.load()
        assertEquals("material", legacy.themeStyle)

        repository.update { it.copy(themeStyle = ThemeStyle.MIUIX.value) }
        val restored = ConfigRepository(context).load()
        assertEquals(legacy.copy(themeStyle = "miuix"), restored)
    }

    @Test
    fun rapidAppearanceUpdatesMergeWithoutLosingFields() = runBlocking {
        val repository = ConfigRepository(context)
        val initial = repository.load()
        coroutineScope {
            launch { repository.update { it.copy(themeStyle = "miuix") } }
            launch { repository.update { it.copy(themeMode = "dark") } }
            launch { repository.update { it.copy(dynamicColor = false) } }
        }
        assertEquals(
            initial.copy(themeStyle = "miuix", themeMode = "dark", dynamicColor = false),
            ConfigRepository(context).load(),
        )
    }
}
