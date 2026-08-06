---
id: lab-new-capability
title: Design and Verify a New Agent Capability
audience:
  - contributor
  - agent
prerequisites:
  - practice-reading-codebase
  - extension-failure-isolation
code_paths:
  - internal/runtime
  - internal/adapter
  - internal/security
test_paths:
  - internal/host/cli/architecture_test.go
  - internal/runtime/app/wire/sandbox_architecture_test.go
source_of_truth:
  - AGENTS.md
  - docs/en/architecture.md
status: verified
last_verified: 2026-08-06
---

# Design and Verify a New Agent Capability

English | [简体中文](../../zh-CN/13-hands-on-labs/10-new-agent-capability.md)

## Goal and Prerequisites

Produce an implementation-ready capability design and a narrow Fixture proof
while preserving Runtime ownership and governance.

## Procedure

1. State user outcome, non-goals, and measurable acceptance.
2. Identify Host input, Protocol operation/events, Engine behavior, Adapter,
   durable state, and projection changes.
3. Enumerate capabilities, Resources, credentials, approvals, sandbox, egress,
   cancellation, retry, and failure isolation.
4. Define compatibility, rollout, observability, and rollback.
5. Implement the smallest vertical Fixture path.
6. Add unit/contract/integration tests and bilingual documentation.
7. Run focused checks, then broaden by blast radius.

## Design Artifact

Complete this table before implementation:

| Concern | Required decision |
| --- | --- |
| authority | who grants, narrows, expires, and revokes it |
| identity | Operation/Turn/Tool/Task/generation/idempotency keys |
| effects | Resources, external systems, partial-effect boundary |
| recovery | replay versus retry, checkpoint, rollback, reconciliation |
| isolation | process/network/sandbox/budget/catalog source |
| compatibility | additive/breaking shape and generated artifacts |
| observability | Events, Receipt, Trace, Usage, stable errors |
| evidence | unit, contract, integration, fault, Race, platform gates |

## Vertical Proof and Adversarial Controls

Implement one success path using a Fixture, then prove:

- malformed and unknown input fail before effects;
- Policy/Approval/Sandbox cannot be bypassed;
- cancellation releases every owned resource;
- stale generation/identity is fenced;
- retry is refused after an unknown partial effect;
- restart reconstructs state without rerunning completed work;
- unavailable optional capability degrades explicitly;
- logs, Events, and fixtures contain no raw secret.

## Go/No-Go Review

Go requires named owners, bounded inputs/outputs, stable identity/error
contracts, deterministic Fixture evidence, rollback/recovery, bilingual docs,
and all required gates. No-Go applies when authority is ambiguous, a live
dependency is the only test, platform security is unknown, or rollback relies
on deleting user state.

## Expected Result

The proof has one owner per concern, no Host-side business loop, no Guard
bypass, stable identities/errors, replay behavior, and explicit verification
evidence.

## Failure Diagnosis

If authority is ambiguous, stop and redesign. If the feature cannot be tested
without a live dependency, introduce a contract Fixture. If rollback cannot
restore durable compatibility, rollout is incomplete.

## Cleanup

Remove experimental registrations/artifacts or retain them only as reviewed
production code with Catalog/docs updates.

## Verification

```bash
go test ./path/to/changed/package
make docs-check book-check
git diff --check
```

## Review Questions

1. Where is the new authority introduced?
2. How is replay/idempotency preserved?
3. What evidence permits rollout?
4. Which failure makes the capability an immediate No-Go?

## Sources and Verification

| Item | Value |
| --- | --- |
| Catalog ID | `lab-new-capability` |
| Status | `verified` |
