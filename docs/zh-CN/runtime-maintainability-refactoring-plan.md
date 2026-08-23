# Runtime 可维护性与所有权边界

> 本文描述当前实现及仍受门禁约束的结构性债务，不再记录已经完成的迁移步骤。
> 外部行为以代码、测试和 `docs/protocol/runtime-protocol.schema.json` 为准。

## 当前结论

Runtime 已完成以下收敛：

- 生产 Turn 只有一个入口：`engine.Engine.Execute`；
- 每个 Turn 先冻结 `TurnSpec`，再打开隔离的 `engine.Scope`；
- `turnkernel.TurnCoordinator` 是生产环境唯一的 `Reducer.Apply` 调用方；
- Context Authority、Prompt Projection 和 Repository Map 分属稳定边界；
- Provider Stream Assembly、Tool Batch/Result Projection 已下沉到 Adapter；
- `runtime/app.Runtime` 是 Service Facade，不是第二个 Agent Loop；
- 构造与关闭统一由 `runtime/app/wire` 和 `ResourceStack` 管理；
- Runtime Package 数量、热点文件和依赖方向由 Architecture Ratchet 约束。

这些结论替代旧的 M0-M7 迁移叙事。是否满足约束由测试和指标命令证明，不能由本文中的
静态行数证明。

## 核心所有权

| Owner | 路径 | 独占职责 |
| --- | --- | --- |
| Composition Root | `internal/runtime/app/wire` | Concrete Construction、Module 顺序、资源注册与回滚 |
| Application Services | `internal/runtime/app` | Operation、Session、Turn Lease、Terminal、Artifact 与 Host Query |
| Turn Engine | `internal/runtime/agent/engine` | 冻结 Turn、执行 Effect、组装结果与 Session Delta |
| Turn Kernel | `internal/runtime/agent/turnkernel` | 状态转换、收敛、交互等待、Effect 与终态决策 |
| Context Authority | `internal/runtime/agent/context` | World、History、Truth、Evidence、Compaction、Manifest 与 Rebase |
| Prompt Projection | `internal/runtime/agent/prompt` | 将权威状态投影为有预算的模型输入 |
| Provider Assembly | `internal/adapter/provider/assembly` | Stream 校验、增量组装、恢复状态与自适应 Checkpoint |
| Tool Runtime | `internal/adapter/tool` | Catalog Binding、Batch、Result、Surface Projection |
| Tool Guard | `internal/adapter/tool/guard` | Policy、Approval、Journal、Sandbox 与授权执行 |
| Durable Assembly | `internal/runtime/app/persistence`、`internal/persist` | Repository、CAS、Context Commit、恢复与持久化事务 |
| WorkGraph | `internal/orchestration/kernel`、`internal/orchestration/store` | Run、Node、Attempt、Lease、Effect 与幂等 Transition |
| Observation | `internal/observability` | 非权威证据、Usage、Trace、Diagnostics 与 Export |

可变状态只能由表中的 Owner 修改。Host、Web Projection、Exporter 和 Compatibility
Facade 都不能成为平行写入路径。

## Turn 主路径

```text
Host Operation
  -> operationDispatcher
  -> TurnService / ActiveTurnRegistry
  -> Engine.Execute
  -> frozen TurnSpec
  -> engine.Scope.Run
  -> TurnCoordinator.Submit
  -> Reducer.Apply
  -> durable Effect dispatch
  -> Provider Assembly / Guarded Tool
  -> TerminalPublisher
  -> atomic terminal transaction
  -> Event Hub projection
```

关键规则：

1. `Engine.Execute` 在进入 Scope 前序列化 Session 级 Engine 状态。
2. Scope 持有 Kernel、Trace、Diagnostics、Verification、Tool Spend、Diff、Control
   和 Context Clone。
3. Provider、Tool、Approval、Input、Verification、Narrative 与 Journal 都以 Kernel
   Effect/Command 闭环。
