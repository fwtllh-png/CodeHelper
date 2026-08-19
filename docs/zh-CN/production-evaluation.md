# 生产测评技术规格

简体中文 | [English](../en/production-evaluation.md)

> 状态：Foundation v2 与具备失败证据能力的 H2 Harness 已通过 Q1 Round 09
> 验收。D1 已 56/56 通过，H1 已 21/21 通过，H2 Re-entry Round 03 已 16/16
> 通过。Round 01 与 02 的 14/16 失败结论保持不可变。H3-H4 与完整 Release
> Admission 仍被禁止。
>
> 执行顺序和工期见[生产测评实施计划](./production-evaluation-implementation-plan.md)。
> 当前可信状态和缺陷见[异常与缺陷台账](./production-evaluation-findings.md)。

## 1. 决策与可信状态

目标质量模型仍然成立，但当前 Evaluation 实现不具备发布权威性：

| 范围 | 当前状态 | 允许用途 |
| --- | --- | --- |
| 17.1 Contract 与 Command Runner | 已作为 Foundation v2 输入验收 | Frozen Harness 使用 |
| 17.2 Capture 与结构 Replay | 已作为 Foundation v2 输入验收 | Frozen Harness 使用 |
| 17.3 Oracle 与 Core Pack | 已失效 | 不能作为准入证据 |
| Foundation v2 F1-F3 与 D1 Harness | 已通过 Q1 Round 06 验收 | Frozen Harness 权威 |
| D1 Product Discovery | 56/56 通过；无 Product Candidate | 不进入 Product Remediation |
| 具备 H1 能力的 Harness | 已通过 Q1 Round 07 验收 | Frozen Harness 权威 |
| 17.4 VS Code 与进程 Chaos | H1 已 21/21 通过 | 仅具 H1 证据；无 H2-H4 权威 |
| 具备 H2 能力的 Harness | 已通过 Q1 Round 09 验收 | Frozen Harness 权威 |
| Live Model 与 Drift | 治理化 Re-entry 后 Round 03 已 16/16 通过 | 仅具 H2 证据；无 H3-H4 权威 |
| 产品假设 PEC-0001 至 PEC-0004 | D1 未重新发现 | 仅保留历史状态 |

`make eval-contract-check`、`make eval-replay` 和 `make eval-oracle` 仍是诊断命令。
命令绿色不能批准产品变更、关闭 Harness Finding 或满足发布门禁。

当前重新准入决策为：

```text
Specification -> Foundation Work Unit -> 一次 Qualification Epoch
              -> Global Assessment -> Harness Freeze
              -> Collect-all Product Discovery -> Global Assessment
              -> 已批准 Product Remediation -> Chaos/Live/RC Admission
```

任何阶段都不能因代码存在或命令退出码为零而自行获得权威性。

## 2. 目标与非目标

### 2.1 目标

系统必须：

1. 评估真实生产 Runtime、Guard、Tool、Persistence 和 Host 路径，不建立第二条业务
   Loop；
2. 让所有结论来自类型化 Evidence，不把进程退出码复制给多个命名 Oracle；
3. 把 Source、Harness、Runtime、VSIX、Fixture、Provider、Model、Config、Seed 和
   Attempt 绑定为一个不可变 Run Identity；
4. 在必需 Evidence、Mutation 执行、Impact 覆盖、Cleanup Proof 或 Identity Binding
   缺失时 Fail Closed；
5. 聚合失败前收集全部独立结果；
6. 保留 First Attempt，并把恢复成功与首次成功分开；
7. 在 Evaluation 持久化前保护 Credential 和私有内容；
8. 在任何产品结论前冻结一个已验收 Harness Digest；
9. 把确认事故转成永久、脱敏、确定性的回归资产；
10. 让发布准入拒绝 Failed、Unavailable、Invalid、Stale、Identity Mismatch 和证据
    不完整状态。

### 2.2 非目标

系统不做以下事情：

- 把 Metadata Replay 当成 Runtime 或 Host Replay；
- 把内存纯函数重复执行当成 Flake Test；
- 让 LLM Judge 决定 Runtime、Security、Persistence 或 Effect 不变量；
- 让一个共享成功 Fixture 代表无关 Scenario Family；
- 绕过 Host、Guard、Approval、Constitution、Journal 或 Sandbox；
- 把私有 Capture 直接写入受 Git 跟踪的 Corpus；
- 用 Retry 隐藏首次失败；
- 在同一个 Discovery 或 Qualification Round 内修复产品；
- 把中间 Work Unit Test 当成完整 Foundation 的验收证据。

