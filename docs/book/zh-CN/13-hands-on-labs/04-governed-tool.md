---
id: lab-governed-tool
title: 实现通过 Guard 的 Tool
audience:
  - contributor
  - agent
prerequisites:
  - extension-tool
code_paths:
  - internal/adapter/tool
  - internal/adapter/tool/guard
test_paths:
  - internal/adapter/tool/tool_test.go
  - internal/adapter/tool/guard/guard_test.go
source_of_truth:
  - internal/adapter/tool/tool.go
  - internal/adapter/tool/guard/guard.go
status: draft
last_verified: null
---

# 实现通过 Guard 的 Tool

简体中文 | [English](../../en/13-hands-on-labs/04-governed-tool.md)

## 目标与前置条件

实现 Read-only Fixture Tool，证明不能绕过 Catalog Binding、Schema、Policy、Resource Claim。

## 步骤

1. 定义只含一个 Bounded Path 的 Strict Descriptor。
2. 执行前将路径解析为 Read Resource。
3. 返回 Bounded Content 和 Structured Metadata。
4. 在 Test Registry 注册并绑定 Catalog Snapshot。
5. 通过 `Guard.ExecuteBound` 调用。
6. 测试 Valid Read、Extra Field、Traversal、Stale Binding、Unknown Tool。

```bash
go test ./internal/adapter/tool ./internal/adapter/tool/guard
```

## Adversarial Matrix

给 Fixture Executor 增加 Atomic Call Counter：

| Condition | Expected | Calls |
| --- | --- | --- |
| Valid Bound Read | Bounded Result/Observed Resource | 1 |
| Extra Field | Schema Error | 0 |
| Traversal/Symlink Escape | Resource/Sandbox Deny | 0 |
| Stale/Revoke Binding | Catalog Error | 0 |
| Policy Deny | Denial Receipt | 0 |
| Canceled Claim Wait | Cancel + Release Claim | 0 |

Snapshot 后 Replace Executor；Old Bound Call 必须失败，不能用 Stale Authority 执行
Replacement。

## Evidence

检查 Descriptor Revision、Normalized Argument、Resolved Resource、Policy、Sandbox、
Claim Release、Bounded Model Content、Structured Metadata。Executor Prose 不是 Filesystem
Access Evidence。

## 预期结果

只有有效 Bound Call 到达 Executor；其他情况均在 Side Effect 前失败并产生 Stable Error。

## 失败诊断

Invalid Input 触发 Executor 表示 Guard Ordering 破坏；Claim 缺 Path 表示 Resource
Resolution 不完整。

## 清理

实验 Tool 应删除；生产保留必须包含 Focused Test/Wire Registration。

## 复习问题

1. Call Counter 如何证明 Guard Ordering？
2. 为什么绑定 Catalog Revision？
3. 哪些 Result 属于 Runtime Evidence？

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `lab-governed-tool` |
| 状态 | `verified` |
