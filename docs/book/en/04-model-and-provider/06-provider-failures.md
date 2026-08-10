---
id: model-provider-failures
title: Retries, Rate Limits, Timeouts, and Failures
audience:
  - contributor
  - operator
  - agent
prerequisites:
  - model-wire-protocols
  - runtime-stream-cancel-errors
code_paths:
  - internal/adapter/provider/httpclient
  - internal/runtime/agent/engine
test_paths:
  - internal/adapter/provider/httpclient/client_test.go
  - internal/adapter/provider/fault_injection_test.go
  - internal/runtime/agent/engine/engine_test.go
source_of_truth:
  - internal/adapter/provider/httpclient/client.go
  - internal/runtime/protocol/problem.go
status: draft
last_verified: null
---

# Retries, Rate Limits, Timeouts, and Failures

English | [简体中文](../../zh-CN/04-model-and-provider/06-provider-failures.md)

## Learning Objectives

Understand Provider failure phases, conservative retry rules, local rate
limiting, timeout propagation, and sanitized error mapping.

## Prerequisites

Read [Wire Protocols](./01-wire-protocols.md) and
[Streaming, Cancellation, and Errors](../03-runtime-kernel/05-streaming-cancellation-errors.md).

## Failure Phases

```mermaid
flowchart LR
    A[Acquire concurrency/rate token] --> B[Encode and send]
    B --> C[Headers/status]
    C --> D[SSE stream]
    D --> E[Normalized Events]
```

Failure before meaningful stream data may be retryable when the request is
idempotent. After text, Tool Call, or other meaningful data, retry can duplicate
visible output or effects and is refused.

## Phase and Retry Matrix

| Phase/failure | Retry condition | Public classification |
| --- | --- | --- |
| local request validation | never | invalid argument |
| concurrency/rate wait | caller Context still live | cancellation/deadline or continue |
| credential resolution | never automatically | unavailable, non-retryable |
| DNS/TLS/connect | only classified transient transport errors | unavailable |
| HTTP 408/425/429/5xx | idempotent request, attempts remain | unavailable + rate metadata |
| permanent 4xx | never | invalid argument |
| successful headers, decoder creation fails | no blind retry | protocol/provider failure |
| stream idle timeout | sample fails | unavailable |
| abrupt/malformed stream | no replay after stream begins | provider failure |
| explicit cancellation | never | canceled |

`ModelRequest.Idempotent` is a prerequisite for multiple HTTP attempts. The
client also derives a request-scoped idempotency key from encoded content and a
sequence; this assists a Provider but does not make arbitrary remote behavior
transactional.

## Controls

- concurrency semaphore bounds in-flight requests;
- local rate limiter spaces calls;
- Context deadline covers waiting, headers, and streaming;
- retry policy handles selected transport/status failures with bounded delay;
- Retry-After and cancellation affect waiting;
- response/body/SSE limits bound remote input.

Provider errors are mapped to machine Problems without exposing Authorization,
raw body secrets, or unrestricted filesystem detail.

## Retry Timing and Health

`Retry-After` may be seconds or an HTTP date. Delay is capped and jittered;
Context cancellation interrupts both server-directed and local backoff.
Rate-limit headers are preserved as structured metadata so Hosts can explain
waiting without parsing prose.

The HTTP client records active requests, consecutive failures, last sanitized
error, and health time. Health is observation for operators/routing policy; it
does not authorize automatic switching to a Route with different capability,
endpoint, or credentials.

An idle timeout starts after the Stream is returned and bounds a blocked
`Recv`. On timeout the request is canceled before parser close to avoid racing
decoder state.

## Code Map

| Concern | Source |
| --- | --- |
| HTTP lifecycle | `httpclient/client.go` |
| SSE parsing | `provider/sse.go` |
| Fault fixtures | `provider/fault_injection_test.go` |
| Engine retry boundary | `agent/engine/engine.go` |
| Public Problems | `runtime/protocol/problem.go` |

## Tradeoffs and Alternatives

Aggressive retry improves success rate for transient failures but increases
latency, cost, and duplicate-risk. No retry is simple but fragile before any
effect. CodeHelper retries only bounded, classified, pre-meaningful failures.

Client-side rate limiting cannot know all Provider quotas, but it prevents one
Runtime from creating avoidable bursts.

## Failure Modes and Security Boundaries

- Deadline while waiting for headers cancels the request.
- Non-retryable 4xx returns immediately.
- 429/selected 5xx follow bounded policy.
- Malformed or oversized SSE fails decoding.
- Cancellation interrupts backoff and rate wait.
- Redirect and debug behavior cannot forward/leak credentials unsafely.
- Partial Stream failure is terminal for that sample.

## Tests and Verification

```bash
go test ./internal/adapter/provider/httpclient
go test ./internal/adapter/provider -run TestFaultInjectionSSEDisconnect
go test ./internal/runtime/agent/engine -run TestEngineRetriesOnlyBeforeMeaningfulStreamData
```

## Hands-On Lab

Read the HTTP client tests for 429, retry delay, timeout-before-headers, and
partial stream. Build a table with phase, retryable status, reason, and
observable Problem.

## Review Questions

1. What makes a Provider failure safe to retry?
2. Why must cancellation interrupt rate-limit and backoff waits?
3. Why should raw response bodies not become public errors?
4. Why does an idempotency key not make every Provider request safe to retry?
5. Why must health observation not silently choose a different Route?

## Further Reading

- [Credential Lifecycle](./05-credential-lifecycle.md)

## Sources and Verification

| Item | Value |
| --- | --- |
| Catalog ID | `model-provider-failures` |
| Status | `verified` |
| Last verified | 2026-08-06 |
