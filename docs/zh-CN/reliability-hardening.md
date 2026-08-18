# 运行时可靠性系统治理

简体中文 | [English](../en/reliability-hardening.md)

本文是 CodeHelper 可靠性问题的长期治理清单。它用于组织扫描、根因归并、统一修复和
验收证据，不代表其中能力已经交付。已交付行为仍以代码、测试、协议 Schema 和产品
文档为准。

## 目标与边界

治理覆盖完整执行链：

```text
CLI / TUI / VS Code / ACP / Worker
                  |
          Operation / Event
                  |
      Application / Turn Kernel
                  |
  Context / Provider / Guarded Tool
                  |
 Policy / Approval / Journal / Sandbox
                  |
 Persistence / Recovery / Observability
```

目标不是消灭所有错误，而是保证错误发生时系统能够：

1. 保留已经取得的进展；
2. 给出稳定、结构化、可定位的失败原因；
3. 在满足安全约束时恢复，而不是重复整轮工作；
4. 无法恢复时进入明确终态或持久等待态；
5. 通过一次共享边界修复覆盖所有 Host 和执行入口。

本文不接受为单个症状增加续写次数、步骤数、Token 数或超时时间等局部补丁。资源仍需
治理，但隐式固定上限不能把仍有进展的任务直接变成终态错误。显式的用户或运维预算
耗尽时，也必须保留可恢复状态并解释停止原因。

## 核心可靠性不变量

后续所有扫描和修复均以这些不变量为判断基线：

1. **单一终态所有者**：只有 Turn Kernel 可以决定 Completed、Failed 或 Canceled；
   Provider、Tool、Host 和预算模块只能提交事实或请求收敛。
2. **进展不会被隐式硬限制终止**：仍在产生有效进展时，固定步骤、续写次数、输出长度
   或墙钟时间不能直接终止任务。
3. **交互等待是结构化状态**：Input 和 Approval 必须持久化为可恢复等待，不能依赖
   文本推断，也不能伪装成失败。
4. **副作用先记录后执行**：有后果的 Effect 必须具备稳定 Operation ID、执行前事实和
   可重放结果；不确定结果不得被当作未执行。
5. **部分输出不静默丢失**：Provider 断流、残缺 Tool Call 和中断的流式参数必须保留
   或明确回退到完整请求。
6. **持久状态与终态原子一致**：最终输出、Receipt、Session Delta、Terminal Event 和
   Outbox 必须共享一个提交边界。
7. **取消可传播且不破坏提交**：取消能够到达所有子操作，但不能撤销已经提交的事实，
   也不能留下永久 Running 状态。
8. **Host 只做投影**：CLI、TUI、VS Code、ACP 和 Worker 不得自行补执行、补终态或
   重新解释 Runtime 失败。
9. **失败可重建**：一次失败必须能够通过 Session、Turn、Operation、Attempt、Effect
   和 Resume 标识还原。
10. **安全失败保持 Fail-closed**：可靠性恢复不得绕过 Policy、Approval、Constitution、
    Journal 或 Sandbox。

## 状态与优先级

| 状态 | 含义 |
| --- | --- |
| `待评估` | 尚未完成基于证据的现状扫描 |
| `扫描中` | 正在收集代码、测试、运行记录和故障注入证据 |
| `修复中` | 根因和共享所有权已确认，正在实现统一修复 |
| `已验证` | 验收项已通过，并记录代码、测试和文档证据 |
| `阻塞` | 存在明确外部依赖，且已记录解除条件 |

优先级定义：

- `P0`：可能导致任务错误终止、重复副作用、状态损坏或无法恢复；
- `P1`：可能导致跨 Host 不一致、资源泄漏、不可诊断或显著降低恢复成功率；
- `P2`：提高长期运行、跨平台和发布期的可靠性。

## 工作流总览

