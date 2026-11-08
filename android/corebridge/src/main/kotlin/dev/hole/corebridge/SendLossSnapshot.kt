package dev.hole.corebridge

import java.util.Locale
import org.json.JSONObject

/** Display the core's QUIC loss estimate; never derive it from receive drops. */
data class SendLossSnapshot(
    val windowSeconds: Int,
    val samples: String,
    val lostPackets: String,
    val ratePercent: Double?,
) {
    fun description(): String {
        val window = if (windowSeconds == 60) "近 1 分钟" else "近 $windowSeconds 秒"
        val scope = "本机 → 对端 · $window"
        val rate = ratePercent ?: return "暂无样本 · $scope"
        return "${String.format(Locale.ROOT, "%.2f%%", rate)} · $scope · 丢失 $lostPackets / 已判定 $samples 包"
    }

    companion object {
        fun fromJson(json: JSONObject?): SendLossSnapshot? {
            if (json == null || !json.has("rate_percent")) return null
            val window = json.optInt("window_seconds")
            val samples = json.optString("samples")
            val lost = json.optString("lost_packets")
            val sampleCount = samples.toULongOrNull() ?: return null
            val lostCount = lost.toULongOrNull() ?: return null
            if (window <= 0 || lostCount > sampleCount) return null
            val rate = if (json.isNull("rate_percent")) null else {
                val number = (json.opt("rate_percent") as? Number)?.toDouble() ?: return null
                if (!number.isFinite() || number !in 0.0..100.0) return null
                number
            }
            if ((sampleCount == 0uL) != (rate == null)) return null
            return SendLossSnapshot(window, samples, lost, rate)
        }
    }
}
