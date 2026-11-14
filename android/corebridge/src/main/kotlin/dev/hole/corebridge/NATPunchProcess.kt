package dev.hole.corebridge

import org.json.JSONObject
import java.time.Instant

data class NATPunchProcess(
    val transportId: String = "", val peerId: String = "", val sequence: String = "0",
    val eventsDropped: Long = 0, val eventsEvicted: Long = 0,
    val attempts: List<NATPunchRun> = emptyList(), val events: List<NATPunchEvent> = emptyList(),
) {
    fun restartsWith(delta: NATPunchProcess): Boolean =
        transportId != delta.transportId || delta.sequenceNumber < sequenceNumber || delta.attempts.any { incoming ->
            attempts.any { it.generation == incoming.generation && it.startedAt != incoming.startedAt }
        }

    fun merge(delta: NATPunchProcess): NATPunchProcess {
        if (delta.attempts.isEmpty()) return this
        val reset = restartsWith(delta)
        val generations = delta.attempts.map { it.generation }.toSet()
        val combined = ((if (reset) emptyList() else events) + delta.events)
            .filter { it.generation in generations }.distinctBy { it.sequence }.sortedBy { it.number }
        val retained = (combined.filterNot { it.isPacket }.takeLast(512) + combined.filter { it.isPacket }.takeLast(4096)).sortedBy { it.number }
        return delta.copy(events = retained)
    }

    internal fun atAbsoluteTime(timeMs: Long): NATPunchProcess {
        val history = events.filter { it.timestampMs <= timeMs }
        return copy(events = history, attempts = attempts.filter { it.startedMs <= timeMs }.map { run ->
            run.at((timeMs - run.startedMs).coerceIn(0L, run.elapsedMs), history.filter { it.generation == run.generation })
        })
    }
    private val sequenceNumber get() = sequence.toLongOrNull() ?: 0
    companion object {
        fun fromJson(text: String) = parse(JSONObject(text), true)
        private fun parse(j: JSONObject, discovery: Boolean): NATPunchProcess = NATPunchProcess(
            transportId = j.optString("transport_id"), peerId = j.optString("peer_id"), sequence = j.optString("sequence", "0"),
            eventsDropped = j.longValue("events_dropped"), eventsEvicted = j.longValue("events_evicted"),
            attempts = j.objects("attempts", 6).map { a ->
                val n = a.optJSONObject("network") ?: JSONObject()
                NATPunchRun(
                    generation = a.optString("generation", "0"), phase = a.optString("phase"),
                    startedAt = a.optString("started_at"), finishedAt = a.optString("finished_at"), elapsedMs = a.longValue("elapsed_ms"),
                    state = a.optString("state"), stage = a.optString("stage"),
                    network = NetworkSnapshot(handle = n.optString("handle", "0"), transport = n.optString("transport"),
                        interfaceName = n.optString("interface"), addresses = n.strings("addresses", 64), dns = n.strings("dns", 32),
                        available = n.optBoolean("available"), validated = n.optBoolean("validated"), metered = n.optBoolean("metered")),
                    rounds = a.objects("rounds", 4).map(NATPunchRound::fromJson),
                    sockets = a.objects("sockets", 512).map(NATPunchSocket::fromJson),
                    discovery = if (discovery) a.optJSONObject("discovery")?.let { parse(it, false) } else null,
                )
            },
            events = j.objects("events", 4608).map(NATPunchEvent::fromJson),
        )
    }
}

data class NATPunchRun(
    val generation: String, val phase: String, val startedAt: String, val finishedAt: String,
    val elapsedMs: Long, val state: String, val stage: String, val network: NetworkSnapshot,
    val rounds: List<NATPunchRound>, val sockets: List<NATPunchSocket>, val discovery: NATPunchProcess? = null,
) {
    internal val startedMs = instantMillis(startedAt)

    fun at(cursorMs: Long, events: List<NATPunchEvent>): NATPunchRun {
        val history = events.filter { it.elapsedMs <= cursorMs }
        val roundsAtCursor = linkedMapOf<Int, NATPunchRound>()
        history.forEach { event -> event.roundSnapshot?.let { roundsAtCursor[it.round] = it } }
        val socketHistory = history.groupBy { it.socketId }
        val finish = history.lastOrNull { it.kind == "finished" }
        return copy(
            elapsedMs = cursorMs, finishedAt = finish?.time.orEmpty(), state = finish?.result?.ifBlank { state } ?: "running",
            stage = history.lastOrNull { it.kind == "stage" }?.stage ?: "preparing",
            rounds = roundsAtCursor.values.toList(),
            sockets = sockets.filter { it.createdMs <= cursorMs }.map { it.at(socketHistory[it.id].orEmpty()) },
            discovery = discovery?.atAbsoluteTime(startedMs + cursorMs),
        )
    }
}

