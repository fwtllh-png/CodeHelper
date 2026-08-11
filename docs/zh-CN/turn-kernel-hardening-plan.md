# Turn Kernel 收敛改造方案

简体中文 | [English](../en/turn-kernel-hardening-plan.md)

## 1. 文档定位

本文是 Turn Kernel 改造的唯一前瞻方案，取代此前按 Phase 4R/Phase 5 累加形成的方案。
历史阶段编号、完成声明和 D01-D11 字符串门禁不再表示当前完成度。

事实优先级如下：

1. 生产调用链和持久化事务；
2. 行为、故障注入、Race 和恢复测试；
3. 本文的状态与处置清单；
4. 历史实施报告。

本文使用四种状态，禁止再把“类型已经存在”写成“迁移已经完成”：

| 状态 | 含义 |
| --- | --- |
| `Foundation` | 可保留的目标架构原语，但不代表生产权威路径已经接入 |
| `Bridge` | 为迁移存在的双轨适配层，必须有明确删除阶段 |
| `Legacy` | 当前仍承载生产行为的旧实现，迁移完成前不能直接删除 |
| `Missing` | 目标架构仍未实现或未进入生产路径 |

## 2. 当前结论

Turn Kernel 尚未完成收敛。仓库目前是一个可运行但权力分散的中间态：

- `turnkernel.State`、`Reducer`、`TurnCoordinator`、Domain Fact 和 Terminal
  Envelope 已形成一组 `Foundation`；
- `engineTurnKernel` 和 `MigrationEffectDispatcher` 已 Durable 路由 Control/Tool
  Effects，但后续阶段 Effect 与决策仍是 `Bridge`；
- `Engine.RunForTurnWithIntentAndAttachments` 仍决定采样循环、Repair、Completion、
  Verification、Journal、Output Release 和大部分终态顺序，是实际 `Legacy`
  控制平面；
- App 和 Runtime 仍参与终态解释、fallback、发布和 Operation Commit；
- Model、Verification、Journal、Terminal Effect 接管、原子 Operation Commit 和
  重启 Outbox 恢复仍是 `Missing`。

因此撤销以下历史结论：

- “Phase 4R 已完成”；
- “D01-D11 全绿等价于 TurnCoordinator 已成为唯一决策者”；
- “Terminal Envelope 已实现 Final Output、Terminal 和真实 Operation Commit 原子提交”；
- “Domain Fact Restore 已接入生产恢复”。

旧字符串门禁已在 C0 删除；仍有长期价值的 no-replay 约束已迁入正式 Ownership Gate。

## 3. 当前真实调用链

### 3.1 Turn 执行

```text
Runtime StartTurn
  -> EngineAdapter
  -> Engine.RunForTurnWithIntentAndAttachments
       -> 创建 engineTurnKernel
            -> 注入的 CoordinatorRuntime
            -> TurnCoordinator
            -> Persistent Runtime 使用 SQLite Domain Fact Store
            -> MigrationEffectDispatcher
                 -> Control/Tool/Model/Verification Effect 路由注册表
                 -> C5 Deferred Projection Effects
       -> Engine Effect Pump 提交 EvaluateTurnStep
       -> Reducer 选择下一条结构化 Action
       -> Engine 执行选定的 Provider/Tool/Verification Action
       -> Terminal Engine Event 仍由 observe 反向翻译
       -> terminalProjector 投影已接受的终态决策
  -> EngineAdapter 冻结旧 Engine 观察数据并构造 Receipt
  -> Runtime Terminal Store 提交 Terminal Envelope
  -> Runtime 发布 Receipt / Terminal
  -> Runtime 单独提交 Operation
```

这条链路存在三个控制环：

1. Engine 过程式业务循环；
2. `engineTurnKernel` 适配和观察循环；
3. App/Runtime 终态提交与 fallback 循环。

目标不是继续同步三个控制环，而是删除后两个平行决策来源，只保留 Coordinator 驱动的
业务循环和 Runtime 的事务/投影职责。

### 3.2 当前持久化事实

- persistent wire 注入同一个 SQLite-backed Coordinator Runtime 和 Terminal Store；
- 每个 Coordinator Transition 都在状态提交或 Effect dispatch 前写入 SQLite Domain
  Facts；
