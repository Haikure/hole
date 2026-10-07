# 传输、会话与兼容性

CLI、手机和 Wear 使用同一 Go 实现。配置与交互入口见 [README](../README.md)，
本文件描述当前协议行为，不包含历史构建流水。

## 信令与认证

客户端通过 `wss://HOST/ws` 连接协调 Worker，使用信令密码以及房间号 / 房间密码。
当前认证模式为 `shared-secret`；设备名标识房间成员，随机 `runtime_id` 标识本次运行实例。
数据面使用 QUIC，连接时核对通过信令交换的证书指纹。

同一进程的证书和应用会话独立于短期网络路径。配置更换服务器、房间、凭据或设备名时，
结束旧运行作用域；同名对端更换证书时，清理旧证书拥有的上游会话。

ICE 配置要求 WSS；本地 WS 测试需显式启用 `transport.allow_insecure_signal` 或 CLI
`-allow-insecure-signal`。凭据不进入快照，日志脱敏凭据与服务器 URL 查询参数。

## 传输选择

| 配置 | 网络 profile | QUIC ALPN |
| --- | --- | --- |
| `transport.preferred: ice` | `ice-quic-mux-v1` | `hole-ice-mux-v1` |
| `transport.preferred: ipv6` | `legacy-ipv6-quic-v2` | `hole-v2` |

配置只接受 `ice` / `ipv6` 短名称。省略 `transport` 保留 IPv6 模式；CLI `-transport auto`
显式选择 ICE 并允许旧 IPv6 profile。原始 hole-v1 不参与当前互通。

`allow_legacy: true` 用于与只支持旧 IPv6 profile 的对端协商，不是 ICE 连接失败后的降级开关。
双方支持 ICE 时继续使用 ICE，并按中继策略尝试新路径。

默认 STUN 为 `stun:stun.cloudflare.com:3478`；显式空 `ice.stun_urls` 仅收集本地候选。
`turn` 默认使用 Worker 签发的短期凭据，TTL 为 6 小时；也支持手动 TURN 或关闭本机中继申请。

## ICE 与 TURN

### 语音能力与房间状态

新版 `join` 可携带可选的 `voice: true` 能力声明。Worker 保留 `joined.room_members` 的旧字符串
数组，同时向成员发送版本化 `room_state`：`members` 是包含 `device_name` 与 `voice` 的完整在线快照。
只有两个成员都声明语音且位于前 8 个语音参与者内时，Worker 才创建 `transport_ready.voice: true`
的 ICE 传输；该传输的 `mappings` 可以为空。未启用语音的旧客户端继续只处理普通 mapping。

语音数据面使用版本 2 的独立 QUIC DATAGRAM envelope（magic、版本、kind、32 位序号、64 位采样时间戳、Opus codec
和长度），不复用普通 UDP mapping 的 channel、分片或应用 socket。语音帧不重传，反馈使用独立的
低频控制包。`room_state` 的在线状态、`transport_ready` 的路径状态和媒体统计彼此独立。

远端包的时间戳仅属于各自采样时钟，不用于比较不同设备是否对齐。接收侧以本机统一 20 ms
播放时钟从各路抖动缓冲取帧，缺包调用 Opus PLC，持续停流后重新缓冲；混音每周期仅输出一帧。
媒体包的序号按实际发送尝试递增（含发送失败），采样时间戳按 960 个采样的播放槽递增：
静音或采集丢帧只在时间戳中留下间隔，采集设备重启时核心保持该时间戳单调。
接收侧按时间戳安排播放、按包序号统计网络丢包；码率使用相邻有效反馈的丢包增量，
忽略重复及乱序反馈，健康流量恢复后不受整场通话的历史丢包率拖累。
序号空洞先进入乱序等待窗口；落后最新包超过 8 个序号或等待达到 160 ms 后才确认为丢包，
窗口内补到的包只计乱序。累计丢包数不回退；反馈的 `HighestSequence` 为已确认区间的最高序号，
不会越过仍在等待的空洞，使丢包增量和序号增量对应同一区间。尚未收到媒体时不发送接收反馈。
PCM 桥接仍接受最多 4 帧批次，Android 采集与播放使用单帧。媒体包格式与协议版本不变；
通话端应一起更新，以避免旧版仍按包序号安排播放或把静音间隔计为丢包。
快照以 `local` 标识本机成员，以独立的 `media_state` 与发送、接收、解码、补帧计数描述媒体，
传输 `active` 本身不会使媒体标为活跃；详细字段见 [桥接 API](../mobile/README.md)。

`turn.order` 可为每端设置独立白名单和尝试顺序，只使用 `udp`、`tcp`、`tls`，最多 3 项且不能
重复；留空为 UDP → TCP → TLS。Cloudflare TURN 在每种类型内先尝试标准端口，再尝试备用端口：

