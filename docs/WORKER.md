# Worker 部署

Worker 提供房间发现、连接协调、状态面板和按需签发 TURN 凭据。
业务数据由客户端通过直连 QUIC、Peer Relay、Hole 或 TURN 传输。

[准备](#准备) · [配置与发布](#配置与发布) · [房间面板](#房间面板) · [升级与兼容](#升级与兼容) · [TURN broker](#turn-broker) · [项目首页](../README.md)

## 准备

- 已登录目标 Cloudflare 账户的 Wrangler CLI。
- 支持项目所需 Durable Objects 的账户配置。
- 如需 Cloudflare TURN，准备 TURN Key ID 及对应 API Token。

从 [wrangler.example.toml](../worker/wrangler.example.toml) 复制本地 `worker/wrangler.toml`，
设置 Worker 名称和路由。使用 `workers.dev` 域名时可移除自定义域名路由，保留以下绑定与迁移历史：

| 绑定 | 类 | 迁移 |
| --- | --- | --- |
| `ROOMS` | `Room` | `v1` |
| `TURN_BROKER` | `TurnBroker` | `v2-ice` |

部署时保留完整 `worker/` 源码及根目录 `versions.json`，由 Wrangler 打包。

## 配置与发布

```bash
cd worker
wrangler secret put PASSWORD

# 使用 Cloudflare TURN 时配置
wrangler secret put TURN_KEY_ID
wrangler secret put TURN_KEY_API_TOKEN

wrangler deploy
```

| 设置 | 位置与作用 |
| --- | --- |
| `server_url` | 客户端填写 `wss://HOST/ws` |
| `PASSWORD` | Worker secret，对应客户端 `password` |
| 房间口令 | 客户端约定的 `token`，与 TURN API Token 无关 |
| `PANEL_PATH` | Worker 变量，默认 `/panel`；设为 `off` 关闭面板 |
| `TURN_KEY_ID` / `TURN_KEY_API_TOKEN` | 只配置在 Worker，客户端接收短期凭据 |

根 `build.sh` 只构建客户端，Worker 需单独部署。

## 房间面板

访问 `https://HOST/panel`，依次输入信令密码、房间号和房间口令。
面板只读，需要先由客户端创建房间，不占用设备名额。

拓扑图展示设备、信令入口与设备间连接，支持拖动、缩放、图层筛选，以及点按查看版本、路径、协商进度、映射和语音状态。
手机支持双指缩放；亮暗主题默认跟随系统，也可手动选择。刷新或重新打开面板时，在会话有效期内自动恢复已验证的房间。
登录时可勾选“在此设备保持登录 7 天”，适合个人设备；默认不勾选，使用浏览器会话 Cookie，服务器有效期最多 12 小时。
浏览器的“恢复上次会话”功能可能保留会话 Cookie，因此公共设备用完应点击“退出”。勾选后有效期固定为 7 天，访问不会延长期限。
切换房间保留信令验证，清除原房间授权；进入新房间仍需验证口令。退出会撤销服务器会话、清除 Cookie，并关闭同一登录会话的面板连接。
退出需要联网，失败时页面会提示重试。服务器保留未完成的撤销记录，由 alarm 重试关闭其他标签页的连接；撤销中的会话不能恢复或新建连接。

面板展示 Worker 最近收到的状态，不能据此推断实时延迟或流量。断线重连时保留并淡化最后收到的拓扑，重新连接后更新。

在 Wrangler 配置的 `[vars]` 中更换入口：

```toml
PANEL_PATH = "/your/path"
```

路径支持斜杠分隔的字母、数字、下划线和连字符，不能使用 `/ws`；无效路径会关闭面板。
面板需要 HTTPS 和同源访问。浏览器只保存随机会话 ID，Cookie 使用 `__Host-` 前缀以及 `Secure`、`HttpOnly`、`SameSite=Strict`；
页面不把密码、房间口令或会话 ID 写入 URL、localStorage 或 sessionStorage，提交后清空密码输入框。
服务器新增的会话记录只存会话 ID 摘要、带密钥的认证绑定、已验证房间及其授权证明，不复制两种密码。
会话绑定访问域名、面板路径和信令密码；更换信令密码后旧会话不能恢复或建立新连接，房间口令变化也会使旧房间授权无法建立新连接。
保持登录意味着同一浏览器的使用者在期限内可查看该房间，不能代替设备锁屏；需要立即结束当前登录时使用“退出”。
每个房间最多 32 个面板连接，全局最多保留 1024 个浏览器会话，过期会话由 alarm 清理。会话复用现有 `ROOMS` 绑定，无需新增迁移。
内部名称 `$hole-panel-sessions` 专用于会话存储，不能用作房间号；已有同名房间需在客户端改用其他房间号。

## 升级与兼容

先部署 Worker，再升级客户端，并沿用原有绑定和迁移历史。

共享核心与 Worker 均至少为 1.1.0 时启用房间控制。同组设备可同时保持多个 Worker 连接；
某个连接意外中断后，可通过仍在线的组员接收通知。需要协商时恢复直接信令，连接恢复后保持在线。
健康数据路径不依赖中央授权周期续租，第三方 TURN 凭据仍按服务期限刷新。

共享核心 1.1.0 使用统一 ALPN `hole`，参与连接的核心需同步升级。
旧 CLI 配置需删除 `transport.preferred`、`transport.allow_legacy` 和 `candidate_addresses`，
`-transport` 参数已移除；IPv6 仍由 ICE 支持。

Worker 版本由根 `versions.json` 的 `worker.version` 定义。
房间签名与状态沿用现有存储，无需新增绑定或迁移；更新时使用完整源码，避免遗漏依赖模块。

固定入口、Peer Relay、Hole 中继和在线配置的能力要求见 [协议说明](PROTOCOL.md)。
固定入口需开放对应 UDP 端口；运行 Hole 中继需使用同房间的 CLI，配置见 [项目说明](../README.md#固定入口与设备中继)。

## TURN broker

broker 通过全局 Durable Object 缓存短期凭据并合并在途请求。

| 项目 | 值 |
| --- | --- |
| 默认 TTL | 6 小时，范围 600–21600 秒 |
| 刷新时间 | 约在生命周期的 80% |
| 全局签发限额 | 最多 8 并发、60 次 / 分钟 |
| 房间限额 | 30 次 / 分钟 |
| 设备限额 | 3 次 / 5 分钟 |

未配置 TURN 时仍可尝试直连、Peer Relay 和已配置的 Hole 中继。
开发检查命令见 [构建指南](BUILDING.md#回归测试)。
