package dev.hole.app.ui

import androidx.compose.foundation.ScrollState
import androidx.compose.foundation.gestures.ScrollableState
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import dev.hole.app.config.*

@OptIn(ExperimentalMaterial3ExpressiveApi::class)
@Composable
fun HoleTheme(mode: ThemeMode, dynamic: Boolean = false, style: ThemeStyle = ThemeStyle.MATERIAL,
    palette: ThemePalette = ThemePalette.BLUE, content: @Composable () -> Unit) {
    val dark = when (mode) {
        ThemeMode.SYSTEM -> isSystemInDarkTheme()
        ThemeMode.LIGHT -> false
        ThemeMode.DARK -> true
    }
    MaterialExpressiveTheme(colorScheme = materialPalette(palette, dark), typography = LightTypography, shapes = HoleShapes, content = content)
}

internal class DesktopBackDispatcher {
    val handlers = linkedMapOf<Any, () -> Unit>()
    fun back(): Boolean = handlers.values.lastOrNull()?.let { it(); true } ?: false
}
internal val LocalDesktopBack = staticCompositionLocalOf { DesktopBackDispatcher() }
@Composable internal fun PlatformBackHandler(enabled: Boolean = true, onBack: () -> Unit) {
    val dispatcher = LocalDesktopBack.current
    val callback by rememberUpdatedState(onBack)
    DisposableEffect(dispatcher, enabled) {
        val key = Any()
        if (enabled) dispatcher.handlers[key] = { callback() }
        onDispose { dispatcher.handlers.remove(key) }
    }
}
internal fun supportsWallpaperColors() = false
internal fun wallpaperColorsDescription() = "桌面使用下方强调色"

@Composable internal fun isWearUi() = false
@Composable internal fun screenSpacing(phone: Dp) = phone
internal val LocalWearRotaryActive = compositionLocalOf { false }
internal data class WearPageChrome(val title: String, val navigation: @Composable () -> Unit,
    val actions: @Composable RowScope.() -> Unit, val footer: @Composable RowScope.() -> Unit)
internal val LocalWearPageChrome = staticCompositionLocalOf<WearPageChrome?> { null }
@Composable internal fun Modifier.wearRotary(state: ScrollableState, enabled: Boolean = true) = this
@Composable internal fun HoleLazyColumn(modifier: Modifier = Modifier, contentPadding: PaddingValues = PaddingValues(0.dp),
    verticalArrangement: Arrangement.Vertical = Arrangement.Top, state: LazyListState = rememberLazyListState(), content: LazyListScope.() -> Unit) =
    LazyColumn(modifier, state = state, contentPadding = contentPadding, verticalArrangement = verticalArrangement, content = content)
@Composable internal fun HoleScrollColumn(modifier: Modifier = Modifier, state: ScrollState = rememberScrollState(), enabled: Boolean = true,
    contentPadding: PaddingValues = PaddingValues(16.dp), verticalArrangement: Arrangement.Vertical = Arrangement.spacedBy(16.dp), content: @Composable ColumnScope.() -> Unit) =
    Column(modifier.verticalScroll(state, enabled).padding(contentPadding), verticalArrangement = verticalArrangement, content = content)
@Composable internal fun WearStackedRow(modifier: Modifier = Modifier, horizontalArrangement: Arrangement.Horizontal = Arrangement.Start,
    verticalAlignment: Alignment.Vertical = Alignment.Top, content: @Composable RowScope.() -> Unit) =
    Row(modifier, horizontalArrangement, verticalAlignment, content)
@Composable internal fun WearDialog(onDismissRequest: () -> Unit, content: @Composable () -> Unit) = Dialog(onDismissRequest, content = content)
@Composable internal fun HoleDialog(onDismissRequest: () -> Unit, content: @Composable () -> Unit) = Dialog(onDismissRequest, content = content)
@Composable internal fun Modifier.phoneDialogScroll() = verticalScroll(rememberScrollState())
