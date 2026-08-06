---
id: security-fail-closed
title: Fail-closed 与平台能力声明
audience:
  - contributor
  - operator
  - agent
prerequisites:
  - security-process-isolation
  - security-extension-trust
code_paths:
  - internal/security
  - internal/adapter/tool/guard
  - internal/adapter/plugin
test_paths:
  - internal/security/sandbox/backend_test.go
  - internal/adapter/plugin/trust_test.go
  - internal/runtime/app/wire/sandbox_architecture_test.go
source_of_truth:
  - internal/security/sandbox/backend.go
  - internal/security/policy/policy.go
status: verified
last_verified: 2026-08-06
---

# Fail-closed 与平台能力声明

简体中文 | [English](../../en/07-security-governance/07-fail-closed.md)

## 学习目标

判断 Missing Evidence 何时必须 Deny，区分 Unavailable/Disabled/Failed，并使 Platform
Security Claim 可测试。

## Fail-closed Rule

当 Operation 需要 Authority/Enforcement，而 Runtime 无法证明满足时拒绝执行：

- Missing/Unvalidated Policy Invocation；
- Unknown Mode、Capability、Tool、Catalog Authority；
- `ask` 时 Approval Host 缺失；
- Required Strong Sandbox Unavailable；
- Credential Missing/Insecure；
- Egress Host 未 Grant；
- Plugin Receipt/Signature/Capability Drift；
- Before-image/Journal Persistence Failure；
- Hard Verification Runner Unavailable。

Fail-closed 不等于 Crash、隐藏 Feature 或 Generic Error。Refusal 应有 Stable Category、
Sanitized Reason、Recovery Path。

## State Vocabulary

| State | Meaning | Correct Behavior |
| --- | --- | --- |
| Disabled | Operator 主动关闭 | Report Config |
| Unavailable | Dependency/Capability 缺失 | Refuse Dependent Action |
| Denied | Policy/Authority 拒绝 | 不绕过重试 |
| Failed | Attempt 失败 | Classify Effect/Recovery |
| Degraded | Partial Non-authoritative Service | Expose Reduced Guarantee |
| Unknown | Evidence/Shape 缺失 | Treat as Unsatisfied |

把 Unknown/Unavailable 转为 False Success 是核心反模式。

## Platform Claim Discipline

“Strong Sandbox” Claim 必须说明：

1. Platform/Backend 与 Detected Capability；
2. 实际应用的 Filesystem/Network/Process Control；
3. Unsupported Case/Fallback；
4. 边界 Attack/Regression Test；
5. Probe Failure 时 Runtime Behavior。

Build Success 或 Backend Construction 本身不是 Evidence。Architecture Test 还确保 Process
Tool 不能构造/关闭 Sandbox，只有 Wiring 拥有 Platform Backend Construction。

## Availability 不扩大 Authority

Repo Index 等 Optional Orientation Feature 可 Graceful Degrade；Consequential Action 的
Enforcement Mechanism 缺失时不可降级。单独 Approve 的 `sandbox:none` 是 New Authority，
不是 Degradation。

Operator Diagnostic 必须诚实区分 Not Evaluated、Unavailable、Zero Findings。

## 验证

```bash
make security-test
make sandbox-attack-test
go test ./internal/runtime/app/wire -run 'Test(ProcessToolsCannotConstructOrDisableSandbox|OnlyWireConstructsPlatformBackend)'
```

## 复习问题

1. Stable Refusal 为什么优于隐藏 Unavailable Tool？
2. 什么情况可 Degrade，什么情况必须 Stop？
3. 声称 Strong Platform Isolation 需要哪些 Evidence？

## 延伸阅读

- [安全手册](../../../zh-CN/security.md)
- [Failure Isolation](../11-extension-ecosystem/06-failure-isolation.md)
