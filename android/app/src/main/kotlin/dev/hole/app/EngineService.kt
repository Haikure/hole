package dev.hole.app

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Binder
import android.os.Build
import android.os.IBinder
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import androidx.core.content.ContextCompat
import dev.hole.app.config.ConfigRepository
import dev.hole.app.config.StoredConfig
import dev.hole.app.config.toStartRequest
import dev.hole.corebridge.CoreClient
import dev.hole.corebridge.CoreController
import dev.hole.corebridge.CoreSnapshot
import java.util.concurrent.atomic.AtomicLong
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Job
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.FlowPreview
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.cancelChildren
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.debounce
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import org.json.JSONObject

/** One engine, one serialized command loop; Activity lifetime never owns a run. */
@OptIn(FlowPreview::class)
open class EngineService : Service() {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val mutableState = MutableStateFlow(CoreSnapshot())
    val state = mutableState.asStateFlow()
    private val mutableCommandError = MutableStateFlow<String?>(null)
    val commandError = mutableCommandError.asStateFlow()
    private val visibleHosts = mutableSetOf<Any>()
    private val telemetryActive = MutableStateFlow(false)
    private val repository by lazy { ConfigRepository(applicationContext) }
    private val runStore by lazy { RunStateStore(applicationContext) }
    private val recoveryLock by lazy { RecoveryWakeLock(applicationContext) }
    private val json = Json { encodeDefaults = true }
    private val ready = CompletableDeferred<CoreController>()
    private val commands = Channel<Command>(Channel.UNLIMITED)
    private val epoch = AtomicLong()
    @Volatile private var client: CoreController? = null
    @Volatile private var requested = false
    @Volatile private var foregroundActive = false
    @Volatile private var currentRoom = ""
    @Volatile private var secrets: List<String> = emptyList()
    @Volatile private var pendingCommand: Job? = null
    private var lastNotification = ""
    private val binder = LocalBinder()

    private sealed interface Command {
        data class Start(val epoch: Long) : Command
        data class Apply(val epoch: Long) : Command
        data class Network(val epoch: Long, val event: String) : Command
        data class Stop(val completion: CompletableDeferred<Unit>? = null) : Command
        data object Renominate : Command
    }
    inner class LocalBinder : Binder() { val service: EngineService get() = this@EngineService }
    protected open suspend fun createCore(): CoreController = CoreClient(applicationContext)
    protected open suspend fun loadSecrets(config: StoredConfig): Pair<String, String> = repository.loadSecrets(config)

    fun setUiVisible(owner: Any, visible: Boolean) = synchronized(visibleHosts) {
        if (visible) visibleHosts.add(owner) else visibleHosts.remove(owner)
        telemetryActive.value = visibleHosts.isNotEmpty()
    }

