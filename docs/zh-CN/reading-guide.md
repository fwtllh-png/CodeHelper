# CodeHelper 代码阅读指南

本文给出一条基于当前实现的源码阅读路径。目标不是按目录遍历文件，而是理解
CodeHelper 的权威状态、调用链、持久化边界和故障恢复语义，并能够独立定位一次真实
Turn 中的问题。

开始前先读：

1. 根目录 `README.md`；
2. [架构与安全设计](./architecture.md)；
3. [Runtime 可维护性与所有权边界](./runtime-maintainability-refactoring-plan.md)；
4. `AGENTS.md` 与 [AI Coding Agent 指南](./agent-guide.md)；
5. `git status --short`，避免把工作区已有修改当成基线。

## 一、先建立正确的阅读模型

### 1.1 一句话架构

```text
Host 提交 Operation
  -> Application Runtime 接受并分派
  -> Engine 冻结 TurnSpec 并打开 Scope
  -> TurnCoordinator 持久化 Reducer Transition
  -> Adapter 执行 Provider 或 Guarded Tool Effect
  -> TerminalPublisher 原子提交终态
  -> Event Hub 将事实投影给 Web / CLI / TUI
```

CodeHelper 不是“CLI 调模型再运行命令”的脚本。它是一个本地、持久、受治理的 Agent
Runtime；Web、CLI、TUI、Worker 和 Subagent 共享同一组执行与安全语义。

### 1.2 五条权威链

阅读任何功能时，先判断它位于哪条链：

| 权威链 | 输入 | 唯一写入者 | 持久结果 |
| --- | --- | --- | --- |
| Operation | 用户或 Host Intent | `runtime/app.OperationService` | Operation Receipt |
| Turn | Kernel Command | `turnkernel.TurnCoordinator` | Domain Fact + State Digest |
| Context | Session Delta/Rebase | `agent/context` + Application Persistence | Context Manifest/CAS |
| Side Effect | Kernel Effect | Provider/Tool/Journal Executor | Effect Result Command |
| Terminal | Frozen Terminal Material | `eventhub.TerminalPublisher` | Terminal Envelope + Outbox |

如果同一事实在两处都可写，通常就是架构问题。Host、Web Store、Observation 和 Exporter
都是 Projection 或 Evidence，不是这些链的替代 Authority。

### 1.3 两个状态机不要混淆

仓库中有两套重要但用途不同的状态机：

- **Turn Kernel**：`internal/runtime/agent/turnkernel`，管理一次 Agent Turn 内的
  Sampling、Tool、Approval、Input、Verification、Commit 和 Terminal。
- **WorkGraph Kernel**：`internal/orchestration/kernel`，管理跨 Turn 的 Run、Node、
  Attempt、Lease 和 Effect。

Turn Kernel 解决“一个回合如何正确结束”；WorkGraph 解决“多个持久工作如何被调度和
恢复”。Workflow、Worker 和 Subagent 不能另建第三套生命周期权威。

### 1.4 代码事实的优先级

发生冲突时按以下顺序判断：

1. Reducer、Validation、Transaction 和 Guard 实现；
2. 与实现同包的 Unit/Property/Race Test；
3. 生成协议与 Contract Fixture；
4. `docs/zh-CN` 当前架构文档；
5. 书籍章节和示例。

注释和命名可以解释意图，但不能替代测试中可观察的行为。

## 二、仓库地图

| 层 | 路径 | 阅读重点 |
| --- | --- | --- |
| 进程入口 | `cmd/codehelper` | Signal、退出码、CLI 委托 |
| Host | `internal/host` | 输入校验、Operation 提交、Event 呈现 |
| Protocol | `internal/runtime/protocol` | 跨 Host 的 Operation/Event/Receipt Contract |
| Application | `internal/runtime/app` | Operation、Session、Turn Lease、Terminal、Recovery |
| Composition | `internal/runtime/app/wire` | 具体依赖构造、资源关闭、后台服务启动 |
| Agent | `internal/runtime/agent` | Turn Kernel、Engine、Context、Prompt |
| Adapter | `internal/adapter` | Provider、Tool、MCP、Skill、Plugin、Hook |
| Security | `internal/security` | Policy、Permission、Constitution、Credential、Sandbox |
| Orchestration | `internal/orchestration` | WorkGraph、Task、Worker、Workflow、Subagent |
| Persistence | `internal/persist` | SQLite、CAS、Event、Session、Snapshot、Journal |
| Observability | `internal/observability` | Receipt、Usage、Trace、Diagnostics、Observation |
| Platform | `internal/platform` | Process、PTY、Repository Walk、OS 差异 |
| Config | `internal/config` | Schema、默认值、环境覆盖、Provenance |
| Web | `web/src` | Browser Runtime Client、Projection、React UI |

`internal/runtime/agent/engine` 与 `internal/runtime/app` 是协调层，文件较多。不要从
`engine.go` 或 `runtime.go` 第一行开始顺序读到底；应沿下面的调用链进入。

## 三、路线 0：先读协议

**目标：** 理解 Host 可以请求什么、Runtime 可以发布什么、失败如何跨层表达。

按顺序阅读：

1. `internal/runtime/protocol/identity.go`：Session、Thread、Turn、Item、Operation 等
   稳定 Identity。
