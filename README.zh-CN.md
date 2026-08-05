# CodeHelper

简体中文 | [English](./README.md)

CodeHelper 是一个使用 Go 实现的、本地运行、终端优先的 AI 编程 Agent Runtime。
它把仓库理解、模型调用、受控工具、审批、验证、持久化会话和任务编排统一在一套
Runtime 协议之后，并同时服务 CLI、TUI、VS Code、HTTP/SSE、ACP 和轻量 Web
控制页。

> 项目状态：初始开发版本。首次公开稳定发布前，接口和持久化格式仍可能调整。

## Runtime 与可执行的知识书籍

CodeHelper 有两个相互支撑的建设目标：

1. **面向真实工程的 Agent Runtime**：具体实现模型接入、上下文工程、受控工具、
   持久化状态、任务编排、可观测性和多种 Host。
2. **可执行的 Agent 工程知识书籍**：建立一套由浅入深、由外及里、持续与代码同步的
   双语知识体系，同时解释 CodeHelper 本身和它背后的技术原理。

因此，这个仓库不仅提供一个工具，也要成为希望系统学习现代 Agent 如何设计、实现、
治理、测试和运行的开发者所使用的学习资料与实战参考项目。知识章节会把概念与真实的
CodeHelper 源码、测试、架构图、失败模式和可复现实验关联起来，使文档与实现能够一起
阅读和验证。

`docs/en` 与 `docs/zh-CN` 下的产品手册负责描述当前行为；规划中的 `docs/book`
负责把背景知识、设计推理、实现导读和动手实验组织为一条从入门到深入的连续阅读路径。
建设边界、全书目录、章节规范和阶段计划见
[知识文档体系建设方案](./docs/zh-CN/knowledge-base-plan.md)。

## 为什么建设 CodeHelper

多数 Coding Agent 原型优先追求演示效果，CodeHelper 更关注产品长期运行后必须具备
的工程属性：

- **本地控制权**：源码和执行仍在用户工作区中。
- **一套 Runtime，多种 Host**：终端、编辑器、自动化和 API 客户端共享同一套
  Operation/Event 模型。
- **受控执行**：所有修改型工具都经过 policy、permission、constitution、journal
  与操作系统沙箱检查。
- **证据优先**：搜索、编辑、审批、验证、用量和 trace 都形成可检查的运行事实。
- **扩展但不分叉控制面**：MCP、Skill、Plugin、Hook、后台 Worker、Workflow 和
  Subagent 都通过受治理的 Adapter 接入。
- **默认关闭而不是假装安全**：安全能力不可用时明确报告，不静默降级为“看起来已隔离”。

## 快速开始

环境要求：

- Go 1.26 或更高版本
- Git
- 仅开发 VS Code 插件时需要 Node.js 和 npm
- 推荐 macOS 或 Linux，以获得更完整的沙箱能力

```bash
git clone https://github.com/fwtllh-png/CodeHelper.git
cd CodeHelper

make build
./bin/codehelper version
./bin/codehelper doctor
```

先运行不访问网络的 fixture：

```bash
./bin/codehelper exec \
  --provider-fixture ./testdata/providers/openai \
  --provider openai \
  --model gpt-fixture \
  --output-format stream-json \
  "总结当前工作区"
```

调用已配置的真实模型：

```bash
export OPENAI_API_KEY='...'
./bin/codehelper exec \
  --provider openai \
  --model gpt-4.1 \
  --api-key-env OPENAI_API_KEY \
  --workspace . \
  --enable-tools \
  --mode act \
  --posture suggest \
  "找到风险最高的缺陷并提出修复方案"
```

启动终端界面：

```bash
./bin/codehelper tui --workspace . --config ./codehelper.toml
```

仓库所有者在 macOS 上使用 DeepSeek 时，可以一条命令完成编译、配置并启动 TUI
或官方 VS Code：

```bash
make deepseek-tui
make deepseek-vscode
```

密钥来源、生成文件、Agent 执行方式和校验说明见
[本机 DeepSeek 一键配置与运行](./docs/zh-CN/deepseek-local.md)。

安装、初始配置、凭证、持久化和 VS Code 使用方式见
[快速开始](./docs/zh-CN/getting-started.md)。

## 产品入口

