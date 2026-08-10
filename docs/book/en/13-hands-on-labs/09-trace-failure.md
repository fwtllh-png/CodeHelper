---
id: lab-trace-failure
title: Investigate a Failure from Traces
audience:
  - learner
  - contributor
  - operator
prerequisites:
  - state-trace-usage-cost
  - state-reconstruct-failure
code_paths:
  - internal/observability/trace
  - internal/persist/state/eventlog
test_paths:
  - internal/observability/trace/trace_test.go
  - internal/persist/state/eventlog/log_test.go
source_of_truth:
  - internal/observability/trace/trace.go
  - internal/persist/state/eventlog/log.go
status: verified
last_verified: 2026-08-10
---

# Investigate a Failure from Traces

English | [简体中文](../../zh-CN/13-hands-on-labs/09-trace-failure.md)

## Goal and Prerequisites

Reconstruct a failed Turn from identity-linked Events, spans, usage, receipts,
and durable state without relying on a UI screenshot.

## Procedure

1. Run the malformed-Tool Fixture and retain its temporary state.
2. Start from terminal Event; record Operation/Thread/Turn/Item IDs.
3. Follow parent/child spans and Provider/Tool attempt metadata.
4. Correlate Catalog binding, policy/approval, sandbox, journal, and verify receipts.
5. Replay Event Log from the prior cursor and compare Projection.
6. State the earliest evidenced fault and rule out downstream symptoms.

```bash
go test ./internal/observability/trace ./internal/persist/state/eventlog
go test ./internal/adapter/provider/openai -run TestChatStreamRejectsMalformedAndAbruptStreams
go test ./internal/adapter/tool/guard -run TestMalformedArgumentsFailBeforePolicy
```

## Investigation Worksheet

| Question | Record |
| --- | --- |
| What was accepted? | Operation ID and admission Event |
| What is canonical order? | Event Cursor/hash evidence |
| Which phase first failed? | Provider/Tool/Approval/Verify Span |
| Did an effect occur? | Tool pairing, Journal, observed changes |
| Was it authorized? | Catalog binding, Policy, Approval identity |
| Was it reverted? | Journal fingerprints/recovery Receipt |
| What was measured? | Usage/Cost/Latency with known/unknown flags |
| What remains uncertain? | gaps, open spans, missing output, conflicts |

Build the timeline by Cursor, then attach timestamps. Label every statement as
**evidence**, **inference**, or **unknown**.

## Corruption Control

Run torn-tail and committed-corruption Event Log tests. The torn final write is
repairable; changed committed bytes must fail closed. Do not rerun the Agent to
“discover” what happened because that creates new effects and evidence.

Write a short incident report with impact, earliest fault, propagation,
terminal classification, recovery action, residual uncertainty, and a
regression test.

## Expected Result

The report distinguishes cause, propagation, terminal classification, and
recovery advice, with no secret/raw sensitive payload.

## Failure Diagnosis

Broken identity links indicate observability defects. Projection mismatch after
replay indicates persistence or reducer drift.

## Cleanup

Delete retained temporary state after redacted evidence is recorded.

## Review Questions

1. Why begin at terminal Event?
2. What is evidence versus inference?
3. Which data must be redacted?
4. Why is Cursor stronger than wall-clock ordering?

## Sources and Verification

| Item | Value |
| --- | --- |
| Catalog ID | `lab-trace-failure` |
| Status | `verified` |
