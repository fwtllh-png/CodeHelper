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
Web 通过 `--mcp-config` 传入，并在 Settings 中展示加载状态。该文件必须位于
`[state].data_dir` 下；启用 stdio Server 还必须显式声明 `host_trusted: true`，因为
Runtime 会将 Server 生命周期绑定到受信配置、Execution Lease 和 Process Broker。

## 完整实用示例

```toml
[runtime]
operation_buffer = 64
event_history = 256
subscriber_buffer = 64

[state]
data_dir = "/absolute/path/outside/workspace/codehelper-state"
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
max_steps = 64                  # 连续无结构化进展的 Step Lease；0 = 不设置
timeout = "2m"                  # 连接、TLS 和响应头阶段
lease_timeout = "2m"            # Guard 授权到 Executor 接管前的 Lease 有效期
approval_timeout = "0s"         # 0 = 审批随 Turn/Session 生命周期，不独立过期
connection_timeout = "0s"       # 0 表示继承 timeout
tls_handshake_timeout = "0s"    # 0 表示继承 timeout
response_header_timeout = "0s"  # 0 表示继承 timeout
idle_timeout = "1m"             # 每个流事件都会续期
max_concurrent = 8
rate_limit = 0                    # 0 = 仅根据 Provider 反馈动态限流
provider_retry_limit = 3          # 每次 Model Sample 的瞬时故障重试预算
budget_tokens = 0            # 0 表示不设置累计 Session Token 上限
turn_budget_tokens = 0       # 0 表示不设置累计 Turn Token 上限
budget_usd = 0               # 0 表示不增加成本上限
reasoning_effort = ""        # 空值为自适应；显式值固定 Effort
native_search = false


`turn_budget_tokens` 统计一个 Turn 内所有模型调用的累计输入与输出。它不是模型的
Context Window：后者只约束单次请求。默认值 `0` 不设置累计上限，单次请求仍受模型
能力约束；连续无结构化进展时仍受 `max_steps` 约束。需要控制成本时应显式设置
`turn_budget_tokens`、`budget_tokens` 或 `budget_usd`。
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
delegation = "adaptive"      # disabled | explicit | adaptive
max_depth = 5
max_parallel = 4
max_resident = 8
max_total = 16
max_steps = 0                   # 0 = 不设置子 Agent Sample 数量上限
max_tokens = 0                  # 0 表示按 Turn 上限和 max_parallel 派生树预算
max_cost_usd = 0
wall_time = "0s"                # 0 = 不设置子 Agent 执行 Lease
workspace = "auto"           # auto | read_only | worktree | same_workspace_serialized

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
truth_max_bytes = 0 # 0 表示根据当前 Route 的硬输入 Token 容量动态计算
truth_max_entities = 256
mandatory_max_entities = 128
fact_max_entities = 96
verified_change_retention_turns = 32
failure_max_entities = 24
handle_max_entities = 32
omission_sample_max_entities = 8
recent_tail_turns = 2
recent_tail_max_tokens = 0 # 0 表示跟随当前 Route 的动态 Auto Compact 容量
semantic_narrative = "inline" # 默认在压缩点生成带来源引用的 continuation checkpoint
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

Web 中的自定义 Endpoint 和内置目录之外的 Model 必须提交完整模型元数据，包括
Canonical ID、Wire ID、Context、Max Output、Capabilities 和可用的 Reasoning
Efforts。该元数据以 `operator_config` 来源保存；只返回 Model ID 的 `/models` 接口
不能作为容量或能力来源。同名 Model 的 probe 结果按 Provider、Endpoint、Protocol 和
Adapter 组成的 Connection Identity 隔离。旧版缺少元数据来源的自定义 Setup Record
不会迁移为猜测值，而会重新进入 Setup Required。

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

[route.summary]
provider = "openai"
model = "gpt-4.1-mini"

[web]
search_backend = "duckduckgo"
```

模型目录声明了默认 Reasoning Effort 时，空的 `reasoning_effort` 使用该默认值；
DeepSeek 的默认值为 High，可选档位为 Off、Low、High、Max。未声明 Effort 集合时，
Runtime 不发送 `reasoning_effort`；声明了集合但没有默认值时，自适应策略只在声明的
集合内选择。显式 Effort 始终固定，且必须由所有已配置 Route 广告；不支持的值会在
Provider I/O 前失败。Reasoning Effort 不再改变输出容量。

`max_output_tokens = 0` 会根据当前 Model Catalog 能力和输入投影后剩余的 Context
空间，为每次请求动态计算上限。初始 Ceiling 来自模型声明的 `MaxOutputTokens`；
正值表示 Operator 显式上限。实际请求还会被 Turn/Session Token Budget、USD Budget
和本次输入后的剩余窗口继续收窄。

默认的 `delegation = "adaptive"` 允许模型在并行收益高于 Spawn 与协调成本时主动委派
独立工作；简单任务、线性依赖任务和写入范围重叠的任务仍由 Parent 完成。`explicit`
只允许 User、Developer、Skill 或内部 System 明确授权的委派，`disabled` 对模型隐藏
Agent Lifecycle Tool。

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

