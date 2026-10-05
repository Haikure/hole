package dev.hole.app.ui

import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.spring
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.selection.selectableGroup
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.unit.dp

internal val primaryRoutes = setOf("home", "config", "details", "voice", "settings")
private data class Destination(val route: String, val title: String, val icon: ImageVector)
private val destinations = listOf(
    Destination("home", "首页", Icons.Default.Home),
    Destination("config", "服务", Icons.Default.Build),
    Destination("details", "连接", Icons.Default.List),
    Destination("voice", "语音", Icons.Default.Call),
    Destination("settings", "设置", Icons.Default.Settings),
)

@Composable
fun AppNavigation(route: String, onNavigate: (String) -> Unit) {
    NavigationDock(destinations.map { it.title }, destinations.map { it.icon },
        destinations.indexOfFirst { it.route == route }.coerceAtLeast(0),
        onSelect = { onNavigate(destinations[it].route) })
}

@Composable
fun SettingsHub(onNavigate: (String) -> Unit) {
    val floatingInset = LocalFloatingInset.current
    HoleScaffold(title = "设置") { insets ->
        androidx.compose.foundation.lazy.LazyColumn(Modifier.padding(insets), contentPadding = PaddingValues(start = 16.dp, end = 16.dp, top = 16.dp, bottom = floatingInset + 16.dp), verticalArrangement = Arrangement.spacedBy(24.dp)) {
            item { HoleSettingsGroup("连接") {
                SettingsLink("房间与身份", "服务器、房间、设备名", Icons.Default.Home) { onNavigate("connection-settings") }
                SettingsLink("连接方式", "直连与中继", Icons.Default.Settings) { onNavigate("transport") }
                SettingsLink("后台运行", "电池与自动恢复", Icons.Default.Refresh) { onNavigate("background") }
            } }
            item { HoleSettingsGroup("应用") {
                SettingsLink("外观", "颜色与显示模式", Icons.Default.Edit) { onNavigate("appearance") }
                SettingsLink("配置文件", "导入与导出", Icons.Default.List) { onNavigate("transfer") }
            } }
        }
    }
}

@Composable
private fun SettingsLink(title: String, subtitle: String, icon: ImageVector, onClick: () -> Unit) {
    Surface(onClick = onClick, color = androidx.compose.ui.graphics.Color.Transparent, shape = MaterialTheme.shapes.medium) {
        ListItem(
            headlineContent = { Text(title) }, supportingContent = { Text(subtitle) },
            leadingContent = { Icon(icon, null, tint = MaterialTheme.colorScheme.primary) },
            trailingContent = { Text("›", style = MaterialTheme.typography.titleLarge) },
            colors = ListItemDefaults.colors(containerColor = androidx.compose.ui.graphics.Color.Transparent),
        )
    }
}
