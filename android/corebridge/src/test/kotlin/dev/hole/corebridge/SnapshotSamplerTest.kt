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
