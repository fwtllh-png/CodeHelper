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
3. ACP stdio and editor interoperability
4. VS Code context bridge, trust, and compatibility

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

Status: completed.

- create `docs/book/en` and `docs/book/zh-CN`;
- define templates, front matter schema, glossary, and navigation;
- add recursive bilingual checks;
- establish planned, draft, and verified status.

Acceptance: missing chapters are visible but cannot be mistaken for delivered
content; the bilingual skeleton passes automated checks.

Delivered artifacts:

- `docs/book/catalog.json` as the structure and status source of truth;
- bilingual book entry points, generated navigation, templates, and glossary;
- `docs/book/schema/chapter.schema.json`;
- `make book-navigation` and `make book-check`;
- recursive bilingual mirror validation in `make docs-check`.

### Stage 1: Complete Introductory Path

Status: completed.

Deliver these chapters first:

1. Why a governed coding-agent runtime is needed
2. CodeHelper system architecture
3. How one Agent turn runs
4. How models, context, and tools cooperate
5. Guard, approval, and sandbox
6. Run and trace the first real task from source

Acceptance: a new reader can build the correct mental model and finish the
first lab without reading the codebase in advance.

Delivered as six verified bilingual chapters covering the governed Runtime
motivation, system architecture, Turn lifecycle, Model/Context/Tool
collaboration, security controls, and a Hermetic first-Turn lab.

Quality pass completed on 2026-08-06: Part 1 now provides the full conceptual
path from chatbot to feedback-loop Agent, LLM/token/context limits,
ReAct/planning/tool/reflection control, Agent versus Workflow/Automation
selection, and governed Runtime synthesis. Every chapter includes source
anchors, failure analysis, review questions, and executable verification.

Part 2 quality pass completed on 2026-08-06: the CodeHelper overview now forms
a continuous path through positioning and non-goals, architecture views,
package ownership, Runtime vocabulary, Turn lifecycle, and Model/Context/Tool
collaboration. Missing chapters were delivered and existing chapters gained
explicit control, data, construction, waiting-state, and snapshot invariants.

Part 3 quality pass completed on 2026-08-06: Runtime Kernel chapters now make
protocol compatibility, application linearization/locking, Agent iteration
commit semantics, startup ownership transfer, streaming delivery and retry,
and crash-window recovery rules explicit. Verification links point to
delivered chapters and executable tests rather than planned placeholders.

Part 4 quality pass completed on 2026-08-06: Model and Provider chapters now
cover cross-protocol semantic mapping, opaque replay data, Catalog identity and
metadata provenance, asymmetric capability evidence, Tool fragment assembly,
Usage subset accounting, exact credential exposure boundaries, and phase-based
retry/health behavior. Stale Fixture and credential claims were corrected.

Part 5 quality pass completed on 2026-08-06: Context Engineering chapters now
separate Message role from authority, stored/rendered/sampled state, Index/
Editor/Tool freshness clocks, Evidence invalidation, capacity and information
loss classes, Memory/Snapshot retention, and controlled quality experiments.
Diagnostics connect Context symptoms to receipts rather than prompt intuition.

Part 6 quality pass completed on 2026-08-06: Tool and controlled-execution
chapters now define Descriptor contracts, Catalog authority layers, Resource/
effect envelopes, Guard trust transitions, write crash windows, evidence
strength, Verification status, and the no-side-effect proof required for model
failure feedback. Stale verification commands were corrected.

Part 7 quality pass completed on 2026-08-06: Security and Governance is now a
complete seven-chapter bilingual part covering the Runtime threat model,
Mode/Posture/Policy, Approval and Constitution, OS process isolation, Egress
and credentials, extension supply-chain trust, and testable fail-closed
platform claims. The previous single-chapter gap has been removed.

Part 8 quality pass completed on 2026-08-06: State and Observability chapters
now define record authority and lifetime, acceptance versus completion,
SQLite/Event Log reservation and reconciliation, CAS/Journal crash windows,
signal cardinality and clock semantics, Verification invalidation, and an
evidence-first failure investigation worksheet.

Part 9 quality pass completed on 2026-08-06: Tasks and Orchestration chapters
now separate Task/Attempt/Turn identities, formalize lease fencing and
effect-specific retry safety, define Automation slot and DAG wave semantics,
document checkpoint commit windows, separate Lane/Fleet control and evidence,
and model child-to-parent merge as guarded two-phase integration.

