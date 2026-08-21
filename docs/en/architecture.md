# Architecture and Security

[简体中文](../zh-CN/architecture.md) | English

## Architectural Goal

CodeHelper keeps one authoritative execution runtime while allowing multiple
presentation and integration surfaces. Hosts submit operations and observe
events; they do not reimplement the agent loop or execute privileged tools
directly.

The supported product hosts are CLI, TUI, VS Code, and ACP. There is no
`codehelper web` or `codehelper serve` product command, embedded browser UI,
pairing/QR flow, or REST/SSE host. MCP stdio serving and internal loopback
helpers are integration mechanisms, not product HTTP hosts.

```text
CLI / TUI / VS Code / ACP
                 |
           Operation / Event
                 |
        Runtime application state
                 |
             Agent engine
        /          |           \
   context      providers    guarded tools
                               |
       policy -> approval -> journal -> sandbox
                 |
       persistence + observability
```

## Package Layers

| Layer | Path | Responsibility |
| --- | --- | --- |
| Entry | `cmd/codehelper` | process context and CLI entry |
| Hosts | `internal/host` | user/client I/O and presentation |
| Runtime | `internal/runtime` | protocol, application state, agent loop, wiring |
| Adapters | `internal/adapter` | models, providers, tools, MCP, skills, plugins, hooks |
| Security | `internal/security` | policy, permissions, constitution, sandbox |
| Orchestration | `internal/orchestration` | tasks, workers, automations, workflows, lanes, fleet |
| Persistence | `internal/persist` | relational state, events, CAS, sessions, snapshots, journals |
| Observability | `internal/observability` | versioned observations, usage, traces, diagnostics, verification, telemetry |
| Platform | `internal/platform` | processes, PTY, OS-specific behavior |
| Configuration | `internal/config` | defaults, TOML, environment, validation, provenance |

## Hard Dependency Rules

1. `runtime/protocol` must remain independent of other implementation packages.
2. Hosts do not import and invoke provider, tool, sandbox, or agent-engine
   implementations directly.
3. Model/tool/security construction belongs in `runtime/app/wire`.
4. The business turn loop belongs in `runtime/agent`.
5. Every consequential tool call passes through `adapter/tool/guard`.
6. UI state is a projection, not the source of runtime truth.
7. Persistent writes are transactional or journaled at their ownership boundary.

Architecture tests enforce important import restrictions. A design change that
requires violating one of these rules needs an explicit architecture update,
not a local shortcut.

## Runtime Composition Root

`runtime/app/wire.NewExec` is an orchestration entry, not a service locator or
business workflow. It creates a construction-only `buildState` and executes a
closed module sequence:

```text
config -> provider -> persistence -> platform -> builtin tools
       -> extension contributors -> security -> extension plan
       -> orchestration -> observability -> agent -> runtime
       -> background services
```

Each module owns one construction boundary and publishes only the values needed
by later modules. `buildState` is never retained by Runtime, Engine, or Session
services. Persistence owns Content, Job Log, and the SQLite foundation;
Platform owns Process, Sandbox, and Repository Index; Orchestration owns
Task/Automation repositories, Workflow executors, Scheduler construction,
Subagents, and child worktrees/toolsets. Provider publishes the selected
Provider/Model catalogs, while Security publishes its Permission Store and
Guard Factory.

Builtin and extension tools receive the same Registry instance. Plugin, Skill,
Memory, Dynamic Tool, Hook, and MCP contributors receive only their explicit
construction capabilities and the shared Registry, never `buildState`. Each
returns a deterministic `ContributionReceipt` listing added Tool identities and
named outputs. Task/Automation registration belongs to Orchestration rather
than the extension contributor chain.

Runtime construction has a prepared state. `RuntimeModule` constructs the
facade and restores static durable state without accepting operations.
`BackgroundModule` performs the initial MCP refresh, starts Runtime terminal
outbox and pending-Turn recovery, starts MCP prewarm, reconciles Automations,
then starts the Worker Scheduler. A failed step aborts construction and the
resource stack rolls back; no background worker starts before Runtime recovery
has succeeded.

