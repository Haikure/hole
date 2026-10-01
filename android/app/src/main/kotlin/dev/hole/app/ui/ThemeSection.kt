package dev.hole.app.ui

import android.os.Build
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.selection.selectableGroup
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Check
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.unit.dp
import dev.hole.app.config.ThemeMode
import dev.hole.app.config.ThemePalette
import dev.hole.app.config.ThemeStyle
import top.yukonga.miuix.kmp.basic.Card as MiuixCard
import top.yukonga.miuix.kmp.basic.HorizontalDivider as MiuixDivider
import top.yukonga.miuix.kmp.basic.DropdownItem
import top.yukonga.miuix.kmp.preference.RadioButtonPreference
import top.yukonga.miuix.kmp.preference.RadioButtonLocation
import top.yukonga.miuix.kmp.preference.WindowSpinnerPreference

@Composable
fun ThemeSection(
    themeStyle: ThemeStyle,
    themeMode: ThemeMode,
    dynamicColor: Boolean,
    onStyleChange: (ThemeStyle) -> Unit,
    onModeChange: (ThemeMode) -> Unit,
    onDynamicColorChange: (Boolean) -> Unit,
    palette: ThemePalette = ThemePalette.BLUE,
    onPaletteChange: (ThemePalette) -> Unit = {},
) {
    if (themeStyle == ThemeStyle.MIUIX) {
        MiuixThemeSection(themeStyle, themeMode, dynamicColor, onStyleChange, onModeChange, onDynamicColorChange)
        return
    }
    Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text("主题风格", style = MaterialTheme.typography.bodyMedium)
        Column(Modifier.selectableGroup(), verticalArrangement = Arrangement.spacedBy(3.dp)) {
            ThemeStyle.entries.forEachIndexed { index, style ->
                ThemeOption(style, selected = themeStyle == style, first = index == 0, onSelect = { onStyleChange(style) })
            }
        }
        Text(
            "即时生效并自动保存，不影响当前连接。",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Text("显示模式", style = MaterialTheme.typography.bodyMedium, modifier = Modifier.padding(top = 4.dp))
        HoleSingleChoice(
            options = ThemeMode.entries.map { it.value to it.label },
            selectedValue = themeMode.value,
            onSelect = { onModeChange(ThemeMode.fromValue(it)) },
            modifier = Modifier.fillMaxWidth(),
        )
        val supportsDynamic = Build.VERSION.SDK_INT >= Build.VERSION_CODES.S
        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f).padding(end = 12.dp)) {
                Text("动态配色")
                Text(
                    if (supportsDynamic) "使用壁纸颜色，也可以选择下方配色。"
                    else "当前系统不支持壁纸配色，可以选择下方配色。",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            HoleSwitch(
                checked = dynamicColor,
                onCheckedChange = onDynamicColorChange,
                label = "动态配色",
                enabled = supportsDynamic,
            )
        }
        Text("配色", style = MaterialTheme.typography.titleMedium)
        Surface(shape = MaterialTheme.shapes.large, color = MaterialTheme.colorScheme.surfaceContainer) {
            Column(Modifier.fillMaxWidth().padding(20.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                Text("你的配色", style = MaterialTheme.typography.titleMedium)
                Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                    listOf(MaterialTheme.colorScheme.primary, MaterialTheme.colorScheme.secondaryContainer, MaterialTheme.colorScheme.tertiaryContainer).forEach { color ->
                        Box(Modifier.weight(1f).height(44.dp).background(color, MaterialTheme.shapes.medium))
                    }
                }
                Text(
                    if (dynamicColor && supportsDynamic) "跟随壁纸" else palette.label,
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
        androidx.compose.foundation.layout.FlowRow(
            modifier = Modifier.fillMaxWidth().selectableGroup(),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            ThemePalette.entries.forEach { choice ->
                val selected = palette == choice && !(dynamicColor && supportsDynamic)
                val swatch = materialPalette(choice, false)
                Surface(
                    shape = CircleShape,
                    color = if (selected) MaterialTheme.colorScheme.secondaryContainer else MaterialTheme.colorScheme.surfaceContainerLow,
                    modifier = Modifier.clip(CircleShape).selectable(
                        selected = selected, role = Role.RadioButton,
                        onClick = { onPaletteChange(choice) },
                    ),
                ) {
                    Row(
                        Modifier.padding(horizontal = 16.dp, vertical = 12.dp),
                        horizontalArrangement = Arrangement.spacedBy(8.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Box(Modifier.size(24.dp).background(swatch.primary, CircleShape), contentAlignment = Alignment.Center) {
                            if (selected) Icon(Icons.Filled.Check, null, Modifier.size(16.dp), tint = swatch.onPrimary)
                        }
                        Text(choice.label, style = MaterialTheme.typography.labelLarge)
                    }
                }
            }
        }
    }
}

/** 使用官方 preference 模块的分组行，而非给 Material 选项卡换色。 */
@Composable
private fun MiuixThemeSection(
    themeStyle: ThemeStyle,
    themeMode: ThemeMode,
    dynamicColor: Boolean,
    onStyleChange: (ThemeStyle) -> Unit,
    onModeChange: (ThemeMode) -> Unit,
    onDynamicColorChange: (Boolean) -> Unit,
) {
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        HoleSectionTitle("主题风格")
        MiuixCard(Modifier.fillMaxWidth().selectableGroup()) {
            ThemeStyle.entries.forEachIndexed { index, style ->
                if (index > 0) MiuixDivider(Modifier.padding(horizontal = 16.dp))
                RadioButtonPreference(
                    title = style.label,
                    summary = if (style == ThemeStyle.MIUIX) "HyperOS 风格 · 官方 Miuix 0.9.3" else style.description,
                    selected = themeStyle == style,
                    onClick = { if (themeStyle != style) onStyleChange(style) },
                    radioButtonLocation = RadioButtonLocation.End,
                    modifier = Modifier.semantics { selected = themeStyle == style },
                )
            }
        }
        HoleSectionTitle("显示")
        MiuixCard(Modifier.fillMaxWidth()) {
            WindowSpinnerPreference(
                title = "显示模式",
                items = ThemeMode.entries.map { DropdownItem(text = it.label) },
                selectedIndex = ThemeMode.entries.indexOf(themeMode),
                onSelectedIndexChange = { onModeChange(ThemeMode.entries[it]) },
            )
            MiuixDivider(Modifier.padding(horizontal = 16.dp))
            val supportsDynamic = Build.VERSION.SDK_INT >= Build.VERSION_CODES.S
            HoleSwitchPreference(
                title = "动态配色",
                summary = if (supportsDynamic) "使用壁纸颜色，关闭时使用经典蓝色" else "Android 12 及以上支持壁纸配色",
                checked = dynamicColor,
                enabled = supportsDynamic,
                onCheckedChange = onDynamicColorChange,
            )
        }
        Text(
            "外观即时保存，不影响当前连接。",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.padding(horizontal = 12.dp, vertical = 4.dp),
        )
    }
}

@Composable
private fun ThemeOption(style: ThemeStyle, selected: Boolean, first: Boolean, onSelect: () -> Unit) {
    val colors = MaterialTheme.colorScheme
    val shape = RoundedCornerShape(
        topStart = if (first) 28.dp else 6.dp, topEnd = if (first) 28.dp else 6.dp,
        bottomStart = if (first) 6.dp else 28.dp, bottomEnd = if (first) 6.dp else 28.dp,
    )
    Surface(
        modifier = Modifier
            .fillMaxWidth()
            .clip(shape)
            .selectable(selected = selected, role = Role.RadioButton, onClick = { if (!selected) onSelect() }),
        shape = shape,
        color = if (selected) colors.primaryContainer else colors.surfaceContainerLow,
        contentColor = if (selected) colors.onPrimaryContainer else colors.onSurface,
    ) {
        Row(
            Modifier.padding(16.dp),
            horizontalArrangement = Arrangement.spacedBy(16.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            if (LocalDensity.current.fontScale <= 1.3f) ThemeSwatch(style)
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Text(style.label, style = MaterialTheme.typography.titleMedium)
                Text(style.description, style = MaterialTheme.typography.bodySmall, color = colors.onSurfaceVariant)
            }
            Box(
                Modifier.size(24.dp).background(if (selected) colors.primary else colors.outlineVariant, CircleShape),
                contentAlignment = Alignment.Center,
            ) {
                if (selected) {
                    Icon(Icons.Filled.Check, contentDescription = null, tint = colors.onPrimary, modifier = Modifier.size(18.dp))
                } else {
                    Box(Modifier.size(20.dp).background(colors.surfaceContainerLow, CircleShape))
                }
            }
        }
    }
}

/** 装饰性缩略图只表达两种风格的形状差异，不占用读屏焦点或拦截点击。 */
@Composable
private fun ThemeSwatch(style: ThemeStyle) {
    val miuix = style == ThemeStyle.MIUIX
    val accent = if (miuix) Color(0xFF3482FF) else Color(0xFF36618E)
    val shape = RoundedCornerShape(if (miuix) 8.dp else 16.dp)
    Column(
        modifier = Modifier
            .size(width = 56.dp, height = 64.dp)
            .clearAndSetSemantics {}
            .background(MaterialTheme.colorScheme.surface, RoundedCornerShape(10.dp))
            .padding(8.dp),
        verticalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        Box(Modifier.width(24.dp).height(4.dp).background(MaterialTheme.colorScheme.onSurface, CircleShape))
        Box(Modifier.fillMaxWidth().height(16.dp).background(accent.copy(alpha = 0.16f), shape))
        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
            Box(Modifier.width(16.dp).height(3.dp).background(MaterialTheme.colorScheme.outline, CircleShape))
            Spacer(Modifier.weight(1f))
            Box(Modifier.size(width = 16.dp, height = 9.dp).background(accent, CircleShape))
        }
    }
}
