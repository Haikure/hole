package dev.hole.forge.client;

import dev.hole.forge.*;
import net.minecraft.client.gui.GuiGraphics;
import net.minecraft.client.gui.components.Button;
import net.minecraft.client.gui.components.Tooltip;
import net.minecraft.client.gui.screens.*;
import net.minecraft.client.gui.screens.multiplayer.JoinMultiplayerScreen;
import net.minecraft.client.multiplayer.ServerData;
import net.minecraft.client.multiplayer.resolver.ServerAddress;
import net.minecraft.client.server.IntegratedServer;
import net.minecraft.network.chat.Component;
import net.minecraft.util.HttpUtil;
import java.util.List;

public final class LobbyScreen extends Screen {
    private final Screen parent;
    private List<WorldEntry> displayed=List.of();
    private int page;
    private boolean busy,handoff;
    private long action;
    private String message="";
    private Button share;
    public LobbyScreen(Screen parent){super(Component.literal("Hole 多人游戏"));this.parent=parent;}
    private int rows(){return Math.max(1,(height-145)/60);}
    private int listWidth(){return Math.min(440,width-24);}
    @Override protected void init() {
        if(handoff){handoff=false;HoleMod.RUNTIME.cancelConnection();}
        displayed=HoleMod.RUNTIME.worlds();
        int rows=rows(),pages=Math.max(1,(displayed.size()+rows-1)/rows),left=(width-listWidth())/2;
        page=Math.min(page,pages-1);
        for(int i=page*rows;i<Math.min(displayed.size(),(page+1)*rows);i++) {
            WorldEntry entry=displayed.get(i);int y=52+(i-page*rows)*60;
            Button enter=Button.builder(Component.literal("加入"),b->open(entry,false)).bounds(left+listWidth()-52,y+7,48,20).build();
            enter.active=!busy && minecraft.level==null;addRenderableWidget(enter);
            Button measure=Button.builder(Component.literal("测延迟"),b->open(entry,true)).bounds(left+listWidth()-52,y+31,48,20)
                .tooltip(Tooltip.create(Component.literal("测量远端 MC 往返延迟；保留就绪通道，加入时直接复用。"))).build();
            measure.active=!busy && minecraft.level==null;addRenderableWidget(measure);
        }
        Button settings=addRenderableWidget(Button.builder(Component.literal("设置"),b->minecraft.setScreen(new SettingsScreen(this))).bounds(width/2-150,height-65,70,20).build());settings.active=!busy;
        Button connect=addRenderableWidget(Button.builder(Component.literal("连接房间"),b->HoleMod.RUNTIME.connect()).bounds(width/2-75,height-65,90,20).build());connect.active=!busy;
        share=addRenderableWidget(Button.builder(Component.literal(HoleMod.RUNTIME.hosting()?"停止共享":"共享世界"),b->share()).bounds(width/2+20,height-65,90,20).build());
        share.active=minecraft.hasSingleplayerServer() && !busy;
        Button next=addRenderableWidget(Button.builder(Component.literal(">"),b->{page=(page+1)%pages;rebuildWidgets();}).bounds(width/2+115,height-65,35,20).build());next.active=pages>1 && !busy;
        addRenderableWidget(Button.builder(Component.literal(busy?"取消连接 / 测量":"返回"),b->{
            if(busy){action++;busy=false;HoleMod.RUNTIME.cancelConnection();message="已取消";rebuildWidgets();}else onClose();
        }).bounds(width/2-150,height-40,145,20).build());
        Button vanilla=addRenderableWidget(Button.builder(Component.literal("服务器地址列表"),b->{
            ClientEvents.allowVanilla=true;
            try {minecraft.setScreen(new JoinMultiplayerScreen(this));}finally{ClientEvents.allowVanilla=false;}
        }).bounds(width/2+5,height-40,145,20).build());vanilla.active=!busy;
        if(HoleMod.RUNTIME.settings().ready())HoleMod.RUNTIME.connect();
    }
    private void share() {
        if(HoleMod.RUNTIME.hosting()){HoleMod.RUNTIME.unpublish();return;}
        if(!HoleMod.RUNTIME.settings().ready()){minecraft.setScreen(new SettingsScreen(this));return;}
        IntegratedServer server=minecraft.getSingleplayerServer();
        if(server==null)return;
        if(!server.isPublished() && !server.publishServer(server.getDefaultGameType(),false,HttpUtil.getAvailablePort())) {message="无法开放 Minecraft 内置服务器端口";return;}
        server.execute(()->HoleMod.RUNTIME.publish("127.0.0.1",server.getPort(),server.getWorldData().getLevelName(),HoleMod.worldInfo(server)));
    }
    private void open(WorldEntry entry,boolean measure) {
        long token=++action;busy=true;message="正在等待房主响应";rebuildWidgets();
        (measure?HoleMod.RUNTIME.measure(entry):HoleMod.RUNTIME.join(entry)).whenComplete((port,error)->minecraft.execute(()->{
            if(token!=action || minecraft.screen!=this)return;
            busy=false;
            if(error!=null){message=HoleMod.RUNTIME.status();rebuildWidgets();return;}
            if(measure){var result=HoleMod.RUNTIME.latency(entry);message=result==null?"未测量":result.label();rebuildWidgets();return;}
            String address="127.0.0.1:"+port;handoff=true;
            ConnectScreen.startConnecting(this,minecraft,ServerAddress.parseString(address),new ServerData(entry.name(),address,false),false);
        }));
    }
    @Override public void tick() {
        if(!displayed.equals(HoleMod.RUNTIME.worlds()))rebuildWidgets();
        if(share!=null)share.setMessage(Component.literal(HoleMod.RUNTIME.hosting()?"停止共享":"共享世界"));
    }
    @Override public void onClose(){minecraft.setScreen(parent);}
    @Override public void removed(){if(!handoff){action++;busy=false;HoleMod.RUNTIME.cancelConnection();}}
    @Override public boolean isPauseScreen(){return false;}
    @Override public void render(GuiGraphics graphics,int x,int y,float partial) {
        renderBackground(graphics);
        int left=(width-listWidth())/2,rows=rows(),textWidth=listWidth()-64;
        for(int i=page*rows;i<Math.min(displayed.size(),(page+1)*rows);i++){
            WorldEntry entry=displayed.get(i);int top=52+(i-page*rows)*60;
            graphics.fill(left,top,left+listWidth(),top+58,0x99000000);
            graphics.drawString(font,font.plainSubstrByWidth(entry.name(),textWidth),left+5,top+4,0xffffff);
            graphics.drawString(font,font.plainSubstrByWidth(entry.info().summary(),textWidth),left+5,top+17,0xaaaaaa);
            var latency=HoleMod.RUNTIME.latency(entry);String timing=latency==null?"未测量":latency.label();
            graphics.drawString(font,font.plainSubstrByWidth(timing,textWidth),left+5,top+31,latency!=null && latency.error().isEmpty()?0x88ddaa:0xcccccc);
            graphics.drawString(font,font.plainSubstrByWidth(entry.info().description().isBlank()?"暂无简介":entry.info().description(),textWidth),left+5,top+44,0x999999);
        }
        super.render(graphics,x,y,partial);
        graphics.drawCenteredString(font,title,width/2,10,0xffffff);
        graphics.drawCenteredString(font,font.plainSubstrByWidth(HoleMod.RUNTIME.status(),width-24),width/2,28,0xcccccc);
        if(displayed.isEmpty())graphics.drawCenteredString(font,Component.literal("同房间内已共享的世界会显示在这里"),width/2,65,0x999999);
        String detail=busy?HoleMod.RUNTIME.progress():message;
        if(!detail.isEmpty())graphics.drawCenteredString(font,font.plainSubstrByWidth(detail,width-24),width/2,height-82,0xffdd88);
        int pages=Math.max(1,(displayed.size()+rows-1)/rows);
        graphics.drawCenteredString(font,"第 "+(page+1)+" / "+pages+" 页 · 世界详情请悬停",width/2,height-14,0x888888);
        for(int i=page*rows;i<Math.min(displayed.size(),(page+1)*rows);i++){
            int top=52+(i-page*rows)*60;
            if(x>=left && x<left+listWidth()-56 && y>=top && y<top+58){
                WorldEntry entry=displayed.get(i);var latency=HoleMod.RUNTIME.latency(entry);
                String info=entry.name()+"\n"+entry.info().summary()+"\n"+(entry.info().description().isBlank()?"暂无简介":entry.info().description())+"\n房主："+entry.device();
                if(latency!=null)info+="\n"+latency.label()+" · "+Math.max(0,(System.currentTimeMillis()-latency.measuredAt())/1000)+" 秒前";
                graphics.renderTooltip(font,font.split(Component.literal(info),Math.min(300,width-24)),x,y);break;
            }
        }
    }
}
