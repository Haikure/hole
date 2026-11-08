# NAT 探测工具

`natprobe` 使用 UDP STUN 探测 NAT 映射和过滤行为，并根据多个目标返回的公网端口判断端口是否稳定、递增或递减：

[快速运行](#快速运行) · [参数与输出](#参数与输出) · [Termux 构建](#termux-构建) · [分类规则](#分类规则) · [端口特征](#端口特征) · [项目首页](../README.md)

## 快速运行

```bash
source scripts/build-env.sh
go run ./tools/natprobe
```

| 可以查看 | 结果边界 |
| --- | --- |
| NAT 映射与过滤行为 | 过滤分类依赖 RFC 5780 支持；证据不足保留未知 |
| 端口稳定、保持与变化趋势 | 描述本次样本，不保证后续端口或直连结果 |
| No-PAT 特征 | 只能报告疑似，不能仅凭 STUN 确认设备配置 |
| 逐服务地址与样本 | 可与图形客户端的 STUN 服务明细核对 |

<details>
<summary>IPv4 目标选择与 DNS 回退</summary>

域名解析与核心默认网络共用 `core/netresolve`：先使用系统 DNS，失败或 2 秒内未完成时，使用内置 `223.5.5.5:53` 备用 DNS（纯 Go，支持 UDP 及截断响应后的 TCP 重试）。备用查询同样最多 2 秒，所有解析仍受 `-timeout` 总时限约束；IPv4 字面地址直接使用，不查询 DNS。静态工具不依赖 libc 或 Termux 的系统 DNS 配置。Android 应用中的网络绑定解析仍使用所选 Network 的 DNS。

核心 NAT 探测与工具都明确选择 IPv4 目标。Android 从所选网络返回的全部有效地址中选择 IPv4，不因首条为 IPv6 而跳过整个服务。图形客户端的“STUN 服务明细”列出每项配置的解析地址、有效基础样本、不同映射和失败原因，便于与工具使用的目标逐项核对。

</details>

## 参数与输出

| 参数 | 默认 / 约束 |
| --- | --- |
| `-stun` | 6 个默认 STUN 端点；首次指定替换默认列表，可重复或逗号分隔，最多 8 个 |
| `-timeout` | 默认 `20s`，包含 DNS 与探测的总时限 |
| `-json` | 输出 JSON；省略时输出文本 |

工具与核心共用 `core/natprobe`。每个端点最多采集 3 轮基础映射响应；过滤分类需要支持 RFC 5780 `OTHER-ADDRESS` / `CHANGE-REQUEST` 的可达服务。

自定义示例：

```bash
go run ./tools/natprobe \
  -stun stun:stun.example.net:3478 \
  -stun stun:stun2.example.net:3478 \
  -timeout 20s \
  -json
```

省略 `stun:` 前缀时工具会自动补上，例如 `-stun stun.example.net:3478`。

| JSON 内容 | 说明 |
| --- | --- |
| 基础诊断 | NAT 类型、映射 / 过滤行为、端口保持、No-PAT 判断 |
| 探测证据 | 基础映射有效样本数、过滤测试端点、原 socket 观测到的映射地址 |
| 递增模型 | `port_step`、`public_ip`、`last_mapping_port`、`allocation_scope`（`same_socket` / `cross_socket`） |

跨 socket 证据使用三个额外 socket 检查步长余数和重传映射复用，失败则降级。模型供核心预测打洞使用，有限样本不保证后续分配规律。

## Termux 构建

可在仓库根目录单独交叉编译供 ARM64 Termux 使用的静态工具，无需 Android SDK / NDK：

```bash
source scripts/build-env.sh
mkdir -p dist/tools
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -mod=readonly -trimpath \
  -ldflags='-s -w' -o dist/tools/natprobe-linux-arm64 ./tools/natprobe
```

将文件复制到 Termux 的私有目录（例如 `~/natprobe`）后，执行 `chmod +x ~/natprobe` 和 `~/natprobe -json`。本地编译可省略 `GOOS` / `GOARCH`，保留 `CGO_ENABLED=0`。若网络无法访问系统及备用 DNS，可通过 `-stun` 指定可达的 IPv4 地址。

## 分类规则

<details>
<summary>映射分类、多公网 IP 与过滤测试的证据要求</summary>

该工具调用共享的 `core/natprobe` 包，使用根 `go.mod` 中的 Pion STUN 库。核心后台诊断与工具默认均采用 20 秒总时限。比较结果时应使用相同的 STUN 列表和网络，并核对探测时间；后台快照最多缓存 5 分钟，网络变化后重新探测。映射分类只采用原 socket 向不同目标发送普通 Binding 请求得到的响应。检测到多个公网 IP 时，先按公网 IP 分组再分类；组内同一目标的多轮映射不一致时，该组保留未知。确认端点无关映射需要组内至少两个不同目标 IP，备用地址发回的 `CHANGE-REQUEST` 响应不算第二个映射目标。

多公网 IP 可能来自运营商地址池。只要某个 IP 组内样本足以分类，就采用该组的 NAT 类型和映射行为：映射稳定时结合过滤结果归为全锥型、地址受限型或端口受限型，过滤未知时归为端点无关映射；任一组确认端口随目标变化时，优先归为对称型。同一目标轮换公网 IP 不会单独导致未知，但每组仍需满足上述证据要求；每个 IP 仅有一个目标、或所有组内映射均不稳定时，类型仍为未知。`reason` 会说明地址池可能性和采用的公网 IP。分组判断不代表已确认运营商配置，过滤行为仍来自独立的过滤探测。

过滤分类为每个候选服务新建 socket，只访问其主端点后进行 `CHANGE-REQUEST` 测试，避免基础映射探测预先打开过滤权限。服务需提供 IP 和端口均不同的有效 `OTHER-ADDRESS`。每次 Binding 请求在单次 1.2 秒和剩余总时限内最多发送 3 次，重传复用事务 ID；总时限耗尽、服务忽略扩展或备用地址连通性无法确认时，过滤行为保留未知。重传只能降低丢包误判概率，备用路径差异和持续丢包仍可能影响结果。未检测到地址转换时也保留实际过滤行为：仅确认端点无关过滤时报告 `open`，观察到受限过滤时报告 `firewall`；过滤未知时报告端点无关映射，不推断网络开放。

</details>

## 端口特征

### 端口保持

`nat.port_preservation` 将原 socket 的有效普通 Binding 样本与本地端口比较，和跨目标端口稳定性分别报告：

| 值 | 条件 |
| --- | --- |
| `preserved` | 全部与本地端口相等 |
| `translated` | 全部与本地端口不同 |
| `mixed` | 相等与不同的样本均存在 |
| `unknown` | 没有有效样本或本地端点未知 |

例如本地端口 `12345`、所有目标均返回公网端口 `40000`，属于稳定但未保持。单个样本即可描述观测到的端口保持或转换，映射类型仍可能因目标不足而未知。

### No-PAT

No-PAT（Basic NAT）只转换 IP，不进行端口转换。`nat.no_pat` 是独立的特征判断，不替代映射类型或过滤类型：

| 值 | 判断依据 |
| --- | --- |
| `suspected` | 本地端点已知，至少两个不同目标 IP 的有效基础样本均映射到同一个外部 IP 和端口，同一目标的多轮映射稳定，且只有 IP 改变、端口全部保持。文本显示“疑似 No-PAT”。 |
| `not_detected` | 至少一个有效基础样本的公网端口不同于本地端口，已观测到端口转换，不符合当前路径上的纯 No-PAT。 |
| `not_applicable` | 满足上述多目标稳定映射条件，且映射 IP 和端口均与本地一致，未检测到地址转换。 |
| `unknown` | 未观测到端口转换，且本地端点未知、有效目标不足或公网映射不一致，无法进一步判断。 |

保留端口的 PAT 也能产生与 No-PAT 相同的 STUN 样本，因此 `suspected` 不能确认设备配置为 No-PAT，也不能排除多层 NAT。判断只描述当前 socket 已观测到的路径行为，不保证其他本地端口或未来连接的行为。

### 稳定性与预测趋势

<details>
<summary>统计范围、跨 socket 验证与递增示例</summary>

端口稳定性按原 socket 的全部公网映射端口判断：即使映射到多个公网 IP，只要观测到的端口一致，仍报告端口稳定；仅组内端口稳定、不同组的端口不同时，不报告整体端口稳定，也不拼接多个公网 IP 的端口推断可预测趋势。端口保持、No-PAT 判断和样本数继续采用全部有效基础样本，不因某个 IP 组能分类而改变统计范围。新 socket 样本只参与端口趋势验证，不改变原 socket 的映射分类、端口稳定性、端口保持、No-PAT 判断、样本数或映射地址列表。

端口趋势先比较原 socket 在同一公网 IP 上不同目标的首次映射响应，按探测顺序判断，再新建 socket 向已成功响应的目标验证。原 socket 必须有至少 3 个不同目标的端口，全部依次加 1 或全部依次减 1，且新 socket 在同一公网 IP 上按同一步长延续，才报告为可预测、端口严格递增或递减。全部端口的总跨度不得超过 1024。同一目标的重复响应不补足目标数，新 socket 样本也不能补足第 3 个目标。JSON 中分别显示为 `predictable_incremental` 和 `predictable_decremental`，文本和 JSON 的 `reason` 会说明跨 socket 的验证结果。

例如原 socket 返回 `40000、40001、40002`，新 socket 返回 `40003` 才满足严格递增条件；新 socket 返回 `40004` 时只算可能可预测，返回已出现的端口、反向变化或公网 IP 改变时，不判为可预测。若新 socket 创建失败、探测超时或没有有效映射样本，`predictability` 为 `unknown`，不会只凭原 socket 的连续端口判为可预测；已确认的 NAT 映射类型和原 socket 样本仍保留。

其他整体趋势判断允许端口间隔和少量波动，但要求总跨度有限且新 socket 样本延续趋势，报告为可能可预测；原 socket 仅有两个目标端口、由新 socket 延续趋势时，最多归入这一类。所有结果都基于有限样本，不同本地端口的行为也可能不同，不能保证之后映射的端口或连接结果。

</details>
