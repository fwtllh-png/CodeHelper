# CodeHelper 文档

简体中文 | [English](../en/README.md)

这里是面向当前代码树持续维护的文档集合。历史实现 RFC 不再作为产品文档保留；仍然
有效的架构决策会以“当前约束”的形式写入对应指南，而不是要求读者重放开发过程。

CodeHelper 也将建设为一本可执行的 Agent 工程知识书籍：把背景概念、系统设计、
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
7. [本地开发与脚本](./development.md)

### 我要使用 CodeHelper

1. [项目介绍与定位](./overview.md)
2. [快速开始](./getting-started.md)
3. [配置说明](./configuration.md)
4. [命令与工作流](./usage.md)
5. [本机 DeepSeek 一键配置与运行](./deepseek-local.md)
6. [安全模型](./security.md)
7. [排障指南](./troubleshooting.md)

### 我要使用 VS Code 插件

1. [快速开始](./getting-started.md)
2. [本机 DeepSeek 一键配置与运行](./deepseek-local.md)
3. [VS Code 插件](./vscode.md)
4. [配置说明](./configuration.md)
5. [排障指南](./troubleshooting.md)

### 我要参与开发

1. [架构设计](./architecture.md)
2. [安全模型](./security.md)
3. [本地开发与脚本](./development.md)
4. [Agent 指南](./agent-guide.md)
5. [TUI 与 VS Code 体验契约](./experience.md)
6. [CONTRIBUTING.zh-CN.md](../../CONTRIBUTING.zh-CN.md)
7. [文档治理](./documentation-governance.md)
8. [后续规划](./roadmap.md)

## 文档事实来源

| 文档内容 | 代码事实来源 |
| --- | --- |
| CLI 名称与参数 | `internal/host/cli` 与 `codehelper <command> --help` |
| TOML 和环境变量 | `internal/config/config.go` |
| Runtime 协议 | `docs/protocol/runtime-protocol.schema.json` |
| 架构边界 | Import 图和 Architecture Test |
| 构建测试命令 | `Makefile` 与扩展 package scripts |
| VS Code 兼容范围 | `extensions/vscode/compatibility.json` |
| TUI 与 VS Code 体验语义 | `docs/experience-contract.json` |
| 知识书籍结构与建设阶段 | `knowledge-base-plan.md` |
| 书籍目录与章节状态 | `docs/book/catalog.json` |
| 书籍 Ownership、新鲜度与发布事实 | `docs/book/governance.json` |
| 路线图 | 只描述目标，不作为“已交付”证明 |

实现与文档不一致时，应先核对实现，在同一变更中修正文档；适合自动化的内容应补充
漂移检查。
