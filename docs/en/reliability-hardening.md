# Runtime Reliability Hardening

[简体中文](../zh-CN/reliability-hardening.md) | English

This document is the long-running governance checklist for CodeHelper
reliability. It organizes audits, root-cause consolidation, shared repairs, and
acceptance evidence. It is not evidence that an item has shipped. Code, tests,
protocol schemas, and product documentation remain authoritative for current
behavior.

## Goal and Scope

The program covers the complete execution path:

```text
CLI / TUI / VS Code / ACP / Worker
                  |
          Operation / Event
                  |
      Application / Turn Kernel
                  |
  Context / Provider / Guarded Tool
                  |
 Policy / Approval / Journal / Sandbox
                  |
 Persistence / Recovery / Observability
```

The goal is not to eliminate every error. When an error occurs, the system must:

1. retain progress already made;
2. report a stable, structured, and attributable failure;
3. resume safely instead of repeating the entire Turn;
4. enter an explicit terminal or durable waiting state when it cannot resume;
5. repair a shared boundary once for every Host and execution entry.

This program does not accept local patches that merely increase continuation
counts, step counts, token counts, or timeouts for one symptom. Resources still
need governance, but implicit fixed limits must not turn progressing work into a
terminal error. Exhausting an explicit user or operator budget must also retain
resumable state and explain why execution stopped.

## Core Reliability Invariants

Every audit and repair is evaluated against these invariants:

1. **One terminal-state owner:** only the Turn Kernel decides Completed, Failed,
   or Canceled. Providers, Tools, Hosts, and budget modules submit facts or
   request convergence.
2. **Implicit hard limits do not terminate progress:** a fixed step,
   continuation, output-length, or wall-clock limit cannot terminate work that
   continues to produce valid progress.
3. **Interaction waits are structured states:** Input and Approval are durable,
   recoverable waits. They are not inferred from prose or represented as
   failures.
4. **Effects are recorded before execution:** every consequential Effect has a
   stable Operation ID, a pre-execution fact, and a replayable result. An
   uncertain outcome is not treated as unexecuted.
5. **Partial output is not silently discarded:** provider disconnects,
   incomplete Tool Calls, and interrupted streaming arguments are retained or
   explicitly fall back to a complete request.
6. **Durable and terminal state commit atomically:** final output, Receipt,
   Session Delta, Terminal Event, and Outbox share one commit boundary.
7. **Cancellation propagates without corrupting commits:** cancellation reaches
   child operations but does not undo committed facts or leave permanent
   Running state.
8. **Hosts only project state:** CLI, TUI, VS Code, ACP, and Workers do not
   execute missing work, invent terminal states, or reinterpret Runtime
   failures.
9. **Failures are reconstructable:** Session, Turn, Operation, Attempt, Effect,
   and Resume identities can reconstruct a failed execution.
10. **Security failures remain fail-closed:** recovery never bypasses Policy,
    Approval, Constitution, Journal, or Sandbox.

## Status and Priority

| Status | Meaning |
| --- | --- |
| `unassessed` | No evidence-based audit has been completed |
| `auditing` | Code, tests, runtime records, and fault evidence are being collected |
| `repairing` | The root cause and shared owner are known and a unified repair is underway |
| `verified` | Acceptance checks passed and code, test, and documentation evidence is recorded |
| `blocked` | A concrete external dependency and its release condition are recorded |

Priority definitions:

- `P0`: can terminate work incorrectly, repeat a side effect, corrupt state, or
  prevent recovery;
- `P1`: can cause Host disagreement, resource leaks, poor diagnosis, or a
  materially lower recovery rate;
- `P2`: improves long-running, cross-platform, and release reliability.

## Workstream Summary

| ID | Workstream | Priority | Status | Primary ownership |
| --- | --- | --- | --- | --- |
| R0 | Failure baseline and repository-wide limit inventory | P0 | unassessed | Runtime / Engineering |
| R1 | Turn state machine and terminal convergence | P0 | unassessed | `internal/runtime/agent` |
| R2 | Dynamic budgets, progress detection, and Context | P0 | unassessed | Agent / Context / Config |
| R3 | Provider streams and incomplete-call recovery | P0 | unassessed | Provider / Agent |
| R4 | Typed faults, Retry, and Deadline semantics | P0 | unassessed | Protocol / Runtime / Adapters |
| R5 | Persistence, Journal, idempotency, and crash recovery | P0 | unassessed | Persist / Runtime |
| R6 | Tool, Guard, Sandbox, and side-effect consistency | P0 | unassessed | Tool / Security / Platform |
| R7 | Concurrency, cancellation, backpressure, and resources | P1 | unassessed | Runtime / Platform |
| R8 | Protocol and cross-Host behavior | P1 | unassessed | Protocol / Hosts |
| R9 | Startup, shutdown, wiring, configuration, and environment | P1 | unassessed | Wire / Config / Hosts |
| R10 | Observability and failure reconstruction | P1 | unassessed | Observability / Persist |
| R11 | Fault injection and reliability gates | P1 | unassessed | Tests / CI |

