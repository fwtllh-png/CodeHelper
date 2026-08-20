# 生产测评异常与缺陷台账

简体中文 | [English](../en/production-evaluation-findings.md)

> 状态：具备 H3 能力的 Harness 已由 Q1 Round 13 冻结。同 Lock H1 Round 03 已
> 21/21 通过，H2 Round 05 已 16/16 通过，正式 H3 Round 02 已 14/14 通过并准入
> 全部 8 条 RC Lane。H3 只准入本地 `validated-dry-run` Candidate；H4 Canary 与
> Rollout Expansion 均未准入。

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
| Foundation v2 F1-F3 与 H3 Harness | 已验收；v3 Harness 已由 Q1 Round 13 冻结 |
| Evaluation 17.4 | H1、H2 与 H3 已通过；H4 未开始 |
| 正式 Product Finding | H2C-0001、H3P-0003 与 H3P-0004 已修复并验证 |
| 历史 Product Hypothesis | 4 |
| 可信 17.4 Pass | 同 Lock H1 Round 03；H2 Round 05；H3 Round 02 |
| 可信 17.4 Repair | 2 |
| Open Systemic Harness Root | 0 |
| Q1 Qualification | Round 13 已通过；H3 Harness `frozen_qualified` |
| D1 Product Discovery | 56/56 通过；无 Product Candidate |
| H1 Production Admission | 同 Lock Round 03 已 21/21 通过 |
| H2 Production Admission | 同 Lock Round 05 已 16/16 通过 |
| H3 Production Admission | Round 02 已 14/14 通过；8/8 RC Lane 已准入 |
| H4 Canary Admission | 未授权 |

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
evaluation/assessments/h1-preflight-global-assessment-26.json
evaluation/assessments/q1-qualification-global-assessment-07.json
evaluation/assessments/h1-production-admission-global-assessment-01.json
evaluation/assessments/h2-preflight-global-assessment-01.json
evaluation/assessments/h2-preflight-global-assessment-02.json
evaluation/assessments/h2-preflight-global-assessment-03.json
evaluation/assessments/q1-qualification-global-assessment-08.json
evaluation/assessments/h2-production-admission-global-assessment-01.json
evaluation/assessments/h2-production-admission-global-assessment-02.json
evaluation/assessments/h2-reentry-decision-01.json
evaluation/assessments/q1-qualification-global-assessment-09.json
evaluation/assessments/h2-reentry-global-assessment-01.json
evaluation/assessments/h2-production-admission-global-assessment-03.json
evaluation/assessments/h3-preflight-global-assessment-06.json
evaluation/assessments/h3-production-admission-global-assessment-01.json
evaluation/assessments/h3-production-admission-global-assessment-02.json
evaluation/assessments/h3-production-admission-global-assessment-03.json
evaluation/assessments/h3-production-admission-global-assessment-04.json
evaluation/assessments/h3-production-admission-global-assessment-05.json
evaluation/assessments/q1-qualification-global-assessment-11.json
evaluation/assessments/q1-qualification-global-assessment-12.json
evaluation/assessments/q1-qualification-global-assessment-13.json
evaluation/assessments/h3-production-admission-global-assessment-06.json
```

Q1 Round 06 已在 8/8 Foundation Task 与连续三次 7/7 Integration Run 后验收具备
D1 能力的 Harness。D1 随后完成全部 56 项 Settlement，关闭 PEH-0025 以及
D1H-0001 至 D1H-0003。

Q1 Round 07 又通过 8/8 Foundation Task 与连续三次 7/7 Integration Run，验收具备
H1 能力的后继 Harness。H1 随后在 Extension Host、Process、Provider、Persistence
与 Filesystem 五条 Lane 上完成全部 21 项 Settlement。两次 Identity Check 与
Owned-resource Cleanup Check 均通过，未准入 Product Candidate。

H2 Preflight 发现并关闭 4 个 Harness Finding 以及 DeepSeek V4 Flash Metadata
Contract 过期的 H2C-0001，Q1 Round 08 随后验收后继 Harness。正式 Round 01 与 02
均通过全部 8 个 Exact-response Sample，但每轮 4 个 Multi-Agent Sample 仅通过
3 个。两个正式 Round 合计 6/8，Wilson 95% 下界为 40.92%，低于 50% 政策阈值。
两轮 Lock Identity 均稳定、私有 Cost Evidence 均为 Known、Outstanding Resource
均为零。因此 H2 结论是 Failed，而不是 Unavailable。

上述失败保持不可变。显式 Re-entry 增加 Schema v2 结构化失败证据，没有修改
Prompt、Denominator 或 Threshold，Q1 Round 09 随后验收该 Harness。固定 12 样本
诊断矩阵在两个 Child Agent 完成后捕获一次 Provider HTTP 400；后续 36 次插桩复现
全部通过，并证明 130/130 个 Reasoning Message 保留 Replay State、Orphan Tool
Call 为零。因此未授权 Product Logic 修复。唯一获批的正式 Re-entry Round 03 已
16/16 通过，包含 12/12 个 Private、Known-cost Live Sample 和稳定的前后 Identity。
H2 已准入。

### H3 Finding 关闭

正式 H3 Round 01 完成 480/480 个 Turn，Failed/Canceled Terminal 均为零，但以
498,233 bytes/Turn 违反固定 Persistence Policy。Assessment 在修复前分离症状：

| ID | 责任域 | 根因 | 关闭 |
| --- | --- | --- | --- |
| H3P-0003 | Product Persistence | 每个 Terminal Envelope 重复累计 Session Delta History | Deterministic Bounded Session Delta Encoding；已验证 |
| H3P-0004 | Product Persistence | 每个自动 Checkpoint 在 CAS 中保存累计未压缩 History | Deterministic Bounded Checkpoint Content Encoding；已验证 |
| H3H-0009 | Harness Sampling | Directory Enumeration 期间 Dynamic CAS Temporary Ref 可能消失 | 只忽略瞬时 `os.ErrNotExist`；已验证 |

联合 480-Turn Verification 将 Persistence Slope 降至 133,391 bytes/Turn，未改变
262,144 bytes/Turn Threshold、Duration、Denominator、Prompt 或 Retry Policy。
由于产品输入改变，Q1 重新开始。Round 11 继续作为 Invalid Runtime Identity 历史；
Round 12 继续作为不可复用的一次 `evaluation-race` Command 失败，该失败在三次精确
确认中均未复现。Q1 Round 13 随后通过 8/8 Foundation Task 与连续三次 7/7
Integration Run。

在 Round 13 Lock 上，H1 Round 03 已 21/21 通过，H2 Round 05 已 16/16 通过。
正式 H3 Round 02 通过全部 14 个 Coordinator Task，四小时 Endurance Workload
完成 480/480 个 Turn。Persistence Slope 为 133,421 bytes/Turn，P95 Latency 为
93 ms，RSS Slope 为 7,383 bytes/Turn，FD Growth 与 Process Restart 均为零。
Foundation、Integration、Chaos、Live、Endurance、Release、VS Code RC 与 Package
Lane 全部通过。Release Evidence v3 对本地 RC Candidate 给出 `admit`；VS Code
Candidate 未上传。

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

Q1、D1、H1、H2 与 H3 均已关闭。H2 已通过唯一显式授权的 Re-entry Round，H3
在分离评估产品修复并重新验收后通过。H4 仍需独立显式授权。

当前顺序：

1. Identity-bound 归因与 Harness Remediation 已完成；
2. Q1 Round 06 已冻结具备 D1 能力的 v3 Candidate Lock；
3. D1 已完成 36 个 Scenario、13 个 Fault Case、5 个 Host Case 与 2 次 Identity
   Check；
4. Q1 Round 07 已冻结具备 H1 能力的后继 Lock；
5. H1 已在五条 Lane 上完成全部 21 项任务；
6. Q1 Round 08 已冻结具备 H2 能力的后继；
7. H2 Round 01 与 02 均因 Multi-Agent 质量而 14/16 失败；
8. 显式 Re-entry 后，Q1 Round 09 冻结 Schema v2 失败证据；
9. H2 Round 03 未修改 Prompt、Threshold 或 Product Logic，已 16/16 通过；
10. H3 Round 01 未通过固定 Persistence Slope Policy；
11. H3P-0003、H3P-0004 与 H3H-0009 已分离修复并验证；
12. Q1 Round 11 与 12 保持不可复用历史后，Round 13 冻结具备 H3 能力的 Lock；
13. 同 Lock H1 Round 03 与 H2 Round 05 已通过；
14. H3 Round 02 已 14/14 通过并准入全部 8 条 RC Lane；
15. H4 未获准入。

Frozen Harness 只有在 v3 Input Identity 不变时才具权威性。

## 7. 禁止操作

H3 完成后、H4 显式授权前：

- 不确认或修复 PEC-0001 至 PEC-0004；
- 不恢复已删除的 17.4 Code；
- 不把未上传的 `validated-dry-run` Candidate 当成 Public Release、Canary 或
  Rollout Expansion；
- Systemic Epoch 仍 Open 时不逐个关闭 Micro-incident；
- Discovery 内不修改 Product Code；
- 不通过削减 Denominator、Status 或 Assertion 获得绿色。
- 不得开始 H4。
