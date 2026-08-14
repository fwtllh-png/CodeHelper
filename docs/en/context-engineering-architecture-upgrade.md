# Context Engineering Architecture Upgrade

[Simplified Chinese](../zh-CN/context-engineering-architecture-upgrade.md) | English

> Status: `proposed`.
>
> Baselines: CodeHelper `f1b1ec1955f4a64149268cfe3bf91b00a0f05d06`;
> Codex reference implementation
> `3bbf1fe75701c97fb190e0867002ba2d9dbda5db`.
>
> Scope: model-visible context construction, persistence, diffs, token
> accounting, tool-result admission, compaction, recovery, provider projection,
> and verification. This proposal does not change ownership of Hosts, Guards,
> Approvals, Journals, or Sandboxes.

Implementation progress:

- CE0: `accepted`; see
  [`context-engineering-ce0-evidence.json`](../context-engineering-ce0-evidence.json).
- CE1-CE7: not started.

## 1. Executive Summary

CodeHelper already has token-native windows, tool-result surface pruning, a
stable tool catalog, deterministic compaction summaries, provider replay
isolation, and per-sample token attribution. Its primary remaining problem is
not one missing optimization. Model-visible context authority is distributed
across:

- `promptcontext.Context` and `TurnContextSnapshot`;
- Engine durable history;
- Turn Scope world state, catalog state, and receipts;
- `SessionDelta.History`;
- Event Log reconstruction; and
- provider-private replay state.

Each component is locally coherent, but no single owner can answer all of these
questions:

> Why did one sample see these items, which durable revision established them,
> which later changes can be appended as diffs, and which baseline remains valid
> after compaction, rollback, or restart?

This proposal introduces a Runtime-owned, versioned, durable `ContextLedger` as
the sole authority for model-visible context. Prompt partitions, conversation
items, world-state patches, tool-result surfaces, and compaction checkpoints
enter the Ledger before Provider Adapters project them into wire requests.

The target is not a copy of Codex and does not replace CodeHelper's
deterministic facts with a model-written summary. It combines:

1. Codex's unified transcript, typed fragments, world-state full/patch model,
   history normalization, and observed window baseline;
2. CodeHelper's deterministic truth summary, content handles, detailed token
   attribution, explicit adapter capabilities, and durable Turn Kernel; and
3. deletion of the existing parallel context authorities instead of permanent
   dual writes or a second execution path.

## 2. Current Mechanism

### 2.1 Frozen Turns

`internal/runtime/agent/engine/turncontext.go` freezes a `TurnSpec` before a Turn
starts. It includes:

- Session, Turn, and Profile revisions;
- Route, Provider, Model, and reasoning capabilities;
- Policy, Mode, Posture, and sandbox identity;
- prompt context, tool catalog, Skills, MCP, and extension snapshots; and
- step, output, token, and cost budgets.

This prevents a running Turn from rereading mutable Profiles or Tool
Registries. The new Ledger must preserve this boundary.

### 2.2 Sample Assembly

`internal/runtime/agent/engine/model_handler.go` assembles model input as:

```text
stable context
  + durable history
  + dynamic turn context
  + output continuation
  + provider tool definitions
```

`promptcontext.MeasureSample` attributes Stable, User, Assistant, Tool,
Dynamic, Continuation, and Tool Definition tokens. It also records logical
request and transport payload digests.

### 2.3 World-State Diffs

The repository map, working set, evidence, and plan use receipt digests to
detect changes. Within one Turn, the first Sample places the complete world
context in the stable prefix. Later Samples append changed sections to history.

The limitation is that this baseline belongs to Turn Scope. A new Turn,
restart, rollback, or history replacement has no durable typed world-state
baseline from which to continue producing patches.

### 2.4 Token Windows

The current gate:

- uses the Provider model context limit as a hard ceiling;
- defaults auto-compaction to 65%;
- injects convergence guidance at 55%;
- enters finish-only behavior at 85%;
- calibrates estimates using the prior Sample's actual input usage; and
- supports `total` and `body_after_prefix` scopes.

`body_after_prefix` currently subtracts estimated Stable tokens from the total.
It does not use the server-observed prefill of the current compaction window.

### 2.5 Tool Results and Compaction

The Tool layer spills large results to the Content Store and returns a
`result_get` handle. Before summary replacement, the window gate prunes closed
Tool Result surfaces from oldest to newest. Summary replacement is skipped when
pruning alone restores the window.

