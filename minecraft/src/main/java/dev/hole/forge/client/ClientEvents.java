package dev.hole.forge.client;

import dev.hole.forge.HoleMod;
import net.minecraft.client.Minecraft;
import net.minecraft.client.gui.components.Button;
import net.minecraft.client.gui.screens.*;
import net.minecraft.client.gui.screens.multiplayer.JoinMultiplayerScreen;
import net.minecraft.network.chat.Component;
import net.minecraftforge.api.distmarker.Dist;
import net.minecraftforge.client.event.*;
import net.minecraftforge.eventbus.api.SubscribeEvent;
import net.minecraftforge.fml.common.Mod;
import net.minecraftforge.event.TickEvent;
import dev.hole.forge.WorldVoice;

@Mod.EventBusSubscriber(modid="hole",value=Dist.CLIENT)
public final class ClientEvents {
    static boolean allowVanilla;
    @SubscribeEvent public static void opening(ScreenEvent.Opening event) {
        if(event.getNewScreen() instanceof JoinMultiplayerScreen && !allowVanilla) event.setNewScreen(new LobbyScreen(event.getCurrentScreen()));
    }
    @SubscribeEvent public static void init(ScreenEvent.Init.Post event) {
        if(event.getScreen() instanceof PauseScreen screen)event.addListener(Button.builder(Component.literal("Hole 语音"),b->Minecraft.getInstance().setScreen(new VoiceScreen(screen))).bounds(screen.width-106,30,100,20).build());
        if(event.getScreen() instanceof PauseScreen screen && Minecraft.getInstance().hasSingleplayerServer()) {
            event.addListener(Button.builder(Component.literal("Hole 联机"),b->Minecraft.getInstance().setScreen(new LobbyScreen(screen)))
                .bounds(screen.width-106,6,100,20).build());
        }
    }
    @SubscribeEvent public static void disconnected(ClientPlayerNetworkEvent.LoggingOut event) {
        // ConnectScreen also clears the client before connecting, with no player.
        if(event.getPlayer()!=null)HoleMod.RUNTIME.leaveGame();
    }
    @SubscribeEvent public static void tick(TickEvent.ClientTickEvent event){
        if(event.phase!=TickEvent.Phase.END)return;
        Minecraft mc=Minecraft.getInstance();
        while(ClientVoiceKeys.SETTINGS.consumeClick())mc.setScreen(new VoiceScreen(mc.screen));
        while(ClientVoiceKeys.MUTE.consumeClick())if(HoleMod.RUNTIME.voice.active())HoleMod.RUNTIME.voice.mode(HoleMod.RUNTIME.voice.mode()==WorldVoice.Mode.LISTEN?WorldVoice.Mode.OPEN:WorldVoice.Mode.LISTEN);
        HoleMod.RUNTIME.voice.pressed(mc.level!=null && mc.screen==null && mc.isWindowActive() && ClientVoiceKeys.TALK.isDown());
        if(!mc.isWindowActive())HoleMod.RUNTIME.voice.mode(WorldVoice.Mode.LISTEN);
    }
    @SubscribeEvent public static void overlay(RenderGuiOverlayEvent.Post event){
        if(!event.getOverlay().id().getPath().equals("hotbar") || Minecraft.getInstance().level==null)return;
        var runtime=HoleMod.RUNTIME;var g=event.getGuiGraphics();
        int y=Minecraft.getInstance().getWindow().getGuiScaledHeight()-52;
        VoiceIcons.draw(g,8,y,runtime.voice.transmitting(),runtime.voice.active());
        g.drawString(Minecraft.getInstance().font,runtime.voice.label(),34,y+7,0xffffff);
    }
}
