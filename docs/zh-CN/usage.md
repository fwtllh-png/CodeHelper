# 使用指南与命令工作流

CLI 是命令可用性的最准确参考：

```bash
codehelper help
codehelper <command> --help
```

<!-- BEGIN GENERATED COMMAND LIST -->
## 生成的命令清单

此清单从 Cobra Command Tree 生成，请勿手工编辑此区块。

| 命令 | 说明 |
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
| `codehelper fleet` | Inspect the Fleet WorkGraph projection |
| `codehelper fleet inspect [flags]` | Inspect a run with tasks and recent events |
| `codehelper fleet list [flags]` | List runs from a fleet ledger |
| `codehelper fleet logs [flags]` | Print recent ledger events for a run |
| `codehelper fleet profile [flags]` | Show fleet roster/profile (workers, lease, heartbeat) |
| `codehelper fleet status [flags]` | Show one fleet run and its tasks |
| `codehelper help` | Show usage |
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
| `codehelper web [flags]` | Run the local CodeHelper Web workspace |
| `codehelper worker` | Execute durable background tasks |
| `codehelper worker enqueue [flags]` | Queue a task for a worker to execute |
| `codehelper worker list [flags]` | List executable tasks and their leases |
| `codehelper worker run [flags]` | Run the scheduler in the foreground until interrupted |
| `codehelper workflow` | Validate and run Workflow IR specs |
| `codehelper workflow run [flags]` | Run a workflow (RuntimeDriver by default; --driver=fake for unit) |
| `codehelper workflow status [flags]` | Show a workflow run and its durable nodes |
| `codehelper workflow validate [flags]` | Validate a workflow JSON spec |
<!-- END GENERATED COMMAND LIST -->

## Setup Flow

`setup` 同时支持显式自动化和交互式本地配置：

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

该流程会校验 Catalog Route、保存非 Secret 的 Credential Reference、探测宿主机
Sandbox，并执行内置 Hermetic Runtime Fixture。`--probe-capabilities` 是显式的真实
Provider 操作。`--require-ready` 把聚合状态映射为 Exit Code `0`、`1` 或 `2`；
未设置时，只要配置流程本身成功就返回 `0`，Blocked 检查仍保留在报告中。

## Quickstart Journey

```bash
codehelper quickstart --json
```

该命令通过内置、无网络 Fixture 依次执行计划、读取、编辑预览、批准、验证、Receipt
与完成。自动批准只用于内置 Fixture，并绑定到准确展示的 Edit Plan ID；已有的
`sample.go` 绝不会被覆盖。

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

### 本机 Web 工作区

```bash
codehelper web \
  --data-dir ./.codehelper \
  --workspace . \
  --enable-tools
```

Web 只监听 `127.0.0.1`，使用 Capability Token、Host/Origin Fence、类型化 HTTP
请求和下行 WebSocket。Workspace Identity、Session Profile、Tool Catalog、Checkpoint、
Plan、Credential 与 Operation 都由 Runtime 或其所属安全组件校验；浏览器只提交 Intent
并投影结果。

没有 Session 时，Web 使用启动配置页代替 Composer 和 Session 详情栏。该页面集中显示
Runtime Provider、可选 Model、模型级 Reasoning、只写 API Key 状态和 Workspace
Isolation；创建 Session 时一次提交所选 Session Profile。同一 Provider 的可用 Model
可在空闲 Session 中继续切换，跨 Provider 变更仍要求重新启动 Runtime。

Web 的 Session usage 默认使用 `include_children=true`，因此父 Session 的汇总包含
其 `agent_nodes` 下 Child Turn 的消耗；关闭该参数可读取 direct usage。两种口径都保留
原始 Child Session/Thread/Turn 归属，不通过重写账本伪造父子关系。

会话主区域提供 `Chat` 和 `Trajectory` 两个视图。Chat 将 Reasoning、Context 和 Tool
按稳定业务节点渐进披露，成功 Tool 默认折叠；Trajectory 使用同一组 Runtime Event，
并通过只读 `trace/query` 补充受控的 Model、Tool、Approval 和 Verification 时序。
时间数据不可用时仍保留 Event Ledger，并明确显示不可用状态。点击 Chat 中已完成 Tool
的 `Inspect` 可定位到同一 Call 的轨迹记录。