The summary deterministically uses the plan, failures, changes, critical paths,
evidence facts, and a digest of removed messages. This prevents a model from
inventing claims such as "verification passed." However, repeated compactions
carry the prior summary as a complete block, while transcript digest entries
retain only bounded flattened text.

### 2.6 Persistence and Recovery

`SessionDelta` atomically commits history, usage, cost, working set, evidence,
failures, compaction state, and plan state. Event Log reconstruction starts
from the newest compaction, fork, or checkpoint and filters failed Turns and
unclosed Tool pairs.

The Delta does not currently persist a typed context baseline, history
revision, observed window prefill, or stable identity for each context item.

## 3. Codex Mechanisms Worth Adopting

This section identifies portable design principles. It does not make
OpenAI-specific Codex behavior a target protocol for CodeHelper.

### 3.1 One ContextManager

Codex `codex-rs/core/src/context_manager/history.rs` jointly owns:

- a copy-on-write Response Item transcript;
- a history version;
- provider token usage;
- a Turn Context reference baseline;
- a world-state baseline; and
- pre-prompt normalization.

Compaction and rollback rewrites increment the history version and invalidate
baselines that can no longer be trusted.

### 3.2 Typed Contextual Fragments

Each Codex context injection is a struct implementing one fragment contract. It
declares:

- its role;
- markers;
- body;
- whether it requires a separate message; and
- how retained history recognizes the same fragment type.

Filtering, rollback, and compaction therefore do not have to guess whether
arbitrary text was injected by the Runtime.

### 3.3 World-State Full/Patch

A new window writes a full snapshot. Later state changes produce typed diffs
and commit them in this order:

1. render the model-visible fragment;
2. append the fragment to conversation history;
3. persist the world-state patch; and
4. advance the baseline.

The baseline cannot move ahead of the context the model actually received.

### 3.4 History Normalization

Before sending a request, Codex has one path that:

- supplies a failed output for missing Tool Call output;
- removes orphan outputs;
- preserves call/output pairs;
- removes unsupported image or audio content according to model modality; and
- applies token-based truncation as Tool output enters history.

### 3.5 Compaction-Window Baselines

Codex maintains IDs, a sequence number, and prefill input tokens for every
auto-compaction window. Server-observed prefill replaces a recovered estimate
when Provider usage becomes available.

### 3.6 Strict Incremental Transport

Codex reuses WebSocket `previous_response_id` only when:

- every non-input request property is equal;
- current input strictly extends the prior request input and prior response
  items;
- the response ID is valid; and
- turn-scoped sticky routing state cannot leak across Turns.

This compresses transport without changing the complete logical transcript.

## 4. Gaps and Root Causes

| Priority | Gap | Root cause | Observable effect |
| --- | --- | --- | --- |
| P0 | Distributed context authority | Stable, History, Scope, and receipts own separate state | Restart and live execution can use different baselines |
| P0 | World-state baseline is not durable across Turns | Digest skipping is Turn-local | A new Turn resends full state and mutates the prefix |
| P0 | Rewrites have no unified revision | Replace, Compact, and Fork handle state separately | Derived caches or cursors can refer to stale history |
| P1 | Incomplete fragment contract | Only Skills and Constitution use markers | Plan, Policy, and Evidence cannot share invalidation rules |
| P1 | Window prefill is estimate-led | No server-observed window ledger | `body_after_prefix` can compact too early or too late |
| P1 | Tool output has no single admission layer | Result Store and pressure pruning run at different stages | Large output can affect several Samples before pruning |
| P1 | Normalization is distributed | Engine, recovery, and Providers each check pairing | New protocols and recovery paths can miss invariants |
| P2 | Repeated summaries carry complete prior text | A summary is one marked text block | Old text displaces new facts across generations |
| P2 | No compaction compatibility contract | Windows only compare token limits | Model downshift reacts after pressure or failure |
| P2 | No unified multimodal cost model | Generic estimates are text-oriented | Image, audio, and encrypted content attribution drifts |

## 5. Design Principles

1. **One authority:** only `ContextLedger` changes model-visible context.
2. **Append by default:** normal Samples append; only Compact, Rollback,
   Restore, Fork, and explicit migration may rewrite.
3. **Rewrites increment revision:** every rewrite advances a revision and
   invalidates derived baselines.
4. **Typed before rendered:** context is stored as typed items and rendered only
   during provider projection.
5. **Persist after visibility:** a world-state baseline advances only after its
   model-visible item enters the Ledger.
