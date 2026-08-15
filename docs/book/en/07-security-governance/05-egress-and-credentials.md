---
id: security-egress-credentials
title: Egress, Credentials, and Data Leakage
audience:
  - contributor
  - operator
  - agent
prerequisites:
  - model-credential-lifecycle
  - security-threat-model
code_paths:
  - internal/security/egress
  - internal/security/keyring
  - internal/adapter/provider/httpclient
test_paths:
  - internal/security/egress/gate_test.go
  - internal/security/keyring/store_test.go
  - internal/adapter/provider/httpclient/credentials_test.go
source_of_truth:
  - docs/en/security.md
  - internal/security/egress/gate.go
status: draft
last_verified: null
---

# Egress, Credentials, and Data Leakage

English | [简体中文](../../zh-CN/07-security-governance/05-egress-and-credentials.md)

## Learning Objectives

Trace secret references to governed HTTP headers, understand host-scoped Egress
Approval, and identify model/log/process leakage paths.

## Secret Lifecycle

Tracked configuration stores `CredentialRef`, not values. Values resolve from
an explicitly named environment variable, OS Keyring, or a constrained
resolver source immediately before HTTP construction.

```text
reference -> request-scoped resolve -> authorization header
          -> governed client -> discard
```

Raw values must not enter Prompt, ModelRequest JSON, Event, Receipt, Trace,
command argument, diagnostic dump, or child environment. Redaction is defense
in depth, not permission to record secrets.

## Egress Gate

An enforcing `egress.Gate` is a session-scoped allowlist of normalized Host,
Port, Protocol, HTTP Method, and private-address permissions. It resolves DNS
immediately before connection and pins the approved IP for dialing. Redirects
and CONNECT targets re-enter the Gate.

Process traffic on macOS can reach only the Runtime-owned loopback proxy port.
`exec_command` declares `network_targets`; undeclared targets, private
resolution, metadata addresses, and direct sockets fail closed. Linux remains
process-network-denied until its namespace proxy bridge is available.

Every decision emits the same bounded `egress.Receipt` shape for Process, Web,
Provider, and MCP paths.

## Leakage Channels and Mitigations

| Channel | Control |
| --- | --- |
| model Context/Tool Result | source budgets, no secret source, bounded output |
| HTTP request/redirect | credential resolver, Egress Gate, safe redirect |
| process environment | allowlist/sanitization, no inherited secret by default |
| logs/errors/dumps | structured redaction and bounded sanitized messages |
| memory/snapshot | explicit write, secret heuristic, access/retention |
| local service | loopback default, authenticated reviewed gateway externally |

Native search and remote content remain untrusted after retrieval. TLS protects
transport to a host; it does not make returned instructions authoritative.

## Incident Response

Stop affected execution/distribution, revoke and rotate credentials, preserve
sanitized evidence, determine Event/Receipt/log scope, invalidate affected
artifacts, fix the control, and add regression coverage. Never paste the leaked
value into an issue.

## Verification

```bash
go test ./internal/security/egress ./internal/security/keyring
go test ./internal/adapter/provider/httpclient -run 'Test.*Credential'
make secret-leak-test
```

## Review Questions

1. Why must redirects be checked again?
2. Which data structures should contain references but never values?
3. Why does TLS not solve prompt injection from remote content?

## Further Reading

- [Credential References and Secret Lifecycle](../04-model-and-provider/05-credential-lifecycle.md)

## Sources and Verification

| Item | Value |
| --- | --- |
| Catalog ID | `security-egress-credentials` |
| Status | `draft` |
| Last verified | Not yet verified |
