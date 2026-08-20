# 生产测评实施计划

简体中文 | [English](../en/production-evaluation-implementation-plan.md)

> 状态：修复后的 H3 Harness 已由 Q1 Round 13 冻结。同 Lock H1 Round 03 已
> 21/21 通过，H2 Round 05 已 16/16 通过，正式 H3 Round 02 已 14/14 通过并得到
> `admit` RC 决策。H4 仍保留独立门禁且未获授权。H4 Implementation 与 Controlled
> Preflight 已完成。重新验收已完成 Q1 Round 14、H1 Round 04 与 H2 Round 06；
> 所需的四小时同 Lock H3 Round 03 因 Operator Time Budget 在 96/480 Turn 时停止，
> 因此正式 H4 继续延期。重复这条未变化的 Chain 不再是下一优先级。D2 复杂场景
> Discovery 的 D2.1 与 D2.2 已在独立 Discovery Lock 下实现并验收。D2.3 Round
> 05 已完成 129/129 Case 结算，并准入 93 个 Harness Incident 与 1 个
> Unattributed Live Observation。Driver Execution Remediation 现已由
> `complex-discovery-d2-drivers-26` 验收：基于 105 个 Case 与 376/376 Pairwise
> Coverage 的 18/18 Check 已通过；该浅层 Round 未准入 Product Candidate。已授权的
> Semantic/In-path 循环在 Round 10 收敛为 20/20 Settled、17 Passed、3 个
> Exact-seed Product Candidate、0 Harness Incident。受影响操作为
> `thread.compact`、`thread.fork` 与 `turn.revert`；它们都能在 Turn Parked on
> Approval 时阻塞后续 Cancel。Product Remediation 仍由独立门禁控制且未授权。

| Stage | 状态 |
| --- | --- |
| S0 | 已完成 |
| F1 Contract、Identity、Admission、Runner | 已验收 |
| F2 Privacy、Promotion、Replay | 已验收 |
| F3 Oracle、Core Pack、Impact | 已验收 |
| Q1 Qualification 与 Freeze | 具备 H3 能力的后继已由 Round 13 完成；`frozen_qualified` |
| D1 Collect-all Product Discovery | 已完成 56/56；无 Product Candidate |
| D2 复杂场景 Discovery | Semantic Round 10 已关闭；3 个 Exact-seed Product Candidate |
| H1 VS Code 与 Process Chaos | 同 Lock Round 03 已完成 21/21 |
| H2 Live Model 与 Drift | 同 Lock Round 05 已完成 16/16 |
| H3 Endurance 与 Release | Round 02 已完成 14/14；RC Candidate 已准入 |
| H4 Canary 与 Incident Closure | Implementation/Preflight 已完成；正式准入延期 |

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
H2 Preflight 在一次分离的 Remediation Cycle 后关闭，Q1 Round 08 冻结首个具备
H2 能力的后继。正式 Round 01 与 02 每轮均通过 11/12 个 Live Sample。Schema v2
失败证据随后进入 Q1 Round 09。固定 12 样本诊断矩阵和 36 样本 Evidence-driven
Investigation 授权一次 Policy 不变的 Re-entry；Round 03 已 16/16 通过。不可变
决策位于
`evaluation/assessments/h2-preflight-global-assessment-01.json` 至 `-03.json`、
`evaluation/assessments/q1-qualification-global-assessment-08.json` 至 `-09.json`、
`evaluation/assessments/h2-reentry-decision-01.json`、
`evaluation/assessments/h2-reentry-global-assessment-01.json` 以及
`evaluation/assessments/h2-production-admission-global-assessment-01.json` 至
`-03.json`。

H3 Preflight 随后交付四小时 Endurance Driver、固定 Resource Slope、Release/VS
Code RC/Package Lane 与八 Lane RC Aggregator。正式 Round 01 完成 480/480 个 Turn，
但在未改变的 Persistence Slope 上限下以 498,233 bytes/Turn 失败。分离的 Assessment
与 Remediation 将增长归因到 Terminal Envelope 中累计的 Session Delta Payload
（H3P-0003）、CAS 中累计的自动 Checkpoint Content（H3P-0004），以及 Sampler 的
瞬时 `ENOENT` 竞争（H3H-0009）。Deterministic Bounded Gzip Encoding 与窄范围
Sampler 修复将 480-Turn Verification Slope 降至 133,391 bytes/Turn，未修改
Threshold 或 Denominator。

