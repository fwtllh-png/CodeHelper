# RFC-007：ExecutionService

> 状态：Implemented。T1 已落地（schema v6、`Claim` / `Heartbeat` / `Settle` / `Requeue` / `Reclaim` / `Attempts`、恢复分流）；T2 已落地（常驻 scheduler 的 claim / reclaim / automation 三循环、drain、`codehelper worker run|enqueue|list`）；T3 已落地（`agent_turn` executor 复用 RFC-006 child runtime，后台 turn 有独立预算，隔离写结果在 task completed 前合回宿主工作区）；T4 已落地（`Needs` 拓扑执行、并发波次与 parallel join、`When` 条件、节点 retry/timeout、`workflow_runs` / `workflow_nodes` 落表、spec 指纹 fail-closed resume、`codehelper workflow run --id` / `workflow status`）。节点 timeout 的 attempt context 会传入 production turn，取消收敛后才允许 retry。节点输出持久化已落地（`workflow_nodes.output_handle` 走 `contentstore.Durable`，resume 后能读回崩溃前的节点产出）。T5 已落地：fleet 调度代码删除、JSONL 降级为审计、CLI 与 TUI 面板转只读；`workflow_run` 与 `shell_command` 两个 executor 接入 production scheduler，`background_executor` 不再是不完整 maturity。T5 补强已关闭 Workflow `profile` / `response_schema` 静默失效：profile 无定义时 fail-closed，response schema 在 production driver 校验完整 JSON 输出。
> 关联：[ROADMAP §7.2](../ROADMAP.zh-CN.md)、[ROADMAP §7.3](../ROADMAP.zh-CN.md)、[ROADMAP §5.4](../ROADMAP.zh-CN.md)、[RFC-006 ChildRuntime](./RFC-006-child-runtime.zh-CN.md)、[RFC-005 EditTransaction](./RFC-005-edit-transaction.zh-CN.md)、[RFC-002 Verify Gate](./RFC-002-verify-gate.zh-CN.md)
> 影响面：`internal/orchestration/{task,automation,fleet,workflow}`、`internal/persist/state/sqlite`、`internal/runtime/app/wire`、`internal/adapter/tool/{task,automation}`、`internal/host/cli`、`internal/config`

---

## 1. 问题

后台执行这条线上的每个零件都存在，但没有一个零件接到下一个。

1. **`tasks` 表有完整生命周期，没有执行者。** `task.Repository` 的包注释自己写着"It does not execute tasks"（`task/repository.go:1`）。production 里唯一把 `queued` 推到 `running` 的是 `task_gate_run`；`StateCompleted` 只有 `repository_test.go` 在用。也就是说今天创建一个后台任务，得到的是一行状态永远停在 `queued` 的记录。
2. **lease 两列从 schema v1 就在，没有任何 Go 代码写它们。** `Update` 的 SQL 会写 `lease_owner` / `lease_expires_at`（`task/repository.go:245-252`），但全仓没有调用方给 `Transition.LeaseOwner` 赋值。
3. **automation 只在会话启动时 tick 一次**（`wire/runtime.go:376`、`699`），此后没有任何周期触发；`automation_runs.status` 插入后再无 `UPDATE`，永远是 `queued`。用户视角的"我排了一个每小时任务"，实际等于"我往表里写了一行 RRULE"。
4. **fleet 是第二套执行面，且是空转的。** `fleet.Ledger` 有自己的 `Claim` / `Heartbeat` / `Reconcile`（`fleet/ledger.go:387`、`421`、`434`），`Worker.RunTask` 会起一个 `codehelper exec` 子进程并心跳（`fleet/ledger.go:481-546`）——但只有测试调用它，`Profile.MaxWorkers` 没有任何执行点，`fleet.Task` 与 `tasks` 表是两个类型、两份真相，wire 层根本不持有 fleet。
5. **workflow 是线性执行器，不是 DAG。** `Runtime.Run` 按 `Spec.Nodes` 数组顺序遍历（`workflow/runtime.go:63-137`）；`NodeParallel` 是顺序 `SpawnTask` 且忽略子结果的成功与失败；没有依赖边、没有 condition、没有节点级 retry/timeout、没有节点状态表，所以崩溃后无从续跑。
6. **唯一真正会执行 agent 工作的东西是 RFC-006 落下的 `childRuntime`**：`Runtime.Submit(StartTurn)` + 事件 pump + settle，带隔离根、预算与 fail-closed 审批。

