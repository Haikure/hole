package dev.hole.app.ui

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.animateContentSize
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.spring
import androidx.compose.animation.expandHorizontally
import androidx.compose.animation.shrinkHorizontally
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.*
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.*
import androidx.compose.ui.unit.dp
import dev.hole.corebridge.CoreSnapshot
import java.time.Duration
import java.time.Instant
import kotlinx.coroutines.delay

@Composable
fun FloatingRunControl(snapshot: CoreSnapshot, enabled: Boolean, onToggleRun: (Boolean) -> Unit, docked: Boolean = false) {
    val running = snapshot.runRequested
    val interaction = remember { MutableInteractionSource() }
    val scale = pressScale(interaction, 1.25f)
    val shape by animateFloatAsState(if (running) 1f else 0f, spring(dampingRatio = .65f), label = "play pause")
    val elapsed by produceState("00:00:00", snapshot.startedAt, running) {
        while (running) {
            val seconds = runCatching { Duration.between(Instant.parse(snapshot.startedAt), Instant.now()).seconds.coerceAtLeast(0) }.getOrDefault(0)
            value = "%02d:%02d:%02d".format(seconds / 3600, seconds / 60 % 60, seconds % 60)
            delay(1000)
        }
    }
    val ink = MaterialTheme.colorScheme.primary
    FloatingSurface(Modifier.graphicsLayer { scaleX = scale; scaleY = scale }.testTag("run-control").semantics {
        contentDescription = if (running) "停止转发" else "启动转发"
        stateDescription = engineLabel(snapshot)
    }) {
        Row(
            Modifier.heightIn(min = 58.dp).animateContentSize(spring(dampingRatio = .75f))
                .clickable(interactionSource = interaction, indication = null, enabled = enabled, role = Role.Button, onClick = { onToggleRun(!running) })
                .padding(horizontal = 18.dp, vertical = 14.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Canvas(Modifier.size(26.dp)) {
                val t = shape.coerceIn(0f, 1f)
                val left = size.width * .20f
                val top = size.height * .16f
                val bottom = size.height * .84f
                val right = size.width * (.86f - .43f * t)
                drawPath(Path().apply {
                    moveTo(left, top)
                    lineTo(right, size.height * (.5f - .34f * t))
                    lineTo(right, size.height * (.5f + .34f * t))
                    lineTo(left, bottom)
                    close()
                }, ink.copy(alpha = if (enabled) 1f else .4f))
                drawRoundRect(ink.copy(alpha = t * if (enabled) 1f else .4f), Offset(size.width * .61f, top), Size(size.width * .22f, bottom - top), CornerRadius(2.dp.toPx()))
            }
            AnimatedVisibility(running && !docked, enter = expandHorizontally() + fadeIn(), exit = shrinkHorizontally() + fadeOut()) {
                Text(elapsed, Modifier.padding(start = 10.dp), color = ink,
                    style = MaterialTheme.typography.titleMedium.copy(fontFeatureSettings = "tnum"))
            }
        }
    }
}