Construction and shutdown share `assembly.ResourceStack`. Session registers
resource closers once, then both partial-build rollback and normal shutdown
close the stack in reverse registration order. A resource closes at most once,
one close failure does not skip later resources, and callers receive the joined
errors with resource identities. This keeps late failures, including Runtime or
Scheduler construction failures, from leaking resources.

## Runtime Ownership Map

```text
CLI / TUI / VS Code / ACP
        | Operation / Event
        v
operationDispatcher -> ActiveTurnRegistry -> TurnCoordinator -> TurnScope
        |                                        |
        v                                        v
    eventhub.Hub <-------------------------- Event projection
        |
        +-> TerminalPublisher -> app/persistence -> SQLite / Event Log / CAS
        |
        +-> SessionService / ArtifactService -> Host queries

wire.NewExec -> construction modules only
chatmerge.Service -> isolated Chat preview / journaled apply / Git baseline
eventview + VS Code projectors -> Host presentation only
```

| Owner | Path | Exclusive responsibility |
| --- | --- | --- |
| Composition root | `internal/runtime/app/wire` | concrete construction and resource registration |
| Durable Runtime assembly | `internal/runtime/app/persistence` | repositories, lifecycle recovery, persistent Runtime options |
| Chat merge service | `internal/runtime/app/chatmerge` | isolated baseline, three-way preview, journaled apply |
| Operation dispatcher | `internal/runtime/app` | typed Operation handler selection and synchronous commit |
| Turn coordinator and scope | `internal/runtime/agent` | reducer authority, effects, controls, and Turn-local state |
| Event hub and terminal publisher | `internal/runtime/app/eventhub`, `internal/runtime/app` | sequence/fanout and atomic terminal publication |
| WorkGraph kernel and store | `internal/orchestration/kernel`, `internal/orchestration/store` | durable Run, Node, Attempt, Lease, and Effect transitions |
| Extension runtime | `internal/runtime/extension`, `internal/runtime/app/extension` | typed contributors, source plans, generations, lifecycle effects, and control receipts |
| Observation plane | `internal/observability/observation`, `internal/observability/router` | evidence schema, privacy admission, durable routing, and exporter isolation |
| Session and artifact services | `internal/runtime/app` | Host-facing query behavior over Runtime-owned ports |
| Go Host projection | `internal/runtime/eventview` | one typed interpretation of Event payloads |
| VS Code projection | `extensions/vscode/src/chat/projector` | exhaustive Event Class presentation |

`codehelper host --adapter acp` is exclusively persistent. The pre-release
one-shot ACP envelope adapter was removed; Hosts cannot select a second
execution path through `exec`.

## Runtime Protocol

The protocol is defined in `internal/runtime/protocol`; the generated public
schema is `docs/protocol/runtime-protocol.schema.json`.

The conceptual model is:

- **Operation:** a requested state transition, such as starting or canceling a
  turn.
- **Event:** an immutable observation emitted by the transition.
- **Receipt:** structured evidence about context, tools, changes, approvals,
  verification, or cost.
- **Projection:** query-oriented state reconstructed from events and relational
  records.

ACP is the editor transport envelope over this shared model. A feature that
exists only in one host is incomplete unless it is intentionally
host-specific presentation.

Event classification is protocol data, not Host policy. The
`event_traits.json` manifest generates the Go trait table, protocol schema,
TypeScript table, and golden from one source; generation fails when a new
Event lacks Class, Item Owner, Durability, Correlation, or Terminal traits.
Go TUI, CLI, and Bench consume typed semantic updates from `eventview` rather
than classifying `Event.Data`; machine NDJSON remains the original envelope.

VS Code consumes the generated traits and projects through domain modules for
stream, tool, interaction, evidence, terminal, and snapshot behavior.
`projector/index.ts` owns sequence and Turn identity, while
`turn-projector.ts` exhaustively dispatches every generated Event Class.

### Application Ownership

The application Runtime is a Facade over explicit owners:

- `operationDispatcher` maps all eight Operation payloads to typed Handlers.
  Handlers return Outcomes containing Events, Async Turn identity, typed
  Problem, and explicit Commit Mode; only Dispatcher applies synchronous
  commit and rejection.
