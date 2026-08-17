# State Observability 架构升级方案

> 状态：SO0-SO7 `accepted`。
>
> CodeHelper 分析基线：`38cc88710a7881a4943f05b248ff05acb0c0ad1c`；
> Codex 参考实现：
> `3bbf1fe75701c97fb190e0867002ba2d9dbda5db`。
>
> 范围：Runtime State、Domain Fact、Protocol Event、Trace、Usage、Metric、
> Diagnostic Payload、Execution Receipt、故障重建、跨进程 Trace Context、
> Observation 持久化与查询。本方案不改变 Host、Turn Kernel、Guard、Approval、
> Journal、Sandbox 或 Terminal Commit 的业务所有权。

实施进度：

- SO0：`accepted`，可执行基线见
  [`state-observability-so0-baseline.json`](../state-observability-so0-baseline.json)；
- SO1：`accepted`，证据见
  [`state-observability-so1-evidence.json`](../state-observability-so1-evidence.json)；
- SO2：`accepted`，证据见
  [`state-observability-so2-evidence.json`](../state-observability-so2-evidence.json)；
- SO3：`accepted`，证据见
  [`state-observability-so3-evidence.json`](../state-observability-so3-evidence.json)；
- SO4：`accepted`，证据见
  [`state-observability-so4-evidence.json`](../state-observability-so4-evidence.json)；
- SO5：`accepted`，证据见
  [`state-observability-so5-evidence.json`](../state-observability-so5-evidence.json)；
- SO6：`accepted`，证据见
  [`state-observability-so6-evidence.json`](../state-observability-so6-evidence.json)；
- SO7：`accepted`，证据见
  [`state-observability-so7-evidence.json`](../state-observability-so7-evidence.json)。

SO0 实测冻结了 50 个 Protocol Event Kind、7 个业务权威域、15 层现有 Identity、
9 条观测表面、10 个标准 Hermetic Session 和 13 个故障注入场景。当前 50 个 Event
Kind 均有 Trait。SO0 发现 `reasoning.delta` 与 `turn.compaction` 存在 2 个声明/落盘
漂移；SO1 已将前者明确为 `transient`、将后者按 `retained` 真实落盘，并删除
`ShouldPersist` 的手工 Kind 列表。代表性 Turn State 为 1434 bytes，携带完整 State
的 Domain Fact 为 1605 bytes。

SO2 已建立独立于 Authority Plane 的增量 Observation Journal、有界 Priority Router、
Payload-first CAS 和 Health Model。Kernel、Provider、Tool、Approval、Verification 与
Terminal 已接入最小 Tap；Crash-critical Start 同步持久化，未完成 Span 在重放时保持
Open，不伪造 Completed。稳定 A/B 基准中 Turn 延迟回退 0.50%，Allocation 回退
2.28%，均通过阶段 Gate。

SO3 已建立确定性 Semantic Reducer 与可重建 Projection DB。Reducer 只消费 Canonical
Journal Sequence，将 Runtime、Turn、Inference、Tool Attempt、Effect、Approval、
Terminal、Verification 和 Agent Interaction 归约为统一图；Missing End 保持 Open，
冲突与证据缺失分别进入 Inconsistency 和 Unknown。Runtime Result 不会自动成为
Model-visible Result，只有显式 Observation 才生成 Visibility Edge。

SO4 已将 Usage、Latency、Receipt、Terminal Envelope 与 Trace 的冻结点收敛为
`TerminalMeasurementSnapshot`。模型调用和 Tool 内模型调用的 Usage 都先进入 Kernel
Domain Fact；Terminal Commit 前一次性冻结 Root Span，Receipt 与 Trace 只消费同一份
Measurement。提交后的持久化与清理不再修改 Turn Total，而进入独立 Cleanup Span。

SO5 已使用官方 OpenTelemetry W3C Propagator 贯通 Provider HTTP/WebSocket、MCP
HTTP/stdio、受信 Runtime Helper、Workflow、Worker 与 Subagent。Canonical
Observation Journal 成功追加后才进入有界 OTEL Projector；In-memory、OTLP
HTTP/protobuf 和 OTLP gRPC Exporter 均通过本地 Collector 测试。Exporter、队列或非法
Trace Context 故障不改变业务结果，普通用户命令不会收到内部 Trace Context。

## 1. 执行摘要

CodeHelper 已经具备强于一般 Coding Agent 的状态正确性基础：

- `TurnCoordinator` 是 Reducer 的唯一生产入口；
- 每次已接受的状态转换先持久化 Domain Fact，再更新内存 State 或派发 Effect；
- Domain Fact 保存完整 State 与 SHA-256 Digest，恢复时验证顺序、状态不变量和摘要；
- Terminal Envelope 原子封存最终 State、Domain Facts、Session Delta、Receipt、
  Operation Commit 和 Projection Outbox；
- Workspace Journal 独立证明文件系统副作用；
- Protocol Event 使用稳定 ID、全局 Cursor、Traits 和持久化 Event Log；
- Usage、Trace、Diagnostics、Verification 和 Receipt 已有类型化数据结构。
- Durable WorkGraph 已将 Run、Node、Attempt、Effect、Lease 和 Outbox 收敛到独立的
  Kernel、Fact Store 和 Projection；
- SG7 Tool Attempt Receipt 已绑定 Effective Permission Profile Digest；
- Extension Control/Lifecycle 已具备持久 Receipt 和最多 256 条的进程内诊断 Trace。

因此，CodeHelper 当前的首要问题不是“没有状态”或“无法恢复”，而是：

> 权威状态很强，但状态为何变化、变化经过哪些执行边界、模型实际看到了什么、进程崩溃
> 前已经发生了什么，仍需人工跨 Domain Fact、Event、SQLite、Trace、Receipt、Runtime
> Capture 和日志进行拼接。

Codex 在这方面最值得借鉴的不是其可变 `SessionState` 或 Rollout 作为业务状态，而是
独立的诊断平面：

1. 先记录原始证据，再离线解释；
2. 原始 Payload 与有序事件骨架分离；
3. 将 Conversation、Inference、Tool、Terminal、Code Cell 和 Agent Interaction
   还原成语义图；
4. 明确区分 Runtime 观察到的内容与模型实际可见的内容；
5. 使用 W3C Trace Context 贯穿 HTTP、进程、远端执行和子任务；
6. 通过 OpenTelemetry 输出标准 Log、Metric 和 Trace；
7. 保持查询投影可重建，投影只能落后于 Canonical Log，不能领先。

本方案保留 CodeHelper 的状态权威链，新增一个严格旁路的
`Observation Plane`：

```text
Business Authority Plane
  Operation -> Turn Kernel -> Domain Fact -> Effect -> Terminal Envelope
                     |              |          |
                     +--------------+----------+
                                    |
                           typed observation taps
                                    |
                                    v
Observation Plane
  ObservationEnvelope -> Payload CAS -> Observation Journal
                                      -> Semantic Reducer
                                      -> Query Projection
                                      -> OTEL Export
                                      -> CLI / TUI / VS Code
```

核心约束是：

> Observation 可以失败、延迟、降采样或被删除，但绝不能批准操作、推进 State、派发
> Effect、改变 Terminal 结果或成为恢复业务执行的依据。

## 2. 当前实现机制

### 2.1 Turn Kernel 状态权威

`internal/runtime/agent/turnkernel` 定义确定性的 Turn State Machine。State 覆盖：

- Phase、Intent、Mode、Profile Revision 和 Policy；
- Open/Closed Tool Call；
- Pending Approval 和 Input；
- Mutation Revision、Observed Change 和 Journal Status；
- Completion、Verification 和 Terminal Decision；
- Usage、Context、Sample Ledger 和 Provider Retry；
- Pending/Completed Effect；
- Repair Budget、Progress 和 Next Action；
- Provisional/Final Output；
- Cancellation 和 Recovery Relation。

`TurnCoordinator.transition` 的顺序为：

```text
Reducer.Apply(oldState, command)
 -> validate and digest newState
 -> construct Domain Facts
 -> durable AppendDomainFacts
 -> publish newState in memory
 -> dispatch Effects
```

该顺序保证持久化失败不会让内存 State 或外部 Effect 越过事实提交。

### 2.2 Domain Fact 与恢复

每个 Domain Fact 包含：

- Turn ID；
- Turn-local Sequence；
- Command Name；
- 可选 Kernel Event；
- 转换后的完整 State；
- State Digest。

恢复时按 Sequence 重放并验证每个 State，而不是重新执行 Reducer 猜测历史。处于
`running` 的 Effect 会先转换为 `requeued`，再由 Dispatcher 按持久化 Payload 恢复。

这是业务恢复权威，后续 Observation Reducer 不得替代或重新解释该状态。

### 2.3 Terminal Envelope

Terminal Commit 是 Turn 的最终原子边界，负责一致提交：

- Frozen Kernel State；
- 完整 Domain Facts；
- Session Delta；
- Final Output；
- Execution Receipt；
- Terminal Event；
- Projection Outbox；
- Operation Commit Receipt。

Terminal Envelope 有稳定 Digest 和 Effect ID，重复提交必须得到相同 Marker。终态提交
后，Store 拒绝新的 Domain Fact。

### 2.4 Protocol Event 与 Event Hub

`eventhub.Hub` 当前负责：

- 分配全局单调 Cursor；
- 追加 Event Store；
- Stable Event ID 幂等恢复；
- 执行 Runtime Lifecycle Projection；
- Replay 与 Subscriber Fanout；
- Slow Subscriber Drop；
- Hub Snapshot。

`event_traits.json` 为每个 Event Kind 声明：

- Class；
- Item Owner；
- Durability；
- Correlation Kind；
- Terminal。

Traits 已用于 Go、TypeScript、Protocol Schema 和 Host Projection 生成，是统一
Observation Schema 的良好基础。

### 2.5 Event Log、SQLite 与 CAS

当前持久化由三部分组成：

| 组件 | 当前职责 |
| --- | --- |
| `events-v1.jsonl` | 有序 Protocol Event 事实与字节 Digest |
| `state-v1.db` | Session、Turn、Task、Usage、Trace 等查询投影 |
| `cas-v1` | 大型不可变 Content 与 Snapshot Payload |

Event Append 使用 Reservation 协议协调 JSONL 与 SQLite：

```text
reserve sequence in SQLite
 -> append and fsync JSONL
 -> commit event index/projection
 -> mark reservation committed
```

启动 Reconcile 可以修复“Event 已落盘但 Projection 未完成”，但会拒绝“Projection
存在而 Canonical Event 不存在”的不一致。

### 2.6 Trace

`internal/observability/trace` 当前实现 Turn-local Span Tree：

- `turn`；
- `model_call`；
- `tool`；
- `approval_wait`；
- `verify`；
- `turn_kernel_transition`。

Span 使用 Turn 内递增 ID，保存 Parent、Started、Ended、Status 和 Attributes。Trace
在 Turn 结束时一次性写入 SQLite；同一 Turn 重写会替换旧 Span。

### 2.7 Usage、Cost 与 Receipt

Usage 由 Protocol Usage Event 投影，每个 `(turn_id, sample)` 保存一行：

- Provider、Model；
- Input、Output、Reasoning、Cached Token；
- Cost Microunits 与 Cost Known；
- Source Event Sequence。