- 启动时通过可续租租约声明 active Turns，并调用 `RestoreTurnCoordinator`；
- running Effects 在重新 dispatch 前通过 Reducer 持久化 requeue；
- Envelope 中的 `OperationCommitFact` 是载荷，不会在同一 SQLite 事务中更新 Runtime
  的真实 Operation 状态；
- Runtime 在投影成功后另行调用 `r.commit(operation.ID)`。

所以当前实现已具备运行中 Coordinator 恢复，但仍不具备真实终态 Operation 原子性和重启
Outbox 投影。

## 4. 代码处置清单

### 4.1 保留并加固的 Foundation

| 路径 | 当前价值 | 后续动作 |
| --- | --- | --- |
| `turnkernel/state.go` | 规范 Phase、State 和 Effect Ledger | 按垂直切片补齐事实，删除只服务旧桥接的字段 |
| `turnkernel/command.go` | 结构化输入和 Result Command | 明确 Executor Result Schema 与幂等身份 |
| `turnkernel/reducer.go` | 纯 Transition 和核心不变量 | 接管 Repair、终态资格和事务状态，不再由 Engine 预判 |
| `turnkernel/invariants.go` | State 校验与 Digest 基础 | 增加跨 Fact、Terminal 和 Effect 闭环校验 |
| `turnkernel/coordinator.go` 中 `TurnCoordinator` | 串行 Command、persist-before-dispatch | 注入生产 Store，增加恢复/租约/停机语义 |
| `turnkernel/terminal_envelope.go` | Envelope、Commit Marker、Outbox 原语 | 与真实 Operation 表和 Event Outbox 合并事务边界 |
| `persist/state/turnstate` | SQLite Domain Fact 和 Terminal Store | 接入活跃 Turn，并提供启动扫描和恢复查询 |

这些文件“可保留”不表示其接口已经稳定。迁移过程中允许收紧或重写，但不得回退为 Engine
直接写 State。

### 4.2 路线走偏遗留的 Bridge

以下内容完成替代后必须删除，不能长期保留兼容：

| 遗留 | 问题 | 删除阶段 |
| --- | --- | --- |
| `engineTurnKernel.observe` 与全部 `observe*Locked` | 从旧 Engine Event 反推 Kernel Command，方向倒置且错误被降级为 drift | C2-C4 分片删除，C6 清零 |
| `applyLocked`、`drifted` 和 Observer 修复式状态推进 | 允许权威 Transition 失败后继续旧循环 | C2 起禁止生产调用，C6 删除 |
| `DeferredEffectDispatcher` 的 `Claim/Complete/Forget` | 明确让旧业务循环充当 Executor，Effect 没有真正接管调度 | C2-C4 替换，C6 删除 |
| `engineTurnKernel.state` 快照副本 | Coordinator 外保留第二份可查询状态 | C2 改为只读 Snapshot API，C6 删除副本 |
| `normalizeTerminalProjection` | 用 Kernel 状态修正旧 Engine 终态，而非由 Kernel 产生终态 | C4 删除 |
| Coordinator Frozen Terminal State | 为原子提交提供权威 Terminal State 与 Domain Fact | C4 起为 Foundation |

`TurnKernelObserver` 和 `TransitionRecord` 可以保留为纯诊断能力，但不得参与恢复、补写、
重试或状态纠正。

### 4.3 仍在服役的 Legacy

以下内容不能现在直接删除，必须先把行为迁入 Coordinator/Reducer/Executor：

| 旧实现 | 当前仍拥有的权力 | 目标归属 |
| --- | --- | --- |
| `engine/turn_handler.go` 主循环 | 采样、工具循环、Repair、Completion/Verification Gate、Output Release | Coordinator + Reducer |
| `engine/terminal_handler.go` | Terminal 去重、取消/失败分类、fallback | Reducer Terminal Decision + Terminal Commit Effect |
| `verifyGate` 和 `completionVerification` | Verification 时机、结果解释和 Repair 动作 | Verification Executor 返回事实，Reducer 决策 |
| `completionGateRequired` 与 Completion Feedback 分支 | Completion 必要性和下一动作 | Frozen Policy + Reducer |
| Engine 直接 `Journal.Commit/Rollback` | Journal 终态顺序 | Journal Executor + Result Command |
| Engine `State` 中终态枚举 | 第二套 Turn Phase | Kernel Phase；Engine Event 只保留非权威进度投影 |
| App `commitTerminal` fallback | 非事务 Sink 可分裂发布 Receipt/Terminal | 所有生产 Turn 强制 TerminalCommitSink |
| Runtime synthetic terminal fallback | Engine 无终态时自行构造失败 | Coordinator 显式失败；Runtime 只拒绝损坏事务 |
| Runtime 单独 `r.commit` | Operation 状态在 Envelope 外提交 | 同库事务或统一 Durable Commit Port |

