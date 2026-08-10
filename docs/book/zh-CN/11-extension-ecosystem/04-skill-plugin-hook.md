---
id: extension-skill-plugin-hook
title: 编写 Skill、Plugin 与 Hook
audience:
  - contributor
  - operator
  - agent
prerequisites:
  - extension-tool
code_paths:
  - internal/adapter/skill
  - internal/adapter/plugin
  - internal/adapter/hooks
test_paths:
  - internal/adapter/skill/resolver_test.go
  - internal/adapter/plugin/lifecycle_test.go
  - internal/adapter/hooks/hooks_test.go
source_of_truth:
  - internal/adapter/skill/catalog.go
  - internal/adapter/plugin/registry.go
  - internal/adapter/hooks/manager.go
status: draft
last_verified: null
---

# 编写 Skill、Plugin 与 Hook

简体中文 | [English](../../en/11-extension-ecosystem/04-skill-plugin-hook.md)

## 学习目标

选择正确扩展机制，理解其 Authority、Lifecycle、Integrity 与 Execution Limit。

| Mechanism | Purpose | Execution |
| --- | --- | --- |
| Skill | Versioned Instruction/Dependency Plan | Bounded Context |
| Plugin | Signed Executable Capability Bundle | Verified Sandboxed Process |
| Hook | Lifecycle/Permission Callback | Bounded Configured Process |

Skill 使用 Strict Manifest、Deterministic Root Precedence、Compatibility/Dependency
Resolution、Lockfile Digest、Enable State、Cycle/Conflict 与 Authority Verification。
加载 Skill 不授予 Tool Capability。

Plugin Trust Receipt 绑定 Bundle Hash、Capability Inventory、Publisher、Version 与
Generation。Distribution 校验 Signature、Replay/Downgrade、Safe Extraction、Atomic
Activation、Rollback、Revoke 与 In-flight Cancellation。Loader Snapshot Executable
并在 Sandbox 运行。

Hook 在定义的 Lifecycle Point 经 Manager/Adapter 运行。Permission Hook 可 Allow/Deny/
Ask；Guard 对 Updated Input 重新验证，Hook Error/Unresolved Ask Fail Closed。

## Authority 对比

| Mechanism | Authority Source | Revocation Effect |
| --- | --- | --- |
| Skill | Manifest/Lock/Enable/Optional Plugin Authority | 阻止 Future Load |
| Plugin | Signed Release/Trust Receipt/Generation | Cancel In-flight/Fence Handle |
| Hook | Strict Config/Lifecycle/Failure Policy | 移除 Callback/Kill Process Tree |

Skill 影响 Model Instruction，不授予 Execution Authority。Plugin 只能经 Registry/Guard
贡献 Executable Tool。Hook 在 Lifecycle Point 建议或过滤，不能自行 Commit Tool Effect。

## Installation/Activation

Plugin Distribution 分离 Verification/Activation：

```text
fetch bounded signed index/artifact
 -> verify publisher/version/digest
 -> safely extract and stage content-addressed bundle
 -> review capability inventory
 -> atomically activate generation
 -> drain old generation
```

Rollback 激活 Previously Verified Record，但不跳过当前 Revocation/Authority。Concurrent
Update 收敛到一个 Generation。

Skill 同样分离 Discovery、Dependency Resolution、Lock Verify、Bounded Load。Deterministic
Root Precedence 防止 Lower Candidate Shadow Governed Skill。

Hook Failure Policy 按 Event 区分：Observer 可 Audit/Fail Open；Message/Tool/Permission
Gate Fail Closed。两者 Output/Environment 都有界并 Sanitized。

## 失败与安全边界

- Symlink Traversal、Malformed State/Lock、Digest Drift 失败。
- Skill Dependency Cycle/Conflict 显式。
- Plugin Wildcard/Tamper/Authority Drift/Unknown Publisher 失败。
- Security Revoke Cancel In-flight Plugin。
- Hook Timeout/Failure 不静默 Allow。
- Extension 默认不获得 Raw Secret。

## 测试与验证

```bash
go test ./internal/adapter/skill
go test ./internal/adapter/plugin
go test ./internal/adapter/hooks
```

## 动手实验

创建含一个 Dependency 的 Local Skill、No-op Hook 和 Fixture Plugin Manifest，比较三者
可影响的 Surface。

## 复习问题

1. 何时应使用 Skill 而非 Plugin？
2. Plugin Trust Receipt 绑定什么？
3. Guard 为什么重验 Hook-updated Input？
4. Skill Authority 与 Plugin Authority 有何区别？
5. Plugin Verification/Activation 为什么分离？

## 延伸阅读

- [Extension Failure 与隔离策略](./06-failure-isolation.md)

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `extension-skill-plugin-hook` |
| 状态 | `verified` |
| 最后验证 | 2026-08-06 |