2. `internal/runtime/protocol/operation.go`：13 类 `OperationKind`、Payload Validation、
   Turn Intent、Recovery Context。
3. `internal/runtime/protocol/event.go`：Event Kind、Typed Event Data、Usage 与
   Terminal Data。
4. `internal/runtime/protocol/receipt.go`：Execution Receipt 的证据结构。
5. `internal/runtime/fault/fault.go` 与 `internal/runtime/protocol/problem.go`：
   Code、Origin、Disposition、Side-effect State 和 Recovery Action。
6. `internal/runtime/protocol/event_traits.json`：Event Class、Item Owner、Durability、
   Correlation 与 Terminal Trait 的生成源。

关键区别：

- Operation 是“请求转换”，不等于已经发生；
- Event 是“已发布事实”，不是命令；
- Receipt 是结构化证据，不是权威状态；
- `UnknownEventData` 可供 Host 只读展示，但 Runtime 不从未知 Event 推断生命周期；
- Retry 不能只看错误字符串，应看 Typed Fault 的 Disposition 和 Side-effect State。

生成物：

- `docs/protocol/runtime-protocol.schema.json`；
- `internal/runtime/protocol/event_traits.gen.go`；
- `web/src/protocol/observation.generated.ts`；
- `web/src/protocol/web-host.generated.ts`。

配套验证：

```bash
make protocol-contract
make protocol-schema
make web-protocol-check
```

读完应能回答：为什么 `turn.start` 被接受，不代表 Turn 已经完成或甚至已经开始采样？

## 四、路线 1：进程入口与组合根

**目标：** 理解一个命令如何构造出完整 Runtime，以及为什么构造失败不会泄漏资源。

```text
cmd/codehelper/main.go
  -> host/cli.RunContext
  -> Cobra command
  -> runExec / runWeb / runTUI
  -> wire.NewExec
  -> defaultBuildModules
  -> backgroundModule
```

### 4.1 Host 入口

- `cmd/codehelper/main.go`：只建立 Process Context 并委托 CLI。
- `internal/host/cli/run.go`：`RunContext`、机器可读错误与退出码。
- `internal/host/cli/cobra.go`：`newRoot` 定义实际命令面。
- `internal/host/cli/exec.go`：最短的真实 Turn Host，可观察 Operation 提交和 Event
  消费。
- `internal/host/cli/web.go`：两阶段 Web Boot；先开放 Boot Surface，再构造并激活
  Runtime。

不要从 CLI 推断业务语义。CLI 只负责参数、I/O 和进程生命周期。

### 4.2 `wire.NewExec`

`internal/runtime/app/wire/runtime.go` 定义 `ExecOptions`、`Session`、`NewExec` 和
`defaultBuildModules()`。当前模块顺序是：

```text
config
-> provider
-> persistence
-> platform
-> builtin-tools
-> extension-tools
-> security
-> extension-plan
-> orchestration
-> observability
-> agent
-> runtime
-> background
```

接着阅读：

- `build_state.go`：构造期唯一共享对象；生产 Runtime 不得保留它；
- `modules_core.go`：Config、Persistence、Platform 和 Builtin Tool；
- `modules_provider.go`：Model Catalog、Route 与 Provider；
- `modules_security.go`：Policy、Permission、Journal、Guard Factory；
- `modules_orchestration.go`：Task、Workflow、Subagent、Scheduler；
- `modules_observability.go`：Observation/Telemetry；
- `modules_runtime.go`：Engine Seed、ThreadManager 和 Application Runtime；
- `module_background.go`：MCP Refresh、Runtime Recovery、Automation Reconcile、
  Worker Start；
- `resource_stack.go`：部分构造失败与正常关闭共用的逆序清理。

重要边界：

- `agentModule` 构造 `agentengine.Options`，但不执行 Turn；
- `runtimeModule` 构造 Facade，但不接受 Operation；
- `backgroundModule` 完成恢复后才启动 Admission 和 Worker；
- Child Engine 从冻结的 Seed 派生，并只能收窄父级 Authority 与 Budget。

建议先运行：

```bash
go test -run 'Test(DefaultBuildModuleOrder|NewExecRollsBack|BackgroundModuleOwns)' \
  ./internal/runtime/app/wire
```

读完应能回答：新增一个共享依赖时，为什么应增加 Module Output，而不是让下游保存
`buildState`？

## 五、路线 2：Operation 到异步 Turn

**目标：** 跟踪 `StartTurn` 从 Host 进入 Application，直到 Engine goroutine 获得
Thread Lease。

```text
Runtime.SubmitWithKey
  -> OperationService.SubmitWithKey
  -> operation queue
  -> operationDispatcher.Dispatch
  -> StartTurnHandler.Handle
  -> TurnService.Start
  -> ActiveTurnRegistry.Reserve
  -> TurnService.run
  -> EngineAdapter.StartTurn
  -> Engine.Execute
```

按顺序阅读：

1. `internal/runtime/app/runtime.go`
   - `Runtime.Submit` / `SubmitWithKey`；
   - `OperationService.SubmitWithKey`；
   - `Runtime.loop`；
   - `TurnService.Start` / `run`。
