package dev.hole.desktop.bridge

import java.io.File
import org.json.JSONObject

/** One native library for the process lifetime. Engine close never unloads the Go runtime. */
internal object NativeCore {
    init {
        val explicit = System.getProperty("hole.native.path")
        val resources = System.getProperty("compose.application.resources.dir")
        val path = if (explicit != null) File(explicit) else {
            check(resources != null) { "未找到应用资源目录，请从完整应用目录启动 Hole" }
            File(resources, System.mapLibraryName("holecore"))
        }
        check(path.isAbsolute && path.isFile) { "未找到本平台 Go 原生库：$path" }
        System.load(path.canonicalPath)
    }

    external fun open(): Long
    external fun call(handle: Long, operation: String, input: ByteArray): ByteArray
    external fun close(handle: Long)
    external fun push(handle: Long, sequence: Long, timestamp: Long, pcm: ByteArray)
    external fun pull(handle: Long): ByteArray

    fun invoke(handle: Long, operation: String, input: String = ""): String {
        val response = JSONObject(call(handle, operation, input.toByteArray(Charsets.UTF_8)).toString(Charsets.UTF_8))
        if (response.has("error")) {
            val raw = response.getString("error")
            val message = runCatching { JSONObject(raw).getString("message") }.getOrDefault(raw)
            error(message)
        }
        return response.getString("value")
    }
}
