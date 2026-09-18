# 构建、签名与验证

仓库根目录的 `build.sh` 是统一发布构建入口。只生成 Release，不部署 Worker、不安装到设备，
也不创建或替换签名密钥。CLI、桌面 Go 桥接和 Android 构建共用 `scripts/build_meta.py` 生成核心版本。

## 工具链

CLI / `desktop-core` 需要 Bash、Python 3 和 Go 1.26+；桌面桥接不需要 Qt、Slint 或 CGo。
项目 Android 构建固定使用 Go 1.26.4。

| Android 组件 | 版本 |
| --- | --- |
| Go | 1.26.4 |
| JDK | 17；Robolectric / Miuix UI 测试宿主另需 21+ |
| x/mobile（gomobile、gobind） | `v0.0.0-20260908204917-8b95e45f8d3e` |
| Gradle Wrapper / AGP | 9.4.1 / 9.2.1 |
| Kotlin / Compose compiler | 2.4.0 |
| SDK Platform | `platforms;android-37.0` |
| SDK Build Tools | 36.0.0 |
| NDK | 28.2.13676358（r28c） |
| minSdk / targetSdk | 26 / 36 |

Android 自动化工具安装脚本面向 Linux x86_64。已有 Go 1.26.4、JDK 17 后，可主动执行：

```bash
bash android/scripts/setup-toolchain.sh
```

脚本准备 SDK / NDK、Gradle 和同版本 gomobile / gobind，默认安装到 `~/.local/share/hole-android/`，
SDK 许可交互确认；`--accept-licenses` 用于显式自动接受。
普通 `build.sh` / `build-core.sh` 不安装 SDK，首次在线构建会下载 Maven / Go 依赖及 Wrapper 分发包。

Android 构建自动读取 `~/.local/share/hole-android/env.sh`；也可通过 `HOLE_TOOLCHAIN_ENV` 指定其他环境文件，
或在没有默认文件时直接提供 `PATH`、`JAVA_HOME`、`ANDROID_HOME`。`JDK17_HOME` 可显式选择绑定用 JDK。
只构建 CLI / `desktop-core` 时不加载 Android 环境，也不要求签名信息。

## 命令与参数

```bash
./build.sh cli                         # 宿主系统 / 架构
./build.sh cli --os linux --arch arm64
./build.sh cli --os windows --arch amd64
./build.sh desktop-core                # 独立 Go stdio 桥接；不构建 GUI
./build.sh desktop-core --os windows --arch amd64
./build.sh android                     # 手机 ARM64
./build.sh wear                        # 手表 ARM32 + ARM64
./build.sh android wear                # 一次 Gradle 调用构建两种 APK
./build.sh all                         # 保持 CLI + 手机 + 手表，不包含 desktop-core
./build.sh all desktop-core            # 显式组合新增桥接目标
./build.sh -t cli -t wear --offline
```

| 参数 | 含义 |
| --- | --- |
| `cli`、`desktop-core`、`android` / `phone`、`wear`、`all` | 位置参数，可组合；重复目标去重，`all` 不包含桌面桥接 |
| `-t NAME` / `--target NAME` | 等价的目标选项，可重复 |
| `--os GOOS`、`--arch GOARCH` | 控制 CLI / `desktop-core`；优先于 `GOOS` / `GOARCH` 环境变量，其次使用宿主值 |
| `-o DIR` / `--output DIR` | 交付根目录，默认仓库 `dist/` |
| `--signing-config FILE` | 签名环境文件，默认仓库 `signing.env` |
| `--offline` | 只使用已缓存 Go / Gradle 依赖；缓存缺项时报告错误 |
| `-h` / `--help` | 显示帮助；无参数时同样显示帮助 |

显式指定的输出目录和配置文件相对调用目录解析；密钥库相对路径固定相对仓库根目录。
CLI / 桌面桥接的交叉编译变量不传入 Android 绑定。不同目标、系统和架构的产物互不覆盖。

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
仅包含占位信息的 `signing.env.example` 随源码发布。

