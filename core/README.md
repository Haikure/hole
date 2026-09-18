# 共享 Go 核心

`hole/core` 供 CLI 与 Android 共用，实现信令、ICE / QUIC、TCP / UDP 映射及可恢复应用会话。
本包不读取配置文件、不解析命令行参数，不安装进程信号处理器，也不退出宿主进程。

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
| `Stop` | 取消网络活动并等待资源清理，实例可再次启动 |
| `Close` | 最终关闭实例和事件通道 |

配置应用按完整配置内容比较；相同配置幂等，变更配置由 supervisor 串行重配。
快照复制内部对象且不包含配置凭据；事件通道有界，宿主通过快照核对最新状态。

## 平台与恢复

`Platform` 区分外部候选、信令、上游连接和本地监听。
默认实现使用桌面 Go 网络 API；Android 注入系统 Network、DNS 和 socket 绑定，
并通过 `Options.RetryNetwork=true` 等待初始网络恢复。

网络路径重建保留未改变的监听器、应用 socket、重放窗口和进程证书。
删除映射、修改目标或运行身份、用户停止、进程退出会结束对应作用域的会话。
协议格式、重放和资源限额见 [协议说明](../docs/PROTOCOL.md)。

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
`CoreVersion` 由统一构建脚本注入提交标识与源码/依赖摘要。