## 3. 规范原则

本文中的 **必须**、**禁止**、**必需** 和 **INVALID** 具有规范性。

1. **Evidence 先于结论。** 每个 Oracle Result 必须引用已准入 Evidence ID。Command
   Result 可以作为声明的 Verification Command Evidence，但不能复制成其他 Oracle
   结论。
2. **事实相互独立。** 每个 Scenario 必须标识自己的 Fixture、Expected Facts、
   Evidence Requirement、Oracle 和 Mutation。可以共享基础组件，不能共享 Scenario
   Truth。
3. **生产路径权威。** Evaluation 只编排受支持的生产入口；生产 Package 禁止导入
   `evaluation`。
4. **Fail Closed。** 必需 Evidence 缺失、必需选择为空、Fault 未触发、声明 Mutation
   未执行或 Cleanup 不确定时，结果为 `invalid`，不得为 `passed`。
5. **Collect All。** 某一失败不能停止无依赖关系的 Run 和 Fault Case；全部可调度项
   Settled 后才能返回聚合失败。
6. **不可变 Identity。** Report 禁止混合不同 Identity Partition；Artifact Filename
   必须无冲突。
7. **持久化前隐私保护。** Raw Command Output、Secret、用户路径和对话内容禁止进入
   Report、Tracked Corpus 或 Evaluation Log。
8. **禁止自证。** 同一个 Producer 不能成为创建并验证同一语义 Claim 的唯一权威。
9. **Qualification Epoch，不是巨型 PR。** Foundation 可拆成独立评审的 Work Unit；
   只有完整冻结集合通过同一个 Qualification Epoch 后才可信。
10. **禁止同 Round 修复。** 失败的 Epoch 或 Discovery Round 必须先关闭并完成 Global
    Assessment，才能开始修复。

## 4. 修正后的架构

```text
Versioned Foundation Specification
  |-- Scenario Contract
  |-- Evidence Requirement 与 Oracle Closure
  |-- Mutation 与 Impact Policy
  |-- Privacy、Cleanup 与 Isolation Contract
  `-- Admission Policy
                         |
                  Evaluation Coordinator
             /             |               \
       Typed Driver    Collect-all       Fault Controller
       CLI/ACP/VSIX      Scheduler       Proxy/Kill/Test-FS
             \             |               /
               Production Runtime and Guard
                         |
       Events / Facts / Receipts / Host / Workspace
                         |
          Evidence Admission 与 Provenance Binder
                         |
          Independent Oracle 与 Negative Control
                         |
    Qualification Report -> Harness Lock -> Release Evidence