```text
直连 → TURN/UDP 3478 → TURN/TCP 3478 → TURN/TCP 80
     → TURN/TLS 5349 → TURN/TLS 443 → 退避后重试
```

新客户端在 join 中声明 `relay_pairing: all-pairs-v1`。Worker 与双方客户端都支持时，0 轮为直连，
后续轮次覆盖两端有效顺序的全部类型组合，最多 25 组。按偏好深度逐层展开，每层先试同序位，
再试该层与此前类型的交叉组合。配置中的三种类型由核心展开为真实端口阶段；线上的
`tcp_80` / `tls_443` 仅用于代次协调和兼容旧客户端，不作为新的配置选项。
设备名排序固定组合方向，两端顺序长度不同时仍得到同一组合计划。每轮完成独立 ICE / QUIC
认证；失败后推进，遍历完成后回到首轮并退避。普通连接的首轮为直连；任一端要求仅中继时，
首轮为有效中继轮次。不会要求两端使用相同 TURN 接入协议。

`ice.relay_only: true` 要求本机开启 TURN，并在 join 中声明 `relay_only: true`。
Worker 在 `joined` / `transport_ready` 中以 `relay_only_policy: relay-only-v1` 声明支持，
在 ready 中返回本端 `relay_only`、`peer_relay_only` 和两者逻辑或 `relay_required`。
任一端要求仅中继，首次连接、失败回环、切网和重连均跳过直连；旧客户端请求回到第 0 轮或
`direct` 时，Worker 将其转换为首个有效中继轮并退避。仅中继端无法申请本轮 TURN 的
`relay_wait` 轮次也跳过。策略变化会重置代次及候选缓存，并通知双方。
本机开启仅中继后立即取消旧的无约束尝试并关闭其数据路径，不等待信令恢复；对端收到新约束
时同样停止旧的无约束路径。仅中继端只发布 relay 候选，普通端仍可用 host / srflx 与之形成
单跳中继。TURN 不可用时等待或报错，不降级纯直连或旧 IPv6 profile。
旧 Worker 未声明支持时，仅中继客户端报告 `worker_upgrade_required`；普通模式继续兼容旧 Worker。

manual URL 必须显式给出端口。`turn:HOST:PORT` 未写 `transport` 时支持 UDP 和 TCP，
按 `turn.order` 选择并推导本轮接入协议；`turns:HOST:PORT` 仅支持 TLS。
显式 `?transport=udp` 或 `?transport=tcp` 限制普通 TURN 的接入类型；`turns` 不接受 UDP。
核心保留原主机、端口和凭据，只尝试 URL 实际支持的类型；与白名单没有交集时报配置错误。
旧配置的 `tcp_80` / `tls_443` 分别迁移为 TCP / TLS，并合并别名；新保存和导出的配置只使用三种类型。

本端在某轮没有对应服务器且未启用 `relay_only` 时，仍收集 host / srflx 并等待对端 relay；
双方收集完毕都没有 relay，或本机 `relay_only` 却没有本轮服务器时，提前跳过该轮。
`turn.mode: off` 的设备向支持轮次协议的 Worker 声明本机不申请 TURN；它仍可在对端中继轮收集
host / srflx 并等待对端 relay。双方都关闭时有效顺序均为空，只保留直连重试，不再尝试五种
默认中继类型。旧 Worker 忽略此声明，继续使用旧命名阶段。
双方上报顺序但未共同支持完整配对时，兼容原来按相同序位配对、按最长顺序计轮数的行为；
本端用完顺序进入 `relay_wait`。任一端不支持顺序时，使用原命名 phase/policy，配置自定义顺序
的一端显示回退提示。完整跨类型覆盖需要更新 Worker 和两端客户端，仅更新一端不能启用。
每个中继轮最长使用候选收集与连通检查预算（默认合计 16 秒）；
只配置实际可用的类型可以缩短无效尝试。
TURN/TCP / TLS 描述客户端连接 TURN 服务器的方式，数据面仍是 QUIC，不引入 DataChannel / SCTP。

两端都发出 `end_of_candidates` 仅说明常规候选收集结束。候选对的 Failed 状态不能证明迟到响应
或 TURN permission 建立后仍不可达，因此不再据此提前结束。检查以阶段时间预算为准，
提高 Pion 请求次数上限，避免候选集中到达触发额外检查时耗尽默认 7 次额度；常规检查间隔为
200ms，候选变化仍可触发检查。确认双方收集结束且候选表持续为空时可以提前跳过，保留异步
候选入表的宽限时间。没有中继候选等已确定无法满足本轮要求的情况仍可提前结束。

