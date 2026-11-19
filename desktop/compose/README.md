# Kotlin 桌面客户端

桌面使用 Kotlin/JVM + Compose Multiplatform，直接编译 `android/app` 中的平台无关页面、主题、组件和动画，
以及 `android/corebridge` 的快照模型与采样器。Android / Wear 继续使用原来的应用入口和 gomobile AAR。

```text
同一份 Compose 页面、组件、动画、配置和快照模型
  Android / Wear → Android corebridge → gomobile JNI → mobile → core
  桌面 JVM      → DesktopCoreClient  → 桌面 JNI 库  → mobile → core
```

桌面原生库由 Go `-buildmode=c-shared -tags desktopjni` 生成，C 层只处理 JNI 参数和内存所有权。
不启动 `hole-core` 子进程，不使用 stdio 或本地控制端口；TCP / UDP 转发、身份、限额、Opus 和混音继续在共享 Go 核心。
引擎使用整数句柄管理，UTF-8 配置 / 快照经过版本化 JSON，PCM 使用字节缓冲区。销毁引擎不会卸载进程内的 Go 运行时。
普通快照复用 Android 的前台 / 后台采样间隔；打洞记录独立增量读取。桌面通过网卡与地址变化轮询通知共享核心。

## 交付方式

| 平台 | 格式 | 行为 |
| --- | --- | --- |
| Windows | 官方 `jpackage` exe 安装包 | 每用户安装，自带裁剪 Java 运行时与 Go DLL；运行不再解压整套文件，退出保留安装文件 |
| Linux | AppImage | 封装 Release 应用目录、裁剪 Java 运行时与 Go `.so`；正常由 AppImage 挂载后运行，不额外使用 memfd 加载核心 |

Windows 固定 `upgradeUuid`，版本号来自根 `versions.json` 的 `frontends.desktop_gui.version`。
新版本安装包支持升级已安装的同一应用；不提供自动下载更新。包中启动程序不能脱离应用目录单独复制使用。
Linux AppImage 仍依赖宿主图形、字体、音频和兼容的 libc，首次运行可能需赋予执行权限。
当前发布工作流使用带 `jmods` 的 Zulu JDK 25，配置 Linux AMD64 / ARM64、Windows AMD64；Windows ARM64 的本机构建需要自行提供匹配 JDK / C 编译器，未配置 CI runner。

新桌面默认入口为 `./build.sh desktop`，旧 Rust 入口为 `./build.sh desktop-rust`，旧产物保存在 `dist/desktop-rust/`。
普通 `all` 继续只包含 CLI、Android 和 Wear。`desktop` 不再隐式构建独立 stdio 宿主；PenMods 仍使用原有 `core` 目标。

## 工具链与构建

本机提前安装：Go 1.27.4、完整 JDK 21+（支持已有 JDK 25，含 JNI 头文件、javac、jlink、jpackage、`jmods/java.base.jmod`）、Bash、Python 3、平台 C 编译器。
Gradle Wrapper 复用 `android/gradlew`，桌面 Gradle 项目独立，不要求 Android SDK / NDK / 签名。
Kotlin 2.4.0、Compose Multiplatform 1.12.2、Material 3 1.12.0-alpha03。
桌面 Material 3 与 Android 发布节奏不同；共享组件和动画源码一致，依赖组合仍需本机构建验证。

`./build.sh desktop` 自动完成 JNI、Kotlin、Release 裁剪和对应平台打包，无需先手工构建原生库。
JDK 选择顺序：`HOLE_DESKTOP_JAVA_HOME` → 合格的 `JAVA_HOME` → `JDK25_HOME` / `JDK21_HOME` → PATH 和系统常见安装目录。
显式桌面路径无效时报错；当前 `JAVA_HOME` 为 Android JDK 17 时会继续寻找已安装的桌面 JDK。
JNI 编译、Gradle 与安装包运行时使用同一 JDK；不再要求系统精确安装 JDK 21，字节码目标保留为 JVM 21。
Release ProGuard 固定为支持 Kotlin 2.4 的 7.10.0。裁剪需要 `jmods`；部分 JDK 25 发行包（如 Temurin）默认不包含它，
需自行补齐同版本、同平台的 JMODs 或选择包含它的 JDK（如 Zulu）。选择器会跳过缺少模块文件的 JDK，显式指定时直接报错。
桌面 JDK 的切换仅在子进程内，不改变 Android 的 JDK 17 配置。

