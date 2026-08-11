---
id: security-fail-closed
title: Fail-Closed Behavior and Platform Claims
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
status: draft
last_verified: null
---

# Fail-Closed Behavior and Platform Claims

English | [简体中文](../../zh-CN/07-security-governance/07-fail-closed.md)

## Learning Objectives

Decide when missing evidence must deny execution, distinguish unavailable from
disabled/failed, and make platform security claims testable.

## Fail-Closed Rule

When an operation requires authority or enforcement and the Runtime cannot
prove that requirement is satisfied, it refuses the operation. Examples:

- missing/unvalidated Policy Invocation;
- unknown Mode, Capability, Tool, or Catalog authority;
- Approval Host absent for an `ask`;
- required strong Sandbox unavailable;
- Credential missing or insecure;
- Egress host not granted;
- Plugin receipt/signature/capability drift;
- before-image or Journal persistence failure;
- hard Verification Runner unavailable.

Fail-closed does not mean crash, hide the feature, or return a generic error.
The refusal should have a stable category, sanitized reason, and recovery path.

## State Vocabulary

| State | Meaning | Correct behavior |
| --- | --- | --- |
| disabled | operator intentionally turned it off | report configuration |
| unavailable | required dependency/capability absent | refuse dependent action |
| denied | Policy/authority said no | do not retry around boundary |
| failed | attempted operation produced failure | classify effect/recovery |
| degraded | partial non-authoritative service remains | expose reduced guarantee |
| unknown | evidence is missing or shape unsupported | treat as not satisfied |

Converting unknown/unavailable into false success is the central anti-pattern.

## Platform Claim Discipline

A claim such as "strong sandbox" must name:

1. platform/backend and detected version/capability;
2. filesystem/network/process controls actually applied;
3. unsupported cases and fallback behavior;
4. attack/regression test that exercises the boundary;
5. Runtime behavior when the probe fails.

Build success or backend construction alone is not evidence. Architecture tests
also ensure Process Tools cannot construct/disable Sandbox and only Wiring owns
platform backend construction.

## Availability Without Authority Escalation

Graceful degradation is appropriate for optional orientation features such as
a Repo Index. It is not appropriate when the missing component is the
enforcement mechanism for a consequential action. An explicit, separately
approved `sandbox:none` escalation is new authority, not degradation.

Operator diagnostics should remain honest: "not evaluated", "unavailable",
and "zero findings" are distinct.

## Verification

```bash
make security-test
make sandbox-attack-test
go test ./internal/runtime/app/wire -run 'Test(ProcessToolsCannotConstructOrDisableSandbox|OnlyWireConstructsPlatformBackend)'
```

## Review Questions

1. Why is a stable refusal better than hiding an unavailable Tool?
2. When is degradation acceptable, and when must execution stop?
3. What evidence is needed to claim strong platform isolation?

## Further Reading

- [Security manual](../../../en/security.md)
- [Failure Isolation](../11-extension-ecosystem/06-failure-isolation.md)

## Sources and Verification

| Item | Value |
| --- | --- |
| Catalog ID | `security-fail-closed` |
| Status | `draft` |
| Last verified | Not yet verified |
