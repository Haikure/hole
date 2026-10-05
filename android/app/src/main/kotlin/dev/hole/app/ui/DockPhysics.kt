package dev.hole.app.ui

import androidx.compose.runtime.*
import androidx.compose.ui.MotionDurationScale
import androidx.compose.ui.geometry.Rect
import kotlinx.coroutines.flow.collectLatest
import kotlin.coroutines.coroutineContext
import kotlin.math.*

// Motion equations and parameters follow chen08209/FlClash's navigation_dock.dart:
// https://github.com/chen08209/FlClash/blob/main/lib/widgets/navigation_dock.dart
// The spring is retargeted without resetting its frame clock or momentum.
internal class DockSpring(initial: Float) {
    var value by mutableFloatStateOf(initial)
        private set
    var velocity by mutableFloatStateOf(0f)
        private set
    var target by mutableFloatStateOf(initial)
        private set
    private var duration = .5f
    private var damping = .68f
    val active get() = value != target || velocity != 0f

    fun to(target: Float, duration: Float = .5f, bounce: Float = .32f) {
        this.duration = duration
        damping = 1f - bounce
        this.target = target
    }

    fun snap() { value = target; velocity = 0f }

    fun step(dt: Float) {
        val omega = (2 * PI / duration).toFloat()
        val offset = value - target
        val decay = exp(-damping * omega * dt)
        val next: Float
        val speed: Float
        if (damping >= .999f) {
            val c = velocity + omega * offset
            next = (offset + c * dt) * decay
            speed = (velocity - omega * c * dt) * decay
        } else {
            val wd = omega * sqrt(1 - damping * damping)
            val cosine = cos(wd * dt)
            val sine = sin(wd * dt)
            next = decay * (offset * cosine + (velocity + damping * omega * offset) / wd * sine)
            speed = decay * (velocity * cosine - (damping * omega * velocity + omega * omega * offset) / wd * sine)
        }
        value = target + next
        velocity = speed
        if (abs(next) < .001f && abs(speed) < .001f) snap()
    }
}

@Composable
internal fun RunDockSprings(vararg springs: DockSpring) {
    LaunchedEffect(*springs) {
        snapshotFlow { springs.any { it.active } }.collectLatest { active ->
            if (active) {
                var previous = withFrameNanos { it }
                while (springs.any { it.active }) {
                    val scale = coroutineContext[MotionDurationScale]?.scaleFactor ?: 1f
                    withFrameNanos { now ->
                        if (scale <= 0f) springs.forEach { it.snap() }
                        else springs.forEach { it.step(((now - previous) / 1e9f / scale).coerceAtMost(.064f)) }
                        previous = now
                    }
                }
            }
        }
    }
}

internal fun rubberBand(overshoot: Float, limit: Float): Float =
    limit * (1 - 1 / (abs(overshoot) * .55f / limit + 1)) * sign(overshoot)

internal fun dockLensBounds(position: Float, velocity: Float, extent: Float, height: Float, lift: Float, density: Float): Rect {
    val stretch = (abs(velocity) / 8f).coerceIn(0f, 1f) * .25f
    val growth = 28f * density * lift
    val width = (extent + growth) * (1 + stretch)
    val lensHeight = (height + growth) * (1 - stretch / 2)
    val x = (position + .5f) * extent
    return Rect(x - width / 2, (height - lensHeight) / 2, x + width / 2, (height + lensHeight) / 2)
}

internal fun dockFlingIndex(target: Float, velocity: Float, extent: Float, pressed: Int, last: Int): Int =
    (target + velocity / extent * .1f).roundToInt().coerceIn(pressed - 1, pressed + 1).coerceIn(0, last)
