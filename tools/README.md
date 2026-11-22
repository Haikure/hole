# NAT 探测工具

`natprobe` 使用 UDP STUN 查看 NAT 映射、过滤行为、公网地址和端口变化趋势，与客户端共用探测实现。

[快速运行](#快速运行) · [参数与输出](#参数与输出) · [结果含义](#结果含义) · [Termux 构建](#termux-构建) · [项目首页](../README.md)

## 快速运行

```bash
source scripts/build-env.sh
go run ./tools/natprobe
```

默认探测 IPv4，包含 DNS 和采样的总时限为 20 秒。域名优先使用系统 DNS，失败时尝试内置备用 DNS `223.5.5.5:53`；
也可通过 `-stun` 指定可达的 IPv4 地址。

## 参数与输出

| 参数 | 默认 / 约束 |
| --- | --- |
| `-stun` | 10 个默认 STUN 端点；首次指定替换默认列表，可重复或逗号分隔，最多 16 个 |
| `-timeout` | 默认 `20s`，包含 DNS 与探测 |
| `-json` | 输出 JSON；省略时输出文本 |

```bash
go run ./tools/natprobe \
  -stun stun:stun.example.net:3478 \
  -stun stun:stun2.example.net:3478 \
  -timeout 20s \
  -json
```

省略 `stun:` 前缀时会自动补上。过滤分类需要支持 RFC 5780 的可达 STUN 服务。

JSON 顶层包含 `local_socket`、`stun_servers`、`mapped_addresses` 和 `nat`；以下分类字段位于 `nat`：

| `nat` 字段 | 含义 |
| --- | --- |
| `status` / `type` | 探测状态和 NAT 类型 |
| `mapping_behavior` / `filtering_behavior` | 映射与过滤行为 |
| `port_preservation` / `no_pat` | 是否保持本地端口、是否符合疑似 No-PAT 特征 |
| `predictability` / `reason` | 端口变化趋势及证据说明 |
| `ip_evidence` | 多公网 IP 的分组证据 |
| `port_step` / `public_ip` / `last_mapping_port` / `allocation_scope` | 证据成立时提供的预测模型 |

## 结果含义

映射、过滤、端口保持和可预测性分别判断，缺少证据时保留未知。
比较工具与图形客户端的结果时，应核对网络、STUN 列表和探测时间；核心快照最多缓存 5 分钟，切网后重新探测。

### NAT 类型与地址池

映射稳定时结合过滤结果区分全锥型、地址受限型和端口受限型；过滤未知时显示端点无关映射。
确认端口随目标变化时归为对称型。

多个公网 IP 按 IP 分组判断，某组证据充分时可报告对应类型；各组均不足时显示“地址池”。
`complete` 表示此次观测完成，不能据此认定已区分锥形与对称型。

### 端口保持与 No-PAT

| 字段 | 值 |
| --- | --- |
| `port_preservation` | `preserved` 全部保持本地端口；`translated` 全部转换；`mixed` 两者均有；`unknown` 证据不足 |
| `no_pat` | `suspected` 疑似只转换 IP；`not_detected` 观察到端口转换；`not_applicable` 未检测到地址转换；`unknown` 证据不足 |

端口保持与跨目标端口稳定是两件事。例如本地端口 12345、全部公网端口为 40000，属于稳定但未保持。
保留端口的 PAT 也可能产生类似 No-PAT 的样本，因此只能报告疑似。

### 可预测性

“可能递增 / 递减”与经过跨 socket 验证的“可预测”分别显示。
多公网 IP 的趋势逐组记录，某个出口的规律不代表整个地址池。所有结论基于有限样本，不能保证后续端口或直连成功。

分类条件、采样上限和预测模型详见 [协议说明](../docs/PROTOCOL.md#分类与预测证据)。

## Termux 构建

在仓库根目录交叉编译 ARM64 静态工具：

```bash
source scripts/build-env.sh
mkdir -p dist/tools
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -mod=readonly -trimpath \
  -ldflags='-s -w' -o dist/tools/natprobe-linux-arm64 ./tools/natprobe
```

复制到 Termux 私有目录后，执行 `chmod +x ~/natprobe` 和 `~/natprobe -json`。
本机编译可省略 `GOOS` / `GOARCH`，保留 `CGO_ENABLED=0`。
