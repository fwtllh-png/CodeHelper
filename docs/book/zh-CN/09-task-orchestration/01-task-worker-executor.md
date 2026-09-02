---
id: task-worker-executor
title: 前台任务与执行边界
audience:
  - learner
  - contributor
  - operator
prerequisites:
  - state-why-durable
code_paths:
  - internal/runtime/agent
  - internal/runtime/app
  - internal/orchestration/subagent
test_paths:
  - internal/runtime/app/application_e2e_test.go
  - internal/orchestration/subagent/subagent_test.go
source_of_truth:
  - internal/runtime/app/runtime.go
  - internal/runtime/agent/engine/engine.go
  - internal/orchestration/subagent/subagent.go
status: draft
last_verified: null
---

# 前台任务与执行边界

## 学习目标

理解 QCode 为什么只保留前台 Turn、Plan 和 Subagent，而不在本地 Runtime 中维护
通用后台 Task Queue、Worker 或 Workflow Scheduler。

## 当前边界

QCode 的产品主线是用户发起的 Coding Turn。每次工作都必须能够追溯到 Session、
Thread、Turn 和 Operation，执行结果通过 Runtime Event 与 Receipt 呈现。

```text
User Operation
    -> Runtime Turn
    -> Agent Engine
    -> Guarded Tool
    -> Verification
    -> Terminal Event
```

需要拆分工作时，父 Turn 可以显式创建 Subagent。Subagent 仍是普通 Runtime Turn：

- 使用独立 Thread；
- 继承受约束的上下文与权限；
- 受 Token、Cost、Depth 和 Concurrency 预算约束；
- 写操作进入隔离 Worktree 或串行共享工作区；
- 结果回到父 Agent，再由父 Agent 决定是否集成。

## 为什么不保留后台执行平面

通用 Task、Worker、Automation、Workflow、Lane 和 Fleet 会引入第二套生命周期、重试、
租约、调度与恢复语义。对本地交互式 Coding Agent，这些能力的维护成本高于常见收益，
也会扩大协议、数据库和安全边界。

删除后台平面后：

- Runtime 不接受 Run/Node 控制操作；
- 模型不再获得 Task 或 Automation 工具；
- Web 不展示后台任务队列；
- SQLite 不创建后台任务和自动化表；
- 长任务继续由前台 Turn、进程终态订阅和 Subagent 完成。

## 设计判断

只有满足以下条件时，才应重新引入独立后台系统：

1. 产品明确需要无人值守、跨进程恢复的任务；
2. 普通 Turn 和 Subagent 无法表达所需生命周期；
3. 有清晰的执行权威、取消、幂等、资源预算和可观测性契约；
4. 收益足以覆盖新增控制面的长期维护成本。

否则应优先扩展现有 Turn 或 Subagent，而不是建立平行执行引擎。

## 验证

```bash
go test ./internal/runtime/app ./internal/runtime/agent/...
go test ./internal/orchestration/subagent
```

## 复习问题

1. 为什么 Subagent 属于 Coding 主线，而通用后台 Worker 不属于？
2. 删除第二套生命周期如何降低恢复和安全建模成本？
3. 哪些需求足以证明重新引入后台调度值得？

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `task-worker-executor` |
| 状态 | `draft` |
| 最后验证 | 尚未验证 |
