# Roadmap

[简体中文](../zh-CN/roadmap.md) | English

This roadmap describes desired outcomes. It is not evidence that a capability
has shipped. Current behavior is documented in the other guides and code.

## Product North Star

A developer should be able to give CodeHelper a real repository task and obtain:

1. correct repository orientation;
2. a bounded, inspectable plan;
3. minimal guarded changes;
4. automatic diagnostics and relevant tests;
5. repair or rollback when verification fails;
6. a durable receipt explaining context, actions, cost, and result;
7. the same semantics in terminal, editor, and automation hosts.

## Near-Term: Stable Initial Release

### Documentation and onboarding

- keep English and Chinese documentation at parity;
- provide installable examples for common providers without embedding secrets;
- maintain CLI/config/link drift checks;
- make platform support and failure modes explicit.

### Correctness

- increase affected-test and dependency-impact precision;
- improve verification command discovery;
- make repair budgets and rollback outcomes easier to understand;
- strengthen crash/restart recovery tests.

### Release readiness

- establish a reproducible CLI and VS Code release pipeline;
- publish checksums, SBOM, provenance, compatibility, and rollback instructions;
- validate clean installs across supported targets;
- establish live-model, real-VS-Code, chaos, endurance, and canary release
  gates from maintained product tests and release workflows;
- define public compatibility policy before persisting a second schema version.

## Medium-Term: Coding Intelligence

- richer language-aware symbols beyond lexical extraction;
- cross-file reference and impact graphs;
- better repository build/test topology;
- semantic retrieval where it materially improves evidence;
- explain why a file or test entered the working set.

The objective is fewer irrelevant reads and fewer unverified edits, not a larger
context window.

## Medium-Term: Execution Reliability

- deterministic task recovery and lease observability;
- workflow cancellation, retry, and checkpoint guarantees;
- clearer subagent merge/conflict semantics;
- bounded long-session compaction with quality evaluation;
- resource isolation for concurrent providers, tools, and workers.

The [Runtime Reliability Hardening](./reliability-hardening.md) program tracks
the detailed audit workstreams, priorities, status, and acceptance evidence.

## Medium-Term: Security and Governance

- stronger cross-platform sandbox evidence;
- explicit egress policy and endpoint inventory;
- signed policy/permission distribution for managed environments;
- structured audit export with redaction guarantees;
- plugin/MCP provenance and operator-facing risk reporting;
- credential rotation and revocation workflows.

## Medium-Term: Host Experience

### TUI

- clearer approval, verification, cost, and background-work panels;
- better session navigation and recovery feedback;
- accessibility and keyboard discoverability.

### VS Code

- stable editor context synchronization;
- richer native diff and diagnostic workflows;
- reliable local single-root and multi-root lifecycle;
- managed runtime updates with transparent rollback;
- bounded performance on large workspaces.

### API and ACP

- published contract examples;
- client conformance fixtures;
- backpressure and replay guidance;
- authentication guidance for non-loopback deployments.

## Long-Term: Ecosystem

- documented extension SDKs after contracts stabilize;
- reusable templates for MCP, skills, hooks, and workflow specs;
- signed registry operations and offline mirrors;
- optional enterprise controls without forking the core runtime.

## Explicit Non-Goals

- maximizing the number of built-in tools;
- replacing repository CI;
- hiding unsafe platform limitations;
- a cloud control plane that requires source upload;
- parallel host-specific agent implementations;
- premature backward compatibility for unpublished development data.

## Roadmap Acceptance Rules

A roadmap item is complete only when:

- implementation is reachable from a supported host;
- safety and failure behavior are defined;
- tests cover the contract;
- observability or receipts make the result inspectable;
- English and Chinese documentation are updated;
- release/support claims match dynamic evidence.

## How to Propose Work

Open a design change with:

1. user problem and measurable outcome;
2. owning package and protocol impact;
3. security and persistence impact;
4. cancellation/failure/recovery semantics;
5. test and rollout plan;
6. documentation changes.

Avoid phase-only names such as `phase1` or `p2`; name work after the domain
behavior it delivers.
