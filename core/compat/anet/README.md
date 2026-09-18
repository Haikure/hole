# anet Android 兼容模块

这是 `github.com/wlynxg/anet` 的最小接口发现兼容实现。
Pion transport v4.1.0 会引入 anet v0.0.5；其 Android 实现引用私有 `net.zoneCache` 符号，
与项目使用的 Go 1.26 链接检查不兼容。

## 行为

- 桌面接口委托给公开的 Go `net` API。
- Android 默认发现入口返回明确错误；实际 ICE 网络始终由 `core/ice_network.go` 和
  `mobile/boundPlatform` 注入，使用系统 ConnectivityManager / Network、DNS 和 fd 绑定。
- 不使用私有运行时符号，不关闭链接检查，也不回退到受限 netlink。

根模块与 Android 绑定模块均通过 `replace` 指向本目录。
源码与 `go.mod` 纳入 AAR 输入和核心版本摘要，修改后需同时验证两套依赖图。

待上游提供兼容的公开 API 实现后，可在完成桌面及 Android 绑定回归后移除此替换。
相关架构见 [共享核心](../../README.md)，验证命令见 [构建指南](../../../docs/BUILDING.md)。
