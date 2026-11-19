package dev.hole.app.config

import dev.hole.core.mobile.Mobile

internal object CoreConfigCodec {
    fun decode(text: String): String = Mobile.decodeCLIConfig(text)
    fun encode(text: String, includeSecrets: Boolean): String = Mobile.encodeCLIConfig(text, includeSecrets)
}
