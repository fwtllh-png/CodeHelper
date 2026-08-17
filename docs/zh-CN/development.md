# 本地开发、测试与脚本

简体中文 | [English](../en/development.md)

## 开发环境

必需：

- Go 1.26+
- Git
- Make

开发 VS Code 插件还需要：

- 与 Lockfile 兼容的 Node.js
- npm
- Electron 集成需要 VS Code 1.96+

可选工具：

- `syft`：生成完整 CycloneDX SBOM；
- `tmux`：可 Attach 的 Lane；
- Linux Bubblewrap/Landlock 或 macOS Seatbelt；

## 初始化

```bash
git clone https://github.com/fwtllh-png/CodeHelper.git
cd CodeHelper
go mod download
make vscode-install
make build
```

`make vscode-install` 使用 `npm ci`，不要通过随意安装依赖改变 Lockfile。

## 快速开发循环

仅 Go 变更：

```bash
gofmt -w path/to/file.go
go test ./path/to/package
go test ./path/to/package -run TestSpecificBehavior -count=1
```

VS Code 变更：

```bash
cd extensions/vscode
npm run check
npm test -- state runtime
```

文档变更：

```bash
make docs-check
make book-check # 修改 docs/book 或 Catalog 时
```

交付前：

```bash
git diff --check
make brand-check
```

## Make Target

### 核心

| Target | 作用 |
| --- | --- |
| `make fmt` | Go 格式化 |
| `make build` | 带 Build Metadata 构建 `bin/codehelper` |
| `make test` | 执行稳定的 Hermetic Lane |
| `make test-hermetic` | 串行执行无网络 Go 测试 |
| `make test-platform-capability` | 执行真实宿主机 Sandbox 能力测试 |
| `make test-integration` | 用真实 Binary 执行 ACP 与 VS Code 集成测试 |
| `make test-release` | 执行 Race、打包、脱敏与发布 Dry-run 门禁 |
| `make hotspot-baseline` | 校验 IMP-006 职责、依赖、体积与测试资产契约 |
| `make architecture-freeze` | 执行四热点 Characterization、Golden、Schema 与聚焦 Race 门禁 |
| `make race` | 串行 Race Go 测试 |
| `make smoke` | 构建并验证 Help/Version |
| `make docs-check` | 检查 Markdown 本地链接与双语结构 |
| `make book-check` | 检查知识书籍 Catalog、元数据、镜像和路径 |
| `make book-navigation` | 根据 Catalog 重新生成双语书籍导航 |
| `make verify` | Docs、Brand、VS Code、Vet、Test、Race 综合门禁 |
| `make clean` | 删除生成的 Build/Release 目录 |

### 安全与契约

| Target | 作用 |
| --- | --- |
| `make security-test` | Security、Guard、Plugin、CLI、Engine、App 测试 |
| `make sandbox-attack-test` | Sandbox/File/Shell Attack Corpus |
| `make secret-leak-test` | 发布二进制 Secret 脱敏测试 |
| `make acp-interop` | 真实二进制 ACP Stdio 生命周期 |
| `make protocol-contract` | ACP 上的共享 Runtime 场景 |
| `make protocol-schema` | 重新生成 Runtime Protocol Schema |
| `make observation-traits` | 重新生成 Go、TypeScript 与 JSON Schema 的 Observation Trait |
| `make observation-traits-check` | 检测生成的 Observation Artifact 是否漂移 |

### VS Code

| Target | 作用 |
| --- | --- |
| `make vscode-install` | `npm ci` |
| `make vscode-check` | 生成文件漂移、TypeScript、ESLint |
| `make vscode-test` | Extension Unit Test |
| `make vscode-runtime-integration` | 不启动 Electron 的真实 Go Runtime Stdio 测试 |
| `make vscode-integration` | 官方 VS Code Electron Flow |
| `make vscode-security` | Extension Security Test |
| `make vscode-performance` | Projection 与 Runtime Ready 性能预算 |
| `make vscode-package` | 构建、安装并握手验证当前 Host Target VSIX |
| `make vscode-package-universal` | 构建并静态安装审计 Universal VSIX |
| `make vscode-distribution` | 多目标 Dry-run 发布产物 |
| `make vscode-rc` | 完整 Release Candidate Gate |

### Benchmark 与发布

