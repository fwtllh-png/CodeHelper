# CodeHelper Agent 工程知识书籍：导航

本导航由 `docs/book/catalog.json` 生成。规划章节可以清晰可见，但不会通过空占位文件伪装成已完成内容。

## 状态

- `planned`：只有 Catalog 条目，正文尚未开始。
- `draft`：中文正文已存在，但尚未完成全部验证。
- `verified`：中文内容、源码引用和验证命令均通过章节门禁。

## 核心阅读路径

建议先阅读以下六章：

- [为什么 Agent 需要受治理的 Runtime](./01-agent-engineering/05-why-governed-runtime.md) — `agent-why-governed-runtime` — `draft` — 规划路径: `01-agent-engineering/05-why-governed-runtime.md`
- [CodeHelper 全局架构](./02-codehelper-overview/02-system-architecture.md) — `overview-system-architecture` — `draft` — 规划路径: `02-codehelper-overview/02-system-architecture.md`
- [一次 Agent Turn 的完整生命周期](./02-codehelper-overview/05-turn-lifecycle.md) — `overview-turn-lifecycle` — `verified` — 规划路径: `02-codehelper-overview/05-turn-lifecycle.md`
- [Model、Context 与 Tool 如何协作](./02-codehelper-overview/06-model-context-and-tool.md) — `overview-model-context-tool` — `draft` — 规划路径: `02-codehelper-overview/06-model-context-and-tool.md`
- [Guard、Approval、Constitution 与 Sandbox](./07-security-governance/03-approval-constitution-sandbox.md) — `security-approval-sandbox` — `draft` — 规划路径: `07-security-governance/03-approval-constitution-sandbox.md`
- [构建并追踪第一个 Agent Turn](./13-hands-on-labs/01-first-agent-turn.md) — `lab-first-turn` — `draft` — 规划路径: `13-hands-on-labs/01-first-agent-turn.md`

## 完整规划目录

### 部分 1: 进入 Agent 工程

- [从 Chatbot 到 Agent](./01-agent-engineering/01-from-chatbot-to-agent.md) — `agent-from-chatbot-to-agent` — `draft` — 规划路径: `01-agent-engineering/01-from-chatbot-to-agent.md`
- [LLM、Token、Context Window 与 Sampling](./01-agent-engineering/02-llm-token-and-context.md) — `agent-llm-token-context` — `draft` — 规划路径: `01-agent-engineering/02-llm-token-and-context.md`
- [ReAct、Planning、Tool Calling 与 Reflection](./01-agent-engineering/03-react-planning-and-tools.md) — `agent-react-planning-tools` — `draft` — 规划路径: `01-agent-engineering/03-react-planning-and-tools.md`
- [Agent、Workflow 与 Automation 的边界](./01-agent-engineering/04-agent-workflow-boundaries.md) — `agent-workflow-boundaries` — `draft` — 规划路径: `01-agent-engineering/04-agent-workflow-boundaries.md`
- [为什么 Agent 需要受治理的 Runtime](./01-agent-engineering/05-why-governed-runtime.md) — `agent-why-governed-runtime` — `draft` — 规划路径: `01-agent-engineering/05-why-governed-runtime.md`

### 部分 2: 认识 CodeHelper

- [项目定位、价值与非目标](./02-codehelper-overview/01-positioning-and-non-goals.md) — `overview-positioning` — `draft` — 规划路径: `02-codehelper-overview/01-positioning-and-non-goals.md`
- [CodeHelper 全局架构](./02-codehelper-overview/02-system-architecture.md) — `overview-system-architecture` — `draft` — 规划路径: `02-codehelper-overview/02-system-architecture.md`
- [Package 所有权与依赖方向](./02-codehelper-overview/03-package-ownership.md) — `overview-package-ownership` — `draft` — 规划路径: `02-codehelper-overview/03-package-ownership.md`
- [Operation、Event、Receipt 与 Projection](./02-codehelper-overview/04-runtime-vocabulary.md) — `overview-runtime-vocabulary` — `draft` — 规划路径: `02-codehelper-overview/04-runtime-vocabulary.md`
- [一次 Agent Turn 的完整生命周期](./02-codehelper-overview/05-turn-lifecycle.md) — `overview-turn-lifecycle` — `verified` — 规划路径: `02-codehelper-overview/05-turn-lifecycle.md`
- [Model、Context 与 Tool 如何协作](./02-codehelper-overview/06-model-context-and-tool.md) — `overview-model-context-tool` — `draft` — 规划路径: `02-codehelper-overview/06-model-context-and-tool.md`