Execution Receipt 在应用层聚合：

- Route 与 Retry；
- Context Selection 与 Budget；
- Tool Execution；
- Verification；
- Workspace Change；
- Usage、Cost 与 Latency；
- Final Workspace Outcome。

Receipt 是用户可读的终态解释，不是比 Domain Fact、Event 或 Journal 更高的事实来源。

### 2.8 Metrics、Log 与 Runtime Capture

当前 Metrics 是进程内 Atomic Counter 和少量累计时长，Runtime Snapshot 或文件导出时
读取。Structured Logger 会递归脱敏。VS Code Runtime Capture 额外记录：

- ACP Request 生命周期；
- Runtime Event；
- stderr、进程退出和 Supervisor State。

这些来源均有价值，但没有统一 Identity、Retention、Schema Version 或 Reducer。

### 2.9 ContextLedger

`internal/runtime/agent/contextstore.Ledger` 是每次模型 Sample 组装的唯一 Authority：

- Stable、History、Dynamic 和 Continuation Partition；
- Tool Definitions；
- 单调 Revision；
- 稳定 Context Item ID；
- 完整 Model-visible Snapshot Digest。

Terminal Session Delta 负责持久化后续 Turn 使用的 History 与 Context 状态。当前已经能
确定某次 Sample 的权威上下文快照，但 Runtime Tool/Process Evidence 到具体
Model-visible Item 之间还没有独立、可查询的 Visibility Edge。SO3 应引用
ContextLedger Item ID 和 Snapshot Digest，而不是重新推断模型可见内容。

### 2.10 Durable WorkGraph

`internal/orchestration/kernel` 已成为 Task、Workflow、Subagent、Automation 和
Background Command 的生命周期权威。每个 WorkGraph Command 通过 Expected Revision
和 Authority Digest 执行，Store 在同一 SQLite 事务中提交：

- Graph Snapshot；
- WorkGraph Facts；
- Command Receipt；
- Effect Outbox；
- Host Projection。

Store 可以从 Facts 重建 Graph，并通过 Snapshot/Replay Digest 检测漂移。Observation
Reducer 必须引用 `RunID`、`NodeID`、`AttemptID`、`EffectID` 和 Fact Sequence，不得
在 Observation Plane 中重建第二套编排状态机。

### 2.11 Extension Observability

Extension Control 当前维护独立的：

- Operation/Receipt/Revision；
- Control Event Sequence；
- Operations、Committed、Failed、Duplicate、Revoke 和 Subscriber Drop Metrics；
- 最近 256 条 Control Trace；
- 按 Alert Code 聚合的告警。

它解决了 Extension 域内的有界诊断问题，但 Trace 仅在内存中、没有跨进程 Context，
也未与 Turn、WorkGraph、Protocol Event 或全局 Trace 统一。

## 3. 当前设计优势

以下能力必须保留，不能为接近 Codex 而降级：

| 能力 | CodeHelper 当前优势 |
| --- | --- |
| Reducer Authority | `TurnCoordinator` 是唯一生产入口 |
| Transition Durability | State 先持久化，Effect 后派发 |
| State Integrity | 每个 Domain Fact 携带完整 State Digest |
| Terminal Atomicity | State、Receipt、Session Delta、Outbox 同事务提交 |
| Side-effect Evidence | Workspace Journal 独立证明写入与回滚 |
| Stable Event Identity | Terminal Projection 支持稳定 Event ID 恢复 |
| Protocol Traits | Event Class、Owner、Durability、Correlation 有生成门禁 |
| Usage Accounting | Sample 级幂等累计，不把未知价格当零成本 |
| Typed Failure | Provider、Tool、Sandbox、Verification 已有结构化结果 |
| WorkGraph Authority | Run/Node/Attempt/Effect 可从 Facts 确定性重建 |
| Permission Evidence | SG7 Receipt 将每个 Tool Attempt 绑定权限摘要 |
| Extension Control | 控制事件、Receipt、Revision 和回滚语义已类型化 |
| Host Boundary | Host 只提交 Operation 和消费 Event |

Codex 的 Rollout、SQLite 或 Trace 不能成为上述权威链的替代品。

## 4. Codex 参考机制

### 4.1 Observe First, Interpret Later

Codex Rollout Trace 热路径只记录：

- 有序 Raw Trace Event；
- 大型 Payload 的引用；
- Thread、Turn 和 Runtime Object Identity；
- 开始、结束和关联边界。

离线 Reducer 再将证据转换为语义图。热路径不需要在所有源对象都已出现前做最终关联，
Reducer 可以暂存未配对事件，直到 Model-visible Source 或 Delivery Target 出现。

### 4.2 Raw Event Spine 与 Payload

一个 Trace Bundle 包含：

```text
manifest.json
trace.jsonl
payloads/*.json
state.json
```

关键不在文件名，而在 Payload-first 不变量：

```text
persist payload
 -> receive stable payload reference
 -> append event referencing payload
```

因此不会出现 Event 指向尚未持久化的证据。

### 4.3 Semantic Graph

Codex Reduced State 明确建模：

- Agent Thread；
- Codex Turn；
- Conversation Item；
- Inference Call；
- Tool Call；
- Terminal Session 与 Operation；
- Code Cell；
- Compaction 与 Compaction Request；
- Agent Interaction Edge；
- Raw Payload Reference。

它可以回答普通 Transcript 无法回答的问题：

- 哪个 Inference Response 产生了某个 Tool Call；
- 某段输出是 Runtime 结果还是模型实际收到的 Tool Output；
- 哪个 Terminal Operation 创建或复用了进程；
- 哪个父 Agent 操作向哪个子 Agent 传递了任务或结果。

### 4.4 Model Visibility

Codex 将以下事实分开：

```text
Runtime observed bytes
Model request-visible bytes
Model response-visible bytes
Caller-facing formatted result
UI projected summary
```

Runtime Payload 不能证明模型看到相同字节。Conversation Item 主要从模型请求和响应
Payload 推导，Tool/Terminal 对象只通过显式 Observation Edge 与其关联。

### 4.5 W3C Trace Context

Codex 使用标准 `traceparent` 和 `tracestate`：

- 从环境变量恢复 Parent Context；
- 向 HTTP Header 注入；
- 跨 exec-server RPC 传递；
- 为 Server Span 设置远端 Parent；
- 在后台 Task 中保留当前 Span；
- 对非法 Trace Context Fail Open，并记录 Warning。

### 4.6 OpenTelemetry

Codex 提供：

- OTLP Log、Metric、Trace Exporter；
- Session-scoped Telemetry Metadata；
- Counter、Histogram、Duration 和 Timer；
- In-memory Exporter 测试；
- Explicit Shutdown/Flush；
- Low-cardinality Tag Sanitization；
- Log-only 与 Trace-safe Event 分流。

### 4.7 Canonical Rollout 与可重建查询

Codex Paginated Thread History 遵循：

```text
flush canonical rollout
 -> materialize newline-terminated records
 -> advance byte offset
 -> update rebuildable SQLite projection
```

SQLite 可以落后，但不能领先 Canonical JSONL。尾部 Partial Record 留给下次投影。

## 5. 不直接复制 Codex 的部分

### 5.1 可变 Session/Turn State

Codex 的 `SessionState` 和 `TurnState` 主要是进程内可变对象，包含 Approval Sender、
Cancellation Handle、History 和 Runtime Services。它不具备 CodeHelper Domain Fact
和 Terminal Envelope 的状态恢复强度。

### 5.2 Rollout 不作为业务权威

Codex Rollout 适合 Transcript 和 Session Resume，但不能替代：

- Reducer State；
- Effect Lifecycle；
- Approval Resolution Ledger；
- Workspace Journal；
- Terminal Atomic Commit。

### 5.3 手工持久化枚举

Codex `should_persist_event_msg` 通过大型 `match` 手工区分 Durable 与 Transient Event。
新 Event 必须人工更新分支，且部分 Error、Warning 和 Tool Runtime Event 默认不进入
Rollout。CodeHelper 应由一个 Schema Manifest 生成全部策略。

### 5.4 Best-effort Trace 的边界

Codex Trace 写入失败不会影响业务，这是正确的；但 CodeHelper 不能让用户可见 Receipt
依赖 Best-effort Trace，否则会削弱 Terminal Commit 的完整性。

### 5.5 高敏 Raw Payload 默认关闭

Codex Trace Bundle 可包含 Prompt、Response、Tool 参数、Terminal 输出和路径。
CodeHelper 必须将 Raw Capture 设为显式 Opt-in，并增加分类、容量、TTL 和脱敏策略。

## 6. 差距与根因

| 优先级 | 差距 | 根因 | 影响 |
| --- | --- | --- | --- |
| Closed/SO1 | Event Durability 声明与实际策略双写 | Traits 与 `ShouldPersist` 分离 | 已收敛为 `Durability.Persisted()` |
| P0 | 崩溃前 Trace 整体丢失 | Turn 结束时一次性写 Span | 无法定位卡死、Crash 和强制退出 |
| P0 | Observation Identity 分散 | Event、Fact、Span、Sample 各自编号 | 调查依赖人工 Join |
| P0 | 无 Model Visibility 证据 | Runtime Event 与模型请求分别记录 | 无法证明模型实际看到哪些内容 |
| P1 | 无跨进程 Trace Context | Span ID 仅 Turn-local | Provider、MCP、Process、Child 链路断裂 |
| P1 | 无确定性故障 Reducer | 当前依赖 Runbook 和人工查询 | 同一故障可能得到不同解释 |
| P1 | Metrics 仅进程内累计 | 无标准 Instrument/Exporter | 重启归零，难做 P95/P99 与告警 |
| P1 | Receipt 与 Trace 冻结点不同 | Receipt 早于 Root Span Close | 两种延迟记录可能不一致 |
| P1 | Observation 故障混入通用 Error | 没有独立健康模型 | 无法判断业务错误还是监测失效 |
| P1 | WorkGraph 与 Turn Trace 分离 | Run/Node/Attempt ID 未进入 Trace | 多 Agent 调查仍需跨 Store 手工拼接 |
| P1 | Extension Trace 仅进程内有界保存 | Extension 自建 Metrics/Trace | 重启后丢失且无法加入全局因果图 |
| P2 | Domain Fact 保存完整 State | 每次 Transition 全量复制 | 长 Turn 写放大和查询成本上升 |
| P2 | Event Hub 持锁执行持久化与投影 | 顺序与 Fanout 共用 Mutex | 慢 Store 会阻塞所有 Publisher |
| P2 | Capture、Dump、Trace 各自管理敏感数据 | 无统一 Data Class | Retention 和分享风险不一致 |

### 6.1 SO1 已关闭的 Durability 漂移

SO0 发现 `event_traits.json` 将以下 Event 标记为 `retained`，但旧
`eventlog.ShouldPersist` 通过独立分支将其丢弃：

- `reasoning.delta`；
- `turn.compaction`。

SO1 已完成：

- `reasoning.delta` 明确声明为 `transient`；
- `turn.compaction` 保持 `retained` 并进入 Event Log；
- `ShouldPersist` 只调用 `traits.Durability.Persisted()`；
- 未知 Event 继续默认保留；
- 50/50 Event Kind 的声明与实际策略一致。

