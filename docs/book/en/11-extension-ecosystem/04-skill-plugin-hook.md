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
test_paths:
  - internal/adapter/skill/resolver_test.go
  - internal/adapter/plugin/lifecycle_test.go
  - internal/adapter/hooks/hooks_test.go
source_of_truth:
  - internal/adapter/skill/catalog.go
  - internal/adapter/plugin/registry.go
  - internal/adapter/hooks/manager.go
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

## Failure Boundaries

- Symlink traversal, malformed state/lock, and digest drift fail.
- Skill dependency cycle/conflict is explicit.
- Plugin wildcards, tamper, authority drift, and unknown publisher fail.
- Security revoke cancels in-flight Plugin calls.
- Hook timeout/process failure cannot silently allow.
- No extension receives raw secrets by default.

## Tests and Verification

```bash
go test ./internal/adapter/skill
go test ./internal/adapter/plugin
go test ./internal/adapter/hooks
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