### 部分 3: Runtime 内核

- [Protocol 与稳定数据契约](./03-runtime-kernel/01-protocol.md) — `runtime-protocol` — `draft` — 规划路径: `03-runtime-kernel/01-protocol.md`
- [Application Runtime 与状态投影](./03-runtime-kernel/02-application-runtime.md) — `runtime-app` — `verified` — 规划路径: `03-runtime-kernel/02-application-runtime.md`
- [模型与工具执行循环](./03-runtime-kernel/03-agent-loop.md) — `runtime-agent-loop` — `verified` — 规划路径: `03-runtime-kernel/03-agent-loop.md`
- [依赖构造与能力装配](./03-runtime-kernel/04-dependency-wiring.md) — `runtime-wiring` — `draft` — 规划路径: `03-runtime-kernel/04-dependency-wiring.md`
- [Streaming、Cancellation 与 Error Taxonomy](./03-runtime-kernel/05-streaming-cancellation-errors.md) — `runtime-stream-cancel-errors` — `draft` — 规划路径: `03-runtime-kernel/05-streaming-cancellation-errors.md`
- [Resume、Recovery 与幂等边界](./03-runtime-kernel/06-resume-and-recovery.md) — `runtime-resume-recovery` — `verified` — 规划路径: `03-runtime-kernel/06-resume-and-recovery.md`

### 部分 4: 模型与 Provider

- [Chat Completion 与 Responses 协议](./04-model-and-provider/01-wire-protocols.md) — `model-wire-protocols` — `draft` — 规划路径: `04-model-and-provider/01-wire-protocols.md`
- [Provider Adapter、Model Catalog 与 Wire ID](./04-model-and-provider/02-provider-and-catalog.md) — `model-provider-catalog` — `draft` — 规划路径: `04-model-and-provider/02-provider-and-catalog.md`
- [Capability Negotiation 与 Route Resolution](./04-model-and-provider/03-capability-and-routing.md) — `model-capability-routing` — `draft` — 规划路径: `04-model-and-provider/03-capability-and-routing.md`
- [Streaming、Reasoning、Tool Call 与 Usage](./04-model-and-provider/04-streaming-reasoning-and-usage.md) — `model-stream-reasoning-usage` — `draft` — 规划路径: `04-model-and-provider/04-streaming-reasoning-and-usage.md`
- [Credential Reference 与 Secret Lifecycle](./04-model-and-provider/05-credential-lifecycle.md) — `model-credential-lifecycle` — `draft` — 规划路径: `04-model-and-provider/05-credential-lifecycle.md`
- [Retry、Rate Limit、Timeout 与故障分类](./04-model-and-provider/06-provider-failures.md) — `model-provider-failures` — `draft` — 规划路径: `04-model-and-provider/06-provider-failures.md`

### 部分 5: Context Engineering

- [Prompt、Message 与 Context](./05-context-engineering/01-prompt-message-context.md) — `context-prompt-message` — `draft` — 规划路径: `05-context-engineering/01-prompt-message-context.md`
- [Workspace、Repository Index 与 Editor Context](./05-context-engineering/02-workspace-index-editor.md) — `context-workspace-index-editor` — `draft` — 规划路径: `05-context-engineering/02-workspace-index-editor.md`
- [Context Source、优先级与生命周期](./05-context-engineering/03-source-priority-lifecycle.md) — `context-source-lifecycle` — `draft` — 规划路径: `05-context-engineering/03-source-priority-lifecycle.md`
- [Token Budget、Compaction 与信息损失](./05-context-engineering/04-budget-and-compaction.md) — `context-budget-compaction` — `draft` — 规划路径: `05-context-engineering/04-budget-and-compaction.md`
- [Memory、Snapshot 与恢复](./05-context-engineering/05-memory-and-snapshot.md) — `context-memory-snapshot` — `draft` — 规划路径: `05-context-engineering/05-memory-and-snapshot.md`
- [如何评估 Context Quality](./05-context-engineering/06-context-quality.md) — `context-quality` — `draft` — 规划路径: `05-context-engineering/06-context-quality.md`