6. **Admission before retention:** size, capabilities, and pairing are enforced
   before an item enters the Ledger.
7. **Truth is deterministic:** goals, changes, failures, and verification facts
   come only from Runtime ledgers.
8. **Transport is not authority:** response IDs, connections, and sticky
   headers never become Runtime context authority.
9. **Capabilities are explicit:** incremental transport, modalities, and
   remote compaction are not inferred from Provider names.
10. **No permanent dual path:** a phase may use a characterization adapter but
    must delete the authority it replaces before exit.

## 6. Target Architecture

```text
TurnSpec
  |
  v
Context Contributors -----> Typed Context Items
  |                               |
  |                               v
  +-----------------------> ContextLedger
                                  |
                 +----------------+----------------+
                 |                |                |
                 v                v                v
          Token Window      Compaction       Durable Delta
                 |                |                |
                 +----------------+----------------+
                                  |
                                  v
                         Provider Projection
                                  |
                         Logical Request Digest
                                  |
                     HTTP/SSE or verified incremental
```

### 6.1 ContextItem

Proposed logical shape:

```go
type ContextItem struct {
    ID         string
    Kind       Kind
    Role       provider.Role
    Source     Source
    Lifetime   Lifetime
    Turn       uint64
    Revision   uint64
    WorldKey   string
    Content    Content
    Digest     string
    TokenCost  TokenCost
    Provenance Provenance
}
```

Requirements:

- `ID` remains stable across persistence, replay, and provider projection;
- `Kind` is an enum rather than a new stringly typed dispatch surface;
- `Lifetime` distinguishes at least Session Prefix, Window State, Turn Input,
  Conversation, and Ephemeral Feedback;
- `WorldKey` identifies state replaced by a later patch;
- `Content` supports text, image, Tool Call, Tool Result, reasoning replay, and
  content handles; and
- every model-visible item has a hard ceiling of 10,000 tokens.

### 6.2 ContextLedger

The Ledger owns at least:

```text
items
history_revision
world_state_baseline
turn_context_baseline
window_number
window_ids
window_prefill
provider_usage
normalization_receipt
```

Core operations:

```text
Append(items)
Snapshot(model_capabilities)
Replace(reason, items)
Rollback(turns)
Fork(checkpoint)
ApplyWorldState(snapshot)
Compact(policy)
RecordProviderUsage(usage)
```

`Snapshot` is the only input to Provider sampling. Provider Adapters no longer
receive independently authoritative Stable, History, and Dynamic collections.

### 6.3 World State

The initial migration includes:

1. Model, Mode, and reasoning policy;
2. permissions, sandbox, and execution target;
3. AGENTS and repository instructions;
4. Tool Catalog, materialized tools, and deferred-tool state;
5. repository map;
6. working set;
7. evidence, risks, and reminders;
8. plan; and
9. Skills, MCP, and extension health.

Every section implements:

```text
StableID
Snapshot
RenderFull
Diff(previous)
RecognizeRetained
TokenLimit
```

A full item is allowed only for:

- a new Session or compaction window;
- a missing baseline;
- a history revision mismatch;
- rollback of the item that established the baseline; or
- an incompatible model capability or context contract.

### 6.4 Admission and Normalization

Before an item is recorded:

1. validate Role, Kind, and Content;
2. enforce Tool Call ID uniqueness;
3. preserve Call/Result pairing;
4. project image, audio, and reasoning according to model capabilities;
5. apply per-kind token budgets;
6. spill large Tool Results to the Content Store;
7. generate bounded head/tail surfaces and handles; and
8. record original size, retained size, reason, and digest.

Pressure-time Tool Result pruning remains a second line of defense. Normal
operation must not depend on it for per-item bounds.

### 6.5 Token Window Ledger

Each window owns:

| Field | Meaning |
| --- | --- |
| `full_active_tokens` | Latest complete input context reported by the Provider |
| `prefill_tokens` | Input baseline of the first request in this window |
| `body_tokens` | `full_active_tokens - prefill_tokens` |
| `tool_definition_tokens` | Current Tool Definition cost |
| `pending_tokens` | Estimated cost of items not yet observed by the Provider |
| `output_reserve` | Output reserved by reasoning and finish policy |
| `auto_compact_limit` | Automatic compaction threshold |
| `hard_limit` | Hard model context window |

Provider usage takes precedence. The estimator covers only items appended after
the last Provider usage observation. One global ratio must not calibrate text,
Tool schemas, and images together.