由于产品修复改变了 Frozen Input，Q1 重新开始。Round 11 作为不可变的 Invalid
Identity 历史保留；Round 12 的一次 `evaluation-race` Command 失败在三次精确确认中
均未复现，因此该 Round 未被复用。Round 13 通过 8/8 Foundation Task 与连续三次
7/7 Integration Run。在该 Lock 上，H1 Round 03 已 21/21 通过，H2 Round 05 已
16/16 通过。正式 H3 Round 02 随后通过全部 14 个 Coordinator Task，完成 480/480
个 Endurance Turn，并准入全部 8 条 Required RC Lane。决策记录在
`evaluation/assessments/h3-production-admission-global-assessment-01.json` 至
`-06.json`、`evaluation/assessments/q1-qualification-global-assessment-11.json`
至 `-13.json`，以及同 Lock H1/H2/H3 Machine Report 中。

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
8. D2 Campaign Definition 可以在 Round 之间演进，但每个已开始 Round 都不可变，
   且必须在修复前完成 Assessment 并关闭；
9. D2 Evidence 不能满足或豁免 H1-H4 Admission Evidence。

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

## 9. D2：复杂场景 Discovery

前置条件：

- Production Runtime Path 和现有 Harness Primitive 保持已验收状态；
- D2 使用独立的版本化 Discovery Lock、Campaign Identity、Input Root 和 Output
  Root；
- D2 不要求正式 H4 完成，也不产生发布权威；
- 实现 Budget 与首轮 Campaign Portfolio 必须在执行前获批。

### D2.1 Campaign Contract 与 Portfolio

状态：已由 `complex-discovery-d2-foundation-01` 完成。12 项 Qualification Check
全部通过。确定性 Plan 包含 7 个独立 Campaign Family 的 129 个 Case，覆盖 539/539
个 Pairwise Interaction、7/7 个 Required Combination、12/12 个 Boundary Value 和
5/5 个 Fault Trigger。Qualified Discovery Lock 引用 Q1 Round 14，但不改变或替代其
Admission Evidence。

交付：

- 严格的 Campaign、Observation、Plan、Discovery Lock 和 D2 Qualification
  Schema；
- 引用已验收 Base Harness/Runtime/Host Partition，并对单独枚举的
  `discovery_input_roots` 求 Hash 的 Discovery Lock；
- Canonical Campaign、Case、Seed、Selection、Environment 和 Artifact Identity；
- 覆盖 Workload、State、Topology、Dependency Behavior、Lifecycle 和 Model
  Variability 的 Declared Axis Catalog；
- Budget、Stop、Cleanup 和 Privacy Policy；
- 报告 Selected、Unselected、Pairwise、Required Higher-order、Boundary 和
  Fault-trigger Combination 的 Coverage Planner；
- 显式区分 Deterministic、Timing-sensitive 和 Live Statistical Campaign。

验收：

- Unknown Field、Empty Axis、Zero-case Selection、Mixed Identity、Undeclared
  Adaptive Choice、Missing Cleanup 和 Unbounded Budget 均被拒绝；
- D2 Contract、Planner、Generator、Fault Control、Privacy、Identity 和 Cleanup
  Negative Control 作为一个 D2 Qualification Epoch 通过；
- 仅 D2 的 Drift 使 Discovery Lock 失效，但不声称未变化的 H1-H4 Evidence 过期；
- Generated Case 不能声称为独立 Campaign Family；
- 相同 Campaign 和 Seed Set 生成相同 Planned Inventory。

预计工作量：0.5 至 0.75 工程周。

### D2.2 Production Driver 与 Generator

状态：最初由 `complex-discovery-d2-drivers-03` 完成；D2.3 Implementation 的后继
为 `complex-discovery-d2-drivers-09`。15 项 Driver Qualification Check 全部基于
冻结的 Q1 Round 14 Runtime 与 VSIX 通过。生成的 Inventory 保持全部
129 个 Planned Case，其中 ACP 75 个、CLI 26 个、Official VS Code Path 28 个。
Provider、Process、Persistence、Filesystem、MCP 与 Guarded Tool Fault Control
均有非零 Trigger Evidence，并在 Qualification 中各实际触发一次。Same-seed
Inventory、Schedule 与 1200 文件 Synthetic Repository Replay、Bounded Scale、
精确 Resource Cleanup、Privacy Closure 与三项 Production Boundary Negative
Control 均通过。没有执行 Campaign Case，也没有评估 Product Candidate。

交付：

- 通过受支持 CLI、ACP 和 Official VS Code Path 生成 Stateful Repository Journey；
- 生成 Long Session、Checkpoint、Resume、Cancellation、Compaction 和 Interrupted
  Effect State；
- Controlled Concurrency 和 Schedule Recording；
- 可组合 Provider、Process、Persistence、Filesystem、MCP 和 Tool Fault Control；
- Scale/Long-tail Repository、Context、Output 和 Durable-state Input；
- Upgrade、Rollback、Reconnect 和 Crash-recovery Journey；
- 跨 Host、Restart Boundary 和等价 Task Form 的 Differential/Metamorphic
  Assertion；
