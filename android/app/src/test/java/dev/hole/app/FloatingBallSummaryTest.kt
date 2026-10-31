package dev.hole.app

import android.widget.ImageButton
import android.widget.TextView
import dev.hole.corebridge.CoreSnapshot
import dev.hole.corebridge.MappingSnapshot
import dev.hole.corebridge.PeerSnapshot
import kotlin.test.assertEquals
import kotlin.test.assertTrue
import org.junit.After
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [26])
class FloatingBallSummaryTest {
    private val context get() = RuntimeEnvironment.getApplication()

    @Before fun resetPanelState() {
        FloatingBallStateStore.setPanelExpanded(false)
    }

    @After fun clearPanelState() {
        FloatingBallStateStore.setPanelExpanded(false)
    }

    @Test fun summaryUsesLineLabelsAndUnknownRttPlaceholder() {
        val panel = FloatingBallSummaryPanel(context, {}, {}, {})
        panel.update(
            CoreSnapshot(
                runRequested = true,
                engineState = "running",
                signalState = "joined",
                mappings = listOf(MappingSnapshot("ssh", "provide", "tcp", "active", tcpSessions = 2)),
                peers = listOf(
                    PeerSnapshot(peerId = "phone", state = "active", pathType = "direct", addressFamily = "IPv6", rttMs = "12", bytesSent = "1024", bytesReceived = "2048"),
                    PeerSnapshot(peerId = "relay", state = "active", pathType = "hole", rttMs = "0"),
                    PeerSnapshot(peerId = "turn", state = "active", pathType = "relay", rttMs = "20"),
                ),
            ),
        )
        val texts = textValues(panel)
        assertTrue("直连 · IPv6" in texts)
        assertTrue("Hole 中继" in texts)
        assertTrue("12 ms" in texts)
        assertTrue("—" in texts)
        assertTrue("还有 1 台设备" in texts)
        assertTrue("1.0 KiB / 2.0 KiB" in texts)
        assertTrue("2 / 0" in texts)
    }

    @Test fun runIconSwitchesAndActionsRemainAccessible() {
        var starts = 0
        var stops = 0
        var details = 0
        val panel = FloatingBallSummaryPanel(context, { starts++ }, { stops++ }, { details++ })

        panel.update(CoreSnapshot(runRequested = false, engineState = "stopped"))
        var runButton = panel.findViewWithTag<ImageButton>("floating-run-action")
        assertEquals("启动转发", runButton.contentDescription)
        runButton.performClick()
        assertEquals(1, starts)

        panel.update(CoreSnapshot(runRequested = true, engineState = "running"))
        runButton = panel.findViewWithTag("floating-run-action")
        assertEquals("停止转发", runButton.contentDescription)
        runButton.performClick()
        assertEquals(1, stops)

        val detailsButton = panel.findViewWithTag<ImageButton>("floating-details-action")
        assertEquals("打开详情", detailsButton.contentDescription)
        detailsButton.performClick()
        assertEquals(1, details)
    }

    @Test fun storePublishesSnapshotAndExpandedStateInProcess() {
        val snapshot = CoreSnapshot(runRequested = true, engineState = "running")
        FloatingBallStateStore.publish(snapshot)
        FloatingBallStateStore.setPanelExpanded(true)
        assertEquals(snapshot, FloatingBallStateStore.snapshot.value)
        assertTrue(FloatingBallStateStore.panelExpanded.value)
    }

    private fun textValues(view: android.view.View): List<String> {
        val values = mutableListOf<String>()
        if (view is TextView) values += view.text.toString()
        if (view is android.view.ViewGroup) {
            for (index in 0 until view.childCount) values += textValues(view.getChildAt(index))
        }
        return values
    }
}
