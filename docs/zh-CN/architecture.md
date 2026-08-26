# 架构与安全设计

## 架构目标

CodeHelper 保持一个本机 Web Supervisor，并为每个已注册 Workspace 构造一个权威执行
Runtime。Host 按 Workspace 路由 Operation 并观察 Event，不复制 Agent 循环，也不
直接执行特权工具。

受支持的产品 Host 只有本机 Web。它只绑定
`127.0.0.1`，使用同源 HTTP RPC 与下行 WebSocket；项目不提供 LAN、公网部署、
Pairing/QR Flow、通用 REST/SSE Host 或 MCP Server 入口。外部 MCP 只作为受治理的
Tool Source 接入。

```text
Web
                 |
          Web Supervisor
         /       |        \
 Runtime A   Runtime B   Runtime C
     |           |           |
 Operation / Event + Agent Engine
     |           |           |
 Context + Provider + Guarded Tool
                 |
       Policy -> Approval -> Journal -> Sandbox
                 |
       Shared Persistence + Observability
```

## 包分层

| 层 | 路径 | 职责 |
| --- | --- | --- |
| 入口 | `cmd/codehelper` | 进程上下文和 Web 启动入口 |
| Host | `internal/host` | 用户/客户端 I/O 与呈现 |
| Runtime | `internal/runtime` | 协议、应用状态、Agent 循环、装配 |
| Adapter | `internal/adapter` | 模型、Provider、Tool、MCP、Skill、Plugin、Hook |
| Security | `internal/security` | Policy、Permission、Constitution、Sandbox |
| Orchestration | `internal/orchestration` | Task、Worker、Automation、Workflow、Lane、Fleet |
| Persistence | `internal/persist` | 关系状态、Event、CAS、Session、Snapshot、Journal |
| Observability | `internal/observability` | 版本化 Observation、Usage、Trace、Diagnostics、Verify、Telemetry |
| Platform | `internal/platform` | 进程、PTY、操作系统差异 |
| Configuration | `internal/config` | 默认值、TOML、环境变量、校验、Provenance |

## 硬依赖规则

1. `runtime/protocol` 不依赖其他实现包。
2. Host 不直接 Import 并调用 Provider、Tool、Sandbox 或 Agent Engine 实现。
3. Model/Tool/Security 的构造属于 `runtime/app/wire`。
4. Turn 业务循环属于 `runtime/agent`。
5. 所有有副作用工具都经过 `adapter/tool/guard`。
6. UI State 是 Projection，不是 Runtime 事实来源。
7. 持久化写入在所属边界内使用事务或 Journal。

Architecture Test 会检查重要 Import 限制。需要违反这些规则的设计必须先进行显式架构
调整，不能用局部捷径绕过。

## Runtime 组合根

`runtime/app/wire.NewExec` 是装配入口，不是 Service Locator 或业务 Workflow。它创建
仅用于构造期的 `buildState`，并执行封闭的 Module 序列：

```text
config -> provider -> persistence -> platform -> builtin tools
       -> extension contributors -> security -> extension plan
       -> orchestration -> observability -> agent -> runtime
       -> background services
```

每个 Module 只拥有一个构造边界，并仅向后续 Module 暴露必要结果。Runtime、Engine 和
Session Service 都不得持有 `buildState`。Persistence 拥有 Content、Job Log 和
SQLite 基础；Platform 拥有 Process、Sandbox 与 Repository Index；Orchestration
拥有 Task/Automation Repository、Workflow Executor、Scheduler 构造、Subagent 与
Child Worktree/Toolset。Provider 显式输出 Provider/Model Catalog，Security 显式
输出 Permission Store 与 Guard Factory。

Builtin 与 Extension Tool 共享同一个 Registry 实例。Plugin、Skill、Memory、
Dynamic Tool、Hook 与 MCP Contributor 只接收其显式构造能力和共享 Registry，不接收
`buildState`；每个 Contributor 返回确定性的 `ContributionReceipt`，记录新增 Tool
Identity 与命名输出。Task/Automation 注册归 Orchestration，而非 Extension
Contributor Chain。

Runtime 构造具有 Prepared 状态。`RuntimeModule` 只构造 Facade 并恢复静态 Durable
State，不接受 Operation；`BackgroundModule` 依次执行 MCP 初次 Refresh、启动 Runtime
的 Terminal Outbox/Pending Turn Recovery、启动 MCP Prewarm、协调 Automation，最后
启动 Worker Scheduler。任一步失败都会终止构造并由 ResourceStack 回滚；Runtime
Recovery 成功前不会启动后台 Worker。

