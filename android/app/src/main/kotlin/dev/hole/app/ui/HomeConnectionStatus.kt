package dev.hole.app.ui

import androidx.compose.animation.*
import androidx.compose.animation.core.*
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Warning
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.dropShadow
import androidx.compose.ui.graphics.*
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.shadow.Shadow
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.*
import androidx.compose.ui.unit.DpOffset
import androidx.compose.ui.unit.dp
import dev.hole.corebridge.CoreSnapshot

internal enum class HomeConnectionPhase(val title: String, val hint: String, val pending: Boolean = false) {
    OFFLINE("未连接", "连接尚未启动"),
    SIGNAL("正在连接信令", "正在联系服务器", true),
    JOINING("正在加入房间", "信令已连接", true),
    RECONNECTING("正在重连信令", "等待恢复房间连接", true),
    WAITING_NETWORK("等待网络", "网络恢复后自动重连", true),
    CONNECTED("已连接", "已加入房间"),
    ERROR("连接异常", "请检查网络或连接设置"),
    STOPPING("正在停止", "正在关闭连接", true),
}

internal fun homeConnectionPhase(snapshot: CoreSnapshot): HomeConnectionPhase = when {
    snapshot.engineState == "stopping" -> HomeConnectionPhase.STOPPING
    snapshot.engineState == "error" -> HomeConnectionPhase.ERROR
    !snapshot.runRequested -> HomeConnectionPhase.OFFLINE
    snapshot.signalState == "error" -> HomeConnectionPhase.ERROR
    snapshot.engineState == "recovering" -> HomeConnectionPhase.WAITING_NETWORK
    snapshot.signalState == "joined" -> HomeConnectionPhase.CONNECTED
    snapshot.signalState == "joining" -> HomeConnectionPhase.JOINING
    snapshot.signalState == "reconnecting" -> HomeConnectionPhase.RECONNECTING
    else -> HomeConnectionPhase.SIGNAL
}

@Composable
fun HomeConnectionStatus(snapshot: CoreSnapshot) {
    val phase = homeConnectionPhase(snapshot)
    val tone = when {
        phase == HomeConnectionPhase.CONNECTED -> Color(0xFF17624F)
        phase == HomeConnectionPhase.ERROR -> Color(0xFF9B3039)
        phase.pending -> Color(0xFF80541A)
        else -> Color(0xFF364256)
    }
    val color by animateColorAsState(tone, tween(420), label = "connection status color")
    val shape = MaterialTheme.shapes.extraLarge
    BoxWithConstraints(Modifier.fillMaxWidth().testTag("home-connection-status")
        .semantics(mergeDescendants = true) { stateDescription = phase.title; liveRegion = LiveRegionMode.Polite }
        .dropShadow(shape, Shadow(14.dp, color.copy(alpha = .22f), offset = DpOffset(0.dp, 6.dp)))
        .clip(shape).background(Brush.linearGradient(listOf(lerp(color, Color.White, .06f), color)))) {
        val stacked = maxWidth < 300.dp || LocalDensity.current.fontScale > 1.3f
        AnimatedContent(phase, transitionSpec = {
            (fadeIn(tween(220)) + slideInVertically(tween(300)) { it / 8 }) togetherWith
                (fadeOut(tween(140)) + slideOutVertically(tween(200)) { -it / 8 })
        }, label = "connection status transition") { displayed ->
            val detail = if (displayed == HomeConnectionPhase.CONNECTED) {
                if (snapshot.mappings.any { it.state == "active" }) "已加入房间 · 服务通道可用" else "已加入房间 · 等待对端"
            } else displayed.hint
            if (stacked) {
                Column(Modifier.fillMaxWidth().padding(22.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
                    ConnectionStatusGlyph(displayed)
                    StatusText(displayed.title, detail)
                }
            } else {
                Row(Modifier.fillMaxWidth().padding(22.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(18.dp)) {
                    ConnectionStatusGlyph(displayed)
                    Column(Modifier.weight(1f)) { StatusText(displayed.title, detail) }
                }
            }
        }
    }
}

@Composable
private fun StatusText(title: String, detail: String) {
    Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
        Text(title, style = MaterialTheme.typography.headlineMedium, color = Color.White)
        Text(detail, style = MaterialTheme.typography.bodyMedium, color = Color.White.copy(alpha = .88f))
    }
}

@Composable
private fun ConnectionStatusGlyph(phase: HomeConnectionPhase) {
    Box(Modifier.size(60.dp).background(Color.White.copy(alpha = .12f), CircleShape), contentAlignment = Alignment.Center) {
        when {
            phase.pending -> CircularProgressIndicator(Modifier.size(48.dp), color = Color.White, strokeWidth = 3.dp, trackColor = Color.White.copy(alpha = .2f))
            phase == HomeConnectionPhase.CONNECTED -> {
                val progress = remember { Animatable(0f) }
                LaunchedEffect(Unit) { progress.animateTo(1f, tween(360, easing = FastOutSlowInEasing)) }
                Canvas(Modifier.size(30.dp)) {
                    val path = Path().apply {
                        moveTo(size.width * .12f, size.height * .52f)
                        lineTo(size.width * .39f, size.height * .78f)
                        lineTo(size.width * .88f, size.height * .22f)
                    }
                    val measure = PathMeasure().apply { setPath(path, false) }
                    val segment = Path()
                    measure.getSegment(0f, measure.length * progress.value, segment, true)
                    drawPath(segment, Color.White, style = Stroke(3.dp.toPx(), cap = StrokeCap.Round, join = StrokeJoin.Round))
                }
            }
            else -> Icon(if (phase == HomeConnectionPhase.ERROR) Icons.Default.Warning else Icons.Default.Close, null, Modifier.size(28.dp), tint = Color.White)
        }
    }
}
