package dev.hole.corebridge

import org.json.JSONObject

data class PortMappingSnapshot(
    val status: String = "",
    val protocol: String = "",
    val address: String = "",
    val leaseSeconds: Long = 0,
    val reason: String = "",
) {
    fun description(): String {
        val state = when (status) {
            "" -> "暂无"
            "mapped" -> "已映射"
            "unavailable" -> "不可用"
            else -> "未知"
        }
        if (status != "mapped") return state
        val method = when (protocol) {
            "upnp" -> "UPnP"
            "nat-pmp" -> "NAT-PMP"
            else -> null
        }
        val lease = if (leaseSeconds > 0) "租期 $leaseSeconds 秒" else "永久租期"
        return listOfNotNull(state, method, address.takeIf { it.isNotBlank() }, lease).joinToString(" · ")
    }

    companion object {
        fun fromJson(j: JSONObject?) = if (j == null) PortMappingSnapshot() else PortMappingSnapshot(
            status = j.optString("status"), protocol = j.optString("protocol"),
            address = j.optString("address"), leaseSeconds = j.optLong("lease_seconds"), reason = j.optString("reason"),
        )
    }
}
