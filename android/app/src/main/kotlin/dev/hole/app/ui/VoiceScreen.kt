package dev.hole.app.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Call
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.dp
import dev.hole.app.ConfigUiState
import dev.hole.corebridge.CoreSnapshot
import dev.hole.corebridge.VoiceMemberSnapshot
import dev.hole.corebridge.VoicePeerSnapshot

@Composable
fun VoiceScreen(
    snapshot: CoreSnapshot,
    configState: ConfigUiState,
    commandError: String? = null,
    microphoneGranted: Boolean = false,
    onVoiceEnabledChange: (Boolean) -> Unit,
    onMutedChange: (Boolean) -> Unit,
    onRequestMicrophone: () -> Unit,
    onBack: () -> Unit,
    onToggleRun: (Boolean) -> Unit = {},
) {
    val voice = snapshot.voice
    val floatingInset = LocalFloatingInset.current
    HoleScaffold(
        title = "语音",
        largeTitle = true,
    ) { insets ->
        LazyColumn(
            Modifier.fillMaxWidth().testTag("voice-screen").padding(insets).padding(horizontal = 20.dp),
            verticalArrangement = Arrangement.spacedBy(18.dp),
        ) {
            item {
                HoleCard(Modifier.fillMaxWidth(), containerColor = MaterialTheme.colorScheme.surfaceContainerLow) {
                    Column(Modifier.padding(22.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
                        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                            Box(
                                Modifier.size(54.dp).clip(CircleShape),
                                contentAlignment = Alignment.Center,
                            ) {
                                androidx.compose.material3.Surface(
                                    modifier = Modifier.matchParentSize(),
                                    shape = CircleShape,
                                    color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.1f),
                                ) {}
                                MicrophoneGlyph(MaterialTheme.colorScheme.onSurface)
                            }
                            Column(Modifier.weight(1f).padding(start = 14.dp)) {
                                Text("房间语音", style = MaterialTheme.typography.titleLarge, color = MaterialTheme.colorScheme.onSurface)
                                Text(voiceStateLabel(voice.state), style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.82f))
                            }
                            HoleSwitch(
                                checked = configState.config.voice.enabled,
                                onCheckedChange = { if (it) onRequestMicrophone() else onVoiceEnabledChange(false) },
                                label = "启用房间语音",
                                enabled = configState.loaded,
                            )
                        }
                        HoleHelp("通话说明", "使用本机麦克风和扬声器与当前房间成员通话，需要先启动连接。")
                        if (!microphoneGranted && configState.config.voice.enabled) {
                            HoleButton("开启麦克风权限", onRequestMicrophone, secondary = true)
                        }
                        commandError?.takeIf { it.contains("音频") || it.contains("麦克风") || it.contains("语音") }?.let {
                            Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
                        }
                        if (voice.enabled || configState.config.voice.enabled) {
                            androidx.compose.material3.HorizontalDivider(color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.16f))
                            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                                Icon(Icons.Filled.Call, contentDescription = null, tint = MaterialTheme.colorScheme.onSurface)
                                Column(Modifier.weight(1f).padding(start = 10.dp)) {
                                    Text(if (voice.muted) "麦克风已静音" else if (voice.captureState == "capturing") "麦克风正在采集" else "等待麦克风音频", style = MaterialTheme.typography.titleSmall, color = MaterialTheme.colorScheme.onSurface)
                                    Text("${voice.members.count { it.voice }} 人正在使用语音", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.78f))
                                }
                                HoleButton(
                                    text = if (voice.muted) "取消静音" else "静音",
                                    onClick = { onMutedChange(!voice.muted) },
                                    enabled = snapshot.runRequested && voice.enabled,
                                    secondary = true,
                                )
                            }
                            Text(
                                "采集 ${voice.capturedFrames} · 混音 ${voice.mixedFrames} · 播放 ${voice.playbackFrames}",
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                            if (voice.audioState == "running") {
                                Text("${voice.audioRoute.ifBlank { "系统通话设备" }} · 回声消除${if (voice.aecEnabled) "已启用" else "不可用"} · 降噪${if (voice.nsEnabled) "已启用" else "不可用"}", style = MaterialTheme.typography.bodySmall)
                            }
                            voice.audioError?.let { Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall) }
                        }
                    }
                }
            }
            item { VoiceSectionTitle("房间成员", voice.members.size) }
            if (voice.members.isEmpty()) {
                item { HoleEmptyCard(if (snapshot.runRequested) "暂无房间成员" else "连接尚未启动") }
            } else {
                items(voice.members, key = { "member-${it.deviceName}-${it.transportId}" }) { member -> Box(Modifier.animateItem()) { VoiceMemberRow(member) } }
            }
            item { VoiceSectionTitle("语音线路", voice.peers.size) }
            if (voice.peers.isEmpty()) {
                item { HoleEmptyCard("暂无语音线路") }
            } else {
                items(voice.peers, key = { "peer-${it.peerId}-${it.transportId}-${it.generation}" }) { peer -> Box(Modifier.animateItem()) { VoicePeerRow(peer) } }
            }
            item { Box(Modifier.padding(bottom = floatingInset + 8.dp)) }
        }
    }
}

