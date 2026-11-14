package dev.hole.app.ui

import android.content.res.Configuration
import androidx.activity.compose.BackHandler
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.slideInVertically
import androidx.compose.animation.slideOutVertically
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.spring
import androidx.compose.animation.core.tween
import androidx.compose.animation.core.LinearOutSlowInEasing
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.background
import androidx.compose.foundation.focusable
import androidx.compose.foundation.gestures.ScrollableState
import androidx.compose.foundation.gestures.detectVerticalDragGestures
import androidx.compose.foundation.gestures.animateScrollBy
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.ScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.input.rotary.onPreRotaryScrollEvent
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalViewConfiguration
import androidx.compose.ui.platform.ViewConfiguration
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.Density
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.DpSize
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import dev.hole.corebridge.CoreSnapshot
import kotlinx.coroutines.channels.Channel

/** Runtime device detection also covers the shared UI hosted outside MainActivity. */
@Composable
internal fun isWearUi(): Boolean {
    val configuration = LocalConfiguration.current
    return configuration.uiMode and Configuration.UI_MODE_TYPE_MASK == Configuration.UI_MODE_TYPE_WATCH || configuration.isScreenRound
}

private const val WearLayoutScale = .8f
private val LocalWearWindowDensity = staticCompositionLocalOf<Density?> { null }

@Composable
internal fun WearDisplayDensity(content: @Composable () -> Unit) {
    val windowDensity = LocalDensity.current
    val viewConfiguration = LocalViewConfiguration.current
    val touchConfiguration = remember(viewConfiguration) {
        object : ViewConfiguration by viewConfiguration {
            // Preserve the minimum hit area in screen pixels as the layout becomes denser.
            override val minimumTouchTargetSize: DpSize
                get() = viewConfiguration.minimumTouchTargetSize / WearLayoutScale
        }
    }
    CompositionLocalProvider(
        LocalWearWindowDensity provides windowDensity,
        LocalDensity provides Density(windowDensity.density * WearLayoutScale, windowDensity.fontScale),
        LocalViewConfiguration provides touchConfiguration,
        content = content,
    )
}

@Composable
internal fun screenSpacing(phone: Dp): Dp = if (isWearUi()) 8.dp else phone

internal data class WearPageChrome(
    val title: String,
    val navigation: @Composable () -> Unit,
    val actions: @Composable RowScope.() -> Unit,
    val footer: @Composable RowScope.() -> Unit,
)

internal val LocalWearPageChrome = staticCompositionLocalOf<WearPageChrome?> { null }
internal val LocalWearRotaryActive = compositionLocalOf { true }

/** One consumer per visible scroll surface; modal/drawer focus never scrolls the page below. */
@Composable
internal fun Modifier.wearRotary(state: ScrollableState, enabled: Boolean = true): Modifier {
    if (!isWearUi()) return this
    val active = LocalWearRotaryActive.current && enabled
    val focus = remember { FocusRequester() }
    val deltas = remember { Channel<Float>(Channel.UNLIMITED) }
    LaunchedEffect(state, active) {
        if (active) {
            // Wait for attachment, including a newly opened dialog or a restored page.
            withFrameNanos { }
            focus.requestFocus()
            for (first in deltas) {
                var delta = first
                while (true) delta += deltas.tryReceive().getOrNull() ?: break
                state.animateScrollBy(delta, tween(90, easing = LinearOutSlowInEasing))
            }
        } else {
            while (deltas.tryReceive().isSuccess) { /* discard stale input */ }
        }
    }
    return onPreRotaryScrollEvent {
        if (active) deltas.trySend(it.verticalScrollPixels).isSuccess else false
    }.focusRequester(focus).focusable(active)
}

@Composable
private fun WearPageHeader() {
    LocalWearPageChrome.current?.let { chrome ->
        Row(Modifier.fillMaxWidth().heightIn(min = 36.dp).padding(horizontal = 20.dp), verticalAlignment = Alignment.CenterVertically) {
            chrome.navigation()
            Text(chrome.title, Modifier.weight(1f).semantics { heading() },
                style = MaterialTheme.typography.titleMedium, textAlign = TextAlign.Center)
            chrome.actions(this)
        }
    }
}

@Composable
private fun WearPageFooter() {
    LocalWearPageChrome.current?.let { chrome ->
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(12.dp, Alignment.CenterHorizontally), content = chrome.footer)
    }
}

