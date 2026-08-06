---
id: extension-mcp
title: Integrating an MCP Server
audience:
  - contributor
  - operator
prerequisites:
  - extension-tool
  - model-credential-lifecycle
code_paths:
  - internal/adapter/mcp
  - internal/adapter/tool/mcp
  - internal/runtime/app/wire
test_paths:
  - internal/adapter/mcp/contract/fixture_test.go
  - internal/adapter/mcp/stdio_integration_test.go
  - internal/adapter/mcp/http_integration_test.go
source_of_truth:
  - internal/adapter/mcp/config.go
  - internal/adapter/mcp/pool.go
status: verified
last_verified: 2026-08-06
---

# Integrating an MCP Server

English | [简体中文](../../zh-CN/11-extension-ecosystem/03-integrating-mcp.md)

## Learning Objectives

Configure stdio or HTTP MCP, discovery, catalog reconciliation, auth,
permissions, health isolation, and shutdown.

## Integration Flow

```mermaid
flowchart LR
    C[Strict MCP config] --> P[Pool]
    P --> T[Stdio / HTTP transport]
    T --> D[Initialize + paginated discovery]
    D --> R[Tool Catalog reconcile]
    R --> G[Normal Tool Guard]
```

Config is versioned and strict. Server names become canonical model Tool names.
Stdio uses controlled process environment and group cleanup. HTTP supports
streamable HTTP and legacy SSE, bounded timeouts, session reconnect, auth
references, and optional OAuth PKCE.

Connection initializes capabilities, discovers paginated Tools/resources/
prompts, and normalizes calls. Pool reloads only changed servers, isolates
health/circuit state, publishes catalog notifications, and shuts all transports
down. Discovered Tools enter the normal Registry and Guard with configured
permission profiles; MCP is not a policy bypass.

## Source-Scoped Reconciliation

Each MCP server is a distinct catalog source. Discovery produces a server
generation containing normalized descriptors and connection identity.
Reconciliation replaces only that source, detects name collisions, and fences
Executors whose connection or revision changed.

```text
notification/config change
 -> discover candidate generation
 -> validate complete source catalog
 -> atomically reconcile source
 -> publish catalog change
```

An incomplete or stale refresh quarantines that source rather than partially
mixing old and new descriptors. Large catalogs remain deferred until Tool
search materializes selected entries.

## Connection and Call Lifecycles

Initialize/discovery/probe calls may be replayable. Business Tool calls are not
replayed merely because an HTTP session became stale. Transport reconnect first
establishes a new generation, then the caller receives an explicit outcome.

Circuit state is per server. Tool-level `isError` is a business result and does
not by itself trip transport health. Timeout, protocol loss, or process death
updates health, revokes visibility when required, and triggers bounded probes.

Shutdown removes observers, cancels requests, closes HTTP streams or the whole
stdio process group, and waits within a deadline.

## Failure Boundaries

- Secret environment and dynamic resource escalation are rejected.
- Optional unsupported capability does not break supported discovery.
- Circuit breaker does not replay business calls.
- One failed server does not hide healthy servers.
- Stale HTTP session reconnects without duplicating the request.
- Catalog refresh is generation/revision bound.

## Tests and Verification

```bash
go test ./internal/adapter/mcp/...
go test ./internal/adapter/tool/mcp
go test ./internal/runtime/app/wire -run TestMCP
```

## Hands-On Lab

Connect the Hermetic stdio fixture, discover a Tool, execute it through Guard,
trigger a catalog notification, then verify revision-safe refresh.

## Review Questions

1. Why do MCP Tools still pass through Guard?
2. What does Pool isolate?
3. Why must reconnect avoid replaying a business call?
4. Why is catalog reconciliation scoped by server source?
5. Which errors should affect the circuit breaker?

## Further Reading

- [Extension Failure and Isolation](./06-failure-isolation.md)

## Sources and Verification

| Item | Value |
| --- | --- |
| Catalog ID | `extension-mcp` |
| Status | `verified` |
| Last verified | 2026-08-06 |
