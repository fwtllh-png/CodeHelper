# Session Context Memory Optimization Design and Implementation Plan

English | [简体中文](../zh-CN/session-context-optimization.md)

> Status: proposal. This document defines a target architecture, data contracts,
> and staged implementation plan. It does not describe shipped behavior.

## 1. Decision Summary

CodeHelper will keep deterministic structured compaction as the sole
authoritative memory and add an optional, non-authoritative semantic Narrative.
The target context has three layers:

```text
Truth Capsule        verifiable Runtime facts that must be retained
Semantic Narrative   disposable LLM explanation of motives and relationships
Recent Raw Tail      recent causal User/Assistant/Tool history
```

The same program also addresses four problems directly related to summary
quality:

1. Truth Entities and Evidence can grow without a long-session bound.
2. Checkpoints restore History but not the matching Working Set, Evidence,
   Failures, and Plan.
3. Every terminal state stores a complete Session Delta, causing write
   amplification.
4. User Memory injects one whole file and lacks scope, retrieval, update,
   deletion, and same-Runtime refresh.

Structured facts, semantic Narrative, user Memory, and Checkpoints must retain
different authority, retention, and failure policies. They must not collapse
back into one opaque text field.

## 2. Current Baseline

The following properties of the current implementation are requirements to
preserve:

- `contextstore.Ledger` partitions each model request into Stable, History,
  Dynamic, and Continuation content and records revision, digest, and token
  attribution.
- Working Set, Evidence, Failures, Plan, World Baseline, and Token Window are
  persisted in `sessiondelta.Delta`.
- Terminal Envelope, Domain Facts, Session Delta, Receipt, and Outbox commit
  atomically; Engine memory changes only after that commit.
- Compaction first projects oversized Tool Result surfaces and then replaces
  history only at safe Tool-pair boundaries.
- Truth Capsules are derived only from Runtime observations, and
  `verified=true` requires Runtime Evidence.
- A summary candidate must contain all current authority entities; it cannot
  become successful by silently dropping facts.
- Old World State and contextual fragments are removed during compaction and
  their current forms are reinjected later.
- Checkpoint schema, identity, Profile revision, and CAS hash are validated.
- User Memory is disabled by default and can only be appended through the
  guarded `remember` Tool.

These constraints are the starting point, not a legacy design to replace.

## 3. Problem Statement

### 3.1 Truth has correctness bounds but no lifetime capacity bound

The current Capsule merges entities from earlier generations. The Failure
Ledger is bounded, but Evidence Facts, Content Handles, historical critical
facts, and other entities have no unified lifecycle policy. When the mandatory
Capsule itself exceeds `summary_max_bytes`, the only safe behavior is to reject
the candidate and eventually return Context resource exhaustion.

### 3.2 Structured facts cannot express all discussion semantics

Goal, Todo, Change, and Evidence answer what happened, but do not reliably
capture:

- why one approach was rejected;
- the exact priority of a user preference;
- relationships between constraints;
- a design direction not yet represented by a Plan Step;
- why an exploratory path was abandoned.

Making all of these authority entities would expand the schema indefinitely and
incorrectly promote model interpretation into Runtime fact.

### 3.3 Checkpoints can mix different points in time

The current Checkpoint stores History and Profile. Restore replaces only
History. Fork first clones the current Engine Working Set, Evidence, Failures,
and Plan, then installs older History. Old conversation state may therefore be
combined with newer evidence. This never replays Tool side effects, but it does
not satisfy the intuitive meaning of branching model context from one point in
history.

### 3.4 Session Delta has persistence write amplification

`sessiondelta.Delta` is a complete snapshot. Even with compressed wire encoding,
each Turn rewrites History and most ledgers. Before compaction, N progressively
larger Turns can produce close to O(N^2) cumulative history writes.

### 3.5 User Memory lacks governance and retrieval

One `memory.md` file is simple and dependable, but:

- all records share a user-level scope;
- enabling Memory injects the whole file instead of retrieving task-relevant
  entries;
- records have no stable identity, deduplication, update, deletion, expiry, or
  provenance;
- a `remember` write does not refresh the current Runtime's static prefix;
- secret detection is only a preventive heuristic.

## 4. Goals and Non-goals

### 4.1 Goals

- Give authoritative model-visible facts a provable upper bound for every
  supported model context window.
- Preserve deterministic facts, recovery, and auditability.
- Improve continuity for design discussions and long tasks with optional
  Narrative.
- Restore the complete model-context state from a Checkpoint.
- Replace repeated full Session snapshots with bounded incremental persistence.
- Provide scoped, lifecycle-aware, retrievable user Memory.
- Emit a Receipt for every omission, downgrade, expiry, and fallback.

### 4.2 Non-goals

- LLM Narrative cannot become Policy, Permission, Verification, or side-effect
  fact.
- Checkpoint does not automatically revert Workspace state or replay Tools.
- Hosts do not implement a second Summary, Memory, or recovery path.
- A vector database is not a first-phase dependency.
- Model prose does not prove causality or prove that context affected an answer.
- Unreleased formats do not receive indefinite dual-write compatibility. Add a
  migration only when a concrete release requirement exists.

## 5. Design Principles

