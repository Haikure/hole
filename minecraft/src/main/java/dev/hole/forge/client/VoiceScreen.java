package dev.hole.forge.client;

import dev.hole.forge.*;
import net.minecraft.client.multiplayer.PlayerInfo;
import net.minecraft.client.gui.GuiGraphics;
import net.minecraft.client.gui.components.*;
import net.minecraft.client.gui.screens.Screen;
import net.minecraft.network.chat.Component;

public final class VoiceScreen extends Screen {
    private final Screen parent;
    private String failure="";
    public VoiceScreen(Screen parent){super(Component.literal("Hole 世界语音"));this.parent=parent;}
    protected void init(){
        HoleRuntime runtime=HoleMod.RUNTIME;
        addRenderableWidget(Button.builder(Component.literal(runtime.voiceEnabled()?"关闭世界语音":"加入世界语音"),b->runtime.enableVoice(!runtime.voiceEnabled()).whenComplete((v,e)->minecraft.execute(()->{failure=e==null?"":e.getMessage();rebuildWidgets();}))).bounds(width/2-140,90,280,20).build());
        addRenderableWidget(Button.builder(Component.literal("关闭麦克风"),b->runtime.voice.mode(WorldVoice.Mode.LISTEN)).bounds(width/2-140,116,136,20).build());
        addRenderableWidget(Button.builder(Component.literal("持续开启麦克风"),b->runtime.voice.mode(WorldVoice.Mode.OPEN)).bounds(width/2+4,116,136,20).build());
        addRenderableWidget(Button.builder(Component.literal("按键说话（按住 "+ClientVoiceKeys.TALK.getTranslatedKeyMessage().getString()+"）"),b->runtime.voice.mode(WorldVoice.Mode.PUSH_TO_TALK)).bounds(width/2-140,142,280,20).build());
        addRenderableWidget(new AbstractSliderButton(width/2-140,168,280,20,Component.empty(),runtime.voice.volume()/150.0){
            {updateMessage();}protected void updateMessage(){setMessage(Component.literal("语音音量："+(int)Math.round(value*150)+"%"));}protected void applyValue(){runtime.voice.volume((int)Math.round(value*150));}
        });
        addRenderableWidget(Button.builder(Component.literal("返回"),b->onClose()).bounds(width/2-140,height-30,280,20).build());
    }
    public void render(GuiGraphics graphics,int x,int y,float partial){
        renderBackground(graphics);super.render(graphics,x,y,partial);
        graphics.drawCenteredString(font,title,width/2,18,0xffffff);
        VoiceIcons.draw(graphics,width/2-9,40,HoleMod.RUNTIME.voice.transmitting(),HoleMod.RUNTIME.voice.active());
        graphics.drawCenteredString(font,Component.literal(HoleMod.RUNTIME.voice.label()),width/2,66,HoleMod.RUNTIME.voice.transmitting()?0x75df83:0xeeeeee);
        PlayerInfo playerInfo=minecraft.player==null || minecraft.getConnection()==null?null:minecraft.getConnection().getPlayerInfo(minecraft.player.getUUID());
        String latencyLabel=playerInfo==null?"房主延迟：等待服务器数据":"房主延迟："+playerInfo.getLatency()+" ms";
        graphics.drawCenteredString(font,Component.literal(latencyLabel),width/2,80,playerInfo==null?0xcccccc:0x88ddaa);
        String note=failure.isEmpty()?(HoleMod.RUNTIME.voiceAvailable()?"仅当前 MC 世界内的玩家 · 最多 8 台语音设备":"进入支持 Hole 的世界后才能加入语音"):failure;
        graphics.drawCenteredString(font,Component.literal(font.plainSubstrByWidth(note,width-24)),width/2,194,0xe0b275);
    }
    public boolean isPauseScreen(){return false;}
    public void onClose(){minecraft.setScreen(parent);}
}
