package dev.hole.corebridge

import kotlin.test.assertEquals
import kotlin.test.assertNull
import org.json.JSONObject
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [26])
class SendLossSnapshotTest {
    @Test fun parsesBilateralMetricsAndBandwidthWithoutRecomputingRates() {
        val peer = PeerSnapshot.fromJson(JSONObject("""{
            "quic_loss":{"window_seconds":60,"samples":"1000","lost_packets":"19","rate_percent":1.9},
            "receive_loss":{"window_seconds":60,"samples":"900","lost_packets":"9","rate_percent":1},
            "bandwidth":{"send":{"peak_bps":8000000,"average_bps":4000000},
              "receive":{"peak_bps":16000000,"average_bps":8000000},
              "total":{"peak_bps":16000000,"average_bps":12000000}}
        }"""))
        assertEquals("1.90% · 双向合计 · 近 1 分钟 · 丢失 19 / 已判定 1000 包", peer.quicLoss!!.description("双向合计"))
        assertEquals("1.00% · 对端 → 本机 · 近 1 分钟 · 丢失 9 / 已判定 900 包", peer.receiveLoss!!.description("对端 → 本机"))
        assertEquals("合计 16.00 Mbps · 上行 8.00 Mbps · 下行 16.00 Mbps · 本连接 · 约 1 秒采样", peer.bandwidth!!.description(true))
        assertEquals("合计 12.00 Mbps · 上行 4.00 Mbps · 下行 8.00 Mbps · 本连接 · 含空闲时间", peer.bandwidth.description(false))
        val old = PeerSnapshot.fromJson(JSONObject("{}"))
        assertNull(old.quicLoss)
        assertNull(old.receiveLoss)
        assertNull(old.bandwidth)
        assertNull(BandwidthSnapshot.fromJson(JSONObject("""{"send":{"peak_bps":-1,"average_bps":0}}""")))
    }

    private fun peer(loss: String) = PeerSnapshot.fromJson(JSONObject("""{"send_loss":$loss,"retry_count":"99","dropped_datagrams":"999"}"""))

    @Test fun displaysCoreRateAndPreservesFullCounterPrecision() {
        val loss = peer("""{"window_seconds":60,"samples":"18446744073709551615","lost_packets":"9007199254740993","rate_percent":1.25}""").sendLoss!!
        assertEquals("1.25% · 本机 → 对端 · 近 1 分钟 · 丢失 9007199254740993 / 已判定 18446744073709551615 包", loss.description())
    }

    @Test fun distinguishesZeroLossIdleAndUnavailable() {
        assertEquals("0.00% · 本机 → 对端 · 近 1 分钟 · 丢失 0 / 已判定 80 包",
            peer("""{"window_seconds":60,"samples":"80","lost_packets":"0","rate_percent":0}""").sendLoss!!.description())
        assertEquals("暂无样本 · 本机 → 对端 · 近 1 分钟",
            peer("""{"window_seconds":60,"samples":"0","lost_packets":"0","rate_percent":null}""").sendLoss!!.description())
        assertNull(PeerSnapshot.fromJson(JSONObject("{}")).sendLoss)
        for (loss in listOf("null", "{}", """{"window_seconds":60,"samples":"1","lost_packets":"0","rate_percent":"0"}""",
            """{"window_seconds":60,"samples":"1","lost_packets":"0","rate_percent":101}""",
            """{"window_seconds":60,"samples":"0","lost_packets":"0","rate_percent":0}""")) {
            assertNull(peer(loss).sendLoss, loss)
        }
    }
}
