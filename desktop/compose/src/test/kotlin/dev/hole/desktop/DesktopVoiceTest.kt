package dev.hole.desktop

import dev.hole.desktop.bridge.*
import java.util.concurrent.atomic.AtomicLong
import kotlinx.coroutines.runBlocking
import kotlin.test.*

class DesktopVoiceTest {
    @Test fun nativeBridgeDropsUnavailablePCMButRejectsInvalidInput() {
        val handle = NativeCore.open()
        try {
            val failure = assertFailsWith<NativeCoreFault> { NativeCore.invoke(handle, "mute", "true") }
            assertEquals("voice_unavailable", failure.code)
            NativeCore.push(handle, 0, 0, ByteArray(1920))
            assertFailsWith<IllegalStateException> { NativeCore.push(handle, -1, 0, ByteArray(1920)) }
        } finally { NativeCore.close(handle) }
        assertFailsWith<IllegalStateException> { NativeCore.push(handle, 1, 960, ByteArray(1920)) }
    }

    @Test fun unavailableCaptureFramesAdvanceClockAndRecoveryKeepsMutePreparation() {
        val clock = AtomicLong()
        var running = true
        var preparations = 0
        val submitted = mutableListOf<Pair<Long, Long>>()
        captureDesktopVoiceFrames(ByteArray(1920), clock, { running }, { true }, prepare = {
            preparations++
            if (preparations == 1) throw NativeCoreFault("voice_unavailable", "starting")
        }, push = { sequence, timestamp, _ ->
            if (sequence == 1L) throw NativeCoreFault("voice_disabled", "reconfiguring")
            submitted += sequence to timestamp
            running = false
        })
        assertEquals(3, preparations)
        assertEquals(3L, clock.get())
        assertEquals(listOf(2L to 1920L), submitted)
        assertFailsWith<IllegalStateException> {
            captureDesktopVoiceFrames(ByteArray(1920), clock, { true }, { true },
                prepare = { throw IllegalStateException("device failed") }, push = { _, _, _ -> })
        }
    }

    @Test fun pendingCoreAndLiveConfigKeepDevicesUntilVoiceIsDisabled() = runBlocking {
        val device = FakeAudio()
        var mute: () -> Boolean = { false }
        val client = DesktopCoreClient { _, _, muted -> mute = muted; device }
        val request = """{"api_version":1,"server_url":"wss://example.invalid/ws","config":{"room":"fixture","password":"password","token":"token","device_name":"desktop-voice","provide":[],"consume":[],"voice":{"enabled":true}}}"""
        try {
            client.startVoiceAudio(request, "auto")
            client.setVoiceMuted(true)
            assertTrue(mute())
            client.applyConfig(request)
            client.startVoiceAudio(request, "auto")
            assertEquals(1, device.starts)
            assertEquals(0, device.closes)
            assertEquals("running", device.state)
            client.applyConfig(request.replace("\"enabled\":true", "\"enabled\":false"))
            assertEquals(1, device.closes)
            assertEquals("stopped", device.state)
        } finally { client.close() }
    }

    private class FakeAudio : DesktopAudio {
        var starts = 0
        var closes = 0
        private var running = false
        override val state get() = if (running) "running" else "stopped"
        override val failure: String? = null
        override val playedFrames = 0L
        override fun start() { starts++; running = true }
        override fun close() { closes++; running = false }
    }
}
