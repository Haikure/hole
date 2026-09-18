# Android 客户端

Kotlin / Jetpack Compose 原生客户端，与 CLI 共用 `core/` 中的传输和会话实现。
支持 Material 3 与 Miuix 主题、TCP / UDP 映射、运行中重配、配置导入导出和结构化连接报告。

## 模块

| 模块 | 内容 |
| --- | --- |
| `app` | 手机界面、配置仓库、通知和前台服务 |
| [wear](wear/README.md) | 共用手机源码的 Wear OS 安装包，独立设备声明和 ABI 选择 |
| `corebridge` | Go AAR 构建、Kotlin 接口、Android Network / DNS / socket 适配 |

当前手机版本为 **0.3.5 / versionCode 8**，包名 `dev.hole.app`，仅打包 `arm64-v8a`。
最低 Android 8.0（API 26），compileSdk 37，targetSdk 36；版本以 [app/build.gradle.kts](app/build.gradle.kts) 为准。

## 构建

从仓库根目录执行：

```bash
cp -n signing.env.example signing.env
# 填写现有密钥库及对应密码、别名
./build.sh android
```

Gradle 自动调用 `:corebridge:bindGoCore`，从当前 Go 源码生成 `corebridge/libs/holecore.aar`。
共享 AAR 包含 ARM32 / ARM64，手机 APK 只选择 ARM64，不需要手工替换 AAR。

交付文件在仓库根目录 `dist/android/`：

- `hole-<version>-release.apk`
- `hole-<version>-release-mapping.txt`
- `build-manifest.json`、`SHA256SUMS`

Release 开启 R8 混淆、资源收缩与 native strip，保留 JNI 所需类和方法。
工具版本、签名配置和完整检查命令见 [构建指南](../docs/BUILDING.md)。

## 使用

1. 设置服务器地址、信令密码、房间号、房间密码和设备名。
2. 添加“提供服务”或“访问服务”，与另一端使用相同服务 ID。
3. 打开条目开关和首页总连接开关，查看连接详情及本地访问端口。
4. 通过“连接方式 · ICE 与中继”选择自动、仅 ICE 或仅 IPv6，并调整 STUN / TURN 设置。

CLI YAML 可导入导出；应用完整备份还包含停用条目与界面设置。凭据由 Android Keystore
管理的 AES-GCM 密钥保护，默认导出不含凭据，备份不复制设备绑定的 Keystore 密文。

## 生命周期和平台边界

- 普通前台服务持有唯一引擎并显示运行通知，不使用 VPN / TUN。
- Android 网络地址来自 `ConnectivityManager` / `LinkProperties`，外部 DNS 和 socket 绑定同一选定 Network。
- 回环监听和回环上游不绑定外部网络；不使用进程全局绑网，也不回退到 Android 受限的 netlink 接口。
- 保存配置、请求运行、已加入房间、服务映射就绪是独立状态。修改有效配置由共享核心串行重配。
- 页面展示结构化状态和报告；不收集 Go 原始日志。前台、后台和停止态使用不同的状态刷新策略。

发布检查应覆盖同签名升级、首次启动、主题切换、TCP / UDP 互通、切网恢复和息屏后台运行。
静态 APK 检查与设备运行检查分别记录；桥接约定见 [mobile/README.md](../mobile/README.md)。