中继阶段同时收集 host、srflx 和 relay 候选，候选对可以是非对称的：一方走自己的 TURN
分配，另一方以直连候选与之配对，只经过一次中继。RFC 8445 候选对优先级已把这类单跳候选对
排在双中继之前；ICE 提名取“已连通且两端候选都过了类型最短等待”的最高优先级候选对，
提名后本代次内固定。含 relay 候选的候选对最早在检查开始 3.5 秒后才可被提名（pion 默认
2 秒），让需要对端先建立 TURN permission 的单跳候选对有时间连通；中继阶段建连因此最多
晚 1.5 秒，直连阶段没有 relay 候选，不受影响。快照 `candidate_pairs` 列出各候选对的
类型、状态、中继跳数和 RTT；提名后 pion 只保活选中候选对，其余数据停留在检查阶段。

TURN 凭据到进入中继阶段时才申请；在途请求复用，失败采用退避。本机凭据暂不可用且未启用
`relay_only` 时，仍收集 host / srflx 并等待对端 relay，不因此重置共享轮次。健康直连不随本机
TURN 缓存刷新而重建；实际选中本机 relay 的路径才随对应凭据刷新。

`RenominateTransports` 手动为每个已有 ICE active 路径发起同阶段 `transport_restart`。
它会创建完整新 ICE 代次，但不断开信令、不重建平台网络绑定，也不做固定周期触发；
旧路径保持到新路径通过 QUIC 身份检查后替换。没有 active 路径、已有 pending 或在途
代次请求、信号离线时不发送请求。

本机 relay 的接入协议来自真实选中 candidate 的 `RelayProtocol`，记录在
`local_relay_protocol`；对端本机申请 TURN 时使用的接入协议通过已认证的 mux 握手
交换，记录在 `remote_relay_protocol`。本机直连候选与对端 relay 候选配对时，`relay_side`
为 `remote`，并不表示本机也申请了中继。尚未完成 mux 握手或旧对端未上报时显示
“接入协议未上报”，不从阶段名、UDP candidate 属性或端口猜测。

## 共享 QUIC 与通道

- 一对设备稳定态共用一条 QUIC，可双向提供和消费多个服务。
- 切换时允许 active + pending 路径；新路径完成认证后替换，应用会话独立保留。
- 控制握手确认 transport ID、generation、两端 runtime 与版本。
- `OPEN_MAPPING` / `MAPPING_ACK` 注册奇偶分配的 channel ID。
- 应用流先写 HMX/1 类型与通道前缀，再进入 v2 会话握手。
- UDP DATAGRAM 外层携带 `channel_id:u32`，内层仍为 HUD/2。
- 每对端只有一个 UDP 接收 / 重组分发器；关闭某条映射不关闭其他通道。

## 可恢复应用会话

`session_id` 是随机 128 位标识。提供端按映射 ID、客户端证书指纹和 session ID 查找会话，
每次恢复继续验证身份。本地监听器与应用 socket 由会话管理器持有，传输重连不等于关闭应用连接。

### TCP

恢复握手交换双方已接收的字节偏移和 FIN 状态。DATA 使用绝对偏移，ACK 仅确认已写入
对端 TCP socket 的数据；未确认后缀重放，重复前缀跳过，FIN 支持半关闭及恢复。

每方向按需缓存最多 16 MiB 未确认数据，使用 32 KiB 分块；ACK 释放完整块，避免搬移未确认后缀。
同一核心运行的所有映射、消费与提供会话共用 128 MiB 重放块容量限额，未使用的块尾容量也计入。
完整块在 ACK 后进入有界复用池，空闲缓存最多 16 MiB 且计入上述总量。5 秒未取用后由回收周期
释放，停止运行时清空；减少持续传输的重复分配。状态通知仅在存在等待者时分配唤醒通道。
达到单会话或共享限额后向应用施加背压，释放容量会唤醒等待会话；不会预分配每个会话的上限。
该限额不包括每个读写循环的帧缓冲、QUIC 接收窗口、内核 socket 缓冲等其他内存。
QUIC 单流接收窗口可增长至 16 MiB，单连接至 32 MiB；TCP 应用层仍只确认已写入目标 socket 的数据。
首次应用连接有独立建立预算；明确的服务拒绝、超时、会话过期或限额错误结束本次连接。
已建立会话按 `session_timeout` 保留脱离传输的状态，默认 10 分钟；正常附着的空闲 TCP
不按这个期限回收，过期恢复请求不会静默创建新的上游连接。
应用 socket 非 EOF 错误通过 QUIC stream reset 代码 `0x485302` 表达终止；接收端关闭对应应用会话，
避免把客户端 RST 当成网络迁移保留到超时。代码 0 的传输取消仍允许恢复。及时终止需要两端均支持
该代码；旧端将未知流错误当作可恢复中断。DATA / ACK / FIN 格式和 session protocol 2 保持兼容。

