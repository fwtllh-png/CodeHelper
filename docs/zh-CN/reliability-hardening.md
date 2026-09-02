# Runtime 可靠性契约

> 本文描述当前可靠性不变量、故障处理和验证入口。历史问题清单与分阶段修复记录不再
> 作为产品事实保留。

## 核心不变量

1. 生产 Turn 只通过 `Engine.Execute` 执行。
2. `TurnCoordinator` 是唯一生产 `Reducer.Apply` 调用方。
3. 外部 Effect 在执行前持久化 `EffectStarted`。
4. Result Command 在 Durable Accept 前可按相同 Identity 重交。
5. Terminal State、Measurement、Session Delta、Final Output、Receipt、Event、
   Operation Receipt 与 Outbox 原子提交。
6. Terminal Commit 成功前不修改 Engine Session State。
7. Optional Telemetry、Narrative 或 Host Projection 失败不能改写业务终态。
8. Approval、Input、Cancel、Lease 和 Recovery 都携带稳定 Identity 与 Fence。
9. Budget Exhaustion 保留可恢复状态，不伪装成未知内部错误。
10. Runtime 关闭后不接受新工作，并等待或明确中断已拥有的资源。

## 收敛与预算

Runtime 区分四种边界：

| 边界 | 来源 | 耗尽行为 |
| --- | --- | --- |
| 模型 Context | Active Route Catalog | Compaction，仍不可容纳则 `resource_exhausted` |
| Token/Cost | 用户或操作员配置；默认按模型/父预算派生 | 结构化阻塞并保留 Resume |
| Progress Lease | 显式 `max_steps`；结构化进展续期，`0` 表示未设置 | 收窄能力并进入一次受限 Finalization |
| Implement No-progress | 公开字段 `implement_no_progress_samples`；Plan 已有完成步骤且仍有 outstanding 时启用 | 读取新文件不再续期，默认 6 个无进展 Sample 后进入 Finish-only |
| No-progress | Kernel 的结构化进展状态 | 消耗 Progress Lease，不按累计步骤截断 |

单 Turn 默认 Token Ceiling 来自当前模型 Context Window。Child Tree 默认预算由父 Turn
预算和并发度派生；Child 单次 Reservation 只占用剩余额度。显式配置是 Operator
Ceiling，不能扩大 Provider 或父级容量。

Progress 包括有效 Mutation、Plan 推进、Verification、Completion，以及按 Turn Intent
定义的新路径或 Evidence。Plan 已有完成步骤且仍有 outstanding 工作时，新的
`file_read` / Evidence 不再续期，并改用 `implement_no_progress_samples`（默认 6）
进入 Finish-only；该阶段不允许 `git_status` / `git_diff` 或无 `start_line` 的
`file_read`。其余 No-progress 的收敛、Finish-only 与终止位置按显式 `max_steps`
的三分之一、三分之二和完整预算派生；`max_steps=0` 且
`implement_no_progress_samples=0` 时不启用 Sample 计数型 No-progress 上限。持续
有进展的工作不会仅因隐式固定计数被终止。

Web 的 Stop 操作使用标准 `user_interrupted` 原因，并把已产生 Workspace Mutation 的
Journal 置为 Suspended，供 Continue 恢复；Shutdown、替换或其他取消原因仍按各自终态
策略处理，不能把“暂停”降级为删除当前 Turn 产物的 Rollback。

## Provider Stream

Provider Adapter 负责单次 Transport，Engine 负责跨 Attempt 的语义恢复：

- 连接、TLS、响应头和流空闲使用不同 Deadline；
- 每个 Stream Event 续期 Idle Deadline；
- Meaningful Output 后的断流保留 Assembly 与 Usage；
- 不完整 Tool Fragment 不能作为可执行 Tool Call；
- Retry、Reset、Compaction、Resume 或链状态不确定时回退完整逻辑请求；
- Stream Checkpoint 随累计 Payload 增长自适应拉开，Terminal/错误强制刷新；
- Logical Request 与 Transport Attempt 分别计量，传输节省不伪装成 Token 节省。

Provider 输出不完整时没有独立的固定续写次数；是否继续由 Context、Token、Cost、
Progress 和 Provider Fault 共同决定。

## Tool 与副作用

Tool Call 在执行前绑定到已向模型展示的 Catalog Snapshot。执行统一经过：

```text
catalog binding
  -> argument/resource validation
  -> policy and permission
  -> approval when required
  -> journal preparation
  -> sandbox/process boundary
  -> outcome and evidence
```

`tool.ExecuteBatch` 负责有界并发、Serial Fence、Panic 隔离和全部 Goroutine Join。
可恢复失败必须证明 Call/Result Identity 完整、没有未知 Side Effect、且模型能够通过
新参数或稍后重试修复。未知副作用、Invariant、Security 和 Journal Failure 不能被包装
成普通 Tool Result 诱导模型重复执行。

