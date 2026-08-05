# Product Overview and Positioning

[简体中文](../zh-CN/overview.md) | English

## Definition

CodeHelper is a local coding-agent runtime, not only a chat interface. A user
expresses an engineering goal; the runtime gathers repository evidence, calls a
model, invokes governed tools, asks for approval when required, verifies the
result, and records an auditable event stream.

The product is terminal-first because terminals are portable and automation
friendly. It is not terminal-only: VS Code, HTTP/SSE, ACP, and web surfaces are
hosts over the same runtime.

## Problem It Solves

AI coding tools become difficult to trust when they:

- hide what context was used;
- write files or execute commands outside one policy boundary;
- duplicate business logic in every editor integration;
- lose state after a process restart;
- report success without tests or diagnostics;
- make cost, approvals, and side effects impossible to audit.

CodeHelper addresses these issues with a protocol-centered runtime and explicit
security, persistence, and evidence layers.

## Core Value

### For individual developers

- one local tool for analysis, implementation, review, and repository operations;
- interactive approval and visible diffs before high-risk actions;
- resumable sessions and stable workspace context;
- provider choice without coupling repository logic to one model vendor.

### For teams and platform engineers

- a runtime that can be embedded behind ACP or HTTP/SSE;
- structured events for observability, evaluation, and policy enforcement;
- hermetic fixtures and contract tests for repeatable integration;
- extensibility through MCP, skills, plugins, hooks, and workflows.

### For AI agents

- explicit repository boundaries and package ownership;
- machine-readable protocol and configuration;
- deterministic build and focused test commands;
- durable evidence about prior reads, edits, failures, and verification.

## Capability Model

| Area | Current capability |
| --- | --- |
| Repository context | file search, symbol index, repository map, working set, evidence ledger |
| Agent loop | streaming model calls, tool calls, bounded steps, budgets, compaction |
| Editing | guarded file operations, edit plans, journaled changes, revert support |
| Verification | diagnostics/repository/affected scopes, soft or hard gate, repair budget |
| Security | posture, approvals, workspace permissions, constitution, OS sandbox |
| Persistence | SQLite projections, event log, snapshots, session metadata, journals |
| Orchestration | durable tasks, worker, automations, workflows, lanes, fleet, subagents |
| Ecosystem | model catalog, MCP, skills, plugins, hooks, memory |
| Hosts | CLI, TUI, HTTP/SSE, ACP, Web UI, VS Code |

## Product Boundaries

CodeHelper is not:

- a hosted source-code SaaS;
- an unrestricted shell wrapper;
- a guarantee that model output is correct;
- a replacement for repository CI or code review;
- a claim that every platform has equivalent sandbox strength;
- a compatibility promise for pre-release persisted development data.

Security controls reduce risk; they do not make arbitrary code execution safe.
Users remain responsible for credentials, repository backups, approvals, and
reviewing consequential changes.

## Differentiation

The main differentiator is not the number of tools. It is the consistency of the
execution path:

```text
User or host
  -> Operation
  -> Runtime / agent engine
  -> guarded adapter
  -> policy + approval + journal + sandbox
  -> side effect
  -> Event + receipt + verification evidence
```

Every host should observe the same semantics. An editor extension must not gain
a private file-writing path, and a background worker must not bypass the same
guard used by an interactive turn.

## Maturity

The repository represents the first consolidated implementation baseline.
Before a public stable release, priorities are correctness, documentation,
cross-platform validation, release repeatability, and reducing accidental
complexity. See [Roadmap](./roadmap.md) for outcome-oriented next steps.
