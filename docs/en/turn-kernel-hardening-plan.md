# Turn Kernel Convergence Plan

[简体中文](../zh-CN/turn-kernel-hardening-plan.md) | English

## 1. Document Role

This document is the only forward-looking plan for Turn Kernel convergence. It
replaces the accumulated Phase 4R/Phase 5 plan. Historical phase numbers,
completion claims, and D01-D11 string gates no longer represent current
completion.

Facts have this priority:

1. production call paths and persistence transactions;
2. behavior, fault-injection, race, and recovery tests;
3. the status and disposition inventory in this document;
4. historical implementation reports.

This document uses four states. The existence of a type must never again be
reported as a completed migration:

| State | Meaning |
| --- | --- |
| `Foundation` | A target-architecture primitive worth retaining; it does not prove production authority |
| `Bridge` | A dual-path migration adapter with a mandatory deletion stage |
| `Legacy` | The old implementation that still carries production behavior and cannot yet be deleted |
| `Missing` | Target behavior that is absent or not connected to production |

## 2. Current Conclusion

Turn Kernel convergence is not complete. The repository is a runnable but
authority-split intermediate state:

- `turnkernel.State`, `Reducer`, `TurnCoordinator`, Domain Facts, and the
  Terminal Envelope form a useful `Foundation`;
- `engineTurnKernel` and `MigrationEffectDispatcher` route Control and Tool
  Effects durably, while later-stage Effects and decisions remain a `Bridge`;
- `Engine.RunForTurnWithIntentAndAttachments` still decides the sampling loop,
  Repair, Completion, Verification, Journal, Output Release, and most terminal
  ordering, so it remains the effective `Legacy` control plane;
- App and Runtime still interpret terminal state, apply fallbacks, publish, and
  commit Operations;
- Model, Verification, Journal, and Terminal Effect takeover, atomic Operation
  Commit, and restart Outbox recovery remain `Missing`.

The following historical conclusions are withdrawn:

- "Phase 4R is complete";
- "D01-D11 green proves that TurnCoordinator is the only decision owner";
- "Terminal Envelope atomically commits Final Output, Terminal, and the real
  Operation state";
- "Domain Fact Restore is wired into production recovery".

The old string gates were deleted in C0. The durable no-replay constraint moved
into the formal Ownership Gate.

## 3. Actual Production Path

### 3.1 Turn execution

```text
Runtime StartTurn
  -> EngineAdapter
  -> Engine.RunForTurnWithIntentAndAttachments
       -> construct engineTurnKernel
            -> injected CoordinatorRuntime
            -> TurnCoordinator
            -> SQLite Domain Fact Store for persistent runtimes
            -> MigrationEffectDispatcher
                 -> routed Control/Tool/Model/Verification Effect registry
                 -> deferred C5 projection Effects
       -> Engine effect pump submits EvaluateTurnStep
       -> Reducer selects the next structured action
       -> Engine executes the selected Provider/Tool/Verification action
       -> Terminal Engine Events are still translated by observe
       -> terminalProjector projects the accepted terminal decision
  -> EngineAdapter freezes legacy Engine observations and builds Receipt
  -> Runtime Terminal Store commits Terminal Envelope
  -> Runtime publishes Receipt / Terminal
  -> Runtime commits the Operation separately
```

There are three control loops:

1. the procedural Engine business loop;
2. the `engineTurnKernel` adaptation and observation loop;
3. the App/Runtime terminal commit and fallback loop.

The target is not to keep these loops synchronized. It is to remove parallel
decision sources so the Coordinator drives the business loop and Runtime owns
only transactions and projection.

### 3.2 Current durability

- persistent wire injects one SQLite-backed Coordinator Runtime and Terminal
  Store;
- every accepted Coordinator transition appends Domain Facts to SQLite before
  state commit or Effect dispatch;
- startup claims active Turns with renewable leases and calls
  `RestoreTurnCoordinator`;
