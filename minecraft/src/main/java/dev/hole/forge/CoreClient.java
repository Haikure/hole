package dev.hole.forge;

import com.google.gson.JsonObject;

public interface CoreClient extends AutoCloseable {
    JsonObject invoke(String operation, JsonObject input) throws Exception;
    default void startAudio() throws Exception { throw new UnsupportedOperationException("语音桥接不可用"); }
    default void stopAudio() throws Exception {}
    default void pushAudio(long sequence, byte[] pcm) throws Exception {}
    default byte[] pullAudio() throws Exception { return new byte[0]; }
    default void muteAudio(boolean muted) throws Exception {
        JsonObject input=new JsonObject();input.addProperty("muted",muted);invoke("set_voice_muted",input);
    }
    void close();
    static CoreClient create() throws Exception {
        String platform=NativeCore.platform();
        return platform.startsWith("android-") ? new NativeCore() : new ProcessCore(platform);
    }
}
