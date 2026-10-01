# Hole PenMods 插件

当前插件版本为 **0.1.1**，`metadata.json` 与 `xmake.lua` 使用相同版本号。

这个目录构建适用于 PenMods 的 `hole_plugin` 插件。插件使用 PenMods 的标准插件生命周期，启动同目录的 `hole-desktop-core` 子进程，通过 stdin/stdout 的 JSON-RPC 协议复用 `core.Engine`。

## 目录

```text
penmods/
├── Main.qml
├── SettingsPage.qml
├── ConfigPage.qml
├── components/
├── metadata.json
├── src/HolePlugin.cpp
├── src/HolePlugin.h
├── xmake.lua
└── build.sh
```

## 构建

需要 PenMods 使用的 ARM64 Qt 和 Zig 交叉工具链：

```bash
./penmods/build.sh
```

默认使用：

```text
Qt:    /home/haiku/program/qt
cross: aarch64-linux-gnu.2.27
mode:  release
```

也可以覆盖：

```bash
QT=/path/to/qt CROSS=aarch64-linux-gnu.2.27 ./penmods/build.sh
```

脚本先调用根目录 `build.sh desktop-core --os linux --arch arm64` 构建核心，再使用 xmake
构建 Release `libhole_plugin.so`。核心使用 `CGO_ENABLED=0`、`-mod=readonly`、`-trimpath`、
`-s -w`，并通过 `scripts/build_meta.py` 写入与 CLI / Android 一致的核心版本标识。
每次打包都从当前源码构建核心并复制 QML；仅复制 QML 不会更新设备上的核心程序。
最终输出到：

```text
dist/penmods/hole_plugin/
├── Main.qml
├── SettingsPage.qml
├── ConfigPage.qml
├── components/
├── hole-desktop-core
├── hole-desktop-core.sha256
├── libhole_plugin.so
├── icon.png
└── metadata.json
```

直接构建插件本体：

```bash
cd penmods
xmake f --qt="/home/haiku/program/qt" --arch=arm64-v8a --toolchain=zigcc --cross=aarch64-linux-gnu.2.27 -m release -vD
xmake build hole_plugin
```

## 安装

将整个产物目录复制到设备：

```text
/userdisk/PenMods/plugins/hole_plugin/
```

然后重启 `YoudaoDictPen`。插件配置保存为同目录的 `config.json`，不会写入主程序配置。
其中 `config` 是传给共享核心的运行配置，`mapping_state` 只保存插件表单中的停用或尚未填写完整的映射，
`turn_state` 保存 TURN 编辑状态；后两者都不会传给核心。切换到协调服务、关闭 TURN 或仅 IPv6 后，
手动 TURN 地址、用户名、凭据及其他 TURN 设置仍保存在本地，重新打开插件并切回手动时可继续使用。
只有启用手动 TURN 的 ICE 模式才向核心传入手动凭据。旧配置没有 `turn_state` 时从 `config.turn` 读取；
旧版本已经清空并保存的内容无法自动找回，需要重新填写。`config.json` 含明文凭据，请妥善保管，不要公开分享。
升级时保留设备上的 `config.json`，同时更新核心程序、插件库和 QML 文件。运行状态中的
“核心版本”可用于核对源码版本，`hole-desktop-core.sha256` 可用于核对核心文件是否完整复制。

## 本地验证

```bash
source scripts/build-env.sh
bash -n penmods/build.sh
python3 -m unittest discover -s scripts -p 'test_build.py'
go test -race -count=1 ./desktop ./cmd/hole-desktop-core
go vet ./desktop ./cmd/hole-desktop-core
node --test penmods/tests/config.test.mjs
QT_QPA_PLATFORM=offscreen QT_QUICK_BACKEND=software qmltestrunner -input penmods/tests
./penmods/build.sh
```

