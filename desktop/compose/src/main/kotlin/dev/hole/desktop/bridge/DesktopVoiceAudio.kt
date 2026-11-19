package dev.hole.desktop.bridge

import javax.sound.sampled.*
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicLong

/** Devices only. Opus, pacing, jitter buffering and mixing remain in Go. */
internal class DesktopVoiceAudio(private val handle: Long, private val captureSequence: AtomicLong) : AutoCloseable {
    private val active = AtomicBoolean(false)
    private val frames = AtomicLong()
    private var input: TargetDataLine? = null
    private var output: SourceDataLine? = null
    private val threads = mutableListOf<Thread>()
    @Volatile var failure: String? = null
        private set
    val state get() = when { failure != null -> "error"; active.get() -> "running"; else -> "stopped" }
    val playedFrames get() = frames.get()

    fun start() {
        val format = AudioFormat(48000f, 16, 1, true, false)
        try {
            input = (AudioSystem.getLine(DataLine.Info(TargetDataLine::class.java, format)) as TargetDataLine).also { it.open(format, 7680); it.start() }
            output = (AudioSystem.getLine(DataLine.Info(SourceDataLine::class.java, format)) as SourceDataLine).also { it.open(format, 7680); it.start() }
            active.set(true)
            launch("capture") {
                val buffer = ByteArray(1920)
                while (active.get()) {
                    var offset = 0
                    while (active.get() && offset < buffer.size) {
                        val count = input!!.read(buffer, offset, buffer.size - offset)
                        check(count > 0) { "麦克风已停止" }
                        offset += count
                    }
                    if (active.get()) {
                        val sequence = captureSequence.getAndIncrement()
                        NativeCore.push(handle, sequence, sequence * 960, buffer)
                    }
                }
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
