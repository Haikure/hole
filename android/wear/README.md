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
界面按手表设备类型启用小屏布局，圆屏使用圆形裁切与可滚动的上下留白；手机继续使用原有布局。

## 圆屏操作

- 旋转表冠可滚动当前页面、表单、详情和说明弹窗；打开导航后横向浏览胶囊导航，关闭后恢复页面滚动。
- 主页面底部使用透明背景的白色弧条，可点按或上拉展开；展开后沿用手机的胶囊导航与运行按钮，支持首页、服务、连接、语音、设置及启动 / 停止转发。点按弧条、下拉或系统返回可收起。
- 沿用手机的渐变卡片、图标、分段选择器、配色与圆形按钮；针对圆屏调整间距和字号，仅在内容拥挤处换行。导航弧条不占用独立底栏，也不绘制黑色底板。
- 手表应用内采用紧凑显示，卡片、控件和间距以约 80% 的布局尺寸绘制，同一屏能显示更多内容。正文保留可读字号并跟随系统字体设置，白弧条的尺寸和手势区域维持原大小。
- 保留手机的导航弹簧高亮、按钮按压和页面切换动画；抽屉带位移与淡入淡出，短白弧条有圆头与展开反馈，表冠滚动平滑过渡。页面切换过程中仅当前页面接收表冠输入。
- 标题、返回与页面操作随内容滚动，保存 / 删除圆形按钮位于表单末尾，不遮挡字段。系统返回及未保存确认继续有效。
- 服务编辑、启停、滑动删除与撤销均保留；语音、麦克风来源、静音、连接筛选、路径详情、NAT 检测及高级设置继续使用共享界面与业务逻辑。
- 中继顺序使用上移 / 下移按钮，保留类型启用、停用与恢复默认；配置导入预览、外观、后台运行与更新入口保持可用。
- 说明、配置确认、连接详情和撤销提示使用可滚动的全屏界面，长内容可通过触摸或表冠浏览。

小屏布局与表冠事件通过 JVM 界面测试检查；真实表冠手感、输入法、系统权限页及圆屏显示仍需目标手表验收。

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
