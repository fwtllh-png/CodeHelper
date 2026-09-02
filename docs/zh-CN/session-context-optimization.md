# Session Context、Memory 与持久化

> 状态：已交付。本文描述当前行为，不再保留 Phase 式实施方案。
> SQLite、Truth Capsule 和 Checkpoint 格式仍处于首次稳定发布前的开发阶段。

## 设计目标

长期 Session 需要同时满足：

- 模型输入受当前 Route 的 Context Window 约束；
- Runtime 事实不会被模型摘要改写；
- 最近因果历史保留合法 Tool Call/Result 配对；
- Terminal、Checkpoint 和 Rebase 不重复写入整份历史；
- Restore/Fork 不把旧 Workspace 证据当成当前事实；
- Usage 与 Cost 单调，不随 Context Restore 回退。

Token 降低不是单独成功标准。正确完成率、Authority 完整性、重复工具调用、持久化写入
和恢复一致性必须一起评估。

## 四个状态平面

| 平面 | 内容 | Authority |
| --- | --- | --- |
| Live Context | History、Working Set、Evidence、Failures、Plan、World、Window | 当前模型继续任务的 Runtime 状态 |
| Workspace Binding | Workspace Identity、Journal Revision、Repository Head、Bound Path Digest | 文件相关声明的新鲜度依据 |
| Accounting | Usage、Cost、Budget Ledger | 单调事实，不随 Restore 回退 |
| Audit | Event、Receipt、Domain Fact、Trace、CAS Payload | 可重建证据，按 Retention 增长 |

这四个平面不能合并成一段 Summary。Context 可以被压缩，Accounting 不能回退，Audit
也不能自动重新进入模型输入。

## 模型可见的三层 Context

```text
Truth Capsule        Runtime 生成的结构化事实
Semantic Narrative   可选的非权威语义说明
Recent Raw Tail      最近的原始因果消息
```

`internal/runtime/agent/context.Authority` 持有当前 World、Working Set、Evidence、
Failures、Plan、Compaction 和 Token Window。`internal/runtime/agent/prompt` 负责把这些
状态投影成有预算的 Model Messages；Host 不参与选择或压缩。

Truth Entity 按 Mandatory、Protected 和 Refreshable 分类。Goal、开放计划、Pending
Interaction、未验证 Change 和开放 Diagnostic 等 Mandatory Fact 不能由 Narrative
替代。Narrative 不参与 Authority Digest，也不能授予 Permission、声明 Verification
成功或证明 Side Effect。

## 动态预算

执行预算和容量预算分开：

- `execution.budget_tokens`：可选的累计 Session 上限；`0` 表示不增加该上限；
- `execution.turn_budget_tokens`：可选的单 Turn 累计操作员上限；`0` 表示不设置。
  模型 Context Window 只约束单次请求，不能作为多次采样的累计成本上限；
- `execution.subagent.max_tokens`：可选的整棵 Child Tree 上限；`0` 时仅在显式设置
  Turn 上限后从该上限和 `max_parallel` 派生；
- `max_output_tokens`：正值是操作员上限，实际值仍受模型能力和剩余输入空间约束；
- 默认 Context Compaction 边界是
  `ContextTokens - 当前 Output Reserve`；显式值只用于 Operator 主动设置更小的成本
  或延迟 Ceiling。

预算检查使用已提交 Usage、当前 Turn Usage、当前 Step Usage、估算输入和输出预留。
耗尽时返回带 Scope、Used、Limit 和 Resource 的结构化 `resource_exhausted`，并保留
可恢复状态；它不是一个由模型无关固定值触发的永久失败。