4. Reducer 只做状态转换；外部 I/O 由 Dispatcher/Adapter 执行。
5. Terminal 事务成功后才把 `SessionDelta` 幂等应用到 Engine。

## Context 与执行分离

`internal/runtime/agent/context` 持有会话事实和压缩算法，Engine 只负责在 Turn 生命周期
中调用它。当前边界如下：

- `context.Authority`：World、Working Set、Evidence、Failures、Plan 和 Window；
- `context.SessionDelta`：终态后可提交的 Context 变化；
- `context.SessionManifest`：Base/Tail 与 Owner Segment 的持久化表示；
- `context.SelectCompaction`：Tool Surface、因果边界、Truth/Tail 的确定性选择；
- `prompt`：把当前 Authority 投影成 Model-visible Messages；
- `engine`：冻结、执行、验证、请求 Rebase，并处理失败策略。

不得把这些状态重新复制回 Engine 字段或 Host Store。新的 Context 能力应先明确它属于
权威事实、非权威叙事、模型投影还是持久化表示。

## Adapter 下沉

Runtime 不应实现协议和工具基础设施细节：

- `provider/assembly.ConsumeStream` 负责 Vendor-neutral Stream Assembly，并按累计增长
  自适应拉开 Durable Checkpoint 间隔；
- `tool.ExecuteBatch` 负责串并行调度、Panic 隔离和 Result 归一化；
- `tool.ProjectModelResults` 保持 Tool Call/Result Identity；
- `tool/result` 负责可恢复结果与模型可见 Surface 裁剪；
- `observability/verify` 负责 Verification Gate；
- `observability/receipt` 负责终态和执行证据构造。

Engine 仍拥有何时调用这些能力以及如何把结果提交给 Kernel 的业务顺序。

## 组合根约束

`wire.NewExec` 通过封闭 Module 序列构造 Runtime。`buildState` 只在构造期存在，下游
Runtime、Engine、Scheduler 和 Contributor 不得保留它。

资源进入 `ResourceStack` 后按逆序关闭；部分构造失败和正常关闭共用同一套逻辑。新增
Module 必须：

1. 声明输入和输出；
2. 只消费前序 Module 的显式能力；
3. 注册已创建资源；
4. 不启动业务循环；
5. 在 Background Module 启动前保持 Runtime 不接收 Operation。

## 结构性债务

当前主要债务是 `runtime/agent/engine` 和 `runtime/app` 中仍有较大的协调文件。后续拆分
应以减少共享状态和变化原因耦合为目标，不能通过机械移动代码、增加单文件 Package 或
隐藏到 Helper 中满足行数指标。

允许继续下沉的判断标准：

- 能形成单一 Owner 和独立不变量；
- 调用方数量或协议边界足以证明抽象价值；
- 下沉后 Engine/App 不再保存重复状态；
- 不引入第二条执行、恢复或终态路径；
- Characterization、Race 和 Recovery Test 可以证明行为未变。

## 架构门禁

```bash
make architecture-metrics
make architecture-ratchet
make turn-kernel-convergence-exit-gate
go test ./internal/runtime/agent/turnkernel
go test ./internal/runtime/agent/engine
go test ./internal/runtime/app
go test ./internal/runtime/app/wire
go test -race -p 1 ./internal/runtime/agent/... ./internal/runtime/app/...
```

架构变更还必须运行 `make docs-check`、`make book-check` 和 `git diff --check`。

## Review 检查表

- 是否新增了 `Engine.Execute` 之外的生产 Turn 入口？
- 是否有人绕过 `TurnCoordinator` 调用 `Reducer.Apply`？
- 是否在 Host、Wire 或 Adapter 中复制了业务状态机？
- 是否让 Context、Terminal、Lease 或 Journal 出现双写？
- 是否让 Optional Observation/Narrative Failure 改写业务结果？
- 是否让配置默认值变成与模型容量无关的隐式执行终止器？
- 是否为新 Owner 增加了恢复、并发和失败测试？
- 是否同步更新架构、源码导读和相关书籍章节？
