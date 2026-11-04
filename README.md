# Hole

Hole 是一款端口映射工具：让两台设备加入同一个房间，把一端的 TCP / UDP 服务映射到另一端的本地端口。可用于远程访问 SSH、开发服务或其他 TCP / UDP 应用。

支持双向多条映射、最多 8 人的可选语音房，以及同一进程内网络切换或传输重连时恢复仍存活的 TCP 会话。

Hole 不创建 VPN / TUN。两端通过 QUIC 建立加密数据通道；Worker 负责房间信令和按需签发 TURN 凭据，不转发业务数据。直连失败时，数据通道可按配置经过同房间的 Hole 中继或 TURN。

```text
业务数据  设备 A 的服务 ── Hole A ══ 加密 QUIC ══ Hole B ── 设备 B 的本地端口
控制信令             Hole A <──────── Worker ────────> Hole B
```

## 导航

[快速开始](#快速开始) · [连接路径](#连接路径) · [NAT 探测与 UDP 直连](#nat-探测与-udp-直连) · [固定入口与设备中继](#固定入口与设备中继) · [客户端与构建](#客户端与构建) · [文档](#文档)

## 快速开始

需要 Go 1.27.3、Bash、Python 3，以及一个可用的 Hole Worker。Worker 部署和 TURN 服务配置见 [Worker 部署指南](docs/WORKER.md)。

在仓库根目录构建 CLI，并从完整示例复制配置：

```bash
./build.sh cli
cp -n config.example.yaml config.yaml
```

编辑 `config.yaml`。两台设备的 `server_url`、`room`、`password` 和 `token` 要一致；`password` 对应 Worker 的信令密钥，`token` 是房间口令。`device_name` 在房间内必须各不相同。选择 ICE 传输：

```yaml
server_url: wss://HOST/ws
room: my-room
password: SIGNAL_PASSWORD
token: ROOM_PASSWORD
device_name: desktop       # 另一台设备使用不同名称
transport:
  preferred: ice
```

删除示例中本端不需要的映射项。在提供服务的一端配置 `provide`，另一端用相同的 `id` 配置 `consume`：

```yaml
# 提供 SSH 服务的一端
provide:
  - id: ssh
    service: tcp://127.0.0.1:22
```

```yaml
# 访问 SSH 服务的一端
consume:
  - id: ssh
    expose: 127.0.0.1:2222
```

在两台设备上分别启动 CLI，然后连接消费端的 `127.0.0.1:2222`：

```bash
./dist/cli/hole-linux-amd64 -config config.yaml
```

上面的二进制名称以 Linux amd64 为例；其他系统和架构使用 `dist/cli/` 中对应的文件。`service` 和 `expose` 支持 TCP 与 UDP。更多配置字段见 [配置示例](config.example.yaml)。

常用 CLI 参数：

| 参数 | 作用 |
| --- | --- |
| `-config FILE` | 指定配置文件；默认读取当前目录的 `config.yaml`。 |
| `-transport VALUE` | 可选 `auto`、`ice` 或 `ipv6`；`auto` 使用 ICE 并兼容旧 IPv6 对端。 |
| `-debug` | 输出连接协商和底层网络错误，日志可能包含服务器及候选地址。 |
| `-version` | 显示共享核心版本。 |

## 连接路径

普通 ICE 连接按以下思路工作：

1. 先尝试 IPv4 / IPv6 UDP 直连。
2. 普通 ICE 直连失败后，符合条件时追加 NAT 感知的 UDP 直连尝试，最多 15 秒。
3. 仍未连通时，按中继顺序尝试可用的 Hole 或 TURN。配置了 Hole 中继时默认先尝试 Hole，再试 TURN/UDP、TURN/TCP、TURN/TLS；可用 `relay.order` 调整。

`transport.preferred` 支持 `ice`、`auto` 和 `ipv6`。`ice` 使用 ICE；`auto` 使用 ICE 并兼容旧 IPv6 对端；`ipv6` 只使用旧 IPv6 路径。省略该设置仍使用 IPv6 模式。`allow_legacy` 用于协商旧对端，不是 ICE 失败后的自动回退开关。

开启 `ice.relay_only: true` 后跳过直连，仅使用可用的 Hole 或 TURN。Worker 和客户端都要支持该策略，才能在连接失败和切网重连时保持约束。

## NAT 探测与 UDP 直连

NAT 辅助尝试在普通 ICE 直连失败后运行。Worker 与双方客户端都支持该能力、双方允许直连且使用 ICE 时才会启用；固定入口、仅中继关系和旧版客户端沿用各自原有路径。15 秒预算包含排队、探测、端口映射和 QUIC 握手。尝试失败后继续原有 Hole / TURN 中继顺序。自管 Worker 升级时先更新 Worker，再更新两端客户端。

配置项位于 `ice` 下：

| 配置 | 默认值 | 作用 |
| --- | --- | --- |
| `udp_punching` | `auto` | `auto` 启用 NAT 感知的 UDP 直连补充尝试；`off` 禁用。 |
| `port_mapping` | `auto` | 在平台和路由器支持时，尝试为隧道 UDP 端口创建 UPnP IGD 或 NAT-PMP 映射；`off` 禁用。它不会映射 `provide` / `consume` 的业务端口。 |
| `nat_stun_urls` | `null` | NAT 分类用的 UDP STUN 地址。`null` 复用 `ice.stun_urls` 并补充默认服务；`[]` 不使用分类服务；也可自定义，最多 8 条。 |

`ice.stun_urls` 用于常规 ICE 候选收集；`ice.nat_stun_urls` 用于 NAT 分类。自动端口映射会在连接使用期间续租，并在路径结束时尝试删除。路由器或平台不支持映射时，Hole 会继续运行其他直连或中继路径。

NAT 类型、端口可预测性和探测时间用于诊断，不保证直连一定成功；支持的图形客户端会在网络或设备详情中显示这些信息。协商时 NAT 快照和最多 8 个 IPv4 映射地址会经 Worker 转交对端；Worker 不因此承载业务流量。底层协商细节见 [协议说明：UDP 直连子阶段](docs/PROTOCOL.md#udp-直连子阶段)。

Android / Wear、桌面和 PenMods 的设置页可控制 UDP 直连与端口映射，并填写分类 STUN 地址；配置导入导出会保留这些选项。CLI 使用上表中的 YAML 字段。

## 固定入口与设备中继

### 固定入口

有公网 UDP 地址或已配置路由器端口转发时，可给一端设置固定入口：

```yaml
transport:
  preferred: ice
  fixed_endpoint:
    listen: 0.0.0.0:40000
    advertise: relay.example.com:50000
```

本例表示公网 UDP 50000 转发到本机 UDP 40000。对端无需设置入口，会从 Worker 获取公布地址并拨号。任一端公布固定入口后，该设备对只使用固定路径；连接失败时不会回退到 ICE 或 TURN，也不能与仅中继模式组合。Worker 和两端核心都需支持固定入口协议，升级顺序为 Worker 后客户端。请先确认公布地址、端口转发和防火墙规则可从对端访问。当前图形客户端可显示固定入口连接状态，但固定监听配置需通过 CLI 设置。

### 同房间 Hole 中继

根目录 Go CLI 可把固定入口同时作为同房间设备中继。服务端和客户端需要在同一个房间：

```yaml
# 中继设备
transport:
  preferred: ice
  fixed_endpoint:
    listen: 0.0.0.0:40000
    advertise: relay.example.com:40000
relay:
  serve:
    enabled: true
```

```yaml
# 使用中继的客户端
relay:
  device: relay-node
  order: [hole, udp, tcp, tls]
```

Hole 中继与该设备的固定业务共用 UDP 监听端口，不新开监听端口。中继只转发端到端 QUIC 数据，不连接目标业务服务。服务容量、限速和升级兼容要求见 [协议说明](docs/PROTOCOL.md#hole-设备中继)。

## 客户端与构建

| 客户端 | 说明 |
| --- | --- |
| CLI | Go 命令行客户端；也支持固定入口和运行 Hole 中继。 |
| Android / Wear OS | 原生应用，共用 Go 核心；支持配置导入导出、连接报告和 NAT 诊断。 |
| 桌面客户端 | Linux / Windows Rust + Slint GUI，内嵌 Go 核心。 |
| PenMods | 使用 `hole-core` 的插件。 |

启用语音的设备在配置中设置 `voice.enabled: true`；同一房间最多 8 台语音设备参与。Android、桌面和 PenMods 的具体交互见各自客户端说明。

构建入口统一为 `build.sh`：

```bash
./build.sh cli
./build.sh desktop
./build.sh android
./build.sh wear
./build.sh all       # CLI + Android 手机 + Wear，不包含 desktop 或 core
```

构建环境、签名、产物和验证方法见 [构建指南](docs/BUILDING.md)。Worker 单独部署，不会随客户端构建自动发布。

## 文档

- [配置示例](config.example.yaml)：CLI 常用 YAML 配置样例。
- [协议说明](docs/PROTOCOL.md)：连接协商、NAT、固定入口、中继、语音和会话恢复边界。
- [Worker 部署指南](docs/WORKER.md)：信令服务、TURN 凭据和协议升级顺序。
- [Android 客户端](android/README.md) · [Wear OS](android/wear/README.md) · [桌面客户端](desktop/README.md) · [PenMods 插件](penmods/README.md)。
- [第三方许可清单](THIRD_PARTY_NOTICES.md)：移植组件来源、许可与修改说明。

项目原创部分采用 GPL-2.0-or-later；第三方组件继续按其原许可分发。包含移植组件的组合交付许可与源码材料见 [许可清单](THIRD_PARTY_NOTICES.md)。
