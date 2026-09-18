package dev.hole.app.ui

import android.app.Activity
import android.os.Build
import androidx.compose.foundation.LocalIndication
import androidx.compose.foundation.LocalOverscrollFactory
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.ColorScheme
import androidx.compose.material3.LocalContentColor
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Shapes
import androidx.compose.material3.Typography
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.dynamicDarkColorScheme
import androidx.compose.material3.dynamicLightColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.SideEffect
import androidx.compose.runtime.remember
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.core.view.WindowCompat
import dev.hole.app.config.ThemeMode
import dev.hole.app.config.ThemeStyle
import top.yukonga.miuix.kmp.theme.ColorSchemeMode
import top.yukonga.miuix.kmp.theme.Colors
import top.yukonga.miuix.kmp.theme.MiuixTheme
import top.yukonga.miuix.kmp.theme.ThemeController
import top.yukonga.miuix.kmp.theme.defaultTextStyles

val LocalThemeStyle = staticCompositionLocalOf { ThemeStyle.MATERIAL }

// Android 12+ 未启用动态配色（或旧版本）时的固定回退方案：
// Material Theme Builder 生成的完整 tonal 色板，全部角色显式声明，不依赖库默认值。
private val LightColors = lightColorScheme(
    primary = Color(0xFF36618E), onPrimary = Color(0xFFFFFFFF),
    primaryContainer = Color(0xFFD1E4FF), onPrimaryContainer = Color(0xFF001D36),
    inversePrimary = Color(0xFFA0CAFD),
    secondary = Color(0xFF535F70), onSecondary = Color(0xFFFFFFFF),
    secondaryContainer = Color(0xFFD7E3F7), onSecondaryContainer = Color(0xFF101C2B),
    tertiary = Color(0xFF6B5778), onTertiary = Color(0xFFFFFFFF),
    tertiaryContainer = Color(0xFFF2DAFF), onTertiaryContainer = Color(0xFF251431),
    background = Color(0xFFF8F9FF), onBackground = Color(0xFF191C20),
    surface = Color(0xFFF8F9FF), onSurface = Color(0xFF191C20),
    surfaceVariant = Color(0xFFDFE2EB), onSurfaceVariant = Color(0xFF43474E),
    surfaceDim = Color(0xFFD8DAE0), surfaceBright = Color(0xFFF8F9FF),
    surfaceContainerLowest = Color(0xFFFFFFFF), surfaceContainerLow = Color(0xFFF2F3FA),
    surfaceContainer = Color(0xFFECEEF4), surfaceContainerHigh = Color(0xFFE6E8EE),
    surfaceContainerHighest = Color(0xFFE1E2E8),
    outline = Color(0xFF73777F), outlineVariant = Color(0xFFC3C7D0),
    scrim = Color(0xFF000000),
    inverseSurface = Color(0xFF2E3135), inverseOnSurface = Color(0xFFEFF0F7),
    error = Color(0xFFBA1A1A), onError = Color(0xFFFFFFFF),
    errorContainer = Color(0xFFFFDAD6), onErrorContainer = Color(0xFF410002),
)

private val DarkColors = darkColorScheme(
    primary = Color(0xFFA0CAFD), onPrimary = Color(0xFF003258),
    primaryContainer = Color(0xFF194975), onPrimaryContainer = Color(0xFFD1E4FF),
    inversePrimary = Color(0xFF36618E),
    secondary = Color(0xFFBBC7DB), onSecondary = Color(0xFF253140),
    secondaryContainer = Color(0xFF3B4858), onSecondaryContainer = Color(0xFFD7E3F7),
    tertiary = Color(0xFFD7BDE4), onTertiary = Color(0xFF3B2948),
    tertiaryContainer = Color(0xFF523F5F), onTertiaryContainer = Color(0xFFF2DAFF),
    background = Color(0xFF111418), onBackground = Color(0xFFE1E2E8),
    surface = Color(0xFF111418), onSurface = Color(0xFFE1E2E8),
    surfaceVariant = Color(0xFF43474E), onSurfaceVariant = Color(0xFFC3C7D0),
    surfaceDim = Color(0xFF111418), surfaceBright = Color(0xFF37393E),
    surfaceContainerLowest = Color(0xFF0C0E13), surfaceContainerLow = Color(0xFF191C20),
    surfaceContainer = Color(0xFF1D2024), surfaceContainerHigh = Color(0xFF282A2F),
    surfaceContainerHighest = Color(0xFF33353A),
    outline = Color(0xFF8D9199), outlineVariant = Color(0xFF43474E),
    scrim = Color(0xFF000000),
    inverseSurface = Color(0xFFE1E2E8), inverseOnSurface = Color(0xFF2E3135),
    error = Color(0xFFFFB4AB), onError = Color(0xFF690005),
    errorContainer = Color(0xFF93000A), onErrorContainer = Color(0xFFFFDAD6),
)

