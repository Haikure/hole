# 桌面 Go 桥接

`hole/desktop` 为 Windows / Linux GUI 提供独立的 stdio 宿主，入口为
`cmd/hole-desktop-core`。当前交付只有 Go 桥接，不含 Rust / Slint 界面。
界面后续实现见 [桌面 GUI 计划](../docs/DESKTOP_GUI_PLAN.md)。

```text
Slint → Rust CoreClient → child stdin/stdout → desktop → core.Engine
                                                        └─ ICE / QUIC / TCP / UDP
```

一个宿主进程持有一个 Engine；多条映射共享它。IPC 只承载配置、控制、事件和快照，
业务字节不经过 GUI。桌面宿主不读取配置文件、不自动启动网络、不监听控制 HTTP/TCP 端口，
不依赖 gomobile、Qt 或 Slint。传输、身份、限额与恢复仍由 `core/` 实现。

## 构建与启动

```bash
./build.sh desktop-core --os linux --arch amd64
./build.sh desktop-core --os windows --arch amd64
```

产物为 `dist/desktop-core/hole-desktop-core-<goos>-<goarch>[.exe]` 及同名 `.sha256`。
使用 `CGO_ENABLED=0`、`-mod=readonly`、`-trimpath`、`-s -w`，缓存进入根 `.cache/`。
构建目标可以组合，但 `cli`、`android`、`wear` 和 `all` 原有目标集合不变；
`all` 不包含 `desktop-core`。桌面源码不属于共享 AAR 的源码摘要输入。

宿主无运行参数；`--help` 写到 stderr，版本通过 `hello` 查询。仅查询本地状态：

```bash
printf '%s\n' \
  '{"jsonrpc":"2.0","id":"1","method":"hello"}' \
  '{"jsonrpc":"2.0","id":"2","method":"snapshot"}' \
  '{"jsonrpc":"2.0","id":"3","method":"shutdown"}' \
  | ./dist/desktop-core/hole-desktop-core-linux-amd64
```

GUI 用绝对路径直接启动对应文件，不经过 shell、不从 PATH 搜索同名程序；配置凭据放在
stdin 消息中，不放到启动参数。Rust `CoreClient` 分离 stdout / stderr，并持续读取两条流。

## 协议 v1

使用 **JSON-RPC 2.0 的单请求子集 + UTF-8 NDJSON 分帧**：

- 每行一个 JSON 对象，发送方以 LF 结束；接收方也接受 CRLF。
- 同一行内使用紧凑 JSON，不发送跨行排版的对象；字符串内换行按 JSON 转义。
- 管道读写分块不是消息边界；GUI 累积到 LF 后再解析，兼容一读多行与半行。
- EOF 前最后一个完整 JSON 对象即使没有 LF，也会处理；空白行忽略。
- 请求必须携带字符串 `id`，长度 1～64 字节，只使用 `A-Z a-z 0-9 . _ : -`。
  GUI 为在途请求生成唯一 ID；宿主按接收顺序处理，同一请求重复发送将再次处理，
  进程之间不保留请求记录。
- 不接受批处理或客户端无 ID 通知；此类消息不执行并返回 `-32600`、`id: null`。
  宿主向 GUI 发送的事件是无 ID 通知。
- 字段名区分大小写；拒绝未知字段、重复键、非法 UTF-8、多个 JSON 值及超过 64 层的嵌套。
- 成功响应只有 `result`，失败只有 `error`。合法请求沿用其 ID，非法请求信封使用 `null`。
- stdout 仅输出协议消息；诊断与帮助写 stderr。配置凭据不作为日志输出；配置导入和显式含凭据导出
  的响应本身属于敏感数据，GUI 不记录这些响应原文。

### 能力查询

```json
{"jsonrpc":"2.0","id":"hello-1","method":"hello"}
```

`result` 字段：

| 字段 | 含义 |
| --- | --- |
| `bridge_version` | 当前桌面控制协议版本，1 |
| `api_version` | 当前共享核心 API 版本，1 |
| `core_version` | 与 CLI / AAR 相同的核心版本标识，不是桌面源码摘要 |
| `methods` | 支持的方法列表 |
| `limits` | 请求/配置文档/输出字节上限、队列容量、写入期限 |

GUI 启动后主动发送 `hello`，核对协议与核心 API 版本后再发送配置。
宿主不主动输出启动横幅，也不要求以 `hello` 作为首条请求。

### 方法

