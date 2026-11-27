package dev.hole.forge;

import org.junit.Test;
import static org.junit.Assert.*;

public class WorldVoiceTest {
    @Test public void explicitMicrophoneAndPushToTalkGateCaptureAndStopFlushes()throws Exception {
        class Audio implements VoiceAudio {
            boolean capture,closed;public void capture(boolean value){capture=value;}public byte[] read(){return capture?new byte[1920]:new byte[0];}public void write(byte[] pcm){}public void close(){closed=true;capture=false;}
        }
        class Core implements CoreClient {
            int sent;boolean muted,stopped;
            public com.google.gson.JsonObject invoke(String op,com.google.gson.JsonObject input){return new com.google.gson.JsonObject();}
            public void startAudio(){}public void stopAudio(){stopped=true;}public void muteAudio(boolean value){muted=value;}public void pushAudio(long seq,byte[] pcm){assertFalse(muted);sent++;}public void close(){}
        }
        Audio audio=new Audio();Core core=new Core();
        try(WorldVoice voice=new WorldVoice(c->audio,false)){
            voice.start(core);voice.tick();assertFalse(audio.capture);assertTrue(core.muted);assertEquals(0,core.sent);
            voice.mode(WorldVoice.Mode.PUSH_TO_TALK);voice.tick();assertEquals(0,core.sent);
            voice.pressed(true);voice.tick();assertTrue(audio.capture);assertEquals(1,core.sent);
            voice.pressed(false);voice.tick();assertFalse(audio.capture);assertTrue(core.muted);assertEquals(1,core.sent);
            voice.mode(WorldVoice.Mode.OPEN);voice.tick();assertEquals(2,core.sent);
            voice.stop();voice.tick();assertTrue(audio.closed);assertTrue(core.stopped);assertFalse(voice.transmitting());assertEquals(2,core.sent);
        }
    }
    @Test public void microphoneDefaultsOffAndNeverTransmitsOutsideSession(){
        try(WorldVoice voice=new WorldVoice()){
            assertEquals(WorldVoice.Mode.LISTEN,voice.mode());assertFalse(voice.transmitting());
            voice.mode(WorldVoice.Mode.OPEN);assertFalse(voice.transmitting());
            voice.mode(WorldVoice.Mode.PUSH_TO_TALK);voice.pressed(true);assertFalse(voice.transmitting());
            voice.stop();assertEquals(WorldVoice.Mode.LISTEN,voice.mode());assertFalse(voice.transmitting());
        }
    }
    @Test public void volumeClampsPCMWithoutOverflow(){
        byte[] pcm={(byte)0xff,0x7f,0,(byte)0x80};WorldVoice.scale(pcm,150);
        assertArrayEquals(new byte[]{(byte)0xff,0x7f,0,(byte)0x80},pcm);
        WorldVoice.scale(pcm,0);assertArrayEquals(new byte[4],pcm);
    }
}