- Windows：Git Bash、MinGW-w64 GCC（`CC` 可显式指定），以及 jpackage 所需 WiX 3 工具。
- Linux：本机架构的系统 GCC 即可，无需 Android NDK 编译器；`CC` 未指定时由 Go 使用默认 C 编译器。
  运行需要 X11 / Wayland 桌面、字体与 Java Sound 可用的系统音频设备；安装 `appimagetool`，
  并通过 `HOLE_APPIMAGE_RUNTIME` 指定匹配架构的本地 type2-runtime。
- 安装包仅在对应系统、架构上生成，`--os` / `--arch` 与宿主不符会报错；不安装或下载 SDK、NDK、JDK、C 编译器及 AppImage 工具。
- 依赖和构建缓存使用根 `.cache/`，项目生成物位于已忽略的 `desktop/compose/build/`，交付进入 `dist/desktop/<os>-<arch>/`。

Linux 示例，路径替换为已安装工具；JDK 25 位于系统常见目录时无需设置 `JAVA_HOME`：

```bash
export HOLE_APPIMAGE_RUNTIME=/path/to/runtime-x86_64
./build.sh desktop
chmod +x dist/desktop/linux-amd64/*.AppImage
./dist/desktop/linux-amd64/Hole-0.1.6-x86_64.AppImage
```

Windows 在 Git Bash 中：

```bash
export HOLE_DESKTOP_JAVA_HOME='C:/path/to/jdk-25'
export CC='C:/msys64/ucrt64/bin/gcc.exe'
export PATH='/c/msys64/ucrt64/bin:/c/Program Files (x86)/WiX Toolset v3.14/bin':"$PATH"
./build.sh desktop --os windows --arch amd64
```

Windows exe 安装包在 `dist/desktop/windows-amd64/`。安装后从开始菜单或桌面快捷方式打开。
Release 使用 ProGuard 裁剪，Go 库保留 `-s -w`；不生成签名密钥，不更改 Android 签名。

## 界面与数据

共用首页、服务编辑、连接详情、NAT 检测、打洞过程与回放、房间与身份、连接方式、语音等页面；
主题、卡片、按钮按压、数值切换、导航弹簧和页面转场直接使用 Android 源码。
宽度达到 1000dp 时使用侧栏，否则使用共用浮动导航。支持 Tab / Shift+Tab、Ctrl+1～5、Esc。
外观支持五组强调色及浅色 / 深色 / 跟随系统；桌面不提供 Android 壁纸取色。
“减少动态效果”目前关闭页面转场，组件内部的交互反馈保留。

默认配置目录：Windows `%APPDATA%/hole-desktop`，Linux `${XDG_CONFIG_HOME:-~/.config}/hole-desktop`；
可用 `HOLE_DESKTOP_CONFIG_DIR` 指向独立目录。Kotlin 使用 `compose-state.json` 原子保存配置、凭据和运行意图，
文件采用 Unix 0600 或 Windows 当前所有者 ACL。凭据与旧 Rust 一样使用受保护文件，不是 Android Keystore 密文。
同一目录只允许一个 Kotlin 实例写入。

首次运行读取旧 Rust 的 `config.json`、`secrets.json`、`run-state.json`，保留原文件不改写。
后续 Kotlin 的修改仅写入自己的状态文件，不与 Rust 实时同步；不要同时运行两种前端连接同一设备身份。
配置损坏或版本不受支持时显示错误，保留原文。导入前需要确认，导入会停止当前连接；
支持 CLI YAML/JSON、Android 备份、旧桌面备份；导出 CLI 配置和与 Android 兼容的备份，可选择包含凭据。

