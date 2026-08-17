# 架构与安全设计

简体中文 | [English](../en/architecture.md)

## 架构目标

CodeHelper 保持一个权威执行 Runtime，同时允许多种呈现和集成入口。Host 提交
Operation 并观察 Event，不复制 Agent 循环，也不直接执行特权工具。

受支持的产品 Host 是 CLI、TUI、VS Code 和 ACP。项目不提供 `codehelper web`、
`codehelper serve`、Embedded Browser UI、Pairing/QR Flow 或 REST/SSE Host。MCP
Stdio Serving 和内部 Loopback Helper 属于集成机制，不是产品 HTTP Host。

```text
CLI / TUI / VS Code / ACP
                 |
           Operation / Event
                 |
        Runtime Application State
                 |
             Agent Engine
        /          |           \
   Context      Provider     Guarded Tool
                               |
       Policy -> Approval -> Journal -> Sandbox
                 |
       Persistence + Observability
```

## 包分层

| 层 | 路径 | 职责 |
| --- | --- | --- |
| 入口 | `cmd/codehelper` | 进程上下文和 CLI 入口 |
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

构造与关闭共享 `assembly.ResourceStack`。Session 只注册一次资源关闭函数；部分构造
失败回滚与正常关闭都按注册逆序关闭同一 Stack。每项资源最多关闭一次，单项关闭失败
不会跳过后续资源，调用方会收到带资源标识的聚合错误。因此 Runtime 或 Scheduler 等
后段构造失败也不会泄漏已创建资源。

## Runtime 所有权图

```text
CLI / TUI / VS Code / ACP
        | Operation / Event
        v
operationDispatcher -> ActiveTurnRegistry -> TurnCoordinator -> TurnScope
        |                                        |
        v                                        v
    eventhub.Hub <-------------------------- Event Projection
        |
        +-> TerminalPublisher -> app/persistence -> SQLite / Event Log / CAS
        |
        +-> SessionService / ArtifactService -> Host Query

wire.NewExec -> 仅负责构造 Module
chatmerge.Service -> 隔离 Chat Preview / Journal Apply / Git Baseline
eventview + VS Code Projector -> 仅负责 Host Presentation
```

| Owner | 路径 | 独占职责 |
| --- | --- | --- |
| Composition Root | `internal/runtime/app/wire` | Concrete Construction 与 Resource Registration |
| Durable Runtime Assembly | `internal/runtime/app/persistence` | Repository、Lifecycle Recovery、Persistent Runtime Options |
| Chat Merge Service | `internal/runtime/app/chatmerge` | Isolated Baseline、Three-way Preview、Journaled Apply |
| Operation Dispatcher | `internal/runtime/app` | Typed Operation Handler Selection 与 Synchronous Commit |
| Turn Coordinator/Scope | `internal/runtime/agent` | Reducer Authority、Effect、Control 与 Turn-local State |
| Event Hub/Terminal Publisher | `internal/runtime/app/eventhub`、`internal/runtime/app` | Sequence/Fanout 与 Atomic Terminal Publication |
| WorkGraph Kernel/Store | `internal/orchestration/kernel`、`internal/orchestration/store` | Durable Run、Node、Attempt、Lease 与 Effect Transition |
| Extension Runtime | `internal/runtime/extension`、`internal/runtime/app/extension` | Typed Contributor、Source Plan、Generation、Lifecycle Effect 与 Control Receipt |
| Observation Plane | `internal/observability/observation`、`internal/observability/router` | Evidence Schema、Privacy Admission、Durable Routing 与 Exporter Isolation |
| Session/Artifact Service | `internal/runtime/app` | Runtime-owned Port 上的 Host-facing Query 行为 |
| Go Host Projection | `internal/runtime/eventview` | Event Payload 的唯一 Typed Interpretation |
| VS Code Projection | `extensions/vscode/src/chat/projector` | Exhaustive Event Class Presentation |

`codehelper host --adapter acp` 现在只有持久化路径。预发布的一次性 ACP Envelope Adapter
已删除，Host 不能再通过 `exec` 选择第二条执行路径。

## Runtime 协议

协议定义位于 `internal/runtime/protocol`，生成后的公开 Schema 位于
`docs/protocol/runtime-protocol.schema.json`。

概念模型：

- **Operation**：请求的状态转换，例如开始或取消 Turn。
- **Event**：状态转换产生的不可变事实。
- **Receipt**：上下文、工具、变更、审批、验证或成本的结构化证据。
- **Projection**：由 Event 和关系记录重建的查询状态。

ACP 是共享模型的编辑器 Transport Envelope。除非是有意的 Host 呈现差异，只在一种
Host 中存在的功能都不完整。

Event 分类是 Protocol 数据，而不是 Host Policy。`event_traits.json` 是唯一生成源，
生成 Go Trait Table、Protocol Schema、TypeScript Table 与 Golden；新增 Event 缺少
Class、Item Owner、Durability、Correlation 或 Terminal Trait 时生成直接失败。
Go TUI、CLI 与 Bench 消费 `eventview` 的 Typed Semantic Update，不再分类
`Event.Data`；Machine NDJSON 仍输出原始 Event Envelope。

