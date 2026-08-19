# 生产测评实施计划

简体中文 | [English](../en/production-evaluation-implementation-plan.md)

> 状态：F1-F3 与具备 H1 能力的 Harness 已通过 Q1 Round 07 验收。D1 Round 01
> 已 56/56 通过，H1 Round 01 已 21/21 通过，均未发现 Product Candidate。
> Product Remediation 未获授权；H2-H4 Admission Work 尚待实施。

| Stage | 状态 |
| --- | --- |
| S0 | 已完成 |
| F1 Contract、Identity、Admission、Runner | 已验收 |
| F2 Privacy、Promotion、Replay | 已验收 |
| F3 Oracle、Core Pack、Impact | 已验收 |
| Q1 Qualification 与 Freeze | 具备 H1 能力的后继已由 Round 07 完成；`frozen_qualified` |
| D1 Collect-all Product Discovery | 已完成 56/56；无 Product Candidate |
| H1 VS Code 与 Process Chaos | 已完成 21/21；无 Product Candidate |

Development Validation 记录在
`evaluation/assessments/foundation-f1-f3-implementation-01.json`，不能替代 Q1 Epoch，
也不会创建 Harness Lock。

Q1 Round 01 记录在
`evaluation/assessments/q1-qualification-global-assessment-01.json`。Foundation
Epoch 8/8 通过，但 Integration-01 的 ACP Interop 失败，VS Code Runtime Integration
超时。Runs 02/03 被正确抑制，因此 Candidate Lock 的 Clean Integration Run 为零。

Q1 Remediation 归因和实现记录在
`evaluation/assessments/q1-remediation-attribution-01.json` 与
`evaluation/assessments/q1-remediation-implementation-01.json`。Focused Post-fix
Validation 不能追加到已失败 Lock；全部变化输入进入不可变后继 Epoch。具备 D1
能力的后继 Harness 已在 Q1 Round 06 通过 8/8 Foundation Task 与三次 7/7
Integration Run，随后 D1 56/56 通过。关闭决策记录在
`evaluation/assessments/q1-qualification-global-assessment-06.json` 与
`evaluation/assessments/d1-product-discovery-global-assessment-01.json`。
H1 Preflight 随后以 18/18 关闭，后继 Harness 在 Q1 Round 07 通过 8/8 Foundation
Task 与三次 7/7 Integration Run，正式 H1 又以 21/21 通过。决策记录在
`evaluation/assessments/h1-preflight-global-assessment-26.json`、
`evaluation/assessments/q1-qualification-global-assessment-07.json` 与
`evaluation/assessments/h1-production-admission-global-assessment-01.json`。

## 1. 执行模型

以下三个边界必须区分：

| 边界 | 含义 | 能否关闭 Finding |
| --- | --- | --- |
| Work Unit | 一个聚焦、可评审的 PR 或 Commit Series | 不能 |
| Qualification Epoch | 一组不可变 Foundation Work Unit 的整体验收 | 可以关闭 Harness Finding |
| Discovery/Verification Round | 基于 Frozen Harness 的一次 Collect-all 产品测评 | Global Assessment 后可以 |

“一个 Foundation 批次”指一个 Qualification Epoch，不指一个巨型 PR。

规则：

1. Work Unit 可以使用 Focused Test 作为开发反馈；
2. Work Unit 通过不构成 Foundation Acceptance；
3. 全部 Foundation Work Unit 进入同一个首轮 Qualification Epoch；
4. 失败 Epoch 不做修复，先关闭并进行 Global Assessment；
5. 修复形成新的不可变 Epoch；
6. 同一个 Harness Lock 连续三次完整 Integration Qualification 通过后，才能开始
   Product Discovery；
7. Discovery Round 内禁止产品修改。

## 2. 审计问题到 Work Unit 的映射

| 审计根因 | 修正 Work Unit | Acceptance Gate |
| --- | --- | --- |
| Command Status 自证 Oracle | F1.2、F3.1 | Oracle Closure Negative Control |
| Suite Policy、Requirement、Budget 不执行 | F1.2 | Admission Policy Matrix |
| Raw Output 和短 Secret 可持久化 | F2.1 | Privacy Bypass Corpus |
| Corpus Verification/Promotion Fail Open | F2.1、F2.2 | Batch Rollback 和全文件扫描 |
| Timeout 泄漏后代；Evidence 可过期 | F1.3 | Process-tree 和 Freshness Test |
| Replay 未进入生产路径 | F2.3 | Provider/Runtime/Host Replay Contract |
| Shared Fixture 和 Optional Fault Matrix | F3.1、F3.2 | Per-scenario Identity 和 Mutation Coverage |
| Impact 可选择空集 | F3.3 | Unknown-path 和 Self-change Test |
| Report Identity/Filename 冲突 | F1.1、F1.3 | Mixed-partition 和 Overwrite Rejection |
| 计划与文档漂移 | S0、Q1 | Mirrored Docs 和 Documentation Gate |