| ID | 工作流 | 优先级 | 状态 | 主要所有权 |
| --- | --- | --- | --- | --- |
| R0 | 失败基线与全仓限制清点 | P0 | 已验证 | Runtime / Engineering |
| R1 | Turn 状态机与终态收敛 | P0 | 已验证 | `internal/runtime/agent` |
| R2 | 动态预算、进展检测与 Context | P0 | 已验证 | Agent / Context / Config |
| R3 | Provider 流式输出与残缺调用恢复 | P0 | 待评估 | Provider / Agent |
| R4 | 类型化错误、Retry 与 Deadline | P0 | 修复中 | Protocol / Runtime / Adapters |
| R5 | 持久化、Journal、幂等与崩溃恢复 | P0 | 修复中 | Persist / Runtime |
| R6 | Tool、Guard、Sandbox 与副作用一致性 | P0 | 待评估 | Tool / Security / Platform |
| R7 | 并发、取消、背压与资源生命周期 | P1 | 待评估 | Runtime / Platform |
| R8 | Protocol 与多 Host 行为一致性 | P1 | 待评估 | Protocol / Hosts |
| R9 | 启停、装配、配置与环境差异 | P1 | 待评估 | Wire / Config / Hosts |
| R10 | 可观测性与失败重建 | P1 | 待评估 | Observability / Persist |
| R11 | 故障注入与可靠性门禁 | P1 | 待评估 | Tests / CI |

## R0：失败基线与全仓限制清点

**扫描范围**

- 所有 `panic`、`log.Fatal`、`os.Exit`、忽略错误和字符串错误分类；
- 所有步骤数、重试数、续写数、Token、队列、Activity 和输出长度限制；
- 所有固定 Timeout、Deadline、Ticker、Lease 和 Heartbeat；
- 所有异步边界、持久化边界和可能产生外部副作用的位置；
- 近期失败记录中重复出现的退出路径和恢复失败路径。

**统一方向**

- 形成一份“限制用途清单”，区分容量保护、背压、收敛请求和终态决策；
- 形成完整的 Failure Taxonomy 与状态迁移图；
- 将同一根因的跨模块症状归并到一个共享 Owner，不按报错位置分别修补。

**完成证据**

- 每个隐式限制都有 Owner、用途、触发行为和移除或保留结论；
- 每个退出点都能映射到类型化 Fault 和 Kernel 状态；
- 失败样本能够映射到 R1-R11 中的一个主工作流。

### R0 基线结果（2026-08-18）

`R0` 的“已验证”只表示扫描和根因归并已经完成，不表示下列问题已经修复。基线对应
`main@9b12c5a`，后续实现工作使用下列稳定 ID 跟踪。

| 项目 | 基线 |
| --- | --- |
| 全仓源文件 | 1,279 个：Go 1,104、TypeScript 146、Python 20、Shell 9 |
| 重点生产代码 | Go 601 个、VS Code TypeScript 87 个 |
| 并发候选边界 | 68 个 Goroutine 启动点、138 个 Channel 声明或构造点 |
| 持久化候选边界 | 167 个 Transaction/Commit/Rollback 相关点 |
| 外部副作用候选边界 | 308 个文件、进程、网络或系统调用相关点 |
| 非测试退出点 | 27 个 `panic`、10 个 `os.Exit`、0 个 `log.Fatal` |
| 可靠性测试基础 | 259 个按名称可识别的恢复/取消/重试等测试，29 个显式故障注入点 |

这些数字是静态候选边界，不是缺陷数量。`panic` 中有 16 个是 goja Host Function
向 JavaScript 抛出异常的实现机制；`os.Exit` 只出现在进程入口、隔离 Helper 和 Schema
Generator。它们仍进入分类清单，但不应被机械删除。

### 限制用途清单

