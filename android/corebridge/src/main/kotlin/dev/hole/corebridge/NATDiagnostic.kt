package dev.hole.corebridge

import org.json.JSONObject
import java.time.OffsetDateTime
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.DateTimeParseException
import java.util.Locale

private val natTimeFormat = DateTimeFormatter.ofPattern("yyyy-MM-dd HH:mm:ss", Locale.ROOT)

fun formatNATTime(value: String): String = try {
    OffsetDateTime.parse(value).atZoneSameInstant(ZoneId.systemDefault()).format(natTimeFormat)
} catch (_: DateTimeParseException) {
    value
}

data class DiagnosticField(val label: String, val value: String, val detail: String = "") {
    val displayValue: String get() = formatNATTime(value)
}
data class DiagnosticSection(val title: String, val fields: List<DiagnosticField>)
data class DiagnosticTile(val icon: String, val label: String, val value: String)
data class NATDiagnostic(
    val state: String = "idle",
    val title: String = "检测当前网络",
    val summary: String = "检查 IPv4 NAT 的映射与过滤方式，了解 UDP 打洞条件。无需加入房间。",
    val sections: List<DiagnosticSection> = emptyList(),
    val tone: String = "neutral",
    val badge: String = "待检测",
    val highlights: List<DiagnosticTile> = emptyList(),
) {
    val busy: Boolean get() = state == "queued" || state == "probing"
    companion object {
        fun fromJson(j: JSONObject?): NATDiagnostic {
            if (j == null) return NATDiagnostic()
            val sections = j.optJSONArray("sections")
            return NATDiagnostic(j.optString("state", "idle"), j.optString("title", "检测当前网络"), j.optString("summary"),
                List(sections?.length() ?: 0) { i ->
                    val section = sections!!.getJSONObject(i)
                    val fields = section.optJSONArray("fields")
                    DiagnosticSection(section.optString("title"), List(fields?.length() ?: 0) { n ->
                        val field = fields!!.getJSONObject(n)
                        DiagnosticField(field.optString("label"), field.optString("value"), field.optString("detail"))
                    })
                }, j.optString("tone", "neutral"), j.optString("badge", "待检测"),
                j.optJSONArray("highlights")?.let { tiles -> List(tiles.length()) { i ->
                    val tile = tiles.getJSONObject(i)
                    DiagnosticTile(tile.optString("icon"), tile.optString("label"), tile.optString("value"))
                } } ?: emptyList())
        }
    }
}
