# 生产测评异常与缺陷台账

简体中文 | [English](../en/production-evaluation-findings.md)

> 状态：Foundation 与 D1 Harness 已验收；D1 Round 01 已完成 56/56。当前没有准入
> 的 Product Candidate、可信 17.4 Release Pass 或 Product Remediation。

本台账遵循[技术规格](./production-evaluation.md)和
[实施计划](./production-evaluation-implementation-plan.md)。Discovery、Global
Assessment、Remediation 和 Verification 必须分 Round。

## 1. 当前可信状态

| 范围 | 状态 |
| --- | --- |
| Architecture Ratchet | 审计时独立可信，112/112 |
| Evaluation 17.1 | 已作为 Foundation v2 输入验收 |
| Evaluation 17.2 | 已作为 Foundation v2 输入验收 |
| Evaluation 17.3 | 已失效 |
| Foundation v2 F1-F3 与 D1 Harness | 已验收；v3 Harness 已冻结 |
| Evaluation 17.4 | 已重置，未开始 |
| 正式 Product Finding | 0 |
| 历史 Product Hypothesis | 4 |
| 可信 17.4 Pass | 0 |
| 可信 17.4 Repair | 0 |
| Open Systemic Harness Root | 0 |
| Q1 Qualification | Round 06 已通过；Harness `frozen_qualified` |
| D1 Product Discovery | 56/56 通过；无 Product Candidate |

机器决策：

```text
evaluation/assessments/17.4-convergence-review-reset-01.json
evaluation/assessments/production-evaluation-independent-audit-01.json
evaluation/assessments/foundation-f1-f3-implementation-01.json
evaluation/assessments/q1-qualification-global-assessment-01.json
evaluation/assessments/q1-remediation-attribution-01.json
evaluation/assessments/q1-remediation-implementation-01.json
evaluation/assessments/q1-qualification-global-assessment-03.json
evaluation/assessments/d1-preflight-global-assessment-01.json
evaluation/assessments/d1-harness-remediation-01.json
evaluation/assessments/q1-qualification-global-assessment-06.json
evaluation/assessments/d1-product-discovery-global-assessment-01.json
```

Q1 Round 06 已在 8/8 Foundation Task 与连续三次 7/7 Integration Run 后验收具备
D1 能力的 Harness。D1 随后完成全部 56 项 Settlement，关闭 PEH-0025 以及
D1H-0001 至 D1H-0003。

## 2. 产品假设

以下 ID 只保留历史连续性。D1 Round 01 未重新发现它们，因此仍不能确认或修复。

| ID | 历史严重度 | 历史现象 | 当前状态 |
| --- | --- | --- | --- |
| PEC-0001 | P1 | Extension Host Reload 后 Reconstructed Binding 未持久化 | 未重新发现 |
| PEC-0002 | P0 | 快速 Runtime Restart 后 Accepted Turn 仍 Active | 未重新发现 |
| PEC-0003 | P1 | Fork 后第一个 Tool 进入 Recovery | 未重新发现 |
| PEC-0004 | P1 | 显式命名 Tool Definition 被遗漏 | 未重新发现 |

历史修复均不保留为产品证据。

## 3. 系统级 Harness 根因

| ID | 严重度 | 根因 | 代表证据 | 关闭条件 |
| --- | --- | --- | --- | --- |
| PEH-0023 | P1 | Core Scenario 和 Oracle False Assurance | Shared Fixture；Provider Split 为零；纯函数 500 Replay；Empty Impact Success | F2.3、F3.1 至 F3.3 |
| PEH-0024 | P1 | Fixture Request Contract 不完整 | Routed/Default Stream 和 Fragment 未完整校验 | F2.3、F3.2 |
| PEH-0025 | P1 | Fail-fast Discovery 与 Recovery Proof 不完整 | 一个 Host/Fault Failure 压制后续 Evidence | Q1.1、D1 |
| PEH-0026 | P1 | 缺少 Machine Freeze 和 Evidence Consistency | Stale Link、Batch 语义歧义、无 Frozen Identity Partition | S0、F1.1、Q1.3 |
| PEH-0027 | P1 | Evaluation Control 跨 Production Boundary | Test Authority 和 Fixture Control 未与 Production 隔离 | F1.1、Q1.3 |
| PEH-0028 | P1 | Execution/Admission Verdict 未闭环 | Command Status 复制给 Oracle；Suite Policy/Requirement/Budget 不执行 | F1.2、F3.1 |
| PEH-0029 | P1 | Run Containment 和 Evidence Identity 不完整 | Descendant Leak；Empty/Stale Evidence；Report Identity/Filename 冲突 | F1.1、F1.3 |
| PEH-0030 | P1 | Privacy 和 Corpus Promotion Fail Open | Raw Output 持久化；短 Secret；Manifest 未扫描；Batch 半提交 | F2.1、F2.2 |
| PEH-0031 | P1 | Integration Cleanup Evidence 不完整 | Round 01 残留 3 个目录；Remediation 已绑定 PID/Path Ownership 并拒绝 Outstanding Cleanup | 新 Q1 Epoch |

除非 Global Assessment 证明存在新的独立 Root，否则新症状归入以上根因。

