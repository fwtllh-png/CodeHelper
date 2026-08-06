---
id: security-extension-trust
title: Trust for MCP, Skills, Plugins, and Hooks
audience:
  - contributor
  - operator
  - agent
prerequisites:
  - security-threat-model
  - extension-skill-plugin-hook
code_paths:
  - internal/adapter/mcp
  - internal/adapter/skill
  - internal/adapter/plugin
  - internal/adapter/hooks
test_paths:
  - internal/adapter/plugin/trust_test.go
  - internal/adapter/plugin/distribution_test.go
  - internal/adapter/hooks/hooks_test.go
source_of_truth:
  - internal/adapter/plugin/trust.go
  - internal/adapter/plugin/distribution.go
status: verified
last_verified: 2026-08-06
---

# Trust for MCP, Skills, Plugins, and Hooks

English | [简体中文](../../zh-CN/07-security-governance/06-extension-trust.md)

## Learning Objectives

Distinguish extension content, code, transport, and lifecycle trust; understand
capability receipts, signed distribution, revocation, and Hook failure policy.

## Different Extension Risks

| Mechanism | Main risk | Required controls |
| --- | --- | --- |
| MCP | remote/local server Tools and responses | endpoint/process review, OAuth/env allowlist, Guard, health isolation |
| Skill | instructions/resources interpreted by model | source/version lock, bounded load, prompt-injection review |
| Plugin | executable bundle and declared capabilities | publisher/signature/digest, capability receipt, Sandbox, revocation |
| Hook | lifecycle subprocess that can deny/update/env | strict config, timeout, sanitized env, bounded output, audit |

All extension-provided Tools still enter Registry/Catalog and execute through
Guard. "Trusted extension" is not direct execution authority.

## Plugin Trust Chain

```text
publisher allowlist
 -> signed registry release and monotonic generation
 -> artifact/manifest digest
 -> safe bounded archive extraction
 -> immutable content-addressed staging
 -> content + capability + generation Receipt
 -> sandboxed load
 -> Catalog authority
```

Capability Inventory names Tools, filesystem roots, network hosts, and process
permission. Wildcards are rejected. Any content, capability, or generation
drift invalidates prior review. Rollback selects a previously verified artifact;
security revocation cancels in-flight authority rather than merely hiding UI.

## Hooks and Permission Hooks

Observer Hooks may fail open only where configured and are audited. Hooks that
gate Message submission, Tool calls, or Permission must fail according to their
declared policy. Deny wins; argument updates return to Guard for full
revalidation; Allow cannot bypass a hard Policy denial.

Hook output is bounded and not copied wholesale into audit. Environments are
sanitized, timeout/cancellation kills the process tree, and Shell environment
audits names rather than values.

## MCP and Skill Boundaries

MCP schema/capability changes cause Catalog reconciliation and stale bindings
are fenced. Circuit/health state isolates failing servers. Skill locks make
resolution reproducible, but Skill text remains untrusted model Context and
cannot grant Tool authority.

## Verification

```bash
go test ./internal/adapter/plugin ./internal/adapter/hooks
go test ./internal/adapter/mcp ./internal/adapter/skill
```

## Review Questions

1. Why must capability drift invalidate a Plugin receipt?
2. Why does Hook Allow not override Policy Deny?
3. Why is a locked Skill reproducible but still untrusted?

## Further Reading

- [Skills, Plugins, and Hooks](../11-extension-ecosystem/04-skill-plugin-hook.md)
