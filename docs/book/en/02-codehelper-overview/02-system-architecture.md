---
id: overview-system-architecture
title: CodeHelper System Architecture
audience:
  - learner
  - contributor
  - agent
prerequisites:
  - agent-why-governed-runtime
  - overview-positioning
code_paths:
  - cmd/codehelper
  - internal/host
  - internal/runtime
  - internal/adapter
  - internal/security
  - internal/orchestration
  - internal/persist
  - internal/observability
  - internal/platform
test_paths:
  - internal/host/cli/architecture_test.go
  - internal/runtime/protocol/schema_test.go
source_of_truth:
  - docs/en/architecture.md
  - docs/protocol/runtime-protocol.schema.json
status: verified
last_verified: 2026-08-06
---

# CodeHelper System Architecture

English | [简体中文](../../zh-CN/02-codehelper-overview/02-system-architecture.md)

## Learning Objectives

You will learn the major layers, dependency direction, Runtime protocol, and
the reason CodeHelper uses one execution core behind many Hosts.

## Prerequisites

Read [Why Agents Need a Governed Runtime](../01-agent-engineering/05-why-governed-runtime.md).

## Problem Background

A coding Agent quickly accumulates interfaces: CLI for scripts, TUI for
interactive work, editor UI, HTTP clients, background workers, and child
Agents. If each interface constructs providers, runs tools, or stores state
independently, behavior diverges precisely where consistency matters most:
security, cancellation, recovery, and evidence.

Architecture must therefore describe authority and dependency direction, not
only a directory tree.

## Three Views of the Same System

Do not try to understand CodeHelper from one package diagram alone:

| View | Question | Follow this path |
| --- | --- | --- |
| Control | Who may request and authorize an effect? | Host → Operation → Runtime → Guard → Platform |
| Data | Where do facts flow and persist? | Provider/Tool → Event/Receipt → Event Log/SQLite → Projection |
| Construction | Who selects concrete implementations? | config → `runtime/app/wire` → interfaces/adapters |

A correct change must fit all three views. For example, a new Tool needs a
construction path, a control path through Guard, and an evidence path through
Events/receipts. Implementing only the executor is incomplete.

## Core Concepts

- A **Host** translates user/client I/O into Operations and Events.
- The **Runtime** owns lifecycle, Agent execution, and shared contracts.
- An **Adapter** connects external models, tools, and extension ecosystems.
- **Security** decides and enforces authority.
- **Persistence** and **observability** preserve facts and evidence.
- **Orchestration** schedules durable or multi-step execution.
- **Platform** implements OS-specific process and sandbox behavior.

## CodeHelper Design

```mermaid
flowchart TB
    subgraph Hosts
      CLI
      TUI
      VSC[VS Code]
      API[ACP]
    end
    P[Runtime Protocol]
    APP[Application Runtime]
    ENG[Agent Engine]
    ADP[Provider / Tool / Extension Adapters]
    SEC[Policy / Guard / Sandbox]
    ORC[Task / Workflow / Fleet]
    PER[SQLite / Events / CAS / Journal]
    OBS[Trace / Usage / Verify]
    PLT[Process / OS]

    Hosts --> P --> APP --> ENG
    ENG --> ADP
    ADP --> SEC --> PLT
    APP --> PER
    ENG --> OBS
    ORC --> APP
```

The important constraint is that arrows do not mean “anything can call
anything below.” For example, a Host may import protocol and application
facades, but not concrete tool, provider, Agent Engine, or sandbox
implementations.

## Package Layers

| Layer | Path | Owns |
| --- | --- | --- |
| Entry | `cmd/codehelper` | process startup and CLI root |
| Hosts | `internal/host` | presentation and transport adapters |
| Runtime | `internal/runtime` | protocol, app lifecycle, Agent loop, wiring |
| Adapters | `internal/adapter` | providers, tools, MCP, skills, plugins, hooks |
| Security | `internal/security` | policy, permissions, constitution, sandbox |
| Orchestration | `internal/orchestration` | task, worker, automation, workflow, fleet |
| Persistence | `internal/persist` | SQLite, event log, CAS, sessions, journal |
| Observability | `internal/observability` | traces, usage, diagnostics, verification |
| Platform | `internal/platform` | process and OS integration |
| VS Code | `extensions/vscode` | editor presentation and ACP client |

## Hard Dependency Rules

