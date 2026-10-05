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
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp
import androidx.compose.ui.unit.dp
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.core.view.WindowCompat
import dev.hole.app.config.ThemeMode
import dev.hole.app.config.ThemePalette

private data class PaletteTones(
    val lightAccent: Color,
    val lightOnAccent: Color,
    val lightContainer: Color,
    val lightOnContainer: Color,
    val lightSecondary: Color,
    val lightOnSecondary: Color,
    val lightSecondaryContainer: Color,
    val lightOnSecondaryContainer: Color,
    val darkAccent: Color,
    val darkOnAccent: Color,
    val darkContainer: Color,
    val darkOnContainer: Color,
    val darkSecondary: Color,
    val darkOnSecondary: Color,
    val darkSecondaryContainer: Color,
    val darkOnSecondaryContainer: Color,
)

private val palettes = mapOf(
    ThemePalette.BLUE to PaletteTones(
        Color(0xFF435E91), Color.White, Color(0xFFD8E2FF), Color(0xFF001A40),
        Color(0xFF565F71), Color.White, Color(0xFFDAE2F9), Color(0xFF131C2C),
        Color(0xFFADC6FF), Color(0xFF102F60), Color(0xFF294677), Color(0xFFD8E2FF),
        Color(0xFFBEC6DD), Color(0xFF283141), Color(0xFF3F4759), Color(0xFFDAE2F9),
    ),
    ThemePalette.GREEN to PaletteTones(
        Color(0xFF386B4B), Color.White, Color(0xFFD0EAD4), Color(0xFF102617),
        Color(0xFF526B56), Color.White, Color(0xFFDCE8DC), Color(0xFF172219),
        Color(0xFF9BD5A9), Color(0xFF173622), Color(0xFF31583D), Color(0xFFC0F0C9),
        Color(0xFFBDCEB9), Color(0xFF263527), Color(0xFF243A29), Color(0xFFD4E7D0),
    ),
    ThemePalette.PURPLE to PaletteTones(
        Color(0xFF66508D), Color.White, Color(0xFFE9DDFF), Color(0xFF24163E),
        Color(0xFF665A73), Color.White, Color(0xFFE8DFF0), Color(0xFF211B25),
        Color(0xFFD0B9FF), Color(0xFF35205C), Color(0xFF503B78), Color(0xFFEBDDFF),
        Color(0xFFD0C2D8), Color(0xFF332B38), Color(0xFF3A3042), Color(0xFFE9DDF0),
    ),
    ThemePalette.ROSE to PaletteTones(
        Color(0xFF98445F), Color.White, Color(0xFFFFD9E2), Color(0xFF3D071B),
        Color(0xFF76565F), Color.White, Color(0xFFF2DDE2), Color(0xFF2B1A1E),
        Color(0xFFFFB1C7), Color(0xFF5A1930), Color(0xFF79334A), Color(0xFFFFD9E2),
        Color(0xFFE0C1C9), Color(0xFF412B31), Color(0xFF482D35), Color(0xFFF4DDE2),
    ),
    ThemePalette.AMBER to PaletteTones(
        Color(0xFF805610), Color.White, Color(0xFFFFDDA8), Color(0xFF2A1800),
        Color(0xFF705C43), Color.White, Color(0xFFF0E1C9), Color(0xFF241D13),
        Color(0xFFF2BF6B), Color(0xFF442C00), Color(0xFF634400), Color(0xFFFFDDA8),
        Color(0xFFD9C6A8), Color(0xFF3C3121), Color(0xFF423625), Color(0xFFF2E2C7),
    ),
)

private val LightTypography = Typography().let { base ->
    base.copy(
        displaySmall = base.displaySmall.copy(fontWeight = FontWeight.SemiBold, letterSpacing = (-0.5).sp),
        headlineLarge = base.headlineLarge.copy(fontWeight = FontWeight.SemiBold, letterSpacing = (-0.35).sp),
        headlineMedium = base.headlineMedium.copy(fontWeight = FontWeight.SemiBold, letterSpacing = (-0.2).sp),
        titleLarge = base.titleLarge.copy(fontWeight = FontWeight.SemiBold),
        titleMedium = base.titleMedium.copy(fontWeight = FontWeight.SemiBold),
        labelLarge = base.labelLarge.copy(fontWeight = FontWeight.SemiBold),
    )
}

