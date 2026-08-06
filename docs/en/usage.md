# Usage and Command Workflows

[简体中文](../zh-CN/usage.md) | English

The CLI is the most precise reference for available commands:

```bash
codehelper help
codehelper <command> --help
```

## Core Workflows

### One-shot execution

```bash
codehelper exec [flags] "PROMPT"
```

Important flags:

| Flag | Purpose |
| --- | --- |
| `--config` | TOML configuration |
| `--provider`, `--model`, `--protocol` | model route |
| `--workspace` | tool workspace |
| `--enable-tools` | register built-in workspace tools |
| `--mode` | `plan`, `act`, or `operate` |
| `--posture` | `never`, `suggest`, `auto`, or `bypass` |
| `--data-dir` | durable state directory |
| `--resume` / `--continue` | resume active thread |
| `--file` | pin a workspace-relative file into the working set |
| `--budget-tokens`, `--budget-usd` | cumulative limits |
| `--output-format stream-json` | NDJSON event output |
| `--provider-fixture` | deterministic local provider |

Use `stream-json` for automation. Consumers should process events rather than
scrape human text.

### Interactive terminal

```bash
codehelper tui --config ./codehelper.toml --workspace .
```

The TUI is a host over the same runtime used by `exec`. It does not have a
separate tool policy. Start with `--posture suggest`.

### ACP host

```bash
codehelper host \
  --adapter acp \
  --data-dir ./.codehelper \
  --workspace .
```

ACP is used by editor/agent clients. Workspace identity arguments bind an editor
workspace URI to a runtime root; hosts must not invent or relax that identity.

## Mode and Posture

Mode and posture answer different questions:

| Setting | Question |
| --- | --- |
| Mode | What kind of work is the agent attempting? |
| Posture | How should tool risk decisions be handled? |

Recommended combinations:

| Scenario | Mode | Posture |
| --- | --- | --- |
| repository explanation | `plan` | `never` |
| normal coding | `act` | `suggest` |
| trusted hermetic fixture | `act` | `auto` |
| isolated local experiment | `act` | `bypass` |
| operational investigation | `operate` | `suggest` |

`auto` is not equivalent to “approve everything”; policy may deny high-risk
tools. `bypass` is not equivalent to disabling constitution or sandbox checks.

## Session and Thread Management

Use `--data-dir` to make sessions resumable:

```bash
codehelper exec --data-dir ./.codehelper ... "Start task"
codehelper exec --data-dir ./.codehelper --resume ... "Continue"
```

Inspect metadata:

```bash
codehelper thread list --data-dir ./.codehelper
codehelper thread read --data-dir ./.codehelper --id THREAD_ID
codehelper thread resume --data-dir ./.codehelper --id THREAD_ID
codehelper thread fork --data-dir ./.codehelper --id THREAD_ID
codehelper thread archive --data-dir ./.codehelper --id THREAD_ID
```

Check subcommand help for exact identifiers and output options.

## Models and Capability Probes

```bash
codehelper model list
codehelper model list --live --provider openai
codehelper model resolve --provider openai --model gpt-4.1 --json
codehelper model probe \
  --provider openai \
  --model gpt-4.1 \
  --data-dir ./.codehelper
```

Probe observations normally tighten capabilities. `--trust-probe` allows a
positive observation to widen the effective catalog and should be used only
with a trusted endpoint.

## Credentials

```bash
codehelper auth suggestions
codehelper auth login --config ./codehelper.toml --kind env --name OPENAI_API_KEY
codehelper auth status --config ./codehelper.toml
codehelper auth list
codehelper auth logout --config ./codehelper.toml
```

Commands print references and availability, not secret values.

## MCP, Skills, Plugins, and Hooks

### MCP

```bash
codehelper mcp add --help
codehelper mcp validate --config ./mcp.json
codehelper mcp status --config ./mcp.json
codehelper mcp tools --config ./mcp.json
codehelper mcp serve
```

MCP servers are isolated by health and circuit-breaker behavior. Treat an MCP
server as executable code with its own supply-chain risk.

### Skills

```bash
codehelper skill list
codehelper skill lint PATH
codehelper skill lock
codehelper skill verify
codehelper skill enable NAME
```

### Plugins

```bash
codehelper plugin list
codehelper plugin install NAME@VERSION
codehelper plugin enable NAME
codehelper plugin rollback NAME
codehelper plugin security-revoke NAME
```

Plugin registries, publishers, receipts, and staged artifacts are verified
before activation. Do not bypass trust material for convenience.

### Hooks

Hooks use a versioned JSON configuration supplied with `--hooks-config`. Hooks
run inside the same governed execution architecture and must have bounded
timeouts and explicit permissions.

## Durable Work

### Worker

```bash
codehelper worker enqueue --help
codehelper worker list --data-dir ./.codehelper
codehelper worker run --data-dir ./.codehelper --posture suggest
```

The one-shot `exec` host does not silently run background work. A worker is a
long-lived process with lease, retry, concurrency, and budget configuration.

### Automations

```bash
codehelper automation list --data-dir ./.codehelper
codehelper automation run --data-dir ./.codehelper --id AUTOMATION_ID
codehelper automation pause --data-dir ./.codehelper --id AUTOMATION_ID
```

### Workflows

```bash
codehelper workflow validate --spec workflow.json
codehelper workflow run \
  --spec workflow.json \
  --data-dir ./.codehelper \
  --driver runtime \
  --provider-fixture ./testdata/providers/workflow-schema
codehelper workflow status --data-dir ./.codehelper --id RUN_ID
```

Runtime workflows require a fixture unless live-provider execution is
explicitly enabled.

### Lanes and fleet inspection

```bash
codehelper lane start --help
codehelper lane list --data-dir ./.codehelper
codehelper lane status --data-dir ./.codehelper --id LANE_ID
codehelper lane log --data-dir ./.codehelper --id LANE_ID

codehelper fleet list --data-dir ./.codehelper
codehelper fleet inspect --data-dir ./.codehelper --id RUN_ID
```

Fleet commands inspect the audit ledger; scheduling authority belongs to the
worker/task subsystem.

## Review and Apply

```bash
codehelper review --workspace . --json
codehelper apply --plan plan.json --dry-run --json
codehelper apply --plan plan.json --json
```

Always inspect a dry run before applying a generated plan in a valuable
workspace.

## Observability

```bash
codehelper doctor --json
codehelper runtime-observe --events 100 --log-file ./runtime.log
codehelper metrics --data-dir ./.codehelper --json
codehelper scorecard --data-dir ./.codehelper --json
```

Logs are redacted, but should still be treated as potentially sensitive
engineering data.

## Shell Completion

```bash
codehelper completion bash
codehelper completion zsh
codehelper completion fish
codehelper completion powershell
```

Follow the shell-specific output instructions to install completion.