- `ActiveTurnRegistry` atomically reserves Thread and Turn, binds control and
  cancellation provenance and Profile revision, and rejects stale release with
  an in-memory token. Its durable Lease ID is the persisted Start Operation ID.
  Pending-work phase comes from the authoritative Turn Kernel snapshot.
- `eventhub.Hub` exclusively owns sequence assignment, append, replay,
  subscription fanout, slow-consumer policy, and close.
- `TerminalPublisher` owns atomic terminal commit, deterministic outbox
  publication identity, Event Hub projection, and restart recovery.
- `SessionService` owns lifecycle, Profile, and Tool Catalog behavior;
  `ArtifactService` owns Checkpoint, Plan, Turn recovery, and persistence.
  Runtime exposes their narrow Host-facing methods directly without a parallel
  interface-only package.

## Turn Data Flow

Before execution, Engine builds an immutable `TurnSpec` containing identity,
request, Session Profile, route, policy, limits, prompt prefix, Tool Catalog,
skills, MCP health, and extension snapshots. `turnexec.Factory` opens a typed
Scope from that Spec. Sampling never re-reads those mutable sources during the
Scope.

Scope owns Turn-only Kernel, trace, diagnostics, verification, Tool spend, diff,
and control state. Cancel, steer, Approval, and Input enter through its
`ControlPort`; a bounded mailbox rejects overflow, and the Request ledger
rejects late, duplicate, or wrong-kind resolutions.

1. A host validates user input and submits an operation.
2. The application resolves session, thread, workspace, and policy state.
3. Prompt context assembles repository map, pinned files, working set, evidence,
   policy, and compacted history.
4. Coordinator requests a Provider Sample Effect; `DurableEffectDispatcher`
   persists
   `EffectStarted` before the Engine calls the provider.
5. Model text, usage, and tool proposals return together through
   `ModelSampleResultReceived`.
6. Reducer persists the sample result and turns tool proposals into Tool
   Effects before executor projection.
7. The Tool executor enters the registry and guard, which evaluate mode,
   posture, permissions, constitution, approvals, and sandbox requirements.
8. Tool, Approval, and Input results return as one retained Result Command;
   Coordinator persists logical closure before host projection.
9. Mutating tools write through journaled/transactional adapters.
10. An interactive main Turn must choose a structured state:
    `request_user_input` creates a durable Input wait, Tool Calls continue the
    same Turn, and an accepted `turn_complete` ends it. Provider `message_stop`
    ends only one Sample; ordinary model prose remains provisional and cannot
    complete the Turn. For `status=complete`, the declaration `summary` is the
    exact user-facing final output and is published without another model
    sample. A convergence finalization may instead use
    `output_mode=preserve_provisional` to retain captured output and append a
    concise closing summary. Runtime never infers required input or completion
    from prose wording. A Child executor has no Input Host and therefore cannot
    wait for user input, but it must still continue through Tool Calls or finish
    through `turn_complete`.
11. `EvaluateTurnStep` makes Reducer select Repair, Verification, Finalize,
    Block, or Complete. Repair, consecutive no-progress, and explicit normal
    work-step limits only request typed Kernel convergence; they do not choose a
    terminal error in Engine or Provider loops. Incomplete Provider output has
    no default continuation-count limit and continues while Context and explicit
    budgets permit. Kernel allows one reserved finalization Sample with only
    terminal/input capabilities. Complete follows the normal commit path;
    incomplete records a summary and pending actions for recovery.
12. Cross-layer failures use the protocol `Fault` contract: error code, origin,
    disposition, retryability, side-effect state, and recovery action. An
    untyped boundary error defaults to `unavailable/resume_turn`; only an
    explicit invariant fault may terminate as `internal/fail_turn`.
13. Journal Commit, Suspend, and Rollback are idempotent durable Effects.
    Persistence failure leaves the Effect requested and the Turn in
    `committing`; Runtime rejects the current operation without manufacturing a
    failed Turn terminal. Recovery retries the same Effect.