2. `internal/runtime/app/operation_dispatch.go`
   - `OperationOutcome`；
   - `operationDispatcher.Dispatch`；
   - `OperationService.Apply`。
3. `internal/runtime/app/active_turn_registry.go`
   - Thread/Turn Reservation；
   - Control Binding；
   - Token-fenced Release。
4. `internal/runtime/app/extension/engine_adapter.go`
   - Application Port 到 Agent Engine 的适配；
   - Editor Context 解析；
   - Receipt 创建；
   - Engine Event 到 Protocol Event 的映射。
5. `internal/runtime/app/thread_manager.go`
   - 每个 Thread 的 Engine 所有权；
   - Session Profile、Pending Interaction 和 Child Engine。

`OperationOutcome` 有四种结果：

| Kind | Commit Mode | 含义 |
| --- | --- | --- |
| `committed` | now | 同步完成，Dispatcher 发布 Event 并提交 Operation |
| `rejected` | now | 同步拒绝，发布 Typed Problem |
| `async` | deferred | Turn 已预留，Operation 由终态事务提交 |
| `terminal` | deferred | 终态材料已进入原子提交路径 |

`ActiveTurnRegistry` 只管理进程内活跃所有权；Durable Turn Lease 由 Coordinator Store
管理。两者不能互相替代。

关键测试：

```bash
go test -run 'Test(StartTurn|RuntimeApprovalPauseResumeE2E|RuntimeInputPauseResumeE2E)' \
  ./internal/runtime/app
```

读完应能回答：为什么异步 Start Operation 不能在 `TurnService.Start` 返回时立即
Commit？

## 六、路线 3：Engine、Scope 与 Turn Kernel

**目标：** 理解生产 Turn 的唯一入口，以及纯状态转换和外部 I/O 如何分离。

### 6.1 冻结 Turn

从 `internal/runtime/agent/engine/turn_handler.go` 开始：

1. `Engine.Execute` 获取 Session 级互斥；
2. `prepareTurnSpec` 校验 Request；
3. `SnapshotTurnSpec` 冻结 Route、Profile、Policy、Tool Catalog、Skill、MCP、
   Extension、Budget、World 和 Window；
4. `scopeFactory.Open` 为 Turn 创建隔离 Scope；
5. `Scope.Run` 获取 Workspace Gate、恢复 Journal Draft、打开 Runtime Kernel。

`Engine.Execute` 是唯一生产入口。`legacy_entrypoints_test.go` 中出现的 `Run*` 方法是
负向架构测试，不是可调用兼容层。

### 6.2 Kernel 数据结构

按顺序阅读 `internal/runtime/agent/turnkernel`：

1. `state.go`：`State`、Phase、Pending Effect、Completion、Verification、Journal、
   Progress 和 Terminal Decision；
2. `command.go`：所有可接受输入；
3. `reducer.go`：命令分派；
4. `reducer_sampling.go`、`reducer_tool.go`、`reducer_interaction.go`、
   `reducer_context.go`、`reducer_verification.go`、`reducer_effect.go`、
   `reducer_terminal.go`：各 Command Family 的纯转换；
5. `invariants.go`：每次转换后必须成立的约束；
6. `coordinator.go`：`Reducer.Apply` 的唯一生产调用方；
7. `effect_dispatcher.go`：Effect Start/Result 的稳定 Identity 与重交；
8. `runtime_kernel*.go`：Engine 使用的语义化 Facade；
9. `runtime_control.go`：有界 Mailbox 与 Request Ledger；
10. `terminal_envelope.go`：冻结终态格式。

Coordinator 的顺序不可调整：

```text
Command
  -> Reducer.Apply(current)
  -> Validate(next)
  -> 生成 State Digest 和 Domain Facts
  -> 按 ExpectedNext 持久化
  -> 更新内存 State
  -> 派发 Effects
```

持久化失败时不更新内存 State，也不派发 Effect。Effect Result 提交失败时，
`DurableEffectDispatcher` 保留同一个 Result Command 供重交，不能再次执行外部副作用。

### 6.3 Engine 业务循环

继续阅读：

- `model_handler.go`：`modelStep`、Prompt Projection、Provider Effect、Usage；
- `tool_handler.go`：Tool Proposal、Replay Plan、Batch Execution、Result Feed；
- `completion_declaration.go`：结构化完成声明；
- `progress.go`：进展签名与 No-progress；
- `verify.go`：Verification Gate；
- `terminal_handler.go`：冻结业务决策、失败 Context、Session Delta、Terminal Material；
- `cancel_handler.go`、`approval_handler.go`：ControlPort 与恢复后的交互；
- `provider_retry.go`：Typed Failure、Backoff、Context Overflow Recovery。

正文是 Provisional Output。只有 Kernel 接受 Completion、Verification 和 Journal 结果
后，输出才进入 Terminal Envelope。`message_stop` 只结束一次 Provider Sample。

最有价值的测试：

```bash
go test -run 'Test(StructuredInteractiveTurn|MutationCompletes|TerminalState|ApplyDoesNotMutate)' \
  ./internal/runtime/agent/turnkernel
go test -run 'TestPhase4R3CoordinatorPersistsBeforeStateAndDispatch' \
  ./internal/runtime/agent/turnkernel
go test ./internal/runtime/agent/engine
```

