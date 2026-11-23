package dev.hole.desktop.bridge

import dev.hole.corebridge.*
import java.net.NetworkInterface
import java.time.Instant
import java.util.concurrent.atomic.AtomicLong
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.*
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import org.json.JSONObject

internal class DesktopCoreClient(
    private val audioFactory: (Long, AtomicLong, () -> Boolean) -> DesktopAudio = { handle, sequence, muted ->
        DesktopVoiceAudio(handle, sequence, muted)
    },
) : CoreController {
    private val handle = NativeCore.open().also { check(it != 0L) { "无法创建 Go 核心" } }
    private val lifecycle = Mutex()
    private val sampler = SnapshotSampler()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val networks = MutableSharedFlow<String>(extraBufferCapacity = 1)
    override val networkEvents = networks.asSharedFlow()
    @Volatile private var closed = false
    @Volatile private var audio: DesktopAudio? = null
    @Volatile private var audioError: String? = null
    @Volatile private var muted = false
    private val captureSequence = AtomicLong()

    init {
        try { check(NativeCore.invoke(handle, "version") == "1") { "桥接 API 版本不匹配" } }
        catch (failure: Exception) { NativeCore.close(handle); scope.cancel(); throw failure }
        scope.launch {
            var previous: String? = null
            var sequence = 0L
            while (isActive) {
                val signature = runCatching { NetworkInterface.getNetworkInterfaces().toList().sortedBy { it.name }.joinToString(";") {
                    "${it.name}:${it.isUp}:${it.inetAddresses.toList().map { address -> address.hostAddress }.sorted()}"
                } }.getOrNull()
                if (signature != null && previous != null && signature != previous) {
                    val event = JSONObject().put("sequence", (++sequence).toString()).put("network_handle", "0").toString()
                    runCatching { networkChanged(event) }
                    networks.tryEmit(event)
                }
                if (signature != null) previous = signature
                delay(3000)
            }
        }
    }

    override val snapshots: Flow<CoreSnapshot> = sampler.snapshots {
        val snapshot = CoreSnapshot.fromJson(NativeCore.invoke(handle, "snapshot"))
        snapshot.copy(voice = snapshot.voice.copy(
            audioState = audio?.state ?: if (audioError != null) "error" else "stopped",
            audioError = audioError ?: audio?.failure,
            audioRoute = if (audio != null) "系统默认音频设备" else "",
            microphoneRoute = if (audio != null) "系统默认麦克风" else "",
            playbackFrames = (audio?.playedFrames ?: 0).toString(),
        ))
    }.flowOn(Dispatchers.IO)

    private suspend fun command(operation: String, input: String = "") = lifecycle.withLock {
        withContext(Dispatchers.IO) {
            check(!closed) { "核心已关闭" }
            try { NativeCore.invoke(handle, operation, input); Unit } finally { sampler.wake() }
        }
    }
    override suspend fun start(requestJSON: String) = command("start", requestJSON)
    override suspend fun stop() { stopVoiceAudio(); command("stop") }
    override suspend fun applyConfig(requestJSON: String) = lifecycle.withLock {
        withContext(Dispatchers.IO) {
            check(!closed) { "核心已关闭" }
            try {
                NativeCore.invoke(handle, "apply", requestJSON)
                if (!voiceEnabled(requestJSON)) { audio?.close(); audio = null; audioError = null }
            } finally { sampler.wake() }
        }
    }
    override suspend fun networkChanged(eventJSON: String) = command("network", eventJSON)
    override suspend fun renominateTransports() = command("renominate")
    override suspend fun startNATProbe(optionsJSON: String) = command("nat_start", optionsJSON)
    override suspend fun cancelNATProbe() = command("nat_cancel")
    override suspend fun setVoiceMuted(muted: Boolean) = lifecycle.withLock {
        withContext(Dispatchers.IO) {
            check(!closed) { "核心已关闭" }
            val previous = this@DesktopCoreClient.muted
            this@DesktopCoreClient.muted = muted
            try { NativeCore.invoke(handle, "mute", muted.toString()) }
            catch (failure: Exception) {
                if (failure !is NativeCoreFault || !failure.voicePending) {
                    this@DesktopCoreClient.muted = previous
                    throw failure
                }
            } finally { sampler.wake() }
            Unit
        }
    }
    override fun setTelemetryActive(active: Boolean) = sampler.setUiVisible(active)
    override suspend fun natPunchProcess(transportId: String, afterSequence: String): NATPunchProcess = withContext(Dispatchers.IO) {
        NATPunchProcess.fromJson(readProcess(transportId, afterSequence))
    }
    private fun readProcess(transportId: String, after: String): String = NativeCore.invoke(handle, "punch",
        JSONObject().put("transport", transportId).put("after", after).toString())
    override suspend fun exportNATPunchProcess(transportId: String): String = withContext(Dispatchers.IO) {
        val process = JSONObject(readProcess(transportId, "0"))
        check(process.optString("transport_id") == transportId && (process.optJSONArray("attempts")?.length() ?: 0) > 0) { "当前连接暂无过程记录" }
        val snapshot = JSONObject(NativeCore.invoke(handle, "snapshot"))
        JSONObject().put("format", "hole-nat-punch-process").put("version", 1).put("exported_at", Instant.now().toString())
            .put("core_version", snapshot.optString("core_version")).put("core_build_id", snapshot.optString("core_build_id"))
            .put("nat_punch_process", process).toString(2) + "\n"
    }
    suspend fun exportSnapshot(): String = withContext(Dispatchers.IO) {
        JSONObject(NativeCore.invoke(handle, "snapshot")).toString(2) + "\n"
    }
    private fun voiceEnabled(requestJSON: String) =
        JSONObject(requestJSON).getJSONObject("config").optJSONObject("voice")?.optBoolean("enabled") == true

    override suspend fun startVoiceAudio(requestJSON: String, microphoneSource: String) = lifecycle.withLock {
        withContext(Dispatchers.IO) {
            check(!closed) { "核心已关闭" }
            if (!voiceEnabled(requestJSON)) return@withContext
            if (audio?.state == "running") return@withContext
            audio?.close(); audio = null; audioError = null
            try {
                audio = audioFactory(handle, captureSequence) { muted }.also { it.start() }
            } catch (failure: Exception) {
                audioError = "音频设备启动失败：${failure.message}"
                throw IllegalStateException(audioError, failure)
            } finally { sampler.wake() }
        }
    }
    override suspend fun stopVoiceAudio() = lifecycle.withLock {
        withContext(Dispatchers.IO) { audio?.close(); audio = null; audioError = null; sampler.wake() }
    }
    override fun close() = runBlocking {
        lifecycle.withLock {
            if (!closed) {
                closed = true
                scope.cancel()
                sampler.close()
                try { audio?.close() } finally { audio = null; NativeCore.close(handle) }
            }
        }
    }
}
