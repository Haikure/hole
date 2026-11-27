package dev.hole.forge;

import com.google.gson.*;
import java.io.*;
import java.net.*;
import java.nio.file.*;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.Test;
import static org.junit.Assert.*;

public class HoleRuntimeTest {
    static final class FakeCore implements CoreClient {
        volatile JsonObject request;
        volatile boolean closed;
        volatile WorldInfo info=new WorldInfo("Test","1.20.1",1,20);
        final String service=WorldEntry.serviceId("mc-abcdef0123456789abcdef0123456789","Test world");
        volatile List<String> services=List.of(service);
        volatile String runtime="runtime-one";
        volatile boolean mappingsReady=true;
        private final boolean listen;
        private final Map<Integer,ServerSocket> listeners=new HashMap<>();
        final AtomicInteger listenerStarts=new AtomicInteger();
        FakeCore(){this(false);}
        FakeCore(boolean listen){this.listen=listen;}
        public JsonObject invoke(String operation,JsonObject input) throws Exception {
            if(operation.equals("start") || operation.equals("apply")){
                request=input.deepCopy();if(listen)listen(input.getAsJsonObject("config").getAsJsonArray("consume"));
            }
            if(!operation.equals("snapshot"))return new JsonObject();
            JsonObject snapshot=JsonParser.parseString("{\"signal_state\":\"joined\",\"room\":{\"discovery_supported\":true,\"services\":[]},\"mappings\":[]}").getAsJsonObject();
            for(String id:services){
                JsonObject advertised=new JsonObject();advertised.addProperty("id",id);advertised.addProperty("device_name","host");advertised.addProperty("protocol","tcp");
                advertised.addProperty("kind","minecraft");advertised.addProperty("runtime_id",runtime);
                advertised.add("minecraft",info.json());
                snapshot.getAsJsonObject("room").getAsJsonArray("services").add(advertised);
            }
            if(request!=null) for(JsonElement item:request.getAsJsonObject("config").getAsJsonArray("consume")) {
                JsonObject mapping=new JsonObject();mapping.add("id",item.getAsJsonObject().get("id"));mapping.addProperty("state",mappingsReady?"active":"connecting");
                JsonObject listener=new JsonObject();listener.addProperty("state",mappingsReady?"ready":"starting");
                mapping.add("listener",listener);snapshot.getAsJsonArray("mappings").add(mapping);
            }
            return snapshot;
        }
        private void listen(JsonArray consume)throws IOException{
            Set<Integer> ports=new HashSet<>();
            for(JsonElement item:consume){
                int port=Integer.parseInt(item.getAsJsonObject().get("expose").getAsString().substring("127.0.0.1:".length()));ports.add(port);
                if(listeners.containsKey(port))continue;
                ServerSocket listener=new ServerSocket(port,4,InetAddress.getByName("127.0.0.1"));listeners.put(port,listener);listenerStarts.incrementAndGet();
                Thread thread=new Thread(()->{
                    while(!listener.isClosed())try(Socket socket=listener.accept()){
                        socket.setSoTimeout(3000);var in=new DataInputStream(socket.getInputStream());var out=new DataOutputStream(socket.getOutputStream());
                        in.readNBytes(WorldPing.varInt(in));in.readNBytes(WorldPing.varInt(in));
                        out.write(new byte[]{4,0,2,'{','}'});out.flush();
                        byte[] ping=in.readNBytes(WorldPing.varInt(in));WorldPing.varInt(out,ping.length);out.write(ping);out.flush();
                    }catch(IOException ignored){}
                },"Hole test status server");thread.setDaemon(true);thread.start();
            }
            for(var iterator=listeners.entrySet().iterator();iterator.hasNext();){
                var entry=iterator.next();if(!ports.contains(entry.getKey())){entry.getValue().close();iterator.remove();}
            }
        }
        public void close(){closed=true;for(ServerSocket listener:listeners.values())try{listener.close();}catch(IOException ignored){}listeners.clear();}
    }
    @Test public void discoveryJoinsOnlyAfterMappingReadyAndWorldWithdrawalPreservesGuestMapping() throws Exception {
        Path directory=Files.createTempDirectory("hole-runtime"),config=directory.resolve("hole.json");
        FakeCore core=new FakeCore();
        try(HoleRuntime runtime=new HoleRuntime(config,()->core)) {
            HoleSettings settings=runtime.settings();settings.serverUrl="wss://fixture.invalid/ws";settings.password="signal";settings.room="room";settings.token="secret";
            runtime.configure(settings).get(3,TimeUnit.SECONDS);
            runtime.publish("127.0.0.1",25570,"Local world",new WorldInfo("Test","1.20.1",1,20)).get(3,TimeUnit.SECONDS);
            assertEquals("minecraft",core.request.getAsJsonObject("config").getAsJsonArray("provide").get(0).getAsJsonObject().get("discovery").getAsString());
            long until=System.nanoTime()+TimeUnit.SECONDS.toNanos(5);
            while(runtime.worlds().isEmpty() && System.nanoTime()<until)Thread.sleep(20);
            assertEquals(1,runtime.worlds().size());
            WorldEntry original=runtime.worlds().get(0);core.info=new WorldInfo("Test","1.20.1",2,20);
            until=System.nanoTime()+TimeUnit.SECONDS.toNanos(5);
            while(runtime.worlds().get(0).info().online()!=2 && System.nanoTime()<until)Thread.sleep(20);
            assertEquals(2,runtime.worlds().get(0).info().online());
            int port=runtime.join(original).get(5,TimeUnit.SECONDS); // A count change must not invalidate the selected world.
            assertTrue(port>0);
            runtime.updateWorld(new WorldInfo("Test","1.20.1",2,20));runtime.connect().get(3,TimeUnit.SECONDS);
            assertEquals(2,core.request.getAsJsonObject("config").getAsJsonArray("provide").get(0).getAsJsonObject().getAsJsonObject("minecraft").get("online").getAsInt());
            runtime.unpublish();
            runtime.connect().get(3,TimeUnit.SECONDS); // barrier behind withdrawal
            JsonObject current=core.request.getAsJsonObject("config");
            assertEquals(0,current.getAsJsonArray("provide").size());
            assertEquals(1,current.getAsJsonArray("consume").size());
            runtime.leaveGame();runtime.connect().get(3,TimeUnit.SECONDS);
            assertEquals(0,core.request.getAsJsonObject("config").getAsJsonArray("consume").size());
        } finally {Files.deleteIfExists(config);Files.delete(directory);}
        assertTrue(core.closed);
    }
    @Test public void unavailableStatusStillKeepsReadyMappingForJoinAndCancelReleasesIt()throws Exception{
        Path directory=Files.createTempDirectory("hole-probe"),config=directory.resolve("hole.json");FakeCore core=new FakeCore();
        try(HoleRuntime runtime=new HoleRuntime(config,()->core)){
            HoleSettings settings=runtime.settings();settings.serverUrl="wss://fixture.invalid/ws";settings.password="signal";settings.room="room";settings.token="secret";
            runtime.configure(settings).get(3,TimeUnit.SECONDS);
            long until=System.nanoTime()+TimeUnit.SECONDS.toNanos(5);
            while(runtime.worlds().isEmpty() && System.nanoTime()<until)Thread.sleep(20);
            WorldEntry entry=runtime.worlds().get(0);
            assertNull(runtime.latency(entry));
            runtime.measure(entry).get(6,TimeUnit.SECONDS); // Fake core never opens the local port.
            runtime.connect().get(3,TimeUnit.SECONDS);
            assertFalse(runtime.latency(entry).error().isEmpty());assertFalse(runtime.measuring());
            assertEquals(1,core.request.getAsJsonObject("config").getAsJsonArray("consume").size());
            String expose=core.request.getAsJsonObject("config").getAsJsonArray("consume").get(0).getAsJsonObject().get("expose").getAsString();
            assertEquals(Integer.parseInt(expose.substring("127.0.0.1:".length())),(int)runtime.join(entry).get(3,TimeUnit.SECONDS));
            core.mappingsReady=false;
            CompletableFuture<Integer> pending=runtime.measure(entry);runtime.cancelConnection();runtime.connect().get(3,TimeUnit.SECONDS);
            assertTrue(pending.isCompletedExceptionally());assertFalse(runtime.measuring());
            assertEquals(0,core.request.getAsJsonObject("config").getAsJsonArray("consume").size());
            core.mappingsReady=true;
            runtime.join(entry).get(5,TimeUnit.SECONDS);runtime.connect().get(3,TimeUnit.SECONDS);
            assertEquals(1,core.request.getAsJsonObject("config").getAsJsonArray("consume").size());
        }finally{Files.deleteIfExists(config);Files.delete(directory);}
    }
    @Test public void measuredWorldsStayReadyAndJoiningReusesPortWithoutReopeningListener()throws Exception{
        Path directory=Files.createTempDirectory("hole-reuse"),config=directory.resolve("hole.json");FakeCore core=new FakeCore(true);
        core.services=List.of(core.service,WorldEntry.serviceId("mc-123456789abcdef0123456789abcdef0","Second world"));
        try(HoleRuntime runtime=new HoleRuntime(config,()->core)){
            configure(runtime);waitFor(()->runtime.worlds().size()==2);
            WorldEntry first=runtime.worlds().get(0),second=runtime.worlds().get(1);
            int firstPort=runtime.measure(first).get(5,TimeUnit.SECONDS);
            assertEquals("",runtime.latency(first).error());
            assertEquals(firstPort,(int)runtime.measure(first).get(5,TimeUnit.SECONDS));
            assertEquals(1,core.listenerStarts.get());
            runtime.measure(second).get(5,TimeUnit.SECONDS);
            assertEquals(2,core.request.getAsJsonObject("config").getAsJsonArray("consume").size());
            assertEquals(2,core.listenerStarts.get());
            assertEquals(firstPort,(int)runtime.join(first).get(3,TimeUnit.SECONDS));
            assertEquals(2,core.listenerStarts.get());
            JsonArray mappings=core.request.getAsJsonObject("config").getAsJsonArray("consume");
            assertEquals(1,mappings.size());assertEquals("127.0.0.1:"+firstPort,mappings.get(0).getAsJsonObject().get("expose").getAsString());
            runtime.leaveGame();runtime.connect().get(3,TimeUnit.SECONDS);
            assertEquals(0,core.request.getAsJsonObject("config").getAsJsonArray("consume").size());
        }finally{Files.deleteIfExists(config);Files.delete(directory);}
    }
    @Test public void lobbyExitRestartAndWithdrawalReleasePreparedWorlds()throws Exception{
        Path directory=Files.createTempDirectory("hole-prepared"),config=directory.resolve("hole.json");FakeCore core=new FakeCore(true);
        try(HoleRuntime runtime=new HoleRuntime(config,()->core)){
            configure(runtime);waitFor(()->!runtime.worlds().isEmpty());WorldEntry old=runtime.worlds().get(0);
            runtime.measure(old).get(5,TimeUnit.SECONDS);
            core.runtime="runtime-two";waitFor(()->!runtime.worlds().get(0).key().equals(old.key()));runtime.connect().get(3,TimeUnit.SECONDS);
            assertNull(runtime.latency(old));assertEquals(0,core.request.getAsJsonObject("config").getAsJsonArray("consume").size());
            WorldEntry current=runtime.worlds().get(0);runtime.measure(current).get(5,TimeUnit.SECONDS);
            runtime.cancelConnection();runtime.connect().get(3,TimeUnit.SECONDS);
            assertEquals(0,core.request.getAsJsonObject("config").getAsJsonArray("consume").size());
            runtime.measure(current).get(5,TimeUnit.SECONDS);core.services=List.of();
            waitFor(()->runtime.worlds().isEmpty());runtime.connect().get(3,TimeUnit.SECONDS);
            assertNull(runtime.latency(current));assertEquals(0,core.request.getAsJsonObject("config").getAsJsonArray("consume").size());
        }finally{Files.deleteIfExists(config);Files.delete(directory);}
    }
    private static void configure(HoleRuntime runtime)throws Exception{
        HoleSettings settings=runtime.settings();settings.serverUrl="wss://fixture.invalid/ws";settings.password="signal";settings.room="room";settings.token="secret";
        runtime.configure(settings).get(3,TimeUnit.SECONDS);
    }
    private static void waitFor(java.util.function.BooleanSupplier condition)throws Exception{
        long until=System.nanoTime()+TimeUnit.SECONDS.toNanos(5);
        while(!condition.getAsBoolean() && System.nanoTime()<until)Thread.sleep(20);
        assertTrue(condition.getAsBoolean());
    }
    @Test public void voiceRequiresWorldMembershipAndLogoutRevokesIt()throws Exception {
        Path directory=Files.createTempDirectory("hole-voice"),config=directory.resolve("hole.json");FakeCore core=new FakeCore();
        try(HoleRuntime runtime=new HoleRuntime(config,()->core)){
            HoleSettings settings=runtime.settings();settings.serverUrl="wss://fixture.invalid/ws";settings.password="signal";settings.room="room";settings.token="secret";
            runtime.configure(settings).get(3,TimeUnit.SECONDS);
            try{runtime.enableVoice(true).get(3,TimeUnit.SECONDS);fail("enabled without world");}catch(ExecutionException expected){}
            runtime.enterWorld("a".repeat(64));runtime.connect().get(3,TimeUnit.SECONDS);
            runtime.enableVoice(true).get(3,TimeUnit.SECONDS);
            JsonObject voice=core.request.getAsJsonObject("config").getAsJsonObject("voice");
            assertTrue(voice.get("enabled").getAsBoolean());assertEquals("a".repeat(64),voice.get("group").getAsString());
            runtime.leaveGame();assertFalse(runtime.voiceAvailable());assertFalse(runtime.voiceEnabled());
            runtime.connect().get(3,TimeUnit.SECONDS);
            voice=core.request.getAsJsonObject("config").getAsJsonObject("voice");assertFalse(voice.get("enabled").getAsBoolean());assertEquals("",voice.get("group").getAsString());
        }finally{Files.deleteIfExists(config);Files.delete(directory);}
    }
}
