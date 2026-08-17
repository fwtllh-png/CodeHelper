# Configuration Reference

[简体中文](../zh-CN/configuration.md) | English

## Resolution Order

Configuration is resolved from lowest to highest precedence:

```text
built-in defaults < TOML file < CODEHELPER_* environment < command flags
```

Use these commands to prevent guesswork:

```bash
codehelper config check --config ./codehelper.toml
codehelper config show --config ./codehelper.toml
codehelper config explain execution.verify.mode --config ./codehelper.toml
```

`config show` includes provenance, allowing an operator to see which source won
for each field. `config explain` returns the resolved value, built-in default,
winning source, risk level, and behavioral impact.

## Configuration Profiles

Profiles control how many defaults are written explicitly; they do not create
different runtime defaults.

| Profile | Intended use |
| --- | --- |
| `minimal` | first successful local or fixture turn |
| `recommended` | normal repository work with context, journal, and soft verification |
| `advanced` | review of limits, workers, subagents, and context budgets |

Render a profile or select one during setup:

```bash
codehelper config profile --profile minimal --workspace . --data-dir .codehelper
codehelper setup --workspace . --profile recommended --interactive
```

## Complete Practical Example

```toml
[runtime]
operation_buffer = 64
event_history = 256
subscriber_buffer = 64

[state]
data_dir = ".codehelper"
busy_timeout = "5s"
event_retention = 1000000

[memory]
enabled = false
path = ".codehelper/memory"

[telemetry]
log_level = "info"

[credential]
kind = "env"                 # env | file | keyring
name = "OPENAI_API_KEY"      # reference, never the secret value

[execution]
provider = "openai"
model = "gpt-4.1"
protocol = "openai_chat"
mode = "act"                 # plan | act | operate
workspace = "."
tools = true
max_output_tokens = 4096
max_steps = 256
timeout = "2m"
idle_timeout = "1m"
max_concurrent = 8
rate_limit = 0
budget_tokens = 0            # 0 means no additional token cap
budget_usd = 0               # 0 means no additional cost cap
reasoning_effort = ""        # empty = adaptive; explicit value fixes the effort
native_search = false

[execution.verify]
mode = "soft"                # off | soft | hard
scope = "diagnostics"        # diagnostics | repository | affected
on_failure = "fail"          # fail | revert
command = ""                 # optional explicit repository check
max_repair_steps = 1
timeout = "2m"

[execution.journal]
durable = true
recover_on_start = true

[execution.subagent]
delegation = "explicit"      # disabled | explicit | adaptive
max_depth = 5
max_parallel = 4
max_resident = 8
max_total = 16
max_steps = 24
max_tokens = 0
max_cost_usd = 0
wall_time = "5m"
workspace = "auto"           # auto | read_only | worktree | same_workspace_serialized

[execution.worker]
enabled = false
max_parallel = 2
max_attempts = 1
lease = "30s"
claim_interval = "1s"
automation_interval = "30s"
retry_backoff = "15s"
retry_backoff_max = "10m"
max_tokens = 0
max_cost_usd = 0

[context.index]
enabled = true
max_file_bytes = 1048576
max_files = 20000

[context.repo_map]
enabled = true
max_bytes = 8192
max_directories = 24

[context.working_set]
enabled = true
max_entries = 16
max_bytes = 8192

[context.evidence]
enabled = true
max_entries = 24
max_bytes = 4096

[context.coding_policy]
enabled = true

[context.compact]
auto_compact_tokens = 0 # 0 derives 65% of the active model window
scope = "total" # or "body_after_prefix"
summary_max_bytes = 8192
max_digest_entries = 120

[route]
lock = false

[route.plan]
provider = "openai"
model = "gpt-4.1-mini"

[route.vision]
provider = "openai-responses"
model = "gpt-4.1"

[route.subquery]
provider = "openai"
model = "gpt-4.1-mini"

[web]
search_backend = "duckduckgo"
```

An empty `reasoning_effort` selects low for read-only work, medium for normal
coding, and high for complex architecture/debugging; repair failures escalate
one level. An explicit effort remains fixed and must be advertised by every
configured route; unsupported values fail before provider I/O.
`max_output_tokens` is always the hard request ceiling; stage reserves never
raise an explicit value.

`delegation = "explicit"` exposes `spawn_agent` only with user, developer,
Skill, or internal system authority. `adaptive` also allows the model to
delegate independent work when parallel benefit exceeds coordination cost.
`disabled` hides Agent lifecycle tools from the model while preserving
internally authorized durable worker execution.

`spawn_agent` captures parent context from the active Runtime Turn. Its
`context_mode` is `task_capsule` by default; `fresh` inherits no parent context,
`last_n_turns` adds up to `context_turns` complete recent Tool exchanges, and
`full` requires explicit authority or role policy. The Tool returns a
`context_receipt` with source identity, inclusion/exclusion reasons, byte and
token budgets, and a SHA-256 digest. Legacy `fork_context` and `parent_context`
arguments are not accepted.

