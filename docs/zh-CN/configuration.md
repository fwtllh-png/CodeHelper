# 配置说明

## 配置优先级

配置从低到高按以下顺序解析：

```text
内置默认值 < TOML 文件 < CODEHELPER_* 环境变量 < Web 启动参数
```

启动时通过 `--config` 指定文件；解析或校验失败会显示在 Web Boot Failure Surface：

```bash
codehelper --config ./codehelper.toml --workspace . --open
```

MCP Server 定义使用独立、严格且带版本的 JSON 文件，不属于 Runtime TOML 控制面。
Web 通过 `--mcp-config` 传入，并在 Settings 中展示加载状态。

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
max_candidates = 32
max_prompt_bytes = 16384
semantic_rerank = false

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
max_output_tokens = 0           # 0 = 使用当前模型声明的 MaxOutputTokens
max_steps = 32                  # 0 = 显式取消普通 Step Budget
timeout = "2m"                  # 连接、TLS 和响应头阶段
connection_timeout = "0s"       # 0 表示继承 timeout
tls_handshake_timeout = "0s"    # 0 表示继承 timeout
response_header_timeout = "0s"  # 0 表示继承 timeout
idle_timeout = "1m"             # 每个流事件都会续期
max_concurrent = 8
rate_limit = 0
budget_tokens = 0            # 0 表示不设置累计 Session Token 上限
turn_budget_tokens = 0       # 0 表示按当前模型 context window 派生每 Turn 上限
budget_usd = 0               # 0 表示不增加成本上限
reasoning_effort = ""        # 空值为自适应；显式值固定 Effort
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
delegation = "explicit"      # disabled | explicit | adaptive
max_depth = 5
max_parallel = 4
max_resident = 8
max_total = 16
max_steps = 0                   # 0 = 只继承进展收敛策略
max_tokens = 0                  # 0 表示按 Turn 上限和 max_parallel 派生树预算
max_cost_usd = 0
wall_time = "0s"                # 0 = 不设置子 Agent 执行 Lease
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
prepare_tokens = 0 # 0 表示根据当前模型 Context Window 动态计算
auto_compact_tokens = 0 # 0 表示根据当前模型 Context Window 动态计算
emergency_tokens = 0 # 0 表示根据当前模型 Context Window 动态计算
scope = "total" # 或 "body_after_prefix"
summary_max_bytes = 0 # 0 表示使用当前 Turn 的硬输入容量作为渲染 Ceiling
max_digest_entries = 120
truth_max_bytes = 5632
truth_max_entities = 256
mandatory_max_entities = 128
fact_max_entities = 96
verified_change_retention_turns = 32
failure_max_entities = 24
handle_max_entities = 32
omission_sample_max_entities = 8
recent_tail_turns = 2
recent_tail_max_tokens = 0 # 0 表示跟随当前 Route 的动态 Auto Compact 容量
semantic_narrative = "off" # off | post_turn | inline
semantic_narrative_max_input_tokens = 4096
semantic_narrative_max_output_tokens = 512
semantic_narrative_max_items = 32
semantic_narrative_item_max_bytes = 512
semantic_narrative_timeout = "30s"
semantic_narrative_retry_limit = 1
owner_delta_max_segments = 16
owner_delta_max_bytes = 65536

三个窗口阈值为 `0` 时，Runtime 不设置提前压缩档位，而是按当前 Route 的
`ContextTokens - OutputReserve` 得到硬输入容量；只有本次请求无法同时容纳输入和输出
保留时才触发 Tool Result Handle 化与 History Replacement。显式非零值属于 Operator
成本或 SLA Ceiling，仍须满足顺序和模型窗口范围校验。

`recent_tail_turns` 是压缩时优先完整保留的最近 Turn 数；
`recent_tail_max_tokens` 是这些原始消息的显式硬上限；为 `0` 时使用当前 Turn
冻结的硬输入容量。若最近 Turn 本身超过有效上限，
终态维护会在安全的 Tool Call/Result 边界内继续压缩当前 Turn，并通过 Truth Capsule
和 durable rebase 保留目标、事实与变更，而不是把超大的原始 transcript 带入下一 Turn。
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

[route.summary]
provider = "openai"
model = "gpt-4.1-mini"

