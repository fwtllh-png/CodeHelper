# CodeHelper 架构与设计

> 状态：Active  
> 配套：[README](../README.md)、[使用说明](./USAGE.zh-CN.md)

本文描述 CodeHelper **当前代码树**中的分层、运行时数据流、安全模型与扩展点。不讨论已删除的契约台账 / 阶段脚手架。

---

## 1. 目标与边界

**目标**

- 在本地工作区提供可审计的 AI 编程 Agent（对话 + 工具 + 审批）。
- 同一套 Runtime 协议服务多种 Host：CLI、TUI、HTTP/SSE、ACP、WebUI。
- 默认 fail-closed：无沙箱后端时不强行「假装安全」；密钥不进配置文件。

**非目标（本仓库当前范围）**

- 不绑定某一家闭源 IDE 插件市场作为必选宿主。
- 不把阶段 parity 台账、reference 探针工程链当作产品运行时的一部分。

---

## 2. 分层总览

```text
┌─────────────────────────────────────────────────────────────┐
│ Hosts (internal/host)                                       │
│   CLI · TUI · WebUI · HTTP/SSE · ACP · pairing              │
└────────────────────────────┬────────────────────────────────┘
                             │ Operation / Event (protocol)
┌────────────────────────────▼────────────────────────────────┐
│ Runtime (internal/runtime)                                  │
│   app.Runtime  ←→  agent.Engine                             │
│   wire：仅装配依赖，不承载业务循环                            │
└───────┬───────────────┬───────────────┬─────────────────────┘
        │               │               │
   Orchestration     Adapters        Persist / Security
   task/fleet/…      provider/tool   state/sandbox/policy
```

| 层 | 路径 | 职责 |
| --- | --- | --- |
| Hosts | `internal/host/` | 解析用户输入、呈现结果；只拿 Runtime 与窄接口 |
| Runtime | `internal/runtime/` | Operation/Event 状态机、Agent 循环、wire 装配 |
| Security | `internal/security/` | mode/审批、permissions.toml、constitution、OS 沙箱 |
| Orchestration | `internal/orchestration/` | task / automation / fleet / workflow / lane / subagent |
| Adapters | `internal/adapter/` | 模型目录、Provider、工具、MCP、plugin、skill、hooks… |
| Persist | `internal/persist/` | SQLite / eventlog / snapshot / session / journal |
| Platform | `internal/platform/` | 进程组、PTY、JobCenter、内容依赖探测 |
| Observability | `internal/observability/` | telemetry、usage、diagnostics |
| Config / Build | `internal/config/`、`buildinfo/` | 配置优先级、版本嵌入 |

### 2.1 依赖禁令

1. `runtime/protocol` **不得**依赖任何其他 `internal/*`。
2. `host/*` **不得**直接 import `adapter/tool`、`adapter/provider`、`security/sandbox`、`runtime/agent`（经 `wire` / Runtime 装配）。
3. 工具执行一律经 `adapter/tool/guard`；宿主不得绕过 Guard。
4. 业务循环只在 `runtime/agent`；UI 只保存呈现态。
5. 装配只在 `runtime/app/wire`。
6. TUI 新增逻辑优先经 `host/tui/facade` 访问 session 能力。

自动化：`internal/host/cli` 的 architecture 测试扫描 import 图。

### 2.2 命名规范

- 禁止在生产/测试 `.go` 文件名中使用阶段号前缀（`p10_`、`p28_` 等）。
- `Test…` 名称描述行为，不描述路线图阶段。

---

## 3. 进程入口与装配

```text
cmd/codehelper/main.go
  └─ signal.NotifyContext
       └─ host/cli.RunContext
            └─ cobra 根命令（exec / tui / serve / host / …）
                 └─ runtime/app/wire.NewExec 或 NewPersistentRuntime
                      └─ app.Runtime.Submit(Operation) → Event 流
```

**Wire（`internal/runtime/app/wire`）负责：**

1. `config.Load`（default → TOML → `CODEHELPER_*` → CLI overrides）
2. 解析模型路由（`adapter/model` 内置目录或自定义 base-url）
3. 可选：sandbox、tool registry、Guard、MCP pool、plugin/skill/hooks/memory
4. `promptcontext.Assemble`（稳定前缀）+ `repocontext.New`（易变尾块）+ `agent/engine.New`
5. 返回可执行的 `app.Runtime`（内存或 durable）

Wire **不**实现 turn 循环；循环在 Engine 内。

---

## 4. Runtime 数据流

### 4.1 协议对象

定义于 `internal/runtime/protocol`：

- **Operation**：Host → Runtime 的命令（如 `turn.start`、`turn.cancel`、`approval.decision`、`thread.compact`、`turn.revert`…）
- **Event**：Runtime → Host 的事实流（`turn.started`、`output.delta`、`tool.result`、`approval.required`、`turn.receipt`、`turn.completed` / `canceled`…）

Host 只应依赖 protocol 类型 + Runtime API，不直接调用 Engine。

生态状态同样只走 Event：`tool.catalog.changed` 绑定 sampling catalog snapshot，
`mcp.health.changed` 投影 per-server breaker 状态，`extension.lifecycle` 投影
Plugin 的 name/version/source/publisher/trust/digest 与
`active/installed/updated/rolled_back/enabled/disabled/revoked`。三者均在采样边界
比较快照，事件不携带签名、公钥、路径或完整远端错误。

trusted-host Dynamic Catalog 是显式启用的 session authority：
`wire.Session` 持有 `dynamic.Manager`（Catalog + Broker + 服务端固定 policy），
ACP/HTTP 只做管理与调用回程，不持有第二份目录。register/replace/revoke 原子写入
共享 Tool Registry；采样、revision binding 与执行仍走同一 Registry/ToolGuard。
Host 执行中的调用由 Broker 保留到 result 或 turn cancel，避免 HTTP poll
中断造成调用丢失。客户端 spec 不包含 capability/resource/sandbox authority。

