package dev.hole.app.config

import java.net.Inet4Address
import java.net.Inet6Address
import java.net.InetAddress
import java.net.URI

// 纯 JVM 的校验与组合规则，语义对齐共享 Go 校验器（core/config.go）：
// UI 的提示是及时反馈，最终仍以核心校验为准。

private val IPV4_REGEX = Regex("^(\\d{1,3})(\\.\\d{1,3}){3}$")
private val DURATION_REGEX = Regex("^((\\d+(\\.\\d*)?|\\.\\d+)(ns|us|µs|μs|ms|s|m|h))+$")
private val DURATION_PART = Regex("(\\d+(?:\\.\\d*)?|\\.\\d+)(ns|us|µs|μs|ms|s|m|h)")
private val IPV6_CHARS = Regex("^[0-9a-fA-F:.]+$")

/** Go net.ParseIP 语义的字面 IP 解析；只接受字面地址，绝不触发 DNS。 */
fun parseLiteralIp(text: String): InetAddress? {
    val value = text.trim()
    if (value.isEmpty()) return null
    return try { if (IPV4_REGEX.matches(value)) {
        if (value.split('.').any { it.length > 1 && it.startsWith('0') }) return null
        if (value.split('.').any { it.toInt() > 255 }) return null
        InetAddress.getByName(value)
    } else if (value.contains(':') && IPV6_CHARS.matches(value)) {
        // 仅含十六进制/冒号/点的输入不会触发名称解析。
        InetAddress.getByName(value) as? Inet6Address
    } else {
        null
    } } catch (_: IllegalArgumentException) { null } catch (_: java.net.UnknownHostException) { null }
}

fun isLiteralIp(text: String): Boolean = parseLiteralIp(text) != null

/** 与 core/agent.go 的 isPublicIPv6 一致：全局单播、非 ULA、非链路本地、非回环。 */
fun isPublicIpv6(text: String): Boolean {
    val ip = parseLiteralIp(text) as? Inet6Address ?: return false
    if (ip.isAnyLocalAddress || ip.isLoopbackAddress || ip.isLinkLocalAddress ||
        ip.isMulticastAddress
    ) return false
    val first = ip.address[0].toInt() and 0xFF
    if (first and 0xFE == 0xFC) return false // fc00::/7 unique local
    return true
}

fun isIpv6Literal(text: String): Boolean = parseLiteralIp(text) is Inet6Address

/** IPv6 字面地址在 host:port 组合中必须加方括号；域名与 IPv4 原样返回。 */
fun wrapIpv6Host(host: String): String {
    val value = host.trim().removePrefix("[").removeSuffix("]")
    return if (isIpv6Literal(value)) "[$value]" else value
}

/** 存储时统一去掉方括号，组合时再按需包回。 */
fun normalizeHostInput(text: String): String =
    text.trim().removePrefix("[").removeSuffix("]").trim()

/** protocol://host:port；IPv6 输出 [::1]:22 形式，与 CLI 的 service 字段一致。 */
fun composeServiceUrl(protocol: String, host: String, port: Int): String =
    "${protocol.lowercase()}://${wrapIpv6Host(host)}:$port"

/** host:port；IPv6 输出 [::1]:18080 形式，与 CLI 的 expose 字段一致。 */
fun composeExpose(host: String, port: Int): String = "${wrapIpv6Host(host)}:$port"

fun parsePort(text: String): Int? = text.trim().toIntOrNull()?.takeIf { it in 1..65535 }

fun isValidDuration(text: String): Boolean {
    val value = text.trim()
    if (value.length > 128 || !DURATION_REGEX.matches(value)) return false
    val factors = mapOf("ns" to 1L, "us" to 1000L, "µs" to 1000L, "μs" to 1000L, "ms" to 1_000_000L,
        "s" to 1_000_000_000L, "m" to 60_000_000_000L, "h" to 3_600_000_000_000L)
    val nanos = DURATION_PART.findAll(value).fold(java.math.BigDecimal.ZERO) { total, part ->
        total + part.groupValues[1].toBigDecimal() * factors.getValue(part.groupValues[2]).toBigDecimal()
    }
    return nanos <= Long.MAX_VALUE.toBigDecimal()
}

fun isValidMappingId(id: String): Boolean {
    val value = id.trim()
    return value.isNotEmpty() && value.length <= 64 && value.none { it.isWhitespace() || it.isISOControl() }
}

/** 信令入口：默认 wss://；明文 ws:// 保留为显式开发配置。 */
fun validateServerUrl(text: String): String? {
    val value = text.trim()
    if (value.isEmpty()) return "信令服务器不能为空"
    val uri = try {
        URI(value)
    } catch (_: Exception) {
        return "不是合法的 URL"
    }
    if (uri.scheme?.lowercase() !in setOf("ws", "wss")) return "必须以 wss:// 或 ws:// 开头"
    if (uri.host.isNullOrBlank()) return "缺少主机名"
    if (uri.userInfo != null || uri.fragment != null) return "凭据使用独立字段，URL 不包含用户信息或片段"
    if (uri.port != -1 && uri.port !in 1..65535) return "服务器端口无效"
    return null
}

