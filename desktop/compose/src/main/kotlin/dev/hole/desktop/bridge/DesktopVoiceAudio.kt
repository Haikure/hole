package dev.hole.desktop.bridge

import javax.sound.sampled.*
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicLong

internal interface DesktopAudio : AutoCloseable {
    val state: String
    val failure: String?
    val playedFrames: Long
    fun start()
}

/** Devices only. Opus, pacing, jitter buffering and mixing remain in Go. */
internal class DesktopVoiceAudio(
    private val handle: Long,
    private val captureSequence: AtomicLong,
    private val muted: () -> Boolean,
) : DesktopAudio {
    private val active = AtomicBoolean(false)
    private val frames = AtomicLong()
    private var input: TargetDataLine? = null
    private var output: SourceDataLine? = null
    private val threads = mutableListOf<Thread>()
    @Volatile override var failure: String? = null
        private set
    override val state get() = when { failure != null -> "error"; active.get() -> "running"; else -> "stopped" }
    override val playedFrames get() = frames.get()

    override fun start() {
        val format = AudioFormat(48000f, 16, 1, true, false)
        try {
            input = (AudioSystem.getLine(DataLine.Info(TargetDataLine::class.java, format)) as TargetDataLine).also { it.open(format, 7680); it.start() }
            output = (AudioSystem.getLine(DataLine.Info(SourceDataLine::class.java, format)) as SourceDataLine).also { it.open(format, 7680); it.start() }
            active.set(true)
            launch("capture") {
                val buffer = ByteArray(1920)
                captureDesktopVoiceFrames(buffer, captureSequence, active::get, read = { samples ->
                    var offset = 0
                    while (active.get() && offset < samples.size) {
                        val count = input!!.read(samples, offset, samples.size - offset)
                        check(count > 0) { "麦克风已停止" }
                        offset += count
                    }
                    offset == samples.size
                }, prepare = {
                    // Restore the user's mute choice before feeding a replacement core.
                    NativeCore.invoke(handle, "mute", muted().toString())
                }, push = { sequence, timestamp, samples -> NativeCore.push(handle, sequence, timestamp, samples) })
            }
            launch("playback") {
                while (active.get()) {
                    val pcm = NativeCore.pull(handle)
                    if (pcm.isEmpty()) { Thread.sleep(5); continue }
                    var offset = 0
                    while (active.get() && offset < pcm.size) {
                        val count = output!!.write(pcm, offset, pcm.size - offset)
                        check(count > 0) { "扬声器已停止" }
                        offset += count
                    }
                    frames.addAndGet(offset.toLong() / 1920)
                }
            }
        } catch (error: Exception) { close(); throw error }
    }
    private fun launch(name: String, block: () -> Unit) {
        threads += Thread({
            try { block() } catch (error: Exception) {
                if (active.getAndSet(false)) {
                    failure = "音频设备错误：${error.message}"
                    input?.close(); output?.close()
                }
            }
        }, "hole-voice-$name").also { it.isDaemon = true; it.start() }
    }
    override fun close() {
        active.set(false)
        runCatching { input?.stop() }; runCatching { input?.close() }
        runCatching { output?.stop() }; runCatching { output?.close() }
        threads.forEach { if (it !== Thread.currentThread()) it.join(1500) }
        threads.clear()
    }
}

// Keep the capture clock advancing through cold starts and scope replacement.
// Unavailable frames are dropped; device and programming failures still stop audio.
internal fun captureDesktopVoiceFrames(
    samples: ByteArray,
    captureSequence: AtomicLong,
    isRunning: () -> Boolean,
    read: (ByteArray) -> Boolean,
    prepare: () -> Unit,
    push: (Long, Long, ByteArray) -> Unit,
) {
    while (isRunning()) {
        if (!read(samples)) {
            check(!isRunning()) { "麦克风已停止" }
            return
        }
        if (!isRunning()) return
        val sequence = captureSequence.getAndIncrement()
        try {
            prepare()
            push(sequence, sequence * (samples.size / 2), samples)
        } catch (failure: NativeCoreFault) {
            if (!failure.voicePending) throw failure
        }
    }
}
