---
id: task-automation-workflow
title: Plan 与 Subagent 协作
audience:
  - learner
  - contributor
  - operator
prerequisites:
  - task-worker-executor
code_paths:
  - internal/runtime/app
  - internal/orchestration/subagent
  - internal/adapter/tool/agent
test_paths:
  - internal/runtime/app/session_artifacts_test.go
  - internal/orchestration/subagent/control_plane_test.go
  - internal/adapter/tool/agent/agent_test.go
source_of_truth:
  - internal/runtime/app/runtime.go
  - internal/orchestration/subagent/control_plane.go
  - internal/adapter/tool/agent/agent.go
status: draft
last_verified: null
---

# Plan 与 Subagent 协作

## 学习目标

理解如何用可见 Plan 和受控 Subagent 完成多步骤 Coding 工作，而不引入通用 Workflow
引擎或定时 Automation。

## Plan 是用户可见状态

Plan 描述当前目标、步骤和状态，但不拥有第二套执行状态机。它的作用是：

- 让用户看见工作分解；
- 约束当前 Turn 的完成条件；
- 为恢复和交接提供稳定摘要；
- 将实现与验证步骤显式区分。

Plan Step 的推进来自 Agent 的明确更新。它不自动调度命令，也不绕过 Tool Guard。

## Subagent 是受控并行单元

独立、可并行且边界清晰的工作可以交给 Subagent。每个子 Agent：

- 获得裁剪后的 Context Capsule；
- 使用独立 Thread 和 Runtime Turn；
- 遵守 Role、Tool Allowlist 与预算；
- 通过 Worktree 隔离写入；
- 将结构化结果交回父 Agent。

父 Agent仍是协调者。它决定何时创建、等待、追问、关闭和集成子 Agent，不能把责任
转交给不可见的后台调度器。

## 顺序与并行

```text
Plan
  -> local step
  -> spawn independent children
  -> wait for terminal results
  -> review and integrate
  -> verify
  -> complete
```

只有互不依赖且写入范围不重叠的工作适合并行。存在数据依赖、同文件写冲突或需要连续
推理时，应在同一 Turn 中顺序执行。

## 为什么没有 Automation

定时任务和无人值守工作不属于当前本地 Coding 主线。删除 Automation 后，Runtime
不再维护 RRULE、后台队列或定时 Tick。外部系统需要自动触发时，应在明确的 Host
边界提交普通 Operation，而不是在 Runtime 内建立第二套控制面。

## 验证

```bash
go test ./internal/runtime/app
go test ./internal/orchestration/subagent
go test ./internal/adapter/tool/agent
```

## 复习问题

1. Plan 与 Workflow 状态机的职责差异是什么？
2. 哪些工作适合交给 Subagent 并行执行？
3. 为什么外部自动化应提交普通 Operation？

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `task-automation-workflow` |
| 状态 | `draft` |
| 最后验证 | 尚未验证 |