当 Web 启动时没有显式或已保存的 Provider/Model，Host 先进入受限 Setup 状态，不构造
默认 Runtime。该状态只暴露受同源 Capability Token 保护的 `setup/apply`；用户提交的
API Key 写入操作系统 Keyring，Provider、Model、Endpoint 与协议写入 Runtime 管理的
非敏感 Setup Record。只有这些事实持久化成功后，Host 才调用 `wire.NewExec` 并
`Activate` 完整 Web Runtime。

Web 进程持有一个全局 Owner Lease 和持久化 Workspace Registry。首次启动创建
Supervisor；其他目录再次执行 `codehelper` 时，通过 Lease 中仅对当前用户可读的
Capability Token 调用已有 Host 的 `workspace/add`，不会启动第二套控制面。每个
Workspace 单独拥有 `wire.Session`、Sandbox、Tool Registry、Repository Index、
Extension 生命周期和后台 Scheduler。共享 SQLite 中的 Session、Event Recovery、
Terminal Outbox 与 WorkGraph Outbox 按规范化 Workspace Root 过滤；关闭一个 Runtime
不能关闭 Supervisor 持有的共享 Store。

构造与关闭共享 `wire.ResourceStack`。Session 只注册一次资源关闭函数；部分构造
失败回滚与正常关闭都按注册逆序关闭同一 Stack。每项资源最多关闭一次，单项关闭失败
不会跳过后续资源，调用方会收到带资源标识的聚合错误。因此 Runtime 或 Scheduler 等
后段构造失败也不会泄漏已创建资源。

## Runtime 所有权图

```text
Web
        | Operation / Event
        v
OperationService -> TurnService -> TurnCoordinator -> TurnScope
        |                |               |
        v                v               v
 operationDispatcher  ActiveTurnRegistry  Context Authority Snapshot
        |
        v
    EventService -> eventhub.Hub <-------- Event Projection
        |
        +-> TerminalPublisher -> app/persistence -> SQLite / Event Log / CAS
        |
        +-> SessionService / ArtifactService -> Host Query

wire.NewExec -> 仅负责构造 Module
orchestration/chatmerge.Service -> 隔离 Chat Preview / Journal Apply / Git Baseline
eventview + Web Projection -> 仅负责 Host Presentation
```

| Owner | 路径 | 独占职责 |
| --- | --- | --- |
| Composition Root | `internal/runtime/app/wire` | Concrete Construction 与 Resource Registration |
| Durable Runtime Assembly | `internal/runtime/app/persistence` | Repository、Lifecycle Recovery、Persistent Runtime Options |
| Chat Merge Service | `internal/orchestration/chatmerge` | Isolated Baseline、Three-way Preview、Journaled Apply |
| Operation Service | `internal/runtime/app` | Queue、Idempotency、Typed Dispatch 与 Operation Commit/Reject |
| Turn Service | `internal/runtime/app` | Active Lease、Control、Cancel Provenance 与 Turn goroutine 生命周期 |
| Event/Recovery Service | `internal/runtime/app` | Event Projection 索引、Observer 与 Durable Recovery |
| Turn Coordinator/Scope | `internal/runtime/agent` | Reducer Authority、Effect、Control 与 Turn-local State |
| Event Hub/Terminal Publisher | `internal/runtime/app/eventhub`、`internal/runtime/app` | Sequence/Fanout 与 Atomic Terminal Publication |
| WorkGraph Kernel/Store | `internal/orchestration/kernel`、`internal/orchestration/store` | Durable Run、Node、Attempt、Lease 与 Effect Transition |
| Extension Runtime | `internal/runtime/extension`、`internal/runtime/app/extension` | Typed Contributor、Source Plan、Generation、Lifecycle Effect 与 Control Receipt |
| Observation Plane | `internal/observability/observation`、`internal/observability/router` | Evidence Schema、Privacy Admission、Durable Routing 与 Exporter Isolation |
| Session/Artifact/Trace Service | `internal/runtime/app` | Runtime-owned Port 上的 Host-facing Query 行为 |
| Agent Preset Service | `internal/runtime/app`、`internal/persist/agentpreset` | Workspace 范围的版本化 Preset 校验、原子持久化与 Session 应用 |
| Benchmark Projection | `internal/runtime/eventview` | Go Benchmark 的 Typed Event Interpretation |
| Web Projection | `web/src` | 浏览器端 Event Projection 与交互状态 |

Web 直接调用 Runtime 的窄化 Session、Operation、History 与 Artifact Service。
浏览器 Transport 不复制 Agent 循环，也不存在第二条兼容执行路径。

