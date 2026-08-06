---
id: overview-system-architecture
title: CodeHelper 全局架构
audience:
  - learner
  - contributor
  - agent
prerequisites:
  - agent-why-governed-runtime
  - overview-positioning
code_paths:
  - cmd/codehelper
  - internal/host
  - internal/runtime
  - internal/adapter
  - internal/security
  - internal/orchestration
  - internal/persist
  - internal/observability
  - internal/platform
test_paths:
  - internal/host/cli/architecture_test.go
  - internal/runtime/protocol/schema_test.go
source_of_truth:
  - docs/zh-CN/architecture.md
  - docs/protocol/runtime-protocol.schema.json
status: verified
last_verified: 2026-08-06
---

# CodeHelper 全局架构

简体中文 | [English](../../en/02-codehelper-overview/02-system-architecture.md)

## 学习目标

理解主要分层、依赖方向、Runtime Protocol，以及 CodeHelper 为什么让多种 Host 共享
同一个执行核心。

## 前置知识

阅读[为什么 Agent 需要受治理的 Runtime](../01-agent-engineering/05-why-governed-runtime.md)。

## 问题背景

Coding Agent 会逐渐拥有 CLI、TUI、编辑器、HTTP Client、后台 Worker 和 Child Agent。
如果每个入口分别构造 Provider、执行 Tool 或保存 State，安全、取消、恢复和证据语义
必然分叉。因此架构需要定义权限与依赖方向，而不只是列出目录。

## 同一系统的三个视图

不要只靠一张 Package Diagram 理解 CodeHelper：

| 视图 | 问题 | 路径 |
| --- | --- | --- |
| Control | 谁可请求并授权 Effect？ | Host → Operation → Runtime → Guard → Platform |
| Data | Fact 如何流动和持久化？ | Provider/Tool → Event/Receipt → Event Log/SQLite → Projection |
| Construction | 谁选择 Concrete Implementation？ | Config → `runtime/app/wire` → Interface/Adapter |

正确 Change 必须同时符合三种视图。例如新 Tool 需要 Construction Path、经过 Guard 的
Control Path，以及通过 Event/Receipt 的 Evidence Path；只实现 Executor 不完整。

## 核心概念

- **Host** 把用户或 Client I/O 转换为 Operation 和 Event。
- **Runtime** 管理 Lifecycle、Agent Execution 与共享契约。
- **Adapter** 连接外部 Model、Tool 和扩展生态。
- **Security** 决策并实施权限。
- **Persistence/Observability** 保存事实与证据。
- **Orchestration** 调度持久或多步骤工作。
- **Platform** 实现 OS Process 与 Sandbox 能力。

## CodeHelper 设计

```mermaid
flowchart TB
    subgraph Hosts
      CLI
      TUI
      VSC[VS Code]
      API[ACP]
    end
    P[Runtime Protocol]
    APP[Application Runtime]
    ENG[Agent Engine]
    ADP[Provider / Tool / Extension Adapters]
    SEC[Policy / Guard / Sandbox]
    ORC[Task / Workflow / Fleet]
    PER[SQLite / Events / CAS / Journal]
    OBS[Trace / Usage / Verify]
    PLT[Process / OS]
    Hosts --> P --> APP --> ENG
    ENG --> ADP
    ADP --> SEC --> PLT
    APP --> PER
    ENG --> OBS
    ORC --> APP
```

图中的箭头不是“上层可任意调用所有下层”。Host 可以使用 Protocol 和 Application
Facade，但不能直接依赖 Tool、Provider、Agent Engine 或 Sandbox 实现。

## Package 分层

| 层 | 路径 | 职责 |
| --- | --- | --- |
| Entry | `cmd/codehelper` | Process Startup 与 CLI Root |
| Host | `internal/host` | Presentation 与 Transport Adapter |
| Runtime | `internal/runtime` | Protocol、Lifecycle、Agent Loop、Wiring |
| Adapter | `internal/adapter` | Provider、Tool、MCP、Skill、Plugin、Hook |
| Security | `internal/security` | Policy、Permission、Constitution、Sandbox |
| Orchestration | `internal/orchestration` | Task、Worker、Workflow、Fleet |
| Persistence | `internal/persist` | SQLite、Event、CAS、Session、Journal |
| Observability | `internal/observability` | Trace、Usage、Diagnostics、Verification |
| Platform | `internal/platform` | Process 与 OS Integration |
| VS Code | `extensions/vscode` | Editor UI 与 ACP Client |