## Durable Effect 与终态

Provider、Tool、Approval、Input、Verification、Narrative 和 Journal 工作都表示为
Kernel Effect。Coordinator 持久化状态转换和 Effect 生命周期；执行器只执行已请求的
外部工作。

Journal Commit、Suspend 和 Rollback 是幂等 Durable Effect。持久化失败时 Turn 保持
`committing` 或可恢复状态，Runtime 拒绝当前 Operation，但不会发布虚假的 Failed
Terminal。重启后以相同 Effect Identity 接续。

业务 Terminal Decision 在 Context 后处理前冻结。Post-turn Compaction、Session Delta
应用或 Outbox Projection 失败只能形成 Secondary Issue 或恢复工作，不能把已提交的
Completed 改成 Failed。

## Recovery

启动恢复顺序由 `runtime/app/wire` 管理：

1. 构造 Durable Repository 与 Runtime；
2. 恢复静态 Session/Thread/Artifact State；
3. 刷新 MCP；
4. 恢复 Terminal Outbox 与 Pending Turn；
5. 启动 MCP Prewarm。

只有同时存在 Accepted Start Operation 和非终态 Domain Fact 的 Turn 才自动恢复。
Running Effect 先 Requeue，再从 Durable Payload 接续。Approval/Input 在执行前恢复原
Request ID，Host 只看到同一个 Wait。

Checkpoint Restore/Fork 只恢复 Context State，并重新核对 Workspace Binding；它不重放
历史 Tool、不回滚文件，也不减少 Usage/Cost。

## 并发与背压

| 资源 | Owner/Fence |
| --- | --- |
| Thread Turn | `ActiveTurnRegistry` Lease Token |
| Shared Workspace Write | Workspace Turn Gate |
| Kernel Transition | Coordinator Revision |
| Approval/Input | Request Ledger |
| Event Sequence | `eventhub.Hub` |
| Web Connection | Capacity Slot + Context |
| Child Budget | Durable Budget Reservation |

队列、Mailbox、Subscriber 和 Connection 都有容量边界。容量耗尽必须返回可分类问题或
关闭慢消费者，不能静默丢失权威状态。

## Fault 契约

跨层错误使用 `protocol.Fault` 描述：

- Code；
- Origin；
- Disposition；
- Retryability；
- Side-effect State；
- Recovery Action。

未分类边界错误默认按 `unavailable/resume_turn` 处理。只有显式不变量故障才能使用
`internal/fail_turn`。调用方不能只依赖错误字符串决定 Retry 或 Rollback。

## 可观测性

Terminal Receipt、Trace 和 Event 共用同一份冻结 Measurement。Runtime Health 的活动
状态来自 Active Registry 与 Engine Recorder；终态事实来自 Terminal Envelope。

Trace、Usage 和 Receipt 只投影已有执行事实，不获得执行权威。判断泄漏时必须同时检查
Active Turn、Provider/Tool Recorder、Lease 和 Pending Interaction，不能只检查
`spans` 表。

## 验证矩阵

| 风险 | 主要门禁 |
| --- | --- |
| Kernel 转换 | `go test ./internal/runtime/agent/turnkernel` |
| Turn/Context/Tool | `go test ./internal/runtime/agent/engine` |
| Terminal/Recovery | `go test ./internal/runtime/app` |
| Provider Stream | `go test ./internal/adapter/provider/assembly ./internal/adapter/provider/httpclient` |
| Guard/Sandbox | `make security-test sandbox-attack-test` |
| Subagent/Admission | `go test ./internal/orchestration/...` |
| Persistence | `go test ./internal/persist/...` |
| 并发 | `go test -race -p 1 ./...` |
| 故障矩阵 | `make reliability-gate` |
| 全仓 Hermetic | `make test-hermetic` |

发布前还需执行 `make verify`、平台能力测试、Web E2E/Soak 和 Release Gate。环境能力
不可用时必须报告 unavailable，不能把未执行写成通过。

## 变更检查表

- 新状态是否只有一个 Owner 和一个持久化入口？
- Retry 是否同时考虑幂等性、Side Effect、进展和总预算？
- Deadline 是否区分连接、空闲、Lease 与清理？
- 恢复是否复用原 Identity，而不是创建替代工作？
- 终态失败是否可能留下未发布 Outbox、Lease 或 Journal？
- Child 是否计入 Agent Tree 的预算账本？
- 新 Trace/Metric 是否有界、低基数且不影响业务结果？
- 是否覆盖 Crash Point、Duplicate、Late Result、Cancel 和 Race？