## 3. S0：规格关闭

**范围：**只修改文档和 Contract Design。

交付：

- 获批的中英文技术规格与实施计划；
- Planned Schema Inventory 和 Ownership；
- Reset Assessment 与更新后的 Findings Register；
- 当前 `eval-*` 命令仍为 Diagnostic 的显式决策；
- 第一轮 Foundation Qualification Epoch 的批准记录。

验收：

```bash
make docs-check
make book-check
git diff --check
```

退出条件：明确批准开始 Foundation Work Unit。

停止条件：Evidence Identity、Oracle Closure、Qualification Epoch、Privacy Boundary
或 Production Isolation 仍有未决歧义。

预计工作量：0.5 至 1 工程周。

## 4. F1：Contract、Identity、Admission 与 Runner

### F1.1 Versioned Contract 与 Identity

交付：

- 严格的 Version 2 Foundation、Scenario、Evidence、Oracle、Qualification 与
  Release Evidence Schema，以及带显式 Input Root 的 Version 3 Harness Lock；
- Canonical Run Partition 和无冲突 Artifact Naming；
- Schema/Go Parity Test 和 Unknown-field Rejection；
- Mixed Source、Harness、Runtime、VSIX、Fixture、Provider、Model、Config、Seed 和
  Attempt Rejection。

Negative Control：

- Duplicate Run/Attempt；
- Mixed Artifact 或 Environment Identity；
- Stale 和 Cross-attempt Evidence；
- Existing Artifact Destination；
- Schema-valid 但语义不完整的 Evidence。

### F1.2 Effective Configuration 与 Admission

交付：

- Suite/Scenario/Driver/Lane Requirement 并集；
- 最严格的 Effective Budget；
- 可执行 Release Policy、Minimum-valid-run 和 Exception Semantics；
- Typed Driver Dispatch；
- 删除 Command-status-to-Oracle Projection。

Negative Control：

- 缺失只在 Suite 声明的 Prerequisite；
- Suite Budget 比 Scenario Budget 更严格；
- Allowed `unavailable` 只影响 Disposition，不改变 Run Truth；
- Expired Exception；
- P0 Exception Attempt；
- Command Exit 0，但 Required Oracle 没有 Evidence。

### F1.3 Runner Containment 与 Report

交付：

- 隔离的 Per-attempt Directory 和 Nonce-bound Evidence；
- Unix Process Group 和 Windows Job Object Ownership；
- 有界 Cancellation Escalation 和 Cleanup Evidence；
- Sanitized stdout/stderr Summary 和 Content Digest；
- Atomic、Non-overwriting Report。

Negative Control：

- Direct Parent Timeout 后 Descendant 存活；
- Descendant 在 Timeout 后继续写；
- Empty/Pre-existing Evidence File；
- PID、Port、Socket、Subscription、Lock 或 Temporary-path Leak；
- stdout/stderr 中包含 Secret；
- Output Truncation 后 Digest 仍正确。

F1 验收：

- 每个 Negative Control 都以预期 `invalid` 或 `failed` 状态失败；
- Direct Process、Process Tree、Evidence 和 Report 测试在适用处通过 Race；
- 生产 Package 不导入 Evaluation。

预计工作量：2.5 至 3 工程周。

## 5. F2：Evidence、Privacy、Promotion 与 Replay

### F2.1 Privacy Admission

交付：

- Enum/Allowlist-based Metadata Admission；
- Go 和 JSON Schema 共享同一 Conditional Privacy Contract；
- Evaluation Persistence 前 Redaction；
- Evidence、Manifest、Report、Index 和 Review Receipt 的全 Artifact Scan；
- 不会回显被拒绝内容的 Sanitized Error。

Negative Control 包含短 Secret、低熵内部 Credential、Unknown Key、Nested
Credential、Private Path、Endpoint、Multiline Content、Binary Input、Malformed JSON
和 Double Redaction。

### F2.2 Transactional Corpus Promotion

交付：

