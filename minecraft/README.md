# Minecraft Forge 联机模组

面向 Minecraft Java 1.20.1 / Forge 47.4.0+ 的实验版模组，Java 17 为最低目标。
同一 Hole 房间中的玩家可以发现共享世界和独立服务器，选择后自动建立本地 TCP 映射。
游戏协议、账号认证和 Forge 模组握手保持 Minecraft 的原有行为。

## 安装与使用

把 `dist/minecraft/hole-forge-1.20.1-0.1.0.jar` 放入实例的 `mods/`。
包内包含 Windows x64、Linux x64 / ARM64 核心进程，以及 Android ARM64 JNI 库。
Windows ARM64 可尝试 x64 Java；本版没有 Windows ARM64 原生核心。

1. 点击“多人游戏”，进入 Hole 大厅，在“设置”中填写信令地址、信令密码、房间名、房间密码。
2. 可选填写同房间内的 Hole 中继设备名，例如 `relay-node`。该设备需预先按 Hole 中继文档配置运行。
3. 房主进入单人世界，暂停菜单点击“Hole 联机”，然后“共享世界”。模组开放内置服务器并读取端口。
4. 好友连接同一 Hole 房间后，列表自动出现世界；点击“加入”，等待通道建立后进入游戏。

世界名称显示最多 27 个 UTF-8 字节，设备短 ID 用于区分重名世界。
大厅保留“服务器地址列表”入口。停止共享只撤下 Hole 映射；Minecraft 已开放的局域网监听要退出世界才关闭。
同房间的所有成员均能发现和访问已共享世界。各端仍需满足 MC / Forge / 整合包及账号兼容要求。

本模组默认关闭 TURN，先尝试直连、现有 Peer Relay，再使用填写的 Hole 中继。
不需要玩家配置游戏端口、服务 ID、设备 ID 或邀请代码。

## 大厅信息与连接进度

世界卡片显示在线人数 / 人数上限、MC 版本和简介，悬停显示完整详情和房主设备。简介来自服务器 MOTD；
人数来自 MC 服务器的实际玩家列表，包含单人房主，与 Hole 房间设备数量无关。
服务器在 tick 结束时检查信息变化，仅变化时提交目录更新；不按秒向 Worker 查询人数。旧版发布者没有信息时显示“人数 / 版本未知”。

每个世界的“测延迟”会按需建立游戏 TCP 通道，再执行一次 MC 状态 / Ping-Pong 请求，显示远端往返毫秒数和直连、设备中继或 Hole 中继路径。
结果记录测量时间，超过一分钟显示“上次”；打开大厅不会批量连接所有房主，也不会自动反复测量。
测量完成后在大厅中保留最近 16 个世界的就绪通道；再次测量或加入同一世界时复用原有映射和端口，无需重新建立通道。
加入世界时释放其他预连接；取消或离开大厅时释放全部预连接，房主重启或撤回世界后清除对应通道和旧结果。
未检测显示“未测量”；通道失败显示“连接失败”；服务器关闭 `enable-status`、不响应或响应异常时显示“测量失败（MC 状态不可用）”，仍可尝试“加入”。
MC 状态不可用时也保留已经就绪的通道供加入使用。测量不登录游戏，不占用玩家名额，不加入世界语音。

加入与测量期间显示等待房主、直连 / NAT 打洞、设备中继 / Hole 中继、映射准备等实际阶段，可随时取消。
通道就绪后交给 Minecraft 原有连接界面显示登录和 Forge 握手；测得的延迟反映测量时的路径，后续连接可能选择不同路径。
阶段读取本地核心快照，延迟请求走数据通道；建立和撤下映射会正常触发信令事件，不新增 Worker 周期轮询或定时器。
Minecraft 开始连接前的空玩家清理事件保留映射；玩家真正退出世界时才撤销游戏映射及语音。

## 世界语音

客户端与房主 / 独立服务器均安装此模组，并更新 Worker。进入世界后按 `V` 打开语音面板，点击“加入世界语音”。
默认语音关闭；加入后默认为仅收听，不开启麦克风。可选择按键说话（默认按住 Caps Lock）或持续开启麦克风，`M` 快速切换麦克风，快捷键可在 MC 控制设置中改绑。面板显示 Minecraft 服务器上报的本玩家到房主延迟，并提供 0–150% 播放音量。
游戏左下角显示麦克风图标与状态文字：关闭或按键待说话时带斜杠，正在发送时为绿色；失去窗口焦点时切回仅收听。
退出世界立即关闭音频并撤销本机语音声明，下次进入需重新主动加入。

语音组由服务器每次启动随机生成，只通过玩家登录后的游戏连接下发，不从世界名称或发现列表推导。
同一 Hole 房间的不同 MC 世界、以及未带世界组的普通 Hole 语音不会互通；同一存档的不同维度属于同一个世界语音组。
本版为世界内全员通话，无距离衰减；仍沿用整个 Hole 房间最多 8 台启用语音设备的限制，独立服务器自身不打开音频。
组标识用于隔离会话，不是针对修改客户端或主动分享组信息的防作弊系统。