所以问题的形状不是"缺一个 scheduler"，而是**四套半执行面各缺一半**。本 RFC 只做一件事：让排队的工作真的会被执行，并且**全仓只剩一条 claim 路径**。

---

## 2. 六条硬约束

这些不是设计偏好，是当前代码的既成事实，任何设计都得先绕过它们。

### C1：一个 thread 一次一个 turn，journal 一次一个活跃 turn

`Runtime` 拒绝在已有活跃 turn 的 thread 上再开 turn（`app/runtime.go:661-667`），而 workspace journal 一次只允许一个活跃 turn（RFC-006 C1）。推论：后台任务不能"顺便在当前 thread 上跑"，必须有自己的 thread；写类后台任务必须有自己的隔离根与自己的工具面。RFC-006 已经把这套机制做完了，**后台执行复用它，不新造第二套写隔离**。

### C2：`Update` 表达不了 heartbeat

两条：同态更新直接返回当前值（`task/repository.go:219-222`），所以"续租但不改状态"是 no-op；而每次真实 transition 都会用 `Transition` 里的 lease 字段覆盖两列，调用方忘带就静默清空租约。心跳与续租因此**必须是新方法**，不能挤进 `Update`。

### C3：状态机没有回队的边

`CanTransition` 允许 `queued → running|canceled`、`running → waiting|failed|canceled|completed`、`waiting → running|failed|canceled`（`task/repository.go:442-454`）。没有 `running → queued`，所以租约超时回收、优雅停机重排队、幂等 retry 三件事全都无路可走。而 `waiting` 已经被 `task_gate_run` 用作"门禁失败，要人看"，把 backoff 也塞进 `waiting` 会让操作者分不清"在等人"和"在等重试"。

### C4：没有 attempt、没有 next_attempt_at、没有 attempt 审计

`tasks` 没有尝试次数列，`task_lifecycle` 只记状态序列（`task_id, sequence`），没有"第几次尝试跑在哪个 thread/turn 上"。M2 的退出指标里有一条"后台任务无审计执行次数必须为 0"，当前的表结构无法支撑这个指标的度量。

### C5：automation tick 的去重不依赖选主

`enqueue` 靠 `tasks.id` 的确定性构造与 `automation_runs` 的 `UNIQUE (automation_id, scheduled_for)` 去重，撞上就返回 `errDuplicateSlot` 并推进 `next_run_at`（`automation/repository.go:318-341`、`458-470`）。这意味着**多个进程同时 tick 同一个库是安全的**，重复 slot 会被数据库挡掉。这条约束直接决定了 scheduler 的形状：不需要 leader election，不需要分布式锁。

### C6：payload 无 schema、kind 是自由字符串，而默认 kind 就叫 `agent`

`task_create` 的默认 kind 是 `"agent"`（`adapter/tool/task/task.go:20`），payload 是任意对象，模型今天把它当**工作板与待办清单**在用（`payload.title`、`work_board` kind）。任何"执行所有 queued 任务"的实现都会把模型记的待办变成真实 turn，一次会话能烧掉整个预算。可执行性必须是显式的、可迁移的、对旧数据 fail-closed 的。

---

## 3. 决策

### D1：可执行靠一列，不靠猜 payload

schema v6 给 `tasks` 加 `executor TEXT`。claim 查询只看 `executor IS NOT NULL`，因此：

- 所有既有行（包括 kind = `agent` / `work_board` 的待办）`executor` 为 NULL，**永远不会被执行**，这是 C6 要的 fail-closed；
- 可执行任务由新入口显式创建（`task_create` 增加 `executor` 参数、automation 的 `task_kind` 校验为已注册 executor），拿不到 executor 的 automation 在创建时就报错，而不是排一个永不执行的 slot；
- 三个 executor 名：`agent_turn`、`workflow_run`、`shell_command`。名字是列的值而不是 kind 的值，`kind` 保持它今天的语义（人读的分类）。
- automation 也要有这一列（`automations.task_executor`）：automation 是任务模板，模板不说明产出物可不可执行，排程就只能继续产出永不执行的行——那正是 RFC 之前的状态。空 executor 仍然允许（那是"给人看的提醒行"，也是既有行为），但拼错的 executor 在创建期就报错，否则它会静默变成一条提醒行。

