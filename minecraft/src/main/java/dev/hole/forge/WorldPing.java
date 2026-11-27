package dev.hole.forge;

import java.io.*;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.TimeUnit;

/** One bounded Minecraft 1.20.1 status/ping exchange through the game mapping. */
final class WorldPing implements AutoCloseable {
    private final Socket socket=new Socket();
    long measure(int port) throws IOException {
        socket.connect(new InetSocketAddress("127.0.0.1",port),5000);
        socket.setSoTimeout(5000);socket.setTcpNoDelay(true);
        DataInputStream input=new DataInputStream(socket.getInputStream());
        DataOutputStream output=new DataOutputStream(socket.getOutputStream());
        ByteArrayOutputStream bytes=new ByteArrayOutputStream();DataOutputStream handshake=new DataOutputStream(bytes);
        varInt(handshake,0);varInt(handshake,763); // 1.20 / 1.20.1, next state STATUS
        byte[] host="localhost".getBytes(StandardCharsets.UTF_8);varInt(handshake,host.length);handshake.write(host);
        handshake.writeShort(port);varInt(handshake,1);packet(output,bytes.toByteArray());packet(output,new byte[]{0});
        DataInputStream status=frame(input);
        if(varInt(status)!=0)throw new IOException("无效的 Minecraft 状态响应");
        int size=varInt(status);
        if(size<0 || size!=status.available())throw new IOException("无效的 Minecraft 状态长度");
        // Status contents are deliberately not used as trusted directory metadata.
        long nonce=System.nanoTime();bytes.reset();handshake.writeByte(1);handshake.writeLong(nonce);
        long start=System.nanoTime();packet(output,bytes.toByteArray());
        DataInputStream pong=frame(input);
        if(varInt(pong)!=1 || pong.available()!=8 || pong.readLong()!=nonce)throw new IOException("无效的 Minecraft 延迟响应");
        return Math.max(1,TimeUnit.NANOSECONDS.toMillis(System.nanoTime()-start));
    }
    private static DataInputStream frame(DataInputStream input)throws IOException{
        int size=varInt(input);
        if(size<1 || size>131072)throw new IOException("Minecraft 状态包超出限制");
        byte[] bytes=new byte[size];input.readFully(bytes);return new DataInputStream(new ByteArrayInputStream(bytes));
    }
    private static void packet(DataOutputStream output,byte[] packet)throws IOException{varInt(output,packet.length);output.write(packet);output.flush();}
    static int varInt(DataInputStream input)throws IOException{
        int value=0;
        for(int i=0;i<5;i++){
            int next=input.readUnsignedByte();
            if(i==4 && (next&0xf0)!=0)throw new IOException("无效的 VarInt");
            value|=(next&127)<<(i*7);if((next&128)==0)return value;
        }
        throw new IOException("VarInt 过长");
    }
    static void varInt(DataOutputStream output,int value)throws IOException{
        do{int next=value&127;value>>>=7;output.writeByte(value==0?next:next|128);}while(value!=0);
    }
    @Override public void close(){try{socket.close();}catch(IOException ignored){}}
}