data class NATPunchRound(
    val round: Int, val trial: Int, val mode: String, val stage: String,
    val localModel: NATPunchModel, val peerModel: NATPunchModel,
    val localPlan: NATPunchPlan?, val peerPlan: NATPunchPlan?, val targets: List<String>,
    val dialer: Boolean, val selectedSocket: String,
    val diagnostic: NATPunchRoundStats? = null,
) {
    companion object {
        fun fromJson(j: JSONObject) = NATPunchRound(
            round = j.optInt("round"), trial = j.optInt("trial"), mode = j.optString("mode"), stage = j.optString("stage"),
            localModel = NATPunchModel.fromJson(j.optJSONObject("local_model")), peerModel = NATPunchModel.fromJson(j.optJSONObject("peer_model")),
            localPlan = j.optJSONObject("local_plan")?.let(NATPunchPlan::fromJson),
            peerPlan = j.optJSONObject("peer_plan")?.let(NATPunchPlan::fromJson), targets = j.strings("targets", 256),
            dialer = j.optBoolean("dialer"), selectedSocket = j.optString("selected_socket"),
            diagnostic = j.optJSONObject("diagnostic")?.let(NATPunchRoundStats::fromJson),
        )
    }
}

data class NATPunchRoundStats(
    val probesSent: Int, val probesReceived: Int, val acksSent: Int, val acksReceived: Int, val acksVerified: Int,
    val sourceRejected: Int, val packetRejected: Int, val readErrors: Int, val quicEstablished: Boolean, val error: String,
) {
    companion object {
        fun fromJson(j: JSONObject) = NATPunchRoundStats(
            probesSent = j.optInt("probes_sent"), probesReceived = j.optInt("probes_received"),
            acksSent = j.optInt("acks_sent"), acksReceived = j.optInt("acks_received"), acksVerified = j.optInt("acks_verified"),
            sourceRejected = j.optInt("source_rejected"), packetRejected = j.optInt("packet_rejected"), readErrors = j.optInt("read_errors"),
            quicEstablished = j.optBoolean("quic_established"), error = j.optString("error"),
        )
    }
}

data class NATPunchModel(
    val nat: NATSnapshot = NATSnapshot(), val mapping: String = "", val filtering: String = "",
    val filteringServer: String = "", val portStep: Int = 0, val publicIp: String = "", val lastMappingPort: Int = 0,
    val allocationScope: String = "", val preservation: String = "", val noPat: String = "",
    val ipEvidence: List<NATPunchIpEvidence> = emptyList(),
) {
    companion object {
        fun fromJson(j: JSONObject?): NATPunchModel {
            if (j == null) return NATPunchModel()
            return NATPunchModel(
                nat = NATSnapshot.fromJson(j), mapping = j.optString("mapping_behavior"), filtering = j.optString("filtering_behavior"),
                filteringServer = j.optString("filtering_server"), portStep = j.optInt("port_step"), publicIp = j.optString("public_ip"),
                lastMappingPort = j.optInt("last_mapping_port"), allocationScope = j.optString("allocation_scope"),
                preservation = j.optString("port_preservation"), noPat = j.optString("no_pat"),
                ipEvidence = j.objects("ip_evidence", 24).map { NATPunchIpEvidence(it.optString("ip"), it.optInt("targets"), it.optInt("ports"), it.optString("trend"), it.optInt("step")) },
            )
        }
    }
}

data class NATPunchIpEvidence(val ip: String, val targets: Int, val ports: Int, val trend: String, val step: Int)
data class NATPunchPlan(val ip: String, val port: Int, val target: Int, val width: Int, val sockets: Int) {
    companion object { fun fromJson(j: JSONObject) = NATPunchPlan(j.optString("ip"), j.optInt("port"), j.optInt("target"), j.optInt("width"), j.optInt("sockets")) }
}

data class NATPunchSocket(
    val id: String, val round: Int, val trial: Int, val role: String, val boundAddress: String, val localAddress: String,
    val localAddressSource: String, val mappedAddress: String, val mappingSource: String, val state: String, val target: String,
    val createdMs: Long, val lastActivityMs: Long, val probesSent: Int, val acksSent: Int, val probesReceived: Int, val acksReceived: Int,
) {
    val label get() = "S${id.substringAfterLast('/').padStart(2, '0')}"
    fun at(events: List<NATPunchEvent>): NATPunchSocket {
        val related = events.filter { it.socketId == id }
        val latest = related.lastOrNull()
        val mapped = related.lastOrNull { it.kind == "mapped" }
        val roleEvent = related.lastOrNull { it.kind == "socket_role" || it.kind == "socket_bound" }
        val local = related.lastOrNull { it.kind in setOf("socket_bound", "socket_selected") && it.localAddress.isNotBlank() }
        var replayState = "bound"
        related.forEach { event ->
            replayState = when (event.kind) {
                "socket_closed" -> "closed"; "socket_retained" -> "retained"; "socket_selected" -> "selected"
                "quic_started", "quic_established" -> "quic"; "ack_verified" -> "verified"
                "probe_sent" -> if (replayState in setOf("bound", "probing")) "probing" else replayState
                else -> replayState
            }
        }
        return copy(state = replayState, role = roleEvent?.detail?.ifBlank { role } ?: role,
            localAddress = local?.localAddress.orEmpty(), localAddressSource = local?.localAddressSource?.ifBlank { local.addressSource }.orEmpty(),
            mappedAddress = mapped?.mappedAddress.orEmpty(), mappingSource = mapped?.addressSource.orEmpty(),
            target = related.lastOrNull { it.remoteAddress.isNotBlank() }?.remoteAddress.orEmpty(), lastActivityMs = latest?.elapsedMs ?: createdMs,
            probesSent = related.count { it.kind == "probe_sent" }, acksSent = related.count { it.kind == "ack_sent" },
            probesReceived = related.count { it.kind == "probe_received" }, acksReceived = related.count { it.kind == "ack_received" })
    }
    companion object {
        fun fromJson(j: JSONObject) = NATPunchSocket(
            id = j.optString("id"), round = j.optInt("round"), trial = j.optInt("trial"), role = j.optString("role"),
            boundAddress = j.optString("bound_address"), localAddress = j.optString("local_address"), localAddressSource = j.optString("local_address_source"),
            mappedAddress = j.optString("mapped_address"), mappingSource = j.optString("mapping_source"), state = j.optString("state"), target = j.optString("target"),
            createdMs = j.longValue("created_ms"), lastActivityMs = j.longValue("last_activity_ms"), probesSent = j.optInt("probes_sent"), acksSent = j.optInt("acks_sent"),
            probesReceived = j.optInt("probes_received"), acksReceived = j.optInt("acks_received"),
        )
    }
}

