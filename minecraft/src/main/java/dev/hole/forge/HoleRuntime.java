package dev.hole.forge;

import com.google.gson.*;
import java.net.*;
import java.nio.file.Path;
import java.util.*;
import java.util.concurrent.*;

/** All core operations are serialized off the render/server threads. */
public final class HoleRuntime implements AutoCloseable {
    interface Factory { CoreClient create() throws Exception; }
    private final Factory factory;
    private final ScheduledExecutorService worker=Executors.newSingleThreadScheduledExecutor(r->{Thread t=new Thread(r,"Hole Minecraft");t.setDaemon(true);return t;});
    private final Path configPath;
    private final ExecutorService probes=Executors.newSingleThreadExecutor(r->{Thread t=new Thread(r,"Hole world ping");t.setDaemon(true);return t;});
    public record Latency(long millis,String route,String error,long measuredAt){
        public String label(){return error.isEmpty()?(System.currentTimeMillis()-measuredAt>60000?"上次 ":"")+millis+" ms"+(route.isEmpty()?"":" · "+route):error;}
    }
    private final Map<String,Latency> latencies=new ConcurrentHashMap<>();
    private record PreparedWorld(WorldEntry entry,int port) {}
    private static final int MAX_PREPARED_WORLDS=16;
    private final Map<String,PreparedWorld> prepared=new LinkedHashMap<>();
    private volatile String progress="";
    private volatile boolean measuring;
    private WorldPing probe;
    private long attempt;
    private WorldEntry selected;
    private WorldInfo hostInfo=WorldInfo.UNKNOWN;
    private volatile HoleSettings settings;
    private volatile List<WorldEntry> worlds=List.of();
    private volatile String status="未连接";
    private volatile boolean hosting;
    public final WorldVoice voice=new WorldVoice();
    private volatile String voiceGroup="";
    private volatile boolean voiceWanted;
    private final java.util.concurrent.atomic.AtomicLong worldEpoch=new java.util.concurrent.atomic.AtomicLong();
    private CoreClient core;
    private boolean running;
    private String hostId="",hostAddress="",selectedId="",network="";
    private int hostPort,localPort,retries;
    private long networkSequence,deadline;
    private CompletableFuture<Integer> joining;
    private volatile boolean closed;

