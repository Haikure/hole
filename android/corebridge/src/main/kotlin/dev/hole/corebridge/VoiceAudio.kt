package dev.hole.corebridge

import android.annotation.SuppressLint
import android.content.Context
import android.media.AudioAttributes
import android.media.AudioDeviceCallback
import android.media.AudioDeviceInfo
import android.media.AudioFormat
import android.media.AudioManager
import android.media.AudioRecord
import android.media.AudioTrack
import android.media.MediaRecorder
import android.media.audiofx.AcousticEchoCanceler
import android.media.audiofx.NoiseSuppressor
import android.os.Build
import android.os.Process
import dev.hole.core.mobile.Engine
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicLong
import org.json.JSONObject

/** Android device access; mixing, codecs and network sessions stay in Go. */
internal class VoiceAudio(context: Context, private val engine: Engine) : AutoCloseable {
    private val manager = context.getSystemService(AudioManager::class.java)
    private val running = AtomicBoolean(false)
    private val played = AtomicLong()
    private var recorder: AudioRecord? = null
    private var track: AudioTrack? = null
    private var aec: AcousticEchoCanceler? = null
    private var ns: NoiseSuppressor? = null
    private var inputThread: Thread? = null
    private var outputThread: Thread? = null
    private var previousMode: Int? = null
    private var previousSpeaker = false
    private var previousDevice: AudioDeviceInfo? = null
    private var routeRegistered = false
    @Volatile private var audioError: String? = null
    @Volatile private var route = ""
    @Volatile private var aecEnabled = false
    @Volatile private var nsEnabled = false
    private val devices = object : AudioDeviceCallback() {
        override fun onAudioDevicesAdded(addedDevices: Array<out AudioDeviceInfo>) { runCatching { refreshRoute() } }
        override fun onAudioDevicesRemoved(removedDevices: Array<out AudioDeviceInfo>) { runCatching { refreshRoute() } }
    }

    @Synchronized
    @SuppressLint("MissingPermission")
    @Suppress("DEPRECATION")
    fun start() {
        check(previousMode == null) { "语音音频已启动" }
        running.set(true)
        try {
            previousMode = manager.mode
            previousSpeaker = manager.isSpeakerphoneOn
            if (Build.VERSION.SDK_INT >= 31) previousDevice = manager.communicationDevice
            manager.mode = AudioManager.MODE_IN_COMMUNICATION
            refreshRoute()
            manager.registerAudioDeviceCallback(devices, null)
            routeRegistered = true
            val minInput = AudioRecord.getMinBufferSize(SAMPLE_RATE, INPUT_CHANNELS, ENCODING)
            require(minInput > 0) { "无法初始化 Android 麦克风" }
            val input = AudioRecord.Builder()
                .setAudioSource(MediaRecorder.AudioSource.VOICE_COMMUNICATION)
                .setAudioFormat(AudioFormat.Builder().setSampleRate(SAMPLE_RATE).setEncoding(ENCODING).setChannelMask(INPUT_CHANNELS).build())
                .setBufferSizeInBytes(maxOf(minInput, FRAME_BYTES * 4))
                .build().also { recorder = it }
            check(input.state == AudioRecord.STATE_INITIALIZED) { "Android 麦克风初始化失败" }
            aec = runCatching { if (AcousticEchoCanceler.isAvailable()) AcousticEchoCanceler.create(input.audioSessionId) else null }.getOrNull()
            aecEnabled = runCatching { aec?.let { it.setEnabled(true) == 0 && it.enabled } == true }.getOrDefault(false)
            ns = runCatching { if (NoiseSuppressor.isAvailable()) NoiseSuppressor.create(input.audioSessionId) else null }.getOrNull()
            nsEnabled = runCatching { ns?.let { it.setEnabled(true) == 0 && it.enabled } == true }.getOrDefault(false)
            val minOutput = AudioTrack.getMinBufferSize(SAMPLE_RATE, OUTPUT_CHANNELS, ENCODING)
            require(minOutput > 0) { "无法初始化 Android 扬声器" }
            val output = AudioTrack.Builder()
                .setAudioAttributes(AudioAttributes.Builder().setUsage(AudioAttributes.USAGE_VOICE_COMMUNICATION).setContentType(AudioAttributes.CONTENT_TYPE_SPEECH).build())
                .setAudioFormat(AudioFormat.Builder().setSampleRate(SAMPLE_RATE).setEncoding(ENCODING).setChannelMask(OUTPUT_CHANNELS).build())
                .setBufferSizeInBytes(maxOf(minOutput, FRAME_BYTES * 4))
                .setTransferMode(AudioTrack.MODE_STREAM)
                .build().also { track = it }
            check(output.state == AudioTrack.STATE_INITIALIZED) { "Android 扬声器初始化失败" }
            input.startRecording()
            check(input.recordingState == AudioRecord.RECORDSTATE_RECORDING) { "Android 麦克风启动失败" }
            output.play()
            inputThread = Thread({ audioLoop { captureLoop(input) } }, "hole-voice-capture").also { it.start() }
            outputThread = Thread({ audioLoop { playbackLoop(output) } }, "hole-voice-playback").also { it.start() }
        } catch (failure: Exception) {
            close()
            throw failure
        }
    }

