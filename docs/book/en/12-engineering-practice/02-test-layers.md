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
  - internal/host/runtimeapi/http/contract_test.go
  - extensions/vscode/src/runtime/integration.test.ts
source_of_truth:
  - Makefile
  - extensions/vscode/package.json
status: verified
last_verified: 2026-08-06
---

# Unit, Contract, Integration, and Electron Tests

English | [简体中文](../../zh-CN/12-engineering-practice/02-test-layers.md)

## Learning Objectives

Select the cheapest test layer that proves a behavior and know when a real
binary, transport, Extension Host, or Electron is required.

| Layer | Proves | Typical command |
| --- | --- | --- |
| Unit/package | local invariants | `go test ./path` |
| Contract | same behavior across transports | `make protocol-contract` |
| Binary integration | framing/process lifecycle | `make api-contract`, `make acp-interop` |
| VS Code static/runtime | TS and real Runtime stdio | `make vscode-check`, `vscode-runtime-integration` |
| Electron/remote | actual VS Code platform | `make vscode-integration` and matrix targets |

Unit tests use fake clocks/backends only at ownership boundaries. Contract tests
share scenarios across HTTP and ACP. Binary tests launch the built artifact.
Electron, Remote SSH, and Dev Container gates are explicit because they acquire
large external runtimes.

## Risk-to-Test Matrix

| Change risk | Minimum evidence |
| --- | --- |
| pure parser/formatter | table unit tests + malformed boundaries |
| protocol shape | schema/golden + HTTP/ACP shared contract |
| persistence/recovery | restart + corruption/crash-window tests |
| consequential Tool | Guard/Policy/Sandbox + observed effect + rollback |
| concurrency/lease | forced interleaving + Race |
| process/transport | real binary lifecycle and cancellation |
| VS Code trust/context | TS unit + ACP Runtime integration; Electron for platform API |
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

## Verification

```bash
go test ./...
make protocol-contract
cd extensions/vscode && npm run check && npm test
```

## Review Questions

1. When is a contract test stronger than package tests?
2. What requires Electron?
3. Why separate downloaded-runtime gates?
4. How does contract risk determine test breadth?
5. When does a fake become weaker than the behavior being claimed?

## Sources and Verification

| Item | Value |
| --- | --- |
| Catalog ID | `practice-test-layers` |
| Status | `verified` |
| Last verified | 2026-08-06 |
