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
| R0 | Failure baseline and repository-wide limit inventory | P0 | verified | Runtime / Engineering |
| R1 | Turn state machine and terminal convergence | P0 | verified | `internal/runtime/agent` |
| R2 | Dynamic budgets, progress detection, and Context | P0 | verified | Agent / Context / Config |
| R3 | Provider streams and incomplete-call recovery | P0 | unassessed | Provider / Agent |
| R4 | Typed faults, Retry, and Deadline semantics | P0 | repairing | Protocol / Runtime / Adapters |
| R5 | Persistence, Journal, idempotency, and crash recovery | P0 | repairing | Persist / Runtime |
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

### R0 Baseline Result (2026-08-18)

`verified` means that the audit and root-cause consolidation are complete. It
does not mean that the findings below have been repaired. This baseline targets
`main@9b12c5a`; implementation work tracks the stable IDs below.

| Item | Baseline |
| --- | --- |
| Repository source files | 1,279: 1,104 Go, 146 TypeScript, 20 Python, 9 Shell |
| Focused production code | 601 Go and 87 VS Code TypeScript files |
| Concurrency candidates | 68 Goroutine starts and 138 Channel declarations or constructors |
| Persistence candidates | 167 Transaction/Commit/Rollback related locations |
| External side-effect candidates | 308 file, process, network, or system-call locations |
| Non-test exits | 27 `panic`, 10 `os.Exit`, and 0 `log.Fatal` calls |
| Existing reliability tests | 259 recovery/cancel/retry-like tests by name and 29 explicit injection points |

These are static candidate boundaries, not defect counts. Sixteen `panic` calls
are the goja mechanism for raising JavaScript exceptions. `os.Exit` appears only
in process entry points, isolation helpers, and the Schema Generator. They stay
in the classification inventory but should not be removed mechanically.

### Limit-Purpose Inventory

| Domain | Current rule | Class | R0 decision | Follow-up |
| --- | --- | --- | --- | --- |
| Main Turn | 256 Steps by default and 1,000 maximum in Profile; then request one Finalization | Implicit convergence limit | Do not converge progressing work by count alone; use an explicit budget or renewable Lease | R1, R2 |
| No-progress detection | Normal Turns stage at 16/32/48 Samples; Research at 8/12/16 | Kernel convergence policy | Keep structured state, but derive thresholds from progress semantics and an observable policy | R1, R2 |
| Repair | Completion/Workspace/Declaration/Verification default to 2/1/2/1 Steps | Repair budget | New progress must reset failure accounting; exhaustion must retain resumable state | R1, R2 |
| Provider | Two-minute request Timeout and one-minute Idle Timeout | Fixed Deadline | `http.Client.Timeout` can terminate an actively streaming request; split connection, idle, and renewable lease scopes | R2, R4 |
| Provider Retry | Configuration defaults to zero, but a typed retryable Fault gets at least one Retry and Empty Response gets one | Implicit retry count | Choose Retry from Fault, idempotency, progress, and total budget instead of an adapter-local count | R3, R4 |
| Subagent | 24 Steps and five-minute Wall Time by default; expiry cancels and records Errored | Fixed terminator | Replace with a renewable Lease under the Parent budget and a resumable result | R2, R7 |
| Workflow / JS VM | 256 Steps by default; Lifetime 1,000, Parallel Items 1,000, concurrency 16 | Mixed budget | Keep capacity protection; Step/Lifetime exhaustion should suspend durably rather than cancel the entire Run | R2, R7 |
| Worker | One Attempt, 30-second Lease, and one-second Claim Interval by default | Durable attempt budget | Explicit task budgets can remain, but defaults, Retry, and Lease need one typed reason | R4, R7 |
| VS Code Supervisor | Restart after 250/500/1,000ms and enter Failed after three attempts | Host-local retry count | A Host count must not declare Runtime work permanently failed; expose durable, actionable recovery | R8, R9 |
| Runtime / Control Queue | Operation, Subscriber, and Turn Mailbox default to 64 | Backpressure capacity | Keep capacity, but critical control cannot return only `resource_exhausted` and make callers guess about replay | R7, R8 |
| Replay / Frame | ACP Replay 256, Frame 4 MiB, and paginated History | Transport capacity | Keep pagination and safety limits; all Hosts need one Continuation/Desync contract | R8 |
| Tool / Process Output | Shell defaults to 4,096 and caps at 10,000 Tokens; Process retains 1/8 MiB and supports Archive | Result-retention capacity | Never terminate a task for this; truncation needs a Receipt, Handle, or Durable Archive | R2, R6 |
| MCP / Web / Hook / Git | Fixed Call, Connect, Close, and Shutdown Deadlines from 250ms to two minutes | Operation Deadline | Separate connection, idle, call, and close scopes; Cleanup Timeout cannot become business failure | R4, R7, R9 |
| Context / Payload / Security | Fragment, file, Schema, Frame, Manifest, and Sandbox path-count limits | Safety or memory boundary | Retain and register centrally; return typed capacity results without silently truncating critical state | R2, R6, R8 |

