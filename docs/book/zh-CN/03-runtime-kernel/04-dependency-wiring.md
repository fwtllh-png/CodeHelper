---
id: runtime-wiring
title: 依赖构造与能力装配
audience:
  - contributor
  - agent
prerequisites:
  - overview-system-architecture
  - runtime-agent-loop
code_paths:
  - internal/runtime/app/wire
test_paths:
  - internal/runtime/app/wire/bootstrap_test.go
  - internal/runtime/app/wire/model_test.go
  - internal/runtime/app/wire/sandbox_architecture_test.go
source_of_truth:
  - internal/runtime/app/wire/runtime.go
  - internal/runtime/app/wire/route.go
status: draft
last_verified: null
---

# 依赖构造与能力装配

简体中文 | [English](../../en/03-runtime-kernel/04-dependency-wiring.md)

## 学习目标

理解 Construction 为什么隔离、Config 如何成为 Runtime，以及 Turn 开始前 Wiring
必须建立哪些不变量。

## 前置知识

阅读[全局架构](../02-codehelper-overview/02-system-architecture.md)和
[Agent Loop](./03-agent-loop.md)。

## 问题背景

Provider、Credential、Route、Sandbox、Tool、Journal、Trace、MCP、Plugin、
Persistence 和 Context Budget 必须一致连接。每个 Host 分别构造会复制 Policy；
Agent Loop 内构造则混淆 Configuration 与 Behavior。

## Construction Flow

```mermaid
flowchart TD
    C[Validated Config] --> R[Routes and Credentials]
    C --> S[Sandbox and Workspace]
    C --> P[Policy and Constitution]
    R --> E[Provider and Agent Engine]
    S --> G[Registry and Guard]
    P --> G
    G --> E
    C --> D[Persistence / Trace / Journal]
    D --> A[Application Runtime]
    E --> A
    A --> H[Host Facade]
```

## Wiring 的职责

`internal/runtime/app/wire` 是 Composition Root，负责：

- 解析 Model Catalog Route 与 Credential Reference；
- 创建 Provider HTTP Client 或 Fixture；
- 构建 Tool Registry、Guard、Policy、Constitution、Sandbox；
- 组装 Stable Prompt Context 与 Partition Budget；
- 连接 Journal、Diagnostics、Verify、Trace、Usage 和 Store；
- 初始化 MCP、Skill、Plugin、Hook、Dynamic Tool；
- 构建 Child Runtime、Worktree 与 Background Executor；
- 选择 Persistent 或 In-memory Application Runtime。

## Startup Invariant

Route/Capability/Config 无效、Workspace Identity 无法 Canonicalize、Required Sandbox
无法注入、Persistence Recovery 失败、Executor/Tool Identity 冲突时，Construction
直接失败，而不是创建部分可信 Runtime。

## Startup Ordering 与 Ownership Transfer

Construction 遵循 Dependency Order，而不是简单 Constructor List：

1. Validate Config 并 Canonicalize Workspace；
2. 打开 Durable Store，在 Acceptance 前 Recovery；
3. 解析 Model Metadata、Route、Limit、Credential Reference；
4. Probe Platform/Sandbox；
5. 构建 Policy、Permission、Constitution、Registry、Guard；
6. 初始化 Extension 并 Reconcile Catalog；
7. 连接 Context、Evidence、Diagnostics、Verify、Usage、Trace；
8. 创建 Thread/Engine Factory；
9. 创建 Application Runtime，最后才暴露 Host Facade。

每步成功后 Ownership 转移。后续步骤失败时，已打开 Store、Transport、Extension Process、
Background Manager 必须逆序 Close。即使 Turn 尚未开始，Constructor Failure 泄漏
Process 也违反 Runtime Contract。

## Capability Provenance

| Fact | 首选来源 |
| --- | --- |
| Model 支持 Tool/Vision | Catalog + Probe Observation |
| Credential Available | Execution Boundary 的 Reference Resolution |
| Strong Sandbox Available | Platform Backend Probe |
| Tool Executable | Registry Descriptor + Dependency Health |
| Extension Trusted | Manifest/Signature/Authority Receipt |
| Persistent State Ready | Open、Schema Check、Recovery 成功 |

Configuration 表达 Intent，不能制造 Environment Capability。

## 代码地图

| 关注点 | 源码 |
| --- | --- |
| Main Assembly | `runtime.go` |
| Route/Budget | `route.go`、`routeset.go` |
| Provider | `model.go` |
| Persistence | `persistent.go` |
| Sandbox Fact | `sandbox_info.go` |
| MCP/Extension | `mcp.go`、`extensions.go` |
| Child/Background | `childruntime.go`、`background_executors.go` |

## 实现导读

Builder Canonicalize Workspace，构建 Store/Observability，加载 Constitution，合并
Policy，创建 Registry/Guard，组装 Prompt Context，再通过 Thread Manager 构造
Agent Engine。Application Runtime 只接收 Engine Interface 与 Durable Facility；
Host 不获得 Provider 或 Guard 实例。

## 设计取舍与替代方案

DI Framework 可以自动化 Graph，却可能隐藏 Ordering 与 Security Decision。CodeHelper
使用显式 Go Construction，使 Reviewer 能追踪进入 Guard 的 Backend 和 Policy。
Composition Root 较大时应按语义拆 Helper，而不是把 Business Loop 移入 Wiring。

## 失败模式与安全边界

- Host 不能构造 Unguarded Registry Shortcut。
- Fixture 与 Live Route 必须显式。
- Sandbox Capability 来自 Backend Probe，而非 Config 声明。
- Secret Value 在 Provider Execution Boundary 解析。
- Child Runtime 的 Budget/Depth/Workspace 必须窄于 Parent Authority。

## 测试与验证

```bash
go test ./internal/runtime/app/wire
go test ./internal/host/cli -run TestCLIDoesNotDependOnExecutionImplementations
```

## 动手实验

从 CLI `exec` 只追踪 Constructor，直到 `app.NewRuntime` 或
`NewRuntimeWithRecovery`。记录 Route、Guard、Sandbox、Journal、Prompt Context
在哪里接入；这条路径不应出现 Business Turn Step。

## 复习问题

1. Wiring 为什么可以依赖具体实现而 Host 不可以？
2. 哪些构造失败必须阻止 Runtime Startup？
3. Capability Probe 为什么优先于 Config Claim？
4. Host Facade 为什么最后暴露？
5. Construction 中途失败时必须关闭哪些 Resource？

## 延伸阅读

- [Application Runtime](./02-application-runtime.md)
- [Provider Adapter、Model Catalog 与 Wire ID](../04-model-and-provider/02-provider-and-catalog.md)

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `runtime-wiring` |
| 状态 | `verified` |
| 最后验证 | 2026-08-06 |
