---
id: extension-skill-plugin-hook
title: 编写 Skill
audience:
  - contributor
  - operator
  - agent
prerequisites:
  - extension-tool
code_paths:
  - internal/adapter/skill
  - internal/runtime/app/extension
  - internal/persist/extensioncontrol
test_paths:
  - internal/adapter/skill/resolver_test.go
  - internal/runtime/app/wire/extension_control_test.go
source_of_truth:
  - internal/adapter/skill/catalog.go
  - internal/runtime/app/wire/modules_capabilities.go
status: draft
last_verified: null
---

# 编写 Skill

## 学习目标

理解 Skill 的 Authority、Lifecycle、Integrity 与 Context Limit。

## Mechanisms

| Mechanism | Purpose | Execution |
| --- | --- | --- |
| Skill | Versioned Instruction/Dependency Plan | Bounded Context |

Skill 使用 Strict Manifest、Deterministic Root Precedence、Compatibility/Dependency
Resolution、Lockfile Digest、Enable State 与 Cycle/Conflict 检查。
加载 Skill 不授予 Tool Capability。

## Direct Capability Ownership

Skill、Memory 与 MCP 不共享通用 Extension Lifecycle。Wire 直接构造 Skill Catalog，
Skill 自身负责 Root Precedence、Dependency Resolution、Lock 与 Enable State。启停操作
通过 Durable Control Store 幂等记录；MCP 与 Memory 分别管理自己的连接和 Store。

## Authority 对比

| Mechanism | Authority Source | Revocation Effect |
| --- | --- | --- |
| Skill | Manifest/Lock/Enable State | 阻止 Future Load |

Skill 影响 Model Instruction，不授予 Execution Authority。

## Discovery/Activation

Skill 同样分离 Discovery、Dependency Resolution、Lock Verify、Bounded Load。Deterministic
Root Precedence 防止 Lower Candidate Shadow Governed Skill。

Web Transport `extension/list`/`extension/control` 与 Web Extensions View 提交到同一
Control Plane。Mutation 按 Operation ID 幂等，并持久化 Prepare/Commit Receipt。
Host 只投影 Runtime-owned State，不实现 Skill Lifecycle。

## 失败与安全边界

- Symlink Traversal、Malformed State/Lock、Digest Drift 失败。
- Skill Dependency Cycle/Conflict 显式。
- Extension 默认不获得 Raw Secret。
- 同一 Operation ID 携带不同内容时 Fail Closed。

## 测试与验证

```bash
go test ./internal/adapter/skill
go test ./internal/runtime/app/extension
go test ./internal/persist/extensioncontrol
```

## 动手实验

创建含一个 Dependency 的 Local Skill，观察解析、锁定和加载时的有界 Context。

## 复习问题

1. Skill Lock 绑定哪些事实？
2. 为什么 Skill Content 不能授予 Tool Authority？

## 延伸阅读

- [Extension Failure 与隔离策略](./06-failure-isolation.md)

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `extension-skill-plugin-hook` |
| 状态 | `draft` |
| 最后验证 | 尚未验证 |
