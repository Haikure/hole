package dev.hole.corebridge

import android.content.Context
import dev.hole.core.mobile.EventSink
import dev.hole.core.mobile.Mobile
import go.Seq
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.flowOn
import kotlinx.coroutines.flow.receiveAsFlow
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext

// Java names/signatures were checked against the generated AAR using javap.
// Construct, collect and close on a background dispatcher. Only application
// context reaches Seq; this object and its Go callback never retain an Activity.
class CoreClient(context: Context) : CoreController {
    private val sampler = SnapshotSampler()
    private val lifecycle = Mutex()
    private val network = AndroidNetworkProvider(context.applicationContext)
    private val pendingNetworks = Channel<String>(Channel.CONFLATED)
    override val networkEvents = pendingNetworks.receiveAsFlow()
    private val engine = run {
        Seq.setContext(context.applicationContext)
        check(Mobile.version() == 1L) { "桥接 API 版本不匹配" }
        Mobile.newEngineWithNetworkBinding(object : EventSink {
            override fun onEvent(eventJSON: String) {
                // Do not call Go lifecycle methods from its callback thread.
                sampler.wake()
            }
        }, network)
    }

    override val snapshots = sampler.snapshots { CoreSnapshot.fromJson(engine.snapshotJSON()) }.flowOn(Dispatchers.IO)
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
        withContext(Dispatchers.IO) { try { network.close(); engine.stop() } finally { sampler.wake() } }
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

    override fun close() {
        network.close()
        engine.close()
        sampler.close()
        pendingNetworks.close()
    }
}