### 4.4 明确缺失的能力

1. Coordinator 构造依赖由 `runtime/app/wire` 注入，而不是 Engine 内创建 Memory Store。
2. 每个生产 Transition 实时写入 SQLite Domain Facts。
3. 启动时扫描未终态 Turn，并调用生产 `RestoreTurnCoordinator`。
4. Effect Executor Registry 真正执行 Provider、Tool、Approval、Input、Verification、
   Journal 和 Terminal Commit。
5. Effect 的 Claim、Attempt、Result 和副作用幂等键具备 Durable 语义。
6. Final Output 在 Terminal Commit 前完全不可见。
7. Terminal Envelope、真实 Operation 状态和 Outbox 在一个事务边界提交。
8. Pending Outbox 在进程重启后自动继续投影。
9. 删除所有 Engine Event 到 Kernel Command 的反向同步。
10. 用行为和所有权测试替换字符串搜索式“完成门禁”。

## 5. 目标架构

```text
Host Operation
      |
      v
Runtime Command Port
      |
      v
TurnCoordinator ---- append Domain Facts ----> Durable Turn Store
      |
      +---- dispatch durable Effect ---------> Effect Executor Registry
      |                                          |
      <----------- same effect_id Result Command-+
      |
      +---- Terminal Commit Effect ----------> Atomic Commit Port
                                                |
                                                +-- Frozen Kernel State
                                                +-- Domain Facts
                                                +-- Receipt
                                                +-- Final Output
                                                +-- Terminal Event
                                                +-- Operation State
                                                +-- Projection Outbox

Projection Worker <---- committed Outbox ---- Runtime Events ----> Hosts
```

### 5.1 唯一写入规则

- 只有 `TurnCoordinator.Submit` 可以调用 Reducer 并推进 Kernel State；
- Executor 只接受 Effect，返回带相同 `effect_id` 的 Result Command；
- Engine 中不得存在 Turn Phase、Completion、Verification、Mutation、Repair、Journal
  或 Terminal 的平行决策状态；
- Runtime 不判断 Turn 应当 Completed、Failed 还是 Canceled；
- UI 和 Host 只消费 committed projection。

### 5.2 终态规则

Terminal 分为两个阶段：

1. `Committing`：Reducer 冻结 Terminal Material 并请求 Terminal Commit Effect；
2. Terminal Commit Result 成功后，Reducer 进入 `Completed/Failed/Canceled`。

Final Output、Receipt 和 Terminal Event 只能来自已提交 Outbox。Commit 失败时 Turn 保持
可恢复的 `Committing`，不能发布 fallback Terminal，也不能泄漏 Output。

### 5.3 恢复规则

- 恢复只读取当前 Schema 的 Domain Facts、Effect Ledger 和 Commit Marker；
- 不读取 Runtime Capture、Trace、UI Event 或自然语言内容推断状态；
- `running` Effect 恢复为可重领状态，Attempt 单调增加；
- 已有 Result 的副作用不得重执行；
- Terminal 已提交但 Outbox 未完成时只恢复投影；
- Fact 缺口、Digest 错误、未知 Effect 或身份冲突全部 Fail Closed。

## 6. 不变量

以下不变量必须由代码和测试共同保证：

1. 每个 State Transition 恰好由一个 accepted Command 产生。
2. State 更新前对应 Domain Facts 已持久化。
3. 每个 Effect ID 最多执行一个逻辑副作用，并恰好闭合一个 Result。
4. Tool Start/Result、Approval、Input、Verification 和 Journal 都服从相同 Effect
   Lifecycle。