## 缓存与产物

构建脚本将以下路径固定在当前项目 `.cache/`，覆盖旧工具链环境中的历史缓存位置：

| 路径 | 用途 |
| --- | --- |
| `.cache/go-build/` | Go 编译缓存 `GOCACHE` |
| `.cache/go/` | 项目 `GOPATH`，含 gomobile 工作数据 |
| `.cache/go/pkg/mod/` | Go 模块缓存 `GOMODCACHE` |
| `.cache/gradle/` | `GRADLE_USER_HOME`，含 Wrapper、Maven 依赖和 Gradle 缓存 |
| `.cache/gradle-project/` | Gradle 项目状态 |
| `.cache/kotlin/` | Kotlin 持久状态 |
| `.cache/tmp/` | 构建临时文件、`GOTMPDIR` |
| `.cache/python/`、`.cache/xdg/` | Python / XDG 缓存 |

SDK、NDK、JDK、gomobile 可执行文件和签名密钥不是构建缓存，保留在安装位置。
`android/*/build/` 和 `android/corebridge/libs/` 是模块生成物，仍在原处且被 Git 忽略。
无需清缓存即可正常增量构建；仓库移动后路径随脚本所在目录重新计算。

交付结构：

```text
dist/
  cli/hole-<goos>-<goarch>[.exe]
  cli/hole-<goos>-<goarch>[.exe].sha256
  desktop-core/hole-desktop-core-<goos>-<goarch>[.exe]
  desktop-core/hole-desktop-core-<goos>-<goarch>[.exe].sha256
  android/hole-<version>-release.apk
  android/hole-<version>-release-mapping.txt
  android/build-manifest.json
  android/SHA256SUMS
  wear/hole-<version>-wear-release.apk
  wear/hole-<version>-wear-release-mapping.txt
  wear/build-manifest.json
  wear/SHA256SUMS
```

CLI / 桌面桥接使用 `CGO_ENABLED=0`、`-trimpath`、`-s -w`，不调用宿主 `strip` 处理其他架构二进制。
Android Go 核心同样使用 `-s -w`，原生库保持 16 KiB 对齐；APK 使用 R8、资源收缩和 native strip，
不包含调试信息或静态符号表，动态链接与 Go 运行所需元数据保留。

构建自动检查 APK 签名、ABI、16 KiB ZIP / ELF 对齐、核心摘要、JNI 保留、R8、资源收缩及 native strip。
普通构建不运行单元测试或完整 lint，清单中的相应字段标为未检查，而不是复用旧报告声明通过。
`SHA256SUMS` 仅引用同目录交付文件；用于追踪核心的 AAR 摘要保留在构建清单中。

桌面桥接的 stdio 协议见 [desktop/README.md](../desktop/README.md)，后续界面见
[GUI 实施计划](DESKTOP_GUI_PLAN.md)。`desktop/` 和 `cmd/hole-desktop-core/` 不在共享核心
源码摘要中，也不被 CLI / gomobile 导入；只修改桌面代码不会改变 AAR 的核心源码身份。

## 回归测试

```bash
# 仓库根目录；手工调用工具也采用项目缓存
source scripts/build-env.sh
python3 -m unittest discover -s scripts -p 'test_build.py'
python3 -m unittest discover -s android/scripts -p 'verify_apk_test.py'
go test -race -count=1 ./...
go vet ./...
node --test worker_ice.test.mjs

# Android 绑定依赖图也单独验证
(cd android/corebridge/gobuild && go test -race -count=1 hole/core hole/mobile && go vet hole/core hole/mobile)
```

`go test ./...` 包含桌面协议、真实 Engine 本地信令及宿主子进程测试；可单独执行
`go test -race -count=1 ./desktop ./cmd/hole-desktop-core`。Windows 交叉编译只验证构建，
Windows 管道、窗口与系统集成运行验收在相应系统单独执行。

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
