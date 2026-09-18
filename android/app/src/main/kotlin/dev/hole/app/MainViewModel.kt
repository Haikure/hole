package dev.hole.app

import android.app.Application
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.content.ServiceConnection
import android.os.IBinder
import java.lang.ref.WeakReference
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import dev.hole.app.config.ConfigRepository
import dev.hole.app.config.ConsumeEntry
import dev.hole.app.config.ConnectionSettings
import dev.hole.app.config.ProvideEntry
import dev.hole.app.config.SecretCipher
import dev.hole.app.config.ImportPreview
import dev.hole.app.config.StoredConfig
import dev.hole.app.config.IceSettings
import dev.hole.app.config.TurnSettings
import dev.hole.app.config.ThemeMode
import dev.hole.app.config.ThemeStyle
import dev.hole.app.config.withDynamicColor
import dev.hole.app.config.findConnectionError
import dev.hole.app.config.splitListField
import dev.hole.corebridge.CoreSnapshot
import kotlinx.coroutines.Job
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/** 已保存配置的 UI 状态。明文密码只存在于本状态中，不落盘、不进快照。 */
data class ConfigUiState(
    val loaded: Boolean = false,
    val config: StoredConfig = StoredConfig(),
    val password: String = "",
    val token: String = "",
    val lastError: String? = null,
    val turnCredential: String = "",
)

class MainViewModel(application: Application) : AndroidViewModel(application) {
    private val mutableState = MutableStateFlow(CoreSnapshot())
    val state = mutableState.asStateFlow()

    private val repository = ConfigRepository(application)
    private val cipher = SecretCipher()
    private val mutableConfigState = MutableStateFlow(ConfigUiState())
    val configState = mutableConfigState.asStateFlow()

    /** 启停命令失败反馈（区别于核心快照 error）；由服务转发。 */
    private val mutableCommandError = MutableStateFlow<String?>(null)
    val commandError = mutableCommandError.asStateFlow()

    private var collector: Job? = null
    private var commandCollector: Job? = null
    private var serviceReference = WeakReference<EngineService>(null)
    private val visibleOwners = mutableSetOf<Any>()
    private val telemetryOwner = Any()

    fun setUiVisible(owner: Any, visible: Boolean) {
        if (visible) visibleOwners.add(owner) else visibleOwners.remove(owner)
        serviceReference.get()?.setUiVisible(telemetryOwner, visibleOwners.isNotEmpty())
    }
    private val connection = object : ServiceConnection {
        override fun onServiceConnected(name: ComponentName, binder: IBinder) {
            collector?.cancel()
            commandCollector?.cancel()
            val service = (binder as EngineService.LocalBinder).service
            serviceReference = WeakReference(service)
            service.setUiVisible(telemetryOwner, visibleOwners.isNotEmpty())
            collector = viewModelScope.launch { service.state.collect { mutableState.value = it } }
            commandCollector = viewModelScope.launch {
                service.commandError.collect { mutableCommandError.value = it }
            }
        }

        override fun onServiceDisconnected(name: ComponentName) {
            collector?.cancel()
            commandCollector?.cancel()
            serviceReference.clear()
            mutableState.value = CoreSnapshot.failure("service_disconnected", "核心服务已断开")
            mutableCommandError.value = null
        }

        override fun onBindingDied(name: ComponentName) = onServiceDisconnected(name)
        override fun onNullBinding(name: ComponentName) = onServiceDisconnected(name)
    }
    private val bound = application.bindService(
        Intent(application, EngineService::class.java), connection, Context.BIND_AUTO_CREATE,
    )

    init {
        if (!bound) mutableState.value = CoreSnapshot.failure("service_bind_failed", "核心服务绑定失败")
        // Repository changes are consumed by Service only while run intent is on.
        viewModelScope.launch {
            try {
                val stored = repository.load()
                val (password, token) = repository.loadSecrets(stored)
                mutableConfigState.value = ConfigUiState(loaded = true, config = stored, password = password, token = token, turnCredential = repository.loadTurnSecret(stored))
            } catch (failure: Exception) {
                mutableConfigState.value = ConfigUiState(loaded = true, lastError = failure.message ?: "配置读取失败")
            }
        }
    }

