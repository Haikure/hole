package dev.hole.forge;

import java.io.*;
import java.net.*;
import java.util.concurrent.*;
import org.junit.Test;
import static org.junit.Assert.*;

public class WorldPingTest {
    private static byte[] frame(DataInputStream in)throws IOException{
        int size=WorldPing.varInt(in);byte[] bytes=new byte[size];in.readFully(bytes);return bytes;
    }
    private static void send(DataOutputStream out,byte[] bytes)throws IOException{WorldPing.varInt(out,bytes.length);out.write(bytes);out.flush();}
    @Test public void latencyWaitsForRemoteMinecraftPong()throws Exception{
        try(ServerSocket server=new ServerSocket(0,1,InetAddress.getLoopbackAddress());WorldPing ping=new WorldPing()){
            ExecutorService executor=Executors.newSingleThreadExecutor();
            try{
                Future<?> remote=executor.submit(()->{
                    try(Socket socket=server.accept()){
                        socket.setSoTimeout(3000);var in=new DataInputStream(socket.getInputStream());var out=new DataOutputStream(socket.getOutputStream());
                        var handshake=new DataInputStream(new ByteArrayInputStream(frame(in)));
                        assertEquals(0,WorldPing.varInt(handshake));assertEquals(763,WorldPing.varInt(handshake));
                        assertArrayEquals(new byte[]{0},frame(in));send(out,new byte[]{0,2,'{','}'});
                        byte[] request=frame(in);assertEquals(9,request.length);assertEquals(1,request[0]);
                        Thread.sleep(90);send(out,request);
                    }catch(Exception e){throw new RuntimeException(e);}
                });
                long measured=ping.measure(server.getLocalPort());
                assertTrue("reported loopback connect time instead of remote RTT: "+measured,measured>=70);
                remote.get(4,TimeUnit.SECONDS);
            }finally{executor.shutdownNow();}
        }
    }
    @Test public void oversizedStatusAndCancelledProbeFailBoundedly()throws Exception{
        try(ServerSocket server=new ServerSocket(0,1,InetAddress.getLoopbackAddress());WorldPing ping=new WorldPing()){
            ExecutorService executor=Executors.newSingleThreadExecutor();
            try{
                Future<?> remote=executor.submit(()->{
                    try(Socket socket=server.accept()){
                        var in=new DataInputStream(socket.getInputStream());frame(in);frame(in);
                        var out=new DataOutputStream(socket.getOutputStream());WorldPing.varInt(out,131073);out.flush();
                    }catch(Exception e){throw new RuntimeException(e);}
                });
                try{ping.measure(server.getLocalPort());fail("oversized frame accepted");}catch(IOException expected){}
                remote.get(4,TimeUnit.SECONDS);
            }finally{executor.shutdownNow();}
        }
        WorldPing closed=new WorldPing();closed.close();
        try{closed.measure(25565);fail("cancelled probe connected");}catch(IOException expected){}
    }
}
