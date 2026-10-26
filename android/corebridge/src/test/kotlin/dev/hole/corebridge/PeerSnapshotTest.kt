package dev.hole.corebridge

import kotlin.test.assertEquals
import org.json.JSONObject
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [26])
class PeerSnapshotTest {
    @Test fun fixedEndpointsAndRolesSurviveTheBridgeAndDefaultToEmpty() {
        val peer = PeerSnapshot.fromJson(JSONObject("""{"profile":"fixed-quic-mux-v1","state":"connecting","phase":"fixed","fixed_endpoint":"gateway.example:40000","listener_id":"server","dialer_id":"phone"}"""))
        assertEquals("gateway.example:40000", peer.fixedEndpoint)
        assertEquals("server", peer.listenerId)
        assertEquals("phone", peer.dialerId)
        assertEquals("connecting", peer.state)
        val old = PeerSnapshot.fromJson(JSONObject("{}"))
        assertEquals("", old.fixedEndpoint)
        assertEquals("", old.listenerId)
        assertEquals("", old.dialerId)
    }
    @Test fun actualRelayAccessAndPolicySurviveTheBridge() {
        val peer = PeerSnapshot.fromJson(JSONObject("""{"peer_id":"desktop","generation":"9007199254740993","path_type":"relay","local_relay_protocol":"tcp","relay_protocol":"tcp","relay_side":"local","relay_policy":"udp-tcp-tls-v1","phase":"relay_tcp_80"}"""))
        assertEquals("tcp", peer.relayProtocol)
        assertEquals("tcp", peer.localRelayProtocol)
        assertEquals("local", peer.relaySide)
        assertEquals("udp-tcp-tls-v1", peer.relayPolicy)
        assertEquals("relay_tcp_80", peer.phase)
        assertEquals("9007199254740993", peer.generation)
    }
    @Test fun oldSnapshotsDoNotInventANewPolicyOrRemoteProtocol() {
        val peer = PeerSnapshot.fromJson(JSONObject("""{"path_type":"relay","remote_type":"relay"}"""))
        assertEquals("", peer.relayPolicy)
        assertEquals("", peer.relaySide)
        assertEquals("", peer.relayProtocol)
    }
    @Test fun customRelayOrdersRoundAndFallbackSurviveTheBridge() {
        val peer = PeerSnapshot.fromJson(JSONObject("""{"relay_order":["udp"],"peer_relay_order":["tls_443","udp"],"relay_round":2,"relay_order_fallback":true}"""))
        assertEquals(listOf("udp"), peer.relayOrder)
        assertEquals(listOf("tls_443", "udp"), peer.peerRelayOrder)
        assertEquals(2, peer.relayRound)
        assertEquals(true, peer.relayOrderFallback)
    }
}