语音使用 Java Sound 的系统默认设备，48 kHz、单声道、PCM16。当前需要设备支持此格式，暂不提供应用内回声消除、
降噪或其他采样率转换；设备失败会显示原因，可在“桌面与音频”重新打开设备。
关闭窗口会停止核心与音频后退出；可显式开启“打开应用时恢复连接”，手动停止会清除恢复意图。
系统托盘、系统登录自启和后台自动更新尚未接入。

## 本地验证

以下是由维护者执行的命令；新增实现没有因现有 AAR、APK、Rust 或旧报告而被判定通过。
先加载项目缓存，并使用桌面 JDK 21+，已有 JDK 25 可直接使用。直接执行 Gradle 前加载桌面工具链选择器，
同样需要 JNI 库的本机 C 编译器。

```bash
source scripts/build-env.sh
source desktop/scripts/toolchain.sh
hole_load_desktop_toolchain

# 先编译 Kotlin，再运行真实 JNI 回归，无需准备 AppImage 工具或 WiX。
bash android/gradlew -p desktop/compose --project-cache-dir "$HOLE_CACHE_ROOT/gradle-project-desktop" compileKotlin
bash android/gradlew -p desktop/compose --project-cache-dir "$HOLE_CACHE_ROOT/gradle-project-desktop" test

# 开发运行；自动先构建当前源码的 JNI 库。
bash android/gradlew -p desktop/compose --project-cache-dir "$HOLE_CACHE_ROOT/gradle-project-desktop" run

# 使用裁剪后的 Release 验证，检查 ProGuard / Java 模块 / 原生资源。
bash android/gradlew -p desktop/compose --project-cache-dir "$HOLE_CACHE_ROOT/gradle-project-desktop" runReleaseDistributable

# Go facade 的两套依赖图与并发回归。
go test -race ./mobile ./core
(cd android/corebridge/gobuild && go test hole/core hole/mobile)

# 发布入口的离线回归（使用编译器替身，不是安装包运行验收）。
python3 -m unittest discover -s scripts -p 'test_build.py'
python3 -m unittest discover -s scripts -p 'test_desktop_toolchain.py'

# Android 共用源码回归需要 Android 工具链，另开终端配置其 JDK。
# JDK 21：JVM 测试与 lint；正式 APK 构建继续由根入口加载既有 JDK 17 与签名。
bash android/gradlew -p android :app:testDebugUnitTest :wear:testDebugUnitTest :app:lintRelease :wear:lintRelease
git diff --check
```

`NativeBridgeTest` 覆盖真实 JNI 的版本 / 快照 / 幂等关闭、中文配置编码、默认凭据脱敏、非法请求与失效句柄，
并验证共享配置模型保留停用映射。旧 Rust 回归入口仍在根 `scripts/test_build.py` 与 `test.yaml`，不作为 Kotlin 验收。

人工检查：

1. 空配置启动、主题切换、窗口缩放、键盘导航，确认与 Android 的组件和动画一致。
2. 填入实际服务器与房间，双方设备名不同，验证 TCP / UDP 提供和使用、停止后端口释放、重复启停与运行中重配。
3. NAT 检测的开始 / 取消，设备详情、打洞记录增量读取、暂停 / 回放和记录导出。
4. 配置导入确认、带 / 不带凭据导出、旧 Rust 配置首次读取、损坏文件保留、第二实例拒绝和恢复连接开关。
5. 与 Android 实际通话，静音、设备不可用及重试；关闭窗口后确认没有核心进程或占用端口残留。
6. Windows 无系统 Java 的干净用户环境安装运行，再用递增版本号的安装包验证升级与配置保留；
   Linux 在目标发行版验证 AppImage 双击、执行权限、图形驱动与音频。
