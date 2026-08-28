---
id: lab-trace-failure
title: 从 Trace 复盘一次失败
audience:
  - learner
  - contributor
  - operator
prerequisites:
  - state-trace-usage-cost
  - state-reconstruct-failure
code_paths:
  - internal/observability/trace
  - internal/observability/usage
  - internal/persist/state/eventlog
test_paths:
  - internal/observability/trace/trace_test.go
  - internal/observability/usage/repository_test.go
  - internal/persist/state/eventlog/log_test.go
source_of_truth:
  - internal/observability/trace/trace.go
  - internal/observability/usage/repository.go
  - internal/persist/state/eventlog/log.go
status: verified
last_verified: 2026-08-17
---

# 从 Trace 复盘一次失败

## 目标与前置条件

用 Runtime Event、Span、Usage、Receipt 与 Durable State 重建 Failed Turn，不依赖截图。

## 步骤

1. 运行 Malformed-tool Fixture 并保留临时 State。
2. 从 Terminal Event 记录 Operation/Thread/Turn/Item ID。
3. 按 Cursor Replay Runtime Event Log。
4. 跟随 Parent/Child Span 和 Provider/Tool Attempt。
5. 关联 Catalog、Policy/Approval、Sandbox、Workspace Journal、Verify Receipt。
6. 比较 Event 与 SQLite Projection。
7. 给出最早有证据的 Fault，排除下游 Symptom。

```bash
go test ./internal/observability/trace ./internal/observability/usage
go test ./internal/persist/state/eventlog
go test ./internal/adapter/provider/openai -run TestChatStreamRejectsMalformedAndAbruptStreams
go test ./internal/adapter/tool/guard -run TestMalformedArgumentsFailBeforePolicy
```

## Investigation Worksheet

| Question | Record |
| --- | --- |
| Accepted？ | Operation ID/Admission Event |
| Canonical Order？ | Cursor/Hash Evidence |
| 哪些 Causal Record 关联？ | Turn/Call/Trace/Span/Parent ID |
| First Failed Phase？ | Provider/Tool/Approval/Verify Span |
| Effect？ | Tool Pair/Journal/Observed Change |
| Authorized？ | Catalog/Policy/Approval |
| Reverted？ | Fingerprint/Recovery Receipt |
| Measured？ | Usage/Cost/Latency Known Flag |
| Unknown？ | Gap/Open Span/Missing Output/Conflict |

先按 Cursor 构造 Timeline，再附 Timestamp。每项结论标记 **Evidence**、**Inference** 或
**Unknown**。

## Corruption Control

运行 Torn-tail/Committed-corruption Event Log Test。Torn Final Write 可修复；Committed
Byte 改变必须 Fail Closed。不要通过重跑 Agent “发现”发生了什么，这会制造新 Effect。

Incident Report 包含 Impact、Earliest Fault、Propagation、Terminal、Recovery、Residual
Uncertainty 与 Regression Test。

## 预期结果

报告区分 Cause、Propagation、Terminal Classification、Recovery Advice，且不含 Secret/
Raw Sensitive Payload。

## 失败诊断

Identity Link 断裂是 Observability Defect；Replay 后 Projection 不同是 Persistence/
Reducer Drift。

## 清理

记录脱敏 Evidence 后删除临时 State。

## 复习问题

1. Cursor 为什么强于 Wall-clock Ordering？
2. 哪些结论必须保持 Unknown？
3. 为什么不能为诊断重跑 Agent？

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `lab-trace-failure` |
| 状态 | `verified` |
| 最后验证 | 2026-08-17 |