- `.tmp/evaluation/promotion/<batch-id>` Staging；
- 读取后重新检查 Source Digest；
- Trusted Producer 或显式 Synthetic Source Class；
- `promotion-review.json`；
- Complete-batch Atomic Install 和 Rollback；
- 不提供 Direct Tracked-corpus Default。

Negative Control：

- 第一 Slice 成功后第二 Slice 冲突；
- Install 前 Crash；
- Digest 与 Read 之间 Source Content 变化；
- Missing Review Receipt；
- Secret 位于 Manifest 而不是 Event Data；
- Extra Unscanned File。

### F2.3 Replay Level 与 Causal Closure

交付：

- 分类型 Structural、Provider、Runtime、Host 和 Crash Replay；
- Frame Mutation 进入 Production Provider Adapter；
- 通过 Controlled Tool 进入 Production Runtime Operation；
- ACP 和 Official VS Code Host Replay；
- Ancestor-closure Causal Slicer；
- Per-mutation Applicability 和 Execution Ledger。

Negative Control：

- Structural Replay 尝试满足 Runtime Replay；
- Provider Split Eligible Event 为零；
- Delay 未进入 Controlled Clock/Transport；
- Unknown Event 绕过 Production Compatibility Boundary；
- Causal Slice 缺少 Effect、Journal 或 Host Ancestor。

F2 验收：

- Tracked Corpus 只包含已 Review 的 Metadata-minimal Asset；
- 每个 Required Mutation 至少执行一次；
- 每个 Run 显示 Replay Level，Aggregation 不能升级 Level；
- Capture Failure 不能改变 Business Outcome。

预计工作量：2.5 至 3 工程周。

## 6. F3：Oracle、Scenario Pack 与 Impact

### F3.1 Evidence Adapter 与 Oracle Closure

交付：

- Runtime、Effect、Workspace、Verification、Persistence、Host、Security、Resource
  和 Deterministic Task-quality Oracle；
- 从 Production Fact 到 Admitted Evidence 的 Typed Adapter；
- Explicit Proved-zero Semantics；
- 每个 Oracle 的 Negative Control；
- 基于首个已证明 Contract Violation 的稳定 Attribution。

### F3.2 Scenario-specific Core Pack

交付：

- 至少 30 个独立 Scenario Family；
- 每个 Scenario 独立 Fixture Identity 和 Expected-fact Set；
- 完整 P0 Invariant-to-Scenario-to-Oracle Traceability；
- Required Fault 和 Mutation Matrix；
- Family Count 不能只靠重命名同一个 Fixture。

首批 P0 Family 覆盖 Terminal Cardinality、Durable Wait、Effect At-most-once、
Guard/Approval Binding、Crash Recovery、Outbox Publication、Host Projection、
Workspace Preservation、Sandbox Fail-closed、Secret 和 Resource Cleanup。

### F3.3 Fail-closed Impact Policy

交付：

- Critical Product-path Coverage；
- Unmatched Product Path 的 Full-P0 Fallback；
- Evaluation Self-change 时选择完整 Foundation；
- Explicit Documentation-only Exclusion；
- Explainable Selection Output。

F3 验收：

- Missing Evidence、Optional-only Mandatory Verification、Empty Fault Matrix、
  Reused Scenario Truth、Zero Mutation Execution 和 Empty Required Impact 全部失败；
- 每个 Scenario 可按 Identity 和 Digest 独立定位；
- 每个 P0 Invariant 具有完整 Traceability。

预计工作量：2.5 至 3 工程周。

## 7. Q1：Collect-All Qualification 与 Harness Freeze

### Q1.1 Collect-All Scheduler

交付：

- 完整 Scheduling Inventory；
- First Failure 不取消独立工作；
- Explicit Dependency-blocked 和 Infrastructure-canceled State；
- Complete Aggregate Report。

### Q1.2 Foundation Qualification Epoch

把全部 F1 至 F3 Contract 和 Negative Control 作为一个不可变 Epoch 执行。Focused
Rerun 可用于诊断，但修复后只有完整 Rerun 才能验收新 Epoch。

必需验证：

```bash
go test -count=1 ./evaluation/...
go test -race -count=1 ./evaluation/...
make test-hermetic
make architecture-ratchet
make docs-check
make book-check
git diff --check
```

Typed VS Code Driver 进入 Epoch 后，增加 VS Code Check。

### Q1.3 Harness Freeze

交付 `harness-lock.json`、Production Artifact Scan，以及 Source、Harness、Runtime 和
VSIX Partition 完全相同的连续三次完整 Integration Qualification Pass。