MCP health/catalog callback 的异步 Sync 是低延迟路径，不是 correctness authority。
每次 sampling 冻结 Tool Catalog 前，Engine 经 wire 注入的 `ToolCatalogSync`
同步当前 Pool；失败时以可重试 `unavailable` 终止采样，provider 不会看到旧目录。
异步 Sync 失败还会 quarantine 全部 `mcp:<server>` 与 `mcp:helpers` source，恢复后
重新注册。Catalog binding 除 revision 外还携带不出 wire 的 entry authority token；
connection 重建、replace/revoke 或跨 source 同名接管都会改变 authority，因此
已冻结的旧调用不能解析成另一个 executor。

逻辑会话按 `ThreadID` 隔离：`ThreadManager` 为每个线程懒创建独立 `Engine`/history，两线程并行 `Submit` 互不污染上下文。显式 `thread.compact` 经 `CompactForced` 发布 `thread.compacted`，携带 `replacement_history` 与 `window_*` 链；durable eventlog 可在 resume 时装回模型可见 history。Turn 内在采样前跑 pre-sampling compact gate（超字节/上下文 token 预算先压），发出可观测的 `turn.compaction`。skills/constitution 以带 marker 的 Contextual Fragment 注入（单片 ≤10K tokens）；compact 会从 history 剥离旧碎片，采样时经 PromptContext 重注。TUI `/compact` 与 HTTP `POST …/compact` 均提交 Runtime `thread.compact`。

**上下文装配分两段**：`promptcontext.Assemble` 在 bootstrap 产出**稳定前缀**（base / mode / repository_instruction / 显式 `@file` 工作集 / skills / user_memory / constitution / policy / `coding_policy`），逐字节不变，因此可被 provider 的 prefix cache 命中；每次 sampling 再追加**易变尾块**（同一 Catalog Snapshot 生成的 `tool_catalog`、`repo_map` / `working_set_ledger` / `evidence` / `plan`），以 `RoleSystem` 放在 history 之后——不写 history、不参与 replay。工具目录不再在启动时冻结：同次请求的 `tools[]`、目录尾块和 tool call revision binding 使用同一 snapshot，明细进入 `turn.receipt.catalog`。仓库地图每 turn 只取一次索引快照（`Index.Ensure` 要 stat 全仓库），工作集账本（`workingset.Ledger`，跨 turn 累积、按 turn 衰减、`Fork` 深拷）则每次采样重渲染，所以同 turn 内刚读/刚改的文件当次就可见。各段的字节与截断都进 `turn.receipt.context_sections`。详见 [RFC-001 §10](rfc/RFC-001-repo-context.zh-CN.md) 与 [RFC-012 T2](rfc/RFC-012-ecosystem-runtime.zh-CN.md)。

**证据账本**（`evidence.Set`）与工作集账本并列：它只记**观测到的事实**（哪个工具在哪个 turn 报出了哪个路径/符号，属定义、引用、测试、配置还是纯文本命中——分类由工具经 `tool.MetadataEvidence` 给出）、**从事实差集派生的风险**（改了没验证 / 改了没读过 / 诊断未清）与**软提醒**（重复调用、同内容重读、未消费的结果 handle）。观测点全部挂在 `runTools` 已有的循环上，不新增任何 I/O；账本跨 turn 累积、`Fork` 深拷、不持久化。渲染顺序是"提醒 → 风险 → 事实"，因为截断保前缀。风险与提醒同时进 `turn.receipt.evidence`、`telemetry` 的 `EvidenceRisks`/`PolicyReminders`，事实与改动还会进 compact 摘要——摘要正是模型会丢掉这类知识的时刻。与之配套的静态 `coding_policy` 分区在稳定前缀里，仅在会话启用工具时注册。详见 [RFC-001 §11](rfc/RFC-001-repo-context.zh-CN.md)。

**compact 保留什么由 `internal/runtime/agent/compact` 决定**，engine 只决定何时压与在哪里切。摘要是六段结构 + 一段流水记录：目标、待办（只列未完成的步骤 + "已完成 N 项"）、失败（`compact.Failures` 跨 turn 账本，接在 `runTools` 的 `IsError` 与 `verifyGate.evaluate` 的失败分支上）、改动（`evidence.Set.Changes()`，带 read/verified/diagnostics 三标记）、关键路径（工作集里 `critical` 的条目）、事实（证据账本），最后是被移除消息的逐条流水。**全部从活账本机械派生，compact 期间不发生模型调用**——会写摘要的模型同样会写"我已经验证过了"。渲染顺序即截断顺序（丢了最贵的排最前），段整段丢而不切一半，流水记录**新在前**、从最旧开始丢。摘要消息用 `<codehelper_summary>` marker 包裹，下一次 compact 认出它并**整块结转**（而不是当普通消息压成一行），这是长会话压第二次仍不失忆的唯一机制。切割按 turn 组原子进行，所以一次 compact 至少需要两个 turn。阈值与预算见 `[context.compact]`，次数与节省字节进 `telemetry` 与 `turn.receipt.context_budget`。详见 [RFC-009](rfc/RFC-009-context-lifecycle.zh-CN.md)。

### 4.2 一次 Turn（简化）

```text
Host Submit(StartTurn)
  → Runtime 分配 thread/turn/item
  → Engine 冻结 TurnContext（model/mode/posture/sandbox/workspace）
        → Guard 安装 CloneSampling policy；会话 SetPolicyMode/SetPermission 只影响下一 turn
  → Provider.Stream（adapter/provider）
  → 如有 tool_calls：
        Guard 鉴权 → 冻结 policy + permissions/constitution
        → 写入路径：before 指纹 + before-image（journal）
        → 工具执行（可进 sandbox 进程）
        → after 指纹比对 → 观测到的改动（kind + 行级增删）记入 turnDiff
        → 结果回灌模型（含可恢复失败）
  → 无 tool_calls 且本 turn 改过文件：Verify Gate
        pass → 提交 journal
        fail 且有修复配额 → [verify] 反馈回灌 → 继续采样
        fail 且配额耗尽 → 按 on_failure 失败或回滚
  → Emit turn.receipt（执行收据）
  → Emit turn.completed / 失败 Problem；恢复会话 policy 指针
```