1. **Authority before fluency:** preserve facts before improving readability.
2. **Bounded by construction:** every model-visible collection has count and
   byte limits where it is built.
3. **Current state, not endless union:** owners redeclare current state; old
   generations do not accumulate forever.
4. **One Runtime authority:** Hosts submit Operations and project Receipts.
5. **Commit before apply:** authoritative Session memory changes only after
   persistence succeeds.
6. **Optional work cannot rewrite business outcome:** Narrative failure is a
   maintenance Receipt only.
7. **State restore is not side-effect replay:** Context and Workspace effects
   remain separate.
8. **Qualification and discovery are separate:** deterministic gates prove
   contracts; exploratory evaluation searches for unknown degradation.

## 6. Target Architecture

```text
Tool/Provider observations
          |
          v
Working Set / Evidence / Failures / Plan
          |
          v
Authority Projection -----> Retention Planner -----> Bounded Truth Capsule
                                                        |
Removed history ---> Narrative Input -------------------+---> Context Window
                         |                              |
                         +-> optional durable LLM job --+     + Recent Raw Tail

Terminal commit
    |
    +-> Session Context Manifest
    |      +-> History base/tail CAS refs
    |      +-> Working/Evidence/Plan CAS refs
    |      +-> State epoch/revision
    |
    +-> Accounting delta
    +-> Runtime events and outbox

Checkpoint
    +-> Context Manifest ref + Profile snapshot
    +-> no Workspace replay or accounting rewind
```

## 7. Bounded Truth Capsule

### 7.1 Entity lifecycle classes

Every Entity carries an owner, retention class, update Turn, and
recoverability:

| Class | Examples | Rule |
| --- | --- | --- |
| `mandatory` | active Goal, open Todo, pending Input, unverified Change, open Diagnostic | retain exactly; fail closed when it cannot fit |
| `protected` | critical Path, recent Failure, unconsumed Handle | retain within per-kind limits and emit Omission on eviction |
| `refreshable` | Definition/Reference Fact, old verified Change | rediscoverable; rank by relevance and freshness |
| `audit_only` | consumed Handle, superseded decision, expired duplicate Fact | absent from model Context but retained in Event/CAS state |

Authority equivalence requires exact equality only for `mandatory` entities.
`protected` and `refreshable` entities must satisfy the retention policy, with
retained, externalized, and evicted counts in the Receipt. This avoids claiming
that every fact remains visible without allowing rediscoverable facts to block
the Session.

### 7.2 Deterministic Retention Planner

The planner allocates budget in stable order:

```text
mandatory
-> protected by kind quota
-> refreshable by criticality, recency, source rank, stable ID
-> omission summaries
-> optional human-readable sections
```

Suggested initial configuration:

```toml
[context.compact]
summary_max_bytes = 8192
truth_max_bytes = 6144
truth_max_entities = 256
fact_max_entities = 96
verified_change_retention_turns = 32
failure_max_entities = 24
handle_max_entities = 32
```

`truth_max_bytes` must be smaller than `summary_max_bytes` to reserve room for
the wrapper, omissions, and Narrative. Configuration validation rejects
impossible combinations.

### 7.3 Current snapshots replace permanent union

Each owner emits a current Entity Snapshot during compaction:

- Plan owns the current Goal and Steps.
- Evidence owns all mandatory risks and a selected Fact set.
- Failure owns a bounded set of recent failures.
- Content Store owns handles that remain readable.
- Working Set owns critical paths and bounded relevant paths.

An earlier Capsule is used only to recover state that the owner has not rebuilt
yet. After Session Delta restoration, restored owner snapshots take authority;
compaction must not continue unioning every historical entity.

### 7.4 Externalizing large values

When an Entity value exceeds its type limit, store the body in CAS and retain:

```text
entity_id
kind
status
content_digest
content_handle
bounded_excerpt
```

The handle continues through the existing guarded Content/`result_get` path. It
must not create an unguarded read interface.

## 8. Non-authoritative Semantic Narrative

### 8.1 Responsibility

Narrative covers semantics that structured facts do not represent well:

- reasons for choosing an approach;
- user preferences and priorities;
- relationships between constraints;
- explored and rejected alternatives;
- the semantic bridge needed to continue the task.

Narrative cannot declare test, mutation, permission, approval, or release
results. Those facts come only from the Truth Capsule.

### 8.2 Generation timing

Narrative is never required to make the current Provider request fit.
Deterministic compaction succeeds independently. Two generation modes are
supported:

- `post_turn` prioritizes reliability. A durable maintenance job starts after
  Terminal Commit and its result becomes eligible for the next Turn.
- `inline` prioritizes continuity. The Agent loop pauses at a blocking
  threshold, generates Narrative through a durable Provider Effect, atomically
  rebases Context, and continues the current Turn.

Even in `inline` mode, Narrative is not required for Context availability. On
timeout, cancellation, malformed output, or Provider failure, Runtime
immediately rebases with Truth Capsule plus Recent Raw Tail. The extra model
call affects semantic quality and latency, not whether the Session can
continue.

### 8.3 Input and output contract

Input is bounded to:

- the bounded Truth Capsule;
- selected removed-message excerpts with stable Message IDs;
- explicit non-authoritative generation instructions;
- no Tools, native search, or side effects;
- fixed low reasoning effort and an independent token budget.

