# 共享 Go 核心

`relay.device` / `relay.order` 提供 Hole 设备中继配置，`relay.serve` 仅由根 CLI 显式能力
`Options.AllowRelayServe` 开放。固定 UDP socket 由单一分流器读取；Linux 批量后端和其他
平台数据报后端共用授权、限速、公平队列和统计。`Snapshot.Relay` 为服务端统计，
`PeerTransports` 包含实际中继设备、公布入口与接入地址。配置及三方生命周期见
[Hole 协议](../docs/PROTOCOL.md#hole-设备中继)。mobile、桌面与插件只消费该共享实现。

`hole/core` 供 CLI 与 Android 共用，实现信令、ICE / 固定入口 QUIC、TCP / UDP 映射及可恢复应用会话。
本包不读取配置文件、不解析命令行参数，不安装进程信号处理器，也不退出宿主进程。

`transport.fixed_endpoint` 在 `auto` / `ice` 配置下提供固定 UDP 监听与公布地址。
对端无需填写连接地址；新版核心自动处理 Worker 下发的 `fixed-quic-mux-v1`，复用平台网络
绑定、DNS、mux、会话恢复与限额。固定设备对失败只重试固定入口，不启动 ICE / TURN。
Android / Wear 重打包新版 AAR 后具备自动拨号能力，固定入口配置界面后续接入。
配置和部署顺序见 [固定入口直连](../docs/PROTOCOL.md#固定入口直连)。

配置中的 `voice.enabled` 默认关闭。开启后，核心只接受平台提供的 48 kHz、单声道、PCM16
20 ms 帧（每帧 960 个采样），通过同一 room 的 voice-only ICE / QUIC 传输发送 Opus 帧。
`PCMSource` / `PCMSink` 是内部接口；gomobile 使用 `PushVoicePCM` / `PullVoicePCM` 批量方法
传递 PCM，不经过 JSON 事件桥，每次最多处理 4 帧。Android corebridge 通过 `AudioRecord` /
`AudioTrack` 接入设备。语音成员、传输和媒体统计从 `Snapshot.voice` 读取，事件只作刷新提示。
语音会话绑定具体 ICE 连接实例，旧线路退出不会解绑已替换的新线路（包括同代次重连）。
语音与转发共用传输时，信令重连只替换数据路径，未变更的 TCP / UDP 应用会话继续恢复。
关闭语音会取消媒体及反馈发送；发送队列拥塞时关闭旧 QUIC 路径来解除阻塞，不等待网络恢复。
采样时间戳与发送包序号独立，静音间隔保留在播放时钟中，不计入网络丢包；码率按最近反馈区间调整。
缺包保留最多 8 个包序号、160 ms 的乱序等待窗口，窗口内补齐不计丢包；反馈只覆盖已经确认的序号区间。

## 宿主接入

```go
func run(ctx context.Context, data []byte) error {
    cfg, err := core.ParseConfig(data)
    if err != nil {
        return err
    }
    serverURL := cfg.ServerURL
    cfg.ServerURL = "" // 运行请求外层是服务器地址的唯一来源

    engine := core.NewEngine(core.Options{})
    defer engine.Close()
    if err := engine.Start(core.Request{
        ServerURL: serverURL,
        Config:    cfg,
    }); err != nil {
        return err
    }
    return engine.Wait(ctx)
}
```

示例函数使用 `context` 和 `hole/core`。界面宿主通过 `Events()` 接收事件，并用 `Snapshot()` 读取当前状态。

| API | 行为 |
| --- | --- |
| `ParseConfig` / `Config.Validate` / `Request.Validate` | 严格解析与同步校验，无网络副作用 |
| `NewEngine` | 创建独立实例，不联网 |
| `Start` | 校验并复制请求，异步开始连接；请求已接受与网络已就绪分开 |
| `ApplyConfig` | 停止态暂存，运行态由 supervisor 串行重配 |
| `NetworkChanged` | 更换外部网络路径，保留符合恢复条件的应用会话 |
| `RenominateTransports` | 对已有 ICE active 路径发起新代次；无可用路径时是幂等 no-op |
| `Stop` | 取消网络活动并等待资源清理，实例可再次启动 |
| `Close` | 最终关闭实例和事件通道 |

配置应用按完整配置内容比较；相同配置幂等，变更配置由 supervisor 串行重配。
快照复制内部对象且不包含配置凭据；事件通道有界，宿主通过快照核对最新状态。

ICE / TURN 底层诊断复用 `kind=log` 事件，附带 `peer`、`phase`、`transport_generation` 和
表示日志来源的 `stage`。Pion warning/error 使用 `state=warning/error` 与 `message`；
尝试最终失败使用 `state=failed` 与保留原始原因的 `error`。普通快照及传输事件继续使用面向用户的错误提示，
CLI 仅在 `-debug` 下展示这些诊断。每次尝试最多输出 64 条底层日志及一条省略提示，最终失败原因不占该额度。
产生事件前按本次尝试的配置、动态 TURN 凭据和 ICE 凭据脱敏，再进入 Engine 的统一脱敏流程；
不受 `PION_LOG_*` 环境变量影响，不启用 info/debug/trace 报文日志。

## 平台与恢复

`Platform` 区分外部候选、信令、上游连接和本地监听。
默认实现使用桌面 Go 网络 API；Android 注入系统 Network、DNS 和 socket 绑定，
并通过 `Options.RetryNetwork=true` 等待初始网络恢复。

网络路径重建保留未改变的监听器、应用 socket、重放窗口和进程证书。
删除映射、修改目标或运行身份、用户停止、进程退出会结束对应作用域的会话。
协议格式、重放和资源限额见 [协议说明](../docs/PROTOCOL.md)。

TCP 重放数据按需分块分配，单方向上限 16 MiB，同一运行的所有映射共用 128 MiB 块容量预算。
性能基准可执行 `go test ./core -run '^$' -bench 'Benchmark(ICETCP|TCPReplayRTT100ms|UDPReassembly)' -benchmem`。
`BenchmarkICETCP` 使用真实本地 TCP → ICE / QUIC → TCP，两端共享本机 CPU，预热后测持续转发。
TCP 基准使用有固定延迟的内存链路，仅衡量应用重放协议，不代表实际 TURN、QUIC 或设备网络带宽。

## 构建与测试

```bash
# 仓库根目录
./build.sh cli
source scripts/build-env.sh
go test -race -count=1 ./...
go vet ./...
```

根 `go.mod` 与 `android/corebridge/gobuild/go.mod` 分别锁定 CLI 和绑定依赖图；
两者共用本包源码及 [anet 兼容模块](compat/anet/README.md)。
`CoreVersion` 从仓库根目录 `versions.json` 注入，CLI、独立 `desktop-core` 和 Android AAR 共用该值。构建脚本另注入由提交标识及核心源码/依赖摘要生成的 `CoreBuildID`，供 APK 检查和构建追踪使用。