读完应能回答：为什么 Provider、Tool 和 Journal 的成功返回仍不能直接把 Turn 标记为
Completed？

## 七、路线 4：Context Authority 与 Prompt Projection

**目标：** 区分权威会话状态、模型可见投影和持久化表示。

### 7.1 Authority

从 `internal/runtime/agent/context/authority.go` 开始。`Authority` 统一拥有：

- Working Set；
- Evidence；
- Failures；
- World Baseline；
- Token Window；
- Compaction State。

History 与 Plan 在 Snapshot/Delta 边界传入，但其 Clone、Restore、Retention 和
Workspace Reconciliation 仍由 Context Package 定义。

继续阅读：

- `store_store.go`：`MessageLedger` 和不可变 Message Snapshot；
- `store_world.go`：World Full/Patch Projection；
- `store_window.go`：Observed Prefill 与 Pending Delta；
- `workingset_workingset.go`：来源合并、衰减和 Critical Path；
- `evidence_evidence.go`：Read、Change、Verification、Diagnostic、Handle；
- `compact_failures.go`：有界失败账本；
- `plan.go`：结构化 Plan。

### 7.2 Compaction

Compaction 不是“让模型总结聊天记录”。阅读顺序：

1. `compaction_policy.go`：是否压缩以及安全切分点；
2. `compaction_candidate.go`：候选 History、Truth、Tail 与预算；
3. `compact_retention.go`：Mandatory/Protected/Refreshable Entity；
4. `compact_truth.go`：结构化 Truth Capsule；
5. `compact_narrative.go`：非权威 Narrative Artifact；
6. `narrative_service.go`：独立 Summary Route；
7. `narrative_rebase.go`：Context Rebase Envelope。

Truth Capsule 来自当前 Owner Snapshot，不从旧摘要递归生成。Narrative 可以失败或被
丢弃，不能证明 Verification、Permission 或 Side Effect。

### 7.3 Session Delta 与 Manifest

- `session_delta.go`：一次终态后的逻辑 Context + Accounting Delta；
- `session_context.go`：Checkpoint Snapshot、Digest、Workspace Binding；
- `session_manifest.go`：History Base/Tail 与 Owner Base/Delta CAS Ref；
- `runtime/app/persistence/context_rebase.go`：Durable Rebase Commit；
- `engine/session_delta.go`：Terminal Commit 成功后的幂等 Apply；
- `engine/checkpoint_context.go`：Export、Restore、Fork。

Context Restore 会重新捕获 Workspace Binding，并失效不匹配的文件事实；它不会重放
工具、回滚文件或减少 Usage。

### 7.4 Prompt

`internal/runtime/agent/prompt` 只做 Projection：

- `context.go`：静态 Prompt Partition；
- `turn.go`：当前 Turn 输入；
- `world_projection.go` / `worldstate.go`：World Full/Patch；
- `repository.go`：Repository Map；
- `codingpolicy.go`：Coding Policy；
- `sample_reason.go`：采样原因。

Prompt 不拥有权威状态，Host 也不能直接拼接未经 Runtime 校验的 Workspace 内容。

关键测试：

```bash
go test ./internal/runtime/agent/context ./internal/runtime/agent/prompt
go test -run 'Test(ContextManifest|WorkspaceReconciliation|RetentionRemainsBounded)' \
  ./internal/runtime/agent/context
```

读完应能回答：为什么缩短模型输入不等于删除 Durable History？

## 八、路线 5：Provider、Stream Assembly 与预算

**目标：** 理解 Vendor-specific Transport 如何被收敛成稳定的 Runtime Result。

按顺序阅读：

1. `internal/adapter/provider/types.go`：Message、Content Block、Tool Call、Stream
   Event、Usage、Replay State；
2. `internal/adapter/model/catalog.go` 与 `route.go`：模型能力和 Purpose Route；
3. `internal/adapter/provider/router`：Provider Route；
4. `internal/adapter/provider/wire`：请求编码与协议属性；
5. `internal/adapter/provider/httpclient/client.go`：连接、状态码、Deadline 和 Transport；
6. 一个具体 Adapter，例如 `provider/deepseek/adapter.go`；
7. `provider/assembly/response_assembly.go`：增量顺序、Tool Fragment、Usage；
8. `provider/assembly/stream_consumer.go`：校验、Projection、Incomplete Output 和
   自适应 Durable Checkpoint；
9. `engine/toolsample.go` 与 `model_handler.go`：Assembly 如何接入 Kernel Effect；
10. `engine/provider_retry.go`：Retry/Resume/完整逻辑请求回退。

需要区分：

- Logical Request 与 Transport Attempt；
- Provider 累计 Usage 与跨 Sample 汇总；
- Meaningful Partial Output 与完整 Tool Call；
- Context Capacity、Turn Token Ceiling、Session Budget 和 Child Tree Budget；
- 模型输出不完整与永久 Provider Failure。

默认 Turn Token Ceiling 从当前 Route 的 Context Window 派生；显式值只能收紧。Child
Tree 默认预算由父 Turn 剩余容量和并发度派生。Stream Checkpoint 的间隔随已持久化
Payload 增长，不按固定 Delta 数写入。