The model returns structured JSON:

```json
{
  "decisions": [{"text": "...", "source_message_ids": ["msg_..."]}],
  "rationale": [{"text": "...", "source_message_ids": ["msg_..."]}],
  "preferences": [{"text": "...", "source_message_ids": ["msg_..."]}],
  "unresolved": [{"text": "...", "source_message_ids": ["msg_..."]}]
}
```

The validator checks schema, UTF-8, lengths, duplicates, unknown source IDs,
and forbidden fields. A source ID proves only that the referenced input exists,
not that the interpretation is correct. Narrative therefore always renders
with `non_authoritative=true`.

### 8.4 Artifact identity

Suggested contract:

```go
type NarrativeArtifact struct {
    Version         int
    ThreadID        protocol.ThreadID
    WindowID        string
    AuthorityDigest string
    InputDigest     string
    RouteDigest     string
    Body            Narrative
    CreatedAt       time.Time
    ExpiresAt       time.Time
}
```

Retain only the latest valid artifact for the current Window. Narratives never
recursively merge across generations. An old Narrative Item that remains
relevant may be carried byte-for-byte, but it cannot be summarized as model
input. Rewriting it requires loading its original Source Message references.
This avoids summary-of-summary drift. Model downshift or Compatibility Hash
change discards old Narrative by default.

### 8.5 Security boundary

- Narrative is untrusted model text.
- Prompt injection from Tool output or user input cannot become Policy.
- Jobs execute only through Provider Router and Durable Effect Dispatcher.
- The `compact` package never calls a Provider directly.
- Raw input persistence follows Observation privacy policy.
- Narrative failure does not trigger Turn repair or spend business
  Verification budget.

### 8.6 Unified three-layer data model

The three layers first exist as a provider-independent Runtime object. An
Anthropic compaction block, OpenAI message, or Host UI state cannot become the
internal source of truth.

```go
type CompactedContext struct {
    Version             int
    CompactionID        string
    ThreadID            protocol.ThreadID
    TurnID              protocol.TurnID
    SourceWindowID      string
    TargetWindowID      string
    SourceContextDigest string
    StablePrefixDigest  string

    Truth               compact.TruthCapsule
    Narrative           *NarrativeArtifact
    Tail                []provider.Message

    AuthorityDigest     string
    NarrativeDigest     string
    TailDigest          string
    Digest              string
}

type Narrative struct {
    Items []NarrativeItem
}

type NarrativeItem struct {
    ID               string
    Kind             string // decision | rationale | preference | unresolved
    Text             string
    SourceMessageIDs []string
    SourceDigest     string
    CreatedTurn      uint64
}
```

`CompactedContext.Digest` covers all three layers and Window identity.
`AuthorityDigest` covers Truth only; Narrative and Tail changes cannot create
authority equivalence. `NarrativeItem.ID` is derived from Kind, canonical text,
and sorted Source Message IDs for retry deduplication.

Model-visible order is fixed:

```text
Stable System Prefix
Truth Capsule
Non-authoritative Narrative, when valid
Recent Raw Tail
Dynamic World State
Continuation/Input
```

Truth should use a `system` Message. Narrative should use a text-only
`assistant` Message with `provenance=context_narrative`. Stable Prefix explicitly
states that Narrative is not instruction, permission, or verification evidence.
Tail preserves original roles and content blocks.

The first implementation does not depend on provider-native compaction blocks.
A native block is an Adapter optimization only after proving logical
equivalence with `CompactedContext`; otherwise changing Provider would change
Session authority.

### 8.7 Trigger and three-layer token budget

Before every Sample, start from provider-observed input and add the locally
estimated Context delta:

```text
projected_input
  = observed_provider_input
  + estimated_delta_after_observation

required_capacity
  = projected_input
  + requested_output_reserve
  + provider_framing_reserve
```

Define three thresholds:

| Threshold | Suggested default | Behavior |
| --- | --- | --- |
| `prepare` | 55% of Hard Limit | generate post-turn Narrative or mark the next compaction |
| `compact` | 65% of Hard Limit | run structured compaction; `inline` pauses |
| `emergency` | 85% of Hard Limit | skip Narrative, rebase structurally, and restrict Tools |

Thresholds use the selected model's actual Context Limit and reserve output,
Tool Definitions, and provider framing. History token count alone cannot drive
the trigger.

Allocate the layer budget in this order:

```text
available_history_budget
  = compact_target
  - stable_prefix
  - tool_definitions
  - dynamic_context
  - output_reserve
  - framing_reserve

1. Mandatory Truth
2. current open causal group and minimum raw tail
3. Protected/Refreshable Truth
4. additional raw tail
5. Semantic Narrative
```

Narrative receives only the remainder after the first four items. If Mandatory
Truth plus the current open causal group exceeds the Hard Limit, Runtime first
projects Tool Result surfaces. If it still cannot fit, return
`resource_exhausted`; never drop an open Tool Call, pending Input, or unverified
Change.

### 8.8 Compaction Plan and cut algorithm

The trigger first creates an immutable `CompactionPlan`:

```go
type CompactionPlan struct {
    ID                  string
    Phase               string
    Trigger             string
    SourceWindowID      string
    TargetWindowID      string
    SourceContextDigest string
    Cut                 int
    RemovedMessageIDs   []string
    TailMessageIDs      []string
    Truth               compact.TruthCapsule
    NarrativeInput      NarrativeInput
    DeterministicResult CompactedContext
    Digest              string
}
```

The cut unit is a causal group, not an arbitrary Message:

- one completed Turn;
- one Assistant Tool Call and its Tool Result;
- closed Tool pairs within the current Turn;
- pending Approval/Input and the request that created it;
- a Provider continuation fragment.

Selection:

1. Apply existing Tool Result surface projection.
2. Generate candidate cuts from the oldest causal group.
3. Require closed Tool pairs on both sides.
4. Never include the current open group in the removed set.
5. Retain at least the current group and the configured recent completed Turns.
6. Strip old World State, Skill, and Constitution fragments for reinjection by
   their owners.
7. Remeasure the complete Context for every candidate rather than estimating
   from history byte differences.
8. Choose the first candidate that reaches target and preserves authority.

Message IDs derive from Thread, Turn, Role, block identity, and content digest.
They remain stable across retry without embedding raw prompts or Tool output.

### 8.9 Narrative Input Builder

The LLM does not receive all removed History. A separate budget selects
high-semantic-density sources:

```text
explicit user constraints and preferences
-> reasons for choosing or rejecting an approach
-> open questions and unfinished directions
-> bounded Error/Failure descriptions
-> Tool Result head/tail excerpts
-> ordinary Assistant narrative
```

Rank by priority and recency, then restore original chronology before sending.
Complete source files, repeated Tool output, Reasoning blocks, old World State,
and Secret/Restricted payloads do not enter Narrative Input. Every excerpt
carries Message ID, Role, Turn, Digest, and truncation state.

Suggested request properties:

```text
tools = []
native_search = false
reasoning_effort = low
temperature = deterministic provider default
max_output_tokens = semantic_narrative_max_output_tokens
purpose = context_compaction
```

`purpose=context_compaction` is Sample attribution, not a Provider bypass. The
initial implementation uses the active Turn's frozen Route. Add an independent
Route only after concrete quality evidence warrants the configuration cost.

The default prompt requires:

1. output only the specified JSON;
2. summarize motives, constraints, preferences, and unresolved questions only;
3. do not declare tests passed, edits completed, approval, or permission;
4. cite one or more Source Message IDs per item;
5. do not emit Tool Calls, execution requests, or commands to continue work;
6. do not repeat long facts that Truth Capsule already represents accurately.

### 8.10 Narrative validation and fallback

Provider output passes these gates:

- Stop Reason indicates complete output, not truncation or content filtering.
- Exactly one JSON text payload exists, with no Tool Call, image, or reasoning
  replay.
- Strict JSON schema rejects unknown fields and trailing content.
- Kind, item bytes, total count, and total bytes are within limits.
- Every Source Message ID belongs to the current `CompactionPlan`.
- Source digests match the plan.
- Item IDs are canonical and unique.
- Artifact Window, Authority, and Route digests match current state.

Validation proves structure and source binding, not semantic correctness.
Narrative remains non-authoritative even after every gate passes. Any failure
emits a `fallback=truth_tail` Receipt and continues with
`DeterministicResult`; it does not retry a normal Agent Sample or trigger
Repair.

### 8.11 Inline pause state machine

`inline` mode adds a Turn-local durable compaction substate:

```text
idle
  -> quiescing
  -> planning
  -> generating_narrative
  -> rebasing
  -> committed
  -> resumed

generating_narrative -- failure/timeout --> fallback
fallback -> rebasing
```

Suggested state:

```go
type CompactionState struct {
    ID                string
    Phase             string
    PlanDigest        string
    NarrativeEffectID string
    RebaseEffectID    string
    SourceWindowID    string
    TargetWindowID    string
    Attempt           uint32
    StartedAt         time.Time
}
```

Control flow:

1. Engine submits `ContextCompactionRequested` at a safe Sample boundary.
2. Reducer enters `quiescing` and stops new Tool Effects.
3. Planning waits until pending Tool, Approval, and Input ledgers reach an
   allowed state.
4. `ContextCompactionPrepared` persists Plan Digest and deterministic fallback.
5. `inline` mode requests `EffectGenerateNarrative`; Provider invocation starts
   only after `EffectStarted` is durable.
6. `NarrativeResultReceived` closes only the matching Effect and rejects late,
   duplicate, or wrong-ID results.
7. Reducer requests `EffectCommitContextRebase`.
8. After Rebase Commit, Runtime replaces Scope History, advances Window, and
   continues the original Turn's next Provider Sample.

Add `EffectGenerateNarrative` and `EffectCommitContextRebase` to complete
`DurableEffectDispatcher` routing and recovery coverage. `runCompactGate`
cannot call Provider synchronously because Crash, Cancel, and duplicate commit
would be outside Turn Kernel authority.

Control semantics:

- User Cancel aborts Narrative Effect and rejects late results; the normal
  Cancel path decides whether terminal structured maintenance runs.