**改动以观测为准（`tool/guard` + `engine/turndiff.go`）**：本 turn 改了哪些文件不再解析工具参数，而是比对每个 write 资源在执行前后的指纹，得出 `created` / `modified` / `deleted` 与相对 turn 起点的行级增删，写进 `result.Metadata["changes"]` 由 engine 记入 `turnDiff`。因此参数里没有单一 `path` 字段的工具（`file_patch`、`file_apply`）同样被 Verify Gate 与收据覆盖；用相同字节重写文件不算改动。详见 [RFC-005 §3](./rfc/RFC-005-edit-transaction.zh-CN.md)。

**工具失败的两类处置**：模型自己能改对的错（`ErrUnread`/`ErrStale`、参数 schema 错误、未知或不可用工具、`ErrPrecondition`、`approval_denied`）以 `tool.Result{IsError: true}` 回灌，turn 继续；策略与沙箱拒绝（`permission_denied`、`mode_denied`、`constitution_deny`、sandbox deny）终止 turn——回灌拒绝会让模型反复试探权限边界，而当前没有拒绝次数预算。分类见 `engine/toolfailure.go`，理由见 [RFC-002 §2](./rfc/RFC-002-verify-gate.zh-CN.md)。

**Verify Gate（`engine/verify.go`）**：在 journal 提交之前对本 turn 改动的文件形成验证结论，只在 `turnDiff` 非空时触发（只读 turn 不验证）。默认 `mode = soft`、`scope = diagnostics`：失败会先尝试修复，耗尽后记录但不阻断；CI/自动化需显式用 `hard`。`diagnostics` 复用已收集的 post-edit 诊断，`repository` 跑仓库验证命令（`observability/verify`），`affected` 经仓库索引把改动路径收敛到受影响测试（Go 包 → `go test ./dir/...`，Python → `pytest <files>`，其余语言映射不出来就报 `unavailable` 而不是判过；配了 `command` 则以它为准并支持 `{paths}`/`{packages}` 占位）。hard 失败时先花修复轮配额（配额独立于 `MaxSteps`，不挤占模型正常步数），耗尽后按 `on_failure` 取 `fail`（`turn.failed`）或 `revert`（`turn.completed` 但跳过提交，由 journal 回滚文件）。每次评估发 `turn.verification`，其 `status`（验证结论）与 `action`（门禁处置）是两个字段——soft 模式下 `failed` + `reported` 表示"记录但不阻断"。

**Execution Receipt（`turn.receipt`）**：每个 turn 在终态事件之前发布一份统一收据，含目标与 plan、mode/posture/sandbox/workspace、file 工具改动路径与读取路径、上下文分区、证据（事实/风险/提醒）、成功与失败工具、审批次数、诊断结论、token/cost/latency 与未解决问题。设计约束：

- 只记录**观测到的**事实。诊断未运行时 `verification.diagnostics = not_evaluated`，与 `passed` 严格区分，收据不会暗示未做过的验证。`verification.verify` 记 Verify Gate 结论；`verification.tests` 仅在 gate 真的跑过仓库命令（`scope = repository`）时同步该结论，诊断 scope 下保持 `not_evaluated`。
- 定价未知时 `cost_known = false`，`cost_microunits` 无意义，宿主必须显示 unknown 而不是 0。
- 改动条目带 `kind`（created/modified/deleted）与相对 turn 起点的 `added`/`removed` 行数；二进制内容不报行数（缺数字比 0 诚实）。自动回滚未能恢复的路径进 `unresolved_issues`。
- `evidence` 同样只记观测：事实来自工具结果元数据，风险是"改了什么"与"证明了什么"的差集，两者都不接受模型自述。
- 尚未采集的分区列在 `not_collected`，避免空分区被读成"什么都没发生"；分区实现后从该列表移除（`read_paths` 随工作集账本、`evidence` 随证据账本划掉，现只剩未回滚副作用）。
- `turn.receipt` 不在 `eventlog.ShouldPersist` 的过滤名单内，因此作为审计事件落盘。

Pending-work（steer / mailbox / approval / input）经 `RoutePending` 规则表调度：运行中灌当前 turn、暂停则 resume、idle 可 `start_new_turn`（steer / mailbox+trigger）、mailbox 无 trigger 则 buffer。

### 4.3 持久化路径

| 模式 | 触发 | 存储 |
| --- | --- | --- |
| Ephemeral | 默认 `exec` 无 `--data-dir` | 内存 Runtime |
| Durable | `--data-dir` / `serve` / ACP | `persist/state`（SQLite + eventlog + CAS） |

Durable 模式下支持 `--resume` / `--continue` 续跑线程；session/UX 快照在 `persist/session`。

---

## 5. 安全模型（四层，勿混用）

| 层 | 包 | 作用 | 可否 bypass |
| --- | --- | --- | --- |
| Session Policy | `security/policy` | mode=`plan\|act\|operate`；posture=`suggest\|auto\|bypass\|never`；生命周期 grant。`operate` 相对 `act` 仅放大 `auto` 下 Process（allow）与 Network/Plugin（ask）判定；`plan` 仍硬只读 | posture=bypass 可弱化审批，**不能**跳过 constitution |
| Permissions | `security/permissions` | 工作区 `.codehelper/permissions.toml` 的 allow/ask/deny 记忆 | 受 policy 约束 |
| Constitution | `security/constitution` | `~/.codehelper/constitution.json` + 仓库 constitution 机械 hold | **不可**被 bypass 跳过 |
| Sandbox | `security/sandbox` | 工作区路径边界 + OS 后端（Seatbelt / bubblewrap / landlock 等） | 无强后端时 fail-closed |