| 方法 | `params` | 结果与副作用 |
| --- | --- | --- |
| `hello` | 省略或 `{}` | 返回能力；不联网 |
| `validate` | 完整运行请求 | `{"valid":true}`；共享校验，不保存配置、不联网 |
| `start` | 完整运行请求 | `{"accepted":true}`；异步启动 Engine，只有信令服务返回 `joined` 后才发布 `engine=running` |
| `apply_config` | 完整运行请求 | `{"accepted":true}`；停止态暂存，运行态重配 |
| `stop` | 省略或 `{}` | 清理本次运行及应用 socket 后返回 `{"accepted":true}`；宿主保留 |
| `snapshot` | 省略或 `{}` | 当前核心快照及桥接事件丢弃计数 |
| `network_changed` | 省略或 `{}` | 通知已有核心重建网络路径；停止态不启动网络 |
| `renominate_transports` | 省略或 `{}` | 对已有 ICE active 路径发起新代次；进行中或无可用路径时幂等 |
| `decode_cli_config` | `{"text":"YAML 或 JSON 文本"}` | `{"config":规范化便携配置}`；纯转换/预览，不改变 Engine |
| `encode_cli_config` | `{"config":便携配置对象,"include_secrets":false}` | `{"text":"CLI YAML"}`；默认脱敏，不改变 Engine |
| `shutdown` | 省略或 `{}` | 关闭 Engine，写出 `{"accepted":true}` 后退出宿主 |

无参方法不接受 `null`、数组或额外字段。`start` / `apply_config` 的响应表示请求被接受，
不表示信令、ICE 或映射已连通；GUI 用事件与快照确定真实状态。

`apply_config` 直接提交完整配置；核心按配置内容判定幂等或重配，不在桥接层维护修订号。
重复 `stop` 可用，随后 `start` 创建新的运行代次；停止态已有配置不自动启动。
手动“重选路径”可使用 `renominate_transports`，沿用 Android 的新数据面语义；`network_changed`
仅用于系统网络变化。

### 与 Android 对齐的配置交换

两个转换方法使用与 Android 相同的 CLI 文档字段：`server_url`、`transport`、`ice`、`turn`、
身份字段、会话期限、候选设置和有效 `provide` / `consume`。`service` / `expose` 仍为字符串端点。
生产代码只依赖核心解析器；测试直接与 `mobile.DecodeCLIConfig` / `EncodeCLIConfig` 比较规范化结果，
保持现有 mobile 源码、接口与构建输入不变。

- `decode_cli_config` 的文档允许缺少服务器/凭据，适合停止态草稿；其他格式与传输约束仍校验。
  缺少的值保留为空，校验用的临时值不会出现在返回配置中。
- 旧文件没有服务器时，GUI 预览阶段保留本机服务器；设备名为空时可沿用本机名。桥接不自动合并。
- `encode_cli_config` 的 `include_secrets` 默认 false；清除信令密码、房间密码和 TURN credential。
  只有显式 true 时输出含凭据 YAML。导入会返回文档自带凭据，用于确认后重新加密保存。
- 便携文档对象包含顶层 `server_url`；转换成运行请求时把它移入 `params.server_url`，
  剩余字段才放入 `params.config`，另加 API 版本。
- 方法不操作文件、不应用配置、不启动/停止 Engine。GUI 确认导入后先等待 `stop`，再替换本机配置，
  保持停止，与 Android 的导入流程一致。
- 完整备份、停用项、主题、凭据加密和自动恢复偏好由 GUI 配置仓库管理；不是核心运行配置的一部分。
- 导入文本、导出对象及导出 YAML 最多 128 KiB；纯转换接口接受停止态草稿，运行前仍需严格 `validate`。

### 完整运行请求

`validate`、`start`、`apply_config` 复用同一参数结构，与 mobile 的运行请求模型一致：

```json
{
  "jsonrpc": "2.0",
  "id": "start-1",
  "method": "start",
  "params": {
    "api_version": 1,
    "server_url": "wss://HOST/ws",
    "config": {
      "room": "ROOM",
      "password": "SIGNAL_PASSWORD",
      "token": "ROOM_TOKEN",
      "device_name": "desktop",
      "session_timeout": "10m",
      "transport": {"preferred": "ice", "allow_legacy": true},
      "provide": [{"id": "ssh", "service": "tcp://127.0.0.1:22"}],
      "consume": []
    }
  }
}
```

上例为便于阅读的排版；实际发送时压成一行。
`server_url` 只放在 `params`，`config` 不重复填写。
配置经 `core.ParseConfig` / `Request.Validate`，默认值、严格字段检查及传输约束与现有客户端相同。
这是完整有效配置，不是增量补丁；GUI 的停用条目、内部 ID、窗口状态等宿主字段不发给核心。

