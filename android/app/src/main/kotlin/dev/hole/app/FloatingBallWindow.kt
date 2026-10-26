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
import android.view.ViewConfiguration
import android.view.WindowInsets
import android.view.WindowManager
import android.widget.ImageView
import kotlin.math.roundToInt

/** Owned only by the service, so leaving the Activity does not remove the window. */
internal class FloatingBallWindow(private val context: Context, private val preferences: FloatingBallPreferences) : AutoCloseable {
    private val windowContext = if (Build.VERSION.SDK_INT >= 30) {
        val display = context.getSystemService(DisplayManager::class.java).getDisplay(Display.DEFAULT_DISPLAY)
        context.createDisplayContext(display).createWindowContext(WindowManager.LayoutParams.TYPE_APPLICATION_OVERLAY, null)
    } else context
    private val manager = windowContext.getSystemService(WindowManager::class.java)
    var view: ImageView? = null
        private set
    private var layout: WindowManager.LayoutParams? = null
    private var downX = 0f
    private var downY = 0f
    private var initialX = 0
    private var initialY = 0
    private var dragging = false
    private val touchSlop = ViewConfiguration.get(windowContext).scaledTouchSlop

    fun show() {
        val size = (preferences.sizeDp() * windowContext.resources.displayMetrics.density).roundToInt()
        val params = layout ?: WindowManager.LayoutParams(
            size, size, WindowManager.LayoutParams.TYPE_APPLICATION_OVERLAY,
            WindowManager.LayoutParams.FLAG_NOT_FOCUSABLE or WindowManager.LayoutParams.FLAG_NOT_TOUCH_MODAL,
            PixelFormat.TRANSLUCENT,
        ).apply {
            @Suppress("RtlHardcoded")
            gravity = Gravity.TOP or Gravity.LEFT
            title = "hole 悬浮球"
            alpha = 0.95f
        }
        params.width = size
        params.height = size
        val (maxX, maxY) = limits(size)
        val (x, y) = preferences.position()
        params.x = (maxX * x).roundToInt()
        params.y = (maxY * y).roundToInt()
        val current = view
        if (current != null) {
            manager.updateViewLayout(current, params)
        } else {
            val icon = ImageView(windowContext).apply {
                setImageResource(R.drawable.ic_launcher)
                contentDescription = "hole 悬浮球，点按返回应用，拖动调整位置"
                background = GradientDrawable().apply { shape = GradientDrawable.OVAL; setColor(Color.WHITE) }
                clipToOutline = true
                setOnClickListener {
                    context.startActivity(Intent(context, MainActivity::class.java)
                        .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_REORDER_TO_FRONT))
                }
                setOnTouchListener { target, event ->
                    when (event.actionMasked) {
                        MotionEvent.ACTION_DOWN -> {
                            downX = event.rawX; downY = event.rawY
                            initialX = params.x; initialY = params.y
                            dragging = false
                            true
                        }
                        MotionEvent.ACTION_MOVE -> {
                            val dx = event.rawX - downX
                            val dy = event.rawY - downY
                            if (dx * dx + dy * dy > touchSlop * touchSlop) dragging = true
                            if (dragging) {
                                val (right, bottom) = limits(params.width)
                                params.x = (initialX + dx.roundToInt()).coerceIn(0, right)
                                params.y = (initialY + dy.roundToInt()).coerceIn(0, bottom)
                                manager.updateViewLayout(target, params)
                            }
                            true
                        }
                        MotionEvent.ACTION_UP, MotionEvent.ACTION_CANCEL -> {
                            if (dragging) {
                                val (right, bottom) = limits(params.width)
                                preferences.setPosition(
                                    if (right > 0) params.x.toFloat() / right else 0f,
                                    if (bottom > 0) params.y.toFloat() / bottom else 0f,
                                )
                            } else if (event.actionMasked == MotionEvent.ACTION_UP) target.performClick()
                            true
                        }
                        else -> false
                    }
                }
            }
            manager.addView(icon, params)
            view = icon
        }
        layout = params
    }

    @Suppress("DEPRECATION")
    private fun limits(size: Int): Pair<Int, Int> {
        val area = if (Build.VERSION.SDK_INT >= 30) {
            val metrics = manager.currentWindowMetrics
            val insets = metrics.windowInsets.getInsetsIgnoringVisibility(WindowInsets.Type.systemBars() or WindowInsets.Type.displayCutout())
            Point(metrics.bounds.width() - insets.left - insets.right, metrics.bounds.height() - insets.top - insets.bottom)
        } else {
            Point().also { manager.defaultDisplay.getSize(it) }.apply {
                y -= view?.rootWindowInsets?.systemWindowInsetTop ?: 0
            }
        }
        return (area.x - size).coerceAtLeast(0) to (area.y - size).coerceAtLeast(0)
    }

    override fun close() {
        view?.let { runCatching { manager.removeViewImmediate(it) } }
        view = null
        layout = null
    }
}
