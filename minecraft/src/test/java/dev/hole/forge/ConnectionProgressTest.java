package dev.hole.forge;

import com.google.gson.*;
import org.junit.Test;
import static org.junit.Assert.*;

public class ConnectionProgressTest {
    @Test public void stagesFollowHostTransportAndIgnoreOtherPeers(){
        JsonObject snapshot=JsonParser.parseString("{\"signal_state\":\"joined\",\"peer_transports\":[{\"peer_id\":\"other\",\"state\":\"active\",\"path_type\":\"direct\"}]}").getAsJsonObject();
        assertTrue(ConnectionProgress.from(snapshot,"host").stage().contains("等待房主"));
        JsonObject peer=JsonParser.parseString("{\"peer_id\":\"host\",\"state\":\"connecting\",\"phase\":\"direct\"}").getAsJsonObject();snapshot.getAsJsonArray("peer_transports").add(peer);
        assertTrue(ConnectionProgress.from(snapshot,"host").stage().contains("打洞"));
        peer.addProperty("phase","hole");assertTrue(ConnectionProgress.from(snapshot,"host").stage().contains("Hole 中继"));
        peer.addProperty("state","active");assertEquals("Hole 中继",ConnectionProgress.from(snapshot,"host").route());
        peer.addProperty("phase","direct");peer.addProperty("path_type","direct");assertEquals("直连",ConnectionProgress.from(snapshot,"host").route());
        snapshot.addProperty("signal_state","reconnecting");assertTrue(ConnectionProgress.from(snapshot,"host").stage().contains("信令"));
    }
}
