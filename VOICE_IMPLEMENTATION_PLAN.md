# 语音房 Go 侧实施计划

## 目标

在现有 `hole` 的 ICE / QUIC / TURN 传输上增加一个自动加入的语音房：同一个 `room` 只有一个语音房，启用语音的在线设备自动互相发现并建立媒体连接。

本计划供 AI 按阶段执行。每完成一个阶段，先运行该阶段的验证，再进入下一阶段。所有改动必须保留用户已有的未提交工作，不自动提交、推送、部署或安装。

## 已确定的产品语义

- `room` 同时是信令房间和语音房，不新增语音房间 ID。
- 不实现邀请、接受、拒绝、呼叫等待或忙线状态。
- 设备启用语音后，加入房间即成为语音参与者；本地静音只停止发送，不离开房间。
- 在线成员列表只展示启用语音的设备，同时分别显示信令在线、传输连接和媒体状态。
- 第一版采用设备之间的点对点 mesh。房间语音参与者默认上限为 8；现有 Worker 的 16 人房间上限保留给普通连接，不能直接作为语音上限。
- Go core 不访问麦克风、扬声器、`AudioRecord`、`AudioTrack`、ALSA 或其他设备 API。
- 平台层向 Go 提供标准 PCM 帧，Go 负责媒体管线、编码、网络传输、解码、抖动和码率控制。
- 网络只传 Opus 帧，不传原始 PCM，不做音频重传，不在断线后重放旧音频。

## 现有代码边界

重点修改范围：

- `core/config.go`：语音配置。
- `core/events.go`：房间成员、语音对端、媒体统计快照和事件。
- `core/engine.go`：语音运行时生命周期和快照合并。
- `core/ice_signal.go`：语音能力、房间状态和语音传输字段。
- `core/ice_coordinator.go`、`core/ice_peer.go`：语音对端的自动建立、重连和销毁。
- `core/peer_mux.go`：独立语音 datagram 通道及媒体反馈通道。
- 新增 `core/voice.go`、`core/voice_peer.go`、`core/voice_wire.go`、`core/voice_bitrate.go`，必要时拆分为更小的文件。
- `worker/worker.js`、`worker/worker_ice.mjs`：房间成员广播和语音设备配对。虽然本计划以 Go 为主，但没有 Worker 配合就无法获得可靠的在线成员列表或自动建立无 mapping 的语音传输。
- `mobile/`：第一阶段只保持 API 可兼容；平台 PCM 二进制桥接单独作为后续阶段，不能把音频帧塞进现有 JSON 事件桥。

普通 TCP / UDP mapping 必须继续使用现有路径，旧客户端和未启用语音的设备不能被要求实现语音。

## 总体架构

```text
平台 AudioSource
        |
        v
      PCM 帧
        |
        v
  core voiceRuntime
   |        |        |
   |        |        +-- 每个 peer 的反馈和自适应码率
   |        +----------- Opus 编码 / 解码、抖动缓冲、丢包隐藏
   +-------------------- 房间语音成员和 VoicePeer 生命周期
        |
        v
  muxPeer 的专用 Voice Datagram
        |
        v
平台 AudioSink
```

职责必须保持清晰：

- `core.Engine` 管理总生命周期、配置、事件和快照。
- `voiceRuntime` 管理本地 PCM、编码器、解码器、抖动缓冲、语音 peer 和媒体统计。
- `voicePeer` 管理一个远端设备的媒体收发、反馈、码率和连接重绑。
- `iceCoordinator` / `icePeer` 只管理认证后的网络路径。
- `muxPeer` 只提供经过授权的语音数据面，不理解麦克风设备。
- Worker 只负责房间成员状态、能力声明和传输授权，不转发语音业务数据。

建议使用 `voiceRuntime` 作为内部名称，避免把它误解为第二个独立的网络 `Engine`。不新增一个可以自行加入房间的公共 VoiceEngine 生命周期。