## 硬依赖规则

1. `runtime/protocol` 不依赖实现 Package。
2. Host 提交 Operation，不执行 Provider 或 Tool。
3. Turn 业务循环留在 `runtime/agent`。
4. 构造逻辑留在 `runtime/app/wire`。
5. 有后果的 Tool 经过 `adapter/tool/guard`。
6. UI State 是 Projection，不是 Runtime Truth。
7. 持久副作用在 Owner Boundary 内使用 Transaction 或 Journal。

`internal/host/cli/architecture_test.go` 会解析 Import；CLI 直接依赖执行实现时测试失败。
架构边界因此是可验证属性。

## Runtime Protocol

```text
Operation -> ordered Events -> Projection
                     \-> Receipt
```

`internal/runtime/protocol` 定义 Operation/Event Tagged Union，生成的
`docs/protocol/runtime-protocol.schema.json` 被 ACP 和 VS Code 共用。ACP 封装同一模型，
而不是定义另一个 Runtime。

Event 包含 Sequence、Operation、Thread、Turn 和 Item Identity，使 Host 可以按 Cursor
重连，并区分 Output、Tool、Approval、Verification 与 Terminal Outcome。

## Construction Boundary

`runtime/app/wire` 可以了解具体实现，负责：

- 解析 Model Route 与 Credential；
- 创建 Tool Registry 与 Guard；
- 组装 Prompt Context 与 Budget；
- 连接 Journal、Diagnostics、Trace 和 Persistence；
- 将 Agent Engine 适配为 Application Engine；
- 创建 Persistent 或 In-memory Runtime。

构造与业务循环分离，避免 Dependency Injection 代码成为第二套 Runtime。

## Persistence 与 Orchestration

SQLite 保存关系 Projection，Event Log 保存有序事实，CAS 保存不可变 Payload，
Snapshot 加速恢复，Workspace Journal 记录 Edit Before-image。

Task、Worker、Workflow、Lane、Fleet 和 Subagent 最终仍回到 Runtime、Guard 和
Sandbox。Orchestration 不是治理例外。

## 设计取舍与替代方案

Monolith 初期更简单，但会隐藏 Owner 并产生循环。每个 Host 一套 Runtime 会降低 UI
局部复杂度，却造成语义分叉。CodeHelper 接受更多 Interface 与 Adapter，以换取统一
权限路径和可测试边界。

Protocol 会增加版本管理成本；显式契约仍优于终端、插件和 Engine 之间的隐式耦合。

## 失败模式与安全边界

- Host 专属执行路径会绕过统一 Guard。
- Host 直接依赖 Provider 会让模型行为受 Presentation 影响。
- `wire` 中的业务逻辑难以在不构造全部依赖时测试。
- 把 Projection 当事实会破坏 Replay 与 Recovery。
- 绕过 Persistence Owner 写入会分裂关系状态与 Event/Journal。
- Extension 直接执行会形成未治理控制面。

## 测试与验证

```bash
go test ./internal/host/cli -run TestCLIDoesNotDependOnExecutionImplementations
go test ./internal/runtime/protocol -run TestTheCommittedSchemaMatchesThisBuild
go test ./internal/runtime/app -run TestRuntimeUnsupportedOperationIsExplicitlyRejected
```

## 动手实验

按顺序定位 `OperationStartTurn`、`Runtime.Submit`、`EngineAdapter.StartTurn`、
`Engine.RunForTurn`、`Guard.ExecuteBound` 和 `wire.NewRuntime` 调用点，再运行
Architecture Test，确认源码路径与架构图一致。

## 复习问题

1. 为什么 `wire` 可以依赖具体实现而 Host 不可以？
2. 什么使 HTTP 与 ACP 成为两个 Transport，而不是两个 Runtime？
3. 半完成 Edit 的恢复应由哪个持久化组件负责？
4. 新 Capability 的 Control、Data、Construction Path 分别是什么？

## 延伸阅读

- [架构手册](../../../zh-CN/architecture.md)
- [Turn 生命周期](./05-turn-lifecycle.md)
- [Guard、Approval、Constitution 与 Sandbox](../07-security-governance/03-approval-constitution-sandbox.md)

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `overview-system-architecture` |
| 状态 | `verified` |
| 最后验证 | 2026-08-06 |
