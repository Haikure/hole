package dev.hole.corebridge

import java.util.Locale
import org.json.JSONObject

data class BandwidthRates(val peakBps: Double, val averageBps: Double) {
    companion object {
        fun fromJson(json: JSONObject?): BandwidthRates? {
            json ?: return null
            val peak = (json.opt("peak_bps") as? Number)?.toDouble() ?: return null
            val average = (json.opt("average_bps") as? Number)?.toDouble() ?: return null
            if (!peak.isFinite() || !average.isFinite() || peak < 0 || average < 0) return null
            return BandwidthRates(peak, average)
        }
    }
}

data class BandwidthSnapshot(val send: BandwidthRates, val receive: BandwidthRates, val total: BandwidthRates) {
    fun description(peak: Boolean, concise: Boolean = false): String {
        fun rate(value: BandwidthRates): String {
            val bps = if (peak) value.peakBps else value.averageBps
            val number = String.format(Locale.ROOT, "%.2f", bps / 1_000_000)
            return "${if (concise) number.trimEnd('0').trimEnd('.') else number} Mbps"
        }
        val scope = if (peak) "本连接 · 约 1 秒采样" else "本连接 · 按有效使用时长"
        val values = "合计 ${rate(total)} · 上行 ${rate(send)} · 下行 ${rate(receive)}"
        return if (concise) values else "$values · $scope"
    }

    companion object {
        fun fromJson(json: JSONObject?): BandwidthSnapshot? {
            json ?: return null
            return BandwidthSnapshot(
                BandwidthRates.fromJson(json.optJSONObject("send")) ?: return null,
                BandwidthRates.fromJson(json.optJSONObject("receive")) ?: return null,
                BandwidthRates.fromJson(json.optJSONObject("total")) ?: return null,
            )
        }
    }
}
