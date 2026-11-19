package dev.hole.app.ui

import android.os.Build
import androidx.compose.runtime.Composable

@Composable
internal fun PlatformBackHandler(enabled: Boolean = true, onBack: () -> Unit) =
    androidx.activity.compose.BackHandler(enabled, onBack)

internal fun supportsWallpaperColors(): Boolean = Build.VERSION.SDK_INT >= Build.VERSION_CODES.S
internal fun wallpaperColorsDescription(): String = "Android 12 及以上版本可用"
