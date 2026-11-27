<p align="center">
  <img src="desktop/gui/ui/assets/app-icon.png" width="88" height="88" alt="Hole 图标">
</p>

<h1 align="center">Hole</h1>

<p align="center"><strong>把另一台设备的服务，接到你的本地端口。</strong></p>

<p align="center">TCP / UDP 端口映射 · QUIC 加密传输 · 多端共用核心</p>

<p align="center">
  <a href="#快速开始">快速开始</a> ·
  <a href="#客户端与构建">选择客户端</a> ·
  <a href="#连接路径">连接原理</a> ·
  <a href="#文档">文档索引</a>
</p>

两台设备加入同一个房间，即可映射 SSH、开发服务或其他 TCP / UDP 应用。

## 能做什么

| 能力 | 说明 |
| --- | --- |
| **双向端口映射** | 同时提供和访问多条 TCP / UDP 服务。 |
| **加密数据通道** | 每对设备共用一条 QUIC，承载映射与语音；默认使用 BBR 拥塞控制。 |
| **直连与中继** | 优先尝试 IPv4 / IPv6 UDP 直连，失败后复用已有直连节点进行 Peer Relay，再按配置使用 Hole / TURN 中继。 |
| **房间信令委托** | 同组设备可同时保持多个 Worker 连接；连接意外中断时，可经仍在线的组员转发通知。 |
| **TCP 会话恢复** | 同一进程内切网或传输重连时，可恢复仍存活的 TCP 会话。 |
| **可选语音房** | 同一房间最多 8 台启用语音的设备参与。 |
| **网络诊断** | 查看 NAT 类型、端口可预测性、端口映射与连接状态。 |

<details>
<summary>语音开关与 BBR 说明</summary>

- **语音**：设置 `voice.enabled: true`，同一房间最多 8 台语音设备参与。音频设备与操作方式见各客户端说明。
- **BBR**：共享核心的设备 QUIC 连接默认使用标准 BBR，覆盖 ICE、预测打洞、固定入口和设备中继；无需修改系统 TCP 参数，不保证任意网络都提速。实现和依赖来源见 [QUIC BBR 说明](core/internal/congestion/README.md)。

</details>

## 客户端与构建