payload 顶层带版本：`{"version": 1, ...}`。版本不认识或出现未知字段就 fail-closed 到 `failed`——**不猜、不按默认值跑**。理由：后台任务没有人在旁边看，猜错的代价是无人监督的写操作。三类 executor 各自维护 v1 payload；`workflow_run` 内嵌不可变的 `Spec`，`shell_command` 不开放 env、sandbox 与宿主进程参数。

### D2：claim 复用现有乐观并发，写操作全部 fenced

不需要新的锁机制：`Update` 的 `WHERE id = ? AND state = ? AND lifecycle_sequence = ?` 已经是 CAS。新增三个方法，语义收窄到只能做一件事：

| 方法 | SQL 谓词 | 语义 |
| --- | --- | --- |
| `Claim(ctx, owner, workspaceRoot, lease, now, limit)` | `state='queued' AND executor IS NOT NULL AND (next_attempt_at IS NULL OR next_attempt_at <= now)`，并通过 `sessions → workspaces` 要求规范化 `root_path = workspaceRoot`；再对选中行做原子 UPDATE | 抢到则 `queued → running`，写 `lease_owner` / `lease_expires_at` / `heartbeat_at`，`attempt += 1`，并插入 `task_attempts` 行；可跨 session 接管但不可跨 workspace |
| `Heartbeat(ctx, id, owner, until)` | `WHERE id=? AND state='running' AND lease_owner=?` | 只动 `lease_expires_at` / `heartbeat_at`，不动 `lifecycle_sequence`（C2） |
| `Settle(ctx, id, owner, transition)` | 同上加 owner 谓词 | 终态写入，清空租约，收尾 `task_attempts` |

**所有写操作带 `lease_owner = ?` 谓词**（fencing）。理由是回收的必然后果：一个卡住的 worker 的租约被回收、任务被别人重跑之后，原 worker 可能还活着并试图 settle。没有 owner 谓词，它会覆盖新 owner 的结果。CAS 失败返回类型化的 `ErrClaimLost`，让 worker 区分"我输了这一行，跳过"和"数据库坏了，要报"。

`Update` 本身不改签名，`task_gate_run` 与 HTTP cancel 的行为一字不变。

### D3：状态机只加一条边，backoff 靠时间列不靠状态

新增 `running → queued`，且只允许三种理由：`lease_expired`（回收）、`draining`（优雅停机）、`retry`（可重试失败）。回队时写 `next_attempt_at = now + backoff(attempt)`，claim 查询天然跳过还没到时间的行。

`waiting` 的语义保持不变：**要人**。这是操作者唯一需要区分的两件事——"它在等我"和"它在等自己"，用两个不同的机制表达，而不是同一个状态加不同 reason。

backoff 是确定性的指数退避（`base * 2^(attempt-1)`，封顶），不加抖动：单进程 claim 没有惊群，而确定性让测试能断言时间点。

`attempt >= max_attempts` 时不再回队，直接 `failed`，`failure_reason` 保留最后一次的错误，历史留在 `task_attempts`。

**drain 把这次尝试还回去，租约超时不还。** 这条是实现时才浮出来的：`attempt` 在 claim 那一刻就 +1，所以 `max_attempts = 1` 的任务一旦被一次例行重启打断就再也跑不了。区别在于我们知不知道发生了什么——drain 是我们自己决定停的，这次尝试根本没轮到它，所以 `attempt` 回退并删掉那条 `task_attempts` 行（一条 attempt 行就代表一次算数的尝试）；租约超时与崩溃恢复则照常计数，因为无法判断"是不是这个任务把 worker 弄死的"，而一个毒药任务无限重试比它失败在那里等人更糟。

### D4：agent 任务就是 RFC-006 的机制，不起子进程

`agent_turn` executor 做的事与 `childRuntime` 完全同构：注册一个 thread、拿隔离根、`Runtime.Submit(StartTurnPayload)`、pump 事件、settle 结果。差别只有队列在哪：child 的队列是父 turn 里的一次工具调用，task 的队列是 SQLite 一行。因此实现方式是**把 `childRuntime` 的 submit/pump/settle 抽成可复用的 turn driver**，两个调用方各自提供"从哪来"和"settle 到哪去"。

`fleet.Worker.RunTask` 的子进程模型（`codehelper exec`）就此删除。理由不是"不好看"：子进程绕过本进程的 Guard、审批、预算账本与事件流，而 M2 的退出条件是"所有执行经过 Guard"。同一个仓库里留两套执行语义，等于留两套安全边界。

