# Tool 与本地执行架构升级方案

简体中文 | [English](../en/tool-execution-architecture-upgrade.md)

> 状态：EX3 `accepted`；EX2 `accepted`；EX1 `accepted`；EX0
> `baseline_frozen`。
>
> 基线：
> [`tool-execution-ex0-baseline.json`](../tool-execution-ex0-baseline.json)。
> EX1 证据：
> [`tool-execution-ex1-evidence.json`](../tool-execution-ex1-evidence.json)。
> EX2 证据：
> [`tool-execution-ex2-evidence.json`](../tool-execution-ex2-evidence.json)。
> EX3 证据：
> [`tool-execution-ex3-evidence.json`](../tool-execution-ex3-evidence.json)。
>
> 范围：Tool 身份、调用、结果投影、Guard 编排、资源调度、本地进程执行、
> 取消、持久终端 Session、输出准入、可观测性与迁移 Gate。
>
> 参考实现：Codex commit
> `3bbf1fe75701c97fb190e0867002ba2d9dbda5db`。

## 1. 执行摘要

CodeHelper 已有较强的执行治理基础：

- 采样时 Catalog Binding 可阻止过期或已替换 Tool 执行；
- Descriptor ResourceResolver 能推导类型化读写 Claim；
- Guard 统一执行 Policy、Approval、Hook、Resource Claims、Journal、
  Diagnostics、Sandbox 升级和网络审批；
- EditPlan 把用户看到的变更与审批内容绑定；
- ResultStore 将大结果保存在稳定的 Content Store Handle 后；
- SessionManager 已具备有界 Live Buffer、持久输出归档、Process Group 和
  Thread Lease。

问题不是缺少安全能力，而是执行语义分散在 Engine、Guard、Registry、各个 Tool
和 ProcessManager 中。当前进程域向模型暴露 15 个 Tool，Input Schema 合计
3,179 Bytes，且 15 个全部标记为 Serial。前台进程输出会在 ResultStore 准入前
完整累积；Engine 的并发 Gate 在等待审批期间仍被占用；终端与后台生命周期由多套
重叠协议表达。

本次升级保留现有安全和持久化基础，建立唯一的本地执行流：

```text
Tool Effect
    |
    v
Execution Coordinator
    |
    v
Guard Pipeline
 prepare -> authorize -> approve -> claim -> attempt -> commit
    |                                      |
    |                                      v
    |                              Local Process Runtime
    v
Typed Outcome -> Model / Host / Hook / Telemetry / Content Store 投影
```

本方案不引入远端 Executor 或 exec-server。

## 2. 目标

升级必须：

1. 在前台进程输出采集阶段限制内存，而不是等进程结束后再截断；
2. 将模型可见的本地进程协议收敛到最多三个 Tool；
3. 将 Approval 等待与稀缺的执行准入解耦；
4. 保留并强化现有资源级 Claims 并发模型；
5. 使用类型化 Invocation 和 Outcome 取代字符串 Metadata 执行协议；
6. 明确定义取消、Teardown、Detached Work 和 Terminal Publication；
7. 在每次 Session 交互时强制检查所有权；
8. 用事件驱动等待替代轮询；
9. 保持 Catalog Authority、Policy、Approval、Constitution、Journal、
   Sandbox、Egress、Diagnostics 和 Content Store 边界；
10. 保持业务循环在 `internal/runtime/agent`，构造在
    `internal/runtime/app/wire`；以及
11. 保持或改善 CE7 的 Input Token 与 Prompt Cache 指标。

## 3. 非目标

本次升级不：

- 增加远端执行、exec-server、容器或环境发现；
- 把 Tool 执行移动到 CLI、TUI、VS Code 或 ACP；
- 替换 Turn Kernel、Durable Effect 或 Runtime Protocol；
- 弱化 Strong Sandbox 或允许无 Guard Fallback；
- 用 Boolean Parallel Flag 替代 Resource Claims；
- 删除 ResultStore Handle 或持久 Job Log；
- 提供公共 Tool SDK；
- 把所有 Tool 改造成 Plugin；
- 长期保留未发布的重复进程协议；
- 用代码行数增长或减少代替架构正确性判断。

