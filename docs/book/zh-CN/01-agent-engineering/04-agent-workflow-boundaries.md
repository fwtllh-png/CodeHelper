---
id: agent-workflow-boundaries
title: Agent、Plan 与外部自动化的边界
audience:
  - learner
  - contributor
  - agent
prerequisites:
  - agent-react-planning-tools
code_paths:
  - internal/runtime/agent
  - internal/runtime/app
  - internal/orchestration/subagent
test_paths:
  - internal/runtime/app/application_e2e_test.go
  - internal/runtime/app/session_artifacts_test.go
  - internal/orchestration/subagent/control_plane_test.go
source_of_truth:
  - internal/runtime/agent/engine/engine.go
  - internal/runtime/app/runtime.go
  - internal/orchestration/subagent/control_plane.go
status: draft
last_verified: null
---

# Agent、Plan 与外部自动化的边界

## 学习目标

能够在单个 Agent Turn、可见 Plan、Subagent 与外部自动化之间选择，并避免在 Runtime
中复制执行控制面。

## 四种机制

| 机制 | 适合场景 | 状态所有者 |
| --- | --- | --- |
| 单个 Turn | 连续推理、局部修改、即时反馈 | Runtime + Turn Kernel |
| Plan | 展示和约束多步骤目标 | Session Artifact |
| Subagent | 边界清晰的并行调查或实现 | Agent Graph + Child Turn |
| 外部自动化 | 定时或无人值守触发 | 外部 Host |

Plan 不是调度器。它记录目标和进度，但执行仍发生在当前 Turn。Subagent 也不是后台
Worker：父 Agent 显式创建并等待它，用户能在同一会话中观察其状态。

## 选择原则

保持在同一 Turn：

- 步骤之间存在强依赖；
- 工作很短；
- 会修改同一批文件；
- 并行收益低于上下文和协调成本。

使用 Subagent：

- 子问题相互独立；
- 需要独立审查；
- 可以声明清晰的 Owned Paths；
- 有足够 Token 与并发预算。

使用外部自动化：

- 必须按时间或外部事件触发；
- 任务需要跨 Runtime 生命周期排队；
- 调度系统已有自己的身份、重试和审计边界。

外部系统应通过公开 Host Operation 启动普通 Turn，不应把调度状态注入 Runtime 数据库。

## 安全边界

无论入口来自用户还是外部自动化，Provider、Tool、Guard、Journal 与 Sandbox 路径保持
一致。Host 只提交操作，不直接执行工具。

## 验证

```bash
go test ./internal/runtime/app ./internal/runtime/agent/...
go test ./internal/orchestration/subagent
```

## 复习问题

1. Plan 为什么不应拥有执行权威？
2. Subagent 与后台 Worker 的生命周期差异是什么？
3. 外部自动化如何复用 Runtime 而不形成第二套执行路径？

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `agent-workflow-boundaries` |
| 状态 | `draft` |
| 最后验证 | 尚未验证 |