**工具路径：**

```text
Engine → tool/guard → (policy + permissions + constitution) → tool 实现 → platform/process(+sandbox)
```

原则：

- Host 永不直接 `exec` 用户命令绕过 Guard。
- 审批事件 `approval.required` 带 `allowed_scopes`（`once` / `session` / `always`）与可修改参数列表。
- 强沙箱工具在执行被识别为 sandbox deny 后，可经 Guard `EscalationPolicy`（默认 escalate-on-failure）**再审批**后以降级 `SandboxAttempt=none` 重试；指纹含 `sandbox:none`，与沙箱内 session grant 隔离，禁止静默 unsandbox；escalate 审批不提供 `always` persist。
- 日志与 metrics 默认脱敏；`scripts/test-secret-leak.sh` 做泄漏回归。

---

## 6. 适配器层设计

### 6.1 模型目录

- 文件：`internal/adapter/model/catalog.v1.json`（`//go:embed`）
- API：`DefaultCatalog()` / `NewResolver()` / `Resolve(RouteRequest) → ReadyRoute`
- ReadyRoute 包含：provider kind、endpoint、wire protocol、credential 引用、模型 limits/capabilities/pricing
- CLI：`codehelper model list|resolve`

自定义 endpoint 可通过 `--base-url` + `--api-key-env` + 可选模型元数据 flags 覆盖。

### 6.2 Provider

- `adapter/provider`：归一化流式事件
- 实现：`openai`、`anthropic`、通用 HTTP client、fixture server
- Hermetic 测试使用 `testdata/providers/*` SSE fixture

### 6.3 Tool

- 注册表：`adapter/tool`
- Guard：统一资源声明、并行 claim、审批挂钩
- 内置能力分布在 `tool/file`、`shell`、`search`、`git`、`web`、`quality`、`mcp`、`rlm`、`task`… 等子包

文件写入工具：

| 工具 | 边界 | 说明 |
| --- | --- | --- |
| `file_write` | 单文件 | 原子整文件写入；已存在则保留权限位 |
| `file_edit` | 单文件 | 唯一匹配替换 |
| `file_apply` | **一次调用 = 一次事务** | `changes[]` 支持 `write`/`edit`/`move`/`delete`；validate-then-apply，校验期在内存合成、任一前置条件不满足则零写入；`dry_run` 返回 unified diff 而不落盘。不依赖 git 与强沙箱 |
| `file_patch` | 一次 diff | `git apply`，需强沙箱；能吃任意 unified diff |

`file_write` / `file_edit` 复用 `file_apply` 的 apply core，避免两套匹配与写入逻辑漂移。资源声明上，`file_apply` 用 `ResourceResolver.ChangesField` 枚举 `changes` 里的全部 `path` 与 `to`（move 的源与目标都算 write）；枚举不出来就拒绝调用，绝不放行未被 Guard 中介的写入。事务边界与 op 语义见 [RFC-005](./rfc/RFC-005-edit-transaction.zh-CN.md)。

检索工具：

| 工具 | 依赖 | 说明 |
| --- | --- | --- |
| `search_text` / `search_files` / `search_project` | `platform/repowalk` | 文本与文件名检索；纯 Go，无 ripgrep |
| `project_map` | `platform/repowalk` | 目录概览；与搜索共用同一份枚举与跳过规则 |
| `search_symbol` / `search_definition` | 仓库索引 | 声明查找（子串 / 精确）；结果带 `resolution: lexical` |
| `search_references` | 仓库索引 | 在索引文件清单上做词边界扫描，默认排除定义点 |
| `search_related_tests` | 仓库索引 | 按语言命名约定映射源文件 → 测试文件 |

`repowalk` 一次 `git ls-files --cached --others --exclude-standard -z` 枚举未忽略文件（ignore 语义交给 git，submodule 内容因此不可见），非 git 仓库退回 `WalkDir`；`vendor`/`node_modules`/`.git`/`.codehelper` 始终按名字跳过。索引在 SQLite v5 的 `repo_index_*` 表里，第一次符号查询惰性构建、之后按 size/mtime + digest 增量刷新。索引不可用（`disabled` / `degraded`）时四个符号工具报 `AvailabilityUnavailable` 并说明原因，文本搜索不受影响。边界与降级契约见 [RFC-001](./rfc/RFC-001-repo-context.zh-CN.md)。

### 6.4 扩展面

| 扩展 | 包 | 说明 |
| --- | --- | --- |
| MCP | `adapter/mcp` + `adapter/tool/mcp` | stdio/streamable HTTP/legacy SSE；per-server reload、health/breaker、permission ceiling；以 `mcp:<server>` source 将远端工具作为 deferred producer reconcile 到统一 Registry，`tool_search` 后才 materialize |
| Plugin | `adapter/plugin` + `adapter/tool/plugin` | 本地 `unsigned-local` 信任；Registry release Ed25519/publisher allowlist；内容寻址缓存与安全解包；durable lifecycle watcher 以 `plugin:lifecycle` source 原子切换 Tool executor；普通 update/rollback drain 在飞调用，security revoke 立即取消 |
| Skill | `adapter/skill` + `adapter/tool/skill` | precedence 发现；strict `skill.toml`、SemVer/DAG、workspace lock 与拓扑 `load_skill`；legacy workspace/user Skill 为 `local/unlocked` |
| Hooks | `adapter/hooks` | before/after 钩子；超时杀进程树 |
| Memory | `adapter/memory` + `tool/memory` | 可开关的持久笔记 |
| LSP | `adapter/lsp` + `tool/lsp` | 诊断/检查辅助 |

装配入口：`wire.NewExec` 按 flags/config 条件注册。

---

## 7. 编排层设计

