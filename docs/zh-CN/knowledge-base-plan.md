# Agent 工程知识文档体系建设方案

简体中文 | [English](../en/knowledge-base-plan.md)

## 1. 文档状态

本文定义 CodeHelper 知识书籍的目标、信息架构、写作契约、建设顺序和验收标准。它是
文档建设方案，不代表其中列出的章节已经完成。已经交付的能力仍以当前代码、测试和
`docs/zh-CN` 产品手册为事实来源。

## 2. 建设目标

CodeHelper 的文档不应只回答“命令怎么运行”，还应系统回答：

- Agent 要解决什么问题，为什么需要 Runtime；
- 模型、上下文、工具、状态、编排和安全如何协作；
- CodeHelper 为什么采用当前架构，而不是其他实现；
- 每个模块在代码中的边界、数据结构和执行流程是什么；
- 如何通过测试、Trace、故障和实验验证这些设计；
- 学习者如何从使用者逐步成长为 Agent Runtime Contributor。

最终成果应像一本可以执行的工程书籍：读者既能连续阅读，也能进入源码、运行命令、
修改模块并观察结果。

## 3. 目标读者

| 读者 | 主要问题 | 推荐内容 |
| --- | --- | --- |
| Agent 初学者 | Agent 与普通 Chatbot 有什么不同 | 基础概念、全景架构、首个 Turn |
| 应用开发者 | 如何可靠接入模型与工具 | Provider、Context、Tool、安全 |
| Runtime 开发者 | 如何实现状态机、协议与恢复 | Runtime、持久化、编排、可观测性 |
| 平台与安全工程师 | 如何治理有副作用的执行 | Guard、Policy、Sandbox、Journal |
| CodeHelper Contributor | 应修改哪个 Package，如何验证 | 模块实现、测试地图、扩展教程 |
| Coding Agent | 哪些契约和代码是事实来源 | 元数据、代码路径、测试路径、不变量 |

## 4. 文档分层

知识书籍不替代现有产品文档。整个文档体系分为三层。

### 4.1 产品手册

位置：`docs/en`、`docs/zh-CN`

负责回答当前版本“是什么、怎么用、怎么配置、怎么排障”，包括：

- Quick Start、Configuration、Usage；
- Architecture 与 Security 概览；
- VS Code、本地开发、Roadmap、Troubleshooting；
- Contributor 与 Agent 的仓库工作规则。

产品手册应简洁、可检索、面向任务，不承担完整技术教学。

### 4.2 Agent 工程知识书籍

规划位置：

```text
docs/book/
├── en/
└── zh-CN/
```

负责从基础原理进入 CodeHelper 设计和源码实现，形成连续阅读路径。章节可以引用产品
手册，但不能复制配置字段和命令参考。

### 4.3 机器可读参考

包括：

- Runtime Protocol JSON Schema；
- VS Code Compatibility Contract；
- CLI `--help`；
- Config Schema 与校验逻辑；
- 生成的 TypeScript Protocol Type；
- 测试 Fixture、Golden File 和 Release Evidence。

书籍解释这些契约，机器可读文件仍是可验证的事实来源。

## 5. 全书结构

### 第一部分：进入 Agent 工程

1. 从 Chatbot 到 Agent
2. LLM、Token、Context Window 与 Sampling
3. ReAct、Planning、Tool Calling 与 Reflection
4. Agent、Workflow 和 Automation 的边界
5. 为什么 Agent 需要 Runtime 与治理

### 第二部分：认识 CodeHelper

1. 项目定位、价值和非目标
2. 一套 Runtime、多种 Host
3. Package Ownership 与依赖方向
4. Operation、Event、Receipt、Projection
5. 一次 Turn 的完整生命周期
6. 从 CLI 到模型再到 Tool 的第一条数据流

### 第三部分：Runtime 内核

1. `protocol`：稳定的数据契约
2. `app`：Session、Operation 与状态投影
3. `agent`：模型和工具执行循环
4. `wire`：依赖构造与能力装配
5. Streaming、Cancellation、Error Taxonomy
6. Resume、Recovery 与幂等边界

### 第四部分：模型与 Provider

