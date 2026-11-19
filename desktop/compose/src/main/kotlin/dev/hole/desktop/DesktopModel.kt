package dev.hole.desktop

import dev.hole.app.config.*
import dev.hole.corebridge.CoreSnapshot
import dev.hole.desktop.bridge.DesktopCoreClient
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.*
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json

internal class DesktopModel(val storage: DesktopStorage, val core: DesktopCoreClient) {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val operations = Mutex()
    private val json = Json { encodeDefaults = true }
    private val mutableConfig = MutableStateFlow(storage.load())
    val config = mutableConfig.asStateFlow()
    val snapshot = MutableStateFlow(CoreSnapshot())
    val error = MutableStateFlow<String?>(null)

    init {
        scope.launch {
            try { core.snapshots.collect { snapshot.value = it } }
            catch (failure: Exception) { snapshot.value = CoreSnapshot.failure("native_bridge_failed", failure.message ?: "原生桥接错误") }
        }
        scope.launch {
            if (config.value.resumeOnOpen && config.value.runRequested) {
                try { toggleRun(true) }
                catch (cancelled: CancellationException) { throw cancelled }
                catch (failure: Exception) { error.value = failure.message }
            }
        }
    }
    private fun request(state: DesktopState) = json.encodeToString(toStartRequest(state.config, state.password, state.token, state.turnCredential))
    private suspend fun persist(next: DesktopState) {
        storage.save(next)
        mutableConfig.value = next
    }
    suspend fun update(transform: (DesktopState) -> DesktopState) = operations.withLock {
        withContext(Dispatchers.IO) {
            val next = transform(config.value)
            ConfigExchange.validateDraft(next.config)
            val runtimeChanged = next.config.connection != config.value.config.connection || next.config.provide != config.value.config.provide ||
                next.config.consume != config.value.config.consume || next.config.voice != config.value.config.voice ||
                next.password != config.value.password || next.token != config.value.token || next.turnCredential != config.value.turnCredential
            val running = snapshot.value.runRequested || config.value.runRequested
            val runtime = if (running && runtimeChanged) request(next) else null
            persist(next)
            if (runtime != null) {
                core.applyConfig(runtime)
                if (next.config.voice.enabled) core.startVoiceAudio(runtime)
            }
        }
    }
    suspend fun toggleRun(enabled: Boolean) = operations.withLock {
        withContext(Dispatchers.IO) {
            val next = config.value.copy(runRequested = enabled)
            if (enabled) {
                val runtime = request(next)
                core.start(runtime)
                persist(next)
                if (next.config.voice.enabled) core.startVoiceAudio(runtime)
            } else {
                persist(next)
                core.stop()
            }
        }
    }
    fun action(block: suspend () -> Unit) {
        scope.launch {
            try { block(); error.value = null }
            catch (cancelled: CancellationException) { throw cancelled }
            catch (failure: Exception) { error.value = failure.message ?: "操作失败" }
        }
    }
    suspend fun retryAudio() {
        val state = config.value
        check(snapshot.value.runRequested && state.config.voice.enabled) { "请先启动连接并启用语音" }
        core.stopVoiceAudio()
        core.startVoiceAudio(request(state))
    }
    suspend fun import(text: String) = operations.withLock {
        withContext(Dispatchers.IO) {
            val imported = storage.importDocument(text, config.value)
            core.stop()
            persist(imported.copy(runRequested = false))
        }
    }
    suspend fun export(includeSecrets: Boolean, backup: Boolean): String = withContext(Dispatchers.IO) {
        val state = config.value
        if (backup) ConfigExchange.backup(state.config, state.password, state.token, includeSecrets, false, state.turnCredential)
        else ConfigExchange.cli(state.config, state.password, state.token, includeSecrets, state.turnCredential)
    }
    suspend fun shutdown() {
        // Preserve the user's run intent for explicit "resume on open"; close still releases the engine.
        scope.coroutineContext[Job]?.cancelAndJoin()
        withContext(Dispatchers.IO) { try { core.close() } finally { storage.close() } }
    }
}
