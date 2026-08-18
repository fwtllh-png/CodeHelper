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
| R0 | 失败基线与全仓限制清点 | P0 | 待评估 | Runtime / Engineering |
| R1 | Turn 状态机与终态收敛 | P0 | 待评估 | `internal/runtime/agent` |
| R2 | 动态预算、进展检测与 Context | P0 | 待评估 | Agent / Context / Config |
| R3 | Provider 流式输出与残缺调用恢复 | P0 | 待评估 | Provider / Agent |
| R4 | 类型化错误、Retry 与 Deadline | P0 | 待评估 | Protocol / Runtime / Adapters |
| R5 | 持久化、Journal、幂等与崩溃恢复 | P0 | 待评估 | Persist / Runtime |
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

## R1：Turn 状态机与终态收敛

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
