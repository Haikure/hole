package dev.hole.corebridge

import kotlin.test.assertContentEquals
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue
import org.junit.Test

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
}