脚本测试覆盖核心构建参数、源码版本更新、完整打包和构建失败时保留已有产物。
Node 测试执行 `Main.qml` 中的配置读写函数，覆盖 TURN 来源切换、仅 IPv6、页面重建、旧配置兼容及未完成草稿的保留；
宿主插件和保存接口使用替身，不代表真实设备落盘验收。
QML 交互测试需要宿主 Qt 5 的 QtQuick、QtQuick.Layouts 和 QtTest 模块，覆盖点按启停类型、
跨行排序、跨区拖动、取消、小屏幕边缘滚动、普通滑动及三种连接方式的配置兼容；不依赖 PenMods 宿主页面。
本地构建与测试不代表 PenMods 真机验收。

## 使用

连接方式与 Android 统一为“自动 / 仅 ICE / 仅 IPv6”。自动对应 `preferred=ice`、
`allow_legacy=true`；仅 ICE 对应 `preferred=ice`、`allow_legacy=false`；仅 IPv6 对应
`preferred=ipv6`、`allow_legacy=false`。已有配置仍可直接读取，无需迁移文件格式。

插件页面按 Android 的分组设置、Miuix 风格卡片、选择行和开关提供可视化配置；用户不需要编辑原始 JSON。插件内部仍将表单转换为 `desktop-core` 的核心配置对象（不包含 `server_url`），例如：

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

高级连接参数中的“本机 TURN 类型顺序”与 Android 使用相同的“尝试顺序 / 可添加”两区，
类型名称为 UDP、TCP、TLS，对应 `udp`、`tcp`、`tls`。点按可启用或停用，长按拖动可调整顺序或跨区移动；拖动期间保留原布局，
松手后一次保存，取消不修改顺序。普通滑动仍滚动页面，长按拖动到屏幕边缘时自动滚动。
“恢复默认顺序”清空自定义列表；留空采用默认顺序，未列出的类型不会尝试。
运行快照显示本机和对端顺序、当前共享轮次，以及旧端不支持时的回退提示。

页面采用与宿主 `YColors` 接近的深色、可滚动布局：首页只放连接状态、启动开关、运行快照以及打开入口；点击“设置”或“配置”会打开独立的 `YBackButtonPage`，设置页放置房间、信令、连接策略和高级 ICE/TURN 参数，配置页放置提供/使用映射。启动使用右侧开关统一控制启动和停止；连接成功后“重选路径”会请求核心为 active 路径建立新 ICE 代次，进行中重复请求会被核心忽略。核心仍在探测、加入房间或重建连接时显示进行中，只有核心收到信令服务的 `joined` 并发布 `engine=running` 后才显示连接成功。输入区域使用宿主注入的 `qmlCreateComponent("YInputPage")` 异步创建输入页，复用 `YPagePopHelper.containerItem`、`placeHolderText`、`enterText()`、`show()` 和 `inputFinished`，不在插件中复制输入页，也不回退到系统键盘。点击后输入区域至少高亮 150ms；点击处理不抢占拖动手势，因此可在输入区域直接滑动页面。

“运行状态”中的映射行显示角色、状态、协议、地址、对端、会话数和收 / 发字节，每个对端通道按字段分行显示状态、路径、地址族、本机连接类型与地址、对端连接类型与地址、中继使用方、探测阶段、延迟和线路流量。“收 / 发”按本机方向统计：`read_bytes` 是从本地端点读入并转发给对端的字节，显示为“发”；`written_bytes` 是从对端接收并写入本地端点的字节，显示为“收”。映射行统计当前运行中映射累计的应用数据，通道行的线路流量按设备线路累计并包含 ICE / QUIC 协议开销，因此大于映射合计，也不作为中继账单。`local_relay_protocol` 与 `remote_relay_protocol` 分别对应两侧实际中继接入方式；旧版 `relay_protocol` 仅作为本机字段的兼容回退。路径尚未选定时，探测阶段只显示一次，不在路径摘要中重复显示。
