package dev.hole.forge;

import java.util.concurrent.*;

/** A world-scoped audio session. Never opens a microphone before explicit input. */
public final class WorldVoice implements AutoCloseable {
    public enum Mode { LISTEN, PUSH_TO_TALK, OPEN }
    interface Factory {VoiceAudio open(CoreClient core)throws Exception;}
    private final Factory factory;
    private final ScheduledExecutorService thread=Executors.newSingleThreadScheduledExecutor(r->{Thread t=new Thread(r,"Hole voice media");t.setDaemon(true);return t;});
    private CoreClient core;
    private VoiceAudio audio;
    private long sequence;
    private boolean capturing;
    private volatile boolean active,pressed;
    private volatile Mode mode=Mode.LISTEN;
    private volatile int volume=100;
    private volatile String error="";
    public WorldVoice(){this(VoiceAudio::open,true);}
    WorldVoice(Factory factory,boolean scheduled){this.factory=factory;if(scheduled)thread.scheduleAtFixedRate(this::tick,0,20,TimeUnit.MILLISECONDS);}
    public synchronized void start(CoreClient client)throws Exception {
        if(active)return;error="";core=client;
        try{core.muteAudio(true);core.startAudio();audio=factory.open(core);active=true;}
        catch(Exception e){stop();error="无法启动语音："+e.getMessage();throw e;}
    }
    public synchronized void stop(){active=false;pressed=false;capturing=false;mode=Mode.LISTEN;if(audio!=null){audio.close();audio=null;}if(core!=null){try{core.stopAudio();}catch(Exception ignored){}core=null;}}
    public synchronized void mode(Mode next){mode=next;pressed=false;}
    public synchronized void clearError(){error="";}
    public Mode mode(){return mode;}
    public void pressed(boolean value){pressed=value;}
    public void volume(int value){volume=Math.max(0,Math.min(150,value));}
    public int volume(){return volume;}
    public boolean active(){return active;}
    public boolean transmitting(){return active && error.isEmpty() && (mode==Mode.OPEN || mode==Mode.PUSH_TO_TALK && pressed);}
    public String error(){return error;}
    public String label(){if(!error.isEmpty())return error;if(!active)return "语音已关闭";return switch(mode){case LISTEN->"麦克风已关闭 · 仅收听";case PUSH_TO_TALK->pressed?"麦克风开启 · 正在说话":"麦克风关闭 · 按键说话";case OPEN->"麦克风开启";};}
    synchronized void tick(){
        if(!active)return;
        try{
            boolean enabled=transmitting();
            if(enabled!=capturing){core.muteAudio(true);audio.capture(enabled);capturing=enabled;if(enabled)core.muteAudio(false);}
            byte[] pcm=audio.read();if(enabled && pcm.length==1920)core.pushAudio(sequence,pcm);sequence++;
            pcm=core.pullAudio();if(pcm.length>0){scale(pcm,volume);audio.write(pcm);}
        }catch(Exception e){String message="语音错误："+e.getMessage();stop();error=message;}
    }
    static void scale(byte[] pcm,int volume){for(int i=0;i+1<pcm.length;i+=2){int value=(short)((pcm[i]&255)|(pcm[i+1]<<8));value=Math.max(-32768,Math.min(32767,value*volume/100));pcm[i]=(byte)value;pcm[i+1]=(byte)(value>>8);}}
    public void close(){stop();thread.shutdownNow();}
}
