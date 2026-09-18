# Wear OS 客户端

独立运行的 Wear OS 安装包，复用手机端 Kotlin 源码、资源、配置、前台服务及 Go 核心。
当前版本为 **0.3.5 / versionCode 9**，版本定义见 [build.gradle.kts](build.gradle.kts)。

## 与手机包的区别

| 项目 | 手机 | Wear |
| --- | --- | --- |
| ABI | `arm64-v8a` | `armeabi-v7a` + `arm64-v8a` |
| watch 特性 | 不要求 | `android.hardware.type.watch` 必需 |
| 独立运行 | 手机应用 | `standalone=true`，不依赖手机配对 |
| 触摸特性过滤 | 手机声明 | touchscreen / faketouch 可选 |
| 包名 | `dev.hole.app` | `dev.hole.app` |
| minSdk / targetSdk | 26 / 36 | 26 / 36 |

部分手表使用 32 位 Android 用户空间，双 ARM ABI 用于覆盖这类设备。
界面当前沿用手机端；圆屏布局和旋钮专属交互尚未适配。

## 构建与安装

从仓库根目录执行，签名配置与手机共用：

```bash
./build.sh wear --signing-config signing.env

# 查看手表的系统版本与 ABI
adb -s SERIAL shell getprop ro.build.version.sdk
adb -s SERIAL shell getprop ro.product.cpu.abilist
adb -s SERIAL install -r dist/wear/hole-0.3.5-wear-release.apk
```

输出在 `dist/wear/`，不覆盖 `dist/android/` 中的手机产物：

- 已签名、R8 优化并 strip 的 Release APK。
- 对应 R8 mapping、构建清单和 SHA-256 校验和。

同包名、同签名且版本号符合设备升级要求时可覆盖安装。
安装问题先检查 ADB 的 `INSTALL_FAILED_*` 结果、系统 API、ABI、签名及版本号。
安装后的圆屏可用性、网络切换和后台运行需要在目标手表上检查。

工具链与验证命令见 [构建指南](../../docs/BUILDING.md)，共享功能见 [Android README](../README.md)。
