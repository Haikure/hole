# hole

通过房间连接设备，将一端的 TCP / UDP 服务映射到另一端的本地端口。
CLI、Android 手机与 Wear OS 共用 Go 核心；Cloudflare Worker 负责信令和按需 TURN 凭据签发。

## 功能

- ICE IPv4 / IPv6 直连，按需尝试 TURN UDP、TCP、TLS 中继；中继阶段优先选择单跳（非对称）候选对。
- 一对设备共享 QUIC 传输，支持双向、多条 TCP / UDP 映射。
- 网络切换和传输重连时保留仍存活的应用会话，TCP 支持确认、重放和半关闭。
- 原生 Android 客户端提供 Material 3 / Miuix 主题、前台服务、配置导入导出和连接报告。
- Wear 安装包包含 ARM32 / ARM64，独立运行，与手机端共用界面和核心。

这是端口映射工具，不创建 VPN / TUN。业务数据经 QUIC 加密传输；协调服务不转发业务数据，
中继由 TURN 服务承担。连接策略和恢复边界见 [协议说明](docs/PROTOCOL.md)。

## 快速开始：CLI

准备 Go 1.26+、Bash 和 Python 3，在仓库根目录执行：

```bash
./build.sh cli
cp -n examples/ice.yaml config.yaml
```

编辑 `config.yaml`。两端使用相同的服务器、房间号、信令密码和房间密码，设备名各不相同：

```yaml
server_url: wss://HOST/ws   # 信令服务器地址，必填
room: ROOM  # 房间号，同一房间内设备允许进行连接
password: SIGNAL_PASSWORD   # 信令服务器密码
token: ROOM_TOKEN   # 房间密码
device_name: desktop    # 当前设备名，房间内唯一，用于区分设备
session_timeout: 10m    # 断开会话重建超时时间
transport:
  preferred: ice    # 接受 ice 或 ipv6，由ice分配或仅IPv6
  allow_legacy: true    # 对端仅支持 IPv6 协议时允许兼容
ice:
  stun_urls:
    - stun:stun.cloudflare.com:3478
  direct_probe_timeout: 3s  # 优先直连时间
  gather_timeout: 6s    # 候选收集期限
  connectivity_timeout: 10s    # 连通性检查期限
  retry_max_delay: 15s    # 最大重试间隔
  interface_allowlist: []
# turn 整段可以省略；以下就是缺省值。Worker 签发短期凭据，CLI 不必填账号。
# 路径：直连 → UDP → TCP（80 → 3478）→ TLS（443 → 5349）。
turn:
  mode: worker
  ttl: 6h
  urls: []
  username: ""
  credential: ""

# 提供方id必须唯一，使用id匹配服务
provide:
  - id: ssh
    service: tcp://127.0.0.1:22    # 提供方需要明确写出协议（tcp/udp）
  - id: voice
    service: udp://1270.0.0.1:8888
```

另一端改用不同的 `device_name`，将 `provide` 换成：

```yaml
consume:
  - id: ssh
    expose: 127.0.0.1:2222
```

在各自设备启动 CLI，然后访问消费端的 `127.0.0.1:2222`：

```bash
# 示例为 Linux amd64；其他系统/架构使用 dist/cli/ 下对应文件。
./dist/cli/hole-linux-amd64 -config config.yaml # 默认使用当前目录下的 config.yaml
```

服务 ID 将 `provide` 与 `consume` 配对。UDP 服务使用 `udp://HOST:PORT`；
消费端仍填写本地监听地址。完整配置见 [config.example.yaml](config.example.yaml)。

常用 CLI 参数：

| 参数 | 用途 |
| --- | --- |
| `-config FILE` | 配置文件，默认 `config.yaml`；必须包含 `server_url` |
| `-transport auto` | 优先 ICE，允许与仅支持 IPv6 协议的对端互通 |
| `-transport ice` / `ipv6` | 仅使用指定传输 |
| `-debug` | 输出调试日志 |
| `-version` | 显示共享核心的提交标识与源码摘要 |

启动后的日志类似：

```text
21:11:20 INFO  hole 01d65b1 设备 desktop
21:11:21 INFO  已加入房间
21:11:41 INFO  laptop: 已连接 直连/IPv4 320ms
21:11:41 INFO  ssh: 映射已就绪，对端 laptop (tcp)
21:15:13 WARN  laptop: 连接中断，重连中
21:15:33 INFO  laptop: 已恢复 TCP 中继/IPv4 5.1s
```

配置中省略 `transport` 时保留 IPv6 模式；上面的示例显式开启 ICE。
STUN 默认使用 Cloudflare，TURN 默认向 Worker 按需申请短期凭据。

## 桌面 GUI 前置桥接

Windows / Linux 的独立 Go stdio 桥接已提供，复用现有核心；Rust + Slint 界面尚待实现。

```bash
./build.sh desktop-core --os linux --arch amd64
./build.sh desktop-core --os windows --arch amd64
```

产物在 `dist/desktop-core/`，Go 桥接不依赖 Qt、Slint 或 CGo。`cli` / `android` / `wear` / `all` 的原有目标集合不变，
需要时显式加上 `desktop-core`。控制协议见 [桌面桥接](desktop/README.md)，
界面分阶段路线和 Android 功能对齐验收矩阵见 [GUI 计划](docs/DESKTOP_GUI_PLAN.md)。

## 构建与签名

[build.sh](build.sh) 是统一构建入口

统一入口只产出 Release，Go 使用 `-trimpath -ldflags="-s -w"`，Android 开启 R8、资源收缩与 native strip。

```bash
./build.sh cli --os linux --arch arm64
./build.sh cli --os windows --arch amd64

# Android / Wear 先配置现有签名密钥
cp -n signing.env.example signing.env
# 编辑 signing.env 中的密钥库路径、密码与别名
./build.sh android
./build.sh wear
./build.sh all
```

产物位于 `dist/cli/`、`dist/android/` 和 `dist/wear/`；所有构建缓存位于项目 `.cache/`。
`dist/`、`.cache/`、`signing.env` 和密钥库文件均被 Git 忽略，测试源码正常纳入版本管理。

环境准备、参数、签名优先级和验证命令见 [构建指南](docs/BUILDING.md)，参数速查用 `./build.sh --help`。
Worker 单独部署，见 [Worker 部署](docs/WORKER.md)。

## 项目结构

| 路径 | 职责 |
| --- | --- |
| 根目录 Go 文件 | CLI 参数、配置读取、事件日志 |
| [core/](core/README.md) | 共享网络、ICE / QUIC、映射与可恢复会话 |
| [mobile/](mobile/README.md) | gomobile 窄接口与 Android 平台网络适配 |
| [desktop/](desktop/README.md)、`cmd/hole-desktop-core/` | 桌面 GUI 的独立 Go stdio 桥接与入口 |
| [android/](android/README.md) | 手机应用、共享桥接模块和 [Wear 模块](android/wear/README.md) |
| `worker.js` / `worker_ice.mjs` | 房间协调、ICE 信令与 TURN broker |
| `scripts/` | 构建环境、源码摘要及构建脚本回归测试 |
| [AGENTS.md](AGENTS.md) | 仓库维护与验证约定 |

Wear 界面目前沿用手机端，圆屏和旋钮的专属交互尚待适配。
会话恢复限于原进程及仍存活的应用 socket；进程重启或应用自行关闭后，连接重新建立。
