package dev.hole.forge.android;

import android.content.Context;
import android.net.ConnectivityManager;
import android.net.LinkProperties;
import android.net.Network;
import android.net.RouteInfo;
import android.os.Build;
import android.os.ParcelFileDescriptor;
import java.net.InetAddress;
import org.json.JSONArray;
import org.json.JSONObject;

/** Runs in FCL's ART VM, not the Minecraft JVM. Platform network and audio only. */
public final class NetworkBridge {
    private final ConnectivityManager manager;
    private final AudioBridge audio;
    public NetworkBridge(Context context) {
        manager = (ConnectivityManager) context.getSystemService(Context.CONNECTIVITY_SERVICE);
        audio = new AudioBridge(context);
    }
    private Network find(String handle) {
        long networkHandle;
        try {
            networkHandle = Long.parseLong(handle);
        } catch (NumberFormatException ignored) {
            throw new IllegalStateException("Selected Android network is unavailable");
        }
        Network network = Build.VERSION.SDK_INT >= Build.VERSION_CODES.P
            ? Network.fromNetworkHandle(networkHandle) : findLegacy(networkHandle);
        if (network != null && manager.getLinkProperties(network) != null) return network;
        throw new IllegalStateException("Selected Android network is unavailable");
    }
    @SuppressWarnings("deprecation")
    private Network findLegacy(long handle) {
        for (Network network : manager.getAllNetworks()) {
            if (network.getNetworkHandle() == handle) return network;
        }
        return null;
    }
    public String call(String operation, String input) throws Exception {
        if(operation.equals("audio"))return audio.call(input);
        if (operation.equals("network") || operation.equals("interfaces")) {
            Network network = manager.getActiveNetwork();
            LinkProperties links = network == null ? null : manager.getLinkProperties(network);
            if (links == null) return operation.equals("interfaces") ? "[]" : "{\"available\":false}";
            JSONArray addresses = new JSONArray(), dns = new JSONArray();
            links.getLinkAddresses().forEach(a -> addresses.put(a.getAddress().getHostAddress()));
            links.getDnsServers().forEach(a -> dns.put(a.getHostAddress()));
            if (operation.equals("interfaces")) return new JSONArray().put(new JSONObject()
                .put("name",links.getInterfaceName()).put("up",true).put("addresses",addresses)).toString();
            String gateway = "";
            for (RouteInfo route : links.getRoutes()) {
                if (route.isDefaultRoute() && route.getGateway() instanceof java.net.Inet4Address) gateway=route.getGateway().getHostAddress();
            }
            return new JSONObject().put("available",true).put("handle",Long.toString(network.getNetworkHandle()))
                .put("interface",links.getInterfaceName()).put("addresses",addresses).put("dns",dns).put("gateway",gateway).toString();
        }
        JSONObject request = new JSONObject(input);
        Network network = find(request.getString("handle"));
        if (operation.equals("bind")) {
            try (ParcelFileDescriptor fd = ParcelFileDescriptor.fromFd(request.getInt("fd"))) {
                network.bindSocket(fd.getFileDescriptor());
            }
            return "{}";
        }
        if (operation.equals("lookup")) {
            JSONArray result = new JSONArray();
            for (InetAddress address : network.getAllByName(request.getString("host"))) result.put(address.getHostAddress());
            return result.toString();
        }
        throw new IllegalArgumentException("Unknown network operation");
    }
}