- Steer queues during compaction and enters Continuation after Rebase.
- Approval/Input creates no new interaction; existing request IDs survive.
- Shutdown requeues a running Effect without creating a second Compaction ID.
- Timeout/rate limit follows bounded Provider retry policy, then deterministic
  fallback.

### 8.12 Atomic Rebase and persistence

Rebase is a recoverable Context state transition, not a UI-only Summary event.
Commit material contains at least:

```text
CompactionPlan Digest
Source/Target Window ID
Source Context Digest
Truth/Narrative/Tail Digest
Replacement History or Context Manifest Ref
Authority Digest
Token Measurement
Compaction Receipt
```

Persistent Runtime commits within one ownership boundary:

1. Context Rebase Domain Fact;
2. new Context Manifest/CAS references;
3. Window Revision;
4. `turn.compaction` outbox entry;
5. Effect completion marker.

Engine or Scope authoritative History cannot change before commit. Apply exactly
once after success. Replaying the same Compaction ID and Digest succeeds;
another Digest conflicts. Commit failure leaves the Turn recoverable and
forbids sampling against the old Window.

Terminal Commit references the currently committed Context Revision. If a
process crashes after a mid-turn Rebase, recovery continues from the new Window
instead of sending old History to Provider again.

Ephemeral Runtime uses the same state machine and digest checks with a Memory
Store, avoiding a second semantic path.

### 8.13 Post-turn jobs, restart, and repeated compaction

In `post_turn` mode, Runtime Outbox submits a typed Context Maintenance
Operation after Terminal Commit. The Operation enters an existing
Runtime/WorkGraph ownership boundary; a Host never starts a background Provider
call directly.

Job identity:

```text
thread_id
source_window_id
authority_digest
narrative_input_digest
route_digest
```

After restart:

- a `requested` Job can be claimed again;
- a `running` Job retries after lease expiry;
- an existing matching Artifact Digest completes idempotently;
- a changed Window or Authority marks the Job `stale` before Provider use;
- an Artifact committed without its Event is republished from Outbox.

Across compaction generations, an old Narrative Item may only:

1. carry forward unchanged while its Source Digest remains valid and budget
   allows;
2. regenerate from persisted Source Messages;
3. be explicitly evicted for expiry, conflict, or low relevance.

Old Narrative text cannot be the sole source for a new Narrative. If raw source
retention has expired, the item may remain unchanged until its TTL with
`source_unavailable`, then must be removed.

### 8.14 Provider projection and prompt cache

Provider Request still comes from one `ContextLedger` Snapshot:

```text
Stable       = Base System + Repository Rules + Constitution
History      = Truth + Narrative + Raw Tail
Dynamic      = current World State + Budget/Convergence feedback
Continuation = incomplete output or queued steering
Definitions  = frozen Tool Catalog
```

Compaction leaves Stable Prefix Digest unchanged, preserving System Prompt cache
eligibility; only the new History prefix is written. An Adapter may project
`CompactedContext` to a native compaction block, but Usage, Logical Digest, and
Host Receipt remain based on the Runtime Snapshot.

Compaction sampling usage is recorded separately:

```text
usage.kind = context_compaction
input/output tokens
cache read/write tokens
latency
provider/model/route digest
```

Business Sample and Compaction Usage cannot overwrite each other. Cost policy
may disable Narrative when budget is low, but never disables structured
compaction.

### 8.15 Runtime Events and Host experience

Extend the existing `turn.compaction` lifecycle rather than asking Hosts to
infer state:

```go
type TurnCompactionData struct {
    CompactionID     string
    Status           string // started | summarizing | rebasing | completed | fallback
    Mode             string // structural | inline | post_turn
    SourceWindowID   string
    TargetWindowID   string
    OriginalTokens   uint64
    RetainedTokens   uint64
    TruthBytes       int
    NarrativeBytes   int
    TailTokens       uint64
    AuthorityDigest  string
    FallbackReason   string
    ElapsedMS        uint64
}
```

Hosts show "Compacting conversation" after `started` and return to normal Turn
presentation after `completed` or `fallback`. Providers do not normally expose
real percentage progress, so UI should prefer Stage and Elapsed. Any percentage
must be explicitly stage-based rather than presented as model progress.

TUI, VS Code, CLI JSON, and ACP project the same Runtime Event. With `inline`
disabled, no blocking UI appears; a `post_turn` Job is visible only as
non-blocking status or Receipt.

## 9. Complete Context Checkpoints

### 9.1 Separate Context from Accounting

Split the current Session Delta:

```go
type ContextSnapshot struct {
    Epoch        uint64
    Revision     uint64
    History      HistoryManifest
    WorkingSet   workingset.Delta
    Evidence     evidence.Delta
    Failures     compact.FailureDelta
    Plan         *interact.Plan
    World        contextstore.WorldBaseline
    Compaction   sessiondelta.Compaction
}

type AccountingDelta struct {
    TurnID         string
    Usage          provider.Usage
    CostMicrounits uint64
}
```

A Checkpoint stores Context and Profile snapshots, but cumulative Accounting
cannot roll back. Restore must not bypass Token or Cost budgets.

### 9.2 Restore semantics

Restore atomically:

1. validates Session, Thread, Checkpoint, Profile revision, and Context digest;
2. requires a quiescent Session;
3. creates a new State Epoch while Revision remains monotonic;
4. replaces History, Working Set, Evidence, Failures, and Plan;
5. validates World Baseline and clears it for a full next projection when
   invalid;
6. creates a new Token Window instead of reusing old Provider observations;
7. persists Restore Fact, Context Manifest, and Outbox;
8. changes Engine memory only after transaction success;
9. never executes historical Tools or modifies the Workspace.

### 9.3 Fork semantics

Checkpoint Fork constructs a new Engine from the Checkpoint Context Snapshot.
It must not clone the current parent Engine first. The new Thread receives:

- an independent State Epoch and Window ID;
- Checkpoint-time Working Set, Evidence, Failures, and Plan;
- explicit parent Session/Thread/Checkpoint lineage;
- a snapshot of the currently valid Profile;
- no shared mutable ledgers.

## 10. Low-amplification Persistence

### 10.1 Context Manifest

Replace repeated full Session snapshots in Terminal Envelopes with a small
Manifest:

```go
type ContextManifest struct {
    Version       int
    ThreadID      protocol.ThreadID
    TurnID        protocol.TurnID
    Epoch         uint64
    BaseRevision  uint64
    Revision      uint64
    History       HistoryManifest
    WorkingRef    ContentRef
    EvidenceRef   ContentRef
    FailuresRef   ContentRef
    PlanRef       *ContentRef
    World         contextstore.WorldBaseline
    Window        contextstore.WindowLedger
    Digest        string
}

type HistoryManifest struct {
    BaseRef  ContentRef
    TailRefs []ContentRef
    Digest   string
}
```

History Base is the latest Compaction, Checkpoint, or Fork replacement.
Subsequent completed Turns append one complete Tool-paired Tail Segment.
Compaction creates a new Base and clears TailRefs.

### 10.2 Atomicity

CAS payloads are staged before the SQLite transaction. The transaction:

- inserts the Manifest;
- increments all content references;
- commits terminal Facts, Operation Receipt, and Outbox;
- marks the Manifest as the current Revision.

Failure releases staged references. Startup accepts only the newest Manifest
whose digest, Epoch, and Revision chain are valid. Event reconstruction remains
an audit and disaster-recovery path, not the normal way to rebuild every
ledger.

### 10.3 Format changes

The target format has an explicit `Version`. While the project is pre-stable,
the default is a one-time format change with an explicit Unsupported Schema
error, not automatic legacy dual-write. Add an offline migration only when a
release compatibility requirement exists, and verify it with real old-state
fixtures.

## 11. User Memory v2

### 11.1 Data model

```go
type MemoryRecord struct {
    ID        string
    Scope     string // user | workspace | repository
    Category  string // preference | convention | fact
    Text      string
    Source    string
    CreatedAt time.Time
    UpdatedAt time.Time
    ExpiresAt *time.Time
    Digest    string
}
```

Records have stable IDs and canonical scopes. Memory remains user data, not
Constitution.

### 11.2 Tools and governance

Guarded tools:

- `remember`: create or deduplicate by digest;
- `memory_list`: list metadata without returning every body;
- `memory_update`: update by ID;
- `forget`: delete by ID;
- `memory_get`: retrieve selected bodies.

Every tool declares a Memory Resource and Access Mode and emits an audit
Receipt. Secret detection remains preventive rather than complete DLP.

### 11.3 Retrieval and refresh

Turn admission freezes a Memory Generation and selects in this order:

```text
explicitly pinned record
-> exact workspace/repository scope
-> lexical relevance
-> optional semantic rerank
-> recency and stable ID tie-break
```

Implement deterministic lexical retrieval first. Enable semantic reranking only
after offline quality evidence demonstrates benefit. A Memory mutation advances
Generation. The active Scope remains frozen; the next Turn reads the new
snapshot without restarting Runtime.

## 12. Configuration and Protocol

Suggested configuration defaults preserve current behavior or stay disabled:

```toml
[context.compact]
prepare_tokens = 0 # 0 derives 55% of Model Hard Limit
auto_compact_tokens = 0 # 0 derives 65% of Model Hard Limit
emergency_tokens = 0 # 0 derives 85% of Model Hard Limit
truth_max_bytes = 6144
truth_max_entities = 256
fact_max_entities = 96
verified_change_retention_turns = 32
recent_tail_turns = 2
recent_tail_max_tokens = 8192
semantic_narrative = "off" # off | post_turn | inline
semantic_narrative_max_input_tokens = 4096
semantic_narrative_max_output_tokens = 512
semantic_narrative_max_items = 32
semantic_narrative_item_max_bytes = 512
semantic_narrative_timeout = "30s"
semantic_narrative_retry_limit = 1

[memory]
enabled = false
max_candidates = 32
max_prompt_bytes = 16384
semantic_rerank = false
```

Protocol and Receipt additions:

- Truth retention candidate, retained, evicted, and externalized counts by
  class;
- Narrative Requested, Generated, Accepted, Discarded, Stale, and failure
  reason;
- Context Manifest Epoch, Revision, Base/Tail count, and logical/physical bytes;
- Checkpoint Context Digest and State Epoch;
- Memory Generation, candidate count, selected IDs, and truncation;
- Compaction mandatory bytes, optional bytes, and omission count.

