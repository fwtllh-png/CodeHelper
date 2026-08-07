# Usage and Command Workflows

[简体中文](../zh-CN/usage.md) | English

The CLI is the most precise reference for available commands:

```bash
codehelper help
codehelper <command> --help
```

<!-- BEGIN GENERATED COMMAND LIST -->
## Generated Command List

Generated from the Cobra command tree. Do not edit this block by hand.

| Command | Summary |
| --- | --- |
| `codehelper apply [flags]` | Apply a reviewed patch plan (dry-run by default) |
| `codehelper auth` | Manage credential configuration references |
| `codehelper auth clear [flags]` | Clear a named credential slot |
| `codehelper auth list [flags]` | List named credential slots (non-secret refs only) |
| `codehelper auth login [flags]` | Write a non-secret credential reference into a config file |
| `codehelper auth logout [flags]` | Clear credential references from a config file |
| `codehelper auth set [flags]` | Set a named credential slot (env/file/keyring ref only) |
| `codehelper auth status [flags]` | Show credential source status without leaking secrets |
| `codehelper auth suggestions [flags]` | Show bundled provider credential env slot suggestions |
| `codehelper automation` | Manage recurring automations |
| `codehelper automation list [flags]` | List automations under a data directory |
| `codehelper automation pause [flags]` | Pause an active automation |
| `codehelper automation run [flags]` | Manually enqueue a durable task for an automation |
| `codehelper completion [bash|zsh|fish|powershell]` | Generate shell completion scripts |
| `codehelper config` | Inspect and reload configuration |
| `codehelper config check [flags]` | Validate configuration |
| `codehelper config explain FIELD [flags]` | Explain a resolved configuration field |
| `codehelper config profile [flags]` | Render a configuration profile |
| `codehelper config reload [flags]` | Reload configuration |
| `codehelper config show [flags]` | Show resolved configuration |
| `codehelper diagnostics [flags]` | One-click readiness aggregate (sandbox/content/policy/LSP) |
| `codehelper doctor [flags]` | Report unified runtime readiness |
| `codehelper exec [flags] PROMPT` | Run a non-interactive agent turn |
| `codehelper execpolicy [flags]` | Evaluate sandbox/approval decision for a tool invocation |
| `codehelper features [flags]` | List feature readiness flags (read-only) |
| `codehelper fleet` | Read the Fleet JSONL audit trail |
| `codehelper fleet inspect [flags]` | Inspect a run with tasks and recent events |
| `codehelper fleet list [flags]` | List runs from a fleet ledger |
| `codehelper fleet logs [flags]` | Print recent ledger events for a run |
| `codehelper fleet profile [flags]` | Show fleet roster/profile (workers, lease, heartbeat) |
| `codehelper fleet status [flags]` | Show one fleet run and its tasks |
| `codehelper help` | Show usage |
| `codehelper host --adapter acp [flags]` | Host Runtime over ACP |
| `codehelper init [flags]` | Create a minimal CodeHelper workspace config and data dirs |
| `codehelper lane` | Manage inline/tmux worker lanes |
| `codehelper lane attach [flags]` | Print tmux attach command for a lane (fail-closed without tmux) |
| `codehelper lane list [flags]` | List durable lane records |
| `codehelper lane log [flags]` | Print recent lane log lines |
| `codehelper lane start --data-dir DIR --id ID -- COMMAND... [flags]` | Start a lane process |
| `codehelper lane status [flags]` | Show one lane record |
| `codehelper lane stop [flags]` | Stop a running lane |
| `codehelper mcp` | MCP server and config management |
| `codehelper mcp add [flags]` | Add or replace a stdio MCP server entry |
| `codehelper mcp disable [flags]` | disable an MCP server entry |
| `codehelper mcp enable [flags]` | enable an MCP server entry |
| `codehelper mcp list [flags]` | List servers from an MCP config file |
| `codehelper mcp remove [flags]` | Remove an MCP server entry |
| `codehelper mcp serve` | Serve CodeHelper tools over MCP stdio |
| `codehelper mcp status [flags]` | Connect to MCP servers and report isolated health |
| `codehelper mcp test [flags]` | Hermetically validate an MCP config file |
| `codehelper mcp tools [flags]` | List tool bindings for MCP servers |
| `codehelper mcp validate [flags]` | Validate MCP config (alias of test) |
| `codehelper metrics [flags]` | Report tokens, cost and latency per model and phase from the state database |
| `codehelper model` | Inspect model catalog routes |
| `codehelper model list [flags]` | List catalog providers and models |
| `codehelper model probe [flags]` | Probe provider capabilities and store tighten-only observations |
| `codehelper model resolve [flags]` | Resolve a provider/model against the catalog |
| `codehelper plugin` | Manage plugin trust and enablement |
| `codehelper plugin disable [flags] NAME` | Disable a plugin |
| `codehelper plugin enable [flags] NAME` | Enable a plugin |
| `codehelper plugin install [flags] NAME@VERSION` | Install a plugin |
| `codehelper plugin list [flags]` | List plugins |
| `codehelper plugin revoke [flags] NAME` | Revoke plugin trust |
| `codehelper plugin rollback [flags] NAME` | Roll back a plugin |
| `codehelper plugin security-revoke [flags] NAME` | Security-revoke a plugin |
| `codehelper plugin trust [flags] NAME` | Trust a plugin |
| `codehelper plugin update [flags] NAME@VERSION` | Update a plugin |
| `codehelper pr [flags]` | Prefill an exec/TUI prompt from PR metadata (thin) |
| `codehelper quickstart [flags]` | Run the network-free governed first-turn journey |
| `codehelper review [uncommitted|base <ref>|commit <sha>|custom <text>] [flags]` | Build a reproducible code-review prompt from git scope |
| `codehelper runtime-observe [flags]` | Emit runtime metrics and redacted logs |
| `codehelper sandbox` | Inspect sandbox capability and posture |
| `codehelper sandbox check [flags]` | Hermetic coherence check of declared sandbox posture |
| `codehelper sandbox probe [flags]` | Probe runtime sandbox capability (may be expensive) |
| `codehelper sandbox status [flags]` | Show declared sandbox capability for this platform |
| `codehelper scorecard [flags]` | One-line-per-metric cost, cache and latency rollup from the state database |
| `codehelper sessions` | List or search session snapshots |
| `codehelper sessions list [flags]` | List session snapshots under --data-dir |
| `codehelper sessions search [flags]` | Search session snapshots by substring |
| `codehelper setup [flags]` | Configure and verify a CodeHelper workspace |
| `codehelper skill` | Manage skills |
| `codehelper skill disable [flags] NAME` | Disable a skill |
| `codehelper skill enable [flags] NAME` | Enable a skill |
| `codehelper skill lint [flags] PATH` | Lint a skill |
| `codehelper skill list [flags]` | List skills |
| `codehelper skill lock [flags]` | Write the skill lock |
| `codehelper skill revoke [flags] NAME` | Revoke a skill |
| `codehelper skill verify [flags]` | Verify the skill lock |
| `codehelper thread` | Inspect and manage local thread metadata |
| `codehelper thread archive [flags]` | Move a thread directory under archived/ |
| `codehelper thread fork [flags]` | Copy a thread directory to a new thread id |
| `codehelper thread list [flags]` | List threads under a data directory |
| `codehelper thread read [flags]` | Read thread metadata and file listing |
| `codehelper thread rename [flags]` | Rename a thread directory |
| `codehelper thread resume [flags]` | Mark a thread as active for subsequent sessions |
| `codehelper tui [flags]` | Start the interactive terminal UI |
| `codehelper update [flags]` | Check for newer CodeHelper releases (no auto-replace) |
| `codehelper update check [flags]` | Query latest release metadata |
| `codehelper version [flags]` | Print version information |
| `codehelper worker` | Execute durable background tasks |
| `codehelper worker enqueue [flags]` | Queue a task for a worker to execute |
| `codehelper worker list [flags]` | List executable tasks and their leases |
| `codehelper worker run [flags]` | Run the scheduler in the foreground until interrupted |
| `codehelper workflow` | Validate and run Workflow IR specs |
| `codehelper workflow run [flags]` | Run a workflow (RuntimeDriver by default; --driver=fake for unit) |
| `codehelper workflow status [flags]` | Show a workflow run and its node checkpoints |
| `codehelper workflow validate [flags]` | Validate a workflow JSON spec |
<!-- END GENERATED COMMAND LIST -->