1. Chat Completion 与 Responses 协议
2. Provider Adapter、Model Catalog 与 Wire ID
3. Capability Negotiation 与 Route Resolution
4. Streaming、Reasoning、Tool Call 和 Usage
5. Credential Reference 与 Secret Lifecycle
6. Retry、Rate Limit、Timeout 与故障分类

### 第五部分：Context Engineering

1. Prompt、Message 与 Context 的区别
2. Workspace、Repository Index 与 Editor Context
3. Context Source、优先级与生命周期
4. Token Budget、Compaction 与信息损失
5. Memory、Snapshot 与恢复
6. 如何评估 Context Quality

### 第六部分：Tool 与受控执行

1. Tool Schema、Registry 与 Dynamic Catalog
2. File、Shell 与 Agent Tool
3. Tool Guard 执行管线
4. Edit Plan、Journal 与 Receipt
5. Verification Gate 与证据
6. Tool Failure 如何反馈给模型

### 第七部分：安全与治理

1. Agent Runtime Threat Model
2. Mode、Posture、Policy 与 Permission
3. Approval 与 Constitution
4. OS Sandbox 和 Process Isolation
5. Egress、Credential 与数据泄漏
6. MCP、Skill、Plugin、Hook Trust
7. Fail-closed 与平台能力声明

### 第八部分：状态与可观测性

1. Durable State 的必要性
2. SQLite Schema、Event Log 与 Projection
3. Session、Snapshot、CAS 与 Workspace Journal
4. Trace、Span、Usage 与 Cost
5. Diagnostics、Maturity 与 Verification
6. 从一次失败运行还原系统行为

### 第九部分：Task 与编排

1. Task、Worker 和 Executor
2. Lease、Heartbeat、Retry 与幂等性
3. Automation 与 Workflow
4. Checkpoint 与恢复
5. Lane、Fleet 和调度
6. Subagent、Worktree 与拓扑关系

### 第十部分：Host 与协议

1. CLI 和 Machine-readable Output
2. TUI State Projection
3. HTTP/SSE Runtime API
4. ACP Stdio 与编辑器互操作
5. Web Control Surface
6. VS Code Context Bridge、Trust 与 Compatibility

### 第十一部分：扩展生态

1. 新增 Provider
2. 新增受治理 Tool
3. 接入 MCP Server
4. 编写 Skill、Plugin 与 Hook
5. 新增 Host 而不复制 Runtime
6. Extension Failure 与隔离策略

### 第十二部分：Agent 工程实践

1. Hermetic Fixture 与真实 Provider Smoke
2. Unit、Contract、Integration 与 Electron Test
3. 并发测试、Race 与确定性同步
4. Benchmark、性能预算与回归
5. 跨平台构建、Sandbox 差异与能力探测
6. VSIX、SBOM、Provenance 与 Release Evidence
7. 如何阅读和修改大型 Agent 工程

### 第十三部分：动手实验

1. 构建并追踪第一个 Agent Turn
2. 使用 Fixture 观察 Streaming Event
3. 实现一个 Provider Adapter
4. 实现一个通过 Guard 的 Tool
5. 构造一次 Approval 与 Denial
6. 构建可恢复 Workflow
7. 调试 Worker Lease 与 Retry
8. 完成一个 VS Code 端到端功能
9. 从 Trace 复盘一次失败
10. 设计并验证一个新的 Agent 能力

## 6. 模块覆盖地图

| 书籍主题 | 主要代码范围 |
| --- | --- |
| Runtime Protocol | `internal/runtime/protocol` |
| Application Runtime | `internal/runtime/app` |
| Agent Loop | `internal/runtime/agent` |
| Dependency Wiring | `internal/runtime/app/wire` |
| Provider、Model、Tool | `internal/adapter` |
| Policy 与 Sandbox | `internal/security` |
| Task 与 Workflow | `internal/orchestration` |
| Durable State | `internal/persist` |
| Trace、Usage、Verify | `internal/observability` |
| CLI、TUI、API Host | `internal/host` |
| OS 与 Process | `internal/platform` |
| VS Code | `extensions/vscode` |

每个主要 Package 最终都应至少有一章设计导读、一张执行流程图和一条可运行验证路径。

