package dev.hole.app.ui

import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.spring
import androidx.compose.foundation.background
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.border
import androidx.compose.foundation.gestures.detectHorizontalDragGestures
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.selection.selectableGroup
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.luminance
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.rememberTextMeasurer
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.dp
import kotlin.math.roundToInt

/** Equal-width segments fill their container; oversized text makes the whole track scroll. */
@Composable
fun LiquidSelection(
    labels: List<String>, selectedIndex: Int, onSelect: (Int) -> Unit,
    modifier: Modifier = Modifier, icons: List<ImageVector>? = null,
) {
    if (labels.isEmpty()) return
    val density = LocalDensity.current
    val textStyle = if (icons == null) MaterialTheme.typography.labelLarge else MaterialTheme.typography.labelSmall
    val measurer = rememberTextMeasurer()
    val minimum = with(density) { (labels.maxOf { measurer.measure(it, textStyle).size.width }.toDp() + if (icons == null) 28.dp else 16.dp).coerceAtLeast(48.dp) }
    val height = if (icons == null) 48.dp else 60.dp
    val colors = MaterialTheme.colorScheme
    val dark = colors.background.luminance() < .4f
    val select by rememberUpdatedState(onSelect)
    var dragging by remember { mutableStateOf(false) }
    var finger by remember { mutableFloatStateOf(0f) }
    val target = if (dragging) finger else selectedIndex.coerceIn(labels.indices).toFloat()
    // FlClash CommonTabBar: mass 1, stiffness 503.551, damping 44.8799.
    val animated by animateFloatAsState(target, spring(dampingRatio = 1f, stiffness = 503.551f), label = "selection position")
    val lift by animateFloatAsState(if (dragging) .95f else 1f, spring(dampingRatio = 1f, stiffness = 503.551f), label = "selection lift")
    BoxWithConstraints(modifier.fillMaxWidth()) {
        val width = maxOf(maxWidth, minimum * labels.size)
        val extent = with(density) { (width / labels.size).toPx() }
        val scrollable = width > maxWidth
        Box(Modifier.horizontalScroll(rememberScrollState()).selectableGroup()) {
            Box(Modifier.width(width).height(height).then(if (scrollable) Modifier else Modifier.pointerInput(extent, labels.size) {
                detectHorizontalDragGestures(
                    onDragStart = { position -> dragging = true; finger = (position.x / extent - .5f).coerceIn(0f, labels.lastIndex.toFloat()) },
                    onDragEnd = { select(finger.roundToInt().coerceIn(labels.indices)); dragging = false },
                    onDragCancel = { dragging = false },
                    onHorizontalDrag = { change, _ -> change.consume(); finger = (change.position.x / extent - .5f).coerceIn(0f, labels.lastIndex.toFloat()) },
                )
            }).testTag(if (icons == null) "segmented-control" else "navigation-track")) {
                val position = if (dragging) finger else animated
                val lensModifier = Modifier.offset { IntOffset((position * extent).roundToInt(), 0) }.width(width / labels.size).fillMaxHeight()
                    .padding(2.dp).graphicsLayer { scaleX = lift; scaleY = lift }.testTag("selection-lens")
                Box(lensModifier
                    .shadow(2.dp, RoundedCornerShape(8.dp), clip = false)
                    .clip(RoundedCornerShape(8.dp)).background(Brush.verticalGradient(listOf(
                        if (dark) Color.White.copy(alpha = .24f) else Color.White.copy(alpha = .98f),
                        if (dark) colors.primary.copy(alpha = .24f) else colors.primaryContainer.copy(alpha = .50f),
                    ))).border(.5.dp, Color.White.copy(alpha = if (dark) .12f else .8f), RoundedCornerShape(8.dp))
                )
                Row(Modifier.fillMaxSize()) {
                    labels.forEachIndexed { index, label ->
                        val active = index == if (dragging) finger.roundToInt() else selectedIndex
                        Column(
                            Modifier.weight(1f).fillMaxHeight().graphicsLayer { scaleX = if (active) lift else 1f; scaleY = if (active) lift else 1f }.clip(CircleShape)
                                .selectable(index == selectedIndex, interactionSource = remember { MutableInteractionSource() }, indication = null, role = if (icons == null) Role.RadioButton else Role.Tab, onClick = { select(index) }),
                            horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.Center,
                        ) {
                            val foreground = if (active) colors.primary else colors.onSurfaceVariant
                            if (icons != null) Icon(icons[index], null, Modifier.size(22.dp), tint = foreground)
                            Text(label, style = textStyle, color = foreground, maxLines = 1, softWrap = false)
                        }
                    }
                }
            }
        }
    }
}
