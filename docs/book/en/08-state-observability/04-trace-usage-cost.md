---
id: state-trace-usage-cost
title: Traces, Spans, Usage, and Cost
audience:
  - contributor
  - operator
prerequisites:
  - model-stream-reasoning-usage
  - context-quality
code_paths:
  - internal/observability/observation
  - internal/observability/router
  - internal/observability/trace
  - internal/observability/tracecontext
  - internal/observability/usage
  - internal/observability/telemetry
  - internal/observability/otel
  - internal/runtime/agent/turnkernel
test_paths:
  - internal/observability/observation/envelope_test.go
  - internal/observability/router/router_test.go
  - internal/observability/trace/trace_test.go
  - internal/observability/trace/rollup_test.go
  - internal/observability/otel/projector_test.go
  - internal/runtime/agent/turnkernel/measurement_test.go
  - internal/observability/usage/repository_test.go
source_of_truth:
  - internal/observability/observation/envelope.go
  - internal/observability/router/router.go
  - internal/observability/trace/trace.go
  - internal/observability/otel/projector.go
  - internal/runtime/agent/turnkernel/measurement.go
  - internal/observability/usage/repository.go
status: verified
last_verified: 2026-08-17
---

# Traces, Spans, Usage, and Cost

English | [简体中文](../../zh-CN/08-state-observability/04-trace-usage-cost.md)

## Learning Objectives

Understand versioned observations, frozen terminal measurement, phase spans,
multi-sample Usage, W3C propagation, pricing provenance, and bounded OTLP
projection.

## Observation Model

```mermaid
flowchart LR
    M[Terminal Measurement Snapshot] --> R[Receipt]
    M --> T[Measurement-derived Trace]
    M --> E[Terminal Envelope]
    T --> P[Provider / Tool / Approval / Verify Spans]
    P --> L[Latency Partition]
    U[Usage Events per Call/Sample] --> A[SQLite Aggregate]
    A --> C[Cost Rollup]
    O[Observation Envelope] --> J[Observation Router / Journal]
    J --> X[Semantic graph / OTLP]
    L --> R
    C --> R
```

Trace Recorder nests phase spans under a Turn, records status/attributes, notes
first output, and closes unfinished spans explicitly. Latency separates total,
provider, first-token, Tool, approval wait, and verification time.

Usage projection requires Turn context and is idempotent by Event. Reports are
cumulative within one call, so the latest report replaces the previous one;
different calls/samples sum. Cached and reasoning tokens remain separate.

Cost is attached using the actual sampled route. Rollups split priced and
unpriced calls: unknown price is not free, and an empty query is not zero-cost
work. Trace rollups scope by Thread, Turn, and time window and compute
percentiles only from completed measurements.

Telemetry metrics use atomic counters. Structured logging recursively redacts
configured credentials and propagates writer failures.

## Frozen Terminal Measurement

Usage and latency become a Kernel-owned `TerminalMeasurementSnapshot` with a
stable digest. The Runtime freezes it once at terminal convergence. Receipt,
measurement-derived Trace, and Terminal Envelope project that same snapshot;
none may resample mutable counters after the terminal decision.

Missing Usage, unknown price, and absent latency remain unknown. Tool-side
model Usage joins the same domain fact instead of being added later by an
observability-only path.

## Observation Envelope and Routing

Every admitted Observation carries schema version, stable Observation ID,
sequence, Runtime and domain identity, optional Trace/Span/parent IDs,
causality links, data policy, bounded summary, and optional CAS payload
reference. Traits generated from
`internal/observability/schema/observation_traits.json` define owner,
durability, required correlations, retention class, priority, and OTLP mapping.

Privacy admission precedes Journal or CAS persistence. Critical evidence is
written synchronously; normal and bulk evidence use bounded queues. Capture
defaults to metadata-only. Writer, queue, and exporter failure update
Observation Health but never change the business Turn result.

## W3C Trace Context and OTLP

W3C `traceparent` and `tracestate` propagate across Provider HTTP, MCP
HTTP/stdio, processes, workflows, and subagents. Invalid or all-zero context
fails closed at the parsing boundary.

The OTLP projector supports in-memory, HTTP/protobuf, and gRPC exporters. Span
attributes and metric labels are selected from a fixed low-cardinality
allowlist. Paths, prompts, Tool arguments, Resource IDs, and raw errors do not
become metric labels. Export queues are bounded and `Flush`/`Shutdown` do not
transfer execution authority to telemetry.

## Observation Types and Cardinality

| Signal | Identity/cardinality | Suitable use |
| --- | --- | --- |
| Metric | bounded labels/counters | health, rate, saturation |
| Log | timestamp + structured fields | discrete sanitized diagnosis |
| Span | Turn/parent/phase IDs | causal timing |
| Observation | stable ID/sequence + domain correlation | redacted causal evidence |
| Usage row | Turn/call/sample/purpose/route | accounting |
| Receipt | one terminal Turn projection | user-facing audit summary |

Putting path, prompt, Tool arguments, or raw error bodies into Metric labels
creates unbounded cardinality and leakage. Those details belong in bounded,
redacted records with explicit access controls.

## Clocks and Aggregation

Span duration uses one Recorder clock and parent identity; wall-clock timestamps
support ordering and query windows. Rollups:

- count open spans but exclude them from completed-duration percentiles;
- reject backward time windows;
- distinguish a foreign Turn from an untraced Turn;
- calculate percentiles only over comparable completed phase samples.

Latency phases may overlap, so their sum is diagnostic partitioning, not always
equal to wall-clock total.

Usage identity includes call/sample because retries and Tool-side model calls
must not overwrite one another. Purpose and actual Route explain where spend
came from.

## Failure Boundaries

- Unfinished Span is visible, not assigned a fictional duration in rollups.
- Missing trace/usage is absent, not zero.
- Duplicate cumulative Usage is not double-counted.
- Non-USD/unknown pricing cannot become known cost.
- Raw secrets must not enter logs or attributes.
- Receipt, Trace, and Terminal Envelope must share one Measurement digest.
- Exporter failure cannot alter a completed/failed/canceled Turn outcome.
- High-cardinality or sensitive fields cannot become Metric labels.

## Tests and Verification

```bash
go test ./internal/observability/observation ./internal/observability/router
go test ./internal/observability/trace ./internal/observability/tracecontext
go test ./internal/observability/usage
go test ./internal/observability/telemetry
go test ./internal/observability/otel
go test ./internal/runtime/agent/turnkernel
```

## Hands-On Lab

Build a Turn with two Provider calls and two cumulative reports per call.
Predict the final token/cost rollup before running the repository tests.

## Review Questions

1. Why replace Usage within a call but sum across calls?
2. Why distinguish untraced from zero latency?
3. What does `CostKnown` communicate?
4. Which data is unsafe as a Metric label?
5. Why may phase latency sums differ from wall-clock total?

## Further Reading

- [Reconstructing a Failed Run](./06-reconstructing-failures.md)

## Sources and Verification

| Item | Value |
| --- | --- |
| Catalog ID | `state-trace-usage-cost` |
| Status | `verified` |
| Last verified | 2026-08-17 |