写类后台任务与写类子 Agent 走同一条路：git worktree 隔离 + 自己的工具面（RFC-006 D3）。非 git 工作区里，写类后台任务在 claim 时就 fail-closed，理由与子 Agent 一致。

后台 `agent_turn` 的终态还包括 apply：隔离 child 成功且 receipt 有写路径时，executor 在关闭 worktree 前调用现有 `agent_merge`，经过 spawn-time `BaseRev` 指纹检查、ToolGuard resource claim、共享 workspace whole-turn gate 与 parent journal。只有 apply 成功才写 task `completed` / `merged=true`；冲突、policy 拒绝或 journal/apply 失败均写 task `failed` / `merged=false`，并保留 child 的 thread/turn/result 供审计。`same_workspace_serialized` 已在宿主根完成写入，不重复 merge。

### D5：启动恢复不抢活 lease，崩溃接管只走 expiry fencing

任意 `exec` / `serve` / ACP 进程都可能打开同一个 state DB，所以“进程启动”等价于“旧 worker 已死”是错误假设。`RecoverInterrupted` 只处理旧版本或非 executor 路径留下的、`lease_owner` 与 `lease_expires_at` 均为空的 `running` 行：

| 行的形状 | 恢复动作 |
| --- | --- |
| 无 lease，`executor IS NULL` | `failed` + `interrupted`。没有执行者的任务无法重跑 |
| 无 lease，`executor` 非空且 `attempt < max_attempts` | `running → queued`，`next_attempt_at = now` |
| 无 lease，`executor` 非空且尝试用尽 | `failed` + `interrupted` |
| 存在 lease（无论当前是否过期） | 启动恢复不修改；由 scheduler 的 `Reclaim(at)` 仅在 `lease_expires_at <= at` 时处理 |

这样新 Host 不会把健康 worker 的 task 重排队；worker 崩溃后，不论是否有新进程启动，都由同一条 lease expiry + owner fencing 路径接管。

### D6：后台任务的 retry 不等 journal 落盘

§5.4 的 journal 落盘是 RFC-005 的残余风险（进程被杀留下半应用状态），它对**宿主工作区**成立。但后台写任务跑在一次性 worktree 里：崩溃后正确的恢复动作是**丢掉那棵 worktree 重跑**，而不是把半应用状态回滚成一致。所以 D5 的重排队不依赖 journal 持久化，M2 里这两条可以并行推进，不构成串行依赖。

代价要说清楚：重跑意味着"至少一次"语义。凡是有外部副作用的 executor（`shell_command` 尤其）必须自己声明幂等性，声明不了的默认 `max_attempts = 1`，宁可让人来看，不要自动重放一次可能已经生效的外部动作。

### D7：scheduler 常驻但不选主，host 决定是否运行

由 C5，多进程 tick 安全，所以 scheduler 是一个普通常驻组件：一个 ticker 调 `automation.Tick`，一个 claim 循环，一个租约回收循环。三者共用一个 `context`，`Close` 时统一停。

启停策略按 host 分，默认值 fail-safe 而不是 fail-fast：

| Host | 默认 | 理由 |
| --- | --- | --- |
| TUI / `serve` | 开 | 长驻进程，有观察面 |
| `codehelper exec`（一次性） | 关 | 一次性进程不该悄悄接下后台工作，然后为了跑完它而延迟退出 |
| `codehelper worker`（新增，前台守护） | 开，且只做这个 | 真正的部署形态：没有交互 host 时也要有人干活 |

会话启动时的单次 tick 保留为兼容路径（ROADMAP §7.2 已这么写），但它变成常驻循环的第一次迭代，而不是一条独立代码路径。

**shutdown drain**：停止 claim → 取消在飞 turn（`CancelReasonHostInterrupted`）→ 把这些任务 `running → queued`（reason `draining`，`next_attempt_at = now`）→ 等 pump 收敛。drain 不是失败：一次干净的停机之后，任务应该在下一个进程里继续，而不是留一串 `failed` 让人手工重排。

### D8：workflow 节点表 + spec 指纹，resume 要么正确要么拒绝

schema v6 加两张表（见 §4）。执行器改动：