5. Cancel 接受后不能创建新的 Provider、Tool 或 Verification Effect。
6. Mutation Revision 改变后旧 Completion 和 Verification 自动失效。
7. Completion、Verification、Repair 和 Terminal Decision 只有 Reducer 一个来源。
8. Terminal State 没有出边。
9. Final Output 在 Terminal Commit 成功前不可被任何 Host 观察。
10. Receipt 只读取 Frozen Kernel Terminal Material。
11. Terminal Envelope、真实 Operation Commit 和 Outbox 原子提交。
12. 重启恢复得到相同 State Digest，且不会重复已完成副作用。
13. 任意持久化或投影失败不会制造两个 Terminal。
14. App、Runtime、Host 和 UI 不从文本推断控制状态。

## 7. 执行原则

1. 按垂直切片迁移，一次只改变一种所有权，切片结束立即删除对应 Bridge。
2. 新旧路径不得同时对同一事实拥有写权；允许读比对，不允许双写兜底。
3. 每个阶段先建立会失败的行为/故障门禁，再改生产代码。
4. 不为当前 pre-release Schema 增加历史兼容迁移。
5. 不恢复 Capture Replay，不建立独立 Replay 包。
6. 不以真实 UI 手工操作替代结构化 Runtime 测试。
7. 工作区现有无关修改不纳入本路线，也不得被覆盖。

## 8. 新迁移路线

旧 Phase 4R/Phase 5 编号停止使用。新路线使用 C0-C6，每阶段只有满足机械退出条件后才能
进入下一阶段。

### C0：真相基线与门禁重建

目标：让测试准确暴露当前三控制环，而不是证明类型或字符串存在。

执行：

- 为 Coordinator Store 注入、生产 Restore 调用、Effect Executor 所有权、Final Output
  零泄漏、真实 Operation 原子提交建立失败测试；
- 建立生产写入口清单，使用 AST/类型依赖或行为测试，避免脆弱字符串包含判断；
- 删除旧 D01-D11 字符串测试，并把 no-replay 约束迁入正式 Ownership Gate；
- 冻结当前旧 Engine 行为，确保后续迁移可逐片对照。

退出：

- 所有 `Foundation/Bridge/Legacy/Missing` 项都有可执行检测；
- 常规测试保持绿色；
- 新目标门禁应按预期失败，并清楚指出实际所有权。

#### C0 可执行证据

C0 使用 `CODEHELPER_TURN_KERNEL_CONVERGENCE_EXIT_GATE` 区分两种语义：

- `make turn-kernel-convergence-baseline` 必须绿色，证明检测器能稳定观察当前事实；
- `make turn-kernel-convergence-exit-gate` 在 C0 必须失败，失败项是 C1-C6 的目标架构欠账；
- 不保留历史字符串门禁；长期架构约束必须进入 AST、类型或行为门禁。

| 证据 | 分类 | 当前事实 | 可执行检测 |
| --- | --- | --- | --- |
| F01 | Foundation | State、Command、Reducer 和不变量可独立运行 | `reducer_test.go`、`fuzz_test.go` |
| F02 | Foundation | Coordinator 是唯一生产 `Reducer.Apply` 入口，并保持 persist-before-state | `TestC0FoundationOwnershipBaseline`、`TestPhase4R3CoordinatorPersistsBeforeStateAndDispatch` |
| F03 | Foundation | Domain Fact、Terminal Envelope 和 SQLite Store 原语存在 | `terminal_envelope_test.go`、`turnstate/store_test.go` |
| C0-D03 | Foundation，C2 已解决 | Control/Tool Effects 通过路由注册表 Durable Start 并保留唯一 Result；Engine 不再手工 Claim/Complete/Forget | `TestC2ControlToolOwnershipBaseline`、`TestC2ToolEffectPersistsStartAndExactlyOneResult` |
| C0-D05 | Foundation，C3 已解决 | Verification Engine Event 不再反向推进 Kernel Command | `TestC3ModelDecisionOwnershipBaseline` |
| C0-D05 terminal | Foundation，C4 已解决 | Terminal Engine Event 不再反向推进 Kernel Command | `TestC4TerminalCommitOwnershipBaseline` |
| C0-D11 | Foundation，C4 已解决 | Coordinator State 与 Domain Fact 冻结终态材料，不再使用 `TerminalObservations` | `TestC4TerminalCommitOwnershipBaseline` |
| C0-D04 | Foundation，C3 已解决 | Reducer Action 独占 Completion、Verification 与 Repair 决策 | `TestC3ModelDecisionOwnershipBaseline` |
| C0-D08 | Foundation，C4 已解决 | Runtime 不再合成平行 Terminal fallback | `TestC4TerminalCommitOwnershipBaseline` |
| C0-D09 | Foundation，C4 已解决 | App 强制使用唯一 `TerminalCommitSink`，已删除 Receipt/Terminal 分发 fallback | `TestC4TerminalCommitOwnershipBaseline` |
| C0-D01 | Foundation，C1 已解决 | persistent wire 注入 SQLite Coordinator Runtime；Engine 不保留 Store 构造 fallback | `TestC0OwnershipFailureBaseline/C0-D01-engine-coordinator-memory-store`、`TestC1SQLiteFactFailureMatrixCommitsNoStateOrEffect` |
| C0-D02 | Foundation，C1 已解决 | 启动租约扫描调用生产 Restore；不完整或重复恢复 Fail Closed | `TestC0OwnershipFailureBaseline/C0-D02-restore-has-no-production-caller`、`TestC1DurableCoordinatorRuntimeScansRestoresAndLeasesActiveTurn` |
| C0-D06 | Foundation，C4 已解决 | Final Output 只在原子 Terminal Commit 后投影 | `TestC4FinalOutputZeroLeakBaseline` / `TestC0FinalOutputZeroLeakExitGate` |
| C0-D07 | Foundation，C4 已解决 | SQLite 在同一事务提交 Terminal Envelope 与真实 Operation | `TestC4TerminalOperationAtomicityBaseline` / `TestC0TerminalOperationAtomicityExitGate` |
| C0-D10 | Foundation，C5 已解决 | Runtime 启动扫描 Pending Terminal Projection，并复用稳定 Event ID | `TestC5RestartProjectionOwnershipBaseline` |

