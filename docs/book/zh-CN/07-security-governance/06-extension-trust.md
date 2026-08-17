---
id: security-extension-trust
title: MCP、Skill、Plugin 与 Hook Trust
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
  - internal/adapter/plugin
  - internal/adapter/hooks
  - internal/runtime/extension
  - internal/runtime/app/extension
test_paths:
  - internal/adapter/plugin/trust_test.go
  - internal/adapter/plugin/distribution_test.go
  - internal/adapter/hooks/hooks_test.go
  - internal/runtime/extension/plan_test.go
  - internal/runtime/app/extension/lifecycle_test.go
source_of_truth:
  - internal/adapter/plugin/trust.go
  - internal/adapter/plugin/distribution.go
  - internal/runtime/extension/plan.go
  - internal/runtime/extension/lifecycle.go
status: draft
last_verified: null
---

# MCP、Skill、Plugin 与 Hook Trust

简体中文 | [English](../../en/07-security-governance/06-extension-trust.md)

## 学习目标

区分 Extension Content、Code、Transport、Lifecycle Trust，理解 Capability Receipt、
Signed Distribution、Revocation 与 Hook Failure Policy。

## Extension Risk

| Mechanism | Main Risk | Required Control |
| --- | --- | --- |
| MCP | Server Tool/Response | Endpoint/Process Review、OAuth/Env Allowlist、Guard、Health |
| Skill | Model 解释 Instruction/Resource | Source/Version Lock、Bounded Load、Injection Review |
| Plugin | Executable/Capability | Publisher/Signature/Digest、Receipt、Sandbox、Revoke |
| Hook | 可 Deny/Update/Env 的 Process | Strict Config、Timeout、Sanitized Env、Audit |

Extension Tool 仍进入 Registry/Catalog 并通过 Guard；“Trusted Extension”不等于 Direct
Execution Authority。

Trust Admission 与 Lifecycle Ownership 相互独立。Runtime 将 Trusted Source 解析为
绑定 Permission Digest 的 Digested Plan。每个 Process、Connection、Hook、
Subscription、Timer、Lease 与 Tool Registration 都归因到 Extension Source、Plan
Revision、Generation、Capability 与 Effect Kind。Disable Drain 所属 Effect；
Revoke/Quarantine Fence Generation。

## Plugin Trust Chain

```text
publisher allowlist
 -> signed registry release + monotonic generation
 -> artifact/manifest digest
 -> safe bounded archive extraction
 -> immutable content-addressed staging
 -> content + capability + generation receipt
 -> sandboxed load
 -> catalog authority
```

Capability Inventory 声明 Tool、Filesystem Root、Network Host、Process Permission，拒绝
Wildcard。Content/Capability/Generation Drift 都使旧 Receipt 失效。Rollback 选择 Previously
Verified Artifact；Security Revoke 会 Cancel In-flight Authority，而非只隐藏 UI。

## Hook/Permission Hook

Observer Hook 仅在配置允许时 Fail Open，并必须 Audit。Message/Tool/Permission Gate Hook
按声明 Policy 失败。Deny Wins；Argument Update 返回 Guard 全量 Revalidate；Allow 不能
绕过 Hard Policy Deny。

Hook Output Bounded 且不整体进入 Audit。Environment Sanitized；Timeout/Cancel Kill
Process Tree；Shell Env Audit 只记录 Name，不记录 Value。

## MCP/Skill Boundary

MCP Schema/Capability Change 触发 Catalog Reconcile，Stale Binding 被 Fence；Circuit/
Health 隔离失败 Server。Skill Lock 保证 Resolution Reproducible，但 Skill Text 仍是
Untrusted Model Context，不能授予 Tool Authority。

Plugin/Skill CLI、ACP 与 VS Code 向同一 Runtime Owner 提交幂等 Control Operation。
Durable Prepare/Commit Receipt 使 Restart/Retry 可审计。Host 不能通过编辑 Local State
或报告 Extension Healthy 建立 Trust。

## 验证

```bash
go test ./internal/adapter/plugin ./internal/adapter/hooks
go test ./internal/adapter/mcp ./internal/adapter/skill
go test ./internal/runtime/extension ./internal/runtime/app/extension
```

## 复习问题

1. Capability Drift 为什么使 Plugin Receipt 失效？
2. Hook Allow 为什么不能覆盖 Policy Deny？
3. Locked Skill 为什么仍不可信？

## 延伸阅读

- [Skill、Plugin 与 Hook](../11-extension-ecosystem/04-skill-plugin-hook.md)

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `security-extension-trust` |
| 状态 | `draft` |
| 最后验证 | 尚未验证 |
