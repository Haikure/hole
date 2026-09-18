package dev.hole.app.config

import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertIs
import kotlin.test.assertNull
import kotlin.test.assertTrue

// 纯 JVM 测试：校验规则与请求转换。按仓库约定测试文件不加入提交。
class ConfigRulesTest {
    @Test
    fun literalIpParsingMirrorsGoSemantics() {
        assertTrue(isLiteralIp("127.0.0.1"))
        assertTrue(isLiteralIp("::1"))
        assertNull(parseLiteralIp("256.1.1.1"))
        assertNull(parseLiteralIp("example.com")) // 绝不触发 DNS
        assertNull(parseLiteralIp("1")) // Go ParseIP 不接受短格式
        assertNull(parseLiteralIp(""))
    }

    @Test
    fun publicIpv6MatchesCoreRule() {
        assertTrue(isPublicIpv6("240e:0123:4567::1"))
        assertTrue(isPublicIpv6("2001:db8::1"))
        assertFalse(isPublicIpv6("fe80::1")) // 链路本地
        assertFalse(isPublicIpv6("fd00::1")) // ULA
        assertFalse(isPublicIpv6("::1")) // 回环
        assertFalse(isPublicIpv6("::")) // 未指定
        assertFalse(isPublicIpv6("ff02::1")) // 组播
        assertFalse(isPublicIpv6("127.0.0.1")) // IPv4 不是 IPv6
    }

    @Test
    fun compositionWrapsIpv6() {
        assertEquals("tcp://127.0.0.1:22", composeServiceUrl("tcp", "127.0.0.1", 22))
        assertEquals("udp://[::1]:53", composeServiceUrl("udp", "::1", 53))
        assertEquals("tcp://ssh.example.com:22", composeServiceUrl("TCP", "ssh.example.com", 22))
        assertEquals("[::]:8080", composeExpose("::", 8080))
        assertEquals("::1", normalizeHostInput("[::1]"))
    }

    @Test
    fun portsAndDurationsAndIds() {
        assertEquals(8080, parsePort("8080"))
        assertNull(parsePort("0"))
        assertNull(parsePort("65536"))
        assertNull(parsePort("abc"))
        assertTrue(isValidDuration("10m"))
        assertTrue(isValidDuration("1h30m"))
        assertFalse(isValidDuration("10"))
        assertFalse(isValidDuration("-5m"))
        assertTrue(isValidMappingId("phone_service"))
        assertFalse(isValidMappingId("a b"))
        assertFalse(isValidMappingId(""))
        assertFalse(isValidMappingId("x".repeat(65)))
    }

    @Test
    fun serverUrlRequiresWsScheme() {
        assertNull(validateServerUrl("wss://host.example/ws"))
        assertNull(validateServerUrl("ws://192.168.1.2:8080/ws"))
        assertTrue(validateServerUrl("https://host.example") != null)
        assertTrue(validateServerUrl("wss://") != null)
        assertTrue(validateServerUrl("") != null)
    }

    @Test
    fun listFieldSplitsOnCommasAndWhitespace() {
        assertEquals(listOf("wlan0", "eth0"), splitListField("wlan0, eth0"))
        assertEquals(listOf("a", "b", "c"), splitListField("a，b； c"))
        assertEquals(emptyList(), splitListField("  "))
    }

    @Test
    fun idConflictRulesCheckEnabledEntriesOnly() {
        val provide = listOf(ProvideEntry("p1", id = "shared", host = "h", port = 1, enabled = true))
        val consume = listOf(
            ConsumeEntry("c1", id = "shared", host = "127.0.0.1", port = 2, enabled = true),
            ConsumeEntry("c2", id = "shared", host = "127.0.0.1", port = 3, enabled = false),
        )
        assertTrue(findIdConflict(provide, consume) != null) // 启用项跨列表冲突
        assertTrue(
            findIdConflict(
                provide,
                listOf(ConsumeEntry("c3", id = "shared", host = "127.0.0.1", port = 2, enabled = true)),
            ) != null,
        )
        assertNull(findIdConflict(provide, listOf(ConsumeEntry("c4", id = "other", host = "127.0.0.1", port = 4, enabled = true))))
        // 停用项允许与启用项同名
        assertNull(
            findIdConflict(
                provide,
                listOf(ConsumeEntry("c5", id = "shared", host = "127.0.0.1", port = 5, enabled = false)),
            ),
        )
        assertTrue(
            findIdConflict(
                listOf(ProvideEntry("p2", id = "dup", host = "h", port = 1, enabled = true), ProvideEntry("p3", id = "dup", host = "h", port = 2, enabled = true)),
                emptyList(),
            ) != null,
        )
    }

    @Test
    fun startRequestShapeMatchesMobileApi() {
        val stored = StoredConfig(
            connection = ConnectionSettings(
                serverUrl = "wss://host.example/ws",
                room = "ROOM",
                deviceName = "android-ab12",
                sessionTimeout = "10m",
                candidateInterfaces = listOf("wlan0"),
                candidateAddresses = listOf("240e:0123:4567::1"),
                connectionMode = "legacy",
            ),
            provide = listOf(
                ProvideEntry("p1", id = "phone_ssh", protocol = "tcp", host = "127.0.0.1", port = 22, enabled = true),
                ProvideEntry("p2", id = "disabled", protocol = "udp", host = "127.0.0.1", port = 53, enabled = false),
            ),
            consume = listOf(ConsumeEntry("c1", id = "remote_web", host = "::1", port = 18080, enabled = true)),
        )
        val request = toStartRequest(stored, password = "PW", token = "TOKEN")
        assertEquals(1, request.apiVersion)
        assertEquals("ROOM", request.config.room)
        assertEquals(listOf("wlan0"), request.config.candidateInterfaces)
        assertEquals(1, request.config.provide.size) // 停用项被过滤
        assertEquals("tcp://127.0.0.1:22", request.config.provide[0].service)
        assertEquals("[::1]:18080", request.config.consume[0].expose)

        val json = Json.encodeToString(request)
        val decoded: StartRequest = Json.decodeFromString(json)
        assertEquals(request, decoded)
        assertTrue(json.contains("\"provide\":[{")) 
        assertTrue(json.contains("\"session_timeout\":\"10m\""))
        assertTrue(!json.contains("enabled") && !json.contains("entryId")) // 宿主字段不进入核心配置
    }

    @Test
    fun startRequestRejectsIncompleteConfig() {
        fun stored(connection: ConnectionSettings) = StoredConfig(connection = connection)

        val missingPassword = runCatching {
            toStartRequest(
                stored(connection = ConnectionSettings(serverUrl = "wss://h/ws", room = "R", deviceName = "d")),
                password = "",
                token = "T",
            )
        }.exceptionOrNull()
        assertTrue(missingPassword is IllegalArgumentException && missingPassword.message?.contains("信令密码") == true)

    }
}
