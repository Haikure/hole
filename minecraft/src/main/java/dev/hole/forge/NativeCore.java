package dev.hole.forge;

import com.google.gson.JsonObject;
import com.google.gson.JsonParser;
import java.io.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.nio.file.attribute.PosixFilePermissions;
import java.security.MessageDigest;
import java.util.HexFormat;
import java.util.Locale;

public final class NativeCore implements CoreClient {
    private static boolean loaded;
    private long handle;
    public NativeCore() throws Exception {
        synchronized (NativeCore.class) {
            if (!loaded) {
                System.load(extract("android-arm64/libholemc.so", false).toString());
                loaded = true;
            }
            handle = open(resource("/hole/android/network.dex"));
        }
        if (handle == 0) throw new IOException("无法创建 Hole 核心");
    }
    public static boolean android() {
        return System.getenv("FCL_NATIVEDIR") != null || System.getenv("ANDROID_ROOT") != null;
    }
    static String platform() {
        String arch = System.getProperty("os.arch", "").toLowerCase(Locale.ROOT);
        arch = switch (arch) { case "aarch64", "arm64" -> "arm64"; case "amd64", "x86_64" -> "amd64"; default -> throw new IllegalStateException("Hole 不支持架构："+arch); };
        if (android()) {
            if (!arch.equals("arm64")) throw new IllegalStateException("安卓 Hole 需要 ARM64 Java 运行时");
            return "android-arm64";
        }
        String os = System.getProperty("os.name", "").toLowerCase(Locale.ROOT);
        if (os.startsWith("windows") && arch.equals("amd64")) return "windows-amd64";
        if (os.equals("linux")) return "linux-"+arch;
        throw new IllegalStateException("Hole 暂不支持此系统："+os);
    }
    static byte[] resource(String name) throws IOException {
        try (InputStream in=NativeCore.class.getResourceAsStream(name)) {
            if (in==null) throw new IOException("模组缺少资源："+name+"，请使用完整构建的 JAR");
            return in.readAllBytes();
        }
    }
    static Path extract(String relative, boolean executable) throws Exception {
        byte[] bytes=resource("/hole/native/"+relative);
        String expected=new String(resource("/hole/native/"+relative+".sha256"),StandardCharsets.US_ASCII).trim();
        if (!HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(bytes)).equals(expected)) throw new IOException("Hole 核心校验失败");
        Path base=Path.of(System.getProperty("java.io.tmpdir"));
        if (android()) {
            String cache=System.getenv("TMPDIR");
            if (cache==null || !Path.of(cache).isAbsolute()) throw new IOException("FCL 未提供私有缓存目录 TMPDIR");
            base=Path.of(cache);
        }
        Path directory=Files.createTempDirectory(base,"hole-mc-");
        if (Files.getFileStore(directory).supportsFileAttributeView("posix")) Files.setPosixFilePermissions(directory,PosixFilePermissions.fromString("rwx------"));
        Path file=directory.resolve(Path.of(relative).getFileName());
        directory.toFile().deleteOnExit(); file.toFile().deleteOnExit();
        Files.write(file,bytes,StandardOpenOption.CREATE_NEW);
        if (executable && !file.toFile().setExecutable(true,true)) throw new IOException("无法设置 Hole 核心执行权限");
        return file.toAbsolutePath();
    }
    private static native long open(byte[] dex);
    private static native byte[] call(long handle,String operation,byte[] input);
    private static native void close(long handle);
    public synchronized JsonObject invoke(String op,JsonObject input) throws Exception {
        if (handle==0) throw new IOException("Hole 核心已关闭");
        JsonObject response=JsonParser.parseString(new String(call(handle,op,input.toString().getBytes(StandardCharsets.UTF_8)),StandardCharsets.UTF_8)).getAsJsonObject();
        if (response.has("error")) throw new IOException(response.get("error").getAsString());
        String value=response.get("value").getAsString();
        return value.isEmpty()?new JsonObject():JsonParser.parseString(value).getAsJsonObject();
    }
    public synchronized void close() { if (handle!=0) { close(handle); handle=0; } }
    public void startAudio() throws Exception { pullAudio(); }
    public void stopAudio() throws Exception { muteAudio(true);pullAudio(); }
    public void pushAudio(long sequence,byte[] pcm) throws Exception {
        JsonObject input=new JsonObject();input.addProperty("sequence",sequence);input.addProperty("pcm",java.util.Base64.getEncoder().encodeToString(pcm));invoke("audio_push",input);
    }
    public byte[] pullAudio() throws Exception {
        JsonObject response=invoke("audio_pull",new JsonObject());
        return response.get("pcm").isJsonNull()?new byte[0]:java.util.Base64.getDecoder().decode(response.get("pcm").getAsString());
    }
}