## R0: Failure Baseline and Limit Inventory

**Audit scope**

- every `panic`, `log.Fatal`, `os.Exit`, ignored error, and string-based error
  classification;
- every step, retry, continuation, token, queue, Activity, and output-length
  limit;
- every fixed Timeout, Deadline, Ticker, Lease, and Heartbeat;
- every asynchronous boundary, persistence boundary, and external side effect;
- recurring exit and failed-recovery paths from recent incidents.

**Unified direction**

- produce a limit-purpose inventory that distinguishes capacity protection,
  backpressure, convergence requests, and terminal decisions;
- produce a complete Failure Taxonomy and state-transition map;
- assign symptoms with the same root cause to one shared owner instead of
  patching each reporting location.

**Completion evidence**

- every implicit limit has an owner, purpose, trigger behavior, and retain or
  remove decision;
- every exit maps to a typed Fault and Kernel state;
- every failure sample maps to one primary R1-R11 workstream.

## R1: Turn State Machine and Terminal Convergence

**Audit scope**

- every transition among Start, Running, Awaiting Input, Awaiting Approval,
  Verifying, Finalizing, Completed, Failed, Canceled, and Suspended;
- duplicate completion, illegal transitions, late results, repeated recovered
  requests, and permanent Running state;
- every combination of `turn_complete`, `request_user_input`, Approval, Cancel,
  Continue, and Journal results;
- semantic differences among Main Turns, Child Turns, Workflows, and recovered
  Turns.

**Unified direction**

- keep terminal and convergence decisions in one Reducer/Kernel state machine;
- make provider stops, budget signals, and Tool failures typed Commands only;
- run model-based or property tests over the state graph to prove that every
  non-waiting state can converge.

**Completion evidence**

- the transition table matches code and covers illegal transitions;
- replayed, duplicate, reordered, and late Commands cannot create a second
  logical terminal state;
- every interactive wait recovers its original Request ID across processes.

## R2: Dynamic Budgets, Progress Detection, and Context

**Audit scope**

- incorrect coupling among Reasoning Effort, Output Reserve, Context Window,
  Tool Spend, cost, and time budgets;
- fixed `max_tokens`, `max_steps`, continuation counts, and wall-clock timeouts;
- Context compaction that loses incomplete Tool Calls, interaction requests,
  approvals, or recovery state;
- Resume Prompts that reinject history and grow recursively.

**Unified direction**

- separate reasoning policy, output capacity, Context capacity, cost, and
  execution leases;
- define progress uniformly from new facts, Tool results, state changes, and
  valid output;
- use capacity pressure to compact, apply backpressure, request convergence, or
  suspend recoverably instead of inventing a terminal state;
- when an explicit budget is exhausted, emit a structured reason, current
  progress, and continuation entry.

**Completion evidence**

- large Tool arguments and long answers are not truncated by Reasoning level;
- progressing work is not terminated by implicit counters;
- no-progress loops converge deterministically without repeating the original
  Prompt forever;
- critical state remains equivalent before and after compaction.

## R3: Provider Streams and Incomplete-Call Recovery

**Audit scope**

- EOF, connection reset, rate limiting, empty responses, duplicate chunks,
  reordered chunks, and missing Finish Reasons;
- partial JSON, incomplete Tool Calls, parallel Tool Calls, and partial
  Reasoning;
- switching between Responses WebSocket and complete requests, including
  invalid response chains;
- attribution across Usage, logical requests, and transport requests.

**Unified direction**

- provide a persistable incremental response assembler with explicit
  completeness states;
- retain incomplete Tool Call fragments and continue the same logical request
  or fall back safely;
- use incremental transport only when support and chain state are certain;
  otherwise use a complete request;
- normalize provider differences in adapters rather than branching the Agent
  Loop.

**Completion evidence**

- disconnecting at any chunk boundary does not silently discard confirmed data;
- Retry cannot execute a closed Tool Call twice;
- provider contract tests cover disconnects, duplicates, reordering, empty
  responses, and format drift.

## R4: Typed Faults, Retry, and Deadline Semantics