### 6.2 Trace Crash Hole

当前 Trace Recorder 在内存中收集所有 Span，`endTrace` 时才整体写入。若进程在以下
位置退出：

- Provider Request 已发送但尚未返回；
- Tool Process 仍运行；
- Approval 正在等待；
- Terminal Commit 正在执行；

SQLite 中不会出现该 Turn 的任何 Span。恢复后同 Turn 再写 Trace 还会覆盖先前
Attempt，而不是形成 Attempt Timeline。

### 6.3 语义关联缺失

当前可以通过 Call ID、Turn ID 和 Event Cursor 手工关联多数事实，但没有一个稳定对象
表达：

```text
Inference Attempt
 -> Response Item
 -> Tool Proposal
 -> Guard Attempt
 -> Approval
 -> Process / MCP Operation
 -> Runtime Result
 -> Model-visible Tool Result
 -> Completion Decision
```

这使复杂错误难以自动归因到 Provider、Runtime、Tool、Security 或 Host Projection。

## 7. 目标

升级完成后必须实现：

1. 所有 Observation 使用统一、版本化 Envelope；
2. 每个 Observation 可关联 Session、Thread、Turn、Operation、Fact、Effect、Sample、
   Call、Attempt 和 Parent Span；
3. Event Durability、Payload Policy、Redaction 和 Retention 由单一 Manifest 生成；
4. Crash 后保留已开始但未结束的 Span 和 Runtime Object；
5. Runtime Evidence 与 Model-visible Evidence 明确分离；
6. 一个确定性 Reducer 可以从 Canonical Observation 重建语义图；
7. Provider、MCP、Process、Worker 和 Subagent 支持 W3C Trace Context；
8. Metrics 支持标准 Counter、Histogram 和 OTLP/In-memory Exporter；
9. Receipt 与持久化 Trace 使用同一个 Terminal Measurement Snapshot；
10. Observation 故障对业务状态和终态结果零影响；
11. Raw Payload 有明确的安全等级、容量上限、TTL 和删除语义；
12. 长 Turn 的状态与观测写放大可量化并受 Gate 约束；
13. CLI、TUI、VS Code 和 ACP 消费同一查询模型；
14. Architecture Ratchet 不下降。

## 8. 非目标

本升级不：

- 用 Event Sourcing 重写 Turn Kernel；
- 用 Observation Reducer 恢复或推进业务 State；
- 用 OTEL Backend 作为本地 Runtime 的可用性依赖；
- 将 Raw Prompt、Secret 或完整 Tool Output 默认上传；
- 把 Host 变成 Trace 或状态权威；
- 为每个文件路径、Call ID 或错误文本创建 Metric Label；
- 在第一阶段重写全部 SQLite Schema；
- 引入远端执行、远端状态存储或中心化控制面；
- 复制 Codex 的全部 Rollout/Thread Store 兼容层；
- 为未发布格式维护永久双写迁移。

## 9. 设计原则

1. **Authority before observability**：先说明事实所有者，再决定如何观察。
2. **Observation never commands**：Observation 不得产生 Command 或 Effect。
3. **Record raw, reduce later**：热路径记录类型化证据，复杂关联交给 Reducer。
4. **Payload before reference**：Payload 必须先持久化，再追加引用它的 Observation。
5. **One schema source**：Durability、Privacy、Retention 和 Correlation 由同一 Manifest
   生成。
6. **Visibility is explicit**：Runtime Seen、Model Seen、User Seen 不互相推断。
7. **Sequence over clock**：因果排序优先使用 Sequence，Wall Clock 只用于展示和延迟。
8. **Attempt is first-class**：Retry、Resume 和 Recovery 不覆盖旧 Attempt。
9. **Missing is not zero**：未测量不转换为零延迟、零成本或成功。
10. **Bounded by default**：队列、Payload、Label、Span Attribute 和 Retention 均有界。
11. **Local first**：本地 Journal 和 Reducer 不依赖外部 Telemetry 服务。
12. **Export is secondary**：OTEL 是可选 Projection，不是 Canonical Evidence。
13. **Privacy by construction**：高敏内容进入 CAS 前就应用 Capture Policy。
14. **No permanent dual path**：阶段完成后删除被替代的手工策略和旧 Writer。

## 10. 目标架构

```text
Hosts
  | Operation                                      Query / Replay
  v                                                     ^
Runtime Dispatcher                                     |
  |                                                     |
  v                                                     |
Turn Coordinator ---- Domain Facts ---- Terminal Envelope
  |                     |                    |
  | typed taps          | references         | frozen measurements
  v                     v                    v
Observation Router -----------------> ObservationEnvelope
  |                                         |
  | payload                                 | ordered metadata
  v                                         v
Content CAS <------------------------ Observation Journal
                                              |
                         +--------------------+-------------------+
                         |                    |                   |
                         v                    v                   v
                 Semantic Reducer      OTEL Projector      Health Monitor
                         |                    |                   |
                         v                    v                   v
                 Observation DB       Logs/Metrics/Trace   Drop/Failure Facts
                         |
          +--------------+---------------+
          |              |               |
          v              v               v
         CLI            TUI          VS Code / ACP
```

### 10.1 三个平面

#### Authority Plane

包括：

- Operation Admission；
- Turn Kernel State；
- Domain Facts；
- Durable Effects；
- Terminal Envelope；
- Workspace Journal；
- Security Authority Profile。

该平面继续决定“允许什么、执行什么、最终状态是什么”。

#### Observation Plane

包括：

- Observation Envelope；
- Incremental Span Lifecycle；
- Raw/Summarized Payload Reference；
- Semantic Reducer；
- Query Projection；
- OTEL Projection。

该平面只回答“发生了什么、为什么、证据在哪里”。

#### Presentation Plane

CLI、TUI、VS Code 和 ACP 只读取 Query Projection 或 Protocol Event，不直接解释 Raw
Payload，也不自建状态真相。

## 11. 权威矩阵

| 事实 | 唯一 Authority | Observation 表达 |
| --- | --- | --- |
| Turn Phase | Turn Kernel State | State transition node |
| Effect 是否应执行 | Pending Effect + Lease | Effect lifecycle edge |
| Turn 是否完成 | Terminal Envelope | Terminal observation |
| 文件是否被修改 | Workspace Journal + after-image | Mutation node |
| 操作是否获批 | Approval/Grant Ledger | Approval decision node |
| WorkGraph 状态 | Orchestration Kernel + WorkGraph Facts | Run/Node/Attempt lifecycle node |
| Extension 状态 | Extension Control/Lifecycle Store | Extension operation node |
| 模型请求内容 | Logical Model Request | Model-visible request payload |
| Provider 返回内容 | Provider Adapter Result | Response payload |
| Tool Runtime 输出 | Tool Executor Outcome | Runtime result payload |
| 模型收到的 Tool Result | Context Ledger Projection | Model-visible result payload |
| Token/Cost | Usage Event + Route Price | Inference accounting node |
| 延迟 | Frozen Measurement + Span | Execution window |
| UI 展示 | Host Projector | Presentation observation |
| Trace Export 状态 | Observation Health | Export outcome |

Observation Reducer 发现冲突时必须：

1. 保留冲突双方；
2. 标注 Authority Rank；
3. 输出 `inconsistent` 或 `indeterminate`；
4. 不修改 Authority Plane。

## 12. 核心数据模型

### 12.1 ObservationEnvelope

建议在 `internal/observability/observation` 定义：

```go
type Envelope struct {
    SchemaVersion uint32
    ID            ObservationID
    Kind          Kind
    Sequence      uint64
    RecordedAt    time.Time
    MonotonicNS   uint64

    Identity      Identity
    Trace         TraceContext
    Causality     Causality
    Policy        DataPolicy
    Payload       *PayloadRef
    Summary       json.RawMessage
}

type Identity struct {
    RuntimeID    string
    SessionID    string
    ThreadID     protocol.ThreadID
    TurnID       protocol.TurnID
    OperationID  protocol.OperationID
    RunID        protocol.RunID
    NodeID       protocol.NodeID
    AttemptID    protocol.AttemptID
    EventID      protocol.EventID
    EventCursor  protocol.Cursor
    FactSequence uint64
    EffectID     string
    SampleID     string
    CallID       string
    Attempt      uint32
    AgentID      string
    ExtensionOperationID string
}
```

约束：

- `ObservationID` 全局唯一且稳定；
- `Sequence` 只在一个 Observation Journal 内单调；
- `EventCursor` 和 `FactSequence` 是引用，不复制其权威；
- 高基数 Identity 不进入 Metric Label；
- Summary 必须是该 Kind 的类型化、限长结构，而非任意 JSON。

### 12.2 TraceContext

```go
type TraceContext struct {
    TraceID     [16]byte
    SpanID      [8]byte
    ParentSpan  [8]byte
    TraceFlags  byte
    TraceState  string
}
```

内部 Trace ID 使用标准 16 Byte，导出时编码为 W3C Hex。Protocol 仅在需要跨 Runtime
边界时携带 `traceparent`/`tracestate`，不把 Trace Context 变成 Event Identity。

### 12.3 Causality

```go
type Causality struct {
    ParentObservationID ObservationID
    Links               []Link
}

type Link struct {
    Relation string
    Target   ObservationID
}
```

第一版允许的 Relation 必须是闭集：

- `caused_by`；
- `produced`；
- `observed_by`；
- `delivered_to`；
- `retried_from`；
- `recovered_from`；
- `projected_as`；
- `verified_by`；
- `committed_with`。

### 12.4 PayloadRef

```go
type PayloadRef struct {
    Digest        string
    MediaType     string
    Encoding      string
    OriginalBytes uint64
    StoredBytes   uint64
    Truncated     bool
    DataClass     DataClass
    Redaction     RedactionStatus
}
```

PayloadRef 使用现有 CAS Digest，不引入第二套 Content Identity。Observation Store 只保存
引用和限长 Summary。

### 12.5 DataClass

```go
type DataClass string

const (
    DataPublicMetadata DataClass = "public_metadata"
    DataOperational    DataClass = "operational"
    DataWorkspace      DataClass = "workspace_content"
    DataConversation   DataClass = "conversation_content"
    DataCredential     DataClass = "credential"
    DataRestricted     DataClass = "restricted"
)
```

规则：

- `credential` 禁止持久化 Payload，只能记录类型化 Presence；
- `restricted` 默认禁止 Capture；
- Workspace、Conversation 必须显式开启 Raw Capture；
- Summary 也必须通过字段级 Redaction。

### 12.6 Observation Trait Manifest

新增一个生成源，例如：

```text
internal/observability/schema/observation_traits.json
```

每个 Observation Kind 声明：

```json
{
  "model.request.started": {
    "owner": "provider_adapter",
    "durability": "retained",
    "payload": "optional_sensitive",
    "retention": "diagnostic",
    "correlation": ["turn", "sample", "attempt"],
    "otel": "span_start"
  }
}
```

生成：

- Go Kind 与 Traits Table；
- JSON Schema；
- Redaction Policy Table；
- Retention Class Table；
- OTEL Mapping；
- TypeScript Traits；
- Golden；
- `event_traits.json` 到 Observation Trait 的兼容映射。

