package dev.hole.forge.client;

import net.minecraftforge.client.event.ClientPlayerNetworkEvent;
import org.junit.Test;

public class ClientEventsTest {
    @Test public void preConnectCleanupWithNoPlayerDoesNotEnterTheRuntime() {
        // Forge fires this before every ConnectScreen connection. There is no
        // mod runtime in this JVM test; entering it would initialize HoleMod.
        ClientEvents.disconnected(new ClientPlayerNetworkEvent.LoggingOut(null,null,null));
    }
}
