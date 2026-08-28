---
id: lab-recoverable-workflow
title: 验证可恢复 Turn
audience:
  - contributor
prerequisites:
  - task-automation-workflow
  - task-checkpoint-recovery
code_paths:
  - internal/runtime/app
  - internal/runtime/agent/turnkernel
  - internal/persist/state/turnstate
test_paths:
  - internal/runtime/app/runtime_test.go
  - internal/runtime/app/wire/persistent_test.go
  - internal/persist/state/turnstate/store_test.go
source_of_truth:
  - internal/runtime/app/runtime.go
  - internal/runtime/app/runtime_start.go
  - internal/runtime/agent/turnkernel/reducer.go
status: verified
last_verified: 2026-08-28
---

# 验证可恢复 Turn

## 目标与前置条件

验证 Runtime 在接受 Turn 后崩溃时，可以从持久化 Operation、Domain Fact 和 Terminal
Outbox 恢复，而不会重复已经确认的副作用。

## 步骤

1. 使用持久化 State Store 构造 Runtime。
2. 提交一个会进入 Tool 或交互等待状态的 Turn。
3. 在终态提交前关闭 Runtime。
4. 使用相同 State Store 重建 Runtime。
5. 检查恢复后的 Turn 状态与事件序列。
6. 再次提交同一 Idempotency Key，确认不会重复执行。

## 运行

```bash
go test ./internal/runtime/app -run 'Test.*Recover|Test.*Pending'
go test ./internal/runtime/app/wire -run 'TestPersistent'
go test ./internal/persist/state/turnstate
```

## 必须检查的证据

- Operation 只有一个接受记录；
- Domain Fact Sequence 连续；
- Terminal Event 至多发布一次；
- 重启后不会重复调用 Provider 或 Tool；
- Workspace Journal 不留下未解释的写入；
- Session 与 Thread Projection 可从持久事实重建。

## 故障解释

如果事件重复，先检查 Terminal Outbox 的稳定 ID；如果 Turn 被重新执行，检查 Pending
Operation 与 Domain Fact 的恢复顺序；如果工作区不一致，检查 Journal Recovery，而
不是增加后台重试循环。

## 清理

测试使用临时目录，不应修改真实工作区。完成后运行：

```bash
git diff --check
go test ./internal/runtime/app/... ./internal/persist/state/turnstate
```

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `lab-recoverable-workflow` |
| 状态 | `verified` |
| 最后验证 | 2026-08-28 |
