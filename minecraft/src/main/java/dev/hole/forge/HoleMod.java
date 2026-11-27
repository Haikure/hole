package dev.hole.forge;

import com.mojang.logging.LogUtils;
import net.minecraft.server.MinecraftServer;
import net.minecraft.SharedConstants;
import net.minecraftforge.event.TickEvent;
import net.minecraftforge.common.MinecraftForge;
import net.minecraftforge.event.server.*;
import net.minecraftforge.fml.common.Mod;
import net.minecraftforge.fml.loading.FMLPaths;

@Mod("hole")
public final class HoleMod {
    public static final HoleRuntime RUNTIME=new HoleRuntime(FMLPaths.CONFIGDIR.get().resolve("hole.json"));
    private WorldInfo lastInfo=WorldInfo.UNKNOWN;
    public HoleMod() {
        WorldVoiceNetwork.register();
        MinecraftForge.EVENT_BUS.addListener(this::started);
        MinecraftForge.EVENT_BUS.addListener(this::stopping);
        MinecraftForge.EVENT_BUS.addListener(this::tick);
        java.lang.Runtime.getRuntime().addShutdownHook(new Thread(RUNTIME::close,"Hole shutdown"));
    }
    private void started(ServerStartedEvent event) {
        WorldVoiceNetwork.start();
        MinecraftServer server=event.getServer();
        if(!server.isDedicatedServer())return;
        if(!RUNTIME.settings().ready()) {LogUtils.getLogger().info("Hole: 填写 config/hole.json 后重启服务器即可自动发布");return;}
        String address=server.getLocalIp();
        if(address==null || address.isBlank() || address.equals("0.0.0.0"))address="127.0.0.1";
        else if(address.equals("::"))address="::1";
        RUNTIME.publish(address,server.getPort(),server.getMotd(),worldInfo(server)).whenComplete((ignored,error)-> {
            if(error!=null) LogUtils.getLogger().warn("Hole: {}",RUNTIME.status());
            else LogUtils.getLogger().info("Hole: 已提交服务器发布，端口 {}；等待信令与映射就绪",server.getPort());
        });
    }
    private void stopping(ServerStoppingEvent event) {
        lastInfo=WorldInfo.UNKNOWN;
        WorldVoiceNetwork.stop();
        RUNTIME.unpublish();
        if(event.getServer().isDedicatedServer()) RUNTIME.disconnect();
    }
    public static WorldInfo worldInfo(MinecraftServer server){
        return new WorldInfo(server.getMotd(),SharedConstants.getCurrentVersion().getName(),server.getPlayerCount(),server.getMaxPlayers());
    }
    private void tick(TickEvent.ServerTickEvent event){
        if(event.phase!=TickEvent.Phase.END || !RUNTIME.hosting())return;
        // End of tick observes the real player list after both login and logout.
        WorldInfo info=worldInfo(event.getServer());
        if(!info.equals(lastInfo)){lastInfo=info;RUNTIME.updateWorld(info);}
    }
}
