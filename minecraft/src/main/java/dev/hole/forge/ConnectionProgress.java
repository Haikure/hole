package dev.hole.forge;

import com.google.gson.*;

/** Reads local core snapshots only; no signaling requests. */
public record ConnectionProgress(String stage,String route) {
    static ConnectionProgress from(JsonObject snapshot,String device){
        String signal=text(snapshot,"signal_state");
        if(!signal.equals("joined") && !signal.equals("delegated"))return new ConnectionProgress("正在恢复信令连接","");
        JsonArray peers=snapshot.getAsJsonArray("peer_transports");
        if(peers!=null)for(JsonElement item:peers){
            JsonObject peer=item.getAsJsonObject();if(!device.equals(text(peer,"peer_id")))continue;
            String phase=text(peer,"phase"),path=text(peer,"path_type"),state=text(peer,"state");
            String route=phase.equals("hole")?"Hole 中继":phase.equals("peer_relay")?"设备中继":path.equals("relay")?"中继":path.equals("direct")?"直连":"";
            if(state.equals("active"))return new ConnectionProgress("通道已建立，正在准备本地映射",route);
            if(phase.equals("hole"))return new ConnectionProgress("正在连接 Hole 中继","");
            if(phase.equals("peer_relay"))return new ConnectionProgress("正在连接设备中继","");
            if(phase.startsWith("relay"))return new ConnectionProgress("正在尝试中继连接","");
            return new ConnectionProgress("正在尝试直连 / NAT 打洞","");
        }
        return new ConnectionProgress("正在等待房主响应","");
    }
    private static String text(JsonObject object,String key){return object.has(key)&&!object.get(key).isJsonNull()?object.get(key).getAsString():"";}
}
