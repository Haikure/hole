# Windows / Linux Rust + Slint GUI 实施计划

## 目标与当前边界

目标是为现有 hole 核心增加 **Rust + Slint** 桌面客户端，
采用中性现代桌面风格，吸收 Fluent 2 / GNOME Adwaita 的层级、间距和交互习惯，
不直接复刻 Android Material / Miuix 视觉，保留传统桌面的键盘、窗口和托盘体验。
**首个完整桌面版本以 Android 手机端功能对齐为验收门槛，不是只有总开关和几个映射字段的精简客户端；
视觉系统按桌面场景单独设计。**

当前已有可独立构建的 [Go stdio 桥接](../desktop/README.md)，还没有 Slint 工程或 GUI 交付包。
本文件是后续实施路线，不表示界面、Windows 运行、托盘或性能已经通过验收。

### 单文件交付与运行时进程边界

发布版可以把 `hole-desktop-core` 二进制作为资源嵌入 GUI 可执行文件，形成用户侧的单文件下载包。
启动时 GUI 仍需将内置字节校验后释放到用户可写的版本化运行时临时目录，再启动 Go 宿主；正常关闭时等待宿主
退出并删除该目录，异常退出留下的旧目录由下一次启动按版本、哈希和进程状态清理。因此跨 Windows / Linux
的通用实现仍是两个运行进程，运行期间通常也会存在 GUI 文件和释放后的宿主文件。嵌入减少的是分发文件数量，
不改变 `GUI → stdio → desktop → core` 的隔离边界。

Windows 的通用 `CreateProcess` 路径需要先释放 Go PE；Linux 的 `memfd_create` 可以减少临时文件，
但不作为跨平台默认路径。GUI 退出流程先发送 `shutdown`，等待宿主退出，关闭管道和进程句柄后删除运行时文件；
Windows 使用独占运行时目录，避免删除仍被子进程占用的文件。真正单进程需要把 Go 核心编译为 C ABI 静态库并由
Rust 调用，需引入 CGO、包装层和新的跨语言生命周期测试，暂不作为首版方案。

约束：

- `core/` 继续唯一实现 ICE / QUIC、身份认证、TCP / UDP 会话、重连和资源限额。
- Slint UI 不直接调用网络协议、管理 socket 或解析控制管道；由 Rust `CoreClient` 提供稳定接口。
- CLI / Android / Wear 的现有入口、依赖与签名策略保持独立。
- 暂不实现新的 NAT 预测打洞、TUN/VPN、Windows Service 或 Linux 守护服务。
- 不新增仓库许可证，不生成或替换签名密钥，不自动安装、部署或启用开机启动。

## Android 功能对齐基线

以当前 `android/app` 实际源码为基线，而不是仅参考首页截图。
主要核对入口：`ui/HomeScreen.kt`、`ui/EditScreens.kt`、`ui/SettingsScreen.kt`、
`ui/TransportSettingsScreen.kt`、`ui/RuntimeDetailsScreen.kt`、`ui/ConfigTransferScreen.kt`、
`ui/ThemeSection.kt`、`config/ConfigExchange.kt`、`EngineService.kt`、`RunStateStore.kt`、
`Diagnostics.kt`，以及 `android/corebridge/.../SnapshotSampler.kt`。

下表是待实现 GUI 的验收清单；“现有桥接”表示底层接口已具备，不表示界面已完成。

