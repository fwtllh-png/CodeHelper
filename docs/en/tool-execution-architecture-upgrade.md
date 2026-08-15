# Tool and Local Execution Architecture Upgrade

[Simplified Chinese](../zh-CN/tool-execution-architecture-upgrade.md) | English

> Status: EX1 `accepted`; EX0 `baseline_frozen`.
>
> Baseline:
> [`tool-execution-ex0-baseline.json`](../tool-execution-ex0-baseline.json).
> EX1 evidence:
> [`tool-execution-ex1-evidence.json`](../tool-execution-ex1-evidence.json).
>
> Scope: tool identity, invocation, result projection, Guard orchestration,
> resource scheduling, local process execution, cancellation, persistent
> terminal sessions, output admission, observability, and migration gates.
>
> Reference implementation reviewed: Codex commit
> `3bbf1fe75701c97fb190e0867002ba2d9dbda5db`.

## 1. Executive Summary

CodeHelper already has strong execution governance:

- sampled Catalog bindings prevent stale or replaced tools from executing;
- Descriptor resource resolvers derive typed read and write claims;
- Guard applies policy, approval, hooks, resource Claims, journal, diagnostics,
  sandbox escalation, and network approval;
- exact edit plans bind a displayed change to its approval;
- ResultStore retains large results behind stable Content Store handles; and
- SessionManager has bounded live buffers, durable output archives, process
  groups, and Thread leases.

The problem is not missing security features. Execution semantics are spread
across Engine, Guard, Registry, individual tools, and ProcessManager. The
current process surface exposes 15 model-visible tools with 3,179 bytes of
input schema. All 15 are marked serial. Foreground process output is fully
accumulated before ResultStore admission, approvals wait while the Engine
parallel-policy gate is held, and terminal/background lifecycle operations use
multiple overlapping protocols.

This upgrade keeps the existing security and persistence foundations while
introducing one local execution flow:

```text
Tool Effect
    |
    v
Execution Coordinator
    |
    v
Guard Pipeline
 prepare -> authorize -> approve -> claim -> attempt -> commit
    |                                      |
    |                                      v
    |                              Local Process Runtime
    v
Typed Outcome -> Model / Host / Hook / Telemetry / Content Store projections
```

No remote executor or exec-server capability is introduced.

## 2. Goals

The upgrade must:

1. bound foreground process memory during collection, not after completion;
2. reduce the model-visible local process protocol to at most three tools;
3. separate approval waiting from scarce execution admission;
4. retain resource-level concurrency through the existing Claims model;
5. replace stringly typed execution metadata with typed invocation and outcome
   contracts;
6. give cancellation, teardown, detached work, and terminal publication
   explicit semantics;
7. enforce Session ownership on every interaction;
8. use event-driven process waiting instead of polling;
9. preserve Catalog authority, policy, approval, constitution, journal,
   sandbox, egress, diagnostics, and Content Store boundaries;
10. keep one business loop in `internal/runtime/agent` and construction in
    `internal/runtime/app/wire`; and
11. maintain or improve CE7 input-token and prompt-cache metrics.

## 3. Non-Goals

This upgrade does not:

- add remote execution, an exec server, containers, or environment discovery;
- move tool execution into CLI, TUI, VS Code, or ACP;
- replace the Turn Kernel, durable effects, or Runtime protocol;
- weaken the strong sandbox or permit an unguarded fallback;
- replace resource Claims with a Boolean parallel flag;
- remove ResultStore handles or durable Job Log archives;
- introduce a public tool SDK;
- make every tool a plugin;
- preserve unreleased duplicate process protocols indefinitely; or
- accept line-count growth or reduction as a substitute for architectural
  correctness.

## 4. Current Architecture

### 4.1 Tool Catalog

`tool.Registry` owns canonical names, aliases, source identity, revisions,
authority tokens, deferred materialization, revocation, schema validation, and
result admission. A sampled `CatalogBinding` is checked again immediately
before execution.

This is stronger than a simple name-to-handler map and remains authoritative.
The upgrade may add a structured `ToolRef`, but it must preserve source and
revision binding.

### 4.2 Guard

`guard.Guard.ExecuteBound` currently owns:

1. argument preparation and resource resolution;
2. policy and permission-hook evaluation;
3. approval recovery and replacement arguments;
4. edit-plan generation and revalidation;
5. resource Claims;
6. pre/post hooks;
7. read-before-write and journal handling;
8. sandbox attempt selection and escalation;
9. egress approval and retry;
10. diagnostics and change receipts; and
11. final result return.

The behavior is correct in important cases, but the single control flow makes
attempt policy, lifecycle state, and cleanup difficult to test independently.

### 4.3 Engine Scheduling