[web]
search_backend = "duckduckgo"
```

模型目录声明了默认 Reasoning Effort 时，空的 `reasoning_effort` 使用该默认值；
DeepSeek 的默认值为 High，可选档位为 Off、Low、High、Max。未声明模型默认值时，
空值继续从 Medium 开始自适应，复杂架构或 Debug 使用 High，Repair 失败后按模型支持
的档位提升一级。显式 Effort 始终固定，且必须由所有已配置 Route 广告；不支持的值会
在 Provider I/O 前失败。Reasoning Effort 不再改变输出容量。

`max_output_tokens = 0` 会根据当前 Model Catalog 能力和输入投影后剩余的 Context
空间，为每次请求动态计算上限。初始 Ceiling 来自模型声明的 `MaxOutputTokens`；
正值表示 Operator 显式上限。实际请求还会被 Turn/Session Token Budget、USD Budget
和本次输入后的剩余窗口继续收窄。

`delegation = "explicit"` 只在 User、Developer、Skill 或内部 System 明确授权时暴露
`spawn_agent`。`adaptive` 还允许模型在并行收益高于协调成本时主动委派独立工作。
`disabled` 对模型隐藏 Agent Lifecycle Tool，但保留内部授权的 Durable Worker 执行。

`spawn_agent` 从当前 Runtime Turn 自动捕获 Parent Context。`context_mode` 默认是
`task_capsule`；`fresh` 不继承 Parent Context，`last_n_turns` 最多加入
`context_turns` 个包含完整 Tool Call/Result 配对的最近 Turn，`full` 需要明确授权或
Role Policy。Tool 返回 `context_receipt`，记录来源、包含/排除原因、字节和 Token
预算及 SHA-256 Digest。旧的 `fork_context` 和 `parent_context` 参数不再接受。
Capsule 未显式配置容量时，使用父 Turn 硬输入容量扣除当前模型可见 Context 后的余量，
再由 Child Agent Token Budget 收窄；不再套用固定的字节或 Token 档位。

Agent Tree、Mailbox、Result 和 Budget Ledger 持久化在 Workspace State Store。
每个 Agent 具有 Canonical Path 和 CAS Revision；终态 Result 与 Completion Outbox
原子提交。Completion 自动通知 Parent，`wait_agent` 只是对同一事实的主动同步方式。
Mailbox 使用稳定 Message ID 和 `Receive/Ack`，未确认消息在重启后重投。
`max_parallel` 限制活跃 Child 数，`max_resident` 还计入仍保留 Result 或 Worktree 的
已完成 Child，`max_total` 则限制整棵 Durable Tree 的累计 Spawn 数，包括已关闭
Agent。Depth、Token 和 Cost Admission 同样作用于 Nested Agent；Child 只能收窄，
不能扩大 Parent Budget。

Child Authority 只能收紧当前 Session Profile。有效 Posture 遵循
`never < suggest < auto < bypass`；写工具权限是 Parent Tool Catalog 与 Child Role
Contract 的交集，Read-only Role 固定使用 `never`。在 `suggest` 下，Child Approval
会在 Host 中显示 Agent Path 与 Role。Host 通过 Parent Session 提交原 Request ID，
Runtime 将决定路由到权威 Child Thread，并在重启后保留 Pending Approval。Deny 会向
Child 返回结构化 Problem 与 `approval_denied` Tool Result。

Bundled `openai-responses` 路由只有在显式广告 Incremental Transport 时，才会
按 Sticky Session Key 复用 Provider 所有的 WebSocket。第一个 Sample 发送完整
逻辑输入；后续 Sample 仅当所有非输入属性不变，且逻辑输入严格扩展已提交链时，
才发送 `previous_response_id` 与新增输入。Route、属性、Compaction、Retry、
Resume、连接或 Response State 存在任何不确定性时，都发送完整请求。其他
Provider 继续使用原有 HTTP Transport。

Incremental Transport 固定使用 `store=false`。Response State 只保留在活动连接
内存中，并在失败或 Idle Timeout 后删除。Usage Event 仅持久化 Request Bytes
以及 Logical/Transport 的 SHA-256 Digest，不保存 Prompt 内容。Request Byte
下降只属于传输证据，不会被报告为 Token 降幅。

`execution.max_steps` 是普通工作的显式预算，不限制结构化 Finalization。默认值为
`32`；显式配置为 `0` 表示取消普通 Step Budget。正数会被冻结进 Turn Kernel Policy。当显式预算
不少于 64 步时，Runtime 会在剩余 16-32 步时注入一次收敛提醒。显式普通工作预算
耗尽后，Kernel 会在预算之外保留一次 Finalization Sample；它只能请求必需输入，
或声明 Complete/Incomplete 状态，不能继续探索或修改。Kernel 授权的 Repair Steps
拥有独立预算。

Agent 还会跟踪连续没有结构化进展的 Sample；对于正在执行 Workspace 工作的 Turn，
这不是新的 16 步执行上限。连续 16 步无进展时要求模型收敛，32 步时限制继续扩散式
探索，但仍允许精确文件读取、工作区修改、有界 Process 收尾（`exec_command` /
`write_stdin`）、必需用户输入、质量检查、Plan 更新和 Completion。Provider 投影与
Tool Executor 共享同一 Allowlist，因此当前批次已广告的 Tool 不会再被误判为
Terminal-only 而拒绝。48 步时 Kernel 进入同一条结构化 Finalization 路径，不再抛出
局部 No-progress 错误。Complete 声明照常提交；Incomplete 声明记录可恢复的摘要与
具体 Pending Actions。任何 Mutation、Plan 步骤完成、Verification 或 Completion
推进都会立即清零计数。Answer 和 Plan Turn 还会把首次读取的新路径与新 Evidence
计为进展；Operation Turn 会把成功的业务 Tool 结果计为进展。Progress 与
Convergence 状态都会持久化并在 Runtime 恢复后延续。仍保持只读的 Answer 或 Plan
Turn 使用更紧的 8/12/16 阈值，但它只统计连续没有新路径或新 Evidence 的 Sample。
持续发现 Evidence 的研究由明确设置的 `execution.max_steps`、Context Window 和
Token/Cost Budget 约束，不再受内部总 Sample 上限影响。Provider 输出不完整时也不再
有默认续写次数上限，只要这些真实容量仍然可用就继续。

`execution.subagent.max_steps` 同样使用 `0 = 未设置` 语义。可选的
`execution.subagent.wall_time` 是可续期执行 Lease：可观测的子 Runtime 进展会续期；
空闲到期时子 Agent 进入可恢复的 Interrupted 状态，而不是记录为永久失败。

`execution.timeout` 不再是 Provider 调用的总墙钟上限，而是连接建立、TLS 协商和
等待响应头的兼容默认值。非零的 `execution.connection_timeout`、
`execution.tls_handshake_timeout` 和 `execution.response_header_timeout` 可分别覆盖
对应阶段；响应体开始后，生命周期只由 Turn Context 或显式执行 Lease 决定。
`execution.idle_timeout` 约束相邻流事件之间的空闲时间，每收到一个事件就重新计时，
因此持续产出进展的长流不会在固定两分钟后被中断。

这些 Convergence Budget 不等于物理边界或用户配置的硬上限。Runtime 不会越过
Token/Cost Ceiling，不会猜测半截 Tool Call，不会绕过 Content Filter，也不会在安全
Compaction 后请求仍无法放入 Context 的 Sample。

显式 `budget_tokens`、`budget_usd`、Subagent 或 Workflow Token/Cost Budget 耗尽时，
Runtime 返回包含 Scope、资源类型和 Used/Limit 的结构化 `resource_exhausted` Fault。
准入检查改用 Projected/Limit，避免把预留容量误报为已提交消耗。`resume_turn` 保留
继续入口。Main Turn 保留可恢复状态，Workflow/后台 Task 进入 Durable
`blocked`/`waiting`，Child 在提交新 Turn 前拒绝准入。预算未提高或补充前不会自动
重试；恢复后不会重跑已完成的 WorkGraph Node 或已闭合 Tool Effect。

`execution.subagent.max_tokens` 与 `max_cost_usd` 是整棵 Agent Tree 的共享上限。
`spawn_agent` 中的 `max_tokens` 或 `max_cost_usd` 是 resident Agent 跨初始 Turn 与
follow-up 的生命周期上限；每次 follow-up 只预留该 Agent 的剩余额度。child 未显式
填写 Token/Cost 上限时，Runtime 按并发度分配一份 Tree 额度
（`Tree / max_parallel`），不会再让第一个 child 预留整棵 Tree。

未知 TOML 字段会被拒绝。这是有意设计：拼错的安全或预算字段不能“看起来已配置但
实际没有生效”。

`context.compact` 先为 Mandatory Truth 和未闭合因果组分配空间，再保留
Protected/Refreshable Truth、Raw Tail 和可选 Narrative。新增计划、Pending Input
或写工具预留如果会超过 Mandatory 上界，会在状态或副作用提交前返回
`resource_exhausted`。`post_turn` Narrative 在业务终态提交后维护 Context；
`inline` 在安全 Tool Pair 边界提交独立 Context Rebase。两种模式都使用
`route.summary`，禁用工具和原生搜索，失败时保留确定性的 Truth + Tail。
Tool 执行前从当前 Turn 的硬输入容量申请 Result Budget，并按并行 Batch 数量分配，
同时受 ResultStore Capacity 收窄。超过预算的模型可见内容替换为带 Handle 与 Digest
的投影，完整原文仍由 Content Store 持有。后续 Sample 只有在
`输入 + Output Reserve` 超过硬窗口时才进一步缩减可重新获取的 Tool Surface。

Memory 使用带稳定 ID 和 Generation 的记录存储。`user`、`workspace` 和
`repository` Scope 按规范化身份隔离；`remember`、`memory_list`、`memory_get`、
`memory_update` 和 `forget` 都经过 Tool Guard。检索首先匹配精确 Scope，再按词法相关性、
更新时间和稳定 ID 排序；`semantic_rerank` 目前必须保持关闭。

## Provider 与模型

主路由由 `[execution].provider` 和 `[execution].model` 决定。`protocol` 描述 Wire
格式，例如：

- `openai_chat`
- `openai_responses`
- `anthropic`

不要猜测标识符。Web Settings 展示 Runtime 发布的 Provider/Model Catalog；即使
Model ID 相同，Provider ID 也可能不同，存在歧义时必须在 TOML 中显式指定 Provider。

Web Settings 的 Connection 页展示 Workspace Runtime 固定的 Provider、Endpoint、
Protocol 和凭据状态；Models 页展示当前 Session 的 Model、能力来源与 Reasoning 档位。
Composer 可直接切换历史 Model。同一 Provider 内标记为 `hot` 的 Model 可作为 Session
Profile 在 Turn 之间切换；运行中的 Turn 继续使用启动时冻结的 Route。
跨 Provider 切换仍属于 Runtime 启动配置，必须修改 `[execution]` 后重启，界面以只读值
呈现该边界，不显示不可操作的 Provider 下拉框。

用途路由支持 `plan`、`vision` 和 `subquery`。设置 `route.lock=true` 后，缺失用途路由
会直接报错，不再静默回落到主执行路由。

## 凭证

`[credential]` 只能保存引用：

| Kind | 含义 | 建议 |
| --- | --- | --- |
| `env` | `name` 是环境变量名 | 本地与 CI 最简单 |
| `file` | `name` 是受保护文件路径 | 使用 `0600` 并交给外部 Secret Manager 管理 |
| `keyring` | `name` 是系统 Keyring Key | 桌面交互环境优先 |

本机 Web 的 Settings 可以把 API Key 只写入系统 Keychain；浏览器和响应只接收
`configured`、`validation` 与引用类型，不会读回密钥值。Web Provider 每次请求解析
Credential Control 的最新引用，因此页面完成 Keychain 轮换后无需重启。

## Mode、Posture 与验证

Mode 写入 TOML；Posture 是 Web Host 启动决策，通过参数提供。二者互不替代。

验证模式：

- `off`：不运行 Verify Gate；
- `soft`：收集并报告结论，但当 Turn Contract 要求验证时仍会阻止完成；
- `hard`：修复预算耗尽后强制执行结论。

`workspace_change` 与显式 Completion Contract 始终要求验证。其无进展 Repair Budget
耗尽后，无论配置 Mode 为何，Turn 都会进入 `blocked`，Journal 则保留为可恢复 Draft。

验证范围：

- `diagnostics`：语言或编辑器诊断；
- `repository`：自动探测或显式指定的仓库命令；
- `affected`：根据变更路径推断的检查。

Affected Verification 支持 Go Package Test、JavaScript/TypeScript Test File、Python
pytest File 和 Rust Cargo Test。Build/Lock Manifest 变更会扩大到对应语言的仓库级
Suite。每个 `turn.verification` Check 都包含命令推导原因。无法识别 Topology 的路径会
明确报告 `unavailable`，不会静默成为绿色结果。

只有仓库验证命令在目标沙箱内稳定可复现时，才应使用 `hard`。

## 编辑后诊断

`[diagnostics.commands]` 将小写文件扩展名映射到可信的、通过 PATH 解析的可执行程序。
每个命令的有界参数列表必须包含 `{path}`。这些命令会在受 Guard 管理的文件编辑后
执行，因此仓库本地配置只有在被显式信任后才能定义它们。

## 状态与持久化

默认用户数据目录为 `~/.codehelper/v1`。工作区可通过 `--data-dir` 或
`[state].data_dir` 使用独立目录。

持久化内容包括 Runtime Projection、Event、CAS、Session Metadata、Usage 和 Journal。
项目仍处于公开发布前，不应依赖旧开发提交产生的数据库兼容性。

`execution.journal.durable=true` 会保留中断 Turn 恢复所需的编辑证据，真实仓库应保持
开启。

## Observation Capture 与 Export

`[telemetry].log_level` 控制结构化 Runtime Log。Observation Capture 与 OTLP Export
属于进程级运维设置，不是 TOML Field：

| 变量 | 取值 | 行为 |
| --- | --- | --- |
| `CODEHELPER_OBSERVATION_CAPTURE` | `off`、`metadata`、`failure`、`full` | 控制 Durable Observation Admission；默认为 `metadata` |
| `CODEHELPER_OTEL_EXPORTER` | `memory`、`off`、`http/protobuf`、`grpc` | 选择 Observation OTLP Projector；未配置 Endpoint 时默认 `off` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Collector Endpoint | 使用标准 OTLP Endpoint 配置 |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | `grpc` 或 `http/protobuf` | 配置 Endpoint 时选择标准 Protocol |

`metadata` 只保留脱敏 Summary，并丢弃 Raw Payload。`failure` 仅为 Failure-like
Observation 保留符合条件的脱敏 Payload。即使使用 `full`，Credential 与 Restricted
Payload 仍会被拒绝；配置 Secret、State Path 与 Config Path 会在任何 Journal/CAS
写入前脱敏。

Observation Payload Retention 与 `[state].event_retention` 不同。
`event_retention` 按条数限制 Runtime Event History；Observation Payload Reference
按内部时间类别管理：Audit/Diagnostic 默认 30 天，Sensitive 24 小时，Ephemeral
1 小时。启动清理释放过期 Reference，只删除已无引用的 CAS Object。

Remote OTLP 构造失败时，Runtime 会关闭 OTLP Projection，避免无界本地累积。
Observation Queue、Journal 或 Exporter Failure 会通过 Admission Receipt 或
`Flush`/`Shutdown` 返回，但绝不会改变 Turn 的业务结果。

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
| `CODEHELPER_MAX_*`、`CODEHELPER_TIMEOUT`、`CODEHELPER_CONNECTION_TIMEOUT`、`CODEHELPER_TLS_HANDSHAKE_TIMEOUT`、`CODEHELPER_RESPONSE_HEADER_TIMEOUT`、`CODEHELPER_IDLE_TIMEOUT` | 限制 |
| `CODEHELPER_BUDGET_TOKENS`、`CODEHELPER_BUDGET_USD` | 会话预算 |
| `CODEHELPER_SUBAGENT_*` | 委派模式、Tree 限制、Child 预算、Wall Time 与 Workspace 策略 |
| `CODEHELPER_VERIFY_*` | 验证行为 |
| `CODEHELPER_STATE_*` | 持久化 |
| `CODEHELPER_LOG_LEVEL` | 结构化 Runtime Log |
| `CODEHELPER_OBSERVATION_CAPTURE`、`CODEHELPER_OTEL_EXPORTER`、`OTEL_EXPORTER_OTLP_*` | Observation Capture 与 OTLP Export |
| `CODEHELPER_CREDENTIAL_KIND`、`CODEHELPER_CREDENTIAL_NAME` | Secret 引用 |
| `CODEHELPER_INDEX_*`、`CODEHELPER_REPO_MAP_*` | 仓库上下文 |
| `CODEHELPER_WORKING_SET_*`、`CODEHELPER_EVIDENCE_*` | 会话上下文 |
| `CODEHELPER_COMPACT_*` | Token Window 压缩 |
| `CODEHELPER_VISION_*`、`CODEHELPER_WEB_SEARCH_BACKEND` | 专用 Adapter |

权威列表位于 `internal/config/environment.go` 的环境变量应用逻辑。

## 配置卫生

- 提交安全示例，不提交个人凭证配置。
- 共享示例优先使用工作区相对路径。
- 生产凭证必须位于仓库外。
- 每次修改配置后重启 Web，并检查 Boot/Settings 中的结构化状态。
- 启动参数、环境变量和 TOML 行为不一致时，以 Runtime 发布的有效配置为准。
- Hard Verify、启用 Worker 和自定义 Shell Command 都应经过 Review。
