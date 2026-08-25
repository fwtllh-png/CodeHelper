---
id: lab-new-capability
title: 设计并验证新的 Agent 能力
audience:
  - contributor
  - agent
prerequisites:
  - practice-reading-codebase
  - extension-failure-isolation
code_paths:
  - internal/runtime
  - internal/adapter
  - internal/security
test_paths:
  - internal/host/web/architecture_test.go
  - internal/runtime/app/wire/sandbox_architecture_test.go
source_of_truth:
  - AGENTS.md
  - docs/zh-CN/architecture.md
status: draft
last_verified: null
---

# 设计并验证新的 Agent 能力

## 目标与前置条件

产出 Implementation-ready Capability Design 和最小 Fixture Proof，保持 Runtime Ownership/
Governance。

## 步骤

1. 描述 User Outcome、Non-goal、Measurable Acceptance。
2. 定位 Host Input、Protocol、Engine、Adapter、Durable State、Projection。
3. 枚举 Capability、Resource、Credential、Approval、Sandbox、Egress、Cancellation、
   Retry、Failure Isolation。
4. 定义 Compatibility、Rollout、Observability、Rollback。
5. 实现最小 Vertical Fixture Path。
6. 添加 Unit/Contract/Integration Test 和中文文档。
7. 先 Focused Check，再按 Blast Radius 扩大。

## Design Artifact

| Concern | Required Decision |
| --- | --- |
| Authority | Grant/Narrow/Expire/Revoke |
| Identity | Operation/Turn/Tool/Task/Generation/Idempotency |
| Effect | Resource/External/Partial-effect Boundary |
| Recovery | Replay/Retry/Checkpoint/Rollback/Reconcile |
| Isolation | Process/Network/Sandbox/Budget/Source |
| Compatibility | Additive/Breaking/Generated Artifact |
| Observability | Event/Receipt/Trace/Usage/Error |
| Evidence | Unit/Contract/Integration/Fault/Race/Platform |

## Vertical Proof/Adversarial Control

先用 Fixture 实现一条 Success Path，再证明：

- Malformed/Unknown Input 在 Effect 前失败；
- Policy/Approval/Sandbox 不可 Bypass；
- Cancellation 释放 Owned Resource；
- Stale Generation/Identity 被 Fence；
- Unknown Partial Effect 后拒绝 Retry；
- Restart 只 Reconstruct，不重跑 Completed Work；
- Optional Capability Unavailable 显式 Degrade；
- Log/Event/Fixture 无 Raw Secret。

## Go/No-go Review

Go 需要 Named Owner、Bounded IO、Stable Identity/Error、Deterministic Fixture、
Rollback/Recovery、中文文档和全部 Required Gate。Authority 模糊、只能 Live Test、
Platform Security Unknown、Rollback 依赖删除 User State 时 No-Go。

## 预期结果

每个 Concern 有唯一 Owner；无 Host Business Loop/Guard Bypass；Identity/Error 稳定；
Replay 明确；Verification Evidence 完整。

## 失败诊断

Authority 模糊时停止并重构设计；只有 Live Dependency 时添加 Contract Fixture；
Rollback 不能恢复 Durable Compatibility 时不得 Rollout。

## 清理

删除实验 Registration/Artifact，或作为已 Review 的生产代码保留。

## 验证

```bash
go test ./path/to/changed/package
make docs-check book-check
git diff --check
```

## 复习问题

1. 哪种 Failure 会立即 No-Go？
2. Replay 与 Retry 如何区分？
3. 什么 Evidence 才允许 Rollout？

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `lab-new-capability` |
| 状态 | `draft` |
| 最后验证 | 尚未验证 |
