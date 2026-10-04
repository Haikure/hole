package dev.hole.app.ui

import android.app.Activity
import android.os.Build
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.ColorScheme
import androidx.compose.material3.ExperimentalMaterial3ExpressiveApi
import androidx.compose.material3.MaterialExpressiveTheme
import androidx.compose.material3.Shapes
import androidx.compose.material3.Typography
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.dynamicDarkColorScheme
import androidx.compose.material3.dynamicLightColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.SideEffect
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.lerp
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.unit.dp
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.core.view.WindowCompat
import dev.hole.app.config.ThemeMode
import dev.hole.app.config.ThemePalette

// KernelSU-style palette: dark graphite surfaces, restrained blue accent, compact corners.
private val LightColors = lightColorScheme(
    primary = Color(0xFF1769E0), onPrimary = Color.White,
    primaryContainer = Color(0xFFD9E7FF), onPrimaryContainer = Color(0xFF001B3E),
    secondary = Color(0xFF526070), onSecondary = Color.White,
    secondaryContainer = Color(0xFFD6E4F7), onSecondaryContainer = Color(0xFF0F1C2A),
    tertiary = Color(0xFF67587A), onTertiary = Color.White,
    tertiaryContainer = Color(0xFFEEDCFF), onTertiaryContainer = Color(0xFF241532),
    background = Color(0xFFF7F9FC), onBackground = Color(0xFF171B21),
    surface = Color(0xFFF7F9FC), onSurface = Color(0xFF171B21),
    surfaceVariant = Color(0xFFE0E5ED), onSurfaceVariant = Color(0xFF414852),
    outline = Color(0xFF707985), outlineVariant = Color(0xFFC1C8D2),
    error = Color(0xFFBA1A1A), onError = Color.White,
    errorContainer = Color(0xFFFFDAD6), onErrorContainer = Color(0xFF410002),
)

private val DarkColors = darkColorScheme(
    primary = Color(0xFFA9C8FF), onPrimary = Color(0xFF003062),
    primaryContainer = Color(0xFF00478D), onPrimaryContainer = Color(0xFFD9E7FF),
    secondary = Color(0xFFBBC7D8), onSecondary = Color(0xFF253140),
    secondaryContainer = Color(0xFF3B4859), onSecondaryContainer = Color(0xFFD6E4F7),
    tertiary = Color(0xFFD8BEEA), onTertiary = Color(0xFF3B2949),
    tertiaryContainer = Color(0xFF523F60), onTertiaryContainer = Color(0xFFEEDCFF),
    background = Color(0xFF0D1014), onBackground = Color(0xFFE2E6ED),
    surface = Color(0xFF0D1014), onSurface = Color(0xFFE2E6ED),
    surfaceVariant = Color(0xFF414852), onSurfaceVariant = Color(0xFFC1C8D2),
    outline = Color(0xFF8B929D), outlineVariant = Color(0xFF414852),
    error = Color(0xFFFFB4AB), onError = Color(0xFF690005),
    errorContainer = Color(0xFF93000A), onErrorContainer = Color(0xFFFFDAD6),
)

private val KernelTypography = Typography()
private val KernelShapes = Shapes(
    extraSmall = RoundedCornerShape(6.dp), small = RoundedCornerShape(8.dp),
    medium = RoundedCornerShape(12.dp), large = RoundedCornerShape(16.dp),
    extraLarge = RoundedCornerShape(20.dp),
)

internal fun materialPalette(palette: ThemePalette, dark: Boolean): ColorScheme {
    val base = if (dark) DarkColors else LightColors
    if (palette == ThemePalette.BLUE) return base
    val tones = when (palette) {
        ThemePalette.GREEN -> listOf(0xFF356A4B, 0xFFA0D4AE, 0xFFC3EFD0, 0xFF1D5134)
        ThemePalette.PURPLE -> listOf(0xFF7055A0, 0xFFD4BBFF, 0xFFEBDDFF, 0xFF573D85)
        ThemePalette.ROSE -> listOf(0xFF98445F, 0xFFFFB1C7, 0xFFFFD9E2, 0xFF7A2C47)
        ThemePalette.AMBER -> listOf(0xFF805610, 0xFFF2BF6B, 0xFFFFDDA8, 0xFF624000)
        ThemePalette.BLUE -> error("handled above")
    }.map(::Color)
    val accent = tones[if (dark) 1 else 0]
    val container = tones[if (dark) 3 else 2]
    val ink = if (dark) Color(0xFFF4F0F4) else Color(0xFF201B20)
    val surface = lerp(if (dark) Color(0xFF121212) else Color(0xFFFCFAFC), accent, 0.025f)
    return base.copy(
        primary = accent, onPrimary = if (dark) Color(0xFF241C16) else Color.White,
        primaryContainer = container, onPrimaryContainer = ink,
        secondary = accent, onSecondary = if (dark) Color(0xFF241C16) else Color.White,
        secondaryContainer = lerp(surface, container, 0.65f), onSecondaryContainer = ink,
        tertiary = accent, onTertiary = if (dark) Color(0xFF241C16) else Color.White,
        tertiaryContainer = container, onTertiaryContainer = ink,
        background = surface, surface = surface, surfaceTint = accent,
        surfaceVariant = lerp(surface, accent, 0.12f), outlineVariant = lerp(surface, accent, 0.28f),
        inversePrimary = tones[if (dark) 0 else 1],
    )
}

@OptIn(ExperimentalMaterial3ExpressiveApi::class)
@Composable
fun HoleTheme(
    mode: ThemeMode,
    dynamic: Boolean,
    // Kept for source/config compatibility. KernelSU is now the only rendered style.
    @Suppress("UNUSED_PARAMETER") style: dev.hole.app.config.ThemeStyle = dev.hole.app.config.ThemeStyle.MATERIAL,
    palette: ThemePalette = ThemePalette.BLUE,
    content: @Composable () -> Unit,
) {
    val dark = when (mode) {
        ThemeMode.SYSTEM -> isSystemInDarkTheme()
        ThemeMode.LIGHT -> false
        ThemeMode.DARK -> true
    }
    val useDynamic = dynamic && Build.VERSION.SDK_INT >= Build.VERSION_CODES.S
    val colors = if (useDynamic) {
        if (dark) dynamicDarkColorScheme(LocalContext.current) else dynamicLightColorScheme(LocalContext.current)
    } else materialPalette(palette, dark)
    val view = LocalView.current
    val activity = view.context as? Activity
    SideEffect {
        if (activity != null && !view.isInEditMode) {
            WindowCompat.getInsetsController(activity.window, view).apply {
                isAppearanceLightStatusBars = !dark
                isAppearanceLightNavigationBars = !dark
            }
        }
    }
    MaterialExpressiveTheme(
        colorScheme = colors,
        typography = KernelTypography,
        shapes = KernelShapes,
        content = content,
    )
}