关键测试：

```bash
go test ./internal/adapter/provider/assembly ./internal/adapter/provider/httpclient
go test -run 'Test(ResponseAssembly|DeltaCoalescing)' \
  ./internal/adapter/provider/assembly
go test -run 'TestEffective.*Budget' ./internal/runtime/app/wire
```

读完应能回答：断流后为什么既不能丢弃已确认输出，也不能执行半个 Tool Call？

## 九、路线 6：Tool、Guard、Journal 与 Sandbox

**目标：** 理解有副作用 Tool 的唯一授权与执行路径。

```text
Model Tool Call
  -> Catalog Binding
  -> Replay/Parallel Plan
  -> tool.ExecuteBatch
  -> Guard.ExecuteBound
  -> argument/resource preparation
  -> policy + constitution + permission
  -> approval when required
  -> journal before-image
  -> sandboxed execution
  -> observed changes + diagnostics
  -> Tool Result Command
```

### 9.1 Tool Contract

- `internal/adapter/tool/tool.go`：Descriptor、Capability、Access Mode、Resource、
  Outcome 和 Result Store；
- `catalog.go`：封闭 Registry、Catalog Snapshot 与 Binding；
- `execution.go`：Prepared Invocation 与 Outcome Facts；
- `batch.go`：Serial Fence、有界并发、Panic 隔离、全部 goroutine Join；
- `model_projection.go`：模型可见 Result；
- `result/`：恢复分类与有界 Surface；
- `handle/` 与 `content/`：完整结果检索。

### 9.2 Guard

从 `internal/adapter/tool/guard/guard.go` 的 `ExecuteBound` 开始，再读：

- `guard.go` 中的 `prepare`、`prepareFileWrites`：参数、资源和写路径准备；
- `pipeline_authorize.go`：Policy、Permission、Approval；
- `pipeline_attempt.go`：Sandbox/Network Escalation 与有限重试；
- `guard.go` 中的完成路径：Outcome、变更和 Receipt；
- `authority.go`：Invocation Authority 与资源身份；
- `guard.go` 中的 `waitForApproval`：稳定 Request ID、TTL、替换参数和恢复。

安全实现：

- `internal/security/policy`：`allow / ask / deny / hold`；
- `internal/security/constitution`：不可被普通配置覆盖的仓库规则；
- `internal/security/permissions`：有 Scope 的持久 Grant；
- `internal/security/sandbox`：平台 Backend 与 Fail-closed；
- `internal/security/egress`：网络目标与 Managed Backend；
- `internal/persist/workspacejournal`：Before/After、Commit、Suspend、Rollback。

`exec_command` 写权限只覆盖显式 `write_paths`。待创建文件必须位于已存在父目录，Guard
在执行前 Preflight，Strong Sandbox 只物化最小占位；目录、Symlink 和身份漂移均拒绝。

关键测试：

```bash
go test ./internal/adapter/tool ./internal/adapter/tool/guard
go test ./internal/security/...
go test -run 'TestGuardRunsDisjointClaimsConcurrentlyAndSerializesConflicts' \
  ./internal/adapter/tool/guard
make sandbox-attack-test
```

读完应能回答：一次审批允许了什么精确资源，以及为什么审批期间不能持有 Admission 或
Resource Claim？

## 十、路线 7：Terminal、持久化与恢复

**目标：** 理解 Turn 如何只提交一次，以及进程崩溃后如何继续。

### 10.1 Terminal Commit

从 `internal/runtime/app/eventhub/terminal.go` 的 `TerminalPublisher.Commit` 开始。
它接收：

- Frozen Kernel State；
- 完整 Domain Facts；
- Frozen Measurement；
- Execution Receipt；
- Typed Terminal Event；
- Session Delta。

提交前，`prepareContextManifest` 将 Session Delta 转为 CAS-backed Context Envelope。
随后同一事务提交：

```text
Terminal Envelope
+ Session Delta / Context Manifest Reachability
+ Final Output
+ Operation Commit Receipt
+ Receipt Event
+ Terminal Event
+ Projection Outbox
```

`Commit` 成功后，`Publish` 才按稳定 Event ID 排空 Outbox。投影失败不会回滚已经提交的
业务终态；`Recover` 会在重启后继续发布。

继续阅读：

- `internal/runtime/app/terminal_runtime.go`：TerminalRuntime Port；
- `internal/runtime/app/extension/terminal_measurement.go`：冻结 Usage/Latency；
- `internal/runtime/app/extension/tool_execution_receipt.go`：工具分类统计；
- `internal/persist/state/turnstate/store.go`：SQLite 原子实现；
- `internal/runtime/app/persistence/repositories.go`：Durable Repository 装配。

### 10.2 Runtime Recovery

阅读：

1. `runtime_start.go`：Prepared Runtime 与 `Runtime.Start`；
2. `eventhub.TerminalPublisher.Recover`：Terminal Outbox；
3. `orchestration.go`：WorkGraph Effect；
4. `turn_recovery.go`：Recovery Source 校验；
5. `startup_terminal.go`：启动期失败的终态收敛；
6. `wire/turn_coordinator.go`：Durable Coordinator、Turn Lease 和 Fact Restore；
7. `turnkernel.RestoreTurnCoordinator`：校验 Sequence/Digest、Requeue Running Effect；
8. `runtime.go` 的 `recoverPendingTurns`：恢复 Approval/Input 和未终态 Turn。