`context.view.recent_tail_turns` 是原文 Turn 数的主边界，
`context.view.history_token_ceiling` 是原始 Tail 的 token residual。未显式配置时，
该 residual 等于当前 Turn 冻结的硬输入容量减去 Mandatory 分区（Stable、
`session_state` 等）；显式正值是更紧的 Operator Ceiling。
Tool Result 在首次准入时定稿：能放下则原文，超限则有界说明 + Handle，之后
不再改写已发送结果。投影从最新
闭合因果组向前填充，两者先到达者为准，并只在安全 Tool Pair 边界切分。
采样路径超窗、超过已知 TPM Burst、或等待将超过 `execution.rate_limit_wait` 时，
对可见 Tail 做一次因果组折叠（`mode=view`），不改写 Durable History；仍超则
`resource_exhausted`。短于等待预算的滚动窗口等待不折叠。History Replacement
只发生在显式 `thread.compact` 与 Turn 终态维护。

长会话分成四层，不恢复被投影裁掉的整段旧原文，也不恢复 75/80/90 改写：

1. 最近 `recent_tail_turns`（默认 2）个 Turn 保留原文；
2. 闭合后把带 `source_message_ids` 的 `unresolved` / `pending_job` /
   `next_step` 提升为未完成 Plan Todo，由 `session_state` 每轮必带；不自动完成
   已有 Todo；
3. 每个闭合 Turn 在 History 之后的 Dynamic 追加一块结构化 Checkpoint，
   write-once，不合并、不改写。失败只记有界失败事实，不带半开 Tool 链。
   取消 Checkpoint 保留下一项未完成 Plan 标题和已读路径指针，仍不带文件正文、
   不带半开 Tool 链。长度由 `context.view.checkpoint_max_bytes` 约束，`0` 继承
   `summary_max_bytes` 再继承 `semantic_narrative_item_max_bytes`；
4. 需要完整旧审计文本时用 `turn_history`（turn id）。首次投影是该 Turn 的
   **尾部（结论）**；全文进 Handle。继续分页用 `result_get` 的 `mode=tail`
   或 `mode=query`（例如 `query=P2`），不要用默认 `mode=summary`。
   首次写入后不再改写。被 last-2 裁掉的旧 Turn 会在 `session_state` 给出确定
   性检索指针（最小–最大 turn 范围），升级前缺失的 Checkpoint 只回封 turn id
   与 `turn_history` 指针，不把会话清单猜回 Plan。应继续原 Session，不要开新
   Session 去找回旧审计。
5. 当 Plan 已有完成步骤或 Working Set 已有已读路径时，`session_state` 必须带
   Resume Fact（`runtime.resume`）：不要重复已完成步骤，下一项未完成工作取
   第一项 outstanding Plan 标题，并列出已读路径。路径上限继承公开的
   `context.working_set.max_entries`。   `working_set` 只列路径，不放文件正文；
   有行号命中时 Resume Fact 还列出 `Located sites`。不要再次 `file_read`，
   除非即将编辑该路径的具体窗口。`search_text` / `search_definition` 命中某
   路径后，对该路径的 `file_read` 必须带 `start_line`，否则工具返回
   `located_site_window_required`。脏的 `git_status` / `git_diff` 不是重读理由。
   可见 Tail 没有那次读取也不是重读理由，应走 `turn_history` / `result_get`；
   若输出被截断，先 `result_get`，不要整文件翻页。已知缺陷用 `search_text` /
   `search_definition` 定位窗口。`search_text` 的 `path` 指向单个文件时，即使
   该文件超过结果 Token 预算派生的扫描上限，仍按公开的 walk 字节上限搜索；
   空命中若带 `skipped.large` 不表示符号不存在。已有行号命中后只读将编辑的
   窗口并立刻改，不要把该文件再翻一遍。取消或失败且未改文件的 Turn 已记在
   Checkpoint 里，不要用 `git_diff` 再确认一遍。Paused Continue 不得先用
   `git_status`、`git_diff` 或 `file_read` 巡视工作区。Plan 已有完成步骤且仍
   有 outstanding 工作时，读取新文件不再续期 No-progress Lease，并改用公开
   字段 `execution.implement_no_progress_samples`（默认 6）进入 Finish-only；
   该阶段不允许 `git_status` / `git_diff` 或无 `start_line` 的 `file_read`。