### 12.7 TerminalMeasurementSnapshot

为消除 Receipt 与 Trace 的冻结点差异，引入：

```go
type TerminalMeasurementSnapshot struct {
    Version       uint32
    FrozenAt      time.Time
    TurnLatency   DurationMeasurement
    FirstOutput   OptionalDuration
    Provider      AggregateMeasurement
    Tool          AggregateMeasurement
    ApprovalWait  AggregateMeasurement
    Verification  AggregateMeasurement
    Persistence   AggregateMeasurement
    Usage         turnkernel.UsageState
    Digest        string
}
```

该 Snapshot 在 Terminal Envelope 构建前冻结，并同时用于：

- Execution Receipt；
- Terminal Envelope Digest；
- 最终 Trace Span；
- Usage/Latency Query Projection。

Observation Writer 可以晚于 Terminal Commit，但不能改变 Measurement Snapshot。

## 13. Observation Kind 分类

### 13.1 Runtime Lifecycle

- `runtime.started`；
- `runtime.ready`；
- `runtime.degraded`；
- `runtime.stopping`；
- `runtime.stopped`；
- `runtime.crashed`。

### 13.2 Operation 与 Turn

- `operation.accepted`；
- `operation.rejected`；
- `turn.started`；
- `turn.transition.committed`；
- `turn.terminal.prepared`；
- `turn.terminal.committed`；
- `turn.recovered`。

### 13.3 Effect

- `effect.requested`；
- `effect.started`；
- `effect.result.retained`；
- `effect.finished`；
- `effect.requeued`。

### 13.4 Model 与 Context

- `model.request.prepared`；
- `model.request.sent`；
- `model.first_output`；
- `model.response.completed`；
- `model.request.failed`；
- `model.retry.scheduled`；
- `context.projected`；
- `context.compacted`；
- `context.visibility.committed`。

### 13.5 Tool 与 Process

- `tool.proposed`；
- `tool.admitted`；
- `tool.started`；
- `tool.runtime.output`；
- `tool.result.produced`；
- `tool.result.model_visible`；
- `tool.finished`；
- `process.started`；
- `process.exited`；
- `process.teardown.completed`。

### 13.6 Security 与 Interaction

- `guard.decision`；
- `approval.requested`；
- `approval.resolved`；
- `authority.profile.bound`；
- `sandbox.attempt.started`；
- `sandbox.denied`；
- `egress.requested`；
- `egress.decided`。

### 13.7 Verification 与 Mutation

- `journal.opened`；
- `mutation.observed`；
- `journal.committed`；
- `journal.suspended`；
- `journal.rolled_back`；
- `verification.started`；
- `verification.finished`。

### 13.8 Agent 与 Orchestration

- `agent.spawned`；
- `agent.task.delivered`；
- `agent.message.sent`；
- `agent.result.delivered`；
- `agent.closed`；
- `workflow.node.transitioned`。

## 14. 采集管线

### 14.1 Observation Tap

Tap 必须位于事实所有者内部或其提交出口：

| Tap | 位置 | 只允许观察 |
| --- | --- | --- |
| Kernel Tap | `TurnCoordinator` 持久化成功后 | Fact Sequence、State Digest、Kernel Event |
| Event Tap | Event Hub Append/Projection 成功后 | Event ID、Cursor、Traits |
| Provider Tap | Provider Adapter 边界 | Request/Response/Attempt/Usage |
| Tool Tap | Guard 与 Typed Executor | Admission、Attempt、Outcome、Teardown |
| Journal Tap | Workspace Journal | Mutation、Commit、Suspend、Rollback |
| Terminal Tap | Terminal Publisher | Envelope Digest、Outbox、Publication |
| Host Tap | Runtime API 边界 | Request、Response、Disconnect、Projection |

禁止：

- Host 直接记录“Tool 执行成功”；
- Reducer 从文本猜测 Approval 或 Sandbox 结果；
- Provider Dump 反向改变 Retry；
- Observation Tap 在业务提交前阻塞等待远端 Exporter。

### 14.2 Router

`ObservationRouter` 提供：

```go
type Router interface {
    Record(context.Context, observation.Record) Receipt
    Flush(context.Context) error
    Snapshot() HealthSnapshot
}
```

`Record` 返回的 Receipt 只描述 Observation Admission：

- accepted；
- sampled；
- payload_dropped；
- queue_full；
- writer_failed；
- disabled。

调用方不得根据该 Receipt 改变业务结果。

### 14.3 有界队列

建议：

- Metadata Queue：4096 条；
- Payload Queue：按 32 MiB 总字节预算；
- 单 Payload 默认 256 KiB；
- Tool/Process Stream 使用 Head/Tail 或 Content Handle；
- Queue Full 时优先保留 Lifecycle、Terminal 和 Failure Metadata；
- Raw Stream Delta 可降采样；
- Drop 必须增加低基数 Metric，并写入下一条可用的 Health Observation。

### 14.4 写入顺序

```text
normalize and redact payload
 -> apply size/admission policy
 -> write payload to CAS
 -> build Envelope with PayloadRef
 -> append Observation Journal record
 -> enqueue reducers/exporters
```

若 CAS 写入失败：

- Envelope 可以记录 `payload_status=unavailable`；
- 不允许写入悬空 PayloadRef；
- Business Flow 继续；
- Observation Health 记录失败分类。

### 14.5 Shutdown

正常 Shutdown：

1. 停止接受新的非终态 Observation；
2. 允许 Terminal/Lifecycle Observation 进入；
3. 在超时内 Flush Journal；
4. Flush Query Projector；
5. Flush OTEL Exporter；
6. 写入 `runtime.stopped` 或超时 Health；
7. 关闭资源。

Crash 不执行修复性“成功关闭”。Reducer 根据没有 End Observation 的对象标记
`aborted` 或 `open`。

## 15. 持久化设计

### 15.1 文件布局

建议在 State Data Dir 下新增：

```text
observability/
  manifest-v1.json
  journal-v1/
    0000000000000001-0000000001000000.jsonl
  projection-v1.db
  export-state-v1.json
```

Payload 继续复用：

```text
cas-v1/
```

不将 Observation 写入 `events-v1.jsonl`，原因是：

- Protocol Event 是产品协议事实；
- Observation 包含更高频、更敏感、更可删除的诊断数据；
- 两者 Retention 和 Failure Policy 不同；
- Observation 不得扩大 Event Hub 的业务写延迟。

### 15.2 Journal Record

每行：

```json
{
  "sequence": 42,
  "envelope": {},
  "sha256": "...",
  "previous_sha256": "..."
}
```

第一版使用 JSONL 便于检查，必须具备：

- Newline Commit Marker；
- Segment Header 和 Schema Version；
- 单调 Sequence；
- Record Digest；
- Previous Digest Chain；
- Torn Tail Repair；
- Committed Interior Corruption Fail Closed；
- Segment Size 上限；
- Atomic Segment Rotation。

### 15.3 Query Projection

`projection-v1.db` 是可删除、可重建的派生数据库，建议首批表：

```text
observation_cursor
trace_spans
inference_calls
tool_attempts
approval_decisions
effect_lifecycles
terminal_operations
journal_mutations
verification_runs
conversation_visibility
agent_interactions
observation_health
```

每张表必须保存：

- Source Observation Sequence；
- Source Observation ID；
- Schema Version；
- Projection Revision。

### 15.4 投影规则

```text
append canonical observation
 -> projector reads after cursor
 -> transactional projection
 -> advance projection cursor
```

约束：

- Projection 可落后，不可领先；
- Replay 幂等；
- 同 Sequence 不同 Payload 为 Corruption；
- Reducer Version 改变时建立新 Projection Revision；
- Projection 故障不阻塞 Business Runtime；
- 查询必须暴露 Projection Lag。

### 15.5 Retention

建议默认：

| 数据 | 默认策略 |
| --- | --- |
| Lifecycle Metadata | 30 天或 100,000 Turn |
| Failure Metadata | 30 天 |
| Raw Workspace/Conversation Payload | 默认不采集；启用后 24 小时 |
| Tool Stream Delta | 1 小时或只保留终态 Head/Tail |
| Query Projection | 跟随 Canonical Journal，可重建 |
| OTEL Export State | 7 天 |
| Credential/Restricted | 永不持久化 |

Retention 删除必须先删除 Journal Segment 的可删除版本，再由 CAS Reference GC 回收对象。
不能仅删除 Projection 后假装 Raw Payload 已被删除。

## 16. Semantic Reducer

### 16.1 Reduced Graph

```go
type Graph struct {
    Version          uint32
    Runtimes         map[string]RuntimeNode
    Threads          map[string]ThreadNode
    Turns            map[string]TurnNode
    InferenceCalls   map[string]InferenceNode
    ToolAttempts     map[string]ToolNode
    Effects          map[string]EffectNode
    Approvals        map[string]ApprovalNode
    TerminalOps      map[string]TerminalOperationNode
    Verifications    map[string]VerificationNode
    Agents           map[string]AgentNode
    Interactions     []InteractionEdge
    Visibility       []VisibilityEdge
    Inconsistencies  []Inconsistency
    Unknowns         []UnknownFact
}
```

### 16.2 ExecutionWindow

每个 Runtime Object 使用：

```go
type ExecutionWindow struct {
    StartedSequence uint64
    EndedSequence   uint64
    StartedAt       time.Time
    EndedAt         time.Time
    Status          string
}
```

Sequence 是因果权威，Timestamp 用于延迟和展示。同毫秒事件不得按时间戳重新排序。

### 16.3 VisibilityEdge

```go
type VisibilityEdge struct {
    SourceKind string
    SourceID   string
    Target     string
    Payload    PayloadRef
    Sequence   uint64
}
```

Target 闭集：

- `runtime`；
- `model_request`；
- `model_response`；
- `tool_caller`；
- `host_projection`；
- `user`。

这允许精确表达：

```text
process stdout
 -> formatted tool result
 -> bounded model-visible result
 -> UI expanded content
```

### 16.4 Reducer 不变量

1. 按 Observation Sequence 重放；
2. PayloadRef 必须可读取或显式标记 unavailable；
3. Object ID 在同一 Reducer Version 中稳定；
4. Tool Attempt 不因 Retry 覆盖；
5. Runtime Result 不自动成为 Model-visible Result；
6. Terminal Envelope 与 Terminal Observation 必须可关联；
7. Child Thread 使用独立 Thread ID，共享 Root Trace ID；
8. Missing End 产生 Open/Aborted，不产生 Completed；
9. Authority 冲突进入 Inconsistency，不静默选择低级事实；
10. 相同输入、Reducer Version 和 Policy 必须产生 Byte-identical Graph。

### 16.5 Explain API

Reducer 应提供：

```go
ExplainTurn(turnID)
ExplainTool(callID)
ExplainFailure(turnID)
ExplainCost(turnID)
ExplainVisibility(sampleID)
ExplainRecovery(turnID)
```

解释结果必须包含：

- 结论；
- Authority Source；
- Observation IDs 和 Sequence；
- Payload References；
- Unknowns；
- Inconsistencies；
- 建议的下一项证据。

## 17. OpenTelemetry 集成

### 17.1 内部与 OTEL 的关系

