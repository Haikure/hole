package dev.hole.forge.android;

import android.Manifest;
import android.content.Context;
import android.content.pm.PackageManager;
import android.media.*;
import android.media.audiofx.AcousticEchoCanceler;
import android.media.audiofx.NoiseSuppressor;
import android.util.Base64;
import org.json.JSONObject;
import java.util.concurrent.ArrayBlockingQueue;
import java.util.concurrent.TimeUnit;

/** Runs exclusively in ART. Bounded queues keep the game/native calls nonblocking. */
public final class AudioBridge {
    private final Context context;
    private final ArrayBlockingQueue<byte[]> input=new ArrayBlockingQueue<>(4),output=new ArrayBlockingQueue<>(4);
    private volatile AudioRecord recorder;
    private volatile AudioTrack player;
    private volatile String error="";
    private AcousticEchoCanceler aec;
    private NoiseSuppressor noise;
    public AudioBridge(Context context){this.context=context;}
    public synchronized String call(String text)throws Exception {
        JSONObject in=new JSONObject(text);String op=in.getString("op");
        try{
            if(op.equals("stop")){stop();return "{}";}
            if(op.equals("start")){stop();error="";start();return "{}";}
            if(!error.isEmpty())return new JSONObject().put("error",error).toString();
            if(op.equals("capture"))capture(in.getBoolean("enabled"));
            if(op.equals("read")){byte[] pcm=input.poll();return new JSONObject().put("pcm",pcm==null?"":Base64.encodeToString(pcm,Base64.NO_WRAP)).toString();}
            if(op.equals("write")){byte[] pcm=Base64.decode(in.getString("pcm"),Base64.NO_WRAP);if(pcm.length!=1920)throw new IllegalArgumentException("PCM frame size");if(!output.offer(pcm)){output.poll();output.offer(pcm);}}
            return "{}";
        }catch(Exception e){if(op.equals("start") || op.equals("capture"))try{stop();}catch(Exception ignored){}return new JSONObject().put("error",e.getMessage()==null?"Android 音频不可用":e.getMessage()).toString();}
    }
    private void start(){
        int size=Math.max(7680,AudioTrack.getMinBufferSize(48000,AudioFormat.CHANNEL_OUT_MONO,AudioFormat.ENCODING_PCM_16BIT));
        AudioTrack track=new AudioTrack.Builder().setAudioAttributes(new AudioAttributes.Builder().setUsage(AudioAttributes.USAGE_GAME).setContentType(AudioAttributes.CONTENT_TYPE_SPEECH).build()).setAudioFormat(new AudioFormat.Builder().setSampleRate(48000).setChannelMask(AudioFormat.CHANNEL_OUT_MONO).setEncoding(AudioFormat.ENCODING_PCM_16BIT).build()).setBufferSizeInBytes(size).setTransferMode(AudioTrack.MODE_STREAM).build();
        if(track.getState()!=AudioTrack.STATE_INITIALIZED){track.release();throw new IllegalStateException("扬声器初始化失败");}
        player=track;track.play();
        Thread t=new Thread(()->{try{while(player==track){byte[] pcm=output.poll(100,TimeUnit.MILLISECONDS);if(pcm!=null){int n=0;while(player==track && n<pcm.length){int count=track.write(pcm,n,pcm.length-n,AudioTrack.WRITE_BLOCKING);if(count<=0)throw new IllegalStateException("扬声器写入失败");n+=count;}}}}catch(Exception e){if(player==track)error="Android 播放失败";}},"Hole Android playback");t.setDaemon(true);t.start();
    }
    private void capture(boolean enabled){
        if(!enabled){AudioRecord old=recorder;recorder=null;if(old!=null){try{old.stop();}catch(Exception ignored){}old.release();}if(aec!=null){aec.release();aec=null;}if(noise!=null){noise.release();noise=null;}input.clear();return;}
        if(recorder!=null)return;
        if(context.checkSelfPermission(Manifest.permission.RECORD_AUDIO)!=PackageManager.PERMISSION_GRANTED)throw new IllegalStateException("请在安卓设置中允许 FCL 使用麦克风，然后重试");
        int size=Math.max(7680,AudioRecord.getMinBufferSize(48000,AudioFormat.CHANNEL_IN_MONO,AudioFormat.ENCODING_PCM_16BIT));
        AudioRecord rec=new AudioRecord.Builder().setAudioSource(MediaRecorder.AudioSource.VOICE_COMMUNICATION).setAudioFormat(new AudioFormat.Builder().setSampleRate(48000).setEncoding(AudioFormat.ENCODING_PCM_16BIT).setChannelMask(AudioFormat.CHANNEL_IN_MONO).build()).setBufferSizeInBytes(size).build();
        if(rec.getState()!=AudioRecord.STATE_INITIALIZED){rec.release();throw new IllegalStateException("麦克风初始化失败");}
        recorder=rec;
        try{if(AcousticEchoCanceler.isAvailable()){aec=AcousticEchoCanceler.create(rec.getAudioSessionId());if(aec!=null)aec.setEnabled(true);}if(NoiseSuppressor.isAvailable()){noise=NoiseSuppressor.create(rec.getAudioSessionId());if(noise!=null)noise.setEnabled(true);}}catch(Exception ignored){}
        rec.startRecording();
        Thread t=new Thread(()->{try{while(recorder==rec){byte[] pcm=new byte[1920];int n=0;while(recorder==rec && n<pcm.length){int count=rec.read(pcm,n,pcm.length-n,AudioRecord.READ_BLOCKING);if(count<=0)throw new IllegalStateException("麦克风读取失败");n+=count;}if(recorder==rec){if(!input.offer(pcm)){input.poll();input.offer(pcm);}}}}catch(Exception e){if(recorder==rec)error="Android 采集失败";}},"Hole Android capture");t.setDaemon(true);t.start();
    }
    private void stop(){capture(false);AudioTrack old=player;player=null;if(old!=null){try{old.stop();}catch(Exception ignored){}old.release();}input.clear();output.clear();}
}