data class NATPunchEvent(
    val sequence: String, val generation: String, val time: String, val elapsedMs: Long, val round: Int, val trial: Int,
    val kind: String, val stage: String, val title: String, val socketId: String, val direction: String, val packet: String,
    val packetSequence: Int, val peerSocket: Int, val retryAfterMs: Long, val probeLimit: Int,
    val localAddress: String, val boundAddress: String, val remoteAddress: String,
    val mappedAddress: String, val addressSource: String, val bytes: Int, val validation: String, val detail: String,
    val transactionId: String = "", val changeRequest: Int = 0, val serverAddress: String = "", val otherAddress: String = "",
    val mode: String = "", val result: String = "", val model: NATPunchModel? = null, val plan: NATPunchPlan? = null,
    val roundSnapshot: NATPunchRound? = null,
    val localAddressSource: String = "", val socketRole: String = "",
) {
    val number get() = sequence.toLongOrNull() ?: 0L
    val timestampMs = instantMillis(time)
    val isPacket get() = direction.isNotBlank() && packet.isNotBlank()
    val isSTUN get() = kind in setOf("stun_packet", "stun_error") || transactionId.isNotBlank()
    val isDatagram get() = kind in setOf("stun_packet", "stun_error", "probe_sent", "probe_received", "ack_sent", "ack_received", "packet_rejected", "source_rejected")
    companion object {
        fun fromJson(j: JSONObject) = NATPunchEvent(
            sequence = j.optString("sequence", "0"), generation = j.optString("generation", "0"), time = j.optString("time"), elapsedMs = j.longValue("elapsed_ms"),
            round = j.optInt("round"), trial = j.optInt("trial"), kind = j.optString("kind"), stage = j.optString("stage"), title = j.optString("title"),
            socketId = j.optString("socket_id"), direction = j.optString("direction"), packet = j.optString("packet"), packetSequence = j.optInt("packet_sequence"), peerSocket = j.optInt("peer_socket"),
            retryAfterMs = j.longValue("retry_after_ms"), probeLimit = j.optInt("probe_limit"),
            localAddress = j.optString("local_address"), boundAddress = j.optString("bound_address"), remoteAddress = j.optString("remote_address"), mappedAddress = j.optString("mapped_address"),
            addressSource = j.optString("address_source"), bytes = j.optInt("bytes"), validation = j.optString("validation"), detail = j.optString("detail"),
            transactionId = j.optString("transaction_id"), changeRequest = j.optInt("change_request"),
            serverAddress = j.optString("server_address"), otherAddress = j.optString("other_address"),
            mode = j.optString("mode"), result = j.optString("result"),
            model = j.optJSONObject("model")?.let(NATPunchModel::fromJson), plan = j.optJSONObject("plan")?.let(NATPunchPlan::fromJson),
            roundSnapshot = j.optJSONObject("round_snapshot")?.let(NATPunchRound::fromJson),
            localAddressSource = j.optString("local_address_source"), socketRole = j.optString("socket_role"),
        )
    }
}

private fun instantMillis(value: String) = runCatching { Instant.parse(value).toEpochMilli() }.getOrDefault(0L)
private fun JSONObject.longValue(key: String) = optString(key).toLongOrNull() ?: 0L
private fun JSONObject.objects(key: String, limit: Int): List<JSONObject> {
    val a = optJSONArray(key) ?: return emptyList()
    return (0 until minOf(a.length(), limit)).mapNotNull { a.optJSONObject(it) }
}
private fun JSONObject.strings(key: String, limit: Int): List<String> {
    val a = optJSONArray(key) ?: return emptyList()
    return (0 until minOf(a.length(), limit)).map { a.optString(it) }
}