| 领域 | 当前规则 | 分类 | R0 结论 | 后续 |
| --- | --- | --- | --- | --- |
| Main Turn | 默认 256 Steps，Profile 最大 1,000；达到后请求一次 Finalization | 隐式收敛上限 | 不能仅按计数收敛仍有进展的 Turn；改为显式预算或动态 Lease | R1、R2 |
| 无进展检测 | 普通 Turn 在 16/32/48 Samples 分级收敛，Research 为 8/12/16 | Kernel 收敛策略 | 保留结构化状态机，但阈值必须由进展语义和可观测策略驱动 | R1、R2 |
| Repair | Completion/Workspace/Declaration/Verification 默认为 2/1/2/1 Steps | Repair Budget | 不能把新进展当作连续失败；耗尽后必须保留可恢复状态 | R1、R2 |
| Provider | Request Timeout 2 分钟，Idle Timeout 1 分钟 | 固定 Deadline | `http.Client.Timeout` 会终止仍在流式产出数据的请求，必须拆成连接、空闲和可续期租约 | R2、R4 |
| Provider Retry | 默认配置为 0，但类型化 Retryable Fault 至少重试 1 次，Empty Response 固定 1 次 | 隐式 Retry Count | Retry 应由 Fault、幂等性、进展和总预算决定，不能由 Adapter 局部计数决定 | R3、R4 |
| Subagent | 默认 24 Steps、5 分钟 Wall Time；到期后 Cancel 并记为 Errored | 固定终止器 | 改为 Parent Budget 下的可续期 Lease 和可恢复结果 | R2、R7 |
| Workflow / JS VM | 默认 256 Steps；Lifetime 1,000、Parallel Items 1,000、并发 16 | 混合预算 | 容量保护可保留；Step/Lifetime 耗尽必须持久挂起而非 Cancel 整个 Run | R2、R7 |
| Worker | 默认 1 Attempt、30 秒 Lease、1 秒 Claim Interval | Durable Attempt Budget | 显式任务预算可保留，但默认值、Retry 和 Lease 必须共享类型化原因 | R4、R7 |
| VS Code Supervisor | 250/500/1,000ms 后重启，3 次后进入 Failed | Host 局部 Retry Count | Host 不能用固定次数决定 Runtime 工作永久失败；应进入可操作的持久恢复状态 | R8、R9 |
| Runtime / Control Queue | Operation、Subscriber、Turn Mailbox 默认均为 64 | 背压容量 | 容量必须保留，但关键控制不能只返回 `resource_exhausted` 后由调用方猜测是否重发 | R7、R8 |
| Replay / Frame | ACP Replay 256，Frame 4 MiB，History 通过分页读取 | 传输容量 | 保留分页和安全上限；所有 Host 必须共享 Continuation/Desync 契约 | R8 |
| Tool / Process Output | Shell 默认 4,096、最大 10,000 Tokens；Process 保留 1/8 MiB 并支持 Archive | 结果保留容量 | 不作为任务终止器；截断必须有 Receipt、Handle 或 Durable Archive | R2、R6 |
| MCP / Web / Hook / Git | 存在 250ms 至 2 分钟的固定 Call、Connect、Close 和 Shutdown Deadline | 操作 Deadline | 按连接、空闲、调用、关闭分类；不得把 Cleanup Timeout 解释为业务失败 | R4、R7、R9 |
| Context / Payload / Security | Fragment、文件、Schema、Frame、Manifest 和 Sandbox 路径数量上限 | 安全或内存边界 | 保留并集中登记；触发时返回结构化容量结果，不得静默截断关键状态 | R2、R6、R8 |

以下限制不是本轮要消除的对象：模型真实 Context/Output 能力、显式用户 Cost/Token
预算、协议分页、安全载荷上限、Sandbox 路径上限，以及带 Truncation Receipt 和完整
Archive 的展示保留上限。问题在于隐式终止、静默丢失和多个 Owner 对同一预算重复
决策。

### 根因清单

| ID | 优先级 | 根因与证据 | 主工作流 |
| --- | --- | --- | --- |
| R0-001 | P0 | Main Turn、Subagent、Workflow、Verification 和 JS VM 分别拥有固定 Step/Wall-time/Lifetime 终止器。Kernel 虽把 Main Turn 的 Step Limit 转成 Convergence，但它仍可能收敛一个持续产生新进展的任务。 | R1、R2 |
| R0-002 | P0 | Provider 总请求 Timeout、Subagent Wall Time 及多个 Tool 30 秒上限混淆了连接、空闲、业务操作和执行 Lease。尤其 `internal/runtime/app/wire/modules_provider.go` 把 2 分钟配置写入 `http.Client.Timeout`，流式进展不能续期。 | R2、R4 |
| R0-003 | P0 | Provider Retry、Worker Attempt、Workflow Retry 和 VS Code Runtime Restart 分别使用本地次数。当前 Provider 默认最多保证一次 Retry，VS Code 三次启动失败后永久进入 `failed`。 | R3、R4、R9 |
| R0-004 | P0 | 若干关键错误被丢弃：Terminal Event 的 `send`、Turn Coordinator `Release`、Checkpoint 发布失败后的内存回滚、Child `Settle`、Lane 状态持久化、Process Session Journal，以及 Observation 异步写入。它们可能造成“已完成但未记录”、Lease 残留或永久 Running。 | R1、R5、R7、R10 |
| R0-005 | P1 | 已有统一 `Problem/Fault`，但 Metadata 尚无 Stage、Operation/Effect/Attempt ID；未分类错误默认映射为可恢复 `unavailable`。生产代码仍有 Egress、VS Code Revocation 和 TUI 投影依赖错误文本。 | R4、R8、R10 |
| R0-006 | P1 | 11 个非 goja Runtime `panic` 包括随机 ID 生成、Process Output 不变量、非法 Permission Kind 及静态 Catalog/Manifest 加载。静态 Must Helper 可保留在构建事实边界；运行期输入和熵源失败必须返回类型化 Fault。 | R4、R9 |
| R0-007 | P1 | Queue、Replay、Frame、Output、Context 和 Payload 上限分散在 Config、Protocol、Host 与 Adapter 中，触发行为包括 Reject、Truncate、Drop、Desync、Converge 和 Panic，没有统一容量语义。 | R2、R6、R7、R8 |
| R0-008 | P1 | `max_steps` 同时存在于 Go Defaults、Engine Fallback、Session Profile、ACP、VS Code Setting 和 Package Schema；Timeout/Retry 也有类似重复默认值。Host 仍能改变 Runtime 终止策略。 | R8、R9 |
| R0-009 | P1 | 已有 Terminal Atomicity、Outbox、Lease、Provider Disconnect 和 Tool Cancel 等强测试，但缺少跨所有异步边界的矩阵，特别是持续进展时的 Deadline、被忽略的 Terminal/Release 错误、Disk Full、Observation 写入失败和 Host Restart 耗尽。 | R5、R7、R10、R11 |