退出条件：`frozen_qualified`。

停止条件：任何 Drift、Required Capability Unavailable、Cleanup Uncertainty 或
Incomplete Inventory。

预计工作量：1.5 至 2 工程周。

## 8. D1：Collect-All Product Discovery

前置条件：Frozen Qualified Harness。

状态：已由 `product-discovery-d1-01` 完成。该 Round Settlement 包含 36 个 Core
Scenario、13 个 Fault Case、5 个 Host Case 与 2 次 Identity Check，56 项全部通过，
未准入 Product Candidate。

运行 Stream、Approval、Input、Cancel、Resume、Multi-Agent、Session Lifecycle、
Reload、Reconnect 和全部受支持 Host Variant。即使已有失败，也执行每个 Required
Attempt。

输出：

- 一份完整 Discovery Report；
- 分离 Product Hypothesis、Harness Incident 和 Environment Failure；
- 一次 Global Assessment；
- Approved Product Candidate List，或不授权产品修复的决策。

D1 不修改代码。

预计工作量：每个完整 Discovery/Assessment Cycle 1 工程周。

## 9. R1：Product Remediation 与 Verification

每个 Approved Product Candidate 需要：

- 所属 Package 内一个 Focused Product Work Unit；
- 一个绑定 Frozen Scenario 的 Minimal Regression；
- 不做无关 Framework Refactor；
- 全部 Approved Repair Settled 后执行一次完整 Verification Round。

修复若改变 Harness Input，会使 Harness Lock 失效；恢复产品结论前必须返回 Q1。

工期取决于重新发现的 Candidate，不计入 Foundation 工作量。

## 10. H1 至 H4：Production Admission

| Stage | Deliverable | 预计工作量 |
| --- | --- | ---: |
| H1 VS Code 与 Process Chaos | 已完成：五条 Lane 共 21/21 | 已完成 |
| H2 Live Model 与 Drift | 重复 DeepSeek Matrix、Identity Partition、Confidence、Cost、Drift | 2 工程周 |
| H3 Endurance 与 Release | 四小时 Workload、Slope、RC Evidence Aggregator、Release Gate | 2 工程周 |
| H4 Canary 与 Incident Closure | Controlled Inventory、Rollout Stop、Rollback、Incident-to-Corpus | 1 至 1.5 工程周 |

H1 完成前，H2 Evidence 不能参与 Release Admission。准备工作可以并行，Authority
不能并行提前获得。

## 11. 修订后的工期与关键路径

原“剩余 8 至 9 工程周”估算撤销，因为 17.3 已失效，17.1/17.2 也必须重新验收。

| 范围 | 工程周 |
| --- | ---: |
| S0 Specification | 0.5 至 1 |
| F1 Contract 与 Runner | 2.5 至 3 |
| F2 Privacy 与 Replay | 2.5 至 3 |
| F3 Oracle 与 Core Pack | 2.5 至 3 |
| Q1 Qualification 与 Freeze | 1.5 至 2 |
| D1 First Product Discovery | 1 |
| H1 至 H4 Admission System | 8 至 8.5 |
| 合计，不含未知 Product Repair | 18.5 至 21.5 |

两名工程师的预计自然时间为 13 至 16 周，因为 S0、Core Identity Contract、首轮
Qualification Epoch 和 Harness Freeze 位于关键路径。Contract 稳定后才开始并行。

## 12. 全局停止条件

出现以下情况时停止实现并执行 Global Assessment：

- 新症状表明存在规格未覆盖的 Shared Root；
- 同一 Boundary 在两轮修正 Epoch 后仍失败；
- 修复在 Runner、Fixture、Oracle 和 Product Code 之间反复迁移；
- 通过减少 Denominator 或修改 Expected Status 使 Gate 变绿；
- 用 Log 或 Prose 替代 Required Evidence；
- Cleanup、Privacy 或 Production Isolation 无法证明；
- Round 内 Source、Harness、Runtime 或 VSIX Identity Drift；
- 当前估算不再符合实际吞吐。

停止是控制动作，不是停止推进。

## 13. 批准边界

本计划下的批准已推进至 H1 完成。

已完成批准与剩余显式边界如下：

1. Foundation Implementation F1 至 F3；
2. Qualification 与 Harness Freeze Q1；
3. Product Discovery D1；
4. Approved Product Remediation R1；
5. Production Admission H1 已完成；H2 至 H4 仍分别保留独立门禁。

前一批准不隐含后一批准。
