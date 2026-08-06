---
id: security-mode-policy
title: Mode, Posture, Policy, and Permission
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
status: verified
last_verified: 2026-08-06
---

# Mode, Posture, Policy, and Permission

English | [简体中文](../../zh-CN/07-security-governance/02-mode-posture-policy.md)

## Learning Objectives

Separate work intent (Mode), interaction posture (Permission), resource scope
(Grant/rule), and final Policy decision.

## Independent Axes

| Axis | Values | Question |
| --- | --- | --- |
| Mode | `plan`, `act`, `operate` | what class of work is requested? |
| Permission | `never`, `suggest`, `auto`, `bypass` | ask, deny, or automate? |
| Capability | read/write/process/network/plugin | what authority does Tool need? |
| Rule | Tool/resource/command + action | is this concrete Invocation in scope? |
| Surface | filesystem/network/process/plugin | should a broad decision be tightened? |

Mode and posture are not a security level slider. `plan+bypass` remains
read-only; `operate+suggest` can still ask; `bypass` cannot defeat a Deny,
Constitution Hold, or required Sandbox.

## Evaluation Order

```text
validated Invocation
 -> strongest repository rule (deny/hold wins)
 -> matching Tool Grant required
 -> Mode capability check
 -> explicit repository allow, or Permission decision
 -> Approval requirement
 -> granular surface tightening
```

Unknown Mode/Permission/Capability, an unvalidated Invocation, or a missing
Grant denies. Specific rules beat broad rules; stronger actions win. Shell
command rules inspect every command segment rather than trusting a prefix on
the first segment.

## Decision Matrix

| Posture | Read | Write | Process | Network/Plugin |
| --- | --- | --- | --- | --- |
| `never` | governed deny/limited read | deny | deny | deny |
| `suggest` | allow | ask | ask | ask |
| `auto` | allow | allow | operate-only or deny | operate may ask |
| `bypass` | allow | allow | mode/rule constrained | mode/rule constrained |

The implementation truth table is authoritative; this table is an orientation,
not permission by documentation.

## Persistent Workspace Permissions

An `always` decision can be compiled into a Workspace permission Rule only for
supported Invocation shapes. It records canonical Tool/resource or command
prefix, never raw credentials. Loading is strict; malformed state fails rather
than broadening authority.

Turn sampling uses a clone of Mode, Permission, and rules so mid-Turn UI changes
do not alter an already running decision context. Approval cache remains
session-coherent but is bound to Invocation fingerprints and expiry.

## Verification

```bash
go test ./internal/security/policy
go test ./internal/security/permissions
```

## Review Questions

1. Why are Mode and Permission separate axes?
2. Why does missing Grant deny even under `bypass`?
3. Why should mid-Turn posture changes wait until the next Turn?

## Further Reading

- [Guard, Approval, Constitution, and Sandbox](./03-approval-constitution-sandbox.md)