    @Synchronized
    @Suppress("DEPRECATION")
    private fun refreshRoute() {
        if (!running.get()) return
        if (Build.VERSION.SDK_INT >= 31) {
            val available = manager.availableCommunicationDevices
            val external = available.filter { it.type != AudioDeviceInfo.TYPE_BUILTIN_SPEAKER && it.type != AudioDeviceInfo.TYPE_BUILTIN_EARPIECE }
            val device = external.firstOrNull { it.id == manager.communicationDevice?.id }
                ?: external.firstOrNull()
                ?: available.firstOrNull { it.type == AudioDeviceInfo.TYPE_BUILTIN_SPEAKER }
                ?: available.firstOrNull()
            if (device != null && manager.setCommunicationDevice(device)) route = device.productName.toString()
        } else {
            val wired = manager.getDevices(AudioManager.GET_DEVICES_OUTPUTS).any {
                it.type == AudioDeviceInfo.TYPE_WIRED_HEADSET || it.type == AudioDeviceInfo.TYPE_WIRED_HEADPHONES ||
                    it.type == AudioDeviceInfo.TYPE_USB_HEADSET || it.type == AudioDeviceInfo.TYPE_USB_DEVICE
            }
            manager.isSpeakerphoneOn = !wired && !manager.isBluetoothScoOn
            route = if (manager.isSpeakerphoneOn) "扬声器" else "系统通话设备"
        }
    }

    private fun audioLoop(block: () -> Unit) {
        try {
            Process.setThreadPriority(Process.THREAD_PRIORITY_AUDIO)
            block()
        } catch (failure: Exception) {
            if (running.getAndSet(false)) {
                audioError = failure.message ?: "语音音频线程异常"
                runCatching { recorder?.stop() }
                runCatching { track?.pause() }
                Thread({ close() }, "hole-voice-cleanup").start()
            }
        }
    }

    private fun captureLoop(input: AudioRecord) {
        captureVoiceFrames(
            samples = ByteArray(FRAME_BYTES),
            isRunning = running::get,
            read = { samples -> readVoiceFrame(samples) { offset, length -> input.read(samples, offset, length, AudioRecord.READ_BLOCKING) } },
            push = engine::pushVoicePCM,
        )
    }

    private fun playbackLoop(output: AudioTrack) {
        while (running.get()) {
            val samples = engine.pullVoicePCM(1) ?: byteArrayOf()
            if (samples.isEmpty()) {
                Thread.sleep(5)
                continue
            }
            var offset = 0
            while (offset < samples.size && running.get()) {
                val written = output.write(samples, offset, samples.size - offset, AudioTrack.WRITE_BLOCKING)
                check(written > 0) { "Android 扬声器写入失败 ($written)" }
                offset += written
            }
            if (offset == samples.size) played.incrementAndGet()
        }
    }

    fun snapshot(voice: VoiceSnapshot): VoiceSnapshot = voice.copy(
        audioState = if (audioError != null) "error" else if (running.get()) "running" else "stopped",
        audioError = audioError, playbackFrames = played.get().toString(),
        audioRoute = route, aecEnabled = aecEnabled, nsEnabled = nsEnabled,
    )

    fun isRunning(): Boolean = running.get()

    @Synchronized
    @Suppress("DEPRECATION")
    override fun close() {
        running.set(false)
        if (routeRegistered) runCatching { manager.unregisterAudioDeviceCallback(devices) }
        routeRegistered = false
        runCatching { recorder?.stop() }
        runCatching { track?.pause() }
        inputThread?.join(1_000)
        outputThread?.join(1_000)
        runCatching { aec?.release() }
        runCatching { ns?.release() }
        runCatching { recorder?.release() }
        runCatching { track?.flush() }
        runCatching { track?.release() }
        recorder = null
        track = null
        aec = null
        ns = null
        inputThread = null
        outputThread = null
        previousMode?.let { mode ->
            runCatching {
                if (Build.VERSION.SDK_INT >= 31) {
                    manager.clearCommunicationDevice()
                    previousDevice?.let { manager.setCommunicationDevice(it) }
                } else {
                    manager.isSpeakerphoneOn = previousSpeaker
                }
            }
            runCatching { manager.mode = mode }
        }
        previousMode = null
        previousDevice = null
    }

    private companion object {
        const val SAMPLE_RATE = 48_000
        const val FRAME_SAMPLES = 960
        const val FRAME_BYTES = FRAME_SAMPLES * 2
        const val ENCODING = AudioFormat.ENCODING_PCM_16BIT
        const val INPUT_CHANNELS = AudioFormat.CHANNEL_IN_MONO
        const val OUTPUT_CHANNELS = AudioFormat.CHANNEL_OUT_MONO
    }
}

// Keep the device clock advancing while a cold start or reconfiguration replaces
// the core. Drop unavailable frames instead of replaying stale audio on recovery.
internal fun captureVoiceFrames(
    samples: ByteArray,
    isRunning: () -> Boolean,
    read: (ByteArray) -> Boolean,
    push: (Long, Long, ByteArray) -> Unit,
) {
    var sequence = 0L
    var timestamp = 0L
    while (isRunning()) {
        if (!read(samples)) {
            check(!isRunning()) { "Android 麦克风读取失败" }
            return
        }
        if (!isRunning()) return
        try {
            push(sequence, timestamp, samples)
        } catch (failure: Exception) {
            val code = runCatching { JSONObject(failure.message.orEmpty()).optString("code") }.getOrNull()
            if (code != "voice_unavailable" && code != "voice_disabled") throw failure
        }
        sequence++
        timestamp += samples.size / 2
    }
}

// A blocking read may still be short. Keep its prefix until one frame is full.
internal fun readVoiceFrame(samples: ByteArray, read: (offset: Int, length: Int) -> Int): Boolean {
    var offset = 0
    while (offset < samples.size) {
        val count = read(offset, samples.size - offset)
        if (count <= 0) return false
        require(count <= samples.size - offset)
        offset += count
    }
    return true
}
