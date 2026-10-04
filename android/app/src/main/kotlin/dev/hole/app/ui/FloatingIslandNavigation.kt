package dev.hole.app.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Home
import androidx.compose.material.icons.filled.Mic
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material.icons.filled.Tune
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp

@Composable
fun FloatingIslandNavigation(selected: String, onSelect: (String) -> Unit) {
    Surface(
        modifier = Modifier.fillMaxWidth().navigationBarsPadding().padding(horizontal = 16.dp, vertical = 10.dp),
        shape = MaterialTheme.shapes.extraLarge,
        tonalElevation = 5.dp,
        shadowElevation = 8.dp,
    ) {
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceEvenly) {
            IslandItem("home", "主页", Icons.Filled.Home, selected, onSelect)
            IslandItem("config", "配置", Icons.Filled.Tune, selected, onSelect)
            IslandItem("voice", "语音", Icons.Filled.Mic, selected, onSelect)
            IslandItem("settings", "设置", Icons.Filled.Settings, selected, onSelect)
        }
    }
}

@Composable
private fun IslandItem(route: String, label: String, icon: androidx.compose.ui.graphics.vector.ImageVector, selected: String, onSelect: (String) -> Unit) {
    NavigationBarItem(
        selected = route == selected,
        onClick = { onSelect(route) },
        icon = { Icon(icon, contentDescription = label) },
        label = { Text(label) },
    )
}
