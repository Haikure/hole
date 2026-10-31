package dev.hole.app

import android.content.Context
import android.content.Intent
import android.graphics.Color
import android.graphics.PixelFormat
import android.graphics.Point
import android.graphics.drawable.GradientDrawable
import android.hardware.display.DisplayManager
import android.os.Build
import android.view.Display
import android.view.Gravity
import android.view.MotionEvent
import android.view.View
import android.view.ViewConfiguration
import android.view.WindowInsets
import android.view.WindowManager
import android.widget.FrameLayout
import android.widget.ImageView
import kotlin.math.max
import kotlin.math.min
import kotlin.math.roundToInt

/** Owned only by the service, so leaving the Activity does not remove the window. */
internal class FloatingBallWindow(
    private val context: Context,
    private val preferences: FloatingBallPreferences,
    startRequested: (() -> Unit)? = null,
    stopRequested: (() -> Unit)? = null,
    detailsRequested: (() -> Unit)? = null,
) : AutoCloseable {
    private val windowContext = if (Build.VERSION.SDK_INT >= 30) {
        val display = context.getSystemService(DisplayManager::class.java).getDisplay(Display.DEFAULT_DISPLAY)
        context.createDisplayContext(display).createWindowContext(WindowManager.LayoutParams.TYPE_APPLICATION_OVERLAY, null)
    } else context
    private val manager = windowContext.getSystemService(WindowManager::class.java)
    private val touchSlop = ViewConfiguration.get(windowContext).scaledTouchSlop
    private val gapPx = (8 * windowContext.resources.displayMetrics.density).roundToInt().coerceAtLeast(1)
    private val onStartRequested = startRequested ?: { openMainActivity(requestStart = true) }
    private val onStopRequested = stopRequested ?: { EngineService.stopRun(context) }
    private val onDetailsRequested = detailsRequested ?: { openMainActivity(openDetails = true) }
    private val root = OverlayRoot(windowContext, { closePanel() }) { expanded }
    private val panel = FloatingBallSummaryPanel(
        windowContext,
        onStart = { closePanel(); onStartRequested() },
        onStop = { closePanel(); onStopRequested() },
        onOpenDetails = { closePanel(); onDetailsRequested() },
    )
    private val icon = ImageView(windowContext)
    private var ballSizePx = 0
    private var layout: WindowManager.LayoutParams? = null
    private var downX = 0f
    private var downY = 0f
    private var initialX = 0
    private var initialY = 0
    private var dragging = false
    private var collapsedOnDown = false
    private var expanded = false
    private var lastSnapshot = FloatingBallStateStore.snapshot.value

    var view: View? = null
        private set

    init {
        root.isClickable = true
        root.setOnClickListener { togglePanel() }
        root.addView(panel)
        root.addView(icon)
        icon.apply {
            setImageResource(R.drawable.ic_launcher)
            contentDescription = "hole 悬浮球，点击展开状态面板，拖动调整位置"
            background = GradientDrawable().apply {
                shape = GradientDrawable.OVAL
                setColor(Color.WHITE)
            }
            clipToOutline = true
            setOnClickListener { togglePanel() }
            setOnTouchListener { target, event -> onBallTouch(target, event) }
        }
    }

    fun updateSnapshot(snapshot: dev.hole.corebridge.CoreSnapshot) {
        lastSnapshot = snapshot
        panel.update(snapshot)
        if (expanded && view != null) {
            layoutExpanded(currentBallPosition())
            updateWindowLayout()
        }
    }

    fun show() {
        val oldPosition = if (view != null) currentBallPosition() else null
        ballSizePx = (preferences.sizeDp() * windowContext.resources.displayMetrics.density).roundToInt()
        if (layout == null) {
            layout = WindowManager.LayoutParams(
                ballSizePx,
                ballSizePx,
                WindowManager.LayoutParams.TYPE_APPLICATION_OVERLAY,
                WindowManager.LayoutParams.FLAG_NOT_FOCUSABLE or
                    WindowManager.LayoutParams.FLAG_NOT_TOUCH_MODAL or
                    WindowManager.LayoutParams.FLAG_WATCH_OUTSIDE_TOUCH,
                PixelFormat.TRANSLUCENT,
            ).apply {
                @Suppress("RtlHardcoded")
                gravity = Gravity.TOP or Gravity.LEFT
                title = "hole 悬浮球"
                alpha = 0.98f
            }
        }
        panel.update(lastSnapshot)
        if (expanded) layoutExpanded(oldPosition ?: savedBallPosition()) else layoutClosed(oldPosition ?: savedBallPosition())
        if (view == null) {
            manager.addView(root, requireNotNull(layout))
            view = root
        } else {
            updateWindowLayout()
        }
    }

    private fun onBallTouch(target: View, event: MotionEvent): Boolean {
        when (event.actionMasked) {
            MotionEvent.ACTION_DOWN -> {
                collapsedOnDown = expanded
                if (expanded) closePanel()
                downX = event.rawX
                downY = event.rawY
                val position = currentBallPosition()
                initialX = position.x
                initialY = position.y
                dragging = false
                return true
            }
            MotionEvent.ACTION_MOVE -> {
                val dx = event.rawX - downX
                val dy = event.rawY - downY
                if (dx * dx + dy * dy > touchSlop * touchSlop) dragging = true
                if (dragging) {
                    layoutClosed(
                        Point(
                            initialX + dx.roundToInt(),
                            initialY + dy.roundToInt(),
                        ),
                    )
                    updateWindowLayout()
                }
                return true
            }
            MotionEvent.ACTION_UP -> {
                if (dragging) {
                    savePosition(currentBallPosition())
                } else if (!collapsedOnDown) {
                    target.performClick()
                }
                return true
            }
            MotionEvent.ACTION_CANCEL -> {
                if (dragging) savePosition(currentBallPosition())
                return true
            }
            else -> return false
        }
    }

    private fun togglePanel() {
        if (expanded) closePanel() else openPanel()
    }

    private fun openPanel() {
        if (view == null) return
        expanded = true
        FloatingBallStateStore.setPanelExpanded(true)
        layoutExpanded(currentBallPosition())
        updateWindowLayout()
    }

    private fun closePanel() {
        if (!expanded) {
            FloatingBallStateStore.setPanelExpanded(false)
            return
        }
        val position = currentBallPosition()
        expanded = false
        FloatingBallStateStore.setPanelExpanded(false)
        layoutClosed(position)
        updateWindowLayout()
    }

    private fun layoutClosed(position: Point) {
        val area = safeArea()
        val maxX = (area.right - area.left - ballSizePx).coerceAtLeast(0)
        val maxY = (area.bottom - area.top - ballSizePx).coerceAtLeast(0)
        val x = position.x.coerceIn(area.left, area.left + maxX)
        val y = position.y.coerceIn(area.top, area.top + maxY)
        val params = requireNotNull(layout)
        params.width = ballSizePx
        params.height = ballSizePx
        params.x = x
        params.y = y
        panel.visibility = View.GONE
        icon.layoutParams = FrameLayout.LayoutParams(ballSizePx, ballSizePx)
    }

    private fun layoutExpanded(anchor: Point) {
        val area = safeArea()
        val maxPanelWidth = (area.right - area.left - ballSizePx - gapPx).coerceAtLeast(dp(180))
        val panelWidth = min(panel.preferredWidth(), maxPanelWidth)
        val maxPanelHeight = (area.bottom - area.top).coerceAtLeast(dp(280))
        panel.visibility = View.VISIBLE
        panel.measure(
            View.MeasureSpec.makeMeasureSpec(panelWidth, View.MeasureSpec.EXACTLY),
            View.MeasureSpec.makeMeasureSpec(maxPanelHeight, View.MeasureSpec.AT_MOST),
        )
        val panelHeight = panel.measuredHeight.coerceAtLeast(dp(180))
        val rootWidth = panelWidth + gapPx + ballSizePx
        val rootHeight = max(panelHeight, ballSizePx)
        val roomLeft = anchor.x - area.left
        val roomRight = area.right - (anchor.x + ballSizePx)
        val panelOnRight = if (roomRight >= panelWidth + gapPx) {
            true
        } else if (roomLeft >= panelWidth + gapPx) {
            false
        } else {
            roomRight >= roomLeft
        }
        val minX = area.left
        val maxX = (area.right - rootWidth).coerceAtLeast(minX)
        val candidateX = if (panelOnRight) anchor.x else anchor.x - panelWidth - gapPx
        val rootX = candidateX.coerceIn(minX, maxX)
        val minY = area.top
        val maxY = (area.bottom - rootHeight).coerceAtLeast(minY)
        val candidateY = anchor.y + (ballSizePx - panelHeight) / 2
        val rootY = candidateY.coerceIn(minY, maxY)
        val ballLeft = if (panelOnRight) 0 else panelWidth + gapPx
        val ballTop = (anchor.y - rootY).coerceIn(0, rootHeight - ballSizePx)
        val params = requireNotNull(layout)
        params.width = rootWidth
        params.height = rootHeight
        params.x = rootX
        params.y = rootY
        panel.layoutParams = FrameLayout.LayoutParams(panelWidth, panelHeight).apply {
            leftMargin = if (panelOnRight) ballSizePx + gapPx else 0
            topMargin = 0
        }
        icon.layoutParams = FrameLayout.LayoutParams(ballSizePx, ballSizePx).apply {
            leftMargin = ballLeft
            topMargin = ballTop
        }
    }

    private fun currentBallPosition(): Point {
        val params = layout ?: return savedBallPosition()
        return if (expanded) {
            val iconParams = icon.layoutParams as? FrameLayout.LayoutParams
            Point(params.x + (iconParams?.leftMargin ?: 0), params.y + (iconParams?.topMargin ?: 0))
        } else {
            Point(params.x, params.y)
        }
    }

    private fun savedBallPosition(): Point {
        val area = safeArea()
        val maxX = (area.right - area.left - ballSizePx).coerceAtLeast(0)
        val maxY = (area.bottom - area.top - ballSizePx).coerceAtLeast(0)
        val position = preferences.position()
        return Point(
            area.left + (maxX * position.first).roundToInt(),
            area.top + (maxY * position.second).roundToInt(),
        )
    }

    private fun savePosition(position: Point) {
        val area = safeArea()
        val maxX = (area.right - area.left - ballSizePx).coerceAtLeast(0)
        val maxY = (area.bottom - area.top - ballSizePx).coerceAtLeast(0)
        preferences.setPosition(
            if (maxX > 0) (position.x - area.left).coerceIn(0, maxX).toFloat() / maxX else 0f,
            if (maxY > 0) (position.y - area.top).coerceIn(0, maxY).toFloat() / maxY else 0f,
        )
    }

    private fun updateWindowLayout() {
        val current = view ?: return
        runCatching { manager.updateViewLayout(current, requireNotNull(layout)) }
    }

    @Suppress("DEPRECATION")
    private fun safeArea(): ScreenArea {
        if (Build.VERSION.SDK_INT >= 30) {
            val metrics = manager.currentWindowMetrics
            val insets = metrics.windowInsets.getInsetsIgnoringVisibility(
                WindowInsets.Type.systemBars() or WindowInsets.Type.displayCutout(),
            )
            return ScreenArea(
                metrics.bounds.left + insets.left,
                metrics.bounds.top + insets.top,
                metrics.bounds.right - insets.right,
                metrics.bounds.bottom - insets.bottom,
            )
        }
        val size = Point().also { manager.defaultDisplay.getSize(it) }
        val statusBar = dimension("status_bar_height")
        val navigationBar = dimension("navigation_bar_height")
        return ScreenArea(0, statusBar, size.x, (size.y - navigationBar).coerceAtLeast(statusBar))
    }

    private fun dimension(name: String): Int {
        val resource = windowContext.resources.getIdentifier(name, "dimen", "android")
        return if (resource == 0) 0 else windowContext.resources.getDimensionPixelSize(resource)
    }

    private fun dp(value: Int): Int =
        (value * windowContext.resources.displayMetrics.density).roundToInt().coerceAtLeast(1)

    private fun openMainActivity(openDetails: Boolean = false, requestStart: Boolean = false) {
        context.startActivity(
            Intent(context, MainActivity::class.java)
                .putExtra("open_details", openDetails)
                .putExtra("request_start", requestStart)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_REORDER_TO_FRONT),
        )
    }

    override fun close() {
        FloatingBallStateStore.setPanelExpanded(false)
        view?.let { runCatching { manager.removeViewImmediate(it) } }
        view = null
        layout = null
        expanded = false
    }

    private data class ScreenArea(val left: Int, val top: Int, val right: Int, val bottom: Int)

    private class OverlayRoot(
        context: Context,
        private val onOutside: () -> Unit,
        private val isExpanded: () -> Boolean,
    ) : FrameLayout(context) {
        override fun dispatchTouchEvent(event: MotionEvent): Boolean {
            if (event.actionMasked == MotionEvent.ACTION_OUTSIDE) {
                if (isExpanded()) onOutside()
                return true
            }
            return super.dispatchTouchEvent(event)
        }

        override fun onTouchEvent(event: MotionEvent): Boolean {
            if (event.actionMasked == MotionEvent.ACTION_DOWN && isExpanded()) onOutside()
            return true
        }
    }
}