## Compaction 流程

每次投影后都重新测量 Active、Total、Compact Limit 与 Hard Limit：

1. 超硬窗口时对可见 Tail 做一次因果组折叠，不改写已发送 Tool Result；
2. 首次准入已超 ResultStore 合同的结果本来就是 Handle 投影，不再事后改写；
3. 计算保持 Tool Pair 闭合的可切分位置；
4. 从当前 Owner Snapshot 生成 Truth Capsule，而不是永久合并旧 Capsule；
5. 在容量允许时保留 Recent Raw Tail；
6. 可选生成 Semantic Narrative；
7. 验证 Authority Digest、Tool Pair 和新 Window；
8. 通过 Durable Rebase 提交，成功后才替换 Engine History。

Tool 原文保存在 Content Store。模型看到的有界 Surface 不是数据删除，也不改变审计
Payload。

失败且非取消的 Turn 不提交不完整的 Provisional 对话，而是追加一个有界、结构化的
`turn_terminal` Context Message，保留目标、Problem 和少量 Failure Fact。这样下一
Turn 能解释失败，又不会把半闭合 Tool Exchange 当成已提交历史。

## Semantic Narrative

`context.view.narrative_mode` 支持：

- `off`：只使用 Truth + Tail；
- `post_turn`：仅在 `turn.completed` 之后维护下一 Turn 的独立 Digest 分区。
  用户暂停 / `turn.canceled` / `turn.failed` 不调用 summary 模型，也不发
  fallback Compaction 卡片。

`digest` 只允许 `ledger` 或 `ledger+narrative`。Session State 始终 mandatory，
因此没有 `digest=off`。

Narrative 请求通过 `route.summary`，禁用 Tool 与 Native Search。输入 Artifact、Source
Message ID 和 Digest 都持久化；输出必须引用已知 Source。Provider、解析、超时或
Staleness 失败不能改写业务 Turn 结果，也不会替换 Durable History。
`route.summary` 的瞬时 429 / 5xx / Timeout 按主采样同一套 RetryPolicy 自适应重试，
但仍受 `semantic_narrative_timeout` 约束，且不得挡住下一轮 Sample。账户硬配额
立即 fallback。没有必须保留的
未完成工作时可降级到 Ledger 投影的 Session State；存在权威未完成 Todo 时必须生成
对应 Continuation Checkpoint，否则不能以缺少工作记忆的 fallback 继续执行。

默认使用 `narrative_mode=post_turn`。Narrative 是一份非权威 Continuation Checkpoint：除决策、理由、
偏好和未决问题外，还可保留关键技术概念、文件与代码接口、错误与修复、待办、当前
工作、下一步和关键上下文。Required Kinds 来自权威 Todo、Changes 和 Critical Paths，
而不是仅凭出现过 Tool Result 推导。每项继续绑定 Source Message ID 与 Digest；缺少
必需 continuation 类别时拒绝该次语义压缩。

## 自适应 Stream Checkpoint

Provider Stream 的恢复 Checkpoint 位于
`internal/adapter/provider/assembly.ConsumeStream`。首个状态和非批量事件立即持久化；
Text、Reasoning、Tool Fragment 与 Transport Progress 按累计增量聚合。

下一次 Checkpoint 所需增量随已持久化规模增长，而不是每个 Delta 都写一次或使用固定
事件数。Terminal、错误和中断会强制刷新剩余状态。该策略减少长输出的写放大，同时保留
可恢复的 Response Assembly。

## Session Delta 与 Manifest

`context.SessionDelta` 表示一次终态后的逻辑变化；`context.SessionManifest` 是它的
持久化表示：

