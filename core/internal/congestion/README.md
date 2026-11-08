# QUIC BBR 来源与接入

共享核心使用标准 BBR 模式，无需修改系统 TCP 拥塞控制参数。

## 实现来源

| 项目 | 固定来源 |
| --- | --- |
| `bbr/`、`common/pacer.go` | [Hysteria](https://github.com/apernet/hysteria/tree/fb25dc852ebbc70658e67889f411887a82b7c281/core/internal/congestion) |
| 上游提交 | `fb25dc852ebbc70658e67889f411887a82b7c281` |
| 许可证 | 保留上游测试与 MIT [LICENSE.md](LICENSE.md) |
| QUIC | `github.com/apernet/quic-go v0.63.1-0.20261004180939-a10df75c260c`，提供 `SetCongestionControl` |

本地调整内部包导入路径，并以标准库 `cmp.Ordered` 和等价数值约束替换 `x/exp/constraints`，避免引入额外工具链依赖。

## 核心接入

共享核心使用标准 BBR 模式，在 TLS 握手后、业务 mux 前调用 `useBBR`；ICE、预测打洞、固定入口、设备中继及旧 IPv6 QUIC 均使用同一入口。每条连接独立维护带宽、RTT、pacing 和窗口状态。初始 MTU 取 QUIC 配置与地址估计的较小值，避免 TURN 的 1200 字节路径被错误初始化。

## 升级约束

- 当前依赖为 Hysteria 的 v0.63 分支，替换原上游 v0.65.0；不包含上游 v0.64/v0.65 的全部修复。
- 升级需跟踪维护分支同步情况，并重新运行传输、重连、语音与 Android 独立依赖图测试。
- 根模块与 `android/corebridge/gobuild` 必须保持同一 QUIC 版本。

接入架构见 [共享核心](../../README.md)，验证入口见 [构建指南](../../../docs/BUILDING.md)。
