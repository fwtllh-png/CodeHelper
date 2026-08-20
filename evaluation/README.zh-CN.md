# CodeHelper 生产测评

[English](./README.md) | 简体中文

`evaluation/` 统一管理所有受 Git 跟踪的生产测评代码和资产。临时运行证据写入
`.tmp/evaluation/`，不进入源码目录。

## 目录

```text
evaluation/
  cmd/codehelper-eval/   测评 CLI
  d2/                    独立 D2 Contract、Planner、Lock 与 CLI
  internal/spec/         Manifest、Scenario、Run、Policy 契约与校验
  internal/source/       Source Commit 与 Dirty Digest
  internal/runner/       拥有 Process Tree 的执行、Attempt 与绑定 Evidence
  internal/report/       稳定 JSON/Markdown 报告
  internal/foundation/   Foundation Inventory、Oracle/Mutation Catalog 与 Digest
  internal/evidence/     统一 Evidence Envelope 与哈希链
  internal/capture/      Capture Adapter、脱敏与因果切片
  internal/replay/       确定性 Replay 与 Mutation
  internal/corpus/       Corpus Promotion、校验与 Replay 准入
  internal/oracle/       九类绑定 Provenance 的语义 Oracle 与故障注入
  internal/corepack/     核心场景包、影响映射与验收
  fixtures/oracle/       Oracle 语义事实 Fixture
  schema/                JSON Schema
  scenarios/             版本化场景
  corpus/                脱敏后的受跟踪 Replay Corpus
  assessments/           机器可读的 Round 关闭与决策
  spec/                  Foundation、Oracle 与 Mutation 机器契约
  manifest.json          Suite 与发布策略入口
```

后续阶段的 Oracle、Fault、VS Code Driver 和 Endurance 实现也必须放在该顶层目录内。
生产 Runtime、Provider、Tool 和 Host 不得导入 `evaluation/`。

## 候选能力

具备 H3 能力的 Harness 已由 Q1 Round 13 冻结。在该精确 Lock 上，同 Lock H1
Round 03 已 21/21 通过，H2 Round 05 已 16/16 通过，正式 H3 Round 02 已 14/14
通过并准入全部 8 条 RC Lane。当前实现只对该本地 `validated-dry-run` RC Candidate
Partition 具备发布权威。H2 Round 01/02 与 H3 Round 01 保持不可变失败历史；H4
Canary 与 Rollout Expansion 仍未授权。H4 Harness 与 Controlled 120-Turn Preflight
已完成。Q1 Round 14、同 Lock H1 Round 04 与 H2 Round 06 已通过；所需 H3 Round
03 由 Operator 在 96/480 Turn 时停止且不可复用，因此正式 H4 未启动。

D2.1 至 D2.3 已实现为独立且不具备发布权威的控制面。历史 Round 05 已完成
129/129 Case 结算：35 个 Passed、93 个 Harness Incident、1 个 Unattributed Live
Observation。Driver Execution Remediation 现已由
`complex-discovery-d2-drivers-26` 验收：其 105-Case 后继关闭 376/376 Pairwise
Interaction 与 18/18 Check，包括有序 Journey Execution、Live CLI Routing 和精确
Topology-to-Driver Routing；该浅层 Round 未准入 Product Candidate。Semantic/In-path
循环随后关闭 Round 10：20/20 Settled、17 Passed、3 个 Exact-seed Product
Candidate、0 Harness Incident。`thread.compact`、`thread.fork` 与 `turn.revert`
均可在 Turn Parked on Approval 时阻塞后续 Cancel。该缺陷已修复，由 Q1 Round 15
冻结，并在 Semantic Round 11 以 20/20 Passed 完成回归。下一层 Input State 深度在
Semantic Round 15 关闭为 25/25 Settled、23 Passed、2 个 Exact-seed Product
Candidate、0 Harness Incident：取消 Parked on `input.required` 的 Turn 不产生
Terminal；恢复后的 Input Reply 已 Resolved，但 Turn 不产生后续 Terminal。这两个
新 Candidate 的修复尚未授权。

修正后的目标契约和执行顺序见：

- `docs/zh-CN/production-evaluation.md`；
- `docs/zh-CN/production-evaluation-implementation-plan.md`；
- `docs/zh-CN/production-evaluation-findings.md`。