自动恢复要求 Accepted Start Operation、非终态 Domain Facts 和有效 Lease/Identity。
Checkpoint Restore 是 Context 恢复；Pending Turn Recovery 是执行生命周期恢复，两者
不是同一操作。

关键测试：

```bash
go test -run 'TestC5Runtime|TestPersistentRuntime' ./internal/runtime/app ./internal/runtime/app/wire
go test -run 'TestPhase4R2TerminalEnvelope' ./internal/runtime/agent/turnkernel
go test ./internal/persist/...
```

读完应能回答：如果 Terminal 已提交但 Web 尚未收到 Event，重启后哪一层负责补发？

## 十一、路线 8：WorkGraph、Worker、Workflow 与 Subagent

**目标：** 理解跨 Turn 的持久任务如何保持唯一状态机和 Lease Fence。

### 11.1 WorkGraph

- `internal/orchestration/model`：Run、Node、Attempt、Effect；
- `kernel/kernel.go`：`Reduce` / `ReduceOwned`；
- `store/store.go`：Snapshot、Fact、Command Receipt、Effect Outbox 的单事务提交；
- `runtime/app/orchestration.go`：Runtime Operation 与 WorkGraph Projection。

`kernel.Command` 必须携带 Command ID、Run ID、Expected Revision 和时间。Claim、
Heartbeat、Release、Settlement 还必须携带 Lease Owner/Epoch 与 Authority Digest。

### 11.2 Worker

`internal/orchestration/worker/worker.go` 是唯一 Claim Authority：

- `Start` 启动 Claim、Reclaim 和 Automation Loop；
- `Dispatch` 按剩余并发容量认领；
- Heartbeat 保持 Lease；
- 丢失 Lease 会取消执行；
- `Close` 停止认领并把未完成工作归还队列；
- 不确定副作用的错误不能自动标成 Retryable。

### 11.3 Workflow

- `workflow/spec.go`：DAG 与 Node Contract；
- `workflow/compiler.go`：编译；
- `workflow/runtime.go`：Run 入口；
- `workflow/controller.go`：执行协调；
- `workflow/orchestrate/runtime_driver.go`：Task/Turn Driver；
- `workflow/jsvm`：受限脚本执行。

Workflow 将 DAG 编译成 WorkGraph Node，不拥有第二套 Checkpoint/Lease 状态机。

### 11.4 Subagent

- `subagent/control_plane.go`：Delegation Intent、Role、Spawn Contract；
- `subagent/subagent.go`：Manager、Tool Execution、Mailbox；
- `subagent/context_fork.go`：Task Capsule 与 Context Mode；
- `subagent/workgraph.go`：Agent Attempt 和恢复；
- `subagent/worktree.go`：隔离工作区；
- `runtime/app/wire/childruntime.go`：真实 Child Engine；
- `runtime/app/wire/agentexecutor.go`：Worker Task 到 Child Turn；
- `orchestration/budget/ledger.go`：整棵 Agent Tree 的 Reservation 与结算。

Child Authority 是父级 Authority 与 Role Policy 的交集。默认 Token Budget 按父级剩余
容量和并发槽位派生；嵌套 Child 只能继续收窄。写入型并发 Child 使用 Worktree，合并由
`orchestration/chatmerge.Service` 处理。

关键测试：

```bash
go test ./internal/orchestration/kernel ./internal/orchestration/store
go test ./internal/orchestration/worker ./internal/orchestration/workflow
go test ./internal/orchestration/subagent ./internal/orchestration/budget
go test -run 'TestChildAgent|TestSchedulerRunsAQueuedTask' ./internal/runtime/app/wire
```

读完应能回答：Worker 崩溃后，为什么旧进程不能用过期 Attempt 结算新 Owner 的工作？

## 十二、路线 9：Web、CLI 与 TUI Projection

**目标：** 理解 Host 如何呈现 Runtime，而不复制业务状态机。

### 12.1 Web Server

`internal/host/runtimeapi/web/server.go` 是本机 HTTP/WebSocket Host：

- `New` 构造 Boot Surface 和随机 Capability Token；
- `Activate` 注入已恢复的 Runtime；
- `/healthz` 和 `/api/v1/bootstrap` 在 Runtime 失败时仍可访问；
- `/api/v1/*` 是类型化同源 RPC；
- `/api/v1/events` 是认证后的下行 WebSocket；
- `browserFence` 校验 Loopback、Host、Origin 和 Token；
- `validateWebEditorContext` 重新验证浏览器提交的文件、图片、符号、诊断和 Tool Result
  引用。

契约来源：

- `internal/host/runtimeapi/web/contract.go`；
- `docs/protocol/web-host.contract.json`；
- `internal/host/runtimeapi/web/web-operation-exposure.json`；
- `testdata/contracts/web-feature-parity.json`。

### 12.2 Browser Runtime

当前 Web Client 没有独立 `chat/projector` 目录。实际入口是：