This program is not removing real model Context/Output capabilities, explicit
user Cost/Token budgets, protocol pagination, security payload limits, Sandbox
path limits, or presentation retention limits that provide both a truncation
Receipt and complete Archive. The defects are implicit termination, silent
loss, and several owners deciding the same budget independently.

### Root-Cause Inventory

| ID | Priority | Root cause and evidence | Primary workstream |
| --- | --- | --- | --- |
| R0-001 | P0 | Main Turn, Subagent, Workflow, Verification, and JS VM each own fixed Step, Wall-time, or Lifetime terminators. The Kernel converts the Main Turn Step Limit into Convergence, but it can still converge work that continues to make progress. | R1, R2 |
| R0-002 | P0 | Provider total request Timeout, Subagent Wall Time, and several 30-second Tool limits conflate connection, idle, business operation, and execution Lease. In particular, `internal/runtime/app/wire/modules_provider.go` applies the two-minute setting to `http.Client.Timeout`, which active stream progress cannot renew. | R2, R4 |
| R0-003 | P0 | Provider Retry, Worker Attempt, Workflow Retry, and VS Code Runtime Restart use independent local counts. The Provider currently guarantees at most one default Retry, while VS Code permanently enters `failed` after three failed launches. | R3, R4, R9 |
| R0-004 | P0 | Critical errors are discarded from Terminal Event `send`, Turn Coordinator `Release`, in-memory rollback after failed Checkpoint publication, Child `Settle`, Lane status persistence, Process Session Journal, and asynchronous Observation writes. These can produce completed-but-unrecorded state, retained Leases, or permanent Running. | R1, R5, R7, R10 |
| R0-005 | P1 | A shared `Problem/Fault` exists, but Metadata lacks Stage and Operation/Effect/Attempt IDs; unclassified errors default to recoverable `unavailable`. Egress, VS Code revocation, and TUI projection still depend on error text. | R4, R8, R10 |
| R0-006 | P1 | Eleven non-goja Runtime `panic` calls include random ID generation, a Process Output invariant, invalid Permission Kind, and static Catalog/Manifest loading. Static Must helpers may remain at a build-fact boundary; runtime input and entropy failures must return typed Faults. | R4, R9 |
| R0-007 | P1 | Queue, Replay, Frame, Output, Context, and Payload limits are distributed across Config, Protocol, Host, and Adapter. Trigger behavior varies among Reject, Truncate, Drop, Desync, Converge, and Panic without one capacity contract. | R2, R6, R7, R8 |
| R0-008 | P1 | `max_steps` is duplicated in Go Defaults, Engine fallback, Session Profile, ACP, VS Code Settings, and Package Schema; Timeout and Retry have similar duplicate defaults. A Host can still alter Runtime termination policy. | R8, R9 |
| R0-009 | P1 | Strong tests cover Terminal Atomicity, Outbox, Lease, Provider Disconnect, and Tool Cancel, but no matrix spans every asynchronous boundary. Gaps include progressing Deadline, discarded Terminal/Release errors, Disk Full, Observation write failure, and Host restart exhaustion. | R5, R7, R10, R11 |