Engine starts one goroutine per proposed call and admits it through
`ToolScheduler`. Serial calls take an exclusive lock; concurrent calls take a
shared lock and a bounded slot. The Guard acquires typed Claims later.

Because scheduler admission happens before Guard approval, a serial call can
hold the exclusive lock while waiting for a person. The Boolean gate also
serializes tools whose concrete resources do not overlap.

### 4.4 Local Process Surface

The model currently sees:

- `shell_read`, `shell_run`, and `terminal_run`;
- six `terminal_*` lifecycle tools;
- four `background_shell_*` lifecycle tools; and
- two `task_shell_*` aliases registered as separate tools.

This duplicates start, poll, input, resize, signal, and close semantics.

### 4.5 Output and Sessions

Foreground `process.Run` stores stdout and stderr in unbounded `bytes.Buffer`
values. Engine streaming is capped, but that cap only limits Host events.
ResultStore admission occurs after the command has already completed and the
full output is resident.

Persistent sessions have a bounded live buffer and durable archive. Their
`Wait` method polls every ten milliseconds. Thread ownership is recorded at
creation and used for cleanup, but interaction methods do not verify the
caller's Thread.

## 5. Foundations to Retain

The following CodeHelper designs remain:

| Foundation | Required property |
| --- | --- |
| Catalog binding | sampled name, source, revision, and authority stay frozen |
| ResourceResolver | policy resources derive from normalized arguments |
| Claims | conflicting resources serialize; independent resources may overlap |
| Guard | every consequential tool uses the same guarded entry |
| EditPlan | approval binds to a fresh, revalidated plan |
| Journal | before-image and commit receipt surround persistent writes |
| Sandbox | strong execution fails closed; escalation requires approval |
| ResultStore | complete results remain retrievable through stable handles |
| Job Log | detached process output survives the live-buffer horizon |
| Turn Kernel | tool start and terminal closure remain durable facts |

Codex's global parallel Boolean and process-store-only output retention are not
copied because CodeHelper already has more precise resource and persistence
models.

## 6. Target Contracts

### 6.1 Tool Reference

The execution identity becomes structured:

```go
type ToolRef struct {
    Name       string
    Source     string
    CatalogID  string
    Generation uint64
    Revision   uint64
    Authority  uint64
}
```

Model wire formats may remain flat where required. Flattening happens only at
the provider boundary; Registry and Guard use `ToolRef`.

### 6.2 Invocation

One immutable invocation carries:

```go
type Invocation struct {
    Identity  InvocationIdentity
    Tool      ToolRef
    Arguments json.RawMessage
    Descriptor Descriptor
    Resources []Resource
    Source    InvocationSource
}
```

Argument normalization and resource resolution occur once. Replacement
arguments produce a new prepared invocation and repeat authorization.

### 6.3 Typed Runtime

Tools progressively adopt:

```go
type Runtime[I, O any] interface {
    Descriptor() Descriptor
    Run(context.Context, I) (O, error)
}
```

The existing `typed` package becomes the default adapter. Raw executors remain
only for protocol-native dynamic integrations that require them.

### 6.4 Outcome

Tool output is one typed value with separate projections:

```go
type Outcome interface {
    ModelProjection() Result
    HostProjection() HostOutput
    HookProjection() (HookOutput, bool)
    TelemetryProjection() TelemetryOutput
    Success() bool
}
```

Security decisions and Engine control flow no longer depend on arbitrary
metadata keys. Compatibility metadata is derived at the protocol edge.

### 6.5 Execution Disposition

Cancellation behavior is declared:

```text
abort_immediately  handler may be abandoned after cancellation
wait_for_teardown  cancellation waits for bounded cleanup
detached           process survives Turn cancellation under a Session lease
```

An atomic terminal owner guarantees exactly one Completed, Failed, Canceled,
Rejected, or Aborted outcome for each Call ID.

## 7. Guard Pipeline

Guard remains the sole consequential execution gateway but is decomposed into
explicit phases:

```text
Prepare
  normalize -> validate schema -> expand -> resolve resources -> bind catalog

Authorize
  repository policy -> constitution -> permission hook -> approval cache

Approve
  edit preview / replacement / escalation / network request and recovery

Admit
  global execution budget -> typed resource Claims

Attempt
  sandbox plan -> local runtime -> bounded stream -> classified result

Commit
  journal -> read/write fingerprints -> diagnostics -> change receipts

Project
  Content Store admission -> typed projections -> durable terminal result
```

Approval waits hold neither global execution slots nor Claims. A fresh policy
check occurs before admission when mutable approval or resource state changed.

Sandbox and network retries are represented as typed Attempt records. A retry
contains reason, sandbox posture, approved grant, start/end timestamps, and
terminal classification. No retry silently changes permissions.