`Runtime` 是这些 Service 的兼容 Facade，不直接拥有 Operation Map 或 Active Turn Map。
Session 生命周期写操作由 `SessionService` 的 mutation lock 串行化。`Engine.Execute`
是唯一生产 Turn 入口；它冻结 `TurnRequest` 和 Context Authority，并为每个 Turn
创建隔离 Scope。`turnkernel.Reducer.Apply` 是唯一状态转换入口，其实现按 Command
Family 分布在 `reducer_sampling.go`、`reducer_tool.go`、
`reducer_interaction.go`、`reducer_context.go`、
`reducer_verification.go` 和 `reducer_terminal.go`。

## Runtime 协议

协议定义位于 `internal/runtime/protocol`，生成后的公开 Schema 位于
`docs/protocol/runtime-protocol.schema.json`。

概念模型：

- **Operation**：请求的状态转换，例如开始或取消 Turn。
- **Event**：状态转换产生的不可变事实。
- **Receipt**：上下文、工具、变更、审批、验证或成本的结构化证据。
- **Projection**：由 Event 和关系记录重建的查询状态。

Web Transport 只负责鉴权、序列化和事件投影。除非是有意的 Host 呈现差异，只在一种
Host 中存在的 Runtime 能力都不完整。

Event 分类是 Protocol 数据，而不是 Host Policy。`event_traits.json` 是唯一生成源，
生成 Go Trait Table、Protocol Schema、TypeScript Table 与 Golden；新增 Event 缺少
Class、Item Owner、Durability、Correlation 或 Terminal Trait 时生成直接失败。
Go Benchmark 消费 `eventview` 的 Typed Semantic Update，不再分类 `Event.Data`。

Web Client 使用 Runtime Snapshot 完成 Hydration，再按当前 Workspace 的 Cursor 消费
Event。持久层 Sequence 在 Supervisor 内全局严格单调，浏览器则按 Workspace 分别保存
Cursor；只有对应 Runtime 明确报告 Retention Gap 时才进入 Desync。
浏览器 Conversation Projection 对高频 Delta 按动画帧合并发布，并保持未变化业务节点
的引用稳定。Trajectory Event Ledger 与 Chat 复用该事件窗口；`trace/query` 只补充
经过 Session/Turn 归属校验和字段白名单投影的时序，不返回任意 Span Attribute。

Workspace Runtime 固定 Provider Connection、Endpoint、Credential Reference 与 Egress
边界，Session Profile 则独立持久化 Model ID。Engine 只允许在同一 Provider
Connection 内从当前 Ready Route 派生新的 Model Route，并在 Turn 开始时冻结到
`TurnSpec`；它不能借模型切换改变 Endpoint 或 Credential。Web Model Catalog 将内置
目录与当前 Workspace 已持久化 Session Profile 中的模型合并，因此用户输入的新 Model
ID 在刷新、重启和其他 Session 中仍可选择。Active Turn 期间拒绝 Profile 修改。

### Application Ownership

Application Runtime 是显式 Owner 组成的 Facade：

- `operationDispatcher` 将 8 类 Operation Payload 映射到强类型 Handler。Handler
  返回包含 Events、Async Turn Identity、Typed Problem 与显式 Commit Mode 的
  Outcome；只有 Dispatcher 执行同步 Commit 与 Rejection。
- `ActiveTurnRegistry` 原子预留 Thread 与 Turn，绑定 Control、Cancel Provenance、
  Profile Revision，并通过内存 Token 拒绝 Stale Release；持久 Lease ID 直接使用已
  持久化的 Start Operation ID。Pending-work Phase 来自权威 Turn Kernel Snapshot。
- `eventhub.Hub` 独占 Sequence 分配、Append、Replay、Subscriber Fanout、Slow
  Consumer Policy 与 Close。
- `TerminalPublisher` 独占 Atomic Terminal Commit、Deterministic Outbox Publish
  Identity、Event Hub Projection 与 Restart Recovery。
- `SessionService` 拥有 Lifecycle、Profile 与 Tool Catalog；`ArtifactService` 拥有
  Checkpoint、Plan、Turn Recovery 与 Persistence。Runtime 直接暴露窄化的 Host
  Query Method，不再保留平行的 Interface-only Package。

## Turn 数据流

执行前，Engine 构造不可变 `TurnSpec`，冻结 Identity、Request、Session Profile、
Route、Policy、Limit、Prompt Prefix、Tool Catalog、Skill、MCP Health 与 Extension
Snapshot。Engine 内的 Scope Factory 从该 Spec 打开单 Turn `engine.Scope`；Scope 运行期间 Sampling
不得重新读取这些可变来源。