- `web/src/runtime/client.ts`：`RuntimeClient`；
- `web/src/runtime/storage.ts`：IndexedDB 中可丢弃的 Browser Projection State；
- `web/src/ui/App.tsx`：React UI 和 `projectTranscript`；
- `web/src/protocol.ts`：类型聚合；
- `web/src/protocol/web-host.generated.ts`：生成契约。

重点读 `RuntimeClient.start`、`connect`、`selectSession`、`applyEvent`、
`scheduleSessionRefresh` 和 `commitCursor`：

1. Bootstrap 获取 Token 与 Readiness；
2. 恢复 Browser Cursor/Draft；
3. 建立 WebSocket；
4. Hydration 期间缓冲 Live Event；
5. 应用带 `through_sequence` 的 Presentation Snapshot；
6. 合并更高 Sequence 的缓冲 Event；
7. Lifecycle Event 只触发合并后的 Session List Refresh；
8. Retention Gap 才重置 Projection。

Sequence 要求严格单调，不要求连续。Browser State、Session List 和 Transcript 都不是
Runtime Authority。

### 12.3 CLI 与 TUI

- `internal/runtime/eventview/view.go`：Go Host 对 Event 的唯一 Typed Interpretation；
- `internal/host/cli/exec.go`：机器/文本输出；
- `internal/host/tui/reducer.go` 与 `projection.go`：终端 UI Projection。

关键验证：

```bash
go test ./internal/host/runtimeapi/web ./internal/runtime/eventview
npm --prefix web run check
npm --prefix web test
make web-parity-check
```

读完应能回答：浏览器刷新后如何避免重复提交 Prompt，又如何避免 Snapshot 覆盖刚到达
的 Live Event？

## 十三、路线 10：可观测性与证据

**目标：** 区分业务事实、终态证据和可丢失 Observation。

按顺序阅读：

- `internal/observability/receipt`：Execution Receipt Builder；
- `internal/observability/usage`：Sample/Turn/Session 聚合；
- `internal/observability/trace`：Span 与 Frozen Latency；
- `internal/observability/verify`：Verification Evidence；
- `internal/observability/diagnostics`：诊断命令；
- `internal/observability/observation`：版本化 Envelope；
- `internal/observability/privacy`：写入前 Admission；
- `internal/observability/router`：Journal/CAS/Exporter 路由；
- `internal/observability/semantic`：从事实重建解释图；
- `internal/observability/supportbundle`：支持材料；
- `internal/observability/telemetry`：低基数指标。

三个容易混淆的数据源：

| 数据 | Authority | 失败影响 |
| --- | --- | --- |
| Domain Fact / Terminal Envelope | 是 | 阻止或恢复业务提交 |
| Execution Receipt / Frozen Measurement | 终态证据 | 必须与终态原子一致 |
| Observation / Exporter | 否 | 记录 Health，不改写业务结果 |

Runtime Health 的活动状态来自 `ActiveTurnRegistry` 和 Engine Recorder，终态来自
Terminal Envelope。`spans` 表为空不能单独证明没有泄漏，还要检查 Lease、Pending
Interaction、Provider Call 和 Tool Execution。

验证：

```bash
make observation-traits-check
go test ./internal/observability/...
go test -run TestSystemDiagnosticsReportsAuthoritativeRuntimeHealth \
  ./internal/host/runtimeapi/web
```

读完应能回答：为什么 OTLP Export 失败不能把一个已经完成的 Turn 改成 Failed？

## 十四、五种实战追踪方法

### 14.1 跟踪一次普通只读 Turn

```text
protocol.StartTurnPayload
-> Runtime.SubmitWithKey
-> operationDispatcher
-> TurnService.Start/run
-> EngineAdapter.StartTurn
-> Engine.Execute
-> Scope.Run
-> RuntimeKernel.RequestModel
-> providerassembly.ConsumeStream
-> EvaluateTurnStep
-> TerminalPublisher.Commit/Publish
```

配合 `TestNonInteractiveReadOnlyResearchCompletesWithoutDeclaration` 和
`TestStartTurnSeparatesModelAndDisplayPrompts` 阅读。

### 14.2 跟踪一次写文件 Turn

从 `ToolCallsProposed` 开始，沿 `tool_handler.go`、`tool.ExecuteBatch`、
`Guard.ExecuteBound`、Workspace Journal、Verification、Journal Commit 和 Terminal
Envelope。重点检查 Mutation Revision 如何使旧 Completion/Verification 失效。

### 14.3 跟踪审批暂停与恢复

沿 `ApprovalRequired`、Guard Pending Map、Protocol Event、`approval.decision`、
`RequestLedger`、`ApprovalResultReceived` 阅读。再看
`TestRuntimeApprovalPauseResumeE2E` 和
`TestC5GuardRestoresApprovalWaitWithoutDuplicateEmission`。

### 14.4 跟踪断流与进程重启

断流路径：

```text
ResponseAssembly checkpoint
-> IncompleteOutputError
-> Typed Provider Failure
-> ProviderRetryRequested
-> 同一 Effect Attempt/Replay State
```

进程重启路径：

```text
Runtime.Start
-> Terminal Outbox Recovery
-> WorkGraph Effect Drain
-> Pending Turn Scan
-> RestoreTurnCoordinator
-> Requeue Running Effect
-> 恢复 Approval/Input
```