14. The business terminal decision is frozen before post-turn context
    maintenance. Compaction, Session Delta application, and non-control event
    projection failures are secondary issues or replayable outbox work; they
    cannot rewrite a completed Turn as failed.
12. The Verification executor returns evidence through
    `VerificationFinished`; Reducer selects Passed, Repair, Reported, Blocked,
    Failed, or Reverted and owns the repair budget.
13. Engine submits `TerminalRequested`; Reducer selects Completed, Failed, or
    Canceled. Journal Commit/Suspend/Rollback then runs as a durable Effect and
    returns `JournalResultReceived`. Suspend retains verification-blocked or
    convergence-blocked changes for a structurally bound Continue Turn.
14. Scope prepares a revisioned, digested `SessionDelta` containing History,
    Usage, Cost, Working Set, Evidence, Failures, and Compaction state.
15. Runtime freezes one digested `TerminalMeasurementSnapshot` for Usage and
    latency. The Receipt, measurement-derived Trace, and Terminal Envelope all
    reference that same snapshot rather than sampling mutable counters again.
16. Persistent Runtime atomically commits frozen state, Measurement, Session
    Delta, final output, receipt, terminal event, outbox, and the real operation
    receipt in one SQLite transaction.
17. Engine applies Session Delta exactly once only after that commit. Commit
    failure leaves Session memory unchanged.
18. On restart, Runtime scans pending terminal projections and appends each
    entry with its stable Event ID before marking that entry published.
19. Accepted StartTurn operations resume automatically only when matching
    non-terminal Domain Facts exist; Coordinator requeues running Effects and
    Engine resumes Provider, Tool, or Journal execution from durable payloads.
20. Approval and Input recovery primes the original request IDs before resumed
    execution, so hosts replay one wait rather than receiving a replacement.

Engine always submits a complete logical model request. A Provider adapter may
project it onto an incremental Responses WebSocket only when the model
explicitly advertises the capability, request properties are unchanged, and
input strictly extends its committed response chain. Response IDs and
connection state never enter Host or Runtime authority; reset, retry,
compaction, resume, or uncertain state falls back to a complete request. Usage
retains separate logical and transport digests plus serialized request bytes,
so transport savings cannot be reported as token savings.

Every route carries an explicit `AdapterID`; the immutable Provider Router is
the only production sampling path. The composition root installs dedicated
OpenAI, DeepSeek, and Anthropic adapters plus one parameterized
OpenAI-compatible adapter. DeepSeek never advertises incremental responses, so
its Chat and Responses routes always use complete HTTP/SSE requests without
`previous_response_id`.

Before summary replacement, the token-window gate deterministically reduces
oversized closed Tool Result surfaces from oldest to newest. The Tool layer
keeps the complete original in the durable Content Store and returns a stable
`result_get` handle plus bounded head and tail excerpts. Call/result pairing is
unchanged. The Engine remeasures after each projection and skips summary
replacement when surface pruning alone restores the window.

`TurnCoordinator` is the only production entry to `Reducer.Apply`. Engine
events are projection-only and never feed Commands back into the state machine.
Durable Runtime construction requires explicit Event, Content, and Terminal
stores; ephemeral Memory stores are selected only by explicit `NewRuntime`
construction.

Cancellation and failure are terminal state transitions, not exceptional
absence of data.

## Persistence

Durable state is composed rather than hidden behind one file:

| Component | Purpose |
| --- | --- |
| SQLite | relational projections for sessions, turns, tasks, usage, traces, workflows |
| Event log | ordered durable runtime facts |
| CAS | immutable content-addressed payloads |
| Session metadata | user-facing thread/session organization |
| Workspace journal | before-images and edit recovery |
| Snapshots | explicit thread state checkpoints |

The SQLite schema is currently the initial schema version. Future public schema
changes must use explicit migrations; pre-release development histories were
intentionally collapsed before the initial baseline.

Persistent runtime wiring injects the SQLite Turn Coordinator Store before any
Engine is created. Each accepted transition appends a Domain Fact before state
commit or Effect dispatch. Startup recovery uses renewable active-Turn leases;
invalid or duplicate recovery fails closed.

