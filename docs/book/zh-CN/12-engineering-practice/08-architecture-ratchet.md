---
id: practice-architecture-ratchet
title: 架构行数棘轮已删除
audience:
  - contributor
  - operator
prerequisites:
  - practice-test-layers
  - practice-benchmark
code_paths:
  - Makefile
  - internal/runtime/agent/turnkernel
test_paths:
  - internal/runtime/agent/turnkernel/convergence_baseline_test.go
  - internal/runtime/app/turn_kernel_convergence_test.go
source_of_truth:
  - testdata/contracts/hotspot-baseline.json
  - Makefile
status: verified
last_verified: 2026-09-01
---

# 架构行数棘轮已删除

## 学习目标

本章之后，读者可以：

- 解释为什么行数、Fanout 和函数长度棘轮被删除；
- 区分“结构门禁”与“行为门禁”；
- 使用仍然有效的 Ownership 与测试命令，而不是行数预算。

## 问题背景

仓库曾经用 `scripts/architecturemetrics`、
`testdata/contracts/architecture-metrics-baseline.json`、`make architecture-ratchet`
和 `make ratchet-fast` 对生产行数、内部 Fanout、Options 字段和热点函数长度做单调
棘轮。测量值超过 Baseline，或 Baseline 相对 `origin/main` 放宽却没有书面
Relaxation，验证就会失败。

这个门禁没有抓住行为回归。它惩罚把分类、预算和序号写清楚的改动，奖励把
`else` 塞进同一行、把两个赋值挤进一行的伪压缩。Agent 和人工 Review 开始为
Ratchet 改代码，而不是为正确性改代码。

行数棘轮因此删除。不要恢复它，也不要用新的隐藏行数上限替代它。

## 核心概念

- **行为门禁**：测试、协议契约、安全副作用 Allowlist、文档与 Book 检查。它们
  验证 Runtime 实际做什么。
- **职责门禁**：`testdata/contracts/hotspot-baseline.json` 把符号绑定到 Owner
  Package/File。它检查职责错位和未审阅内部依赖，不检查“这个文件是不是多了
  八行”。
- **行数棘轮**：按 AST 计数否决提交。已删除。

## QCode 设计

结构正确性靠 Ownership 和测试，不靠行数预算：

- `make hotspot-baseline` 校验热点职责归属。
- `make security-side-effect-check` 校验生产副作用入口及其 Owner Allowlist。
- `make capacity-policy-check` 阻止把已退役的容量档位写回代码。
- Turn State Ownership 仍由 `turn-kernel-convergence-baseline` 与
  `turn-kernel-convergence-exit-gate` 断言：只有 Coordinator 调用
  `Reducer.Apply`，External Work 使用 Durable Effect，Terminal Commit 保持原子，
  Restart 使用 Domain Facts。
- `make architecture-freeze` 跑热点职责、引擎/配置/协议表征测试和聚焦 Race，
  不再比较行数 Baseline。
- `make verify` 不再调用架构行数棘轮。

拆分过大文件仍然值得做，但理由是所有权、测试边界和可读性，不是为了让计数器
安静。

## 代码地图

| 关注点 | 来源 | 重要性 |
| --- | --- | --- |
| 行为冻结 | `Makefile` 的 `architecture-freeze` | 热点职责 + 表征测试，无行数比较 |
| 热点职责 | `testdata/contracts/hotspot-baseline.json` | 符号与 Owner File 绑定 |
| Turn Kernel Ownership | `turnkernel/convergence_baseline_test.go` | C0-C6 与 Phase 4R 语义 Ownership |

## 失败模式与安全边界

- 不要把行数、Fanout 或函数长度重新做成 `make verify` 或 Agent 预检失败条件。
- 不要为了少几行而合并本应分开的预算计数器、控制流或测试断言。
- 删除行数棘轮并不放松 Policy、Approval、Journal、Sandbox 或 Protocol 契约。

## 测试与验证

```bash
make hotspot-baseline
make turn-kernel-convergence-baseline
make turn-kernel-convergence-exit-gate
make architecture-freeze
make book-check
```

这些命令不需要网络或真实 Provider。

## 动手实验

打开一次真实的结构改动（例如把两个预算计数器分开），先按可读性写完，再运行上面的
命令。确认没有行数棘轮要求你把 `else` 或赋值挤回同一行。若拆分文件，在
`hotspot-baseline` 里更新 Owner，而不是发明一个新的行数上限。

## 复习问题

1. 行数棘轮为什么会把实现推向更差的形状？
2. 热点职责检查和行数上限有什么不同？
3. 拆分大文件时应该依据什么，而不是依据 Baseline 数字？

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `practice-architecture-ratchet` |
| 状态 | `verified` |
| 最后验证 | 2026-09-01 |