Scope 独占 Turn 级 Kernel、Trace、Diagnostics、Verification、Tool Spend、Diff 与
Control State。Cancel、Steer、Approval、Input 统一进入 `ControlPort`；有界 Mailbox
拒绝溢出，Request Ledger 拒绝 Late、Duplicate 与 Kind Mismatch Resolution。

1. Host 校验用户输入并提交 Operation。
2. Application 解析 Session、Thread、Workspace 和 Policy。
3. Prompt Context 组装 Repo Map、Pin 文件、Working Set、Evidence、Policy 与压缩历史。
4. Coordinator 请求 Provider Sample Effect；`DurableEffectDispatcher` 在 Engine
   调用 Provider 前持久化 `EffectStarted`。
5. 模型 Text、Usage 与 Tool Proposal 通过 `ModelSampleResultReceived` 一次返回。
6. Reducer 持久化 Sample Result，并在 Executor 投影前将 Tool Proposal 转换为 Tool Effect。
7. Tool Executor 进入 Registry 和 Guard；Guard 评估 Mode、Posture、Permission、
   Constitution、Approval 与 Sandbox。
8. Tool、Approval、Input Result 以一个可保留重试的 Result Command 返回；Coordinator
   在 Host Projection 前持久化逻辑闭合。
9. 修改型工具通过 Journal/事务 Adapter 写入。
10. 交互式主 Turn 必须选择结构化状态：`request_user_input` 创建可持久化的 Input
    Wait，Tool Call 继续同一个 Turn，被接受的 `turn_complete` 才结束 Turn。
    Provider `message_stop` 只结束一次 Sample；普通模型正文保持 Provisional，不能完成
    Turn。对于 `status=complete`，声明中的 `summary` 是精确的用户可见 Final Output，
    Runtime 无需额外 Model Sample 即可发布。Convergence Finalization 也可以使用
    `output_mode=preserve_provisional` 保留已捕获正文并追加简短收尾。Runtime 不根据
    正文措辞推断必需输入或完成状态。Child Executor 没有 Input Host，不能等待用户
    输入，但仍必须通过 Tool Call 继续或通过 `turn_complete` 完成。
11. `EvaluateTurnStep` 由 Reducer 选择 Repair、Verification、Finalize、Block 或
    Complete。Repair、连续 No-progress 与显式普通 Work Step Limit 只会请求类型化
    Kernel Convergence，不会由 Engine 或 Provider 局部循环直接决定终态错误。
    Provider 输出不完整时没有默认续写次数上限，只要 Context 与显式 Budget 允许就继续。
    Kernel 允许一次只保留 Terminal/Input 能力的 Finalization Sample。Complete 进入
    正常 Commit；Incomplete 记录用于恢复的摘要与 Pending Actions。
12. 跨层故障统一使用协议 `Fault` 契约：Error Code、Origin、Disposition、
    Retryability、Side-effect State 和 Recovery Action。未分类的边界错误默认是
    `unavailable/resume_turn`；只有显式不变量故障才能以
    `internal/fail_turn` 终止。
13. Journal Commit、Suspend 和 Rollback 是幂等 Durable Effect。持久化失败时
    Effect 保持 Requested，Turn 保持 `committing`；Runtime 拒绝当前 Operation，
    但不会伪造 Failed Turn Terminal。恢复流程重试同一个 Effect。
14. 业务 Terminal Decision 在 Turn 后 Context 维护之前冻结。Compaction、
    Session Delta 应用和非控制事件投影失败只能成为 Secondary Issue 或可重放
    Outbox 工作，不能把已完成 Turn 改写为失败。
15. Verification Executor 通过 `VerificationFinished` 返回证据；Reducer 选择 Passed、
    Repair、Reported、Blocked、Failed 或 Reverted，并独占 Repair Budget。
16. Engine 提交 `TerminalRequested`；Reducer 选择 Completed、Failed 或 Canceled。
    随后 Journal Commit/Suspend/Rollback 作为 Durable Effect 执行，并返回
    `JournalResultReceived`。Suspend 会为结构化绑定的 Continue Turn 保留
    Verification-blocked 或 Convergence-blocked 修改。
17. Scope 准备带 Revision 与 Digest 的 `SessionDelta`，包含 History、Usage、Cost、
    Working Set、Evidence、Failures 与 Compaction State。
18. Runtime 为 Usage 与 Latency 冻结同一份带 Digest 的
    `TerminalMeasurementSnapshot`。Receipt、由 Measurement 投影的 Trace 与 Terminal
    Envelope 都引用该 Snapshot，不会再次读取可变 Counter。