    private suspend fun save(transform: (StoredConfig) -> StoredConfig): Boolean {
        return try {
            val stored = repository.update(transform)
            mutableConfigState.value = mutableConfigState.value.copy(config = stored, lastError = null)
            true
        } catch (failure: Exception) {
            if (failure is CancellationException) throw failure
            mutableConfigState.value = mutableConfigState.value.copy(lastError = failure.message ?: "配置保存失败")
            false
        }
    }

    fun upsertProvide(entry: ProvideEntry, index: Int? = null) = viewModelScope.launch {
        save { config ->
            val current = config.provide.toMutableList()
            val existing = current.indexOfFirst { it.entryId == entry.entryId }
            when {
                existing >= 0 -> current[existing] = entry
                index != null && index in current.indices -> current.add(index, entry)
                else -> current.add(entry)
            }
            config.copy(provide = current)
        }
    }

    fun upsertConsume(entry: ConsumeEntry, index: Int? = null) = viewModelScope.launch {
        save { config ->
            val current = config.consume.toMutableList()
            val existing = current.indexOfFirst { it.entryId == entry.entryId }
            when {
                existing >= 0 -> current[existing] = entry
                index != null && index in current.indices -> current.add(index, entry)
                else -> current.add(entry)
            }
            config.copy(consume = current)
        }
    }

    /** 返回被删条目与位置供撤销；撤销是一次新的配置修改，不复活已结束的会话。 */
    suspend fun deleteProvide(entryId: String): Pair<ProvideEntry, Int>? {
        val index = mutableConfigState.value.config.provide.indexOfFirst { it.entryId == entryId }
        if (index < 0) return null
        val removed = mutableConfigState.value.config.provide[index]
        return if (save { it.copy(provide = it.provide.filterNot { entry -> entry.entryId == entryId }) }) removed to index else null
    }

    suspend fun deleteConsume(entryId: String): Pair<ConsumeEntry, Int>? {
        val index = mutableConfigState.value.config.consume.indexOfFirst { it.entryId == entryId }
        if (index < 0) return null
        val removed = mutableConfigState.value.config.consume[index]
        return if (save { it.copy(consume = it.consume.filterNot { entry -> entry.entryId == entryId }) }) removed to index else null
    }

    fun toggleProvide(entryId: String, enabled: Boolean) = viewModelScope.launch {
        save { config ->
            config.copy(provide = config.provide.map { if (it.entryId == entryId) it.copy(enabled = enabled) else it })
        }
    }

    fun toggleConsume(entryId: String, enabled: Boolean) = viewModelScope.launch {
        save { config ->
            config.copy(consume = config.consume.map { if (it.entryId == entryId) it.copy(enabled = enabled) else it })
        }
    }

    /**
     * 保存连接设置与两种密码（在最新配置快照上合并连接字段）。
     * 返回 null 表示成功；否则返回可直接展示的错误信息。
     */
    suspend fun saveConnectionSettings(
        serverUrl: String,
        password: String,
        room: String,
        token: String,
        deviceName: String,
        sessionTimeout: String,
        candidateInterfacesText: String,
        candidateAddressesText: String,
    ): String? {
        return try {
        val connection = withContext(Dispatchers.IO) { ConnectionSettings(
            serverUrl = serverUrl.trim(),
            room = room.trim(),
            deviceName = deviceName.trim(),
            sessionTimeout = sessionTimeout.trim(),
            candidateInterfaces = splitListField(candidateInterfacesText),
            candidateAddresses = splitListField(candidateAddressesText),
            passwordCipher = if (password == mutableConfigState.value.password) mutableConfigState.value.config.connection.passwordCipher
                else if (password.isEmpty()) "" else cipher.encrypt(password.toCharArray()),
            tokenCipher = if (token == mutableConfigState.value.token) mutableConfigState.value.config.connection.tokenCipher
                else if (token.isEmpty()) "" else cipher.encrypt(token.toCharArray()),
        ) }
            // 只更新连接字段，保留事务执行时最新的外观选择及映射；使用仓库返回的版本号。
            val stored = repository.update { latest -> latest.copy(connection = latest.connection.copy(
                serverUrl = connection.serverUrl, room = connection.room, deviceName = connection.deviceName,
                sessionTimeout = connection.sessionTimeout, candidateInterfaces = connection.candidateInterfaces, candidateAddresses = connection.candidateAddresses,
                passwordCipher = connection.passwordCipher, tokenCipher = connection.tokenCipher,
            )) }
            mutableConfigState.value = mutableConfigState.value.copy(config = stored, password = password, token = token, lastError = null)
            null
        } catch (failure: Exception) {
            if (failure is CancellationException) throw failure
            failure.message ?: "配置保存失败"
        }
    }