- running Effects are durably requeued through the Reducer before dispatch;
- `OperationCommitFact` is envelope payload and does not update the real
  Runtime Operation state in the same SQLite transaction;
- Runtime calls `r.commit(operation.ID)` separately after projection.

The current implementation provides in-flight Coordinator recovery, but not
real terminal Operation atomicity or restart Outbox projection.

## 4. Code Disposition

### 4.1 Foundation to retain and harden

| Path | Current value | Required work |
| --- | --- | --- |
| `turnkernel/state.go` | Canonical phases, State, and Effect ledger | Add facts by vertical slice; remove bridge-only fields |
| `turnkernel/command.go` | Structured inputs and Result Commands | Define executor result schemas and idempotent identities |
| `turnkernel/reducer.go` | Pure transitions and core invariants | Own Repair, terminal eligibility, and transaction state |
| `turnkernel/invariants.go` | State validation and digest basis | Add cross-Fact, terminal, and Effect closure validation |
| `TurnCoordinator` in `coordinator.go` | Serialized commands and persist-before-dispatch | Inject production Store and define restore, lease, and shutdown semantics |
| `turnkernel/terminal_envelope.go` | Envelope, Commit Marker, and Outbox primitives | Join the real Operation table and Event Outbox transaction boundary |
| `persist/state/turnstate` | SQLite Domain Fact and Terminal Store | Connect active Turns and provide startup recovery queries |

Retaining these files does not freeze their interfaces. They may be tightened
or rewritten, but State writes must never move back into Engine.

### 4.2 Misguided Bridge code to delete

These paths must be removed after their replacement lands:

| Bridge | Problem | Deletion stage |
| --- | --- | --- |
| `engineTurnKernel.observe` and all `observe*Locked` | Reverse-translates legacy Engine Events into Kernel Commands and downgrades errors to drift | Delete slice by slice in C2-C4; zero in C6 |
| `applyLocked`, `drifted`, and observer repair transitions | Let the old loop continue after an authoritative transition fails | Ban production use from C2; delete in C6 |
| `DeferredEffectDispatcher` `Claim/Complete/Forget` | Explicitly leaves execution under the old loop instead of Effects | Replace in C2-C4; delete in C6 |
| `engineTurnKernel.state` snapshot copy | Keeps a second queryable State beside Coordinator | Replace with read-only Snapshot API in C2; delete in C6 |
| `normalizeTerminalProjection` | Corrects a legacy terminal event instead of producing terminal state from Kernel | Delete in C4 |
| Coordinator Frozen Terminal State | Authoritative terminal state and Domain Facts for atomic commit | Foundation since C4 |

`TurnKernelObserver` and `TransitionRecord` may remain as diagnostics only.
They must never restore, repair, retry, or correct state.

### 4.3 Legacy code still carrying production behavior

Do not delete these paths until their behavior moves into the
Coordinator/Reducer/Executor model:

| Legacy path | Authority it still owns | Target owner |
| --- | --- | --- |
| Main loop in `engine/turn_handler.go` | Sampling, tool loop, Repair, Completion/Verification gates, Output Release | Coordinator + Reducer |
| `engine/terminal_handler.go` | Terminal deduplication, cancel/failure classification, fallback | Reducer Terminal Decision + Terminal Commit Effect |
| `verifyGate` and `completionVerification` | Verification timing, interpretation, and Repair action | Verification Executor returns facts; Reducer decides |
| `completionGateRequired` and Completion feedback branches | Completion requirement and next action | Frozen Policy + Reducer |
| Direct Engine `Journal.Commit/Rollback` | Journal terminal order | Journal Executor + Result Command |
| Terminal values in Engine `State` | A second Turn phase model | Kernel Phase; Engine Events become non-authoritative progress only |
| App `commitTerminal` fallback | Splits Receipt and Terminal for non-transactional sinks | Require TerminalCommitSink for production Turns |
| Runtime synthetic terminal fallback | Creates failure when Engine provides no terminal state | Coordinator emits explicit failure; Runtime rejects corrupt transactions |
| Separate Runtime `r.commit` | Commits Operation outside the envelope | Shared transaction or one Durable Commit Port |

