# Wear OS 客户端

独立运行的 Wear OS 安装包，复用手机端 Kotlin 源码、资源、配置、前台服务及 Go 核心。
Wear OS 与手机共用 [versions.json](../../versions.json) 中的 Android 前端版本和 `versionCode`。

[与手机包的区别](#与手机包的区别) · [构建与安装](#构建与安装) · [手机端使用说明](../README.md#使用) · [项目首页](../../README.md)

| 共用能力 | 说明 |
| --- | --- |
| NAT 与端口映射 | 复用手机配置与快照；直连策略只在共享 Go 核心实现 |
| Hole 中继 | 支持设备名、统一顺序、配置交换和路径诊断；可使用 CLI 中继，不能在手表开启中继服务 |
| 验收边界 | 真机移动切网、运营商 NAT 和后台运行需在目标设备检查 |

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
```

输出在 `dist/wear/`，不覆盖手机产物。包含已签名、R8 优化并 strip 的 Release APK、R8 mapping、构建清单和 SHA-256 校验和。

<details>
<summary>通过 ADB 检查并安装到目标手表</summary>

```bash
# 查看手表的系统版本与 ABI
adb -s SERIAL shell getprop ro.build.version.sdk
adb -s SERIAL shell getprop ro.product.cpu.abilist
android_version="$(python3 -c 'import json; print(json.load(open("versions.json"))["frontends"]["android"]["version"])')"
adb -s SERIAL install -r "dist/wear/hole-${android_version}-wear-release.apk"
```

同包名、同签名且版本号符合设备升级要求时可覆盖安装。
安装问题先检查 ADB 的 `INSTALL_FAILED_*` 结果、系统 API、ABI、签名及版本号。
安装后的圆屏可用性、网络切换和后台运行需要在目标手表上检查。

</details>

工具链与验证命令见 [构建指南](../../docs/BUILDING.md)，共享功能见 [Android README](../README.md)。
