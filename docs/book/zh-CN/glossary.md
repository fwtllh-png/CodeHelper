# Agent 工程术语表

本术语表定义知识书籍的推荐表达。代码标识、协议名称和启动参数保持源码形式。

| 术语 | 推荐解释 | 使用约束 |
| --- | --- | --- |
| Agent | 使用模型、上下文、工具和状态，经过一个或多个步骤追求目标的系统。 | 不能只用它指代模型。 |
| Agent loop | 模型推理、Tool Request、Tool Result 和继续执行组成的受控循环。 | 在 CodeHelper 中属于 `internal/runtime/agent`。 |
| Approval（审批） | 人类对有后果操作作出的授权或拒绝决定。 | Approval 不能替代安全硬约束。 |
| Automation（自动化） | Trigger 或 Schedule 匹配时创建持久工作的规则。 | 与交互式 Agent Turn 区分。 |
| Capability（能力） | Model、Provider、Runtime 或 Host 声明支持的功能。 | 例如 Streaming 与 Tool Call。 |
| Catalog（目录） | 用于发现 Model、Tool 或书籍章节的结构化注册表。 | 必须说明具体是哪一种 Catalog。 |
| Checkpoint（检查点） | 用于 State-only Restore/Fork 的不可变 Session History/Profile Artifact。 | Workflow Execution 使用 WorkGraph Fact，不维护独立 Checkpoint State Machine。 |
| Constitution（宪法约束） | 不可绕过的 Runtime Tool 执行规则。 | 保留 CodeHelper 类型的英文名称。 |
| Context（上下文） | 当前推理时提供给模型的信息。 | 与持久状态、模型训练数据区分。 |
| Context compaction（上下文压缩） | 在保留后续工作所需信息的同时缩小 Context。 | 必须说明信息损失取舍。 |
| Credential reference（凭证引用） | 指向环境变量、受保护文件或 OS Keyring 的非 Secret 引用。 | TOML 保存引用，不保存 Secret。 |
| Event（事件） | Operation 推进时产生的不可变 Runtime 事实。 | 与当前 Projection State 区分。 |
| Fail closed（失败关闭） | 无法确认安全决策或能力时拒绝执行。 | 静默降级不能称为 Fail-closed。 |
| Fixture（夹具） | 在没有真实依赖时驱动实际 Runtime 的确定性记录输入输出。 | 无网络时优先写作 Hermetic Fixture。 |
| Effect Owner | 附着于 Process、Connection、Registration、Subscription、Lease 或 Timer 的 Extension/Source/Plan/Generation Identity。 | Disable/Revoke 用它 Drain/Fence Effect。 |
| Fleet（执行集群） | WorkGraph State 与 Ordered Fact 的 Read/Audit Projection。 | Fleet 不 Enqueue、Claim、Settle 或 Resume Work。 |
| Guard（执行守卫） | 按 Policy、Permission、Approval、Constitution、Journal 和 Sandbox 要求检查 Tool 的边界。 | Host 不能绕过。 |
| Host（交互宿主） | 提交 Operation 并投影 Event 的用户或 Client Adapter。 | Web 是当前唯一产品 Host。 |
| Idempotency（幂等性） | 重试操作不会产生非预期重复副作用的性质。 | 必须说明幂等 Key 或边界。 |
| Journal（工作区日志） | 记录预期和已完成 Workspace 副作用，用于证据与恢复的持久数据。 | 不是普通应用日志。 |
| Lane（执行通道） | Durable Placement 与显式 Inline/tmux Process Adapter。 | Placement 不是 Lifecycle/Scheduling Authority。 |
| Lease（租约） | Worker 对持久工作的限时所有权。 | 通常与 Heartbeat、接管规则一起说明。 |
| MCP | Model Context Protocol，通过外部 Server 暴露 Tool 和 Resource。 | 每章首次出现时写出全称。 |
| Model（模型） | 由 Model ID 或 Wire ID 寻址的推理系统。 | 不与 Provider 混用。 |
| Operation（操作） | 提交给 Runtime、包含身份、输入、Mode 和执行选项的请求。 | Event 与 Receipt 归属于 Operation。 |
| Observation Envelope | 带版本、通过 Privacy Admission 的因果证据记录，包含稳定 Identity、Correlation、有界 Summary 与可选 CAS Payload Reference。 | 不是 Runtime Event 或执行权威。 |
| Permission posture（权限姿态） | 处理 Tool 风险的策略，例如 `never`、`suggest`、`auto`、`bypass`。 | `bypass` 下仍应用硬约束。 |
| Plugin（插件） | 通过受治理扩展边界加载的打包扩展。 | 必须解释 Trust 与 Lifecycle。 |
| Policy（策略） | 对动作进行 Allow、Deny 或 Require Approval 的可配置规则。 | 与 Constitution 区分。 |
| Projection（投影） | 应用有序 Event 后推导出的当前状态。 | 可以从持久事实重建。 |
| Provider（提供方） | 传输模型请求和响应的服务或 Adapter。 | 一个 Provider 可以暴露多个 Model。 |
| Receipt（执行回执） | 描述执行结果和副作用的结构化证据。 | 不能作为 Event 的同义词。 |
| ReAct | 交替进行 Reasoning 和 Action/Tool Use 的模式。 | 它不是完整 Runtime 架构。 |
| Recovery（恢复） | 中断或失败后恢复有效执行状态。 | 必须说明使用哪些持久事实。 |
| Runtime（运行时） | 统一管理 Operation、Agent Loop、Tool、State、Governance 和 Event 的系统。 | Host 是 Runtime 的 Client。 |
| Sandbox（沙箱） | 约束进程、文件和其他资源的 OS 级隔离。 | 不可用时不能宣称 Strong Sandbox。 |
| Session（会话） | 跨 Operation 保持对话或执行连续性的持久对象。 | 与单个 Process Lifecycle 区分。 |
| Skill（技能） | 通过扩展系统加载的指令或过程能力。 | Skill 不能绕过 Runtime 治理。 |
| Snapshot（快照） | 用于加速恢复或检查的物化状态镜像。 | 必须解释与 Event Source 的一致性。 |
| Span（跨度） | Trace 内部有时间范围的嵌套工作单元。 | 用于可观测性，不是产品状态。 |
| Task（任务） | 具有 Lifecycle、Attempt 和 Ownership 的持久可执行工作单元。 | 没有 Executor 的 Task Record 不可执行。 |
| Terminal Measurement Snapshot | Terminal Convergence 时只冻结一次的 Digested Usage/Latency Fact。 | Receipt、Trace 与 Terminal Envelope 共享它。 |
| Tool（工具） | 模型可以请求、用于检查或影响环境的类型化能力。 | 有后果的 Tool 必须经过 Guard。 |
| Trace（追踪） | 关联 Span 与 Runtime Identity 的端到端可观测记录。 | 不嵌入 Secret 或原始敏感数据。 |
| Turn（轮次） | 一次 User 到 Agent 的交互，可以包含多个模型和 Tool Step。 | 与单次 Inference Request 区分。 |
| Verification（验证） | 检查工作是否满足验收条件并产生证据的过程。 | Tool Call 成功本身不等于验证。 |
| Wire ID | 通过传输协议发送给 Provider 的 Model Identifier。 | 可能与 Catalog Model ID 不同。 |
| Worker（工作进程） | Claim 并执行 Durable Task 的 Process 或 Loop。 | Ownership 由 Lease 和 Heartbeat 管理。 |
| WorkGraph | 以 Snapshot、Ordered Fact、Receipt 与 Outbox Row 原子提交的权威 Run/Node/Attempt/Lease/Effect State Machine。 | Worker 是 Claim Authority；Projection 只读。 |
| Workflow（工作流） | 编译为统一 Durable WorkGraph 的 Validated Multi-step Graph。 | 与单个 Agent Loop、Session Checkpoint 区分。 |
| Workspace（工作区） | CodeHelper 操作的 Repository 或 Directory 边界。 | Identity 与 Trust 都按它划分。 |

## 翻译与风格

- Public Type、Package Path、Protocol Field、Command 和 Flag 保持原文。
- Acronym 每章首次出现时写出全称。
- 一个概念优先使用一个术语；新增同义表达前先更新术语表。
- 外部规范拥有定义权时，遵循该规范并在对应章节链接一手资料。
- 本文件是推荐中文译法的唯一事实来源。