## 7. 章节写作契约

每章使用统一结构：

1. 学习目标；
2. 前置知识；
3. 问题背景；
4. 核心概念；
5. CodeHelper 的设计；
6. 执行流程与架构图；
7. 关键代码地图；
8. 实现细节；
9. 设计取舍与替代方案；
10. 失败模式和安全边界；
11. 测试与验证；
12. 动手实验；
13. 复习问题；
14. 延伸阅读；
15. 事实来源和最后校验信息。

章节顶部增加机器可读元数据：

```yaml
---
id: runtime-operation-lifecycle
title: 一次 Operation 的生命周期
audience:
  - learner
  - contributor
  - agent
prerequisites:
  - agent-runtime-overview
code_paths:
  - internal/runtime/protocol
  - internal/runtime/app
test_paths:
  - internal/runtime/app/*_test.go
source_of_truth:
  - docs/protocol/runtime-protocol.schema.json
status: planned
last_verified: null
---
```

## 8. 写作原则

- **由外及里**：先解释用户可观察行为，再进入协议、状态和实现。
- **由浅入深**：先建立术语和心智模型，再讨论并发、恢复和安全细节。
- **概念连接代码**：重要结论必须关联源码、测试或机器契约。
- **解释原因**：不仅描述当前实现，还说明约束、取舍和被放弃的替代方案。
- **实验可复现**：命令、Fixture、预期输出和清理步骤必须明确。
- **事实与规划分离**：未交付内容标记为 Planned，不能写成现状。
- **避免源码复刻**：只摘录理解设计所需的最小代码，避免文档随重构整体失效。
- **安全默认**：示例不包含真实 Secret，不引导绕过 Guard 或 Sandbox。
- **双语同等完整**：中英文具有相同章节结构、代码事实和验收标准。

## 9. 图表体系

优先使用可版本管理的 Mermaid 图，包括：

- C4 Context、Container 和 Component 图；
- Package 依赖图；
- Turn、Tool、Approval、Resume 时序图；
- Operation、Task、Workflow 状态机；
- Provider、Context、Tool 数据流；
- Security Control Stack；
- SQLite、Event 和 Projection 关系图；
- Worker、Lane、Fleet 与 Subagent 拓扑图。

图表必须有正文解释，不能以图片替代语义。复杂图应拆分为“全景图 + 局部图”。

## 10. 双语策略

- `docs/book/en` 与 `docs/book/zh-CN` 使用相同相对路径和章节 ID；
- 一个功能变更不能只更新单一语言的事实描述；
- 术语表维护唯一英文术语、推荐中文译法和禁用歧义；
- 代码标识、协议名和 CLI 参数保持原文；
- 翻译应保持技术语义，不要求机械逐句对应；
- 文档检查应拒绝缺失镜像、章节 ID 不一致和目录漂移。

## 11. 自动化与质量门禁

在现有 `make docs-check` 基础上逐步增加 `make book-check`：

1. Markdown Link 和图片目标存在；
2. 中英文目录与文件镜像；
3. Front Matter 字段和章节 ID 合法；
4. `code_paths`、`test_paths`、`source_of_truth` 存在；
5. 目录中的章节顺序与文件一致；
6. 不引用已删除文档或历史品牌；
7. Mermaid Code Block 可解析；
8. 标记为 Verified 的命令具有对应自动化检查；
9. 不包含 Key-shaped Secret；
10. 章节更新时间和实现变更之间不存在明确漂移。

`book-check` 最终进入 `make verify`，但耗时较重的可执行 Lab 保持独立门禁。

## 12. 建设阶段

### 阶段 0：规范和骨架

- 创建 `docs/book/en` 与 `docs/book/zh-CN`；
- 定义章节模板、Front Matter Schema、术语表和导航；
- 扩展文档检查，支持递归双语镜像；
- 建立“已发布、草稿、规划”状态标记。

验收：空缺章节可见但不会被误认为已交付；中英文骨架和自动检查通过。

### 阶段 1：全景阅读闭环

优先完成：

1. 为什么需要受治理的 Coding Agent Runtime；
2. CodeHelper 全局架构；
3. 一次 Agent Turn 如何运行；
4. Model、Context 与 Tool 如何协作；
5. Guard、Approval 与 Sandbox；
6. 从源码运行并追踪第一个真实任务。

