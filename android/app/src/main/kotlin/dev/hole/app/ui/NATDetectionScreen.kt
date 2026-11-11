package dev.hole.app.ui

import androidx.compose.animation.animateContentSize
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import dev.hole.app.ui.HoleLazyColumn as LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowForward
import androidx.compose.material.icons.automirrored.filled.FactCheck
import androidx.compose.material.icons.automirrored.filled.ShowChart
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.luminance
import androidx.compose.ui.graphics.lerp
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.dp
import dev.hole.corebridge.NATDiagnostic

internal data class NATColors(val ink: Color, val surface: Color)

@Composable
internal fun natColors(tone: String): NATColors {
    val dark = MaterialTheme.colorScheme.surface.luminance() < .5f
    return when (tone) {
        "green" -> if (dark) NATColors(Color(0xFF8EE0AE), Color(0xFF183B2A)) else NATColors(Color(0xFF14643A), Color(0xFFE1F5E8))
        "blue" -> if (dark) NATColors(Color(0xFF9EC8FF), Color(0xFF1A3555)) else NATColors(Color(0xFF1B579A), Color(0xFFE5F0FF))
        "yellow" -> if (dark) NATColors(Color(0xFFF5D477), Color(0xFF40351A)) else NATColors(Color(0xFF755600), Color(0xFFFFF3C5))
        "red" -> if (dark) NATColors(Color(0xFFFFB4AB), Color(0xFF4A2526)) else NATColors(Color(0xFF9C3035), Color(0xFFFFE8E7))
        else -> NATColors(MaterialTheme.colorScheme.onSurfaceVariant, MaterialTheme.colorScheme.surfaceContainerHigh)
    }
}

private fun natIcon(name: String): ImageVector = when (name) {
    "mapping", "映射与过滤" -> Icons.Default.SwapHoriz
    "shield" -> Icons.Default.Shield
    "trend", "端口特征" -> Icons.AutoMirrored.Filled.ShowChart
    "steps" -> Icons.Default.LinearScale
    "samples", "检测记录" -> Icons.AutoMirrored.Filled.FactCheck
    "network", "检测时的网络" -> Icons.Default.Lan
    "STUN 服务明细" -> Icons.Default.Dns
    else -> Icons.Default.NetworkCheck
}

