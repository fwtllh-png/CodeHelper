---
id: security-extension-trust
title: MCP 与 Skill Trust
audience:
  - contributor
  - operator
  - agent
prerequisites:
  - security-threat-model
  - extension-skill-plugin-hook
code_paths:
  - internal/adapter/mcp
  - internal/adapter/skill
  - internal/runtime/app/extension
test_paths:
  - internal/adapter/mcp/config_test.go
  - internal/adapter/skill/catalog_test.go
source_of_truth:
  - internal/adapter/mcp/config.go
  - internal/adapter/skill/catalog.go
status: draft
last_verified: null
---

# MCP 与 Skill Trust

## 学习目标

区分 Extension Content、Transport 与 Execution Trust，理解配置来源、内容锁定与撤销。

## Extension Risk

| Mechanism | Main Risk | Required Control |
| --- | --- | --- |
| MCP | Server Tool/Response | Endpoint/Process Review、OAuth/Env Allowlist、Guard、Health |
| Skill | Model 解释 Instruction/Resource | Source/Version Lock、Bounded Load、Injection Review |

Extension Tool 仍进入 Registry/Catalog 并通过 Guard；“Trusted Extension”不等于 Direct
Execution Authority。

MCP 与 Skill 各自拥有独立的配置、完整性和撤销边界，不能借由“能力已启用”获得
执行权限。

## MCP/Skill Boundary

MCP Schema/Capability Change 触发 Catalog Reconcile，Stale Binding 被 Fence；Circuit/
Health 隔离失败 Server。Skill Lock 保证 Resolution Reproducible，但 Skill Text 仍是
Untrusted Model Context，不能授予 Tool Authority。

Web 向同一 Runtime Owner 提交幂等 Control Operation。
Durable Prepare/Commit Receipt 使 Restart/Retry 可审计。Host 不能通过编辑 Local State
或报告 Extension Healthy 建立 Trust。

## 验证

```bash
go test ./internal/adapter/mcp ./internal/adapter/skill
go test ./internal/runtime/app/extension
```

## 复习问题

1. MCP 配置漂移为什么必须产生新的 Catalog Generation？
2. Locked Skill 为什么仍不可信？

## 延伸阅读

- [编写 Skill](../11-extension-ecosystem/04-skill-plugin-hook.md)

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `security-extension-trust` |
| 状态 | `draft` |
| 最后验证 | 尚未验证 |
