package dev.hole.app

import android.os.Build
import dev.hole.corebridge.CoreSnapshot
import dev.hole.app.ui.peerPathLabel
import dev.hole.app.ui.phaseLabel
import java.net.URI
import java.net.URLEncoder
import java.time.Instant

fun redactDiagnostic(text: String, secrets: List<String>): String {
    var result = text
    for (secret in secrets.filter { it.isNotEmpty() }.sortedByDescending { it.length }) {
        for (spelling in listOf(secret, URLEncoder.encode(secret, "UTF-8"))) result = result.replace(spelling, "[redacted]")
    }
    // Never retain query credentials from URLs in errors/logs.
    return result.replace(Regex("(?i)(wss?://[^\\s?]+)\\?[^\\s]+"), "$1?[redacted]").take(128 * 1024)
}

fun diagnosticReport(snapshot: CoreSnapshot, config: ConfigUiState, background: BackgroundInfo): String {
    val server = runCatching { URI(config.config.connection.serverUrl).let { "${it.scheme}://${it.host}${if (it.port > 0) ":${it.port}" else ""}${it.rawPath.orEmpty()}" } }.getOrDefault("")
    val report = buildString {
        appendLine("hole 诊断 · ${Instant.now()}")
        appendLine("APK ${BuildConfig.VERSION_NAME} (${BuildConfig.VERSION_CODE}) · Android ${Build.VERSION.RELEASE} / API ${Build.VERSION.SDK_INT}")
        appendLine("ABI ${Build.SUPPORTED_ABIS.joinToString()} · Core ${snapshot.coreVersion}")
        appendLine("API ${snapshot.apiVersion} · 会话协议 ${snapshot.sessionProtocol} · ${snapshot.transport}")
        appendLine("用户运行意图 ${snapshot.runRequested} · 核心 ${snapshot.engineState} · 信令 ${snapshot.signalState}")
        appendLine("启动 ${snapshot.startedAt} · 运行代次 ${snapshot.generation} · 传输代次 ${snapshot.transportGeneration}")
        appendLine("服务器 $server · 房间 ${config.config.connection.room} · 设备 ${config.config.connection.deviceName}")
        appendLine("重连 ${snapshot.reconnects} · 切网 ${snapshot.networkChanges} · 丢弃事件 ${snapshot.eventsDropped}")
        appendLine("网络 ${snapshot.network.transport} / ${snapshot.network.interfaceName} / handle=${snapshot.network.handle}")
        appendLine("可用 ${snapshot.network.available} · 验证 ${snapshot.network.validated} · 计费 ${snapshot.network.metered}")
        appendLine("地址 ${snapshot.network.addresses.joinToString()} · DNS ${snapshot.network.dns.joinToString()}")
        appendLine("电池不优化 ${background.batteryExempt} · 后台受限 ${background.backgroundRestricted} · 通知 ${background.notificationsEnabled}")
        appendLine("开机恢复 ${background.resumeAfterBoot} · ${background.lastResumeError}")
        snapshot.errorMessage?.let { appendLine("错误 ${snapshot.errorCode}: $it") }
        for (mapping in snapshot.mappings) {
            appendLine("映射 ${mapping.id}: ${mapping.role} / ${mapping.protocol} / ${mapping.state} / ${mapping.endpoint}")
            appendLine("  对端 ${mapping.peer} · 路径 ${mapping.path} · TCP ${mapping.tcpSessions} / UDP ${mapping.udpSessions}")
            appendLine("  活跃 TCP 会话累计读取 ${mapping.tcpReadBytes} / 写入 ${mapping.tcpWrittenBytes} / 待确认 ${mapping.replayBytes} 字节")
            mapping.error?.let { appendLine("  错误 $it") }
        }
        appendLine("设备线路（结构化状态，不含原始日志或候选凭据）")
        for (peer in snapshot.peers) {
            appendLine("设备 ${peer.peerId}：${peer.state} / ${peerPathLabel(peer)}")
            appendLine("  本机接入 ${peer.localRelayProtocol.ifBlank { peer.relayProtocol }.ifBlank { "非本机中继" }} · 对端接入 ${peer.remoteRelayProtocol.ifBlank { "未上报" }} · 中继使用方 ${peer.relaySide} · 策略 ${peer.relayPolicy.ifBlank { "兼容阶段" }}")
            appendLine("  本地 ${peer.localAddress} (${peer.localType}) · 对端 ${peer.remoteAddress} (${peer.remoteType})")
            appendLine("  通道 ${peer.activeChannels}/${peer.mappingCount} · RTT ${peer.rttMs} ms · 建连 ${peer.connectMs} ms")
            appendLine("  线路发送 ${peer.bytesSent} / 接收 ${peer.bytesReceived} 字节 · UDP 丢弃 ${peer.droppedDatagrams}")
            appendLine("  ${peer.transportId} / generation ${peer.generation} / phase ${peer.phase}（${phaseLabel(peer.phase)}） / retries ${peer.retryCount}")
            appendLine("  中继状态 ${peer.relayState} · 凭据到期 ${peer.turnExpiresAt} · 授权到期 ${peer.leaseUntil}")
            peer.errorCode?.let { appendLine("  当前问题 $it: ${peer.errorMessage.orEmpty()}") }
        }
    }
    return redactDiagnostic(report, listOf(config.password, config.token, config.turnCredential))
}