    public HoleRuntime(Path path) {
        this(path,CoreClient::create);
    }
    HoleRuntime(Path path, Factory factory) {
        this.factory=factory;
        configPath=path;
        try { settings=HoleSettings.load(path); }
        catch(Exception e) { settings=new HoleSettings(); status="Hole 配置读取失败，请修正 "+path.getFileName(); }
        worker.scheduleWithFixedDelay(this::poll,0,1,TimeUnit.SECONDS);
    }
    public HoleSettings settings() { return settings.copy(); }
    public List<WorldEntry> worlds() { return worlds; }
    public String status() { return status; }
    public boolean hosting() { return hosting; }
    public String progress(){return progress;}
    public boolean measuring(){return measuring;}
    public Latency latency(WorldEntry entry){return latencies.get(entry.key());}
    public boolean voiceAvailable(){return !voiceGroup.isEmpty();}
    public boolean voiceEnabled(){return voiceWanted;}
    public void enterWorld(String group){
        if(!group.matches("[a-f0-9]{64}"))return;
        long epoch=worldEpoch.get();
        task(()->{if(epoch!=worldEpoch.get())return null;voice.stop();voiceGroup=group;if(running)apply();return null;});
    }
    public CompletableFuture<Void> enableVoice(boolean enabled){
        return task(()->{
            if(enabled && voiceGroup.isEmpty())throw new IllegalStateException("请先进入支持 Hole 语音的 MC 世界");
            voice.stop();voice.clearError();voiceWanted=enabled;
            try{if(running)apply();else if(enabled)connectNow();}
            catch(Exception e){voiceWanted=false;throw e;}
            return null;
        });
    }
    public CompletableFuture<Void> configure(HoleSettings next) {
        return task(()->{ next.validate(); next.save(configPath); settings=next.copy(); disconnectCore(); connectNow(); return null; });
    }
    public CompletableFuture<Void> connect() { return task(()->{connectNow();return null;}); }
    public CompletableFuture<Void> publish(String address,int port,String title) {
        return publish(address,port,title,WorldInfo.UNKNOWN);
    }
    public CompletableFuture<Void> publish(String address,int port,String title,WorldInfo info) {
        return task(()->{
            settings.validate();
            if(port<1 || port>65535) throw new IllegalArgumentException("Minecraft 尚未监听 TCP 端口");
            hostId=WorldEntry.serviceId(settings.deviceId,title);hostAddress=address;hostPort=port;hosting=true;
            hostInfo=info;
            try { if(running) apply(); else connectNow(); }
            catch(Exception e) { hosting=false;hostId="";throw e; }
            return null;
        });
    }
    public void updateWorld(WorldInfo info){task(()->{if(hosting && !info.equals(hostInfo)){hostInfo=info;if(running)apply();}return null;});}
    public void unpublish() { task(()->{hostId="";hosting=false;hostPort=0;if(running)apply();return null;}); }
    public CompletableFuture<Integer> join(WorldEntry entry) {
        return openWorld(entry,false);
    }
    public CompletableFuture<Integer> measure(WorldEntry entry){return openWorld(entry,true);}
    public void cancelConnection(){task(()->{prepared.clear();releaseSelection();return null;});}
    private void releaseSelection()throws Exception{
        if(selected!=null)prepared.remove(selected.key());
        cancelJoin();
        try{if(running)apply();}catch(Exception e){disconnectCore();throw e;}
    }
    private CompletableFuture<Integer> openWorld(WorldEntry entry,boolean measure) {
        CompletableFuture<Integer> answer=new CompletableFuture<>();
        task(()->{
            if(worlds.stream().noneMatch(w->w.key().equals(entry.key()))) throw new IllegalStateException("该世界已下线，请刷新列表");
            boolean pending=selected!=null && !prepared.containsKey(selected.key());
            cancelJoin();selected=entry;measuring=measure;selectedId=entry.id();retries=0;
            PreparedWorld cached=prepared.get(entry.key());
            localPort=cached==null?freePort():cached.port();
            progress="正在等待房主响应";
            joining=answer; deadline=System.nanoTime()+TimeUnit.SECONDS.toNanos(75);
            try{if(cached==null || pending)apply();}catch(Exception e){status=clean(e);joining=null;try{releaseSelection();}catch(Exception ignored){}throw e;}
            status="正在连接："+entry.name();poll();return null;
        }).exceptionally(e->{answer.completeExceptionally(e);return null;});
        return answer;
    }
    public void leaveGame() { worldEpoch.incrementAndGet();voiceGroup="";voiceWanted=false;voice.stop();cancelConnection(); }
    public void disconnect() { task(()->{hostId="";hosting=false;disconnectCore();return null;}); }
    private <T> CompletableFuture<T> task(Callable<T> operation) {
        CompletableFuture<T> result=new CompletableFuture<>();
        if(closed) { result.completeExceptionally(new IllegalStateException("Hole 已关闭"));return result; }
        worker.execute(()->{
            try {result.complete(operation.call());}
            catch(Exception | LinkageError e) {status=clean(e);result.completeExceptionally(e);}
        });return result;
    }
    private String clean(Throwable error) {
        String text=error.getMessage(); if(text==null) text=error.getClass().getSimpleName();
        for(String secret:List.of(settings.password,settings.token)) if(!secret.isEmpty())text=text.replace(secret,"[redacted]");
        return text.length()>240?text.substring(0,240):text;
    }
    private void connectNow() throws Exception {
        settings.validate();
        if(core==null) core=factory.create();
        if(!running) {core.invoke("start",request());running=true;status="正在加入 Hole 房间";}
    }
    private JsonObject request() {
        JsonObject config=new JsonObject();
        config.addProperty("room",settings.room);config.addProperty("password",settings.password);
        config.addProperty("token",settings.token);config.addProperty("device_name",settings.deviceId);
        JsonArray provide=new JsonArray(),consume=new JsonArray();
        if(!hostId.isEmpty()) {
            JsonObject item=new JsonObject();item.addProperty("id",hostId);
            item.addProperty("discovery","minecraft");
            if(hostInfo.known())item.add("minecraft",hostInfo.json());
            item.addProperty("service","tcp://"+new InetSocketAddress(hostAddress,hostPort).getHostString().replaceAll("^(.*:.*)$","[$1]")+":"+hostPort);
            provide.add(item);
        }
        for(PreparedWorld cached:prepared.values()) {
            if(!cached.entry().id().equals(selectedId))consume.add(consume(cached.entry().id(),cached.port()));
        }
        if(!selectedId.isEmpty())consume.add(consume(selectedId,localPort));
        config.add("provide",provide);config.add("consume",consume);
        JsonObject voiceConfig=new JsonObject();voiceConfig.addProperty("enabled",voiceWanted && !voiceGroup.isEmpty());
        voiceConfig.addProperty("group",voiceGroup);config.add("voice",voiceConfig);
        JsonObject turn=new JsonObject();turn.addProperty("mode","off");config.add("turn",turn);
        JsonObject relay=new JsonObject();relay.addProperty("device",settings.relayDevice);
        JsonArray order=new JsonArray();order.add("hole");relay.add("order",order);config.add("relay",relay);
        JsonObject request=new JsonObject();request.addProperty("api_version",1);request.addProperty("server_url",settings.serverUrl);request.add("config",config);
        return request;
    }
    private static JsonObject consume(String id,int port){
        JsonObject item=new JsonObject();item.addProperty("id",id);item.addProperty("expose","127.0.0.1:"+port);return item;
    }
    private void apply() throws Exception { core.invoke("apply",request()); }
    private static int freePort() throws Exception {
        try(java.net.ServerSocket socket=new java.net.ServerSocket(0,1,InetAddress.getByName("127.0.0.1"))) {return socket.getLocalPort();}
    }
    private void poll() {
        if(closed || !running || core==null)return;
        try {
            if(NativeCore.android()) {
                JsonObject current=core.invoke("platform_network",new JsonObject());
                String key=current.toString();
                if(!network.isEmpty() && !network.equals(key)) {
                    JsonObject event=new JsonObject();event.addProperty("sequence",Long.toString(++networkSequence));
                    core.invoke("network",event);
                }
                network=key;
            }
            JsonObject snapshot=core.invoke("snapshot",new JsonObject());
            if(voiceWanted && !voiceGroup.isEmpty() && !voice.active() && voice.error().isEmpty() && snapshot.has("voice") && snapshot.getAsJsonObject("voice").get("enabled").getAsBoolean())voice.start(core);
            String signal=snapshot.get("signal_state").getAsString();
            boolean online=signal.equals("joined") || signal.equals("delegated");
            worlds=online?WorldEntry.fromSnapshot(snapshot,settings.deviceId):List.of();
            if(online){
                Set<String> visible=new HashSet<>();for(WorldEntry entry:worlds)visible.add(entry.key());
                latencies.keySet().removeIf(key->!visible.contains(key));
                if(prepared.keySet().removeIf(key->!visible.contains(key)))apply();
            }
            if(selected!=null && (joining!=null || measuring)){
                if(online && worlds.stream().noneMatch(w->w.key().equals(selected.key())))throw new IllegalStateException("该世界已下线或房主已重启");
                if(probe==null)progress=ConnectionProgress.from(snapshot,selected.device()).stage();
            }
            if(joining!=null && !joining.isDone()) {
                if(System.nanoTime()>deadline) throw new TimeoutException("连接超时，请检查房主和 Hole 中继是否在线");
                for(JsonElement item:snapshot.getAsJsonArray("mappings")) {
                    JsonObject mapping=item.getAsJsonObject();
                    if(!selectedId.equals(mapping.get("id").getAsString()))continue;
                    JsonObject listener=mapping.getAsJsonObject("listener");
                    if(listener!=null && listener.has("error") && !listener.get("error").isJsonNull()) {
                        if(retries++>=3)throw new IllegalStateException("本地映射端口绑定失败");
                        prepared.remove(selected.key());
                        localPort=freePort();apply();return;
                    }
                    if(mapping.get("state").getAsString().equals("active") && listener!=null && listener.get("state").getAsString().equals("ready")) {
                        if(measuring){if(probe==null)startProbe(ConnectionProgress.from(snapshot,selected.device()).route());}
                        else{
                            boolean others=prepared.values().stream().anyMatch(cached->!cached.entry().id().equals(selectedId));
                            prepared.clear();if(others)apply();
                            progress="通道已就绪，正在进入 Minecraft 登录";
                            CompletableFuture<Integer> answer=joining;joining=null;status="连接已就绪";answer.complete(localPort);
                        }
                        return;
                    }
                }
            } else {
                status=online?(hosting?"正在共享世界 · ":"")+"已加入 "+settings.room+" · "+worlds.size()+" 个世界":"信令状态："+signal;
                if(snapshot.has("error") && snapshot.get("error").isJsonObject()) status=clean(new Exception(snapshot.getAsJsonObject("error").get("message").getAsString()));
                if(online && snapshot.has("room") && snapshot.get("room").isJsonObject()) {
                    JsonObject room=snapshot.getAsJsonObject("room");
                    if(!room.has("discovery_supported") || !room.get("discovery_supported").getAsBoolean())status="请升级 Hole Worker 以启用世界发现";
                }
            }
        } catch(Exception | LinkageError error) {
            status=clean(error);
            if(joining!=null) {
                if(measuring && selected!=null)latencies.put(selected.key(),new Latency(0,"","连接失败",System.currentTimeMillis()));
                CompletableFuture<Integer> answer=joining;joining=null;
                try{releaseSelection();}catch(Exception ignored){}
                status=clean(error);answer.completeExceptionally(error);
            }
        }
    }
    private void startProbe(String route){
        progress="通道已就绪，正在测量 MC 往返延迟";
        long token=attempt;WorldEntry entry=selected;int port=localPort;WorldPing current=new WorldPing();probe=current;
        // Total timeout also covers a peer that drips bytes below the socket timeout.
        worker.schedule(current::close,8,TimeUnit.SECONDS);
        probes.execute(()->{
            long millis=0;String failure="";
            try(current){millis=current.measure(port);}catch(Exception e){failure="测量失败（MC 状态不可用）";}
            Latency result=new Latency(millis,route,failure,System.currentTimeMillis());
            task(()->{
                if(token!=attempt || !measuring)return null;
                latencies.put(entry.key(),result);CompletableFuture<Integer> answer=joining;
                prepared.remove(entry.key());prepared.put(entry.key(),new PreparedWorld(entry,port));
                boolean evicted=prepared.size()>MAX_PREPARED_WORLDS;
                if(evicted)prepared.remove(prepared.keySet().iterator().next());
                joining=null;
                cancelJoin();
                try{if(evicted)apply();if(answer!=null)answer.complete(port);}
                catch(Exception e){status=clean(e);if(answer!=null)answer.completeExceptionally(e);throw e;}
                return null;
            });
        });
    }
    private void cancelJoin() {
        attempt++;if(probe!=null){probe.close();probe=null;}measuring=false;selected=null;progress="";
        if(joining!=null) {joining.completeExceptionally(new CancellationException("已取消连接"));joining=null;}
        selectedId="";localPort=0;
    }
    private void disconnectCore() {
        voice.stop();
        prepared.clear();cancelJoin();running=false;worlds=List.of();network="";
        latencies.clear();
        if(core!=null) {core.close();core=null;}
        status="未连接";
    }
    public void close() {
        if(closed)return;
        closed=true;
        probes.shutdownNow();
        worker.execute(this::disconnectCore);worker.shutdown();
        try {if(!worker.awaitTermination(30,TimeUnit.SECONDS))worker.shutdownNow();}
        catch(InterruptedException e){Thread.currentThread().interrupt();worker.shutdownNow();}
        voice.close();
    }
}
