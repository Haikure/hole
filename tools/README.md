# NAT 探测工具

`natprobe` 使用 UDP STUN 探测 NAT 映射和过滤行为，并根据多个目标返回的公网端口判断映射是否稳定、递增或递减：

```bash
go run ./tools/natprobe
```

默认使用 4 个 STUN 端点，每个端点最多采集 3 轮基础映射响应。过滤类型依赖 RFC 5780 的 `OTHER-ADDRESS` 和 `CHANGE-REQUEST` 支持；普通 STUN 服务不支持这两个扩展时，过滤类型无法测出。需要时可在自定义列表中加入当前网络可达的 RFC 5780 服务。首次使用 `-stun` 时会替换默认列表，支持重复参数或逗号分隔，最多 8 个：

```bash
go run ./tools/natprobe \
  -stun stun:stun.example.net:3478 \
  -stun stun:stun2.example.net:3478 \
  -timeout 20s \
  -json
```

`-timeout` 设置 DNS 与探测的总时限，默认 20 秒；`-json` 输出包含 NAT 类型、映射与过滤行为、有效样本数、过滤测试所用端点和观测到的映射地址。省略 `stun:` 前缀时工具会自动补上，例如 `-stun stun.example.net:3478`。

该工具独立实现 STUN 请求和行为分类，只依赖根 `go.mod` 中的 Pion STUN 库，不导入 `core`。映射分类需要多个有效响应地址；过滤分类需要服务端提供有效的 `OTHER-ADDRESS` 并响应地址或端口变更测试。普通 STUN 服务不支持这些扩展时，过滤行为仍会显示未知。端口趋势按不同目标的首次映射响应排序，允许端口间隔和少量波动，但要求总跨度有限且新 socket 样本延续趋势；结果仍是有限样本推断，不能保证之后映射的端口。