- 每个 Generated Case 的 Resource Ownership 和精确 Cleanup Evidence。

实现约束：

- 使用 Production Host/Runtime Entry Point 和现有 Guarded Tool；
- Generation 和 Fault Authority 保留在 `evaluation`；
- 禁止引入第二条 Business Loop 或 Production-only Test Switch；
- 使用有界 Synthetic Repository 和已评审 Private Fixture；禁止把 User Content
  写入 Report 或 Tracked Corpus。

验收：

- 每个 Driver 都有一个 Negative Control，证明它进入预期 Production Boundary；
- 每个 Declared Fault 都有非零 Trigger Evidence；
- Same-seed Deterministic Replay 可复现 Planned Input 和 Schedule；
- Cancellation/Timeout 后不残留 Owned Process、Lock、Port、Temporary Path、
  Subscription 或 Durable Test State。

预计工作量：1.5 至 2 工程周。

### D2.3 首轮 Collect-All Campaign

状态：已由 `complex-discovery-d2-campaign-05` 执行并关闭。最终不可变 Round 完成
129/129 Case 结算，其中 35 个 Passed、0 个 Budget Skip、93 个 Exact-seed Harness
Incident、1 个 Unattributed Live Observation。70 个 Fault-bearing Case 均关闭其
声明的 Trigger Evidence，129 个 Synthetic Workspace 均有不同 Digest。未准入
Product Candidate。

这些 Harness Incident 证明 D2.2 生成的 Journey 超出了 Driver 的可执行覆盖。最大
Missing Step Cluster 为 Compaction Observation（33）、Runtime Restart/Reconnect
（各 29）、Session Extension/Checkpoint Restore（各 28）、Upgrade（20）、Crash
Recovery（19）与 Rollback（19）。`differential_host-009` 保持 Unattributed，因为
两次 Live Attempt 结果不同，且现有 Live Smoke 未证明其声明的 ACP Path。Driver
Execution Remediation 与后继 Qualification Epoch 关闭这些缺口前，不授权重复
Campaign。

该 Remediation 已由 `complex-discovery-d2-drivers-26` 关闭。静态 Step 推断已替换为
有序 Driver Receipt；CLI、ACP 与 Official VS Code Runtime Client 现会真实执行并
证明确定性的 Compaction、Checkpoint、Cancellation、Restart、受控 Artifact
Replacement、Rollback 与 Reconnect Probe。Qualification 还会对 Host Routing
Fail Closed。后继 Contract 包含 105 个 Case，并关闭 376/376 Pairwise Interaction。
Live Model Variability 被隔离到权威的 CLI Single-Turn Smoke。`subagent_worker`
在 Dedicated Driver 能执行并证明 `spawn_agent` 前被排除，ACP Reachability 不能
替代该 Topology。

独立授权的 Semantic/In-path 循环使用后继 Lock
`complex-discovery-d2-drivers-36`，将 Catalog 从 8 个增加到 20 个真实生产路径
Case，覆盖 Approval 与 Workspace Effect、Checkpoint/Compaction Recovery、
Concurrent Session、Same-session Reentry、MCP Cancellation、Multi-host Workspace
Conflict 与 Active-thread Mutation Exclusion。Round 10 关闭为 17 Passed、3 个
Exact-seed Product Candidate、0 Harness Incident。三个 Candidate 在
`thread.compact`、`thread.fork` 与 `turn.revert` 上共享同一个系统性 Runtime
Dispatch 根因。

首个不可变 Round 至少运行以下独立 Campaign Family：

1. Stateful Edit/Verify/Checkpoint/Resume Journey；
2. Concurrency 与 Cancellation Interleaving；
3. 组合 Dependency 与 Persistence Fault；
4. Scale 与 Long-tail State；
5. Differential/Metamorphic Host Behavior；
6. Upgrade、Rollback、Reconnect 与 Recovery；
7. 单独设置 Budget 的 Live Model Variability。

执行前固定最大 Run 数、Wall-clock、Model Cost、Parallelism、Seed 和 Stop Policy。
在 Aggregate Judgment 前调度全部独立 Case，并记录 Budget-skipped Work，禁止减少
Declared Denominator。

输出：

- 完整 Inventory 与 Coverage Ledger；
- 带 Causal Evidence 的 Admitted Observation；
- Deterministic Failure 的精确 Rerun，以及 Timing-sensitive Failure 的 Bounded
  Reproduction Matrix；
- Product Candidate、Harness Incident、Environment Failure、Expected Variance
  或 Unattributed 分类；
- Resource 与 Privacy Closure。

预计工作量：首轮有界 Round 为 1 工程周。

### D2.4 Global Assessment 与 Re-entry

修改 Product 或 Harness Code 前先关闭 Round。按最早已证明 Boundary 聚类症状，保留
First-attempt History，并决定：