### Exit and Error-Handling Conclusions

- process-level `os.Exit` calls are at valid process boundaries and remain;
- goja Host Function `panic(runtime.NewGoError(...))` calls are recovered at the
  VM boundary and may remain, but `fmt.Errorf("%v", recovered)` currently loses
  the typed cause chain and belongs to R4;
- static Must helpers such as `DefaultCatalog`, `MustLoad`, and `MustReadiness`
  may handle only compile-time embedded facts backed by tests; they must not
  expand to user input or external runtime state;
- random ID, Permission mapping, and Process Output runtime panics should become
  typed errors converged by the correct owner;
- best-effort cleanup Close/Remove failures may be secondary, but must enter
  Secondary Issue or Health. Errors changing Durable State, Lease, Terminal, or
  Result cannot be ignored.

### Async and Fault-Injection Baseline

Current tests cover Event Log torn tails, Domain Fact/Terminal Commit failure,
Terminal Outbox recovery, Journal Draft recovery, Lease fencing, Provider SSE
disconnect, Tool cancellation, Approval/Input recovery, and VS Code cursor
replay. The Durable Kernel therefore has a strong base, but coverage clusters
around a few boundaries rather than a boundary-by-fault-by-state-by-recovery
matrix.

`go test -count=1 ./...` produced three timing-sensitive failures during this
audit:

- the MCP Stdio fixture exceeded its five-second initialization Context;
- two Lane tests remained `running` through their five-second polling windows.

Each focused test passed with `-count=5`, so current evidence indicates
fixed-Deadline flakes under parallel package load rather than stable functional
failures. The repository's standard serial `make test-hermetic` lane passed in
full; `npm run check` passed; `npm test -- runtime` reported 54 passes and four
skipped real-Runtime integration scenarios. R11 should convert those skipped
and timing-sensitive cases into deterministic gates.

### R0 Handoff Order

1. R1/R2 unify Progress, Convergence, explicit Budget, and renewable Execution
   Lease;
2. R4 defines complete Fault/Deadline/Retry decisions, then R3 integrates
   Provider Partial Stream behavior;
3. R5 repairs every discarded Durable/Terminal/Lease error;
4. R7/R8/R9 unify Queue, Host restart, configuration sources, and resource
   lifecycle;
5. R10/R11 establish failure reconstruction and the full-boundary fault matrix.

## R1: Turn State Machine and Terminal Convergence

**Current progress (2026-08-18)**

- explicit Main Turn Step Budget is frozen into Turn Kernel Policy and the
  Reducer triggers Convergence from completed Samples; the Engine loop no longer
  owns a fixed-step terminator;
- Main Turn, Subagent, and Workflow `max_steps` now default to zero and install
  no implicit limit;
- Kernel-authorized Repair Steps retain an independent consecutive-no-progress
  budget and are not consumed by the normal-work budget;
- explicit Workflow budget exhaustion now produces durable `blocked` nodes that
  can Resume instead of canceling the entire Run;
- Terminal Projection failure returns a recoverable Fault; Turn Coordinator
  Release failure enters `secondary_issues` and Durable Runtime retries it
  continuously. Child WorkGraph/Manager Settlement errors are no longer
  discarded;
- the executable state graph in `state_graph_property_test.go` covers all eleven
  Kernel phases, all three terminal kinds, illegal transitions, duplicate and
  late Commands, and order equivalence for independent Tool and Approval
  results. A rejected Command must preserve the original State Digest;
