# 配置说明

简体中文 | [English](../en/configuration.md)

## 配置优先级

配置从低到高按以下顺序解析：

```text
内置默认值 < TOML 文件 < CODEHELPER_* 环境变量 < 命令行参数
```

不要靠猜测排查配置：

```bash
codehelper config check --config ./codehelper.toml
codehelper config show --config ./codehelper.toml
codehelper config explain execution.verify.mode --config ./codehelper.toml
```

`config show` 会输出 Provenance，可确认每个字段最终由哪个来源覆盖。`config explain`
返回最终值、内置默认值、胜出来源、风险级别和行为影响。

## 配置 Profile

Profile 只控制生成文件中显式写出的默认字段数量，不会形成不同的 Runtime 默认值。

| Profile | 用途 |
| --- | --- |
| `minimal` | 首次本地或 Fixture 成功 Turn |
| `recommended` | 带 Context、Journal 和 Soft Verify 的常规仓库工作 |
| `advanced` | 审阅限制、Worker、Subagent 与 Context Budget |

可以单独渲染 Profile，也可在 Setup 中选择：

```bash
codehelper config profile --profile minimal --workspace . --data-dir .codehelper
codehelper setup --workspace . --profile recommended --interactive
```

## 完整实用示例

```toml
[runtime]
operation_buffer = 64
event_history = 256
subscriber_buffer = 64

[state]
data_dir = ".codehelper"
busy_timeout = "5s"
event_retention = 1000000

[memory]
enabled = false
path = ".codehelper/memory"

[telemetry]
log_level = "info"

[credential]
kind = "env"                 # env | file | keyring
name = "OPENAI_API_KEY"      # 只保存引用，不能填写密钥值

[execution]
provider = "openai"
model = "gpt-4.1"
protocol = "openai_chat"
mode = "act"                 # plan | act | operate
workspace = "."
tools = true
max_output_tokens = 4096
max_steps = 64
timeout = "2m"
idle_timeout = "1m"
max_concurrent = 8
rate_limit = 0
budget_tokens = 0            # 0 表示不增加 Token 上限
budget_usd = 0               # 0 表示不增加成本上限
reasoning_effort = ""
native_search = false

[execution.verify]
mode = "soft"                # off | soft | hard
scope = "diagnostics"        # diagnostics | repository | affected
on_failure = "fail"          # fail | revert
command = ""                 # 可选的显式仓库验证命令
max_repair_steps = 1
timeout = "2m"

[execution.journal]
durable = true
recover_on_start = true

[execution.subagent]
max_depth = 5
max_parallel = 4
max_steps = 8
max_tokens = 0
max_cost_usd = 0
wall_time = "5m"
workspace = "auto"           # auto | read_only | worktree | same_workspace_serialized

[execution.worker]
enabled = false
max_parallel = 2
max_attempts = 1
lease = "30s"
claim_interval = "1s"
automation_interval = "30s"
retry_backoff = "15s"
retry_backoff_max = "10m"
max_tokens = 0
max_cost_usd = 0

[context.index]
enabled = true
max_file_bytes = 1048576
max_files = 20000

[context.repo_map]
enabled = true
max_bytes = 8192
max_directories = 24

[context.working_set]
enabled = true
max_entries = 16
max_bytes = 8192

[context.evidence]
enabled = true
max_entries = 24
max_bytes = 4096

[context.coding_policy]
enabled = true

[context.compact]
max_history_bytes = 262144
summary_max_bytes = 8192
max_digest_entries = 120

[route]
lock = false

[route.plan]
provider = "openai"
model = "gpt-4.1-mini"

[route.vision]
provider = "openai-responses"
model = "gpt-4.1"

[route.subquery]
provider = "openai"
model = "gpt-4.1-mini"

[web]
search_backend = "duckduckgo"
```

未知 TOML 字段会被拒绝。这是有意设计：拼错的安全或预算字段不能“看起来已配置但
实际没有生效”。

## Provider 与模型

主路由由 `[execution].provider` 和 `[execution].model` 决定。`protocol` 描述 Wire
格式，例如：

- `openai_chat`
- `openai_responses`
- `anthropic`

不要猜测标识符，直接查询 Catalog：

```bash
codehelper model list
codehelper model resolve --provider openai --model gpt-4.1
codehelper model resolve --provider openai-responses --model gpt-4.1
```

即使 Model ID 相同，Provider ID 也可能不同。存在歧义时必须显式指定 Provider。

用途路由支持 `plan`、`vision` 和 `subquery`。设置 `route.lock=true` 后，缺失用途路由
会直接报错，不再静默回落到主执行路由。