    override fun onCreate() {
        super.onCreate()
        getSystemService(NotificationManager::class.java).createNotificationChannel(
            NotificationChannel(CHANNEL_ID, "转发连接状态", NotificationManager.IMPORTANCE_LOW).apply {
                description = "持续转发的连接状态、会话数量与停止操作"
            },
        )
        scope.launch {
            var core: CoreController? = null
            try {
                core = createCore()
                client = core
                ready.complete(core)
                val loaded = core
                launch { telemetryActive.collect { loaded.setTelemetryActive(it) } }
                launch {
                    loaded.networkEvents.debounce(250).collect {
                        if (requested) commands.send(Command.Network(epoch.get(), it))
                    }
                }
                loaded.snapshots.collect { snapshot ->
                    withContext(Dispatchers.Main.immediate) {
                        mutableState.value = snapshot.copy(runRequested = requested)
                        recoveryLock.update(requested && snapshot.signalState != "joined")
                        runCatching { updateNotification(mutableState.value) }
                    }
                }
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (failure: Throwable) {
                ready.completeExceptionally(failure)
                mutableState.value = CoreSnapshot.failure(
                    if (failure is LinkageError) "native_load_failed" else "bridge_failed",
                    describeFault(failure),
                ).copy(runRequested = requested)
                recoveryLock.update(false)
            } finally {
                ready.cancel()
                coroutineContext.cancelChildren()
                withContext(NonCancellable) { core?.close() }
                client = null
            }
        }
        scope.launch {
            for (command in commands) {
                try {
                    when (command) {
                        is Command.Start -> cancellableCommand { applyLatest(command.epoch) }
                        is Command.Apply -> cancellableCommand { applyLatest(command.epoch) }
                        is Command.Network -> cancellableCommand { if (isCurrent(command.epoch)) ready.await().networkChanged(command.event) }
                        Command.Renominate -> if (requested) ready.await().renominateTransports()
                        is Command.Stop -> {
                            try { stopInternal(); command.completion?.complete(Unit) }
                            catch (failure: Throwable) { command.completion?.completeExceptionally(failure); throw failure }
                        }
                    }
                } catch (cancelled: CancellationException) {
                    throw cancelled
                } catch (failure: Throwable) {
                    mutableCommandError.value = describeFault(failure)
                }
            }
        }
        scope.launch {
            try {
                repository.load()
                repository.changes.debounce(250).collect {
                    if (requested) commands.send(Command.Apply(epoch.get()))
                }
            } catch (failure: Exception) {
                if (failure is CancellationException) throw failure
                mutableCommandError.value = describeFault(failure)
            }
        }
        // System-permitted service recreation restores intent, not old TCP sockets.
        if (runStore.isRequested()) requestStart()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_START_RUN -> requestStart()
            ACTION_STOP_RUN -> {
                runStore.setRequested(false)
                requested = false
                epoch.incrementAndGet() // Invalidate pending start/config loads immediately.
                pendingCommand?.cancel()
                mutableState.value = mutableState.value.copy(runRequested = false, engineState = "stopping")
                recoveryLock.update(false)
                commands.trySend(Command.Stop())
            }
            ACTION_RENOMINATE -> if (requested) commands.trySend(Command.Renominate)
            else -> if (runStore.isRequested()) { if (!requested) requestStart() } else stopSelf()
        }
        return if (requested) START_STICKY else START_NOT_STICKY
    }

    private fun requestStart() {
        runStore.setRequested(true)
        requested = true
        mutableState.value = mutableState.value.copy(runRequested = true)
        startForegroundCompat() // Always before disk/JNI/network work.
        commands.trySend(Command.Start(epoch.incrementAndGet()))
    }

    private fun isCurrent(value: Long) = requested && epoch.get() == value

    // User Stop interrupts a cold native wait / disk read / reconfiguration
    // instead of waiting behind it. Only this actor starts work; stop itself is
    // never cancelled by a later Start and completes before the next run.
    private suspend fun cancellableCommand(work: suspend () -> Unit) = coroutineScope {
        val task = async(start = CoroutineStart.LAZY) { work() }
        pendingCommand = task
        try { task.start(); task.await() }
        catch (_: CancellationException) { coroutineContext.ensureActive() }
        finally { if (pendingCommand === task) pendingCommand = null }
    }

    private suspend fun applyLatest(value: Long) {
        if (!isCurrent(value)) return
        val core = ready.await() // A cold start waits for native initialization instead of failing spuriously.
        if (!isCurrent(value)) return
        val stored = repository.load()
        val (password, token) = loadSecrets(stored)
        val turnSecret = repository.loadTurnSecret(stored)
        secrets = listOf(password, token, turnSecret)
        val request = json.encodeToString(toStartRequest(stored, password, token, turnCredential = turnSecret))
        if (!isCurrent(value)) return
        currentRoom = redactDiagnostic(stored.connection.room, secrets)
        // User intent is authoritative even after an initially invalid config.
        // Start is a no-op for an identical run; a different live request is
        // rejected with already_running and goes through real ApplyConfig.
        // This never Stop/Starts a running engine to imitate reconfiguration.
        try { core.start(request) } catch (failure: Exception) {
            if (failure.message?.contains("already_running") == true) core.applyConfig(request) else throw failure
        }
        mutableCommandError.value = null
    }

    private suspend fun stopInternal() {
        try {
            client?.stop()
            mutableCommandError.value = null
        } finally {
            withContext(Dispatchers.Main.immediate) {
                if (!requested) {
                    foregroundActive = false
                    ServiceCompat.stopForeground(this@EngineService, ServiceCompat.STOP_FOREGROUND_REMOVE)
                    stopSelf()
                }
            }
        }
    }

