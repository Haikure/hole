# 构建、签名与验证

仓库根目录的 `build.sh` 是统一发布构建入口。只生成 Release，不部署 Worker、不安装到设备，
也不创建或替换签名密钥。核心版本和各前端版本集中维护在仓库根目录的 `versions.json`。

[工具链](#工具链) · [命令与参数](#命令与参数) · [CI](#github-actions-构建与发布) · [版本号](#版本号) · [签名](#签名) · [产物](#缓存与产物) · [验证](#回归测试) · [项目首页](../README.md)

| 构建目标 | 常用命令 | 产物目录 |
| --- | --- | --- |
| CLI | `./build.sh cli` | `dist/cli/` |
| 独立核心宿主 | `./build.sh core` | `dist/core/` |
| Linux / Windows 桌面 | `./build.sh desktop` | `dist/desktop/` |
| 旧 Rust 桌面 | `./build.sh desktop-rust` | `dist/desktop-rust/` |
| Android 手机 | `./build.sh android` | `dist/android/` |
| Wear OS | `./build.sh wear` | `dist/wear/` |
| CLI + 手机 + 手表 | `./build.sh all` | 各自目录；不含桌面或独立核心 |

PenMods 使用 [插件构建入口](../penmods/README.md#构建)。

## 工具链

| 目标 | 工具与依赖 |
| --- | --- |
| CLI / `core` | Bash、Python 3、Go 1.27.4；独立宿主无需图形库或 CGo |
| `desktop` | Kotlin 2.4.0 / Compose Multiplatform 1.12.2，完整 JDK 21+（支持 JDK 25）、Go 1.27.4、本机 C 编译器；仅在目标系统和架构上构建 |
| Linux 桌面 | 系统 GCC、appimagetool、本架构 type2-runtime；可显式运行 `bash desktop/scripts/setup-appimage.sh` 准备工具，或自行提供 `HOLE_APPIMAGETOOL` / `HOLE_APPIMAGE_RUNTIME`；AppImage 自带裁剪 Java 运行时与 Go 原生库 |
| Windows 桌面 | Git Bash、MinGW-w64 GCC、WiX 3；官方 jpackage 每用户 exe 安装包，固定升级标识，自带 Java 运行时与 Go DLL |
| `desktop-rust` | Rust 1.95+ / Cargo，Yororen UI 0.4 / GPUI CE 0.3；Linux 开发依赖与交叉工具链见旧桌面 README |

`./build.sh desktop` 直接执行原生桥接、Kotlin Release 与平台打包，并自动选择已安装的完整 JDK 21+。
优先使用 `HOLE_DESKTOP_JAVA_HOME`，其次是合格的 `JAVA_HOME`、`JDK25_HOME` / `JDK21_HOME`，最后搜索 PATH 和系统常见目录。
已有带 `jmods` 的 JDK 25 无需再安装 JDK 21。桌面字节码目标为 JVM 21，构建、JNI 和随包运行时统一使用所选 JDK，ProGuard 7.10.0 支持 Kotlin 2.4。
Kotlin 桌面不加载 Android JDK 17；与 Android 组合构建时先在子进程选择桌面 JDK，再加载 Android 的既有工具链。
旧 Rust 交叉构建需提前安装对应 Rust target 和平台 linker / sysroot（Linux 含目标 ALSA 库）。
普通构建不会安装 JDK、C 编译器、AppImage 工具、Rust 或系统开发包。
Linux 可先执行 `bash desktop/scripts/setup-appimage.sh`，显式下载固定版本 AppImage 工具到根 `.cache/`；
本机与 Actions 复用同一脚本，后续 `./build.sh desktop` 自动发现已有缓存。
开发 / 验证使用 `bash desktop/scripts/gradle.sh <任务>`，统一选择桌面 JDK、项目缓存和离线选项，无需准备安装包工具。
项目 Android 构建固定使用 Go 1.27.4。

| Android 组件 | 版本 |
| --- | --- |
| Go | 1.27.4 |
| JDK | 17；Robolectric / Miuix UI 测试宿主另需 21+ |
| x/mobile（gomobile、gobind） | `v0.0.0-20261011034528-197770444867` |
| Gradle Wrapper / AGP | 9.4.1 / 9.2.1 |
| Kotlin / Compose compiler | 2.4.0 |
| SDK Platform | `platforms;android-37.0` |
| SDK Build Tools | 36.0.0 |
| NDK | 28.2.13676358（r28c） |
| minSdk / targetSdk | 26 / 36 |

Android 自动化工具安装脚本面向 Linux x86_64。已有 Go 1.27.4、JDK 17 后，可主动执行：

```bash
bash android/scripts/setup-toolchain.sh
```

脚本准备 SDK / NDK、Gradle 和同版本 gomobile / gobind，默认安装到 `~/.local/share/hole-android/`。
普通构建脚本不安装 SDK 或 Rust 工具链；首次在线构建会下载所需 Go、Cargo、Maven 依赖及 Gradle Wrapper 分发包。

Android 构建自动读取 `~/.local/share/hole-android/env.sh`；也可通过 `HOLE_TOOLCHAIN_ENV` 指定其他环境文件，
或在没有默认文件时直接提供 `PATH`、`JAVA_HOME`、`ANDROID_HOME`。`JDK17_HOME` 可显式选择绑定用 JDK。
只构建 CLI / `core` / `desktop` 时不加载 Android 环境，也不要求签名信息。

## 命令与参数

<details>
<summary>目标组合、交叉编译与离线构建示例</summary>

```bash
./build.sh cli                         # 宿主系统 / 架构
./build.sh cli --os linux --arch arm64
./build.sh cli --os windows --arch amd64
./build.sh core                        # 独立 Go stdio 核心宿主；不构建 GUI
./build.sh core --os windows --arch amd64
./build.sh desktop                    # 本机：Windows exe 安装包 / Linux AppImage
./build.sh desktop --os windows --arch amd64  # 在 Windows AMD64 上运行
./build.sh desktop-rust --os windows --arch amd64  # 旧 Rust，可配置交叉工具链
./build.sh android                     # 手机 ARM64
./build.sh wear                        # 手表 ARM32 + ARM64
./build.sh android wear                # 一次 Gradle 调用构建两种 APK
./build.sh all                         # CLI + 手机 + 手表，不包含 core 或 desktop
./build.sh all core                    # 显式组合独立 Go 核心宿主
./build.sh all desktop                 # 显式增加桌面 GUI
./build.sh -t cli -t wear --offline
```

</details>

| 参数 | 含义 |
| --- | --- |
| `cli`、`core`、`desktop`、`desktop-rust`、`android` / `phone`、`wear`、`all` | 位置参数，可组合；重复目标去重，`all` 不包含 `core` 或桌面目标 |
| `-t NAME` / `--target NAME` | 等价的目标选项，可重复 |
| `--os GOOS`、`--arch GOARCH` | 优先于 `GOOS` / `GOARCH` 环境变量，其次使用宿主值；Kotlin desktop 要求匹配宿主，CLI / core / desktop-rust 可交叉构建 |
| `-o DIR` / `--output DIR` | 交付根目录，默认仓库 `dist/` |
| `--signing-config FILE` | 签名环境文件，默认仓库 `signing.env` |
| `--offline` | 只使用已缓存 Go / Cargo / Gradle 依赖；缓存缺项时报告错误 |
| `-h` / `--help` | 显示帮助；无参数时同样显示帮助 |

### 路径与目标隔离

显式指定的输出目录和配置文件相对调用目录解析；密钥库相对路径固定相对仓库根目录。
CLI / 核心宿主 / 桌面的交叉编译变量不传入 Android 绑定。不同目标、系统和架构的产物互不覆盖。

## GitHub Actions 构建与发布

### 发布工作流

`.github/workflows/build.yaml` 支持手动构建和版本 tag 推送。手动运行只上传 Artifact，不创建 GitHub Release；推送任意 tag
时使用完整构建矩阵，并在所有构建成功后创建同名 GitHub Release，说明内容来自仓库根目录的 `CHANGELOG.md`。
Release 不再上传 `.sha256` / `SHA256SUMS` 校验文件；GitHub Release 页面会显示每个上传资产的 SHA-256。
默认 `type=all` 会并行构建：

- Android 手机 ARM64；
- Wear OS 单个 APK，包含 ARM32 + ARM64；
- `desktop` 的 Linux AMD64 / ARM64、Windows AMD64；Windows ARM64 需要本地原生工具链，尚未配置 CI runner；
- `core` 的 Linux / Windows AMD64 / ARM64；
- `cli` 的 Linux / Windows AMD64 / ARM64。

| 工作流选项 / 产物 | 行为 |
| --- | --- |
| `type` | 可选 `all` / `android` / `wear` / `desktop` / `core` / `cli`；`android` 仅手机 ARM64，`wear` 仅手表双 ARM ABI，均忽略 `arch` / `os` |
| `arch` / `os` | 筛选 CLI / `core` / `desktop` 矩阵 |
| Artifact | 每个矩阵任务独立上传；手机 `hole-android-arm64`、手表 `hole-wear-arm32-arm64`，各只含一个 APK |
| 压缩 | `compression-level: 0`；GitHub 下载接口仍提供归档容器 |

Android 手机与 Wear Release 共用以下仓库或组织 Actions secrets：

- `HOLE_SIGNING_KEYSTORE_BASE64`：已有 Release keystore 的 Base64 内容；
- `HOLE_SIGNING_STORE_PASSWORD`、`HOLE_SIGNING_KEY_ALIAS`、`HOLE_SIGNING_KEY_PASSWORD`：对应签名设置。

工作流只在运行器临时目录解码 keystore，不生成或替换签名密钥。Linux ARM64 桌面任务使用 `ubuntu-24.04-arm` 运行器，Windows ARM64 Kotlin GUI 仅支持自备工具链的本机构建。工作流不调用 `penmods/build.sh`。
手机与 Wear 使用同一套矩阵任务定义，分别调用 `build.sh android` / `build.sh wear`；每个 APK 都经过根入口的
签名、版本、ABI、设备声明和 Release 优化检查。tag 发布保留 `hole-<版本>-release.apk` 与
`hole-<版本>-wear-release.apk` 原名，应用内更新按各模块 `BuildConfig.IS_WEAR` 选择对应附件；Wear 不回退到手机包。

## 版本号

### 版本清单与同步

编辑根目录 `versions.json`：`core.version` 是共享核心版本，CLI、独立核心宿主和 Android AAR 始终使用同一个值；Android 手机与 Wear OS 共用 `frontends.android` 的版本名和版本码。桌面 GUI 与 PenMods 插件各自维护前端版本。`worker.version` 独立定义 Worker 软件版本，由 Worker 模块直接读取并上报；客户端能力依据实际共享核心版本和已确认的 Worker 版本判断。
修改后运行 `python3 scripts/sync_versions.py`，同步 Cargo package/lock 元数据以及 PenMods 的 `xmake.lua` 和 `metadata.json`。Android Gradle 配置直接读取该清单。桌面 GUI 与 PenMods 正式构建会先检查同步状态；也可手动运行 `python3 scripts/sync_versions.py --check`。

核心构建标识由提交标识和共享核心源码/依赖摘要自动生成，和手动维护的核心版本分开记录；Android APK 验证会同时核对核心版本与构建标识。

## 签名

首次配置：

```bash
cp -n signing.env.example signing.env
chmod 600 signing.env
```

编辑文件，填写已有密钥库的信息（Bash 赋值语法，可使用引号与 `$HOME`）：

```bash
HOLE_SIGNING_STORE_FILE="$HOME/.android/release.keystore"
HOLE_SIGNING_STORE_PASSWORD='STORE_PASSWORD'
HOLE_SIGNING_KEY_ALIAS='KEY_ALIAS'
HOLE_SIGNING_KEY_PASSWORD='KEY_PASSWORD'
```

已有的非空 `HOLE_SIGNING_*` 环境变量优先于文件，便于 CI 注入；不提供文件时也可仅使用环境变量。
四项均为必填。脚本先检查密钥库和别名，再运行构建，最后核对 APK 证书与所配置密钥一致。
密码通过环境变量传递，不作为构建命令参数或写入交付清单。

手机和 Wear 使用同一组签名设置。覆盖升级沿用原签名身份；脚本不会静默改用新生成的调试密钥。
本地 `signing.env` / `signing.*.env`、`*.keystore`、`*.jks` 和 `android/signing/` 不进入 Git。

## 缓存与产物

### 项目缓存

构建脚本将以下路径固定在当前项目 `.cache/`，覆盖旧工具链环境中的历史缓存位置：

| 路径 | 用途 |
| --- | --- |
| `.cache/go-build/` | Go 编译缓存 `GOCACHE` |
| `.cache/go/` | 项目 `GOPATH`，含 gomobile 工作数据 |
| `.cache/go/pkg/mod/` | Go 模块缓存 `GOMODCACHE` |
| `.cache/cargo-home/` | Cargo registry、crate 下载和索引缓存 |
| `.cache/cargo-target/` | 桌面 GUI Release 构建产物与增量缓存 |
| `.cache/gradle/` | `GRADLE_USER_HOME`，含 Wrapper、Maven 依赖和 Gradle 缓存 |
| `.cache/gradle-project/` | Gradle 项目状态 |
| `.cache/kotlin/` | Kotlin 持久状态 |
| `.cache/tmp/` | 构建临时文件、`GOTMPDIR` |
| `.cache/python/`、`.cache/xdg/` | Python / XDG 缓存 |

SDK、NDK、JDK、gomobile 可执行文件和签名密钥不是构建缓存，保留在安装位置。
`android/*/build/` 和 `android/corebridge/libs/` 是模块生成物，仍在原处且被 Git 忽略。
无需清缓存即可正常增量构建；仓库移动后路径随脚本所在目录重新计算。

### 交付结构

```text
dist/
  cli/hole-<goos>-<goarch>[.exe]
  cli/hole-<goos>-<goarch>[.exe].sha256
  core/hole-core-<goos>-<goarch>[.exe]
  core/hole-core-<goos>-<goarch>[.exe].sha256
  desktop/windows-<goarch>/Hole-<version>.exe
  desktop/linux-<goarch>/Hole-<version>-<appimage-arch>.AppImage
  desktop-rust/<goos>-<goarch>/hole-desktop[.exe]
  android/hole-<version>-release.apk
  android/hole-<version>-release-mapping.txt
  android/build-manifest.json
  android/SHA256SUMS
  wear/hole-<version>-wear-release.apk
  wear/hole-<version>-wear-release-mapping.txt
  wear/build-manifest.json
  wear/SHA256SUMS
```

### 优化与自动检查

CLI / 独立 Go 核心宿主使用 `CGO_ENABLED=0`、`-trimpath`、`-s -w`，不调用宿主 `strip` 处理其他架构二进制。
Kotlin 桌面使用 Compose Release / ProGuard 与裁剪 Java 运行时，JNI 原生库使用 `-s -w`；
Windows 使用官方 `packageReleaseExe`，Linux 使用 `createReleaseDistributable` 后封装 AppImage。
桌面 JDK 21+ 必须包含 `jmods/java.base.jmod`；CI 使用 Zulu JDK 25，缺少 JMODs 的发行包需先自行补齐匹配模块。
命令与本地验证见 [Kotlin 桌面说明](../desktop/compose/README.md)。JNI 带 `desktopjni` 标记，不进入普通 CLI / Android 构建。

旧 Rust GUI 使用 Cargo `release` profile 和 `embedded-core` feature；`desktop-rust` 目录包含内嵌核心的 GUI
与 SHA-256 文件。构建时通过 `HOLE_EMBED_CORE` 传入刚生成的同架构宿主，校验 ELF / PE 后压缩嵌入；
Cargo 跟踪该输入的变化。Linux 运行时使用可执行 memfd 和 `/proc/self/fd`，Windows 使用私有临时目录，
加载和清理细节见 [桌面桥接](../desktop/README.md#内嵌核心的加载与回收)。
Android Go 核心同样使用 `-s -w`，原生库保持 16 KiB 对齐；APK 使用 R8、资源收缩和 native strip，
不包含调试信息或静态符号表，动态链接与 Go 运行所需元数据保留。

构建自动检查 APK 签名、ABI、16 KiB ZIP / ELF 对齐、核心摘要、JNI 保留、R8、资源收缩及 native strip。
普通构建不运行单元测试或完整 lint，清单中的相应字段标为未检查，而不是复用旧报告声明通过。
`SHA256SUMS` 仅引用同目录交付文件；用于追踪核心的 AAR 摘要保留在构建清单中。

旧桌面桥接的 stdio 协议、Rust 构建和界面说明见 [desktop/README.md](../desktop/README.md)。
`desktop/`、`desktop/gui/` 和 `cmd/hole-core/` 不在共享核心
源码摘要中，也不被 CLI / gomobile 导入；只修改桌面代码不会改变 AAR 的核心源码身份。

## 回归测试

按改动范围选择检查；下面的全量命令用于跨模块或发布验证，不是每次修改的固定清单。

| 改动 | 最小检查 |
| --- | --- |
| 文档 | `git diff --check` |
| Worker | 两个 Worker 测试文件 |
| Go 单模块 | 受影响包测试；核心 / mobile 同时检查 Android 独立依赖图 |
| 并发、传输、身份或资源限额 | 在相关包追加 race 检查 |
| Android UI / 业务 | 相关 JVM 测试与 lint；构建、桥接、签名或交付变化时追加 Release |

日常 Go 检查保留默认缓存，不无条件添加 `-count=1`。

### 桌面 CI

旧桌面 Rust 编译与测试使用独立的 [test.yaml](../.github/workflows/test.yaml)，通过 `desktop-rust` 构建。
它不代表 Kotlin 已通过验证。Kotlin 本地 JNI、共享 UI、Release 包和实际连接的验证命令见
[Kotlin 桌面验证](../desktop/compose/README.md#本地验证)。

| 项目 | 说明 |
| --- | --- |
| 自动触发 | `main`、临时分支 `codex/nat-direct` 推送涉及桌面、共享核心、构建脚本或版本的变更；相关 PR |
| 手动触发 | `gh workflow run test.yaml --repo Haikure/hole --ref <分支>` |
| 查看 / 下载 | `gh run view` / `gh run download` |
| 源码范围 | 工作流只读取远端分支；未提交源码需先获准提交到远端 |

CI 通过 `scripts/test_desktop_timezones.py` 在 UTC、上海、纽约分别启动独立 Rust 测试进程，验证 NAT 时间秒级格式、跨日、带偏移输入、夏令时及本机 / 对端展示。Linux 使用 `TZ`，Windows 切换 runner 时区并在结束后恢复；常规测试不修改时区。

核心构建标识覆盖 `core/internal/nat`。自动 NAT 测试使用本地 STUN / 内存路由器 fixture，不代表真实路由器、运营商 NAT、移动切网或成功率验收。

### Go、脚本与 Worker

<details>
<summary>跨模块 / 发布全量检查命令</summary>

```bash
# 仓库根目录；手工调用工具也采用项目缓存
source scripts/build-env.sh
python3 -m unittest discover -s scripts -p 'test_build.py'
python3 -m unittest discover -s scripts -p 'test_release_workflow.py'
python3 -m unittest discover -s android/scripts -p 'verify_apk_test.py'
go test -race -count=1 ./...
go vet ./...
node --test worker/worker_ice.test.mjs worker/worker_fixture.test.mjs

# Android 绑定依赖图也单独验证
(cd android/corebridge/gobuild && go test -race -count=1 hole/core hole/mobile && go vet hole/core hole/mobile)
```

</details>

`go test ./...` 包含桌面协议、真实 Engine 本地信令及宿主子进程测试；可单独执行
`go test -race -count=1 ./desktop ./cmd/hole-core`。Windows 交叉编译只验证构建，
Windows 管道、窗口与系统集成运行验收在相应系统单独执行。

Hole 中继专项覆盖 `core/hole_*test.go`、Worker 三方生命周期、各客户端配置往返和
PenMods 排序交互。可用 `go test ./core -run '^$' -bench BenchmarkHoleForwarding -benchmem`
测量鉴权、路由入队热路径；它不包含内核 UDP 收发，不代表公网吞吐或设备性能。
`BenchmarkHoleBackendLoopback` 对照逐包与 Linux 批量系统调用，报告每包耗时、吞吐和
批次完成耗时的 p50/p95/p99；这是回环提交加接收时间，不是公网 RTT。
CLI 中继及跨平台核心保留 `CGO_ENABLED=0`；Android/Wear、桌面和 PenMods 沿用各自
Release 构建入口。真实跨 NAT、长时间稳定性及真机音频播放需单独验收。

### Android JVM、lint 与 APK

Go 集成测试使用本地 socket；Worker 测试需要提供 `node:test` 的 Node.js。
Android UI 测试使用已有 JDK 21+，通过 `JDK_TEST_HOME` 指定其目录：

```bash
source scripts/build-env.sh
hole_load_android_toolchain
cd android
./gradlew --no-daemon \
  --project-cache-dir "$HOLE_CACHE_ROOT/gradle-project" \
  "-Pkotlin.project.persistent.dir=$HOLE_CACHE_ROOT/kotlin" \
  "-Djava.io.tmpdir=$TMPDIR" \
  -Dorg.gradle.java.home="$JDK_TEST_HOME" \
  :app:lintRelease :wear:lintRelease \
  :app:testDebugUnitTest :wear:testDebugUnitTest :corebridge:testDebugUnitTest
```

测试宿主使用 debug 变体；交付 APK 仍为 Release。完成对应任务后，额外检查报告：

```bash
# 仓库根目录
python3 android/scripts/verify-apk.py android/app/build/outputs/apk/release/app-release.apk \
  --module app --variant release --check-reports
python3 android/scripts/verify-apk.py android/wear/build/outputs/apk/release/wear-release.apk \
  --module wear --variant release --check-reports
```

可加 `--previous OLD_APK` 检查与旧包同签名、同包名且 versionCode 递增。
清单记录静态检查结果；设备安装、运行、切网、息屏和升级检查另行记录，不由脚本推断。
