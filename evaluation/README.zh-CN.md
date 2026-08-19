# CodeHelper 生产测评

[English](./README.md) | 简体中文

`evaluation/` 统一管理所有受 Git 跟踪的生产测评代码和资产。临时运行证据写入
`.tmp/evaluation/`，不进入源码目录。

## 目录

```text
evaluation/
  cmd/codehelper-eval/   测评 CLI
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

以下实现当前不具发布权威性。阶段 17.4 Convergence Review 已撤销此前完成结论。
Foundation v2 目前已在一个 Frozen v3 Harness 下验收，但这不会自动形成 Product
Discovery、Product Remediation、VS Code E2E、Chaos 或 Release 结论。

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

Q1 Round 01 与 Round 02 保留为不可变失败和治理历史。Q1 Round 03 已在相同 v3
Input、Runtime、VSIX 与 Lock Identity 下，以 8/8 Foundation Task 和连续三次 7/7
Integration Run 验收 Remediation 输入。每次 Run 均清理 5 个 Runtime Process 与
4 个临时目录，Outstanding 为零。Harness Lock `sha256:177f9cd2...` 状态为
`frozen_qualified`。下一阶段为 D1 Collect-all Product Discovery。

17.4 已重置且实现已删除，机器决策位于
`assessments/17.4-convergence-review-reset-01.json`。

运行候选诊断：

```bash
make eval-contract-check
make eval-foundation-check
make eval-replay
make eval-oracle
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