```text
Canonical Observation Journal
             |
             +-> Local Reducer
             |
             +-> OTEL Projector
```

不允许：

```text
OTEL Backend -> Runtime State
OTEL Span -> Effect Retry
Exporter Ack -> Terminal Commit
```

### 17.2 Span 结构

建议 Span：

```text
runtime
  operation
    turn
      kernel.transition
      model.request
      tool.attempt
        approval.wait
        sandbox.attempt
        process / mcp
      verification
      terminal.commit
```

并行 Tool 是 Turn 的并行 Child Span。Agent Child Turn 使用 Span Link 和共享 Root Trace，
不强制形成错误的同步父子时间关系。

### 17.3 W3C Propagation

传播边界：

- Provider HTTP Header；
- MCP HTTP/stdio Metadata；
- Process 环境变量，仅对受信 Runtime Helper；
- Worker Task Payload；
- Workflow Node；
- Subagent TurnSpec；
- ACP/Runtime API 可选 Metadata。

安全要求：

- `tracestate` 长度和 Grammar 必须验证；
- 不向任意用户命令默认注入内部 Trace Context；
- Trace ID 不包含 Session、User 或 Workspace 信息；
- Provider Header 注入仍通过 Egress Policy；
- 外部 Parent 只能影响 Trace，不影响 Authority。

### 17.4 Metrics

第一版标准指标：

```text
codehelper.operation.count
codehelper.turn.duration
codehelper.turn.ttft
codehelper.provider.request.duration
codehelper.provider.request.count
codehelper.tool.duration
codehelper.tool.count
codehelper.approval.wait.duration
codehelper.terminal.commit.duration
codehelper.observation.queue.depth
codehelper.observation.dropped
codehelper.observation.write.failure
codehelper.projection.lag
codehelper.payload.bytes
codehelper.reducer.inconsistency
```

允许 Label：

- status；
- phase；
- provider；
- model family；
- tool class；
- error category；
- sandbox strength；
- observation kind class。

禁止 Label：

- Session/Turn/Call ID；
- 路径；
- Prompt；
- Command；
- URL；
- Raw Error；
- User/Account ID。

### 17.5 Exporter

支持：

- None；
- JSON File；
- OTLP HTTP；
- OTLP gRPC；
- In-memory Test Exporter。

所有 Exporter 必须：

- 有界 Batch；
- 超时；
- Drop Policy；
- 显式 Flush/Shutdown；
- 故障隔离；
- 禁止 Raw Payload 默认导出。

## 18. Receipt 与 Measurement 收敛

### 18.1 当前问题

当前 Receipt 在 Terminal Event 前读取 Turn Latency，Root Span 在 Scope 返回时关闭。
因此 Receipt 的 Total 是“截至冻结时”，持久化 Trace 的 Total 是“截至 Scope 结束时”。

### 18.2 目标顺序

```text
finish all business effects
 -> freeze TerminalMeasurementSnapshot
 -> build Receipt from snapshot
 -> build Terminal Envelope with same digest
 -> atomic terminal commit
 -> append terminal observation referencing snapshot digest
 -> close/export trace with frozen business measurement
```

Scope 清理、Exporter Flush 等终态之后的工作进入独立 Cleanup Span，不修改 Turn Receipt。

### 18.3 Measurement Authority

Measurement Snapshot 是 Terminal Envelope 内的持久化测量事实，但它仍不决定业务
Completion。若 Measurement 缺失：

- Receipt 标记 `recorded=false`；
- Terminal 仍按 Kernel Decision 提交；
- 不填零值；
- Observation Health 记录缺失原因。

## 19. Event Trait 收敛

### 19.1 单一来源

扩展当前 `event_traits.json`，或生成一个兼容的统一 Manifest，至少增加：

- Persist Policy；
- Payload Policy；
- Privacy Class；
- Retention Class；
- Observation Mapping；
- OTEL Mapping。

`eventlog.ShouldPersist` 必须变为生成表查询：

```go
func ShouldPersist(kind protocol.EventKind) bool {
    traits, ok := protocol.Traits(kind)
    return !ok || traits.Durability.Persisted()
}
```

未知 Event 的策略必须明确：

- Protocol Event 默认 Fail Closed 构建；
- 运行时遇到未知 Kind 默认保留 Metadata，不保留 Raw Payload；
- Generator 阻止缺失 Trait 的代码合入。

### 19.2 Trait Gate

CI 必须验证：

- 所有 Event Kind 有 Trait；
- 所有 Observation Kind 有 Trait；
- Durability 与 Store Policy 一致；
- TypeScript 与 Go Golden 一致；
- Retained Event 在恢复测试中真实可 Replay；
- Transient Event 不被查询层错误声明为 Durable。

## 20. 多 Agent 与 Orchestration

### 20.1 Trace Identity

建议：

```text
Session Tree -> one Root Trace ID
Agent Thread -> one stable Thread span/link identity
Turn         -> one Turn Span
Attempt      -> child span or linked retry span
```

独立用户 Session 不共享 Trace。Fork 是否共享 Trace 取决于是否仍属于同一 Runtime
Operation；持久化 Thread Lineage 不能仅由 Trace 推断。

### 20.2 Interaction Edge

第一版支持：

- Spawn；
- Task Delivery；
- Message；
- Result Delivery；
- Close；
- Integration Apply/Discard。

每条 Edge 记录：

- Source/Target Agent；
- Source/Target Turn；
- Operation ID；
- Carried Payload Ref；
- Started/Ended Sequence；
- Delivery Status。

### 20.3 Child Authority

Child 自报的 Test、Verification 或 Completion 只形成 Child Observation。父 Runtime
必须通过现有 Integration 和 Verification Authority 决定是否接受，不因 Trace Edge
自动升级为 Gate Evidence。

## 21. 安全、隐私与合规

### 21.1 Capture Mode

```go
type CaptureMode string

const (
    CaptureOff      CaptureMode = "off"
    CaptureMetadata CaptureMode = "metadata"
    CaptureFailure  CaptureMode = "failure"
    CaptureFull     CaptureMode = "full"
)
```

默认 `metadata`：

- 保存 Identity、Kind、大小、Digest 和状态；
- 不保存 Prompt、文件正文、Tool 参数和完整输出。

`failure` 只为失败链路保存受限 Payload，仍应用 Redaction 和大小上限。

`full` 必须由用户显式启用，并显示路径、TTL 和敏感性警告。

### 21.2 Redaction

Redaction 必须在 CAS 写入前完成，覆盖：

- 已知 Credential Value；
- Authorization/Bearer；
- API Key、Token、Password、Secret 字段；
- Provider 配置中的 Secret Reference；
- Environment Allowlist 外的敏感变量；
- 用户配置的 Literal Secret；
- Restricted Path 内容。

仅在 Export 时脱敏不够，因为本地 CAS 已经持久化。

### 21.3 文件权限

- Observation Root：`0700`；
- Journal、Manifest、Projection DB：`0600`；
- CAS Object：继承现有安全策略；
- 临时文件：同目录创建、`0600`、fsync 后原子替换；
- Support Bundle 必须重新 Redact，不能直接压缩整个 State Dir。

### 21.4 控制面保护

Observation 配置和 Retention 状态属于 Runtime Control Plane：

- 普通 File/Shell Tool 不得修改；
- 通过现有 Control Plane Path Protection；
- 不允许 Model 启用 Full Capture；
- Capture Mode 变更必须来自 Host Operation 或受信配置。

## 22. 性能设计

### 22.1 热路径预算

热路径只允许：

- 分配小型固定 Envelope；
- 读取已有 Identity；
- 计算受限 Summary；
- 非阻塞写有界队列。

禁止热路径：

- 构建完整 Semantic Graph；
- 同步 OTLP；
- 复制无限 Tool Output；
- 扫描整个 Event Log；
- 对大型 Payload 重复 JSON Marshal；
- 使用高争用全局 Mutex。

### 22.2 Payload 去重

复用 CAS Digest 后：

- 相同 Logical Request Snapshot 不重复存储；
- Runtime Result 和 Model-visible Result 可引用不同 Payload；
- 相同 Payload 的多个 Visibility Edge 只增加 Metadata；
- Retention GC 按 Reference Count/Reachability 回收。

### 22.3 Backpressure

优先级：

```text
terminal/failure/security
  > lifecycle/attempt
  > accounting/measurement
  > payload summary
  > stream delta/raw payload
```

队列压力不得阻塞 Authority Plane。只有本地显式 Debug Session 可以配置
`capture_backpressure=block`，且不能用于正常产品模式。

### 22.4 Event Hub 解耦

后续阶段可以将 Event Hub 拆为：

```text
sequence reservation
 -> durable append
 -> ordered projection queue
 -> fanout
```

但必须保持：

- Event 顺序；
- Stable ID 幂等；
- Terminal Projection 原子性；
- Subscriber 不领先 Durable Append。

该优化不得与 Observation Plane 第一阶段绑定，避免同时改变业务 Event Path。

## 23. API 与用户体验

### 23.1 CLI

建议新增：

```text
codehelper observe status
codehelper observe list --session <id>
codehelper observe turn <turn-id>
codehelper observe tool <call-id>
codehelper observe explain-failure <turn-id>
codehelper observe visibility <sample-id>
codehelper observe reduce --session <id>
codehelper observe export --session <id> --redacted
codehelper observe prune
```

CLI 默认只读取 Metadata 和 Reduced Graph。读取 Raw Payload 需要显式
`--include-sensitive`，并受 Host Approval。

### 23.2 TUI

现有 Usage、Latency 和 Observation Panel 扩展为：

- Turn Timeline；
- Provider Attempt；
- Tool/Approval/Process Tree；
- Verification 和 Mutation；
- Projection Lag；
- Unknown/Inconsistency；
- Observation Health。

### 23.3 VS Code

VS Code 使用 Runtime Query API，不再自己拼接 Capture 作为唯一分析方式。Capture 仍可
作为 Host/ACP/Supervisor 外围证据，并通过 Root Trace ID 与 Runtime Observation 关联。

### 23.4 ACP

ACP 不暴露 Raw Local Path。返回：

- Redacted Summary；
- Observation ID；
- Payload Availability；
- Runtime Query Handle。

## 24. 迁移与兼容

### 24.1 初始阶段

本项目尚未发布稳定持久化格式，因此优先建立干净的 V1 Observation Schema，不增加
没有真实用户需求的长期兼容迁移。

### 24.2 现有数据

旧数据处理：

- `events-v1.jsonl` 继续可 Replay；
- `state-v1.db` 的 Usage/Span 继续可查询；
- 不伪造旧数据的 Observation ID；
- 可通过一次性 Importer 创建
  `source=legacy_projection` 的低置信 Observation；
- Legacy Observation 不得声称具备 Model Visibility 或完整 Attempt。

### 24.3 双写限制

允许阶段性：

```text
old trace repository + new observation journal
```

但只用于 Characterization。完成 SO4 后：

- Receipt 只读 Terminal Measurement；
- Query 只读新 Projection；
- 删除旧 Trace Writer 或将其变为新 Projector；
- 不永久维护两套 Span Authority。

### 24.4 Schema Version

每层独立版本：

- Observation Envelope Version；
- Journal Format Version；
- Reducer Version；
- Projection Schema Version；
- Export Mapping Version。

