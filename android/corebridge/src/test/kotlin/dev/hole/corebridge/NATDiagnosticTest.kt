package dev.hole.corebridge

import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.TimeZone

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [26])
class NATDiagnosticTest {
    @Test fun timestampsFollowSystemZoneAndKeepRawEvidence() {
        val originalZone = TimeZone.getDefault()
        try {
            TimeZone.setDefault(TimeZone.getTimeZone("Asia/Shanghai"))
            val raw = "2026-11-07T23:59:59.123456789Z"
            val field = DiagnosticField("开始时间", raw)
            assertEquals("2026-11-08 07:59:59", field.displayValue)
            assertEquals(raw, field.value)
            assertEquals(field.displayValue, NATSnapshot(probedAt = raw).probedAtDisplay)
            assertEquals(field.displayValue, formatNATTime("2026-11-08T05:44:59.123456789+05:45"))
            TimeZone.setDefault(TimeZone.getTimeZone("America/New_York"))
            assertEquals("2026-03-08 01:59:59", formatNATTime("2026-03-08T06:59:59Z"))
            assertEquals("2026-03-08 03:00:00", formatNATTime("2026-03-08T07:00:00Z"))
            assertEquals("2026-11-07 18:59:59", field.displayValue)
            assertEquals("暂无", DiagnosticField("完成时间", "暂无").displayValue)
            assertEquals("", NATSnapshot().probedAtDisplay)
        } finally {
            TimeZone.setDefault(originalZone)
        }
    }

    @Test fun retainsDetailedEvidenceAndUnknownValues() {
        val report = NATDiagnostic.fromJson(JSONObject("""{"state":"insufficient","title":"信息不足","summary":"缺少过滤证据","sections":[{"title":"映射与过滤","fields":[{"label":"过滤行为","value":"未知 / 尚未确认","detail":"未知不等于无限制"}]}]}"""))
        assertFalse(report.busy)
        assertEquals("未知不等于无限制", report.sections.single().fields.single().detail)
        assertEquals("未知 / 尚未确认", report.sections.single().fields.single().value)
        assertTrue(report.copy(state = "queued").busy)
        assertTrue(report.copy(state = "probing").busy)
        assertFalse(report.copy(state = "network_changed").busy)
        assertEquals("idle", NATDiagnostic.fromJson(null).state)
        val colored = NATDiagnostic.fromJson(JSONObject("""{"tone":"yellow","badge":"可能可预测","highlights":[{"icon":"samples","label":"有效样本","value":"18"}]}"""))
        assertEquals("yellow", colored.tone)
        assertEquals("可能可预测", colored.badge)
        assertEquals("18", colored.highlights.single().value)
    }
}
