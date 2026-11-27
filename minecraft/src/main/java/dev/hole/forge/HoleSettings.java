package dev.hole.forge;

import com.google.gson.Gson;
import com.google.gson.GsonBuilder;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.nio.file.attribute.PosixFilePermissions;
import java.util.UUID;

public final class HoleSettings {
    private static final Gson JSON = new GsonBuilder().setPrettyPrinting().create();
    public String serverUrl = "";
    public String password = "";
    public String room = "";
    public String token = "";
    public String relayDevice = "";
    public String deviceId = "mc-" + UUID.randomUUID().toString().replace("-", "");

    public boolean ready() { return !serverUrl.isBlank() && !password.isEmpty() && !room.isBlank() && !token.isEmpty(); }
    public void validate() {
        if (!ready()) throw new IllegalArgumentException("请填写信令地址、信令密码、房间名和房间密码");
        java.net.URI uri = java.net.URI.create(serverUrl);
        if (!"wss".equals(uri.getScheme()) || uri.getHost() == null || uri.getUserInfo() != null)
            throw new IllegalArgumentException("信令地址需要 wss://主机/ws");
        if (!relayDevice.isEmpty() && !relayDevice.matches("[A-Za-z0-9._-]{1,128}"))
            throw new IllegalArgumentException("Hole 中继需要填写同房间内的设备名");
    }
    public HoleSettings copy() { return JSON.fromJson(JSON.toJson(this), HoleSettings.class); }
    public static HoleSettings load(Path path) throws IOException {
        if (!Files.exists(path)) { HoleSettings settings = new HoleSettings(); settings.save(path); return settings; }
        if (Files.size(path) > 16384) throw new IOException("Hole 配置超过 16 KiB");
        try {
            HoleSettings settings = JSON.fromJson(Files.readString(path), HoleSettings.class);
            if (settings == null || settings.serverUrl == null || settings.password == null || settings.room == null || settings.token == null || settings.relayDevice == null || settings.deviceId == null || !settings.deviceId.matches("mc-[a-f0-9]{32}"))
                throw new IOException("Hole 配置字段无效");
            return settings;
        } catch (com.google.gson.JsonParseException e) { throw new IOException("Hole 配置 JSON 无效",e); }
    }
    public void save(Path path) throws IOException {
        Files.createDirectories(path.toAbsolutePath().getParent());
        Path stage = Files.createTempFile(path.toAbsolutePath().getParent(), ".hole-", ".json");
        try {
            if (Files.getFileStore(stage).supportsFileAttributeView("posix")) Files.setPosixFilePermissions(stage, PosixFilePermissions.fromString("rw-------"));
            Files.writeString(stage, JSON.toJson(this), StandardCharsets.UTF_8);
            try { Files.move(stage,path,StandardCopyOption.ATOMIC_MOVE,StandardCopyOption.REPLACE_EXISTING); }
            catch (AtomicMoveNotSupportedException e) { Files.move(stage,path,StandardCopyOption.REPLACE_EXISTING); }
        } finally { Files.deleteIfExists(stage); }
    }
}