| Target | 作用 |
| --- | --- |
| `make bench` | 在强宿主机 Sandbox 上执行 Fixture Coding Benchmark |
| `make catalog-bench` | Dynamic Tool Catalog Scale Benchmark |
| `make multi-agent-performance` | 有界 Agent Event 投影性能预算 |
| `make live-model-smoke` | 显式、非 Hermetic 的真实模型冒烟 |
| `make live-multi-agent-smoke` | 需要凭据的真实 Provider Agent Spawn/Wait/Completion 冒烟 |
| `make package VERSION=x.y.z` | 多平台 CLI Package 与 SBOM |

## 仓库脚本

| 脚本 | 行为 |
| --- | --- |
| `scripts/check-docs.sh` | 检查本地 Markdown 链接与双语镜像 |
| `scripts/check-book.sh` | 检查 Agent 工程知识书籍契约 |
| `scripts/render-book-navigation.py` | 根据书籍 Catalog 生成导航 |
| `scripts/check-brand.sh` | 拒绝历史产品品牌残留 |
| `scripts/test-brand-check.sh` | Brand Scanner 自测 |
| `scripts/test-secret-leak.sh` | 对已构建二进制执行脱敏测试 |
| `scripts/run-test-lane.py` | 执行 Test Lane 并写入结构化证据 |
| `scripts/commanddocs` | 生成或校验双语命令清单 |
| `scripts/observationtraitgen` | 从单一 Manifest 生成 Observation Trait 与公开 Schema |
| `scripts/live-model-smoke.sh` | 调用一个显式配置的真实模型 |
| `scripts/package-release.sh` | 构建五个平台、Checksum、SBOM、Manifest 与 Smoke |
| `scripts/deepseek-local.sh` | 编译配置本机 DeepSeek，并启动 TUI 或 VS Code |
| `scripts/setup-vscode-local.sh` | macOS 官方 VS Code 本地安装 |

Benchmark 与评估报告是临时 `.tmp` 或 CI Artifact。每个比率都携带分子与分母；
分母为空时输出 `null`，不会用 0 冒充已测量结果。`make bench` 仍是严格 Release
Gate，不会把 Unavailable 任务视为 Passed。

`extensions/vscode/scripts` 管理 TypeScript Build、Protocol/Compatibility 生成、
Electron/Remote Integration、VSIX、Release Manifest、Provenance、Matrix Evidence 和
RC Report。

所有脚本必须：

- 能从任意调用目录解析仓库 Root；
- 使用严格错误处理；
- 正确传递失败退出码；
- 除非提供 Override，否则不写个人绝对路径；
- 不打印 Secret，也不从受 Git 跟踪的仓库文档读取 Secret；
- Git 忽略的本机 DeepSeek Runbook 只能作为凭证输入；
- 说明生成目录与清理行为。

## 生成文件

不要手工编辑：

- `docs/protocol/runtime-protocol.schema.json`
- `docs/protocol/observation.schema.json`
- `internal/observability/observation/traits.gen.go`
- `extensions/vscode/src/protocol/observation.generated.ts`
- `extensions/vscode/src/protocol/generated.ts`
- `extensions/vscode/src/compatibility/generated.ts`

使用：

```bash
make protocol-schema
make observation-traits
cd extensions/vscode
npm run generate:protocol
npm run generate:compatibility
```

生成结果应与源变更一起提交。

`internal/observability/schema/observation_traits.json` 是唯一手工维护的 Observation
Trait Manifest。新增 Kind 必须先声明 Owner、Durability、Payload Policy、Retention
Class、必需 Correlation、OTLP Mapping 与 Priority，生成才能通过。

## 测试策略

测试被明确分为四条 Lane：

| Lane | 命令 | 契约 |
| --- | --- | --- |
| Hermetic | `make test` | 默认 PR 安全测试；不依赖网络、凭证、GUI 或宿主机 Sandbox |
| Platform Capability | `make test-platform-capability` | 真实 OS Sandbox 行为；缺失前置条件时报告 `unavailable` |
| Integration | `make test-integration` | 真实 CLI/ACP 与 VS Code Runtime 生命周期 |
| Release | `make test-release` | 高成本 Race、Benchmark、Cross-build、脱敏与打包门禁 |

每条 Lane 都在 `.tmp/test-lanes/` 下写入 JSON 结果，状态为 `passed`、`failed` 或
`unavailable`。CI 强制要求 Linux 平台能力；本地不支持的环境明确报告
`unavailable`，不会伪装成能力已通过。

架构回归由两个互补契约约束：