/** Retains the phone LazyColumn contract; round-screen margins scroll away with content. */
@Composable
internal fun HoleLazyColumn(
    modifier: Modifier = Modifier,
    contentPadding: PaddingValues = PaddingValues(0.dp),
    verticalArrangement: Arrangement.Vertical = Arrangement.Top,
    state: androidx.compose.foundation.lazy.LazyListState = rememberLazyListState(),
    content: LazyListScope.() -> Unit,
) {
    val wear = isWearUi()
    LazyColumn(
        modifier.wearRotary(state), state = state,
        contentPadding = if (wear) WearContentPadding else contentPadding,
        verticalArrangement = if (wear) Arrangement.spacedBy(8.dp) else verticalArrangement,
    ) {
        if (wear) item { WearPageHeader() }
        content()
        if (wear) item { WearPageFooter() }
    }
}

private val WearContentPadding = PaddingValues(start = 12.dp, end = 12.dp, top = 24.dp, bottom = 44.dp)

@Composable
internal fun HoleScrollColumn(
    modifier: Modifier = Modifier,
    state: ScrollState = rememberScrollState(),
    enabled: Boolean = true,
    contentPadding: PaddingValues = PaddingValues(16.dp),
    verticalArrangement: Arrangement.Vertical = Arrangement.spacedBy(16.dp),
    content: @Composable ColumnScope.() -> Unit,
) {
    val wear = isWearUi()
    Column(modifier.wearRotary(state, enabled).verticalScroll(state, enabled = enabled)
        .padding(if (wear) WearContentPadding else contentPadding),
        verticalArrangement = if (wear) Arrangement.spacedBy(8.dp) else verticalArrangement) {
        if (wear) WearPageHeader()
        content()
        if (wear) WearPageFooter()
    }
}

/** RowScope is kept for existing weighted children; each child gets its own line on Wear. */
@Composable
internal fun WearStackedRow(
    modifier: Modifier = Modifier,
    horizontalArrangement: Arrangement.Horizontal = Arrangement.Start,
    verticalAlignment: Alignment.Vertical = Alignment.Top,
    content: @Composable RowScope.() -> Unit,
) {
    if (!isWearUi()) Row(modifier, horizontalArrangement, verticalAlignment, content)
    else Column(modifier, verticalArrangement = Arrangement.spacedBy(6.dp)) { RowScopeContent(content) }
}

// Translate Row weights into full-width children when laying them out vertically.
private val WearRowScope = object : RowScope {
    override fun Modifier.weight(weight: Float, fill: Boolean): Modifier = fillMaxWidth()
    override fun Modifier.align(alignment: Alignment.Vertical): Modifier = this
    override fun Modifier.alignBy(alignmentLine: androidx.compose.ui.layout.HorizontalAlignmentLine): Modifier = this
    override fun Modifier.alignByBaseline(): Modifier = this
    override fun Modifier.alignBy(alignmentLineBlock: (androidx.compose.ui.layout.Measured) -> Int): Modifier = this
}
@Composable private fun RowScopeContent(content: @Composable RowScope.() -> Unit) = WearRowScope.content()

@Composable
internal fun WearDialog(onDismissRequest: () -> Unit, content: @Composable () -> Unit) {
    Dialog(onDismissRequest, properties = DialogProperties(usePlatformDefaultWidth = false, decorFitsSystemWindows = false)) {
        CompositionLocalProvider(LocalWearPageChrome provides null, LocalWearRotaryActive provides true) {
            Surface(Modifier.fillMaxSize().imePadding(), color = MaterialTheme.colorScheme.background) { content() }
        }
    }
}

