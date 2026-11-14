# Android Go 桥接 API

`hole/mobile` 是共享核心的 gomobile 窄接口，当前 **API version 1**。
生成的 Java 包为 `dev.hole.core.mobile`；构建入口是 `./build.sh android` 或 `./build.sh wear`。
本目录不复制网络协议或转发实现。

[接口](#接口) · [手动 NAT 检测](#手动-nat-检测) · [NetworkBinding](#networkbinding) · [配置交换与构建](#配置交换与构建) · [Android 客户端](../android/README.md)

| 约定 | 内容 |
| --- | --- |
| API 版本 | `1` |
| Java 包 | `dev.hole.core.mobile` |
| 创建实例 | 不联网；Android 注入 `NetworkBinding` |
| 生命周期 | `stop` 可重复并等待清理，`close` 最终关闭 |
| 状态来源 | 结构化事件通知 + JSON 快照；不包含凭据 |

## 接口

```java
Mobile.version();
Engine engine = Mobile.newEngineWithNetworkBinding(sink, binding);
engine.start(requestJSON);
engine.applyConfig(requestJSON);
engine.snapshotJSON();
engine.natPunchProcessJSON(transportId, afterSequence);
engine.networkChanged(eventJSON);
engine.renominateTransports();
engine.startNATProbe(optionsJSON);
engine.cancelNATProbe();
engine.stop();
engine.close();
```

Android 使用带 `NetworkBinding` 的构造方法；旧 `newEngine` 和 `newEngineWithNetworkProvider` 入口保留兼容。
创建实例不联网；`stop` 可重复调用并等待清理，`close` 为最终关闭。

### 手动 NAT 检测

手动 NAT 检测无需房间配置。`startNATProbe` 异步接受 `stun_urls`、`nat_stun_urls`、
`interface_allowlist` 三个可选字段的 JSON 对象（最多 16 KiB）；服务列表的 null / 空数组语义与 ICE 一致。
结果在 `snapshot.nat_diagnostic`，包括状态、起止时间、原始分类、网络信息和带字段说明的分组。
事件 `nat_diagnostic` 在停止转发时也能唤醒宿主；显式 `stop` 取消检测并抑制旧回调，之后可再次手动检测。
Android 检测的 DNS 和 UDP 均绑定当前物理 Network，切网使旧结果失效。

### 打洞过程

`natPunchProcessJSON(transportId, afterSequence)` 增量读取一个设备对的打洞过程，`afterSequence` 为上一轮返回的
十进制 `sequence` 字符串，首次传 `"0"`。结果独立于普通运行快照，包含连接尝试、NAT 模型、校准计划、socket
状态及新事件；历史尝试的 `discovery` 包含当时关联的自动 NAT 采样记录。
`sequence`、`generation`、毫秒时间与事件移出 / 未记录计数使用十进制字符串，其他报文计数使用 JSON 数值。
读取不启动连接。宿主在页面可见时轮询，按序号合并事件；检测到代次起始时间或序号重置时从 `"0"` 重新读取。
页面回放使用事件中的 `round_snapshot` 恢复对应时间的模型与端口计划。

### 运行请求

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

### 语音桥接与统计

- `config.voice.enabled` 可选且默认关闭。语音 PCM 不进入 JSON 请求或事件桥：`PushVoicePCM`
  每次接收最多 4 个连续的 PCM16 帧，`PullVoicePCM` 每次返回最多 4 帧，`SetVoiceMuted` 控制本地发送。
  桥接队列可容纳完整 4 帧批次；Android corebridge 按单个 20 ms 帧连接 `AudioRecord` / `AudioTrack`。
- 语音快照的本机成员使用 `local: true` / `transport_state: local`；`capture_state` 与
  `captured_frames` 表示核心收到的本机 PCM。`mixed_frames` 表示写入核心输出桥的帧数，
  不代表设备已播放。线路的 `state` 表示传输，`media_state` 根据最近 2 秒实际发送、解码
  区分 `waiting` / `sending` / `receiving` / `active`；`sent_frames`、`received_frames`、
  `decoded_frames`、`concealed_frames` 分别记录成功发送、收到、解码与 PLC / 静音补帧。
  这些 64 位计数仍使用十进制字符串。字段为 API 1 的兼容扩展，旧快照缺失时不得推断媒体活跃。
### 事件、错误与版本

- 错误以含 `code`、`message` 的 JSON 传出。状态快照与事件不包含凭据。
- 快照中的 `core_version` 是统一维护的核心版本号；`core_build_id` 是提交及共享核心源码/依赖摘要，用于定位具体构建。
- `EventSink.onEvent` 在 Go 后台线程执行，应及时返回；生命周期命令调度到其他线程。
- Android 只接收结构化事件，桥接层过滤原始日志；`stop` 返回后不再交付旧回调。
- `renominateTransports()` 只请求对已有 ICE active 路径建立新代次；系统网络变化仍用
  `networkChanged()`。

## NetworkBinding

`NetworkJSON.gateway` 来自所选 Network 的 LinkProperties 默认 IPv4 路由；映射流量与 DNS 继续经过该 Network，不回退到默认网络。

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

| 配置 | 交换与兼容行为 |
| --- | --- |
| `ice.port_mapping` | 默认 `auto`，可设为 `off` |
| `ice.nat_stun_urls` | 保留 null / 空数组语义；CLI 交换与备份共用核心字段 |
| `relay.device` / `relay.order` | 原样进入核心和 CLI 交换，区分省略与显式空顺序 |
| `relay.serve.enabled: true` | 导入和启动均明确返回“仅 CLI 支持”，不会静默删除 |

快照包含 Hole 路径设备、公布入口、实际接入地址和失败原因。

`Mobile.decodeCLIConfig` / `Mobile.encodeCLIConfig` 使用共享解析器，CLI YAML 包含 `server_url`。
完整应用备份由 Kotlin 管理，包含停用条目等宿主字段，不复制 Android Keystore 密文。

`android/corebridge/gobuild` 是独立绑定模块，锁定 x/mobile 及所需依赖并通过 `replace hole` 引用当前源码。
`build-core.sh` 固定 `-androidapi=26` 和 `-javapkg=dev.hole.core`，生成 ARM32 / ARM64 AAR，
开启 Go strip 与 16 KiB ELF 对齐。Gradle 跟踪源码、锁文件、构建脚本和根目录 `versions.json` 中的核心版本。

详细工具链和双依赖图测试见 [构建指南](../docs/BUILDING.md)。
