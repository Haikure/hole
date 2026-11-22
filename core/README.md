# 共享 Go 核心

`hole/core` 实现信令、ICE / 固定入口 QUIC、TCP / UDP 映射及可恢复应用会话，供 CLI、Android、桌面与插件共用。
本包不读取配置文件、不解析命令行参数，不安装进程信号处理器，也不退出宿主进程。

[宿主接入](#宿主接入) · [网络与诊断](#网络与诊断) · [平台与恢复](#平台与恢复) · [构建与测试](#构建与测试) · [项目首页](../README.md)

| 宿主负责 | 核心负责 |
| --- | --- |
| 配置存储、界面和进程生命周期 | 严格解析、身份、重配与资源限额 |
| 平台 Network、DNS 与 socket 绑定 | 信令、直连、中继与传输重连 |
| 音频设备采集和播放 | Opus、混音、媒体传输与统计 |

## 宿主接入

```go
func run(ctx context.Context, data []byte) error {
    cfg, err := core.ParseConfig(data)
    if err != nil {
        return err
    }
    serverURL := cfg.ServerURL
    cfg.ServerURL = "" // 运行请求外层是服务器地址的唯一来源

    engine := core.NewEngine(core.Options{})
    defer engine.Close()
    if err := engine.Start(core.Request{
        ServerURL: serverURL,
        Config:    cfg,
    }); err != nil {
        return err
    }
    return engine.Wait(ctx)
}
```

示例函数使用 `context` 和 `hole/core`。界面宿主通过 `Events()` 接收事件，并用 `Snapshot()` 读取当前状态。

| API | 行为 |
| --- | --- |
| `ParseConfig` / `Config.Validate` / `Request.Validate` | 严格解析与同步校验，无网络副作用 |
| `NewEngine` | 创建独立实例，不联网 |
| `Start` | 校验并复制请求，异步开始连接；请求已接受与网络已就绪分开 |
| `ApplyConfig` | 停止态暂存；运行态按配置差异本地应用、在线更新声明或重建传输 |
| `NetworkChanged` | 更换外部网络路径，保留符合恢复条件的应用会话 |
| `RenominateTransports` | 对已有 ICE active 路径发起新代次；无可用路径时是幂等 no-op |
| `StartNATProbe` / `CancelNATProbe` | 开始或取消无需房间的手动 NAT 检测，结果在独立诊断快照中 |
| `NATPunchProcess` | 按设备传输 ID 和已读取序号增量读取打洞步骤、socket、端口计划与收发记录 |
| `Stop` | 取消网络活动并等待资源清理，实例可再次启动 |
| `Close` | 最终关闭实例和事件通道 |

配置应用按完整配置内容比较；相同配置幂等。ICE coordinator 串行应用本地变更，并在现有
WebSocket 上提交带修订号的 `member_update`；确认前的连续编辑合并为最新声明。
映射通道使用原 QUIC 控制流增删；修改目标、协议或监听地址只终止对应会话。
会话期限在本地更新，探测服务和预算用于后续尝试。双方协商在线配置能力后，语音开关
通过带修订号的控制消息应用，不关闭共享 QUIC。身份变化仍由 supervisor 重建运行范围；
旧 Worker 未支持在线配置时回退到重新加入，旧对端的语音变化重建对应传输。
运行态 `config:applying` 表示已接受期望配置，`config:applied` 表示本地配置与服务声明已同步，
具体映射和语音是否就绪仍以各自快照为准。在线更新期间信令状态保持 `joined` 或 `delegated`。
`delegated` 下仅影响本地策略的更新完成后同样发布一次 `config:applied`；需要变更服务声明的配置先恢复 WSS 并等待 Worker 确认。
在线应用使引擎从 `reconfiguring` 恢复为 `running` 时，同步发布 `engine:running`，无需等待信令重新加入；
断线恢复期间的配置完成不表示连接已恢复。
快照复制内部对象且不包含配置凭据；事件通道有界，宿主通过快照核对最新状态。

本地访问端口的生命周期使用 `kind=listener` 事件，`state=ready/error/closed` 分别表示实际绑定成功、创建失败和监听关闭，`target` 是本地地址，`protocol` 是 TCP / UDP，失败附带分类后的原始原因。`MappingSnapshot.listener` 独立保存同样的状态、协议、实际 `address` 和错误，端口为 0 时记录系统分配的真实端口，宿主可在事件丢失后核对。设备路径中断时会保留本地监听，因此不会发布监听关闭；映射就绪和监听成功均不代表目标服务已经接受应用连接。

<details>
<summary>底层日志事件与脱敏</summary>

ICE / TURN 底层诊断复用 `kind=log` 事件，附带 `peer`、`phase`、`transport_generation` 和
表示日志来源的 `stage`。Pion warning/error 使用 `state=warning/error` 与 `message`；
尝试最终失败使用 `state=failed` 与保留原始原因的 `error`。普通快照及传输事件继续使用面向用户的错误提示，
CLI 仅在 `-debug` 下展示这些诊断。每次尝试最多输出 64 条底层日志及一条省略提示，最终失败原因不占该额度。
产生事件前按本次尝试的配置、动态 TURN 凭据和 ICE 凭据脱敏，再进入 Engine 的统一脱敏流程；
不受 `PION_LOG_*` 环境变量影响，不启用 info/debug/trace 报文日志。

CLI 默认只显示设备、映射、监听和目标服务的状态变化及分类错误；NAT 结果、候选对、中继计数和进程级网络库原文放在 `-debug`，普通结构化状态事件不再同时重复打印一条无额外信息的 DEBUG。重复的相同诊断在首次和每五次输出，保留阶段和代次上下文。CLI 对普通 INFO / WARN / ERROR 行不再按凭据做全局字符串替换，直接展示核心提供的内容；DEBUG 诊断继续处理凭据。`-log-format json` 仅改变输出格式，复用同一套筛选、分级和诊断凭据处理规则，仍写入 stdout；其字段约定见根 README。

</details>

## 网络与诊断

### 手动 NAT 检测

`Engine.StartNATProbe` / `CancelNATProbe` 提供无需房间的手动 IPv4 NAT 检测，复用 `natprobe`。
每实例最多一项检测；与自动 NAT 探测、预测打洞共用分配锁，排队及执行总限时 60 秒，实际采集最多 20 秒。
结果独立保存在 `Snapshot.NATDiagnostic`，不覆盖路径选择证据；Stop / Close 取消并等待清理，
NetworkChanged 使记录失效，完成的分类在 5 分钟后标为过期。原始字段及中文说明供各图形前端共同展示。
共享探测使用平台的 IPv4 解析入口，避免双栈 DNS 返回顺序丢失 STUN 目标。手动结果另含逐服务的解析地址、
基础样本、去重映射及失败原因，区分目标数、样本数和映射数；预测字段仅在对应模型成立时提供。

### 打洞过程记录

`Engine.NATPunchProcess(transportID, afterSequence)` 独立读取指定设备对的过程记录。记录包括普通 ICE、
预测打洞及中继阶段，保留最近 6 次连接尝试、4096 条近期事件与 512 条步骤事件，序号单调递增。
每次返回当前尝试、轮次、socket 状态及新事件；后台 NAT 采样过程附在对应尝试中。
模型、双方端口计划、扫描端点与轮次汇总在事件中保留当时的快照，可按事件回放。

STUN 记录包含事务编号、CHANGE-REQUEST、请求服务、实际响应来源、OTHER-ADDRESS 和已匹配事务的映射；
UDP 打洞记录包含每次 PROBE / ACK 的端点、长度、序号、最小重传间隔与认证结果。地址保留其观测来源。
预测探测在本轮时限内持续重传，每个 socket 的探测与 ACK 合计至少间隔 200ms，共用每端最多 200 包 / 秒的预算。
拨号端回复 ACK 后保留主动探测机会；扫描时，收到有效对端探测后优先重试该候选的挑战，避免已可达端口被扫描或 ACK 排队拖延。
拨号端验证有效 ACK、开始 QUIC 拨号后停止所有主动 PROBE，握手期间继续回复有效探测；接收端持续探测至 QUIC 建立或本轮结束。
`probe_limit` 为 0 或省略时表示不设次数上限，停止时机由打洞阶段与本轮时限控制。
记录窗口的移出数量及采集时未记录的数量单独返回；普通运行快照继续使用原有字段。

### DNS 与直连顺序

`netresolve` 提供默认网络与独立 NAT 工具共用的系统 DNS 和纯 Go 备用解析（`223.5.5.5:53`）。默认网络查询先尝试系统 DNS，系统及备用各最多 2 秒，并受调用者总时限限制；Android 绑定网络继续使用平台 DNS。信令连接沿用先尝试系统拨号、失败后备用解析的顺序。

允许直连的设备对始终先尝试普通 ICE，不等待 NAT 分类，也不因对称型或证据不足跳过。双方与 Worker 协商 `direct_order: ice-first-v1` 后，ICE 失败才切换新代次进入 NAT 分类及后续打洞；成功直接使用连接，打洞耗尽再进入中继。后台 NAT 探测及展示独立运行。旧 Worker 或混合能力设备对保留普通 ICE，失败后进入中继。

<details>
<summary>NAT 实现文件与快照字段</summary>

| 文件 / 包 | 职责 |
| --- | --- |
| `natprobe` | 与独立工具共用 UDP STUN 探测、分类、端口保持及 No-PAT 特征 |
| `internal/nat` | 平台探测适配、IGD / NAT-PMP |
| `nat_transport.go` | 最多三次完整分类探测与状态上报 |
| `nat_punch*.go` | NAT 分组、有限端口预测与认证 UDP 探测 |
| `ice_check_pacing.go` | 锥形 ICE 的实际请求间隔 |
| `ice_port_mapping.go` | ICE 候选端口的映射租约 |

所有成功路径复用同一个设备对 mux。`Network.NAT` 为本机网络结果，`PeerTransports.local_nat/peer_nat` 分别为本机/对端结果。
分类与映射状态独立；仅中继及固定入口不启动 NAT 探测与自动映射。新核心源码自动进入 `scripts/build_meta.py` 的递归摘要。

</details>

### 房间控制与按需信令

共享核心 / Worker 1.1.0 起，已有设备 QUIC 同时承载独立房间控制流。同组设备可以同时保持多个
Worker 连接；连接意外中断的成员可通过仍在线的组员接收通知，快照显示 `signal_state=delegated`。
意外断开先发布携带原始脱敏原因的 `signal:reconnecting` 事件，再决定委托或立即重连；配置更新触发的主动重连不发布这条失败事件。
委托期间，对端离线不会因旧映射的路径重试恢复本机信令；NAT、建连结果和中继回收上报排队等待下次直接连接。
对端仍可达或 Worker 确认双方仍在线但已分组时，恢复必要的路径协商；手动重选、配置更新及入口丢失仍按需恢复信令。
最后一个入口恢复失败一次即通知组内其他成员共同恢复。不同组不额外建立控制连接，
业务或语音消失后可保留已有控制边；普通路径不依赖中央授权续租。

房间帧保留原始来源签名、去重 ID 和跳数；定向 Worker 授权通知端到端加密。
通知独立处理，来源节点及目录按修订号过滤旧状态。核心快照增加 `worker_version` 和 `room`，
Worker 保存分组、信令入口、连接图及版本镜像；WebUI 尚未提供。
共享 ALPN 固定为 `hole`，功能依据软件版本判断；旧仅 IPv6 模式与配置入口已移除，
IPv6 仍通过 ICE 支持。完整边界见 [房间协议](../docs/PROTOCOL.md#房间控制与按需信令)。

### 固定入口与设备中继

Peer Relay 在直连及打洞预算耗尽后、Hole / TURN 之前自动尝试。Worker 根据两端已确认的直连选择
同房间节点，并授权一条绑定运行实例与连接代次的单跳路径；三端共享核心再次核对实际直连。
数据通过已有 QUIC DATAGRAM 转发，内部端到端 QUIC 继续提供身份验证、TCP / UDP、语音和会话恢复。
无节点、握手超时或直连断开后进入原有中继轮次。
身份或协议校验失败会停止该设备对并上报错误；中继本地管理容量、撤销、断链和闲置回收；同一已撤销代次不能被重放复活。
`PeerTransports.path_type=peer_relay`，`relay_device` 为实际节点；不增加移动端专用传输逻辑。
边界见 [Peer Relay 协议](../docs/PROTOCOL.md#peer-relay-节点中继)。

`relay.device` / `relay.order` 提供 Hole 设备中继配置，`relay.serve` 仅由根 CLI 显式能力
`Options.AllowRelayServe` 开放。固定 UDP socket 由单一分流器读取；Linux 批量后端和其他
平台数据报后端共用授权、限速、公平队列和统计。`Snapshot.Relay` 为服务端统计，
`PeerTransports` 包含实际中继设备、公布入口与接入地址。配置及三方生命周期见
[Hole 协议](../docs/PROTOCOL.md#hole-设备中继)。mobile、桌面与插件只消费该共享实现。

`transport.fixed_endpoint` 提供固定 UDP 监听与公布地址。
对端无需填写连接地址；新版核心自动处理 Worker 下发的 `fixed-quic-mux-v1`，复用平台网络
绑定、DNS、mux、会话恢复与限额。固定设备对失败只重试固定入口，不启动 ICE / TURN。
Android / Wear 重打包新版 AAR 后具备自动拨号能力，固定入口配置界面后续接入。
配置和部署顺序见 [固定入口直连](../docs/PROTOCOL.md#固定入口直连)。

### 语音

<details>
<summary>PCM 接入、连接生命周期与反馈</summary>

配置中的 `voice.enabled` 默认关闭。开启后，核心只接受平台提供的 48 kHz、单声道、PCM16
20 ms 帧（每帧 960 个采样），通过同一 room 的 voice-only ICE / QUIC 传输发送 Opus 帧。
`PCMSource` / `PCMSink` 是内部接口；gomobile 使用 `PushVoicePCM` / `PullVoicePCM` 批量方法
传递 PCM，不经过 JSON 事件桥，每次最多处理 4 帧。Android corebridge 通过 `AudioRecord` /
`AudioTrack` 接入设备。语音成员、传输和媒体统计从 `Snapshot.voice` 读取，事件只作刷新提示。
语音会话绑定具体 ICE 连接实例，旧线路退出不会解绑已替换的新线路（包括同代次重连）。
语音与转发共用传输时，信令重连只替换数据路径，未变更的 TCP / UDP 应用会话继续恢复。
关闭语音会取消媒体及反馈发送；发送队列拥塞时关闭旧 QUIC 路径来解除阻塞，不等待网络恢复。
采样时间戳与发送包序号独立，静音间隔保留在播放时钟中，不计入网络丢包；码率按最近反馈区间调整。
缺包保留最多 8 个包序号、160 ms 的乱序等待窗口，窗口内补齐不计丢包；反馈只覆盖已经确认的序号区间。

</details>

## 平台与恢复

`Platform` 区分外部候选、信令、上游连接和本地监听。
默认实现使用桌面 Go 网络 API；Android 注入系统 Network、DNS 和 socket 绑定，
并通过 `Options.RetryNetwork=true` 等待初始网络恢复。

网络路径重建保留未改变的监听器、应用 socket、重放窗口和进程证书。
删除映射、修改目标或运行身份、用户停止、进程退出会结束对应作用域的会话。
协议格式、重放和资源限额见 [协议说明](../docs/PROTOCOL.md)。

TCP 重放数据按需分块分配，单方向上限 16 MiB，同一运行的所有映射共用 128 MiB 块容量预算。
性能基准可执行 `go test ./core -run '^$' -bench 'Benchmark(ICETCP|TCPReplayRTT100ms|UDPReassembly)' -benchmem`。
`BenchmarkICETCP` 使用真实本地 TCP → ICE / QUIC → TCP，两端共享本机 CPU，预热后测持续转发。
TCP 基准使用有固定延迟的内存链路，仅衡量应用重放协议，不代表实际 TURN、QUIC 或设备网络带宽。

## 构建与测试

```bash
# 仓库根目录
./build.sh cli
source scripts/build-env.sh
go test -race -count=1 ./...
go vet ./...
```

根 `go.mod` 与 `android/corebridge/gobuild/go.mod` 分别锁定 CLI 和绑定依赖图；
两者共用本包源码及 [anet 兼容模块](compat/anet/README.md)。
`CoreVersion` 从仓库根目录 `versions.json` 注入，CLI、独立核心宿主和 Android AAR 共用该值。构建脚本另注入由提交标识及核心源码/依赖摘要生成的 `CoreBuildID`，供 APK 检查和构建追踪使用。