2026-08-11 机械结果：C0 已完成。常规聚焦测试、Race、Reducer Fuzz、Docs、Book、
Architecture Freeze 和 diff check 全绿；目标 Exit Gate 按预期稳定失败。

#### C1 输入清单

C1 已基于以下限定输入完成：

1. 在 `runtime/app/wire` 定义并注入每 Turn 的 durable Store/Dispatcher 依赖；
2. 让 Engine 构造函数失去创建 Memory Store 的生产权限；
3. 为活跃 Turn 增加 SQLite 查询、启动扫描和唯一恢复入口；
4. 定义停机、租约、并发恢复和 running Effect 重领规则；
5. 为每次 Transition 的 SQLite Fact 写入建立失败矩阵，不增加内存/SQLite 双写兜底；
6. 保持 C0 baseline 绿色，并只消除 C1 所拥有的 D01/D02 目标失败。

### C1：Durable Coordinator 构造与恢复入口

目标：每个生产 Turn 从创建开始就使用同一个 Durable Store。

执行：

- 在 `runtime/app/wire` 构造 Turn Dependencies；
- Engine 不再创建 Memory Store；
- Transition 实时写入 SQLite Domain Facts；
- 增加 Active Turn 索引、启动扫描和 `RestoreTurnCoordinator` 生产入口；
- 明确进程停机、租约和恢复并发规则。

退出：

- 杀进程后可从中间 Phase 恢复相同 Digest；
- 不完整 Fact、错误 Digest 和重复恢复 Fail Closed；
- 生产代码不存在 Turn Coordinator 的 Memory Store fallback。

2026-08-11 机械结果：C1 已完成。目标 Exit Gate 不再包含 D01/D02；SQLite 重启恢复相同
中间态 Digest；不完整、损坏、重复及并发租约恢复均 Fail Closed；running Effect 通过
Durable Reducer Transition requeue。C1 完成时，C2-C6 所有的 D03-D11 行为未改变。

### C2：Control 与 Tool Effect 垂直迁移

目标：先迁移边界清晰的 Cancel、Approval、Input 和 Tool Lifecycle。

执行：

- 用真实 Executor Registry 替换这些 Effect 的 Deferred Claim/Complete；
- Durable Effect Claim 发生在执行前，Result 持久化后闭合；
- App 只提交 Control Command；
- 删除对应 `observeToolLocked`、`resolveWaitLocked` 和手工 `Forget` 路径。

退出：

- 并发 Cancel/Result、重复 Result、Result Sink 失败和重启恢复均保持恰好一次逻辑闭合；
- 这些切片不再经过 `engineTurnKernel.observe`；
- Deferred Dispatcher 中不再支持这些 Effect Kind。