| Android 已有能力 | 桌面端对齐要求 | 现有桥接 / 待建宿主职责 | 阶段 |
| --- | --- | --- | --- |
| 总连接开关、保存/请求运行/真正连通分离 | 相同状态语义；首次启动关闭，手动停止优先 | `start`、`stop`、`snapshot`；GUI 保存运行意图 | M1–M2 |
| 提供/访问服务、TCP/UDP、多映射 | 新增、编辑、删除/撤销、列表顺序；服务地址支持域名/IPv4/IPv6，监听地址为字面 IP | 核心校验；GUI CRUD、稳定条目 ID | M2–M3 |
| 条目独立开关和停用配置保留 | 停用不丢字段，只把启用项发给核心 | GUI 配置仓库 → 完整有效配置 | M2 |
| 房间、设备、服务器与凭据设置 | 信令密码、房间密码、设备名、WSS 地址完整支持；草稿和未保存退出确认 | `validate` / `apply_config`；GUI 草稿与系统凭据库 | M2 |
| 自动、仅 ICE、仅 IPv6 | 与 Android 完全相同的配置映射和兼容语义 | `transport` 原样通过共享配置接口 | M2–M3 |
| STUN 自定义、清空及恢复默认值 | 多 URL、空数组仅本地候选、恢复 Cloudflare 默认项 | `ice.stun_urls`，不另造探测逻辑 | M2–M3 |
| TURN 协调服务/手动/关闭 | URL、用户名、凭据、TTL；UDP/TCP/TLS 路径与当前核心一致 | `turn`；手动凭据由 GUI 系统加密存储 | M2–M3 |
| 高级连接参数 | 直连/收集/连通期限、重试间隔、网卡白名单、仅中继、显式本地 WS 测试 | `ice` / `transport` 全字段 | M2–M3 |
| 会话期限与 IPv6 高级项 | `session_timeout`、候选网卡/地址；ICE 与手动 IPv6 候选冲突同样校验 | 共享配置解析与校验 | M2–M3 |
| 运行中重配、切网恢复 | 单独改映射不模拟 Stop/Start；按核心保留有效应用会话 | `apply_config` / `network_changed` | M1–M2 |
| 连接详情中的手动重新连接 | 有运行意图时触发路径重建，不清空配置或无条件销毁业务会话 | `network_changed`，GUI 显示“重新连接”操作 | M1–M3 |
| 设备线路与映射详情 | 直连/中继、地址族、选中地址、候选数、通道、RTT、流量、TCP 重放、UDP 丢弃和错误 | 完整 `core.Snapshot`；GUI 列表/详情模型 | M2–M3 |
| TURN 真实接入信息、授权和凭据剩余时间 | 按实际选中线路展示；对端未上报的协议明确显示“未上报”，不按端口猜测 | `peer_transports` 字段 | M2–M3 |
| CLI YAML/JSON 导入和 YAML 导出 | Android/CLI/桌面互通，默认不导出凭据；含凭据须显式勾选 | 已有 `decode_cli_config` / `encode_cli_config`；GUI 文件选择和确认 | M2 |
| 导入预览、确认后停止再替换 | 预览不改变当前运行；确认先清除运行意图并等待停止，保存后保持停止 | 纯转换接口 + `stop`，GUI 原子保存 | M2 |
| 旧 CLI 文档兼容 | 缺少服务器时保留本机服务器；空设备名沿用本机名；不把校验占位值写入配置 | 转换保留空值；GUI 合并，新增条目生成独立 ID | M2 |
| 完整备份/恢复 | 包含停用项、顺序、外观和恢复选项；支持读取 Android v1/v2 备份的可移植字段 | GUI 版本化备份适配，见下方规则 | M2 |
| 系统加密保存凭据 | Windows / Linux 使用各自凭据机制，备份不带设备绑定密文 | GUI 平台存储，不复制 Android Keystore | M2–M4 |
| Android 主题、跟随系统/浅色/深色 | 桌面采用独立的中性现代桌面风格，不复刻 Material / Miuix；同一主题支持三种明暗模式与系统配色 | Slint 桌面设计系统；不实现风格选择器 | M3 |
| 结构化连接报告与脱敏导出 | 报告覆盖 Android 当前诊断字段，加上桌面 OS/GUI/桥接版本；报告不含原始 Go 日志 | `snapshot` + GUI 报告格式化和脱敏 | M2–M3 |
| 前台/后台/停止态差异采样 | 先对齐 1500ms / 15000ms / 无周期采样，事件触发最小间隔 100ms | Rust/Slint 调度；不修改协议保活 | M4 |
| 前台通知中的状态和停止 | 桌面托盘状态、打开详情和停止菜单；隐藏窗口继续运行 | GUI 常驻进程及托盘，非后台系统服务 | M4 |
| 运行意图、系统恢复与开机恢复 | 持久化意图；只有用户开启登录后恢复且上次仍请求运行时恢复；手动停止后保持停止 | GUI 平台自动启动与恢复协调 | M4 |
| 本机网络与后台状态 | 展示真实网卡/地址/DNS等可获得信息，后台页面说明桌面托盘、登录恢复和系统限制 | 桌面平台适配；缺失字段显示未提供，而非推断离线 | M4 |

平台对应关系：Android 前台服务/通知对应桌面窗口隐藏后的 Slint 宿主与托盘；
Android Keystore 对应系统凭据库；Android 电池优化、WakeLock、Network handle 和壁纸 Monet
属于平台特有能力，不生硬复刻。桌面实现等价用户能力，平台不支持的状态清晰标明。
Android 的选定 Network 绑定机制不直接移植；桌面仍通过共享 `Platform` 边界实现平台差异。

当前默认桌面 Platform 的部分 Network 元数据可能为空；后续在 `desktop` 平台适配或 Rust 平台信息层补足，
不伪造 Android handle，也不把默认 `false` 当作真实的断网结论。

