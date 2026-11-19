package dev.hole.app.config

import dev.hole.desktop.bridge.NativeCore

internal object CoreConfigCodec {
    fun decode(text: String): String = NativeCore.invoke(0, "decode", text)
    fun encode(text: String, includeSecrets: Boolean): String = NativeCore.invoke(0, if (includeSecrets) "encode_secrets" else "encode", text)
}