## 4. 当前架构

### 4.1 Tool Catalog

`tool.Registry` 负责 Canonical Name、Alias、Source Identity、Revision、
Authority Token、Deferred Materialization、Revocation、Schema Validation 和
Result Admission。采样得到的 `CatalogBinding` 会在执行前再次验证。

该设计比普通的 Name-to-Handler Map 更强，必须保留。升级可以引入结构化
`ToolRef`，但不能丢失 Source 与 Revision Binding。

### 4.2 Guard

`guard.Guard.ExecuteBound` 当前同时负责：

1. 参数准备和资源解析；
2. Policy 与 Permission Hook；
3. Approval 恢复和替换参数；
4. EditPlan 生成与重新校验；
5. Resource Claims；
6. Pre/Post Hook；
7. Read-before-write 与 Journal；
8. Sandbox Attempt 与升级；
9. Egress Approval 与重试；
10. Diagnostics 和 Change Receipt；
11. 最终结果返回。

重要行为是正确的，但单一控制流使 Attempt Policy、Lifecycle State 和 Cleanup
难以独立测试和演进。

### 4.3 Engine 调度

Engine 为每个模型提出的 Call 启动 Goroutine，再通过 `ToolScheduler` 准入。
Serial Call 获取独占锁，Concurrent Call 获取共享锁与有界 Slot。Guard 随后才获取
类型化 Claims。

因为 Scheduler Admission 发生在 Guard Approval 之前，Serial Call 等待人工审批时
会持有独占锁。Boolean Gate 还会串行化实际资源互不冲突的 Tool。

### 4.4 本地进程 Tool Surface

模型当前可见：

- `shell_read`、`shell_run`、`terminal_run`；
- 六个 `terminal_*` 生命周期 Tool；
- 四个 `background_shell_*` Tool；
- 两个以独立 Tool 注册的 `task_shell_*` Alias。

Start、Poll、Input、Resize、Signal 与 Close 语义发生重复。

### 4.5 输出与 Session

前台 `process.Run` 用无界 `bytes.Buffer` 保存 stdout/stderr。Engine Stream Budget
只限制 Host Event，不能限制进程采集内存。ResultStore 在命令结束、完整输出已经驻留
后才执行准入。

持久 Session 已有有界 Live Buffer 与 Durable Archive，但 `Wait` 每 10ms 轮询一次。
Thread Ownership 在创建时被记录并用于清理，却没有在交互方法中验证。

## 5. 必须保留的基础

| 基础 | 必须保持的性质 |
| --- | --- |
| Catalog Binding | 固定采样时 Name、Source、Revision 和 Authority |
| ResourceResolver | 从规范化参数推导 Policy Resource |
| Claims | 冲突资源串行，无冲突资源允许重叠 |
| Guard | 所有 Consequential Tool 使用同一安全入口 |
| EditPlan | Approval 绑定到最新且重新校验的 Plan |
| Journal | 持久写入前后生成 Before-image 与 Commit Receipt |
| Sandbox | Strong Execution Fail Closed，升级必须审批 |
| ResultStore | 完整结果通过稳定 Handle 检索 |
| Job Log | Detached Process 输出跨越 Live Buffer Horizon |
| Turn Kernel | Tool Start 与 Terminal Closure 是 Durable Fact |

不照搬 Codex 的全局 Parallel Boolean 和仅依赖 Process Store 的输出保留方式，因为
CodeHelper 已有更精确的资源与持久化模型。

## 6. 目标 Contract

### 6.1 Tool Reference

执行身份结构化：

```go
type ToolRef struct {
    Name       string
    Source     string
    CatalogID  string
    Generation uint64
    Revision   uint64
    Authority  uint64
}
```

Provider Wire 需要时仍可使用扁平名称，但 Flatten 只发生在 Provider 边界。Registry
和 Guard 使用 `ToolRef`。

### 6.2 Invocation

统一的不可变调用：