- History 使用 Base/Tail CAS Ref；
- Working Set、Evidence、Failures 和 Plan 使用 Owner Segment；
- 未变化内容复用既有 Ref；
- Segment 达到配置的操作员上限后生成新 Base；
- 每个 Ref 校验 Handle、Digest 和物理字节数。

Terminal 路径先幂等 Stage CAS，再在 SQLite 事务中提交 Manifest 可达性、Frozen
Measurement、Receipt、Final Output、Terminal Event、Operation Receipt 和 Outbox。
事务成功后 Engine 才 Apply Delta。

## Checkpoint Restore 与 Fork

Checkpoint 保存 Context Manifest 与 Profile Snapshot，不保存可回放 Side Effect，也
不回退 Accounting。

Restore/Fork 执行以下步骤：

1. 校验 Schema、Identity、Profile Revision、CAS 和 Context Digest；
2. 重建 Checkpoint 时点的 Context-only State；
3. 重新捕获 Sparse Workspace Binding；
4. 使不匹配的 Change、Read、Evidence 和 Verification Claim 失效；
5. 重写包含旧 Workspace 声明的 Truth Capsule；
6. 创建新的 Epoch、Revision 和 Token Window；
7. 原子提交 Context Commit 与 Lineage。

`exact_context` 只描述 Context State；`side_effects_replayed=false` 明确说明 Restore
不会回滚 Workspace 或重新执行 Tool。

## User Memory

Memory 是独立的非权威数据面。记录具有稳定 ID、Generation、来源、可选过期时间，以及
`user`、`workspace`、`repository` Scope。Workspace/Repository Scope 使用规范化身份，
不能用未经解析的路径字符串隔离。

每个 Turn 在 Admission 时冻结 Memory Snapshot，并按 Pin、精确 Scope、词法相关性、
更新时间和稳定 ID 选择记录。当前 Turn 的写入只影响下一 Turn。`remember`、
`memory_list`、`memory_get`、`memory_update` 和 `forget` 都经过 Tool Guard。
`semantic_rerank` 尚未接线，必须保持关闭。

## Failure Policy

| 故障 | 行为 |
| --- | --- |
| Mandatory Truth 无法容纳 | 在提交新状态或副作用前拒绝 |
| Context 仍超过模型窗口 | 返回可恢复的 `resource_exhausted` |
| Narrative 失败或过期 | 丢弃 Narrative，保留 Truth + Tail |
| Rebase Commit 失败 | 不替换 Engine History，保留恢复入口 |
| Manifest/CAS 校验失败 | 拒绝加载或 Checkpoint 操作 |
| Workspace Binding 不匹配 | 恢复 Context，失效文件相关 Authority |
| Memory 检索失败 | 跳过注入并记录 Receipt |
| Memory 含 Credential Material | 拒绝持久化 |

## 代码地图

| 关注点 | 路径 |
| --- | --- |
| Authority、Compaction、Manifest | `internal/runtime/agent/context` |
| Prompt Projection | `internal/runtime/agent/prompt` |
| Turn 集成与 Rebase Effect | `internal/runtime/agent/engine` |
| Provider Stream Assembly | `internal/adapter/provider/assembly` |
| Durable Rebase | `internal/runtime/app/persistence` |
| Checkpoint Artifact | `internal/persist/artifact`、`internal/persist/snapshot` |
| Memory Store/Tools | `internal/adapter/memory`、`internal/adapter/tool/memory` |

## 验证

```bash
go test ./internal/runtime/agent/context
go test ./internal/runtime/agent/engine
go test ./internal/runtime/app
go test ./internal/runtime/app/persistence
go test ./internal/persist/artifact ./internal/persist/snapshot
go test ./internal/adapter/provider/assembly
go test -race -p 1 ./internal/runtime/agent/... ./internal/runtime/app/...
```

长期 Session 评估必须同时检查 Authority Digest、Tool Pair、Checkpoint
Reconciliation、Usage 单调性、每 Turn 物理写入和重复工作率。
