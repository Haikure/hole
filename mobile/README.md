# Android Go 桥接 API

`hole/mobile` 是共享核心的 gomobile 窄接口，当前 **API version 1**。
生成的 Java 包为 `dev.hole.core.mobile`；构建入口是 `./build.sh android` 或 `./build.sh wear`。
本目录不复制网络协议或转发实现。

## 接口

```java
Mobile.version();
Engine engine = Mobile.newEngineWithNetworkBinding(sink, binding);
engine.start(requestJSON);
engine.applyConfig(requestJSON);
engine.snapshotJSON();
engine.networkChanged(eventJSON);
engine.stop();
engine.close();
```

Android 使用带 `NetworkBinding` 的构造方法；旧 `newEngine` 和 `newEngineWithNetworkProvider` 入口保留兼容。
创建实例不联网；`stop` 可重复调用并等待清理，`close` 为最终关闭。

请求结构：

```json
{
  "api_version": 1,
  "server_url": "wss://HOST/ws",
  "config": {
    "room": "ROOM",
    "password": "SIGNAL_PASSWORD",
    "token": "ROOM_TOKEN",
    "device_name": "android-device",
    "session_timeout": "10m",
    "transport": {"preferred": "ice", "allow_legacy": true},
    "provide": [{"id": "ssh", "service": "tcp://127.0.0.1:22"}],
    "consume": []
  }
}
```

- 请求上限 128 KiB，拒绝未知字段和多个 JSON 对象；配置复用核心严格解析器。
- `config_revision` 是旧版本可选字段，核心不读取、不比较，也不在快照和事件中回传；新宿主无需发送。
- 代次、序号及 64 位计数以字符串跨越 JSON 边界。
- `server_url` 位于请求外层；应用专用的 `enabled` / `entry_id` 不进入核心配置。
- 错误以含 `code`、`message` 的 JSON 传出。状态快照与事件不包含凭据。
- `EventSink.onEvent` 在 Go 后台线程执行，应及时返回；生命周期命令调度到其他线程。
- Android 只接收结构化事件，桥接层过滤原始日志；`stop` 返回后不再交付旧回调。

## NetworkBinding

| 方法 | 职责 |
| --- | --- |
| `networkJSON()` | 提供选定 Network 的 handle、可用性、接口与地址 |
| `interfacesJSON()` | 兼容 API 1 的接口快照入口 |
| `bindSocket(fd, handle)` | 在 connect / listen 前将外部 socket 绑定指定 Network |
| `lookupIP(host, handle)` | 使用相同 Network 执行系统 DNS 解析 |

fd 仅在同步回调期间借用。宿主可复制 fd 调用公开平台 API，但原 fd 的所有权属于 Go。
Network handle 使用十进制字符串。失效网络返回明确错误，不改用默认路由或全局 DNS；
回环监听和回环上游不绑定外部 Network。引擎不持有 Activity。

网络变更事件示例：

```json
{"sequence":"1","network_handle":"4294967397"}
```

序号单调递增；重复或迟到事件忽略，停止态不启动网络。

## 配置交换与构建

`Mobile.decodeCLIConfig` / `Mobile.encodeCLIConfig` 使用共享解析器，CLI YAML 包含 `server_url`。
完整应用备份由 Kotlin 管理，包含停用条目等宿主字段，不复制 Android Keystore 密文。

`android/corebridge/gobuild` 是独立绑定模块，锁定 x/mobile 及所需依赖并通过 `replace hole` 引用当前源码。
`build-core.sh` 固定 `-androidapi=26` 和 `-javapkg=dev.hole.core`，生成 ARM32 / ARM64 AAR，
开启 Go strip 与 16 KiB ELF 对齐。Gradle 跟踪源码、锁文件、构建脚本和核心版本。

详细工具链和双依赖图测试见 [构建指南](../docs/BUILDING.md)。
