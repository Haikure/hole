# Hole PenMods 插件

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

脚本会先以 `CGO_ENABLED=0` 构建 ARM64 `hole-desktop-core`，然后使用 xmake 构建 `libhole_plugin.so`，最终输出到：

```text
dist/penmods/hole_plugin/
├── Main.qml
├── hole-desktop-core
├── libhole_plugin.so
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
其中 `config` 是传给共享核心的运行配置，`mapping_state` 只保存插件表单中的停用或尚未填写完整的映射，不会传给核心。

## 使用

插件页面按 Android 的分组设置、Miuix 风格卡片、选择行和开关提供可视化配置；用户不需要编辑原始 JSON。插件内部仍将表单转换为 `desktop-core` 的核心配置对象（不包含 `server_url`），例如：

```json
{
  "room": "ROOM",
  "password": "SIGNAL_PASSWORD",
  "token": "ROOM_TOKEN",
  "device_name": "pen",
  "session_timeout": "10m",
  "transport": {"preferred": "ice", "allow_legacy": true},
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

页面采用与宿主 `YColors` 接近的深色、可滚动布局：首页只放连接状态、启动开关、运行快照以及打开入口；点击“设置”或“配置”会打开独立的 `YBackButtonPage`，设置页放置房间、信令、连接策略和高级 ICE/TURN 参数，配置页放置提供/使用映射。启动使用右侧开关统一控制启动和停止；核心仍在探测、加入房间或重建连接时显示进行中，只有核心收到信令服务的 `joined` 并发布 `engine=running` 后才显示连接成功。输入区域使用宿主注入的 `qmlCreateComponent("YInputPage")` 异步创建输入页，复用 `YPagePopHelper.containerItem`、`placeHolderText`、`enterText()`、`show()` 和 `inputFinished`，不在插件中复制输入页，也不回退到系统键盘。点击后输入区域至少高亮 150ms；点击处理不抢占拖动手势，因此可在输入区域直接滑动页面。

“运行状态”中的每个对端通道按字段分行显示状态、路径、地址族、本机连接类型与地址、对端连接类型与地址、中继使用方、探测阶段、延迟和流量。`local_relay_protocol` 与 `remote_relay_protocol` 分别对应两侧实际中继接入方式；旧版 `relay_protocol` 仅作为本机字段的兼容回退。路径尚未选定时，探测阶段只显示一次，不在路径摘要中重复显示。