The Agent Tree, Mailbox, Result, and Budget Ledger live in the Workspace state
store. Every Agent has a canonical path and CAS revision; terminal Result and
Completion Outbox commit atomically. Completion notifies the parent
automatically, while `wait_agent` synchronizes on the same fact. Stable Message
IDs and `Receive/Ack` preserve unacknowledged delivery across restart.
`max_parallel` limits active children, `max_resident` includes completed
children whose Result or worktree is still retained, and `max_total` bounds all
spawns for the durable tree, including closed agents. Depth, token, and cost
admission also apply to nested agents; a child may narrow but never expand its
parent's budget.

Child authority can only narrow the active Session Profile. Effective posture
follows `never < suggest < auto < bypass`, writable Tools are the intersection
of the parent catalog and the child Role Contract, and read-only Roles use
`never`. Under `suggest`, a child Approval appears in the Host with its Agent
path and Role. The Host submits the original Request ID through the parent
Session; Runtime routes the decision to the authoritative child Thread and
preserves pending approvals across restart. A denial produces a structured
Problem and an `approval_denied` Tool Result for the child.

Bundled `openai-responses` routes that advertise incremental transport keep a
Provider-owned WebSocket per sticky session key. The first sample sends the
complete logical input; a later sample sends `previous_response_id` and only
new input when all non-input properties are unchanged and the logical input
strictly extends the committed chain. Any route, property, compaction, retry,
resume, connection, or response-state uncertainty sends a complete request.
Other providers continue using their existing HTTP transport.

Incremental transport always uses `store=false`. Response state is retained
only in the active connection and is discarded on failure or idle timeout.
Usage events persist request byte counts plus SHA-256 logical and transport
digests, never prompt content. Request-byte savings are transport evidence and
are not reported as token savings.

`execution.max_steps` is a hard safety and cost bound, not a target. The
default is 256 for coding Turns and the supported range is 1-1000. For budgets
of at least 64 steps, Runtime injects one convergence warning with 16-32 steps
remaining so the model can finish a coherent result or declare concrete
`pending_actions` instead of being terminated without notice.

The Agent also tracks consecutive samples without structured progress. This is
not a 16-step execution limit. At 16 no-progress samples it asks the model to
converge, at 32 it limits further exploration while retaining exact file reads,
workspace mutations, quality checks, plan updates, and completion, and at 48 it
stops the Turn with an explicit no-progress error. Any mutation, completed plan
step, verification, or completion resets the counter immediately. Answer and
Plan Turns additionally count newly read paths and new evidence; Operation
Turns count successful business Tool results. The progress state is durable
across Runtime recovery.

Unknown TOML fields are rejected. This is intentional: a misspelled safety or
budget field must not look configured while having no effect.

## Provider and Model Selection

The primary route is `[execution].provider` plus `[execution].model`.
`protocol` describes the wire format, such as:

- `openai_chat`
- `openai_responses`
- `anthropic`

Use the catalog rather than guessing identifiers:

```bash
codehelper model list
codehelper model resolve --provider openai --model gpt-4.1
codehelper model resolve --provider openai-responses --model gpt-4.1
```

Provider IDs can be distinct even when model IDs are equal. Always specify the
provider when resolution would otherwise be ambiguous.

Purpose routes support `plan`, `vision`, and `subquery`. With `route.lock=true`,
a missing purpose route is an error instead of falling back to the primary
execution route.

## Credentials

`[credential]` contains only a reference:

| Kind | Meaning | Recommendation |
| --- | --- | --- |
| `env` | `name` is an environment variable | easiest local/CI setup |
| `file` | `name` is a protected file path | use mode `0600` and external secret management |
| `keyring` | `name` is an OS keyring key | preferred interactive desktop setup |

Manage references with `codehelper auth`. Secret values are redacted from normal
configuration and diagnostic output.

## Modes, Postures, and Verification

Mode is configured in TOML; posture is a host/command decision supplied through
flags. They are independent.

Verification modes:

- `off`: do not run the verify gate;
- `soft`: collect and report a verdict without turning a successful edit into a
  failed turn unless the Turn contract requires verification;
- `hard`: enforce the verdict after repair attempts are exhausted.

`workspace_change` and explicit completion contracts always require verification.
When their no-progress repair budget is exhausted, the Turn becomes `blocked`
and its journal remains as a resumable draft regardless of the configured mode.

Scopes:

- `diagnostics`: language/editor diagnostics;
- `repository`: detected or explicit repository command;
- `affected`: checks inferred from changed paths.

Affected verification supports Go package tests, JavaScript/TypeScript test
files, Python pytest files, and Rust Cargo tests. Build or lock manifest changes
widen to the language repository suite. Every `turn.verification` check includes
the derivation reason for its command. Paths with no supported topology remain
explicitly `unavailable`; they never become a silent green pass.

Use `hard` only after the repository's verification command is deterministic in
the intended sandbox.

## Post-Edit Diagnostics

