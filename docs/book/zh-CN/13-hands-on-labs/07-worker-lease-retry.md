---
id: lab-worker-retry
title: 调试 Worker Lease 与 Retry
audience:
  - contributor
  - operator
prerequisites:
  - task-worker-executor
  - task-lease-retry
code_paths:
  - internal/orchestration/task
  - internal/orchestration/worker
test_paths:
  - internal/orchestration/task/execution_test.go
  - internal/orchestration/worker/worker_test.go
source_of_truth:
  - internal/orchestration/task/execution.go
  - internal/orchestration/worker/worker.go
status: verified
last_verified: 2026-08-06
---

# 调试 Worker Lease 与 Retry

简体中文 | [English](../../en/13-hands-on-labs/07-worker-lease-retry.md)

## 目标与前置条件

强制 Lease Expiry 并分类 Retry，同时保证至多一个 Active Claim 和 Idempotent Outcome。

## 步骤

1. 创建确定性 Identity 的 Queued Task。
2. Worker A Claim，Fake Clock 越过 Expiry。
3. Worker B 以新 Generation Takeover。
4. Worker A 提交 Late Heartbeat/Result。
5. 注入 Retryable、Terminal、Canceled Outcome。

```bash
go test ./internal/orchestration/task ./internal/orchestration/worker
```

## Timeline/Assertion

```text
t0 create queued task
t1 A claims -> attempt 1, owner A, expiry E1
t2 clock > E1
t3 B reclaims -> attempt 2, owner B, expiry E2
t4 A heartbeat/settle rejected
t5 B settles exactly one terminal
```

每一步记录 Task Version/State、Attempt、Owner、Expiry、Schedule、Failure、Executor Counter。
Lease 是 Repository Fence，不证明 A 已停止 External Effect。

## Retry Control

- Graceful Drain 返还 Attempt；
- Lease Expiry 消耗 Attempt；
- Retryable Failure 使用 Capped Backoff；
- Attempts Exhausted 转 Terminal；
- Non-idempotent Shell 拒绝 Auto Retry；
- Healthy Foreign Lease 在 Recovery 中保留。

Expiry/Backoff 使用 Fake Time，Takeover 使用 Channel，不以 Sleep 同步。

## 预期结果

拒绝 Stale Worker；Retryable Failure 有界 Backoff；Terminal 不循环；Duplicate Completion
保持 Idempotent。

## 失败诊断

两个 Generation 都被接受表示 Lease Fencing Failure；Terminal 后 Retry 表示 State
Machine Corruption。

## 清理

删除临时 Task DB，停止两个 Worker Loop。

## 复习问题

1. Drain/Expiry 的 Attempt Accounting 有何不同？
2. Lease Fence 为什么不能撤销 External Effect？
3. Completion 如何保持 Idempotent？

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `lab-worker-retry` |
| 状态 | `verified` |