### UDP

消费端按本地源地址分配会话 ID，提供端为每个 ID 持有独立 UDP socket，防止多客户端回包串流。
稳定隧道 ID 让新传输接管仍存活的关联。

单报文最大 65535 字节，分片初始帧长 1100 字节并随路径 MTU 缩小。直连阶段的 QUIC 启用路径
MTU 发现（RFC 8899），分片帧长随发现结果增大，快照 `datagram_limit` 为当前数据报上限；
中继阶段保持 1200 字节初始报文，不探测。
重组期限为 5 秒，每传输上限 128 个未完成报文 / 4 MiB；共享传输另限制完整报文队列的累计内存。
缺片、超限和发送错误仅丢弃对应报文。UDP 不增加可靠重传，空闲关联按双向活动时间回收。
完整单帧报文直接复制到交付队列，不分配逐字节重组位图。重组过期表最多每 250 ms 扫描一次，
访问具体条目时仍检查其 5 秒期限；去重记录维持 2048 条上限。

### 资源与恢复边界

TCP / UDP 各有 256 个会话限额；ICE 消费侧还执行设备级总量限制，提供侧执行全局限制。
共享传输的 64 个握手槽位在会话握手完成后释放，长连接不占用握手槽位。QUIC 接收流限额同时
容纳 256 条 TCP、映射 UDP 控制流及在途握手，避免提前卡在 64 / 128 条流。
停止、进程退出、应用主动关闭、配置作用域变化或超过保留期，会结束对应会话。
网络切换期间业务可能暂停，进程重启后建立新会话，不恢复旧进程 socket。

## 协调与租约

Worker 持久化传输 ID / generation，协调双端并发重启，并校验消息发送者、目标设备关系和代次。
新客户端在 `join` 中发送 `relay_order`；支持轮次的 Worker 在 `transport_ready` 中返回
`relay_round`、本端 `relay_order` 和 `peer_relay_order`。新客户端以 `transport_restart.relay_round`
推进轮次，`phase` 仍按收方顺序翻译，供旧客户端使用；旧 Worker 忽略未知 join 字段并继续走
原 phase 路径。Worker 会校验顺序 token、去重和长度，顺序变化会重置传输代次与候选缓存。
每端每代最多 64 个候选，单候选最多 2048 字符、累计文本 16 KiB；新信令消息最多 64 KiB。
房间最多 16 个在线设备，房间离线后保留认证 token。

客户端加入 / 重连时完整 `transport_sync`。支持轻量续租时，每 5 分钟对在线 ICE 映射关系
发送 `transport_renew`，Worker 用 `transport_lease` 返回 10 分钟租约，不重放候选或写数据库。
旧 Worker 回退为定期完整同步。没有在线 ICE 映射或仅 IPv6 连接时，不发送这种周期业务同步。

信令短断期间健康数据路径可继续存在，但授权受 10 分钟租约约束，离线时不注册新通道。
在线记录不按创建时间过期；离线传输记录保留 24 小时，再由一次性 alarm 清理。

## Android 网络边界

外部 ICE / STUN / TURN、信令和 DNS 使用同一选定 Network。候选地址来自系统 LinkProperties，
回环应用监听与上游回环连接不绑定外部网络。Network 失效返回明确错误，恢复交给 supervisor。
UI 快照刷新与协议保活分离，改变界面采样频率不改变传输保活或会话语义。

映射快照中的 `protocol` 表示该隧道实际使用的应用协议，同一映射只会产生 TCP 或 UDP
其中一种会话计数；`tcp_sessions` / `udp_sessions` 仅保留兼容字段，展示端应按 `protocol`
选择对应字段。`read_bytes` 与 `written_bytes` 是该映射应用数据的方向计数，适用于 TCP 和
UDP：`read_bytes` 是从本地端点读入并转发给对端的字节（本机发送），`written_bytes` 是从
对端接收并写入本地端点的字节（本机接收）；两者按当前运行中的映射累计，TCP 会话关闭后
不会归零，展示端不要按字段名反推方向。`tcp_read_bytes` / `tcp_written_bytes` 仍用于 TCP
重放细节兼容展示。对端传输快照中的 `bytes_sent` / `bytes_received` 是 ICE 线路累计字节，
包含协议开销，用于展示线路流量，不与映射的 `read_bytes` / `written_bytes` 对账。

平台适配约定见 [mobile API](../mobile/README.md)，Go 兼容层见 [anet README](../core/compat/anet/README.md)。