| 入口 | 命令或路径 | 主要用途 |
| --- | --- | --- |
| 单次 CLI | `codehelper exec` | 脚本、CI 实验、机器可读事件流 |
| 终端界面 | `codehelper tui` | 交互式仓库开发 |
| HTTP/SSE Runtime | `codehelper serve` | 本地客户端和集成 |
| ACP Runtime | `codehelper host --adapter acp` | 编辑器与 Agent 协议客户端 |
| Web 控制页 | `codehelper web` | 轻量本地检查 |
| VS Code 插件 | `extensions/vscode` | 编辑器原生对话、上下文、变更、审批和任务 |
| Worker 与编排 | `worker`、`automation`、`workflow`、`lane`、`fleet` | 持久化、多步骤执行 |

## 一分钟理解安全模型

`--mode` 描述 Agent 正在进行的工作类型：

- `plan`：只读分析与计划
- `act`：常规编码工作
- `operate`：运维型工作流

`--posture` 描述工具决策方式：

- `never`：只读
- `suggest`：风险操作请求用户审批
- `auto`：按策略自动判断，不被允许的操作直接拒绝
- `bypass`：宽松的本地权限，但仍不能绕过 constitution 和沙箱硬边界

日常交互推荐 `suggest`。`bypass` 只应用于隔离且可信的工作区。凭证应保存为环境变量、
文件或系统 Keyring 的引用，TOML 中不应出现原始密钥。

## 仓库结构

```text
cmd/codehelper/          进程入口
internal/host/           CLI、TUI、HTTP/SSE、ACP、Web UI
internal/runtime/        Operation/Event Runtime 与 Agent Engine
internal/adapter/        Provider、Model、Tool、MCP、Skill、Plugin、Hook
internal/security/       Policy、Permission、Constitution、Sandbox
internal/orchestration/  Task、Worker、Workflow、Lane、Fleet、Subagent
internal/persist/        SQLite、Event Log、Session、Snapshot、Journal
internal/observability/  Usage、Trace、Verify、Diagnostics、Telemetry
internal/platform/       进程和操作系统集成
extensions/vscode/       TypeScript VS Code 插件
docs/                    持续维护的双语文档
scripts/                 构建、验证、配置和发布脚本
testdata/                Hermetic Provider 与 Benchmark Fixture
```

## 文档

| 读者 | 简体中文 | English |
| --- | --- | --- |
| 文档总览 | [docs/zh-CN](./docs/zh-CN/README.md) | [docs/en](./docs/en/README.md) |
| 产品介绍与定位 | [项目介绍](./docs/zh-CN/overview.md) | [Overview](./docs/en/overview.md) |
| 安装与上手 | [快速开始](./docs/zh-CN/getting-started.md) | [Getting started](./docs/en/getting-started.md) |
| 配置 | [配置说明](./docs/zh-CN/configuration.md) | [Configuration](./docs/en/configuration.md) |
| 命令与工作流 | [使用指南](./docs/zh-CN/usage.md) | [Usage](./docs/en/usage.md) |
| 架构 | [架构设计](./docs/zh-CN/architecture.md) | [Architecture](./docs/en/architecture.md) |
| 安全 | [安全指南](./docs/zh-CN/security.md) | [Security](./docs/en/security.md) |
| 本地开发 | [本地开发](./docs/zh-CN/development.md) | [Development](./docs/en/development.md) |
| 本机 DeepSeek | [一键配置运行](./docs/zh-CN/deepseek-local.md) | [One-click setup](./docs/en/deepseek-local.md) |
| Agent 上下文 | [Agent 指南](./docs/zh-CN/agent-guide.md) | [Agent guide](./docs/en/agent-guide.md) |
| Agent 工程知识书籍 | [知识文档建设方案](./docs/zh-CN/knowledge-base-plan.md) | [Knowledge documentation plan](./docs/en/knowledge-base-plan.md) |
| 产品方向 | [后续规划](./docs/zh-CN/roadmap.md) | [Roadmap](./docs/en/roadmap.md) |

## 开发

```bash
make build
make test
make docs-check
make verify
```

`make verify` 覆盖面很广，部分测试依赖平台安全能力。修改单个子系统时，应优先使用
[本地开发指南](./docs/zh-CN/development.md)列出的聚焦命令。

修改 Runtime 契约、安全边界、持久化状态或生成协议文件前，请阅读
[CONTRIBUTING.zh-CN.md](./CONTRIBUTING.zh-CN.md)。
