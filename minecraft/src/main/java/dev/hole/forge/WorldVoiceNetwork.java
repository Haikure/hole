package dev.hole.forge;

import net.minecraft.resources.ResourceLocation;
import net.minecraft.network.FriendlyByteBuf;
import net.minecraft.server.level.ServerPlayer;
import net.minecraftforge.network.*;
import net.minecraftforge.network.simple.SimpleChannel;
import net.minecraftforge.event.entity.player.PlayerEvent;
import net.minecraftforge.eventbus.api.SubscribeEvent;
import net.minecraftforge.fml.common.Mod;
import java.security.SecureRandom;
import java.util.HexFormat;
import java.util.function.Supplier;

/** World membership comes from a play packet delivered after Minecraft login. */
@Mod.EventBusSubscriber(modid="hole")
public final class WorldVoiceNetwork {
    private static final String VERSION="1";
    private static final SimpleChannel CHANNEL=NetworkRegistry.newSimpleChannel(new ResourceLocation("hole","world_voice"),()->VERSION,v->VERSION.equals(v)||NetworkRegistry.ABSENT.equals(v)||NetworkRegistry.ACCEPTVANILLA.equals(v),v->VERSION.equals(v)||NetworkRegistry.ABSENT.equals(v)||NetworkRegistry.ACCEPTVANILLA.equals(v));
    private static String group="";
    public static void register(){CHANNEL.messageBuilder(Membership.class,0,NetworkDirection.PLAY_TO_CLIENT).encoder(Membership::encode).decoder(Membership::decode).consumerMainThread(Membership::handle).add();}
    public static void start(){byte[] random=new byte[32];new SecureRandom().nextBytes(random);group=HexFormat.of().formatHex(random);}
    public static void stop(){group="";}
    @SubscribeEvent public static void login(PlayerEvent.PlayerLoggedInEvent event){
        if(event.getEntity() instanceof ServerPlayer player && !group.isEmpty() && CHANNEL.isRemotePresent(player.connection.connection))CHANNEL.send(PacketDistributor.PLAYER.with(()->player),new Membership(group));
    }
    private record Membership(String group){
        void encode(FriendlyByteBuf out){out.writeUtf(group,64);}
        static Membership decode(FriendlyByteBuf in){String group=in.readUtf(64);if(!group.matches("[a-f0-9]{64}"))throw new IllegalArgumentException("Invalid voice world");return new Membership(group);}
        void handle(Supplier<NetworkEvent.Context> context){HoleMod.RUNTIME.enterWorld(group);context.get().setPacketHandled(true);}
    }
}
