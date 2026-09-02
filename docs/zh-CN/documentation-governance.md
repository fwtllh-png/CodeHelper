# 文档治理

QCode 将文档结论视为持续维护的工程契约。书籍通过 Ownership、变更影响、
可复现的发布事实、新鲜度和读者反馈进行治理，而不是依赖偶发的编辑清理。

## 事实来源

| 文件 | 权威范围 |
| --- | --- |
| `docs/book/catalog.json` | Part 顺序、章节身份、中文标题和交付状态 |
| `docs/book/schema/chapter.schema.json` | 章节 Front Matter 契约 |
| `docs/book/governance.json` | Owner、源码责任域、新鲜度 SLA、发布事实、截图和外链例外 |
| 章节 Front Matter | 章节特定的代码、测试和事实依赖 |

Ownership 或源码责任域映射变化时，先修改注册表。

## 机器契约 Artifact

当前命令与测试消费的仓库级 JSON 统一放在 `testdata/contracts`。`docs` 仅在受治理
位置保留文档注册表和生成的文档 Schema。一次性阶段 Evidence、Benchmark Run 和验收
Snapshot 保留在 `.tmp`、CI Artifact 与 Git 历史中。持久结论应汇总到持续维护的中文
文档，而不是新增逐阶段 Evidence JSON 文件。

## PR 文档影响门禁

影响检查器比较 PR 的 Base 与 Head。它优先将变更路径映射到章节 Front Matter；
若没有章节声明该路径，再回退到源码责任域。对应中文章节发生变化时，章节才被视为
已更新。

PR Body 使用以下机器可读块：

```text
Documentation-impact: affected
Documentation-chapters: runtime-protocol, host-web
Documentation-rationale: N/A
```

可观察事实没有变化时使用：

```text
Documentation-impact: none
Documentation-chapters: N/A
Documentation-rationale: 本次重构不改变协议和 Web 行为。
```

理由是供 Review 的工程断言，不是绕过 Owner Review 的开关。

可在本地针对 Base Revision 执行：

```bash
BASE_REF=origin/main make doc-impact
```

## 事实与发布门禁

`make docs-check` 验证链接、中文单一文档树、Ownership、新鲜度、截图清单和治理单元测试。
`make book-check` 验证 Catalog 以及全部章节元数据。

发布前执行：

```bash
make release-fact-check
```

`governance.json` 中的命令验证当前 Web 启动行为、Protocol Schema 漂移、
Compatibility 数据和文档契约。新增发布结论无法由现有命令证明时，必须增加
对应的 Fact Command。

## 新鲜度与漂移

Verified 章节的最长复核周期为 180 天，150 天进入预警。每周 Workflow 执行严格的
源码漂移检查：若章节声明的 Code、Test 或 Source-of-truth 路径具有晚于
`last_verified` 的 Git 日期，必须重新核查并更新中文章节。

严格漂移检查后，用 `make doc-reverify` 对书籍执行对账：声明源码路径在
`last_verified` 之后发生变化的章节，会在中文文件和 Catalog 中降级为
`draft`；源码未变化的章节则以当天日期重新盖章。应用前用
`make doc-reverify-dry-run` 预览结果。

截图容易老化，因此仅在必要时使用。书籍中的每张图片都必须在
`governance.json` 登记 SHA-256；相同信息可以用 Mermaid 或文本表达时，应优先使用
可在源码中 Review 的形式。

外链按周检查而不是每个 PR 检查，避免普通开发依赖网络可用性。稳定且有意保留的
例外必须在注册表显式登记并经过 Owner Review。

## 反馈与周期

读者通过 Documentation Feedback Issue 表单报告错误事实、实验失败、缺少前置知识和
导航阻力。维护者按以下方式分类：

- 事实漂移：随所属源码变更修复，或在七天内修复；
- 可复现性或安全缺陷：按产品缺陷优先处理；
- 阅读顺序问题：纳入每月导航 Review；
- 增强建议：关联到对应章节或 Roadmap 条目。

每月 Review 检查未关闭的文档反馈、Prerequisite Edge 和导航顺序。Release Review
必须关闭 Fact Gate 失败，并记录受平台限制的命令。Ownership、Impact 和 Freshness
例外必须留存在 Tracked 配置或 PR 记录中。