### 退出与错误处理结论

- 进程级 `os.Exit` 均位于合法进程边界，保留；
- goja Host Function 的 `panic(runtime.NewGoError(...))` 会在 VM 边界 Recover，可保留，
  但当前 `fmt.Errorf("%v", recovered)` 会丢失类型链，应纳入 R4；
- `DefaultCatalog`、`MustLoad`、`MustReadiness` 等静态 Must Helper 只允许处理编译期内嵌
  事实，并由测试保证；不得扩展到用户输入或运行时外部状态；
- 随机 ID、Permission 映射和 Process Output 的运行期 `panic` 应改成类型化错误并在
  正确 Owner 处收敛；
- Cleanup 的 Best-effort Close/Remove 可以忽略主返回值，但必须进入 Secondary Issue
  或 Health；会改变 Durable State、Lease、Terminal、Result 的错误不得忽略。

### 异步与故障注入基线

现有测试已经覆盖 Event Log Torn Tail、Domain Fact/Terminal Commit 失败、Terminal
Outbox 恢复、Journal Draft 恢复、Lease Fencing、Provider SSE 断流、Tool Cancel、
Approval/Input 恢复和 VS Code Cursor Replay。这说明 Durable Kernel 基础较强，但覆盖
集中在少数边界，尚未形成“边界 × 故障 × 预期状态 × 恢复动作”矩阵。

本次执行 `go test -count=1 ./...` 时出现三个时间敏感失败：

- MCP Stdio Fixture 在 5 秒 Context 下初始化超时；
- 两个 Lane 测试在 5 秒轮询窗口内保持 `running`。

对应测试单独 `-count=5` 均通过，因此当前证据指向并行负载下的固定 Deadline Flake，
而不是稳定功能失败。仓库标准串行 `make test-hermetic` 全量通过；`npm run check`
通过；`npm test -- runtime` 为 54 通过、4 个真实 Runtime Integration 场景跳过。
R11 应把这些跳过项和时间敏感用例转换为确定性门禁。

### R0 移交顺序

1. R1/R2 先统一 Progress、Convergence、显式 Budget 和可续期 Execution Lease；
2. R4 定义完整 Fault/Deadline/Retry 决策，再由 R3 接入 Provider Partial Stream；
3. R5 修复所有 Durable/Terminal/Lease 错误丢弃点；
4. R7/R8/R9 统一 Queue、Host Restart、配置来源和资源生命周期；
5. R10/R11 建立 Failure Reconstruction 与全边界故障矩阵。

## R1：Turn 状态机与终态收敛

**当前进展（2026-08-18）**

- Main Turn 的显式 Step Budget 已冻结进 Turn Kernel Policy，由 Reducer 根据已完成
  Sample 统一触发 Convergence；Engine 外层循环不再拥有固定步数终止器；
