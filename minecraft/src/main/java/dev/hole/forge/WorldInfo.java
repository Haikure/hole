package dev.hole.forge;

import com.google.gson.*;
import java.nio.charset.StandardCharsets;

/** Small, public world details; never includes player names or server addresses. */
public record WorldInfo(String description, String version, int online, int max) {
    public static final WorldInfo UNKNOWN=new WorldInfo("","",0,0);
    public WorldInfo {
        description=clean(description,192);version=clean(version,32);
        online=Math.max(0,Math.min(1000000,online));max=Math.max(0,Math.min(1000000,max));
    }
    public boolean known(){return !version.isEmpty();}
    public String summary(){return known()?online+"/"+max+" 人 · MC "+version:"人数 / 版本未知";}
    public JsonObject json(){
        JsonObject out=new JsonObject();out.addProperty("description",description);out.addProperty("version",version);
        out.addProperty("online",online);out.addProperty("max",max);return out;
    }
    static WorldInfo from(JsonObject service){
        try{
            JsonObject m=service.getAsJsonObject("minecraft");
            return m==null?UNKNOWN:new WorldInfo(m.get("description").getAsString(),m.get("version").getAsString(),m.get("online").getAsInt(),m.get("max").getAsInt());
        }catch(RuntimeException ignored){return UNKNOWN;}
    }
    private static String clean(String value,int limit){
        if(value==null)return "";
        StringBuilder result=new StringBuilder();int bytes=0;
        value=value.replaceAll("§.","");
        for(int point:value.codePoints().toArray()){
            if(Character.isISOControl(point))point=' ';
            String next=new String(Character.toChars(point));int size=next.getBytes(StandardCharsets.UTF_8).length;
            if(bytes+size>limit)break;result.append(next);bytes+=size;
        }
        return result.toString();
    }
}