### 6.6 Compaction

Compaction emits two layers:

1. **Truth Capsule, authoritative**
   - goal;
   - open and completed todos;
   - changed files and verification state;
   - failed attempts;
   - critical facts and sources;
   - pending approval or input; and
   - content handles.
2. **Narrative Summary, optional and non-authoritative**
   - carries discussion or decision context that is difficult to structure;
   - cannot create Verified, Changed, or Passed fact fields; and
   - is omitted on failure, budget pressure, or unsupported Providers.

Repeated compaction merges Truth Capsules by stable entity ID. It does not carry
the complete previous summary as one block. Narrative may be regenerated but
cannot overwrite Truth.

### 6.7 Provider Projection

A Provider Adapter only:

- converts a Ledger Snapshot to Chat, Responses, or Messages wire protocol;
- retains valid replay state for the same Adapter;
- computes logical request and transport payload digests;
- attempts incremental transport only when the capability is explicit; and
- falls back to a complete request when strict extension fails.

DeepSeek remains:

```text
incremental_responses = false
complete logical request
HTTP/SSE
server-side prompt cache
```

## 7. Core Benefits and Verification

Every benefit must be demonstrated with paired Baseline/Candidate executions of
the same canonical workload.

| Benefit | Mechanism | Primary metric | Acceptance gate |
| --- | --- | --- | --- |
| Lower expensive input | Cross-Turn world-state patches and stable prefixes | `uncached_input_tokens` P50 | At least 20% below baseline |
| Lower cumulative input | Fewer full reinjections and repeated dynamic tails | `input_tokens` P50 | At least 15% below baseline |
| Better cache continuity | Append-only context and stable Tool contract | `cached_share` after Sample 3 | At least 95% and no lower than baseline |
| Correct compaction timing | Server-observed prefill | Rejections and trigger error | Rejections=0; error <=5% |
| Correct recovery | Durable baseline, revision, and window | Restart semantic digest | 100% match |
| Correct Tool history | One normalizer | Pairing and orphan count | Pairing=100%; orphans=0 |
| Better long-thread retention | Structured Truth merge | Facts after three compactions | Critical facts=100% |
| Bounded individual items | Admission token cap | Maximum item tokens | Every item <=10,000 |
| Better diagnosis | Item receipts and attribution | Unknown token share | At most 2% |
| Lower maintenance burden | Delete parallel authorities | Owned LOC and authority count | Authority=1; final net growth <=0 |

### 7.1 Metric Definitions

```text
uncached_input_tokens = input_tokens - cached_tokens
cached_share = cached_tokens / input_tokens
context_estimation_error =
  abs(estimated_input_tokens - provider_input_tokens) / provider_input_tokens
world_state_full_rate =
  full_world_state_items / model_samples
unknown_token_share =
  unattributed_input_tokens / provider_input_tokens
```

Cached tokens must not be added to input a second time. Reasoning tokens must
not be added to output a second time.

### 7.2 Correctness Hard Gates

Token savings cannot compensate for failure of any of these gates:

- Tool Call/Result pairing is 100%;
- restart, fork, rollback, and checkpoint restore expose no stale world state;
- failed Turns do not enter recovered model-visible history;
- pending approval or input is not marked resolved by compaction;
- Truth Capsules do not invent verification claims;
- DeepSeek never sends `previous_response_id`;
- old replay state is invisible after Route or Adapter changes; and
- Guard, Approval, Journal, and Sandbox paths remain unchanged.

### 7.3 Quality Gates

Canonical task output must also satisfy:

- task completion rate is no lower than baseline;
- workspace diff matches the expected result;
- verification pass rate is no lower than baseline;
- Sample Count P50 does not increase;
- Tool Call Count P50 increases by no more than 5%;
- Provider errors and repair Samples do not increase; and
- P95 Turn latency increases by no more than 5%, unless tokens fall by more than
  30% and the report explicitly justifies the tradeoff.

## 8. Verification Workloads

The suite covers at least:

| Scenario | Primary concern |
| --- | --- |
| Long read-only analysis | Stable prefix, cache, and no-change completion |
| Multi-file edit and verify | Working set, evidence, and change truth |
| Tool-heavy search | Result admission, handles, and pairing |
| Mid-turn world-state change | Typed patch ordering and persistence |
| Restart during model/tool wait | Baseline, pending effects, and replay |
| Rollback and fork | History revision and baseline invalidation |
| Three consecutive compactions | Truth retention and generational loss |
| Model switch/downshift | Compaction compatibility |
| Image input | Modality projection and token estimates |
| DeepSeek live | Complete SSE, cache share, and DSML completion correctness |