Part 10 quality pass completed on 2026-08-06: Hosts and Protocols chapters now
define CLI output-channel contracts, TUI reducer and reconstruction
invariants, HTTP admission/idempotency and SSE replay-to-live handoff, ACP
connection ordering, browser/deployment trust boundaries, and VS Code
Supervisor, Cursor, multi-root Workspace, and compatibility recovery.

Part 11 quality pass completed on 2026-08-06: Extension Ecosystem chapters now
define Provider route and stream commit boundaries, Tool generation binding,
MCP source-scoped reconciliation, distinct Skill/Plugin/Hook authorities,
Host extension ownership tests, and a unified lifecycle covering activation,
drain, revocation, quarantine, recovery, and failure-domain isolation.

Part 12 quality pass completed on 2026-08-06: Agent Engineering Practice now
defines Fixture fidelity and smoke interpretation, risk-driven test layers,
concurrency linearization and cleanup evidence, comparable performance
measurement and budget governance, buildable/runnable/supported platform
claims, an artifact attestation chain, and a hypothesis/evidence-led change
workflow for human and Agent contributors.

Part 13 quality pass completed on 2026-08-06: all ten Hands-On Labs now use
current commands and test names, explicit temporary-state isolation, evidence
worksheets, adversarial controls, crash-window or authority matrices, measurable
completion gates, failure diagnosis, and cleanup checks. The sequence forms a
network-free path from a first Turn through Provider, Tool, governance,
recovery, orchestration, VS Code, incident reconstruction, and capability
rollout design.

### Stage 2: Runtime Core

Status: complete. All 24 bilingual chapters across Runtime Kernel, Model and
Provider, Context Engineering, and Tool/Execution are verified.

- protocol, app, agent, and wire;
- providers and models;
- context engineering;
- tools, guard, and verification;
- core sequence and state diagrams.

Acceptance: each core package has design, implementation, test, and lab entry
points.

### Stage 3: Persistence and Orchestration

Status: complete. All 12 bilingual chapters across State and Observability and
Tasks and Orchestration are verified.

- SQLite, events, sessions, snapshots, and journal;
- tasks, workers, automation, and workflows;
- lanes, fleets, subagents, and worktrees;
- recovery, retry, and idempotency cases.

Acceptance: readers can explain and verify the complete lifecycle of durable
background work.

### Stage 4: Hosts and Ecosystem

Status: complete. All 10 bilingual chapters across Hosts and Protocols and the
Extension Ecosystem are verified.

- CLI, TUI, ACP, and VS Code;
- MCP, skills, plugins, and hooks;
- provider, tool, and host extension tutorials.

Acceptance: readers can add an extension without violating runtime boundaries.

### Stage 5: Engineering Practice and Labs

Status: complete. Seven bilingual engineering-practice chapters and all ten
bilingual hands-on labs are verified.

- testing, benchmarks, security, and release engineering;
- systematic hands-on labs;
- real failure investigations and debugging paths;
- learning routes and review questions.

Acceptance: every lab has prerequisites, expected results, failure diagnosis,
and cleanup, and is reproducible on supported platforms.

### Stage 6: Continuous Governance

Status: complete. Documentation maintenance is now an executable repository
contract rather than an editorial convention.

- `docs/book/governance.json` maps 13 source domains and all delivered chapters
  to maintainers, freshness policy, release facts, screenshots, and link
  exceptions;
- `.github/CODEOWNERS` is generated from that registry and checked for drift;
- the PR impact gate maps changed source/test/fact paths to affected chapter
  IDs and requires same-PR English and Chinese updates or a reviewed no-impact
  rationale;
- `make release-fact-check` verifies documentation, book metadata, runtime help,
  protocol schema, and compatibility facts before release;
- weekly governance checks enforce source/verification drift, the 180-day
  freshness SLA, screenshot digests, and external links;
- a dedicated reader feedback form and monthly navigation review close the
  loop on factual, reproducibility, prerequisite, translation, and reading-path
  problems.

Acceptance: ownership and exceptions are tracked, factual source changes cannot
silently bypass documentation review, and release claims have reproducible
commands.

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

## 17. Ongoing Operation

Stages 0 through 6 are complete. The knowledge system now operates through
normal PR impact review, weekly drift checks, release fact verification, and a
monthly reader-feedback/navigation pass. Future stages should be opened only
for a new learning or governance capability, not for routine maintenance.
