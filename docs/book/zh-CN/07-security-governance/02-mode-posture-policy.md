---
id: security-mode-policy
title: Mode、Posture、Policy 与 Permission
audience:
  - contributor
  - operator
  - agent
prerequisites:
  - security-threat-model
code_paths:
  - internal/security/policy
  - internal/security/permissions
test_paths:
  - internal/security/policy/policy_test.go
  - internal/security/policy/granular_test.go
  - internal/security/permissions/permissions_test.go
source_of_truth:
  - internal/security/policy/policy.go
  - internal/security/permissions/permissions.go
status: draft
last_verified: null
---

# Mode、Posture、Policy 与 Permission

简体中文 | [English](../../en/07-security-governance/02-mode-posture-policy.md)

## 学习目标

区分 Work Intent（Mode）、Interaction Posture（Permission）、Resource Scope
（Grant/Rule）与最终 Policy Decision。

## 独立维度

| Axis | Value | Question |
| --- | --- | --- |
| Mode | `plan`、`act`、`operate` | 请求哪类工作？ |
| Permission | `never`、`suggest`、`auto`、`bypass` | Deny/Ask/Automate？ |
| Capability | Read/Write/Process/Network/Plugin | Tool 需要什么 Authority？ |
| Rule | Tool/Resource/Command + Action | Invocation 是否在 Scope？ |
| Surface | Filesystem/Network/Process/Plugin | 是否需要进一步收紧？ |

Mode/Posture 不是单一 Security Slider。`plan+bypass` 仍 Read-only；`operate+suggest` 仍可
Ask；`bypass` 不能击穿 Deny、Constitution Hold 或 Required Sandbox。

## Evaluation Order

```text
validated invocation
 -> strongest repository rule (deny/hold wins)
 -> matching tool grant required
 -> mode capability check
 -> repository allow or permission decision
 -> approval requirement
 -> granular surface tightening
```

Unknown Mode/Permission/Capability、Unvalidated Invocation、Missing Grant 全部 Deny。
Specific Rule 优先于 Broad Rule，Stronger Action 胜出。Shell Rule 检查每个 Command
Segment，而不是只信任第一个 Prefix。

## Decision Orientation

| Posture | Read | Write | Process | Network/Plugin |
| --- | --- | --- | --- | --- |
| `never` | Governed Deny/Limited | Deny | Deny | Deny |
| `suggest` | Allow | Ask | Ask | Ask |
| `auto` | Allow | Allow | Operate-only/Deny | Operate 可 Ask |
| `bypass` | Allow | Allow | 仍受 Mode/Rule | 仍受 Mode/Rule |

实现中的 Truth Table 才是 Authority；文档表格只用于理解。

## Persistent Workspace Permission

`always` Decision 只有在 Supported Invocation Shape 下才可编译为 Workspace Rule。记录
Canonical Tool/Resource 或 Command Prefix，不记录 Raw Credential。Malformed State
Fail Closed，不能扩大 Authority。

Turn Sampling Clone Mode、Permission、Rule，因此 Mid-turn UI Change 不改变 Running
Decision Context。Approval Cache 保持 Session Coherent，但仍绑定 Invocation Fingerprint/
Expiry。

## 验证

```bash
go test ./internal/security/policy
go test ./internal/security/permissions
```

## 复习问题

1. Mode 与 Permission 为什么是独立维度？
2. `bypass` 下 Missing Grant 为什么仍 Deny？
3. Mid-turn Posture Change 为什么应在 Next Turn 生效？

## 延伸阅读

- [Guard、Approval、Constitution 与 Sandbox](./03-approval-constitution-sandbox.md)