2026-08-11 机械结果：C2 已完成。目标 Exit Gate 不再包含 D03。Tool、Approval、Input
Effects 均在执行前持久化 `EffectStarted`，并在 Sink 重试期间保留唯一 Result Command。
并发 Cancel/Result、重复 Result 和重启 requeue 均只闭合一个逻辑 Effect。在 C2 边界，
这些切片不再经过 `engineTurnKernel.observe`，Deferred Dispatch 仅保留 C3-C4 Effect Kind。

### C3：Model、Completion、Verification 与 Repair 接管

目标：Coordinator 成为业务循环调度者，Engine 降级为 Executor 集合。

执行：

- Provider Sample 由 Effect 驱动；
- 模型 Text/Tool Proposal/Usage 以 Result Command 返回；
- Reducer 决定 Completion Requirement、Verification、Repair Budget 和 Next Effect；
- Verification Executor 只返回证据，不决定 Repair/Fail/Complete；
- 删除 `completionVerification`、Engine Gate 分支和自然语言反馈驱动的控制判断。

退出：

- Engine 步进泵只执行结构化 Reducer Action，不包含 Completion、Verification 或
  Repair Policy 决策；
- Mutation 后旧证据失效完全由 Reducer 测试证明；
- Provider、Verification 和 Repair 可从任意持久化边界恢复。

2026-08-11 机械结果：C3 已完成。Provider Sampling 与 Verification 均使用路由 Effect，
在执行前持久化 `EffectStarted`，并保留唯一 Result Command。
`ModelSampleResultReceived` 一次返回 Text、Tool Proposal 和 Usage；
`EvaluateTurnStep` 选择 Repair、Verify 或 Complete；`VerificationFinished` 只携带证据，
Verification Action 与 Repair Budget 由 Reducer 独占决定。Engine 已删除
`completionVerification`、手工 Repair 消耗、Verification 决策和 Verification 反向
Observer。运行中的 Model Effect 可在恢复时 Durable Requeue，Model Result 内嵌套派发
Tool Effect 不会死锁。D04 与 D05 的 Verification 部分已清零；Terminal Observer、
Journal、Output 和 Commit 所有权留给 C4。

### C4：Journal、Output 与 Terminal 原子收口

目标：消除最终输出提前发布和虚假的 Operation Commit 原子性。

执行：

- Journal Commit/Rollback 改为 Effect + Result Command；
- Reducer 生成 Frozen Terminal Material；
- Terminal Store 与 Runtime Operation Repository 共用一个 Atomic Commit Port；
- Final Output、Receipt、Terminal Event 和 Operation 状态只写入 Outbox；
- 将 Engine `terminalHandler` 所有权降级为仅投影的 terminal projector；删除 App
  非事务 fallback 和 Runtime synthetic terminal。

退出：

- 在 Fact、Journal、Receipt、Output、Terminal、Operation、Outbox、Marker 每个阶段注入失败，
  Host 均看不到部分终态；
- 数据库中不存在 Envelope committed 但 Operation pending 的状态；
- 生产路径在 Commit 前没有 Output Delta。

2026-08-11 机械结果：C4 已完成。Journal Commit/Rollback 通过路由 Effect 执行，
先持久化 Start，再以 `JournalResultReceived` 回流。Reducer 在 App 拼装前冻结
Final Output 与 Terminal State。Persistent Runtime 使用 `CommitTerminalOperation`，
在同一 SQLite 事务写入 Domain Fact、Receipt、Final Output、Terminal Event、Outbox、
Commit Marker 与真实 Operation Receipt。Output Delta、Receipt、Terminal 只在该事务
成功后投影；存在 Durable Lifecycle 但缺少原子端口时 Fail Closed。Terminal Event
反向 Observer、`TerminalObservations`、App 分开发送 fallback 与 Runtime synthetic
terminal 均已删除。目标 Exit Gate 当前只剩属于 C5 Outbox/重启恢复的 D10。

### C5：Projection 与重启恢复闭环

目标：证明提交后投影和运行中恢复都可重复、可中断。

执行：

- 启动时扫描 Pending Effects、Committing Turns 和 Pending Outbox；
- 投影使用稳定 Event ID，支持崩溃后幂等继续；
- 恢复 Approval/Input 等待、Provider Retry、Tool Running 和 Terminal Projection；
- 增加并发恢复所有权或租约，防止两个 Coordinator 同时执行。