- 默认 Main Turn、Subagent 和 Workflow `max_steps` 已改为 `0`，不再注入隐式上限；
- Kernel 授权的 Repair Steps 仍使用独立连续无进展预算，不会被普通工作预算吞掉；
- Workflow 显式预算耗尽已改为 Durable `blocked`，保留节点并支持 Resume，不再
  Cancel 整个 Run；
- Terminal Projection 失败会返回可恢复 Fault；Turn Coordinator Release 失败会进入
  `secondary_issues` 并由 Durable Runtime 持续重试，Child WorkGraph/Manager Settle
  也不再丢弃错误；
- `state_graph_property_test.go` 的可执行状态图覆盖全部 11 个 Kernel Phase、三种终态、
  非法转换、重复/晚到 Command，以及独立 Tool/Approval 结果的乱序等价性；拒绝的
  Command 必须保持原 State Digest 不变；
- Approval 和 Input 等待通过真实 Durable Domain Facts 重建 Coordinator，恢复前后
  State Digest 与 Request ID 保持一致。

**验收结果（2026-08-18）**

- 状态图、终态唯一性、Command 排列和跨进程交互恢复测试通过；
- `internal/runtime/agent/turnkernel`、Agent Engine、Workflow、Protocol 和 Wire
  定向测试通过；
- Hermetic、Race、Architecture Ratchet、VS Code、Docs 和 Book 门禁通过。

**扫描范围**

- Start、Running、Awaiting Input、Awaiting Approval、Verifying、Finalizing、
  Completed、Failed、Canceled 和 Suspended 的全部转换；
- 重复完成、非法跳转、晚到结果、恢复后重复请求和永久 Running；
- `turn_complete`、`request_user_input`、Approval、Cancel、Continue 和 Journal
  结果的所有组合；
- Main Turn、Child Turn、Workflow 和恢复 Turn 的语义差异。

**统一方向**

- 用一个 Reducer/Kernal 状态机拥有终态和收敛决策；
- Provider 停止、预算信号和 Tool 失败只生成类型化 Command；
- 对状态图运行模型测试或属性测试，验证所有非等待状态最终可收敛。

**完成证据**

- 状态转换表与代码一致，并覆盖非法转换；
- 重放、重复、乱序和晚到 Command 不会产生第二个逻辑终态；
- 所有交互等待都能跨进程恢复原 Request ID。

## R2：动态预算、进展检测与 Context

**当前进展（2026-08-18）**

- `0 = 未设置` 已贯通 Config、Session Profile、CLI、ACP/VS Code 解码和 Engine；
  历史默认 Profile 的 `8/64/256` 会迁移为 `0`，显式用户预算保持不变；
- 持续变化的 Progress Signature 不受总 Sample 数限制；无进展 8/12/16 或
  16/32/48 策略仍由 Kernel 持久化并收敛；
- Subagent `wall_time` 已从绝对 Wall-clock 终止器改为可续期执行 Lease，Runtime
  进展会续期，空闲到期进入可恢复 `interrupted`；
- Workflow 默认不再注入 256 Steps，显式 Step/Token/Cost Budget 耗尽进入
  `blocked`；
- Provider 不再使用 `http.Client.Timeout` 总墙钟：连接、TLS 和响应头使用独立阶段
  Timeout，流事件使用可续期 Idle Timeout，调用生命周期由 Turn Context/Lease
  所有；
- Context 压缩候选在提交前验证 Authority Capsule：Goal、Plan/Todo、Failure、
  Change、Critical Path、Evidence Fact/Handle 必须实体级等值；Tool Pair 必须保持
  闭合。Receipt 和 Protocol Event 携带 Authority Digest 与等价性结果；
- Protocol 提供统一 `BudgetExhaustion`：Main Turn、Workflow 和 Child 的显式
  Token/Cost 耗尽都携带 Scope、资源类型、Used/Limit、`resource_exhausted` 和
  `resume_turn`；预算未提高前不会自动重试；
- Workflow 的预算耗尽分支全部进入 Durable `blocked`，后台 Workflow Task 投影为
  `waiting` 而不是 `failed`；增加预算后可从 WorkGraph 恢复，已完成节点不会重跑；
- Session Delta 持久化 Turn 与模型可见历史分组绑定。Continue 根据
  `SourceTurnID` 只移除已被 Recovery Capsule 取代的源分组，保留更早事实和闭合
  Tool Pair；连续 Continue 和跨进程恢复不会递归注入旧 Recovery Prompt。

**验收结果（2026-08-18）**

