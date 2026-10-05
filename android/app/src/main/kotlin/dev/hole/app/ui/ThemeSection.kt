package dev.hole.app.ui

import android.os.Build
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Check
import androidx.compose.material3.FilterChip
import androidx.compose.material3.FilterChipDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp
import dev.hole.app.config.ThemeMode
import dev.hole.app.config.ThemePalette

@Composable
@OptIn(ExperimentalLayoutApi::class)
fun ThemeSection(
    @Suppress("UNUSED_PARAMETER") themeStyle: dev.hole.app.config.ThemeStyle,
    themeMode: ThemeMode,
    dynamicColor: Boolean,
    @Suppress("UNUSED_PARAMETER") onStyleChange: (dev.hole.app.config.ThemeStyle) -> Unit,
    onModeChange: (ThemeMode) -> Unit,
    onDynamicColorChange: (Boolean) -> Unit,
    palette: ThemePalette = ThemePalette.BLUE,
    onPaletteChange: (ThemePalette) -> Unit = {},
) {
    Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text("显示模式", style = MaterialTheme.typography.titleSmall)
        HoleSingleChoice(
            ThemeMode.entries.map { it.value to it.label },
            themeMode.value,
            { onModeChange(ThemeMode.fromValue(it)) },
        )
        val supportsDynamic = Build.VERSION.SDK_INT >= Build.VERSION_CODES.S
        HoleSwitchPreference(
            title = "动态配色",
            summary = if (supportsDynamic) "跟随系统壁纸颜色" else "Android 12 及以上版本可用",
            checked = dynamicColor,
            enabled = supportsDynamic,
            label = "动态配色",
            insideMargin = androidx.compose.foundation.layout.PaddingValues(0.dp),
            onCheckedChange = onDynamicColorChange,
        )
        Text("强调色", style = MaterialTheme.typography.titleSmall)
        FlowRow(
            Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            ThemePalette.entries.forEach { choice ->
                val selected = palette == choice && !dynamicColor
                FilterChip(
                    selected = selected,
                    onClick = { onPaletteChange(choice) },
                    label = { Text(choice.label) },
                    leadingIcon = {
                        Surface(
                            modifier = Modifier.size(16.dp),
                            shape = CircleShape,
                            color = palettePreview(choice),
                            contentColor = Color.White,
                        ) {
                            if (selected) {
                                androidx.compose.foundation.layout.Box(contentAlignment = Alignment.Center) {
                                    Icon(Icons.Filled.Check, contentDescription = null, modifier = Modifier.size(12.dp))
                                }
                            }
                        }
                    },
                    shape = MaterialTheme.shapes.large,
                    colors = FilterChipDefaults.filterChipColors(
                        selectedContainerColor = palettePreview(choice),
                        selectedLabelColor = Color.White,
                        selectedLeadingIconColor = Color.White,
                    ),
                )
            }
        }
    }
}

private fun palettePreview(palette: ThemePalette) = when (palette) {
    ThemePalette.BLUE -> androidx.compose.ui.graphics.Color(0xFF435E91)
    ThemePalette.GREEN -> androidx.compose.ui.graphics.Color(0xFF386B4B)
    ThemePalette.PURPLE -> androidx.compose.ui.graphics.Color(0xFF66508D)
    ThemePalette.ROSE -> androidx.compose.ui.graphics.Color(0xFF98445F)
    ThemePalette.AMBER -> androidx.compose.ui.graphics.Color(0xFF805610)
}
