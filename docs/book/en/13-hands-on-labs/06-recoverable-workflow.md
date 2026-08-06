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
  - internal/orchestration/workflow/checkpoint
test_paths:
  - internal/orchestration/workflow/workflow_test.go
  - internal/orchestration/workflow/checkpoint/checkpoint_test.go
source_of_truth:
  - internal/orchestration/workflow/runtime.go
  - internal/orchestration/workflow/checkpoint/checkpoint.go
status: verified
last_verified: 2026-08-06
---

# Build a Recoverable Workflow

English | [简体中文](../../zh-CN/13-hands-on-labs/06-recoverable-workflow.md)

## Goal and Prerequisites

Run a small DAG, fail after a checkpoint, and resume without repeating completed
side effects.

## Procedure

1. Define three nodes: prepare, execute, verify; make verify depend on execute.
2. Persist checkpoint after each successful node.
3. Inject a deterministic failure before verify completes.
4. Reconstruct from durable workflow/checkpoint state.
5. Resume and assert prepare/execute are not duplicated.

```bash
go test ./internal/orchestration/workflow ./internal/orchestration/workflow/checkpoint
```

## Crash-Window Matrix

Instrument each node with a durable invocation counter.

| Stop point | Resume expectation |
| --- | --- |
| before `NodeStarted` | node is eligible |
| after `NodeStarted`, before effect | retry only under node policy |
| after effect, before settlement | indeterminate unless effect is idempotent/journaled |
| after output store, before settlement | terminal absent; orphan content reclaimable |
| after `NodeSettled` | node/result reused, never rerun |

Run the existing resume and output-handle tests:

```bash
go test ./internal/orchestration/workflow -run 'Test(ResumeOnlyRunsWhatDidNotFinish|NodeRetry|NodeTimeout)'
go test ./internal/orchestration/workflow/checkpoint -run 'Test(NodeOutputSurvives|ANodeWhoseOutputIsGone|ResumeRefuses)'
```

## Graph-Drift Control

After the first checkpoint, change one dependency or prompt and attempt resume
under the same Run ID. Fingerprint mismatch must stop before any node runs.
Record Run/Node status, attempts, output handles, and counters before/after.

## Expected Result

The DAG advances only ready nodes, the failed node remains diagnosable, resume
uses the latest compatible checkpoint, and terminal state is unique.

## Failure Diagnosis

Repeated execute indicates missing idempotency/checkpoint commit. Resume from an
incompatible graph must fail explicitly rather than guess.

## Cleanup

Delete the temporary state directory; do not reuse its checkpoint IDs.

## Review Questions

1. What is committed atomically with a checkpoint?
2. Which nodes may rerun?
3. How is graph drift detected?
4. Which crash window requires effect-specific recovery?

## Sources and Verification

| Item | Value |
| --- | --- |
| Catalog ID | `lab-recoverable-workflow` |
| Status | `verified` |
