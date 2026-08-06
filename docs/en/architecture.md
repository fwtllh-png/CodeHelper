# Architecture and Security

[简体中文](../zh-CN/architecture.md) | English

## Architectural Goal

CodeHelper keeps one authoritative execution runtime while allowing multiple
presentation and integration surfaces. Hosts submit operations and observe
events; they do not reimplement the agent loop or execute privileged tools
directly.

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
| Observability | `internal/observability` | usage, traces, diagnostics, verification, telemetry |
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

## Turn Data Flow

1. A host validates user input and submits an operation.
2. The application resolves session, thread, workspace, and policy state.
3. Prompt context assembles repository map, pinned files, working set, evidence,
   policy, and compacted history.
4. A provider streams model output and tool requests.
5. Tool requests enter the registry and guard.
6. The guard evaluates mode, posture, permissions, constitution, approvals, and
   sandbox requirements.
7. Mutating tools write through journaled/transactional adapters.
8. Verification collects diagnostics or executes repository checks.
9. Events and receipts are persisted and projected to each host.

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

### MCP

External servers expose tools through a protocol adapter. Health, timeouts,
circuit breakers, and tool binding isolation prevent one failing server from
silently corrupting all tool availability.

### Skills

Skills package instructions and resources. Discovery, manifests, locks, and
enablement state make the selected content explicit.

### Plugins

Plugins add executable capabilities. Registry signatures, publisher trust,
immutable staging, receipts, enablement, rollback, and revocation form the
activation chain.

### Hooks

Hooks observe or gate lifecycle points. They must remain bounded and cannot
become an alternate unguarded execution path.

## Orchestration Architecture

- **Task repository:** durable state and leases.
- **Worker:** claims and executes eligible tasks.
- **Automation:** schedules task templates.
- **Workflow:** validated DAG/IR with node checkpoints.
- **Lane:** manages an inline or tmux-backed worker process.
- **Fleet ledger:** read model for distributed run/task events.
- **Subagent:** bounded child runtime with depth, budget, and workspace isolation.

All orchestration eventually returns to runtime/tool/security boundaries.

## Change Checklist

Before modifying architecture:

1. Identify the owning layer.
2. Check whether protocol or persisted state changes.
3. Define cancellation, retry, and terminal behavior.
4. Preserve the guard and sandbox path.
5. Add contract or architecture tests.
6. Update both language versions of affected documentation.
7. Regenerate protocol/compatibility artifacts when required.