```go
type Invocation struct {
    Identity   InvocationIdentity
    Tool       ToolRef
    Arguments  json.RawMessage
    Descriptor Descriptor
    Resources  []Resource
    Source     InvocationSource
}
```

参数规范化和资源解析只执行一次。Replacement Arguments 生成新的 Prepared
Invocation，并重新执行 Authorization。

### 6.3 Typed Runtime

Tool 逐步迁移到：

```go
type Runtime[I, O any] interface {
    Descriptor() Descriptor
    Run(context.Context, I) (O, error)
}
```

现有 `typed` Package 成为 Builtin Tool 的默认适配器。只有协议原生 Dynamic
Integration 可以保留 Raw Executor。

### 6.4 Outcome

Tool 输出成为具有多种投影的类型化值：

```go
type Outcome interface {
    ModelProjection() Result
    HostProjection() HostOutput
    HookProjection() (HookOutput, bool)
    TelemetryProjection() TelemetryOutput
    Success() bool
}
```

Security Decision 与 Engine Control Flow 不再读取任意 Metadata Key。兼容 Metadata
只在 Protocol Edge 派生。

### 6.5 Execution Disposition

取消行为显式声明：

```text
abort_immediately  取消后允许放弃 Handler
wait_for_teardown  取消后等待有界 Cleanup
detached           进程在 Session Lease 下跨 Turn 存活
```

Atomic Terminal Owner 保证每个 Call ID 只有一个 Completed、Failed、Canceled、
Rejected 或 Aborted Outcome。

## 7. Guard Pipeline

Guard 继续是唯一的 Consequential Execution Gateway，但拆分为明确阶段：

```text
Prepare
  normalize -> validate schema -> expand -> resolve resources -> bind catalog

Authorize
  repository policy -> constitution -> permission hook -> approval cache

Approve
  edit preview / replacement / escalation / network request and recovery

Admit
  global execution budget -> typed resource Claims

Attempt
  sandbox plan -> local runtime -> bounded stream -> classified result

Commit
  journal -> read/write fingerprints -> diagnostics -> change receipts

Project
  Content Store admission -> typed projections -> durable terminal result
```

Approval Wait 不持有 Global Execution Slot 或 Claim。若 Approval 或 Resource State
在等待期间变化，Admission 前再次执行 Policy Check。

Sandbox 与 Network Retry 表达为类型化 Attempt Record。每次 Retry 记录 Reason、
Sandbox Posture、Approved Grant、开始/结束时间和 Terminal Classification。任何
Retry 都不能静默改变权限。

## 8. 调度模型

最终 Scheduler 分为两层：

1. 公平的有界执行预算限制 Active Handler；
2. Typed Claims 只串行化重叠资源。

`ParallelPolicy` 暂时保留给无法推导资源的 Effect，不再全局串行化所有 Process
Tool。等待 Approval、User Input 或 Future Start Time 的 Call 不属于 Active
Execution。

Scheduler 必须满足：

- 取消 Queued Waiter 时不泄漏 Helper Goroutine；
- 连续 Reader 不能饿死 Writer；
- 每个 Call 的 Release 只执行一次；
- Detached Process Spawn 后释放 Launch Claim，后续使用 Session Claim；
- Dispatch Queue 与 Handler Latency 分开计量。

## 9. 统一的本地进程协议

目标模型 Surface：

### `exec_command`

启动本地命令，参数包括：

- Command 与 Working Directory；
- 是否使用 TTY；
- 有界 Initial Yield；
- Output Token Budget；
- Timeout；
- 允许时声明 Exact Write Paths；
- 显式 Sandbox Escalation Request 与 Justification。

返回 Terminal Result，或返回用于继续交互的 Session ID。

### `write_stdin`

继续已有 Session：

- Empty Input 用于 Poll Output；
- Non-empty Input 写入 stdin；
- 可选 Rows/Columns 调整 TTY；
- 可选 Signal 支持 Interrupt、Terminate、Kill；
- Close 回收 Process Group。

每次操作都验证 Session 与 Thread Ownership。

### `shell_read`