独立审计决策为
`assessments/production-evaluation-independent-audit-01.json`。
F1-F3 实现结果为
`assessments/foundation-f1-f3-implementation-01.json`。
Q1 Round 01 及其失败的 Freeze 决策记录在
`assessments/q1-qualification-global-assessment-01.json`。
Q1 Remediation 归因和候选实现记录在
`assessments/q1-remediation-attribution-01.json` 与
`assessments/q1-remediation-implementation-01.json`。
Q1 Round 03 与成功 Freeze 决策记录在
`assessments/q1-qualification-global-assessment-03.json`。
具备 D1 能力的后继 Harness 与 Discovery 关闭决策记录在
`assessments/q1-qualification-global-assessment-06.json` 与
`assessments/d1-product-discovery-global-assessment-01.json`。
H1 Preflight 关闭、具备 H1 能力的后继 Lock 与正式 H1 关闭决策记录在
`assessments/h1-preflight-global-assessment-26.json`、
`assessments/q1-qualification-global-assessment-07.json` 与
`assessments/h1-production-admission-global-assessment-01.json`。
H2 Preflight、重新验收与不可变正式决策记录在
`assessments/h2-preflight-global-assessment-01.json` 至 `-03.json`、
`assessments/q1-qualification-global-assessment-08.json` 至 `-09.json`、
`assessments/h2-reentry-decision-01.json`、
`assessments/h2-reentry-global-assessment-01.json` 以及
`assessments/h2-production-admission-global-assessment-01.json` 至 `-03.json`。
H3 失败、Remediation、重新验收与最终准入记录在
`assessments/h3-production-admission-global-assessment-01.json` 至 `-06.json`，
以及 `assessments/q1-qualification-global-assessment-11.json` 至 `-13.json`。
D2.3 关闭与 Driver Remediation 决策记录在
`assessments/d2-campaign-global-assessment-01.json` 与
`assessments/d2-driver-remediation-global-assessment-01.json`。
原 Semantic 收敛与三个已修复 Product Candidate 记录在
`assessments/d2-semantic-global-assessment-01.json` 以及
`assessments/d2-product-candidate-0001.json` 至 `-0003.json`。Round 15 与两个新
Candidate 记录在 `assessments/d2-semantic-round-15-assessment.json` 以及
`assessments/d2-product-candidate-0004.json` 至 `-0005.json`。

F1 实现当前包含：

- 严格 JSON Schema 和未知字段拒绝；
- Suite/Scenario/Run/Oracle/Policy 强类型；
- 重复 ID、缺失 Oracle、空分母和无效例外校验；
- 构建时 Source Provenance 与 Version 3 Harness Input-root Identity；
- Suite/Scenario Effective Admission、不可变 Run Partition、Process-tree Timeout、
  Output Digest、Fresh Evidence Binding 和无冲突 Report；
- 首次 Attempt 与 Recovery Attempt 分离；
- 字节稳定的 JSON/Markdown Report；
- 权限为 `0600` 的原子 Report 写入。

F2 实现当前包含：

- VS Code Runtime Capture、Provider Event 和 Observation Journal Adapter；
- 相对时间、稳定 Alias、因果边和 SHA-256 哈希链；
- Metadata-only 脱敏、Credential/绝对路径/高熵内容扫描；
- 完整轨迹、Operation 和未完成 ACP Request 因果切片；
- 经 Review 的 Whole-batch Atomic Promotion 到 Private Staging；
- delay、duplicate、truncate、interruption、unknown、malformed 和 provider split
  变异；
- 11 条来自两次真实 VS Code + DeepSeek 运行的脱敏轨迹，以及一条使 Split Coverage
  可执行的安全 Synthetic Provider Trace。

F3 实现当前包含：

- Runtime、Effect、Workspace、Verification、Persistence、Host、Security、Resource
  和 Task-quality 九类确定性 Oracle；
- 稳定 Failure Signature 和七个责任域，Harness Failure 独立归因；
- 36 个核心场景族，其中 18 个 P0 使用显式 Scenario-specific Oracle Closure；
- 覆盖全部九类 Oracle 的 13 个故障注入验收，包含重复 Effect、双 Terminal、
  Stuck Running、Receipt Drift、Replay Drift 和 Guard Bypass；
- 22 条 Changed-path Impact Rule 和 Full-P0 Fallback；
- 12 条 Corpus 上的 500 次 Replay 命令。

Q1 Round 01、02 与 04 保留为不可变失败和治理历史。具备 D1 能力的后继 Harness
通过 Q1 Round 06，D1 随后完成全部 56 项任务。具备 H1 能力的后继 Harness 通过 Q1
Round 07，H1 完成全部 21 项任务。具备 H2 能力的后继 Harness 通过 Q1 Round 08。
Round 01 与 02 各自 14/16 后，Schema v2 失败证据进入 Q1 Round 09，Lock 为
`sha256:b2f944b8...`。固定诊断矩阵和 Evidence-driven Investigation 授权一次
Re-entry；正式 H2 Round 03 已 16/16 通过。

