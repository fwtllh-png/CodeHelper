---
id: practice-test-layers
title: Unit, Contract, Integration, and Electron Tests
audience:
  - contributor
prerequisites:
  - practice-fixtures-smoke
  - host-vscode
code_paths:
  - internal
  - extensions/vscode
test_paths:
  - internal/host/runtimeapi/acp/contract_test.go
  - extensions/vscode/src/test/suite/index.ts
  - extensions/vscode/src/performance/gate.test.ts
source_of_truth:
  - Makefile
  - extensions/vscode/package.json
  - extensions/vscode/scripts/matrix/journeys.mjs
status: draft
last_verified: null
---

# Unit, Contract, Integration, and Electron Tests

English | [简体中文](../../zh-CN/12-engineering-practice/02-test-layers.md)

## Learning Objectives

Select the cheapest test layer that proves a behavior and know when a real
binary, transport, Extension Host, or Electron is required.

| Layer | Proves | Typical command |
| --- | --- | --- |
| Unit/package | local invariants | `go test ./path` |
| Contract | shared runtime behavior through ACP | `make protocol-contract` |
| Binary integration | ACP framing/process lifecycle | `make acp-interop` |
| VS Code static/runtime | TS and real Runtime stdio | `make vscode-check`, `vscode-runtime-integration` |
| Electron | actual local VS Code platform | `make vscode-integration`, Rosetta integration |
| Release matrix | Journey completeness and artifacts | `make vscode-rc` |

Unit tests use fake clocks/backends only at ownership boundaries. ACP contract
tests exercise shared runtime scenarios. Binary tests launch the built artifact.
Electron gates are explicit because they acquire a large external runtime.
The VS Code suite currently has 173 tests; the Runtime integration lane runs
the four cross-process cases instead of accepting their skipped form.

Repository verification is reported through four lanes:

| Lane | Boundary |
| --- | --- |
| Hermetic | no network, live credentials, GUI, or real HOME |
| Platform capability | explicit OS/sandbox prerequisites and unavailable evidence |
| Integration | real binary, ACP, and VS Code Runtime integration |
| Release | cross-build, Race, secrets, benchmark, and packaging |

Unavailable, skipped, failed, and passed remain distinct. Each lane report
preserves command, platform, duration, exit code, status, and reason.

## Risk-to-Test Matrix

| Change risk | Minimum evidence |
| --- | --- |
| pure parser/formatter | table unit tests + malformed boundaries |
| protocol shape | schema/golden + ACP and Host Journey contracts |
| persistence/recovery | restart + corruption/crash-window tests |
| consequential Tool | Guard/Policy/Sandbox + observed effect + rollback |
| concurrency/lease | forced interleaving + Race |
| process/transport | real binary lifecycle and cancellation |
| VS Code trust/context | TS unit + ACP Runtime integration; Electron for platform API |
| VS Code recovery/projection | Runtime artifact tests + Electron Retry/Continue/Plan + Patch resync |
| release/update | artifact-content, digest, install, rollback, revocation |

Test breadth follows changed contracts, not changed line count. A one-line
protocol or security change may require more evidence than a large isolated
refactor.

## Test Doubles and Ownership

Fake a dependency only where its owner exposes a contract. Fake clocks prove
time decisions; fake transports prove decoder logic; neither proves OS process
cleanup or wire framing. Tests should assert the owner-visible outcome and
avoid reproducing implementation logic inside the fake.

Skipped, unavailable, failed, and passed are distinct results. A gate that
requires an external environment records prerequisites and why it did not run.

## Failure Boundaries

- A mocked transport cannot prove binary framing.
- A skipped environment gate is reported, not called passed.
- Generated protocol/compatibility drift fails static checks.
- Electron tests remain outside ordinary `verify`.
- A Remote SSH or Dev Container result cannot substitute for the local-only
  product matrix.

## Native Chat Journey Matrix

Electron ARM64 covers Empty, local Workspace, Forced Colors, Native Runtime,
and local Multi-root. Rosetta x64 covers Native Runtime and Multi-root. The
shared Journey manifest contains 19 automated IDs and one documented manual
Panel-move Journey. Matrix generation fails when an automated ID is missing;
RC fails when a manual Journey is undocumented.

Dynamic evidence includes all native context types, resource navigation,
Light/Dark/High Contrast, forced colors, approximately 200% zoom, IME,
hidden-view resume, streaming cancellation, Retry/Continue, model picker,
Thinking, Tools, Credential validation, approval/verification receipts,
Session lifecycle/search, and three Plan destinations.

## Verification

```bash
go test ./...
make protocol-contract
make test-hermetic
cd extensions/vscode && npm run check && npm test
make vscode-rc
```

## Review Questions

1. When is a contract test stronger than package tests?
2. What requires Electron?
3. Why separate downloaded-runtime gates?
4. How does contract risk determine test breadth?
5. When does a fake become weaker than the behavior being claimed?
6. Why does Journey evidence complement package test counts?

## Sources and Verification

| Item | Value |
| --- | --- |
| Catalog ID | `practice-test-layers` |
| Status | `verified` |
| Last verified | 2026-08-07 |
