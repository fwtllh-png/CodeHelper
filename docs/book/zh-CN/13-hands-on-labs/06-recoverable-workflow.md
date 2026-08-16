---
id: lab-recoverable-workflow
title: 构建可恢复 Workflow
audience:
  - contributor
prerequisites:
  - task-automation-workflow
  - task-checkpoint-recovery
code_paths:
  - internal/orchestration/workflow
  - internal/orchestration/kernel
  - internal/orchestration/store
test_paths:
  - internal/orchestration/workflow/workgraph_test.go
  - internal/orchestration/store/store_test.go
source_of_truth:
  - internal/orchestration/workflow/runtime.go
  - internal/orchestration/store/store.go
status: verified
last_verified: 2026-08-16
---

# 构建可恢复 Workflow

简体中文 | [English](../../en/13-hands-on-labs/06-recoverable-workflow.md)

## 目标与前置条件

运行小型 DAG，在 Durable WorkGraph Settlement 后失败并恢复，避免重复已完成工作。

## 步骤

1. 定义 Prepare、Execute、Verify 三个 Node。
2. 每个成功 Node 通过 WorkGraph Settlement Command 持久化。
3. 在 Verify 完成前注入确定性失败。
4. 从 Ordered WorkGraph Facts 重建。
5. Resume 并断言 Prepare/Execute 不重复。

```bash
go test ./internal/orchestration/workflow ./internal/orchestration/store
```

## Crash-window Matrix

为每个 Node 增加 Durable Invocation Counter：

| Stop Point | Resume |
| --- | --- |
| Claim Commit 前 | Node 保持 Eligible |
| Claim 后/Effect Bind 前 | 按 Lease/Epoch Policy 恢复 |
| After Effect/Before Settle | 需 Idempotency/Journal，否则 Indeterminate |
| Terminal Transaction 中 | Settlement 与 Outbox 同时 Commit 或 Rollback |
| After Settlement | Reuse/Never Rerun |

```bash
go test ./internal/orchestration/workflow -run 'Test(DurableWorkGraph|NodeTimeout|SpecDrift)'
go test ./internal/orchestration/store -run 'Test(StoreTerminalCommit|AuditDetects)'
```

## Graph-drift Control

首个 Settlement 后修改 Dependency/Prompt，并用相同 Run ID Resume。Fingerprint Mismatch
必须在 Node 执行前停止。记录前后的 Run/Node Status、Attempt、Output Handle、Counter。

## 预期结果

DAG 只推进 Ready Node；Failed Node 可诊断；Resume 使用同一个 Fact-replayed WorkGraph；
Terminal State 唯一。

## 失败诊断

Execute 重复表示 Idempotency/Settlement 缺失；Graph 不兼容必须显式失败。

## 清理

删除临时 State Directory，不复用 Run ID。

## 复习问题

1. 哪个 Crash Window 需要 Effect-specific Recovery？
2. Graph Drift 如何被检测？
3. Missing Output 为什么不等于 Node 未执行？

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `lab-recoverable-workflow` |
| 状态 | `verified` |
| 最后验证 | 2026-08-16 |
