# AI Coding Agent 指南

本文给 AI Coding Agent 提供在本仓库可靠工作的最小上下文，也可作为人工 Review
检查表。

## 任务目标

保持 CodeHelper 是“一套受治理的本地 Coding Agent Runtime + 单一 Web Host”。优先保证
正确性、证据、安全边界和可维护性，而不是能力数量。

## 开始工作

1. 阅读根目录 `README.md`。
2. 阅读[架构设计](./architecture.md)。
3. 修改实现前先读最近的 Package Test。
4. 从 `Makefile` 查找标准验证命令。
5. 检查 `git status`，不能覆盖无关用户改动。

## 所有权地图

| 变更领域 | 起始路径 |
| --- | --- |
| Web Process/Flag | `internal/host/web` |
| HTTP/Web Transport | `internal/host/runtimeapi` |
| Operation/Event Shape | `internal/runtime/protocol` |
| Turn/Session State | `internal/runtime/app` |
| Model/Tool Loop | `internal/runtime/agent` |
| Dependency Construction | `internal/runtime/app/wire` |
| Typed Extension Lifecycle | `internal/runtime/extension`、`internal/runtime/app/extension` |
| Model/Provider | `internal/adapter/model`、`internal/adapter/provider` |
| Tool | `internal/adapter/tool` |
| Approval/Sandbox | `internal/security`、`internal/adapter/tool/guard` |
| Task/Workflow/Lease | `internal/orchestration/kernel`、`internal/orchestration/store` |
| Durable Data | `internal/persist` |
| Observation/Usage/Trace | `internal/observability` |
| Web | `web` |

## 不可破坏的约束

- Host 不直接执行 Tool。
- `wire` 不实现业务循环。
- Web 不建立第二套 Runtime。
- 不绕过 Guard、Policy、Constitution、Journal 或 Sandbox。
- 不在事务化 WorkGraph Command/Store 路径之外写入 Orchestration Lifecycle State。
- Observation、Capture 或 Exporter Failure 不能改变 Turn 的业务结果。
- 受 Git 跟踪的 Config、Log、Fixture、Docs 中不保存原始凭证。
- 不读取、打印、总结、Patch 或强制添加被忽略的
  `docs/DEEPSEEK-LIVE.zh-CN.md` 本机 Runbook。
- 有结构化 Parser 时，不用脆弱字符串逻辑解析结构化格式。
- 不静默接受未知 Config Field 或 Protocol Variant。
- 未实际执行验证时，不声称“验证通过”。
- 没有明确需求时，不为未发布开发状态增加兼容 Migration。

## 工作方法

### 探索

使用 `rg` 和定向读取，确认：

- 所属 Package；
- 当前测试；
- Public/Persisted Contract；
- 跨平台文件；
- 生成文件；
- 用户已修改文件。

### 计划

明确行为变化、不变量、文件和验证。变更应留在既有所有权边界内。

### 实现

- 遵循本地模式；
- 做最小且完整的变更；
- 使用领域语义名称；
- 只为不明显约束添加注释；
- 中文文档与代码事实同步更新；
- 使用仓库命令重新生成 Artifact。

### 验证

先运行最窄测试，再按影响面扩大：

```bash
go test ./path/to/package
make docs-check
cd web && npm run check && npm test -- relevant-area
```

结束前运行 `git diff --check`。

## 契约变更

### Protocol

修改 `internal/runtime/protocol` 时：

1. 更新 Validation 与 Test；
2. 重新生成 JSON Schema；
3. 重新生成 Web Protocol Type；
4. 运行 Web Transport 与 HTTP Contract Test；
5. 更新中文文档。

### Persistence

修改 Durable Schema 时：

1. 明确兼容预期；
2. 保证初始化就是最新 Schema；
3. 公开发布后使用事务 Migration 与 Migration Test；
4. 验证 Foreign Key、Index、Rollback 与 Corruption Handling；
5. 更新运维文档。

### Security

修改 Guard、Policy、Permission、Constitution、Sandbox、Egress、Credential 或 Plugin
Trust 时：

1. 枚举攻击者可控输入；
2. 保持 Fail-closed；
3. 除成功路径外，测试拒绝与清理；
4. 运行聚焦 Race/Security Test；
5. 不弱化平台能力声明。

### Orchestration

修改 Task、Workflow、Worker、Fleet、Lane 或 Subagent Lifecycle 时：

1. 将 Transition 表达为带 Revision 的 WorkGraph Command；
2. 在同一 SQLite 事务提交 Aggregate、Fact、Command Receipt、Effect Outbox 与兼容
   Projection；
3. 使用 Lease Owner/Epoch Fence Claim 与 Settlement；
4. 保持 Fleet 与 Host View 只读；
5. 测试 Restart、Duplicate Command、Stale Lease 与 Terminal Atomicity。

### Observability

修改 Observation Kind、Capture、Retention、Trace 或 Exporter 时：

1. 更新 `internal/observability/schema/observation_traits.json`；
2. 运行 `make observation-traits`；
3. 保持 Privacy Admission 先于 Journal/CAS Persistence；
4. 只使用有界低基数 Metric Label；
5. 证明 Writer/Exporter Failure 与业务执行隔离。

## 测试预期

| 风险 | 预期测试 |
| --- | --- |
| 局部逻辑 | Unit Test |
| 共享组件 | Unit + Integration Consumer |
| Protocol | Golden/Schema + Transport Contract |
| Observation | Trait Generation + Privacy/Router/Exporter Failure Test |
| Persistence | Create/Read/Update + Failure/Reopen |
| Concurrency | 确定性同步 + Race |
| Security | Allow、Deny、Malformed Input、Cleanup |
| UI | State Projection + Message Validation |
| Script | Happy Path、Invalid Input、Exit Status |

仓库已有 Target 或 Fixture Framework 时，不创建脱离框架的私有测试脚本。

## 文档预期

文档属于功能的一部分：

- 描述当前行为，不描述开发编年史；
- 区分已交付能力与 Roadmap；
- 示例命令必须存在于 `--help`；
- 受 Git 跟踪的文档不嵌入真实凭证；
- 操作仓库所有者的本机 DeepSeek 环境时，调用 `make deepseek-init` 或
  `make deepseek-web`，不检查被忽略的 Runbook；
- 保持本地链接有效；
- 更新 `docs/zh-CN`，不创建英文镜像；
- 删除过时材料，不保留互相矛盾的副本。

建设 Agent 工程知识书籍时：

- 把 `docs/book/catalog.json` 作为结构、标题、路径、里程碑和交付状态的事实来源；
- 不为 `planned` 章节创建 Markdown 占位文件；
- 章节进入 `draft` 时创建中文文件；
- 从中文模板开始，并保持 Front Matter 与 Catalog 一致；
- 使用 `docs/book/governance.json` 管理 Ownership、Freshness 和 Release Fact；
- 在 PR Documentation Impact 区块声明受影响 Chapter ID；
- Catalog 变化后运行 `make book-navigation`；
- 完成前运行 `make book-check`。

## 完成报告

说明：

- 修改内容；
- 关键文件或子系统；
- 执行的测试；
- 环境失败或跳过测试；
- 兼容或 Migration 影响；
- 未纳入任务的剩余 Untracked File。
