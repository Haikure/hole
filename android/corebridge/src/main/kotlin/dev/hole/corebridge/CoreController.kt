package dev.hole.corebridge

import kotlinx.coroutines.flow.Flow

/** Lifecycle boundary shared by the real JNI client and deterministic host tests. */
interface CoreController : AutoCloseable {
    val snapshots: Flow<CoreSnapshot>
    val networkEvents: Flow<String>
    suspend fun start(requestJSON: String)
    suspend fun stop()
    suspend fun applyConfig(requestJSON: String)
    suspend fun networkChanged(eventJSON: String)
    fun reconnect()
    /** UI visibility affects telemetry frequency, never connection lifetime. */
    fun setTelemetryActive(active: Boolean) {}
}