- `testdata/contracts/hotspot-baseline.json` 将职责绑定到 Package Symbol 和 Owner
  File。职责丢失或错位、未审阅内部依赖、热点增长和测试资产删除都会使
  `make hotspot-baseline` 失败。
- `testdata/contracts/architecture-metrics-baseline.json` 限制直接内部 Package
  Fanout、生产代码行数、Options/Mutex 字段、热点文件/函数体积以及重复的 Protocol
  Event Switch 站点。`make architecture-ratchet` 会测量当前仓库，并在
  `ARCHITECTURE_BASE_REF` 包含旧基线时比较新旧阈值。

架构阈值只能单调收紧。提高阈值必须为对应指标填写非空 `relaxations` 理由；删除目标或
指标必须填写显式 `retirements` 理由。过期豁免会使 Ratchet 失败，避免临时额度静默变成
永久例外。离散状态指标不留 Headroom；Package、文件和函数行数分别最多保留 100、20
和 5 行额度。实际值下降后若与阈值差距超过该额度，检查也会失败并要求同步下调基线。
实际测量报告写入 `.tmp/architecture/metrics.json`。

## 基础 Kit

共享的持久化机械逻辑位于 `internal/persist/sqlkit`。Repository 可以复用事务生命周期、
行集合扫描、Canonical JSON、Nullable 值、UTC 时间戳和精确 RowsAffected 检查。SQL
文本、乐观并发、状态迁移、Not-found 错误与领域 Scanner 仍由对应 Repository 持有。
`sqlkit.WithTx` 不重试也不嵌套事务；Callback 错误触发 Rollback，Rollback 错误会合并，
Callback Panic 会先 Rollback 再继续抛出。

简单 Typed Tool 使用 `internal/adapter/tool/typed` 完成严格 JSON 解码和输出编码，使用
`internal/adapter/tool/result` 构造结构化成功或失败结果。Typed Adapter 不是新的执行
路径：Descriptor 仍注册到普通 Registry，调用仍需要授权，Consequential Tool 仍经过
Guard、Approval、Journal 与 Sandbox Policy。Resource Resolution、Availability 和
Repeat Behavior 继续由各 Tool Descriptor 显式声明。

首批迁移范围为 `completion`、`lsp`、`memory`、`revert`、`skill` 和 `toolsearch`。
迁移门禁禁止这些 Tool 重新引入本地 `json.Unmarshal`，防止 Call Shape 语义分叉。
Task `List` 与 `Get` 共用 Scanner，一次查询读取所有字段，并具备 1000 行 Benchmark
和单查询 Contract Test。

聚焦验证：

```bash
go test -race ./internal/persist/... \
  ./internal/orchestration/task \
  ./internal/orchestration/automation
go test -race ./internal/adapter/tool/...
go test -run '^$' -bench BenchmarkList1000 -benchtime=1x \
  ./internal/orchestration/task
```

按风险选择测试：

| 变更 | 最低验证 |
| --- | --- |
| 局部纯函数 | Package Test |
| 共享 Runtime 行为 | Package + 依赖 Runtime Test |
| Protocol | Schema Drift + ACP + HTTP Contract |
| Observation Kind/Exporter | Trait Generation + Observation/Router/OTLP Test |
| Persistence | Repository + State Package Test |
| Guard/Security | 聚焦 Race Test + Attack Corpus |
| VS Code State/UI | Typecheck、ESLint、相关 Node Test |
| Release Script | Dry-run Package/Distribution Gate |
| 产品文档 | `make docs-check` |
| 知识书籍或 Catalog | `make docs-check && make book-check` |

真实平台能力测试使用 `capability` Go Build Tag，因此不会意外进入 Hermetic Lane。
需要真实 Binary 的集成测试由 `make test-integration` 执行，不能从 Unit Test 绿灯推断
其已通过。

## 平台说明

- macOS 使用平台专用沙箱行为和 Keychain。
- Linux 强度依赖 Kernel 和可用 Sandbox Helper。
- Windows 的进程与文件系统隔离保证不同。
- 高并发 Package Test 下，Race 和真实 Fixture 生命周期测试可能对资源敏感。

不能为了让不支持的平台“看起来支持”而削弱测试。

## 发布开发

CLI Package：

```bash
VERSION=0.1.0 RELEASE_STAGE=experimental make package
```

VS Code Dry-run：

```bash
make vscode-distribution
make vscode-matrix-report
```

正式发布凭证必须从仓库外注入。兼容契约和发布边界见
[VS Code 插件](./vscode.md)。
