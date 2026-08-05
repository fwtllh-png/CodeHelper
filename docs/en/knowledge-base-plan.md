# Agent Engineering Knowledge Documentation Plan

[简体中文](../zh-CN/knowledge-base-plan.md) | English

## 1. Document Status

This document defines the goals, information architecture, chapter contract,
delivery sequence, and acceptance criteria for the CodeHelper knowledge book.
It is a construction plan, not a claim that the listed chapters already exist.
Current code, tests, and the product manuals under `docs/en` remain the source
of truth for shipped behavior.

## 2. Objective

CodeHelper documentation should answer more than "which command do I run?" It
should explain:

- what problems Agents solve and why they need a runtime;
- how models, context, tools, state, orchestration, and security cooperate;
- why CodeHelper uses its current architecture instead of alternatives;
- where each module starts and ends in code;
- how tests, traces, failures, and experiments demonstrate the design;
- how a learner can progress from user to Agent runtime contributor.

The result should read like an executable engineering book. Readers can follow
it in order, enter the source at any chapter, run commands, modify a module,
and observe the consequences.

## 3. Audiences

| Audience | Main question | Recommended material |
| --- | --- | --- |
| Agent learner | How is an Agent different from a chatbot? | fundamentals, architecture, first turn |
| Application developer | How do I integrate models and tools reliably? | provider, context, tools, security |
| Runtime developer | How do I implement protocols, state, and recovery? | runtime, persistence, orchestration |
| Platform/security engineer | How are side effects governed? | guard, policy, sandbox, journal |
| CodeHelper contributor | Which package should change and how is it verified? | implementation maps, tests, extension labs |
| Coding Agent | Which contracts and files are authoritative? | metadata, code paths, tests, invariants |

## 4. Documentation Layers

The knowledge book complements rather than replaces the current manuals.

### 4.1 Product Manuals

Location: `docs/en`, `docs/zh-CN`

They answer what the current release does and how to use, configure, develop,
and troubleshoot it:

- quick start, configuration, and usage;
- architecture and security overviews;
- VS Code, local development, roadmap, and troubleshooting;
- contributor and coding-agent repository rules.

Manuals should remain concise, searchable, and task-oriented.

### 4.2 Agent Engineering Knowledge Book

Planned location:

```text
docs/book/
├── en/
└── zh-CN/
```

The book moves from technical foundations into CodeHelper design and source
implementation. It may link to the manuals but should not duplicate field and
command references.

### 4.3 Machine-Readable References

These include:

- the runtime protocol JSON Schema;
- the VS Code compatibility contract;
- CLI `--help`;
- config validation and schema behavior;
- generated TypeScript protocol types;
- fixtures, golden files, and release evidence.

The book explains these contracts; machine-readable artifacts remain the
verifiable source of truth.

## 5. Proposed Volumes

### Part I: Entering Agent Engineering

1. From chatbot to Agent
2. LLMs, tokens, context windows, and sampling
3. ReAct, planning, tool calling, and reflection
4. Boundaries between Agents, workflows, and automation
5. Why Agents need a governed runtime

### Part II: Understanding CodeHelper

1. Positioning, value, and non-goals
2. One runtime and many hosts
3. Package ownership and dependency direction
4. Operation, Event, Receipt, and Projection
5. The complete lifecycle of one turn
6. The first data flow from CLI to model to tool

### Part III: Runtime Kernel

1. `protocol`: stable data contracts
2. `app`: sessions, operations, and projections
3. `agent`: the model/tool execution loop
4. `wire`: dependency construction
5. Streaming, cancellation, and error taxonomy
6. Resume, recovery, and idempotency boundaries

### Part IV: Models and Providers

1. Chat Completion and Responses protocols
2. Provider adapters, model catalog, and wire IDs
3. Capability negotiation and route resolution
4. Streaming, reasoning, tool calls, and usage
5. Credential references and secret lifecycle
6. Retries, rate limits, timeouts, and failure classes

### Part V: Context Engineering

1. Prompts, messages, and context
2. Workspaces, repository indexes, and editor context
3. Context sources, priority, and lifecycle
4. Token budgets, compaction, and information loss
5. Memory, snapshots, and recovery
6. Evaluating context quality

### Part VI: Tools and Governed Execution

1. Tool schemas, registry, and dynamic catalog
2. File, shell, and Agent tools
3. The Tool Guard pipeline
4. Edit plans, journals, and receipts
5. Verification gates and evidence
6. Feeding tool failures back to the model

### Part VII: Security and Governance

1. Agent runtime threat model
2. Mode, posture, policy, and permission
3. Approval and constitution
4. OS sandbox and process isolation
5. Egress, credentials, and data leakage
6. Trust for MCP, skills, plugins, and hooks
7. Fail-closed behavior and platform claims

### Part VIII: State and Observability

1. Why durable state is required
2. SQLite schema, event log, and projections
3. Sessions, snapshots, CAS, and workspace journal
4. Traces, spans, usage, and cost
5. Diagnostics, maturity, and verification
6. Reconstructing a failed run