**Audit scope**

- string-matched errors, causes lost through wrapping, and missing failure
  stages;
- retryable failures that are not retried, permanent failures that loop, and
  retries performed independently at multiple layers;
- timeouts that conflate connection, idle, operation, Turn lease, and user
  budget;
- retries without idempotency, backoff, jitter, or progress checks.

**Unified direction**

- define a stable Fault taxonomy across Protocol, Runtime, and adapters;
- carry at least Kind, Stage, Retryability, Operation ID, Cause, and Resume Hint;
- let one recovery policy choose Retry, Resume, Wait, Block, or Fail from the
  Fault, idempotency, and progress;
- use layered, propagated, configurable, and renewable Deadline semantics.

**Completion evidence**

- exactly one layer decides whether each error is retried;
- the same Fault has the same machine semantics in every Host;
- every timeout identifies its scope and cannot leave unrecoverable Running
  state.

## R5: Persistence, Journal, Idempotency, and Crash Recovery

**Audit scope**

- transaction boundaries across SQLite, Event Log, CAS, Session, Snapshot,
  Journal, and Outbox;
- crashes after execution but before recording, and after recording but before
  projection;
- Terminal Commit, Session Delta application, Outbox publication, and recovery
  scans;
- corrupt data, full disks, killed processes, and version changes.

**Unified direction**

- establish logically exactly-once closure with Operation, Effect, and Attempt
  identities;
- model external side effects as Prepared, Started, Result Known, Committed, or
  Outcome Unknown;
- commit terminal facts atomically and apply in-memory state only afterward;
- replay stable facts during recovery instead of regenerating completed work.

**Completion evidence**

- recovery remains deterministic after a crash injected at every persistence
  write point;
- Terminal Events are neither lost nor logically duplicated;
- Outcome Unknown has a dedicated path that does not blindly retry side
  effects.

## R6: Tool, Guard, Sandbox, and Side-Effect Consistency

**Audit scope**

- the full Tool Registry, Guard, Policy, Permission, Approval, Constitution,
  Journal, and Sandbox call chain;
- idempotency boundaries for file edits, Shell, network, Git, MCP, and external
  service operations;
- preservation of stdout, stderr, Exit Code, partial output, and Sandbox denial;
- environment failures misclassified as policy denial, or safety boundaries
  bypassed in the opposite direction.

**Unified direction**

- use one Guarded Effect protocol for every consequential Tool;
- persist intent and approval evidence before execution and structured results
  afterward;
- distinguish Denied, Unavailable, Execution Failed, and Outcome Unknown;
- pass recovery and Retry through Guard again instead of reusing expired
  authorization.

**Completion evidence**

- no Host, Workflow, Subagent, or extension path bypasses Guard;
- replay cannot reapply file or external side effects;
- macOS, Linux, and Windows capability differences produce structured,
  testable outcomes.

## R7: Concurrency, Cancellation, Backpressure, and Resources

**Audit scope**

- Goroutine leaks, blocked Channels, duplicate Close, lock ordering, data races,
  and orphaned processes;
- slow Hosts, Providers, Tools, and full queues;
- cancellation propagation across Parent/Child, Workflow, Worker, and external
  processes;
- backpressure in mailboxes, Event Hub, Outbox, and streaming output.

**Unified direction**

- assign an explicit owner to every Goroutine, Channel, Process, and
  Subscription;
- use structured concurrency and one-way cancellation; after a commit boundary,
  cancellation only stops later work;
- expose backpressure or persist queued work when queues fill instead of
  silently discarding critical state;
- close resources in reverse dependency order and join errors.

**Completion evidence**

- race, leak, and cancellation-storm tests pass consistently;
- a slow consumer cannot deadlock Runtime or lose a Terminal Event;
- after cancellation, resources are released within an observable bound and
  state enters a valid terminal or waiting state.

## R8: Protocol and Cross-Host Behavior

**Audit scope**

- schemas for Operation, Event, Receipt, Problem/Fault, and Terminal Data;
- duplicate, missing, reordered, unknown, and version-skewed events;
- interpretation differences among Go `eventview`, CLI/TUI, ACP, and the VS
  Code Projector;
- Hosts that infer completion, failure, approval, or recovery.

**Unified direction**

- keep Event Traits and generated schemas as the only event-semantics source;
- let Hosts project structured Runtime facts only;
- drive every Host conformance test from the same recorded event sequence;
- use repository generation commands for protocol changes.

**Completion evidence**

- the same event sequence produces equivalent state in every Host;
- a new Event without Traits or projection handling fails the build;
- Replay, Reconnect, and Unknown Event behavior has an explicit contract.