| 包 | 职责 |
| --- | --- |
| `orchestration/task` | 任务投影与状态、按 `sessions → workspaces.root_path` 限定 execution authority 的 lease claim、heartbeat / settle / requeue / reclaim 与 attempt 审计；**不**自己跑模型 |
| `orchestration/worker` | 唯一的调度器：claim / reclaim / automation 三条循环、`Executor` 插件点、drain 时把在飞任务放回队列；跨 session 接管不跨 workspace |
| `orchestration/automation` | 周期/触发式 automation 记录与入队（executor 与最大尝试次数随记录走） |
| `orchestration/fleet` | 只追加的 JSONL 审计轨；**不调度**（调度归 `worker`） |
| `orchestration/workflow` | 受限 Workflow IR + JS VM（`jsvm`）+ orchestrate 驱动；DAG 依赖、条件、节点级 retry/timeout，attempt context 下传到 Driver/真实 turn，取消收敛后才 retry；节点 checkpoint 落库后可续跑；response schema 在 Driver 边界做 JSON 后置校验 |
| `orchestration/lane` | inline / tmux worker 会话 |
| `orchestration/subagent` | 角色路由、工作树隔离、mailbox、takeover；控制面 `List` / `Wait` / `FollowUp` / `Interrupt`（生命周期 Status）；模型工具 `agent` / `agent_wait` / `agent_list` / `agent_followup` / `agent_interrupt` / `agent_close`（Host 只渲染，不直调 Manager） |

编排组件通过 CLI 与 Runtime/工具面交互，避免 Host 直接拼底层 Provider。
Workflow `Node.response_schema` 与 JS `task({response_schema: ...})` 进入同一个
`TaskRequest`：production driver 在启动 turn 前编译本地 schema（禁止外部 `$ref`），
完成后校验唯一 JSON value，失败则节点失败。该层不声称 provider constrained
decoding。`profile` 当前没有 registry 或 route 定义，非空值在启动 turn 前拒绝，
不会降级成无约束 prompt 文本。

Workflow runtime 按 DAG 波次并发提交节点，但 production 普通 thread 共用宿主
workspace journal。journal 的 commit/rollback 是 whole-turn transaction，因此这些
thread 共用 `WorkspaceTurnGate` 并实际串行；隔离 worktree Engine 会清掉宿主
journal 与 gate，可独立运行。当前 Workflow 尚未提供每节点 worktree + merge。

后台 `agent_turn` 的写入终态跨过两棵树：child 在隔离 git worktree 完成后，
executor 在 task settle 前持有宿主 `WorkspaceTurnGate`，复用 `agent_merge` 的
spawn-time baseline 检查、ToolGuard resource claim 和 parent journal 应用改动。
apply 失败时 task 为 failed 且 `merged=false`，worktree 随后销毁；只有 apply
成功或 `same_workspace_serialized` 已直接写入宿主根时才报告 `merged=true`。

---

## 8. 持久化与可观测性

**Persist**

- `state`：投影库 + eventlog + CAS 锁
- **写边界**：`Append` / `AppendEvents` 只写 durable eventlog（及 `event_index`），不改 thread title/status/parent；`PatchThreadMeta` 只改关系型 thread 元数据，不追加 event、不推进 sequence。二者不可混写到同一 API。
- **落盘策略**：durable 路径经 `eventlog.ShouldPersist` 过滤 streaming noise（`output.delta` / `reasoning.delta` / `tool.state` / `tool.output` / `turn.compaction`）；审批、turn 终态、compact/fork、tool.result 等审计事件必留。Live 订阅不受影响——`tool.output` 是运行中工具的实况解说，完整输出随 `tool.result` 落盘，所以重放不需要那些碎片。
- **Agent 图**：`agent.spawned` / `agent.status` / `agent.message` 进 eventlog，并投影 `agent_spawn_edges`；重启后 `List(ParentID)` 可读边，mailbox 不是唯一真相源。
- `snapshot`：检查点
- `session` / `session/ux`：会话与 UX 快照
- `contentstore`：大体量工具结果内容。`Memory` 是进程内的；`Durable` 把引用计数 CAS 适配成同一接口，journal before-image 与 workflow 节点输出走它，`Release` 掉最后一个引用即删除（磁盘上留着无人引用的字节只会让目录单向变大）
- `workspacejournal`：工作区变更日志，支撑 `review` / `apply`。账本与 before-image 落在 `{workspace}/.codehelper/journal`，下一个进程启动时回滚被杀进程留下的未 commit turn；已 commit 的保留，owner 进程仍活着的跳过
- task startup recovery：只分流没有 lease 的旧式 `running` 行；带 lease 的 task 不因另一个 Host 打开数据库而改变，必须由 scheduler 在 lease 到期后 `Reclaim`
- `joblog`：后台 job 输出的落盘副本。内存环形缓冲有界，poller 落后时 cursor 仍指向流里同一个位置，字节从这里补

**Observability**

- `telemetry`：日志级别、指标
- `usage`：用量仓储
- 工具内部模型采样的 usage 也走 Runtime durable event；emit/projection 返回错误会
  终止对应 turn，不能继续生成与 usage 投影不一致的成功 receipt
- `diagnostics`：诊断汇总

CLI：`metrics`、`runtime-observe`、`doctor`、`diagnostics`。

---

## 9. Host 适配一览

| Host | 入口 | 说明 |
| --- | --- | --- |
| CLI | `codehelper <cmd>` | cobra；`exec` 为非交互主路径 |
| TUI | `codehelper tui` | Bubble Tea；slash 命令见 `host/tui/commands` |
| HTTP/SSE | `codehelper serve` | Runtime API + 事件流；提供 thread/task/agent/usage 读面 |
| ACP+ | `codehelper host --adapter acp` | stdio JSON-RPC；`session/submit` 承载任意 Operation，`session/replay`/`session/load` 提供游标恢复，`thread/task/agent/usage` 方法提供有界只读投影 |
| VS Code Companion | `extensions/vscode` | ACP+ client；拥有 binary 子进程、握手、Trust posture 与 workspace cursor，不拥有第二套 Runtime |
| WebUI | `codehelper web` | 内嵌静态控制页；可配合 pairing QR |

