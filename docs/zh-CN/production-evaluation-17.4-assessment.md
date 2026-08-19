# 生产测评 17.4 Convergence Review 重置评估

简体中文 | [English](../en/production-evaluation-17.4-assessment.md)

> 状态：已关闭的重置决策。本文记录此前 17.4 实现和证据不可信的原因，不保留任何
> 历史产品 Pass 或 Repair。

## 决策

Phase 17.4 状态为 `reset_not_started`。

机器可读决策：

```text
evaluation/assessments/17.4-convergence-review-reset-01.json
```

后续独立 Foundation 审计：

```text
evaluation/assessments/production-evaluation-independent-audit-01.json
```

## 为什么必须重置

此前实现无法支持可信产品结论：

1. 36 个 Scenario Family 复用同一个成功 Fixture；
2. Runtime 和 Verification Verdict 可在 Mandatory Evidence 缺失时通过；
3. Provider Split Mutation 实际执行次数为零，但聚合计数仍为绿色；
4. Impact Selection 可成功返回空 Scenario Set；
5. Integration 和 Chaos Discovery 首败即停；
6. Fixture Request Route 和 Fragment 没有完整 Contract；
7. 产品修复前，Harness Input 未冻结为一个机器 Digest；
8. Evaluation-only Control 可能跨入 Production Build Boundary；
9. 历史产品修复发生在 Harness Qualification 之前。

这些是系统性 Harness Root，不是一串独立 Incident。

## 已失效证据

重置使以下内容失效：

- 此前 17.4 Observed Pass；
- 此前 17.4 Product Attribution；
- 此前 17.4 Product Repair；
- 此前 VSIX Driver、Fixture Control、Crash Point 和 Chaos Evidence；
- 基于未冻结 Harness 的任何结论；
- 因 First-failure Termination 被压制的结果。

PEC-0001 至 PEC-0004 只保留为历史 Hypothesis，重新发现前不能成为正式 Product
Finding。

## 保留事实

以下事实仍然有效：

- Reset Assessment 记录的 Source Baseline Commit；
- Architecture Ratchet 结果，因为它由独立门禁强制；
- Systemic Harness Risk 的存在；
- Discovery、Global Assessment、Remediation 和 Verification 必须分 Round；
- Production 单向依赖规则。

候选 17.1 和 17.2 代码只有通过 Negative Requalification 后才能复用，不能自动继承
到新 Foundation。

## 独立审计扩展

独立审计确认了此前全部 False-assurance 问题，并增加三个 Foundation Root：

| Root | 范围 |
| --- | --- |
| PEH-0028 | Command Verdict 和 Suite Admission 未形成可执行 Oracle Closure |
| PEH-0029 | Process Ownership、Evidence Freshness、Run Identity 和 Report Naming 不完整 |
| PEH-0030 | Command Output、Privacy Validation、Corpus Provenance 和 Batch Promotion Fail Open |

审计还确认 Documentation Gate 因本文缺失而失败。

## 重新准入条件

Phase 17.4 只有满足以下条件才能重新开始：

1. 修正后的 Technical Specification 和 Implementation Plan 获批；
2. Foundation Work Unit F1 至 F3 完成；
3. 一个 Collect-all Foundation Qualification Epoch 通过；
4. 同一个 Harness Lock 连续三次通过完全相同的 Integration Qualification；
5. Production Artifact Scan 证明不包含 Evaluation Control；
6. Product Discovery 单独获得授权。

仅批准规格不等于满足这些条件。

## 当前授权

允许：

- 修正规格和文档；
- Review 候选 Foundation Code；
- 获得明确批准后准备 Foundation Work Unit。

不允许：

- Product Discovery；
- Product Remediation；
- 恢复已删除的 17.4 Code；
- 把当前 `eval-*` 绿色输出当作 Release Evidence；
- 把历史 PEC 重新分类为 Confirmed。