## R9: Startup, Shutdown, Wiring, Configuration, and Environment

**Audit scope**

- rollback and resource closure when any `wire.NewExec` module fails;
- startup order for Journal, Provider, MCP, Sandbox, Scheduler, and Host
  processes;
- duplicated or conflicting defaults across CLI, ACP, VS Code, and Workflow;
- differences across macOS, Linux, Windows, CI, Remote SSH, and isolated
  worktrees.

**Unified direction**

- retain one Composition Root and a reverse-order Resource Stack;
- give configuration one schema, precedence order, provenance record, and
  runtime snapshot;
- start background services only after Runtime recovery completes;
- represent missing capabilities through probes and typed Unavailable results.

**Completion evidence**

- failure at every construction step leaks no previously created resources;
- every Host resolves the same configuration to the same TurnSpec;
- platform tests do not rely on silent degradation.

## R10: Observability and Failure Reconstruction

**Audit scope**

- correlation among Session, Thread, Turn, Operation, Effect, Attempt, Lease,
  and Resume;
- state transitions, Retry decisions, budget changes, recovery sources, and
  terminal reasons;
- secrets or oversized payloads in logs, Traces, Receipts, and diagnostics;
- metrics that distinguish user cancellation, policy denial, resource
  exhaustion, and internal defects.

**Unified direction**

- propagate stable correlation IDs across every asynchronous boundary;
- record decision inputs and structured outcomes instead of relying on prose
  logs to reconstruct state;
- record only summaries, digests, and governed references for sensitive or
  large content;
- provide diagnostic output designed for failure reconstruction.

**Completion evidence**

- any failed Turn can answer where it stopped, why, what was attempted, and
  whether it can continue;
- IDs correlate Retry, Resume, and repeated side effects;
- Secret Leak Tests and observation schema gates pass.

## R11: Fault Injection and Reliability Gates

**Audit scope**

- asynchronous boundaries around Provider, Tool, Approval, Input, Journal,
  SQLite, Outbox, Host, and Shutdown;
- disconnects, delays, duplicates, reordering, full disks, process crashes,
  permission changes, and cancellation races;
- gaps in current Unit, Contract, Integration, Race, Fuzz, and endurance tests.

**Unified direction**

- maintain a boundary-by-fault-by-expected-state-by-recovery matrix;
- prefer deterministic fakes, fixtures, and crash points over timing-sensitive
  tests;
- turn core invariants into property tests and Architecture/Reliability gates;
- reproduce each new production failure in a fixture before repairing the
  shared root cause.

**Completion evidence**

- every P0 boundary covers success, retryable failure, permanent failure,
  cancellation, and crash recovery;
- fault tests prove no repeated side effects, missing terminal states, or
  permanent Running state;
- reliability gates are part of standard, reproducible validation.

## Recommended Execution Order

To avoid several local repairs changing the same semantics concurrently, only
one primary workstream should normally be `repairing`:

1. **Establish the baseline:** R0;
2. **Stabilize kernel semantics:** R1, R2, R4;
3. **Repair execution and recovery boundaries:** R3, R5, R6;
4. **Unify surrounding lifecycles:** R7, R8, R9;
5. **Complete evidence and continuous gates:** R10, R11.

R11 does not need to wait until the end. Every P0 repair should add its fault
injection case immediately; the final step integrates those cases into the
standard gate.

## Work Item Template

Give every finding a stable ID such as `R3-001` and record it under the owning
workstream:

```text
ID:
Status:
Symptom and reproduction:
Violated invariant:
Root cause and true owner:
Affected entries:
Unified repair:
Compatibility and migration impact:
Tests and fault injection:
Documentation impact:
Completion evidence:
```

An item becomes `verified` only when all of these conditions hold:

- the root cause is repaired at the correct ownership boundary without a Host
  or caller workaround;
- normal, failure, cancellation, and recovery paths have tests;
- no new implicit hard termination limit is introduced;
- no security governance layer is bypassed;
- English and Chinese documentation are updated together;
- focused tests, `git diff --check`, and applicable repository gates pass.

## Maintenance Rules

- every status change includes links to code, tests, runtime records, or fault
  injection evidence;
- one incident may affect several workstreams but has only one root-cause owner;
- changes to Protocol, persisted formats, or security semantics require design
  and compatibility impact first;
- a regression reopens the original ID instead of creating a hidden duplicate;
- Roadmap describes desired outcomes, this document tracks governance progress,
  and Architecture describes shipped facts. Do not use them interchangeably.