### 部分 6: Tool 与受控执行

- [Tool Schema、Registry 与 Dynamic Catalog](./06-tools-and-execution/01-schema-registry-catalog.md) — `tool-schema-registry` — `draft` — 规划路径: `06-tools-and-execution/01-schema-registry-catalog.md`
- [File、Shell 与 Agent Tool](./06-tools-and-execution/02-file-shell-agent-tools.md) — `tool-builtins` — `draft` — 规划路径: `06-tools-and-execution/02-file-shell-agent-tools.md`
- [Tool Guard 执行管线](./06-tools-and-execution/03-tool-guard-pipeline.md) — `tool-guard-pipeline` — `draft` — 规划路径: `06-tools-and-execution/03-tool-guard-pipeline.md`
- [Edit Plan、Journal 与 Receipt](./06-tools-and-execution/04-edit-journal-receipt.md) — `tool-edit-journal-receipt` — `draft` — 规划路径: `06-tools-and-execution/04-edit-journal-receipt.md`
- [Verification Gate 与证据](./06-tools-and-execution/05-verification-and-evidence.md) — `tool-verification` — `draft` — 规划路径: `06-tools-and-execution/05-verification-and-evidence.md`
- [Tool Failure 如何反馈给模型](./06-tools-and-execution/06-failure-feedback.md) — `tool-failure-feedback` — `draft` — 规划路径: `06-tools-and-execution/06-failure-feedback.md`

### 部分 7: 安全与治理

- [Agent Runtime Threat Model](./07-security-governance/01-threat-model.md) — `security-threat-model` — `draft` — 规划路径: `07-security-governance/01-threat-model.md`
- [Mode、Posture、Policy 与 Permission](./07-security-governance/02-mode-posture-policy.md) — `security-mode-policy` — `draft` — 规划路径: `07-security-governance/02-mode-posture-policy.md`
- [Guard、Approval、Constitution 与 Sandbox](./07-security-governance/03-approval-constitution-sandbox.md) — `security-approval-sandbox` — `draft` — 规划路径: `07-security-governance/03-approval-constitution-sandbox.md`
- [OS Sandbox 与 Process Isolation](./07-security-governance/04-process-isolation.md) — `security-process-isolation` — `draft` — 规划路径: `07-security-governance/04-process-isolation.md`
- [Egress、Credential 与数据泄漏](./07-security-governance/05-egress-and-credentials.md) — `security-egress-credentials` — `draft` — 规划路径: `07-security-governance/05-egress-and-credentials.md`
- [MCP、Skill 与 Hook Trust](./07-security-governance/06-extension-trust.md) — `security-extension-trust` — `draft` — 规划路径: `07-security-governance/06-extension-trust.md`
- [Fail-closed 与平台能力声明](./07-security-governance/07-fail-closed.md) — `security-fail-closed` — `draft` — 规划路径: `07-security-governance/07-fail-closed.md`

### 部分 8: 状态与可观测性