Web 删除 Session 时会要求显式确认。对于已失去执行者的未完成 Turn 或隔离 worktree，
确认删除表示同时丢弃其未完成状态和隔离改动；仍有内存执行者或恢复中 Operation 的
Session 会拒绝删除，必须先停止执行。

`system/diagnostics` 的 `runtime_health` 从 Runtime active registry 和各 Thread Engine
的 in-memory recorder 读取 active Turn、Provider Call、Tool Execution 与 Pending
Interaction。终态 Trace 的 durable source 是 `turn_terminal_envelopes` 中的 frozen
measurement；原始 `spans` 表不是健康检查权威来源。

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

`auto` 不表示“自动同意一切”。在 `act` Mode 下，低风险操作自动放行，Process、
Network 和 Plugin Tool 会先请求审批；在 `operate` Mode 下，沙箱进程可自动执行，
Network 和 Plugin Tool 仍会请求审批。`bypass` 也不会关闭 Constitution 或 Sandbox
硬边界。

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

Plugin 与 Skill 命令投影同一个 Runtime Extension Control Plane。Query 返回
Runtime-owned Source、Trust、Generation、Capability、Health 与 Receipt State。
Mutation 使用稳定 Operation ID 和 Durable Prepare/Commit Receipt，因此以同一 ID
重试时不能静默应用不同 Payload。Disable 会 Drain 所属 Effect，Revoke 会 Fence
已加载 Generation。Host 不能通过扫描文件推断 Extension State。

## 持久化任务

Durable Task、Workflow、Automation、Verification、Background Command 与 Agent
Execution 使用同一套 WorkGraph Lifecycle。一次 Transition 会在同一事务提交
Aggregate Snapshot、Ordered Fact、Command Receipt、Effect Outbox 与兼容 Projection。
恢复时只接续未完成 Node/Attempt，不会重新执行已完成 Node。

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

Fleet 命令只检查 WorkGraph Projection 与 Ordered Fact，不拥有 Enqueue、Claim、
Settle 或 Resume 权限。Worker 是唯一 Claim Authority；Lease Epoch 变化后，旧 Owner
不能再提交 Settlement。Lane Placement 只记录工作位置，不会创建另一套 Scheduler。

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
codehelper features --json
codehelper diagnostics --workspace . --json
codehelper runtime-observe --events 100 --log-file ./runtime.log
codehelper metrics --data-dir ./.codehelper --json
codehelper scorecard --data-dir ./.codehelper --json
```

三个 Readiness 命令共享同一结果模型。`status` 为 `ready`、`degraded` 或
`blocked`；每项检查都包含原因，并可包含影响和修复动作。三种状态的 Exit Code
分别为 `0`、`1`、`2`，自动化判断不会再与 JSON 结论冲突。

日志会脱敏，但仍可能包含敏感工程信息。

`runtime-observe` 执行有界的进程内 Metric/Log 演练，不替代持久化 Turn Accounting。
`metrics` 与 `scorecard` 查询所选 State Database 中的 Usage/Span Row；未知 Cost 或
缺失 Latency 会保持 Unknown，不会被写成 0。

Durable Observation Capture 在 Runtime Process 上配置：

```bash
CODEHELPER_OBSERVATION_CAPTURE=failure \
CODEHELPER_OTEL_EXPORTER=grpc \
OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4317 \
codehelper tui --config ./codehelper.toml --workspace .
```

Capture 默认只保留 Metadata。`failure` 与 `full` 只有在完成 Redaction 后才可能保留
符合条件的 Payload；Credential 与 Restricted Payload 永不持久化。OTLP 默认关闭，
显式配置后支持 gRPC 与 HTTP/protobuf。Observation Journal、Queue 或 Exporter Failure 只影响
对应的 Admission/Flush 结果，不改变 Turn 的 Completed/Failed/Canceled 业务结果。

## Shell 补全

```bash
codehelper completion bash
codehelper completion zsh
codehelper completion fish
codehelper completion powershell
```

按输出的 Shell 专用说明完成安装。