## 配置互通与行为约定

- UI 的“自动”转换为 `preferred: ice, allow_legacy: true`；“仅 ICE”为 `ice, false`；
  “仅 IPv6”为 `ipv6, false`。`allow_legacy` 是与旧对端协商兼容，不是 ICE 失败后降级。
- CLI 文件只包含有效项和 `server_url`。导入/导出转换方法不读写磁盘、不启动网络，
  不自动改动当前 Engine；Rust GUI 宿主负责文件操作、预览与用户确认。
- 导入确认按 Android 顺序执行：取消旧待启动意图 → 清除持久化运行意图 → 等待 `stop`
  → 原子保存新配置并用本机系统密钥加密导入的凭据 → 保持停止。取消或格式检查失败保留旧配置。
- 便携文件上限 128 KiB。含凭据的导入结果和显式导出结果属于敏感数据，退出预览后及时释放，
  不进入 GUI 调试日志、事件列表、诊断包或剪贴板历史。
- 完整桌面备份采用独立、版本化格式，保留通用字段及桌面专属设置，不把桌面附加字段伪装成
  Android 可直接消费的备份；Android v1/v2 导入支持其条目、主题、连接和恢复偏好，
  删除设备密文，重新进行草稿校验。导入的 Android 主题只保留有效的明暗模式及系统配色偏好，
  不把 Material / Miuix 视觉或主题选择器带入桌面。反向通用迁移使用 CLI YAML。
- 配置替换不静默启用 OS 自动启动任务；备份中的恢复偏好可预览，系统集成启用仍由用户确认。
- 对齐测试使用同一组 CLI/Android 便携配置样本，比较规范化字段、启用项、迁移和脱敏结果；
  Go 两端 CLI 配置转换现已有交叉契约测试，后续 Rust GUI 备份适配补齐对应样本。

## 目录与依赖方向

```text
cmd/hole-desktop-core/       已有：独立 Go 宿主入口
desktop/*.go                已有：stdio 协议与核心适配
desktop/gui/                待建：Rust + Slint 工程
  Cargo.toml                Rust 依赖与目标配置
  build.rs                  Slint 编译期 UI 生成
  src/
    main.rs                 Slint 运行时与应用生命周期
    core_client.rs          子进程、分帧、请求关联与进程生命周期
    application_controller.rs 用户意图与完整配置应用管理
    models/                 映射、对端、结构化事件等 UI 模型
    platform/               平台凭据、托盘与系统集成
    storage/                配置、备份与运行意图持久化
  ui/
    app.slint               应用窗口与页面路由
    theme/                  色彩、字体、间距、圆角与动画 token
    controls/               中性现代桌面风格 Slint 组件
    pages/                  总览、映射、设备/连接详情、诊断、设置、配置交换
  tests/                    Rust 单元测试、协议假宿主与 GUI 集成测试
```

依赖方向固定为 `GUI → stdio → desktop → core`，不让 `core` 依赖 Slint、desktop 或 mobile。
GUI 使用 Rust stable、Cargo 和 Slint；具体 Rust toolchain、Slint 版本及 Windows/Linux 图形后端
在建立工程时锁定，以目标 Windows / Linux 平台上的实际工具链验证为准。

## M1：最小进程通信与生命周期

### 工作

1. 建立 Rust + Slint 工程、最小窗口和 Rust `CoreClient`，只展示连接状态与启动/停止按钮。
2. `std::process::Command` 用应用包内的绝对路径启动匹配架构的宿主；不经过 shell，不搜索 PATH。
   Windows 配置无额外控制台窗口的创建方式，同时保留重定向的标准流。
3. 持续分别读取 stdout / stderr；stdout 按 LF 分帧，处理拆包、粘包、CRLF 和输出上限。
4. 先请求 `hello`，核对 `bridge_version`、`api_version` 及方法支持，再允许提交配置。
5. 为请求分配字符串 ID，维护有界待响应表与超时；不把超时操作当作已经回滚。
   不自动重放 `start` / `apply_config`；先查询快照或显式重新启动宿主。
6. 对 `event` 通知按核心代次/序号过滤，合并调度 `snapshot`，通过 Slint event loop 更新 UI 模型。
7. 用户退出时请求 `shutdown` 并等待确认和进程退出；超时关闭 stdin，再执行有界终止策略。
   “关闭窗口隐藏到托盘”与“退出并停止映射”分别处理。
8. 宿主异常退出时清空旧在途请求、停止采样、标明转发已中断；重启后重新握手、重新查询状态。
   GUI 持有的配置可供再次启动；后续恢复协调仅依据持久化用户意图进行一次明确的新启动，
   与盲重放旧请求区分，不建立无限崩溃重启循环；旧应用 socket 不恢复。