## 8. Scheduling Model

The final scheduler has two layers:

1. a fair bounded execution budget limits active handlers;
2. typed Claims serialize only overlapping resources.

`ParallelPolicy` remains temporarily for effects without derivable resources.
It no longer globally serializes every process tool. A call waiting for
approval, user input, or a future start time is not an active execution.

Required scheduler properties:

- cancellation removes a queued waiter without helper-goroutine leakage;
- writers are not starved by a continuous stream of readers;
- one Call holds each release exactly once;
- detached processes release launch Claims after spawn and acquire
  Session-specific Claims for later interactions; and
- queue and handler latency are measured separately.

## 9. Unified Local Process Protocol

The target model-visible surface is:

### `exec_command`

Starts a local command with:

- command and working directory;
- TTY selection;
- bounded initial yield;
- output-token budget;
- timeout;
- declared exact write paths where allowed; and
- explicit sandbox escalation request and justification.

It returns either a terminal result or a Session ID for continued interaction.

### `write_stdin`

Continues an existing Session:

- empty input polls for output;
- non-empty input writes stdin;
- optional rows and columns resize the TTY;
- optional signal supports interrupt, terminate, and kill; and
- close reclaims the process group.

Every operation validates Session and Thread ownership.

### `shell_read`

This may remain as a third optimized read-only tool if measurements show that
its stronger default isolation and smaller schema improve safety or model
selection. Otherwise it becomes an internal preset of `exec_command`.

Legacy process tools are removed within the migration stage. They are not kept
as a second shipped execution path.

## 10. Output Architecture

Collection and model admission are separate:

```text
process bytes
   +-> bounded live stream -> Host events
   +-> bounded head/tail collector -> immediate result
   +-> durable archive / Content Store -> complete retrieval
```

The collector records total bytes and omitted middle bytes. Memory use is
bounded independently for stdout and stderr or for the merged PTY stream.
ResultStore still applies token-native admission and returns a stable retrieval
handle. A command producing gigabytes must not produce gigabytes of resident
Go memory or one giant protocol event.

## 11. Session Lifecycle

SessionManager becomes event-driven and lease-aware:

- output append, exit, failure, and close signal waiters through a generation
  channel or condition;
- every read, write, resize, signal, wait, and close receives the caller
  identity and verifies the owning Session and Thread;
- detached work has explicit Session, Thread, Turn, and originating Call
  provenance;
- completion and pruning do not race with interaction or terminal
  publication;
- bounded live output and durable archive cursors retain current semantics; and
- Runtime shutdown and Thread release reclaim owned process groups.

## 12. Observability

Each tool call records:

- dispatch wait, approval wait, claim wait, handler, teardown, and total time;
- catalog identity and invocation source;
- attempt count and sandbox/network decision source;
- collected, streamed, retained, omitted, and model-projected bytes/tokens;
- terminal owner and cancellation disposition;
- resource claims and conflict wait reason; and
- Session creation, interaction, completion, and cleanup.

Host output remains a projection. Durable execution receipts are the source of
truth.

## 13. Migration Stages

### EX0: Characterization Baseline

Status: `baseline_frozen`.

Work:

- add the deterministic `toolexecbaseline` generator;
- freeze process-tool count, input-schema bytes, serial-tool count, names,
  safety contracts, risks, and hotspot trends;
- add monotonic validation for tool count and schema bytes;
- add `make tool-execution-ex0`; and
- document the complete target architecture and stage gates.

No production behavior changes.

Exit:

- current process surface and known risks are reproducible;
- Catalog authority, Claims, Result handles, and typed adapter availability are
  verified;
- focused Tool, Process, and Engine tests pass; and
- Architecture Ratchet passes.

### EX1: Bounded Output Collection

Status: `accepted`.

Work:

- introduce a shared bounded head/tail collector;
- route foreground stdout, stderr, and PTY through it;
- preserve complete output through the durable store when configured;
- expose omitted-byte and total-byte receipts; and
- add high-volume and cancellation tests without allocating proportional
  memory.

Exit:

- a 1 GiB synthetic stream has bounded retained memory;
- small command output is byte-for-byte compatible;
- stream cursors remain monotonic; and
- `foreground_output_bounded` becomes true.

Delivered:

- `process.Run` retains at most 8 MiB per stdout/stderr stream by default;
- model-facing Shell execution retains at most 1 MiB per stream before
  ResultStore token admission;
- a shared head/tail collector preserves exact small output and reports total,
  retained, and omitted bytes;
- stdout, stderr, and merged PTY output use the same bounded collector;
- an optional synchronous Archive Sink receives every complete chunk with
  bounded-memory backpressure and reports degraded archival without losing the
  bounded command result; and