CodeHelper 在二进制中内置版本化的 `system-code-review`、`system-debugging`、
`system-refactor` 和 `system-test-expansion` Skill。它们提供领域工作流及 Subagent
拆分建议，不承载安全或委派授权。Skill 同名覆盖顺序为 Workspace、显式配置目录、
User、Builtin；因此项目可以替换默认工作流。Builtin Skill 可通过现有 Skill Control
禁用，其版本、来源和内容摘要会进入 Catalog 与 Receipt；由于内容随二进制固定，
单独使用 Builtin Skill 不要求 Workspace Lock。

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

`execution.max_steps` 是连续无结构化进展的显式执行 Lease，默认值为 `64`；
显式配置为 `0` 表示不设置 Sample 数量上限。Mutation、Plan 推进、Verification、
Completion 和按 Intent 定义的新 Evidence 会续期 Lease，因此正常产出的长任务不会
因为累计 Sample 数达到 64 而中断。Lease 耗尽后，Kernel 会在预算之外保留一次
Finalization Sample；它只能请求必需输入，或声明 Complete/Incomplete 状态，不能继续
探索或修改。Kernel 授权的 Repair Steps 拥有独立预算。

Agent 还会跟踪连续没有结构化进展的 Sample；对于正在执行 Workspace 工作的 Turn，
No-progress 阶段由显式 `execution.max_steps` 派生：约三分之一时要求收敛，约三分之二
时限制继续扩散式探索，但仍允许精确文件读取、工作区修改、有界 Process 收尾
（`exec_command` / `write_stdin`）、必需用户输入、质量检查、Plan 更新和 Completion；
直到完整 Lease 耗尽才进入结构化 Finalization。Provider 投影与 Tool Executor
共享同一 Allowlist，因此当前批次已广告的 Tool 不会再被误判为 Terminal-only 而拒绝。
Complete 声明照常提交；Incomplete 声明记录可恢复的摘要与具体 Pending Actions。任何
Mutation、任意 Plan 状态变化、Verification 或 Completion 推进都会立即清零计数。Answer
和 Plan Turn 还会把首次读取的新路径与新 Evidence 计为进展；Operation Turn 会把成功
的业务 Tool 结果计为进展。Progress 与 Convergence 状态都会持久化并在 Runtime 恢复后
延续。`execution.max_steps=0` 时不启用基于 Sample 数量的 No-progress 上限，持续工作
仍受模型 Context Window 和显式 Token/Cost Budget 约束。

`execution.subagent.max_steps` 同样使用 `0 = 未设置` 语义。可选的
`execution.subagent.wall_time` 是可续期执行 Lease：可观测的子 Runtime 进展会续期；
空闲到期时子 Agent 进入可恢复的 Interrupted 状态，而不是记录为永久失败。

`execution.timeout` 不再是 Provider 调用的总墙钟上限，而是连接建立、TLS 协商和
等待响应头的兼容默认值。非零的 `execution.connection_timeout`、
`execution.tls_handshake_timeout` 和 `execution.response_header_timeout` 可分别覆盖
对应阶段；响应体开始后，生命周期只由 Turn Context 或显式执行 Lease 决定。
`execution.idle_timeout` 约束相邻流事件之间的空闲时间，每收到一个事件就重新计时，
因此持续产出进展的长流不会在固定两分钟后被中断。

`execution.rate_limit` 是运维侧声明的初始请求速率上限。无论该值是否为零，Runtime
都会按 Provider、Endpoint、Credential 引用和 Model 共享动态限流状态：优先采用
`Retry-After`，其次采用 `RateLimit-Reset`/`X-RateLimit-Reset`；Provider 未返回时间
提示时，根据实际请求耗时和连续限流反馈逐步延长冷却。冷却等待可取消且不会占用
Provider 并发槽。

`semantic_narrative=inline` 会在压缩当前 Turn 时通过 `route.summary` 生成独立的
结构化 Continuation Checkpoint。Checkpoint 保留文件与代码接口、当前工作和下一步，
并要求每项引用输入消息。`off` 只保留 Truth Capsule 与原始 Tail，适合不依赖历史
工作记忆的场景；长时间编码 Turn 不建议关闭。

`execution.provider_retry_limit` 是单次 Model Sample 对 5xx、网络中断、Timeout
等普通瞬时故障的重试预算。明确分类为 `rate_limit` 的 429 不消耗该次数预算：
Runtime 按 Provider 返回的恢复时间或共享动态冷却持续等待并重试，直到 Provider
恢复或 Turn 被取消；账户硬配额错误仍立即停止。等待由 Runtime 调度，不要求模型或
用户轮询。该配置可通过 `CODEHELPER_PROVIDER_RETRY_LIMIT` 覆盖。

`execution.lease_timeout` 是 Guard 完成授权到 Executor 消费 Execution Lease 之间的
公开上限，可由 `CODEHELPER_LEASE_TIMEOUT` 或受信配置覆盖。调用 Context 的 Deadline
更早时使用更早值。Lease 被消费后，运行中进程的 Timeout、Cancel、Wait 和 Reap 由
Executor/Broker 生命周期负责，不能因为 Lease 到期而放弃回收。