`[diagnostics.commands]` maps lowercase file extensions to trusted, PATH-resolved
executables. Each command must include `{path}` in its bounded argument list.
These commands execute after guarded file edits, so repository-local config
cannot define them unless that config was explicitly trusted.

## State and Persistence

The default user data directory is `~/.codehelper/v1`. A workspace can use a
dedicated `--data-dir` or `[state].data_dir`.

Persistent state includes runtime projections, events, content-addressed data,
session metadata, usage, and journals. The current project is pre-release; do
not depend on compatibility with development databases from older commits.

`execution.journal.durable=true` stores enough edit evidence to recover
interrupted turns. Keep it enabled for real repositories.

## Observation Capture and Export

`[telemetry].log_level` controls structured Runtime logging. Observation
capture and OTLP export are process-level operational settings rather than TOML
fields:

| Variable | Values | Behavior |
| --- | --- | --- |
| `CODEHELPER_OBSERVATION_CAPTURE` | `off`, `metadata`, `failure`, `full` | controls durable Observation admission; default is `metadata` |
| `CODEHELPER_OTEL_EXPORTER` | `memory`, `off`, `http/protobuf`, `grpc` | selects the Observation OTLP projector |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | collector endpoint | enables standard OTLP endpoint configuration |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | `grpc` or `http/protobuf` | standard protocol selection when an endpoint is set |

`metadata` keeps redacted summaries and drops raw payloads. `failure` keeps
eligible redacted payloads only for failure-like observations. `full` still
rejects Credential and Restricted payloads and redacts configured secrets,
state paths, and config paths before any Journal or CAS write.

Observation payload retention is separate from `[state].event_retention`.
`event_retention` bounds Runtime Event history by count. Observation payload
references use internal time-based classes: audit and diagnostic default to 30
days, sensitive to 24 hours, and ephemeral to one hour. Startup pruning
releases expired references and deletes only unreferenced CAS objects.

If remote OTLP construction fails, Runtime falls back to the in-memory
projector. Observation queue, Journal, or exporter failures are reported as
observation health and never change the business result of a Turn.

## Context Controls

- `index`: bounded symbol extraction for code navigation;
- `repo_map`: bounded repository structure and entry-point summary;
- `working_set`: paths touched or pinned during the session;
- `evidence`: facts established, risks, and unverified changes;
- `coding_policy`: stable method instructions;
- `compact`: when and how long history is summarized.

`search_definition` and `search_references` accept an optional `path`, `line`,
and `character`. With a concrete position they prefer the injected language
provider; otherwise, or when that provider is unavailable, they use the lexical
repository index. Results always identify `resolution`, `source`, `version`,
and `confidence`; a failed semantic attempt also records the fallback reason.

Disabling context sections can reduce input size but also increases repeated
search and weakens continuity. Adjust bounds before disabling them.

## Environment Variables

Common overrides:

| Variable family | Fields |
| --- | --- |
| `CODEHELPER_PROVIDER`, `CODEHELPER_MODEL`, `CODEHELPER_PROTOCOL` | primary model route |
| `CODEHELPER_MODE`, `CODEHELPER_WORKSPACE`, `CODEHELPER_TOOLS` | execution behavior |
| `CODEHELPER_MAX_*`, `CODEHELPER_TIMEOUT`, `CODEHELPER_IDLE_TIMEOUT` | limits |
| `CODEHELPER_BUDGET_TOKENS`, `CODEHELPER_BUDGET_USD` | session budgets |
| `CODEHELPER_SUBAGENT_*` | delegation, tree limits, child budgets, wall time, and workspace strategy |
| `CODEHELPER_VERIFY_*` | verification behavior |
| `CODEHELPER_STATE_*` | persistence |
| `CODEHELPER_LOG_LEVEL` | structured Runtime logging |
| `CODEHELPER_OBSERVATION_CAPTURE`, `CODEHELPER_OTEL_EXPORTER`, `OTEL_EXPORTER_OTLP_*` | observation capture and OTLP export |
| `CODEHELPER_CREDENTIAL_KIND`, `CODEHELPER_CREDENTIAL_NAME` | secret reference |
| `CODEHELPER_INDEX_*`, `CODEHELPER_REPO_MAP_*` | repository context |
| `CODEHELPER_WORKING_SET_*`, `CODEHELPER_EVIDENCE_*` | session context |
| `CODEHELPER_COMPACT_*` | token-window compaction |
| `CODEHELPER_VISION_*`, `CODEHELPER_WEB_SEARCH_BACKEND` | specialized adapters |

The authoritative list is the environment application block in
`internal/config/environment.go`.

## Configuration Hygiene

- Commit a safe example, not a credential-bearing personal config.
- Prefer workspace-relative paths in shared examples.
- Keep production credentials outside the repository.
- Run `config check` after every config change.
- Use `config show` when a flag, environment variable, and TOML appear to
  disagree.
- Treat `bypass`, hard verification, worker enablement, and custom shell
  commands as review-required changes.
