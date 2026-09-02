---
id: context-quality
title: 如何评估 Context Quality
audience:
  - contributor
  - operator
  - agent
prerequisites:
  - context-source-lifecycle
  - context-budget-compaction
code_paths:
  - internal/runtime/agent/context
  - internal/runtime/agent/prompt
  - internal/observability
test_paths:
  - internal/runtime/agent/prompt/catalog_benchmark_test.go
  - internal/runtime/agent/context/evidence_evidence_test.go
  - internal/observability/receipt/receipt_test.go
source_of_truth:
  - internal/runtime/agent/prompt/context.go
  - internal/runtime/agent/context/evidence_evidence.go
status: draft
last_verified: null
---

# 如何评估 Context Quality

## 学习目标

用 Coverage、Precision、Freshness、Provenance、Budget Efficiency 与 Outcome Evidence
替代“Prompt 看起来不错”的主观评价。

## 前置知识

阅读 [Context Source、优先级与生命周期](./03-source-priority-lifecycle.md) 与
[Token Budget、Compaction 与信息损失](./04-budget-and-compaction.md)。

## Quality Dimension

| 维度 | 问题 | 可观测信号 |
| --- | --- | --- |
| Coverage | Required State 是否存在？ | Critical Path、Missing/Degraded Section |
| Precision | 是否排除无关 State？ | Retained/Original Ratio、Omitted Entry |
| Freshness | 是否匹配本次 Sample？ | Digest、Turn、Document Version |
| Provenance | Claim 能否追踪？ | Source Path、Receipt、Evidence Kind |
| Integrity | Content 是否验证？ | Hash、Canonical Path、Paired History |
| Efficiency | Useful Context 成本？ | Byte/Token、Cache-stable Prefix |
| Outcome | 是否改善正确执行？ | Repeat Call、Blind Edit、Verification |

Token 数量不能单独衡量 Quality：小 Context 可能遗漏 Critical File，大 Context 可能埋没
唯一 Relevant Diagnostic。

## Measurement Loop

```mermaid
flowchart LR
    A[Assemble] --> R[Partition Receipts]
    R --> S[Sample]
    S --> E[Evidence Ledger]
    E --> O[Turn Receipt / Metrics]
    O --> T[Tests / Baselines]
    T --> A
```

## Receipt、Evidence 与 Outcome

Prompt Receipt 记录 Source、Digest、Original/Retained Byte/Token 与 Truncation Reason。
Evidence 记录 Fact、Risk、Reminder、Change Mark。Turn Receipt 再结合 Usage、Latency、
Verification 与 Changed Path。

Working Set 与 Affected-test Entry 还保留 Selection Explanation：Entry Kind、Reason、
Supporting Evidence、Score 与逐条 Truncation。Host 从 Receipt 投影这些字段，不能根据
文件名重建一个看似合理的原因，否则 Presentation Inference 会变成错误 Authority。

因此可以回答：

- Failed Turn 是否缺少 Relevant File，还是忽略了已有 Evidence？
- Catalog 增长是否增加 Context，却没有增加实际使用的 Tool？
- Write 前是否没有 Read？
- Passing Verification 是否被后续 Write 失效？

## Baseline 与实验

`catalog_benchmark_test.go` 保存默认 Tool Catalog Size Baseline；确定性测试校验稳定
Order/Digest。Context Change 应同时比较 Structural Metric 与 Task Outcome，只有 Byte
下降并不代表成功。

有效实验应固定 Fixture Task，每次只改变一个 Source/Budget，记录 Completion、Tool
Call、Repeated Read、Verification、Input Token、Truncation 与 Latency。

## Derived Metric 及其限制

```text
retention_ratio = retained_bytes / original_bytes
risk_clearance  = risks_cleared_by_verification / risks_opened
repeat_rate     = repeated_equivalent_calls / total_calls
evidence_yield  = consumed_facts / context_or_tool_cost
cache_stability = samples_with_same_stable_digest / comparable_samples
```

这些是 Diagnostic Signal，不是 Universal Score。低 Retention Ratio 对 Noisy Catalog
可能健康，对 Critical File 可能危险；“Consumed Fact”也需要 Operational Definition，
Model Prose 不能证明因果。

## Controlled Experiment Checklist

1. 固定 Repository Revision、Fixture/Model Route、Tool Catalog、Mode、Security Posture；
2. 每次只改变一个 Source、Ordering Rule 或 Budget；
3. 运行足量 Deterministic Case；
4. 同时比较 Success Criteria 与 Failure Category；
5. 检查 Receipt，确认预期 Context 确实变化；
6. 比较 Cost/Latency/Repeated Work，不只看 Completion；
7. 记录 Smaller Context 丢失 Critical Evidence 的 Regression。

Task/Route 不稳定时，Token/Success 差异无法归因于 Context。

## Failure Diagnosis Matrix

| Symptom | Context Hypothesis | Evidence |
| --- | --- | --- |
| Repeated File Read | Working Set Stale/Missing | Read Digest、Reminder、Tail Receipt |
| Blind Edit | File/Evidence 缺失 | Change Risk、Tool Receipt |
| Wrong Module | Repo Map/Editor Focus 差 | Outline、Critical Path、Editor Digest |
| Policy Bypass Attempt | Untrusted Text 被当 Authority | Partition Source、Guard Decision |
| 未运行却声称通过 | Summary/Model Invention | Verification Receipt |
| High Cost/No Progress | Noisy Partition/Catalog | Retention Ratio、Per-sample Usage |

## 代码地图

| 关注点 | 源码 |
| --- | --- |
| Partition Receipt | `prompt/context.go` |
| Evidence Ledger | `agent/evidence` |
| Turn Receipt | `observability/receipt/receipt.go` |
| Usage/Latency | `internal/observability` |
| Catalog Baseline | `prompt/catalog_benchmark_test.go` |

## 设计取舍

人工 Prompt Review 能发现措辞问题但主观；端到端成功率重要但诊断能力弱。QCode
组合 Deterministic Structure Check、Provenance Receipt、Evidence Signal 与 Task Outcome。

## 失败模式与安全边界

- Digest 只证明相等，不证明 Truth/Authority。
- Passing Test 只对其 Coverage 范围构成 Evidence。
- Missing Receipt Data 不能解释为 Zero Usage。
- Telemetry 不得记录 Raw Secret/Sensitive Content。
- 有意增加 Context 时必须显式评审 Baseline。
- Context Size 与 Success 的相关性不是因果性。

## 测试与验证

```bash
go test ./internal/runtime/agent/prompt
go test ./internal/runtime/agent/context
go test ./internal/runtime/app -run 'TestReceipt(ReportsReadPathsAndContextSections|ReportsEvidence)'
```

## 动手实验

选择一个 Fixture Turn，记录 Partition Byte、Truncation、Evidence Risk、Tool Call、Usage
与 Verification；改变一个 Partition Budget 后重跑，解释 Quality 是否改善，而不只是变小。

## 复习问题

1. Token Count 为什么不足以衡量 Context Quality？
2. Digest 能证明什么？
3. 哪些信号能揭示无关或陈旧 Context？

## 延伸阅读

- [Verification 与 Evidence](../06-tools-and-execution/05-verification-and-evidence.md)

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `context-quality` |
| 状态 | `draft` |
| 最后验证 | 尚未验证 |
