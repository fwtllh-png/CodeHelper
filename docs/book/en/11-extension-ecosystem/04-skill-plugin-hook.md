---
id: extension-skill-plugin-hook
title: Building Skills, Plugins, and Hooks
audience:
  - contributor
  - operator
  - agent
prerequisites:
  - extension-tool
code_paths:
  - internal/adapter/skill
  - internal/adapter/plugin
  - internal/adapter/hooks
  - internal/runtime/extension
  - internal/runtime/app/extension
  - internal/persist/extensioncontrol
  - internal/persist/extensionlifecycle
  - internal/persist/extensionplan
test_paths:
  - internal/adapter/skill/resolver_test.go
  - internal/adapter/plugin/lifecycle_test.go
  - internal/adapter/hooks/hooks_test.go
  - internal/runtime/extension/plan_test.go
  - internal/runtime/app/extension/lifecycle_test.go
source_of_truth:
  - internal/adapter/skill/catalog.go
  - internal/adapter/plugin/registry.go
  - internal/adapter/hooks/manager.go
  - internal/runtime/extension/registry.go
  - internal/runtime/extension/plan.go
  - internal/runtime/extension/lifecycle.go
status: draft
last_verified: null
---

# Building Skills, Plugins, and Hooks

English | [简体中文](../../zh-CN/11-extension-ecosystem/04-skill-plugin-hook.md)

## Learning Objectives

Choose the correct extension mechanism and understand its authority, lifecycle,
integrity, and execution limits.

## Mechanisms

| Mechanism | Purpose | Execution |
| --- | --- | --- |
| Skill | versioned instructions/dependency plan | loaded into bounded context |
| Plugin | signed executable capability bundle | verified sandboxed process |
| Hook | lifecycle/permission callback | bounded configured process |

Skills use strict manifests, deterministic root precedence, compatibility and
dependency resolution, lockfile digests, enable state, cycle/conflict checks,
and authority verification. Loading a Skill does not grant Tool capability.

Plugins bind trust receipts to bundle hash, capability inventory, publisher,
version, and generation. Distribution verifies signatures, replay/downgrade,
safe archive extraction, staged atomic activation, rollback, revoke, and
in-flight cancellation. Loader snapshots executable content and runs sandboxed.

Hooks run at defined lifecycle points through Manager/Adapter. Permission hooks
may allow, deny, or ask, but Guard revalidates updated inputs and fails closed
on hook errors or unresolved ask.

## Shared Runtime Extension Contract

Skills, Plugins, Hooks, Memory, Dynamic Tools, and MCP adapt into one typed
Runtime extension core. A contributor declares identity, phase, capabilities,
failure policy, timeout, and output budget. Registration validates the
contract, seals an immutable Registry, and invokes contributors with explicit
capabilities rather than construction state or a private Tool Registry.

Source resolution produces a deterministic, digested Plan bound to the current
permission digest. Every process, connection, Hook, subscription, lease, timer,
and Tool registration carries an `EffectOwner`:

```text
Extension + Source + Plan Revision + Generation + Capability + Effect Kind
```

Lifecycle transitions persist redacted Receipts. Disable drains owned Effects;
security revoke or quarantine fences stale generations. Restart reconciliation
uses durable Plan, lifecycle, and control stores rather than scanning files and
guessing what was active.

## Authority Comparison

| Mechanism | Authority source | Revocation effect |
| --- | --- | --- |
| Skill | manifest, lock digest, enabled state, optional Plugin authority | blocks future load/context injection |
| Plugin | signed release + trust receipt + active generation | cancels in-flight calls and fences loaded handles |
| Hook | local strict config + lifecycle point + failure policy | removes future callbacks; cancellation kills process tree |

A Skill influences model instructions, not execution authority. A Plugin may
contribute executable Tools only through Registry/Guard. A Hook advises or
filters a lifecycle point; it cannot commit Tool effects itself.

## Installation and Activation

Plugin distribution separates verification from activation:

```text
fetch bounded signed index/artifact
 -> verify publisher/version/digest
 -> safely extract and stage content-addressed bundle
 -> review capability inventory
 -> atomically activate generation
 -> drain old generation
```

Rollback activates a previously verified record; it does not skip current
revocation or authority checks. Concurrent updates converge on one generation.

Skill resolution similarly separates discovery, dependency resolution,
lockfile verification, and bounded load. Root precedence is deterministic so a
lower-precedence candidate cannot shadow a governed one.

Hook failure policy is event-specific: observer failures may be audited and
fail open, while message/tool/permission gates fail closed. Output and
environment are bounded and sanitized in both cases.

Plugin and Skill CLI, ACP `extension/list`/`extension/control`, and the VS Code
Extensions view submit to this same control plane. Mutations are idempotent by
Operation ID and persist prepare/commit receipts. Hosts project Runtime-owned
state; they do not implement extension lifecycle.

## Failure Boundaries

- Symlink traversal, malformed state/lock, and digest drift fail.
- Skill dependency cycle/conflict is explicit.
- Plugin wildcards, tamper, authority drift, and unknown publisher fail.
- Security revoke cancels in-flight Plugin calls.
- Hook timeout/process failure cannot silently allow.
- No extension receives raw secrets by default.
- An Extension generation cannot retain Effects after revoke/quarantine.
- Reusing an Operation ID with different content fails closed.

## Tests and Verification

```bash
go test ./internal/adapter/skill
go test ./internal/adapter/plugin
go test ./internal/adapter/hooks
go test ./internal/runtime/extension ./internal/runtime/app/extension
go test ./internal/persist/extensioncontrol ./internal/persist/extensionlifecycle
go test ./internal/persist/extensionplan
```

## Hands-On Lab

Create a local Skill with one dependency, a no-op Hook, and a Fixture Plugin
manifest. Compare what each is allowed to influence.

## Review Questions

1. When should a feature be a Skill rather than Plugin?
2. What does a Plugin trust receipt bind?
3. Why does Guard revalidate hook-updated input?
4. How does Skill authority differ from Plugin authority?
5. Why are Plugin verification and activation separate phases?

## Further Reading

- [Extension Failure and Isolation](./06-failure-isolation.md)

## Sources and Verification

| Item | Value |
| --- | --- |
| Catalog ID | `extension-skill-plugin-hook` |
| Status | `draft` |
| Last verified | Not yet verified |
