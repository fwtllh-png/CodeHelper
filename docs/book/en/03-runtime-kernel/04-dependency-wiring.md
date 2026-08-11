---
id: runtime-wiring
title: Dependency Construction and Capability Wiring
audience:
  - contributor
  - agent
prerequisites:
  - overview-system-architecture
  - runtime-agent-loop
code_paths:
  - internal/runtime/app/wire
test_paths:
  - internal/runtime/app/wire/bootstrap_test.go
  - internal/runtime/app/wire/model_test.go
  - internal/runtime/app/wire/sandbox_architecture_test.go
source_of_truth:
  - internal/runtime/app/wire/runtime.go
  - internal/runtime/app/wire/route.go
status: draft
last_verified: null
---

# Dependency Construction and Capability Wiring

English | [简体中文](../../zh-CN/03-runtime-kernel/04-dependency-wiring.md)

## Learning Objectives

Understand why construction is isolated, how configuration becomes a Runtime,
and which invariants Wiring must establish before a Turn starts.

## Prerequisites

Read [System Architecture](../02-codehelper-overview/02-system-architecture.md)
and [Agent Loop](./03-agent-loop.md).

## Problem Background

Providers, credentials, routes, sandboxes, tools, journals, traces, MCP,
plugins, persistence, and context budgets must be connected consistently.
Constructing them in every Host duplicates policy. Constructing them inside the
Agent loop mixes configuration with behavior.

## Construction Flow

```mermaid
flowchart TD
    C[Validated Config] --> R[Resolve Routes and Credentials]
    C --> S[Probe Sandbox and Workspace]
    C --> P[Build Policy and Constitution]
    R --> E[Provider and Agent Engine]
    S --> G[Tool Registry and Guard]
    P --> G
    G --> E
    C --> D[Persistence / Trace / Journal]
    D --> A[Application Runtime]
    E --> A
    A --> H[Host Facade]
```

## What Wiring Owns

`internal/runtime/app/wire` is the composition root. It may import concrete
implementations to:

- resolve Model Catalog routes and Credential References;
- create Provider HTTP clients or Fixtures;
- build Tool Registry, Guard, Policy, Constitution, and Sandbox Backend;
- assemble stable Prompt Context and partition budgets;
- connect Journal, Diagnostics, Verify, Trace, Usage, and stores;
- initialize MCP, Skill, Plugin, Hook, and Dynamic Tool managers;
- build child runtimes, Worktrees, and background executors;
- choose persistent or in-memory application Runtime.

## Invariants Before Start

Construction fails rather than producing a partially trusted Runtime when:

- route/capability/config is invalid;
- workspace identity cannot be canonicalized;
- required Sandbox cannot be injected;
- persistence recovery fails;
- duplicate executors or Tool identities conflict;
- an extension cannot satisfy its activation contract.

Default Prompt budgets and security defaults live near construction because
they define the Runtime assembled from user configuration.

## Startup Ordering and Ownership Transfer

Construction follows a dependency order, not merely a list of constructors:

1. validate configuration and canonicalize Workspace identity;
2. open durable stores and perform recovery before acceptance;
3. resolve model metadata, routes, limits, and credential references;
4. probe platform/Sandbox capability;
5. build Policy, permissions, Constitution, Registry, and Guard;
6. initialize extensions and reconcile their catalog contributions;
7. connect context, evidence, diagnostics, verification, usage, and trace;
8. create Thread/Engine factories;
9. create Application Runtime and only then expose a Host facade.

Ownership transfers at each successful step. If a later step fails, already
opened stores, transports, extension processes, and background managers must be
closed in reverse order. A constructor that leaks a process on failure violates
the Runtime contract even if no Turn started.

## Capability Provenance

Every enabled capability should answer where the fact came from:

| Fact | Preferred source |
| --- | --- |
| Model supports Tools/Vision | Catalog plus probe observation |
| Credential available | reference resolution at execution boundary |
| Strong Sandbox available | platform Backend probe |
| Tool executable | Registry Descriptor plus dependency health |
| Extension trusted | manifest/signature/authority receipt |
| Persistent state ready | successful open, schema check, and recovery |

Configuration expresses intent; it cannot manufacture environmental capability.

## Code Map

| Concern | Source |
| --- | --- |
| Main assembly | `runtime.go` |
| Route/budget defaults | `route.go`, `routeset.go` |
| Provider construction | `model.go` |
| Persistent Runtime | `persistent.go` |
| Sandbox facts | `sandbox_info.go` |
| MCP/extensions | `mcp.go`, `extensions.go` |
| Child/background work | `childruntime.go`, `background_executors.go` |

## Implementation Walkthrough

The main builder canonicalizes Workspace, builds stores and observability,
loads Constitution, merges Policy rules, creates Registry and Guard, assembles
Prompt Context, then constructs Agent Engines through a Thread Manager.

The application Runtime receives only its `Engine` interface and durable
facilities. Hosts receive the resulting session/facade, not the concrete
Provider or Guard.

## Tradeoffs and Alternatives

A dependency-injection framework could automate graphs but hide ordering and
security decisions. CodeHelper uses explicit Go construction so reviewers can
trace exactly which Backend and Policy enter a Guard.

Large composition roots are harder to read. The response is semantic helper
files, not moving business loops into Wiring.

## Failure Modes and Security Boundaries

- No Host-specific shortcut may construct an unguarded Tool Registry.
- Fixture and live Provider routes must remain explicit.
- Sandbox capability claims come from Backend probing, not configuration text.
- Secret values are resolved at Provider execution boundary, not Prompt setup.
- Child Runtime budgets/depth/workspace must be narrower than parent authority.

## Tests and Verification

```bash
go test ./internal/runtime/app/wire
go test ./internal/host/cli -run TestCLIDoesNotDependOnExecutionImplementations
```

## Hands-On Lab

Start at the CLI `exec` command and trace only constructor calls until
`app.NewRuntime` or `NewRuntimeWithRecovery`. Record where Route, Guard,
Sandbox, Journal, and Prompt Context are attached. No business Turn step should
occur during this trace.

## Review Questions

1. Why may Wiring import concrete implementations while Hosts may not?
2. Which construction failures must prevent Runtime startup?
3. Why should capability probing override configuration claims?
4. Why must the Host facade be exposed last?
5. What resources must be closed when construction fails midway?

## Further Reading

- [Application Runtime](./02-application-runtime.md)
- [Provider adapters and Model Catalog](../04-model-and-provider/02-provider-and-catalog.md)

## Sources and Verification

| Item | Value |
| --- | --- |
| Catalog ID | `runtime-wiring` |
| Status | `draft` |
| Last verified | Not yet verified |