19. Persistent Runtime 在同一 SQLite 事务原子提交 Frozen State、Measurement、
    Session Delta、Final Output、Receipt、Terminal Event、Outbox 与真实 Operation
    Receipt。
20. Engine 只在该 Commit 成功后幂等 Apply Session Delta；Commit 失败不修改 Session
    内存。
21. 重启时 Runtime 扫描 Pending Terminal Projection，以稳定 Event ID 逐条 Append，
    成功后再将对应 Entry 标记为 Published。
22. accepted StartTurn 仅在存在对应非终态 Domain Fact 时自动恢复；Coordinator requeue
    Running Effect，Engine 从 Durable Payload 接续 Provider、Tool 或 Journal 执行。
23. Approval/Input 恢复在接续执行前预装原 Request ID，Host 只回放一个 Wait，不会收到
    替代请求。

Engine 始终提交完整逻辑模型请求。只有模型显式广告能力、请求属性不变且输入严格扩展
已提交 Response Chain 时，Provider Adapter 才能将该请求投影为 Incremental
Responses WebSocket。Response ID 与连接状态不会进入 Host 或 Runtime Authority；
Reset、Retry、Compaction、Resume 或任意不确定状态都会回退完整请求。Usage 分别保留
Logical/Transport Digest 与序列化 Request Bytes，传输收益不会被报告为 Token 收益。

每条 Route 都携带显式 `AdapterID`，不可变 Provider Router 是生产环境唯一采样路径。
Composition Root 安装专用 OpenAI、DeepSeek、Anthropic Adapter，以及一个参数化的
OpenAI-compatible Adapter。DeepSeek 不广告 Incremental Responses，因此其 Chat 与
Responses Route 始终使用完整 HTTP/SSE 请求，不发送 `previous_response_id`。

Token Window Gate 在 Summary Replacement 前，按从旧到新的确定顺序缩减已闭合 Tool
Result 的 Model-visible Surface。Tool 层把完整原文保存在 Durable Content Store，
并返回稳定的 `result_get` Handle 与有界 Head/Tail 摘要；Call/Result 配对保持不变。
Engine 在每次投影后重新测量，若 Surface Pruning 已恢复窗口，则跳过 Summary
Replacement。

完整传输 Route 还使用独立的滚动 Surface Budget：最新 Tool Batch 保持完整，已经被
后续 Sample 消费的旧结果缩减为稳定 Handle 投影，不必等待全局 Compaction。累计和
单项保留值是可配置的模型可见容量边界，不是 Turn 执行预算；完整原文仍在 Content
Store。Provider 请求只物化每个 World Section 的最新版本，并移除已闭合 Tool Round
中的旧状态文本和 Reasoning；Durable History、World Patch 链和原始 Tool Result
不被改写。
增量 Route 保持严格追加投影，不执行这些会破坏 Response Chain 前缀的转换。

`TurnCoordinator` 是生产环境唯一 `Reducer.Apply` 入口。Engine Event 只用于投影，
不会反向生成 Command 写回状态机。Durable Runtime 构造必须显式提供 Event、Content、
Terminal Store；Memory Store 仅由显式 `NewRuntime` Ephemeral 构造选择。

Cancel 和 Failure 是明确终态，不是“没有返回数据”。

## 持久化

Durable State 由多个明确组件组合：

| 组件 | 用途 |
| --- | --- |
| SQLite | Session、Turn、Task、Usage、Trace、Workflow 关系 Projection |
| Event Log | 有序 Runtime 事实 |
| CAS | 不可变内容寻址 Payload |
| Session Metadata | 面向用户的 Session/Thread 组织 |
| Workspace Journal | Before Image 与编辑恢复 |
| Snapshot | 显式 Thread 状态检查点 |

SQLite 当前是初始 Schema。未来公开版本变更必须使用显式 Migration；首次基线前的开发
迁移历史已经有意压缩。

Persistent Runtime Wiring 在创建 Engine 前注入 SQLite Turn Coordinator Store。每个
已接受 Transition 都在 State Commit 或 Effect Dispatch 前追加 Domain Fact。启动恢复
使用可续租的 Active Turn Lease；无效或重复恢复 Fail Closed。

