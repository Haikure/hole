package dev.hole.corebridge

import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [26])
class PortMappingSnapshotTest {
    @Test fun peerMappingIsParsedAndDisplayedWithoutJson() {
        val peer = PeerSnapshot.fromJson(JSONObject("""{"port_mapping":{"status":"mapped","protocol":"nat-pmp","address":"203.0.113.5:40000","lease_seconds":300}}"""))
        assertEquals("已映射 · NAT-PMP · 203.0.113.5:40000 · 租期 300 秒", peer.portMapping.description())
        assertEquals(300L, peer.portMapping.leaseSeconds)
        val permanent = PortMappingSnapshot.fromJson(JSONObject("""{"status":"mapped","protocol":"upnp","address":"203.0.113.5:40000"}"""))
        assertEquals("已映射 · UPnP · 203.0.113.5:40000 · 永久租期", permanent.description())
    }

    @Test fun missingUnavailableAndFutureStatesRemainReadable() {
        for (json in listOf("{}", """{"port_mapping":null}""", """{"port_mapping":{"status":""}}""")) {
            assertEquals("暂无", PeerSnapshot.fromJson(JSONObject(json)).portMapping.description())
        }
        val unavailable = PortMappingSnapshot.fromJson(JSONObject("""{"status":"unavailable","reason":"网关不支持"}"""))
        assertEquals("不可用", unavailable.description())
        assertEquals("网关不支持", unavailable.reason)
        assertEquals("未知", unavailable.copy(status = "future_state").description())
    }
}