    // 外观逐字段更新最新快照：快速连续切换时不互相覆盖。
    fun setThemeStyle(style: ThemeStyle) = viewModelScope.launch {
        save { it.copy(themeStyle = style.value) }
    }

    fun setThemeMode(mode: ThemeMode) = viewModelScope.launch {
        save { it.copy(themeMode = mode.value) }
    }

    fun setDynamicColor(style: ThemeStyle, enabled: Boolean) = viewModelScope.launch {
        save { it.withDynamicColor(style, enabled) }
    }

    override fun onCleared() {
        collector?.cancel()
        commandCollector?.cancel()
        serviceReference.get()?.setUiVisible(telemetryOwner, false)
        visibleOwners.clear()
        serviceReference.clear()
        if (bound) getApplication<Application>().unbindService(connection)
        super.onCleared()
    }

    // ---- 运行意图（A2）：总开关 → 前台服务；实际状态以核心快照为准 ----

    /** 连接设置是否满足核心必填校验；不完整时返回原因，供总开关入口提示。 */
    fun connectionError(): String? {
        val config = mutableConfigState.value
        return findConnectionError(
            config.config.connection,
            config.password.takeIf { it.isNotEmpty() },
            config.token.takeIf { it.isNotEmpty() },
        )
    }

    fun startRun() = EngineService.startRun(getApplication())

    fun stopRun() = EngineService.stopRun(getApplication())

    fun reconnect() = EngineService.reconnect(getApplication())

    suspend fun importConfig(preview: ImportPreview) {
        val engineService = requireNotNull(serviceReference.get()) { "正在等待核心服务" }
        engineService.stopForImport()
        val connection = preview.config.connection.copy(
            passwordCipher = preview.password.takeIf { it.isNotEmpty() }?.let { cipher.encrypt(it.toCharArray()) }.orEmpty(),
            tokenCipher = preview.token.takeIf { it.isNotEmpty() }?.let { cipher.encrypt(it.toCharArray()) }.orEmpty(),
            turnCredentialCipher = preview.turnCredential.takeIf { it.isNotEmpty() }?.let { cipher.encrypt(it.toCharArray()) }.orEmpty(),
        )
        val stored = repository.update { preview.config.copy(connection = connection) }
        RunStateStore(getApplication()).setResumeAfterBoot(preview.resumeAfterBoot)
        mutableConfigState.value = ConfigUiState(true, stored, preview.password, preview.token, turnCredential = preview.turnCredential)
    }
    suspend fun saveTransportSettings(mode: String, ice: IceSettings, turn: TurnSettings, credential: String, insecure: Boolean): String? = try {
        val encrypted = withContext(Dispatchers.IO) {
            if (credential == mutableConfigState.value.turnCredential) mutableConfigState.value.config.connection.turnCredentialCipher
            else credential.takeIf { it.isNotEmpty() }?.let { cipher.encrypt(it.toCharArray()) }.orEmpty()
        }
        val stored = repository.update { it.copy(connection = it.connection.copy(connectionMode = mode, ice = ice, turn = turn, turnCredentialCipher = encrypted, allowInsecureSignal = insecure)) }
        mutableConfigState.value = mutableConfigState.value.copy(config = stored, turnCredential = credential, lastError = null)
        null
    } catch (failure: Exception) { if (failure is CancellationException) throw failure; failure.message ?: "连接方式保存失败" }

}