若数据证明其更强的默认隔离和更小 Schema 能改善安全或模型选择，可以作为第三个
只读 Tool 保留；否则成为 `exec_command` 的内部 Preset。

Legacy Process Tool 必须在迁移阶段内删除，不能作为第二套已交付执行路径保留。

## 10. 输出架构

Collection 与 Model Admission 分离：

```text
process bytes
   +-> bounded live stream -> Host events
   +-> bounded head/tail collector -> immediate result
   +-> durable archive / Content Store -> complete retrieval
```

Collector 记录 Total Bytes 与 Omitted Middle Bytes。stdout/stderr 或 PTY Merged
Stream 的内存分别受固定上限控制。ResultStore 继续执行 Token-native Admission
并返回稳定 Retrieval Handle。命令即使输出数 GiB，也不能产生同等规模的 Go Heap
或单个 Protocol Event。

## 11. Session Lifecycle

SessionManager 改为 Event-driven 且 Lease-aware：

- Output Append、Exit、Failure、Close 通过 Generation Channel 或 Condition 唤醒；
- Read、Write、Resize、Signal、Wait、Close 接收 Caller Identity 并验证 Session
  与 Thread Owner；
- Detached Work 记录 Session、Thread、Turn 与 Originating Call；
- Completion 和 Pruning 不与 Interaction 或 Terminal Publication 竞争；
- 保留现有 Bounded Live Output 与 Durable Archive Cursor 语义；
- Runtime Shutdown 与 Thread Release 回收所属 Process Group。

## 12. 可观测性

每个 Tool Call 记录：

- Dispatch Wait、Approval Wait、Claim Wait、Handler、Teardown 和 Total Time；
- Catalog Identity 与 Invocation Source；
- Attempt Count 与 Sandbox/Network Decision Source；
- Collected、Streamed、Retained、Omitted、Model-projected Bytes/Tokens；
- Terminal Owner 与 Cancellation Disposition；
- Resource Claim 和 Conflict Wait Reason；
- Session Create、Interact、Complete 与 Cleanup。

Host Output 仍是 Projection，Durable Execution Receipt 是事实来源。

## 13. 迁移阶段

### EX0：Characterization Baseline

状态：`baseline_frozen`。

工作：

- 新增确定性的 `toolexecbaseline` Generator；
- 冻结 Process Tool 数量、Input Schema Bytes、Serial Tool 数量、名称、安全
  Contract、Risk 与 Hotspot Trend；
- 对 Tool Count 和 Schema Bytes 增加单调校验；
- 新增 `make tool-execution-ex0`；
- 记录完整目标架构与 Stage Gate。

不修改 Production Behavior。

退出条件：

- 当前 Process Surface 和已知风险可重复；
- Catalog Authority、Claims、Result Handle 与 Typed Adapter Availability 已验证；
- Tool、Process、Engine 聚焦测试通过；
- Architecture Ratchet 通过。

### EX1：Bounded Output Collection

状态：`accepted`。

工作：

- 引入共享 Bounded Head/Tail Collector；
- Foreground stdout、stderr、PTY 全部接入；
- 配置 Durable Store 时保留完整输出；
- 输出 Omitted Bytes 与 Total Bytes Receipt；
- 增加不按输出规模分配内存的 High-volume 与 Cancellation Test。

退出条件：

- 1 GiB Synthetic Stream 的 Retained Memory 有固定上限；
- 小命令输出 Byte-for-byte 兼容；
- Stream Cursor 单调；
- `foreground_output_bounded` 变为 true。

已交付：

- `process.Run` 默认每个 stdout/stderr Stream 最多保留 8 MiB；
- 模型可见 Shell 在 ResultStore Token Admission 前每个 Stream 最多保留 1 MiB；
- 共享 Head/Tail Collector 对小输出保持完全一致，并记录 Total、Retained 与
  Omitted Bytes；
- stdout、stderr 与 Merged PTY 使用同一有界 Collector；
- 可选同步 Archive Sink 接收每个完整 Chunk，通过 Backpressure 保持内存有界，
  Archive 降级时保留有界命令结果并报告错误；
