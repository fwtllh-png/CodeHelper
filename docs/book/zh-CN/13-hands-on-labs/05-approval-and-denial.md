---
id: lab-approval-denial
title: 构造 Approval 与 Denial
audience:
  - learner
  - contributor
prerequisites:
  - tool-guard-pipeline
  - security-approval-sandbox
code_paths:
  - internal/security/policy
  - internal/runtime/app
test_paths:
  - internal/security/policy/policy_test.go
  - internal/runtime/app/runtime_test.go
source_of_truth:
  - internal/security/policy/policy.go
  - internal/runtime/protocol/message.go
status: draft
last_verified: null
---

# 构造 Approval 与 Denial

简体中文 | [English](../../en/13-hands-on-labs/05-approval-and-denial.md)

## 目标与前置条件

观察 Allow、Ask、Deny、Stale Decision 与 Post-approval Revalidation，不执行真实写入。

## 步骤

1. 为 Synthetic Write Resource 构造 Invocation。
2. 在 Allow/Ask/Deny Policy 下 Evaluate。
3. Ask 时捕获 Approval Identity，经 Runtime Operation 决策。
4. 决策前改变 Edit-plan/Resource Identity。
5. 验证 Stale/Drifted Decision 不能授权。

```bash
go test ./internal/security/policy/...
go test ./internal/runtime/app -run 'Test.*Approval'
```

## Decision Matrix

| Scenario | Expected Authority |
| --- | --- |
| Allow | Exact Evaluated Invocation |
| Deny | None/No Executor |
| Approve Once | One Matching Call Before Expiry |
| Approve Session | Matching Bounded Scope |
| Wrong Request/Call/Plan | Reject Stale Decision |
| Changed Argument/Resource | Fresh Evaluation |
| Expired/Canceled | None |
| Sandbox Escalation | Separate Approval |

记录 Approval ID、Argument Digest、Canonical Resource、Scope、Expiry、Decision Source、
Event/Receipt。增加 Executor Counter，证明 Pause/Deny 无 Side Effect。

## Negative Control

Approve Original Request 后修改一个 Resource，再提交旧 Decision。Runtime 必须拒绝且
Counter 保持零，从而区分 Authority Binding Failure 与 Execution Failure。

## 预期结果

Deny Terminal；Ask 暂停且不执行；匹配 Approval 只恢复一次；Drift 触发重新 Evaluate；
Receipt 保留 Decision Provenance。

## 失败诊断

Decision 前执行属于 Guard Ordering Failure；跨 Identity/Resource 复用属于 Authority
Binding Defect。

## 清理

状态均为 Test-local；删除手工创建的临时 Policy。

## 复习问题

1. Approval 绑定哪些 Identity？
2. Sandbox Escalation 为什么需要新 Decision？
3. Deny 与 Execution Failure 有何区别？

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `lab-approval-denial` |
| 状态 | `verified` |
