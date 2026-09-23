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

`udp-tcp-tls-v1` 策略顺序：

```text
直连 → TURN/UDP → TURN/TCP 80 → TURN/TCP 3478
     → TURN/TLS 443 → TURN/TLS 5349 → 退避后重试
```

每阶段有独立 ICE 代次，先尝试同组首选端口，再尝试备用端口；未配置的阶段跳过。
与旧客户端或旧 Worker 互通时协商兼容阶段，不混用两套顺序。
TURN/TCP / TLS 描述客户端连接 TURN 服务器的方式，数据面仍是 QUIC，不引入 DataChannel / SCTP。

两端都发出 `end_of_candidates` 后，候选对集合即已确定；若全部检查失败，本阶段立即结束并进入
下一条路径，不再等满阶段预算。

中继阶段同时收集 host、srflx 和 relay 候选，候选对可以是非对称的：一方走自己的 TURN
分配，另一方以直连候选与之配对，只经过一次中继。RFC 8445 候选对优先级已把这类单跳候选对
排在双中继之前；ICE 提名取“已连通且两端候选都过了类型最短等待”的最高优先级候选对，
提名后本代次内固定。含 relay 候选的候选对最早在检查开始 3.5 秒后才可被提名（pion 默认
2 秒），让需要对端先建立 TURN permission 的单跳候选对有时间连通；中继阶段建连因此最多
晚 1.5 秒，直连阶段没有 relay 候选，不受影响。快照 `candidate_pairs` 列出各候选对的
类型、状态、中继跳数和 RTT；提名后 pion 只保活选中候选对，其余数据停留在检查阶段。

TURN 凭据到进入中继阶段时才申请；在途请求复用，失败采用退避。健康直连不随本机 TURN
缓存刷新而重建；实际选中本机 relay 的路径才随对应凭据刷新。

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

每方向最多缓存 256 KiB 未确认数据，达到上限后向本地应用施加背压。
首次应用连接有独立建立预算；明确的服务拒绝、超时、会话过期或限额错误结束本次连接。
已建立会话按 `session_timeout` 保留脱离传输的状态，默认 10 分钟；正常附着的空闲 TCP
不按这个期限回收，过期恢复请求不会静默创建新的上游连接。

### UDP

消费端按本地源地址分配会话 ID，提供端为每个 ID 持有独立 UDP socket，防止多客户端回包串流。
稳定隧道 ID 让新传输接管仍存活的关联。

单报文最大 65535 字节，分片初始帧长 1100 字节并随路径 MTU 缩小。直连阶段的 QUIC 启用路径
MTU 发现（RFC 8899），分片帧长随发现结果增大，快照 `datagram_limit` 为当前数据报上限；
中继阶段保持 1200 字节初始报文，不探测。
重组期限为 5 秒，每传输上限 128 个未完成报文 / 4 MiB；共享传输另限制完整报文队列的累计内存。
缺片、超限和发送错误仅丢弃对应报文。UDP 不增加可靠重传，空闲关联按双向活动时间回收。

### 资源与恢复边界

TCP / UDP 各有 256 个会话限额；ICE 消费侧还执行设备级总量限制，提供侧执行全局限制。
停止、进程退出、应用主动关闭、配置作用域变化或超过保留期，会结束对应会话。
网络切换期间业务可能暂停，进程重启后建立新会话，不恢复旧进程 socket。

## 协调与租约

Worker 持久化传输 ID / generation，协调双端并发重启，并校验消息发送者、目标设备关系和代次。
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
选择对应字段。`read_bytes` 与 `written_bytes` 是该映射本地端点的累计收发字节，适用于 TCP
和 UDP；`tcp_read_bytes` / `tcp_written_bytes` 仍用于 TCP 重放细节兼容展示。

平台适配约定见 [mobile API](../mobile/README.md)，Go 兼容层见 [anet README](../core/compat/anet/README.md)。