## 4. 独立审计结果

2026-08-19 独立审计从当前 Branch、Plan、Implementation 和 Gate 重新开始。两名独立
Validator 对 12 个 Candidate 全部确认。

| Audit ID | Root | 结果 |
| --- | --- | --- |
| C1 | PEH-0028 | Command Exit Status 可生成无 Evidence 的 P0 Oracle Pass |
| C2 | PEH-0028 | Suite Admission Contract 只校验不执行 |
| C3 | PEH-0030 | Raw stdout/stderr 未脱敏进入 Private Report |
| C4 | PEH-0030 | Privacy 和 Corpus Manifest Contract Fail Open |
| C5 | PEH-0030 | Promotion 缺 Trusted Provenance 和 Batch Atomicity |
| C6 | PEH-0029 | Runner 不拥有 Process Tree 和 Fresh Evidence |
| C7 | PEH-0023 | Structural Replay 未执行 Production Runtime/Host |
| C8 | PEH-0023 | 36 个 Scenario Name 自检同一个 Shared Fixture |
| C9 | PEH-0023 | Unmatched Critical Path 返回 Empty Success Selection |
| C10 | PEH-0029 | Report 允许 Mixed Identity 和 Artifact Overwrite |
| C11 | PEH-0026 | Batch Semantics 与 Remaining-effort Estimate 不一致 |
| C12 | PEH-0026 | 缺失 Reset Document 导致 `make docs-check` 失败 |

Positive Diagnostic Command 不能推翻以上 Finding。Negative Probe 已证明绿色聚合计数可
与 Missing Evidence、Unexecuted Mutation、Empty Impact、Privacy Bypass 和 Inert
Suite Policy 同时存在。

## 5. 当前资产处置

Q1 Round 01 以一个 Passed Foundation Epoch 和一个 Invalid Integration Run 关闭。
获批 Remediation 已将症状分离归因：

| ID | 责任域 | 归因 | Remediation 候选 |
| --- | --- | --- | --- |
| Q1C-0001a | Product ACP Contract | Optional Empty Session Title 进入要求非空的 Lifecycle Invariant | 持久化前提供 ACP 默认标题 |
| Q1C-0001b | Product Runtime Adapter | Cancel 在 Start 准入后、Engine Scope 激活前到达并被拒绝 | 把 Pre-scope Control 映射到既有 Coordinator-not-active Sentinel |
| Q1C-0002a | Harness Fixture | Approval Policy 使用过期 Tool 名，或未通过 Typed Runtime Argument 传递 | 显式绑定 `file_apply`/`quality_verify` Ask Rule |
| Q1C-0002b | Harness Fixture | Editor Context 缺少结构化完成流 | 增加必需的 `turn_complete` Stream |
| PEH-0031 | Harness Lifecycle | Node Timeout 不执行异步 Cleanup，且 Q1 没有 Owned-resource Result | Bounded Wait 与 Identity-bound Cleanup Evidence |

以上是 Q1 Qualification Finding，不是正式 17.4 Product Discovery Finding。Q1
Round 06 已通过连续三次 Clean Integration Run 验证当前 Harness。每次 Run 均通过
7 项 Task，并清理 5 个 Runtime Process 与 4 个临时目录，Outstanding 为零。
Q1C-0001a/b、Q1C-0002a/b、PEH-0031、Q1G-0001/0002 与 D1H-0001 至
D1H-0003 已关闭。

通过 Negative Requalification 后可保留：

- Strict JSON Decode；
- Source 和 Dirty-content Digest；
- Bounded Output Collection；
- Private Atomic File Write；
- Canonical Evidence Encoding 和 Hash-chain Integrity。

必须替换或重设计：

- Command Status 到 Verdict 的 Projection；
- Validation-only Admission Policy；
- Unbound Evidence File；
- Direct Tracked-corpus Promotion；
- Generic Metadata String Allowlist；
- Shared Scenario Truth；
- Optional Fault/Mutation Coverage；
- Empty Impact Success；
- Metadata-only Flake Claim；
- Attempt-only Artifact Name。

## 6. 重新准入规则

Q1 与 D1 均已关闭。未准入 Product Candidate，因此 Product Remediation 仍被禁止。
下一规格门禁为 H1-H4 Admission Work。

当前顺序：

1. Identity-bound 归因与 Harness Remediation 已完成；
2. Q1 Round 06 已冻结具备 D1 能力的 v3 Candidate Lock；
3. D1 已完成 36 个 Scenario、13 个 Fault Case、5 个 Host Case 与 2 次 Identity
   Check；
4. 未准入 Product Candidate 或 Product Remediation。

Frozen Harness 只有在 v3 Input Identity 不变时才具权威性。

## 7. 禁止操作

D1 完成后、H1-H4 Admission 前：

- 不确认或修复 PEC-0001 至 PEC-0004；
- 不恢复已删除的 17.4 Code；
- 不把当前绿色计数作为 Release Evidence；
- Systemic Epoch 仍 Open 时不逐个关闭 Micro-incident；
- Discovery 内不修改 Product Code；
- 不通过削减 Denominator、Status 或 Assertion 获得绿色。