### 验收

- 无配置启动只创建停止态宿主，不开始网络连接。
- 半行、多行、事件先于响应、错误 JSON、超长输出、无响应和异常退出均有测试。
- 正常退出、GUI 崩溃和管道断开不遗留持续运行的宿主。
- Windows 与 Linux 分别实际运行测试；编译成功单独记录，不替代运行验收。

## M2：状态模型与配置流

### 工作

- `EngineState`：运行状态、信令、网络与错误，并以 Slint 属性暴露给 UI。
- `MappingModel`：provide / consume、服务 ID、协议、端点、状态、会话和流量，使用 Slint `Model` 接口。
- `PeerModel`：连接对端、直连/中继、地址族、实际 relay 协议、RTT 和切换状态。
- `EventModel`：有界结构化状态记录、过滤与复制；与 Android 一样不收集原始 Go 日志。
- 连接报告：按 `Diagnostics.kt` 对齐状态、版本、线路、会话统计和错误；删除 URL 查询参数，
  脱敏信令密码、房间密码、TURN 凭据及 URL 编码形式，报告大小限制为 128 KiB。
- 草稿配置与生效配置分离；每次提交完整配置，由核心按内容幂等或重配。
- 复用桥接 `validate` 校验格式与约束；`accepted` 与“信令已加入/映射已就绪”明确区分。
- uint64 在 Rust 中使用 `u64` 保存，跨桥接和 Slint 属性边界使用十进制字符串；不转换成有精度风险的 UI 数字类型做比较。
- 用户条目 ID、停用项、窗口状态等留在 GUI 配置中，只把有效条目映射成核心配置。
- 保存普通配置与系统凭据分离：评估 Windows Credential Manager / DPAPI 与 Linux Secret Service，
  用户是否保存密码应明确可控，日志和诊断包不包含这些凭据。
- CLI YAML / JSON 导入、YAML 导出使用现有 `decode_cli_config` / `encode_cli_config`，
  不在 Slint/Rust GUI 复制 YAML 与端点校验。转换支持缺少凭据的停止态草稿，运行前再调用严格 `validate`。
- 完整备份/恢复、Android 便携备份适配与导入预览流程属于本阶段必做项，不留到完整版本之后。

### 验收

- 配置提交不依赖 JavaScript 数字精度；无效配置被拒绝且草稿保留。
- 删除映射、切换网络、配置作用域变化按核心状态呈现，不由 GUI 推测会话恢复。
- 丢弃事件计数增加或序号出现缺口后，可通过快照恢复权威状态。
- 密码字段、系统凭据访问失败与脱敏导出有独立测试。

## M3：中性现代桌面主题组件与页面

采用独立的桌面设计系统，不建立 Material / Miuix 或其他多风格主题切换框架，也不直接复用 Android Compose 组件。
视觉上借鉴 Fluent 2 的状态表达和 GNOME Adwaita 的桌面密度，但不绑定任何一个平台工具包；
基于 Slint 的声明式组件和原生输入能力保留输入、焦点、键盘与可访问性行为，再定制跨平台外观。

### 主题组件

- 统一浅色/深色调色板、系统强调色、字体层级、8px 间距栅格、6–10px 圆角与有限阴影 token。
- 同一桌面主题支持跟随系统、浅色、深色；优先使用系统强调色，系统未提供时回落默认蓝色调色板，
  不把桌面系统配色宣称等同于 Android 壁纸 Monet。
- 页面采用侧边栏 + 内容区的桌面布局；设置页保持紧凑分组，卡片只用于状态概览和明确的功能区，不把移动端大卡片铺满窗口。
- 组件：分组设置、按钮、开关、输入框、下拉菜单、状态标签、对话框、提示条、表格和可虚拟化列表，统一封装为 Slint 自定义组件。
- 标题栏、菜单、文件选择、托盘和快捷键优先遵循目标平台行为；应用内容保持统一视觉，平台差异放在 Rust `platform/` 适配层。
- 明确 hover、pressed、disabled、focus、error 状态；支持减少动画。
- 动效优先使用透明度与位移；实时模糊、大面积离屏效果和常驻动画逐项测量后采用。
- 系统标题栏作为初始实现；自绘标题栏在验证拖动、缩放、最大化、快捷键及多屏行为后再引入。

### 页面