退出：

- 在每个 Effect 和每条 Outbox 之间杀进程，恢复后状态、Receipt 和 Terminal 唯一；
- 不依赖 Capture、Trace 或 UI Event；
- Race、重复启动和 Store 瞬时失败测试通过。

2026-08-11 机械结果：C5 已完成。每个 Effect 在校验 Digest 之外持久化结构化
Payload。Runtime 启动时只重新调度同时具备非终态 Domain Fact 的 accepted StartTurn；
Engine Open 随后恢复 Coordinator 与 Durable Registry。运行中的 Provider、Tool、
Journal Effect 以递增 Attempt requeue；Committing Turn 直接接续 Journal 闭合，不返回
Model Sampling。Approval/Input Wait 从 Lifecycle Projection 预装，复用原 Request ID，
不重复发送 Required Event。Terminal Outbox Entry 携带稳定的 Event、Operation、
Thread、Turn、Item 身份；启动时全局扫描 Pending Terminal Projection，以 Event ID
唯一约束作为 Projection CAS，并在 Append 后逐条 Mark。并发和重复恢复因此只产生一份
Receipt 与 Terminal。Convergence Exit Gate 在 C5 已全绿；C6 随后删除了迁移桥接。

### C6：删除桥接与架构冻结

目标：仓库只剩一条可解释的 Turn 控制路径。

删除：

- `engineTurnKernel.observe*`、`applyLocked`、`drifted`；
- `DeferredEffectDispatcher`；
- Engine 的 Turn Phase 决策和 `terminalProjector`；
- 失效的历史完成报告；
- 所有生产 Memory Store fallback 和非事务 Terminal Sink。

最终门禁：

- Reducer 写入口唯一；
- Durable Coordinator 构造入口唯一；
- 每种 Effect Kind 的 Executor 唯一；
- Terminal Commit Port 唯一；
- 没有 Event-to-Command 反向同步；
- 全量测试、Race、Reducer Fuzz、恢复故障矩阵、Docs、Book、Architecture Freeze 和
  `git diff --check` 全绿。

2026-08-11 机械结果：C6 已完成。`TurnCoordinator` 是生产环境唯一
`Reducer.Apply` 调用方。`DeferredEffectDispatcher`、`MigrationEffectDispatcher`、
`engineTurnKernel.observe`、`applyLocked`、`drifted`、`terminalProjector`、
`normalizeTerminalProjection`、`BeginTerminal` 以及未使用的 Terminal/Outbox Effect
占位均已删除。七类 Effect 全部使用 `DurableEffectDispatcher`。Approval/Input
Command 在 Request 产生点提交，不再由 Event Projection 反向驱动。Engine 只提交
`TerminalRequested` 事实；Completed、Failed、Canceled 由 Reducer 独占选择，Engine
只投影冻结决策。Durable Runtime 未显式注入 Event、Content、Terminal Store 时
Fail Closed。C0-C6 Baseline、最终 Ownership Exit Gate、恢复矩阵、Race、Reducer
Fuzz、Docs、Book 与 Architecture Freeze 共同构成完成证据。

## 9. 验证矩阵

每个阶段至少运行：

```bash
go test ./internal/runtime/agent/turnkernel
go test ./internal/runtime/agent/engine
go test ./internal/runtime/app
go test ./internal/persist/state/turnstate
go test -race ./internal/runtime/agent/turnkernel ./internal/runtime/agent/engine ./internal/runtime/app
go test ./internal/runtime/agent/turnkernel -run=Fuzz -fuzz=FuzzReducer -fuzztime=30s
make docs-check
make book-check
make architecture-freeze
git diff --check
```

按阶段增加：

- Persistence Fault Matrix；
- Effect Crash/Retry Matrix；
- Terminal Atomicity Matrix；
- Restart/Outbox Matrix；
- 静态 Ownership Gate。

真实 Runtime Gate 和 VS Code 人工 Turn 继续暂停，直到用户明确恢复。

## 10. 当前下一步

C0-C6 收敛路线已完成。不要恢复已废止的旧 Phase 5。真实 Runtime 与 VS Code
人工 Gate 仍是独立、必须由用户明确触发的验证。

对外状态统一表述为：

> Turn Kernel 生产所有权收敛已完成，并已通过机械冻结。