private val HoleShapes = Shapes(
    extraSmall = RoundedCornerShape(4.dp),
    small = RoundedCornerShape(8.dp),
    medium = RoundedCornerShape(12.dp),
    large = RoundedCornerShape(16.dp),
    extraLarge = RoundedCornerShape(20.dp),
)

internal fun materialPalette(palette: ThemePalette, dark: Boolean): ColorScheme {
    val tones = palettes.getValue(palette)
    if (!dark) {
        return lightColorScheme(
            primary = tones.lightAccent,
            onPrimary = tones.lightOnAccent,
            primaryContainer = tones.lightContainer,
            onPrimaryContainer = tones.lightOnContainer,
            secondary = tones.lightSecondary,
            onSecondary = tones.lightOnSecondary,
            secondaryContainer = tones.lightSecondaryContainer,
            onSecondaryContainer = tones.lightOnSecondaryContainer,
            tertiary = Color(0xFF526477),
            onTertiary = Color.White,
            tertiaryContainer = Color(0xFFDCE6F0),
            onTertiaryContainer = Color(0xFF17212C),
            background = Color(0xFFFAF9FC),
            onBackground = Color(0xFF1B1B20),
            surface = Color(0xFFFAF9FC),
            onSurface = Color(0xFF1B1B20),
            surfaceDim = Color(0xFFDCE4E0),
            surfaceBright = Color(0xFFFAF9FC),
            surfaceContainerLowest = Color.White,
            surfaceContainerLow = Color(0xFFF3F2F7),
            surfaceContainer = Color(0xFFEEEEF3),
            surfaceContainerHigh = Color(0xFFE8E7ED),
            surfaceContainerHighest = Color(0xFFE2E1E7),
            surfaceVariant = Color(0xFFE8E7ED),
            onSurfaceVariant = Color(0xFF48474F),
            outline = Color(0xFF78847F),
            outlineVariant = Color(0xFFCBC9D2),
            error = Color(0xFFBA1A1A),
            onError = Color.White,
            errorContainer = Color(0xFFFFDAD6),
            onErrorContainer = Color(0xFF410002),
        )
    }
    return darkColorScheme(
        primary = tones.darkAccent,
        onPrimary = tones.darkOnAccent,
        primaryContainer = tones.darkContainer,
        onPrimaryContainer = tones.darkOnContainer,
        secondary = tones.darkSecondary,
        onSecondary = tones.darkOnSecondary,
        secondaryContainer = tones.darkSecondaryContainer,
        onSecondaryContainer = tones.darkOnSecondaryContainer,
        tertiary = Color(0xFFB7C9DE),
        onTertiary = Color(0xFF22313E),
        tertiaryContainer = Color(0xFF384B5B),
        onTertiaryContainer = Color(0xFFD3E5F5),
        background = Color(0xFF121216),
        onBackground = Color(0xFFE5E1E9),
        surface = Color(0xFF141318),
        onSurface = Color(0xFFE5E1E9),
        surfaceDim = Color(0xFF121216),
        surfaceBright = Color(0xFF35413D),
        surfaceContainerLowest = Color(0xFF0E0D12),
        surfaceContainerLow = Color(0xFF1C1B20),
        surfaceContainer = Color(0xFF201F25),
        surfaceContainerHigh = Color(0xFF2B2930),
        surfaceContainerHighest = Color(0xFF35333B),
        surfaceVariant = Color(0xFF36343D),
        onSurfaceVariant = Color(0xFFCBC5D1),
        outline = Color(0xFF899691),
        outlineVariant = Color(0xFF48454F),
        error = Color(0xFFFFB4AB),
        onError = Color(0xFF690005),
        errorContainer = Color(0xFF93000A),
        onErrorContainer = Color(0xFFFFDAD6),
    )
}

@OptIn(ExperimentalMaterial3ExpressiveApi::class)
@Composable
fun HoleTheme(
    mode: ThemeMode,
    dynamic: Boolean,
    // Retained only so older saved appearance preferences continue to load.
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
        typography = LightTypography,
        shapes = HoleShapes,
        content = content,
    )
}
