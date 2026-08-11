---
id: extension-mcp
title: 接入 MCP Server
audience:
  - contributor
  - operator
prerequisites:
  - extension-tool
  - model-credential-lifecycle
code_paths:
  - internal/adapter/mcp
  - internal/adapter/tool/mcp
  - internal/runtime/app/wire
test_paths:
  - internal/adapter/mcp/contract/fixture_test.go
  - internal/adapter/mcp/stdio_integration_test.go
  - internal/adapter/mcp/http_integration_test.go
source_of_truth:
  - internal/adapter/mcp/config.go
  - internal/adapter/mcp/pool.go
status: draft
last_verified: null
---

# 接入 MCP Server

简体中文 | [English](../../en/11-extension-ecosystem/03-integrating-mcp.md)

## 学习目标

配置 Stdio/HTTP MCP、Discovery、Catalog Reconcile、Auth、Permission、Health Isolation
与 Shutdown。

## Integration Flow

```mermaid
flowchart LR
    C[Strict MCP Config] --> P[Pool]
    P --> T[Stdio / HTTP Transport]
    T --> D[Initialize + Discovery]
    D --> R[Catalog Reconcile]
    R --> G[Normal Tool Guard]
```

Config Versioned/Strict，Server Name 生成 Canonical Model Tool Name。Stdio 控制 Process
Environment/Group Cleanup；HTTP 支持 Streamable HTTP、Legacy SSE、Bounded Timeout、
Session Reconnect、Auth Reference 与 OAuth PKCE。

Connection Initialize Capability，分页发现 Tool/Resource/Prompt 并 Normalize Call。Pool
只 Reload Changed Server，隔离 Health/Circuit，发布 Catalog Notification，并关闭全部
Transport。Discovered Tool 进入普通 Registry/Guard，MCP 不是 Policy Bypass。

## Source-scoped Reconciliation

每个 MCP Server 是独立 Catalog Source。Discovery 生成包含 Normalized Descriptor/
Connection Identity 的 Server Generation。Reconcile 只替换该 Source，检测 Name
Collision，并 Fence Connection/Revision 已变化的 Executor。

```text
notification/config change
 -> discover candidate generation
 -> validate complete source catalog
 -> atomically reconcile source
 -> publish catalog change
```

Incomplete/Stale Refresh Quarantine 单个 Source，不混合 Old/New Descriptor。Large Catalog
保持 Deferred，直到 Tool Search Materialize 选中项。

## Connection/Call Lifecycle

Initialize/Discovery/Probe 可 Replay；Business Tool Call 不因 Stale HTTP Session 自动
Replay。Transport 先重建 Generation，再向 Caller 返回 Explicit Outcome。

Circuit 按 Server 隔离。Tool `isError` 是 Business Result，不自动破坏 Transport Health。
Timeout、Protocol Loss、Process Death 更新 Health，必要时 Revoke Visibility，并触发
Bounded Probe。

Shutdown 移除 Observer、Cancel Request、关闭 HTTP Stream 或整个 Stdio Process Group，
并在 Deadline 内等待。

## 失败与安全边界

- Secret Environment/Dynamic Resource Escalation 被拒绝。
- Unsupported Optional Capability 不破坏已支持 Discovery。
- Circuit Probe 不 Replay Business Call。
- 单 Server Failure 不隐藏 Healthy Server。
- Stale Session Reconnect 不 Duplicate Request。
- Catalog Refresh 绑定 Generation/Revision。

## 测试与验证

```bash
go test ./internal/adapter/mcp/...
go test ./internal/adapter/tool/mcp
go test ./internal/runtime/app/wire -run TestMCP
```

## 动手实验

连接 Hermetic Stdio Fixture，发现并经 Guard 执行 Tool，再触发 Catalog Notification，
验证 Revision-safe Refresh。

## 复习问题

1. MCP Tool 为什么仍经过 Guard？
2. Pool 隔离什么？
3. Reconnect 为什么不能 Replay Business Call？
4. Catalog Reconcile 为什么按 Server Source？
5. 哪些 Error 应影响 Circuit Breaker？

## 延伸阅读

- [Extension Failure 与隔离策略](./06-failure-isolation.md)

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `extension-mcp` |
| 状态 | `draft` |
| 最后验证 | 尚未验证 |