- a 1 GiB synthetic-stream test proves collector capacity remains 1 MiB.

### EX2: Typed Execution Core and Guard Pipeline

Work:

- add structured ToolRef, prepared Invocation, Outcome, Attempt, and
  disposition types;
- make `typed.Define` the default built-in tool path;
- split Guard into phase-owned components without adding a bypass;
- move approval waiting before execution admission; and
- emit typed execution receipts.

Exit:

- no security decision reads arbitrary result metadata;
- replacement arguments re-run preparation and authorization;
- approval waits consume no execution slot or Claim;
- existing approval and journal tests remain equivalent; and
- Guard remains the only consequential entry.

### EX3: Unified Process Protocol

Work:

- implement `exec_command` and `write_stdin`;
- decide `shell_read` using measured safety and token evidence;
- migrate Host presentation and recovery;
- enforce Session ownership on every operation;
- replace polling waits with event notifications; and
- delete displaced model-visible process tools before stage completion.

Exit:

- model-visible process tools are at most three;
- input-schema bytes are at least 60% below EX0;
- no duplicate process execution path remains;
- Session ownership denial and cleanup tests pass; and
- `unified_process_protocol`, `session_owner_enforced`, and
  `event_driven_session_wait` become true.

### EX4: Resource Scheduler and Cancellation

Work:

- replace the global serial RW gate with fair budget admission plus Claims;
- add explicit cancellation dispositions and terminal ownership;
- make teardown bounded and observable;
- protect detached process interaction with Session Claims; and
- add deterministic race and starvation tests.

Exit:

- unrelated resources execute concurrently;
- approval waiting cannot block unrelated execution;
- cancellation P95 is below two seconds in the hermetic suite;
- every Call has exactly one terminal outcome; and
- no orphan process remains after Session or Thread close.

### EX5: Convergence and Cleanup

Work:

- migrate remaining built-ins to typed outcomes;
- remove compatibility metadata read by Engine business logic;
- delete legacy adapters and stale tests;
- tighten architecture and schema baselines;
- update English and Chinese operational documentation; and
- run full hermetic, race, security, VS Code, docs, and token gates.

Exit:

- one local execution path remains;
- Input Token P50 has zero regression from CE7;
- prompt-cache continuity has zero regression;
- security coverage remains 100%;
- Architecture Ratchet has no regression; and
- all versioned evidence is committed.

## 14. Acceptance Gates

| Area | Gate |
| --- | --- |
| Tool surface | at most 3 model-visible process tools |
| Schema | at least 60% below the EX0 3,179-byte baseline |
| Memory | retained foreground output has a fixed configured upper bound |
| Concurrency | disjoint Claims overlap; conflicting Claims serialize |
| Approval | wait time does not consume execution admission |
| Cancellation | P95 below 2 seconds; exactly one terminal outcome |
| Cleanup | zero orphan processes after Session/Thread shutdown |
| Ownership | every Session operation rejects a foreign Thread |
| Security | no Guard, policy, approval, journal, or sandbox bypass |
| Tokens | Input Token P50 does not regress from CE7 |
| Architecture | Ratchet, docs, race, and focused security gates pass |

## 15. Validation

Every stage runs its focused packages first, then:

```bash
make tool-execution-ex0
go test -race ./internal/adapter/tool/... ./internal/platform/process \
  ./internal/runtime/agent/engine
make docs-check
make book-check
git diff --check
```

Stages that change Host projections also run the VS Code protocol, check, and
relevant tests. Token-affecting stages run the existing CE7 comparison lane.

## 16. Rollback and Failure Policy

- A stage does not merge with both old and new production execution paths.
- A failed sandbox or ownership check fails closed.
- A failed output archive retains bounded live output and reports degraded
  recoverability; it does not silently claim complete retention.
- A failed terminal publication remains recoverable through the existing Turn
  effect and outbox mechanisms.
- Baseline reductions tighten future limits; regressions require an explicit
  architecture decision, not a baseline overwrite.

## 17. Ownership

| Concern | Owner |
| --- | --- |
| Tool contracts and Registry | `internal/adapter/tool` |
| Guard phases and attempts | `internal/adapter/tool/guard` |
| Local process and Session primitives | `internal/platform/process` |
| Sandbox enforcement | `internal/security/sandbox` |
| Tool batch, scheduling, and terminal state | `internal/runtime/agent` |
| Construction | `internal/runtime/app/wire` |
| Durable output and receipts | `internal/persist`, `internal/observability` |
| Host presentation | `internal/runtime/eventview`, `extensions/vscode` |

These boundaries are mandatory throughout the upgrade.
