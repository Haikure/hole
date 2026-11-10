package dev.hole.corebridge

import kotlin.test.assertEquals
import kotlin.test.assertTrue
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class SnapshotSamplerTest {
    @Test fun periodicReadsDropFromFortyToFourPerMinuteInBackground() = runTest {
        for ((visible, expected) in listOf(true to 41, false to 5)) {
            val sampler = SnapshotSampler { testScheduler.currentTime }
            sampler.setUiVisible(visible)
            var reads = 0
            var emissions = 0
            val job = backgroundScope.launch {
                sampler.snapshots { reads++; CoreSnapshot(runRequested = true) }.collect { emissions++ }
            }
            runCurrent()
            advanceTimeBy(60_000)
            runCurrent()
            assertEquals(expected, reads, "includes the initial read")
            assertEquals(1, emissions, "equal snapshots do not cross the host boundary again")
            sampler.close()
            runCurrent()
            assertTrue(job.isCompleted)
        }
    }
    @Test fun stoppedCoreDoesNotPollButCommandsStillWakeIt() = runTest {
        val sampler = SnapshotSampler { testScheduler.currentTime }
        var reads = 0
        var state = CoreSnapshot(runRequested = false)
        backgroundScope.launch { sampler.snapshots { reads++; state }.collect {} }
        runCurrent()
        advanceTimeBy(3_600_000)
        runCurrent()
        assertEquals(1, reads)
        state = state.copy(runRequested = true)
        sampler.wake()
        runCurrent()
        assertEquals(2, reads)
        advanceTimeBy(15_000)
        runCurrent()
        assertEquals(3, reads)
        sampler.close()
    }
    @Test fun eventBurstsAreBoundedAndDoNotStarveNewState() = runTest {
        val sampler = SnapshotSampler { testScheduler.currentTime }
        var reads = 0
        var latest = 0
        var seen = ""
        backgroundScope.launch {
            sampler.snapshots { reads++; CoreSnapshot(runRequested = true, reconnects = latest.toString()) }.collect { seen = it.reconnects }
        }
        runCurrent()
        repeat(100) { latest++; sampler.wake(); runCurrent(); advanceTimeBy(10) }
        runCurrent()
        assertTrue(reads <= 11, "no per-event JNI snapshot storm: $reads reads")
        assertEquals("100", seen)
        sampler.close()
    }
    @Test fun finishedDiagnosticsOnlyPollUntilEvidenceExpires() = runTest {
        val sampler = SnapshotSampler { testScheduler.currentTime }
        sampler.setUiVisible(true)
        var reads = 0
        var state = CoreSnapshot(natDiagnostic = NATDiagnostic(state = "probing"))
        var seen = ""
        backgroundScope.launch {
            sampler.snapshots { reads++; state }.collect { seen = it.natDiagnostic.state }
        }
        runCurrent()
        advanceTimeBy(1_500)
        runCurrent()
        assertEquals(2, reads, "active probes still refresh promptly while stopped")
        state = state.copy(natDiagnostic = NATDiagnostic(state = "complete"))
        sampler.wake()
        advanceTimeBy(100)
        runCurrent()
        assertEquals("complete", seen)
        val completedReads = reads
        advanceTimeBy(60_000)
        runCurrent()
        assertEquals(completedReads + 4, reads, "finished evidence uses the slow expiry check")
        state = state.copy(natDiagnostic = NATDiagnostic(state = "expired"))
        advanceTimeBy(15_000)
        runCurrent()
        assertEquals("expired", seen)
        val expiredReads = reads
        advanceTimeBy(3_600_000)
        runCurrent()
        assertEquals(expiredReads, reads, "expired results do not keep telemetry alive")
        sampler.close()
    }
    @Test fun terminalDiagnosticsSleepButCanBeRestarted() = runTest {
        for (terminal in listOf("cancelled", "failed", "network_changed", "not_applicable", "expired")) {
            val sampler = SnapshotSampler { testScheduler.currentTime }
            sampler.setUiVisible(true)
            var reads = 0
            var state = CoreSnapshot(natDiagnostic = NATDiagnostic(state = terminal))
            var seen = ""
            val job = backgroundScope.launch {
                sampler.snapshots { reads++; state }.collect { seen = it.natDiagnostic.state }
            }
            runCurrent()
            advanceTimeBy(60_000)
            runCurrent()
            assertEquals(1, reads, "$terminal must not keep polling")
            state = state.copy(natDiagnostic = NATDiagnostic(state = "queued"))
            sampler.wake()
            runCurrent()
            assertEquals("queued", seen, "commands must wake a stopped sampler")
            advanceTimeBy(1_500)
            runCurrent()
            assertEquals(3, reads)
            sampler.close()
            runCurrent()
            assertTrue(job.isCompleted)
        }
    }
    @Test fun aForegroundTransitionDoesNotWaitForTheBackgroundDeadline() = runTest {
        val sampler = SnapshotSampler { testScheduler.currentTime }
        var reads = 0
        backgroundScope.launch { sampler.snapshots { reads++; CoreSnapshot(runRequested = true) }.collect {} }
        runCurrent()
        advanceTimeBy(1_000)
        sampler.setUiVisible(true)
        runCurrent()
        assertEquals(2, reads)
        advanceTimeBy(1_500)
        runCurrent()
        assertEquals(3, reads)
        sampler.close()
    }
}
