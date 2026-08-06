# 使用指南与命令工作流

简体中文 | [English](../en/usage.md)

CLI 是命令可用性的最准确参考：

```bash
codehelper help
codehelper <command> --help
```

## 核心工作流

### 单次执行

```bash
codehelper exec [flags] "PROMPT"
```

关键参数：

| 参数 | 作用 |
| --- | --- |
| `--config` | TOML 配置 |
| `--provider`、`--model`、`--protocol` | 模型路由 |
| `--workspace` | 工具工作区 |
| `--enable-tools` | 注册内置工作区工具 |
| `--mode` | `plan`、`act` 或 `operate` |
| `--posture` | `never`、`suggest`、`auto` 或 `bypass` |
| `--data-dir` | 持久化状态目录 |
| `--resume` / `--continue` | 恢复活跃 Thread |
| `--file` | 把工作区相对路径 Pin 到 Working Set |
| `--budget-tokens`、`--budget-usd` | 累计预算 |
| `--output-format stream-json` | NDJSON 事件输出 |
| `--provider-fixture` | 确定性的本地 Provider |

自动化应使用 `stream-json`，消费结构化 Event，而不是抓取人类可读文本。

### 交互终端

```bash
codehelper tui --config ./codehelper.toml --workspace .
```

TUI 与 `exec` 使用同一 Runtime，不存在独立工具策略。日常建议从
`--posture suggest` 开始。

### ACP Host

```bash
codehelper host \
  --adapter acp \
  --data-dir ./.codehelper \
  --workspace .
```

ACP 面向编辑器与 Agent 客户端。Workspace Identity 把编辑器 URI 绑定到 Runtime Root，
Host 不能自行放宽或伪造该身份。

## Mode 与 Posture

二者回答不同问题：

| 配置 | 问题 |
| --- | --- |
| Mode | Agent 正在尝试哪类工作？ |
| Posture | 如何处理工具风险决策？ |

推荐组合：

| 场景 | Mode | Posture |
| --- | --- | --- |
| 解释仓库 | `plan` | `never` |
| 常规编码 | `act` | `suggest` |
| 可信 Hermetic Fixture | `act` | `auto` |
| 隔离的本地实验 | `act` | `bypass` |
| 运维调查 | `operate` | `suggest` |

`auto` 不表示“自动同意一切”，策略仍可能拒绝高风险工具。`bypass` 也不会关闭
Constitution 或 Sandbox 硬边界。

## Session 与 Thread

使用 `--data-dir` 获得可恢复会话：

```bash
codehelper exec --data-dir ./.codehelper ... "开始任务"
codehelper exec --data-dir ./.codehelper --resume ... "继续任务"
```

查看元数据：

```bash
codehelper thread list --data-dir ./.codehelper
codehelper thread read --data-dir ./.codehelper --id THREAD_ID
codehelper thread resume --data-dir ./.codehelper --id THREAD_ID
codehelper thread fork --data-dir ./.codehelper --id THREAD_ID
codehelper thread archive --data-dir ./.codehelper --id THREAD_ID
```

精确参数和输出选项以子命令 `--help` 为准。

## 模型与能力 Probe

```bash
codehelper model list
codehelper model list --live --provider openai
codehelper model resolve --provider openai --model gpt-4.1 --json
codehelper model probe \
  --provider openai \
  --model gpt-4.1 \
  --data-dir ./.codehelper
```

Probe 默认只收紧能力。`--trust-probe` 允许正向观测扩大有效能力，只应对可信 Endpoint
使用。

## 凭证

```bash
codehelper auth suggestions
codehelper auth login --config ./codehelper.toml --kind env --name OPENAI_API_KEY
codehelper auth status --config ./codehelper.toml
codehelper auth list
codehelper auth logout --config ./codehelper.toml
```

命令输出引用和可用状态，不输出 Secret 值。

## MCP、Skill、Plugin 与 Hook

### MCP

```bash
codehelper mcp add --help
codehelper mcp validate --config ./mcp.json
codehelper mcp status --config ./mcp.json
codehelper mcp tools --config ./mcp.json
codehelper mcp serve
```

MCP Server 通过独立 Health 与 Circuit Breaker 隔离。应把 MCP Server 视为具有供应链
风险的可执行代码。

### Skill

```bash
codehelper skill list
codehelper skill lint PATH
codehelper skill lock
codehelper skill verify
codehelper skill enable NAME
```

### Plugin

```bash
codehelper plugin list
codehelper plugin install NAME@VERSION
codehelper plugin enable NAME
codehelper plugin rollback NAME
codehelper plugin security-revoke NAME
```

Plugin Registry、Publisher、Receipt 和 Staging Artifact 在激活前都要验证。不能为了
方便绕过 Trust Material。

### Hook

Hook 使用 `--hooks-config` 提供的版本化 JSON。Hook 仍位于同一受治理架构内，应具有
有界超时和显式权限。

## 持久化任务

### Worker

```bash
codehelper worker enqueue --help
codehelper worker list --data-dir ./.codehelper
codehelper worker run --data-dir ./.codehelper --posture suggest
```

单次 `exec` 不会静默执行后台任务。Worker 是长生命周期进程，具有 Lease、Retry、
Concurrency 与 Budget 配置。

### Automation

```bash
codehelper automation list --data-dir ./.codehelper
codehelper automation run --data-dir ./.codehelper --id AUTOMATION_ID
codehelper automation pause --data-dir ./.codehelper --id AUTOMATION_ID
```

### Workflow

```bash
codehelper workflow validate --spec workflow.json
codehelper workflow run \
  --spec workflow.json \
  --data-dir ./.codehelper \
  --driver runtime \
  --provider-fixture ./testdata/providers/workflow-schema
codehelper workflow status --data-dir ./.codehelper --id RUN_ID
```

Runtime Workflow 默认要求 Fixture；真实 Provider 必须显式开启。

### Lane 与 Fleet

```bash
codehelper lane start --help
codehelper lane list --data-dir ./.codehelper
codehelper lane status --data-dir ./.codehelper --id LANE_ID
codehelper lane log --data-dir ./.codehelper --id LANE_ID

codehelper fleet list --data-dir ./.codehelper
codehelper fleet inspect --data-dir ./.codehelper --id RUN_ID
```

Fleet 命令用于检查 Audit Ledger；调度权属于 Worker/Task 子系统。

## Review 与 Apply

```bash
codehelper review --workspace . --json
codehelper apply --plan plan.json --dry-run --json
codehelper apply --plan plan.json --json
```

在重要工作区应用生成计划前，必须先检查 Dry Run。

## 可观测性

```bash
codehelper doctor --json
codehelper runtime-observe --events 100 --log-file ./runtime.log
codehelper metrics --data-dir ./.codehelper --json
codehelper scorecard --data-dir ./.codehelper --json
```

日志会脱敏，但仍可能包含敏感工程信息。

## Shell 补全

```bash
codehelper completion bash
codehelper completion zsh
codehelper completion fish
codehelper completion powershell
```

按输出的 Shell 专用说明完成安装。
