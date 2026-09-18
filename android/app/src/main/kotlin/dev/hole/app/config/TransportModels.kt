package dev.hole.app.config

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

const val DEFAULT_STUN_URL = "stun:stun.cloudflare.com:3478"
const val ICE_PROFILE = "ice-quic-mux-v1"
const val LEGACY_PROFILE = "legacy-ipv6-quic-v2"
const val PREFERRED_ICE = "ice"
const val PREFERRED_IPV6 = "ipv6"

@Serializable
data class CoreTransport(
    val preferred: String = PREFERRED_ICE,
    @SerialName("allow_legacy") val allowLegacy: Boolean = true,
    @SerialName("allow_insecure_signal") val allowInsecureSignal: Boolean = false,
)
@Serializable
data class IceSettings(
    @SerialName("stun_urls") val stunUrls: List<String> = listOf(DEFAULT_STUN_URL),
    @SerialName("direct_probe_timeout") val directProbeTimeout: String = "3s",
    @SerialName("gather_timeout") val gatherTimeout: String = "6s",
    @SerialName("connectivity_timeout") val connectivityTimeout: String = "10s",
    @SerialName("retry_max_delay") val retryMaxDelay: String = "15s",
    @SerialName("interface_allowlist") val interfaceAllowlist: List<String> = emptyList(),
    @SerialName("include_loopback") val includeLoopback: Boolean = false,
    @SerialName("relay_only") val relayOnly: Boolean = false,
)
@Serializable
data class TurnSettings(
    val mode: String = "worker",
    val ttl: String = "6h",
    val urls: List<String> = emptyList(),
    val username: String = "",
)
@Serializable
data class CoreTurn(
    val mode: String = "worker", val ttl: String = "6h", val urls: List<String> = emptyList(),
    val username: String = "", val credential: String = "",
)

fun ConnectionSettings.coreTransport() = CoreTransport(
    preferred = if (connectionMode == "legacy") PREFERRED_IPV6 else PREFERRED_ICE,
    allowLegacy = connectionMode == "auto", allowInsecureSignal = allowInsecureSignal,
)
fun ConnectionSettings.coreTurn(credential: String) = if (turn.mode == "manual") {
    CoreTurn(turn.mode, turn.ttl, turn.urls, turn.username, credential)
} else CoreTurn(mode = turn.mode, ttl = turn.ttl)

fun migrateStoredConfig(config: StoredConfig, version: Int): StoredConfig {
    require(version in 1..2) { "配置格式版本不受支持，保留原文件" }
    if (version == 2) return config.copy(schemaVersion = 2)
    val old = config.connection
    // Existing explicit IPv6 addresses and local ws fixtures retain their old
    // semantics. Ordinary WSS configurations gain ICE with explicit fallback.
    val mode = if (old.candidateAddresses.isNotEmpty() || old.serverUrl.startsWith("ws://")) "legacy" else "auto"
    return config.copy(schemaVersion = 2, connection = old.copy(connectionMode = mode))
}
