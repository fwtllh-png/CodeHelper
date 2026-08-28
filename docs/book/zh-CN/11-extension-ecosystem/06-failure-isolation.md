---
id: extension-failure-isolation
title: Extension Failure 与隔离策略
audience:
  - contributor
  - operator
prerequisites:
  - extension-mcp
  - extension-skill-plugin-hook
code_paths:
  - internal/adapter/mcp
  - internal/adapter/skill
  - internal/adapter/hooks
  - internal/runtime/extension
  - internal/runtime/app/extension
test_paths:
  - internal/adapter/mcp/pool_t3_test.go
  - internal/adapter/skill/resolver_test.go
  - internal/runtime/extension/state_test.go
  - internal/runtime/app/wire/extension_control_test.go
source_of_truth:
  - internal/adapter/mcp/health.go
  - internal/adapter/skill/catalog.go
  - internal/adapter/hooks/manager.go
  - internal/runtime/app/extension/control.go
status: draft
last_verified: null
---

# Extension Failure 与隔离策略

## 学习目标

设计 Extension Failure Domain、Stable Error Category、Revocation、Timeout、Resource
Cleanup 与 Degraded Operation。

## Isolation Layers

```mermaid
flowchart TD
    X[Extension] --> I[Identity / Authority]
    I --> C[Capability / Resource]
    C --> P[Process / Network / Sandbox]
    P --> H[Health / Circuit / Timeout]
    H --> R[Generation / Revocation]
    R --> O[Observable Degraded State]
```

MCP 隔离每个 Server Connection/Circuit；Failed Server 不隐藏 Healthy Catalog。Probe
测试恢复但不 Replay Business Call。Skill 在 Context Injection 前完成 Bounded
Discovery/Resolution。Hook 受 Timeout/Cancellation 约束且 Fail Closed。

Stable Error Category 区分 Unavailable、Circuit-open、Stale Catalog、Dependency
Conflict、Signature/Tamper 与 Policy Denial。Recoverable Category 可反馈模型；
Security/Unknown Failure 为 Terminal。

Degraded Mode 必须显式：Unavailable Descriptor、Health Snapshot、Issue、Lifecycle
Receipt 与 Log 说明缺失。Silent Removal 会让模型/Operator 错误推断 Capability。

## Extension Lifecycle

Validation 证明 Shape/Integrity；Authorization 授予 Bounded Capability。MCP 用 Server
Generation 和 Catalog Revision 隔离连接与调用，Skill 用 Lock 和 Enable State 控制
加载，Hook 用 Runtime Lease 和进程树清理约束执行。Control Mutation 使用 Durable
Prepare/Commit Receipt 与幂等 Operation ID。

## Failure Domain Matrix

| Failure | Containment | Recovery |
| --- | --- | --- |
| Provider | One Sample/Route | Meaningful Output 前 Retry |
| Tool | One Bound Call/Turn | 按 Effect Phase Feedback/Terminal |
| MCP | One Server Circuit/Source/Effect Owner | Probe/Reconnect/New Generation |
| Skill | One Dependency Plan | Repair Lock/Reload |
| Hook | One Callback | Kill Tree/Failure Policy |
| Host | One Transport | Cursor Replay |

Bulkhead 同时需要 Resource/Authority Isolation：独立 Timeout、Process/Connection、Output
Limit、Catalog Source、Cancellation、Generation Fence。

## Feedback/Observability

Model Feedback 只包含 Stable、Actionable、Sanitized Category 和 Retry Safety。Operator
Record 保留 Extension Identity、Source、Plan Revision、Permission Digest、Generation、
Transition、Bounded Cause 与 Affected Call。Unknown、Tamper、
Partial-effect Failure 不转成 Retry Advice。

Lifecycle Fact 也以脱敏 Evidence 进入 Observation Router。Observation/Exporter Failure
不能让 Failed Extension 变成 Healthy，也不能改变 Call 的业务结果。

Health 不是 Authority。Healthy Process + Revoked Generation 仍不可运行；Optional
Extension Unhealthy 不影响无关 Runtime Function。

## 失败与安全边界

- Extension 不能运行时扩大 Capability。
- Revocation 高于 Cached/Loaded Authority。
- Cancellation 关闭 Process Group/Transport。
- Retry 有界且不重复 Meaningful Work。
- Source Reconcile 不替换其他 Source Tool。
- Error/Log Bounded/Redacted。
- Disable 不能遗留无归属 Live Effect。
- Receipt/Observation Projection 不能成为 Lifecycle Authority。

## 测试与验证

```bash
go test ./internal/adapter/mcp -run 'Test(Pool|Circuit)'
go test ./internal/adapter/skill ./internal/adapter/hooks
go test ./internal/runtime/extension ./internal/runtime/app/extension
```

## 动手实验

分别制造 MCP Server Failure、Skill Lock Drift 和 Hook Timeout，对比 Catalog
Visibility、进程清理与 Model Feedback。

## 复习问题

1. 为什么显式 Unavailable 而非静默隐藏？
2. Catalog Generation 变化必须使什么失效？
3. 哪些 Extension Failure 可安全反馈模型？
4. Disable、Circuit Open 与 Hook Cancel 有何区别？
5. Isolation 为什么还需要 Generation Fence？

## 延伸阅读

- [Tool Failure 如何反馈给模型](../06-tools-and-execution/06-failure-feedback.md)

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `extension-failure-isolation` |
| 状态 | `draft` |
| 最后验证 | 尚未验证 |