Every Scenario fixes:

- prompt bytes;
- workspace fixture;
- Session Profile;
- model and Provider;
- Tool Catalog Snapshot;
- maximum steps and budgets; and
- cache arm.

## 9. Experiment and Evidence Protocol

### 9.1 Execution Order

1. two Baseline A/A batches to establish noise;
2. two Candidate A/A batches to establish implementation stability;
3. paired Baseline/Candidate hermetic runs;
4. a cache-disabled live arm;
5. a cache-enabled live arm;
6. restart, rollback, and compaction fault injection; and
7. comparison and gate reports.

### 9.2 Sample Size

- Hermetic: every fixture passes.
- Benchmark: at least 10 runs per arm.
- Live: at least 10 successful samples per arm.
- If the P50 difference is below 10%, increase to 20 successful samples.
- Unfavorable samples cannot be removed. Only predefined infrastructure failure
  codes may be excluded, and exclusions are reported separately.

### 9.3 Evidence Layout

```text
.tmp/context-engineering/
  baseline/
    config.json
    requests.ndjson
    usage.ndjson
    context-items.ndjson
    compactions.ndjson
    scenarios.json
  candidate/
    ...
  comparison.json
  gates.json
  report.md
```

Stable phase evidence is copied to:

```text
docs/context-engineering-ceN-evidence.json
```

Evidence includes Commit, dirty state, Model, Provider, prompt digest, fixture
digest, Sample Count, P50/P95, gate status, and failure reasons.

### 9.4 Benchmark Contamination Controls

- Cache-enabled and cache-disabled arms use different stable cache keys.
- Baseline and Candidate use different cache namespaces.
- Warmup is excluded from Samples but remains recorded.
- Samples with different logical request digests cannot be paired.
- Retry, Repair, and Continuation remain distinct Samples.
- Transport byte savings cannot be reported as token savings.
- Unknown Provider prices cannot produce inferred cost savings.

## 10. Implementation Phases

### CE0: Characterization and Baseline

Deliver:

- canonical model-visible context dumps;
- context item and partition attribution;
- restart, rollback, fork, and compaction goldens; and
- baseline evidence.

Exit:

- fixtures replay current behavior;
- token attribution coverage is at least 98%;
- A/A P50 drift is at most 5%; and
- production behavior is unchanged.

### CE1: ContextLedger Skeleton

Deliver:

- `internal/runtime/agent/contextstore`;
- typed `ContextItem`, Snapshot, and Revision;
- a one-way adapter from existing Prompt/History state; and
- equivalent Provider request goldens.

Exit:

- every production Sample is built from one Snapshot;
- logical request digests match the baseline;
- no second Provider call path exists; and
- replaced assembly helpers are deleted in the same phase.

### CE2: Typed World State

Deliver:

- the Section contract;
- Full/Patch snapshots;
- durable baseline and revision in Session Delta; and
- migration of Policy, Tools, Repo Map, Working Set, Evidence, Plan, and Skills.

Exit:

- at most one Full injection occurs per window;
- unchanged state emits no model-visible item;
- the first post-restart Sample does not unconditionally reinject Full; and
- rollback of the baseline item safely falls back to Full.

### CE3: Token Window Ledger

Deliver:

- Window ID, number, and server-observed prefill;
- per-kind estimators;
- pending-item cost; and
- separate Full, Body, Tools, and Reserve accounting.

Exit:

- text estimator P95 error is at most 5%;
- multimodal P95 error is at most 10%;
- context rejection count is zero; and
- compaction trigger error is at most 5%.

### CE4: Admission and Normalization

Deliver:

- token-native admission for every Tool output;
- a Pair/Orphan normalizer;
- modality projection; and
- unified Content Store handle and receipt behavior.

Exit:

- Pairing=100% and Orphans=0;
- every model-visible item is at most 10,000 tokens;
- a 100 KiB Tool Result surface shrinks by at least 80%; and
- complete original content remains retrievable by handle.

### CE5: Structured Compaction

Deliver:

- stable-entity Truth Capsules;
- multi-generation merge;
- optional Narrative; and
- compatibility hash and model-downshift policy.

Exit:

- critical fact retention remains 100% after three compactions;
- summaries contain no invented verification;
- retained tokens are below target; and
- compaction failure leaves original history intact.

