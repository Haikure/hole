package dev.hole.forge.client;

import dev.hole.forge.*;
import net.minecraft.client.gui.GuiGraphics;
import net.minecraft.client.gui.components.*;
import net.minecraft.client.gui.screens.Screen;
import net.minecraft.network.chat.Component;
import java.util.List;

final class SettingsScreen extends Screen {
    private final Screen parent;
    private final String[] labels={"信令地址（wss://主机/ws）","信令密码","房间名","房间密码","Hole 中继设备名（可选）"};
    private EditBox[] fields;
    private HoleSettings values;
    private String error="";
    SettingsScreen(Screen parent) {super(Component.literal("Hole 房间设置"));this.parent=parent;values=HoleMod.RUNTIME.settings();}
    @Override protected void init() {
        if(fields!=null) capture();
        fields=new EditBox[5];
        String[] data={values.serverUrl,values.password,values.room,values.token,values.relayDevice};
        int top=Math.max(30,height/2-108);
        for(int i=0;i<fields.length;i++) {
            EditBox field=new EditBox(font,width/2-150,top+i*35+12,300,20,Component.literal(labels[i]));
            field.setMaxLength(i==0?2048:128);field.setValue(data[i]);
            if(i==1 || i==3) field.setFormatter((text,offset)->net.minecraft.util.FormattedCharSequence.forward("*".repeat(text.length()),net.minecraft.network.chat.Style.EMPTY));
            fields[i]=addRenderableWidget(field);
        }
        addRenderableWidget(Button.builder(Component.literal("保存并连接"),b->{
            capture();
            try {values.validate();}catch(Exception e){error=e.getMessage();return;}
            b.active=false;
            HoleMod.RUNTIME.configure(values.copy()).whenComplete((ignored,failure)->minecraft.execute(()->{
                b.active=true;
                if(failure==null && minecraft.screen==this) minecraft.setScreen(parent);
                else error=HoleMod.RUNTIME.status();
            }));
        }).bounds(width/2-150,top+190,145,20).build());
        addRenderableWidget(Button.builder(Component.literal("取消"),b->onClose()).bounds(width/2+5,top+190,145,20).build());
    }
    private void capture() { values.serverUrl=fields[0].getValue().trim();values.password=fields[1].getValue();values.room=fields[2].getValue().trim();values.token=fields[3].getValue();values.relayDevice=fields[4].getValue().trim(); }
    @Override public void tick(){for(EditBox field:fields)field.tick();}
    @Override public void onClose(){minecraft.setScreen(parent);}
    @Override public void render(GuiGraphics graphics,int x,int y,float partial) {
        renderBackground(graphics);super.render(graphics,x,y,partial);
        graphics.drawCenteredString(font,title,width/2,10,0xffffff);
        for(int i=0;i<fields.length;i++)graphics.drawString(font,labels[i],fields[i].getX(),fields[i].getY()-11,0xcccccc,false);
        if(!error.isEmpty())graphics.drawWordWrap(font,Component.literal(error),width/2-150,Math.min(height-28,fields[4].getY()+60),300,0xff7777);
    }
    @Override public boolean isPauseScreen(){return false;}
}