Session Checkpoint 与 Plan Artifact 复用 Snapshot Index 和 CAS。Checkpoint 只保存
经过校验的 Context Manifest 与 Profile Snapshot；Manifest 将 History 拆为 Base/Tail，
并将 Working Set、Evidence、Failures 和 Plan 拆为有界 Owner Segment。Restore 不能
执行历史 Event，也不回退独立的 Usage/Cost Accounting。Checkpoint Restore/Fork 创建
新的 State Epoch 和 Token Window，并用 Sparse Workspace Binding 重新核对文件相关
Evidence；不匹配的验证声明会失效为 stale。持久化 Restore/Fork Event 保证重启重建
结果确定；事件显式引用已提交的 Context Commit ID、Digest、Revision 和 Epoch。
Workspace 对账同时重写 History 中已压缩的 Truth Capsule，不能让旧
`verified/current` 声明继续进入下一次采样。Fork 血缘、子 Thread Context 基线与当前
Active Session Thread 属于关系型 Lifecycle State，而不是 Host-local State。

Plan Mode 的 Workspace 只读性由 Policy Effect 强制：普通 Write/Process/Network
继续拒绝，只有 Resource 为 Session Plan 的低风险状态更新可通过。`submit_plan`
生成版本化 JSON Artifact，并在 Artifact Body 内记录 Revision、Supersedes Identity、
步骤依赖、验证证据与文件摘要。批准操作作为已持久化的 `turn.start` Payload 接受，
Runtime 在 Dispatch 时重新校验 Session/Thread/Profile 和文件摘要，再将执行 Prompt
及 Turn-scoped Act/Autopilot Policy 注入 Engine。Host 不先修改 Profile，因此不存在
“Profile 已切换但 Turn 未接受”的两阶段窗口；重启恢复仍重放同一 Accepted Operation。

Act Mode 额外接受 Session Profile 中的 `planning_policy=off|adaptive|required` 与
`plan_approval=manual|auto`。Guard 在 Capability、Resource、Effect 和 Risk 已规范化
后执行 Planning Gate：`required` 拦截全部有后果的 Effect，`adaptive` 至少拦截高风险、
网络写、外部写、Agent Lifecycle 和同次调用中的多文件写。成功的 `submit_plan` Tool
Result 才能推进 Turn-scoped `submitted/approved` 状态；文本声明不能解锁工具。`auto`
只解锁当前 Turn，`manual` 必须通过已有的 Durable Plan Execution 启动批准后的新 Turn。
每个 Turn 结束时状态归零。

Turn 的 Model Route 继续在 Scope 创建时冻结。独立 Plan Mode 选择 `PurposePlan`；
Act 内规划选择 `PurposeAct`，因此 Auto 流程可以在同一 Turn 中从规划继续执行，而不会
发生中途换模型或重建 Context 的隐式状态变化。

Workspace Git 状态由 `internal/platform/workspacequery` 在已绑定沙箱中查询。Web Host
只路由显式、带幂等键的分支切换请求；服务只接受已存在的本地分支，并在 Runtime 有活动
Turn 或待处理 Operation 时拒绝切换。Session 列表聚合同时保存
`session_id -> workspace_id` 来源映射，所有 Session 请求固定使用 Owner Workspace，
不从切换中的 UI 状态临时推断。

Provider Replay State 同时绑定 Adapter、Provider 和 Model。Router 在目标 Route 改变时
清除不兼容的原生 Replay，仅保留可见 Assistant 内容，避免同 Adapter 跨模型切换后因
Provenance 不匹配导致下一 Turn 失败。

Terminal Envelope 不再重复写入完整 Session Snapshot，而是引用 CAS 中的 Context
Manifest。CAS 先按 Digest 幂等 Stage，SQLite 再提交 Manifest 可达性和 Terminal
事实。Inline Narrative 使用 `generate_narrative` 与 `commit_context_rebase` Durable
Effect；Rebase 由 `runtime/app/persistence` 单点提交，提交成功后 Engine 才替换 History。

## Observation 架构

Runtime Event 继续作为 Host Protocol。Observation Plane 是独立且不具执行权威的证据
系统，用于因果诊断、Telemetry Export 与 Retention。每条获准记录都会成为版本化
`ObservationEnvelope`，包含稳定 Observation ID、有序 Sequence、Runtime/Domain
Identity、可选 W3C Trace Context、Causality Link、Data Policy、有界 Summary，以及
可选 CAS Payload Reference。

`observation_traits.json` 是每种 Observation Kind 的 Owner、Durability、Payload
Policy、Retention Class、必需 Correlation、OpenTelemetry Mapping 与 Queue Priority
的事实来源，并生成 Go Trait Table、TypeScript Table 与
`docs/protocol/observation.schema.json`。

Router 会在任何 Journal 或 CAS 写入前应用 Privacy Policy。Critical Evidence 在脱离
业务 Cancellation 的同步路径持久化；Normal 与 Bulk Record 进入有界 Queue。Queue
Pressure、Privacy Error 和 Payload Drop 由 Admission Receipt 显式返回，Journal 或
Exporter Failure 由 `Flush`/`Shutdown` 返回；这些故障绝不改写业务 Turn 的成功或
失败结果。Observation Plane 不获得执行权威。