| 能力 | 设计 | 为什么不是别的 |
| --- | --- | --- |
| DAG 依赖 | `Node.Needs []string`，拓扑排序后按层执行，环在校验期报错 | 用显式依赖边而不是数组顺序，才能表达"两个节点可并行" |
| parallel join | 一层内的节点并发执行，全部终态后汇聚；任一失败按该节点的 retry 策略处理，仍失败则整层失败 | 今天的 `NodeParallel` 忽略子结果，等于并行地不知道结果 |
| （实施说明）`NodeParallel` 的归宿 | 不删，改语义：它的 `children` 变成它自己的依赖边，它本身只做 join——全部子节点 `completed` 才 `completed`，否则 `failed`。并发由"同一波次"提供，不再由这个节点提供 | 删掉它会让既有 spec 直接失效；保留成 join 之后，"并行地不知道结果"这个真问题被修掉，且旧 spec 的语义只增强不改变 |
| condition | 结构化谓词 `Node.When = {node, status}`，不引入表达式语言 | 表达式语言要沙箱、要确定性、要版本兼容；结构化谓词的能力刚好够"上游失败时跑补偿节点" |
| retry / timeout | `Node.Retry = {max_attempts, backoff_ms}`、`Node.TimeoutMS` | 与 D3 的任务级 retry 同形，节点级别是 worker 内的循环 |
| checkpoint / resume | 每个节点终态落 `workflow_nodes`；resume 时跳过已成功节点 | 崩溃后从头重跑是把已完成的写操作再做一遍 |

resume 的 fail-closed 点：`workflow_runs.spec_hash` 与当前 spec 的指纹不一致时**拒绝 resume**，报"spec 已变更，请重新起一次 run"。不一致还续跑意味着按新 DAG 跳过旧节点，这是最难查的一类错误。

**实施约束：** DAG 仍按波次并发提交，但 production driver 的普通 thread 共用一个可写 workspace 与单 active-turn journal。为保持 turn 级 commit/rollback 原子性，这些 Engine 统一经过共享 whole-turn gate；同一 workspace 上的节点实际串行。真正并行写执行要求每节点隔离根及明确 merge 语义，当前未实现，不能用“支持波次并发”掩盖。

**timeout 约束：** `Driver.SpawnTask(ctx, request)` 必须让 attempt context 控制实际工作。runtime 不再用 goroutine 包住无 context driver 后只停止等待；production driver 在 deadline/cancel 时向该 attempt 的 thread/turn 提交 `CancelTurnPayload`，`SpawnTask` 返回后 retry 才能开始。忽略 context 的自定义 driver 会阻塞 run，而不会通过遗弃旧 goroutine制造重叠副作用。

`jsvm` 脚本层不做 checkpoint。理由是解释器状态无法序列化，硬要做只能记录"跑到第几个 spawn"，而脚本可能有本地变量依赖前面的结果——那是假恢复。脚本层的定位因此收窄：**编排实验与一次性任务**，需要跨重启的编排走 IR。这一条要写进文档，否则用户会以为两条路等价。

### D9：预算与审批：后台自己的账本，审批 fail-closed 且留痕

- 后台执行有自己的 `rlm.Governor`（`execution.worker.max_tokens` / `max_cost_usd` / `max_parallel`），与子 Agent 的账本、宿主的账本三者分开。理由与 RFC-006 D7 一样：一个池子被烧穿时，操作者要知道是谁烧的。后台 turn 内部再 spawn 子 Agent，子 Agent 仍记在子账本上。
- 后台 turn 没有人可问，审批沿用 RFC-006 D4 的自动拒绝 + 记录到结果 `unresolved`。任务因此可能以"完成但有未解决审批"结束，这比挂到租约超时好。
- 「后台任务无审计执行次数必须为 0」的实现是 `task_attempts`：每次 claim 一行，记 `thread_id` / `turn_id` / 起止时间 / 终态。turn 本身的事件已经在 eventlog 里，两者用 `turn_id` 对得上。不新增协议事件面（见 §8）。

### D10：收敛的边界：谁是真相，谁降级，谁保留