## Setup Flow

`setup` supports explicit automation and prompted local use:

```bash
codehelper setup \
  --workspace . \
  --provider openai \
  --model gpt-4.1 \
  --credential-kind env \
  --credential-name OPENAI_API_KEY \
  --json

codehelper setup --workspace . --interactive
```

The flow validates the catalog route, stores a non-secret credential reference,
probes the host sandbox, and executes a bundled hermetic Runtime fixture.
`--probe-capabilities` is an explicit live-provider operation.
`--require-ready` maps the aggregate status to exit codes `0`, `1`, or `2`;
without it, successful configuration exits `0` while preserving blocked checks
in the report.

## Quickstart Journey

```bash
codehelper quickstart --json
```

This command runs a bundled, network-free journey through plan, read, edit
preview, approval, verification, receipt, and completion. Its automatic
approval applies only to the bundled fixture and is bound to the exact displayed
Edit Plan ID. Existing `sample.go` files are never overwritten.

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
`session/profile/get` returns the Runtime-owned Session Profile and model
capabilities. `session/profile/update` uses `expectedRevision` optimistic
concurrency, refuses updates during an active Turn, and reports whether the
change reset the prompt-cache identity.
`session/tool/catalog` returns the Session projection of the unified Runtime
tool registry. Its enabled state is controlled by the Profile Allowlist and
does not replace Guard or approval decisions.

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
codehelper features --json
codehelper diagnostics --workspace . --json
codehelper runtime-observe --events 100 --log-file ./runtime.log
codehelper metrics --data-dir ./.codehelper --json
codehelper scorecard --data-dir ./.codehelper --json
```

The three readiness commands share one result model. `status` is `ready`,
`degraded`, or `blocked`; every check includes a reason and may include its
impact and repair action. Exit codes are `0`, `1`, and `2` respectively, so
automation and the JSON conclusion cannot disagree.

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