- Main/Workflow/Child Token 与 Cost 耗尽共享同一可恢复 Fault 契约；
- 恢复历史身份、连续 Continue、Session Delta 重启恢复及压缩后绑定清理测试通过；
- 持续进展、无进展收敛、Context Authority 等价和 Tool Pair 闭合测试通过；
- Hermetic、Race、Architecture Ratchet、VS Code、Docs 和 Book 门禁通过。

**扫描范围**

- Reasoning Effort、Output Reserve、Context Window、Tool Spend、Cost 和时间预算
  是否错误耦合；
- 固定 `max_tokens`、`max_steps`、续写次数和 Wall-clock Timeout；
- Context 压缩是否丢失未完成 Tool Call、交互请求、审批或恢复状态；
- Resume Prompt 是否重复注入历史并递归膨胀。

**统一方向**

- 分离推理策略、输出容量、Context 容量、成本和执行租约；
- 建立基于新事实、Tool 结果、状态变化和有效输出的统一进展检测；
- 容量压力触发压缩、背压、收敛请求或可恢复挂起，不直接伪造终态；
- 显式预算耗尽时生成结构化原因、当前进展和继续执行入口。

**完成证据**

- 大型 Tool 参数和长回答不会因 Reasoning 档位被截断；
- 持续有进展的任务不会被隐式计数器终止；
- 无进展循环能够确定性收敛，且不会无限重复原 Prompt；
- 压缩前后关键状态具备等价性测试。

## R3：Provider 流式输出与残缺调用恢复

**扫描范围**

- EOF、连接重置、限流、空响应、重复 Chunk、乱序 Chunk 和缺失 Finish Reason；
- 半个 JSON、半个 Tool Call、多个并行 Tool Call 和部分 Reasoning；
- Responses WebSocket 与完整请求之间的切换和 Response Chain 失效；
- Usage、逻辑请求和传输请求的归因是否一致。

**统一方向**

- 建立可持久化的增量响应组装器和明确的完整性状态；
- 残缺 Tool Call 保留原始片段，继续同一逻辑请求或安全回退；
- 只有明确支持且链路确定时使用增量传输，状态不确定即回退完整请求；
- Provider 差异在 Adapter 层规范化，不能泄漏为 Agent Loop 分支。

**完成证据**

- 在任意 Chunk 边界断流均不会静默丢失已确认数据；
- Retry 不会重复执行已闭合 Tool Call；
- Provider 契约测试覆盖断流、重复、乱序、空响应和格式漂移。

## R4：类型化错误、Retry 与 Deadline

**当前进展（2026-08-18）**

- Provider Deadline 已拆成 Connection、TLS Handshake、Response Header 和 Stream
  Idle 四个阶段；持续进展的流不再受固定总墙钟限制；
- Terminal Projection 失败返回 `RetryStep` Fault 并保持可恢复状态，Awaiting
  Recovery Projection 错误也会和主 Fault 一起返回；
- 尚需统一 Provider、Worker、Workflow 与 Host 的 Retry Owner 和 Deadline
  Metadata。

**扫描范围**

- 字符串匹配错误、过度 Wrap 后丢失 Cause、错误阶段缺失；
- 可重试错误未重试、永久错误反复重试和多个层级同时重试；
- Timeout 是否混淆连接、空闲、单操作、Turn 租约和用户预算；
- Retry 是否缺少幂等性、退避、抖动和进展判断。

**统一方向**

- 定义跨 Protocol、Runtime 和 Adapter 的稳定 Fault 分类；
- Fault 至少携带 Kind、Stage、Retryability、Operation ID、Cause 和 Resume Hint；
- 由一个恢复策略根据 Fault、幂等性和进展选择 Retry、Resume、Wait、Block 或 Fail；
- Deadline 使用可传播、可配置、可续期的分层语义。

**完成证据**

- 每个错误只由一个层级决定 Retry；
- 相同 Fault 在所有 Host 中具有相同机器语义；
- Timeout 能明确指出作用域，且不会留下无法恢复的 Running 记录。

## R5：持久化、Journal、幂等与崩溃恢复

**当前进展（2026-08-18）**

- Turn Coordinator Release 在终态提交前执行，失败写入 Terminal Secondary Issue；
  Durable Runtime 将失败项放入持续重试集合并停止续租，成功后才清理内存 Coordinator；