### 4.4 Explicitly missing capabilities

1. `runtime/app/wire` injects per-Turn Coordinator dependencies; Engine does
   not create a Memory Store.
2. Every production transition appends Domain Facts to SQLite immediately.
3. Startup scans non-terminal Turns and calls production
   `RestoreTurnCoordinator`.
4. A real Effect Executor Registry executes Provider, Tool, Approval, Input,
   Verification, Journal, and Terminal Commit Effects.
5. Effect claims, attempts, results, and side-effect idempotency keys are
   durable.
6. Final Output is invisible before Terminal Commit.
7. Terminal Envelope, real Operation state, and Outbox commit in one
   transaction boundary.
8. Pending Outbox projection resumes automatically after restart.
9. No Engine Event is translated back into a Kernel Command.
10. Behavioral and ownership tests replace string-search completion gates.

## 5. Target Architecture

```text
Host Operation
      |
      v
Runtime Command Port
      |
      v
TurnCoordinator ---- append Domain Facts ----> Durable Turn Store
      |
      +---- dispatch durable Effect ---------> Effect Executor Registry
      |                                          |
      <----------- same effect_id Result Command-+
      |
      +---- Terminal Commit Effect ----------> Atomic Commit Port
                                                |
                                                +-- Frozen Kernel State
                                                +-- Domain Facts
                                                +-- Receipt
                                                +-- Final Output
                                                +-- Terminal Event
                                                +-- Operation State
                                                +-- Projection Outbox

Projection Worker <---- committed Outbox ---- Runtime Events ----> Hosts
```

### 5.1 Single-writer rules

- only `TurnCoordinator.Submit` may invoke Reducer and advance Kernel State;
- an Executor accepts an Effect and returns one Result Command with the same
  `effect_id`;
- Engine must not retain parallel Turn Phase, Completion, Verification,
  Mutation, Repair, Journal, or Terminal decision state;
- Runtime does not decide whether a Turn is Completed, Failed, or Canceled;
- UI and Hosts consume committed projections only.

### 5.2 Terminal rules

Terminal processing has two phases:

1. `Committing`: Reducer freezes Terminal Material and requests a Terminal
   Commit Effect;
2. Reducer enters `Completed/Failed/Canceled` only after a successful Terminal
   Commit Result.

Final Output, Receipt, and Terminal Event come only from the committed Outbox.
A commit failure leaves the Turn recoverable in `Committing`; it cannot publish
a fallback Terminal or leak Output.

### 5.3 Restore rules

- restore reads only current-schema Domain Facts, Effect Ledger, and Commit
  Marker;
- it never infers state from Runtime Capture, Trace, UI Events, or natural
  language;
- a `running` Effect becomes reclaimable and its Attempt increases
  monotonically;
- a side effect with a durable Result is never executed again;
- a committed Terminal with an incomplete Outbox restores projection only;
- Fact gaps, digest mismatches, unknown Effects, and identity conflicts fail
  closed.

## 6. Invariants

Code and tests must enforce all of these:

1. Each State transition is produced by exactly one accepted Command.
2. Its Domain Facts are durable before State changes.
3. Each Effect ID performs at most one logical side effect and closes exactly
   one Result.
4. Tool, Approval, Input, Verification, and Journal share one Effect lifecycle.
5. Accepted Cancel prevents new Provider, Tool, and Verification Effects.
6. A Mutation Revision change invalidates old Completion and Verification.
7. Reducer is the only source of Completion, Verification, Repair, and
   Terminal decisions.