### Part IX: Tasks and Orchestration

1. Tasks, workers, and executors
2. Leases, heartbeats, retries, and idempotency
3. Automation and workflows
4. Checkpoints and recovery
5. Lanes, fleets, and scheduling
6. Subagents, worktrees, and topology

### Part X: Hosts and Protocols

1. CLI and machine-readable output
2. TUI state projection
3. HTTP/SSE runtime API
4. ACP stdio and editor interoperability
5. Web control surface
6. VS Code context bridge, trust, and compatibility

### Part XI: Extension Ecosystem

1. Adding a provider
2. Adding a governed tool
3. Integrating an MCP server
4. Building skills, plugins, and hooks
5. Adding a host without duplicating the runtime
6. Extension failures and isolation

### Part XII: Agent Engineering Practice

1. Hermetic fixtures and live-provider smoke tests
2. Unit, contract, integration, and Electron tests
3. Concurrency tests, race detection, and deterministic synchronization
4. Benchmarks, performance budgets, and regressions
5. Cross-platform builds, sandbox differences, and capability probing
6. VSIX, SBOM, provenance, and release evidence
7. Reading and changing a large Agent codebase

### Part XIII: Hands-On Labs

1. Build and trace the first Agent turn
2. Observe streaming events with a fixture
3. Implement a provider adapter
4. Implement a tool that passes through Guard
5. Exercise approval and denial
6. Build a recoverable workflow
7. Debug worker leases and retries
8. Complete a VS Code feature end to end
9. Investigate a failure from traces
10. Design and verify a new Agent capability

## 6. Module Coverage Map

| Book topic | Main code area |
| --- | --- |
| Runtime protocol | `internal/runtime/protocol` |
| Application runtime | `internal/runtime/app` |
| Agent loop | `internal/runtime/agent` |
| Dependency wiring | `internal/runtime/app/wire` |
| Providers, models, tools | `internal/adapter` |
| Policy and sandbox | `internal/security` |
| Tasks and workflows | `internal/orchestration` |
| Durable state | `internal/persist` |
| Traces, usage, verification | `internal/observability` |
| CLI, TUI, API hosts | `internal/host` |
| OS and processes | `internal/platform` |
| VS Code | `extensions/vscode` |

Every major package should eventually have a design walkthrough, an execution
diagram, and a reproducible verification path.

## 7. Chapter Contract

Each chapter follows the same structure:

1. learning objectives;
2. prerequisites;
3. problem background;
4. core concepts;
5. CodeHelper design;
6. execution flow and diagrams;
7. key code map;
8. implementation details;
9. tradeoffs and alternatives;
10. failure modes and security boundaries;
11. tests and verification;
12. hands-on lab;
13. review questions;
14. further reading;
15. sources of truth and verification status.

Machine-readable metadata appears at the top:

```yaml
---
id: runtime-operation-lifecycle
title: The Lifecycle of an Operation
audience:
  - learner
  - contributor
  - agent
prerequisites:
  - agent-runtime-overview
code_paths:
  - internal/runtime/protocol
  - internal/runtime/app
test_paths:
  - internal/runtime/app/*_test.go
source_of_truth:
  - docs/protocol/runtime-protocol.schema.json
status: planned
last_verified: null
---
```

## 8. Writing Principles

- **Outside in:** begin with observable behavior, then enter contracts, state,
  and implementation.
- **Beginner to advanced:** establish vocabulary and mental models before
  concurrency, recovery, and security details.
- **Concepts connect to code:** important claims link to source, tests, or
  machine-readable contracts.
- **Explain why:** include constraints, tradeoffs, and rejected alternatives.
- **Reproducible labs:** state commands, fixtures, expected results, and cleanup.
- **Separate facts from plans:** mark unshipped behavior as planned.
- **Do not reproduce source:** quote only the minimum needed for explanation.
- **Secure by default:** examples contain no real secret and do not bypass hard
  controls.
- **Equal bilingual quality:** both languages carry the same facts, structure,
  and acceptance criteria.

## 9. Diagram System

Prefer version-controlled Mermaid for:

- C4 context, container, and component views;
- package dependencies;
- turn, tool, approval, and resume sequences;
- operation, task, and workflow state machines;
- provider, context, and tool data flows;
- the layered security control stack;
- SQLite, event, and projection relationships;
- worker, lane, fleet, and subagent topology.

Every diagram requires a textual explanation. Complex subjects use an overview
plus focused diagrams instead of one unreadable canvas.

## 10. Bilingual Strategy

- `docs/book/en` and `docs/book/zh-CN` use identical relative paths and IDs.
- A behavior change may not update only one language.
- The glossary records the canonical English term, preferred Chinese
  translation, and ambiguous terms to avoid.
- Code identifiers, protocol names, and CLI flags remain unchanged.
- Translation preserves technical meaning rather than sentence-by-sentence
  mechanics.
- Checks reject missing mirrors, mismatched IDs, and navigation drift.

