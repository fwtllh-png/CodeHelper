---
id: task-lease-retry
title: Turn 生命周期、取消与幂等性
audience:
  - contributor
  - operator
  - agent
prerequisites:
  - task-worker-executor
  - runtime-resume-recovery
code_paths:
  - internal/runtime/app
  - internal/runtime/agent/turnkernel
  - internal/persist/state/turnstate
test_paths:
  - internal/runtime/app/runtime_test.go
  - internal/runtime/agent/turnkernel/reducer_test.go
  - internal/persist/state/turnstate/store_test.go
source_of_truth:
  - internal/runtime/app/service_facade.go
  - internal/runtime/app/operation_dispatch.go
  - internal/runtime/agent/turnkernel/reducer.go
status: verified
last_verified: 2026-08-28
---

# Turn 生命周期、取消与幂等性

## 学习目标

理解前台 Turn 如何在没有后台 Worker Lease 的情况下维持唯一执行、取消、恢复和幂等。

## Operation 是入口

Host 提交带唯一 ID 的 Operation。Runtime 负责：

- 校验 Session、Thread 和 Turn 归属；
- 持久化接受事实；
- 拒绝冲突或重复请求；
- 只把已接受的请求交给 Engine；
- 通过稳定终态关闭生命周期。

幂等性发生在 Operation 边界，而不是依赖模型输出文本。相同 Idempotency Key 与相同
请求可返回既有结果；相同 Key 对应不同请求必须拒绝。

## Active Turn Fence

`ActiveTurnRegistry` 确保同一 Thread 同时只有一个活动 Turn。内存 Token 防止旧 goroutine
释放新 Turn，持久化 Operation ID 则支持进程重启后的恢复判断。

```text
reserve(thread, turn)
    -> execute
    -> terminal commit
    -> release(exact token)
```

取消通过 Runtime ControlPort 进入当前 Turn。它不会绕过 Reducer，也不会直接把数据库
状态改成 canceled。最终状态必须由同一 Turn 生命周期提交。

## 重试边界

CodeHelper 不提供后台 Worker Retry。需要重试时：

- Provider 瞬时错误由 Provider 策略处理；
- Tool 重试必须符合 Tool 的重复执行契约；
- Turn 恢复使用持久化 Domain Fact；
- Subagent 后续工作通过 `followup_task` 创建新 Turn；
- 用户可显式重新提交失败操作。

每种重试都必须有自己的幂等依据，不能用一个通用重试循环覆盖不同副作用。

## 验证

```bash
go test ./internal/runtime/app
go test ./internal/runtime/agent/turnkernel
go test ./internal/persist/state/turnstate
```

## 复习问题

1. 为什么 Active Turn Token 和 Operation ID 解决不同问题？
2. 为什么取消必须经过 Reducer 和终态提交？
3. Provider、Tool、Turn 与 Subagent 重试为何不能共享一个通用策略？

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `task-lease-retry` |
| 状态 | `verified` |
| 最后验证 | 2026-08-28 |