- Coordinator Open/Restore 回滚错误会与主错误聚合返回；
- Child WorkGraph 与 Manager Settlement 使用幂等、无固定次数的后台重试；恢复期间新
  Child Turn 返回结构化 `unavailable`，不再删除 Settlement 错误；
- 尚需继续清理 Checkpoint 回滚、Observation 写入和 Process Journal 等 R0-004
  剩余错误丢弃点。

**扫描范围**

- SQLite、Event Log、CAS、Session、Snapshot、Journal 和 Outbox 的事务边界；
- “执行成功但记录前崩溃”和“记录成功但投影前崩溃”；
- Terminal Commit、Session Delta 应用、Outbox 发布和恢复扫描；
- 数据损坏、磁盘满、进程被杀和版本变化。

**统一方向**

- 以 Operation、Effect 和 Attempt 标识建立逻辑 Exactly-once Closure；
- 对外部副作用采用 Prepared、Started、Result Known、Committed 或 Outcome Unknown；
- 终态事实共享原子提交，内存状态只在提交成功后应用；
- 恢复过程重放稳定事实，不重新生成已经完成的工作。

**完成证据**

- 在每个持久化写入点注入崩溃后，恢复结果仍确定；
- Terminal Event 不丢失、不重复产生逻辑终态；
- Outcome Unknown 有专用处理路径，不会盲目重试副作用。

## R6：Tool、Guard、Sandbox 与副作用一致性

**扫描范围**

- Tool Registry、Guard、Policy、Permission、Approval、Constitution、Journal 和
  Sandbox 的完整调用链；
- 文件修改、Shell、网络、Git、MCP 和外部服务操作的幂等边界；
- stdout、stderr、Exit Code、部分输出和 Sandbox 拒绝是否被完整保留；
- 环境故障是否被错误包装成策略拒绝，或反向绕过安全边界。

**统一方向**

- 所有后果型 Tool 共享一套 Guarded Effect 协议；
- 执行前持久化意图与审批证据，执行后持久化结构化结果；
- 明确区分 Denied、Unavailable、Execution Failed 和 Outcome Unknown；
- 恢复与 Retry 继续经过 Guard，不能复用过期授权。

**完成证据**

- 不存在从 Host、Workflow、Subagent 或扩展绕过 Guard 的执行路径；
- 重放不会重复应用文件或外部副作用；
- macOS、Linux、Windows 能力差异产生结构化且可测试的结果。

## R7：并发、取消、背压与资源生命周期

**扫描范围**

- Goroutine 泄漏、阻塞 Channel、重复 Close、锁顺序、数据竞争和悬挂进程；
- 慢 Host、慢 Provider、慢 Tool 和满队列时的行为；
- Parent/Child、Workflow、Worker 和外部进程的取消传播；
- Mailbox、Event Hub、Outbox 和流式输出的背压策略。

**统一方向**

- 明确每个 Goroutine、Channel、Process 和 Subscription 的 Owner；
- 使用结构化并发和单向取消传播，提交边界之后只停止后续工作；
- 队列满时产生可观测背压或持久化排队，不静默丢弃关键状态；
- 关闭流程按依赖逆序执行并聚合错误。

**完成证据**

- Race、Leak 和取消风暴测试稳定通过；
- 慢消费者不会拖死 Runtime，也不会丢失 Terminal Event；
- 取消后所有资源在有界观察期内释放，状态进入合法终态或等待态。

## R8：Protocol 与多 Host 行为一致性

**扫描范围**

- Operation、Event、Receipt、Problem/Fault 和 Terminal Data 的 Schema；
- 事件重复、遗漏、乱序、未知类型和版本差异；
- Go `eventview`、CLI/TUI、ACP 与 VS Code Projector 的解释差异；
- Host 是否自行推断完成、失败、审批或恢复。

**统一方向**

- Event Traits 和生成 Schema 作为事件语义的唯一数据源；
- Host 只投影结构化 Runtime 事实；
- 通过同一事件录制驱动所有 Host 的 Conformance Test；
- 协议变更使用仓库生成命令，不手改生成文件。

**完成证据**

- 同一事件序列在所有 Host 得到等价状态；
- 新增 Event 缺少 Trait 或投影处理时构建失败；
- Replay、Reconnect 和 Unknown Event 行为有明确契约。

## R9：启停、装配、配置与环境差异

**扫描范围**