## 11. Automation and Quality Gates

Build `make book-check` incrementally on top of `make docs-check`:

1. validate Markdown and image links;
2. enforce recursive bilingual mirrors;
3. validate front matter and chapter IDs;
4. verify `code_paths`, `test_paths`, and `source_of_truth`;
5. compare navigation order with files;
6. reject removed documents and legacy branding;
7. parse Mermaid blocks;
8. require automation for commands marked verified;
9. detect key-shaped secrets;
10. detect explicit drift between verified chapters and implementation.

`book-check` ultimately joins `make verify`; expensive executable labs remain a
separate gate.

## 12. Delivery Stages

### Stage 0: Standards and Skeleton

- create `docs/book/en` and `docs/book/zh-CN`;
- define templates, front matter schema, glossary, and navigation;
- add recursive bilingual checks;
- establish published, draft, and planned status.

Acceptance: missing chapters are visible but cannot be mistaken for delivered
content; the bilingual skeleton passes automated checks.

### Stage 1: Complete Introductory Path

Deliver these chapters first:

1. Why a governed coding-agent runtime is needed
2. CodeHelper system architecture
3. How one Agent turn runs
4. How models, context, and tools cooperate
5. Guard, approval, and sandbox
6. Run and trace the first real task from source

Acceptance: a new reader can build the correct mental model and finish the
first lab without reading the codebase in advance.

### Stage 2: Runtime Core

- protocol, app, agent, and wire;
- providers and models;
- context engineering;
- tools, guard, and verification;
- core sequence and state diagrams.

Acceptance: each core package has design, implementation, test, and lab entry
points.

### Stage 3: Persistence and Orchestration

- SQLite, events, sessions, snapshots, and journal;
- tasks, workers, automation, and workflows;
- lanes, fleets, subagents, and worktrees;
- recovery, retry, and idempotency cases.

Acceptance: readers can explain and verify the complete lifecycle of durable
background work.

### Stage 4: Hosts and Ecosystem

- CLI, TUI, HTTP/SSE, ACP, Web, and VS Code;
- MCP, skills, plugins, and hooks;
- provider, tool, and host extension tutorials.

Acceptance: readers can add an extension without violating runtime boundaries.

### Stage 5: Engineering Practice and Labs

- testing, benchmarks, security, and release engineering;
- systematic hands-on labs;
- real failure investigations and debugging paths;
- learning routes and review questions.

Acceptance: every lab has prerequisites, expected results, failure diagnosis,
and cleanup, and is reproducible on supported platforms.

### Stage 6: Continuous Governance

- map module owners to chapters;
- add documentation impact to review checklists;
- verify documentation facts before release;
- inspect code links, commands, and screenshots for drift;
- adjust reading paths from reader feedback.

## 13. Chapter Definition of Done

A chapter becomes `verified` only when:

- English and Chinese versions both exist;
- objectives and prerequisites are explicit;
- core claims reference current source or contracts;
- important flows have text and, where useful, diagrams;
- the chapter covers success and at least one failure mode;
- verification commands were run and platform limits recorded;
- code paths, test paths, and links pass checks;
- roadmap items are not presented as shipped behavior;
- no real secret, personal absolute path, or unreproducible dependency exists;
- someone familiar with the module, or an Agent grounded in the code, reviewed
  it.

## 14. Maintenance Rules

- Update the related chapter in the same change as a module contract.
- Repair code maps after refactors; do not retain stale paths.
- After public release, compatibility changes update both background and
  migration guidance.
- Preserve historical design only when it explains a current constraint; do
  not restore chronological implementation RFCs.
- When documentation conflicts with code, investigate from code and tests
  rather than assuming either side is correct.
- An Agent reads the chapter's declared sources of truth before editing it.

## 15. Non-Goals

The book will not:

- become an encyclopedia of all LLM research;
- reproduce third-party official documentation;
- replace explanation with large source dumps;
- promise unimplemented product capability;
- record low-value development chronology merely to increase length;
- replace tests, schemas, or CLI help.

## 16. Risks and Controls

| Risk | Control |
| --- | --- |
| Documentation drifts as code grows | metadata, path checks, change checklist |
| Book duplicates product manuals | explicit teaching versus task-reference boundary |
| Languages diverge | mirror checks, shared IDs, same-change updates |
| Chapters discuss concepts without implementation | required code maps, tests, labs |
| Chapters discuss code without foundations | required background, tradeoffs, further reading |
| Diagrams become unmaintainable | Mermaid source, layered views, text explanations |
| Examples encourage unsafe execution | fixtures, least privilege, platform boundaries |

## 17. Next Step

After this plan is approved, execute Stage 0 first: commit the book directories,
chapter template, glossary, navigation, and initial `book-check`. Then deliver
the six Stage 1 chapters as the first complete learning path. Continue with the
runtime core, persistence and orchestration, hosts and ecosystem, and finally
engineering practice instead of creating many empty or unverifiable chapters
at once.
