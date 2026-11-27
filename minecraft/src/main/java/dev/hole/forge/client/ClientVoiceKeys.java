package dev.hole.forge.client;

import net.minecraft.client.KeyMapping;
import net.minecraftforge.client.event.RegisterKeyMappingsEvent;
import net.minecraftforge.eventbus.api.SubscribeEvent;
import net.minecraftforge.fml.common.Mod;
import net.minecraftforge.api.distmarker.Dist;
import org.lwjgl.glfw.GLFW;

@Mod.EventBusSubscriber(modid="hole",value=Dist.CLIENT,bus=Mod.EventBusSubscriber.Bus.MOD)
public final class ClientVoiceKeys {
    public static final KeyMapping SETTINGS=new KeyMapping("Hole 语音设置",GLFW.GLFW_KEY_V,"Hole 世界语音");
    public static final KeyMapping TALK=new KeyMapping("Hole 按住说话",GLFW.GLFW_KEY_CAPS_LOCK,"Hole 世界语音");
    public static final KeyMapping MUTE=new KeyMapping("Hole 麦克风开关",GLFW.GLFW_KEY_M,"Hole 世界语音");
    @SubscribeEvent public static void keys(RegisterKeyMappingsEvent event){event.register(SETTINGS);event.register(TALK);event.register(MUTE);}
}