## 第一阶段：定义兼容的配置和公共数据模型

### 1. `VoiceConfig`

在 `core/config.go` 增加最小配置：

```go
type VoiceConfig struct {
    Enabled bool `yaml:"enabled" json:"enabled"`
}
```

将其加入 `Config`，默认 `false`。第一版不把采样率、声道数、Opus profile、网络包大小等实现常量暴露到用户配置，避免形成无法兼容的配置面。

保留现有严格解析、归一化和配置比较逻辑。语音开关变化应触发运行时创建或销毁，但不应破坏普通 mapping 的应用会话。

### 2. PCM 类型

在 `core/voice.go` 定义固定内部格式：

```go
type PCMFormat struct {
    SampleRate   int // 48000
    Channels     int // 1
    FrameSamples int // 960，20 ms
}

type PCMFrame struct {
    Sequence  uint64
    Timestamp uint64 // 单调采样时钟，不使用墙上时钟
    Samples   []int16
}
```

约束：

- 只接受 48 kHz、单声道、PCM16 little-endian 的 20 ms 帧。
- `len(Samples)` 必须等于 960；非法帧拒绝，不在热路径隐式重采样。
- 时间戳按采样数递增；序号按帧递增。
- 设备侧需要其他格式时，在平台适配器完成重采样或声道转换。

### 3. 平台 I/O 接口

先定义 Go 内部接口并用 fake 实现测试，不立即暴露为 gomobile 接口：

```go
type PCMSource interface {
    ReadPCM(context.Context, *PCMFrame) error
}

type PCMSink interface {
    WritePCM(context.Context, PCMFrame) error
}
```

接口不得携带设备句柄、JNI 类型、`[]byte` 的长期所有权或阻塞无限期的回调。平台桥接阶段再根据 Android / 桌面线程模型决定是 pull、批量 push 还是共享环形缓冲区。

## 第二阶段：房间成员和自动语音传输授权

### 1. join 能力声明

在新版本 join 消息中增加语音能力字段，例如：

```json
{
  "voice": true
}
```

字段必须是可选的。未声明或为 `false` 的旧设备继续按普通 mapping 工作，不会被加入语音 mesh。

### 2. 房间状态消息

不要把现有 `room_members: []string` 直接改成对象数组，因为旧 Worker 的字符串数组会导致 Go JSON 反序列化失败。新增版本化消息，例如：

```json
{
  "type": "room_state",
  "members": [
    {"device_name":"phone-a", "voice":true},
    {"device_name":"desktop-b", "voice":false}
  ]
}
```

Worker 在以下时机向房间成员发送完整快照：

- 新设备加入。
- 设备离开或 WebSocket 关闭。
- 同名设备重连替换旧连接。

Go 端只把当前 WebSocket 成员视为 `online`，不要根据 ICE 是否成功推测在线。在线、传输、媒体必须是三个独立状态。

### 3. 自动建立 voice-only transport

修改 `worker/worker_ice.mjs`：

- 对两个 `voice: true` 成员创建 pair record，即使 `mappings` 为空。
- `transport_ready` 增加 `voice: true`。
- 继续携带现有 runtime、证书指纹、transport generation、ICE 和 TURN 信息。
- 普通 mapping 配对逻辑保持不变。
- 对端离线时发送已有 `peer_state`，同时让 `room_state` 反映成员变化。
- 语音参与者超过 8 时，不再建立新的语音 pair，返回明确的房间容量错误；普通 mapping 不受影响。

修改 `core/ice_signal.go` 和 `core/ice_coordinator.go`：

- 解析并验证 `voice` 字段。
- 允许 `voice=true` 且 `mappings=[]` 的 `transport_ready`。
- 为 voice-only transport 创建 `icePeer`，但不创建普通 mapping channel。
- 证书指纹、runtime、generation 和 relay 策略验证必须与普通 transport 完全一致。