升级不得用一个全局版本掩盖不同兼容边界。

## 25. 分阶段实施

### SO0：基线与事实冻结

目标：量化当前观测能力、成本和缺口，避免无基线重构。

状态：`accepted`。

工作项：

1. 建立标准 Session Matrix；
2. 采集 Event、Domain Fact、Trace、Usage、Receipt、Capture 体积；
3. 注入 Provider、Tool、Approval、Terminal Commit 和进程崩溃；
4. 记录人工故障重建耗时；
5. 固化 Event Durability 对照表；
6. 建立 Storage/Latency/Allocation Baseline；
7. 生成 Golden Session Fixtures。

交付：

```text
docs/state-observability-so0-baseline.json
scripts/stateobservabilitybaseline/
```

已交付结果：

- 50/50 Event Kind 完成 Trait 审计；
- 确认 2 个 Durability 漂移，Ratchet 禁止新增；
- 冻结 7 个 Authority、15 层 Identity 和 9 条 Observation Surface；
- 10 个标准 Session 与 13 个故障注入场景均绑定现有 Hermetic Test；
- 冻结 Turn State、Domain Fact、Span、Tool Attempt Receipt、WorkGraph Fact 和
  Extension Trace 的代表性字节基线；
- 冻结 Event Policy Audit、State JSON 和 1000 Span Snapshot 的时间与 Allocation
  基线；
- 复杂故障当前至少需要连接 7 个数据源、执行 6 次人工 Join。

Gate：

- 所有现有 Event Kind 完成 Durability 审计；
- 至少覆盖 10 类失败；
- 基线可在 Hermetic 环境重跑；
- 不修改生产行为。

以上 Gate 已全部通过。

### SO1：统一 Schema 与 Identity

目标：建立 Observation Envelope、Traits 和 Identity Contract。

状态：`accepted`。

工作项：

1. 新增 `internal/observability/observation`；
2. 定义 Envelope、Identity、Causality、PayloadRef、DataClass；
3. 建立 Observation Trait Manifest 与 Generator；
4. 将 Event Durability 映射到单一 Manifest；
5. 增加 Schema/Golden/Architecture Test；
6. 为 Turn、Sample、Call、Effect、Attempt 建立稳定映射。

已交付结果：

- 建立 61 个闭集 Observation Kind 和 13 个 Owner；
- Envelope 原生覆盖 15 层 Identity、9 种 Causality Relation 和 W3C Trace Shape；
- DataPolicy、PayloadRef、DataClass、Redaction、Retention 和 Priority 完成强校验；
- 单一 Manifest 生成 Go Traits、TypeScript Contract 和 JSON Schema；
- Generator Check 阻止 Manifest 与三端生成物漂移；
- Observation 包通过 Architecture Test 保持对业务 Runtime 的零依赖；
- 50/50 Protocol Event 的 Trait 与实际持久化策略一致，Drift 从 2 降为 0；
- `turn.compaction` 作为 Audit Event 持久化，`reasoning.delta` 明确保持瞬态；
- 未增加 Runtime Tap、Journal 写入或远端 Export。

Gate：

- 100% Observation Kind 有 Trait；
- 100% Protocol Event 的实际 Persist Policy 与 Trait 一致；
- 新 Event/Observation 缺 Trait 时生成失败；
- 不改变 Terminal、Event 和 Tool 行为；
- Architecture Ratchet 不下降。

以上 Gate 已全部通过。

### SO2：增量 Observation Journal

目标：关闭 Trace Crash Hole。

状态：`accepted`。

工作项：

1. 实现 Segment Journal；
2. 复用 CAS Payload；
3. 实现 Payload-first Write；
4. 实现有界 Router、Queue 和 Priority；
5. 增加 Torn Tail、Interior Corruption、Disk Full、Queue Full 测试；
6. 增加 Observation Health；
7. 在 Kernel、Provider、Tool、Terminal 增加最小 Tap。

已交付结果：

- 新增 Segment JSONL Journal、Manifest V1、单调 Journal Sequence、SHA-256 Record
  Digest 和 Previous Digest Chain；
- 只修复最后一个 Open Segment 的 Torn Tail，任何 Interior Corruption 均 Fail
  Closed；
- Router Admission 分配 `observed_sequence`，保留真实观察顺序和 Priority Eviction
  后的 Gap；Journal Append 分配 `sequence`，只表示确定性重放顺序；
- Payload 先写现有 CAS，再将稳定 PayloadRef 写入 Journal；CAS 失败时保留 Metadata
  且不产生悬空引用，Journal 失败时释放已获取的 CAS Reference；
- Metadata Queue 默认容量 4096，Payload Budget 默认 32 MiB，单 Payload 默认上限
  256 KiB；Critical 同步持久化，Normal/Bulk 异步写入，Normal 可淘汰 Bulk；
- Health 独立记录 Accepted、Written、Payload Written/Dropped、按原因 Drop、按类型
  Write Failure、Queue Depth/Bytes、In-flight 和 Last Error；
- `turn.started`、`model.request.sent`、`tool.started`、`approval.requested`、
  `verification.started` 和 `turn.terminal.prepared` 作为 Crash-critical Start；
- Domain Fact Observer 只在 Fact Durable Append 成功后触发，且在 Effect Dispatch
  前记录引用；Observer Panic 或 Observation Writer Failure 均不改变业务 State；
- 真实 Wiring E2E 已覆盖 Turn Start、Domain Fact、Model Start/End、Tool Start/End
  和 Terminal Prepared/Committed 共 8 类 Observation；
- Open Span 在 Recorder Close 时不补写 End，留待 SO3 Reducer 解释为 Open/Aborted；
- SO0 可执行基线已识别 Incremental Journal 和 Health Model，Known Gaps 从 7 降至 5；
- Apple M5 Pro、`50x × 5` 稳定采样下，Journal Append 中位 6073 ns/4 allocs，
  Critical Metadata Admission 中位 266.7 ns/1 alloc，Turn 每轮约 16 条 Observation；
- Turn A/B 中位延迟从 7.512 ms 增至 7.549 ms，回退 0.50%；Allocation 从 2894
  增至 2960，回退 2.28%。

Gate：

- 任意 Span Start 后 Crash 均留下可解释 Open Object；
- Journal Interior Corruption Fail Closed；
- Observation Writer 失败不改变业务 Terminal；
- 不产生悬空 PayloadRef；
- P95 Turn 延迟回退不超过 2%；
- Allocation 回退不超过 3%。

以上 Gate 已全部通过。可执行证据见
[`state-observability-so2-evidence.json`](../state-observability-so2-evidence.json)。

### SO3：Semantic Reducer

目标：从 Canonical Observation 确定性生成语义图。

状态：`accepted`。

工作项：

1. 定义 Reduced Graph；
2. 实现 Inference、Tool、Effect、Approval、Terminal Reducer；
3. 实现 Visibility Edge；
4. 实现 Multi-Agent Interaction Edge；
5. 实现 Unknown 和 Inconsistency；
6. 实现 Projection DB；
7. 增加 Byte-identical Golden Replay。

已交付结果：

- 新增 `internal/observability/semantic`，且 Architecture Test 禁止其依赖 Host、
  Engine、Tool、Security、Orchestration 或其他业务 Authority；
- Graph V1 覆盖 Runtime、Thread、Turn、Inference、Tool Attempt、Effect、
  Approval、Terminal Operation、Verification、Agent、Interaction 和 Visibility；
- 所有对象使用 Length-prefixed Semantic ID，避免简单字符串连接的边界碰撞；
- Journal `sequence` 是唯一重放顺序；Timestamp 只用于展示与时长，不参与排序；
- `observed_sequence` 按 Runtime 在完整重放后计算 Gap，Priority 导致的乱序不会被
  误判为丢失，重复 Admission Sequence 进入 Inconsistency；
- Missing End 保持 `open` 且不伪造 Completed；Missing Start、Payload Unavailable、
  Provisional Attempt 和缺失 Causality Target 进入 Unknown；
- Duplicate Start/End、Identity Conflict 和 Source Cursor Conflict 进入
  Inconsistency 或 Fail Closed，不覆盖先前证据；
- Inference 与 Tool 均保留 Attempt ordinal；Runtime Tool/Approval Tap 已显式写入
  Attempt 1，旧数据缺失 ordinal 时使用可查询的 Provisional Attempt 并记录 Unknown；
- Runtime Result 与 Model-visible Result 分离；每个
  `tool.result.model_visible` 和 `context.visibility.committed` 都生成显式
  Visibility Edge；
- Payload 只保留 PayloadRef 和 `available/unavailable/unverified` 状态，不复制
  Payload Body；Projection 也不复制任意 inline Summary；
- Projection Repository 事务化保存 Reducer Version、Source Sequence、Source
  Digest、Graph Digest 和 Canonical Graph JSON；
- 同 Sequence 不同 Source Digest 拒绝覆盖，Load 时验证 Graph Digest，Query 暴露
  Projection Lag；
- Projection 可从空 SQLite 数据库完整重建，同一 Journal 重放幂等；
- Explain API 已覆盖 Turn、Tool、Failure、Cost、Visibility 和 Recovery；
- 11 条代表性 Observation 的 Canonical Graph Golden SHA-256 为
  `22de2612635d02a5ccc3aa20898fad66cacf3590504804cb9b99ce184fdd430d`；
- Apple M5 Pro、`100x × 5` 采样下，Semantic Replay 中位 8134 ns、
  13043 B、158 allocs；
- SO0 可执行基线已将 Explicit Model Visibility 标记为 true，Known Gaps 从 5
  降至 4；
- Architecture Ratchet 从 103 项提升至 104 项，新 Semantic 包自身受 Fanout、
  Production Lines、Options 和 Mutex 预算约束。

Gate：

- 同一 Journal 重放结果 Byte-identical；
- 100% Tool Attempt 关联 Turn、Call、Attempt；
- 100% Model-visible Result 有显式 Visibility Edge；
- Missing End 不生成 Completed；
- Projection 可从空 DB 完整重建。

以上 Gate 已全部通过。可执行证据见
[`state-observability-so3-evidence.json`](../state-observability-so3-evidence.json)。

### SO4：Measurement 与 Receipt 收敛

目标：Receipt、Terminal Envelope 和 Trace 使用同一冻结测量。

状态：`accepted`。

工作项：

1. 定义 Terminal Measurement Snapshot；
2. 在 Terminal Prepare 前冻结；
3. Receipt 改为消费 Snapshot；
4. Trace Projector 消费同一 Snapshot；
5. Cleanup 使用独立 Span；
6. 删除旧的重复 Timer/Latency 聚合。

已交付结果：

- 在 `turnkernel` 定义 Version 1 `TerminalMeasurementSnapshot`，原生包含 FrozenAt、
  Turn/First Output/Provider/Tool/Approval/Verification/Persistence Duration 和
  Frozen Usage；
- Measurement 与 Usage 分别计算 SHA-256 Digest，Terminal Envelope Digest 覆盖完整
  Measurement；
- Engine Terminal 回调先读取 Kernel Frozen State，再原子冻结 Recorder，最后构造
  Receipt 与 Terminal Envelope；