所有 Host 共享同一套 Operation/Event 语义，而且这句话有测试兜着：`internal/host/runtimeapi/contract` 是一份用协议词汇写的场景表，ACP 与 HTTP 各提供一个 driver 把它翻译成自己的信封（`make protocol-contract`）。场景断言的是「一个 turn 恰好一个终态」「按游标续读不丢不重」「history 只有落盘事件、live 才有增量」「thread/task/agent/usage 读模型双 Host 同源」「catalog/health/lifecycle 三类生态事件双 Host 同源」「取消恰好一个终态」「点名不存在的 turn 必须被拒且不许说可重试」「审批挂住并被决定恢复」。只读 DTO 统一定义在 `internal/host/runtimeapi/view`；协议形状另有生成式 JSON Schema（`make protocol-schema`，产物 `docs/protocol/runtime-protocol.schema.json`）与漂移测试。

ACP 的事件模型是**会话级**的：`Serve` 启动时向 Runtime 建立一条随进程生命周期的订阅，后台 `pump` 按 `event.ThreadID` 找到所属会话并转发 `session/update`，顺带驱动 `session/prompt` 的终止回复。这样在两个 turn 之间提交的 Operation（compact/fork/revert）产生的事件同样有出口。订阅被 Runtime 作为慢订阅者丢弃时按最后转发的 sequence 重订；重订发现历史已被裁剪则推 `session/desync`，而不是假装恢复成功。协议细节见 [RFC-003](./rfc/RFC-003-vscode-transport.zh-CN.md)。

Runtime 重启后的模型 history 从 durable event 重建，但提交边界必须与 live Engine 一致：
`turn.started`、`tool.start`、`tool.result` 先按 turn 暂存，只有 `turn.completed` 才进入
history；failed、canceled 和进程中断留下的半截 turn 被丢弃。`tool.start` 恢复为
assistant function call，只有 start/result 成对的 call ID 才能进入 provider replay；
Responses encoder 还会在 HTTP 前再次拒绝孤立 `function_call_output`。

VS Code Extension Host 为 local workspace 的每个 root 管理一个 ACP 子进程。
binary 先做 `version --json` 身份检查，再以 `initialize` 协商版本、方法和
Operation/Event 集；异常退出最多有界重启三次。扩展持久化 session/thread/cursor，
恢复时先分页 replay durable history，并缓冲同时到达的 live event，避免新 sequence
抢先推进 cursor。Workspace Trust 只决定 Runtime posture：trusted 为 `suggest`，
untrusted 为 `never`；UI 不承担最终安全边界。

每个 root 的 BindingStore v3 保存最多 32 个 session binding、当前选择和独立 cursor。
自动引导的首个 Chat 保持 shared 语义；显式 New Chat 通过
`session/new.isolation=worktree` 创建独立 worktree、Engine、sandbox、tool registry
和 workspace journal。隔离 Engine 不继承主 `WorkspaceTurnGate`，所以不同 Chat 的
provider、工具和验证可并行；submit/cancel/approval/input 始终携带 session identity。
`session/load` 按 durable metadata 派生并复验 worktree，`session/history` 为新的
Extension Host hydration 最近 200 个 turn，live `session/replay` 继续只从已持久化
cursor 接续。

占位 Chat 标题在首个 prompt 被 Runtime 接受后由 Extension Host 通过纯本地、有界
规则生成。`session/rename` 只更新该 binding 的 primary thread，随后 BindingStore v3
原子替换对应 title；失败只保留占位名，不影响已提交 turn。Threads Tree、Chat selector
和重启恢复因此读取同一持久化标题。

隔离 Chat 不直接写主 workspace。`session/merge` preview 复用 `file_apply` planner
生成绑定 before/after、diff 和 path 的 plan ID；apply 重新规划并比较 identity，再走
expected fingerprint、workspace journal 和 atomic transaction。创建 Chat 时保存的
主 workspace baseline 已漂移时零写入拒绝；`PermissionNever` 下 Runtime 自身拒绝
apply，不能靠 Webview 隐藏按钮代替安全门禁。

Chat Webview 不直接持有 transport：`SessionCommands` 是 submit/cancel/approval/input
的唯一扩展写面，`ChatProjector` 消费 typed Event 并批量生成只读 snapshot。Webview
使用 nonce CSP、无远程资源，消息严格枚举且不能提交路径。模型正文和 textual
`reasoning.delta` 先由 Extension Host 解析成有界 Markdown AST，Webview 只通过
标签白名单和 `createTextNode` 创建 DOM，不接收 HTML；推理过程与最终结论分区显示，
signature/加密 reasoning data 不进入 UI。`@file` / `@selection`
由 Extension Host 从 active editor 生成 `turn.start.context`；Runtime 用
`sandbox.Workspace` 重新校验 canonical path、file URI、完整文件 SHA-256 与 UTF-16
range，再把有界正文加入模型 prompt。eventlog 保存模型实际 prompt 用于恢复，同时
`turn.started.display_prompt` 给 UI 保留未展开的用户文本。

V2 Context Bridge 继续复用 `turn.start.context`：引用可标记
`file|selection|symbol|diagnostics` 和显式来源，但每项仍必须绑定 file URI、
workspace-relative path、document version 与完整 SHA-256。Runtime 复验文件身份及所有
UTF-16 range 后才展开正文，并把逐项 `EditorContextReceipt` 同时写入 durable
`turn.started` 与 terminal `turn.receipt`。symbol/diagnostic 只是有界、不可信的编辑器
metadata；Runtime receipt 证明模型实际收到的字节和截断，不证明 language service
语义正确。