Windows / Linux 使用系统默认 48 kHz 单声道 PCM16 音频设备。FCL 使用 Android AudioRecord / AudioTrack；首次开启麦克风时若权限不足，
按界面提示到安卓设置允许 FCL 使用麦克风后，重新加入语音。FCL 可用虚拟按键绑定说话键；安卓回声消除 / 降噪按设备能力启用。
语音经共享核心的 Opus / QUIC 通道传输，游戏内音量和麦克风状态不需要持续访问 Worker。
当前实现已提供桌面与 Android 适配，实际声卡、耳机、FCL 权限和双人通话仍需实机验收。

## 独立服务器

同一个 JAR 安装到 Forge 1.20.1 服务器的 `mods/`。第一次启动生成 `config/hole.json`，填写后重启：

```json
{
  "serverUrl": "wss://HOST/ws",
  "password": "SIGNAL_PASSWORD",
  "room": "my-room",
  "token": "ROOM_PASSWORD",
  "relayDevice": "relay-node",
  "deviceId": "保留首次运行自动生成的值"
}
```

`relayDevice` 留空即可不指定 Hole 中继。请保留每个实例自动生成的 `deviceId`，不要把同一个实例配置完整复制给多台设备。
配置包含明文凭据，不应提交到 Git。POSIX 平台保存时设置为仅所有者可读写。
损坏配置会显示错误并保留原文件，不自动覆盖。

服务器完成启动后自动发布世界，显示名来自 MOTD；当前监听地址和 `getPort()` 返回的端口仅作为本地转发目标。
服务器关闭时停止发布。当前支持普通 Forge 独立服务器，不是 Bukkit / Paper 插件；不自动启动另一个 MC 进程。
不要将 `server-port` 设置为 `0`；本版尚未适配这种随机端口服务器配置。

## Worker 与服务目录

需要同步更新本仓库的 Worker；模组本身不会部署它。
旧 Worker 不公布服务目录，界面会提示更新。信令地址要求 `wss://`。
普通 Hole `provide` 默认只注册转发服务，不发布到发现目录。模组共享世界或独立服务器启动发布时，
自动携带 `discovery: minecraft`；新的签名目录公布显式发布的服务 ID、TCP 协议、`kind: minecraft` 标识，及可选 `minecraft` 世界信息（简介、版本、人数、人数上限），不包含本地目标地址、玩家名单或凭据。
仅把普通服务 ID 改成 `minecraft.` 前缀不会使其被发布。信令面板会为发布者显示 `MC` 标识，房间一览及设备详情展示世界名称，设备详情同时展示人数、版本和简介，尚无人连接时也可见。
每个设备按 ID 排序最多公布 32 条服务，并受字节上限约束；Minecraft 每实例使用一条。
房间当前最多 16 台在线设备，独立服务器和 Hole 中继同样占用设备名额。

## FCL Android ARM64

要求 Android 8+、ARM64 Java 17，以及向游戏进程提供 `DALVIK_JAVAVM`、`DALVIK_APPLICATION` 和私有缓存 `TMPDIR` 的 FCL 版本。
原生库为 Android `arm64-v8a`，使用 16 KiB ELF 对齐。
模组在 FCL 的 Android 运行时中加载自带的小型 DEX 适配器，经公开 `ConnectivityManager` / `Network` API 提供网络快照、DNS 和 socket 绑定。
游戏 JVM 与 Android VM 的对象引用不会混用。切网时轮询检测变化并通知共享核心重建路径。

缺少上述接口时会提示更新 FCL，不能用 Linux ARM64 可执行文件代替。
此实现需要在实际 FCL 版本和手机上验收；编译成功不代表已通过手机联机、切网或后台恢复测试。

## 构建与验证

从根目录运行：

```bash
./build.sh minecraft
```

完整包当前在 Linux x64 构建，需要已有 Go 1.27.4、JDK 17、Gradle 8、Android SDK 37.0、Build Tools 36.0.0、NDK 28.2.13676358。
不自动安装工具链。通过 `HOLE_MINECRAFT_JAVA_HOME` / `HOLE_MINECRAFT_GRADLE` 可指定已有工具，Android 路径沿用 `HOLE_TOOLCHAIN_ENV`。
所有缓存进入根 `.cache/`；`--offline` 复用缓存，`--output` 指定交付目录；该目标不接受 `--os` / `--arch`，也不隐式包含在 `all`。

GitHub Actions 发布工作流支持手动选择 `type=minecraft`，忽略 `arch` / `os`，上传只含一个 JAR 的
`hole-minecraft-forge-1.20.1` Artifact。工作流的 `type=all` 和 tag 构建也包含模组；tag 构建成功后，
`hole-forge-1.20.1-<版本>.jar` 会随其他产物发布到 GitHub Release。CI 使用 Gradle 8.14.3，完整模组构建包含 JVM 测试，
单独构建模组不需要 Android APK 签名 secrets。详情见[构建与发布说明](../docs/BUILDING.md#github-actions-构建与发布)。

```bash
bash minecraft/scripts/gradle.sh test
source scripts/build-env.sh
go test -race ./core -run 'TestRoomDiscovery|TestServiceAdvertisements|TestRoomControl'
node --test worker/worker_ice.test.mjs worker/worker_fixture.test.mjs
```

JVM 测试中的真实核心进程检查需要先完成核心资源构建。
发布前仍需用两个真实 MC 客户端验证加入、整合包握手、独立服务器、直连 / Hole 中继、退出清理，并在 FCL 手机上验证 JNI 与切网。