Any new Event kind or field uses the Runtime Protocol generator for Go, Schema,
and TypeScript. Hosts only display Runtime-owned Receipts.

## 13. Failure Policy

| Failure | Behavior |
| --- | --- |
| Mandatory Truth cannot fit | return `resource_exhausted` and preserve original History |
| Refreshable Entity exceeds budget | deterministic eviction with Omission |
| Narrative Provider or parse failure | discard Narrative; business Turn is unchanged |
| Inline Narrative has insufficient budget | skip Narrative and commit Truth plus Tail |
| Narrative Artifact is stale | ignore it and record Stale |
| Context Rebase Commit fails | forbid further sampling and enter `resume_turn` recovery |
| Context Manifest/CAS commit fails | fail Terminal Commit and retry idempotently |
| Checkpoint Context validation fails | reject Restore/Fork |
| World Baseline does not match | clear it and perform a full next projection |
| Memory retrieval fails | skip with Context Receipt; a failed write fails its Tool |
| Secret or Restricted Memory | reject without persisting payload |

## 14. Implementation Phases

### Code ownership and work packages

| Work item | Primary owner path | Constraint |
| --- | --- | --- |
| Entity classes, Retention Planner, and Omission | `internal/runtime/agent/compact` | deterministic logic only; no Provider or Persistence calls |
| Context Snapshot, Epoch, and Manifest codec | `internal/runtime/agent/sessiondelta` | durable contracts and canonical codecs only |
| Compaction Gate, Narrative Effect, and Scope state | `internal/runtime/agent/engine` | every Sample continues through Turn Coordinator |
| Terminal, Restore, Fork, and Maintenance Operation | `internal/runtime/app` | preserve commit-before-apply and Runtime authority |
| CAS references, Manifest, and Checkpoint repositories | `internal/persist` | references and SQLite state are atomic or compensatable |
| Narrative Provider request | `internal/adapter/provider` | continue through Provider Router; no side-channel client |
| Memory record store and Tools | `internal/adapter/memory`, `internal/adapter/tool/memory` | every write passes through Guard |
| Defaults, validation, and environment | `internal/config` | unknown fields fail closed and new capability defaults off |
| Events, Receipts, and schemas | `internal/runtime/protocol` | generator synchronizes Go, JSON Schema, and TypeScript |
| Construction and feature gates | `internal/runtime/app/wire` | wiring only; no business loop |
| Host presentation | `internal/host`, `extensions/vscode` | project Runtime Receipts without inferring Context facts |

Split each Phase into Contract, Implementation, Recovery, Observability, and
Gate commits. Do not change durable format, compaction policy, and Host
presentation in one commit; otherwise a failure cannot be attributed to state
contract, compaction quality, or projection.

### Phase 0: Baseline and observability

- Freeze current Context Digest, Compaction Receipt, and long-session storage
  benchmark.
- Record Truth entity count, mandatory bytes, and Session Delta
  logical/physical bytes.
- Record ledger digests before and after Checkpoint Restore.
- Add 30, 120, and 480 Turn hermetic fixtures.

Exit: observations only; model-visible Context and existing goldens do not
change unexpectedly.

### Phase 1: Bounded Truth Retention

- Add Retention Class and Planner under `internal/runtime/agent/compact`.
- Let each ledger emit a current Entity Snapshot instead of permanent union.
- Add CAS externalization and Omission Receipts.
- Keep Semantic Narrative disabled.

Exit: Capsule stays within bounds for 480 Turns, mandatory loss is zero, and
identical input produces identical digest.

### Phase 2: Context-consistent Checkpoints

- Extract `ContextSnapshot` and `AccountingDelta` from `sessiondelta`.
- Store complete Context in Checkpoints.
- Add State Epoch and atomic commit to Restore/Fork.
- Add regression coverage for old History mixed with newer Evidence.

Exit: every Context ledger digest matches the Checkpoint after Restore/Fork,
Usage/Cost remains monotonic, and side-effect replay stays zero.

### Phase 3: Context Manifest and incremental History

- Define Manifest, History Base, and Turn Tail Segment.
- Replace repeated snapshot payloads with CAS references.
- Implement Manifest recovery, reference cleanup, and crash points.
- Do not add legacy dual-write without a concrete requirement.

Exit: physical write amplification for 480 Turns drops at least 70% from the
baseline. Every commit crash recovers to either the old or new Revision, never
a mixed state.

### Phase 4: Optional Semantic Narrative

- Define `CompactedContext`, `CompactionPlan`, Narrative Item, and Artifact
  codecs.
- Add `EffectGenerateNarrative`, `EffectCommitContextRebase`, and their
  Commands and Domain Facts to Turn Kernel.
- Use Provider Router for no-Tool, low-budget generation.
- Implement `post_turn` jobs, `inline` pause/rebase, source-ID validation, stale
  fencing, and deterministic fallback.
- Extend the `turn.compaction` lifecycle and make TUI, CLI, ACP, and VS Code
  project Runtime state only.
- Keep the feature flag `off` by default.

Exit: disabled mode is digest-identical to Phase 3; Narrative failure never
changes Turn terminal state; Narrative cannot change Authority Digest; recovery
after a mid-turn Rebase crash neither duplicates the commit nor returns to the
old Window.

