package dev.hole.corebridge

import kotlin.test.assertContentEquals
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue
import kotlin.test.assertFailsWith
import kotlin.test.assertSame
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [26])
class VoiceAudioTest {
    @Test fun shortReadsKeepEveryByteInTheFrame() {
        val source = ByteArray(1920) { (it % 251).toByte() }
        val frame = ByteArray(source.size)
        val offsets = mutableListOf<Int>()
        assertTrue(readVoiceFrame(frame) { offset, length ->
            offsets += offset
            val count = minOf(127, length)
            source.copyInto(frame, offset, offset, offset + count)
            count
        })
        assertContentEquals(source, frame)
        assertEquals(0, offsets.first())
        assertEquals(1905, offsets.last())
    }

    @Test fun readFailureDoesNotSubmitAnIncompleteFrame() {
        var calls = 0
        assertFalse(readVoiceFrame(ByteArray(1920)) { _, _ ->
            if (calls++ == 0) 960 else -3
        })
        assertEquals(2, calls)
        assertFalse(readVoiceFrame(ByteArray(1920)) { _, _ -> 0 })
    }

    @Test fun captureResumesAfterCoreReplacementWithoutReplayingUnavailableFrames() {
        var running = true
        var reads = 0
        val attempts = mutableListOf<Pair<Long, Long>>()
        val delivered = mutableListOf<Pair<Long, Byte>>()
        captureVoiceFrames(ByteArray(1920), isRunning = { running }, read = { samples ->
            samples.fill(reads.toByte())
            reads++
            true
        }, push = { sequence, timestamp, samples ->
            attempts += sequence to timestamp
            when (sequence) {
                0L, 2L -> throw Exception("""{"code":"voice_unavailable","message":"核心重建中"}""")
                1L -> throw Exception("""{"code":"voice_disabled","message":"等待语音配置"}""")
                else -> delivered += sequence to samples.first()
            }
            if (sequence == 4L) running = false
        })
        assertEquals(listOf(0L to 0L, 1L to 960L, 2L to 1920L, 3L to 2880L, 4L to 3840L), attempts)
        assertEquals(listOf(3L to 3.toByte(), 4L to 4.toByte()), delivered)
    }

    @Test fun permanentOrUnstructuredPushFailuresStillPropagate() {
        for (failure in listOf(
            Exception("""{"code":"invalid_voice_pcm","message":"bad PCM"}"""),
            Exception("""{"code":"device_failed","message":"voice_unavailable"}"""),
            IllegalStateException("voice_unavailable"),
        )) {
            val thrown = assertFailsWith<Exception> {
                captureVoiceFrames(ByteArray(1920), { true }, { true }) { _, _, _ -> throw failure }
            }
            assertSame(failure, thrown)
        }
    }

    @Test fun stopDuringCoreReplacementEndsCaptureWithoutAnotherRead() {
        var running = true
        var reads = 0
        captureVoiceFrames(ByteArray(1920), { running }, { reads++; true }) { _, _, _ ->
            running = false
            throw Exception("""{"code":"voice_unavailable"}""")
        }
        assertEquals(1, reads)
    }

    @Test fun deviceReadFailureStillStopsCapture() {
        assertFailsWith<IllegalStateException> {
            captureVoiceFrames(ByteArray(1920), { true }, { false }) { _, _, _ -> error("must not submit") }
        }
    }
}
