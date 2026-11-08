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