| 页面 | 内容 |
| --- | --- |
| 总览 | 主开关、引擎/信令状态、网络、活跃映射、主要故障 |
| 映射 | 提供/消费列表、启停草稿、创建编辑、协议与端点校验 |
| 设备 | 对端及共享传输、实际路径、RTT、流量和连接阶段 |
| 连接详情/诊断 | 运行时间、会话/线路详情、手动重新连接、结构化事件和脱敏连接报告 |
| 设置 | 房间和服务器、身份凭据、完整 ICE/TURN 高级项、桌面主题明暗/配色、托盘和恢复行为 |
| 配置交换 | CLI 导入/导出、完整备份/恢复、包含凭据开关、导入预览与停止后替换 |

### 验收

- 中性现代桌面主题的跟随系统/浅色/深色模式、中文/英文长文本、125% / 150% / 200% 缩放及多屏切换无裁切。
- 页面中没有 Android Material / Miuix 视觉复刻、主题风格切换入口或多风格兼容分支；Android 备份中的主题选项不改变此约束。
- 键盘可完成配置、启动和停止；输入法、焦点顺序与屏幕阅读器名称可用。
- 长列表使用 Slint 虚拟化列表模型，列表更新按行差异进行，不每次快照整体重建。

## M4：性能与系统集成

- 初始采样与 Android `SnapshotSampler` 对齐：运行且可见时 1500ms、后台运行时 15000ms，
  停止态只由事件/命令唤醒；事件密集时最短读取间隔 100ms，采用合并而非持续延后刷新的去抖。
  隐藏时停止无意义动画；这些策略不改变核心保活与重连行为。
- 托盘负责打开窗口、显示当前状态和明确退出；Linux 无托盘环境保留完整窗口操作路径。
- 开机/登录后恢复为显式选项，默认关闭，仅恢复仍为“请求运行”的配置；手动停止、确认配置导入、
  “退出并停止”先同步清除该意图。区分窗口隐藏、系统会话结束与用户停止，记录恢复失败原因。
  升级后重启按有效运行意图恢复，不跨进程承诺 TCP socket 续接。
- 日志、在途请求、IPC 接收缓存、历史流量采样均设上限，长期运行不随时间持续增长。
- 统计整个 GUI + Go 宿主进程树，在空闲、窗口隐藏、连接、持续传输、重连与多映射下测量
  RSS/PSS 或 Windows working set/private bytes、CPU、GPU 和句柄数。
- 在固定硬件、相同映射数量及相同负载下形成基线，再制定内存和 CPU 门槛；不预设未经测试的 MB 数字。

## M5：打包与完整回归

- 新 GUI 构建入口必须显式选择目标；不要让现有 `cli`、`android`、`wear`、`all` 意外依赖 Rust/Cargo/Slint。
- Go 宿主继续通过根 `build.sh desktop-core` 构建；Cargo registry、target 和 Slint 构建缓存统一放在根 `.cache/`。
- 桌面目录包包含 Slint GUI 可执行文件、必要资源和匹配架构的 Go 宿主，后者使用固定文件名定位；
  单文件发布包则把 Go 宿主作为资源嵌入 GUI，并在启动时释放到版本化运行时临时目录，退出后回收，
  启动时清理上次异常退出留下的过期目录。
  新交付进入独立 `dist/desktop/`，保留原 CLI/APK 留存包。
- 核心版本沿用 `scripts/build_meta.py`；桌面包另记录 GUI、桥接协议、Rust/Slint 版本及文件校验和。
- 评估 Windows 运行库、Slint 图形后端、Linux X11/Wayland 与最低发行版依赖；签名仅使用维护者提供的密钥。
- 发布前核对 Slint crates、渲染后端及分发方式对应的许可要求，仓库许可证由维护者单独决定。
- 分开记录 Go/Worker 回归、Rust 单元测试、Slint UI/集成测试、Release 打包和真实系统验收结果。
- 功能对齐矩阵逐项通过才标为“完整桌面版”；M1 的通信窗口只用于开发验证。
  至少完成 Android ↔ 桌面 TCP/UDP 双向、多映射、默认直连/中继、切网恢复和配置文件互通测试。

## 不影响现有客户端的检查点

1. CLI 主入口及日志行为不依赖 `desktop`；Android 只绑定 `hole/mobile` 和 `hole/core`。
2. 根 Go 与 Android 绑定依赖图不为 GUI 引入 Rust、Slint、CGo 或额外 Go 依赖。
3. 只改桌面源码时共享核心摘要不变；改核心行为时才按现有规则重建 AAR。
4. 每阶段运行现有 Go/Worker/构建脚本回归；触及 Android 源码或构建配置时执行对应 Release、lint、JVM 验证。
5. 桌面功能逐步启用，未完成的 GUI 不被描述为已交付客户端。
