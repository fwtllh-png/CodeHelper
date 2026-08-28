---
id: lab-worker-retry
title: 调试 Subagent 超时与恢复
audience:
  - contributor
  - operator
prerequisites:
  - task-worker-executor
  - task-lease-retry
code_paths:
  - internal/orchestration/admission
  - internal/orchestration/subagent
  - internal/runtime/app/wire
test_paths:
  - internal/orchestration/admission/governor_test.go
  - internal/orchestration/subagent/control_test.go
  - internal/runtime/app/wire/childruntime_test.go
source_of_truth:
  - internal/orchestration/admission/governor.go
  - internal/orchestration/subagent/lifecycle.go
  - internal/runtime/app/wire/childruntime.go
status: verified
last_verified: 2026-08-28
---

# 调试 Subagent 超时与恢复

## 目标与前置条件

验证子 Agent 在超时、取消、进程重启和 Follow-up 情况下仍保持预算、Thread 与结果
归属一致。

## 场景一：超时

1. 配置较短但显式的 Subagent Wall Time。
2. 启动一个不会自行完成的测试 Provider Turn。
3. 等待 Runtime 触发取消。
4. 断言 Agent 进入终态，Admission Lease 被释放。

超时必须来自公开配置，测试不得把生产行为绑定到隐藏常量。

## 场景二：重启恢复

1. 持久化一个处于 running 或 waiting 的 Agent Graph 节点；
2. 关闭并重建 Runtime；
3. 确认 Child Thread 重新注册；
4. 确认已完成结果不会重新执行；
5. 确认未完成 Turn 按 Runtime Recovery 规则处理。

## 场景三：Follow-up

对已完成 Agent 提交 Follow-up，确认：

- 复用同一 Agent 身份与 Thread；
- 创建新的 Turn；
- 预算只预留剩余容量；
- 新结果覆盖当前可见结果，但历史事件仍可回放。

## 运行

```bash
go test ./internal/orchestration/admission
go test ./internal/orchestration/subagent
go test ./internal/runtime/app/wire -run 'TestChild|TestPersistent'
```

## 诊断顺序

先检查 Agent Graph Revision，再检查 Runtime Turn Event，最后检查预算与 Worktree
清理。不要寻找 Worker Lease 或后台任务队列；这些组件不属于当前 Runtime。

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `lab-worker-retry` |
| 状态 | `verified` |
| 最后验证 | 2026-08-28 |