旧版请求中的 `config_revision` 仍被桥接兼容接收但会忽略；新请求无需携带。
事件序号、代次以及核心 64 位计数使用十进制字符串，避免 GUI 层数字类型精度丢失。
请求 ID 是关联标识。

### 快照与事件

`snapshot` 的 `result`：

```json
{"snapshot":{"api_version":1,"engine_state":"stopped"},"bridge_events_dropped":"0"}
```

上例只展示结构；实际 `snapshot` 包含完整 `core.Snapshot`，包括映射、对端传输、网络、
流量、RTT、代次和状态。快照不返回配置密码；GUI 自行持有与保存用户配置。

事件通知：

```json
{"jsonrpc":"2.0","method":"event","params":{"kind":"engine","sequence":"1","generation":"1","time":"2026-09-14T00:00:00Z","state":"starting"}}
```

`params` 是结构化 `core.Event`，保留其序号与代次；与 Android 一样过滤 `kind: log`
的原始 Go 日志。这种主动过滤可能使事件序号不连续，不计入桥接队列丢弃数。
事件可能与其他请求响应交错；不要按“下一行就是当前响应”处理。
按 ID 关联响应，按事件序号/代次过滤旧通知；收到重要事件后合并刷新快照，
计数采用低频采样。优先使用快照而不是拼接事件推算完整状态。

### 错误

```json
{"jsonrpc":"2.0","id":"apply-2","error":{"code":-32000,"message":"核心操作未完成","data":{"code":"network_error","message":"网络操作失败"}}}
```

| JSON-RPC code | 场景 |
| --- | --- |
| `-32700` | 非法 JSON 或 UTF-8 |
| `-32600` | 请求信封、ID、重复键、批处理或大小/深度无效 |
| `-32601` | 未支持的方法 |
| `-32602` | 参数格式、核心 API 版本或配置校验错误 |
| `-32603` | 响应超过输出上限 |
| `-32000` | 核心生命周期错误；`data` 携带核心 Fault |

普通无效请求不会结束宿主，后续完整行继续处理。解析错误不回显原始配置；
可识别的核心 Fault 沿用共享核心的脱敏消息。

## 限额、背压与生命周期

- 单请求信封最多 1 MiB、单输出消息最多 2 MiB，均不含行结束符。
  完整运行请求 `params` 及配置文档各自仍限制为 128 KiB；较大的信封只用于容纳导入文本的 JSON 转义开销。
- 桥接最多排队 1 条输入、4 条响应、64 条事件；核心自身的事件队列仍有独立限额。
- 响应优先，不因事件拥塞被丢弃。事件队列满或单事件超限时丢弃事件，累计在
  `bridge_events_dropped`；核心队列丢弃计数另见 `snapshot.events_dropped`。
- 单条输出连续 5 秒未写完时结束宿主并清理 Engine，避免 GUI 停止读取后继续占用网络。
  宿主空闲等待输入没有心跳或读超时。
- 超长输入行返回 `request_too_large` 后结束连接，不继续扫描或执行其后请求。
- `shutdown` / 输入 EOF 先关闭 Engine，再排空已生成的响应；剩余事件不保证交付。
- 输入断开、输出失败、上下文取消都会关闭 Engine 及拥有的管道。
  POSIX SIGINT / SIGTERM 走同一取消清理；Windows GUI 优先使用 `shutdown` 或关闭 stdin。
- GUI 保留独占管道句柄；退出后关闭 stdin，避免其他进程继承写端而延后 EOF。
  强制结束进程前优先等待清理；重新启动宿主意味着新的核心会话，不自动重放 `start`。

退出码：正常 EOF、`shutdown` 或取消为 0；I/O 错误、写入超时或输入超限为 1；参数用法错误为 2。
后台常驻服务模式、磁盘文件操作和配置持久化留在桌面宿主层；当前桥接提供纯文本配置转换。

## 回归验证

```bash
source scripts/build-env.sh
go test -race -count=1 ./desktop ./cmd/hole-desktop-core
go vet ./desktop ./cmd/hole-desktop-core
python3 -m unittest discover -s scripts -p 'test_build.py'
```

测试覆盖协议边界、共享配置校验、Android 配置交换契约、uint64 精度、背压、取消、EOF、关闭确认、实际子进程管道，
以及真实 Engine 对本地 WebSocket 信令的启动、重配、切网、停止和重启。
跨编译验证不等同于 Windows 桌面运行验收；完整客户端回归见 [构建指南](../docs/BUILDING.md)。
