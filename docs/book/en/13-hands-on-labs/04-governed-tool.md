---
id: lab-governed-tool
title: Implement a Governed Tool
audience:
  - contributor
  - agent
prerequisites:
  - extension-tool
code_paths:
  - internal/adapter/tool
  - internal/adapter/tool/guard
test_paths:
  - internal/adapter/tool/tool_test.go
  - internal/adapter/tool/guard/guard_test.go
source_of_truth:
  - internal/adapter/tool/tool.go
  - internal/adapter/tool/guard/guard.go
status: draft
last_verified: null
---

# Implement a Governed Tool

English | [简体中文](../../zh-CN/13-hands-on-labs/04-governed-tool.md)

## Goal and Prerequisites

Implement a read-only Fixture Tool and prove it cannot bypass Catalog Binding,
Schema validation, Policy, or Resource claims.

## Procedure

1. Define a strict Descriptor with one bounded path argument.
2. Resolve the path as a read Resource before execution.
3. Return bounded Content plus Structured Metadata.
4. Register it in a test Registry and bind a Catalog snapshot.
5. Execute through `Guard.ExecuteBound`.
6. Test valid read, extra field, traversal, stale binding, and undeclared Tool.

```bash
go test ./internal/adapter/tool ./internal/adapter/tool/guard
```

## Adversarial Matrix

Instrument the Fixture Executor with an atomic call counter.

| Input/condition | Expected result | Executor calls |
| --- | --- | --- |
| valid bound read | bounded result + observed read Resource | 1 |
| extra/unknown field | schema error | 0 |
| traversal/symlink escape | resource/sandbox denial | 0 |
| stale/revoked binding | catalog error | 0 |
| Policy deny | denial Receipt | 0 |
| canceled claim wait | cancellation and released claim | 0 |

Then replace the registered Executor after taking the snapshot. The old bound
call must fail rather than execute the replacement under stale authority.

## Evidence to Inspect

Check Descriptor identity/revision, normalized arguments, resolved Resources,
Policy decision, Sandbox requirement, claim release, bounded model content, and
structured metadata. The Executor's prose is not evidence of filesystem access.

## Expected Result

Only the valid bound call reaches the Executor. Every invalid case fails before
side effects and records a stable denial/error.

## Failure Diagnosis

Executor invocation on invalid input means Guard ordering is broken. A path not
present in claims means Resource resolution is incomplete.

## Cleanup

Remove the Fixture Tool if it was created only for the lab; retain a production
Tool only with focused tests and Wire registration.

## Review Questions

1. Why bind calls to Catalog generation?
2. Why resolve Resources before Policy?
3. What output is safe for the model?
4. How does the call counter prove Guard ordering?

## Sources and Verification

| Item | Value |
| --- | --- |
| Catalog ID | `lab-governed-tool` |
| Status | `verified` |