Session Checkpoints and Plan Artifacts reuse the Snapshot index and CAS. A
Checkpoint stores only a verified model-visible history baseline and Profile
snapshot; restoring it cannot execute historical events. Durable restore and
fork events make restart reconstruction deterministic. Fork lineage and the
active Session Thread remain relational lifecycle state rather than Host-local
state.

## Observation Architecture

Runtime Events remain the Host protocol. The observation plane is a separate,
non-authoritative evidence system for causal diagnosis, telemetry export, and
retention. Every admitted record becomes a versioned `ObservationEnvelope`
containing a stable Observation ID, ordered sequence, Runtime and domain
identity, optional W3C Trace Context, causality links, data policy, bounded
summary, and an optional CAS payload reference.

`observation_traits.json` is the source of truth for each observation kind's
owner, durability, payload policy, retention class, required correlations,
OpenTelemetry mapping, and queue priority. It generates the Go trait table,
TypeScript table, and `docs/protocol/observation.schema.json`.

The Router applies privacy policy before any journal or CAS write. Critical
evidence is written synchronously outside business cancellation; normal and
bulk records use bounded queues. Queue pressure, privacy errors, journal
failures, payload drops, and exporter failures update observation health but
never rewrite a successful or failed business Turn result. `Flush` and
`Shutdown` drain the observation and OTLP queues without making the
observability plane an execution authority.

Capture is controlled by `CODEHELPER_OBSERVATION_CAPTURE`:

- `off` disables observation admission;
- `metadata` is the default and stores redacted summaries without raw payloads;
- `failure` admits redacted payloads only for failure-like observations;
- `full` admits eligible redacted payloads.

Credential and restricted payloads are never persisted. Configured secret
values and the state/config roots are redacted before storage. Payloads use the
Content Store and retention classes: audit and diagnostic payload references
default to 30 days, sensitive payloads to 24 hours, and ephemeral payloads to
one hour. Startup retention releases expired references and deletes only
unreferenced CAS objects; observation metadata remains available for
explanation.

W3C Trace Context propagates across Provider HTTP, MCP HTTP/stdio, processes,
workflows, and subagents. The OTLP projector supports in-memory, HTTP/protobuf,
and gRPC exporters selected through environment variables. Its metric labels
come from a fixed low-cardinality allowlist and cannot contain paths, prompts,
Tool arguments, or resource IDs. The semantic reducer can deterministically
rebuild an explainable graph from the raw journal; support bundle construction
re-redacts selected records and writes an exclusive mode-`0600` archive.

## Context Architecture

Context is split by stability and purpose:

- stable coding policy and system constraints;
- repository map and symbol index;
- user-pinned files;
- evolving working set;
- evidence and unresolved risks;
- recent event history or a structured compact summary.

Bounds are part of correctness. An unbounded context system eventually becomes
expensive, slow, and less coherent.

The execution receipt explains every selected working-set file or test with its
selection sources, supporting evidence, relevance score, and per-entry budget
outcome. `included=false` with a truncation reason means the selector chose the
path but the rendered context budget cut its line. Hosts project this same
receipt rather than reconstructing selection reasons.

## Security Model

Security is layered because no single control answers every question.

### 1. Mode and posture

Describe intent and approval behavior for the current host/session.

### 2. Workspace permissions

Remember what a user has allowed for a specific workspace. They are narrower
than global authority and must remain workspace-bound.

### 3. Constitution

Hard constraints that ordinary session configuration cannot bypass.

### 4. Tool guard

The single decision point for tool identity, risk, approvals, repository policy,
and edit evidence.

### 5. Edit journal and verification

Provide recoverability and evidence after a write was authorized.
The execution receipt preserves every verification attempt, command derivation,
failure category, repair count, final gate action, and final workspace outcome.
Rollback distinguishes restored paths, conflicts, and non-file side effects;
the aggregate pass/fail fields remain only as compatibility summaries.

### 6. OS sandbox

Limits process/filesystem/network behavior at the operating-system boundary.
Backend strength varies by platform. If a required boundary is unavailable,
execution fails closed.

## Secret and Network Boundaries