验收：新读者可以在不预读源码的情况下建立正确心智模型，并完成第一个实验。

### 阶段 2：Runtime 核心

- Protocol、App、Agent、Wire；
- Provider 与 Model；
- Context Engineering；
- Tool、Guard 与 Verification；
- 补齐核心状态机和时序图。

验收：核心 Package 均有设计、实现、测试和实验四类入口。

### 阶段 3：持久化与编排

- SQLite、Event、Session、Snapshot、Journal；
- Task、Worker、Automation、Workflow；
- Lane、Fleet、Subagent 与 Worktree；
- 故障恢复、重试和幂等案例。

验收：读者可以解释并验证后台任务从创建到恢复的完整生命周期。

### 阶段 4：Host 与扩展生态

- CLI、TUI、HTTP/SSE、ACP、Web、VS Code；
- MCP、Skill、Plugin、Hook；
- Provider、Tool 和 Host 扩展教程。

验收：读者可以在不破坏 Runtime 边界的前提下完成一个扩展。

### 阶段 5：工程实践与 Lab

- 测试、Benchmark、Security、Release；
- 系统化动手实验；
- 真实故障复盘和调试路径；
- 学习路线与复习问题。

验收：实验具有前置条件、预期结果、失败诊断和清理步骤，可在支持的平台重复执行。

### 阶段 6：持续治理

- 将模块 Owner 与章节关联；
- PR Checklist 增加文档影响判断；
- Release 前执行事实校验；
- 定期检查代码引用、命令和截图漂移；
- 根据读者反馈调整阅读顺序。

## 13. 单章完成标准

一章只有同时满足以下条件才标记为 `verified`：

- 中英文版本均存在；
- 学习目标和前置知识明确；
- 核心结论关联当前代码或协议；
- 关键流程有文本说明，必要时有图；
- 包含成功路径和至少一个失败模式；
- 验证命令实际执行并记录平台边界；
- 代码路径、测试路径和链接通过自动检查；
- 没有把 Roadmap 写成已交付能力；
- 没有真实 Secret、个人绝对路径或不可复现依赖；
- 由熟悉对应模块的人或 Agent 基于代码复核。

## 14. 维护规则

- 模块契约变化时，在同一变更中更新对应章节；
- 代码重构后同步修复代码地图，不保留失效路径；
- 公开发布后，兼容性变化必须更新背景解释和迁移说明；
- 历史设计只在解释当前约束时保留，不恢复开发编年史式 RFC；
- 章节内容与代码冲突时，以代码和测试为起点核查，而不是直接假定任一方正确；
- Agent 修改书籍前先读取章节元数据列出的事实来源。

## 15. 非目标

知识书籍不会：

- 成为所有 LLM 论文的百科全书；
- 复制第三方官方文档；
- 用大量源码粘贴替代解释；
- 承诺尚未实现的产品能力；
- 为追求篇幅而记录无长期价值的开发过程；
- 替代代码测试、Schema 或 CLI Help。

## 16. 主要风险与控制

| 风险 | 控制方式 |
| --- | --- |
| 文档规模增长后与代码漂移 | 元数据、路径检查、模块变更 Checklist |
| 书籍与产品手册重复 | 明确“原理教学”和“任务参考”边界 |
| 中英文长期不一致 | 镜像检查、相同章节 ID、同变更更新 |
| 章节只讲概念不落地 | 强制代码地图、测试和 Lab |
| 章节只讲实现缺少背景 | 强制问题背景、取舍和延伸阅读 |
| 图表复杂且难维护 | Mermaid 源码、分层视图、正文解释 |
| 示例误导安全操作 | 使用 Fixture、最小权限和明确平台边界 |

## 17. 下一步

方案获确认后，先执行阶段 0，提交书籍目录、章节模板、术语表、导航和
`book-check` 基础能力；随后按阶段 1 的六章建立第一条完整学习路径。后续章节按
Runtime 核心、持久化编排、Host 生态和工程实践依次推进，避免同时铺开大量只有标题
而没有可验证内容的文档。