V3 的 `WorkspaceIdentity` 不把 remote editor URI 当作文件路径。Extension Host 以
canonical workspace URI 计算 root ID，并同时绑定 editor URI、Runtime 绝对路径和
remote kind；该身份进入 CLI launch、ACP initialize 和 `turn.start`。Runtime 先验证
editor URI 的 scheme/authority/root-relative path，再映射到 Runtime path 走既有
sandbox/symlink/digest/range 复验。binary target、ACP/operation range 和 required
features 来自同一机读 compatibility manifest；生产扩展不会仅凭 executable 名称启动。

V3 binary 来源为 `external|managed|bundled`，`auto` 只按有效 bundled、有效 managed、
external 选择。managed 与 bundled 共用 strict canonical release manifest、Ed25519
trust root、key rotation chain、target/size/SHA-256 和 compatibility verifier。managed
artifact 写入 Extension Host `globalStorageUri` 的内容寻址目录；跨窗口锁串行安装，
`state.json` 通过同目录 rename 原子发布 active/pending/last-known-good。新 artifact
完成 ACP handshake 和 session attach 后才转 healthy；启动失败回滚，revoked active
与 last-known-good 都不能执行。下载只访问固定 HTTPS origin，并限制 redirect、timeout
和 bytes；manifest 以 ETag/`If-None-Match` 重验证，缓存 bytes 仍重新验签。更新检查
single-flight，staging 前保留 artifact 两倍大小加 16 MiB 的磁盘余量；activation
热路径只读本地 state，不访问网络。

V3 release matrix 从同一份 signed binary manifest 生成 universal thin VSIX 与五个
`TargetPlatform` 包。target 包只含自身 binary；universal 不含 `bin/`。总 provenance
绑定 extension bundle、compatibility、trust roots、binary manifest、CycloneDX SBOM、
channel mapping 和所有 VSIX digest。Marketplace/Open VSX 只改变 publish command，
企业/离线目录复用相同 artifact、provenance 和 checksum。源码 manifest 保留
`private=true`，只有通过签名输入校验的 release staging 才删除，publish credential
不进入产物或 dry-run plan。

release provenance 额外绑定 Git commit、完整 source tree fingerprint 和
`clean|dirty` 状态。生产签名构建只允许 clean worktree；dirty tree 只允许生成
`validated-dry-run`，且必须保持 `publishable=false, uploaded=false`。RC 聚合器独立
复验 VSIX allowlist、dependency audit、secret scan、CycloneDX SBOM、Ed25519
manifest、SHA256SUMS、15/15 E2E matrix 与性能报告。

扩展 manifest 固定 `extensionKind=["workspace"]`，所以 binary discovery、storage、
PATH、cwd 与 Runtime child process 都位于 Workspace Extension Host。标准 Remote
Host 提供 `vscode-remote:` URI 时保留并校验 authority；若 Host 将 workspace URI
转换为远端本机 `file:`，扩展用 `remoteName + remote hostname digest` 恢复稳定且跨主机
隔离的 editor authority。两种形态都要求 Host-local `file:` storage、绝对 Runtime
path 和与 `process.platform/process.arch` 一致的 binary target，扩展不从 UI 端自行
执行 SSH。目标 VS Code 的官方 Remote SSH 与 Dev Containers gate 会在 Linux arm64
Workspace Host 内运行同一 native flow，并断言 Extension Host PID 与 Runtime PID
属于远端或容器环境。WSL2 不属于当前 gate。

multi-root 由 Extension Host 的 `WorkspaceRuntimeRegistry` 编排，不扩大单 Runtime
sandbox。每个 canonical root ID 独占 controller、ContextBridge、Supervisor、
session/thread/cursor binding、event/projector namespace 和
`storageUri/runtime/<root_id>` data directory。Chat 仅通过显式 selector 改 root；
selection/Code Action 按文档所属 root 路由；Changes 与后台 Tree 以 root 分组。
request/turn/plan ID 只能在 root namespace 内解释，diff 虚拟 URI 也同时包含 root 与
plan identity。Registry 最多管理 8 个 root，folder remove/re-add 会等待旧 Runtime
停止后再复用 durable state。

V3 E2E 证据使用 schema v1 JSON，一 job 一文件，再由 aggregate report 对 required
job 做 fail-closed 汇总。当前动态矩阵覆盖 darwin-arm64、Rosetta darwin-x64、
Remote SSH Linux arm64，以及 Dev Container Linux arm64/amd64；source 取自 Runtime
host snapshot，不从配置推断。Remote SSH 通过终止 Workspace Extension Host 验证
durable session reattach；Container gate 验证 fresh attach 与 Runtime restart/replay。
Windows x64 只有 PE/VSIX/manifest 静态分发审计，不标为动态 E2E。

Editor Host 启动 ACP 时增加 `--edit-plan-approvals`。该开关在 Guard 层把可规划
workspace writer 强制收紧为逐次 ask，即使 repository rule 或旧 approval cache 原本
允许写入也不能绕过。file transaction 先在内存 compose `tool.EditPlan`，协议把
plan/diff/before/after 交给扩展的只读 virtual document；`approval.decision.plan_id`
返回后 Runtime 重新 compose 并比较 identity，再走原 read-before-edit、journal、
expected fingerprint 与 atomic commit。扩展只展示，不拥有 apply 权限。

V2 将 virtual document provider 提升为 extension 级 `EditPlanPreview`，Chat 与
`CodeHelper Changes` 共用。Changes projector 只从 durable approval/diagnostics/
verification event 恢复有界 plan 文件模型；选择文件按 plan ID + index 打开单个
`vscode.diff`。Tree approve/deny 不信任投影自身，必须回到 Chat 当前 pending approval
重新校验 request/turn/item/plan/expiry/scope 后调用同一 `SessionCommands`。因此 replay
可以恢复只读审阅状态，但不能恢复执行权限，也不会自动打开 diff 或重复 modal。

