package dev.hole.corebridge

import org.json.JSONObject

data class MappingSnapshot(
    val id: String, val role: String, val protocol: String, val state: String,
    val endpoint: String = "", val peer: String = "", val path: String = "",
    val tcpSessions: Long = 0, val udpSessions: Long = 0,
    val tcpReadBytes: String = "0", val tcpWrittenBytes: String = "0",
    val readBytes: String = "0", val writtenBytes: String = "0", val replayBytes: String = "0",
    val error: String? = null,
    val profile: String = "",
)

data class PeerSnapshot(
    val peerId: String = "", val transportId: String = "", val generation: String = "0",
    val profile: String = "", val state: String = "", val phase: String = "", val pendingPhase: String = "",
    val pathType: String = "", val addressFamily: String = "", val relayProtocol: String = "", val localRelayProtocol: String = "", val remoteRelayProtocol: String = "",
    val localAddress: String = "", val remoteAddress: String = "", val localType: String = "", val remoteType: String = "",
    val localCandidates: Int = 0, val remoteCandidates: Int = 0, val mappingCount: Int = 0, val activeChannels: Int = 0,
    val connectMs: String = "0", val rttMs: String = "0", val bytesSent: String = "0", val bytesReceived: String = "0",
    val droppedDatagrams: String = "0", val retryCount: String = "0", val relayState: String = "",
    val leaseUntil: String = "0", val turnExpiresAt: String = "0", val errorCode: String? = null, val errorMessage: String? = null,
    val relaySide: String = "", val relayPolicy: String = "",
) {
    companion object {
        fun fromJson(j: JSONObject): PeerSnapshot = PeerSnapshot(
            peerId = j.optString("peer_id"), transportId = j.optString("transport_id"), generation = j.optString("generation", "0"),
            profile = j.optString("profile"), state = j.optString("state"), phase = j.optString("phase"), pendingPhase = j.optString("pending_phase"),
            pathType = j.optString("path_type"), addressFamily = j.optString("address_family"), relayProtocol = j.optString("relay_protocol"), localRelayProtocol = j.optString("local_relay_protocol").ifBlank { j.optString("relay_protocol") }, remoteRelayProtocol = j.optString("remote_relay_protocol"),
            localAddress = j.optString("local_address"), remoteAddress = j.optString("remote_address"), localType = j.optString("local_type"), remoteType = j.optString("remote_type"),
            localCandidates = j.optInt("local_candidates"), remoteCandidates = j.optInt("remote_candidates"), mappingCount = j.optInt("mapping_count"), activeChannels = j.optInt("active_channels"),
            connectMs = j.optString("connect_ms", "0"), rttMs = j.optString("rtt_ms", "0"), bytesSent = j.optString("bytes_sent", "0"), bytesReceived = j.optString("bytes_received", "0"),
            droppedDatagrams = j.optString("dropped_datagrams", "0"), retryCount = j.optString("retry_count", "0"), relayState = j.optString("relay_state"),
            leaseUntil = j.optString("lease_until", "0"), turnExpiresAt = j.optString("turn_expires_at", "0"),
            errorCode = j.optJSONObject("error")?.optString("code"), errorMessage = j.optJSONObject("error")?.optString("message"),
            relaySide = j.optString("relay_side"), relayPolicy = j.optString("relay_policy"),
        )
    }
}

data class NetworkSnapshot(
    val handle: String = "0", val transport: String = "none", val interfaceName: String = "",
    val addresses: List<String> = emptyList(), val dns: List<String> = emptyList(),
    val available: Boolean = false, val validated: Boolean = false, val metered: Boolean = false,
)

