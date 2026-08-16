---
id: lab-recoverable-workflow
title: Build a Recoverable Workflow
audience:
  - contributor
prerequisites:
  - task-automation-workflow
  - task-checkpoint-recovery
code_paths:
  - internal/orchestration/workflow
  - internal/orchestration/kernel
  - internal/orchestration/store
test_paths:
  - internal/orchestration/workflow/workgraph_test.go
  - internal/orchestration/store/store_test.go
source_of_truth:
  - internal/orchestration/workflow/runtime.go
  - internal/orchestration/store/store.go
status: verified
last_verified: 2026-08-16
---

# Build a Recoverable Workflow

English | [简体中文](../../zh-CN/13-hands-on-labs/06-recoverable-workflow.md)

## Goal and Prerequisites

Run a small DAG, fail after a durable WorkGraph settlement, and resume without
repeating completed work.

## Procedure

1. Define three nodes: prepare, execute, verify; make verify depend on execute.
2. Persist each successful Node through a WorkGraph settlement command.
3. Inject a deterministic failure before verify completes.
4. Reconstruct from ordered WorkGraph facts.
5. Resume and assert prepare/execute are not duplicated.

```bash
go test ./internal/orchestration/workflow ./internal/orchestration/store
```

## Crash-Window Matrix

Instrument each node with a durable invocation counter.

| Stop point | Resume expectation |
| --- | --- |
| before Claim commits | Node remains eligible |
| after Claim, before Effect bind | recover through Lease/Epoch policy |
| after external Effect, before Settlement | indeterminate unless Effect is idempotent/journaled |
| during terminal transaction | Settlement and Outbox both commit or both roll back |
| after Settlement | Node/result reused, never rerun |

Run the existing resume and output-handle tests:

```bash
go test ./internal/orchestration/workflow -run 'Test(DurableWorkGraph|NodeTimeout|SpecDrift)'
go test ./internal/orchestration/store -run 'Test(StoreTerminalCommit|AuditDetects)'
```

## Graph-Drift Control

After the first settlement, change one dependency or prompt and attempt resume
under the same Run ID. Fingerprint mismatch must stop before any node runs.
Record Run/Node status, attempts, output handles, and counters before/after.

## Expected Result

The DAG advances only ready nodes, the failed node remains diagnosable, resume
uses the same fact-replayed WorkGraph, and terminal state is unique.

## Failure Diagnosis

Repeated execute indicates missing idempotency/settlement. Resume from an
incompatible graph must fail explicitly rather than guess.

## Cleanup

Delete the temporary state directory; do not reuse its Run IDs.

## Review Questions

1. What is committed atomically with a WorkGraph command?
2. Which nodes may rerun?
3. How is graph drift detected?
4. Which crash window requires effect-specific recovery?

## Sources and Verification

| Item | Value |
| --- | --- |
| Catalog ID | `lab-recoverable-workflow` |
| Status | `verified` |
| Last verified | 2026-08-16 |