- [Durable State 的必要性](./08-state-observability/01-why-durable-state.md) — `state-why-durable` — `verified` — 规划路径: `08-state-observability/01-why-durable-state.md`
- [SQLite、Event Log 与 Projection](./08-state-observability/02-sqlite-event-projection.md) — `state-sqlite-event-projection` — `draft` — 规划路径: `08-state-observability/02-sqlite-event-projection.md`
- [Session、Snapshot、CAS 与 Workspace Journal](./08-state-observability/03-session-snapshot-journal.md) — `state-session-snapshot-journal` — `draft` — 规划路径: `08-state-observability/03-session-snapshot-journal.md`
- [Trace、Span、Usage 与 Cost](./08-state-observability/04-trace-usage-cost.md) — `state-trace-usage-cost` — `verified` — 规划路径: `08-state-observability/04-trace-usage-cost.md`
- [Diagnostics、Maturity 与 Verification](./08-state-observability/05-diagnostics-and-verification.md) — `state-diagnostics-verification` — `draft` — 规划路径: `08-state-observability/05-diagnostics-and-verification.md`
- [从失败运行还原系统行为](./08-state-observability/06-reconstructing-failures.md) — `state-reconstruct-failure` — `draft` — 规划路径: `08-state-observability/06-reconstructing-failures.md`

### 部分 9: Task 与编排

- [Task、Worker 与 Executor](./09-task-orchestration/01-task-worker-executor.md) — `task-worker-executor` — `draft` — 规划路径: `09-task-orchestration/01-task-worker-executor.md`
- [Lease、Heartbeat、Retry 与幂等性](./09-task-orchestration/02-lease-heartbeat-retry.md) — `task-lease-retry` — `verified` — 规划路径: `09-task-orchestration/02-lease-heartbeat-retry.md`
- [Automation 与 Workflow](./09-task-orchestration/03-automation-and-workflow.md) — `task-automation-workflow` — `draft` — 规划路径: `09-task-orchestration/03-automation-and-workflow.md`
- [Checkpoint 与恢复](./09-task-orchestration/04-checkpoint-and-recovery.md) — `task-checkpoint-recovery` — `verified` — 规划路径: `09-task-orchestration/04-checkpoint-and-recovery.md`
- [Lane、Fleet 与调度](./09-task-orchestration/05-lane-fleet-scheduling.md) — `task-lane-fleet` — `verified` — 规划路径: `09-task-orchestration/05-lane-fleet-scheduling.md`
- [Subagent、Worktree 与拓扑关系](./09-task-orchestration/06-subagent-worktree-topology.md) — `task-subagent-worktree` — `draft` — 规划路径: `09-task-orchestration/06-subagent-worktree-topology.md`

### 部分 10: Host 与协议

- [本机 Web Transport 与 Runtime Authority](./10-hosts-protocols/04-web-transport.md) — `host-web-transport` — `verified` — 规划路径: `10-hosts-protocols/04-web-transport.md`
- [React Web 工作区与 Runtime Authority](./10-hosts-protocols/06-web.md) — `host-web` — `draft` — 规划路径: `10-hosts-protocols/06-web.md`

### 部分 11: 扩展生态

- [新增 Provider](./11-extension-ecosystem/01-adding-provider.md) — `extension-provider` — `draft` — 规划路径: `11-extension-ecosystem/01-adding-provider.md`
- [新增受治理 Tool](./11-extension-ecosystem/02-adding-tool.md) — `extension-tool` — `draft` — 规划路径: `11-extension-ecosystem/02-adding-tool.md`
- [接入 MCP Server](./11-extension-ecosystem/03-integrating-mcp.md) — `extension-mcp` — `draft` — 规划路径: `11-extension-ecosystem/03-integrating-mcp.md`
- [编写 Skill 与 Hook](./11-extension-ecosystem/04-skill-plugin-hook.md) — `extension-skill-plugin-hook` — `draft` — 规划路径: `11-extension-ecosystem/04-skill-plugin-hook.md`
- [新增 Host 而不复制 Runtime](./11-extension-ecosystem/05-adding-host.md) — `extension-host` — `draft` — 规划路径: `11-extension-ecosystem/05-adding-host.md`
- [Extension Failure 与隔离策略](./11-extension-ecosystem/06-failure-isolation.md) — `extension-failure-isolation` — `draft` — 规划路径: `11-extension-ecosystem/06-failure-isolation.md`

### 部分 12: Agent 工程实践