## 凭证

`[credential]` 只能保存引用：

| Kind | 含义 | 建议 |
| --- | --- | --- |
| `env` | `name` 是环境变量名 | 本地与 CI 最简单 |
| `file` | `name` 是受保护文件路径 | 使用 `0600` 并交给外部 Secret Manager 管理 |
| `keyring` | `name` 是系统 Keyring Key | 桌面交互环境优先 |

使用 `codehelper auth` 管理引用。常规配置与诊断输出会对 Secret 做脱敏处理。

## Mode、Posture 与验证

Mode 写入 TOML；Posture 是 Host/命令级决策，通过参数提供。二者互不替代。

验证模式：

- `off`：不运行 Verify Gate；
- `soft`：收集并报告结论，但不把成功编辑改判为失败；
- `hard`：修复预算耗尽后强制执行结论。

验证范围：

- `diagnostics`：语言或编辑器诊断；
- `repository`：自动探测或显式指定的仓库命令；
- `affected`：根据变更路径推断的检查。

Affected Verification 支持 Go Package Test、JavaScript/TypeScript Test File、Python
pytest File 和 Rust Cargo Test。Build/Lock Manifest 变更会扩大到对应语言的仓库级
Suite。每个 `turn.verification` Check 都包含命令推导原因。无法识别 Topology 的路径会
明确报告 `unavailable`，不会静默成为绿色结果。

只有仓库验证命令在目标沙箱内稳定可复现时，才应使用 `hard`。

## 状态与持久化

默认用户数据目录为 `~/.codehelper/v1`。工作区可通过 `--data-dir` 或
`[state].data_dir` 使用独立目录。

持久化内容包括 Runtime Projection、Event、CAS、Session Metadata、Usage 和 Journal。
项目仍处于公开发布前，不应依赖旧开发提交产生的数据库兼容性。

`execution.journal.durable=true` 会保留中断 Turn 恢复所需的编辑证据，真实仓库应保持
开启。

## 上下文控制

- `index`：有界符号提取；
- `repo_map`：有界仓库结构与入口概览；
- `working_set`：会话触达或 Pin 的路径；
- `evidence`：已证明事实、风险和未验证变更；
- `coding_policy`：稳定工作方法；
- `compact`：长历史何时以及如何压缩。

`search_definition` 和 `search_references` 可接收 `path`、`line`、`character`。提供
具体位置时优先使用注入的 Language Provider；未提供位置或 Provider 不可用时，使用
Lexical Repository Index。结果始终标注 `resolution`、`source`、`version` 和
`confidence`；语义调用失败时还会记录降级原因。

关闭上下文段可以减少输入，但通常会增加重复搜索并削弱连续性。优先调整上限，而不是
直接关闭。

## 环境变量

常用覆盖项：

| 变量族 | 字段 |
| --- | --- |
| `CODEHELPER_PROVIDER`、`CODEHELPER_MODEL`、`CODEHELPER_PROTOCOL` | 主模型路由 |
| `CODEHELPER_MODE`、`CODEHELPER_WORKSPACE`、`CODEHELPER_TOOLS` | 执行行为 |
| `CODEHELPER_MAX_*`、`CODEHELPER_TIMEOUT`、`CODEHELPER_IDLE_TIMEOUT` | 限制 |
| `CODEHELPER_BUDGET_TOKENS`、`CODEHELPER_BUDGET_USD` | 会话预算 |
| `CODEHELPER_VERIFY_*` | 验证行为 |
| `CODEHELPER_STATE_*` | 持久化 |
| `CODEHELPER_CREDENTIAL_KIND`、`CODEHELPER_CREDENTIAL_NAME` | Secret 引用 |
| `CODEHELPER_INDEX_*`、`CODEHELPER_REPO_MAP_*` | 仓库上下文 |
| `CODEHELPER_WORKING_SET_*`、`CODEHELPER_EVIDENCE_*` | 会话上下文 |
| `CODEHELPER_COMPACT_*` | 历史压缩 |
| `CODEHELPER_VISION_*`、`CODEHELPER_WEB_SEARCH_BACKEND` | 专用 Adapter |

权威列表位于 `internal/config/config.go` 的环境变量应用逻辑。

## 配置卫生

- 提交安全示例，不提交个人凭证配置。
- 共享示例优先使用工作区相对路径。
- 生产凭证必须位于仓库外。
- 每次修改配置后运行 `config check`。
- 参数、环境变量和 TOML 行为不一致时运行 `config show`。
- `bypass`、Hard Verify、启用 Worker 和自定义 Shell Command 都应经过 Review。
