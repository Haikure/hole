# 仓库维护约定

## 项目边界

- 根目录是 Go CLI，`core/` 是 CLI / Android 共用实现，`mobile/` 是 gomobile 窄接口。
- `android/app` 为手机，`android/wear` 复用手机源码，`android/corebridge` 管理 AAR 和平台网络适配。
- Worker 实现在 `worker.js` / `worker_ice.mjs`，部署配置为 `wrangler.toml`。
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

## 验证

按改动范围执行，并说明实际执行结果：

```bash
source scripts/build-env.sh
bash -n build.sh scripts/build-env.sh android/scripts/build-core.sh android/scripts/setup-toolchain.sh
python3 -m unittest discover -s scripts -p 'test_build.py'
python3 -m unittest discover -s android/scripts -p 'verify_apk_test.py'
go test -race -count=1 ./...
go vet ./...
node --test worker_ice.test.mjs
git diff --check
```

核心 / mobile / 依赖变化时另测绑定依赖图：

```bash
(cd android/corebridge/gobuild && go test -race -count=1 hole/core hole/mobile && go vet hole/core hole/mobile)
```

Android 变化时运行所涉及目标的 Release 构建、lint 和 JVM 测试；后者需已有 JDK 21+。
命令及 APK 验证入口见 `docs/BUILDING.md`。不要仅凭已有 AAR、APK 或旧测试报告判定当前源码通过。
