# Hole PenMods 插件

`hole_plugin` 使用 PenMods 标准插件生命周期，启动同目录的 `hole-core` 子进程，通过 stdin/stdout JSON-RPC 复用 `core.Engine`。

[构建](#构建) · [安装](#安装) · [使用](#使用) · [NAT 检测](#nat-检测) · [本地验证](#本地验证) · [项目首页](../README.md)

| 页面 | 内容 |
| --- | --- |
| 主页 | 一屏连接面板、启停按钮、在线设备和可用服务计数、四个图标入口 |
| 设备 | 设备卡片、横向延迟 / 流量指标、QUIC 表格、可展开的线路和运行诊断 |
| 设置 | 房间、信令、连接策略与高级 ICE / TURN 参数 |
| 服务 | 提供 / 使用切换、可折叠服务卡片与地址 / 端口编辑 |
| NAT 检测 | 无需房间的独立检测、分组结果与 STUN 明细 |

## 使用

1. 在“设置”填写房间、信令与连接策略。
2. 在“服务”切换提供 / 使用并添加映射，与对端使用相同服务 ID。点按服务卡片展开编辑。
3. 用主页右侧电源按钮启动或停止，在“设备”中查看连接、流量和线路详情。

首页按 320×170 横屏布局，底部横向排列“设备、服务、网络、设置”。矢量图标由 Canvas 绘制，
不依赖图标字体或外部图片。设备页返回键回到首页；设置、服务、网络继续使用宿主返回页和输入页。
连接成功表示已加入房间，在线设备与可用服务数取自最新快照，停用或尚未连接的服务不会计入。

| 连接方式 | `preferred` | `allow_legacy` |
| --- | --- | --- |
| 自动 | `ice` | `true` |
| 仅 ICE | `ice` | `false` |
| 仅 IPv6 | `ipv6` | `false` |

已有配置可直接读取，无需迁移文件格式。

### 重载核心

首页右上角的“重载核心”用于替换 `hole-core` 后立即使用新文件，无需重启词典笔主程序。
在插件目录中将新文件先放到临时名称，再替换：

```bash
chmod 755 hole-core.new
mv -f hole-core.new hole-core
```

随后点击“重载核心”。插件先请求旧核心退出，2 秒内未退出则强制结束；确认旧进程结束后，
从同一路径启动新核心并重新握手。原先开启的连接会使用当前配置重新建立，原先关闭的保持关闭；
现有连接、会话和 NAT 检测会随旧核心结束，统计重新开始。重载期间按钮禁用，重复点击不会创建多个进程。
新文件缺失或没有执行权限时保留旧进程并提示；新核心启动失败、提前退出或 10 秒内未完成握手时显示错误，修正文件后可再次重载。
“运行诊断”中的核心版本与核心构建标识可用于确认更新结果，未连接时也能刷新快照。

首次使用此功能需要更新 `libhole_plugin.so` 和 `Main.qml` 并重启 `YoudaoDictPen`；以后仅替换核心时点击“重载核心”即可。

### 服务配置

插件页面使用分组设置、Miuix 风格卡片、选择行和开关提供可视化配置，无需手工编辑 JSON。

<details>
<summary>表单对应的核心配置示例</summary>

核心配置对象不包含 `server_url`：

```json
{
  "room": "ROOM",
  "password": "SIGNAL_PASSWORD",
  "token": "ROOM_TOKEN",
  "device_name": "pen",
  "session_timeout": "10m",
  "transport": {"preferred": "ice", "allow_legacy": true},
  "turn": {"mode": "worker", "order": ["tls", "udp"]},
  "provide": [],
  "consume": [
    {"id": "ssh", "expose": "127.0.0.1:2222"}
  ]
}
```

映射地址沿用核心配置格式：提供服务的 `service` 写成
`tcp://HOST:PORT` 或 `udp://HOST:PORT`，使用服务的 `expose` 写成
`HOST:PORT`；IPv6 地址使用方括号，例如 `tcp://[::1]:22` 和
`[::1]:2222`。页面内部仍把协议、地址和端口拆成独立控件，保存时会合并为
上述字符串，避免发送核心不接受的旧版嵌套对象格式。

</details>

### 中继排序

高级连接参数中的统一中继顺序与 Android 使用相同的“尝试顺序 / 可添加”两区，
类型名称为 Hole、UDP、TCP、TLS，对应 `hole`、`udp`、`tcp`、`tls`。点按可启用或停用，长按拖动可调整顺序或跨区移动；拖动期间保留原布局，
松手后一次保存，取消不修改顺序。普通滑动仍滚动页面，长按拖动到屏幕边缘时自动滚动。
“恢复默认顺序”清空自定义列表；留空采用默认顺序，未列出的类型不会尝试。
运行快照显示本机和对端顺序、当前共享轮次，以及旧端不支持时的回退提示。

高级表单在展开且页面可见时创建，字段按自然高度排列；未启用的字段不占空间。
加载器先使用卡片可用宽度测量表单，再将自然高度交给布局，避免收起后的零尺寸使展开卡在一条线。
收起时释放表单控件，编辑内容保留在页面模型中，再次展开继续显示当前值。

同房间 Hole 中继设备名独立保存，未编辑顺序时保留旧 TURN 顺序的迁移语义；TURN 关闭
仍可使用 Hole。配置原样交给共享核心，`relay.serve.enabled: true` 明确报“仅 CLI 支持”，
不会静默删除。快照和报告显示 Hole 设备、公布入口、实际接入地址与错误。

### 页面与保存

| 交互 | 行为 |
| --- | --- |
| 页面布局 | 深色面板与蓝色操作点缀；首页入口一屏可见，详情按需展开；设置、服务和网络使用独立 `YBackButtonPage` |
| 连接状态 | 探测、加入房间或应用配置时显示进行中；通过核心事件与快照同步状态，在线应用完成无需等待重新加入房间 |
| 重选路径 | 对 active 路径请求新 ICE 代次；进行中的重复请求由核心忽略 |
| 输入反馈 | 点击后至少高亮 150ms；不抢占拖动，可直接在输入区域滑动页面 |
| 服务编辑 | 地址与端口横排，新增空服务自动展开；停用保留配置 |
| 设备详情 | 按设备与传输标识复用卡片，刷新只更新数值；详情和运行诊断展开时才创建，收起后释放 |
| 刷新 | QML 工作线程解析快照，合并连续刷新并丢弃过时结果；未变化的 NAT 报告保持原控件 |
| 动效与尺寸 | 页面淡入、详情展开 / 收起及按钮按压反馈；普通操作高 32px，启停与检测按钮为 34px |

<details>
<summary>宿主输入页接入</summary>

通过宿主注入的 `qmlCreateComponent("YInputPage")` 异步创建输入页，复用 `YPagePopHelper.containerItem`、`placeHolderText`、`enterText()`、`show()` 和 `inputFinished`，不复制输入页，也不回退到系统键盘。

</details>

配置在最后一次编辑 600ms 后自动保存。启动开关保持开启时，完整且通过表单校验的配置会自动提交给共享核心 `apply_config`；尚未填完整的编辑只保存，补齐后会自动提交。核心按 Android 相同的重配逻辑处理新配置，并保留仍符合新旧配置和连接作用域的应用会话。

映射在线修改完成或配置无变化时，保存提示更新为“已保存，已应用”。插件收到配置事件后立即刷新快照，
并在运行期间每 2 秒核对状态；即使完成事件丢失，也会退出“正在应用配置”。修改前请求的旧快照不会覆盖新状态。
断线期间完成配置应用仍显示连接恢复中；请求被拒绝时保留具体错误提示。

### 运行状态与流量

| 展示 | 内容 |
| --- | --- |
| 映射行 | 角色、状态、协议、地址、对端、会话数、收 / 发字节 |
| 对端通道 | 状态、路径、地址族、两端连接类型与地址、中继使用方、探测阶段、延迟、线路流量、QUIC 统计、连接重试和丢弃报文 |
| 发 | `read_bytes`：本地端点读入并转发给对端 |
| 收 | `written_bytes`：对端接收并写入本地端点 |

映射统计本次运行累计的应用数据；通道按设备线路累计，包含 ICE / QUIC 开销，因此大于映射合计，也不作为中继账单。
`local_relay_protocol` / `remote_relay_protocol` 对应两侧实际接入方式，旧 `relay_protocol` 仅回退兼容本机字段。路径未选定时只显示一次探测阶段。

每条对端通道显示 `retry_count`（连接重试 / 路径重选请求次数）和 `dropped_datagrams`（当前活动线路在本机接收处理时丢弃的数据报数量，包含无效报文、通道不可用、重组失败和队列超限等情况）。
这两项不代表 QUIC 数据包重传次数或网络丢包率。明确上报的零值显示为 `0 次` / `0 个`，旧核心缺少字段时显示“未上报”，大计数保留完整精度；随快照刷新更新，直连、TURN / Hole 中继和固定入口均显示。

设备详情以 QUIC 表格显示丢包率、峰值速率、平均速率，合计、上行、下行并列，单位与统计时间集中标注。
上行丢包率由核心 `send_loss` 提供，表示本机→对端本次 QUIC 连接累计的判丢比例（判丢包数 / 已判定总包数）。
统计排除尚未判定的在途包、纯 ACK 包和 MTU 探测失败；新线路重新采样，无样本显示“暂无样本”，未提供显示“—”。
下行丢包率来自对端通过 QUIC 控制流回传的统计，合计按双向包数加权合并，不经过 Worker。
两端需更新核心，对端未支持或反馈过期时显示“暂无反馈”。这是当前端到端 QUIC 路径的估计值，严重乱序可能触发判丢，
TURN TCP / TLS 等可靠外层也可能掩盖底层链路丢包；本机队列丢弃保持独立。升级需要同时替换包中的 `hole-core` 和 QML。
峰值速率与平均速率以 Mbps 显示。峰值取本连接约 1 秒采样的最大使用速率，
平均按有流量的采样时长计算，空闲采样区间不计入；包含重传和控制开销，不代表线路容量，切换连接后重新统计。
详细口径见 [协议统计说明](../docs/PROTOCOL.md#重连与状态展示)。

固定通道显示“固定入口直连”及已上报的地址族，建连与重试状态取自核心；公布入口、监听方、拨号方和实际两端地址分别分行显示，缺失字段省略。固定通道隐藏 TURN 顺序等 ICE 专用信息，保留线路统计，失败提示明确仅重试固定入口。沿用现有滚动布局，固定监听配置界面尚未接入。

## NAT 检测

主页的“网络”和设置页的“检测网络”打开独立详情页，无需配置房间即可开始、取消、重新检测。
320×170 屏幕使用可滚动页面、两列图标信息块，以及可展开的映射与过滤、端口特征、网络、记录和 STUN 服务明细。
窄屏信息块自动改为单列；类型色标与手机一致，文字区分可预测趋势、证据不足及过期结果。
每项保留证据说明和未知状态；检测通过现有 `hole-core` 桥接完成，不在 QML 中实现网络算法。
包中包含 `NATPage.qml`、`components/NATPanel.qml` 和矢量图标组件 `NATIcon.qml`，升级时需同时更新核心、插件库和 QML。

高级设置支持自动 UDP 端口映射和 NAT 分类 STUN 地址，导入导出保留 null/空数组差异。运行快照显示本机/对端 NAT、可预测性及探测时间，算法由 `hole-core` 提供。插件仍使用原有标准生命周期和 Qt 资源，不增加宿主 Hook。

## 构建

需要 PenMods 使用的 ARM64 Qt 和 Zig 交叉工具链：

```bash
./penmods/build.sh
```

| 构建项 | 默认值 |
| --- | --- |
| Qt | `/home/haiku/program/qt` |
| cross | `aarch64-linux-gnu.2.27` |
| mode | `release` |

也可以覆盖：

```bash
QT=/path/to/qt CROSS=aarch64-linux-gnu.2.27 ./penmods/build.sh
```

脚本先调用根目录 `build.sh core --os linux --arch arm64` 构建核心，再使用 xmake
构建 Release `libhole_plugin.so`。核心使用 `CGO_ENABLED=0`、`-mod=readonly`、`-trimpath`、
`-s -w`，并通过 `scripts/build_meta.py` 写入与 CLI / 核心宿主 / Android AAR 一致的核心版本号和构建标识。
每次构建都会重新生成核心程序并更新 QML 运行资源；仅复制 QML 不会更新设备上的核心程序。
最终输出到：

```text
dist/penmods/hole_plugin/
├── Main.qml
├── SettingsPage.qml
├── ConfigPage.qml
├── NATPage.qml
├── components/
├── hole-core
├── hole-core.sha256
├── libhole_plugin.so
├── icon.png
└── metadata.json
```

<details>
<summary>只构建插件库与版本同步</summary>

```bash
source scripts/build-env.sh
cd penmods
xmake f --qt="/home/haiku/program/qt" --arch=arm64-v8a --toolchain=zigcc --cross=aarch64-linux-gnu.2.27 -m release -vD
xmake build hole_plugin
```

插件版本统一维护在 [versions.json](../versions.json)。修改后运行 `python3 scripts/sync_versions.py`，同步 `metadata.json`、`xmake.lua`、桌面 Cargo 元数据和锁文件。

</details>

## 安装

将整个产物目录复制到设备：

```text
/userdisk/PenMods/plugins/hole_plugin/
```

然后重启 `YoudaoDictPen`。**升级时保留设备上的 `config.json`，同时更新核心程序、插件库和 QML 文件。**

<details>
<summary>配置存储、旧配置兼容与文件校验</summary>

插件配置保存为同目录的 `config.json`，使用缩进 JSON 便于阅读，不会写入主程序配置。
其中 `config` 是传给共享核心的运行配置，`mapping_state` 只保存插件表单中的停用或尚未填写完整的映射，
`turn_state` 保存 TURN 编辑状态；后两者都不会传给核心。切换到协调服务、关闭 TURN 或仅 IPv6 后，
手动 TURN 地址、用户名、凭据及其他 TURN 设置仍保存在本地，重新打开插件并切回手动时可继续使用。
只有启用手动 TURN 的 ICE 模式才向核心传入手动凭据。旧配置没有 `turn_state` 时从 `config.turn` 读取；
旧版本已经清空并保存的内容无法自动找回，需要重新填写。`config.json` 含明文凭据，请妥善保管，不要公开分享。
升级时保留设备上的 `config.json`，同时更新核心程序、插件库和 QML 文件。运行状态中的
“核心版本”显示统一维护的核心版本号；Android APK 构建清单还记录核心构建标识。`hole-core.sha256` 可用于核对核心文件是否完整复制。

</details>

## 目录

<details>
<summary>插件源码布局</summary>

```text
penmods/
├── Main.qml
├── SettingsPage.qml
├── ConfigPage.qml
├── NATPage.qml
├── components/
├── metadata.json
├── src/HolePlugin.cpp
├── src/HolePlugin.h
├── xmake.lua
└── build.sh
```

</details>

## 本地验证

<details>
<summary>构建、配置、快照与 QML 交互检查</summary>

```bash
source scripts/build-env.sh
bash -n penmods/build.sh
python3 -m unittest discover -s scripts -p 'test_build.py'
go test -race -count=1 ./desktop ./cmd/hole-core
go vet ./desktop ./cmd/hole-core
node --test penmods/tests/*.test.mjs
bash penmods/tests/run-bridge-tests.sh
QT_QPA_PLATFORM=offscreen QT_QUICK_BACKEND=software qmltestrunner -input penmods/tests
./penmods/build.sh
```

脚本测试覆盖核心构建参数、源码版本更新、完整打包和构建失败时保留已有产物。
Node 测试执行 `Main.qml` 的 QUIC 格式化与配置反馈函数，检查统计口径、缺失值和保存状态。
桥接回归测试需要本机 C++ 编译器、Qt 5 Core / Test 开发包和 moc，覆盖无重连应用、事件丢失、旧快照、断线及请求拒绝。
QML 交互测试需要 Qt 5 的 QtQuick、QtQuick.Layouts 和 QtTest 模块，覆盖首页一屏尺寸、启停、重载和导航、
服务编辑与折叠、高级设置展开与重开、窄屏与连接模式切换、设备卡片和 QUIC 单元格复用、
详情按需创建与释放，以及 NAT 检测操作和时间格式。
测试不依赖宿主页面或截图脚本，本地检查不代表 PenMods 真机验收。

</details>