```

### 4.1 Ownership

| 路径 | 职责 |
| --- | --- |
| `evaluation/spec` | 计划中的 Foundation、Scenario、Oracle、Mutation、Impact 和 Admission Contract |
| `evaluation/internal/runner` | Typed Driver Dispatch、Attempt Lifecycle、进程隔离和有界 Capture |
| `evaluation/internal/evidence` | Identity、Provenance、Admission、Canonical Encoding 和 Digest |
| `evaluation/internal/oracle` | 基于已准入 Evidence 的独立语义结论 |
| `evaluation/internal/replay` | 显式区分 Structural、Provider、Runtime、Host 和 Crash Replay |
| `evaluation/internal/qualification` | Collect-all Scheduling、Negative Control 和 Epoch Report |
| `evaluation/internal/freeze` | Harness Lock 构建和 Drift 拒绝 |
| `evaluation/internal/report` | 无冲突私有 Artifact 和聚合决策 |
| `evaluation/vscode` | 隔离的官方 VS Code Extension Host Driver |
| `.tmp/evaluation` | 私有 Raw Capture、Staging、Run Output 和可丢弃 Evidence |

这些是计划中的 Ownership 边界，文档提及不代表当前已经交付。

### 4.2 Production Isolation

- Evaluation 可以导入受支持的 Host 和 Runtime Contract。
- 生产 Go、TypeScript、Script、Generated Asset 和 Package 禁止导入或打包
  Evaluation Control Code。
- Crash Point 和 Fixture Control 必须要求 Test-only Authority，且不能进入 Production
  Binary 或 VSIX。
- Production Artifact Scan 是 Harness Freeze 的必需输入。

## 5. 机器契约集合

实现必须为以下 Artifact 提供严格、版本化 Schema，并拒绝未知字段：

| Artifact | 目的 |
| --- | --- |
| `foundation.json` | 根规格和必需 Contract 清单 |
| `scenario.json` | 一个可执行 Scenario 及其 Expected Facts |
| `oracle-contract.json` | Required Evidence、Absence Policy、Cardinality 和 Cross-check |
| `mutation-contract.json` | 适用 Input Class、Expected Observation 和最小执行次数 |
| `impact-policy.json` | Changed-path Rule、Critical Fallback 和 Uncovered-path Policy |
| `harness-lock.json` | Frozen Harness 的 Canonical Digest Input |
| `qualification.json` | 完整 Epoch Inventory、Identity、Result 和 Decision |
| `promotion-review.json` | Corpus Promotion 的隐私和正确性批准 |
| `release-evidence.json` | 发布准入消费的 Source-bound Lane Evidence |

### 5.1 Scenario Contract

每个 Scenario 必须声明：

```json
{
  "schema_version": 2,
  "id": "vscode-approval-runtime-restart",
  "family": "approval-recovery",
  "risk": "P0",
  "driver": "vscode-electron",
  "fixture_id": "approval-restart-v1",
  "run_plan": {"attempts": 3, "collect_all_group": "host-recovery"},
  "expected_facts": ["approval_parked", "runtime_killed", "terminal_once"],
  "required_evidence": ["runtime_events", "effect_ledger", "host_projection"],
  "required_oracles": ["runtime", "effect", "persistence", "host", "resource"],
  "required_mutations": ["kill_after_approval_park"],
  "cleanup_contract": "vscode-process-tree-v1",
  "impact_tags": ["runtime", "host", "interaction", "persistence"]
}
```

Contract Compiler 必须拒绝：

- 没有独立 `fixture_id` 和 Expected-fact Set 的 Scenario；
- 没有至少一个责任 Oracle 的 P0 Invariant；
- Evidence Closure 不完整的 Required Oracle；
- 没有兼容 Driver 或 Injection Point 的 Declared Mutation；
- 没有 Cleanup Owner 的 Scenario；
- 未进入受支持生产路径的 Driver。

### 5.2 Evidence Identity 与 Provenance

每条 Evidence 必须直接包含或继承：

```text
RunID, AttemptID, ScenarioID, SourceIdentity, HarnessIdentity,
RuntimeArtifactIdentity, HostArtifactIdentity, FixtureIdentity,
ProviderIdentity, ModelIdentity, ConfigIdentity, Seed, Producer,
EvidenceKind, EvidenceDigest
```

Canonical Run Partition 为：

```text
SHA256(
  schema_version || source || dirty_digest || harness_digest ||
  runtime_digest || host_digest || scenario_digest || fixture_digest ||
  provider_digest || model_digest || config_digest || seed || attempt
)
```

规则：

- 不属于精确 Run Partition 的 Evidence 必须拒绝；
- Evidence File 位于进程启动前新建的 Per-attempt Directory，禁止复用；
- Producer 先写临时文件，再用 Final Digest 原子 Seal；
- 空文件不能满足 `required_evidence`；
- Report Aggregation 拒绝混合 Partition；
- Artifact Name 包含 Scenario、Variant、Attempt 和 Run Partition；已存在文件是错误，
  不是覆盖目标。

### 5.3 Oracle Closure

Oracle Contract 定义：

- 可准入的 Evidence Kind 和 Producer Identity；
- Required/Optional Cardinality；
- 已证明的零值是否有效；
- Evidence 缺失、Malformed、Stale 或 Identity Mismatch 时的状态；
- Cross-evidence Comparison；
- 负责的 Failure Domain；
- 证明 Oracle 能发现目标缺陷的 Negative Control。

仅有 `evidence_available: true` 而没有 Provenance，不构成 Evidence。

| Oracle | 必需证明 |
| --- | --- |
| Runtime | Accepted Operation、Ordered Events/Facts、Terminal 或 Durable Park Owner/Deadline、Lease 和 Resume Binding |
| Effect | Effect Claim、Guard/Approval Binding、Journal 或 External Counter、Execution/Result Cardinality |
| Workspace | Before/After Tree Digest、Expected/Forbidden Path、Pre-existing Dirty Preservation |
| Verification | Scenario 声明的精确 Mandatory Command、Exit/Signal/Timeout、Output Digest、Execution Identity |
| Persistence | Event/Fact Rebuild、Snapshot、Receipt、Terminal、Outbox、Reopen 和 Recovery Identity |
| Host | ACP Cursor、Visible Item、Wait、Terminal、Reload/Reconnect Projection 和继续操作能力 |
| Security | Guard、Policy、Approval、Constitution、Sandbox、Egress、Secret Scan 和 Fail-closed Outcome |
| Resource | Process Identity、Process Group、FD、Subscriber、Port、Temporary Path、Queue、RSS 和 Persistence Slope |
| Task Quality | 优先确定性 Task Assertion；LLM Judge 只用于非 P0 Explanation Quality |

Applicability 在 Scenario Contract 编译时确定。Run 不为不适用 Oracle 生成合成
`passed`。

### 5.4 Admission Status

允许的结果状态保持为：

| 状态 | 含义 |
| --- | --- |
| `passed` | 已执行，全部 Required Evidence 和 Assertion 通过 |
| `failed` | 已执行，产品或环境行为违反 Contract |
| `unavailable` | 声明的非产品 Capability 不可用 |
| `not_evaluated` | 批准的 Run Plan 未包含该项 |
| `invalid` | Harness、Identity、Evidence、Selection、Injection、Cleanup 或 Privacy Contract 失败 |

对于 P0 Required Work，只有 `passed` 可准入。`unavailable`、`not_evaluated` 和
`invalid` 不能减少分母，也不能通过 Exception 变成成功。

## 6. Runner 与 Collect-All Execution

### 6.1 Effective Configuration

Runner 必须计算唯一 Effective Configuration：

- Requirement 是 Suite、Scenario、Driver 和 Lane Requirement 的并集；
- Budget 采用全部适用限制中的最严格值；
- Exception 只由 Admission Evaluator 应用；
- `minimum_valid_runs`、Repetition 和 Attempt Limit 在 Collect-all Settlement 后执行；
- Nonblocking Policy 只影响 Release Disposition，不改变 Run Status Truth。

### 6.2 Process 与 Workspace Isolation

每个 Attempt 获得隔离的 Workspace、HOME、State、Extension、User Data、Port、
Socket、Evidence 和 Report Directory。

Timeout、Cancel 或 Shutdown 时，Runner 必须：

1. 阻止创建新 Child；
2. 终止完整 Process Group 或平台 Job Object；
3. 在有界升级流程中等待 Settled；
4. 验证没有 Owned PID、Port、Socket、Subscription、Lock 或 Temporary Path；
5. 生成 Cleanup Evidence；
6. Ownership 或 Cleanup 不确定时将 Attempt 标为 `invalid`。

Raw stdout/stderr 只是有界 Private Capture Input。Report 只能保留脱敏 Summary、Byte
Count、Truncation Status 和 Content Digest。

### 6.3 Collect-All Scheduler

- 聚合前先调度全部独立 Run、Host、Attempt 和 Fault Case；
- 一个失败不能取消无关工作；
- Dependency-blocked Work 必须显式报告；
- Infrastructure Cancellation 记录所有未调度或中断项；
- Scheduler 返回完整 Inventory，缺失 Required Item 时禁止报告成功。

## 7. Replay、Mutation 与 Causality

Replay Level 是相互独立的 Capability：

| Level | 必需执行 |
| --- | --- |
| Structural Replay | 验证 Canonical Evidence、Hash Chain、Identity 和 Causal Graph |
| Provider Replay | 让 Recorded Frame 进入生产 Provider Adapter |
| Runtime Replay | 通过受控 Tool 向生产 Runtime 提交 Operation |
| Host Replay | 驱动 ACP 或 VS Code Projection、Cursor、Wait 和 Reconnect |
| Crash Replay | 在命名 Durable Boundary 杀进程并恢复同一状态 |

低层 Replay 禁止满足高层 Scenario。

每个 Mutation Contract 声明 Applicability 和 Expected Observation。Qualification
按 Mutation 报告 Eligible Count、Executed Count、Detected Count、
Rejected-invalid Count 和 Skip Reason。Required Mutation 的 Eligible 或 Executed
次数为零时结果为 `invalid`。

Provider Split Mutation 必须保留可重建 Frame Byte 并进入生产 Parser。Delay 使用受控
Clock 或 Transport。Unknown 和 Malformed Input 必须进入目标 Compatibility Boundary。

Causal Slicing 必须跨 ACP、Runtime、Provider、Effect、Journal 和 Host Identity 计算
目标 Failure 的 Ancestor Closure。仅按 Identity Filter 不构成 Causal Slice。

## 8. Impact Selection

Impact Policy 必须包含：

- `cmd`、`internal/config`、Runtime Protocol、Provider、MCP、Tool、Security、
  Persistence、Orchestration、Host、VS Code、Script、Schema、`go.mod`、`go.sum`
  和 Release Workflow 的 Critical Mapping；
- Valid 但 Unmatched Product Path 的 Full-P0 Fallback；
- Evaluation Contract、Oracle、Fixture、Mutation Logic 或 Impact Policy 自身变更时
 选择完整 Foundation；
- Documentation-only Change 的显式排除规则；
- Matched Rule 和每个 Selected Scenario 的原因。

Required Lane 的 Empty Selection 为 `invalid`。

## 9. Privacy 与 Corpus Promotion

### 9.1 Data Boundary

- Raw Capture 只保存在授权的本地私有目录，权限 `0600` 且 Retention 有界；
- Source Format 允许时，Capture Producer 在写 Evaluation-readable File 前脱敏
  Credential 和 Restricted Content；
- Evaluation 禁止把 Raw Input 复制到 Log、Error、Report 或 Corpus；
- Allowed Metadata Value 使用 Enum 或 Allowlist，禁止使用通用“像协议字符串”放行；
- Secret Scan 覆盖 Manifest、Report、Index 和 Evidence 等全部 Promoted File；
- Go Validation 和 JSON Schema 必须表达相同的 Conditional Privacy Rule。

### 9.2 Promotion Transaction

Promotion 必须先写入 `.tmp/evaluation/promotion/<batch-id>`。Tracked Corpus 不提供
Direct-write Default。

只有满足以下条件，Batch 才能准入：

1. 所有 Slice 在声明的 Replay Level 完成 Canonicalize 和 Replay；
2. 所有文件通过 Schema、Path、Credential、Entropy 和 Restricted-content Scan；
3. 读取后重新检查 Source Digest，防止 Source Substitution；
4. Source Class 由 Trusted Producer 证明，否则标记 Synthetic；
5. Human Privacy/Correctness Review 生成 `promotion-review.json`；
6. 完整 Batch 原子安装；
7. 失败时不留下任何 Promoted Subset。

Hash Chain 证明内部完整性，不证明来源真实性。Provenance 和 Review Receipt 才是
Trust Root。

## 10. Harness Qualification 与 Freeze

### 10.1 Foundation Qualification Epoch

Epoch Input 是一组不可变的 Work Unit Commit，必须执行：

- Contract/Schema Positive 和 Negative Test；
- 全部 Scenario-specific Fixture 和 Expected Fact；
- Oracle Negative Control 和 Cross-evidence Check；
- 全部 Declared Mutation，且执行次数非零；
- Impact Known、Unknown、Critical 和 Self-change Case；
- Process-tree Timeout、Cancellation 和 Cleanup；
- Privacy Bypass 和 Corpus Batch Rollback；
- Report Collision、Stale Evidence 和 Mixed Identity；
- Production Artifact Isolation Scan；
- 全部并发组件的 Race Test。

任一失败都关闭 Epoch 并触发一次 Global Assessment。禁止在失败 Epoch 内做 Focused
Repair。

### 10.2 Harness Lock

`harness-lock.json` 的 Canonical Digest 包含：

- 全部 Schema、Contract、Fixture、Prompt、Routed/Default Provider Stream、Request
  Fragment、Assertion、Driver、Adapter、Fault Control、Cleanup Contract、Config、
  Build Tag 和 Test Authority；
- Evaluation Binary、Runtime Binary、VSIX 和 Production-isolation Scan；
- 构建时 Source Provenance 和 Toolchain Identity。

Harness Lock v3 还记录规范化 `input_roots`。Live Identity Verification 会重新枚举这些
Root，并要求完整 Path/Digest 集合一致，从而拒绝 Executable Input 的修改、新增或
删除。Qualification 后的 Assessment 与双语治理文档位于 Executable Input Root
之外，因此发布它们不会改变已验收 Candidate Identity；但它们不能修改 Harness
Code、Runtime Source、Fixture、Test、Schema 或 Build Input，否则 Lock 立即失效。

同一个 Lock 必须连续三次通过完整 Integration Qualification。任何输入变化都会创建
新 Lock 并重置计数。

只有这样 Harness Status 才能变成 `frozen_qualified`。

## 11. Product Discovery 与 Remediation

Harness Freeze 后：

1. 运行全部 Required Host、Scenario、Attempt 和 Fault Case；
2. 保留全部结果，包括 First Failure 之后的失败；
3. 不修改产品地关闭 Round；
4. 完成一次 Global Assessment，并按 Systemic Root 聚类症状；
5. 批准或拒绝 Product Candidate；
6. 在独立 Product Work Unit 中修复已批准 Candidate；
7. 增加绑定 Frozen Scenario Contract 的 Minimal Regression；
8. 使用同一个 Harness Lock 或显式重新验收的后继 Lock 执行新 Verification Round。

历史 PEC ID 在此流程下重新发现前都只是 Hypothesis。

## 12. Release Lane 与 Admission

| Lane | Required Evidence | Authority |
| --- | --- | --- |
| Foundation Contract | Schema、Negative Control、Privacy、Identity | 阻断 Foundation Qualification |
| P0 Replay | 受影响 Provider/Runtime/Host/Crash Replay | Freeze 后阻断 Merge |
| Integration | Real Binary、ACP、Official VS Code、Fixture Provider | Freeze 后阻断 Main |
| Chaos | Process、Network、SQLite、Filesystem、Concurrency Matrix | 阻断 Release |
| Live | 重复 Model Matrix、Drift、Cost、Task Quality | 阻断 Release |
| Endurance | 四小时 Workload 和 Resource Slope | 阻断 RC |
| RC | 绑定 Source/Runtime/VSIX/Harness 的 Aggregate | 阻断 Candidate |
| Canary | Controlled Rollout 和 Rollback Signal | 阻断扩大范围 |

Release Admission 要求：

- P0 Invariant：100%；
- Duplicate Consequential Effect：0；
- Missing/Multiple Terminal：0；
- Ownerless Running/Parked State：0；
- Guard、Sandbox、Privacy 和 Production-isolation Violation：0；
- Required `unavailable`、`not_evaluated`、`invalid`、Unmatched Impact 和
  Unexecuted Mutation：0；
- Unattributed P0/P1：0；
- Source、Harness、Runtime、VSIX、Provider、Model 和 Config Partition 精确匹配；
- 无 Stale 或 Expired Evidence。

统计 Live Threshold、Cost Budget 和 Endurance Slope 是版本化 Policy，不能抵消 Hard
Invariant。

## 13. 当前资产处置

以下实现思路只有通过 Negative Requalification 后才能保留：

- Strict JSON Decode 和 Unknown-field Rejection；
- Source Commit 和 Dirty-content Digest；
- Bounded Output Collection；
- Mode `0600` Atomic File Write；
- Canonical Evidence Encoding 和 SHA-256 Chain Validation。

以下行为必须替换：

- 把 Command Status 复制给全部 Oracle；
- 让 Suite Policy 和 Budget 只参与配置校验；
- 接受 Empty、Stale 或 Unbound Evidence；
- 直接向 Tracked Corpus Promotion；
- 使用 Generic String Allowlist 处理 Privacy；
- Scenario Family 共享同一 Truth Fixture；
- Optional-only Verification 通过 Mandatory Contract；
- Optional 或 Empty Fault Coverage；
- Empty Impact Success；
- 用 Metadata-only 500-run Replay 充当 Flake Gate；
- Report Filename 只按 Attempt 命名。

当前绿色计数只是历史诊断，不是 Migration Baseline。

## 14. 规格验收

只有满足以下条件，本规格才可进入实现批准：

1. 中英文文档作为同一变更完成 Review；
2. Implementation Plan 把每个审计 Root 映射到 Work Unit 和 Gate；
3. 全部 Planned Machine Artifact 都有 Owner、Producer、Consumer 和 Fail-closed Rule；
4. Qualification Epoch 与 PR Work Unit 的区别得到确认；
5. Reset Assessment 和 Findings Register 与本文可信状态一致；
6. `make docs-check`、`make book-check` 和 `git diff --check` 通过。

D1、H1 与 H2 完成不授权完整 Release Admission。H3-H4 仍分别保留显式授权和
Admission Gate。
