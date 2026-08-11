---
id: model-provider-failures
title: Retry、Rate Limit、Timeout 与故障分类
audience:
  - contributor
  - operator
  - agent
prerequisites:
  - model-wire-protocols
  - runtime-stream-cancel-errors
code_paths:
  - internal/adapter/provider/httpclient
  - internal/runtime/agent/engine
test_paths:
  - internal/adapter/provider/httpclient/client_test.go
  - internal/adapter/provider/fault_injection_test.go
  - internal/runtime/agent/engine/engine_test.go
source_of_truth:
  - internal/adapter/provider/httpclient/client.go
  - internal/runtime/protocol/problem.go
status: draft
last_verified: null
---

# Retry、Rate Limit、Timeout 与故障分类

简体中文 | [English](../../en/04-model-and-provider/06-provider-failures.md)

## 学习目标

理解 Provider Failure Phase、保守 Retry、Local Rate Limit、Timeout Propagation 与
Sanitized Error Mapping。

## 前置知识

阅读 [Wire Protocol](./01-wire-protocols.md)和
[Streaming/Cancellation](../03-runtime-kernel/05-streaming-cancellation-errors.md)。

## Failure Phase

```mermaid
flowchart LR
    A[Acquire concurrency/rate token] --> B[Encode and send]
    B --> C[Headers/status]
    C --> D[SSE stream]
    D --> E[Normalized Events]
```

Meaningful Stream 之前的失败，在 Request Idempotent 时可能 Retry；产生 Text、Tool Call
等数据后 Retry 可能复制 Output/Effect，因此拒绝。

## Phase/Retry Matrix

| Phase/Failure | Retry Condition | Public Classification |
| --- | --- | --- |
| Local Validation | 不 Retry | Invalid Argument |
| Concurrency/Rate Wait | Caller Context 存活 | Cancel/Deadline 或 Continue |
| Credential Resolution | 不自动 Retry | Unavailable、Non-retryable |
| DNS/TLS/Connect | 仅 Classified Transient | Unavailable |
| HTTP 408/425/429/5xx | Idempotent 且有 Attempt | Unavailable + Rate Metadata |
| Permanent 4xx | 不 | Invalid Argument |
| Header 成功、Decoder 创建失败 | 不 Blind Retry | Protocol/Provider Failure |
| Stream Idle Timeout | Sample Fail | Unavailable |
| Abrupt/Malformed Stream | Stream 开始后不 Replay | Provider Failure |
| Explicit Cancellation | 不 | Canceled |

只有 `ModelRequest.Idempotent` 才允许多次 HTTP Attempt。Client 从 Encoded Content 与
Sequence 生成 Request-scoped Idempotency Key；它可帮助 Provider，但不能使任意 Remote
Behavior Transactional。

## Controls

- Concurrency Semaphore 限制 In-flight Request；
- Local Rate Limiter 控制 Burst；
- Context Deadline 覆盖 Wait、Header、Streaming；
- Retry Policy 只处理选定 Transport/Status Failure；
- Retry-After 与 Cancellation 影响 Wait；
- Body/SSE Limit 限制 Remote Input。

## 代码地图

| 关注点 | 源码 |
| --- | --- |
| HTTP Lifecycle | `httpclient/client.go` |
| SSE | `provider/sse.go` |
| Fault Fixture | `provider/fault_injection_test.go` |
| Engine Retry | `agent/engine/engine.go` |
| Public Problem | `runtime/protocol/problem.go` |

## Retry Timing 与 Health

`Retry-After` 可以是 Seconds 或 HTTP Date。Delay 被 Cap/Jitter；Context Cancellation
可中断 Server-directed/Local Backoff。Rate-limit Header 以 Structured Metadata 保留，
Host 无需解析 Prose 就能解释等待。

HTTP Client 记录 Active Request、Consecutive Failure、Last Sanitized Error、Health Time。
Health 是 Operator/Route Policy 的 Observation，不授权静默切换到不同 Capability、
Endpoint 或 Credential 的 Route。

Idle Timeout 在 Stream 返回后约束 Blocked `Recv`；Timeout 时先 Cancel Request，再
Close Parser，避免 Decoder State Race。

## 设计取舍与替代方案

Aggressive Retry 提高瞬时成功率，却增加 Latency、Cost 和 Duplicate Risk；完全不 Retry
又会放大 Pre-effect Transient Failure。CodeHelper 只重试 Bounded、Classified、
Pre-meaningful Failure。

## 失败模式与安全边界

- Header 前 Deadline 会取消 Request。
- Non-retryable 4xx 立即返回。
- 429/部分 5xx 按 Bounded Policy 处理。
- Malformed/Oversized SSE Decode 失败。
- Cancellation 中断 Backoff 与 Rate Wait。
- Redirect/Debug 不得不安全转发 Credential。
- Partial Stream Failure 对当前 Sample 为 Terminal。

## 测试与验证

```bash
go test ./internal/adapter/provider/httpclient
go test ./internal/adapter/provider -run TestFaultInjectionSSEDisconnect
go test ./internal/runtime/agent/engine -run TestEngineRetriesOnlyBeforeMeaningfulStreamData
```

## 动手实验

阅读 HTTP Client 中 429、Retry Delay、Timeout-before-header、Partial Stream Test，
为每个用例记录 Phase、Retryable、Reason 和 Observable Problem。

## 复习问题

1. 什么条件使 Provider Failure 可以安全 Retry？
2. Cancellation 为什么必须打断 Rate/Backoff Wait？
3. Raw Response Body 为什么不能直接成为 Public Error？
4. Idempotency Key 为什么不使所有 Provider Request 可安全 Retry？
5. Health Observation 为什么不能静默选择不同 Route？

## 延伸阅读

- [Credential Lifecycle](./05-credential-lifecycle.md)

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `model-provider-failures` |
| 状态 | `draft` |
| 最后验证 | 尚未验证 |
