# CodeHelper

[简体中文](./README.zh-CN.md) | English

CodeHelper is a local, terminal-first AI coding agent runtime written in Go. It
combines repository understanding, model calls, guarded tools, approval,
verification, durable sessions, and orchestration behind one runtime protocol.
The same runtime can be used from the CLI, TUI, VS Code, HTTP/SSE, ACP, or the
embedded web control page.

> Project status: initial development release. Interfaces and persisted formats
> may still change before the first public stable release.

## Runtime and Executable Book

CodeHelper has two complementary goals:

1. **A production-oriented Agent runtime:** a concrete implementation of model
   access, context engineering, governed tools, durable state, orchestration,
   observability, and multiple hosts.
2. **An executable Agent engineering book:** a progressively structured,
   bilingual body of knowledge that explains both the system and the technical
   foundations behind it, from first principles to package-level
   implementation.

The repository is therefore intended not only to provide a tool, but also to
serve as a learning resource and practical reference project for people who
want to understand how modern Agents are designed, implemented, secured,
tested, and operated. Chapters will connect concepts to real CodeHelper source,
tests, diagrams, failure modes, and reproducible labs so that the documentation
and implementation can be studied together.

The product manuals under `docs/en` and `docs/zh-CN` describe current behavior.
The planned book under `docs/book` will organize background knowledge, design
reasoning, implementation walkthroughs, and hands-on exercises into a
continuous outside-in, beginner-to-advanced reading path. See the
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
- macOS or Linux recommended for the strongest sandbox support

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
  "Summarize the current workspace"
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
| HTTP/SSE runtime | `codehelper serve` | local clients and integrations |
| ACP runtime | `codehelper host --adapter acp` | editor/agent protocol clients |
| Web control page | `codehelper web` | lightweight local inspection |
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
internal/host/           CLI, TUI, HTTP/SSE, ACP, Web UI
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
| Agent engineering book | [Knowledge documentation plan](./docs/en/knowledge-base-plan.md) | [知识文档建设方案](./docs/zh-CN/knowledge-base-plan.md) |
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

## Contributing and License

Read [CONTRIBUTING.md](./CONTRIBUTING.md) before changing runtime contracts,
security boundaries, persisted state, or generated protocol files.