### CE6: Provider Projection and Cache

Deliver:

- exhaustive request-property comparison;
- stable-prefix digest;
- incremental eligibility receipts; and
- complete fallback reasons.

Exit:

- capable Providers use incremental transport for strict extensions;
- property, Route, Retry, Compaction, and Resume changes force full fallback;
- DeepSeek always uses complete HTTP/SSE; and
- logical history and transport deltas are equivalent.

### CE7: Convergence and Deletion

Deliver:

- deletion of old Stable/History/Dynamic authorities;
- deletion of receipt-only baselines;
- updated protocol, monitoring, documentation, and architecture ratchet; and
- final evidence.

Exit:

- Context Authority Count=1;
- owned production paths have net growth at most zero from CE0;
- no feature flag or permanent dual write remains; and
- all correctness, efficiency, and architecture gates pass.

## 11. Test Matrix

| Layer | Required coverage |
| --- | --- |
| Unit | Item validation, diffs, revisions, estimates, admission, merge |
| Property/Fuzz | UTF-8 truncation, pair normalization, patch round trips |
| Engine | Sample layout, window gate, compaction, Repair, Continuation |
| Persistence | Atomic Delta, restart, revision conflict, checkpoint |
| Provider | Chat/Responses/Messages projection and replay filtering |
| Runtime | Pending model, Tool, Approval, and Input recovery |
| Multi-Agent | Task Capsule, fork, child budgets, result merge |
| VS Code/ACP | Context receipts, replay, compact, and fork consistency |
| Live | DeepSeek cache, usage, SSE, and DSML Tool completion |

Each phase runs at least:

```bash
go test ./internal/runtime/agent/...
go test ./internal/runtime/app/...
go test ./internal/adapter/provider/...
go test ./internal/adapter/tool/...
go test ./internal/persist/...
go test -race ./internal/runtime/agent/...
make architecture-ratchet
make token-bench
make docs-check
make book-check
git diff --check
```

Changes to VS Code protocol or receipts also run:

```bash
cd extensions/vscode
npm run check
npm test
```

## 12. Risks and Controls

| Risk | Control |
| --- | --- |
| Ledger becomes a new God Object | Core stores state only; contributors, normalizer, budgeter, and compactor remain separate |
| Migration creates two authorities | Each phase permits only a one-way adapter and deletes the old writer before exit |
| Typed items expand persistence | Persist snapshots/patches without duplicating rendered text and typed payload |
| Early admission loses important output | Original enters Content Store; surface retains head, tail, and handle |
| Model Narrative hallucinates | Narrative is non-authoritative; Runtime alone generates Truth fields |
| Patch baseline drifts | Validate both revision and baseline digest; mismatch triggers Full |
| Token optimization harms quality | Correctness gates take precedence over efficiency gates |
| Cache experiments cross-warm | Namespace isolation and paired digests |
| Provider details leak inward | Capabilities drive behavior; Adapters only project |
| DeepSeek accidentally uses incremental | Capability tests and live request dumps form two gates |
| Production code keeps growing | Delete replaced paths every phase and enforce the CE7 net-growth gate |

## 13. Non-goals

This proposal does not:

- change Guard, Approval, Constitution, Journal, or Sandbox ordering;
- persist Provider connections or response IDs as Runtime authority;
- emulate `previous_response_id` for DeepSeek;
- replace durable history with a vector database;
- automatically remove explicitly pinned user context;
- skip required verification to save tokens; or
- preserve the old Context Engine as a long-term compatibility mode.

## 14. Definition of Done

The upgrade is complete only when:

1. one versioned Ledger constructs all model-visible context;
2. World State Full/Patch survives Turn, restart, fork, and compaction boundaries;
3. every Context Item is typed, bounded, and attributed;
4. Tool Call/Result Pairing is 100% and Orphans=0;
5. Runtime Truth retention remains 100% across three compactions;
6. DeepSeek keeps complete HTTP/SSE and replay remains Adapter-isolated;
7. `uncached_input_tokens` P50 falls by at least 20%;
8. `input_tokens` P50 falls by at least 15%;
9. cache share after Sample 3 is at least 95%;
10. task completion, verification, Sample Count, and error rate do not regress;
11. architecture ratchet, race, hermetic, live, and documentation gates pass;
12. old context authorities, feature flags, and dual writes are deleted; and
13. raw artifacts, comparisons, and gate evidence prove every claimed benefit.
