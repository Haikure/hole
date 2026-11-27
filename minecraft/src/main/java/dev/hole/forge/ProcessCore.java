package dev.hole.forge;

import com.google.gson.*;
import java.io.*;
import java.nio.charset.StandardCharsets;
import java.util.Map;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicLong;
import java.nio.ByteBuffer;
import java.nio.ByteOrder;

final class ProcessCore implements CoreClient {
    private final Process process;
    private final OutputStream writer;
    private final ArrayBlockingQueue<byte[]> playback=new ArrayBlockingQueue<>(4);
    private volatile long audioId;
    private final AtomicLong sequence=new AtomicLong();
    private final Map<String,CompletableFuture<JsonObject>> pending=new ConcurrentHashMap<>();
    private volatile boolean closed;
    ProcessCore(String platform) throws Exception {
        String file=platform+"/hole-core"+(platform.startsWith("windows-")?".exe":"");
        process=new ProcessBuilder(NativeCore.extract(file,true).toString()).start();
        writer=process.getOutputStream();
        Thread responses=new Thread(this::read,"Hole core responses"); responses.setDaemon(true); responses.start();
        Thread diagnostics=new Thread(()-> { try (InputStream in=process.getErrorStream()) { in.transferTo(OutputStream.nullOutputStream()); } catch(IOException ignored) {} },"Hole core diagnostics");
        diagnostics.setDaemon(true); diagnostics.start();
        try {
            JsonObject hello=invoke("hello",new JsonObject());
            if (hello.get("bridge_version").getAsInt()!=1 || hello.get("api_version").getAsInt()!=1) throw new IOException("Hole 桥接版本不兼容");
        } catch(Exception e) { close(); throw e; }
    }
    private void read() {
        try (InputStream in=new BufferedInputStream(process.getInputStream())) {
            ByteArrayOutputStream line=new ByteArrayOutputStream();int c;
            while ((c=in.read())!=-1) {
                if(c==0 && line.size()==0) {
                    byte[] packet=in.readNBytes(1936);
                    if(packet.length!=1936)throw new EOFException();
                    long stream=ByteBuffer.wrap(packet).order(ByteOrder.LITTLE_ENDIAN).getLong();
                    synchronized(playback){if(stream==audioId && stream!=0) {
                        byte[] pcm=java.util.Arrays.copyOfRange(packet,16,1936);
                        if(!playback.offer(pcm)){playback.poll();playback.offer(pcm);}
                    }}
                    continue;
                }
                if (c=='\n') {
                    JsonObject object=JsonParser.parseString(line.toString(StandardCharsets.UTF_8)).getAsJsonObject(); line.reset();
                    if (object.has("id") && !object.get("id").isJsonNull()) {
                        CompletableFuture<JsonObject> future=pending.remove(object.get("id").getAsString());
                        if (future!=null) future.complete(object);
                    }
                } else {
                    if(line.size()>=2*1024*1024) throw new IOException("Hole 响应过大");
                    line.write(c);
                }
            }
        } catch(Exception ignored) {
            // Raw protocol frames can contain credentials; never log them.
        } finally {
            closed=true;
            pending.values().forEach(f->f.completeExceptionally(new IOException("Hole 核心进程已退出")));
            pending.clear(); process.destroy();
        }
    }
    public JsonObject invoke(String operation,JsonObject input) throws Exception {
        if(closed) throw new IOException("Hole 核心进程已关闭");
        String id=Long.toString(sequence.incrementAndGet());
        JsonObject request=new JsonObject();
        request.addProperty("jsonrpc","2.0"); request.addProperty("id",id);
        request.addProperty("method",operation.equals("apply")?"apply_config":operation.equals("network")?"network_changed":operation);
        if(!input.entrySet().isEmpty()) request.add("params",input);
        CompletableFuture<JsonObject> future=new CompletableFuture<>(); pending.put(id,future);
        try {
            synchronized(writer) { writer.write(request.toString().getBytes(StandardCharsets.UTF_8)); writer.write('\n'); writer.flush(); }
            JsonObject response=future.get(20,TimeUnit.SECONDS);
            if(response.has("error")) throw new IOException(response.getAsJsonObject("error").toString());
            JsonElement result=response.get("result");
            if(operation.equals("snapshot") && result!=null && result.isJsonObject()) return result.getAsJsonObject().getAsJsonObject("snapshot");
            return result!=null && result.isJsonObject()?result.getAsJsonObject():new JsonObject();
        } finally { pending.remove(id); }
    }
    public void startAudio() throws Exception {
        JsonObject input=new JsonObject();input.addProperty("enabled",true);
        long stream=invoke("set_voice_audio",input).get("stream_id").getAsLong();
        synchronized(playback){playback.clear();audioId=stream;}
    }
    public void stopAudio() throws Exception {
        synchronized(playback){audioId=0;playback.clear();}JsonObject input=new JsonObject();input.addProperty("enabled",false);invoke("set_voice_audio",input);
    }
    public void pushAudio(long sequence,byte[] pcm) throws Exception {
        long stream=audioId;if(stream==0 || pcm.length!=1920)return;
        byte[] packet=ByteBuffer.allocate(1937).order(ByteOrder.LITTLE_ENDIAN).put((byte)0).putLong(stream).putLong(sequence).put(pcm).array();
        synchronized(writer){writer.write(packet);writer.flush();}
    }
    public byte[] pullAudio(){byte[] pcm=playback.poll();return pcm==null?new byte[0]:pcm;}
    public void close() {
        if(!closed) { try { invoke("shutdown",new JsonObject()); } catch(Exception ignored) {} }
        closed=true;
        try { writer.close(); if(!process.waitFor(3,TimeUnit.SECONDS)) process.destroyForcibly(); }
        catch(Exception ignored) { process.destroyForcibly(); }
    }
}
