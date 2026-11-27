package dev.hole.forge;

import com.google.gson.JsonObject;
import javax.sound.sampled.*;
import java.util.Base64;
import java.util.concurrent.ArrayBlockingQueue;

/** Audio devices only; codecs, pacing and network authorization belong to core. */
public interface VoiceAudio extends AutoCloseable {
    void capture(boolean enabled) throws Exception;
    byte[] read() throws Exception;
    void write(byte[] pcm) throws Exception;
    void close();
    static VoiceAudio open(CoreClient core) throws Exception {return NativeCore.android()?new Android(core):new Desktop();}

    final class Android implements VoiceAudio {
        private final CoreClient core;
        Android(CoreClient core)throws Exception {this.core=core;call("start",null,false);}
        private JsonObject call(String op,byte[] pcm,boolean enabled)throws Exception {
            JsonObject input=new JsonObject();input.addProperty("op",op);input.addProperty("enabled",enabled);
            if(pcm!=null)input.addProperty("pcm",Base64.getEncoder().encodeToString(pcm));
            JsonObject out=core.invoke("android_audio",input);
            if(out.has("error"))throw new IllegalStateException(out.get("error").getAsString());return out;
        }
        public void capture(boolean enabled)throws Exception {call("capture",null,enabled);}
        public byte[] read()throws Exception {JsonObject r=call("read",null,false);return Base64.getDecoder().decode(r.get("pcm").getAsString());}
        public void write(byte[] pcm)throws Exception {call("write",pcm,false);}
        public void close(){try{call("stop",null,false);}catch(Exception ignored){}}
    }
    final class Desktop implements VoiceAudio {
        private final AudioFormat format=new AudioFormat(48000f,16,1,true,false);
        private final ArrayBlockingQueue<byte[]> input=new ArrayBlockingQueue<>(4),output=new ArrayBlockingQueue<>(4);
        private volatile boolean active=true;
        private volatile TargetDataLine microphone;
        private final SourceDataLine speaker;
        private volatile String error;
        Desktop()throws Exception {
            speaker=(SourceDataLine)AudioSystem.getLine(new DataLine.Info(SourceDataLine.class,format));
            try{speaker.open(format,7680);speaker.start();}catch(Exception e){speaker.close();throw e;}
            thread("playback",()->{while(active){byte[] pcm=output.poll(100,java.util.concurrent.TimeUnit.MILLISECONDS);if(pcm!=null){int n=0;while(active && n<pcm.length){int wrote=speaker.write(pcm,n,pcm.length-n);if(wrote<=0)throw new IllegalStateException("扬声器停止");n+=wrote;}}}});
        }
        private interface Work {void run()throws Exception;}
        private void thread(String name,Work work){Thread t=new Thread(()->{try{work.run();}catch(Exception e){if(active)error="音频设备错误："+e.getMessage();}},"Hole voice "+name);t.setDaemon(true);t.start();}
        public synchronized void capture(boolean enabled)throws Exception {
            if(!enabled){TargetDataLine old=microphone;microphone=null;if(old!=null)old.close();input.clear();return;}
            if(microphone!=null)return;
            TargetDataLine line=(TargetDataLine)AudioSystem.getLine(new DataLine.Info(TargetDataLine.class,format));
            try{line.open(format,7680);line.start();}catch(Exception e){line.close();throw e;}
            microphone=line;
            thread("capture",()->{while(active && microphone==line){byte[] pcm=new byte[1920];int n=0;while(active && microphone==line && n<pcm.length){int count=line.read(pcm,n,pcm.length-n);if(count<=0)break;n+=count;}if(microphone!=line)break;if(n!=pcm.length)throw new IllegalStateException("麦克风停止");if(!input.offer(pcm)){input.poll();input.offer(pcm);}}});
        }
        public byte[] read(){if(error!=null)throw new IllegalStateException(error);byte[] pcm=input.poll();return pcm==null?new byte[0]:pcm;}
        public void write(byte[] pcm){if(error!=null)throw new IllegalStateException(error);if(!output.offer(pcm)){output.poll();output.offer(pcm);}}
        public void close(){active=false;try{capture(false);}catch(Exception ignored){}speaker.close();output.clear();}
    }
}