8. Terminal State has no outgoing transition.
9. No Host can observe Final Output before Terminal Commit succeeds.
10. Receipt reads only Frozen Kernel Terminal Material.
11. Terminal Envelope, real Operation Commit, and Outbox commit atomically.
12. Restart restore reproduces the State Digest without repeating completed
    side effects.
13. Persistence or projection failure cannot create two Terminals.
14. App, Runtime, Host, and UI never infer control state from text.

## 7. Execution Rules

1. Migrate by vertical slice. Delete the corresponding Bridge at the end of
   each slice.
2. New and old paths must never both write the same fact. Read-only comparison
   is allowed; dual-write fallback is not.
3. Add a failing behavior/fault gate before changing production code.
4. Do not add historical compatibility migrations to the current pre-release
   schema.
5. Do not restore Capture Replay or create an independent Replay package.
6. Do not replace structured Runtime tests with manual UI operation.
7. Do not include or overwrite unrelated worktree changes.

## 8. New Migration Route

Phase 4R/Phase 5 numbering is retired. The new route uses C0-C6. A stage starts
only after the previous stage meets its mechanical exit conditions.

### C0: Rebuild the truth baseline and gates

Goal: expose the current three control loops instead of proving that a type or
string exists.

Actions:

- add failing tests for Coordinator Store injection, production Restore,
  Effect Executor ownership, zero Final Output leakage, and real Operation
  atomicity;
- inventory production write entries using AST/type dependencies or behavior
  tests rather than brittle substring checks;
- delete the old D01-D11 string tests and move the no-replay constraint into
  the formal Ownership Gate;
- characterize the legacy Engine behavior for vertical migration comparison.

Exit:

- every `Foundation/Bridge/Legacy/Missing` item has executable evidence;
- the normal suite remains green;
- new target gates fail as expected and identify actual owners.

#### C0 executable evidence

C0 uses `CODEHELPER_TURN_KERNEL_CONVERGENCE_EXIT_GATE` to separate two
meanings:

- `make turn-kernel-convergence-baseline` must pass and prove the detectors
  stably observe current facts;
- `make turn-kernel-convergence-exit-gate` must fail during C0; its failures
  are target-architecture debt for C1-C6;
- no historical string gate remains; durable architecture constraints belong
  in AST, type, or behavioral gates.

| Evidence | Classification | Current fact | Executable detector |
| --- | --- | --- | --- |
| F01 | Foundation | State, Command, Reducer, and invariants run independently | `reducer_test.go`, `fuzz_test.go` |
| F02 | Foundation | Coordinator is the only production `Reducer.Apply` entry and preserves persist-before-state | `TestC0FoundationOwnershipBaseline`, `TestPhase4R3CoordinatorPersistsBeforeStateAndDispatch` |
| F03 | Foundation | Domain Fact, Terminal Envelope, and SQLite Store primitives exist | `terminal_envelope_test.go`, `turnstate/store_test.go` |
| C0-D03 | Foundation, resolved in C2 | Control/Tool Effects use the routed registry with durable Start and one retained Result; Engine has no manual Claim/Complete/Forget | `TestC2ControlToolOwnershipBaseline`, `TestC2ToolEffectPersistsStartAndExactlyOneResult` |
| C0-D05 | Foundation, resolved in C3 | Verification Engine Events no longer reverse-drive Kernel commands | `TestC3ModelDecisionOwnershipBaseline` |
| C0-D05 terminal | Foundation, resolved in C4 | Terminal Engine Events no longer reverse-drive Kernel commands | `TestC4TerminalCommitOwnershipBaseline` |
| C0-D11 | Foundation, resolved in C4 | Coordinator state and Domain Facts freeze terminal material without `TerminalObservations` | `TestC4TerminalCommitOwnershipBaseline` |
| C0-D04 | Foundation, resolved in C3 | Reducer actions own Completion, Verification, and Repair decisions | `TestC3ModelDecisionOwnershipBaseline` |
| C0-D08 | Foundation, resolved in C4 | Runtime no longer synthesizes a parallel Terminal fallback | `TestC4TerminalCommitOwnershipBaseline` |
| C0-D09 | Foundation, resolved in C4 | App requires one `TerminalCommitSink`; split Receipt/Terminal emit fallback is removed | `TestC4TerminalCommitOwnershipBaseline` |
| C0-D01 | Foundation, resolved in C1 | persistent wire injects the SQLite Coordinator Runtime; Engine has no Store construction fallback | `TestC0OwnershipFailureBaseline/C0-D01-engine-coordinator-memory-store`, `TestC1SQLiteFactFailureMatrixCommitsNoStateOrEffect` |
| C0-D02 | Foundation, resolved in C1 | startup lease scanning calls production Restore and fails closed on incomplete or duplicate recovery | `TestC0OwnershipFailureBaseline/C0-D02-restore-has-no-production-caller`, `TestC1DurableCoordinatorRuntimeScansRestoresAndLeasesActiveTurn` |
| C0-D06 | Foundation, resolved in C4 | Final Output is projected only after atomic Terminal Commit | `TestC4FinalOutputZeroLeakBaseline` / `TestC0FinalOutputZeroLeakExitGate` |
| C0-D07 | Foundation, resolved in C4 | SQLite commits Terminal Envelope and the real Operation in one transaction | `TestC4TerminalOperationAtomicityBaseline` / `TestC0TerminalOperationAtomicityExitGate` |
| C0-D10 | Foundation, resolved in C5 | Runtime startup scans pending terminal projections and reuses stable Event IDs | `TestC5RestartProjectionOwnershipBaseline` |

