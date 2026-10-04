package dev.hole.corebridge

import android.media.AudioAttributes
import android.media.AudioFormat
import android.media.AudioRecord
import android.media.AudioTrack
import android.media.MediaRecorder
import android.os.Process
import android.annotation.SuppressLint
import dev.hole.core.mobile.Engine
import java.util.concurrent.atomic.AtomicBoolean

/**
 * Android PCM adapter. Device access stays in this module; the Go core only
 * sees bounded batches of 48 kHz mono PCM16 frames through the mobile facade.
 */
internal class VoiceAudio(private val engine: Engine) : AutoCloseable {
    private val running = AtomicBoolean(false)
    private var recorder: AudioRecord? = null
    private var track: AudioTrack? = null
    private var inputThread: Thread? = null
    private var outputThread: Thread? = null

    @SuppressLint("MissingPermission")
    fun start() {
        check(running.compareAndSet(false, true)) { "语音音频已启动" }
        val minInput = AudioRecord.getMinBufferSize(SAMPLE_RATE, INPUT_CHANNELS, ENCODING)
        require(minInput > 0) { "无法初始化 Android 麦克风" }
        val input = AudioRecord.Builder()
            .setAudioSource(MediaRecorder.AudioSource.VOICE_COMMUNICATION)
            .setAudioFormat(AudioFormat.Builder().setSampleRate(SAMPLE_RATE).setEncoding(ENCODING).setChannelMask(INPUT_CHANNELS).build())
            .setBufferSizeInBytes(maxOf(minInput, BATCH_SAMPLES * 2 * 2))
            .build()
        check(input.state == AudioRecord.STATE_INITIALIZED) { "Android 麦克风初始化失败" }
        val minOutput = AudioTrack.getMinBufferSize(SAMPLE_RATE, OUTPUT_CHANNELS, ENCODING)
        require(minOutput > 0) { "无法初始化 Android 扬声器" }
        val output = AudioTrack.Builder()
            .setAudioAttributes(AudioAttributes.Builder().setUsage(AudioAttributes.USAGE_VOICE_COMMUNICATION).setContentType(AudioAttributes.CONTENT_TYPE_SPEECH).build())
            .setAudioFormat(AudioFormat.Builder().setSampleRate(SAMPLE_RATE).setEncoding(ENCODING).setChannelMask(OUTPUT_CHANNELS).build())
            .setBufferSizeInBytes(maxOf(minOutput, BATCH_SAMPLES * 2 * 2))
            .setTransferMode(AudioTrack.MODE_STREAM)
            .build()
        check(output.state == AudioTrack.STATE_INITIALIZED) { "Android 扬声器初始化失败" }
        recorder = input
        track = output
        input.startRecording()
        output.play()
        inputThread = Thread(::captureLoop, "hole-voice-capture").also { it.start() }
        outputThread = Thread(::playbackLoop, "hole-voice-playback").also { it.start() }
    }

    private fun captureLoop() {
        Process.setThreadPriority(Process.THREAD_PRIORITY_AUDIO)
        val samples = ByteArray(BATCH_SAMPLES * 2)
        var sequence = 0L
        var timestamp = 0L
        while (running.get()) {
            val count = recorder?.read(samples, 0, samples.size, AudioRecord.READ_BLOCKING) ?: -1
            if (count <= 0) break
            if (count == samples.size) {
                try { engine.pushVoicePCM(sequence, timestamp, samples) } catch (_: Exception) { /* core lifecycle may be stopping */ }
                sequence += BATCH_FRAMES
                timestamp += BATCH_SAMPLES
            }
        }
    }

    private fun playbackLoop() {
        Process.setThreadPriority(Process.THREAD_PRIORITY_AUDIO)
        while (running.get()) {
            val samples = engine.pullVoicePCM(BATCH_FRAMES.toLong())
            if (samples == null || samples.isEmpty()) {
                Thread.sleep(10)
                continue
            }
            var offset = 0
            while (offset < samples.size && running.get()) {
                val written = track?.write(samples, offset, samples.size - offset, AudioTrack.WRITE_BLOCKING) ?: -1
                if (written <= 0) break
                offset += written
            }
        }
    }

    override fun close() {
        if (!running.compareAndSet(true, false)) return
        recorder?.stop()
        track?.pause()
        inputThread?.join(500)
        outputThread?.join(500)
        recorder?.release()
        track?.flush()
        track?.release()
        recorder = null
        track = null
        inputThread = null
        outputThread = null
    }

    private companion object {
        const val SAMPLE_RATE = 48_000
        const val FRAME_SAMPLES = 960
        const val BATCH_FRAMES = 4
        const val BATCH_SAMPLES = FRAME_SAMPLES * BATCH_FRAMES
        const val ENCODING = AudioFormat.ENCODING_PCM_16BIT
        const val INPUT_CHANNELS = AudioFormat.CHANNEL_IN_MONO
        const val OUTPUT_CHANNELS = AudioFormat.CHANNEL_OUT_MONO
    }
}