- Recorder Freeze 幂等；冻结后 Root/Phase Latency 不再变化，后续耗时进入独立
  `turn_cleanup` Span；
- Receipt 删除事件流 Usage 累计、终态覆盖和独立 `time.Since` 路径，只读 Frozen
  Kernel Usage 与 Measurement Latency；
- `measurement_recorded`、`measurement_digest` 和 `usage_digest` 已进入 Go Protocol、
  JSON Schema 与 VS Code TypeScript Contract；
- Model Usage 在 `ModelSampleResultReceived` 中记录 Cost 与定价状态；Vision、
  Sub-query 等 Tool 内模型 Usage 通过 `SupplementalUsageRecorded` Domain Fact 进入
  Kernel；
- Usage Call Count 明确记录，混合已定价与未定价调用时 `CostKnown=false`，Terminal
  Freeze 后拒绝任何迟到 Usage；
- Trace Repository 不再从终态后的可变 Engine Span Tree 重写 Turn；Terminal Commit
  成功后从 Measurement 确定性投影 Root 与 Provider/Tool/Approval/Verify Aggregate；
- Trace Root 同时携带 Measurement Digest 和 Usage Digest；Terminal Prepared 与
  Terminal Committed Observation Summary 也引用相同 Measurement Digest；
- Missing Latency 保持 `recorded=false`，不生成零时长 Trace 或虚假 Receipt
  Partition；
- Commit Failure 不会投影 Measurement Trace，Trace Projection Failure 也不会改变
  已提交业务 Terminal；
- Terminal Recovery 重放 Measurement Projection 幂等，Frozen Usage 不重复累计；
- SO0 可执行基线已将 Terminal Measurement Snapshot 标记为 true，Known Gaps 从 4
  降至 3；
- Apple M5 Pro、`100x × 5` 采样下，Measurement Seal 中位 3615 ns/3052 B/22
  allocs，Trace Projection 中位 683.3 ns/3408 B/20 allocs；
- Architecture Ratchet 从 104 项提升至 106 项，Measurement 的 Kernel 与 Runtime
  文件均有独立行数门禁。

Gate：

- Receipt 与 Trace Total/Usage Digest 100% 一致；
- Missing Measurement 保持 Unknown，不变成零；
- Commit Failure 不发布 Receipt 或 Terminal；
- Recovery 重放不重复累计 Usage；
- Terminal Envelope Digest 覆盖 Measurement Digest。

以上 Gate 已全部通过。可执行证据见
[`state-observability-so4-evidence.json`](../state-observability-so4-evidence.json)。

### SO5：W3C Trace 与 OTEL

目标：建立跨 Provider、MCP、Process、Worker、Subagent 的标准 Trace。

状态：`accepted`。

工作项：

1. 引入标准 Trace ID/Span ID；
2. 实现 W3C Parse/Inject；
3. 接入 Provider HTTP；
4. 接入 MCP 和受信 Runtime Helper；
5. 接入 Worker/Workflow/Subagent；
6. 实现 OTLP 与 In-memory Exporter；
7. 建立 Low-cardinality Metric Registry；
8. 增加 Flush/Shutdown。

Gate：

- 跨边界 Parent/Link 关联率 100%；
- 非法 Trace Context 不影响业务；
- 普通用户命令不泄漏内部 Trace Context；
- Exporter 不可用时业务零回退；
- Metric Label Cardinality Gate 通过。

实施结果：

- `tracecontext` 基于官方 `propagation.TraceContext` 实现标准 `traceparent` 与
  `tracestate` Parse/Inject，非法输入保留原业务 Context 并返回观测错误；
- Turn、Provider、Tool Span 使用标准 16 Byte Trace ID 和 8 Byte Span ID，Terminal
  Prepared/Committed 复用 Turn 根身份，Committed 确定性关闭根 Span；
- Provider HTTP 与 Responses WebSocket、MCP HTTP Header 与 stdio `_meta`、
  Workflow TaskRequest、Worker Executor Context、Subagent Turn Context 均完成传播；
- Process 仅在显式 `TrustedRuntimeHelper` 时注入 `TRACEPARENT`/`TRACESTATE`，普通
  用户命令保持无内部 Trace Context；
- OTEL Projector 只消费 Journal 已追加的 Envelope，保留 Trace/Span ID，支持
  In-memory、OTLP HTTP/protobuf、OTLP gRPC 与 Off；
- 有界队列默认 4096 条，Metric Registry 最多 512 个 Series，只允许 8 个已审核维度，
  拒绝 Turn/Session/Call ID、路径和自由文本；
- 已输出 Observation、Operation、Turn、Provider、Tool、Approval、Terminal Commit、
  Queue、Drop、Projection Lag、Payload Bytes 和 Reducer Inconsistency 指标；
- `ForceFlush` 等待所有已接受投影完成，`Shutdown` 停止接收后 Drain；Exporter 构造
  失败回退 In-memory，异步导出失败只更新 Projection Health；
- Apple M5 Pro、`100x × 5` 采样下，W3C Inject/Extract 中位 `923.8 ns/op`、
  `803 B/op`、17 allocs；OTEL Projection 中位 `13293 ns/op`、`1397 B/op`、
  23 allocs；
- SO0 Known Gaps 从 3 降至 1，Architecture Ratchet 从 106 项提升至 108 项。

以上 Gate 已全部通过。可执行证据见
[`state-observability-so5-evidence.json`](../state-observability-so5-evidence.json)。

### SO6：安全、Retention 与 Support Bundle

目标：让 Raw Evidence 可控、可删、可分享。

状态：`accepted`。

工作项：

1. 实现 Capture Mode；
2. 字段级 Data Class；
3. CAS 前 Redaction；
4. Segment Retention 与 CAS GC；
5. Redacted Support Bundle；
6. 控制面路径保护；
7. Secret Corpus 与 Restricted Path 测试。

Gate：

- Credential Payload 零落盘；
- Raw Capture 默认关闭；
- TTL 到期后 Journal 和 CAS 均不可恢复 Payload；
- Support Bundle 二次脱敏；
- 文件权限符合 `0700/0600`；
- Security Ratchet 不下降。

实施结果：

- Capture Mode 支持 `off`、`metadata`、`failure`、`full`，生产默认
  `metadata`；只有受信 Wiring 可读取环境配置，Model 无法提升 Capture；
- Summary 始终执行字段级脱敏；Payload 在 CAS Put 前执行结构化敏感键、已知
  Credential、Bearer Token 与 Restricted Path 脱敏；
- Credential/Restricted Payload 在任何 Capture Mode 下均禁止持久化；
- Retention 默认 Audit/Diagnostic 30 天、Sensitive 24 小时、Ephemeral 1 小时；
  GC 先重建并原子替换无过期 PayloadRef 的 Journal Digest Chain，再释放 CAS 引用并
  删除零引用对象；
- Support Bundle 只包含 Manifest、Envelope 与可选二次脱敏 Payload，不压缩 State
  Dir，不暴露原 CAS Digest；
- Journal/CAS/Bundle 目录和文件分别保持 `0700`/`0600`；
- Secret Corpus、真实磁盘扫描、共享 CAS 引用、TTL 不可恢复、Race、Security Test 与
  Secret Leak Test 全部通过；
- Apple M5 Pro、`100x × 5` 下 Payload Redaction 中位 `3707 ns/op`。

可执行证据见
[`state-observability-so6-evidence.json`](../state-observability-so6-evidence.json)。

### SO7：规模化与最终收敛

目标：降低写放大、删除旧路径并完成真实环境验收。

状态：`accepted`。

工作项：

1. 评估 Domain Fact Full State 写放大；
2. 在不削弱恢复的前提下引入周期 Full Snapshot + Typed Delta + Digest Chain；
3. 删除旧 Trace Writer 和重复 Metrics；
4. 优化 Event Hub 持锁范围；
5. 完成 VS Code + DeepSeek 多 Turn 实测；
6. 对比 `.tmp/runtime-monitor` 历史；
7. 更新中英文架构与运行监测文档。

Gate：

- State Replay 与旧 Full State Golden 一致；
- Domain Fact 存储 P50 至少下降 50%；
- Observation Payload 去重率可观测；
- Token、Tool、Session、PID、Span、Lease 零残留；
- P95 Turn 延迟回退不超过 2%；
- Architecture Ratchet 不下降；
- 所有旧双写路径删除。

实施结果：

- SQLite Domain Fact 物理格式改为每 16 条 Full Snapshot，其余保存 State 顶层 Typed
  Delta，并以 `previous_state_digest -> state_digest` 建链；
- Kernel `DomainFactStore` 逻辑 API 不变，读取时逐条重建、Validate 并校验 Digest；
  32 条大状态 Golden 中 Full State 为 `347246 B`，新格式为 `33620 B`，下降
  `90.32%`；
- Engine-facing `trace.Sink` 与生产 `spans` Writer 已删除；CLI/TUI Accounting 直接从
  Terminal Envelope 的冻结 Measurement 重建 Latency；
- Provider/Turn/Tool 的旧内存重复 Metrics 写入已删除，低基数 OTEL Observation
  Metrics 成为标准导出；安全、成本、压缩等非重复运维计数继续保留；
- Event Hub 使用独立 Publish Serializer 维持 Sequence 与订阅无缺口，状态锁不再覆盖
  Store I/O、Projection 或 Observation Callback；
- Observation Health 暴露 Payload Written、Deduplicated 与 Dedup Rate；
- `200x × 10` Turn 基准中 Observation Enabled P95 `10.164 ms/op`，Disabled P95
  `10.308 ms/op`，无延迟回退；
- VS Code Target VSIX + DeepSeek `deepseek-v4-flash` 完成同 Session 两 Turn：
  2/2 Completed、2 Model Calls、13216 Input Tokens、14 Output Tokens、1792 Cached
  Tokens、Turn P95 `3437 ms`；
- 实测生成 42 条 Canonical Observation、2 个 Trace ID，默认 Metadata 下 PayloadRef
  为 0；终态和 VS Code 退出后 Active Turn、Pending Outbox、Lease、Open Span、
  Runtime/VS Code Process 均为 0；
- 后验复杂 Turn 实测发现生产 Envelope 未填充 `monotonic_ns`，并在 Critical 与异步
  Observation 重叠时观察到一次微秒级 `recorded_at` 逆序；Router 现以独立短临界区
  统一 Canonical Journal 写入时钟，填充 Runtime-relative Monotonic NS，并对墙钟回拨
  做非递减钳制，旧 Journal 仍可读取；
- 修复后 VS Code + DeepSeek 只读架构审计生成 260 条 Canonical Observation、195 条
  Domain Fact 和 21 个成功 Tool Result；Canonical Sequence 与 Observed Sequence 集合
  均连续，Monotonic NS 严格递增，Receipt 与 Terminal Measurement 的 Usage/Latency
  完全一致。真实 Domain Fact 从 Full `2478999 B` 降至 Snapshot/Delta `592133 B`，
  下降 `76.11%`，终态与退出后仍保持零 Lease、Outbox、Open Span 和进程残留；
- SO0 Known Gaps 降至 0，Architecture Ratchet 提升至 `112/112`。

可执行证据见
[`state-observability-so7-evidence.json`](../state-observability-so7-evidence.json)。

## 26. 测试策略