Mechanical result on 2026-08-11: C0 is complete. The normal focused suite,
Race, Reducer Fuzz, Docs, Book, Architecture Freeze, and diff check are green;
the target Exit Gate failed stably as expected.

#### C1 input inventory

C1 started and completed from these bounded inputs:

1. define and inject per-Turn durable Store/Dispatcher dependencies in
   `runtime/app/wire`;
2. remove Engine's production authority to construct a Memory Store;
3. add SQLite active-Turn queries, startup scanning, and one restore entry;
4. define shutdown, lease, concurrent restore, and running Effect reclaim
   rules;
5. add a failure matrix for SQLite Fact writes on every Transition without an
   in-memory/SQLite dual-write fallback;
6. keep the C0 baseline green and remove only the D01/D02 target failures owned
   by C1.

### C1: Durable Coordinator construction and restore entry

Goal: every production Turn uses one Durable Store from creation.

Actions:

- construct Turn dependencies in `runtime/app/wire`;
- remove Engine's Memory Store construction;
- append every transition to SQLite Domain Facts in real time;
- add Active Turn index, startup scan, and production
  `RestoreTurnCoordinator`;
- define shutdown, lease, and concurrent restore behavior.

Exit:

- process termination at an intermediate Phase restores the same Digest;
- incomplete Facts, invalid Digests, and duplicate restore fail closed;
- production has no Turn Coordinator Memory Store fallback.

Mechanical result on 2026-08-11: C1 is complete. D01/D02 are absent from the
target Exit Gate. SQLite restart restores the same intermediate digest;
incomplete, corrupt, duplicate, and concurrently leased recovery fail closed;
running Effects requeue through a durable Reducer transition. At C1
completion, D03-D11 behavior owned by C2-C6 was unchanged.

### C2: Migrate Control and Tool Effects vertically

Goal: migrate the bounded Cancel, Approval, Input, and Tool lifecycles first.

Actions:

- replace Deferred claim/complete for these Effects with the real Executor
  Registry;
- durably claim before execution and close after durable Result;
- App submits Control Commands only;
- delete the matching `observeToolLocked`, `resolveWaitLocked`, and manual
  `Forget` paths.

Exit:

- concurrent Cancel/Result, duplicate Result, Result Sink failure, and restart
  preserve exactly-one logical closure;