- 1 GiB Synthetic Stream Test 证明 Collector Capacity 始终为 1 MiB。

### EX2：Typed Execution Core 与 Guard Pipeline

状态：`accepted`。

工作：

- 新增结构化 ToolRef、Prepared Invocation、Outcome、Attempt 和 Disposition；
- `typed.Define` 成为 Builtin Tool 默认路径；
- 拆分 Guard Phase，但不增加 Bypass；
- Approval Wait 移到 Execution Admission 之前；
- 输出类型化 Execution Receipt。

退出条件：

- Security Decision 不再读取任意 Result Metadata；
- Replacement Arguments 重新执行 Prepare 与 Authorize；
- Approval Wait 不占 Execution Slot 或 Claim；
- 现有 Approval 与 Journal Test 保持等价；
- Guard 仍是唯一 Consequential Entry。

已交付：

- Authority-frozen `ToolRef`、不可变 Prepared Invocation、Invocation Source、
  显式 Cancellation Disposition、Typed Outcome/Security Signal 和
  Attempt/Execution Receipt Contract；
- Registry 原生 `OutcomeExecutor` 适配，Typed Outcome 与 Execution Receipt
  可跨 ResultStore Projection 保留；
- 每个 `typed.Define` Executor 显式声明 Disposition，Foreground Shell Tool
  已迁移到 Typed Boundary，同时保留 Argument Expansion；
- Guard 拆分为 Preparation/Authorization 与 Admitted Attempt 组件；
- Engine 通过 Context 注入 Execution Admission，Guard 仅在 Resource Claims
  与实际 Attempt 紧邻前获取 Scheduler Slot；
- Initial、Sandbox Escalation、Egress Approval 均在释放 Scheduler 与 Claim 后等待；
- Sandbox 与 Egress 安全决策只读取 Typed Signal 或 Typed Error，不读取任意
  Result Metadata；
- Replacement Arguments 会生成新的 Prepared Invocation，并在执行前重新经过
  Policy 与 Permission Hook Authorization。

### EX3：Unified Process Protocol

状态：`accepted`。

工作：

- 实现 `exec_command` 与 `write_stdin`；
- 用安全与 Token 数据决定是否保留 `shell_read`；
- 迁移 Host Presentation 与 Recovery；
- 每次 Session Operation 强制所有权；
- 用 Event Notification 替换 Polling Wait；
- Stage 结束前删除被替代的模型可见 Process Tool。

退出条件：

- Model-visible Process Tool 不超过三个；
- Input Schema Bytes 比 EX0 至少降低 60%；
- 不存在重复 Process Execution Path；
- Session Ownership Denial 与 Cleanup Test 通过；
- `unified_process_protocol`、`session_owner_enforced`、
  `event_driven_session_wait` 变为 true。

已交付：

- `exec_command` 统一负责有界前台 Yield 与 Detached Pipe/PTY Session 创建；
  `write_stdin` 统一负责增量输出、stdin、Resize、Signal 与 Close；
- 保留 `shell_read` 作为第三个优化 Tool，因为它具有更小 Schema、Read
  Capability、机械强制只读 Workspace 与禁用网络；
- 删除十二个被替代的 `shell_run`、`terminal_*`、`background_shell_*` 和
  `task_shell_*` 模型协议，不保留 Alias 或第二执行路径；
- Session Read、Wait、Write、Resize、Signal、Close 均验证调用方 Thread Lease，
  Thread Cleanup 则保留显式 Manager-owned 路径；
- Session Wait 改为由输出与进程退出驱动的有界通知 Channel，不再使用十毫秒
  Ticker；
- Pipe 与 PTY Process 共用一个 Session State Machine、有界 Live Buffer、
  Durable Archive Cursor、Timeout、Process-group Cleanup 与 Job Center Projection；
- Initial Output Aggregation 由请求的 `output_tokens` 预算进行 Head/Tail
  有界保留，Detached 完整输出继续写入 Job Log Archive；
