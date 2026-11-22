# Worker 部署

`worker/worker.js` 提供房间 WebSocket 协调，`worker/worker_ice.mjs` 提供 ICE / 固定入口关系管理和 TURN broker，`worker/worker_room.mjs` 管理房间图和认证通知。
Worker 不转发业务数据；客户端之间使用直连 QUIC、Peer Relay 节点中继、Hole 设备中继或 TURN。

[准备](#准备) · [配置与发布](#配置与发布) · [升级兼容](#升级与兼容) · [Hole 授权](#hole-三方授权) · [TURN broker](#turn-broker) · [验证](#验证) · [项目首页](../README.md)

| Worker 负责 | 客户端负责 |
| --- | --- |
| 房间发现、设备关系、建连协调与状态镜像 | UDP 入口、DNS、QUIC 与业务转发 |
| 按需签发 TURN 短期凭据 | 直连、Hole / TURN 数据通道 |

## 准备

- 已登录目标 Cloudflare 账户的 Wrangler CLI。
- 支持项目所需 Durable Objects 的账户配置。
- 如需 Cloudflare TURN 中继，准备 TURN Key ID 与对应 API Token。

从 [wrangler.example.toml](../worker/wrangler.example.toml) 复制本地 `worker/wrangler.toml`，按环境设置 Worker 名称和 `[[routes]]`。
使用 `workers.dev` 域名时可移除自定义域名路由。保留以下绑定和迁移关系：

| 绑定 | 类 | 迁移 |
| --- | --- | --- |
| `ROOMS` | `Room` | `v1` |
| `TURN_BROKER` | `TurnBroker` | `v2-ice` |

已有部署继续沿用迁移历史。更新时保留完整 `worker/` 源码和根目录 `versions.json`，由 Wrangler 打包部署；沿用原绑定与迁移配置。

## 配置与发布

```bash
# 从仓库根目录进入 worker/
cd worker
wrangler secret put PASSWORD

# 使用 Cloudflare TURN 时配置；仅直连模式可省略这两项
wrangler secret put TURN_KEY_ID
wrangler secret put TURN_KEY_API_TOKEN

wrangler deploy
```

| 设置 | 配置位置 / 对应关系 |
| --- | --- |
| `server_url` | 客户端填写 `wss://HOST/ws` |
| `PASSWORD` | Worker secret；对应客户端 `password` |
| 房间口令 | 客户端约定的 `token`，与 TURN API Token 无关 |
| `PANEL_PATH` | 可选 Worker 变量，面板路径，默认 `/panel`；设为 `off` 可关闭 |
| `TURN_KEY_ID` / `TURN_KEY_API_TOKEN` | 只配置在 Worker；客户端接收按需签发的短期凭据 |

`build.sh` 只构建客户端，不执行上述部署命令。

## 房间面板

部署后访问 `https://HOST/panel`。先输入信令密码 `PASSWORD`，再输入房间号和客户端配置的房间口令 `token`。
在 Wrangler 配置的 `[vars]` 中设置 `PANEL_PATH = "/your/path"` 可更换入口；路径由斜杠分隔的字母、数字、下划线或连字符组成，不能使用 `/ws`，无效路径会关闭面板。面板需要 HTTPS 和同源访问。

面板分两页验证：信令密码校验成功后，才显示房间号与房间密码输入页。进入房间后以可拖动、缩放的拓扑图显示设备，实际直连信令的设备以橙色虚线连接 Cloudflare 节点；设备之间的连接以绿色实线、灰色虚线和红色虚线分别表示已确认、协商中和受阻。信令委托设备不绘制到 Cloudflare 的直接连线。
悬停可预览状态，点击设备、Cloudflare 节点或连线可查看版本、信令状态、连通分组、双方确认的路径、协商与打洞进度、映射 ID 和语音状态。可以切换全部、设备或信令连接图层；手机支持拖动与双指缩放，点按后显示底部详情卡片。设备和连线随事件平滑增删，系统开启减少动态效果时停用动画。
亮暗主题默认跟随系统，手动选择后只保存主题偏好。切换房间保留当前信令验证，退出则清除当前页面的凭据与房间状态；断线重连期间保留并淡化最后收到的拓扑，直到重新取得房间状态。
这些数据来自 Worker 已收到的房间图与传输事件；没有新增客户端周期上报或主动探测。委托信令和连接状态以最近收到的事件为准，不能用来推断实时延迟或流量；暂未在 Worker 可见的变化不会显示。
面板只读，首次加入必须由设备创建房间，面板不能创建房间或占用设备名额。房间口令错误和房间不存在均拒绝访问。

信令密码与房间口令只保存在当前页面内存；断线时使用内存中的凭据重新认证并恢复订阅，关闭页面或退出后需重新输入。浏览器使用有效期 10 分钟的短期会话建立 WebSocket，不把密码放入 URL；更换信令密码会使旧会话无法建立新连接。会话签名密钥保存在现有 `ROOMS` 绑定的独立 Durable Object 中，无需新增绑定或迁移，每个房间最多 32 个面板连接。

## 升级与兼容

协议升级按 **Worker → 客户端** 顺序部署。

共享核心和 Worker 均至少为 1.1.0 时启用房间控制与按需信令：每个现有连通组保留少量 WS，
其他成员复用已有 QUIC 接收通知；建连双方临时直接连接 Worker。成员不依赖在线租期，
健康路径不需要中央周期续租。最后一个入口恢复失败一次后通知组内其他成员共同恢复。
房间签名密钥、连接图和状态镜像保存在原 Room storage，不新增绑定、secret 或 DO 迁移。
`worker.version` 独立维护在根 `versions.json`；部署包需包含其依赖，不能只复制两个旧 Worker 源文件。
ALPN 统一改为 `hole`，旧仅 IPv6 模式已移除，参与连接的核心需同步升级；原 `ipv6` / `allow_legacy`
配置需要更新。设备图与版本进入 `room_mirror`，面板另外使用现有传输事件展示协商状态。详见 [按需信令协议](PROTOCOL.md#房间控制与按需信令)。

Peer Relay 需要 Worker 和三端客户端协商 `peer-relay-v1`，无需新增绑定、secret 或 Durable Object 迁移。
Worker 只选择同房间、已与两端直连的节点，向节点发放绑定运行身份与直连代次的路由授权；业务密文经客户端的已有连接转发。
新版容量限制由中继本地执行，Worker 不周期续租；旧软件组合仍按协商的租约和容量记录处理。
直连及打洞耗尽后尝试一次节点路径，无候选或失败则进入原 Hole / TURN 轮次。旧客户端和旧 Worker 保持原有回退行为。
授权字段、本地回收及数据帧见 [Peer Relay 协议](PROTOCOL.md#peer-relay-节点中继)。

新版 Worker 的 `joined.live_config` 能力支持在现有 WebSocket 更新服务声明，映射和语音变化
无需反复退出房间。客户端仍兼容旧 Worker：未声明能力时回退到重新加入。语音原地开关还要求
双方客户端支持在线配置协议，旧对端会重建对应传输。该能力不需要新增 Durable Object 迁移；
更新时由完整源码打包。协议与修订确认规则见 [在线配置](PROTOCOL.md#在线配置与兼容)。
`joined.signal_resume` 能力允许同一运行实例的信令 WebSocket 替换时复用仍健康的数据路径；
新版授权不以时间到期，WS 替换保留健康连接代次；网络、身份或实际传输约束变化仍会正常重建。

路径恢复与手动重选修复需要同时更新 `worker.js`、`worker_ice.mjs` 和两端核心，无需新增绑定、
secret 或 Durable Object 迁移。重连比较忽略在线声明的确认元数据，备用 Hole 节点变化不再
重置健康路径；旧代次失败不能推进新代次，已进入预测打洞时迟到的 ICE 失败也不再跳到中继。
手动重选从普通 ICE 开始，失败后可进入预测打洞，使用 `manual_probe` 标记延续本轮探测；
旧连接在新路径认证成功前保留，已有健康直连时不会因探测失败降级到中继。
具体回退与仅中继 / 固定入口约束见 [凭据与手动重选](PROTOCOL.md#凭据与手动重选)。

NAT 分类服务上限为 16 条；地址池模型每个公网 IP 的目标数与端口数上限相应为 16 和 48，公网 IP 分组仍最多 24 组。扩容分类服务时需同步更新 `worker_ice.mjs`，避免有效 offer / model 因旧的 8 目标限制被拒收。

<details>
<summary>固定入口：能力、路径约束与网络准备</summary>

固定入口功能直接扩展当前协议，需要 Worker 和两端核心同步升级，不增加固定协议的旧版兼容分支。
新版成员声明 `fixed-quic-mux-v1`，有固定监听的成员在 join 中公布 `fixed_endpoint`。
Worker 持久化并在 `transport_ready` 中返回入口、监听方和拨号方；一端配置后，整对设备仅使用
固定路径，失败只推进代次和方向，不转入 ICE 或 TURN。与任一端仅中继要求冲突时返回
`fixed_relay_conflict`。Worker 本身不监听隧道 UDP，也不需要新增绑定或迁移。
入口设备需自行开放或映射 UDP 端口；业务 `consume.expose` 端口按服务协议开放。
域名解析、QUIC 连接和业务转发在客户端进行。真实公网地址、端口映射和防火墙需另外验收。
</details>

<details>
<summary>TURN 顺序、跨类型配对与仅中继策略</summary>

Worker 与新客户端均支持 `relay_order` / `relay_round` 后才会启用每端独立 TURN 顺序；
进一步由双方声明 `relay_pairing: all-pairs-v1` 启用完整跨类型组合，最多 25 个中继轮次，
避免可用类型处于不同序位时无法相遇。新旧客户端混用时继续原轮次计划；能力变化会重置代次。
Worker 识别 `relay_enabled: false` 后将该端有效顺序置空，双方都关闭时只保留直连重试，
单侧关闭时仍为该端保留 `relay_wait`，让它接收对端 relay 候选。旧 Worker 忽略此字段并保留
命名 phase 行为。
Worker 支持 `relay_only: true`，将任一端的仅中继要求通知双方，并在首次连接和所有重试回环中
跳过直连；仅中继端无法参与的 `relay_wait` 轮也会跳过。`joined` / `transport_ready` 返回
`relay_only_policy: relay-only-v1`，ready 同时返回双方设置及合并后的 `relay_required`。
策略随成员与传输记录持久化，策略变化使代次和候选缓存失效。普通端仍可使用单跳中继。
仅中继客户端连接旧 Worker 时提示升级，不静默回退；应先部署 Worker，再更新客户端。
只要任一端没有上报顺序，Worker 就保持原有命名 phase 与 `relay_policy` 协商。
新 Worker 校验不超过 5 项的内部 token（`udp`、`tcp_80`、`tcp`、`tls_443`、`tls`），并在顺序变化时
重置 generation 与 ICE 候选缓存。
这些是核心从配置 `udp` / `tcp` / `tls` 自动展开的内部阶段。Cloudflare 凭据 URL 按
UDP 3478、TCP 3478、TCP 80、TLS 5349、TLS 443 排序；缓存键随端口优先级策略变化。
行为细节见 [协议说明](PROTOCOL.md#ice-与-turn)。

</details>

<details>
<summary>语音能力与房间容量</summary>

启用语音的 join 使用可选 `voice: true` 字段。Worker 广播 `room_state` 完整成员快照，并为两个
语音成员创建允许空 `mappings` 的 voice-only transport；语音参与者上限为 8，超过时返回
`voice_capacity`，不影响普通 16 人房间和 mapping 配对。旧客户端仍收到原有 `room_members` 字符串数组。

</details>

## Hole 三方授权

`joined` 声明 `hole-relay-v1` 能力。具备真实映射或语音关系的两端通过 `hole_device` 和
统一顺序生成 `relay_plan`；无需先创建到中继设备的映射。只有声明固定入口与
`hole_serve` 的 CLI 可服务。Hole 轮次向中继发送 `hole_grant`，向双方发送带授权的
`transport_ready`；包含设备身份、运行实例、网络和传输代次、入口及软件版本。
容量按正在建立或活动的设备对统计，不按映射数统计；暂时不可用时双方按计划回退。

`hole_revoke` 在离线、服务变化和阶段推进时撤销授权。三方重连均重新核对身份，
新版中继本地管理容量和生命周期；任一端 2 分钟没有认证流量即回收路由。已回收的旧代次授权不能被同步重放复活。
`hole_failed` 保存失败状态，休眠后不得恢复旧路由。Hole 轮次不需要 TURN broker 配置。
旧 Worker、旧对端及旧中继必须先升级；未使用 Hole 的设备继续使用原兼容流程。
部署顺序为 Worker → 客户端。

## TURN broker

broker 是全局 Durable Object，按房间、设备、密钥策略和 TTL 缓存凭据，并合并在途请求。

| 项目 | 值 |
| --- | --- |
| TTL | 默认 6 小时，范围 600–21600 秒 |
| 刷新 | 约在生命周期的 80% |
| 缓存策略版本 | `TURN_POLICY_VERSION` |
| 全局签发限额 | 最多 8 并发、60 次 / 分钟 |
| 房间限额 | 30 次 / 分钟 |
| 设备限额 | 3 次 / 5 分钟 |

签发请求具有超时、响应大小和迟到结果检查，alarm 根据真实过期时间安排清理。
未配置 TURN 时直连仍会尝试，客户端显示中继服务当前状态。

## 验证

```bash
# 仓库根目录
node --test worker/worker_ice.test.mjs worker/worker_fixture.test.mjs
```

测试使用内存 Durable Object 模型，覆盖协商、关系恢复、候选路由、续租和 broker 行为。
实际部署后还需检查账户绑定、WSS 连接、短期凭据签发与真实网络的直连 / 中继路径。
迁移、按需信令和会话恢复约定见 [协议说明](PROTOCOL.md)。