## 第三阶段：复用 mux 建立专用媒体数据面

### 1. 语音能力握手

在 `muxHello` 增加语音能力和协议版本，例如：

```go
Voice        bool `json:"voice,omitempty"`
VoiceVersion int  `json:"voice_version,omitempty"`
```

只有信令已授权、双方都声明语音、且 `VoiceVersion` 兼容时，才创建语音通道。普通 mapping 的 mux hello 与通道授权逻辑不能放宽。

### 2. 专用 Voice Datagram

不要复用普通 UDP mapping 的 `channel_id`、UDP session ID、分片重组器或应用 socket。增加独立 envelope：

```text
magic: 4 bytes
version: 1 byte
kind: media / feedback
sequence: uint32
timestamp: uint64
codec: opus
payload length
payload
```

实际字段可以采用现有二进制编码风格，但必须：

- 设置明确的最大包长，不能依赖 IP 分片。
- Opus 帧超出当前 `DatagramLimit` 时丢弃或重新编码，不能进行会增加延迟的媒体分片。
- 检查 magic、版本、kind、长度、序号和时间戳溢出。
- 语音帧通过 QUIC DATAGRAM 发送，不走可靠 stream，避免队头阻塞。
- 反馈使用低频独立控制消息，不能每个媒体帧发送一个反馈包。

### 3. mux API

给 `muxPeer` 提供内部语音接口，不把 `quic.Conn` 泄漏到 voiceRuntime：

```go
type voiceLink interface {
    SendVoiceDatagram([]byte) error
    ReceiveVoiceDatagram(context.Context) ([]byte, error)
    SendVoiceFeedback([]byte) error
    ReceiveVoiceFeedback(context.Context) ([]byte, error)
    Context() context.Context
}
```

语音发送必须由单独 writer goroutine 处理。调用方不能在 PCM 实时线程直接调用 QUIC 写操作。

## 第四阶段：voiceRuntime 和 voicePeer

### 1. 生命周期

`Engine` 在请求配置中 `Voice.Enabled` 时创建 `voiceRuntime`，但只在信令加入后、存在语音成员时启动媒体循环。

- `Start`：初始化 runtime，等待房间状态和 voice peer。
- `ApplyConfig` 关闭语音：停止采集、停止播放、关闭所有 voice peer、释放缓冲区。
- `Stop` / `Close`：先停止 PCM I/O，再停止编码器和网络 goroutine。
- 网络切换或 transport generation 变化：保留参与者列表，关闭旧媒体 link，清空旧帧，绑定新 link。
- 信令短暂断开：成员列表标记为未知或离线，禁止继续发送旧 peer 的媒体数据。

### 2. peer 生命周期

每个 `voicePeer` 包含：

- 远端设备名、transport ID、generation、link。
- 一个 Opus encoder 和 decoder，或按相同码率共享 encoder 的安全实现。
- 一个有界发送队列。
- 一个有界接收队列和 jitter buffer。
- 收发序号、丢包、乱序、延迟、抖动和最近反馈。
- 一个自适应码率控制器。

不要让 peer 直接管理房间成员；成员变化由 coordinator 统一 reconcile。

### 3. PCM 管线

发送方向：

1. 从 `PCMSource` 读取一个 20 ms PCM 帧。
2. 校验序号、时间戳和样本数。
3. 根据每个 peer 的目标码率编码成 Opus。
4. 写入该 peer 的有界媒体发送队列。
5. 由单独 writer 发送 datagram。

接收方向：

1. 从 voice link 收取 datagram。
2. 校验并按序号放入 jitter buffer。
3. 按播放时钟取出帧；缺帧时执行 Opus PLC 或丢弃策略。
4. 解码为固定 PCM 帧。
5. 写入 `PCMSink`。

混音策略必须明确：第一版采用单人优先/单远端播放，或定义固定的多人混音器；不能在代码中隐式覆盖远端 PCM。若实现多人混音，必须在 core 内完成并限制混音缓冲总量。

