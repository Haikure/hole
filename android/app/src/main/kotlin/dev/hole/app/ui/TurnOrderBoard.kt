package dev.hole.app.ui

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectDragGesturesAfterLongPress
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.boundsInRoot
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.layout.positionInRoot
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.dp
import androidx.compose.ui.zIndex
import dev.hole.app.config.ThemeStyle
import kotlin.math.abs
import kotlin.math.roundToInt
import top.yukonga.miuix.kmp.theme.MiuixTheme

/** token 顺序即默认顺序，与 core/ice_config.go 的 turn.order 校验保持一致。 */
internal val turnOrderTypes = listOf("udp", "tcp_80", "tcp", "tls_443", "tls")

internal fun turnOrderTypeLabel(token: String): String = when (token) {
    "udp" -> "UDP"
    "tcp_80" -> "TCP 80"
    "tcp" -> "TCP 3478"
    "tls_443" -> "TLS 443"
    "tls" -> "TLS 5349"
    else -> token
}

/** 按落点处的既有 chip 布局计算插入下标：同行比左右，跨行比上下。 */
internal fun turnOrderInsertIndex(selected: List<String>, dragged: String, bounds: Map<String, Rect>, point: Offset, rowSlack: Float): Int {
    var index = 0
    for (token in selected) {
        if (token == dragged) continue
        val center = bounds[token]?.center ?: continue
        if (center.y < point.y - rowSlack || (abs(center.y - point.y) <= rowSlack && center.x < point.x)) index++
    }
    return index
}

private data class TurnDragSession(
    val token: String,
    val order: List<String>,
    val bounds: Map<String, Rect>,
    val dividerY: Float,
    val point: Offset,
    val rowSlack: Float,
    val insertIndex: Int,
)

/**
 * 分隔线上方为启用顺序，下方为未启用类型：点按在两侧之间移动，
 * 长按拖动并按落点插入。拖动中保持列表布局稳定，松手时一次性提交。
 */