- these slices no longer pass through `engineTurnKernel.observe`;
- Deferred Dispatcher supports none of these Effect kinds.

Mechanical result on 2026-08-11: C2 is complete. D03 is absent from the target
Exit Gate. Tool, Approval, and Input Effects persist `EffectStarted` before
execution and retain one Result Command across sink retry. Concurrent
Cancel/Result, duplicate Result, and restart requeue close one logical Effect.
At the C2 boundary these slices no longer used `engineTurnKernel.observe`, and
deferred dispatch was limited to C3-C4 Effect kinds.

### C3: Take over Model, Completion, Verification, and Repair

Goal: Coordinator drives the business loop and Engine becomes a set of
Executors.

Actions:

- drive Provider Sample through Effects;
- return model text, tool proposals, and usage as Result Commands;
- let Reducer decide Completion requirement, Verification, Repair budget, and
  the next Effect;
- make Verification Executor return evidence without deciding
  Repair/Fail/Complete;
- delete `completionVerification`, Engine gate branches, and text-driven
  control decisions.

Exit:

- the Engine step pump executes structured Reducer actions and contains no
  Completion, Verification, or Repair policy decision;
- Reducer tests alone prove invalidation of stale evidence after mutation;
- Provider, Verification, and Repair restore at every durable boundary.

Mechanical result on 2026-08-11: C3 is complete. Provider sampling and
verification use routed Effects with durable `EffectStarted` and retained
Result Commands. `ModelSampleResultReceived` returns text, tool proposals, and
usage together; `EvaluateTurnStep` selects Repair, Verify, or Complete.
`VerificationFinished` records evidence while Reducer alone selects the
verification action and spends its repair budget. The Engine no longer owns
`completionVerification`, manual repair spending, verification decisions, or
the verification reverse observer. Running Model Effects requeue durably on
restore, and nested Tool Effect dispatch from a Model Result is deadlock-safe.
D04 and the verification portion of D05 are removed; terminal observation,
Journal, Output, and commit ownership remain C4 work.

### C4: Converge Journal, Output, and Terminal atomically

Goal: remove early Final Output and false Operation Commit atomicity.

Actions:

- convert Journal Commit/Rollback to Effect + Result Command;
- make Reducer produce Frozen Terminal Material;
- share one Atomic Commit Port between Terminal Store and Runtime Operation
  Repository;
- place Final Output, Receipt, Terminal Event, and Operation state in Outbox
  only;
- replace Engine `terminalHandler` ownership with a projection-only terminal
  projector; delete App non-transactional fallback and Runtime synthetic
  terminal.

Exit:

- faults at Fact, Journal, Receipt, Output, Terminal, Operation, Outbox, and
  Marker stages expose no partial terminal state to a Host;
- the database cannot contain a committed Envelope with a pending Operation;
- production emits no Output Delta before commit.

Mechanical result on 2026-08-11: C4 is complete. Journal Commit/Rollback runs
as a routed Effect with durable start and `JournalResultReceived`. Reducer
freezes Final Output and terminal state before App assembly. Persistent Runtime
uses `CommitTerminalOperation` to write Domain Facts, Receipt, Final Output,
Terminal Event, Outbox, commit marker, and the real Operation receipt in one
SQLite transaction. Output Delta, Receipt, and Terminal are projected only
after that transaction commits. A durable lifecycle without the atomic port
fails closed. Terminal Event reverse observation, `TerminalObservations`, the
split App emit fallback, and Runtime synthetic terminals are removed. The
target Exit Gate now fails only on D10, which belongs to C5 restart/outbox
recovery.

### C5: Close projection and restart recovery

Goal: prove that post-commit projection and in-flight recovery are both
interruptible and repeatable.

Actions:

- scan Pending Effects, Committing Turns, and Pending Outbox at startup;
- use stable Event IDs for idempotent projection after crashes;
- restore Approval/Input waits, Provider Retry, Tool Running, and Terminal
  projection;