data class CoreSnapshot(
    val nativeReady: Boolean = false,
    val configured: Boolean = false,
    val runRequested: Boolean = false,
    val engineState: String = "loading",
    val signalState: String = "disconnected",
    val apiVersion: Int = 0,
    val sessionProtocol: Int = 0,
    val coreVersion: String = "",
    val provideCount: Int = 0,
    val consumeCount: Int = 0,
    val errorCode: String? = null,
    val errorMessage: String? = null,
    val generation: String = "0",
    val transportGeneration: String = "0",
    val networkChanges: String = "0",
    val reconnects: String = "0",
    val eventsDropped: String = "0",
    val startedAt: String = "",
    val mappings: List<MappingSnapshot> = emptyList(),
    val network: NetworkSnapshot = NetworkSnapshot(),
    val transport: String = "IPv6 / QUIC / hole-v2",
    val liveConfiguration: Boolean = false,
    val networkBinding: Boolean = false,
    val peers: List<PeerSnapshot> = emptyList(),
) {
    val tcpSessions: Long get() = mappings.sumOf { it.tcpSessions }
    val udpSessions: Long get() = mappings.sumOf { it.udpSessions }
    companion object {
        fun fromJson(text: String): CoreSnapshot {
            val json = JSONObject(text)
            require(json.getInt("api_version") == 1) { "桥接 API 版本不匹配" }
            val mappings = json.getJSONArray("mappings")
            var provide = 0
            var consume = 0
            val details = mutableListOf<MappingSnapshot>()
            for (i in 0 until mappings.length()) {
                when (mappings.getJSONObject(i).getString("role")) {
                    "provide" -> provide++
                    "consume" -> consume++
                }
                val item = mappings.getJSONObject(i)
                details += MappingSnapshot(
                    id = item.getString("id"), role = item.getString("role"),
                    protocol = item.optString("protocol"), state = item.getString("state"),
                    endpoint = item.optString("endpoint"), peer = item.optString("peer"), path = item.optString("path"),
                    tcpSessions = item.optString("tcp_sessions", "0").toLongOrNull() ?: 0,
                    udpSessions = item.optString("udp_sessions", "0").toLongOrNull() ?: 0,
                    tcpReadBytes = item.optString("tcp_read_bytes", "0"),
                    tcpWrittenBytes = item.optString("tcp_written_bytes", "0"),
                    readBytes = item.optString("read_bytes", item.optString("tcp_read_bytes", "0")),
                    writtenBytes = item.optString("written_bytes", item.optString("tcp_written_bytes", "0")),
                    replayBytes = item.optString("replay_bytes", "0"),
                    error = item.optJSONObject("error")?.optString("message"),
                    profile = item.optString("profile"),
                )
            }
            val error = json.optJSONObject("error")
            val network = json.optJSONObject("network") ?: JSONObject()
            val capabilities = json.optJSONObject("capabilities") ?: JSONObject()
            fun strings(key: String): List<String> {
                val array = network.optJSONArray(key) ?: return emptyList()
                return (0 until array.length()).map { array.getString(it) }
            }
            return CoreSnapshot(
                nativeReady = true,
                configured = json.getBoolean("configured"),
                runRequested = json.getBoolean("run_requested"),
                engineState = json.getString("engine_state"),
                signalState = json.getString("signal_state"),
                apiVersion = json.getInt("api_version"),
                sessionProtocol = json.getInt("session_protocol"),
                coreVersion = json.getString("core_version"),
                provideCount = provide,
                consumeCount = consume,
                errorCode = error?.optString("code"),
                errorMessage = error?.optString("message"),
                generation = json.optString("generation", "0"),
                transportGeneration = json.optString("transport_generation", "0"),
                networkChanges = json.optString("network_changes", "0"),
                reconnects = json.optString("reconnects", "0"),
                eventsDropped = json.optString("events_dropped", "0"),
                startedAt = json.optString("started_at"),
                mappings = details,
                network = NetworkSnapshot(
                    handle = network.optString("handle", "0"), transport = network.optString("transport", "none"),
                    interfaceName = network.optString("interface"), addresses = strings("addresses"), dns = strings("dns"),
                    available = network.optBoolean("available"), validated = network.optBoolean("validated"), metered = network.optBoolean("metered"),
                ),
                transport = capabilities.optString("transport", "IPv6 / QUIC / hole-v2"),
                liveConfiguration = capabilities.optBoolean("live_configuration"),
                networkBinding = capabilities.optBoolean("network_binding"),
                peers = json.optJSONArray("peer_transports")?.let { array -> (0 until array.length()).map { PeerSnapshot.fromJson(array.getJSONObject(it)) } }.orEmpty(),
            )
        }

        fun failure(code: String, message: String) = CoreSnapshot(
            engineState = "error", errorCode = code, errorMessage = message,
        )
    }
}
