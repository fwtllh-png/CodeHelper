---
id: lab-worker-retry
title: Debug Worker Leases and Retries
audience:
  - contributor
  - operator
prerequisites:
  - task-worker-executor
  - task-lease-retry
code_paths:
  - internal/orchestration/task
  - internal/orchestration/worker
test_paths:
  - internal/orchestration/task/execution_test.go
  - internal/orchestration/worker/worker_test.go
source_of_truth:
  - internal/orchestration/task/execution.go
  - internal/orchestration/worker/worker.go
status: verified
last_verified: 2026-08-06
---

# Debug Worker Leases and Retries

English | [简体中文](../../zh-CN/13-hands-on-labs/07-worker-lease-retry.md)

## Goal and Prerequisites

Force lease expiry and classify retry behavior while preserving at-most-one
active claim and idempotent outcomes.

## Procedure

1. Create a queued Task with deterministic identity.
2. Claim it using Worker A and advance a fake clock past lease expiry.
3. Let Worker B take over with a new lease generation.
4. Submit a late heartbeat/result from Worker A.
5. Inject retryable, terminal, and canceled execution outcomes.

```bash
go test ./internal/orchestration/task ./internal/orchestration/worker
```

## Timeline and Assertions

```text
t0 create queued task
t1 A claims -> attempt 1, owner A, expiry E1
t2 clock > E1
t3 B reclaims -> attempt 2, owner B, expiry E2
t4 A heartbeat/settle rejected
t5 B settles exactly one terminal
```

Collect Task version, state, attempt records, owner, expiry, scheduled time,
failure reason, and Executor call count at each point. The lease is a repository
fence, not proof that A stopped producing external effects.

## Retry Controls

Test these separately:

- graceful drain returns to queue and refunds the attempt;
- lease expiry consumes the attempt;
- retryable failure applies capped backoff;
- exhausted attempts become terminal failure;
- non-idempotent shell work refuses automatic retry;
- healthy foreign lease survives recovery.

Use fake time for expiry/backoff and channels for takeover; do not synchronize
with sleep.

## Expected Result

Stale Worker updates are rejected; retryable failure schedules bounded backoff;
terminal failure does not loop; duplicate completion is idempotent.

## Failure Diagnosis

Two accepted generations indicate lease fencing failure. Retry after terminal
classification indicates state-machine corruption.

## Cleanup

Delete the temporary Task database and stop both Worker loops.

## Review Questions

1. Why does a lease need a generation/token?
2. Which failures are retryable?
3. What makes completion idempotent?
4. Why can lease fencing not undo an external effect?

## Sources and Verification

| Item | Value |
| --- | --- |
| Catalog ID | `lab-worker-retry` |
| Status | `verified` |
