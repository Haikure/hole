package dev.hole.app.ui

import android.os.Build
import androidx.compose.foundation.layout.*
import androidx.compose.material3.*
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import dev.hole.app.config.ThemeMode
import dev.hole.app.config.ThemePalette

@Composable
fun ThemeSection(
    themeStyle: dev.hole.app.config.ThemeStyle,
    themeMode: ThemeMode,
    dynamicColor: Boolean,
    onStyleChange: (dev.hole.app.config.ThemeStyle) -> Unit,
    onModeChange: (ThemeMode) -> Unit,
    onDynamicColorChange: (Boolean) -> Unit,
    palette: ThemePalette = ThemePalette.BLUE,
    onPaletteChange: (ThemePalette) -> Unit = {},
) {
    Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text("KernelSU 风格", style = MaterialTheme.typography.titleMedium)
        Text("统一使用紧凑圆角与蓝色强调色，兼容旧配置但不再切换主题实现。", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Text("显示模式", style = MaterialTheme.typography.bodyMedium)
        HoleSingleChoice(ThemeMode.entries.map { it.value to it.label }, themeMode.value, { onModeChange(ThemeMode.fromValue(it)) })
        val supportsDynamic = Build.VERSION.SDK_INT >= Build.VERSION_CODES.S
        HoleSwitchPreference(
            title = "动态配色",
            summary = if (supportsDynamic) "使用系统壁纸颜色" else "当前系统不支持动态配色",
            checked = dynamicColor,
            enabled = supportsDynamic,
            label = "动态配色",
            onCheckedChange = onDynamicColorChange,
        )
        Text("固定配色", style = MaterialTheme.typography.bodyMedium)
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            ThemePalette.entries.forEach { choice ->
                FilterChip(
                    selected = palette == choice && !dynamicColor,
                    onClick = { onPaletteChange(choice) },
                    label = { Text(choice.label) },
                    modifier = Modifier.weight(1f),
                )
            }
        }
    }
}
