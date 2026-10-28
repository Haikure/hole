package dev.hole.app.config

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

const val DEFAULT_STUN_URL = "stun:stun.cloudflare.com:3478"
const val ICE_PROFILE = "ice-quic-mux-v1"
const val LEGACY_PROFILE = "legacy-ipv6-quic-v2"
const val PREFERRED_ICE = "ice"
const val PREFERRED_IPV6 = "ipv6"

@Serializable
data class RelayServeSettings(val enabled: Boolean = false,
    @SerialName("max_pairs") val maxPairs: Int = 64,
    @SerialName("bandwidth_mbps") val bandwidthMbps: Int = 0)
@Serializable
data class RelaySettings(val device: String = "", val order: List<String>? = null,
    val serve: RelayServeSettings = RelayServeSettings())

fun effectiveRelayOrder(relay: RelaySettings, turn: TurnSettings): List<String> = relay.order ?: buildList {
    if (relay.device.isNotBlank()) add("hole")
    addAll(normalizedTurnOrder(turn.order).ifEmpty { listOf("udp", "tcp", "tls") })
}
fun holeRelayOrderError(order: List<String>?): String? = when {
    order == null -> null
    order.size > 4 || order.any { it !in setOf("hole", "udp", "tcp", "tls") } -> "中继顺序只接受 Hole、UDP、TCP、TLS"
    order.distinct().size != order.size -> "中继类型不能重复"
    else -> null
}

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
    val order: List<String> = emptyList(),
)
@Serializable
data class CoreTurn(
    val mode: String = "worker", val ttl: String = "6h", val urls: List<String> = emptyList(),
    val username: String = "", val credential: String = "", val order: List<String> = emptyList(),
)

fun ConnectionSettings.coreTransport() = CoreTransport(
    preferred = if (connectionMode == "legacy") PREFERRED_IPV6 else PREFERRED_ICE,
    allowLegacy = connectionMode == "auto", allowInsecureSignal = allowInsecureSignal,
)
fun normalizedTurnOrder(order: List<String>): List<String> =
    if (order.any { it == "tcp_80" || it == "tls_443" }) order.map { when (it) {
        "tcp_80" -> "tcp"
        "tls_443" -> "tls"
        else -> it
    } }.distinct() else order

fun ConnectionSettings.coreTurn(credential: String) = if (turn.mode == "manual") {
    CoreTurn(turn.mode, turn.ttl, turn.urls, turn.username, credential, normalizedTurnOrder(turn.order))
} else CoreTurn(mode = turn.mode, ttl = turn.ttl, order = normalizedTurnOrder(turn.order))

fun migrateStoredConfig(config: StoredConfig, version: Int): StoredConfig {
    require(version in 1..2) { "配置格式版本不受支持，保留原文件" }
    val old = config.connection.copy(turn = config.connection.turn.copy(order = normalizedTurnOrder(config.connection.turn.order)))
    val voice = config.voice.copy(microphoneSource = normalizeMicrophoneSource(config.voice.microphoneSource))
    if (version == 2) return config.copy(schemaVersion = 2, connection = old, voice = voice)
    // Existing explicit IPv6 addresses and local ws fixtures retain their old
    // semantics. Ordinary WSS configurations gain ICE with explicit fallback.
    val mode = if (old.candidateAddresses.isNotEmpty() || old.serverUrl.startsWith("ws://")) "legacy" else "auto"
    return config.copy(schemaVersion = 2, connection = old.copy(connectionMode = mode), voice = voice)
}
