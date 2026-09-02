---
id: task-lane-fleet
title: 前台并发与工作区隔离
audience:
  - contributor
  - operator
prerequisites:
  - task-worker-executor
  - task-automation-workflow
code_paths:
  - internal/orchestration/admission
  - internal/orchestration/subagent
  - internal/runtime/app/wire
test_paths:
  - internal/orchestration/admission/governor_test.go
  - internal/orchestration/subagent/worktree_allocation_test.go
  - internal/runtime/app/wire/childworktree_test.go
source_of_truth:
  - internal/orchestration/admission/governor.go
  - internal/orchestration/subagent/worktree.go
  - internal/runtime/app/wire/childruntime.go
status: verified
last_verified: 2026-08-28
---

# 前台并发与工作区隔离

## 学习目标

理解删除 Lane、Fleet 和后台 Scheduler 后，QCode 如何约束前台并发与写入隔离。

## 并发来源

当前 Runtime 的并发来自两处：

- 单个 Turn 内可并行的只读 Tool Call；
- 父 Agent 显式创建的多个 Subagent。

两者都由当前用户工作触发，不存在独立后台 Worker 抢占队列。

## Admission Governor

Subagent Admission 同时约束：

- 最大递归深度；
- 最大并发数；
- Token 总预算；
- Cost 总预算。

零值不代表隐藏的固定档位。生产限制来自显式配置和模型能力，避免因经验常量导致不同
模型或环境下的错误拒绝。

## 工作区隔离

写入型子 Agent 优先使用独立 Git Worktree。只读子 Agent 可以共享工作区；显式选择
Serialized 模式时，写入型子 Agent必须持有整个 Workspace Turn Gate。

```text
Parent Workspace
    +-> read-only child: shared read
    +-> writing child: isolated worktree
    +-> serialized child: exclusive turn gate
```

写入路径声明和集成候选会进入 Agent Graph。父 Agent 在集成前检查冲突、基线与验证
结果，不能依赖调度顺序假设。

## 为什么没有 Lane 与 Fleet

Lane 原本描述进程放置，Fleet 原本提供后台运行投影。它们要求额外的进程生命周期、
状态同步和恢复协议。对本地 Coding 主线，Subagent Thread、Worktree 和 Runtime
Event 已提供足够的执行与观察边界。

## 验证

```bash
go test ./internal/orchestration/admission
go test ./internal/orchestration/subagent
go test ./internal/runtime/app/wire -run Child
```

## 复习问题

1. Tool 并发与 Subagent 并发的所有者分别是谁？
2. 为什么写入型子 Agent 需要 Worktree 或串行门禁？
3. Admission 的零值为何不能解释成私有固定阈值？

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `task-lane-fleet` |
| 状态 | `verified` |
| 最后验证 | 2026-08-28 |