    suspend fun stopForImport() {
        withContext(Dispatchers.Main.immediate) {
            runStore.setRequested(false)
            requested = false
            epoch.incrementAndGet()
            pendingCommand?.cancel()
            recoveryLock.update(false)
            mutableState.value = mutableState.value.copy(runRequested = false)
        }
        val completion = CompletableDeferred<Unit>()
        commands.send(Command.Stop(completion))
        completion.await()
    }

    private fun describeFault(failure: Throwable): String {
        val raw = failure.message ?: "核心命令执行失败"
        val message = runCatching { JSONObject(raw).optString("message").ifEmpty { raw } }.getOrDefault(raw)
        return redactDiagnostic(message, secrets)
    }

    private fun startForegroundCompat() {
        val type = if (Build.VERSION.SDK_INT >= 34) ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE else 0
        ServiceCompat.startForeground(this, NOTIFICATION_ID, buildNotification(mutableState.value), type)
        foregroundActive = true
    }

    private fun updateNotification(snapshot: CoreSnapshot) {
        if (!foregroundActive) return
        // No polling notification writes when only byte counters change.
        val signature = "$currentRoom/${snapshot.signalState}/${snapshot.engineState}/${snapshot.provideCount}/${snapshot.consumeCount}/${snapshot.tcpSessions}/${snapshot.udpSessions}"
        if (signature == lastNotification) return
        lastNotification = signature
        getSystemService(NotificationManager::class.java).notify(NOTIFICATION_ID, buildNotification(snapshot))
    }

    private fun buildNotification(snapshot: CoreSnapshot): Notification {
        val open = PendingIntent.getActivity(this, 0, Intent(this, MainActivity::class.java).putExtra("open_details", true), PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE)
        val stop = PendingIntent.getService(this, 1, Intent(this, EngineService::class.java).setAction(ACTION_STOP_RUN), PendingIntent.FLAG_IMMUTABLE)
        val text = buildString {
            if (currentRoom.isNotBlank()) append("房间 $currentRoom · ")
            append(dev.hole.app.ui.signalLabel(snapshot.signalState))
            append(" · ${snapshot.tcpSessions} TCP / ${snapshot.udpSessions} UDP")
        }
        return NotificationCompat.Builder(this, CHANNEL_ID)
            .setSmallIcon(R.drawable.ic_notification).setContentTitle("hole 转发服务")
            .setContentText(text).setStyle(NotificationCompat.BigTextStyle().bigText(text))
            .setOngoing(true).setOnlyAlertOnce(true).setVisibility(NotificationCompat.VISIBILITY_PRIVATE)
            .setForegroundServiceBehavior(NotificationCompat.FOREGROUND_SERVICE_IMMEDIATE)
            .setContentIntent(open).addAction(0, "停止", stop).build()
    }

    override fun onBind(intent: Intent): IBinder = binder
    override fun onTaskRemoved(rootIntent: Intent?) {
        // Removing the UI is not user Stop. The existing foreground service remains;
        // no alarm, permanent wake lock or self-restart loop is created.
        super.onTaskRemoved(rootIntent)
    }
    override fun onDestroy() {
        requested = false
        epoch.incrementAndGet()
        recoveryLock.close()
        commands.close()
        scope.cancel()
        super.onDestroy()
    }

    companion object {
        const val ACTION_START_RUN = "dev.hole.app.action.START_RUN"
        const val ACTION_STOP_RUN = "dev.hole.app.action.STOP_RUN"
        const val ACTION_RENOMINATE = "dev.hole.app.action.RENOMINATE"
        private const val CHANNEL_ID = "connection"
        private const val NOTIFICATION_ID = 1
        fun startRun(context: android.content.Context) {
            ContextCompat.startForegroundService(context, Intent(context, EngineService::class.java).setAction(ACTION_START_RUN))
        }
        fun stopRun(context: android.content.Context) {
            context.startService(Intent(context, EngineService::class.java).setAction(ACTION_STOP_RUN))
        }
        fun renominate(context: android.content.Context) {
            context.startService(Intent(context, EngineService::class.java).setAction(ACTION_RENOMINATE))
        }
    }
}
