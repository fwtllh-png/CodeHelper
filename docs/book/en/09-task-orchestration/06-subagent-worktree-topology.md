---
id: task-subagent-worktree
title: Subagents, Worktrees, and Topology
audience:
  - learner
  - contributor
  - agent
prerequisites:
  - task-worker-executor
  - tool-builtins
code_paths:
  - internal/orchestration/subagent
  - internal/adapter/tool/agent
  - internal/persist/state
  - internal/runtime/app/wire
test_paths:
  - internal/orchestration/subagent/subagent_test.go
  - internal/orchestration/subagent/control_test.go
  - internal/orchestration/subagent/control_plane_test.go
  - internal/persist/state/agentgraph_test.go
  - internal/runtime/app/wire/childworktree_test.go
source_of_truth:
  - internal/orchestration/subagent/subagent.go
  - internal/orchestration/subagent/control_plane.go
  - internal/orchestration/subagent/lifecycle.go
  - internal/orchestration/subagent/result.go
  - internal/orchestration/subagent/workgraph.go
status: draft
last_verified: null
---

# Subagents, Worktrees, and Topology

English | [简体中文](../../zh-CN/09-task-orchestration/06-subagent-worktree-topology.md)

## Learning Objectives

Understand child-Agent roles, topology, mailbox/control, budgets, isolated
worktrees, write claims, and guarded merge.

## Topology

```mermaid
flowchart TD
    P[Parent Runtime] --> M[Subagent Manager]
    M --> A[Child Agent / Thread]
    M --> B[Child Agent / Thread]
    A --> WA[Isolated Worktree]
    B --> WB[Isolated Worktree]
    A <--> X[Monotonic Mailbox]
    B <--> X
    WA --> G[Guarded Merge]
    WB --> G
    G --> P
```

Manager parses a finite Role set, maps Role to profile/stance, enforces depth
and concurrency budgets, provisions worktrees, records graph edges/status, and
routes Tool execution through a Gate. RuntimeHost owns real child Turns.

Agent Tree, Mailbox, Result, Completion Outbox, Budget Ledger, and worktree
ownership are durable Workspace state. Each Agent has a canonical path and CAS
revision. Terminal Result and Completion Outbox commit atomically, so parent
notification survives restart. Stable Message IDs plus `Receive/Ack` preserve
unacknowledged delivery.

Control operations list, wait, follow up, interrupt, complete/fail, and close.
`wait_agent` synchronizes on the same durable completion fact that also
notifies the parent. Result records Usage, artifacts, verification status, and
write paths.

Writing children use isolated Git worktrees when configured. Path claims detect
overlapping writes. Merge expands the child result into concrete parent file
resources, checks baseline drift, previews/dry-runs, and applies through guarded
file transactions. Serialized strategy may share the host Workspace/Journal;
isolated worktrees must not.

## Authority Inheritance

A child inherits bounded Context and shared accounting, not the parent's full
authority. Role selects a profile/stance; budgets can only narrow remaining
depth, concurrency, tokens, cost, and wall-clock. Child Tool Calls still pass
through its Guard/Policy. Takeover or a writing stance cannot manufacture a
missing Sandbox, Git Workspace, or Approval Host.

`spawn_agent` captures parent Context from the active Runtime Turn. The default
`task_capsule` includes bounded task evidence; `fresh`, `last_n_turns`, and
authorized `full` are explicit modes. The returned Context Receipt records
source identity, inclusion/exclusion reasons, byte/token budgets, and digest.
Legacy caller-supplied parent-context payloads are rejected.

Durable graph edges record parent/child identity and status across restart.
`max_parallel` bounds active children, `max_resident` also counts completed
children retaining Result/worktree state, and `max_total` bounds all spawns in
the durable tree. Mailbox sequence orders messages, but message delivery does
not itself start a Turn unless the control contract says so.

Child authority can only narrow the parent Session Profile. Under `suggest`, a
child Approval appears in the Host with Agent path and Role; the Host submits
the original Request ID through the parent Session and Runtime routes the
decision to the authoritative child Thread. Pending Approval survives restart.

## Merge as Two-Phase Integration

```text
child settles Result + write paths + baseline
 -> parent expands concrete Resources
 -> check claims and parent baseline drift
 -> preview/dry-run and Approval
 -> guarded atomic apply in parent Workspace
 -> verification and Receipt
```

Child completion is not parent integration. The parent may reject a valid child
result because another child claimed a path or the parent changed since fork.
Only the guarded apply changes parent bytes.

Worktrees isolate filesystem mutations, while write claims coordinate semantic
merge targets. Two branches can independently edit the same path without OS
conflict but still cannot both merge blindly.

## Failure Boundaries

- Unknown role/profile/stance fails before child execution.
- Depth, parallel, token, cost, and wall-clock budgets are enforced.
- Writing stance without a valid Git isolation strategy is rejected.
- Sibling worktrees are not removed during cleanup.
- Overlapping write claims conflict.
- Child self-report is not gate-proven verification.
- Parent drift blocks merge.
- Terminal Result and parent Completion Outbox cannot commit separately.
- Child Approval cannot widen parent authority or replace its Request ID.

## Tests and Verification

```bash
go test ./internal/orchestration/subagent
go test ./internal/adapter/tool/agent
go test ./internal/persist/state -run TestAgent
go test ./internal/runtime/app/wire -run 'Test.*(Child|Worktree|Agent)'
```

## Hands-On Lab

Run two writing-child conflict tests. Trace worktree identity, claimed paths,
baseline fingerprints, and the guarded merge decision.

## Review Questions

1. Why is each child a real Runtime Turn?
2. When may a child share the parent Journal?
3. Why are write claims still needed with worktrees?
4. Which authority may a child inherit, and which must be reacquired?
5. At what step do child changes become parent Workspace changes?

## Further Reading

- [Agent Tool](../06-tools-and-execution/02-file-shell-agent-tools.md)

## Sources and Verification

| Item | Value |
| --- | --- |
| Catalog ID | `task-subagent-worktree` |
| Status | `draft` |
| Last verified | Not yet verified |