- Config stores secret references, not secret values.
- Provider and web egress use governed clients and explicit endpoints.
- Logs and reports redact known secrets but are still sensitive.
- MCP and plugins are supply-chain boundaries, not trusted text files.
- Listening services default to loopback.
- Dynamic tools require a trusted client and explicit enablement.

## Extension Architecture

`internal/runtime/extension` defines one typed contributor contract for Thread,
Turn, Context, Tool, and MCP capabilities. Registration validates identity,
failure policy, timeout, and output budget, then seals an immutable Registry.
Contributors receive explicit capabilities and return bounded receipts; they do
not receive construction state or a private Tool Registry.

Extension sources resolve deterministically by source priority into a digested
Plan bound to the active permission digest. Activation gives every process,
connection, Hook, subscription, lease, timer, and Tool registration an
`EffectOwner` containing Extension, source, Plan revision, generation,
capability, and effect kind. Disable drains owned effects; revocation or
quarantine fences stale generations. Lifecycle receipts are durable and
redacted.

Plugin and Skill CLI commands, ACP `extension/list` and `extension/control`,
and the VS Code Extensions view use the same Runtime control plane. Mutation
operations are idempotent by operation ID and persist prepare/commit receipts;
Hosts only submit operations and project Runtime-owned state.

### MCP

External servers expose tools through a protocol adapter. Health, timeouts,
circuit breakers, and tool binding isolation prevent one failing server from
silently corrupting all tool availability.

### Skills

Skills package instructions and resources. Discovery, manifests, locks, and
enablement state make the selected content explicit. Turn selection preserves
exactly named, required, and previously used Skills before applying the bounded
lexical candidate limit. The Turn freezes name-to-handle bindings, and loading
revalidates the content digest, dependency plan, lock, and optional Plugin
authority. `skills_read` accepts every exact handle it advertises for that
frozen entry (skill, package, or resource), then returns the canonical Skill
handle. A genuinely invalid or stale handle returns a structured
`skills_list` recovery action instead of terminating the Turn. The execution
receipt records selection size, explicit matches, token projection, cache use,
and query or candidate truncation.

### Plugins

Plugins add executable capabilities. Registry signatures, publisher trust,
immutable staging, receipts, enablement, rollback, and revocation form the
activation chain.

### Hooks

Hooks observe or gate lifecycle points. They must remain bounded and cannot
become an alternate unguarded execution path.

## Orchestration Architecture

Task, Workflow, Automation, background command, verification, and Agent work
converge on the durable WorkGraph model:

```text
Command(expected revision)
  -> pure WorkGraph Kernel
  -> Aggregate + ordered Facts + Effects
  -> one SQLite transaction
     (snapshot + facts + command receipt + effect outbox + projections)
```

- **WorkGraph Kernel:** owns Run, Node, Attempt, Lease Epoch, and Effect state
  transitions without I/O.
- **WorkGraph Store:** commits transitions atomically, deduplicates command IDs,
  and detects snapshot/fact drift.
- **Worker:** is the only claim authority; heartbeats and settlement are fenced
  by owner, Lease Epoch, authority digest, and revision.
- **Automation and Workflow:** compile scheduled or DAG work into WorkGraph
  Nodes rather than maintaining a second checkpoint state machine.
- **Lane:** records durable placement and explicitly manages inline or
  tmux-backed process adapters; placement is not lifecycle authority.
- **Fleet:** projects and audits WorkGraph state. It cannot enqueue, claim,
  settle, or resume work; repair may rebuild only the snapshot cache from
  ordered facts.
- **Subagent:** runs a bounded child Runtime with a durable Agent Tree, Mailbox,
  Result, Budget Ledger, worktree ownership, approval routing, and journaled
  integration.

All orchestration eventually returns to runtime/tool/security boundaries.

## Change Checklist

Before modifying architecture:

1. Identify the owning layer.
2. Check whether protocol or persisted state changes.
3. Define cancellation, retry, and terminal behavior.
4. Preserve the guard and sandbox path.
5. Add contract or architecture tests.
6. Update both language versions of affected documentation.
7. Regenerate protocol, observation-trait, and compatibility artifacts when
   required.