### 26.1 单元测试

- Envelope Validation；
- Trait Generation；
- Trace Context Parse/Inject；
- Payload Admission；
- Redaction；
- Journal Append/Replay；
- Reducer；
- Projection；
- Measurement Freeze；
- Retention。

### 26.2 属性测试与 Fuzz

- 任意 Observation Sequence 重放；
- Duplicate/Gap/Reorder；
- PayloadRef Corruption；
- JSONL Torn Tail；
- Reducer Pending Association；
- Trace Parent Cycle；
- Data Class Policy；
- W3C Header Grammar。

### 26.3 Fault Injection

故障点：

```text
payload before fsync
payload after fsync
journal partial write
journal fsync
projection transaction
provider after send
provider after first output
tool after process start
approval wait
terminal prepare
terminal commit
otel export
shutdown flush
```

验证：

- Authority Plane 结果不变；
- Observation 明确 Open、Dropped、Unavailable 或 Indeterminate；
- 不出现虚假 Completed；
- 不出现悬空 PayloadRef。

### 26.4 Contract Test

- Go/TypeScript Trait 一致；
- Protocol Event 到 Observation Mapping；
- CLI/TUI/VS Code 同一 Turn 得到相同 Reduced Status；
- OTEL Span 与 Local Graph Identity 对应；
- Receipt 与 Measurement Digest 一致。

### 26.5 安全测试

- Secret Literal；
- Bearer/API Key；
- Environment Credential；
- Workspace `.env`；
- Control Plane Path；
- Symlink/Hardlink；
- Support Bundle；
- Malformed `tracestate`；
- High-cardinality Label Injection。

### 26.6 性能测试

场景：

- Text-only Turn；
- 100 Tool Call；
- 16 Parallel Tool；
- 100 KiB Tool Result；
- 1,000 Span；
- 32 Child Agent；
- 10,000 Event Replay；
- Projection Rebuild；
- Retention GC。

指标：

- P50/P95/P99 Latency；
- Allocation；
- Queue Depth；
- Drop Rate；
- Journal Bytes；
- CAS Dedup；
- Projection Lag；
- Reducer Throughput。

## 27. 验收指标

### 27.1 正确性

| 指标 | 目标 |
| --- | --- |
| Event Trait 与 Persist Policy 一致率 | 100% |
| Tool Attempt Identity 关联率 | 100% |
| Model Visibility 显式关联率 | 100% |
| Terminal/Receipt Measurement Digest 一致率 | 100% |
| Reducer Deterministic Golden | 100% |
| Crash 后虚假 Completed | 0 |
| 悬空 PayloadRef | 0 |

### 27.2 性能

| 指标 | 目标 |
| --- | --- |
| P95 Turn Latency 回退 | <= 2% |
| Allocation 回退 | <= 3% |
| Text-only Metadata 增量 | <= 8 KiB/Turn |
| Observation Queue Drop | 正常负载为 0 |
| Projection Lag | 正常负载 P95 < 1 秒 |
| Reducer 10k Observation | < 2 秒，本机基线 |

### 27.3 可靠性

| 指标 | 目标 |
| --- | --- |
| Observation Writer 故障改变业务结果 | 0 |
| Exporter 故障改变业务结果 | 0 |
| Crash 后可见 Open Runtime Object | 100% |
| Projection 从空库重建成功率 | 100% |
| Shutdown Flush 有界 | 100% |

### 27.4 安全

| 指标 | 目标 |
| --- | --- |
| Credential Payload 落盘 | 0 |
| Raw Capture 默认启用 | 0 |
| Restricted Path Raw Capture | 0 |
| Metric 高基数违规 | 0 |
| TTL 后可恢复敏感 Payload | 0 |

## 28. 建议代码布局

```text
internal/observability/
  observation/
    envelope.go
    identity.go
    traits.go
    validation.go
  capture/
    policy.go
    redaction.go
    admission.go
  journal/
    writer.go
    reader.go
    segment.go
    recovery.go
  reducer/
    graph.go
    reducer.go
    inference.go
    tool.go
    terminal.go
    visibility.go
    agent.go
  projection/
    store.go
    schema.go
    query.go
  otel/
    provider.go
    trace_context.go
    metrics.go
    projector.go
  health/
    health.go
  schema/
    observation_traits.json

internal/runtime/agent/
  measurement/
    snapshot.go
    digest.go

scripts/
  stateobservabilitybaseline/
  observationtraits/
```

依赖规则：

- `observation` 不依赖 Engine、Host 或具体 Tool；
- `reducer` 只依赖 Observation Contract；
- `otel` 不被 Turn Kernel 导入；
- Tap Interface 定义在 Observation 所有层，不在 Host；
- 构造全部位于 `internal/runtime/app/wire`；
- Observation Store 关闭进入 Resource Stack。

## 29. 风险与缓解

### 29.1 新系统变成第二状态权威

风险：Reducer 输出被 Runtime 用于恢复 Effect 或 Completion。

缓解：

- Architecture Test 禁止 Runtime Agent 导入 Reducer Query；
- Query 类型不实现 Domain Fact/Effect Store 接口；
- 文档与代码注释明确 Authority Matrix。

### 29.2 Raw Payload 泄密

风险：Debug Capture 保存 Prompt、源码、命令或凭据。

缓解：

- 默认 Metadata；
- CAS 前 Redaction；
- Data Class；
- TTL；
- Control Plane 配置；
- Secret Corpus Gate。

### 29.3 热路径回退

风险：大量 Observation Marshal 和写盘增加延迟。

缓解：

- 有界 Metadata Envelope；
- Payload CAS 去重；
- 异步 Journal；
- Priority Drop；
- SO0 性能基线和每阶段 Gate。

### 29.4 Schema 过度设计

风险：一次性建模所有 Runtime Object，延长周期。

缓解：

- SO1 只建立通用 Envelope；
- SO2 只覆盖 Kernel/Provider/Tool/Terminal；
- Reducer 按真实查询需求增加对象；
- Unknown Payload 保留引用，不强行解释。

### 29.5 Domain Fact Delta 削弱恢复

风险：为节省空间引入 Patch 后无法恢复或验证。

缓解：

- 放在 SO7；
- 保留周期 Full Snapshot；
- Delta Digest Chain；
- 与当前 Full State Replay 做双轨 Characterization；
- 任何不一致立即回退 Full State。

### 29.6 OTEL Cardinality 和成本

风险：把 ID、路径或错误文本作为 Label。

缓解：

- Metric Registry；
- 编译期允许 Label；
- Runtime Sanitizer；
- Cardinality Benchmark；
- 高基数内容只进入本地 Payload。

## 30. 回滚策略

每阶段必须可以独立回滚：

| 阶段 | 回滚方式 |
| --- | --- |
| SO1 | 禁用 Observation Router，保留 Schema 无运行影响 |
| SO2 | 配置 `capture=off`，不读取 Journal |
| SO3 | 删除 Projection DB，保留 Canonical Journal |
| SO4 | 在阶段提交前保留旧 Receipt Characterization，仅回滚完整阶段 |
| SO5 | Exporter=None，停止 Context Propagation |
| SO6 | Capture 降为 Metadata，执行 Prune |
| SO7 | Domain Fact 恢复 Full State Writer |

禁止回滚时：

- 删除 Authority Plane 数据；
- 将 Observation Graph 写回 Domain Fact；
- 用旧 Projection 覆盖新 Event；
- 静默保留用户要求删除的 Raw Payload。

## 31. 预期收益

### 31.1 故障定位

从人工查询多个数据源变为：

```text
turn id
 -> deterministic timeline
 -> failed object
 -> authority evidence
 -> payload reference
 -> unknown/inconsistency
```

目标将常见故障调查从小时级降低到分钟级。

### 31.2 崩溃可诊断

Provider、Tool、Approval、Process 或 Terminal Commit 中途崩溃后，系统仍能证明：

- 哪些工作已开始；
- 哪些结果已收到；
- 哪些结果尚未进入 Kernel；
- 哪些副作用由 Journal 观察；
- 哪些事实仍未知。

### 31.3 模型行为可解释

可以准确回答：

- 某个 Sample 的完整 Logical Request 是什么；
- 哪些内容只是 Runtime 输出；
- 哪些 Tool Result 经过裁剪后才进入模型；
- 哪个 Response 产生了某个 Tool Call；
- Compaction 前后模型可见内容如何变化。

### 31.4 多 Agent 可观测

父子 Agent 的 Spawn、Task、Message、Result 和 Integration 形成统一图，不再仅依赖
各线程局部 Event 和自报状态。

### 31.5 性能与成本治理

标准 Histogram 和 Attempt Graph 可以定位：

- TTFT 回退；
- Provider Retry 成本；
- Approval Wait；
- Tool Queue 与 Process Teardown；
- Projection Lag；
- Context/Tool Payload 放大；
- 未知价格或重复 Usage。

### 31.6 数据安全

Capture、Dump、Trace 和 Support Bundle 使用一致的 Data Class、Redaction、TTL 和
权限，不再各自定义敏感数据策略。

### 31.7 存储效率

- Payload CAS 去重避免重复大对象；
- Observation Journal 与 Protocol Event 分离，允许不同 Retention；
- SO7 的 Domain Fact Delta 目标将状态存储 P50 至少降低 50%；
- Query Projection 可删除重建，不承担 Canonical 数据成本。

## 32. 最终完成定义

State Observability 升级只有在以下条件全部满足时才算完成：

1. Turn Kernel、Domain Fact、Terminal Envelope 和 Journal 权威不变；
2. Event Durability 只有一个 Schema Source；
3. Crash 前已开始的关键对象均可观察；
4. Semantic Reducer 确定性且可从空 Projection 重建；
5. Runtime Evidence 与 Model-visible Evidence 明确区分；
6. Receipt 和 Trace 使用同一 Measurement Digest；
7. W3C Trace 覆盖 Provider、MCP、Worker 和 Subagent；
8. Observation/Exporter 故障对业务零影响；
9. Raw Payload 默认关闭、可按 TTL 完整删除；
10. P95 延迟和 Allocation Gate 通过；
11. 旧 Trace/Metric 双写路径删除；
12. CLI、TUI、VS Code 使用同一 Query Contract；
13. 文档、Schema、Golden、Architecture Test 和真实 VS Code 证据齐全；
14. Architecture Ratchet 与 Security Ratchet 不下降。

## 33. 推荐实施顺序

推荐严格按以下顺序推进：

```text
SO0 baseline
 -> SO1 schema and identity
 -> SO2 journal and crash evidence
 -> SO3 semantic reducer
 -> SO4 terminal measurement convergence
 -> SO5 W3C and OTEL
 -> SO6 privacy and retention
 -> SO7 scale and final convergence
```

不建议先接 OTEL。若没有统一 Identity、Durability 和 Local Canonical Observation，
OTEL 只会把当前碎片化状态复制到远端，增加成本而不能解决故障解释问题。

SO0-SO7 已完成。后续演进应以 `make state-observability-so7`、SO0 Baseline、
Observation Manifest 和 Architecture Ratchet 为回归边界；任何新增状态、Payload、
Exporter 或查询路径都必须先证明不会重新引入 Authority/Observation 双写、敏感数据
落盘或无界基数。
