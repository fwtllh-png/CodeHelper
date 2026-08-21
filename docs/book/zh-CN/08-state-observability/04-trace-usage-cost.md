---
id: state-trace-usage-cost
title: Trace、Span、Usage 与 Cost
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

# Trace、Span、Usage 与 Cost

## 学习目标

理解版本化 Observation、冻结终态 Measurement、Phase Span、Multi-sample Usage、
W3C Propagation、Pricing Provenance 与有界 OTLP Projection。

## Observation Model

```mermaid
flowchart LR
    M[Terminal Measurement Snapshot] --> R[Receipt]
    M --> T[Measurement-derived Trace]
    M --> E[Terminal Envelope]
    T --> P[Provider / Tool / Approval / Verify]
    P --> L[Latency Partition]
    U[Usage per Call/Sample] --> A[SQLite Aggregate]
    A --> C[Cost Rollup]
    O[Observation Envelope] --> J[Observation Router / Journal]
    J --> X[Semantic Graph / OTLP]
    L --> R
    C --> R
```

Trace Recorder 将 Phase Span 嵌套在 Turn 下，记录 Status/Attribute、First Output，并
显式关闭 Unfinished Span。Latency 分离 Total、Provider、First-token、Tool、Approval
Wait 与 Verification。

Usage Projection 要求 Turn Context，并按 Event 幂等。同一 Call 内 Usage 是 Cumulative，
因此 Latest Report 替换 Previous；不同 Call/Sample 相加。Cached/Reasoning Token 分离。

Cost 使用实际 Sample Route。Rollup 区分 Priced/Unpriced：Unknown Price 不是 Free，
Empty Query 也不是 Zero-cost Work。Trace Rollup 按 Thread/Turn/Time Window Scope，
Percentile 只使用完整 Measurement。

Telemetry Metric 使用 Atomic Counter；Structured Logger 递归 Redact Credential，并
传播 Writer Failure。

## Frozen Terminal Measurement

Usage 与 Latency 成为 Kernel-owned、带稳定 Digest 的 `TerminalMeasurementSnapshot`。
Runtime 在终态收敛时只冻结一次；Receipt、Measurement-derived Trace 与 Terminal
Envelope 都投影同一 Snapshot，不能在终态决定后再次采样 Mutable Counter。

Missing Usage、Unknown Price 与 Absent Latency 保持 Unknown。Tool-side Model Usage
进入同一 Domain Fact，而不是由 Observability 路径事后补加。

## Observation Envelope 与 Routing

每条获准 Observation 包含 Schema Version、稳定 Observation ID、Sequence、
Runtime/Domain Identity、可选 Trace/Span/Parent ID、Causality Link、Data Policy、
有界 Summary 与可选 CAS Payload Reference。
`internal/observability/schema/observation_traits.json` 生成的 Trait 定义 Owner、
Durability、必需 Correlation、Retention Class、Priority 与 OTLP Mapping。

Privacy Admission 先于 Journal/CAS Persistence。Critical Evidence 同步写入；Normal 与
Bulk Evidence 使用有界 Queue。Capture 默认只保留 Metadata。Writer、Queue 与 Exporter
Failure 更新 Observation Health，但绝不改变业务 Turn Result。

## W3C Trace Context 与 OTLP

W3C `traceparent`/`tracestate` 跨 Provider HTTP、MCP HTTP/stdio、Process、Workflow 与
Subagent 传播。Malformed 或 All-zero Context 在解析边界 Fail Closed。

OTLP Projector 支持 In-memory、HTTP/protobuf 与 gRPC Exporter。Span Attribute 与
Metric Label 来自固定低基数 Allowlist；Path、Prompt、Tool Argument、Resource ID 与
Raw Error 不会成为 Metric Label。Export Queue 有界，`Flush`/`Shutdown` 不会把执行
权威转交给 Telemetry。

## Observation Type 与 Cardinality

| Signal | Identity/Cardinality | Suitable Use |
| --- | --- | --- |
| Metric | Bounded Label/Counter | Health/Rate/Saturation |
| Log | Timestamp + Structured Field | Sanitized Diagnosis |
| Span | Turn/Parent/Phase ID | Causal Timing |
| Observation | Stable ID/Sequence + Domain Correlation | 脱敏因果证据 |
| Usage Row | Turn/Call/Sample/Purpose/Route | Accounting |
| Receipt | One Terminal Turn Projection | User-facing Audit |

Path、Prompt、Tool Argument、Raw Error Body 放入 Metric Label 会造成 Unbounded
Cardinality/Leakage，应进入受限、Redacted Record。

## Clock 与 Aggregation

Span Duration 使用同一 Recorder Clock/Parent Identity，Wall-clock Timestamp 用于 Ordering/
Query Window。Rollup：

- 统计 Open Span，但不纳入 Completed-duration Percentile；
- 拒绝 Backward Window；
- 区分 Foreign Turn 与 Untraced Turn；
- 仅对 Comparable Completed Phase 计算 Percentile。

Latency Phase 可能 Overlap，因此 Phase Sum 是 Diagnostic Partition，不保证等于 Wall-clock
Total。

Usage Identity 包含 Call/Sample，避免 Retry/Tool-side Model Call 相互覆盖；Purpose/
Actual Route 解释 Spend 来源。

## 失败与安全边界

- Unfinished Span 可见，不在 Rollup 中伪造 Duration。
- Missing Trace/Usage 是 Absent，不是 Zero。
- Cumulative Usage 不重复计数。
- Unknown Pricing 不变成 Known Cost。
- Raw Secret 不进入 Log/Attribute。
- Receipt、Trace 与 Terminal Envelope 必须共享同一 Measurement Digest。
- Exporter Failure 不能改变 Completed/Failed/Canceled Turn Outcome。
- High-cardinality/Sensitive Field 不能成为 Metric Label。

## 测试与验证

```bash
go test ./internal/observability/observation ./internal/observability/router
go test ./internal/observability/trace ./internal/observability/tracecontext
go test ./internal/observability/usage
go test ./internal/observability/telemetry
go test ./internal/observability/otel
go test ./internal/runtime/agent/turnkernel
```

## 动手实验

构造含两个 Provider Call、每个 Call 两次累计报告的 Turn，先预测 Token/Cost Rollup。

## 复习问题

1. 为什么 Call 内 Replace、Call 间 Sum？
2. 为什么 Untraced 不等于 Zero Latency？
3. `CostKnown` 表达什么？
4. 哪些 Data 不应成为 Metric Label？
5. Phase Latency Sum 为什么可能不等于 Wall-clock Total？

## 延伸阅读

- [从失败运行还原系统行为](./06-reconstructing-failures.md)

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `state-trace-usage-cost` |
| 状态 | `verified` |
| 最后验证 | 2026-08-17 |
