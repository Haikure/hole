# 仓库维护约定

## 项目边界

- 根目录是 Go CLI，`core/` 是 CLI / Android 共用实现，`mobile/` 是 gomobile 窄接口。
- `android/app` 为手机，`android/wear` 复用手机源码，`android/corebridge` 管理 AAR 和平台网络适配。
- Worker 实现在 `worker/worker.js` / `worker/worker_ice.mjs`，部署配置参考 `worker/wrangler.example.toml`。
- 发布入口是根目录 `build.sh`。技术说明集中于 `docs/` 和各模块 README，不新增会话流水式 Markdown。

## 修改原则

1. 先查看 `git status`，保留现有未提交工作；只修改任务涉及的内容。
2. 根 `go.mod` 与 `android/corebridge/gobuild/go.mod` 是独立依赖图，两者共用核心源码。
3. 传输、TCP / UDP 会话、身份和资源限额逻辑只在共享核心实现，不在 Android 再造一份。
4. 修改协议、配置、CLI 参数、版本或构建入口时，同时更新对应 README / docs 和回归测试。
5. 发布文档使用简体中文，描述当前行为；不要把本地构建、静态检查写成真实设备验收。
6. 本仓库暂不新增许可证；许可证变更由维护者明确决定。

## 构建与缓存

- 使用 `./build.sh cli|android|wear|all`；客户端交付均为 Release，保留 Go `-s -w`、R8、资源收缩和 native strip。
- 所有构建缓存进入根 `.cache/`；直接执行工具前 `source scripts/build-env.sh`。
- Android 环境由 `hole_load_android_toolchain` 加载，再强制采用项目缓存，避免旧 `env.sh` 重定向缓存。
- SDK / NDK / JDK 不随普通构建自动安装；工具链版本见 `docs/BUILDING.md`。
- CLI 交叉编译变量不传入 Android；保持共享 AAR 的 ARM32 / ARM64 与手机的 ARM64 策略。
- 核心摘要统一使用 `scripts/build_meta.py`，输入变化应使 `:corebridge:bindGoCore` 失效并重建。
- 新交付放在 `dist/`；不覆盖根目录旧二进制，也不删除原 `android/dist/` 留存包。

## 签名与版本管理

- 签名只从本地 `signing.env` / 显式配置文件或 `HOLE_SIGNING_*` 环境变量读取，环境变量优先。
- 不生成、替换或提交已有签名密钥，不记录密码；保留 JNI 绑定类与方法。
- `.cache/`、`dist/`、模块生成物、本地配置和密钥不进入 Git。
- Go、Kotlin、Python、Worker 测试源码纳入 Git，不恢复 `*test*` 一类泛化忽略规则。
- 不自动提交、推送、部署 Worker 或安装到设备。

## 发布说明

- `CHANGELOG.md` 是 `.github/workflows/build.yaml` 创建 GitHub Release 时使用的正文来源（`body_path`）。
- 文件只保留本次发布 tag 相对上一个 tag 的变更内容；准备新版本时整体替换旧说明，不累积历史版本章节。
- 发布说明使用简体中文，基于两个 tag 之间的实际变更撰写，不写仓库介绍或未发布内容。

## 验证

验证按改动范围分级；下面的全量命令是跨模块 / 发布检查参考，不是每次任务的固定清单。先选覆盖本次改动的最小检查，完成修改后集中执行一次，避免每轮编辑都重跑全套。Go 测试默认保留缓存，不要无条件添加 `-count=1`；`-race`、全仓 `go vet` 和完整 Android Release 构建按并发 / 传输 / 依赖风险或发布需求选择。最终说明实际执行和未执行的检查。

- 仅文档 / Markdown：`git diff --check`。
- Worker：运行 `node --test worker/worker_ice.test.mjs worker/worker_fixture.test.mjs`。
- Go 单模块改动：先运行受影响包或测试；涉及并发、传输生命周期、身份或资源限额时，再运行相关包的 race 测试。
- Android UI / 业务逻辑：运行相关 JVM 测试和 lint；只有改动影响构建、桥接、签名或交付时才加 Release 构建。
- 跨模块改动、Go 依赖升级或发布：按涉及模块组合下列全量检查；无需为了未受影响的模块补跑测试。

全量验证命令参考，按改动范围选取：

```bash
source scripts/build-env.sh
bash -n build.sh scripts/build-env.sh android/scripts/build-core.sh android/scripts/setup-toolchain.sh
python3 -m unittest discover -s scripts -p 'test_build.py'
python3 -m unittest discover -s android/scripts -p 'verify_apk_test.py'
go test -race -count=1 ./...
go vet ./...
node --test worker/worker_ice.test.mjs worker/worker_fixture.test.mjs
git diff --check
```

核心 / mobile 源码或 Go 依赖变化时，额外检查 Android 绑定模块使用自己的依赖图能否编译并运行共享包测试。日常快速检查使用默认缓存和非 race 模式：

```bash
(cd android/corebridge/gobuild && go test hole/core hole/mobile)
```

只有并发 / 传输风险较高、依赖或导出 API 变化，或发布前需要提高把握时，再对相关包追加 `-race -count=1` 和 `go vet`。Android JVM 测试需已有 JDK 21+。
命令及 APK 验证入口见 `docs/BUILDING.md`。不要仅凭已有 AAR、APK 或旧测试报告判定当前源码通过。
