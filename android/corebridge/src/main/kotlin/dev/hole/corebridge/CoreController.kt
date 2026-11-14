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
    suspend fun renominateTransports()
    suspend fun startNATProbe(optionsJSON: String) {}
    suspend fun cancelNATProbe() {}
    suspend fun natPunchProcess(transportId: String, afterSequence: String): NATPunchProcess = NATPunchProcess()
    suspend fun exportNATPunchProcess(transportId: String): String = error("当前核心未提供打洞过程导出")
    suspend fun startVoiceAudio(requestJSON: String, microphoneSource: String = "auto") {}
    suspend fun stopVoiceAudio() {}
    suspend fun setVoiceMuted(muted: Boolean) {}
    /** UI visibility affects telemetry frequency, never connection lifetime. */
    fun setTelemetryActive(active: Boolean) {}
}