Capture 由 `CODEHELPER_OBSERVATION_CAPTURE` 控制：

- `off`：关闭 Observation Admission；
- `metadata`：默认值，只保留脱敏 Summary，不保留 Raw Payload；
- `failure`：仅为 Failure-like Observation 保留可接受的脱敏 Payload；
- `full`：为符合策略的 Observation 保留脱敏 Payload。

Credential 与 Restricted Payload 永不持久化。配置的 Secret Value 以及 State/Config
Root 会在写入前脱敏。Payload 复用 Content Store，并按 Retention Class 管理：Audit
与 Diagnostic 默认保留 30 天，Sensitive 保留 24 小时，Ephemeral 保留 1 小时。启动
清理会释放过期 Reference，只删除已无引用的 CAS Object；Observation Metadata 仍可
用于解释。

W3C Trace Context 会跨 Provider HTTP、MCP HTTP/stdio、Process、Workflow 与
Subagent 传播。OTLP Projector 默认关闭，可通过环境变量选择 In-memory、HTTP/protobuf 和
gRPC Exporter。Metric Label 来自固定的低基数 Allowlist，不能包含 Path、Prompt、
Tool Argument 或 Resource ID。故障分析直接按 Cursor 重放 Raw Journal，并与 Runtime
Event、Trace、Usage、Receipt 和 Workspace Journal 交叉核对。

## 上下文架构

上下文按稳定性和用途拆分：

- 稳定 Coding Policy 与系统约束；
- Repo Map 与 Symbol Index；
- 用户 Pin 文件；
- 持续演进的 Working Set；
- Evidence 与未解决风险；
- 最近 Event History 或结构化 Compact Summary。

上限是正确性的一部分。无界上下文最终会变贵、变慢并降低一致性。

长期 Session 的模型可见 Context 分为三层：

1. Runtime 生成、经过 Retention 和 Admission 的 Truth Capsule；
2. 可选、非权威、带 Source Message Fence 的 Semantic Narrative；
3. 保持原始 Role/Block 和 Tool Pair 完整性的 Recent Raw Tail。

Authority Digest 只覆盖 Mandatory Truth。Protected 与 Refreshable Entity 按稳定优先级
和类型配额保留，淘汰通过聚合 Omission 解释。Narrative 只能走 `summary` Route，禁用
Tool 和 Native Search；Provider、解析、超时或 Staleness 失败都回退到 Truth + Tail，
不能改写业务 Turn 终态。

Execution Receipt 会逐条解释入选的 Working Set 文件或测试，包括选择来源、支撑
Evidence、相关性分数和单条预算结果。`included=false` 加截断原因表示 Selector 选择了
该路径，但渲染后的上下文预算裁掉了对应行。各 Host 投影同一份 Receipt，不自行反推
选择原因。

用户 Memory 是独立的非权威数据面。记录具有稳定 ID、Generation、来源、过期时间以及
`user`、`workspace`、`repository` Scope；Workspace 和 Repository 使用规范化身份隔离。
Turn Admission 冻结当前 Generation，按显式 Pin、精确 Scope、词法相关性、新鲜度和
稳定 ID 选择记录。Memory 写入只影响下一 Turn，且 CRUD 工具全部经过统一 Guard。

## 安全模型

安全采用分层结构，因为单一控制无法回答所有问题。

### 1. Mode 与 Posture

描述当前 Host/Session 的意图和审批行为。

### 2. Workspace Permission

记录用户对特定工作区允许的操作，必须绑定工作区，不能变成全局授权。

### 3. Constitution

普通 Session 配置不能绕过的硬约束。

### 4. Tool Guard

Tool Identity、Risk、Approval、Repository Policy 与 Edit Evidence 的统一决策点。

### 5. Edit Journal 与 Verify

授权写入后提供可恢复性和正确性证据。
Execution Receipt 会保留每次 Verification Attempt、命令推导原因、失败分类、Repair
次数、最终 Gate Action 和最终 Workspace Outcome。Rollback 会区分已恢复路径、冲突和
无法回滚的非文件副作用；原有 Pass/Fail 聚合字段只作为兼容摘要保留。

### 6. OS Sandbox

在操作系统边界限制进程、文件系统和网络。不同平台 Backend 强度不同；所需边界不可用
时，执行必须 fail closed。

## Secret 与网络边界