### Phase 5: User Memory v2

- Add record store, scopes, generations, and CRUD Tools.
- Implement lexical retrieval and next-Turn same-Runtime refresh first.
- Cover secrets, symlinks, concurrent writes, and deletion.
- Keep semantic reranking experimental.

Exit: records never leak across Workspaces; deletion takes effect next Turn;
selection order is deterministic; Memory cannot expand Permission.

## 15. Testing and Evaluation

### 15.1 Qualification Track

Required coverage:

- Unit: retention ordering, tombstones, omissions, Narrative validator, and
  Manifest codec.
- Property/Fuzz: entity ordering, duplicate IDs, corrupt digests, UTF-8 cuts,
  and unknown schemas.
- Engine: pre/mid/post-turn compaction, Provider overflow, and model downshift.
- Control: Cancel, Steer, Shutdown, late results, and duplicate results during
  compaction.
- Persistence: every CAS/SQLite crash point, idempotent retry, and reference
  leaks.
- Checkpoint: Restore/Fork point-in-time consistency, Profile conflict, and busy
  Session.
- Security: prompt injection, secret Memory, cross-Workspace scope, and Tools
  disabled.
- Race: concurrent Memory CRUD, Manifest recovery, and Fork/Restore fencing.
- Protocol: synchronized Go, Schema, TypeScript traits, and goldens.
- Hosts: CLI, TUI, ACP, and VS Code project the same Receipt.

Standard validation:

```bash
go test ./internal/runtime/agent/compact
go test ./internal/runtime/agent/contextstore
go test ./internal/runtime/agent/sessiondelta
go test ./internal/runtime/agent/engine
go test ./internal/runtime/app
go test ./internal/persist/...
go test -race -p 1 ./internal/runtime/agent/... ./internal/runtime/app/...
make docs-check
make book-check
git diff --check
```

### 15.2 Discovery Track

Exploratory evaluation separately searches for unknown degradation:

- generate long conversations, repeated compactions, model up/downshifts, and
  Fork graphs;
- inject conflicting User, Tool, and Memory text;
- create many verified Changes, Facts, and Content Handles;
- interrupt Narrative Job, CAS Stage, and Terminal Commit at random points;
- compare structured-only with structured-plus-Narrative for repeated reads,
  wrong-file edits, and user correction.

Discovery reports newly reached state space and actual findings. A green
Qualification suite is not a substitute.

### 15.3 Quality metrics

Core metrics:

```text
mandatory_fact_loss = 0
authority_digest_mismatch = 0
checkpoint_context_mismatch = 0
side_effect_replay = 0
context_resource_exhausted_rate
truth_bytes / context_window
physical_bytes_written / logical_new_bytes
narrative_accept / discard / stale rate
repeated_read_rate
repeated_equivalent_tool_call_rate
user_correction_rate
```

Token reduction alone is not success. Correct completion, verification
coverage, wrong edits, and repeated work must be evaluated together.

## 16. Rollout and Rollback

1. Ship Phase 0 metrics without behavior changes.
2. Compute Bounded Truth as a shadow Receipt before changing actual Context.
3. Enable Checkpoint v2 under a dedicated format version.
4. Use dual-read/single-write for Manifest only during an explicitly required
   migration window, then remove the old path.
5. Keep Narrative off by default and roll it out by Workspace/Profile.
6. Require explicit Memory v2 import; do not silently copy potentially
   sensitive existing files.

Every Phase has an independent feature gate or format boundary. Rollback
disables that Phase; it must never fall back to LLM-only Summary and bypass the
Truth Capsule.

## 17. Risks and Mitigations

| Risk | Mitigation |
| --- | --- |
| Retention drops valuable but refreshable facts | Omission Receipt, CAS handle, and long-session Discovery |
| Narrative introduces hallucination or prompt injection | non-authoritative marker, no-Tool job, source IDs, independent Guard |
| Manifest increases recovery complexity | monotonic Epoch/Revision, canonical digest, crash matrix |
| Checkpoint v2 is mistaken for Workspace rollback | API/UI retain `side_effects_replayed=false` |
| Incorrect Memory scope leaks data | canonical Workspace identity and Memory disabled by default |
| Program scope grows too large | deliver strict independent Phases that can stop separately |

## 18. Final Acceptance Criteria

The program is complete only when:

1. Truth Capsule, model Context, and physical persistence have explicit bounds
   through a 480 Turn Session.
2. Mandatory entities remain authority-equivalent across repeated compaction,
   restart, and model downshift.
3. Checkpoint Restore/Fork cannot include Context State created after the
   Checkpoint.
4. Restore cannot rewind Usage, Cost, Permission, or Workspace side effects.
5. Narrative-off remains deterministic; Narrative-on failures cannot alter
   business terminal outcome.
6. `inline` can pause, atomically rebase, and continue the same Turn without
   duplicate compaction after Cancel or restart.
7. User Memory supports scoped query, update, and deletion effective on the
   next Turn.
8. Every Context omission, downgrade, and invalidation is explainable from a
   Receipt.
9. Hermetic, Race, Architecture, Protocol, VS Code, Docs, and Book gates all
   pass.
