---
id: lab-approval-denial
title: Exercise Approval and Denial
audience:
  - learner
  - contributor
prerequisites:
  - tool-guard-pipeline
  - security-approval-sandbox
code_paths:
  - internal/security/policy
  - internal/runtime/app
test_paths:
  - internal/security/policy/policy_test.go
  - internal/runtime/app/runtime_test.go
source_of_truth:
  - internal/security/policy/policy.go
  - internal/runtime/protocol/message.go
status: draft
last_verified: null
---

# Exercise Approval and Denial

English | [简体中文](../../zh-CN/13-hands-on-labs/05-approval-and-denial.md)

## Goal and Prerequisites

Observe allow, ask, deny, stale decision, and post-approval revalidation without
performing a real write.

## Procedure

1. Build a test Invocation for a synthetic write Resource.
2. Evaluate it under allow, ask, and deny policies.
3. For ask, capture Approval identity and decide through Runtime Operation.
4. Change the bound edit-plan/resource identity before deciding.
5. Confirm stale or drifted decisions cannot authorize execution.

```bash
go test ./internal/security/policy/...
go test ./internal/runtime/app -run 'Test.*Approval'
```

## Decision Matrix

| Scenario | Expected authority |
| --- | --- |
| allow | this exact evaluated invocation |
| deny | none; no Executor call |
| ask + approve once | one matching call before expiry |
| ask + approve session | matching bounded scope only |
| wrong request/call/plan ID | stale decision rejected |
| changed arguments/resources | fresh evaluation required |
| expired/canceled approval | none |
| Sandbox escalation after approval | separate approval required |

Record Approval request identity, call arguments digest, canonical Resources,
scope, expiry, decision source, and resulting Event/Receipt. Add an Executor
counter to prove that pause and deny produce no side effect.

## Negative Control

Approve the original request, mutate one Resource, then submit the old decision.
The Runtime must reject it and leave the counter at zero. This isolates
authority binding from ordinary execution failure.

## Expected Result

Deny is terminal, ask pauses without executing, matching approval resumes once,
and drift requires fresh evaluation. Receipts preserve decision provenance.

## Failure Diagnosis

Execution before decision is a Guard ordering failure. Reusing a decision across
identity/resource changes is an authority-binding defect.

## Cleanup

All state is test-local; remove temporary policy files if manually created.

## Review Questions

1. What is approval bound to?
2. Why revalidate after approval?
3. How does deny differ from failed execution?
4. Why is Sandbox escalation a new authority decision?

## Sources and Verification

| Item | Value |
| --- | --- |
| Catalog ID | `lab-approval-denial` |
| Status | `verified` |
