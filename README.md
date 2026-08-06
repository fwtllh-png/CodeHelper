# CodeHelper

[简体中文](./README.zh-CN.md) | English

[![CI](https://github.com/fwtllh-png/CodeHelper/actions/workflows/ci.yml/badge.svg)](https://github.com/fwtllh-png/CodeHelper/actions/workflows/ci.yml)
[![License: Apache-2.0](https://img.shields.io/badge/License-Apache--2.0-blue.svg)](./LICENSE)
[![Go 1.26+](https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go)](./go.mod)
[![Release](https://img.shields.io/github/v/release/fwtllh-png/CodeHelper?display_name=tag&sort=semver)](https://github.com/fwtllh-png/CodeHelper/releases)
[![Discussions](https://img.shields.io/github/discussions/fwtllh-png/CodeHelper)](https://github.com/fwtllh-png/CodeHelper/discussions)

**A local, guarded AI coding-agent runtime and executable Agent engineering
book, written in Go.**

CodeHelper puts repository understanding, model calls, guarded tools, approval,
verification, durable sessions, and orchestration behind one runtime protocol.
Use the same runtime from the CLI, TUI, VS Code, ACP, automation, and worker
surfaces.

> Project status: initial development release. Interfaces and persisted formats
> may still change before the first public stable release.

## Runtime and Executable Book

| Deliverable | What it provides |
| --- | --- |
| **Production-oriented Agent runtime** | Model access, context engineering, governed tools, durable state, orchestration, observability, and multiple hosts |
| **Executable Agent engineering book** | A bilingual path from first principles to real source, tests, diagrams, failure modes, and reproducible labs |

The product manuals under `docs/en` and `docs/zh-CN` document shipped behavior.
The [Agent Engineering Book](./docs/book/en/README.md) connects design reasoning
to CodeHelper implementation and hands-on exercises. Its structure and delivery
rules are defined in the
[Knowledge Documentation Plan](./docs/en/knowledge-base-plan.md).

## Why CodeHelper

Most coding-agent prototypes optimize for a convincing demo. CodeHelper focuses
on the engineering properties required after the demo:

- **Local authority:** source code and execution remain in the user's workspace.
- **One runtime, multiple hosts:** terminal, editor, automation, and API clients
  share the same operation/event model.
- **Guarded execution:** every mutating tool passes policy, permission,
  constitution, journal, and OS-sandbox checks.
- **Evidence over claims:** searches, edits, approvals, verification, usage, and
  traces are recorded as inspectable runtime facts.
- **Extensible without a second control plane:** MCP, skills, plugins, hooks,
  background workers, workflows, and subagents enter through governed adapters.
- **Fail closed:** unavailable security capabilities are reported explicitly;
  CodeHelper does not silently pretend an unsafe operation is isolated.

## Quick Start

Requirements:

- Go 1.26 or newer
- Git
- Node.js and npm only when developing the VS Code extension

Supported platform boundary:

| Platform | Runtime | Sandbox boundary |
| --- | --- | --- |
| macOS | Supported | Strong when the Seatbelt backend is available |
| Linux | Supported | Strong when Bubblewrap and Landlock requirements are available |
| Windows | Supported with platform-specific limits | Partial; operations requiring a strong sandbox fail closed |

```bash
git clone https://github.com/fwtllh-png/CodeHelper.git
cd CodeHelper

make build
./bin/codehelper version
./bin/codehelper doctor
```

Run a network-free fixture:

```bash
./bin/codehelper exec \
  --provider-fixture ./testdata/providers/openai \
  --provider openai \
  --model gpt-fixture \
  --output-format stream-json \
  "say hello"
```

Run against a configured model:

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
  "Find the highest-risk defect and propose a fix"
```

Start the terminal UI:

```bash
./bin/codehelper tui --workspace . --config ./codehelper.toml
```

For the repository owner's macOS DeepSeek environment, build, configure, and
run the TUI or official VS Code with one command:

```bash
make deepseek-tui
make deepseek-vscode
```

See [One-Click Local DeepSeek](./docs/en/deepseek-local.md) for credential
lookup, generated files, Agent usage, and verification.

See [Getting Started](./docs/en/getting-started.md) for installation, initial
configuration, credentials, persistence, and VS Code setup.

## Product Surfaces

| Surface | Command or path | Primary use |
| --- | --- | --- |
| One-shot CLI | `codehelper exec` | scripts, CI experiments, machine-readable event streams |
| Terminal UI | `codehelper tui` | interactive repository work |
| ACP runtime | `codehelper host --adapter acp` | editor/agent protocol clients |
| VS Code extension | `extensions/vscode` | editor-native chat, context, changes, approvals, jobs |
| Worker and automation | `worker`, `automation`, `workflow`, `lane`, `fleet` | durable and multi-step execution |

## Safety in One Minute

`--mode` controls what class of work the agent is attempting:

- `plan`: inspect and plan
- `act`: normal coding work
- `operate`: operational workflows

`--posture` controls how tool decisions are handled:

- `never`: read-only posture
- `suggest`: request approval for risky actions
- `auto`: apply policy automatically, denying actions that are not allowed
- `bypass`: broad local permission, still subject to hard constitution and
  sandbox boundaries

Use `suggest` for normal interactive work. Use `bypass` only in an isolated,
trusted workspace. Credentials are stored as references to environment
variables, files, or OS keyrings; raw secrets do not belong in TOML files.

## Repository Map

```text
cmd/codehelper/          process entry point
internal/host/           CLI, TUI, ACP
internal/runtime/        operation/event runtime and agent engine
internal/adapter/        providers, models, tools, MCP, skills, plugins, hooks
internal/security/       policy, permissions, constitution, sandbox
internal/orchestration/  tasks, workers, workflows, lanes, fleet, subagents
internal/persist/        SQLite, event log, sessions, snapshots, journals
internal/observability/  usage, traces, verification, diagnostics, telemetry
internal/platform/       process and OS integration
extensions/vscode/       TypeScript VS Code extension
docs/                    maintained bilingual documentation
scripts/                 repeatable build, validation, setup, and release scripts
testdata/                hermetic provider and benchmark fixtures
```

## Documentation

| Audience | English | 简体中文 |
| --- | --- | --- |
| Documentation index | [docs/en](./docs/en/README.md) | [docs/zh-CN](./docs/zh-CN/README.md) |
| Product and positioning | [Overview](./docs/en/overview.md) | [项目介绍](./docs/zh-CN/overview.md) |
| Installation | [Getting started](./docs/en/getting-started.md) | [快速开始](./docs/zh-CN/getting-started.md) |
| Configuration | [Configuration](./docs/en/configuration.md) | [配置说明](./docs/zh-CN/configuration.md) |
| Commands and workflows | [Usage](./docs/en/usage.md) | [使用指南](./docs/zh-CN/usage.md) |
| Architecture | [Architecture](./docs/en/architecture.md) | [架构设计](./docs/zh-CN/architecture.md) |
| Security | [Security](./docs/en/security.md) | [安全指南](./docs/zh-CN/security.md) |
| Local development | [Development](./docs/en/development.md) | [本地开发](./docs/zh-CN/development.md) |
| Local DeepSeek | [One-click setup](./docs/en/deepseek-local.md) | [一键配置运行](./docs/zh-CN/deepseek-local.md) |
| AI agent context | [Agent guide](./docs/en/agent-guide.md) | [Agent 指南](./docs/zh-CN/agent-guide.md) |
| Agent engineering book | [Book and navigation](./docs/book/en/README.md) | [书籍与导航](./docs/book/zh-CN/README.md) |
| Knowledge system plan | [Documentation plan](./docs/en/knowledge-base-plan.md) | [文档建设方案](./docs/zh-CN/knowledge-base-plan.md) |
| Documentation governance | [Ownership and gates](./docs/en/documentation-governance.md) | [Ownership 与门禁](./docs/zh-CN/documentation-governance.md) |
| Product direction | [Roadmap](./docs/en/roadmap.md) | [后续规划](./docs/zh-CN/roadmap.md) |

## Development

```bash
make build
make test
make docs-check
make verify
```

`make verify` is intentionally broad and may require platform security
capabilities. Use the focused targets documented in
[Development](./docs/en/development.md) when working on one subsystem.

## Contributing, Security, and License

Read [CONTRIBUTING.md](./CONTRIBUTING.md) before changing runtime contracts,
security boundaries, persisted state, or generated protocol files.

Report suspected vulnerabilities privately according to
[SECURITY.md](./SECURITY.md), not through a public issue.

CodeHelper is licensed under the [Apache License 2.0](./LICENSE).