@Composable
private fun MicrophoneGlyph(color: androidx.compose.ui.graphics.Color) {
    Canvas(Modifier.size(24.dp)) {
        val stroke = size.minDimension * 0.09f
        drawRoundRect(
            color = color,
            topLeft = Offset(size.width * 0.34f, size.height * 0.08f),
            size = Size(size.width * 0.32f, size.height * 0.52f),
            cornerRadius = CornerRadius(size.width * 0.16f),
        )
        drawArc(
            color = color,
            startAngle = 0f,
            sweepAngle = 180f,
            useCenter = false,
            topLeft = Offset(size.width * 0.16f, size.height * 0.25f),
            size = Size(size.width * 0.68f, size.height * 0.58f),
            style = Stroke(width = stroke, cap = StrokeCap.Round),
        )
        drawLine(
            color = color,
            start = Offset(size.width * 0.5f, size.height * 0.82f),
            end = Offset(size.width * 0.5f, size.height * 0.94f),
            strokeWidth = stroke,
            cap = StrokeCap.Round,
        )
        drawLine(
            color = color,
            start = Offset(size.width * 0.36f, size.height * 0.94f),
            end = Offset(size.width * 0.64f, size.height * 0.94f),
            strokeWidth = stroke,
            cap = StrokeCap.Round,
        )
    }
}

@Composable
private fun VoiceSectionTitle(title: String, count: Int) {
    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        Text(title, style = MaterialTheme.typography.titleLarge, modifier = Modifier.weight(1f))
        Text("$count", style = MaterialTheme.typography.titleMedium, color = MaterialTheme.colorScheme.primary, fontWeight = FontWeight.SemiBold)
    }
}

@Composable
private fun VoiceMemberRow(member: VoiceMemberSnapshot) {
    HoleCard(Modifier.fillMaxWidth()) {
        Row(Modifier.fillMaxWidth().padding(16.dp), verticalAlignment = Alignment.CenterVertically) {
            androidx.compose.material3.Surface(
                modifier = Modifier.size(44.dp),
                shape = CircleShape,
                color = MaterialTheme.colorScheme.secondaryContainer,
                contentColor = MaterialTheme.colorScheme.onSecondaryContainer,
            ) {
                Box(contentAlignment = Alignment.Center) {
                    Text(member.deviceName.firstOrNull()?.uppercase() ?: "?", style = MaterialTheme.typography.titleMedium)
                }
            }
            Column(Modifier.weight(1f).padding(start = 13.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Text(member.deviceName.ifBlank { "未命名设备" } + if (member.local) "（本机）" else "", style = MaterialTheme.typography.titleMedium)
                Text(
                    listOf(memberVoiceLabel(member.voice), if (member.local) "本机音频" else peerStateLabel(member.transportState), mediaStateLabel(member.mediaState)).joinToString(" · "),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }
}

@Composable
private fun VoicePeerRow(peer: VoicePeerSnapshot) {
    HoleCard(Modifier.fillMaxWidth()) {
        Column(Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                Icon(Icons.Filled.Call, contentDescription = null, tint = MaterialTheme.colorScheme.primary)
                Text(peer.peerId.ifBlank { "未知设备" }, style = MaterialTheme.typography.titleMedium, modifier = Modifier.padding(start = 10.dp))
            }
            Text(
                "${peerStateLabel(peer.state)} · ${peer.path.ifBlank { "路径待确认" }} · ${peer.bitrate} bit/s",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Text(
                "丢包 ${peer.packetLoss} · 队列 ${peer.queueDepth} · 抖动 ${peer.jitterDepth}",
                style = MaterialTheme.typography.labelMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Text("${mediaStateLabel(peer.mediaState)} · 发送 ${peer.sentFrames} · 解码 ${peer.decodedFrames} · 补帧 ${peer.concealedFrames}", style = MaterialTheme.typography.labelMedium)
        }
    }
}

private fun voiceStateLabel(state: String): String = when (state) {
    "active", "running" -> "语音混音运行中"
    "starting", "starting_audio" -> "正在准备音频"
    "waiting" -> "等待房间成员"
    "connecting" -> "语音线路连接中"
    "ready" -> "线路已连接，等待音频"
    "error", "failed" -> "音频初始化失败"
    else -> "语音未启用"
}

private fun memberVoiceLabel(enabled: Boolean): String = if (enabled) "已开启语音" else "未开启语音"

private fun mediaStateLabel(state: String): String = when (state) {
    "active" -> "音频收发中"
    "sending" -> "音频发送中"
    "receiving" -> "音频接收中"
    "capturing" -> "麦克风采集中"
    "muted" -> "麦克风已静音"
    "paused" -> "媒体异常"
    "offline" -> "媒体离线"
    else -> "等待音频"
}

private fun peerStateLabel(state: String): String = when (state) {
    "active", "connected" -> "已连接"
    "connecting", "dialing" -> "连接中"
    "failed", "error" -> "连接失败"
    else -> state.ifBlank { "等待连接" }
}