- Model-visible Process Surface 从 15 个 Tool、3,179 Schema Bytes 降为 3 个
  Tool、1,112 Schema Bytes，降幅 65.02%，三项 EX3 风险控制探针均变为 true。

### EX4：Resource Scheduler 与 Cancellation

工作：

- 用 Fair Budget Admission + Claims 替换 Global Serial RW Gate；
- 增加显式 Cancellation Disposition 与 Terminal Ownership；
- Teardown 有界且可观测；
- Detached Process Interaction 使用 Session Claim；
- 增加确定性的 Race 与 Starvation Test。

退出条件：

- 无关 Resource 可以并发；
- Approval Wait 不阻塞无关执行；
- Hermetic Suite 的 Cancellation P95 小于 2 秒；
- 每个 Call 只有一个 Terminal Outcome；
- Session/Thread Close 后无 Orphan Process。

### EX5：Convergence 与 Cleanup

工作：

- 迁移剩余 Builtin 到 Typed Outcome；
- 删除 Engine Business Logic 对兼容 Metadata 的读取；
- 删除 Legacy Adapter 与过期 Test；
- 收紧 Architecture 与 Schema Baseline；
- 同步中英文运维文档；
- 运行完整 Hermetic、Race、Security、VS Code、Docs、Token Gate。

退出条件：

- 只剩一条 Local Execution Path；
- Input Token P50 对 CE7 零回退；
- Prompt Cache Continuity 零回退；
- Security Coverage 保持 100%；
- Architecture Ratchet 无回退；
- 所有版本化 Evidence 已提交。

## 14. 验收 Gate

| 领域 | Gate |
| --- | --- |
| Tool Surface | Model-visible Process Tool 不超过 3 个 |
| Schema | 比 EX0 的 3,179 Bytes 至少降低 60% |
| Memory | Retained Foreground Output 有固定配置上限 |
| Concurrency | Disjoint Claims 并发，Conflicting Claims 串行 |
| Approval | Wait Time 不占 Execution Admission |
| Cancellation | P95 小于 2 秒，每个 Call 只有一个终态 |
| Cleanup | Session/Thread Shutdown 后 Orphan Process 为 0 |
| Ownership | 所有 Session Operation 拒绝 Foreign Thread |
| Security | 无 Guard、Policy、Approval、Journal、Sandbox Bypass |
| Token | Input Token P50 对 CE7 零回退 |
| Architecture | Ratchet、Docs、Race、Security Gate 通过 |

## 15. 验证

每阶段先运行聚焦 Package，再运行：

```bash
make tool-execution-ex0
go test -race ./internal/adapter/tool/... ./internal/platform/process \
  ./internal/runtime/agent/engine
make docs-check
make book-check
git diff --check
```

修改 Host Projection 的阶段还需运行 VS Code Protocol、Check 与相关 Test。影响
Token 的阶段运行现有 CE7 Comparison Lane。

## 16. Rollback 与失败策略

- Stage 不得以新旧两套 Production Execution Path 同时存在的状态合入；
- Sandbox 或 Ownership Check 失败时 Fail Closed；
- Output Archive 失败时保留有界 Live Output 并报告 Recoverability Degraded，
  不能静默声明完整保留；
- Terminal Publication 失败时通过现有 Turn Effect 与 Outbox 恢复；
- Baseline 改善后收紧后续 Limit，回退必须有显式 Architecture Decision，不能直接
  覆盖历史 Baseline。

## 17. Ownership

| Concern | Owner |
| --- | --- |
| Tool Contract 与 Registry | `internal/adapter/tool` |
| Guard Phase 与 Attempt | `internal/adapter/tool/guard` |
| Local Process 与 Session Primitive | `internal/platform/process` |
| Sandbox Enforcement | `internal/security/sandbox` |
| Tool Batch、Scheduling、Terminal State | `internal/runtime/agent` |
| Construction | `internal/runtime/app/wire` |
| Durable Output 与 Receipt | `internal/persist`、`internal/observability` |
| Host Presentation | `internal/runtime/eventview`、`extensions/vscode` |

整个升级过程必须遵守这些边界。
