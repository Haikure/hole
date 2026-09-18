package dev.hole.corebridge

import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.isActive
import kotlinx.coroutines.withTimeoutOrNull

// Telemetry scheduling only: never changes signaling, ICE/QUIC keepalives or
// session recovery. A conflated event still wakes a background/stopped reader.
internal class SnapshotSampler(
    private val nowMillis: () -> Long = { System.nanoTime() / 1_000_000 },
) {
    private val changed = Channel<Unit>(Channel.CONFLATED)
    @Volatile private var uiVisible = false

    fun wake() { changed.trySend(Unit) }
    fun setUiVisible(visible: Boolean) {
        if (uiVisible != visible) {
            uiVisible = visible
            wake()
        }
    }
    fun close() { changed.close() }

    // CONFLATED has at most one pending item; do not spin while a producer runs.
    private fun drainClosed(): Boolean = changed.tryReceive().isClosed

    fun snapshots(read: () -> CoreSnapshot): Flow<CoreSnapshot> = flow {
        if (drainClosed()) return@flow
        var previous = read()
        var lastReadAt = nowMillis()
        emit(previous)
        while (currentCoroutineContext().isActive) {
            val result = if (previous.runRequested) {
                withTimeoutOrNull(if (uiVisible) FOREGROUND_INTERVAL_MS else BACKGROUND_INTERVAL_MS) { changed.receiveCatching() }
            } else {
                // Lifecycle commands explicitly wake us even when the mobile
                // facade suppresses callbacks while stopped.
                changed.receiveCatching()
            }
            if (result?.isClosed == true) break
            // Bound bursts of native events without a debounce that could
            // starve updates under a continuous stream of events.
            val remaining = MIN_INTERVAL_MS - (nowMillis() - lastReadAt)
            if (remaining > 0) delay(remaining)
            if (drainClosed()) break
            val next = read()
            lastReadAt = nowMillis()
            if (next != previous) emit(next)
            previous = next
        }
    }

    companion object {
        const val FOREGROUND_INTERVAL_MS = 1_500L
        const val BACKGROUND_INTERVAL_MS = 15_000L
        const val MIN_INTERVAL_MS = 100L
    }
}