- 哪些 Product Candidate 获准进入独立 Remediation；
- 哪些 Harness Incident 要求新 Qualification Epoch；
- 哪些 Environment Failure 可以在 Matrix 不变时重试；
- 哪些 Unattributed P0/P1 Observation 阻断 Re-entry；
- 哪些 Minimal Redacted Asset 可以进入 Corpus Promotion Review。

Focused Diagnosis 可以在关闭后执行，但不能重写该 Round。Remediation 与 Verification
随后使用 R1 流程。

预计工作量：0.5 至 0.75 工程周。

D2 首次实现总估算：3.5 至 4.5 工程周，不含未知产品修复。

## 10. R1：Product Remediation 与 Verification

每个 Approved Product Candidate 需要：

- 所属 Package 内一个 Focused Product Work Unit；
- 一个绑定 Frozen Scenario 的 Minimal Regression；
- 不做无关 Framework Refactor；
- 全部 Approved Repair Settled 后执行一次完整 Verification Round。

修复若改变 Harness Input，会使 Harness Lock 失效；恢复产品结论前必须返回 Q1。

工期取决于重新发现的 Candidate，不计入 Foundation 工作量。

## 11. H1 至 H4：Production Admission

| Stage | Deliverable | 预计工作量 |
| --- | --- | ---: |
| H1 VS Code 与 Process Chaos | 已在 H3 Lock 上再次完成：Round 03 已 21/21 通过 | 已完成 |
| H2 Live Model 与 Drift | 已在 H3 Lock 上再次完成：Round 05 已 16/16 通过 | 已完成 |
| H3 Endurance 与 Release | 已完成：Round 02 已 14/14 通过并准入 8/8 RC Lane | 已完成 |
| H4 Canary 与 Incident Closure | Controlled Inventory、Rollout Stop、Rollback、Incident-to-Corpus | 1 至 1.5 工程周 |

H3 已完成。其 `validated-dry-run` Candidate 未上传且不可发布，也不授权 H4
Canary 或 Rollout Expansion。

## 12. 修订后的工期与关键路径

原“剩余 8 至 9 工程周”估算撤销，因为 17.3 已失效，17.1/17.2 也必须重新验收。

| 范围 | 工程周 |
| --- | ---: |
| S0 Specification | 0.5 至 1 |
| F1 Contract 与 Runner | 2.5 至 3 |
| F2 Privacy 与 Replay | 2.5 至 3 |
| F3 Oracle 与 Core Pack | 2.5 至 3 |
| Q1 Qualification 与 Freeze | 1.5 至 2 |
| D1 First Product Discovery | 1 |
| D2 Initial Complex-scenario Discovery | 3.5 至 4.5 |
| H1 至 H4 Admission System | 8 至 8.5 |
| 合计，不含未知 Product Repair | 22 至 26 |

包含 D2 后，两名工程师的预计自然时间为 15 至 19 周，因为 S0、Core Identity
Contract、首轮 Qualification Epoch、Harness Freeze 和 D2 Contract Qualification
位于关键路径。Contract 稳定后才开始并行。

## 13. 全局停止条件

出现以下情况时停止实现并执行 Global Assessment：

- 新症状表明存在规格未覆盖的 Shared Root；
- 同一 Boundary 在两轮修正 Epoch 后仍失败；
- 修复在 Runner、Fixture、Oracle 和 Product Code 之间反复迁移；
- 通过减少 Denominator 或修改 Expected Status 使 Gate 变绿；
- 用 Log 或 Prose 替代 Required Evidence；
- Cleanup、Privacy 或 Production Isolation 无法证明；
- Round 内 Source、Harness、Runtime 或 VSIX Identity Drift；
- 当前估算不再符合实际吞吐。
- D2 Campaign 产生相关的 Systemic Symptom，需要先 Review Portfolio 再运行更多
  Case；
- D2 Exploration 反复在 Driver、Fault Injection、Environment 与 Product 之间迁移
  Failure，且归因不收敛。

停止是控制动作，不是停止推进。

## 14. 批准边界

本计划下的批准已推进至 H3 完成。

已完成批准与剩余显式边界如下：

1. Foundation Implementation F1 至 F3；
2. Qualification 与 Harness Freeze Q1；
3. Product Discovery D1；
4. Approved Product Remediation R1；
5. Production Admission H1、H2 与 H3 已完成；
6. H4 Canary 与 Incident Closure 已延期且未获授权；
7. D2.1 与 D2.2 Implementation/Qualification 已完成；
8. D2.3 Round 05 与 Semantic Round 10 已关闭；Driver/Semantic Qualification
   绑定 `complex-discovery-d2-drivers-36`；
9. 已确认 3 个 D2 Product Candidate，但 D2.4 Re-entry 与 Product Remediation
   需要独立批准。

前一批准不隐含后一批准。