正式 H3 Round 01 完成 480/480 个 Turn，但未通过保持不变的 Persistence Slope
上限。分离的 Remediation 对累计 Session Delta 与 Checkpoint Content 做有界编码；
随后 Q1 Round 13 通过 8/8 Foundation Task 与连续三次 7/7 Integration Run。
同 Lock H1 Round 03 与 H2 Round 05 通过后，正式 H3 Round 02 完成 480/480 个
Endurance Turn，并准入 Foundation、Integration、Chaos、Live、Endurance、Release、
VS Code RC 与 Package Evidence。该 RC Candidate 未上传，也不授权 H4。

旧 17.4 实现已重置并删除；当前 H1 已按后继流程重建并重新验收。原重置决策仍位于
`assessments/17.4-convergence-review-reset-01.json`。

运行候选诊断：

```bash
make eval-contract-check
make eval-foundation-check
make eval-replay
make eval-oracle
```

检查 D2.1 Campaign 与确定性 Inventory：

```bash
go run ./evaluation/d2/cmd/codehelper-discovery check --root .
```

基于现有 Frozen Base Lock 创建一个不可变 D2 Qualification Epoch：

```bash
go run ./evaluation/d2/cmd/codehelper-discovery qualify \
  --root . \
  --id complex-discovery-d2-foundation-01 \
  --base-lock .tmp/evaluation/q1/foundation-v2-q1-14/harness-lock.json \
  --output .tmp/evaluation/d2/complex-discovery-d2-foundation-01
```

基于冻结的 Production Artifact 验收修复后的 D2 Driver 与 Generator：

```bash
go run ./evaluation/d2/cmd/codehelper-discovery qualify-drivers \
  --root . \
  --id complex-discovery-d2-drivers-36 \
  --base-lock .tmp/evaluation/q1/foundation-v2-q1-14/harness-lock.json \
  --runtime bin/codehelper \
  --vsix extensions/vscode/dist/codehelper-vscode-0.0.1.vsix \
  --output .tmp/evaluation/d2/complex-discovery-d2-drivers-36
```

最终关闭的 Semantic Campaign 只能在其精确 Lock 上复现：

```bash
go run ./evaluation/d2/cmd/codehelper-discovery semantic-campaign \
  --root . \
  --id complex-discovery-d2-semantic-10 \
  --discovery-lock .tmp/evaluation/d2/complex-discovery-d2-drivers-36/discovery-lock.json \
  --runtime bin/codehelper \
  --output .tmp/evaluation/d2/complex-discovery-d2-semantic-10
```

下列命令仅用于说明历史 Round 05 Evidence。未获明确授权前，不得替换为后继 Lock
或执行新 Campaign：

```bash
go run ./evaluation/d2/cmd/codehelper-discovery campaign \
  --root . \
  --id complex-discovery-d2-campaign-05 \
  --discovery-lock .tmp/evaluation/d2/complex-discovery-d2-drivers-09/discovery-lock.json \
  --plan .tmp/evaluation/d2/complex-discovery-d2-drivers-09/campaign-plan.json \
  --inventory .tmp/evaluation/d2/complex-discovery-d2-drivers-09/driver-inventory.json \
  --runtime bin/codehelper \
  --vsix extensions/vscode/dist/codehelper-vscode-0.0.1.vsix \
  --live \
  --output .tmp/evaluation/d2/complex-discovery-d2-campaign-05
```

运行第一阶段自检 Scenario：

```bash
go run ./evaluation/cmd/codehelper-eval run \
  --root . \
  --suite evaluation-contract \
  --scenario contract-self-check \
  --run-id local-contract-check
```

报告默认写入 `.tmp/evaluation/<run-id>/`。

按修改路径选择核心场景：

```bash
go run ./evaluation/cmd/codehelper-eval impact select \
  --path internal/security/policy/policy.go \
  --path internal/persist/state/store.go
```

把本地私有 Capture 晋升为新 Corpus：

```bash
go run ./evaluation/cmd/codehelper-eval capture promote \
  --input /private/path/runtime-capture.jsonl \
  --format vscode_runtime_capture_v1 \
  --prefix candidate-id \
  --batch candidate-batch \
  --review /private/path/promotion-review.json
```

必须先在 `.tmp/evaluation/` Review 和验证，确认不含用户内容后，才能将输出移动到
`evaluation/corpus/`。原始 Capture 不得进入 Git。

## 边界

- CLI 只驱动版本化 Scenario，不实现 Agent Loop。
- 测评代码不能直接绕过 Host、Runtime 或 Guard 执行业务 Tool。
- Capture、Evidence 和 Report 失败不能改变被测业务结果。
- Tracked Scenario 不得包含 Credential、真实用户路径或未授权 Workspace 内容。
- 新状态必须使用 `passed`、`failed`、`unavailable`、`not_evaluated` 或 `invalid`。
- 计划但尚未实现的命令必须在文档中明确标记，不能伪装为已交付能力。
