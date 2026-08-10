---
id: lab-provider-adapter
title: Implement a Provider Adapter
audience:
  - contributor
prerequisites:
  - extension-provider
  - practice-fixtures-smoke
code_paths:
  - internal/adapter/provider
  - internal/adapter/model
test_paths:
  - internal/adapter/provider/openai/stream_test.go
  - internal/adapter/provider/fault_injection_test.go
source_of_truth:
  - internal/adapter/provider/types.go
status: draft
last_verified: null
---

# Implement a Provider Adapter

English | [简体中文](../../zh-CN/13-hands-on-labs/03-provider-adapter.md)

## Goal and Prerequisites

Build a Fixture-only stream decoder extension before any network client.

## Procedure

1. Copy a small synthetic SSE file into a temporary test directory.
2. Decode text, Tool fragments, Usage, terminal, and malformed frames into
   normalized `provider.Event`.
3. Assert ordering, bounded fragments, one terminal, and stable errors.
4. Add fuzz seeds for truncated lines and unknown fields.
5. Run:

```bash
go test ./internal/adapter/provider/...
go test ./internal/adapter/provider/openai -fuzz FuzzStreamParserOpenAI -fuzztime=5s
```

Do not register a production Provider for this lab.

## Test Matrix and Evidence

| Case | Required observation |
| --- | --- |
| fragmented text | ordered normalized deltas |
| fragmented Tool call | executable only after complete valid JSON |
| cumulative usage | correct call/sample accounting |
| unknown optional field | ignored without changing known semantics |
| malformed required field | stable terminal decode error |
| disconnect before output | retry classification retained |
| disconnect after output | partial-output flag; no transparent replay |

Use a fake clock/body closer to assert cancellation closes decoder and response
resources. Compare the new decoder against `provider.Provider` and `Stream`
contracts, not another wire adapter.

## Completion Gate

The lab is complete only when success, malformed input, cancellation, and
partial-stream failure all have deterministic tests, the Fuzz run finds no
panic/unbounded growth, and no production catalog entry was added.

## Expected Result

The decoder is deterministic, accepts forward-compatible unknown fields,
rejects invalid required shapes, and never exposes credential material.

## Failure Diagnosis

Duplicate terminal indicates lifecycle state error; incomplete Tool arguments
indicate fragment assembly error; unbounded scanner growth violates limits.

## Cleanup

Remove the temporary test/fixture files and confirm `git status --short`.

## Review Questions

1. Why start at the decoder?
2. When are Tool arguments executable?
3. Which errors may be retried?
4. What marks the Provider stream commit point?

## Sources and Verification

| Item | Value |
| --- | --- |
| Catalog ID | `lab-provider-adapter` |
| Status | `verified` |
