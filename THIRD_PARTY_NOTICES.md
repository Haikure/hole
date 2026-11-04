# 许可与来源

Hole 原创代码由 Hole 贡献者以 **GPL-2.0-or-later** 授权，全文见 [LICENSE](LICENSE)。该授权允许接收者选择 GPL2 或后续版本。包含下面 LGPL3 移植部分的组合程序选择 GPL3 条款，全文见 [LICENSES/GPL-3.0.txt](LICENSES/GPL-3.0.txt)。各模块已有的第三方许可继续有效。

| 范围 | 来源与固定版本 | 许可与修改 |
| --- | --- | --- |
| `core/internal/nat/classify.go`、`probe.go`、`punch.go` | EasyTier，提交 `24e0303fc55e4e645a11818954dd5b712fb94510`；`easytier-core/src/connectivity/stun/client.rs`、`hole_punch/udp/{common,client,server,socket_array}.rs`、`config/mod.rs` | Copyright EasyTier contributors；保留 LGPL-3.0，全文见 `LICENSES/LGPL-3.0.txt`。Go 重写分类证据、递增/递减预测、84/25 socket 策略、静态 UDP STUN 列表；增加代次认证、单 reader 分流、取消与时间预算。未移植 TCP 打洞或 TXT 发现。 |
| Go 依赖及 `core/compat/anet` | 根及 Android 独立 `go.mod` / `go.sum` | 保留模块中的 LICENSE / COPYING / NOTICE；交付脚本从实际模块缓存收集原文及模块版本。 |
| 桌面 Rust / Slint 依赖 | `desktop/gui/Cargo.lock` | 保留每个 crate 的原许可；交付脚本从本地 Cargo 缓存收集原文和版本。 |
| Android 依赖 | `android/*.gradle.kts` 及各模块依赖声明 | 保留 Android SDK、Kotlin、AndroidX 及依赖各自许可；SDK / NDK / JDK 为单独获取的构建工具，不随应用重标许可。 |

移植后的修改由 Hole 贡献者完成。LGPL3 的附加权限及其引用的 GPL3 全文均随仓库和交付提供。`core/internal/nat/mapping.go` 为原创的 IGD / NAT-PMP 实现，不来自 EasyTier 的路由器库。

`./build.sh` 在交付目录生成 `licenses/` 和 `hole-source.tar.gz`。源码包包含当前工作树中的项目源码、锁定文件、构建脚本、许可材料和本地已解析 Go / Cargo 模块源码；不是仅导出 HEAD。Gradle 库中携带的原始许可另行提取到 `licenses/gradle/`。重建入口与工具链版本见 `docs/BUILDING.md`，依赖按锁定文件从正常模块源获取。本地配置、签名密钥、缓存与旧二进制不进入源码包。Android 重建需接收者提供自己的签名设置。
