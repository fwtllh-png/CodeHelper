# QCode 文档

这里是面向当前代码树持续维护的文档集合。历史实现 RFC 不再作为产品文档保留；仍然
有效的架构决策会以“当前约束”的形式写入对应指南，而不是要求读者重放开发过程。
产品手册只维护中文版本，`docs/en` 和 `docs/book/en` 不属于允许的仓库结构。

QCode 同时维护一本可执行的 Agent 工程知识书籍：把背景概念、系统设计、
源码导读、测试、故障和动手实验组织为一条渐进式学习路径。信息架构与建设阶段见
[知识文档体系建设方案](./knowledge-base-plan.md)。

## 按目标阅读

### 我要系统学习 Agent 工程

1. [项目与系统全景](./overview.md)
2. [Agent 工程知识书籍](../book/zh-CN/README.md)
3. [书籍导航与章节状态](../book/zh-CN/NAVIGATION.md)
4. [知识文档体系建设方案](./knowledge-base-plan.md)
5. [架构设计](./architecture.md)
6. [安全模型](./security.md)
7. [源码阅读路线指南](./reading-guide.md)
8. [本地开发与脚本](./development.md)

### 我要使用 QCode

1. [项目介绍与定位](./overview.md)
2. [快速开始](./getting-started.md)
3. [配置说明](./configuration.md)
4. [Web 使用与工作流](./usage.md)
5. [安全模型](./security.md)
6. [排障指南](./troubleshooting.md)
7. [Provider TPM 限流与错误“消息截断”问题分析](./provider-tpm-rate-limit-and-false-truncation-analysis.md)

### 我要使用 Web 工作区

1. [快速开始](./getting-started.md)
2. [配置说明](./configuration.md)
3. [排障指南](./troubleshooting.md)

### 我要参与开发

1. [架构设计](./architecture.md)
2. [Runtime 可维护性与所有权边界](./runtime-maintainability-refactoring-plan.md)
3. [安全执行边界重构方案](./security-execution-boundary-refactoring-plan.md)
4. [Runtime 可靠性契约](./reliability-hardening.md)
5. [Web 主入口架构](./web-primary-entry-plan.md)
6. [Session Context、Memory 与持久化](./session-context-optimization.md)
7. [固定阈值审计与动态容量治理](./fixed-threshold-audit.md)
8. [长会话 Token 与调用开销优化方案](./token-cost-optimization-plan.md)
9. [Provider TPM 限流与错误“消息截断”问题分析](./provider-tpm-rate-limit-and-false-truncation-analysis.md)
10. [安全模型](./security.md)
11. [本地开发与脚本](./development.md)
13. [源码阅读路线指南](./reading-guide.md)
14. [Agent 指南](./agent-guide.md)
15. [CONTRIBUTING.md](../../CONTRIBUTING.md)
16. [文档治理](./documentation-governance.md)
17. [后续规划](./roadmap.md)

## 文档事实来源

| 文档内容 | 代码事实来源 |
| --- | --- |
| Web 启动参数 | `internal/host/web` 与 `qcode --help` |
| TOML、环境变量与默认值 | `internal/config/schema.go`、`defaults.go`、`environment.go` |
| Runtime 协议 | `docs/protocol/runtime-protocol.schema.json` |
| 架构边界 | Import 图和 Architecture Test |
| 构建测试命令 | `Makefile` 与 `web/package.json` |
| Web 体验语义 | `testdata/contracts/web-experience-contract.json` |
| 知识书籍信息架构 | `knowledge-base-plan.md` |
| 书籍目录与章节状态 | `docs/book/catalog.json` |
| 书籍 Ownership、新鲜度与发布事实 | `docs/book/governance.json` |
| Runtime 所有权与主流程 | `runtime-maintainability-refactoring-plan.md` 与 Architecture Test |
| 可靠性不变量 | `reliability-hardening.md` 与 `testdata/contracts/reliability-matrix.json` |
| 路线图 | 只描述目标，不作为“已交付”证明 |

实现与文档不一致时，应先核对实现，在同一变更中修正文档；适合自动化的内容应补充
漂移检查。
