package dev.hole.forge;

import com.google.gson.*;
import java.nio.charset.StandardCharsets;
import java.util.*;

public record WorldEntry(String id, String device, String name, String runtimeId, WorldInfo info) {
    public String key(){return device+"/"+runtimeId+"/"+id;}
    public static String serviceId(String device, String title) {
        StringBuilder shortTitle=new StringBuilder();
        for(int point:title.codePoints().toArray()) {
            String next=shortTitle.toString()+new String(Character.toChars(point));
            if(next.getBytes(StandardCharsets.UTF_8).length>27) break;
            shortTitle.appendCodePoint(point);
        }
        return "minecraft."+device.substring(3,19)+"."+Base64.getUrlEncoder().withoutPadding().encodeToString(shortTitle.toString().getBytes(StandardCharsets.UTF_8));
    }
    public static List<WorldEntry> fromSnapshot(JsonObject snapshot,String ownDevice) {
        List<WorldEntry> result=new ArrayList<>();
        if(!snapshot.has("room") || !snapshot.get("room").isJsonObject()) return result;
        JsonArray services=snapshot.getAsJsonObject("room").getAsJsonArray("services");
        if(services==null) return result;
        Map<String,Integer> counts=new HashMap<>();
        for(JsonElement item:services) {
            if(item.isJsonObject() && item.getAsJsonObject().has("id")) counts.merge(item.getAsJsonObject().get("id").getAsString(),1,Integer::sum);
        }
        for(JsonElement item:services) {
            try {
                JsonObject service=item.getAsJsonObject();
                if(!service.has("kind") || !"minecraft".equals(service.get("kind").getAsString())) continue;
                String id=service.get("id").getAsString(), device=service.get("device_name").getAsString();
                if(device.equals(ownDevice) || !service.get("protocol").getAsString().equals("tcp") || counts.get(id)!=1) continue;
                if(!id.matches("minecraft\\.[a-f0-9]{16}\\.[A-Za-z0-9_-]{0,36}")) continue;
                String name=new String(Base64.getUrlDecoder().decode(id.substring(27)),StandardCharsets.UTF_8).replaceAll("[\\p{Cntrl}§]","");
                String runtime=service.has("runtime_id")?service.get("runtime_id").getAsString():"";
                result.add(new WorldEntry(id,device,name.isBlank()?"Minecraft 世界":name,runtime,WorldInfo.from(service)));
            } catch(RuntimeException ignored) { }
        }
        return List.copyOf(result);
    }
}
