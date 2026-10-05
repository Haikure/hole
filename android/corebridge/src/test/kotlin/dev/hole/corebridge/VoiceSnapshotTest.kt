package dev.hole.corebridge

import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue
import org.json.JSONObject
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [26])
class VoiceSnapshotTest {
    private fun snapshot(voice: String): VoiceSnapshot = CoreSnapshot.fromJson(JSONObject()
        .put("api_version", 1).put("session_protocol", 2).put("core_version", "test")
        .put("configured", true).put("run_requested", true).put("engine_state", "running")
        .put("signal_state", "online").put("mappings", org.json.JSONArray())
        .put("voice", JSONObject(voice)).toString()).voice

    @Test fun localIdentityAndMediaCountersSurviveTheBridge() {
        val voice = snapshot("""{
            "enabled":true,"state":"ready","capture_state":"capturing","captured_frames":"9007199254740993","mixed_frames":"40",
            "members":[{"device_name":"self","voice":true,"local":true,"transport_state":"local","media_state":"capturing"}],
            "peers":[{"peer_id":"remote","state":"active","media_state":"receiving","sent_frames":"4","received_frames":"5","decoded_frames":"3","concealed_frames":"2"}]
        }""")
        assertEquals("ready", voice.state)
        assertEquals("capturing", voice.captureState)
        assertEquals("9007199254740993", voice.capturedFrames)
        assertEquals("40", voice.mixedFrames)
        assertTrue(voice.members.single().local)
        assertEquals("receiving", voice.peers.single().mediaState)
        assertEquals("4", voice.peers.single().sentFrames)
        assertEquals("5", voice.peers.single().receivedFrames)
        assertEquals("3", voice.peers.single().decodedFrames)
        assertEquals("2", voice.peers.single().concealedFrames)
    }

    @Test fun oldSnapshotsDoNotClaimMediaActivity() {
        val voice = snapshot("""{"enabled":true,"peers":[{"state":"active"}],"members":[{"device_name":"remote"}]}""")
        assertFalse(voice.members.single().local)
        assertEquals("waiting", voice.captureState)
        assertEquals("waiting", voice.peers.single().mediaState)
        assertEquals("0", voice.peers.single().decodedFrames)
        assertEquals("stopped", voice.audioState)
    }
}
