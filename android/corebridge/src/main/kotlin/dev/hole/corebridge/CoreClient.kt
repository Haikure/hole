package dev.hole.corebridge

import android.content.Context
import android.content.pm.PackageManager
import dev.hole.core.mobile.EventSink
import dev.hole.core.mobile.Mobile
import go.Seq
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.flowOn
import kotlinx.coroutines.flow.receiveAsFlow
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import org.json.JSONObject

// Java names/signatures were checked against the generated AAR using javap.
// Construct, collect and close on a background dispatcher. Only application
// context reaches Seq; this object and its Go callback never retain an Activity.
class CoreClient(context: Context) : CoreController {
    private val appContext = context.applicationContext
    private val sampler = SnapshotSampler()
    private val lifecycle = Mutex()
    private val network = AndroidNetworkProvider(appContext)
    private val pendingNetworks = Channel<String>(Channel.CONFLATED)
    override val networkEvents = pendingNetworks.receiveAsFlow()
    private val engine = run {
        Seq.setContext(appContext)
        check(Mobile.version() == 1L) { "桥接 API 版本不匹配" }
        Mobile.newEngineWithNetworkBinding(object : EventSink {
            override fun onEvent(eventJSON: String) {
                // Do not call Go lifecycle methods from its callback thread.
                sampler.wake()
            }
        }, network)
    }
    @Volatile private var voiceAudio: VoiceAudio? = null

    override val snapshots = sampler.snapshots {
        val snapshot = CoreSnapshot.fromJson(engine.snapshotJSON())
        voiceAudio?.let { snapshot.copy(voice = it.snapshot(snapshot.voice)) } ?: snapshot
    }.flowOn(Dispatchers.IO)
    override fun setTelemetryActive(active: Boolean) { sampler.setUiVisible(active) }

    /**
     * 生命周期命令与快照采集、回调线程互不共享执行线程；
     * 这里的互斥只保证 Kotlin 侧命令按序提交（Go 侧另有自己的锁）。
     */
    override suspend fun start(requestJSON: String): Unit = lifecycle.withLock {
        withContext(Dispatchers.IO) {
            val registered = network.startMonitoring { pendingNetworks.trySend(it) }
            try { engine.start(requestJSON) } catch (failure: Exception) {
                if (registered) network.close()
                throw failure
            } finally { sampler.wake() }
        }
    }

    override suspend fun stop(): Unit = lifecycle.withLock {
        withContext(Dispatchers.IO) {
            try { voiceAudio?.close(); voiceAudio = null; network.close(); engine.stop() } finally { sampler.wake() }
        }
    }

    override suspend fun applyConfig(requestJSON: String): Unit = lifecycle.withLock {
        withContext(Dispatchers.IO) { try { engine.applyConfig(requestJSON) } finally { sampler.wake() } }
    }

    override suspend fun networkChanged(eventJSON: String): Unit = lifecycle.withLock {
        withContext(Dispatchers.IO) { try { engine.networkChanged(eventJSON) } finally { sampler.wake() } }
    }

    override suspend fun renominateTransports(): Unit = lifecycle.withLock {
        withContext(Dispatchers.IO) { try { engine.renominateTransports() } finally { sampler.wake() } }
    }

    override suspend fun startVoiceAudio(requestJSON: String): Unit = lifecycle.withLock {
        val enabled = JSONObject(requestJSON).optJSONObject("config")?.optJSONObject("voice")?.optBoolean("enabled", false) == true
        withContext(Dispatchers.IO) {
            if (!enabled) {
                voiceAudio?.close()
                voiceAudio = null
                return@withContext
            }
            if (appContext.checkSelfPermission(android.Manifest.permission.RECORD_AUDIO) != PackageManager.PERMISSION_GRANTED) {
                error("voice_microphone_permission_required: 请先授予麦克风权限")
            }
            if (voiceAudio?.isRunning() == true) return@withContext
            voiceAudio?.close()
            voiceAudio = null
            voiceAudio = VoiceAudio(appContext, engine).also { it.start() }
            sampler.wake()
        }
    }

    override suspend fun stopVoiceAudio(): Unit = lifecycle.withLock {
        withContext(Dispatchers.IO) {
            voiceAudio?.close()
            voiceAudio = null
        }
    }

    override suspend fun setVoiceMuted(muted: Boolean): Unit = lifecycle.withLock {
        withContext(Dispatchers.IO) {
            engine.setVoiceMuted(muted)
            sampler.wake()
        }
    }

    override fun close() {
        voiceAudio?.close()
        voiceAudio = null
        network.close()
        engine.close()
        sampler.close()
        pendingNetworks.close()
    }
}
