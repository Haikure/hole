package dev.hole.forge;

import com.google.gson.*;
import org.junit.Test;
import static org.junit.Assert.*;

public class WorldEntryTest {
    @Test public void unicodeTitleAndAmbiguousProvider() {
        String device="mc-0123456789abcdef0123456789abcdef";
        String id=WorldEntry.serviceId(device,"海岛世界🌊和一段非常长的名字");
        assertTrue(id.length()<=64);
        JsonObject service=new JsonObject();service.addProperty("id",id);service.addProperty("protocol","tcp");service.addProperty("device_name",device);
        JsonArray services=new JsonArray();services.add(service);
        JsonObject room=new JsonObject();room.add("services",services);
        JsonObject snapshot=new JsonObject();snapshot.add("room",room);
        assertTrue(WorldEntry.fromSnapshot(snapshot,"guest").isEmpty());
        service.addProperty("kind","minecraft");
        service.addProperty("runtime_id","runtime-one");
        service.add("minecraft",new WorldInfo("欢迎\n来到§a海岛","1.20.1",2,20).json());
        assertTrue(WorldEntry.fromSnapshot(snapshot,"guest").get(0).name().startsWith("海岛世界🌊"));
        WorldEntry before=WorldEntry.fromSnapshot(snapshot,"guest").get(0);
        assertEquals("2/20 人 · MC 1.20.1",before.info().summary());
        assertEquals("欢迎 来到海岛",before.info().description());
        service.getAsJsonObject("minecraft").addProperty("online",3);
        assertEquals(before.key(),WorldEntry.fromSnapshot(snapshot,"guest").get(0).key());
        service.addProperty("runtime_id","runtime-two");
        assertNotEquals(before.key(),WorldEntry.fromSnapshot(snapshot,"guest").get(0).key());
        assertTrue(WorldEntry.fromSnapshot(snapshot,device).isEmpty());
        service.addProperty("kind","unknown");
        assertTrue(WorldEntry.fromSnapshot(snapshot,"guest").isEmpty());
        service.addProperty("kind","minecraft");
        JsonObject duplicate=service.deepCopy();duplicate.addProperty("device_name","another-host");services.add(duplicate);
        assertTrue(WorldEntry.fromSnapshot(snapshot,"guest").isEmpty());
    }
    @Test public void unrelatedServicesAreNotMinecraftWorlds() {
        JsonObject snapshot=JsonParser.parseString("{\"room\":{\"services\":[{\"id\":\"ssh\",\"protocol\":\"tcp\",\"device_name\":\"host\"},{\"id\":\"minecraft.invalid\",\"protocol\":\"tcp\",\"device_name\":\"host\"}]}}").getAsJsonObject();
        assertTrue(WorldEntry.fromSnapshot(snapshot,"guest").isEmpty());
    }
}
