package dev.hole.app.ui

import androidx.compose.foundation.background
import androidx.compose.animation.*
import androidx.compose.animation.core.tween
import androidx.compose.animation.core.MutableTransitionState
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.SnackbarHostState
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.dropShadow
import androidx.compose.ui.graphics.*
import androidx.compose.ui.graphics.shadow.Shadow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.DpOffset
import dev.hole.corebridge.CoreSnapshot

val LocalFloatingInset = staticCompositionLocalOf { 0.dp }

@Composable
fun FloatingAppFrame(
    route: String, onNavigate: (String) -> Unit,
    snapshot: CoreSnapshot, enabled: Boolean, onToggleRun: (Boolean) -> Unit,
    snackbarHostState: SnackbarHostState? = null,
    content: @Composable () -> Unit,
) {
    val floating = route in primaryRoutes
    val bottomInset = if (floating) 100.dp + WindowInsets.navigationBars.asPaddingValues().calculateBottomPadding() else 0.dp
    Box(Modifier.fillMaxSize().background(MaterialTheme.colorScheme.background)) {
        CompositionLocalProvider(LocalFloatingInset provides bottomInset) { content() }
        if (floating) Row(
            Modifier.align(Alignment.BottomCenter).widthIn(max = 560.dp).fillMaxWidth()
                .navigationBarsPadding().padding(horizontal = 21.dp, vertical = 14.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Box(Modifier.weight(1f)) { AppNavigation(route, onNavigate) }
            val runSlot = remember { MutableTransitionState(false) }.apply { targetState = route == "home" }
            AnimatedVisibility(
                visibleState = runSlot,
                enter = expandHorizontally(tween(420), expandFrom = Alignment.End, clip = false) + fadeIn(tween(220)) + scaleIn(tween(320), initialScale = .5f),
                exit = shrinkHorizontally(tween(260), shrinkTowards = Alignment.End, clip = false) + fadeOut(tween(160)) + scaleOut(tween(220), targetScale = .7f),
                label = "home run button slot",
            ) {
                Box(Modifier.padding(start = 8.dp)) {
                    FloatingRunControl(snapshot, enabled && route == "home", onToggleRun, docked = true)
                }
            }
        }
        if (snackbarHostState != null) {
            HoleSnackbarHost(
                snackbarHostState,
                Modifier.align(Alignment.BottomCenter).widthIn(max = 560.dp).fillMaxWidth()
                    .imePadding().navigationBarsPadding().padding(bottom = if (floating) 100.dp else 0.dp),
            )
        }
    }
}

@Composable
fun Modifier.dockShadow(): Modifier {
    val scheme = MaterialTheme.colorScheme
    val strength = if (scheme.background.luminance() < .4f) 3f else 1f
    return dropShadow(CircleShape, Shadow(radius = 24.dp, color = Color.Black.copy(alpha = .08f * strength), offset = DpOffset(0.dp, 8.dp)))
        .dropShadow(CircleShape, Shadow(radius = 3.dp, color = Color.Black.copy(alpha = .04f * strength), offset = DpOffset(0.dp, 1.dp)))
}

@Composable
fun FloatingSurface(modifier: Modifier = Modifier, content: @Composable BoxScope.() -> Unit) {
    Box(modifier.dockShadow().clip(CircleShape).background(MaterialTheme.colorScheme.surfaceContainer), content = content)
}

@Composable
fun RaisedPanel(modifier: Modifier = Modifier, color: Color = MaterialTheme.colorScheme.surfaceContainerLow, content: @Composable ColumnScope.() -> Unit) {
    val shape = MaterialTheme.shapes.extraLarge
    val dark = MaterialTheme.colorScheme.background.luminance() < .4f
    Column(modifier.dropShadow(shape, Shadow(radius = 10.dp, color = Color(0xFF071B3C).copy(alpha = if (dark) .28f else .08f), offset = DpOffset(0.dp, 4.dp)))
        .clip(shape).background(Brush.linearGradient(listOf(
            if (dark) lerp(color, Color.White, .045f) else lerp(color, Color.White, .70f), color,
        ))).border(.8.dp, Brush.linearGradient(listOf(Color.White.copy(alpha = if (dark) .16f else .95f), Color.Transparent)), shape), content = content)
}
