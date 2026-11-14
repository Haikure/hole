# 桌面客户端与 Go 桥接

`hole/desktop` 为 Windows / Linux GUI 提供独立的 stdio 宿主，入口为
`cmd/hole-core`；Rust + Yororen UI GUI 源码位于 `desktop/gui/`。

[构建与启动](#构建与启动) · [界面操作](#桌面界面与操作) · [NAT 检测](#nat-检测) · [语音](#房间语音) · [桥接协议](#协议-v1) · [验证](#回归验证) · [项目首页](../README.md)

| 使用桌面客户端 | 开发宿主或前端 |
| --- | --- |
| 单文件 GUI，内嵌同平台 Go 核心 | JSON-RPC 2.0 + NDJSON，桥接 API 1 |
| TCP / UDP 映射、配置交换、NAT 诊断和语音 | 生命周期、事件、快照与可选二进制 PCM |
| Linux / Windows | 传输、身份、限额及恢复统一由 `core/` 实现 |

```mermaid
flowchart LR
    gui["Yororen UI 界面"] <--> client["Rust CoreClient"]
    client <-->|"stdin / stdout"| bridge["Go 桌面宿主"]
    bridge <--> engine["core.Engine<br/>ICE / QUIC / TCP / UDP"]
```

一个宿主进程持有一个 Engine；多条映射共享它。IPC 承载配置、控制、事件、快照和显式启用的语音 PCM，
TCP / UDP 映射业务字节不经过 GUI。桌面宿主不读取配置文件、不自动启动网络、不监听控制 HTTP/TCP 端口，
不依赖 gomobile 或图形库。传输、身份、限额与恢复仍由 `core/` 实现。

## 构建与启动

```bash
./build.sh desktop
./build.sh desktop --os linux --arch arm64
./build.sh desktop --os windows --arch amd64
```

`desktop` 生成 `dist/desktop/<goos>-<goarch>/hole-desktop[.exe]` 单文件客户端及 `.sha256`，
同平台 Go 核心以 gzip 压缩后内嵌，无需携带独立核心程序。构建完整 GUI 需要 Rust stable / Cargo；交叉构建需预装 Rust target
及对应 linker / sysroot。缓存统一位于根 `.cache/`。

只构建 Go stdio 核心宿主时使用 `./build.sh core --os linux --arch amd64`；产物为
`dist/core/hole-core-<goos>-<goarch>[.exe]` 及同名 `.sha256`。
核心宿主使用 `CGO_ENABLED=0`、`-mod=readonly`、`-trimpath`、`-s -w`，不依赖图形库或 CGo。
`cli`、`android`、`wear` 和 `all` 不隐式包含 `core` 或 `desktop`；需要时可显式组合。
桌面源码不属于共享 AAR 的源码摘要输入。

宿主无运行参数；`--help` 写到 stderr，版本通过 `hello` 查询。仅查询本地状态：

```bash
printf '%s\n' \
  '{"jsonrpc":"2.0","id":"1","method":"hello"}' \
  '{"jsonrpc":"2.0","id":"2","method":"snapshot"}' \
  '{"jsonrpc":"2.0","id":"3","method":"shutdown"}' \
  | ./dist/core/hole-core-linux-amd64
```

GUI 用绝对路径直接启动对应文件，不经过 shell、不从 PATH 搜索同名程序；配置凭据放在
stdin 消息中，不放到启动参数。Rust `CoreClient` 分离 stdout / stderr，并持续读取两条流。

### 内嵌核心的加载与回收

<details>
<summary>平台加载方式、退出清理与开发覆盖</summary>

`./build.sh desktop` 先构建同平台的 stripped Go 核心，再通过 Cargo 的 `embedded-core` feature
把它嵌入 GUI。构建脚本校验 ELF / PE 架构，记录未压缩大小和 SHA-256；核心文件变化会使 Cargo
重新嵌入。`dist/core/` 保留独立构建产物，分发客户端时只需复制 `dist/desktop/` 下的 GUI 文件。

- Linux：流式解压到 `memfd_create` 的匿名内存文件，校验大小和 SHA-256，再设置执行权限和写入封印，
  通过 `/proc/self/fd/…` 启动核心。不会创建普通核心临时文件；核心停止后关闭内存文件句柄。
  需要可用的 `/proc` 和允许可执行 memfd 的系统策略；禁止该操作时显示错误，不自动退回磁盘释放。
- Windows：释放到 `%LOCALAPPDATA%/hole-desktop/runtime/v1-<sha256>-<随机标识>/core.exe`。
  目录 ACL 仅向所有者和 SYSTEM 授权；写入后重新读取并校验，在核心退出前持有只读文件句柄和独占占用锁。
  每个 GUI 实例使用独立目录。正常退出等待核心结束后删除临时文件；下次启动只回收命名匹配、
  无占用锁且只含已知文件的残留目录，不跟随链接或重解析点，也不递归删除其他内容。

运行期间保留 GUI 与 Go 核心两个进程，现有控制和语音 IPC 不变。内存加载描述的是核心可执行文件，
GUI 配置仍按原方式保存；系统可能对匿名内存使用交换空间。
GUI 退出会等待控制线程完成关闭与清理。强制结束 GUI 后，核心也会沿 stdin EOF 路径退出。

开发时 `cargo build` / `cargo test` 默认不嵌入核心，沿用独立宿主查找方式。显式设置绝对路径
`HOLE_CORE` 可覆盖内嵌核心用于调试；无效的显式路径会报错。发布版默认优先使用内嵌核心，
不会自动加载同目录其他核心文件。直接开启 `embedded-core` 时必须提供绝对路径 `HOLE_EMBED_CORE`：

```bash
source scripts/build-env.sh
cd desktop/gui
HOLE_EMBED_CORE="$HOLE_ROOT/dist/core/hole-core-linux-amd64" \
  cargo test --offline --features embedded-core
```

这条命令包含 Linux 真正从 memfd 启动核心、握手和释放句柄的测试。Windows 文件回收的共用逻辑可在
Linux 测试中验证，Windows ACL / CreateProcess 仍需 Windows 系统运行验收。
Linux 单文件运行验收可用 `xvfb-run -a python3 desktop/gui/tests/embedded_core_linux.py dist/desktop/linux-amd64/hole-desktop`：
测试只复制 GUI，检查子进程来自 memfd、确认关闭及强制结束 GUI 后核心沿 EOF 退出。

</details>

## 桌面界面与操作

桌面使用 Yororen UI 0.4 与 GPUI CE 0.3。窗口默认 1240×820，最小 780×540，保留系统标题栏。
侧栏为「首页、服务、连接、语音、设置」，宽度不足 1000px 时收为图标导航。
页面随宽度切换单双栏并允许纵向滚动，卡片不设描边，采用 24px 圆角、浅色渐变和柔和阴影。
应用图标使用 `gui/ui/assets/app-icon.png`，同时用于页面标识、Linux 窗口图标和 Windows 可执行文件资源。
背景、卡片渐变、五组配色、连接状态色和浮动导航延续 Android 的设计；导航选中背景使用相同的弹簧参数，减少动态效果时立即定位。
按钮悬停与按下、开关滑动、分段选中背景、数值更新、设备详情切换、阶段展开，以及抽屉、确认框和提示的进出场均有过渡。
外观中的“减少动态效果”会让这些动效立即到达目标状态；报文流动只在实际记录变化时播放。
常用操作使用组件库内置图标，网络、服务与打洞过程使用内嵌 SVG，无需下载图标资源。

- 首页显示运行状态、设备与服务数量、实时速率、累计流量和近期活动。各页面共用侧栏的启动 / 停止入口。
- 服务按提供 / 使用分组，支持搜索、启用、编辑、排序、删除与撤销；编辑在右侧抽屉中完成。
- 连接页展示设备、实际地址、NAT、中继与 QUIC 质量，宽屏并排显示列表和详情，窄屏上下排列。
- 设置包含房间与身份、连接方式、外观、桌面行为；配置交换和备份另有页面。编辑草稿保留到保存或还原。
- 外观支持跟随系统、浅色、深色，以及与 Android 对应的五组强调色。普通卡片无描边，键盘焦点保留可见反馈。

设备的 QUIC 统计按丢包率、峰值速率、平均速率显示合计、上行、下行。
无样本与未收到对端反馈分别显示；本地丢弃、重试独立展示。平均速率按有流量的采样时长计算。
统计口径见 [协议统计说明](../docs/PROTOCOL.md#重连与状态展示)。

| 按键 / 操作 | 行为 |
| --- | --- |
| Ctrl+1～5 | 切换一级导航 |
| Tab / Shift+Tab | 遍历控件 |
| Enter / Space | 激活获得焦点的操作按钮 |
| Esc | 关闭记录详情、关闭确认框、确认放弃服务编辑，或返回首页 |
| 关闭窗口 | 确认后停止核心并退出 |

配置、凭据和运行意图仍使用原有独立文件，替换前端无需迁移配置。
“打开应用时恢复连接”只在已启用该选项且上次仍请求运行时生效，手动停止会清除恢复意图。
系统托盘和系统登录自启尚未接入。

### 平台行为与打洞过程

桌面与 PenMods 启动同一个 `hole-core` 宿主；Android 经 mobile 接口进入相同的 `core.Engine`。
界面不计算 NAT 模型、预测端口、重试时序或传输角色。
“重选路径”调用 `renominate_transports`；系统网络变化对应 `network_changed`，二者分别保留选路与网络失效语义。
外观偏好不进入运行请求，不因修改主题而重启连接。

“连接 → 查看打洞过程”读取核心的独立过程记录，包括阶段时间线、socket、双方模型与计划、端口矩阵、报文和 NAT 采样。
布局与 Android 保持一致：双端点活动卡、四项统计、回放控制和“过程 / 端口 / 报文”分段导航。
过程按阶段折叠，NAT 采样按分类与公网 IP 绘制端口曲线；端口页显示使用的网络与 socket 卡片。
报文页支持连接收发 / 模型采样切换，突出端点端口与认证结果；记录详情在右侧展开，可筛选同一 STUN 事务并复制原始记录。
支持切换连接尝试、筛选轮次 / 方向 / 报文类型、分页、暂停、逐条与连续回放，以及复制原始记录。
过程读取与普通快照分开；每次最多读取 256 条，按代次与采样轨道维护增量游标。
读取完两条轨道的当前分页后才能暂停；暂停同时冻结连接与模型采样展示，不暂停实际打洞。序号使用完整 uint64，丢弃与移出保留窗口分别显示。
矩阵最多显示 32 个 socket × 64 个目标，原始计划保留在详情中。

## NAT 检测

“连接 → NAT 检测”打开独立诊断页，总览和设置页也有入口。无需加入房间，可开始、取消、重新检测及复制结果。

| 展示 | 内容 |
| --- | --- |
| 概览 | 按窗口宽度排列 1–3 列图标信息块；类型色标与手机一致 |
| 详情 | NAT 类型解释、映射 / 过滤、公网及本机地址、端口特征、样本与时间、逐 STUN 服务记录 |
| 网络与设备详情 | 本机 / 对端 NAT 类型、可预测性及探测时间 |
| 连接设置 | 自动端口映射、分类 STUN 服务；导入导出与备份保留配置 |

Linux 默认网关读取 `/proc/net/route`，Windows 使用系统 IPv4 路由表及接口优先级；其他平台缺少适配时映射不可用，不猜测网关地址。

## 房间语音

侧栏“语音”页提供语音开关、静音、房间成员和语音线路状态。双方填写相同房间并开启语音，
启动连接后自动建立通话，无需添加服务映射。语音需要自动 / ICE 连接方式；仅 IPv6 模式下不能启用。
语音开关保存在本机配置，支持 CLI YAML、桌面备份及 Android 备份交换；旧配置默认关闭。

Rust 使用 CPAL 打开系统默认麦克风和播放设备：Linux 使用 ALSA，Windows 使用 WASAPI。
Linux 构建需 `pkg-config` 和 ALSA 开发包（如 `libasound2-dev`），运行需系统 ALSA 库及可用的默认设备。
优先使用 48 kHz，设备只支持其他采样率时在平台适配层转换为 48 kHz 单声道 PCM16。
Opus、混音、抖动缓冲、成员发现和网络传输全部复用共享 Go 核心。

停止连接、关闭语音、重配或退出会释放音频设备；静音停止本机发送但继续收听。
音频设备不可用或系统权限被拒绝时页面显示原因，恢复后点“重试音频设备”。切换系统默认设备后可点
“重新打开音频设备”。静音状态在本次 GUI 进程的重配与设备重启中保留；不作为配置导出。
桌面适配目前不提供应用内回声消除和降噪，建议使用耳机。编译、自动化和截图检查不代表真实麦克风、
Windows 设备或 Android 跨端通话验收。

## 协议 v1

<details>
<summary>展开完整桥接协议：分帧、方法、配置交换、快照与错误</summary>

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
| `core_version` | 来自根目录 `versions.json`，与 CLI / Android AAR 相同 |
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
| `start_nat_probe` | 可选 `stun_urls`、`nat_stun_urls`、`interface_allowlist` 的对象 | 异步返回 accepted；无需 `start` 或房间凭据，结果见 `snapshot.nat_diagnostic`，由 `nat_diagnostic` 事件通知 |
| `cancel_nat_probe` | 省略或 `{}` | 取消手动 NAT 检测 |
| `set_voice_audio` | `{"enabled":true}` | 运行且启用语音时返回新的十进制字符串 `stream_id`，启用下述二进制 PCM 扩展；false 停止扩展并返回 `"0"` |
| `set_voice_muted` | `{"muted":true}` | 设置核心本机语音发送静音；不停止播放 |
| `decode_cli_config` | `{"text":"YAML 或 JSON 文本"}` | `{"config":规范化便携配置}`；纯转换/预览，不改变 Engine |
| `encode_cli_config` | `{"config":便携配置对象,"include_secrets":false}` | `{"text":"CLI YAML"}`；默认脱敏，不改变 Engine |
| `shutdown` | 省略或 `{}` | 关闭 Engine，写出 `{"accepted":true}` 后退出宿主 |

无参方法不接受 `null`、数组或额外字段。`start` / `apply_config` 的响应表示请求被接受，
不表示信令、ICE 或映射已连通；GUI 用事件与快照确定真实状态。

`apply_config` 直接提交完整配置；核心按配置内容判定幂等或重配，不在桥接层维护修订号。
重复 `stop` 可用，随后 `start` 创建新的运行代次；停止态已有配置不自动启动。
手动“重选路径”可使用 `renominate_transports`，沿用 Android 的新数据面语义；`network_changed`
仅用于系统网络变化。

### 可选二进制 PCM 扩展

先通过 `hello.methods` 检查 `set_voice_audio` / `set_voice_muted`。未显式开启音频扩展时，
宿主仅输出原有 NDJSON。开启后，两个方向都在 JSON 行之间插入固定 **1937 字节** 的二进制包：

| 偏移 | 内容 |
| --- | --- |
| 0 | NUL 标记（`0x00`，不是有效 JSON 行的起始字节） |
| 1–8 | `stream_id`，无符号 64 位小端序 |
| 9–16 | 采集帧序号，无符号 64 位小端序；播放方向保留为 0 |
| 17–1936 | 960 个有符号 PCM16 小端序样本，48 kHz、单声道、20 ms |

二进制包没有 LF 结束符，PCM 内的 LF / NUL 必须原样保留。读取方在消息边界检查首字节，
遇到 NUL 后精确读满包长；否则读取一行 JSON。帧序号从 0 开始递增，丢帧保留序号间隔，
核心时间戳为序号 × 960；回退、重复、溢出和旧流标识帧被丢弃。
每次开启返回新流标识；`start` / `apply_config` / `stop` / `shutdown` 使旧流失效。
GUI 先释放旧设备，再重配并重新开启音频扩展；播放端也用流标识过滤仍在管道里的旧包。

麦克风回调仅投递有界队列，由专用线程写入管道；输入 / 播放队列最多各 4 帧。
控制响应优先于音频输出。PCM 不进入 JSON、事件、日志、配置或诊断报告；无需新增监听端口。

### 与 Android 对齐的配置交换

两个转换方法使用与 Android 相同的 CLI 文档字段：`server_url`、`transport`、`ice`、`turn`、`voice`、
身份字段、会话期限、候选设置和有效 `provide` / `consume`。`service` / `expose` 仍为字符串端点。
生产代码只依赖核心解析器；测试直接与 `mobile.DecodeCLIConfig` / `EncodeCLIConfig` 比较规范化结果，
保持现有 mobile 源码、接口与构建输入不变。

连接设置支持 Hole 中继设备名及 `relay.order` 统一顺序，接受 `hole`、`udp`、`tcp`、`tls`。
恢复默认顺序保存显式空数组；未编辑顺序时保留旧 `turn.order` 迁移语义。关闭 TURN 不关闭
Hole；没有本端设备名仍可参与对端指定的 Hole 路径。配置存储、备份和 CLI 交换保留这些字段；
开启中继服务的 CLI 配置明确报“仅 CLI 支持”。运行详情与报告显示实际中继设备、公布入口、
接入地址、两端顺序及旧协议回退状态。

固定通道在首页线路摘要、设备详情、服务传输方式、语音页和报告中显示“固定入口直连”，建连中与重试中的
通道按固定 profile / 阶段识别，状态仍取自核心。详情区分公布入口、监听方、拨号方及实际两端地址，
缺失字段省略；不显示不适用的 ICE 候选、TURN 顺序和中继凭据。路径失败后只重试固定入口。
固定监听配置界面尚未接入。

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

上例只展示结构；实际 `snapshot` 包含完整 `core.Snapshot`，其中 `core_version` 为统一维护的核心版本，`core_build_id` 为构建标识，还包括映射、
对端传输、网络、流量、RTT、代次和状态。快照不返回配置密码；GUI 自行持有与保存用户配置。

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

</details>

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

使用项目缓存与当前源码构建：

```bash
source scripts/build-env.sh
./build.sh desktop --offline
export HOLE_EMBED_CORE="$PWD/dist/core/hole-core-linux-amd64"
cd desktop/gui
cargo test --locked --release --offline --no-default-features --features embedded-core
```

Rust 测试覆盖配置交换、快照精度、音频适配、内嵌核心完整性和生命周期、配置偏好不重启连接、打洞分页与回放。
`tests/platform_contract.rs` 调用 Node.js 执行 PenMods 实际的配置序列化函数，对照桌面的自动 / ICE / IPv6 策略、
TURN、分类 STUN 的 null / 空列表与自定义列表，防止 UI 重写改变有效参数。该测试需要 Node.js。

Linux 窗口检查需要 Xvfb、X11 / Xtst、Python Pillow 和可用的 Vulkan 驱动（无 GPU 环境可使用 Mesa）：

```bash
source scripts/build-env.sh
xvfb-run -a -s "-screen 0 1600x1100x24" python3 desktop/gui/tests/ui_x11.py dist/desktop/linux-amd64/hole-desktop
xvfb-run -a python3 desktop/gui/tests/embedded_core_linux.py dist/desktop/linux-amd64/hole-desktop
go test ./desktop ./cmd/hole-core
git diff --check
```

窗口检查使用独立配置目录，截图保存在 `.cache/desktop-ui/`。
内嵌核心检查只复制 GUI，验证 memfd 执行、确认退出和 GUI 被强制结束后的核心 EOF 清理。
这些检查不代表真实 NAT 网络、Windows 窗口或跨平台语音验收；网络算法的回归仍由共享核心的集成测试覆盖。