| 组件 | 处置 |
| --- | --- |
| `tasks` + `task_attempts`（SQLite） | **唯一状态真相**。所有 executor 的排队与结算都在这里 |
| `fleet` 的调度代码（`Claim` / `Reconcile` / `Worker.RunTask` / `Profile.MaxWorkers`） | **删除**。留下第二条 claim 路径就是留下第二份真相 |
| `fleet` 的 JSONL 账本 | 降级为 workflow run 的审计流，仅供现有 CLI / TUI 面板读；面板改读 `tasks` 之后（tui-panels 线程）整包退役 |
| `lane` | **保留**，它是另一种原语：跑用户可见的分离终端进程（inline / tmux），不是 agent turn。它进 jobs 面板，不进 executor 注册表 |
| `shell_command` executor | 走既有 `process` 沙箱层（Guard + policy），不走 lane。lane 的 tmux 会话逃出本进程的策略边界 |

`workflow_run` 的 run ID 默认由 task ID 稳定派生，task retry / worker 重启仍命中同一组 checkpoint；spec 指纹变化由 `checkpoint.Repository.Ensure` fail-closed。节点 turn 复用当前 Session Runtime，不递归创建 Session 或 scheduler。`shell_command` 通过 ToolGuard 调用已注册的 `shell_run`，因此 repository rule、hook、policy 与强沙箱仍是同一条生产边界；后台没有 Approval Host，Ask 立即以 `approval_host_unavailable` 失败。两类 payload 只有显式 `idempotent=true` 才允许 `max_attempts > 1`。

### D11：Workflow profile/schema 必须有可证明语义

`TaskRequest.Profile` 没有 profile registry、配置格式或 route 映射，production driver
不得把它静默丢弃，也不得仅拼进 prompt 冒充 profile。非空 profile 在创建 turn
前以 `workflow task profile is unsupported` 拒绝。

`response_schema` 进入 `Node`、JS `task()` 与两个 production driver。Spec/Driver
在 turn 前编译 schema（最大 64 KiB、禁止外部 `$ref`），turn 完成后要求输出是
唯一完整 JSON value 并执行 JSON Schema 校验；通过时写入 `TaskResult.Data`，
失败时节点失败。它是 workflow 输出后置条件，不是 provider constrained decoding，
因此不改变 RFC-010 对通用 structured output 的后置决定。

---

## 4. schema v6

```sql
ALTER TABLE tasks ADD COLUMN executor TEXT;                     -- NULL = 不可执行（既有行）
ALTER TABLE tasks ADD COLUMN attempt INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tasks ADD COLUMN max_attempts INTEGER NOT NULL DEFAULT 1;
ALTER TABLE tasks ADD COLUMN next_attempt_at TEXT;
ALTER TABLE tasks ADD COLUMN heartbeat_at TEXT;

CREATE INDEX tasks_claimable ON tasks(state, executor, next_attempt_at);
CREATE INDEX tasks_lease ON tasks(state, lease_expires_at);

ALTER TABLE automations ADD COLUMN task_executor TEXT;
ALTER TABLE automations ADD COLUMN task_max_attempts INTEGER NOT NULL DEFAULT 1;

CREATE TABLE task_attempts (
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    attempt INTEGER NOT NULL CHECK (attempt > 0),
    owner TEXT NOT NULL,
    thread_id TEXT,
    turn_id TEXT,
    status TEXT NOT NULL,          -- running / completed / failed / canceled / interrupted
    reason TEXT,
    started_at TEXT NOT NULL,
    ended_at TEXT,
    PRIMARY KEY (task_id, attempt)
);

CREATE TABLE workflow_runs (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    task_id TEXT REFERENCES tasks(id) ON DELETE SET NULL,
    spec_hash TEXT NOT NULL,
    spec_json TEXT NOT NULL,
    status TEXT NOT NULL,
    goal TEXT NOT NULL DEFAULT '',
    error TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK (json_valid(spec_json))
);

CREATE TABLE workflow_nodes (
    run_id TEXT NOT NULL REFERENCES workflow_runs(id) ON DELETE CASCADE,
    node_id TEXT NOT NULL,
    status TEXT NOT NULL,          -- pending / running / completed / failed / skipped
    attempt INTEGER NOT NULL DEFAULT 0,
    output_handle TEXT,            -- contentstore handle，大输出不进这一列
    reason TEXT,
    started_at TEXT,
    ended_at TEXT,
    PRIMARY KEY (run_id, node_id)
);
CREATE INDEX workflow_nodes_run_status ON workflow_nodes(run_id, status);
```

`spec_json` 整份存下来是刻意的：resume 时要对指纹，也要能在 spec 文件已经被改掉之后解释"当时跑的是什么"。大节点输出走 handle，与 RFC-006 D5 的子结果持久化同一机制。

