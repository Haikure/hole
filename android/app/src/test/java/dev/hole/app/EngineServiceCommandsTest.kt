package dev.hole.app

import android.content.Intent
import android.os.Looper
import androidx.core.content.edit
import dev.hole.app.config.ConfigRepository
import dev.hole.app.config.ConnectionSettings
import dev.hole.app.config.ProvideEntry
import dev.hole.app.config.StoredConfig
import dev.hole.corebridge.CoreController
import dev.hole.corebridge.CoreSnapshot
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicInteger
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.runBlocking
import org.json.JSONObject
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config
import org.robolectric.annotation.LooperMode
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

class FixtureEngineService : EngineService() {
    override suspend fun createCore(): CoreController { gate.await(); return core }
    override suspend fun loadSecrets(config: StoredConfig): Pair<String, String> = "test-password" to "test-token"
    companion object {
        var gate = CompletableDeferred<Unit>()
        var core = RecordingCore()
    }
}
class RecordingCore : CoreController {
    override val snapshots = MutableStateFlow(CoreSnapshot(nativeReady = true, engineState = "stopped"))
    override val networkEvents = MutableSharedFlow<String>(extraBufferCapacity = 8)
    val starts = CopyOnWriteArrayList<String>()
    val applies = CopyOnWriteArrayList<String>()
    val stops = AtomicInteger()
    val networks = AtomicInteger()
    val closed = AtomicBoolean()
    val telemetry = CopyOnWriteArrayList<Boolean>()
    private var currentRequest: String? = null
    override suspend fun start(requestJSON: String) {
        if (snapshots.value.runRequested) {
            if (currentRequest != requestJSON) error("{\"code\":\"already_running\"}")
            return
        }
        currentRequest = requestJSON
        starts += requestJSON
        snapshots.value = snapshots.value.copy(configured = true, runRequested = true, engineState = "running", signalState = "joined")
    }
    override suspend fun applyConfig(requestJSON: String) {
        applies += requestJSON
        currentRequest = requestJSON
        snapshots.value = snapshots.value.copy(configured = true)
    }
    override suspend fun stop() {
        stops.incrementAndGet()
        snapshots.value = snapshots.value.copy(runRequested = false, engineState = "stopped", signalState = "disconnected")
    }
    override suspend fun networkChanged(eventJSON: String) { networks.incrementAndGet() }
    override fun reconnect() { networks.incrementAndGet() }
    override fun setTelemetryActive(active: Boolean) { telemetry += active }
    override fun close() { closed.set(true) }
}

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [26])
@LooperMode(LooperMode.Mode.PAUSED)
class EngineServiceCommandsTest {
    private val context get() = RuntimeEnvironment.getApplication()
    @Before fun prepare() = runBlocking {
        context.getSharedPreferences("run_state", 0).edit(commit = true) { clear() }
        FixtureEngineService.gate = CompletableDeferred()
        FixtureEngineService.core = RecordingCore()
        ConfigRepository(context).update { StoredConfig(connection = ConnectionSettings(serverUrl = "wss://fixture.invalid/ws", room = "room", deviceName = "android-test")) }
        Unit
    }
    private fun until(condition: () -> Boolean) {
        val end = System.nanoTime() + 5_000_000_000L
        while (System.nanoTime() < end) {
            shadowOf(Looper.getMainLooper()).idle()
            if (condition()) return
            Thread.sleep(5)
        }
        assertTrue(condition(), "service command did not converge")
    }
    private fun tick(milliseconds: Long = 350) {
        val end = System.nanoTime() + milliseconds * 1_000_000L
        while (System.nanoTime() < end) { shadowOf(Looper.getMainLooper()).idle(); Thread.sleep(5) }
    }
    @Test fun visibilityChangesSamplingWithoutRestartingTheCore() {
        FixtureEngineService.gate.complete(Unit)
        val controller = Robolectric.buildService(FixtureEngineService::class.java).create()
        val service = controller.get()
        val first = Any(); val second = Any()
        try {
            until { FixtureEngineService.core.telemetry.isNotEmpty() }
            service.setUiVisible(first, true)
            until { FixtureEngineService.core.telemetry.last() }
            service.setUiVisible(second, true)
            service.setUiVisible(first, false)
            tick(120)
            assertTrue(FixtureEngineService.core.telemetry.last())
            service.setUiVisible(second, false)
            until { !FixtureEngineService.core.telemetry.last() }
            assertTrue(FixtureEngineService.core.starts.isEmpty())
            assertEquals(0, FixtureEngineService.core.stops.get())
        } finally { controller.destroy() }
    }
    @Test fun coldStartWaitsForCoreButUserStopDoesNotWaitBehindIt() {
        val controller = Robolectric.buildService(FixtureEngineService::class.java).create()
        val service = controller.get()
        try {
            service.onStartCommand(Intent().setAction(EngineService.ACTION_START_RUN), 0, 1)
            assertNotNull(shadowOf(service).lastForegroundNotification)
            assertTrue(RunStateStore(context).isRequested())
            service.onStartCommand(Intent().setAction(EngineService.ACTION_STOP_RUN), 0, 2)
            until { !service.state.value.runRequested && shadowOf(service).isStoppedBySelf }
            assertFalse(RunStateStore(context).isRequested())
            FixtureEngineService.gate.complete(Unit)
            until { service.state.value.nativeReady }
            tick()
            assertTrue(FixtureEngineService.core.starts.isEmpty())
        } finally { controller.destroy() }
        until { FixtureEngineService.core.closed.get() }
    }
    @Test fun runningConfigChangesConvergeAndStopSuppressesLateCallbacks() = runBlocking {
        FixtureEngineService.gate.complete(Unit)
        val controller = Robolectric.buildService(FixtureEngineService::class.java).create()
        val service = controller.get()
        val core = FixtureEngineService.core
        try {
            service.onStartCommand(Intent().setAction(EngineService.ACTION_START_RUN), 0, 1)
            until { core.starts.size == 1 && service.state.value.signalState == "joined" }
            val repository = ConfigRepository(context)
            repeat(9) { index -> repository.update { it.copy(provide = listOf(ProvideEntry("p", "ssh", "tcp", "127.0.0.1", 22, index % 2 == 0))) } }
            until { core.applies.isNotEmpty() }
            val last = JSONObject(core.applies.last()).getJSONObject("config").getJSONArray("provide")
            assertEquals(1, last.length())
            val stopped = service.onStartCommand(Intent().setAction(EngineService.ACTION_STOP_RUN), 0, 2)
            assertEquals(android.app.Service.START_NOT_STICKY, stopped)
            until { core.stops.get() == 1 }
            val applied = core.applies.size
            repository.update { it.copy(provide = emptyList()) }
            core.networkEvents.tryEmit("{\"sequence\":\"99\",\"network_handle\":\"old\"}")
            tick()
            assertEquals(applied, core.applies.size)
            assertEquals(0, core.networks.get())
            assertFalse(service.state.value.runRequested)
        } finally { controller.destroy() }
    }
    @Test fun requestedServiceRecreationAndTaskRemovalKeepIntent() {
        FixtureEngineService.gate.complete(Unit)
        RunStateStore(context).setRequested(true)
        val controller = Robolectric.buildService(FixtureEngineService::class.java).create()
        try {
            val service = controller.get()
            service.onStartCommand(null, 0, 1)
            until { FixtureEngineService.core.starts.size == 1 }
            service.onTaskRemoved(Intent())
            assertTrue(RunStateStore(context).isRequested())
            val text = shadowOf(service).lastForegroundNotification.extras.toString()
            assertFalse(text.contains("test-password"))
            assertFalse(text.contains("test-token"))
        } finally { controller.destroy() }
    }

    @Test fun correctingAnInitiallyInvalidConfigStartsTheRequestedRun() = runBlocking {
        val repository = ConfigRepository(context)
        val entry = ProvideEntry("one", "duplicate", "tcp", "127.0.0.1", 22)
        repository.update { it.copy(provide = listOf(entry, entry.copy(entryId = "two"))) }
        FixtureEngineService.gate.complete(Unit)
        val controller = Robolectric.buildService(FixtureEngineService::class.java).create()
        try {
            val service = controller.get()
            service.onStartCommand(Intent().setAction(EngineService.ACTION_START_RUN), 0, 1)
            until { service.commandError.value != null }
            assertTrue(FixtureEngineService.core.starts.isEmpty())
            repository.update { it.copy(provide = listOf(entry)) }
            until { FixtureEngineService.core.starts.size == 1 && service.state.value.signalState == "joined" }
            assertTrue(service.state.value.runRequested)
        } finally { controller.destroy() }
    }
}