后台观察面由 `BackgroundQuery`、`BackgroundProjector` 和六个原生 Tree View
组成。query 读取 Runtime workspace-scoped read model；projector 合并 durable
replay、live event 与 query snapshot，并按 terminal identity 去重通知。Tree View
没有固定 poll timer，只在 visibility、Runtime ready 或相关 event 上做 50 ms 合并
刷新。Jobs 复用 durable task source，仅筛选具有 executor 的可执行项。

Agent graph schema v10 以 `(workspace_root, child_agent_id)` 为主键并保存创建
session；旧无身份行迁移到空 legacy scope。ACP 将带匹配 `workspace_root` 的
`agent.*` 作为 workspace-visible event 广播并纳入 session replay，因此 graph 专用
thread 不再被误判为未绑定事件；其他 workspace 和普通 session/thread event 仍隔离。

扩展发布门禁把进程内与真实 Host 分开测量：Node suite 负责协议、投影、安全和
10k event 预算；真 CodeHelper binary suite 负责 stdio 生命周期与写入闭环；固定
VS Code 1.96.4 的 Electron suite 负责 activation、commands/views 和无 workspace
不启动；VSIX gate 审计归档内容后用同一 VS Code CLI 实际安装。发布物只含 Extension
Host bundle、manifest、图标和文档，不内嵌 Runtime binary、源码、source map 或依赖树。

---

## 10. 配置优先级

```text
Defaults()  <  TOML (--config)  <  CODEHELPER_* 环境变量  <  CLI overrides
```

主要 TOML 段：`runtime`、`state`、`memory`、`telemetry`、`credential`、`execution`、`vision`、`web`。

未知字段：`DisallowUnknownFields`（fail-closed）。

凭证：`credential.kind` = `env|file|keyring`，`credential.name` 为引用名，**禁止**明文密钥。

细节与示例见 [USAGE.zh-CN.md](./USAGE.zh-CN.md)。

---

## 11. 构建与质量门禁

| 目标 | 作用 |
| --- | --- |
| `make verify` | gofmt + brand + vet + `go test` + 串行 `go test -race` |
| `make build` / `cross-build` | 产物 / 多平台编译检查 |
| `make security-test` / `sandbox-attack-test` / `secret-leak-test` | 安全回归 |
| `make protocol-contract` | 同一份场景表跑 ACP 与 HTTP 两个 Host |
| `make protocol-schema` | 重新生成协议 JSON Schema（漂移由测试守住） |
| `make acp-interop` / `api-contract` | 用发布二进制跑真 stdio / 真 HTTP |
| `make vscode-security` / `vscode-performance` | Webview/Trust 静态安全与事件/Runtime ready 预算 |
| `make vscode-runtime-integration` | 真实 binary 的 ACP 生命周期、恢复、审批和编辑闭环 |
| `make vscode-integration` | 固定 VS Code 1.96.4 Electron Extension Host |
| `make vscode-package` | VSIX 内容审计与实际安装验证 |
| `make package` | `scripts/package-release.sh` 多平台打包 |

品牌检查：`scripts/check-brand.sh` 扫描 `.go` / Makefile / `.sh`。

---

## 12. 扩展指南（给贡献者）

1. **新 Host**：只依赖 `protocol` + `app.Runtime`（或 facade）；禁止直连 Engine/tool。
2. **新工具**：在 `adapter/tool/<name>` 实现 → 注册表登记 → 必走 Guard；补资源声明。
3. **新 Provider**：实现流式解析，对齐 `adapter/provider` 事件模型；加 fixture 测试。
4. **新编排能力**：落在 `orchestration/*`，通过 CLI 或 tool 暴露，不把状态机塞进 TUI。
5. **改安全语义**：同时更新本文第 5 节与相关测试；勿把四层职责揉进单一包。

---

## 13. 关键路径索引

| 主题 | 路径 |
| --- | --- |
| 进程入口 | `cmd/codehelper/main.go` |
| CLI 根 | `internal/host/cli/cobra.go` |
| Wire 装配 | `internal/runtime/app/wire/` |
| Runtime | `internal/runtime/app/` |
| Agent | `internal/runtime/agent/engine/` |
| Protocol | `internal/runtime/protocol/` |
| Config | `internal/config/config.go` |
| Model 目录 | `internal/adapter/model/catalog.v1.json` |
| Tool Guard | `internal/adapter/tool/guard/` |
| Verify Gate | `internal/runtime/agent/engine/verify.go`、`internal/observability/verify/` |
| 共享工作区串行 | `internal/runtime/agent/engine.WorkspaceTurnGate`、`internal/runtime/app/wire/childruntime.go`、`internal/runtime/app/wire/childworktree.go` |
| 编辑事务 | `internal/adapter/tool/file/apply.go`、`internal/persist/workspacejournal/`、`internal/platform/textdiff/` |
| 仓库索引 | `internal/platform/repowalk/`、`internal/platform/symbols/`、`internal/persist/repoindex/`、`internal/adapter/tool/search/symbol.go` |
| 上下文装配 | `internal/runtime/agent/promptcontext/`（`context.go` 稳定前缀 / `turn.go` 易变尾块 / `codingpolicy.go` 静态方法）、`internal/runtime/agent/repomap/`、`internal/runtime/agent/workingset/`、`internal/runtime/agent/evidence/`、`internal/runtime/agent/repocontext/` |
| 结构化 compact | `internal/runtime/agent/compact/`（`compact.go` 摘要与渲染 / `failures.go` 跨 turn 失败账本）、`internal/runtime/agent/engine/compaction.go` |
| Sandbox | `internal/security/sandbox/` |
| 后台执行 | `internal/orchestration/worker/`、`internal/orchestration/task/execution.go`、`internal/runtime/app/wire/agentexecutor.go`、`internal/runtime/app/wire/background_executors.go`、`internal/host/cli/worker_cmd.go` |
| 协议契约 | `internal/host/runtimeapi/contract/`、`internal/runtime/protocol/schema.go`、`docs/protocol/runtime-protocol.schema.json` |
