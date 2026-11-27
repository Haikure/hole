package dev.hole.forge;

import com.google.gson.*;
import org.junit.Test;
import org.junit.Assume;
import static org.junit.Assert.*;

public class ProcessCoreTest {
    @Test public void binaryAudioInterleavesWithJSONAndMuteWithoutAudioHardware()throws Exception {
        Assume.assumeFalse(NativeCore.android());
        try(CoreClient core=CoreClient.create()){
            JsonObject request=JsonParser.parseString("{\"api_version\":1,\"server_url\":\"ws://127.0.0.1:9/ws\",\"config\":{\"room\":\"fixture\",\"password\":\"signal\",\"token\":\"secret\",\"device_name\":\"voice-test\",\"provide\":[],\"consume\":[],\"turn\":{\"mode\":\"off\"},\"ice\":{\"stun_urls\":[]},\"voice\":{\"enabled\":true}}}").getAsJsonObject();
            request.addProperty("server_url","wss://127.0.0.1:9/ws");core.invoke("start",request);
            long until=System.nanoTime()+java.util.concurrent.TimeUnit.SECONDS.toNanos(5);
            while(!core.invoke("snapshot",new JsonObject()).getAsJsonObject("voice").get("enabled").getAsBoolean() && System.nanoTime()<until)Thread.sleep(20);
            core.startAudio();byte[] pcm=new byte[1920];java.util.Arrays.fill(pcm,(byte)10);
            for(int i=0;i<12;i++){core.pushAudio(i,pcm);Thread.sleep(20);core.invoke("snapshot",new JsonObject());core.pullAudio();}
            assertTrue(core.invoke("snapshot",new JsonObject()).getAsJsonObject("voice").get("captured_frames").getAsLong()>0);
            core.muteAudio(true);assertTrue(core.invoke("snapshot",new JsonObject()).getAsJsonObject("voice").get("muted").getAsBoolean());
            core.stopAudio();assertEquals(1,core.invoke("snapshot",new JsonObject()).get("api_version").getAsInt());
        }
    }
    @Test public void embeddedCoreHasNoNetworkSideEffectsAndCloses() throws Exception {
        Assume.assumeFalse(NativeCore.android());
        try(CoreClient core=CoreClient.create()) {
            JsonObject snapshot=core.invoke("snapshot",new JsonObject());
            assertFalse(snapshot.get("run_requested").getAsBoolean());
            assertEquals(1,snapshot.get("api_version").getAsInt());
            assertEquals(0,snapshot.getAsJsonArray("mappings").size());
        }
    }
}
