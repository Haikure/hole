package dev.hole.forge.client;

import net.minecraft.client.gui.GuiGraphics;

final class VoiceIcons {
    static void draw(GuiGraphics g,int x,int y,boolean transmitting,boolean enabled){
        int color=transmitting?0xff65dd7d:enabled?0xffefb25c:0xffa1a1a1;
        g.fill(x-3,y-2,x+22,y+23,0xbb161c20);
        g.fill(x+6,y+1,x+12,y+12,color);g.fill(x+3,y+8,x+5,y+15,color);g.fill(x+13,y+8,x+15,y+15,color);
        g.fill(x+5,y+14,x+13,y+16,color);g.fill(x+8,y+16,x+10,y+19,color);g.fill(x+5,y+19,x+13,y+21,color);
        if(!transmitting)for(int i=0;i<19;i++)g.fill(x+i,y+i,x+i+2,y+i+2,0xfff06b6b);
    }
}