1. `runtime/protocol` is independent of implementation packages.
2. Hosts submit Operations; they do not execute providers or tools.
3. Business turn loops remain in `runtime/agent`.
4. Construction remains in `runtime/app/wire`.
5. Consequential tools pass through `adapter/tool/guard`.
6. UI state is a projection, not runtime truth.
7. Persistent effects are transactional or journaled at the owner boundary.

`internal/host/cli/architecture_test.go` parses imports and fails when the CLI
depends directly on execution implementations. Architecture is therefore a
tested property.

## Runtime Protocol

The transport-neutral vocabulary is:

```text
Operation -> ordered Events -> Projection
                     \-> Receipt
```

`internal/runtime/protocol` defines tagged unions for Operations and Events.
The generated schema in `docs/protocol/runtime-protocol.schema.json` is shared
with ACP and VS Code generation. ACP wraps the same model rather than defining
a separate runtime.

An Event carries sequence, Operation, Thread, Turn, and Item identity. This
allows a Host to reconnect, replay from a cursor, and distinguish output,
tools, approvals, verification, and terminal outcomes.

## Construction Boundary

`runtime/app/wire` is allowed to know concrete implementations. It:

- resolves model routes and credentials;
- creates the tool Registry and Guard;
- assembles prompt context and budgets;
- connects journals, diagnostics, traces, and persistence;
- adapts the Agent Engine to the application Engine interface;
- creates persistent or in-memory Runtime instances.

Keeping construction separate prevents dependency injection logic from
becoming a second business loop.

## Persistence and Orchestration

SQLite stores relational projections; the event log stores ordered facts; CAS
stores immutable payloads; snapshots speed restoration; the workspace journal
records edit before-images.

Tasks, workers, workflows, lanes, fleets, and subagents may create or schedule
work, but ultimately return to the same Runtime, Guard, and sandbox boundaries.
Orchestration is not an exception to governance.

## Tradeoffs and Alternatives

A monolithic package reduces initial ceremony but hides ownership and makes
cycles likely. Independent runtimes per Host simplify local UI code but create
semantic drift. CodeHelper accepts more interfaces and adapters in exchange
for one authority path and testable boundaries.

The protocol also adds versioning work. That cost is deliberate: a visible
contract is safer than undocumented coupling between a terminal, extension,
and execution engine.

## Failure Modes and Security Boundaries

- Host-specific execution would bypass common Guard semantics.
- Provider imports in a Host would make model behavior depend on presentation.
- Business logic in `wire` would be hard to test without constructing the world.
- Treating projection state as truth would lose replay and recovery guarantees.
- Writing directly around persistence owners could separate relational state
  from events or journals.
- An extension adapter that executes directly would become an ungoverned
  control plane.

## Tests and Verification

```bash
go test ./internal/host/cli -run TestCLIDoesNotDependOnExecutionImplementations
go test ./internal/runtime/protocol -run TestTheCommittedSchemaMatchesThisBuild
go test ./internal/runtime/app -run TestRuntimeUnsupportedOperationIsExplicitlyRejected
```

These tests cover import boundaries, generated protocol drift, and explicit
Runtime rejection.

## Hands-On Lab

Trace one path without reading implementation bodies:

1. Find `OperationStartTurn` in `internal/runtime/protocol`.
2. Find `Runtime.Submit` in `internal/runtime/app`.
3. Find `EngineAdapter.StartTurn` in `internal/runtime/app/application.go`.
4. Find `Engine.RunForTurn` in `internal/runtime/agent/engine`.
5. Find `Guard.ExecuteBound` in `internal/adapter/tool/guard`.
6. Find `NewRuntime` calls in `internal/runtime/app/wire`.

Then run the architecture test. The exercise demonstrates that the source tree
encodes the same route described by the diagram.

## Review Questions

1. Why is `wire` allowed to import concrete implementations while Hosts are not?
2. What makes HTTP and ACP two transports rather than two runtimes?
3. Which durable component should own recovery from a half-applied edit?
4. For a new capability, what are its control, data, and construction paths?

## Further Reading

- [Architecture manual](../../../en/architecture.md)
- [Turn lifecycle](./05-turn-lifecycle.md)
- [Guard, Approval, Constitution, and Sandbox](../07-security-governance/03-approval-constitution-sandbox.md)

## Sources and Verification

| Item | Value |
| --- | --- |
| Catalog ID | `overview-system-architecture` |
| Status | `verified` |
| Last verified | 2026-08-06 |