- Approval and Input waits are rebuilt through real durable Domain Facts. The
  restored Coordinator preserves both the State Digest and Request ID.

**Acceptance result (2026-08-18)**

- state graph, single-terminal, Command ordering, and cross-process interaction
  recovery tests pass;
- focused Turn Kernel, Agent Engine, Workflow, Protocol, and Wire tests pass;
- Hermetic, Race, Architecture Ratchet, VS Code, Docs, and Book gates pass.

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

**Current progress (2026-08-18)**

- `zero = unset` now spans Config, Session Profile, CLI, ACP/VS Code decoding,
  and Engine. Historical default Profiles at `8/64/256` migrate to zero while
  explicit user budgets remain unchanged;
- a changing Progress Signature has no total-Sample limit. Durable Kernel
  no-progress stages at 8/12/16 or 16/32/48 remain active;
- Subagent `wall_time` is now a renewable execution Lease rather than an
  absolute wall-clock terminator. Runtime progress renews it and idle expiry
  produces recoverable `interrupted` state;
- Workflow no longer injects 256 Steps by default, and explicit Step/Token/Cost
  exhaustion produces `blocked`;
- Provider no longer uses a total-wall-clock `http.Client.Timeout`: connection,
  TLS, and response headers have phase limits, stream events renew Idle Timeout,
  and Turn Context/Lease owns call lifetime;
- Context candidates verify an Authority Capsule before commit. Goal, Plan/Todo,
  Failure, Change, Critical Path, and Evidence Fact/Handle entities must remain
  equal, while Tool Pairs remain closed. Receipts and Protocol Events carry the
  Authority Digest and equivalence result;
- Protocol provides one `BudgetExhaustion` contract. Explicit Main Turn,
  Workflow, and Child Token/Cost exhaustion carries Scope, resource kind,
  Used/Limit, `resource_exhausted`, and `resume_turn`; execution does not
  automatically retry before the budget changes;
- every Workflow budget-exhaustion branch enters durable `blocked`, while a
  background Workflow Task projects `waiting` rather than `failed`. Increasing
  the budget resumes from WorkGraph state without replaying completed nodes;
- Session Delta persists the binding between Turn identity and model-visible
  history groups. Continue removes only the source group replaced by the
  Recovery Capsule, preserving older facts and closed Tool Pairs. Repeated
  Continue and process restart no longer recursively inject old Recovery
  Prompts.

**Acceptance result (2026-08-18)**

- Main, Workflow, and Child Token and Cost exhaustion share one recoverable
  Fault contract;
- recovery-history identity, repeated Continue, Session Delta restart, and
  post-compaction binding cleanup tests pass;
- progressing work, deterministic no-progress convergence, Context Authority
  equivalence, and Tool Pair closure tests pass;
- Hermetic, Race, Architecture Ratchet, VS Code, Docs, and Book gates pass.

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

**Current progress (2026-08-18)**

- Provider Deadline is split across Connection, TLS Handshake, Response Header,
  and Stream Idle phases; a progressing stream has no fixed total-wall-clock
  limit;
- Terminal Projection failure returns a `RetryStep` Fault and preserves
  recoverable state. Awaiting Recovery projection failure is joined with the
  primary Fault;
- one Retry owner and complete Deadline Metadata across Provider, Worker,
  Workflow, and Host remain.

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

**Current progress (2026-08-18)**

- Turn Coordinator Release runs before terminal publication and failure is
  recorded as a Terminal Secondary Issue. Durable Runtime continuously retries
  failed releases without renewing their Leases and removes the in-memory
  Coordinator only after success;
- Coordinator Open/Restore rollback errors are joined with the primary error;
- Child WorkGraph and Manager Settlement use idempotent retry without a fixed
  attempt count. New Child Turns return typed `unavailable` while recovery is
  pending instead of deleting Settlement errors;
- discarded Checkpoint rollback, Observation writes, and Process Journal errors
  from the rest of R0-004 remain.

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