## 第五阶段：带宽和缓冲区效率约束

这里的带宽优化重点是避免缓冲区导致的额外等待、复制和重复发送，而不是只降低 Opus 的目标码率。

### 1. 时间预算优先于字节预算

所有实时队列以“帧数和毫秒数”为主指标，同时设置字节上限：

- 采集待编码队列：最多 3 帧，约 60 ms。
- 每个 peer 的编码待发送队列：最多 3 帧，约 60 ms。
- 接收 jitter buffer：目标 3～5 帧，约 60～100 ms；上限 8 帧，超过即丢弃最旧帧。
- 播放待写入队列：最多 3 帧，约 60 ms。
- 任意实时队列禁止超过 200 ms。

队列长度应进入语音统计和码率反馈，不能只记录字节数。

### 2. 丢帧策略

- 发送端拥塞时丢弃最旧待发帧，保留最新帧，不能让旧声音排队播放。
- 接收端延迟超过上限时跳过旧帧，快速回到当前时间。
- 语音帧永不重传；反馈包可以丢失，下一个反馈覆盖旧反馈。
- 断线重连时清空所有旧媒体队列和 jitter buffer。
- 静音期间不生成持续 PCM 媒体帧；允许低频发送状态或保活信息。

### 3. 内存和复制

- 使用固定容量 ring buffer 和可复用 frame slot，禁止在 20 ms 热路径中反复扩容 `[]byte`。
- 使用 `sync.Pool` 或专用 slab 管理 Opus payload，但必须有明确的归还路径；不得把池对象交给异步 goroutine 后失去所有权。
- 平台输入 PCM 只复制一次进入 core；编码器读取后不能为每个 peer 复制整份 PCM。
- 多 peer 场景下，共享只读 PCM frame，并为每个 peer 单独编码；若实现编码结果复用，使用引用计数或明确的所有权转移。
- 不把 PCM、Opus payload 或统计对象放入无界 channel。
- 反馈按 500 ms～1 s 聚合，禁止每帧创建 JSON 或单独分配反馈对象。

### 4. 发送调度

- 媒体 writer 不能等待普通 mapping 数据填满批次，也不能为了合并包增加一个播放周期的延迟。
- 语音包保持一帧一个 datagram；只有在协议明确支持且不增加等待时才允许批量反馈。
- 如果现有 mux 的普通 UDP 发送会长期占满 QUIC DATAGRAM 发送机会，增加 mux 内部调度器，让语音队列拥有高优先级，同时限制语音占用比例，避免普通 mapping 完全饥饿。
- 第一版必须记录 voice send queue delay、queue drops、datagram drops 和实际发送间隔，用集成测试验证。

### 5. Opus 和包大小

- 默认 20 ms、单声道、12～32 kbps。
- 开启 Opus DTX；是否启用 in-band FEC 由丢包反馈控制，但不能把 FEC 当作重传。
- 编码结果必须小于当前路径 datagram 上限，预留 envelope 开销。
- 不能依赖 QUIC / IP 分片来承载语音。
- 码率调整不应重建连接或重置序号；只更新后续编码帧。

## 第六阶段：自适应码率

### 1. 每 peer 独立控制

每个发送方向维护一个 `bitrateController`。初始参数：

- `min = 12000`
- `start = 24000`
- `max = 32000`
- 反馈间隔：500 ms～1 s
- 最短码率保持时间：2 s

如果 CPU 或构建约束暂时不允许每 peer 独立 encoder，可以先使用房间统一码率，但必须把接口设计成未来能切换为每 peer 码率；不能把统一码率写死在 wire 层。

### 2. 反馈指标

接收端反馈：

- 最高连续收到序号和丢包区间。
- 最近窗口丢包率、乱序率和 late frame 数。
- jitter buffer 当前深度和超限丢帧数。
- 最近接收时间戳和播放延迟。
- 可选 RTT；优先使用现有 ICE transport RTT。