- add lease or ownership protection against two live Coordinators.

Exit:

- process termination between every Effect and Outbox entry still produces one
  State, Receipt, and Terminal;
- recovery does not use Capture, Trace, or UI Events;
- race, duplicate-start, and transient Store failure tests pass.

Mechanical result on 2026-08-11: C5 is complete. Every Effect now stores its
structured payload beside a verified digest. Runtime startup redispatches only
accepted StartTurn operations that also have non-terminal Domain Facts; Engine
Open then restores the Coordinator and durable registry. Running Provider,
Tool, and Journal Effects are requeued with incremented attempts. Committing
Turns resume Journal closure without returning to model sampling. Approval and
Input waits are primed from lifecycle projections and reuse their original
request IDs without emitting duplicate Required events. Terminal Outbox entries
carry stable Event, Operation, Thread, Turn, and Item identities. Startup scans
all pending terminal projections, uses Event ID uniqueness as the projection
CAS, and marks each entry independently after append. Concurrent and repeated
recovery therefore produces one Receipt and one Terminal. The convergence Exit
Gate became fully green at C5; C6 subsequently removed the migration bridges.

### C6: Delete bridges and freeze architecture

Goal: leave one explainable Turn control path.

Delete:

- `engineTurnKernel.observe*`, `applyLocked`, and `drifted`;
- `DeferredEffectDispatcher`;
- Engine Turn Phase decisions and `terminalProjector`;
- obsolete completion reports;
- every production Memory Store fallback and non-transactional Terminal Sink.

Final gates:

- one Reducer write entry;
- one Durable Coordinator construction entry;
- one Executor per Effect kind;
- one Terminal Commit Port;
- no Event-to-Command reverse synchronization;
- full tests, Race, Reducer Fuzz, recovery fault matrix, Docs, Book,
  Architecture Freeze, and `git diff --check` pass.

Mechanical result on 2026-08-11: C6 is complete. `TurnCoordinator` is the only
production caller of `Reducer.Apply`. `DeferredEffectDispatcher`,
`MigrationEffectDispatcher`, `engineTurnKernel.observe`, `applyLocked`,
`drifted`, `terminalProjector`, `normalizeTerminalProjection`,
`BeginTerminal`, and the unused terminal/outbox Effect placeholders are
removed. All seven Effect kinds use `DurableEffectDispatcher`. Approval and
Input Commands originate at request creation rather than Event projection.
Engine submits `TerminalRequested` facts; Reducer alone chooses Completed,
Failed, or Canceled, and Engine projects the frozen decision. A durable Runtime
without explicitly injected Event, Content, and Terminal stores fails closed.
The C0-C6 baseline, final ownership Exit Gate, recovery matrix, Race, Reducer
Fuzz, Docs, Book, and Architecture Freeze are the completion evidence.

## 9. Validation Matrix

Every stage runs at least:

```bash
go test ./internal/runtime/agent/turnkernel
go test ./internal/runtime/agent/engine
go test ./internal/runtime/app
go test ./internal/persist/state/turnstate
go test -race ./internal/runtime/agent/turnkernel ./internal/runtime/agent/engine ./internal/runtime/app
go test ./internal/runtime/agent/turnkernel -run=Fuzz -fuzz=FuzzReducer -fuzztime=30s
make docs-check
make book-check
make architecture-freeze
git diff --check
```

Add by stage:

- Persistence Fault Matrix;
- Effect Crash/Retry Matrix;
- Terminal Atomicity Matrix;
- Restart/Outbox Matrix;
- static Ownership Gate.

Live Runtime gates and manual VS Code Turns remain paused until the user
explicitly resumes them.

## 10. Immediate Next Step

The C0-C6 convergence route is complete. Do not resume the retired Phase 5.
Live Runtime and manual VS Code gates remain separate, explicitly user-triggered
validation.

Use this status externally:

> Turn Kernel production authority convergence is complete and mechanically
> frozen.