@Composable
internal fun WearAppFrame(
    route: String, onNavigate: (String) -> Unit,
    snapshot: CoreSnapshot, enabled: Boolean, onToggleRun: (Boolean) -> Unit,
    snackbarHostState: SnackbarHostState?, content: @Composable () -> Unit,
) {
    var expanded by rememberSaveable { mutableStateOf(false) }
    val reveal by animateFloatAsState(if (expanded) 1f else 0f,
        spring(dampingRatio = .85f, stiffness = 500f), label = "wear drawer reveal")
    val primary = route in primaryRoutes
    LaunchedEffect(route) { expanded = false }
    BackHandler(expanded) { expanded = false }
    val round = LocalConfiguration.current.isScreenRound
    val layoutDensity = LocalDensity.current
    val windowDensity = LocalWearWindowDensity.current ?: layoutDensity
    val handleScale = windowDensity.density / layoutDensity.density
    val threshold = with(windowDensity) { 12.dp.toPx() }
    Box(Modifier.fillMaxSize().then(if (round) Modifier.clip(CircleShape) else Modifier)
        .background(MaterialTheme.colorScheme.background)) {
        Box(Modifier.fillMaxSize().graphicsLayer { alpha = 1f - .85f * reveal; scaleX = 1f - .025f * reveal; scaleY = scaleX }
            .then(if (expanded) Modifier.clearAndSetSemantics {} else Modifier)) {
            CompositionLocalProvider(LocalFloatingInset provides 0.dp, LocalWearRotaryActive provides !expanded) { content() }
        }
        AnimatedVisibility(expanded,
            enter = slideInVertically(spring(dampingRatio = .88f, stiffness = 420f)) { it } + fadeIn(tween(180)),
            exit = slideOutVertically(tween(220)) { it } + fadeOut(tween(160))) {
            CompositionLocalProvider(LocalWearPageChrome provides null, LocalWearRotaryActive provides expanded) {
                val navigationScroll = rememberScrollState()
                val density = LocalDensity.current
                LaunchedEffect(route) {
                    val selected = destinations.indexOfFirst { it.route == route }.coerceAtLeast(0)
                    navigationScroll.scrollTo(with(density) { (selected * 48).dp.roundToPx() })
                }
                var drag by remember { mutableFloatStateOf(0f) }
                Box(Modifier.fillMaxSize().testTag("wear-navigation").pointerInput(threshold) {
                    detectVerticalDragGestures(onDragStart = { drag = 0f }, onVerticalDrag = { change, amount ->
                        drag += amount
                        if (drag > threshold) { change.consume(); expanded = false }
                    })
                }, contentAlignment = Alignment.Center) {
                    Column(Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 24.dp),
                        horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(8.dp)) {
                        Text("转动表冠浏览", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        Box(Modifier.fillMaxWidth().clip(CircleShape).wearRotary(navigationScroll).horizontalScroll(navigationScroll)) {
                            // Keep the phone dock, including its spring lens and drag selection.
                            Box(Modifier.width(240.dp)) { AppNavigation(route) { expanded = false; onNavigate(it) } }
                        }
                        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                            FloatingRunControl(snapshot, enabled, onToggleRun, docked = true)
                            Text(if (snapshot.runRequested) "停止转发" else "启动转发",
                                Modifier.clickable(enabled = enabled) { onToggleRun(!snapshot.runRequested) },
                                style = MaterialTheme.typography.bodyMedium)
                        }
                    }
                }
            }
        }
        if (primary && !WindowInsets.isImeVisible) {
            var drag by remember { mutableFloatStateOf(0f) }
            Canvas(Modifier.align(Alignment.BottomCenter).width(100.dp * handleScale).height(36.dp * handleScale)
                .testTag("wear-navigation-handle")
                .semantics { contentDescription = if (expanded) "收起导航" else "展开导航" }
                .clickable(interactionSource = remember { MutableInteractionSource() }, indication = null,
                    role = Role.Button, onClick = { expanded = !expanded })
                .pointerInput(threshold, expanded) {
                    detectVerticalDragGestures(onDragStart = { drag = 0f }, onVerticalDrag = { change, amount ->
                        drag += amount
                        if ((!expanded && drag < -threshold) || (expanded && drag > threshold)) {
                            change.consume()
                            expanded = !expanded
                        }
                    })
                }) {
                // Only a white arc is drawn: no pill, scrim, bottom bar or reserved black strip.
                val path = Path().apply {
                    moveTo(size.width * .27f, size.height * (.68f - .06f * reveal))
                    quadraticTo(size.width * .5f, size.height * (.94f - .18f * reveal), size.width * .73f, size.height * (.68f - .06f * reveal))
                }
                drawPath(path, Color.White, style = Stroke((5.dp * handleScale).toPx(), cap = StrokeCap.Round))
            }
        }
        if (snackbarHostState != null) HoleSnackbarHost(snackbarHostState)
    }
}

/** Dialog content remains unchanged on phones; Wear gets a full, scrollable surface. */
@Composable
internal fun HoleDialog(onDismissRequest: () -> Unit, content: @Composable () -> Unit) {
    if (isWearUi()) WearDialog(onDismissRequest) {
        HoleScrollColumn(Modifier.fillMaxSize()) { content() }
    } else Dialog(onDismissRequest, content = content)
}

@Composable
internal fun Modifier.phoneDialogScroll(): Modifier =
    if (isWearUi()) this else verticalScroll(rememberScrollState())
