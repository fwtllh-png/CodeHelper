# QCode Agent 工程知识书籍

本书通过一套真实、受治理的 Runtime 教授 Agent 工程。内容从基础知识和可观察行为开始，
逐步进入协议、源码、安全、持久执行、任务编排、Web Host、扩展机制和动手实验。

本书正在建设中。Catalog 中可以看到某一章，不代表该章已经交付。

## 从这里开始

- [完整导航与章节状态](./NAVIGATION.md)
- [术语表](./glossary.md)
- [知识文档体系建设方案](../../zh-CN/knowledge-base-plan.md)
- [中文章节模板](./_templates/chapter.md)
- 机器可读目录：[`docs/book/catalog.json`](../catalog.json)
- Front Matter 契约：
  [`docs/book/schema/chapter.schema.json`](../schema/chapter.schema.json)

## 状态模型

| 状态 | 含义 |
| --- | --- |
| `planned` | 章节只存在于 Catalog 和导航中。 |
| `draft` | 中文正文已存在，但内容未完成或尚未验证。 |
| `verified` | 中文内容、源码引用、测试和命令均通过章节门禁。 |

只有 `draft` 和 `verified` 章节才创建 Markdown 文件。这样既能让缺失内容清晰可见，
又不会用空文件填充仓库。

## 阅读方式

新读者应按[导航](./NAVIGATION.md)中的核心阅读路径阅读。有经验的 Contributor
可以从具体模块进入，但仍建议先阅读全局架构与 Runtime 术语。

每一章都连接以下内容：

```text
技术背景
  -> QCode 设计
  -> Package 与契约地图
  -> 实现导读
  -> 失败与安全分析
  -> 测试与可复现实验
```

## 写作流程

1. 从 `docs/book/catalog.json` 选择一个 `planned` 章节。
2. 把状态修改为 `draft`。
3. 将中文模板复制到 Catalog 推导出的路径。
4. 替换全部占位内容，保证 Front Matter ID 和状态一致。
5. 填写真实的前置章节、源码路径和测试路径。
6. 重新生成导航：

   ```bash
   python3 scripts/render-book-navigation.py
   ```

7. 运行：

   ```bash
   make book-check
   make docs-check
   ```

8. 只有满足[建设方案](../../zh-CN/knowledge-base-plan.md)中的全部单章完成标准，才把
   状态修改为 `verified`。

## 事实与规划

- 产品手册描述当前行为。
- 书籍章节解释背景、设计、实现和证据。
- Catalog 声明交付状态。
- 代码、测试、Schema 和生成契约仍然是权威事实来源。
- Roadmap 内容必须明确标记，不能描述成已经交付。
