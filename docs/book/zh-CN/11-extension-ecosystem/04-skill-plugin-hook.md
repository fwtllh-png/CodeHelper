---
id: extension-skill-plugin-hook
title: 编写 Skill 与 Hook
audience:
  - contributor
  - operator
  - agent
prerequisites:
  - extension-tool
code_paths:
  - internal/adapter/skill
  - internal/adapter/hooks
  - internal/runtime/extension
  - internal/runtime/app/extension
  - internal/persist/extensioncontrol
  - internal/persist/extensionplan
test_paths:
  - internal/adapter/skill/resolver_test.go
  - internal/adapter/hooks/hooks_test.go
  - internal/runtime/extension/plan_test.go
  - internal/runtime/app/wire/extension_control_test.go
source_of_truth:
  - internal/adapter/skill/catalog.go
  - internal/adapter/hooks/manager.go
  - internal/runtime/extension/registry.go
  - internal/runtime/extension/plan.go
status: draft
last_verified: null
---

# 编写 Skill 与 Hook

## 学习目标

选择正确扩展机制，理解其 Authority、Lifecycle、Integrity 与 Execution Limit。

## Mechanisms

| Mechanism | Purpose | Execution |
| --- | --- | --- |
| Skill | Versioned Instruction/Dependency Plan | Bounded Context |
| Hook | Lifecycle/Permission Callback | Bounded Configured Process |

Skill 使用 Strict Manifest、Deterministic Root Precedence、Compatibility/Dependency
Resolution、Lockfile Digest、Enable State 与 Cycle/Conflict 检查。
加载 Skill 不授予 Tool Capability。

Hook 在定义的 Lifecycle Point 经 Manager/Adapter 运行。Permission Hook 可 Allow/Deny/
Ask；Guard 对 Updated Input 重新验证，Hook Error/Unresolved Ask Fail Closed。

## Shared Runtime Extension Contract

Skill、Hook、Memory 与 MCP 都适配到统一 Typed Runtime
Extension Core。Contributor 声明 Identity、Phase、Capability、Failure Policy、Timeout
与 Output Budget。注册时校验 Contract 并 Seal 不可变 Registry；Contributor 只接收
显式 Capability，不接收 Construction State 或私有 Tool Registry。

Source Resolution 生成绑定当前 Permission Digest 的 Deterministic Digested Plan。
Skill 的启停操作通过 Durable Control Store 幂等记录；Hook 与 MCP 的进程和连接生命周期
由各自 Adapter 管理，不通过共享可执行包生命周期间接授权。

## Authority 对比

| Mechanism | Authority Source | Revocation Effect |
| --- | --- | --- |
| Skill | Manifest/Lock/Enable State | 阻止 Future Load |
| Hook | Strict Config/Lifecycle/Failure Policy | 移除 Callback/Kill Process Tree |

Skill 影响 Model Instruction，不授予 Execution Authority。Hook 在 Lifecycle Point
建议或过滤，不能自行 Commit Tool Effect。

## Discovery/Activation

Skill 同样分离 Discovery、Dependency Resolution、Lock Verify、Bounded Load。Deterministic
Root Precedence 防止 Lower Candidate Shadow Governed Skill。

Hook Failure Policy 按 Event 区分：Observer 可 Audit/Fail Open；Message/Tool/Permission
Gate Fail Closed。两者 Output/Environment 都有界并 Sanitized。

Web Transport `extension/list`/`extension/control` 与 Web Extensions View 提交到同一
Control Plane。Mutation 按 Operation ID 幂等，并持久化 Prepare/Commit
Receipt。Host 只投影 Runtime-owned State，不实现 Extension Lifecycle。

## 失败与安全边界

- Symlink Traversal、Malformed State/Lock、Digest Drift 失败。
- Skill Dependency Cycle/Conflict 显式。
- Hook Timeout/Failure 不静默 Allow。
- Extension 默认不获得 Raw Secret。
- 同一 Operation ID 携带不同内容时 Fail Closed。

## 测试与验证

```bash
go test ./internal/adapter/skill
go test ./internal/adapter/hooks
go test ./internal/runtime/extension ./internal/runtime/app/extension
go test ./internal/persist/extensioncontrol
go test ./internal/persist/extensionplan
```

## 动手实验

创建含一个 Dependency 的 Local Skill 和 No-op Hook，比较两者
可影响的 Surface。

## 复习问题

1. 何时应使用 Skill，何时应使用 Hook？
2. Guard 为什么重验 Hook-updated Input？
3. Skill Lock 绑定哪些事实？
4. Hook Failure Policy 为什么按 Event 区分？

## 延伸阅读

- [Extension Failure 与隔离策略](./06-failure-isolation.md)

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `extension-skill-plugin-hook` |
| 状态 | `draft` |
| 最后验证 | 尚未验证 |