- `wire.NewExec` 各模块构造失败时的回滚和资源关闭；
- Journal、Provider、MCP、Sandbox、Scheduler 和 Host Process 启动顺序；
- CLI、ACP、VS Code、Workflow 中重复或冲突的默认值；
- macOS、Linux、Windows、CI、Remote SSH 和隔离 Worktree 差异。

**统一方向**

- 保持单一 Composition Root 和逆序 Resource Stack；
- 配置具有单一 Schema、来源优先级、Provenance 和运行时快照；
- 后台服务只在 Runtime 恢复完成后启动；
- 能力缺失通过 Probe 和类型化 Unavailable 表达。

**完成证据**

- 每个构造步骤失败都不会泄漏已创建资源；
- 相同配置在所有 Host 解析为同一 TurnSpec；
- 平台差异测试不会依赖静默降级。

## R10：可观测性与失败重建

**扫描范围**

- Session、Thread、Turn、Operation、Effect、Attempt、Lease 和 Resume 关联；
- 状态迁移、Retry 决策、预算变化、恢复来源和终态原因；
- 日志、Trace、Receipt 和诊断信息中的秘密或超大载荷；
- 指标是否能够区分用户取消、策略拒绝、资源不足和内部缺陷。

**统一方向**

- 每个异步边界传播稳定 Correlation ID；
- 记录决策输入和结构化结果，不依赖自然语言日志还原状态；
- 对敏感和大体积内容只记录摘要、Digest 和受控引用；
- 建立面向失败重建的查询或诊断输出。

**完成证据**

- 任一失败 Turn 可以回答“在哪一层、为何停止、尝试了什么、能否继续”；
- Retry、Resume 和重复副作用能够通过 ID 关联；
- Secret Leak Test 和观测 Schema Gate 通过。

## R11：故障注入与可靠性门禁

**扫描范围**

- Provider、Tool、Approval、Input、Journal、SQLite、Outbox、Host 和 Shutdown
  等异步边界；
- 断流、延迟、重复、乱序、磁盘满、进程崩溃、权限变化和取消竞态；
- 当前 Unit、Contract、Integration、Race、Fuzz 和长时间测试的覆盖缺口。

**统一方向**

- 建立“边界 × 故障 × 预期状态 × 恢复动作”的故障注入矩阵；
- 优先使用确定性 Fake、Fixture 和 Crash Point，减少时间敏感测试；
- 将核心不变量变成属性测试和 Architecture/Reliability Gate；
- 线上新故障先补可复现 Fixture，再修共享根因。

**完成证据**

- P0 边界至少覆盖成功、可重试、永久失败、取消和崩溃恢复；
- 故障测试能够证明无重复副作用、无终态丢失和无永久 Running；
- 可靠性门禁纳入标准验证命令并保持可重复。

## 推荐执行顺序

为避免多个局部修复同时改变同一语义，默认只允许一个主工作流处于 `修复中`：

1. **建立基线**：R0；
2. **固定内核语义**：R1、R2、R4；
3. **修复执行与恢复边界**：R3、R5、R6；
4. **统一外围生命周期**：R7、R8、R9；
5. **补齐证据与持续门禁**：R10、R11。

R11 不需要等到最后才开始。每完成一个 P0 修复，都应同步增加对应故障注入用例；最后
再把这些用例统一接入门禁。

## 单项跟进模板

每个发现使用稳定 ID，例如 `R3-001`，并在对应工作流下记录：

```text
ID:
状态:
症状与复现:
违反的不变量:
根因与真正 Owner:
受影响入口:
统一修复:
兼容与迁移影响:
测试与故障注入:
文档影响:
完成证据:
```

单项只有同时满足以下条件才能标记为 `已验证`：

- 根因位于正确的所有权边界，没有通过 Host 或调用方旁路修复；
- 正常路径、失败路径、取消路径和恢复路径均有测试；
- 不引入新的隐式硬终止限制；
- 不绕过任何安全治理层；
- 中英文文档同步；
- 相关聚焦测试、`git diff --check` 和适用的仓库门禁通过。

## 维护规则

- 状态变更必须伴随代码、测试、运行记录或故障注入证据链接；
- 一次故障可能影响多个工作流，但只能指定一个根因 Owner；
- 如果修复需要改变 Protocol、持久格式或安全语义，先更新设计与兼容影响；
- 已验证项发生回归时重新打开原 ID，不创建隐藏重复项；
- Roadmap 描述目标，本文记录治理进度，Architecture 描述当前已交付事实，三者不得
  混用。