`execution.approval_timeout` 是等待人工审批的可选墙钟上限。默认 `0`，审批请求随
Turn 或 Session 的取消而结束，不会因用户暂时离开页面而自动阻断 Turn。受监管环境
可设置非零时长，或通过 `CODEHELPER_APPROVAL_TIMEOUT` 覆盖；到期请求仍按
`approval_expired` Fail Closed，不能在过期后执行。

这些 Convergence Budget 不等于物理边界或用户配置的硬上限。Runtime 不会越过
Token/Cost Ceiling，不会猜测半截 Tool Call，不会绕过 Content Filter，也不会在安全
Compaction 后请求仍无法放入 Context 的 Sample。

显式 `budget_tokens`、`budget_usd` 或 Subagent Token/Cost Budget 耗尽时，
Runtime 返回包含 Scope、资源类型和 Used/Limit 的结构化 `resource_exhausted` Fault。
准入检查改用 Projected/Limit，避免把预留容量误报为已提交消耗。`resume_turn` 保留
继续入口。Main Turn 保留可恢复状态，Child 在提交新 Turn 前拒绝准入。预算未提高或
补充前不会自动重试；恢复后不会重跑已闭合 Tool Effect。

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
默认 `truth_max_bytes=0`，Runtime 按当前 Route 扣除 Output Reserve 后的硬输入
Token 容量动态派生字节上限，并受公开的 1 MiB 安全上限及显式
`summary_max_bytes` 约束；显式非零配置仍优先。
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

Web Settings 的 Connection 页展示并管理 Runtime Provider、Endpoint、Protocol 和凭据；
Models 页展示当前 Session 的 Model、能力来源与 Reasoning 档位。Connection 中的
Provider 变更会在没有活动或待处理工作的前提下重建已注册的 Workspace Runtime，并把
现有 Session Profile 迁移到新 Provider 和初始 Model；构造失败时继续保留旧 Runtime。
Composer 可直接切换历史 Model。同一 Provider 内标记为 `hot` 的 Model 可作为 Session
Profile 在 Turn 之间切换；运行中的 Turn 继续使用启动时冻结的 Route。

用途路由支持 `plan`、`vision` 和 `summary`。设置 `route.lock=true` 后，缺失用途路由
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
`[state].data_dir` 使用独立目录。State Directory 不能位于 Workspace 内部，也不能
包含 Workspace；启用 Durable Journal 时缺少外部 State Store 会导致 Runtime
Fail Closed。

每个 Workspace 在 `<data-dir>/workspaces/<workspace-id>/` 下拥有彼此隔离的
`control`、`sandbox-home` 和 `artifacts` 目录。只有 `sandbox-home` 可映射为 Sandbox
写目录；`control` 和 `artifacts` 不会暴露给 Workspace 进程。

持久化内容包括 Runtime Projection、Event、CAS、Session Metadata、Usage 和 Journal。
项目仍处于公开发布前，不应依赖旧开发提交产生的数据库兼容性。

`execution.journal.durable=true` 会保留中断 Turn 恢复所需的编辑证据，真实仓库应保持
开启。

stdio MCP 配置示例：

```json
{
  "version": 1,
  "servers": {
    "local-service": {
      "transport": "stdio",
      "host_trusted": true,
      "command": "/absolute/path/to/server",
      "tools": {
        "lookup": {
          "capability": "read",
          "access_mode": "read",
          "parallel_policy": "concurrent",
          "sandbox_requirement": "none"
        }
      }
    }
  }
}
```

`host_trusted` 只承认位于外部 State Directory 的 Operator 配置。它表示 MCP Server
当前以宿主进程权限运行，不表示 MCP Tool Call 绕过 Guard。

## Telemetry

`[telemetry].log_level` 控制本地结构化 Runtime Log。Trace Span、Usage 与 Receipt
使用 Runtime 自身的 SQLite 和终态存储，不需要独立 Capture 或 OTLP 配置。

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
| `CODEHELPER_MAX_*`、`CODEHELPER_TIMEOUT`、`CODEHELPER_LEASE_TIMEOUT`、`CODEHELPER_CONNECTION_TIMEOUT`、`CODEHELPER_TLS_HANDSHAKE_TIMEOUT`、`CODEHELPER_RESPONSE_HEADER_TIMEOUT`、`CODEHELPER_IDLE_TIMEOUT`、`CODEHELPER_PROVIDER_RETRY_LIMIT` | 限制 |
| `CODEHELPER_BUDGET_TOKENS`、`CODEHELPER_BUDGET_USD` | 会话预算 |
| `CODEHELPER_SUBAGENT_*` | 委派模式、Tree 限制、Child 预算、Wall Time 与 Workspace 策略 |
| `CODEHELPER_VERIFY_*` | 验证行为 |
| `CODEHELPER_STATE_*` | 持久化 |
| `CODEHELPER_LOG_LEVEL` | 结构化 Runtime Log |
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
- Hard Verify、启用写能力和自定义 Shell Command 都应经过 Review。