/** 高级设置中逗号/空白分隔的列表字段。 */
fun splitListField(text: String): List<String> =
    text.split(Regex("[,，;；\\s]+")).map { it.trim() }.filter { it.isNotEmpty() }

fun joinListField(values: List<String>): String = values.joinToString(", ")

/** 同类列表内不重复；同一有效配置中 provide 与 consume 不得共用 id（只检查启用项）。 */
fun findIdConflict(provide: List<ProvideEntry>, consume: List<ConsumeEntry>): String? {
    val enabledProvide = provide.filter { it.enabled }
    val enabledConsume = consume.filter { it.enabled }
    val seen = mutableSetOf<String>()
    for (entry in enabledProvide) {
        if (!seen.add(entry.id.trim())) return "provide 中存在重复的映射 ID \"${entry.id}\""
    }
    for (entry in enabledConsume) {
        if (entry.id.trim() in seen) {
            return "映射 ID \"${entry.id}\" 不能同时出现在启用的 provide 和 consume 中"
        }
        if (!seen.add(entry.id.trim())) return "consume 中存在重复的映射 ID \"${entry.id}\""
    }
    return null
}

/** 有效配置必须的连接字段；不完整时返回第一条原因。 */
fun findConnectionError(settings: ConnectionSettings, password: String?, token: String?): String? {
    validateServerUrl(settings.serverUrl)?.let { return it }
    if (settings.room.isBlank()) return "房间号不能为空"
    if (password.isNullOrBlank()) return "信令密码不能为空"
    if (token.isNullOrBlank()) return "房间密码不能为空"
    if (settings.deviceName.isBlank()) return "设备名不能为空"
    if (!isValidDuration(settings.sessionTimeout)) return "会话保留期限格式无效，例如 10m"
    return null
}

/**
 * 由 SavedConfig 生成 mobile facade API 1 的 Start 请求 JSON。
 * 只包含启用项；空集合输出 []；凭据由调用方解密后传入，不在此层接触 Keystore。
 * 校验失败抛 IllegalArgumentException，消息可直接展示。
 */
fun toStartRequest(
    stored: StoredConfig,
    password: String,
    token: String,
    turnCredential: String = "",
): StartRequest {
    findConnectionError(stored.connection, password, token)?.let { throw IllegalArgumentException(it) }
    findIdConflict(stored.provide, stored.consume)?.let { throw IllegalArgumentException(it) }
    val connection = stored.connection
    require(connection.connectionMode in setOf("auto", "ice", "legacy")) { "连接方式无效" }
    if (connection.connectionMode != "legacy") {
        require(connection.candidateAddresses.isEmpty()) { "手动 IPv6 候选地址只用于“仅 IPv6”模式；使用 ICE 前请清空此项" }
        require(!connection.serverUrl.startsWith("ws://") || connection.allowInsecureSignal) { "ICE 默认使用 wss://；本地 ws:// 测试需在连接方式中显式开启" }
    }
    require(connection.turn.mode in setOf("worker", "manual", "off")) { "中继来源无效" }
    if (connection.turn.mode == "manual") require(connection.turn.urls.isNotEmpty() && connection.turn.username.isNotBlank() && turnCredential.isNotEmpty()) { "手动中继需要服务器、用户名和凭据" }
    for (address in stored.connection.candidateAddresses) {
        if (!isPublicIpv6(address)) throw IllegalArgumentException("候选地址 \"$address\" 需要公网 IPv6 字面地址")
    }
    val provide = stored.provide.filter { it.enabled }.map {
        require(isValidMappingId(it.id)) { "provide 项缺少有效的映射 ID" }
        require(parsePort(it.port.toString()) == it.port) { "provide \"${it.id}\" 的端口无效" }
        require(it.host.isNotBlank()) { "provide \"${it.id}\" 缺少服务地址" }
        require(it.protocol in setOf("tcp", "udp")) { "仅支持 TCP 或 UDP" }
        CoreProvide(it.id.trim(), composeServiceUrl(it.protocol, it.host, it.port))
    }
    val consume = stored.consume.filter { it.enabled }.map {
        require(isValidMappingId(it.id)) { "consume 项缺少有效的映射 ID" }
        require(parsePort(it.port.toString()) == it.port) { "consume \"${it.id}\" 的端口无效" }
        require(isLiteralIp(it.host)) { "consume \"${it.id}\" 的监听地址必须是字面 IP，例如 127.0.0.1" }
        CoreConsume(it.id.trim(), composeExpose(it.host, it.port))
    }
    return StartRequest(
        serverUrl = stored.connection.serverUrl.trim(),
        config = CoreConfig(
            room = stored.connection.room.trim(),
            password = password,
            token = token,
            deviceName = stored.connection.deviceName.trim(),
            sessionTimeout = stored.connection.sessionTimeout.trim(),
            candidateInterfaces = stored.connection.candidateInterfaces,
            candidateAddresses = stored.connection.candidateAddresses,
            provide = provide,
            consume = consume,
            transport = connection.coreTransport(), ice = connection.ice, turn = connection.coreTurn(turnCredential),
        ),
    )
}