### 14.5 跟踪 Child Agent

从 `agent` Tool 进入 `AgentControl.SpawnIntent`，再跟到 Budget Reservation、
WorkGraph Attempt、`childRuntime.StartTurn`、Child Engine、Terminal Settlement 和
Chat Merge。检查 Agent Path、Trace、Usage 和 Permission Digest 是否保持父子归属。

## 十五、按变更类型定位代码

| 需求 | 首读位置 | 必须联动 |
| --- | --- | --- |
| 新 Operation/Event | `runtime/protocol` | Schema、Traits、Host Contract、Web Types |
| 改 Turn 状态 | `turnkernel/state.go`、`reducer_*.go` | Coordinator/Property/Recovery Test |
| 改 Agent 循环 | `engine/turn_handler.go` | Kernel Command，不新增状态写入点 |
| 改 Context | `agent/context` | Prompt Projection、Manifest、Restore |
| 改 Provider | `adapter/provider` | Assembly、Usage、Retry、Capability |
| 新 Tool | `adapter/tool` | Catalog、Guard、Receipt、Sandbox |
| 改权限 | `security`、`tool/guard` | Allow/Deny/Approval/Cleanup/Race |
| 改终态 | `app/eventhub/terminal.go` | Turnstate Transaction、Outbox Recovery |
| 改 Session | `runtime/app/service_facade.go` | Lifecycle Store、Web Query |
| 改 Workflow | `orchestration/workflow` | WorkGraph Command/Store/Worker |
| 改 Subagent | `orchestration/subagent` | Child Runtime、Budget、Worktree、Merge |
| 改 Web API | `host/runtimeapi/web` | Contract JSON、Generated TS、Client |
| 改 Web 展示 | `web/src/runtime/client.ts`、`ui/App.tsx` | Hydration、Cursor、Projection Test |
| 改 Observation | `observability/schema` | Privacy、Router、Trait Generation |

## 十六、推荐阅读节奏

### 第一轮：建立边界，约 2 小时

阅读协议、组合根和 Application Operation。只回答“谁拥有状态”和“谁允许调用谁”，
不要陷入 Provider 或 Tool 细节。

### 第二轮：跑通一个 Turn，约 3 小时

从 `application_e2e_test.go` 进入，沿 `Engine.Execute`、Kernel、Provider/Tool 和
TerminalPublisher 画出真实调用图。

### 第三轮：理解长期状态，约 2 小时

阅读 Context Authority、Compaction、Session Delta、Manifest 和 Terminal Transaction。
重点区分 Truth、Narrative、Projection 与 Accounting。

### 第四轮：理解安全与故障，约 3 小时

阅读 Guard、Sandbox、Approval、Provider Retry、Durable Effect 和 Recovery Test。
对每个 Retry 问：Identity 是否相同，副作用是否已知，预算是否仍有余额？

### 第五轮：理解扩展面，约 2 小时

阅读 WorkGraph、Worker、Workflow、Subagent 和 Web Hydration。确认这些子系统只通过
Runtime/Kernel Contract 扩展，而不是复制核心循环。

## 十七、阅读与验证工具

常用命令：

```bash
# 查文件和符号
rg --files internal/runtime/agent
rg -n 'func \\(.*\\) Execute|type .*State' internal

# 阅读公开 API 和依赖
go doc ./internal/runtime/agent/turnkernel
go list -deps ./cmd/codehelper

# 跑最窄契约
go test -run TestName -v ./internal/path/to/package

# 核心架构与恢复
make architecture-ratchet
make turn-kernel-convergence-exit-gate
make reliability-gate

# 文档与 Web
make docs-check
make book-check
npm --prefix web run check
npm --prefix web test

# 高风险并发变更
go test -race -p 1 ./internal/runtime/agent/... ./internal/runtime/app/...
```

不要用目录名猜实现，也不要只读 Interface。对一个关键调用至少同时查看：

1. Interface/Protocol；
2. 生产 Adapter；
3. Authority/Transaction；
4. Failure Test；
5. Recovery 或 Race Test。

## 十八、完成阅读后的自检

能够准确回答以下问题，才算掌握主干：

- `StartTurn` 的 Operation 在何时真正 Commit？
- 谁是 `Reducer.Apply` 的唯一生产调用方？
- Effect Result 持久化失败时，为什么不能重新执行副作用？
- Provider 的 Transport Attempt 如何归属同一 Logical Request？
- Truth Capsule 与 Semantic Narrative 的 Authority 差异是什么？
- Session Delta、Context Manifest 和 Checkpoint 分别表示什么？
- Terminal Commit 与 Event Publish 为什么分成事务和 Outbox 两步？
- Approval、Input、Cancel 和 Steer 如何进入正在运行的 Scope？
- WorkGraph Revision 与 Lease Epoch 分别防止哪种竞态？
- Child Agent 的预算、权限、Context 和 Workspace 如何从父级收窄？
- Web Hydration 如何处理 Snapshot 与并发 Live Event？
- 哪些故障可以改变业务结果，哪些只能改变 Observation Health？

这些问题都能从上述代码与测试中得到确定答案；如果答案只能来自文档措辞而无法由测试
证明，应继续追到对应的 Authority 和持久化边界。