@OptIn(ExperimentalLayoutApi::class)
@Composable
fun TurnOrderBoard(
    order: List<String>,
    onChange: (List<String>) -> Unit,
    modifier: Modifier = Modifier,
    onDraggingChanged: (Boolean) -> Unit = {},
) {
    var bounds by remember { mutableStateOf(mapOf<String, Rect>()) }
    var origin by remember { mutableStateOf(Offset.Zero) }
    var dividerY by remember { mutableStateOf(Float.MAX_VALUE) }
    var dragSession by remember { mutableStateOf<TurnDragSession?>(null) }
    val currentOrder = rememberUpdatedState(order)
    val currentOnChange = rememberUpdatedState(onChange)
    val currentOnDraggingChanged = rememberUpdatedState(onDraggingChanged)
    val haptics = LocalHapticFeedback.current
    val density = LocalDensity.current
    val miuix = LocalThemeStyle.current == ThemeStyle.MIUIX
    val materialColors = MaterialTheme.colorScheme
    val accent = if (miuix) MiuixTheme.colorScheme.primary else materialColors.primary
    val muted = if (miuix) MiuixTheme.colorScheme.onSurfaceSecondary else materialColors.onSurfaceVariant
    val dividerColor = if (miuix) MiuixTheme.colorScheme.dividerLine else materialColors.outlineVariant
    val idleContainer = if (miuix) MiuixTheme.colorScheme.surfaceContainer else materialColors.surfaceContainerLow
    val selectedContainer = if (miuix) accent.copy(alpha = 0.12f) else materialColors.secondaryContainer
    val chipShape = if (miuix) RoundedCornerShape(14.dp) else MaterialTheme.shapes.medium

    fun chipBounds(token: String) = Modifier.onGloballyPositioned {
        val rect = it.boundsInRoot()
        if (bounds[token] != rect) bounds = bounds + (token to rect)
    }

    fun dragModifier(token: String) = Modifier.pointerInput(token) {
        detectDragGesturesAfterLongPress(
            onDragStart = { offset ->
                haptics.performHapticFeedback(HapticFeedbackType.LongPress)
                val snapshot = bounds.toMap()
                val tokenBounds = snapshot[token]
                val point = tokenBounds?.center ?: origin + offset
                val base = currentOrder.value.filterNot { it == token }
                val rowSlack = tokenBounds?.height?.times(0.5f) ?: with(density) { 20.dp.toPx() }
                dragSession = TurnDragSession(
                    token = token,
                    order = currentOrder.value.toList(),
                    bounds = snapshot,
                    dividerY = dividerY,
                    point = point,
                    rowSlack = rowSlack,
                    insertIndex = turnOrderInsertIndex(base, token, snapshot, point, rowSlack),
                )
                currentOnDraggingChanged.value(true)
            },
            onDrag = { change, amount ->
                val session = dragSession
                if (session?.token == token) {
                    val point = session.point + amount
                    val base = session.order.filterNot { it == token }
                    val index = turnOrderInsertIndex(base, token, session.bounds, point, session.rowSlack)
                    dragSession = session.copy(point = point, insertIndex = index)
                    change.consume()
                }
            },
            onDragEnd = {
                val session = dragSession
                if (session?.token == token) {
                    val base = session.order.filterNot { it == token }
                    val next = if (session.point.y < session.dividerY) {
                        base.toMutableList().apply { add(session.insertIndex.coerceIn(0, size), token) }
                    } else {
                        base
                    }
                    if (next != session.order) currentOnChange.value(next)
                    dragSession = null
                    currentOnDraggingChanged.value(false)
                }
            },
            onDragCancel = {
                if (dragSession?.token == token) {
                    dragSession = null
                    currentOnDraggingChanged.value(false)
                }
            },
        )
    }

    val pool = turnOrderTypes.filterNot { it in order }

    Box(modifier.fillMaxWidth().onGloballyPositioned { origin = it.positionInRoot() }) {
        Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                Text("尝试顺序", style = MaterialTheme.typography.labelLarge)
                Spacer(Modifier.weight(1f))
                Text(if (order.isEmpty()) "默认顺序" else "${order.size} 种", style = MaterialTheme.typography.labelMedium, color = muted)
            }
            FlowRow(
                Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.spacedBy(8.dp),
                verticalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                order.forEachIndexed { index, token ->
                    TurnOrderChip(
                        token = token,
                        badge = index + 1,
                        dimmed = dragSession?.token == token,
                        background = selectedContainer,
                        foreground = materialColors.onSurface,
                        accent = accent,
                        muted = muted,
                        borderColor = accent.copy(alpha = 0.55f),
                        shape = chipShape,
                        modifier = chipBounds(token).then(dragModifier(token)),
                        onClickLabel = "移除 ${turnOrderTypeLabel(token)}",
                        onClick = { if (dragSession == null) currentOnChange.value(order - token) },
                    )
                }
                if (order.isEmpty()) {
                    Text(
                        "留空使用默认顺序；点按下方类型可添加",
                        style = MaterialTheme.typography.bodySmall,
                        color = muted,
                        modifier = Modifier.padding(vertical = 10.dp),
                    )
                }
            }
            Box(Modifier.fillMaxWidth().onGloballyPositioned { dividerY = it.boundsInRoot().center.y }) {
                HorizontalDivider(color = dividerColor)
            }
            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                Text("可添加", style = MaterialTheme.typography.labelLarge)
                Spacer(Modifier.weight(1f))
                Text("${pool.size} 种", style = MaterialTheme.typography.labelMedium, color = muted)
            }
            FlowRow(
                Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.spacedBy(8.dp),
                verticalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                pool.forEach { token ->
                    TurnOrderChip(
                        token = token,
                        badge = null,
                        dimmed = dragSession?.token == token,
                        background = idleContainer,
                        foreground = materialColors.onSurfaceVariant,
                        accent = accent,
                        muted = muted,
                        borderColor = dividerColor,
                        shape = chipShape,
                        modifier = chipBounds(token).then(dragModifier(token)),
                        onClickLabel = "启用 ${turnOrderTypeLabel(token)}",
                        onClick = { if (dragSession == null) currentOnChange.value(order + token) },
                    )
                }
            }
            Text(
                "长按拖动可调整顺序或移到上方启用区；点按可启用或停用。",
                style = MaterialTheme.typography.bodySmall,
                color = muted,
            )
        }

        dragSession?.let { session ->
            val base = session.order.filterNot { it == session.token }
            if (session.point.y < session.dividerY) {
                val target = base.getOrNull(session.insertIndex)
                val targetBounds = target?.let { session.bounds[it] }
                    ?: base.lastOrNull()?.let { session.bounds[it] }
                if (targetBounds != null) {
                    val markerX = if (target != null) targetBounds.left else targetBounds.right
                    val markerHeight = with(density) { targetBounds.height.toDp() }
                    Box(
                        Modifier
                            .zIndex(1f)
                            .offset {
                                IntOffset(
                                    (markerX - origin.x - 1.5.dp.toPx()).roundToInt(),
                                    (targetBounds.top - origin.y).roundToInt(),
                                )
                            }
                            .width(3.dp)
                            .height(markerHeight)
                            .clip(CircleShape)
                            .background(accent)
                            .graphicsLayer { alpha = 0.9f },
                    )
                }
            }
            TurnOrderChip(
                token = session.token,
                badge = if (session.point.y < session.dividerY) session.insertIndex + 1 else null,
                dimmed = false,
                background = selectedContainer,
                foreground = materialColors.onSurface,
                accent = accent,
                muted = muted,
                borderColor = accent,
                shape = chipShape,
                elevated = true,
                modifier = Modifier
                    .zIndex(2f)
                    .offset {
                        val size = bounds[session.token]?.size ?: return@offset IntOffset.Zero
                        IntOffset(
                            (session.point.x - origin.x - size.width / 2).roundToInt(),
                            (session.point.y - origin.y - size.height / 2).roundToInt(),
                        )
                    }
                    .clearAndSetSemantics {},
            )
        }
    }
}

