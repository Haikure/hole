package dev.hole.corebridge

import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [26])
class NATSnapshotTest {
    @Test fun statusesAndPredictabilityRemainIndependent() {
        val n = NATSnapshot.fromJson(JSONObject("""{"status":"complete","type":"symmetric","predictability":"decremental","samples":3,"probed_at":"2026-11-04T00:00:00Z"}"""))
        assertEquals("对称型 · 可能可预测（递减）", n.description())
        assertEquals(3, n.samples)
        assertTrue(n.copy(status = "expired").description().startsWith("已过期"))
        assertEquals("对端未上报", NATSnapshot.fromJson(null).description())
        assertEquals("信息不足", NATSnapshot.fromJson(null).description(local = true))
        assertTrue(n.copy(status = "insufficient", type = "unknown", predictability = "stable").description().contains("映射稳定"))
        val p = PeerSnapshot.fromJson(JSONObject().put("peer_nat", JSONObject("""{"status":"probing"}""")))
        assertTrue(p.peerNAT.description().contains("探测中"))
    }
}