---

## 5. 要改的既有断言

| 位置 | 现在断言什么 | 改成什么 |
| --- | --- | --- |
| `task/repository_test.go` | `RecoverInterrupted` 把 running 一律标 failed | 按 executor 分流（D5），旧行仍 failed |
| `wire/persistent_test.go:279` | 中断的 running 任务变 failed | 可执行任务重排队，不可执行任务 failed |
| `fleet/ledger_test.go` | `Claim` / `Reconcile` / `Worker.RunTask` 的行为 | 随调度代码一起删；保留账本追加与 replay 的测试 |
| `workflow/runtime_test.go` | 按数组顺序执行、`NodeParallel` 顺序 spawn | 拓扑顺序、并发 join、节点状态落表 |
| `wire/runtime.go:376`、`699` | 启动单次 tick | 常驻 scheduler 的第一次迭代（实施偏离：启动 tick 保留。`execution.worker.enabled = false` 时它是唯一的补账入口，开了 worker 之后它只是常驻循环第一次迭代之前的一次重复，去重靠 C5 的 slot 唯一约束） |
| `internal/host/cli/diagnostics_maturity_test.go` | 未开齐时 `background_executor=partial` | 三类 executor 开齐后移除该不完整 maturity 键 |

---

## 6. 分片顺序

| 分片 | 内容 | 完成的判据 |
| --- | --- | --- |
| T1 | schema v6 + `Claim` / `Heartbeat` / `Settle` / `ErrClaimLost` + D5 恢复分流 | 表与仓储层有测试，无执行者时行为不变 |
| T2 | 常驻 scheduler / claim 循环 / 租约回收 / drain + `codehelper worker` | 杀掉进程后另一个进程能接管同一任务 |
| T3 | `agent_turn` executor（复用 RFC-006 的 turn driver）+ 后台 Governor | 一个后台任务真的跑出 turn 与收据 |
| T4 | workflow 节点表、DAG / join / when / retry / timeout、resume 与 spec 指纹 | 中途杀死后续跑只补未完成节点 |
| T5 | `shell_command` executor + fleet 调度代码删除 + 文档 | 全仓只有一条 claim 路径 |

`shell_command` 排在最后是因为它的幂等性最难，且 D6 的"至少一次"语义在它身上代价最大。

---

## 7. 测试

hermetic 要求与 RFC-006 一致：不联网、不依赖真实时钟长等待。

- **claim 竞争**：两个 worker 抢同一行，一个成功、一个拿到 `ErrClaimLost`，不出现两次执行；
- **fencing**：租约被回收后，旧 owner 的 `Settle` 必须失败，新 owner 的结果不被覆盖；
- **回收与恢复**：租约过期 → 重排队 attempt+1；新 Host 启动不修改其他 owner 的活 lease；无 lease 的旧式 running 行才由启动恢复分流；
- **workspace authority**：同一 DB 中两个 workspace 的 queued task 并存，worker 只 claim 自己规范化 root 下的行；
- **drain**：`Close` 期间在飞任务回到 `queued` 而不是 `failed`；
- **backoff**：注入时钟，断言第 n 次重试的 `next_attempt_at`；
- **automation**：注入时钟推进多个 slot，断言去重（C5）与 `next_run_at` 推进；两个 Repository 同时 tick 同一库，只产生一份 task；
- **agent_turn 端到端**：provider fixture + 后台任务 → 断言 `task_attempts` 有 thread/turn、任务 `completed`、收据可读；写类任务在非 git 工作区被 fail-closed；git worktree 写任务只有 merge 进入宿主工作区后才 completed；
- **节点 timeout fencing**：两个连续超时 attempt 的 driver `maxLive=1`；production fixture 还要为每个 attempt 观察到对应 `turn.canceled`，证明不是只停止上层等待；
- **workflow resume**：三节点 DAG，跑完第一个后杀掉 run，resume 只执行剩下两个；spec 改一个字节后 resume 被拒；
- **生产 executor 接线**：scheduler readiness 同时声明三类 executor；`workflow_run` 真实运行同波节点、共享 journal 不冲突并写 checkpoint；`shell_command` 经 Guard 执行，policy deny 时命令没有被调用；非幂等多 attempt 在执行前拒绝；
- **workflow task 参数**：production scheduler fixture 验证 `response_schema` 成功与 mismatch；profile/非法 schema 在 turn 前拒绝；JS 与 DAG 都把 schema 传到 Driver；
- **不可执行行**：`executor IS NULL` 的任务在 scheduler 跑满一轮后仍是 `queued`（C6 的回归测试，防止有人"顺手"放开）。