- Config 保存 Secret Reference，不保存 Secret Value。
- Provider 与 Web 出网使用受治理 Client 和显式 Endpoint。
- Log 与 Report 会脱敏，但仍属于敏感工程数据。
- MCP 与 Plugin 是供应链边界，不是可信文本文件。
- 服务默认监听 Loopback。
- Dynamic Tool 需要可信客户端和显式开启。

## 扩展架构

`internal/runtime/extension` 为 Thread、Turn、Context、Tool 与 MCP Capability 定义
统一 Typed Contributor Contract。注册时校验 Identity、Failure Policy、Timeout 与
Output Budget，随后 Seal 不可变 Registry。Contributor 只接收显式 Capability 并返回
有界 Receipt，不接收 Construction State 或私有 Tool Registry。

Extension Source 按 Source Priority 确定性解析为绑定当前 Permission Digest 的
Digested Plan。每个 Process、Connection、Hook、Subscription、Lease、Timer 与 Tool
Registration 都通过 `EffectOwner` 绑定 Extension、Source、Plan Revision、
Generation、Capability 和 Effect Kind。Disable 会 Drain 所属 Effect；Revoke 或
Quarantine 会 Fence 旧 Generation。Lifecycle Receipt 持久化且已脱敏。

Web Transport `extension/list`/`extension/control` 与 Web Extensions View 使用同一
Runtime Control Plane。Mutation 按 Operation ID 幂等，并持久化
Prepare/Commit Receipt；Host 只提交 Operation 与投影 Runtime-owned State。

### MCP

外部 Server 通过协议 Adapter 暴露 Tool。Health、Timeout、Circuit Breaker 和 Tool
Binding 隔离避免单个 Server 故障污染全部工具。

### Skill

Skill 打包指令和资源。Discovery、Manifest、Lock 与 Enablement State 让最终内容可见。
Turn Selection 会先保留被精确点名、Required 以及此前使用过的 Skill，再应用有界词法
候选上限。Turn 会冻结 Name-to-handle Binding；加载时重新校验 Content Digest、
Dependency Plan、Lock 与可选 Plugin Authority。`skills_read` 接受该冻结条目广告的
任一精确 Handle（Skill、Package 或 Resource），并在结果中返回规范化 Skill Handle。
真正无效或过期的 Handle 会返回结构化 `skills_list` 恢复动作，而不会直接终止 Turn。
Execution Receipt 会记录选择规模、显式命中、Token Projection、Cache 使用情况以及
Query/Candidate 截断。

### Plugin

Plugin 增加可执行能力。Registry Signature、Publisher Trust、Immutable Staging、
Receipt、Enablement、Rollback 与 Revocation 构成激活链。

### Hook

Hook 观察或拦截生命周期点，必须有界，不能成为另一条无 Guard 执行路径。

## 编排架构

Task、Workflow、Automation、Background Command、Verification 与 Agent Work 已收敛
到 Durable WorkGraph Model：

```text
Command(expected revision)
  -> pure WorkGraph Kernel
  -> Aggregate + ordered Facts + Effects
  -> one SQLite transaction
     (snapshot + facts + command receipt + effect outbox + projections)
```

- **WorkGraph Kernel**：拥有 Run、Node、Attempt、Lease Epoch 与 Effect State
  Transition，不执行 I/O。
- **WorkGraph Store**：原子提交 Transition、按 Command ID 去重，并检测
  Snapshot/Fact Drift。
- **Worker**：唯一 Claim Authority；Heartbeat 与 Settlement 受 Owner、Lease Epoch、
  Authority Digest 和 Revision Fence。
- **Automation/Workflow**：将 Schedule 或 DAG 编译为 WorkGraph Node，不维护第二套
  Checkpoint State Machine。
- **Lane**：记录 Durable Placement，并显式管理 Inline/tmux Process Adapter；
  Placement 不是 Lifecycle Authority。
- **Fleet**：投影并审计 WorkGraph State，不能 Enqueue、Claim、Settle 或 Resume；
  Repair 只能从 Ordered Fact 重建 Snapshot Cache。
- **Subagent**：运行有界 Child Runtime，并持久化 Agent Tree、Mailbox、Result、Budget
  Ledger、Worktree Ownership、Approval Routing 与 Journaled Integration。

所有编排最终仍回到 Runtime、Tool 和 Security 边界。

## 架构变更检查表

1. 确认所属层。
2. 判断是否改变协议或持久化状态。
3. 定义 Cancel、Retry 与 Terminal 行为。
4. 保持 Guard 与 Sandbox 路径。
5. 增加 Contract 或 Architecture Test。
6. 同步更新中文文档。
7. 必要时重新生成 Protocol、Observation Trait 与 Compatibility Artifact。