| 客户端 | 特点 | 从源码构建 |
| --- | --- | --- |
| [CLI](#快速开始) | Go 命令行；支持固定入口和运行 Hole 中继 | `./build.sh cli` |
| [Android](android/README.md) | 原生应用，支持配置交换、语音与连接报告 | `./build.sh android` |
| [Wear OS](android/wear/README.md) | 独立运行，共用手机源码与 Go 核心 | `./build.sh wear` |
| [桌面](desktop/compose/README.md) | Kotlin Compose；Windows 安装包 / Linux AppImage，支持映射、语音与网络诊断 | `./build.sh desktop` |
| [旧 Rust 桌面](desktop/README.md) | Rust + Yororen UI，内嵌 stdio 核心宿主 | `./build.sh desktop-rust` |
| [PenMods](penmods/README.md) | 使用 `hole-core` 的插件 | [插件构建说明](penmods/README.md#构建) |
| [Minecraft Forge](minecraft/README.md) | 实验版 1.20.1 联机模组，世界发现与人数、按需测延迟、连接进度、世界内语音，支持独立服务器和 FCL ARM64 适配 | `./build.sh minecraft` |

构建产物放在 `dist/`。`./build.sh all` 构建 CLI、Android 手机和 Wear，不包含桌面、独立核心宿主或 Minecraft 模组。工具链、签名与验证方法见 [构建指南](docs/BUILDING.md)。

## 快速开始

以 **设备 A 提供 SSH，设备 B 通过本地 `2222` 端口访问** 为例。

可从 [Releases](https://github.com/Haikure/hole/releases) 下载对应平台的客户端，并准备一个可用的 [Hole Worker](docs/WORKER.md)。图形客户端的操作见上方说明。

### 1. 准备程序与配置

下载对应平台的 CLI，并将 [配置示例](config.example.yaml) 保存为 `config.yaml`。

<details>
<summary>从源码构建 CLI</summary>

需要 Go、Bash 和 Python 3，工具版本见 [构建指南](docs/BUILDING.md)。在仓库根目录执行：

```bash
./build.sh cli
cp -n config.example.yaml config.yaml
```

</details>

### 2. 加入同一房间

两端编辑各自的 `config.yaml`：前四项保持一致，`device_name` 各不相同。

```yaml
server_url: wss://HOST/ws
room: my-room
password: SIGNAL_PASSWORD  # Worker 信令密钥
token: ROOM_PASSWORD       # 房间口令
device_name: device-a      # 设备 B 改为 device-b
```

### 3. 配对服务

删除示例中本端不需要的映射项，两端使用相同的服务 `id`。
普通 `provide` 默认只注册用于配对的服务，不进入房间发现列表。Minecraft 模组共享世界时会显式声明发布，信令面板以 `MC` 标识展示。

**设备 A · 提供 SSH**（本机 SSH 服务监听 `22` 端口）：

```yaml
provide:
  - id: ssh
    service: tcp://127.0.0.1:22
```

**设备 B · 使用 SSH**：

```yaml
consume:
  - id: ssh
    expose: 127.0.0.1:2222
```

### 4. 启动并访问

两端分别启动 CLI。以下使用 Linux amd64 的源码构建路径；下载发行版时替换为实际程序路径：

```bash
./dist/cli/hole-linux-amd64 -config config.yaml
```

在设备 B 上连接，把 `USER` 换成设备 A 的 SSH 用户名：

```bash
ssh -p 2222 USER@127.0.0.1
```

`service` 和 `expose` 支持 TCP 与 UDP。完整字段见 [配置示例](config.example.yaml)。普通连接统一使用 ICE，无需选择自动或 ICE；旧 CLI 配置中的 `transport.preferred` 字段需删除，`-transport` 参数已移除。

<details>
<summary>常用 CLI 参数</summary>

| 参数 | 作用 |
| --- | --- |
| `-config FILE` | 指定配置文件；默认读取当前目录的 `config.yaml`。 |
| `-debug` | 输出连接协商和底层网络错误，日志可能包含服务器及候选地址。 |
| `-log-format text\|json` | 默认 `text`；`json` 每行输出一条结构化日志，可与 `-debug` 组合。 |
| `-version` | 显示共享核心版本。 |

</details>

### CLI 日志

日志写入 stdout，使用本地时间，按设备、映射和目标服务的状态变化输出。
连接成功时显示路径、实际远端地址、耗时和已测得的 RTT；“已监听”表示本地端口已绑定，
“映射已就绪”表示通道已建立，首次访问成功后才显示“目标服务已连通”。

信令断开时先显示原因；仍有可达的房间信令入口时显示“信令委托”，最后一个入口断开时显示错误。
设备离线与路径恢复分别报告，相同原因的连续重试会合并。

使用 `-debug` 查看 NAT、候选对和底层网络诊断，`-log-format json` 可输出逐行 JSON，便于日志采集。
普通状态行保留原文，DEBUG 诊断处理已知凭据；服务器概览省略用户信息与查询参数。

### 配置热重载

CLI 自动监听配置文件，保存后校验并应用；文件暂时不可读或配置无效时继续使用原配置。
修改注释或保存相同配置不会触发重连。

映射变更只更新受影响的通道和应用连接，未受影响的 TCP / UDP 会话继续运行。
STUN、探测预算等参数用于后续尝试；中继策略、网卡限制和固定入口变化会按需更新路径。
修改服务器、房间、凭据或设备名会重新认证并结束原会话。
旧 Worker 不支持在线更新时会重新加入房间，语音开关能否复用连接取决于双方能力。

## 连接路径

```mermaid
flowchart LR
    app["设备 B 的应用"] <--> local["Hole B<br/>本地端口 :2222"]
    local <-->|"加密 QUIC · 直连 / 中继"| remote["Hole A"]
    remote <--> service["设备 A 的 SSH<br/>:22"]
    worker["Worker · 房间信令"] -.-> local
    worker -.-> remote
```

Hole 以端口为单位转发，不创建 VPN / TUN。Worker 负责房间发现、建连协调和按需签发 TURN 凭据，**不转发业务数据**。
本机与 Worker 的连接中断时，可通过同组仍在线的设备接收信令通知；已有健康数据连接可继续使用。
Worker 自带只读房间面板，默认访问 `https://HOST/panel`，依次输入信令密码、房间号和房间口令后查看成员、信令和连接状态。有效会话可自动恢复房间，个人设备可选择保持登录 7 天，浏览器不保存两种密码，退出会撤销会话；路径可配置，见 [Worker 部署](docs/WORKER.md#房间面板)。行为与状态字段见 [房间控制与按需信令](docs/PROTOCOL.md#房间控制与按需信令)。

普通 ICE 连接按顺序尝试，成功后即使用该路径：

| 顺序 | 路径 | 何时尝试 |
| --- | --- | --- |
| ① | **普通 ICE 直连** | 首先尝试 IPv4 / IPv6 UDP，不等待 NAT 分类结果。 |
| ② | **按 NAT 类型打洞** | ICE 失败后，双方客户端与 Worker 支持新能力且证据适用时尝试。 |
| ③ | **Peer Relay 节点中继** | 直连方式耗尽后，尝试同房间内已与双方直连的节点。 |
| ④ | **Hole / TURN 中继** | 无可用节点或节点中继失败后，按 `relay.order` 尝试可用中继。 |

配置了 Hole 中继时，默认顺序为 **Hole → TURN/UDP → TURN/TCP → TURN/TLS**。

“重选路径”从普通 ICE 直连重新开始，失败后按能力与 NAT 证据进入预测打洞，再尝试中继；
仅中继模式仍跳过直连，固定入口仍只重试固定路径。探测期间保留旧连接，新路径通过认证后才替换。
已有健康直连时，探测失败继续使用原连接，不因失败降级到中继；自动恢复中的旧代次失败也不会推进新代次。
完整行为需要更新 Worker 和两端客户端。

Peer Relay 自动协商，无需固定公网入口或额外配置。它只复用已存在的直连，不主动为中继建立新连接；
节点只转发端到端加密数据。需要更新 Worker 和三端核心；旧端及仅中继模式继续使用原有路径。
关闭本机 TURN 申请不会关闭 Peer Relay。每个节点最多承载 8 条中继设备对，详见 [节点中继协议](docs/PROTOCOL.md#peer-relay-节点中继)。

<details>
<summary>ICE 连接与仅中继</summary>

普通连接统一使用共享 ICE / QUIC 传输，无需配置连接策略。
旧 `ipv6` 模式和 `allow_legacy` 已移除；IPv6 地址仍由 ICE 正常探测和直连。
共享核心 1.1.0 使用 ALPN `hole`，参与连接的核心需同步升级。

- `ice.relay_only: true` 跳过直连，仅使用可用的 Hole 或 TURN。
- ICE 失败后的分类打洞需要两端核心与 Worker 满足相应版本门槛，未协商时仍先尝试普通 ICE，失败后进入中继。
- IPv4 不适用时，保留首次普通 ICE（包括 IPv6），失败后跳过分类打洞。

详见 [传输选择](docs/PROTOCOL.md#传输选择) 与 [ICE 协商](docs/PROTOCOL.md#ice-直连nat-状态与端口映射)。

</details>

## NAT 探测与 UDP 端口映射

允许直连时，Hole 可自动为隧道 UDP 端口申请临时路由器映射：先尝试 UPnP IGD，再尝试 NAT-PMP。映射不可用时，继续其他直连或中继路径。

路由器映射续租失败或地址变化时更新映射状态，当前直连继续检查存活，实际失联后再重连。进入预测打洞阶段时，支持设备调度的双方先重新采样并交换 NAT 模型；短时间重连也会刷新，避免旧的不可预测分类持续跳过预测尝试。

| 配置 | 默认值 | 作用 |
| --- | --- | --- |
| `ice.port_mapping` | `auto` | 自动申请隧道端口映射；`off` 禁用。不会映射 `provide` / `consume` 的业务端口。 |
| `ice.nat_stun_urls` | `null` | NAT 分类服务：省略、`null` 或 `[]` 均复用 `ice.stun_urls` 并补充内置默认服务；非空列表使用自定义服务，最多 16 条。 |

普通 STUN 列表也为空时，NAT 分类仍使用内置默认服务；清空补充服务不会停用探测。

Android / Wear、桌面和 PenMods 可在设置中调整这些选项，导入导出会保留配置。图形客户端提供 NAT 检测入口；命令行可独立运行：

```bash
source scripts/build-env.sh
go run ./tools/natprobe
```

NAT 类型与端口可预测性用于策略选择和诊断，不保证直连成功。结果含义见 [NAT 工具说明](tools/README.md)，
算法、预算与协商细节见 [协议说明](docs/PROTOCOL.md#ice-直连nat-状态与端口映射)。

## 固定入口与设备中继

<details>
<summary>固定入口：已有公网 UDP 地址或路由器端口转发</summary>

有公网 UDP 地址或已配置路由器端口转发时，可给一端设置固定入口：

```yaml
transport:
  fixed_endpoint:
    listen: 0.0.0.0:40000
    advertise: relay.example.com:50000
```

本例将公网 UDP `50000` 转发到本机 UDP `40000`。对端从 Worker 获取公布地址并拨号，无需设置入口。

- **只使用固定路径**：失败后不回退 ICE / TURN，也不能与仅中继模式组合。
- **先升级 Worker，再升级客户端**：两端核心与 Worker 均需支持固定入口协议。
- **入口需要可达**：确认公布地址、端口转发和防火墙规则。
- **通过 CLI 配置监听**：图形客户端可显示固定入口状态。

详见 [固定入口直连](docs/PROTOCOL.md#固定入口直连)。

</details>

<details>
<summary>Hole 设备中继：让同房间 CLI 设备转发数据</summary>

根目录 Go CLI 可把固定入口同时作为同房间设备中继。服务端和客户端需要在同一个房间：

```yaml
# 中继设备
device_name: relay-node
transport:
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

</details>

## 文档

| 我想了解… | 文档 |
| --- | --- |
| YAML 字段与完整配置 | [配置示例](config.example.yaml) |
| 部署信令服务、配置 TURN | [Worker 部署指南](docs/WORKER.md) |
| 工具链、签名、构建产物与验证 | [构建指南](docs/BUILDING.md) |
| 连接协商、固定入口、中继与会话恢复边界 | [协议说明](docs/PROTOCOL.md) |
| NAT 类型、端口趋势与诊断结果 | [NAT 探测工具](tools/README.md) |

Worker 需单独部署，不会随客户端构建自动发布。

---

许可证：[GPL-3.0-only](LICENSE)