---

## 8. 明确不做

- **不加 task/job 协议事件面。** 审计靠 `tasks` + `task_attempts` + 既有 turn 事件；新增事件面要同时补 ACP/HTTP 双 host 的 contract fixture（§11.3 硬门禁），而 TUI 面板轮询仓储已经够用。等 M5 有真实外部消费者时再定形状。
- **不做分布式调度。** 不选主、不跨机器、不做工作窃取。作用域是"同一个工作区的若干本地进程"，靠 SQLite 的事务与租约。
- **不做 workflow 补偿节点（compensation）。** ROADMAP §7.3 里有，但补偿的语义依赖"哪些副作用可逆"，那要等 §5.4 的 checkpoint 落地才有基础。本轮只做到"上游失败可以触发另一个节点"（D8 的 `when`）。
- **不给 jsvm 做恢复**（D8 已说明理由）。
- **不做 per-project 资源配额的持久化统计。** 本轮只有进程内的 Governor 上限；跨进程的配额账本属于 M3 的 usage/cost 落库。

---

## 9. 未决问题

1. ~~**claim 的作用域**~~（已解决）。Claim join `sessions → workspaces`，只接受规范化 `root_path` 与 worker execution root 相同的行；session 可不同，因此同 workspace 的新进程仍能接管，另一个 workspace 的 Guard/tool root 不会误执行该任务。
2. **`max_attempts` 的默认值来源**。executor 级默认（agent 3、shell 1）还是配置级默认，二者冲突时谁赢，未定。
3. **后台 turn 的可见性**。后台 thread 的事件会进 SSE / ACP 的全量事件流，长时间后台任务可能刷掉交互 turn 的可见性。是否需要 host 侧按 thread 过滤，等 TUI 面板落地后实测。
4. **`fleet` JSONL 的退役时机**。取决于 TUI/CLI 面板改读 `tasks` 的进度，本 RFC 只规定它不再参与调度。
5. **workflow 节点的隔离粒度**。当前一次 run 复用宿主 workspace，并以 whole-turn gate 保持 journal 原子性，因此写节点不真正并行。每节点一棵 worktree及其 merge 语义仍未实现。
6. ~~**节点输出的跨进程可见性**~~（已解决）。`workflow_nodes.output_handle` 现在真的被写：`checkpoint.Repository` 拿一个 `Outputs`（生产上是 `contentstore.Durable` 包住 `{data-dir}/cas-v1`），`NodeSettled` 把节点输出按内容寻址存进去、把 handle 写进这一列，`LoadNodes` 再读回填 `NodeRecord.Content`。所以 resume 后的 run 摘要包含崩溃前那些节点产出的内容，而不是只报状态。handle 的字节丢了（换 data-dir、CAS 被清）按"无输出"处理而不是报错——状态才是 resume 依赖的东西，丢文本不会让记录变错。仍未做的是"上游结果驱动下游 prompt"：IR 不注入上游输出，那是独立决策。
7. **被中断在 `running` 的节点**。resume 只采纳 `completed` / `skipped` 的节点，`running` 的一律重跑：崩溃点之前它可能已经做了一半副作用，假设它成功比重跑更危险。这条与 D6 的"至少一次"语义一致，代价是不幂等的节点会重复副作用。

---

## 10. 验收

- 一个 `agent_turn` 任务从 `queued` 走到 `completed`，`task_attempts` 里有 thread/turn，事件流里能找到对应 turn 与收据；
- `kill -9` 掉持有租约的进程，另一个进程在租约超时后接管并跑完，同一任务不出现两次成功；
- 优雅停机后任务在 `queued` 而不是 `failed`；
- automation 的每个 due slot 只产生一个 task（多进程并发 tick 下同样成立）；
- 三节点 workflow 中途中断后 resume 只补未完成节点，spec 变更时 resume 被拒；
- `executor IS NULL` 的历史任务永不被执行；
- 全仓 `grep` 只有一个 claim 实现；
- `background_executor` 不再出现在不完整 maturity；scheduler readiness 列出三个 executor。