- [Hermetic Fixture 与真实 Provider Smoke](./12-engineering-practice/01-fixtures-and-smoke.md) — `practice-fixtures-smoke` — `draft` — 规划路径: `12-engineering-practice/01-fixtures-and-smoke.md`
- [Unit、Contract、Integration 与 Browser Test](./12-engineering-practice/02-test-layers.md) — `practice-test-layers` — `draft` — 规划路径: `12-engineering-practice/02-test-layers.md`
- [并发测试、Race 与确定性同步](./12-engineering-practice/03-concurrency-and-race.md) — `practice-concurrency-race` — `draft` — 规划路径: `12-engineering-practice/03-concurrency-and-race.md`
- [Benchmark、性能预算与回归](./12-engineering-practice/04-benchmark-and-performance.md) — `practice-benchmark` — `draft` — 规划路径: `12-engineering-practice/04-benchmark-and-performance.md`
- [跨平台构建与能力探测](./12-engineering-practice/05-cross-platform.md) — `practice-cross-platform` — `draft` — 规划路径: `12-engineering-practice/05-cross-platform.md`
- [Binary、Web Asset、SBOM 与 Release Evidence](./12-engineering-practice/06-release-evidence.md) — `practice-release-evidence` — `draft` — 规划路径: `12-engineering-practice/06-release-evidence.md`
- [如何阅读和修改大型 Agent 工程](./12-engineering-practice/07-reading-codebase.md) — `practice-reading-codebase` — `draft` — 规划路径: `12-engineering-practice/07-reading-codebase.md`
- [架构度量与回归棘轮](./12-engineering-practice/08-architecture-ratchet.md) — `practice-architecture-ratchet` — `verified` — 规划路径: `12-engineering-practice/08-architecture-ratchet.md`

### 部分 13: 动手实验

- [构建并追踪第一个 Agent Turn](./13-hands-on-labs/01-first-agent-turn.md) — `lab-first-turn` — `draft` — 规划路径: `13-hands-on-labs/01-first-agent-turn.md`
- [使用 Fixture 观察 Streaming Event](./13-hands-on-labs/02-streaming-fixture.md) — `lab-streaming-fixture` — `draft` — 规划路径: `13-hands-on-labs/02-streaming-fixture.md`
- [实现 Provider Adapter](./13-hands-on-labs/03-provider-adapter.md) — `lab-provider-adapter` — `draft` — 规划路径: `13-hands-on-labs/03-provider-adapter.md`
- [实现通过 Guard 的 Tool](./13-hands-on-labs/04-governed-tool.md) — `lab-governed-tool` — `draft` — 规划路径: `13-hands-on-labs/04-governed-tool.md`
- [构造 Approval 与 Denial](./13-hands-on-labs/05-approval-and-denial.md) — `lab-approval-denial` — `draft` — 规划路径: `13-hands-on-labs/05-approval-and-denial.md`
- [构建可恢复 Workflow](./13-hands-on-labs/06-recoverable-workflow.md) — `lab-recoverable-workflow` — `verified` — 规划路径: `13-hands-on-labs/06-recoverable-workflow.md`
- [调试 Worker Lease 与 Retry](./13-hands-on-labs/07-worker-lease-retry.md) — `lab-worker-retry` — `verified` — 规划路径: `13-hands-on-labs/07-worker-lease-retry.md`
- [完成 Web 端到端功能](./13-hands-on-labs/08-web-feature.md) — `lab-web-feature` — `draft` — 规划路径: `13-hands-on-labs/08-web-feature.md`
- [从 Trace 复盘一次失败](./13-hands-on-labs/09-trace-failure.md) — `lab-trace-failure` — `verified` — 规划路径: `13-hands-on-labs/09-trace-failure.md`
- [设计并验证新的 Agent 能力](./13-hands-on-labs/10-new-agent-capability.md) — `lab-new-capability` — `draft` — 规划路径: `13-hands-on-labs/10-new-agent-capability.md`

---

不要直接编辑此文件。修改 `docs/book/catalog.json` 后运行 `python3 scripts/render-book-navigation.py`。
