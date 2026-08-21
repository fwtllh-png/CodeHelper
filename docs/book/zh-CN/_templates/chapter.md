---
id: chapter-id
title: 章节标题
audience:
  - learner
  - contributor
prerequisites:
code_paths:
test_paths:
source_of_truth:
  - path/to/authoritative-artifact
status: draft
last_verified: null
---

# 章节标题

> 起草说明：把此模板复制到 `docs/book/catalog.json` 声明的路径，替换所有占位内容，
> 并在同一变更中更新 Catalog 和导航。

## 学习目标

完成本章后，读者能够：

- 解释问题和相关术语；
- 从源码追踪 CodeHelper 的执行路径；
- 复现主要行为和至少一个失败模式。

## 前置知识

列出必读章节、工具和平台能力。

## 问题背景

先解释一般性的 Agent 工程问题，再引入 CodeHelper。

## 核心概念

定义术语，并区分容易混淆的概念。

## CodeHelper 设计

说明当前行为、所有权边界、不变量，以及选择当前方案的原因。

## 执行流程

先使用文字说明。确实有助于理解时，再加入聚焦的 Mermaid 图。

## 代码地图

| 关注点 | 源码 | 作用 |
| --- | --- | --- |
| 主要实现 | `path/to/package` | 替换此行 |
| 契约或 Schema | `path/to/contract` | 替换此行 |
| 测试 | `path/to/tests` | 替换此行 |

## 实现导读

只导读理解设计所需的最小 Type 和 Function 集合。链接源码，不复制大段实现。

## 设计取舍与替代方案

说明约束、替代方案和后果。

## 失败模式与安全边界

至少覆盖一个失败路径；适用时说明攻击者可控输入、Fail-closed 行为、清理和平台限制。

## 测试与验证

```bash
# 替换为真实存在且已经执行的命令。
go test ./path/to/package
```

记录预期结果和环境限制。命令未实际执行前，不能把章节标记为 `verified`。

## 动手实验

### 目标

说明可观察结果。

### 步骤

1. 准备 Fixture 或隔离 Workspace。
2. 运行目标行为。
3. 检查证据。

### 预期结果

说明什么结果可以证明成功。

### 清理

说明如何删除生成状态。

## 复习问题

1. 当前设计中最重要的不变量是什么？
2. 绕过所有权边界会造成什么故障？
3. 哪个测试是该行为最强的证据？

## 延伸阅读

- 链接一手规范或权威外部资料。

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `chapter-id` |
| 状态 | `draft` |
| 最后验证 | 尚未验证 |
