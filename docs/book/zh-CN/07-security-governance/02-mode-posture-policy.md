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

## 学习目标

区分 Work Intent（Mode）、Interaction Posture（Permission）、Resource Scope
（Grant/Rule）与最终 Policy Decision。

## 独立维度

| Axis | Value | Question |
| --- | --- | --- |
| Mode | `plan`、`act`、`operate` | 请求哪类工作？ |
| Permission | `never`、`suggest`、`auto`、`bypass` | Deny/Ask/Automate？ |
| Capability | Read/Write/Process/Network/External | Tool 需要什么 Authority？ |
| Rule | Tool/Resource/Command + Action | Invocation 是否在 Scope？ |
| Surface | Filesystem/Network/Process/External | 是否需要进一步收紧？ |

Mode/Posture 不是单一 Security Slider。`plan+bypass` 仍 Read-only；`operate+suggest` 仍可
Ask；`bypass` 不能击穿 Deny、Constitution Hold 或 Required Sandbox。

## Evaluation Order

```text
validated invocation
 -> matching Managed tool grant required
 -> Repository deny/hold/ask
 -> User deny/ask/allow
 -> mode capability check
 -> permission decision
 -> approval requirement
 -> granular surface tightening
```

Unknown Mode/Permission/Capability、Unvalidated Invocation、Missing Grant 全部 Deny。
低权 Authority Source 不能覆盖高权 Deny/Ask，Repository 只能收紧。Shell Rule 使用
Bash AST 与 Static argv Prefix；Allow 不能跨 Pipeline、Redirect、Subshell、Dynamic
Word 或 Interpreter Payload。

## Decision Orientation

| Posture | Read | Write | Process | Network/External |
| --- | --- | --- | --- | --- |
| `never` | Governed Deny/Limited | Deny | Deny | Deny |
| `suggest` | Allow | Ask | Ask | Ask |
| `auto` | Allow | Allow | Operate-only/Deny | Operate 可 Ask |
| `bypass` | Allow | Allow | 仍受 Mode/Rule | 仍受 Mode/Rule |

实现中的 Truth Table 才是 Authority；文档表格只用于理解。

## Persistent Workspace Permission

`always` Decision 只有在 Supported Invocation Shape 下才可编译为 User Rule。Shell
Grant 使用 AST-canonical Command Identity。Persistent Allow Prefix 禁止 Shell、
Interpreter，以及单 Token `git`/`rm`。Malformed State Fail Closed，不能扩大 Authority。

Managed、User、Repository Rule 以 Revisioned Snapshot 原子发布。Guard 在 Authorization
前绑定一个 Revision，Profile Provenance 记录 Revision 与每个 Source Digest。

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

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `security-mode-policy` |
| 状态 | `draft` |
| 最后验证 | 尚未验证 |
