---
id: extension-tool
title: 新增受治理 Tool
audience:
  - contributor
  - agent
prerequisites:
  - tool-schema-registry
  - tool-guard-pipeline
code_paths:
  - internal/adapter/tool
  - internal/adapter/tool/builtin
test_paths:
  - internal/adapter/tool/tool_test.go
  - internal/adapter/tool/guard/guard_test.go
source_of_truth:
  - internal/adapter/tool/tool.go
  - internal/adapter/tool/guard/guard.go
status: draft
last_verified: null
---

# 新增受治理 Tool

简体中文 | [English](../../en/11-extension-ecosystem/02-adding-tool.md)

## 学习目标

实现具有完整 Schema、Resource、Capability、Sandbox、Parallelism、Result 与 Test
Contract 的 Tool。

## 扩展清单

1. 实现 `Executor.Descriptor` 与 `Execute`。
2. 使用 Strict Object Schema 和 `additionalProperties: false`。
3. 声明 Visibility、Capability、Access、Resource Template、Parallel Policy、
   Sandbox Requirement、Availability 与 Alias。
4. 返回 Bounded Model Content 与 Structured Runtime Metadata。
5. 只在任何 Side Effect 前使用 Precondition Error。
6. Mediated File Writer 实现 `EditPlanner`、Atomic Commit 与 Read-before-edit。
7. 在 Adapter/Wire 注册，Host 不直接调用。

```mermaid
flowchart LR
    D[Descriptor] --> R[Registry]
    C[Model Call] --> G[Guard]
    R --> G
    G --> P[Policy / Approval / Claim / Sandbox]
    P --> X[Executor]
    X --> O[Result + Metadata]
```

Resource Resolution 必须在 Policy 前枚举所有可能 Effect。无法从 Normalized Argument
描述 Write Path 时，应拒绝或使用 Trusted Argument Expander。

## Registration、Snapshot 与 Binding

Registry 验证 Descriptor 并分配 Source/Revision。Catalog Snapshot 是 Immutable
Model-visible View。Sampling 将 Tool Name 绑定到 Snapshot；Execution 使用
`ExecuteBound`，因此 Sampling 后 Replace/Revoke 会失败，而非用旧 Model Decision
执行新代码。

```text
register descriptor/executor
 -> validate identity/schema/effect
 -> publish catalog generation
 -> bind sample to generation/revision
 -> guard revalidates bound call
 -> execute exact implementation
```

Alias 归一到 Canonical Identity。Revoke 后重新注册产生 New Revision，Stale Executor
Handle 被 Fence。

## Effect/Result Contract

Descriptor Claim 是 Upper Bound。Resource Expansion 在 Policy 前把 Argument 转为具体
File、Command、Host、Service；Guard 比较 Declared/Observed Effect。

Model Content 是 Bounded Feedback；Structured Metadata、Handle、Journal Change、
Verification 是 Runtime Evidence。“Success”文本不能覆盖 Failed Process、Unobserved
Write 或 Revoked Binding。

## 失败与安全边界

- Tool 不构造/禁用自己的 Sandbox。
- Capability 与真实 Effect 一致。
- Executor Output 不自我授权。
- Partial Side Effect 不得标记 Precondition。
- Oversized Output 使用 Result Handle。
- Guard 观察实际 Write。

## 测试与验证

```bash
go test ./internal/adapter/tool
go test ./internal/adapter/tool/guard
```

## 动手实验

新增 Read-only Fixture Tool，验证 Invalid Schema、Traversal、Unadvertised Binding 在
Execute 前失败。

## 复习问题

1. Resource Resolver 为什么属于 Descriptor？
2. Tool 何时可返回 `ErrPrecondition`？
3. Host 为什么不能直接调用 Executor？
4. 为什么 Sampled Tool 要绑定 Catalog Revision？
5. 哪些 Result 属于 Feedback，哪些属于 Runtime Evidence？

## 延伸阅读

- [Tool Guard 执行管线](../06-tools-and-execution/03-tool-guard-pipeline.md)

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `extension-tool` |
| 状态 | `verified` |
| 最后验证 | 2026-08-06 |
