package dev.hole.app.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.selection.selectableGroup
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.draw.drawWithContent
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.RoundRect
import androidx.compose.ui.graphics.*
import androidx.compose.ui.graphics.drawscope.*
import androidx.compose.ui.graphics.layer.drawLayer
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.input.pointer.util.VelocityTracker
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlin.math.abs
import kotlin.math.min
import kotlin.math.roundToInt

/** Native rendering of FlClash's spring lens, including partial tinting as it crosses an item. */
@Composable
fun NavigationDock(labels: List<String>, icons: List<ImageVector>, selected: Int, onSelect: (Int) -> Unit) {
    val lens = remember { DockSpring(selected.toFloat()) }
    val lift = remember { DockSpring(0f) }
    val swell = remember { DockSpring(0f) }
    val pull = remember { DockSpring(0f) }
    var pressed by remember { mutableStateOf(false) }
    val activate by rememberUpdatedState(onSelect)
    val current by rememberUpdatedState(selected)
    RunDockSprings(lens, lift, swell, pull)
    LaunchedEffect(selected, pressed) { if (!pressed) lens.to(selected.toFloat()) }
    val density = LocalDensity.current
    val haptic = LocalHapticFeedback.current
    val colors = MaterialTheme.colorScheme
    val layer = rememberGraphicsLayer()
    BoxWithConstraints(Modifier.fillMaxWidth().height(62.dp).drawWithContent {
        val growth = 1 + swell.value * min(.25f, 16.dp.toPx() / size.maxDimension)
        val x = growth * (1 + abs(pull.value) / size.width * .5f)
        withTransform({ translate(pull.value, 0f); scale(x, growth, center) }) { this@drawWithContent.drawContent() }
    }) {
        val extent = with(density) { (maxWidth - 8.dp).toPx() / labels.size }
        val barWidth = with(density) { maxWidth.toPx() }
        fun position(x: Float): Float {
            val raw = (x - with(density) { 4.dp.toPx() }) / extent - .5f
            return when {
                raw < 0 -> rubberBand(raw, .35f)
                raw > labels.lastIndex -> labels.lastIndex + rubberBand(raw - labels.lastIndex, .35f)
                else -> raw
            }
        }
        Box(Modifier.fillMaxSize().dockShadow().background(colors.surfaceContainer, CircleShape)
            .pointerInput(extent, labels.size) {
                awaitEachGesture {
                    val down = awaitFirstDown(requireUnconsumed = false)
                    down.consume()
                    val tracker = VelocityTracker()
                    tracker.addPosition(down.uptimeMillis, down.position)
                    var index = position(down.position.x).roundToInt().coerceIn(labels.indices)
                    var dragging = false
                    var commit = false
                    pressed = true
                    lift.to(1f, .28f, .2f)
                    swell.to(1f, .28f, .2f)
                    lens.to(index.toFloat())
                    try {
                        while (true) {
                            val event = awaitPointerEvent()
                            val change = event.changes.firstOrNull { it.id == down.id } ?: break
                            if (change.isConsumed) break
                            tracker.addPosition(change.uptimeMillis, change.position)
                            if (!change.pressed) { commit = true; change.consume(); break }
                            if (abs(change.position.x - down.position.x) >= viewConfiguration.touchSlop) dragging = true
                            if (dragging) {
                                val overshoot = change.position.x - change.position.x.coerceIn(0f, barWidth)
                                pull.to(rubberBand(overshoot, size.height * 7f / 32f), .12f, 0f)
                                val at = position(change.position.x)
                                lens.to(at, .12f, 0f)
                                val next = at.roundToInt().coerceIn(labels.indices)
                                if (next != index) { haptic.performHapticFeedback(HapticFeedbackType.TextHandleMove); index = next }
                            }
                            change.consume()
                        }
                    } finally {
                        if (commit && dragging) index = dockFlingIndex(lens.target, tracker.calculateVelocity().x, extent, index, labels.lastIndex)
                        lift.to(0f); swell.to(0f); pull.to(0f)
                        lens.to(if (commit) index.toFloat() else current.toFloat())
                        if (commit && index != current) activate(index)
                        pressed = false
                    }
                }
            }) {
            Box(Modifier.fillMaxSize().padding(4.dp).testTag("navigation-track").selectableGroup()
                .drawBehind {
                    val rect = dockLensBounds(lens.value, lens.velocity, extent, size.height, lift.value, density.density)
                    val fill = colors.onSecondaryContainer.copy(alpha = .08f * lift.value.coerceIn(0f, 1f)).compositeOver(colors.secondaryContainer)
                    drawRoundRect(fill, rect.topLeft, rect.size, CornerRadius(rect.height / 2))
                }) {
                Row(Modifier.fillMaxSize().drawWithContent {
                    layer.record { this@drawWithContent.drawContent() }
                    val rect = dockLensBounds(lens.value, lens.velocity, extent, size.height, lift.value, density.density)
                    val path = Path().apply { addRoundRect(RoundRect(rect, CornerRadius(rect.height / 2))) }
                    clipPath(path, ClipOp.Difference) { drawLayer(layer) }
                    clipPath(path) {
                        val paint = Paint().apply { colorFilter = ColorFilter.tint(colors.primary, BlendMode.SrcIn) }
                        drawIntoCanvas { it.saveLayer(androidx.compose.ui.geometry.Rect(Offset.Zero, size), paint) }
                        drawLayer(layer)
                        drawIntoCanvas { it.restore() }
                    }
                }) {
                    labels.forEachIndexed { index, label ->
                        Column(Modifier.weight(1f).fillMaxHeight().selectable(index == selected, interactionSource = remember { MutableInteractionSource() }, indication = null, role = Role.Tab, onClick = { activate(index) })
                            .drawWithContent {
                                val emphasis = (1 - abs(lens.value - index)).coerceIn(0f, 1f)
                                scale(1 + .12f * emphasis * lift.value, pivot = center) { this@drawWithContent.drawContent() }
                            },
                            verticalArrangement = Arrangement.Center, horizontalAlignment = Alignment.CenterHorizontally) {
                            Icon(icons[index], null, Modifier.size(24.dp), tint = colors.onSurfaceVariant)
                            Spacer(Modifier.height(2.dp))
                            Text(label, color = colors.onSurfaceVariant, style = MaterialTheme.typography.labelSmall.copy(fontSize = 10.sp), maxLines = 1)
                        }
                    }
                }
            }
        }
    }
}
