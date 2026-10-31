package dev.hole.app

import android.content.Context
import android.content.res.Configuration
import android.graphics.Color
import android.graphics.Typeface
import android.graphics.drawable.GradientDrawable
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.widget.ImageButton
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import dev.hole.app.ui.engineLabel
import dev.hole.app.ui.formatBytes
import dev.hole.app.ui.isFixedPeer
import dev.hole.app.ui.peerPathLabel
import dev.hole.app.ui.signalLabel
import dev.hole.corebridge.CoreSnapshot
import dev.hole.corebridge.PeerSnapshot

/**
 * Compact Material-like status card used by the overlay window.
 *
 * This is intentionally a normal View hierarchy: an overlay service has no
 * Activity lifecycle to host a Compose composition, while its controls still
 * expose normal Android accessibility actions.
 */
internal class FloatingBallSummaryPanel(
    context: Context,
    private val onStart: () -> Unit,
    private val onStop: () -> Unit,
    private val onOpenDetails: () -> Unit,
) : ScrollView(context) {
    private val density = resources.displayMetrics.density
    private val desiredWidth = dp(304)
    private val content = LinearLayout(context).apply {
        orientation = LinearLayout.VERTICAL
        gravity = Gravity.START
    }
    private var snapshot = CoreSnapshot()

    init {
        isFillViewport = false
        clipToOutline = true
        elevation = dp(10).toFloat()
        background = roundedBackground(cardColor(), borderColor())
        addView(content, LayoutParams(LayoutParams.MATCH_PARENT, LayoutParams.WRAP_CONTENT))
        update(snapshot)
    }

    override fun onMeasure(widthMeasureSpec: Int, heightMeasureSpec: Int) {
        val availableHeight = (resources.displayMetrics.heightPixels * 0.78f).toInt().coerceAtLeast(dp(260))
        val cappedHeight = MeasureSpec.makeMeasureSpec(availableHeight, MeasureSpec.AT_MOST)
        super.onMeasure(widthMeasureSpec, cappedHeight)
        val width = when (MeasureSpec.getMode(widthMeasureSpec)) {
            MeasureSpec.EXACTLY -> MeasureSpec.getSize(widthMeasureSpec)
            else -> desiredWidth
        }
        setMeasuredDimension(width, measuredHeight.coerceAtMost(availableHeight))
    }

    fun preferredWidth(): Int = desiredWidth

    fun update(next: CoreSnapshot) {
        snapshot = next
        content.removeAllViews()
        content.setPadding(dp(18), dp(16), dp(18), dp(14))

        val header = LinearLayout(context).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
        }
        header.addView(textView("hole", 20f, primaryTextColor(), Typeface.BOLD), weightedParams(1f))
        header.addView(
            textView(statusText(next), 13f, secondaryTextColor(), Typeface.NORMAL).apply {
                gravity = Gravity.END
                contentDescription = "运行状态：" + statusText(next)
            },
            LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT),
        )
        content.addView(header, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT))
        addGap(5)
        content.addView(textView(signalText(next), 12f, secondaryTextColor(), Typeface.NORMAL))
        addGap(14)

        addMetricPair(
            "设备线路", next.peers.size.toString(),
            "可用服务", next.mappings.count { it.state == "active" }.toString(),
        )
        addGap(8)
        addMetricPair(
            "TCP / UDP", next.tcpSessions.toString() + " / " + next.udpSessions,
            "上行 / 下行", trafficText(next),
        )

        val activePeers = floatingBallActivePeers(next)
        if (activePeers.isNotEmpty()) {
            addGap(15)
            content.addView(textView("对端线路", 12f, secondaryTextColor(), Typeface.BOLD))
            addGap(4)
            activePeers.forEachIndexed { index, peer ->
                if (index > 0) addDivider()
                addPeer(peer)
            }
            val remaining = next.peers.size - activePeers.size
            if (remaining > 0) {
                addGap(4)
                content.addView(textView("还有 " + remaining + " 台设备", 12f, secondaryTextColor(), Typeface.NORMAL))
            }
        }

        addGap(12)
        addDivider()
        addGap(8)
        val actions = LinearLayout(context).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
        }
        val runButton = actionButton(
            if (next.runRequested) R.drawable.ic_stop else R.drawable.ic_play_arrow,
            if (next.runRequested) "停止转发" else "启动转发",
            "floating-run-action",
        ) {
            if (snapshot.runRequested) onStop() else onStart()
        }
        actions.addView(runButton, buttonParams())
        actions.addView(
            textView(if (next.runRequested) "转发运行中" else "点击播放图标启动", 13f, secondaryTextColor(), Typeface.NORMAL).apply {
                setPadding(dp(4), 0, dp(4), 0)
            },
            weightedParams(1f),
        )
        actions.addView(
            actionButton(R.drawable.ic_open_in_new, "打开详情", "floating-details-action", onOpenDetails),
            buttonParams(),
        )
        content.addView(actions, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, dp(52)))
    }

    private fun addMetricPair(firstLabel: String, firstValue: String, secondLabel: String, secondValue: String) {
        val row = LinearLayout(context).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
        }
        row.addView(metric(firstLabel, firstValue), weightedParams(1f))
        row.addView(metric(secondLabel, secondValue), weightedParams(1f))
        content.addView(row, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT))
    }

    private fun metric(label: String, value: String): View {
        val column = LinearLayout(context).apply {
            orientation = LinearLayout.VERTICAL
            gravity = Gravity.START
        }
        column.addView(textView(label, 11f, secondaryTextColor(), Typeface.NORMAL))
        column.addView(textView(value, 16f, primaryTextColor(), Typeface.BOLD))
        return column
    }

    private fun addPeer(peer: PeerSnapshot) {
        val row = LinearLayout(context).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            contentDescription = peer.peerId.ifBlank { "对端设备" } + "，" + floatingBallPathLabel(peer) + "，" + floatingBallRttLabel(peer)
        }
        val detail = LinearLayout(context).apply {
            orientation = LinearLayout.VERTICAL
            gravity = Gravity.START
        }
        detail.addView(textView(peer.peerId.ifBlank { "未命名设备" }, 14f, primaryTextColor(), Typeface.BOLD))
        detail.addView(textView(floatingBallPathLabel(peer), 12f, secondaryTextColor(), Typeface.NORMAL))
        row.addView(detail, weightedParams(1f))
        row.addView(
            textView(floatingBallRttLabel(peer), 13f, primaryColor(), Typeface.BOLD).apply { gravity = Gravity.END },
            LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT),
        )
        content.addView(row, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, dp(42)))
    }

    private fun actionButton(icon: Int, description: String, tagValue: String, listener: () -> Unit): ImageButton {
        return ImageButton(context).apply {
            tag = tagValue
            setImageResource(icon)
            setColorFilter(Color.WHITE)
            background = roundedBackground(primaryColor(), Color.TRANSPARENT, oval = true)
            contentDescription = description
            tooltipText = description
            setOnClickListener { listener() }
            isClickable = true
            isFocusable = true
        }
    }

    private fun addDivider() {
        content.addView(View(context).apply { setBackgroundColor(dividerColor()) },
            LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, dp(1)))
    }

    private fun addGap(dpValue: Int) {
        content.addView(View(context), LinearLayout.LayoutParams(1, dp(dpValue)))
    }

    private fun textView(value: String, sizeSp: Float, color: Int, style: Int): TextView {
        return TextView(context).apply {
            text = value
            textSize = sizeSp
            setTextColor(color)
            typeface = Typeface.create(Typeface.DEFAULT, style)
            includeFontPadding = true
        }
    }

    private fun weightedParams(weight: Float): LinearLayout.LayoutParams =
        LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, weight).apply {
            marginEnd = dp(10)
        }

    private fun buttonParams(): LinearLayout.LayoutParams =
        LinearLayout.LayoutParams(dp(48), dp(48))

    private fun roundedBackground(fill: Int, stroke: Int, oval: Boolean = false): GradientDrawable =
        GradientDrawable().apply {
            shape = if (oval) GradientDrawable.OVAL else GradientDrawable.RECTANGLE
            setColor(fill)
            if (stroke != Color.TRANSPARENT) setStroke(dp(1), stroke)
            if (!oval) cornerRadius = dp(24).toFloat()
        }

    private fun dp(value: Int): Int = (value * density).toInt().coerceAtLeast(1)

    private fun cardColor(): Int = if (isDarkTheme()) Color.rgb(32, 31, 37) else Color.rgb(238, 238, 243)
    private fun primaryTextColor(): Int = if (isDarkTheme()) Color.rgb(229, 225, 233) else Color.rgb(27, 27, 32)
    private fun secondaryTextColor(): Int = if (isDarkTheme()) Color.rgb(203, 197, 209) else Color.rgb(72, 71, 79)
    private fun primaryColor(): Int = if (isDarkTheme()) Color.rgb(173, 198, 255) else Color.rgb(67, 94, 145)
    private fun dividerColor(): Int = if (isDarkTheme()) Color.rgb(72, 69, 79) else Color.rgb(203, 201, 210)
    private fun borderColor(): Int = if (isDarkTheme()) Color.rgb(72, 69, 79) else Color.rgb(203, 201, 210)
    private fun isDarkTheme(): Boolean =
        resources.configuration.uiMode and Configuration.UI_MODE_NIGHT_MASK == Configuration.UI_MODE_NIGHT_YES

    private fun statusText(snapshot: CoreSnapshot): String =
        engineLabel(snapshot)

    private fun signalText(snapshot: CoreSnapshot): String =
        if (snapshot.runRequested) "信令 · " + signalLabel(snapshot.signalState) else "转发服务未启动"

    private fun trafficText(snapshot: CoreSnapshot): String {
        val sent = snapshot.peers.sumOf { it.bytesSent.toLongOrNull() ?: 0L }
        val received = snapshot.peers.sumOf { it.bytesReceived.toLongOrNull() ?: 0L }
        return formatBytes(sent.toString()) + " / " + formatBytes(received.toString())
    }
}

internal fun activePeerCount(snapshot: CoreSnapshot): Int = snapshot.peers.count {
    it.state in FLOATING_BALL_ACTIVE_STATES
}

internal fun floatingBallActivePeers(snapshot: CoreSnapshot): List<PeerSnapshot> =
    snapshot.peers.filter { it.state in FLOATING_BALL_ACTIVE_STATES }.take(2)

internal fun floatingBallPathLabel(peer: PeerSnapshot): String = when {
    peer.pathType == "hole" -> "Hole 中继"
    peer.pathType == "relay" -> "TURN 中继"
    peer.pathType == "direct" || isFixedPeer(peer) -> {
        val family = peer.addressFamily.takeIf { it.isNotBlank() }?.let { " · " + it }.orEmpty()
        "直连" + family
    }
    else -> peerPathLabel(peer)
}

internal fun floatingBallRttLabel(peer: PeerSnapshot): String =
    peer.rttMs.toLongOrNull()?.takeIf { it > 0 }?.let { it.toString() + " ms" } ?: "—"

private val FLOATING_BALL_ACTIVE_STATES = setOf(
    "active", "switching", "connecting", "reconnecting", "waiting_credentials",
)
