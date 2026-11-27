# Kotlin 桌面客户端

Windows / Linux 桌面客户端，使用 Kotlin Compose 界面和共享 Go 核心，支持 TCP / UDP 映射、房间语音、配置交换及 NAT 诊断。
主要页面和配置与 [Android 客户端](../../android/README.md) 共用。

[安装与使用](#安装与使用) · [配置与数据](#配置与数据) · [语音与桌面行为](#语音与桌面行为) · [从源码构建](#从源码构建) · [项目首页](../../README.md)

## 安装与使用

| 平台 | 安装包 | 使用方式 |
| --- | --- | --- |
| Windows | `Hole-<version>.exe` | 运行安装程序后，从开始菜单或桌面快捷方式打开 |
| Linux | `Hole-<version>-<arch>.AppImage` | 赋予执行权限后运行 |

安装包自带 Java 运行时。Linux 仍需系统图形、字体和音频支持。

```bash
chmod +x Hole-*.AppImage
./Hole-<version>-<arch>.AppImage
```

1. 在设置中填写服务器、房间、凭据和唯一设备名。
2. 在服务页添加提供或使用服务，两端使用相同服务 ID。
3. 从首页启动，在连接详情查看实际线路、本地访问端口和错误。

连接页提供 NAT 检测、打洞过程、筛选、暂停、回放和记录导出。
窗口较宽时使用侧栏，窄窗口使用底部导航；支持 Tab / Shift+Tab、Ctrl+1～5 和 Esc。
外观支持五种强调色及浅色、深色、跟随系统，可在设置中减少页面转场动画。

## 配置与数据

默认配置目录：

| 平台 | 路径 |
| --- | --- |
| Windows | `%APPDATA%/hole-desktop` |
| Linux | `~/.config/hole-desktop`，设置 `XDG_CONFIG_HOME` 时改用其下的 `hole-desktop` |

可用 `HOLE_DESKTOP_CONFIG_DIR` 指定独立目录。同一目录只允许一个实例写入。
配置、凭据和运行意图保存在受文件权限保护的 `compose-state.json` 中。

首次运行可读取旧 Rust 客户端的 `config.json`、`secrets.json` 和 `run-state.json`，保留原文件。
两种客户端后续分别保存配置，避免同时以相同设备身份连接。

支持 CLI YAML / JSON、Android 备份及旧桌面备份。导入需确认并停止当前连接；导出可选择是否包含凭据。
Minecraft 发布标识、世界信息和 `voice.group` 在导入、本机保存、备份及导出间保留，并传递给共享核心。
共享配置也保留核心转换结果中的 `ice.punch` 参数，未限制网卡时使用空数组。
配置文件损坏或版本不支持时会显示错误并保留原文。

## 语音与桌面行为

语音使用系统默认音频设备，需要支持 48 kHz、单声道 PCM16。
设备不可用时显示原因，可在“桌面与音频”重新打开；应用内暂不提供回声消除、降噪或采样率转换。
核心尚未就绪或重建期间会丢弃当前采集帧，保留音频设备和静音选择，核心恢复后继续发送。
保持语音启用的配置更新不会重开设备；关闭语音或停止连接时才释放设备。

关闭窗口会停止核心和音频后退出。“打开应用时恢复连接”需手动启用，手动停止会清除恢复意图。
当前不提供系统托盘、系统登录自启或后台自动更新。Windows 新版安装包可覆盖升级并保留配置。

## 从源码构建

需要 Go、Bash、Python 3、完整 JDK 21+（支持 JDK 25，包含 JNI 头文件及 `jmods`）和本机 C 编译器。
Linux 需 GCC 与 AppImage 工具；Windows 需 Git Bash、MinGW-w64 GCC 和 WiX 3。
版本及环境配置见 [构建指南](../../docs/BUILDING.md)。

```bash
# Linux 首次准备 AppImage 工具
bash desktop/scripts/setup-appimage.sh
./build.sh desktop
```

可用 `HOLE_DESKTOP_JAVA_HOME` 指定 JDK。构建仅支持当前系统和架构，自动完成原生库、Kotlin Release 和安装包打包。
产物位于 `dist/desktop/<os>-<arch>/`，缓存位于根 `.cache/`。

开发运行使用 `bash desktop/scripts/gradle.sh run`。
旧 Rust 客户端使用独立的 `./build.sh desktop-rust`，说明见 [旧桌面与 stdio 桥接](../README.md)。
