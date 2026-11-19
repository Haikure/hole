package dev.hole.app.ui

import android.app.Activity
import android.content.Context
import android.os.Build
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalView
import androidx.core.view.WindowCompat
import dev.hole.app.config.*

internal fun holeColorScheme(context: Context, dark: Boolean, dynamic: Boolean, palette: ThemePalette): ColorScheme =
    if (dynamic && Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
        if (dark) dynamicDarkColorScheme(context) else dynamicLightColorScheme(context)
    } else materialPalette(palette, dark)

@OptIn(ExperimentalMaterial3ExpressiveApi::class)
@Composable
fun HoleTheme(
    mode: ThemeMode, dynamic: Boolean,
    @Suppress("UNUSED_PARAMETER") style: ThemeStyle = ThemeStyle.MATERIAL,
    palette: ThemePalette = ThemePalette.BLUE, content: @Composable () -> Unit,
) {
    val dark = when (mode) {
        ThemeMode.SYSTEM -> isSystemInDarkTheme()
        ThemeMode.LIGHT -> false
        ThemeMode.DARK -> true
    }
    val colors = holeColorScheme(LocalContext.current, dark, dynamic, palette)
    val wear = isWearUi()
    val view = LocalView.current
    val activity = view.context as? Activity
    SideEffect {
        if (activity != null && !view.isInEditMode) WindowCompat.getInsetsController(activity.window, view).apply {
            isAppearanceLightStatusBars = !dark
            isAppearanceLightNavigationBars = !dark
        }
    }
    MaterialExpressiveTheme(colorScheme = colors, typography = if (wear) WearTypography else LightTypography, shapes = HoleShapes) {
        if (wear) WearDisplayDensity {
            CompositionLocalProvider(LocalContentColor provides colors.onBackground, content = content)
        } else content()
    }
}