private val MaterialTypography = Typography()
private val MaterialShapes = Shapes()
private val MiuixTypography = defaultTextStyles().let { text ->
    Typography(
        displayLarge = text.title1, displayMedium = text.title1, displaySmall = text.title1,
        headlineLarge = text.title1, headlineMedium = text.title2, headlineSmall = text.title3,
        titleLarge = text.title3, titleMedium = text.headline1.copy(fontWeight = FontWeight.Medium),
        titleSmall = text.headline2,
        bodyLarge = text.main, bodyMedium = text.body1, bodySmall = text.body2,
        labelLarge = text.button, labelMedium = text.footnote1, labelSmall = text.footnote2,
    )
}
private val MiuixShapes = Shapes(
    extraSmall = RoundedCornerShape(6.dp), small = RoundedCornerShape(8.dp),
    medium = RoundedCornerShape(16.dp), large = RoundedCornerShape(20.dp),
    extraLarge = RoundedCornerShape(28.dp),
)

@Composable
fun HoleTheme(
    mode: ThemeMode,
    dynamic: Boolean,
    style: ThemeStyle = ThemeStyle.MATERIAL,
    content: @Composable () -> Unit,
) {
    val dark = when (mode) {
        ThemeMode.SYSTEM -> isSystemInDarkTheme()
        ThemeMode.LIGHT -> false
        ThemeMode.DARK -> true
    }
    val useDynamic = dynamic && Build.VERSION.SDK_INT >= Build.VERSION_CODES.S
    val materialColors = if (useDynamic) {
        if (dark) dynamicDarkColorScheme(LocalContext.current) else dynamicLightColorScheme(LocalContext.current)
    } else if (dark) DarkColors else LightColors
    val controller = remember(dark, useDynamic) {
        ThemeController(
            colorSchemeMode = when {
                useDynamic && dark -> ColorSchemeMode.MonetDark
                useDynamic -> ColorSchemeMode.MonetLight
                dark -> ColorSchemeMode.Dark
                else -> ColorSchemeMode.Light
            },
        )
    }
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
    val platformOverscroll = LocalOverscrollFactory.current
    // 两个 Provider 始终位于同一组合位置。切换风格只更新值，不重建路由、表单和撤销队列。
    MiuixTheme(controller = controller) {
        val miuix = style == ThemeStyle.MIUIX
        val colors = if (miuix) MiuixTheme.colorScheme.toMaterialColorScheme(dark) else materialColors
        val miuixIndication = LocalIndication.current
        val miuixOverscroll = LocalOverscrollFactory.current
        MaterialTheme(
            colorScheme = colors,
            typography = if (miuix) MiuixTypography else MaterialTypography,
            shapes = if (miuix) MiuixShapes else MaterialShapes,
        ) {
            CompositionLocalProvider(
                LocalThemeStyle provides style,
                LocalContentColor provides colors.onBackground,
                LocalIndication provides if (miuix) miuixIndication else LocalIndication.current,
                LocalOverscrollFactory provides if (miuix) miuixOverscroll else platformOverscroll,
                content = content,
            )
        }
    }
}

/** 为共用的文本、布局、滑动删除等 Material 组件提供与 Miuix 一致的语义色。 */
internal fun Colors.toMaterialColorScheme(dark: Boolean): ColorScheme =
    (if (dark) DarkColors else LightColors).copy(
        primary = primary, onPrimary = onPrimary,
        primaryContainer = tertiaryContainer, onPrimaryContainer = onSurface,
        inversePrimary = primary,
        secondary = primary, onSecondary = onPrimary,
        secondaryContainer = surfaceContainer, onSecondaryContainer = onSurfaceContainer,
        tertiary = primaryVariant, onTertiary = onPrimary,
        tertiaryContainer = tertiaryContainer, onTertiaryContainer = onSurface,
        background = surface, onBackground = onSurface,
        surface = surface, onSurface = onSurface,
        surfaceVariant = surfaceVariant, onSurfaceVariant = onSurfaceSecondary,
        surfaceDim = surface, surfaceBright = surfaceContainerHighest,
        surfaceContainerLowest = background, surfaceContainerLow = surfaceContainer,
        surfaceContainer = surfaceContainer, surfaceContainerHigh = surfaceContainerHigh,
        surfaceContainerHighest = surfaceContainerHighest,
        surfaceTint = primary,
        inverseSurface = onSurface, inverseOnSurface = surface,
        outline = outline, outlineVariant = dividerLine,
        error = error, onError = onError,
        errorContainer = errorContainer, onErrorContainer = onErrorContainer,
    )