### 3. 调整规则

先实现可测试的阈值控制：

- 丢包率大于 8%，或 jitter buffer 连续超过目标：码率乘以 0.8。
- 丢包率大于 15%，或连续出现 datagram too large：直接降到当前值与最低值中较大者。
- 丢包率小于 2%、队列稳定且持续至少 5 秒：码率增加 10%。
- 每次调整后至少保持 2 秒，避免上下抖动。
- 所有结果限制在 `[min, max]`，并记录调整原因。

控制器必须是纯 Go、无网络依赖、可用固定时间和模拟反馈进行单元测试。

## 第七阶段：快照、事件和移动桥接

### 1. Snapshot

在 `Snapshot` 增加：

- 当前语音房成员完整快照。
- 本机语音是否启用、是否静音、媒体状态。
- 每个 voice peer 的连接状态、路径、RTT、码率、丢包、队列丢帧。

所有 64 位计数继续使用现有 JSON decimal string 约定。成员和 peer 列表必须稳定排序，避免 UI 因 Map 顺序发生无意义刷新。

### 2. Event

增加结构化事件：

- `room`：房间成员集合变更。
- `voice_peer`：语音 peer 建立、暂停、恢复、关闭或失败。
- `voice_stats`：低频统计更新，不要每个媒体帧发布事件。

事件仍然是提示，权威状态必须来自 `Snapshot`。事件队列满时丢事件不能丢状态。

### 3. mobile 窄接口

第一阶段不通过现有 JSON event sink 传 PCM。后续平台桥接应提供：

- 初始化或注册 PCM source/sink。
- 开始/停止语音输入输出。
- 本地静音切换。
- 批量 PCM push/pull 或 direct buffer 机制。

每 20 ms 调用一次 Java/Kotlin 方法会产生不必要的 JNI 调度和内存复制；优先使用批量帧或有界共享环形缓冲区。平台适配器负责麦克风权限、音频焦点、回声消除、降噪、自动增益和蓝牙路由。

## 第八阶段：测试计划

### 单元测试

新增至少以下测试：

- `core/voice_wire_test.go`：合法帧、非法长度、版本、序号和时间戳。
- `core/voice_buffer_test.go`：ring buffer 上限、丢旧帧、清空、时间预算。
- `core/voice_jitter_test.go`：乱序、丢包、延迟超限、PLC 触发。
- `core/voice_bitrate_test.go`：丢包上升降码率、稳定网络升码率、滞后和边界。
- `core/voice_test.go`：PCM 格式、静音、重连和 peer 生命周期。
- `core/peer_mux_test.go`：voice-only mux、普通 mapping 与 voice 并存、授权失败。

### 集成测试

- 两个设备只启用语音、不配置普通 mapping，能够建立 voice transport。
- 三个以上设备加入同一房间，成员列表和 peer 集合正确。
- 新设备加入和旧设备离开时，所有已在线设备收到一致的 `room_state`。
- 普通 mapping 与语音并存时，语音不会被 mapping channel 错误消费。
- 模拟 5%、10%、15% 丢包和突发延迟，码率按规则调整，发送队列不超过上限。
- 网络切换、TURN 中继和 transport generation 变化时，旧媒体帧被丢弃且新帧恢复。
- 房间达到语音参与者上限时只拒绝新的语音 peer，不影响普通 mapping。
- 旧 Worker 或未启用语音的对端仍可使用原有 TCP / UDP mapping。

### 仓库验证

按仓库约定执行：

```bash
source scripts/build-env.sh
bash -n build.sh scripts/build-env.sh android/scripts/build-core.sh android/scripts/setup-toolchain.sh
python3 -m unittest discover -s scripts -p 'test_build.py'
python3 -m unittest discover -s android/scripts -p 'verify_apk_test.py'
go test -race -count=1 ./...
go vet ./...
node --test worker_ice.test.mjs
git diff --check
(cd android/corebridge/gobuild && go test -race -count=1 hole/core hole/mobile && go vet hole/core hole/mobile)
```

