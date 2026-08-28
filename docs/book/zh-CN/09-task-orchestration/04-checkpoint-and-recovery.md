---
id: task-checkpoint-recovery
title: Checkpoint 与恢复
audience:
  - contributor
  - operator
  - agent
prerequisites:
  - task-automation-workflow
  - state-session-snapshot-journal
code_paths:
  - internal/runtime/app
  - internal/runtime/agent/turnkernel
  - internal/persist/snapshot
  - internal/persist/state/turnstate
test_paths:
  - internal/runtime/app/session_artifacts_test.go
  - internal/runtime/app/runtime_test.go
  - internal/persist/snapshot/repository_test.go
  - internal/persist/state/turnstate/store_test.go
source_of_truth:
  - internal/runtime/app/artifact_runtime.go
  - internal/runtime/app/runtime.go
  - internal/persist/snapshot/repository.go
status: verified
last_verified: 2026-08-28
---

# Checkpoint 与恢复

## 学习目标

理解 Session Checkpoint、Turn Domain Fact、Terminal Outbox 与 Workspace Journal
分别恢复什么，以及为什么它们不需要后台 Workflow 状态机。

## 四类恢复事实

| 事实 | 用途 |
| --- | --- |
| Session History | 重建对话与模型上下文 |
| Checkpoint | 恢复或 Fork 某个可见会话状态 |
| Turn Domain Fact | 恢复未完成 Turn 的 Reducer 状态 |
| Workspace Journal | 回滚中断写入并检查冲突 |

这些事实按职责分离。Checkpoint 不是执行队列，Journal 也不是对话历史。

## 启动恢复顺序

```text
open durable stores
    -> restore static session state
    -> publish pending terminal outbox
    -> recover pending turns
    -> accept new operations
```

Runtime 在恢复完成前不接受新 Operation，防止新工作与旧终态竞争。终态发布使用稳定
身份关闭“已提交但尚未广播”的崩溃窗口。

## Checkpoint

Checkpoint 保存可恢复或可 Fork 的 Session Artifact。恢复必须校验：

- Checkpoint 属于当前 Session；
- 引用的 Content Object 存在且摘要一致；
- Profile 与历史版本可解释；
- 恢复不会覆盖另一个活动 Turn。

恢复产生新的可审计操作，不静默改写历史。

## 不再恢复后台任务

当前 Runtime 不维护 Task、Workflow 或 Worker Lease，因此启动流程不会扫描后台队列、
重领节点或重放定时任务。长任务的恢复边界是 Runtime Turn 和 Subagent Agent Graph。

## 验证

```bash
go test ./internal/runtime/app
go test ./internal/runtime/agent/turnkernel
go test ./internal/persist/snapshot ./internal/persist/state/turnstate
```

## 复习问题

1. Checkpoint 与 Turn Domain Fact 分别解决什么问题？
2. 为什么 Runtime 必须先完成终态恢复再接受新操作？
3. 删除后台任务恢复后，哪些持久事实仍然属于 Coding 主线？

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `task-checkpoint-recovery` |
| 状态 | `verified` |
| 最后验证 | 2026-08-28 |
