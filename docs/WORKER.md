# Worker 部署

`worker.js` 提供房间 WebSocket 协调，`worker_ice.mjs` 提供 ICE 关系管理和 TURN broker。
Worker 不转发业务数据；客户端之间使用直连 QUIC 或通过 TURN 中继。

## 准备

- 已登录目标 Cloudflare 账户的 Wrangler CLI。
- 支持项目所需 Durable Objects 的账户配置。
- 如需 Cloudflare TURN 中继，准备 TURN Key ID 与对应 API Token。

编辑根目录 `wrangler.toml`，按环境设置 Worker 名称和 `[[routes]]`。
使用 `workers.dev` 域名时可移除自定义域名路由。保留以下绑定和迁移关系：

| 绑定 | 类 | 迁移 |
| --- | --- | --- |
| `ROOMS` | `Room` | `v1` |
| `TURN_BROKER` | `TurnBroker` | `v2-ice` |

已有部署继续沿用迁移历史。更新时同时发布 `worker.js`、`worker_ice.mjs` 与 `wrangler.toml`。

## 配置与发布

```bash
# 仓库根目录
wrangler secret put PASSWORD

# 使用 Cloudflare TURN 时配置；仅直连模式可省略这两项
wrangler secret put TURN_KEY_ID
wrangler secret put TURN_KEY_API_TOKEN

wrangler deploy
```

客户端 `server_url` 填写 `wss://HOST/ws`，`password` 对应 `PASSWORD`。
房间密码是客户端之间约定的 `token`，不是 TURN API Token。
TURN 主凭据只配置在 Worker；客户端接收按需签发的短期凭据。

`build.sh` 只构建客户端，不执行上述部署命令。
协议升级按 **Worker → CLI / Android / Wear** 顺序部署，保留与旧客户端所需的兼容 profile。
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

## TURN broker

broker 是全局 Durable Object，按房间、设备、密钥策略和 TTL 缓存凭据，合并在途请求，
约在凭据生命周期的 80% 刷新。默认 TTL 6 小时，范围为 600–21600 秒。
可通过 `TURN_POLICY_VERSION` 调整缓存策略版本。

签发限额为全局最多 8 个并发、60 次/分钟，房间 30 次/分钟、设备 3 次/5 分钟。
签发请求具有超时、响应大小和迟到结果检查，alarm 根据真实过期时间安排清理。
未配置 TURN 时直连仍会尝试，客户端显示中继服务当前状态。

## 验证

```bash
node --test worker/worker_ice.test.mjs
```

测试使用内存 Durable Object 模型，覆盖协商、关系恢复、候选路由、续租和 broker 行为。
实际部署后还需检查账户绑定、WSS 连接、短期凭据签发与真实网络的直连 / 中继路径。
迁移、租约和会话恢复约定见 [协议说明](PROTOCOL.md)。
