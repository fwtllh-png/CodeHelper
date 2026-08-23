# CodeHelper

[![CI](https://github.com/fwtllh-png/CodeHelper/actions/workflows/ci.yml/badge.svg)](https://github.com/fwtllh-png/CodeHelper/actions/workflows/ci.yml)
[![License: Apache-2.0](https://img.shields.io/badge/License-Apache--2.0-blue.svg)](./LICENSE)
[![Go 1.26+](https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go)](./go.mod)
[![Release](https://img.shields.io/github/v/release/fwtllh-png/CodeHelper?display_name=tag&sort=semver)](https://github.com/fwtllh-png/CodeHelper/releases)
[![Discussions](https://img.shields.io/github/discussions/fwtllh-png/CodeHelper)](https://github.com/fwtllh-png/CodeHelper/discussions)

**一个使用 Go 实现的、本地运行、受控执行的 AI Coding Agent Runtime，也是一套
可执行的 Agent 工程知识书籍。**

CodeHelper 将仓库理解、模型调用、受治理工具、审批、验证、持久化会话与编排统一放在
一套 Runtime 协议之后，并同时服务本机 Web、CLI、TUI、自动化与 Worker。

> 项目状态：初始开发版本。首次公开稳定发布前，接口和持久化格式仍可能调整。

## Runtime 与可执行的知识书籍

| 交付物 | 提供的价值 |
| --- | --- |
| **面向真实工程的 Agent Runtime** | 模型接入、上下文工程、受控工具、持久化状态、任务编排、可观测性和多种 Host |
| **可执行的 Agent 工程知识书籍** | 从基础原理进入真实源码、测试、架构图、失败模式和可复现实验的中文路径 |

`docs/zh-CN` 下的中文产品手册描述已交付行为。
[Agent 工程知识书籍](./docs/book/zh-CN/README.md)把设计推理与 CodeHelper 实现和
动手实验关联起来。建设边界、全书目录、章节规范和阶段计划见
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
- 重新构建 Web 前端时需要 Node.js 和 npm

支持平台边界：

| 平台 | Runtime | 沙箱边界 |
| --- | --- | --- |
| macOS | 支持 | Seatbelt Backend 可用时为 Strong |
| Linux | 支持 | 满足 Bubblewrap 和 Landlock 要求时为 Strong |
| Windows | 支持，但存在平台特定限制 | Partial；需要 Strong Sandbox 的操作会拒绝执行 |

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
  "say hello"
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

启动本机 Web 工作区：

```bash
./bin/codehelper web \
  --workspace . \
  --config ./codehelper.toml \
  --enable-tools
```

Web 只监听 `127.0.0.1`，默认选择可用端口。终端会分别输出页面开始监听和
Runtime 完成恢复的 URL。

仓库所有者在 macOS 上使用 DeepSeek 时，可以一条命令完成编译、配置并启动 Web
或 TUI：

```bash
make deepseek-web
make deepseek-tui
```

密钥来源、生成文件、Agent 执行方式和校验说明见
[本机 DeepSeek 一键配置与运行](./docs/zh-CN/deepseek-local.md)。

安装、初始配置、凭证、持久化和 Web 使用方式见
[快速开始](./docs/zh-CN/getting-started.md)。

## 产品入口

| 入口 | 命令或路径 | 主要用途 |
| --- | --- | --- |
| 本机 Web | `codehelper web` | 默认交互入口、会话、审批、变更与运行状态 |
| 单次 CLI | `codehelper exec` | 脚本、CI 实验、机器可读事件流 |
| 终端界面 | `codehelper tui` | 交互式仓库开发 |
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
internal/host/           Web、CLI、TUI
internal/runtime/        Operation/Event Runtime 与 Agent Engine
internal/adapter/        Provider、Model、Tool、MCP、Skill、Plugin、Hook
internal/security/       Policy、Permission、Constitution、Sandbox
internal/orchestration/  Task、Worker、Workflow、Lane、Fleet、Subagent
internal/persist/        SQLite、Event Log、Session、Snapshot、Journal
internal/observability/  Usage、Trace、Verify、Diagnostics、Telemetry
internal/platform/       进程和操作系统集成
web/                     React/TypeScript 本机 Web 前端
docs/                    持续维护的中文文档
scripts/                 构建、验证、配置和发布脚本
testdata/                Hermetic Provider 与 Benchmark Fixture
```

## 文档

| 主题 | 文档 |
| --- | --- |
| 文档总览 | [docs/zh-CN](./docs/zh-CN/README.md) |
| 产品介绍与定位 | [项目介绍](./docs/zh-CN/overview.md) |
| 安装与上手 | [快速开始](./docs/zh-CN/getting-started.md) |
| 配置 | [配置说明](./docs/zh-CN/configuration.md) |
| 命令与工作流 | [使用指南](./docs/zh-CN/usage.md) |
| 架构 | [架构设计](./docs/zh-CN/architecture.md) |
| Runtime 所有权与可维护性 | [核心流程与边界](./docs/zh-CN/runtime-maintainability-refactoring-plan.md) |
| 安全 | [安全指南](./docs/zh-CN/security.md) |
| 本地开发 | [本地开发](./docs/zh-CN/development.md) |
| 本机 DeepSeek | [一键配置运行](./docs/zh-CN/deepseek-local.md) |
| Agent 上下文 | [Agent 指南](./docs/zh-CN/agent-guide.md) |
| Agent 工程知识书籍 | [书籍与导航](./docs/book/zh-CN/README.md) |
| 知识体系方案 | [文档建设方案](./docs/zh-CN/knowledge-base-plan.md) |
| 文档治理 | [Ownership 与门禁](./docs/zh-CN/documentation-governance.md) |
| 运行时可靠性 | [可靠性契约](./docs/zh-CN/reliability-hardening.md) |
| 产品方向 | [后续规划](./docs/zh-CN/roadmap.md) |

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
[CONTRIBUTING.md](./CONTRIBUTING.md)。

疑似安全漏洞必须按照 [SECURITY.md](./SECURITY.md) 私下报告，不应创建
公开 Issue。

CodeHelper 使用 [Apache License 2.0](./LICENSE)。