Worker 修改后必须补充 `worker/worker.test.mjs` 或现有测试文件，覆盖房间成员广播、语音能力配对、语音容量和旧消息兼容。

## 分阶段执行顺序

### P0：协议和模型，不接真实音频

- [ ] 增加 `VoiceConfig`、PCM 类型、成员和统计结构。
- [ ] 增加 join / room_state / transport_ready 的语音字段解析和校验。
- [ ] 增加 fake voice peer 与 snapshot/event 测试。
- [ ] 不改变普通 mapping 行为。

### P1：Worker 成员和 voice-only transport

- [ ] Worker 广播完整房间成员状态。
- [ ] Worker 为语音成员建立 pair record。
- [ ] Go 接受无 mapping 的 voice transport。
- [ ] 完成在线、connecting、active、paused、offline 状态收敛。

### P2：Voice Datagram 和 PCM 管线

- [ ] 完成 mux voice capability handshake。
- [ ] 完成独立 datagram envelope。
- [ ] 完成有界发送队列、接收 jitter buffer 和清空规则。
- [ ] 接入可构建于 CLI 和 gomobile 的 Opus 实现；若依赖需要 CGo，先暂停接入并解决三目标构建策略。
- [ ] 用 fake PCM source/sink 完成端到端测试。

### P3：自适应码率和调度

- [ ] 完成 feedback 聚合和发送。
- [ ] 完成 per-peer bitrate controller。
- [ ] 完成 voice 优先调度和 queue/drop 统计。
- [ ] 完成丢包、延迟、重连、TURN 集成测试。

### P4：平台桥接

- [ ] Android 实现 AudioRecord / AudioTrack 适配器。
- [ ] 增加麦克风权限、前台服务 microphone 类型、音频焦点和蓝牙路径。
- [ ] 桌面实现对应 PCM source/sink，或明确暂不支持桌面采集。
- [ ] 通过批量 PCM 或共享 ring buffer 接入 mobile 窄接口。

### P5：文档和交付

- [ ] 更新根 README、`core/README.md`、`docs/PROTOCOL.md`、`mobile/README.md` 和 Android 文档。
- [ ] 更新配置示例和协议版本说明。
- [ ] 完成全量仓库验证。
- [ ] 产物只放 `dist/`，不覆盖旧包，不提交 `.cache/`、`dist/` 或本地签名文件。

## 验收标准

以下条件全部满足后，才认为 Go 侧第一版完成：

1. 启用语音的设备加入同一个 room 后，其他语音设备能在 Snapshot 中看到它，离开后能收敛为 offline。
2. 不配置任何 TCP / UDP mapping 时，两个语音设备仍能建立经过身份认证的 voice-only transport。
3. PCM 输入输出完全通过平台接口，core 没有操作系统音频设备调用。
4. 网络线上只有 Opus 媒体帧和低频反馈，没有原始 PCM。
5. 任一实时队列都有限制，端到端额外排队延迟不会无限增长；重连后不会播放旧帧。
6. 模拟丢包和延迟时，码率能在限定范围内调整，且不会出现频繁上下抖动。
7. 普通 TCP / UDP mapping、ICE 重连、TURN 和网络切换回归测试全部通过。
8. 旧的非语音设备仍能按原协议运行，未启用语音的配置不增加媒体线程、连接或缓冲区。

## 非目标

不实现：

- 邀请、接受、拒绝、来电通知、忙线和通话记录。
- 视频、屏幕共享、录音、回声服务器或语音转文字。
- 浏览器 WebRTC 互通。
- Worker 侧 SFU 或服务器混音。
- 16 人规模的高质量 mesh 保证。
