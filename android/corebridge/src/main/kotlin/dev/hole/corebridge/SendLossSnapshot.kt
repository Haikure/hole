package dev.hole.corebridge

import java.util.Locale
import org.json.JSONObject

/** Display the core's QUIC loss estimate; never derive it from receive drops. */
data class SendLossSnapshot(
    val scope: String,
    val samples: String,
    val lostPackets: String,
    val ratePercent: Double?,
) {
    fun rateLabel(): String = ratePercent?.let { String.format(Locale.ROOT, "%.2f%%", it) } ?: "暂无样本"

    fun description(direction: String = "本机 → 对端"): String {
        val directionScope = "$direction · $scope"
        val rate = ratePercent ?: return "暂无样本 · $directionScope"
        return "${String.format(Locale.ROOT, "%.2f%%", rate)} · $directionScope · 丢失 $lostPackets / 已判定 $samples 包"
    }

    companion object {
        fun fromJson(json: JSONObject?): SendLossSnapshot? {
            if (json == null || !json.has("rate_percent")) return null
            val scope = when (json.optString("scope")) {
                "connection" -> "本连接"
                "" -> {
                    val window = json.optInt("window_seconds")
                    if (window <= 0) return null
                    if (window == 60) "近 1 分钟" else "近 $window 秒"
                }
                else -> return null
            }
            val samples = json.optString("samples")
            val lost = json.optString("lost_packets")
            val sampleCount = samples.toULongOrNull() ?: return null
            val lostCount = lost.toULongOrNull() ?: return null
            if (lostCount > sampleCount) return null
            val rate = if (json.isNull("rate_percent")) null else {
                val number = (json.opt("rate_percent") as? Number)?.toDouble() ?: return null
                if (!number.isFinite() || number !in 0.0..100.0) return null
                number
            }
            if ((sampleCount == 0uL) != (rate == null)) return null
            return SendLossSnapshot(scope, samples, lost, rate)
        }
    }
}