@Composable
fun NATDetectionScreen(report: NATDiagnostic, ready: Boolean, error: String?, onStart: () -> Unit,
    onCancel: () -> Unit, onBack: () -> Unit, onSettings: () -> Unit) {
    val bottom = LocalFloatingInset.current
    val colors = natColors(report.tone)
    HoleScaffold(title = "NAT 检测", navigationIcon = { HoleBackButton(onBack) }) { insets ->
        LazyColumn(Modifier.fillMaxSize().padding(insets).testTag("nat-detection"),
            contentPadding = PaddingValues(start = 20.dp, end = 20.dp, top = 12.dp, bottom = bottom + 24.dp),
            verticalArrangement = Arrangement.spacedBy(20.dp)) {
            item {
                RaisedPanel(color = lerp(MaterialTheme.colorScheme.surfaceContainerLow, colors.surface, .3f)) {
                    Column(Modifier.fillMaxWidth().animateContentSize().padding(20.dp), verticalArrangement = Arrangement.spacedBy(14.dp)) {
                        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                            Surface(shape = CircleShape, color = colors.ink.copy(alpha = .1f)) {
                                Icon(Icons.Default.NetworkCheck, null, Modifier.padding(10.dp).size(24.dp), tint = colors.ink)
                            }
                            Text(report.badge, style = MaterialTheme.typography.labelLarge, color = colors.ink)
                        }
                        Text(report.title, style = MaterialTheme.typography.headlineMedium, color = colors.ink)
                        Text(report.summary, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurface)
                        if (report.busy) LinearProgressIndicator(Modifier.fillMaxWidth().testTag("nat-progress"))
                        if (report.busy) {
                            HoleButton("取消检测", onCancel, Modifier.fillMaxWidth().testTag("nat-cancel"))
                        } else {
                            HoleButton(if (report.state == "idle") "开始检测" else "重新检测", onStart,
                                Modifier.fillMaxWidth().testTag("nat-start"), enabled = ready)
                        }
                    }
                }
            }
            if (!ready) item { Text("正在准备检测核心…", color = MaterialTheme.colorScheme.onSurfaceVariant) }
            if (!error.isNullOrBlank()) item { Text(error, color = MaterialTheme.colorScheme.error) }
            if (report.highlights.isNotEmpty()) item {
                BoxWithConstraints(Modifier.fillMaxWidth()) {
                    val columns = (maxWidth.value / (145 * LocalDensity.current.fontScale)).toInt().coerceIn(1, 3)
                    Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                        report.highlights.chunked(columns).forEach { row ->
                            Row(Modifier.fillMaxWidth().then(if (isWearUi()) Modifier else Modifier.height(IntrinsicSize.Min)), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                                row.forEach { tile ->
                                    RaisedPanel(Modifier.weight(1f).fillMaxHeight().testTag("nat-tile-${tile.icon}")) {
                                        Column(Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                                            Icon(natIcon(tile.icon), null, Modifier.size(22.dp), tint = colors.ink)
                                            Text(tile.label, style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                                            Text(tile.value, style = MaterialTheme.typography.titleMedium, color = MaterialTheme.colorScheme.onSurface)
                                        }
                                    }
                                }
                                repeat(columns - row.size) { Spacer(Modifier.weight(1f)) }
                            }
                        }
                    }
                }
            }
            if (report.state == "idle") item {
                HoleSettingsGroup("会检测什么") {
                    NATGuideRow("映射与过滤", "确认不同目标是否共用公网映射，以及哪些来源能够向你发送数据。")
                    NATGuideRow("端口特征", "查看端口规律、分配步长、端口保持和证据范围。")
                    NATGuideRow("网络与记录", "列出本机 IPv4 / IPv6 地址、样本、检测时间和无法判定的原因。")
                }
            }
            items(report.sections, key = { it.title }) { section ->
                Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                    Row(Modifier.padding(horizontal = 8.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        Icon(natIcon(section.title), null, Modifier.size(20.dp), tint = colors.ink)
                        Text(section.title, style = MaterialTheme.typography.titleSmall, color = colors.ink)
                    }
                    HoleCard(Modifier.fillMaxWidth()) {
                        Column(Modifier.padding(screenSpacing(16.dp))) {
                            section.fields.forEachIndexed { index, field ->
                                Column(Modifier.fillMaxWidth().padding(vertical = 10.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                                    Text(field.label, style = MaterialTheme.typography.labelLarge, color = colors.ink)
                                    SelectionContainer { Text(field.displayValue, style = MaterialTheme.typography.bodyLarge) }
                                    if (field.detail.isNotBlank()) Text(field.detail, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                                }
                                if (index < section.fields.lastIndex) HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant.copy(alpha = .45f))
                            }
                        }
                    }
                }
            }
            item {
                Text("检测结果反映本次 IPv4 UDP 采样。未知表示证据不足；IPv6 地址和 NAT 类型都不能替代实际直连检查。",
                    style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                HoleTextButton("配置检测服务", onSettings)
            }
        }
    }
}

@Composable
private fun NATGuideRow(title: String, text: String) {
    Column(Modifier.padding(vertical = 10.dp), verticalArrangement = Arrangement.spacedBy(5.dp)) {
        Text(title, style = MaterialTheme.typography.titleSmall)
        Text(text, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

@Composable
fun NATDetectionEntry(report: NATDiagnostic, onClick: () -> Unit) {
    val colors = natColors(report.tone)
    RaisedPanel {
        Row(Modifier.fillMaxWidth().clickable(onClick = onClick).padding(screenSpacing(18.dp)), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(14.dp)) {
            Surface(shape = CircleShape, color = colors.surface) {
                Icon(Icons.Default.NetworkCheck, null, Modifier.padding(10.dp), tint = colors.ink)
            }
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Text("NAT 检测", style = MaterialTheme.typography.titleMedium)
                Text(if (report.state == "idle") "了解网络的直连条件" else report.title, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            Icon(Icons.AutoMirrored.Filled.ArrowForward, "查看 NAT 检测", Modifier.size(20.dp))
        }
    }
}