@Composable
private fun TurnOrderChip(
    token: String,
    badge: Int?,
    dimmed: Boolean,
    background: Color,
    foreground: Color,
    accent: Color,
    muted: Color,
    borderColor: Color,
    shape: androidx.compose.ui.graphics.Shape,
    modifier: Modifier = Modifier,
    elevated: Boolean = false,
    onClickLabel: String? = null,
    onClick: () -> Unit = {},
) {
    Surface(
        shape = shape,
        color = background,
        contentColor = foreground,
        border = BorderStroke(if (elevated) 1.5.dp else 1.dp, borderColor),
        shadowElevation = if (elevated) 8.dp else 0.dp,
        modifier = modifier
            .graphicsLayer { alpha = if (dimmed) 0.35f else 1f }
            .clickable(enabled = !elevated, onClickLabel = onClickLabel, onClick = onClick),
    ) {
        Row(
            Modifier.padding(horizontal = 10.dp, vertical = 9.dp),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            if (badge != null) {
                Box(
                    Modifier.size(21.dp).background(accent, CircleShape),
                    contentAlignment = Alignment.Center,
                ) {
                    Text("$badge", style = MaterialTheme.typography.labelSmall, color = if (LocalThemeStyle.current == ThemeStyle.MIUIX) MiuixTheme.colorScheme.onPrimary else MaterialTheme.colorScheme.onPrimary)
                }
            } else {
                Text("+", style = MaterialTheme.typography.titleMedium, color = accent)
            }
            Text(turnOrderTypeLabel(token), style = MaterialTheme.typography.labelLarge)
            DragGrip(color = muted)
        }
    }
}

@Composable
private fun DragGrip(color: Color) {
    Canvas(
        Modifier
            .size(width = 12.dp, height = 18.dp)
            .clearAndSetSemantics {},
    ) {
        drawGrip(color)
    }
}

private fun DrawScope.drawGrip(color: Color) {
    val radius = 1.25.dp.toPx()
    val columnGap = 4.dp.toPx()
    val rowGap = 5.dp.toPx()
    val firstX = (size.width - columnGap) / 2
    val firstY = (size.height - rowGap * 2) / 2
    repeat(3) { row ->
        drawCircle(color, radius, Offset(firstX, firstY + row * rowGap))
        drawCircle(color, radius, Offset(firstX + columnGap, firstY + row * rowGap))
    }
}