VS Code 消费生成的 Traits，并通过 Stream、Tool、Interaction、Evidence、Terminal 与
Snapshot 领域模块投影。`projector/index.ts` 拥有 Sequence 与 Turn Identity，
`turn-projector.ts` 对生成的全部 Event Class 做 Exhaustive Dispatch。

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
Snapshot。`turnexec.Factory` 从该 Spec 打开强类型 Scope；Scope 运行期间 Sampling
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
10. `EvaluateTurnStep` 由 Reducer 选择 Repair、Verification 或 Complete。
11. Verification Executor 通过 `VerificationFinished` 返回证据；Reducer 选择 Passed、
    Repair、Reported、Blocked、Failed 或 Reverted，并独占 Repair Budget。
12. Engine 提交 `TerminalRequested`；Reducer 选择 Completed、Failed 或 Canceled。
    随后 Journal Commit/Suspend/Rollback 作为 Durable Effect 执行，并返回
    `JournalResultReceived`。Suspend 会为结构化绑定的 Continue Turn 保留
    Verification-blocked Draft。
13. Scope 准备带 Revision 与 Digest 的 `SessionDelta`，包含 History、Usage、Cost、
    Working Set、Evidence、Failures 与 Compaction State。
14. Runtime 为 Usage 与 Latency 冻结同一份带 Digest 的
    `TerminalMeasurementSnapshot`。Receipt、由 Measurement 投影的 Trace 与 Terminal
    Envelope 都引用该 Snapshot，不会再次读取可变 Counter。
15. Persistent Runtime 在同一 SQLite 事务原子提交 Frozen State、Measurement、
    Session Delta、Final Output、Receipt、Terminal Event、Outbox 与真实 Operation
    Receipt。
16. Engine 只在该 Commit 成功后幂等 Apply Session Delta；Commit 失败不修改 Session
    内存。
17. 重启时 Runtime 扫描 Pending Terminal Projection，以稳定 Event ID 逐条 Append，
    成功后再将对应 Entry 标记为 Published。
18. accepted StartTurn 仅在存在对应非终态 Domain Fact 时自动恢复；Coordinator requeue
    Running Effect，Engine 从 Durable Payload 接续 Provider、Tool 或 Journal 执行。
19. Approval/Input 恢复在接续执行前预装原 Request ID，Host 只回放一个 Wait，不会收到
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
经过校验的 Model-visible History Baseline 与 Profile Snapshot；Restore 不能执行历史
Event。持久化 Restore/Fork Event 保证重启重建结果确定。Fork 血缘与当前 Active
Session Thread 属于关系型 Lifecycle State，而不是 Host-local State。

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
Pressure、Privacy Error、Journal Failure、Payload Drop 与 Exporter Failure 只更新
Observation Health，绝不改写业务 Turn 的成功或失败结果。`Flush` 与 `Shutdown`
负责排空 Observation/OTLP Queue，但 Observation Plane 不获得执行权威。

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
Subagent 传播。OTLP Projector 支持通过环境变量选择 In-memory、HTTP/protobuf 和
gRPC Exporter。Metric Label 来自固定的低基数 Allowlist，不能包含 Path、Prompt、
Tool Argument 或 Resource ID。Semantic Reducer 可从 Raw Journal 确定性重建可解释
Graph；Support Bundle 构造会再次脱敏所选记录，并以独占 mode `0600` 写入 Archive。

## 上下文架构

上下文按稳定性和用途拆分：

- 稳定 Coding Policy 与系统约束；
- Repo Map 与 Symbol Index；
- 用户 Pin 文件；
- 持续演进的 Working Set；
- Evidence 与未解决风险；
- 最近 Event History 或结构化 Compact Summary。

上限是正确性的一部分。无界上下文最终会变贵、变慢并降低一致性。

Execution Receipt 会逐条解释入选的 Working Set 文件或测试，包括选择来源、支撑
Evidence、相关性分数和单条预算结果。`included=false` 加截断原因表示 Selector 选择了
该路径，但渲染后的上下文预算裁掉了对应行。各 Host 投影同一份 Receipt，不自行反推
选择原因。

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

Plugin/Skill CLI、ACP `extension/list`/`extension/control` 与 VS Code Extensions View
使用同一 Runtime Control Plane。Mutation 按 Operation ID 幂等，并持久化
Prepare/Commit Receipt；Host 只提交 Operation 与投影 Runtime-owned State。

### MCP

外部 Server 通过协议 Adapter 暴露 Tool。Health、Timeout、Circuit Breaker 和 Tool
Binding 隔离避免单个 Server 故障污染全部工具。

### Skill

Skill 打包指令和资源。Discovery、Manifest、Lock 与 Enablement State 让最终内容可见。

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
6. 同步更新中英文文档。
7. 必要时重新生成 Protocol、Observation Trait 与 Compatibility Artifact。
