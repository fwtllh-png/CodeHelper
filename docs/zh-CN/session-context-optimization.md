# Session Context Memory 优化设计与实施方案

简体中文

> 状态：已实施。Semantic Narrative 默认关闭；`post_turn` 和 `inline` 需显式配置。
> 本次格式切换将 SQLite Schema 提升为 v3、Truth Capsule 提升为 v2、Checkpoint
> Protocol 提升为 v2；项目尚未稳定发布，因此不保留旧格式双写或自动迁移。

实现入口：

- `internal/runtime/agent/context`：Retention、Admission、Narrative Artifact 和
  Compaction Plan；
- `internal/runtime/agent/context`：Context Snapshot、Workspace Binding、
  Base/Tail Manifest 和 Rebase Envelope；
- `internal/runtime/agent/engine`：三层 Context、Summary Route、Inline/Post-turn
  Rebase 与 Turn Kernel Effect；
- `internal/runtime/app/persistence`、`internal/persist/snapshot`：原子 Rebase 和
  Manifest-backed Checkpoint；
- `internal/adapter/memory`、`internal/adapter/tool/memory`：Scoped Memory Record、
  Lexical Retrieval 和受 Guard 的 CRUD。

## 1. 决策摘要

CodeHelper 保留现有确定性结构化压缩作为唯一权威记忆，并在其上增加一个可选、
非权威的语义 Narrative。优化的对象不是一段越来越聪明的 Summary 文本，而是一个
有准入、有生命周期、有 Workspace 一致性校验、可原子提交的 Session Context State。

模型可见上下文由三层组成：

```text
Truth Capsule        Runtime 事实，可校验、必须保留
Semantic Narrative   LLM 生成的动机与关系说明，可丢弃、不可授权
Recent Raw Tail      最近的原始 User/Assistant/Tool 因果历史
```

持久化状态则明确拆成四个互不替代的平面：

```text
Live Context State     当前模型继续任务所需的有界状态
Workspace Binding      Context 中 Workspace 事实所绑定的版本和内容证据
Accounting             单调 Usage、Cost 和 Budget，不随 Restore 回退
Audit History          Event、Receipt 和 CAS 历史，按 Retention 线性增长
```

本方案同时解决五个与长期 Session 直接相关的问题：

1. Truth Entity 和 Evidence 跨长期 Session 无界增长；
2. Checkpoint 只恢复 History，不能恢复同一时刻的 Working Set、Evidence、Failures 和
   Plan；
3. 每个终态保存完整 Session Delta，造成长期 Session 的写放大；
4. 旧 Checkpoint Evidence 与当前 Workspace 内容可能不一致；
5. 用户 Memory 是整文件注入，缺少 Scope、检索、更新、删除和同 Runtime 刷新。

结构化事实、语义 Narrative、用户 Memory 和 Checkpoint 必须继续拥有不同的 Authority、
Retention 和 Failure Policy，不能再次合并成一个不透明文本字段。任何优化都不能以
“摘要成功”为理由保留已经过期的验证声明、绕过 Provider Router，或把审计存储误称为
常数空间。

## 2. 当前基线

当前实现已经具备以下必须保留的性质：

- `agentcontext.MessageLedger` 将每次模型输入分成 Stable、History、Dynamic 和 Continuation，
  并生成 Revision、Digest 和 Token Attribution；
- Working Set、Evidence、Failures、Plan、World Baseline 和 Token Window 随
  `agentcontext.SessionDelta` 持久化；
- Terminal Envelope、Domain Facts、Session Delta、Receipt 和 Outbox 原子提交，
  提交成功后才更新 Engine 内存；
- Compaction 先裁剪大型 Tool Result Surface，再在安全 Tool Pair 边界替换历史；
- Truth Capsule 只从 Runtime 观察生成，`verified=true` 必须来自 Runtime Evidence；
- Summary 候选必须包含当前全部 Authority Entity，不能通过丢事实换取绿色结果；
- 旧 World State 和 Contextual Fragment 会在压缩时移除，再注入当前版本；
- Checkpoint 内容经过 Schema、Identity、Profile Revision 和 CAS Hash 校验；
- 用户 Memory 默认关闭，只能通过受 Guard 管理的 `remember` Tool 追加。

这些约束是优化的起点，不是需要替换的旧实现。

## 3. 问题定义

### 3.1 Truth Capsule 只有正确性边界，没有长期容量边界

当前 Capsule 会合并前代 Entity。Failure Ledger 有数量限制，但 Evidence Facts、
Content Handles、历史 Critical Facts 和其他 Entity 没有统一的生命周期策略。
当 Mandatory Capsule 本身超过 `summary_max_bytes` 时，安全行为只能是拒绝压缩并最终
返回 Context Resource Exhausted。

### 3.2 结构化事实无法完整表达讨论语义

Goal、Todo、Change 和 Evidence 能回答“发生了什么”，但不能稳定表达：

- 为什么拒绝某个方案；
- 用户偏好的具体优先级；
- 两个约束之间的关系；
- 尚未固化为 Plan Step 的设计方向；
- 某段探索为何停止。

把这些全部扩展成 Authority Entity 会使 Schema 持续膨胀，并错误地把模型解释升级为
Runtime 事实。

### 3.3 Checkpoint 存在时间点不一致

当前 Checkpoint 保存 History 和 Profile。Restore 只替换 History；Fork 则先复制当前
Engine 的 Working Set、Evidence、Failures、Plan，再替换为旧 History。结果可能是旧
对话历史与新 Evidence 同时出现。该行为虽然不会重放 Tool Side Effect，但不满足
“从某一历史时点建立独立 Context 分支”的直觉。

反方向地，把旧 Working Set 和 Evidence 一并精确恢复也不一定正确。Checkpoint 之后的
文件修改仍然留在 Workspace；旧的 `verified=true`、Read Digest 或 Diagnostic 可能已经
不再描述当前文件。因此 Checkpoint 需要同时保存 Workspace Binding，并在 Restore/Fork
时对 Workspace-dependent Entity 执行失效或重新验证，不能只追求账本字节相等。

### 3.4 Session Delta 有持久化写放大

`agentcontext.SessionDelta` 是完整快照。即使 Wire 使用压缩编码，每个 Turn 仍会再次写入历史和
大部分账本。压缩发生前，N 个逐渐增长的 Turn 可能产生接近 O(N^2) 的累计历史写入。

### 3.5 用户 Memory 缺少治理和检索

单个 `memory.md` 简单可靠，但存在以下限制：

- 所有记录共享一个用户级 Scope；
- 启用后按文件整体注入，而不是按当前任务检索；
- 没有稳定 Record ID、去重、更新、删除、过期或来源；
- `remember` 写入后，当前 Runtime 的静态前缀不会刷新；
- Secret 检测只是一组预防性启发式规则。

## 4. 目标与非目标

### 4.1 目标

- 对每个通过 Context Admission 的 Model Route，给模型可见状态一个可证明的硬上界；
- 在 Mandatory 状态进入账本前执行准入控制，避免 Session 在压缩时才永久失效；
- 保留结构化事实的确定性、可恢复性和审计能力；
- 用可选 Narrative 提升设计讨论和长期任务的语义连续性；
- Checkpoint Restore/Fork 恢复讨论状态，并将 Workspace-dependent Evidence 与当前
  Workspace 重新对账；
- 将稳定 Session 的 Live State 持久化从重复全量快照改为有界增量，使每 Turn 新增
  物理写入有上界；
- 提供有 Scope、有生命周期、可查询的用户 Memory；
- 让每次省略、降级、过期和回退都产生可观测 Receipt。

### 4.2 非目标

- 不允许 LLM Narrative 成为 Policy、Permission、Verification 或 Side Effect 事实；
- 不通过 Checkpoint 自动回滚 Workspace 或重放历史 Tool；
- 不在共享 Workspace 上把旧 Evidence 无条件恢复成当前事实；
- 不在 Host 中实现第二套 Summary、Memory 或恢复逻辑；
- 不把 Vector Database 作为第一阶段前置依赖；
- 不承诺由模型文本证明因果关系或“事实被模型使用”；
- 不为了兼容未发布格式引入长期双写；只有明确发布需求才增加迁移。

## 5. 设计原则

1. **Authority before fluency**：先保证事实完整，再优化可读性。
2. **Bounded by construction**：所有模型可见集合必须在构造时有数量和字节上界。
3. **Admission before saturation**：会扩大 Mandatory State 的操作先证明仍能容纳，再提交。
4. **Workspace claims are versioned claims**：验证、读取和诊断只在其 Workspace Binding
   有效时保持 Authority。
5. **Current state, not endless union**：当前 Owner 重新声明状态；历史代际不能永久并集。
6. **One Runtime authority**：Host 只提交 Operation 和投影 Receipt。
7. **Commit before apply**：持久化成功前不修改 Session 权威内存。
8. **Optional work cannot rewrite business outcome**：Narrative 失败只能成为维护 Receipt。
9. **State restore is not side-effect replay**：Context 与 Workspace Side Effect 明确分离。
10. **Bounded live state, retained audit**：Live State 有界；Audit 按明确 Retention 线性增长，
    不能把两者混为“总存储有界”。
11. **Qualification and discovery are separate**：确定性门禁证明契约，探索性评测寻找未知
   退化，不能互相冒充。

## 6. 目标架构

```text
Tool/Provider observations
          |
          v
Owner Ledgers + Workspace Binding
          |
          v
Admission Controller -----> Retention Planner -----> Bounded Truth Capsule
                                                        |
Removed history ---> Narrative Input -------------------+---> Context Window
                         |                              |
                         +-> optional durable LLM job --+     + Recent Raw Tail

Terminal commit
    |
    +-> Session Context Manifest
    |      +-> per-owner base/delta CAS refs
    |      +-> Workspace Binding
    |      +-> State epoch/revision
    |
    +-> Accounting delta
    +-> Runtime events and outbox

Checkpoint
    +-> Context Manifest ref + Profile snapshot
    +-> Workspace Binding + reconciliation policy
    +-> no implicit Workspace replay or accounting rewind
```

## 7. 有界 Truth Capsule

### 7.1 Mandatory Admission

Retention 不能在压缩时才第一次处理容量问题。任何会扩大 Mandatory State 的 Runtime
Transition 都必须先经过 `ContextAdmissionController`：

```go
type AdmissionRequest struct {
    ThreadID              protocol.ThreadID
    BaseContextRevision   uint64
    RouteCompatibility    string
    AddedMandatory        []compact.TruthEntity
    ResolvedMandatoryIDs  []string
    ProjectedStableTokens uint64
    ProjectedToolTokens   uint64
}

type AdmissionDecision struct {
    Allowed            bool
    Reason             string
    ProjectedTruthBytes int
    ProjectedEntities   int
    RequiredActions     []string
}
```

至少以下 Transition 需要准入：新增 Plan Step、扩大未验证 Change 集、创建长期 Pending
Input、登记开放 Diagnostic，以及 Model Downshift。准入使用同一个 Canonical Truth Codec 和
所选 Route 的完整预算，不使用粗略 Entity 数量代替字节测量。

对于文件写入这类只有 Tool 执行后才知道精确 Change Set 的操作，Guard 在执行 Side
Effect 前创建有界 `MandatoryReservation`，按 Tool 声明的最大影响面预留容量；Tool Result
返回后，Reducer 用实际 Entity 原子结算或释放 Reservation。无法给出有限最大影响面的
Tool 不得在接近 Saturation 时执行。这样 Admission 不会发生在文件已经修改之后。

容量不足时，Runtime 在状态提交前返回结构化建议，例如先验证已有 Change、关闭完成的
Plan Step、拆分新 Thread 或选择更大 Context 的 Route。不得先提交无限增长的 Mandatory
State，再等下一次 Compaction 把 Session 变成永久 `resource_exhausted`。

### 7.2 Entity 生命周期分类

每个 Entity 必须带有 Owner、Retention Class、更新时间和可恢复性：

| Class | 示例 | 规则 |
| --- | --- | --- |
| `mandatory` | Active Goal、Open Todo、Pending Input、未验证 Change、开放 Diagnostic | 必须精确保留；无法容纳时失败关闭 |
| `protected` | Critical Path、最近 Failure、未消费 Handle | 在类型上限内保留；淘汰时必须产生 Omission |
| `refreshable` | Definition/Reference Fact、已验证旧 Change | 可按需重新搜索；按相关性和新鲜度淘汰 |
| `audit_only` | 已消费 Handle、被替代决策、过期重复事实 | 不再进入模型 Context，仍保留在 Event/CAS |

Authority Equivalence 只要求仍然有效的 `mandatory` Entity 精确等价。Workspace-dependent
Entity 还必须具有有效的 Workspace Binding；Binding 失效后，Entity 必须转成 `stale`
或重新验证，不能继续携带 `verified=true`。`protected` 和 `refreshable`
必须满足 Retention Policy，并在 Receipt 中记录保留、降级和淘汰数量。这样既不伪造
“全部事实仍在”，也不让可重新发现的事实阻塞整个 Session。

Omission 本身也必须有界。Receipt 只保存按 Kind/Reason 聚合的 Count、范围 Digest 和最多
若干 Sample ID，不为每个被淘汰 Entity 创建永久可见的 Omission Entity。

### 7.3 确定性 Retention Planner

Planner 按以下稳定顺序分配预算：

```text
mandatory
-> protected by kind quota
-> refreshable by criticality, recency, source rank, stable ID
-> omission summaries
-> optional human-readable sections
```

建议初始配置：

```toml
[context.compact]
summary_max_bytes = 8192
truth_max_bytes = 5632
truth_max_entities = 256
mandatory_max_entities = 128
fact_max_entities = 96
verified_change_retention_turns = 32
failure_max_entities = 24
handle_max_entities = 32
omission_sample_max_entities = 8
```

Config Validation 不能只检查 `truth_max_bytes < summary_max_bytes`。它必须使用 Canonical
Codec 的最坏 Wrapper、聚合 Omission、最小 Raw Tail 和 Provider Framing Reserve 验证
组合；Narrative 只使用全部必要项之后的剩余预算。

### 7.4 当前快照取代永久并集

每次压缩由各 Owner 输出当前 Entity Snapshot：

- Plan Owner 只输出当前 Goal 和 Steps；
- Evidence Owner 输出全部 Mandatory Risk，以及经过选择的 Facts；
- Failure Owner 输出有界最近失败；
- Content Store 输出仍可读取的 Handle；
- Working Set 输出 Critical Paths 和有界相关路径。

前代 Capsule 只用于恢复仍被 Owner 声明、但当前进程尚未重建的状态。完成一次
Session Delta Restore 后，应由恢复后的 Owner Snapshot 接管，而不是继续无条件合并
所有旧 Entity。

### 7.5 Workspace Binding

Workspace-dependent Entity 至少绑定以下信息：

```go
type WorkspaceBinding struct {
    WorkspaceIdentity string
    JournalRevision   uint64
    RepositoryHead    string
    SparseDigest      string
    BoundPaths        []BoundPath
}

type BoundPath struct {
    Path          string
    ContentDigest string
}
```

`SparseDigest` 覆盖所有被 Authority Entity 引用的路径，不要求对整个大型仓库每 Turn 做全量
Hash。Workspace Identity、Journal Revision 或 Bound Path Digest 不匹配时：

1. Goal、Plan、用户偏好等 Context-only Entity 仍可恢复；
2. Read、Verified Change、Diagnostic 和文件 Fact 标记为 `stale`；
3. 下一次 Provider Sample 前按预算执行 Re-project/Re-verify；
4. 未完成重新验证前，Truth 不得声明旧验证仍然有效；
5. Receipt 报告 Invalidated、Revalidated 和仍然 Stale 的 Entity 数量。

### 7.6 大值外置

Entity Value 超过类型限制时写入 CAS，Capsule 只保留：

```text
entity_id
kind
status
content_digest
content_handle
bounded_excerpt
```

Handle 必须继续经过现有 `result_get`/Content Guard 路径，不能建立绕过 Guard 的读取接口。

## 8. 非权威 Semantic Narrative

### 8.1 职责

Narrative 只回答结构化事实不擅长的问题：

- 方案选择的原因；
- 用户表达的偏好和优先级；
- 约束之间的关系；
- 已探索但放弃的方向；
- 下一步接续工作所需的语义桥接。

Narrative 不得声明测试、修改、权限、审批或发布结果。相关事实只能来自 Truth Capsule。

### 8.2 生成时机

Narrative 不参与“让当前 Provider Request 能够放入窗口”的必要路径。确定性 Compaction
必须先独立成功。支持两种生成模式：

- `post_turn`：可靠性优先。Terminal Commit 将待处理的 Compaction State 和 Input
  Artifact 一并持久化，Runtime 随后执行维护；进程中断时由下一次 Thread 激活重试，
  生成结果从下一 Turn 开始生效；
- `inline`：连续性优先。达到阻塞阈值时暂停当前 Agent Loop，通过 Durable Provider
  Effect 生成 Narrative，原子 Rebase 后继续当前 Turn。

`inline` 仍然不是 Context 可用性的前提：Narrative 超时、取消、解析失败或 Provider
失败时，Runtime 必须立即使用 Truth Capsule + Recent Raw Tail 完成 Rebase。这样额外
模型调用只影响语义质量和延迟，不决定 Session 能否继续。

### 8.3 输入与输出契约

输入只包含有界材料：

- Bounded Truth Capsule；
- 被移除消息的有界候选片段及稳定 Message ID；
- 明确的非权威生成说明；
- 禁用全部 Tool、Native Search 和 Side Effect；
- 固定低 Reasoning Effort 和独立 Token Budget。

Builder 的结果必须先成为 Durable、Bounded、经过 Privacy Admission 的输入 Artifact：

```go
type NarrativeInputArtifact struct {
    Version         int
    ThreadID        protocol.ThreadID
    SourceWindowID  string
    AuthorityDigest string
    RouteDigest     string
    PrivacyClass    string
    Excerpts        []NarrativeExcerpt
    Digest          string
    ExpiresAt       time.Time
}
```

Durable Job 和 Inline Effect 保存 `NarrativeInputRef`，不能只保存 Digest。若 Privacy
Policy 不允许持久化某段原始内容，Builder 必须在提交 Job 前删除该 Excerpt；重启时没有
有效 Input Artifact 就确定性标记 `stale` 并走 Truth + Tail 回退，不能从不完整日志猜测
原请求。

模型必须返回结构化 JSON：

```json
{
  "decisions": [{"text": "...", "source_message_ids": ["msg_..."]}],
  "rationale": [{"text": "...", "source_message_ids": ["msg_..."]}],
  "preferences": [{"text": "...", "source_message_ids": ["msg_..."]}],
  "unresolved": [{"text": "...", "source_message_ids": ["msg_..."]}]
}
```

Validator 检查 Schema、UTF-8、长度、重复项、未知 Source ID 和禁止字段。Source ID 只能
证明“引用存在”，不能证明模型解释正确，因此 Narrative 始终以
`non_authoritative=true` 渲染。

### 8.4 Artifact 身份

建议新增：

```go
type NarrativeArtifact struct {
    Version         int
    ThreadID        protocol.ThreadID
    WindowID        string
    AuthorityDigest string
    InputDigest     string
    RouteDigest     string
    Body            Narrative
    CreatedAt       time.Time
    ExpiresAt       time.Time
}
```

只保留每个当前 Window 的最新有效 Artifact。Narrative 不能跨代递归合并；仍然相关的
旧 Narrative Item 可以原样携带，但不能作为模型输入再次总结；需要重写时必须根据其
Source Message Ref 回读原始记录。这样避免 Summary-of-Summary 漂移。Model
Downshift 或 Compatibility Hash 变化时默认丢弃旧 Narrative。

### 8.5 安全边界

- Narrative 内容按不可信模型文本处理；
- Tool Output 和用户文本中的 Prompt Injection 不能变成 Policy；
- Narrative Job 只能通过 Provider Router 和 Durable Effect Dispatcher；
- 不允许直接从 `compact` Package 调 Provider；
- Raw Input 是否持久化遵循 Observation Privacy Policy；
- Narrative 失败不触发 Turn Repair，也不消耗业务 Verification Budget。

### 8.6 三层统一数据结构

三层结构必须先作为 Provider 无关的 Runtime 对象存在，再由 Adapter 投影为具体模型
协议。不能把 Anthropic `compaction` Block、OpenAI Message 或某个 Host 的 UI State
作为内部事实来源。

```go
type CompactedContext struct {
    Version             int
    CompactionID        string
    ThreadID            protocol.ThreadID
    TurnID              protocol.TurnID
    SourceWindowID      string
    TargetWindowID      string
    SourceContextDigest string
    StablePrefixDigest  string

    Truth               compact.TruthCapsule
    Narrative           *NarrativeArtifact
    Tail                []provider.Message

    AuthorityDigest     string
    NarrativeDigest     string
    TailDigest          string
    Digest              string
}

type Narrative struct {
    Items []NarrativeItem
}

type NarrativeItem struct {
    ID               string
    Kind             string // decision | rationale | preference | unresolved
    Text             string
    SourceMessageIDs []string
    SourceDigest     string
    CreatedTurn      uint64
}
```

`CompactedContext.Digest` 覆盖三层内容和所有 Window Identity。`AuthorityDigest` 只覆盖
Truth，Narrative 和 Tail 变化不能制造 Authority 等价。`NarrativeItem.ID` 由 Kind、
Canonical Text 和排序后的 Source Message ID 计算，保证重试去重。

模型可见顺序固定为：

```text
Stable System Prefix
Truth Capsule
Non-authoritative Narrative（如果有效）
Recent Raw Tail
Dynamic World State
Continuation/Input
```

Truth 和 Narrative 在 Runtime 内首先表示成带 Schema、Digest 和 Provenance 的
`ContextDataBlock`。Adapter 再把它投影为目标 Provider 支持的强定界数据块；不得仅依赖
一个模型不可见的 `provenance` 字段，也不默认伪装成普通 Assistant 历史声明。Stable
Prefix 必须明确说明 Narrative 是不可信数据，不是指令、权限或验证证据。Tail 保留原始
Role 和 Content Block。

首版不直接依赖 Provider Native Compaction Block。Native Block 只能作为 Adapter
优化，并且必须证明其逻辑请求与 `CompactedContext` 等价；否则切换 Provider 会改变
Session Authority。

### 8.7 Trigger 与三层 Token Budget

每个 Sample 前使用 Provider 已观测输入作为基线，再加本地 Context Delta：

```text
projected_input
  = observed_provider_input
  + estimated_delta_after_observation

required_capacity
  = projected_input
  + requested_output_reserve
  + provider_framing_reserve
```

定义三个阈值：

| 阈值 | 默认建议 | 行为 |
| --- | --- | --- |
| `prepare` | Hard Limit 的 55% | Post-turn 预生成 Narrative 或标记下一次压缩 |
| `compact` | Hard Limit 的 65% | 运行结构化压缩；`inline` 模式进入暂停状态 |
| `emergency` | Hard Limit 的 85% | 跳过 Narrative，立即结构化 Rebase 并限制 Tool |

阈值必须使用所选 Model 的实际 Context Limit，并为输出、Tool Definition 和 Provider
Framing 保留空间。不能只按 History Token 触发。

三层预算按以下顺序分配：

```text
available_history_budget
  = compact_target
  - stable_prefix
  - tool_definitions
  - dynamic_context
  - output_reserve
  - framing_reserve

1. Mandatory Truth
2. 当前未闭合因果组和 minimum raw tail
3. Protected/Refreshable Truth
4. Additional raw tail
5. Semantic Narrative
```

Narrative 只消费前四项之后的剩余预算。若 Mandatory Truth 加当前未闭合因果组已超过
Hard Limit，Runtime 必须先执行 Tool Result Surface Pruning；仍无法容纳时返回
`resource_exhausted`，不能丢失 Open Tool Call、Pending Input 或未验证 Change。

### 8.8 Compaction Plan 与切分算法

触发后先生成不可变 `CompactionPlan`：

```go
type CompactionPlan struct {
    ID                  string
    Phase               string
    Trigger             string
    SourceWindowID      string
    TargetWindowID      string
    SourceContextDigest string
    Cut                 int
    RemovedMessageIDs   []string
    TailMessageIDs      []string
    Truth               compact.TruthCapsule
    NarrativeInput      NarrativeInput
    DeterministicResult CompactedContext
    Digest              string
}
```

切分单位不是任意 Message，而是 Causal Group：

- 一个完成 Turn；
- 一个 Assistant Tool Call 与对应 Tool Result；
- 当前 Turn 内已经闭合的 Tool Pair；
- Pending Approval/Input 及其请求消息；
- Provider Continuation Fragment。

选择规则：

1. 先执行现有 Tool Result Surface Pruning；
2. 从最旧 Causal Group 开始生成候选 Cut；
3. Cut 两侧都必须满足 Tool Pair 完整性；
4. 当前未闭合 Group 永不进入 Removed Set；
5. Tail 至少保留当前 Group 和配置的最近完成 Turn；
6. 删除旧 World State、Skill 和 Constitution Fragment，由当前 Owner 重新投影；
7. 每个候选都重新测量完整 Context，而不是按历史字节差推测；
8. 选择第一个满足 Target 且 Authority 等价的候选。

Message ID 应由 Thread、Turn、Role、Block Identity 和 Content Digest 生成。它必须跨重试
稳定，但不把原始 Prompt 或 Tool Output 写入 ID。

### 8.9 Narrative Input Builder

LLM 不直接接收全部 Removed History。Builder 在独立预算内选择语义密度较高的 Source：

```text
显式用户约束和偏好
-> 方案选择或拒绝理由
-> Open Question 和未完成方向
-> Error/Failure 的有界说明
- 通过独立 Privacy Admission 的 Tool Result Head/Tail Excerpt；首版默认排除全部
  Tool Result
-> 普通 Assistant 叙述
```

选择先按 Priority 和 Recency 排名，再恢复原始时间顺序发送。完整代码文件、重复 Tool
输出、Reasoning Block、旧 World State 和 Secret/Restricted Payload 不进入 Narrative
Input。每个 Excerpt 必须携带 Message ID、Role、Turn、Digest 和是否截断。

建议请求属性：

```text
tools = []
native_search = false
reasoning_effort = low
temperature = deterministic provider default
max_output_tokens = semantic_narrative_max_output_tokens
route_purpose = summary
usage_kind = context_compaction
```

Phase 4 必须正式启用现有注册但尚未接线的 `model.PurposeSummary`。Narrative Request
通过 `RouteSet.For(PurposeSummary)` 解析；只有 Route Lock 允许时才回退到 Act Route。
`usage_kind=context_compaction` 只是 Usage/Observation Attribution，不是第二个 Router
Purpose，也不新增旁路 Client。Inline 模式在生成 Plan 时冻结解析后的 Route Digest；
Post-turn Job 则冻结 Job 创建时的 Route Digest。

默认 Prompt 要求：

1. 只输出指定 JSON；
2. 只总结动机、约束、偏好和未决问题；
3. 不声明测试通过、修改完成、审批通过或权限；
4. 每项引用一个或多个 Source Message ID；
5. 不输出 Tool Call、代码执行请求或继续任务的命令；
6. 不复制 Truth Capsule 已能准确表达的大段事实。

### 8.10 Narrative Validation 与降级

Provider Result 必须通过以下 Gate：

- Stop Reason 表示完整输出，不能是截断或 Content Filter；
- 只包含一个 JSON Text Payload，不含 Tool Call、Image 或 Reasoning Replay；
- JSON 使用严格 Schema，拒绝未知字段和尾随内容；
- Kind、单项字节、总项数和总字节满足上限；
- 所有 Source Message ID 都属于当前 `CompactionPlan`；
- Source Digest 与计划一致；
- Item ID Canonical 且无重复；
- Artifact Window、Authority 和 Route Digest 与当前状态一致。

Validator 只能证明结构和来源绑定，不能证明模型解释语义正确。因此即使全部通过，
Narrative 仍然非权威。任何 Gate 失败都产生 `fallback=truth_tail` Receipt，并使用
`DeterministicResult` 继续，不重试普通 Agent Sample，也不触发 Repair。

### 8.11 Inline 暂停状态机

`inline` 模式增加一个 Turn-local、Durable Compaction Substate：

```text
idle
  -> quiescing
  -> planning
  -> generating_narrative
  -> rebasing
  -> committed
  -> resumed

generating_narrative -- failure/timeout --> fallback
fallback -> rebasing
```

状态建议：

```go
type CompactionState struct {
    ID                string
    Phase             string
    PlanDigest        string
    PlanRef           string
    NarrativeInputRef string
    NarrativeEffectID string
    RebaseEffectID    string
    SourceWindowID    string
    TargetWindowID    string
    Attempt           uint32
    StartedAt         time.Time
}
```

具体控制流：

1. Engine 在安全 Sample 边界提交 `ContextCompactionRequested`；
2. Reducer 进入 `quiescing`，停止新 Tool Effect；
3. 只有 Pending Tool、Approval 和 Input Ledger 达到允许状态后才生成 Plan；
4. `ContextCompactionPrepared` 持久化 Plan/Input CAS Ref、Digest 和确定性回退结果；
5. `inline` 模式请求 `EffectGenerateNarrative`，并在 `EffectStarted` 持久化后调用
   Provider；
6. `NarrativeResultReceived` 只关闭对应 Effect，拒绝 Late/Duplicate/Wrong-ID Result；
7. Reducer 请求 `EffectCommitContextRebase`；
8. Rebase Commit 成功后替换 Scope History、推进 Window，并继续原 Turn 的下一个
   Provider Sample。

建议新增 `EffectGenerateNarrative` 和 `EffectCommitContextRebase`，并将它们纳入
`DurableEffectDispatcher` 的完整路由和恢复测试。不能从 `runCompactGate` 同步直调
Provider，否则 Crash、Cancel 和重复提交无法被 Turn Kernel 解释。

Control 语义：

- User Cancel：取消 Narrative Effect，不接纳 Late Result；按 Cancel 路径决定是否执行
  Terminal 结构化维护；
- Steer：在 Compaction 期间排队，Rebase 后作为 Continuation 注入；
- Approval/Input：不创建新的交互请求；已有请求保持原 ID；
- Shutdown：Running Effect 在恢复时重新排队，不能制造第二个 Compaction ID；
- Timeout/Rate Limit：最多按 Provider Policy 重试，耗尽后确定性降级。

### 8.12 原子 Rebase 与持久化

Rebase 是一个可恢复的 Context State Transition，不是仅供 UI 展示的 Summary Event。
提交材料至少包含：

```text
CompactionPlan Digest
Source/Target Window ID
Source Context Digest
Truth/Narrative/Tail Digest
Replacement History 或 Context Manifest Ref
Authority Digest
Token Measurement
Compaction Receipt
```

唯一提交 Owner 是 `internal/runtime/app/persistence`。它提供单一的
`CommitContextRebase(ContextRebaseEnvelope)` 接口。Inline 路径在一个 SQLite 事务中
提交：

1. 新 Context Manifest 的可达记录；
2. Window Revision；
3. `EffectCommitContextRebase` 的完成 Domain Fact。

`turn.compaction` 是提交成功后的 Projection；它不能反向改变 Rebase 结果。Terminal
自身的 Receipt/Terminal Event 继续使用现有 Terminal Outbox 原子提交与恢复。

CAS Blob 在事务前按 Digest 幂等 Stage；SQLite 事务只提交 Manifest 可达性和 Ref
Ownership。事务失败留下的不可达 Blob 由启动恢复或 Retention GC 清理，不能假设文件
系统 CAS 引用计数与 SQLite 天然组成一个原子事务。Engine、`sessiondelta` 和 Snapshot
Repository 都不得单独提交 Rebase 的一部分。

提交前不能修改 Engine 或 Scope 的权威 History。提交成功后 Apply Exactly Once；相同
Compaction ID 与 Digest 重放返回成功，不同 Digest 返回 Conflict。提交失败时 Turn
进入可恢复状态，不允许带旧 Window 继续采样。

Terminal Commit 必须引用当前已提交 Context Revision。这样 Mid-turn Rebase 后发生
Crash，恢复过程会从新 Window 继续，而不是再次把旧历史发送给 Provider。

Ephemeral Runtime 使用相同 State Machine 和 Digest 校验，只把 Store 替换为 Memory
实现，避免形成第二套语义。

### 8.13 Post-turn 维护、重启与多代压缩

`post_turn` 模式在 Terminal Commit 中持久化 `prepared` Compaction State 和完整
Narrative Input Artifact。Runtime 在 Terminal 投影完成后执行维护；若进程在其间退出，
Thread 恢复会重新装载该状态，并在接受下一 Turn 前完成或降级。维护调用始终由 Runtime
触发，Host 不能直接启动 Provider 调用。

Maintenance Identity：

```text
thread_id
source_window_id
authority_digest
narrative_input_digest
narrative_input_ref
route_digest
```

重启后：

- `prepared` 或 `generating_narrative` 状态可按稳定 Identity 重试；
- 已进入 `rebasing` 且进程内仍持有 Artifact 时只重试提交，不重复采样；
- 已有相同 Artifact Digest 时幂等完成；
- Window 或 Authority 已变化时拒绝旧结果并记录失败原因；
- Context Rebase 先于完成事件投影；投影失败不回滚已提交 Context。

多代压缩时，旧 Narrative Item 只能：

1. 在仍有预算且 Source Digest 有效时原样携带；
2. 根据持久化 Source Message 重新生成；
3. 因过期、冲突或低相关性被明确淘汰。

禁止把旧 Narrative Text 作为新 Narrative 的唯一 Source。若原始 Source 已按 Retention
删除，则 Item 可在 TTL 内原样保留并标记 `source_unavailable`，到期后删除。

### 8.14 Provider Projection 与 Prompt Cache

Provider Request 仍由 `ContextLedger` 的同一个 Snapshot 生成：

```text
Stable      = Base System + Repository Rules + Constitution
History     = Truth + Narrative + Raw Tail
Dynamic     = current World State + Budget/Convergence feedback
Continuation= incomplete output or queued steering
Definitions = frozen Tool Catalog
```

Compaction 后 Stable Prefix Digest 不变，Provider 可以继续命中 System Prompt Cache；只需
写入新的 History Prefix。若 Adapter 支持 Native Compaction，可以把内部
`CompactedContext` 投影为 Native Block，但 Usage、Logical Digest 和 Host Receipt
仍以 Runtime Snapshot 为准。

Compaction 调用本身的 Usage 必须单独记录：

```text
usage.kind = context_compaction
input/output tokens
cache read/write tokens
latency
provider/model/route digest
```

业务 Sample Usage 与 Compaction Usage 不得相互覆盖。Cost Budget 可以配置是否允许
Narrative；预算不足只关闭 Narrative，不跳过结构化压缩。

### 8.15 Runtime Event 与 Host 体验

扩展现有 `turn.compaction` Lifecycle，而不是让 Host 猜测状态：

```go
type TurnCompactionData struct {
    CompactionID     string
    Status           string // started | summarizing | rebasing | completed | fallback
    Mode             string // structural | inline | post_turn
    SourceWindowID   string
    TargetWindowID   string
    OriginalTokens   uint64
    RetainedTokens   uint64
    TruthBytes       int
    NarrativeBytes   int
    TailTokens       uint64
    AuthorityDigest  string
    FallbackReason   string
    ElapsedMS        uint64
}
```

Host 在 `started` 后显示“Compacting conversation”，在 `completed` 或 `fallback` 后恢复
普通 Turn 展示。Provider 通常不返回真实百分比，因此 UI 应优先显示 Stage 和 Elapsed；
如需要百分比，只能使用明确标注的 Stage-based Progress，不能伪装成模型生成进度。

TUI、Web、CLI JSON 和 Web Transport 必须投影同一 Runtime Event。关闭 `inline` 时不应出现
阻塞式 UI；`post_turn` Job 只通过非阻塞状态或 Receipt 展示。

## 9. Workspace-aware Context Checkpoint

### 9.1 拆分 Context 与 Accounting

当前 Session Delta 同时携带 Context State 和每 Turn Accounting Payload。优化后拆成：

```go
type ContextSnapshot struct {
    Epoch        uint64
    Revision     uint64
    History      HistoryManifest
    WorkingSet   agentcontext.WorkingSetDelta
    Evidence     evidence.Delta
    Failures     compact.FailureDelta
    Plan         *interact.Plan
    World        agentcontext.WorldBaseline
    Compaction   agentcontext.Compaction
    Workspace    WorkspaceBinding
}

type AccountingDelta struct {
    TurnID         string
    Usage          provider.Usage
    CostMicrounits uint64
}
```

Checkpoint 保存 `ContextSnapshot` 和 Profile Snapshot，但不保存可回退的累计
Accounting。Restore 不能通过回退 Usage/Cost 绕过 Budget。`ContextSnapshot` 中的
Workspace Binding 是对当时 Workspace 的声明，不是文件快照，也不自动回滚文件。

### 9.2 Restore 语义

Restore 必须原子执行：

1. 校验 Session、Thread、Checkpoint、Profile Revision 和 Context Digest；
2. 确认 Session Quiescent；
3. 创建新的 State Epoch，Revision 单调递增；
4. 恢复 History、Context-only Working Set、Failures 和 Plan；
5. 比较 Checkpoint Workspace Binding 与当前 Workspace；
6. 对匹配的 Workspace-dependent Evidence 保持 Authority，对不匹配项清除
   `verified` 并标记 `stale`，同时重写 History 中对应的结构化 Truth Capsule；
7. 校验 World Baseline，不匹配时清空并在下一 Sample Full Project；
8. 创建新的 Token Window，不能复用旧 Provider Observation；
9. 通过 Persistence Owner 原子提交 Restore Fact、Context Manifest、Reconciliation
   Receipt 和 Outbox；
10. 事务成功后才替换 Engine 内存；
11. 永不执行历史 Tool，也不修改 Workspace。

Restore 返回 `exact_context=true` 只表示 Context-only State 与 Checkpoint 相同；只有全部
Bound Path 仍匹配时才能返回 `workspace_claims_valid=true`。Host 必须分别展示这两个
事实。返回成功前，新的 Context Manifest 必须成为 `context_current`；Restore/Fork
Event 必须引用其 Commit ID、Digest、Revision 和 Epoch，不能依赖后续 Turn 补写。

### 9.3 Fork 语义

Checkpoint Fork 必须从 Checkpoint 的 Context Snapshot 构造新 Engine，不能先复制当前
Parent Engine。支持两种显式模式：

- `shared_workspace`：默认模式。新 Thread 读取当前共享 Workspace，并按 Restore 规则使
  旧 Workspace Evidence 失效或重新验证；
- `isolated_worktree`：调用现有受治理的 Worktree/Journal 能力构造隔离 Workspace。只有
  能证明文件 Baseline 与 Checkpoint Binding 一致时，才保留旧 Workspace Authority。

新 Thread 获得：

- 独立 State Epoch 和 Window ID；
- Checkpoint 时点的 Context-only Working Set、Failures 和 Plan，以及经过 Reconciliation
  的 Evidence；
- 显式 Parent Session/Thread/Checkpoint Lineage；
- 当前合法 Profile 的快照；
- 零共享可变账本。

Shared Workspace Fork 在一个 SQLite 事务中创建子 Thread 行和初始 Context Manifest
指针。即使子 Thread 尚无 Terminal Delta，重启恢复也必须从该当前 Manifest 构造
Session Delta，而不是退化为 History-only。

## 10. 低写放大的持久化

### 10.1 Live Projection、Owner Log 与 Audit 分离

只把 History 变成 Base/Tail 不足以解决写放大。Working Set、Evidence、Failures 和 Plan
也必须各自提供有界 Live Projection 与增量 Owner Delta：

```text
Owner Base Snapshot
    + bounded Delta Segment per committed Turn
    + periodic Owner Compaction
    -> current bounded Live Projection

Audit Event/CAS
    -> append-only or retention-managed historical evidence
```

Live Projection 受第 7 节的 Admission 和 Retention 约束；Audit History 不进入模型预算，
按独立 Retention 保留。正常恢复读取最新 Manifest 指向的 Base + Delta，不扫描全部 Event；
Event Reconstruction 只用于审计和灾难恢复。

### 10.2 Context Manifest

以小型 Manifest 替换 Terminal Envelope 中重复的完整 Session Snapshot：

```go
type ContextManifest struct {
    Version       int
    ThreadID      protocol.ThreadID
    TurnID        protocol.TurnID
    Epoch         uint64
    BaseRevision  uint64
    Revision      uint64
    History       HistoryManifest
    Working       OwnerManifest
    Evidence      OwnerManifest
    Failures      OwnerManifest
    Plan          OwnerManifest
    Workspace     WorkspaceBinding
    World         agentcontext.WorldBaseline
    Window        agentcontext.WindowLedger
    Digest        string
}

type HistoryManifest struct {
    BaseRef  ContentRef
    TailRefs []ContentRef
    Digest   string
}

type OwnerManifest struct {
    BaseRef   ContentRef
    DeltaRefs []ContentRef
    Digest    string
}
```

History Base 是最近 Compaction/Checkpoint/Fork 的 Replacement History；之后每个完成
Turn 只追加一个完整、Tool-paired Tail Segment。下一次 Compaction 创建新 Base 并清空
TailRefs。其他 Owner 同样只追加当前 Turn 的 Canonical Delta；达到 Delta 数量或字节阈值
时创建新 Base。不得每 Turn 把完整 Evidence Snapshot 重新编码到一个新 CAS Blob。

### 10.3 原子性

CAS Payload 在 SQLite 事务前暂存。事务负责：

- 插入 Manifest；
- 提交所有 Content Ref 的可达性与 Owner；
- 提交 Terminal Facts、Operation Receipt 和 Outbox；
- 标记 Manifest 为当前 Revision。

事务失败时暂存 Blob 保持不可达，由可重复 GC 清理。启动恢复只接受 Digest、Epoch 和
Revision 链全部有效的最新 Manifest。不得依赖跨 SQLite 与文件系统 CAS 的同步引用计数
回滚来声称原子性。

### 10.4 复杂度目标

在所有 Live Projection 都已受约束后，目标复杂度为：

```text
model-visible live context              O(1), hard bounded
physical live-state write per Turn      O(1), hard bounded
normal restore work                     O(base + bounded deltas)
retained audit storage after N Turns    O(N), retention controlled
```

“总物理存储恒定”不是目标，也与保留审计事实冲突。Benchmark 必须分别报告 Live State、
Audit、CAS Orphan 和 SQLite WAL 字节。

### 10.5 格式变更策略

目标格式应有显式 `Version`。当前项目仍处于预稳定阶段，默认采用一次性格式切换和清晰
的 Unsupported Schema 错误，不自动增加 Legacy 双写。只有发布兼容要求明确存在时，
才实现离线迁移工具，并用真实旧状态 Fixture 验证。

## 11. 用户 Memory v2

### 11.1 数据模型

```go
type MemoryRecord struct {
    ID        string
    Scope     string // user | workspace | repository
    Category  string // preference | convention | fact
    Text      string
    Source    string
    CreatedAt time.Time
    UpdatedAt time.Time
    ExpiresAt *time.Time
    Digest    string
}
```

记录必须有稳定 ID 和 Canonical Scope。Memory 仍是用户数据，不是 Constitution。

### 11.2 工具与治理

提供受 Guard 管理的：

- `remember`：创建或按 Digest 去重；
- `memory_list`：列出 Metadata，不默认返回全部正文；
- `memory_update`：按 ID 更新；
- `forget`：按 ID 删除；
- `memory_get`：读取选定正文。

每个工具都声明 Memory Resource、Access Mode 和审计 Receipt。Secret 检测继续作为预防
层，但文档必须明确它不是完整 DLP。

### 11.3 检索和刷新

Turn Admission 冻结一个 Memory Generation，并按以下顺序检索：

```text
explicitly pinned record
-> exact workspace/repository scope
-> lexical relevance
-> optional semantic rerank
-> recency and stable ID tie-break
```

首先实现确定性 Lexical Retrieval；Semantic Rerank 只有在离线质量评测证明有效后才
启用。Memory Tool 写入后增加 Generation，当前 Scope 不变，下一 Turn 自动读取新快照，
无需重启 Runtime。

`workspace` 和 `repository` Scope 必须复用 Session Lifecycle 的 Canonical Workspace
Identity，不得自行用未解析路径字符串做隔离。Repository Scope 还需要稳定 Repository
Identity；Git Worktree 可以共享 Repository Scope，但 Workspace Scope 必须隔离。无法
证明 Identity 时只允许 User Scope 或拒绝写入，不能猜测归属。

## 12. 配置与协议

建议新增配置全部默认关闭或保持当前行为：

```toml
[context.compact]
prepare_tokens = 0 # 0 表示 Model Hard Limit 的 55%
auto_compact_tokens = 0 # 0 表示 Model Hard Limit 的 65%
emergency_tokens = 0 # 0 表示 Model Hard Limit 的 85%
truth_max_bytes = 5632
truth_max_entities = 256
mandatory_max_entities = 128
fact_max_entities = 96
verified_change_retention_turns = 32
recent_tail_turns = 2
recent_tail_max_tokens = 8192
semantic_narrative = "off" # off | post_turn | inline
semantic_narrative_max_input_tokens = 4096
semantic_narrative_max_output_tokens = 512
semantic_narrative_max_items = 32
semantic_narrative_item_max_bytes = 512
semantic_narrative_timeout = "30s"
semantic_narrative_retry_limit = 1
owner_delta_max_segments = 16
owner_delta_max_bytes = 65536

[memory]
enabled = false
max_candidates = 32
max_prompt_bytes = 16384
semantic_rerank = false
```

`recent_tail_turns` 表示优先保留的最近 Turn 数，`recent_tail_max_tokens` 则是原始
tail 的硬上限；当两者冲突时以 token 上限为准，并在终态提交 deterministic rebase。

协议和 Receipt 需要增加：

- Truth Retention：按 Class 的候选、保留、淘汰、外置数量；
- Narrative：Requested、Generated、Accepted、Discarded、Stale、Failure Reason；
- Context Manifest：Epoch、Revision、Base/Tail Count、Logical/Physical Bytes；
- Checkpoint：Context Digest 和 State Epoch；
- Memory：Generation、Candidate Count、Selected IDs、Truncation；
- Compaction：Mandatory Bytes、Optional Bytes、Omission Count。
- Admission：Allowed、Projected Mandatory Bytes、拒绝原因和 Required Actions；
- Workspace Reconciliation：Binding Match、Invalidated、Revalidated 和 Stale 数量；
- Persistence：按 Live/Audit/Orphan 分类的 Logical/Physical Bytes。

新增 Event Kind 或字段必须通过 Runtime Protocol Generator 更新 Go、Schema 和 TypeScript，
Host 只能展示这些 Runtime-owned Receipt。

## 13. Failure Policy

| 故障 | 处理 |
| --- | --- |
| Mandatory Truth 无法容纳 | `resource_exhausted`，保留原历史 |
| 新 Mandatory State 无法通过 Admission | 在提交状态前拒绝，并返回可执行的收敛建议 |
| Refreshable Entity 超预算 | 确定性淘汰并记录 Omission |
| Narrative Provider/解析失败 | 丢弃 Narrative，业务 Turn 不变 |
| Inline Narrative 没有足够预算 | 跳过 Narrative，直接提交 Truth + Tail |
| Narrative Artifact 过期 | 忽略并记录 Stale |
| Context Rebase Commit 失败 | 禁止继续采样，进入 `resume_turn` 恢复 |
| Context Manifest/CAS 提交失败 | Terminal Commit 失败并幂等重试 |
| Checkpoint Context 校验失败 | 拒绝 Restore/Fork |
| Checkpoint Workspace Binding 不匹配 | 恢复 Context-only State，使文件相关 Authority 失效并重新投影 |
| World Baseline 不匹配 | 清空 Baseline，下一 Sample Full Project |
| Memory 检索失败 | 默认跳过并暴露 Context Receipt；写操作失败则 Tool 失败 |
| Secret/Restricted Memory | 拒绝写入，不持久化 Payload |

## 14. 实施阶段

### Code Ownership 与 Work Package

| 工作项 | 主要 Owner 路径 | 约束 |
| --- | --- | --- |
| Entity Class、Retention Planner、Omission | `internal/runtime/agent/context` | 纯确定性逻辑，不调用 Provider 或 Persistence |
| Mandatory Admission、Workspace Reconciliation | `internal/runtime/agent` | 使用 Canonical Projection；不在 Host 推断容量或有效性 |
| Context Snapshot、Epoch、Manifest Codec | `internal/runtime/agent/context` | 只定义 Durable Contract 和 Canonical Codec |
| Compaction Gate、Narrative Effect、Scope 状态 | `internal/runtime/agent/engine` | 所有 Sample 继续经过 Turn Coordinator |
| Terminal、Restore、Fork、Maintenance Operation | `internal/runtime/app` | 保持 Commit-before-apply 和 Runtime Authority |
| Rebase/Restore 单一提交入口 | `internal/runtime/app/persistence` | 一个 Envelope、一个 SQLite 事务、一个 Commit Result |
| CAS Ref、Manifest、Checkpoint Repository | `internal/persist` | CAS 先 Stage；SQLite 提交可达性；Orphan 由 GC 清理 |
| Narrative Provider Request | `internal/adapter/provider` | 继续经过 Provider Router，不增加旁路 Client |
| Memory Record Store 与 Tools | `internal/adapter/memory`、`internal/adapter/tool/memory` | 所有写操作经过 Guard |
| 默认值、校验、环境变量 | `internal/config` | 未知字段失败关闭，新增能力默认关闭 |
| Event、Receipt、Schema | `internal/runtime/protocol` | 通过 Generator 同步 Go、JSON Schema、TypeScript |
| 具体构造与 Feature Gate | `internal/runtime/app/wire` | 只负责 Wiring，不承载业务循环 |
| Host 展示 | `internal/host`、`web` | 只投影 Runtime Receipt，不推断 Context 事实 |

推荐把每个 Phase 拆成 Contract、Implementation、Recovery、Observability 和 Gate 五组
提交。不得在同一个提交中同时改变 Durable Format、Compaction Policy 和 Host 展示，
否则失败时无法判断是状态契约、压缩质量还是 Projection 回归。

### Phase 0：基线与观测

- 冻结当前 Context Digest、Compaction Receipt 和长期 Session Storage Benchmark；
- 增加 Truth Entity 数量、Mandatory Bytes、Session Delta Logical/Physical Bytes；
- 增加 Checkpoint Restore 前后账本 Digest；
- 建立 30、120、480 Turn 的 Hermetic Fixture。

退出条件：只增加观测，不改变 Model-visible Context；现有 Golden 无非预期漂移。

### Phase 1：有界 Truth Retention

- 增加 Mandatory Admission、Retention Class 和 Planner；
- 各账本实现当前 Entity Snapshot，不再永久并集；
- 增加 CAS Handle 外置和 Omission Receipt；
- 增加 Model Downshift Admission 和 Mandatory Saturation Fixture；
- 保持 Semantic Narrative 关闭。

退出条件：480 Turn 下 Capsule 始终满足上界；无法容纳的新 Mandatory State 在提交前被
拒绝；已接纳 Mandatory Entity 零丢失；同输入 Digest 完全一致。

### Phase 2：Owner Delta 与 Context Manifest

- 从 `sessiondelta` 提取 `ContextSnapshot` 与 `AccountingDelta`；
- 为 History、Working、Evidence、Failures 和 Plan 定义 Base/Delta Manifest；
- 使用 CAS Reference 替换重复 Snapshot Payload；
- 实现 Manifest Recovery、Owner Compaction、Orphan GC 和 Crash Point；
- 在明确需要前不增加 Legacy 双写。

退出条件：480 Turn Live State 每 Turn 物理新增写入有硬上界，Audit 总量为 O(N)；任意
提交 Crash Point 后恢复到旧 Revision 或新 Revision，不能出现混合状态。

### Phase 3：Workspace-consistent Checkpoint

- Checkpoint 保存 Context Snapshot、Workspace Binding 和 Profile；
- Restore/Fork 引入 State Epoch、Workspace Reconciliation 和原子 Commit；
- 支持显式 `shared_workspace`，将 `isolated_worktree` 作为独立能力门禁；
- 增加旧 History/旧 Evidence/新 Workspace 混合的回归测试。

退出条件：Context-only Ledger 与 Checkpoint 一致；全部 Workspace Claim 要么 Binding
有效，要么已失效/重验证；Usage/Cost 单调；Side Effect Replay 始终为零。

### Phase 4：可选 Semantic Narrative

- 定义 `CompactedContext`、`CompactionPlan`、Narrative Item 和 Artifact Codec；
- 正式接线 `PurposeSummary`，通过 Provider Router 执行无 Tool、低预算模型调用；
- 实现 Durable Narrative Input Artifact、`post_turn` Maintenance State、Source ID Validator、
  Staleness Fence 和确定性降级；
- 首版不启用 Inline；
- 扩展 `turn.compaction` Lifecycle，并让 TUI、CLI、Web Transport 和 Web 只投影 Runtime
  状态；
- Feature Flag 默认 `off`。

退出条件：关闭时 Digest 与 Phase 3 完全一致；开启后 Narrative 失败不改变 Turn 终态；
任何 Narrative Claim 都不能改变 Authority Digest；重启不丢失或重新猜测 Input。

### Phase 5：Inline Rebase

- 在 Post-turn 质量收益达到门槛后再引入 Inline Pause/Rebase；
- 为 Turn Kernel 增加 `EffectGenerateNarrative`、`EffectCommitContextRebase` 及完整
  Command/Domain Fact；
- 由 `app/persistence` 的单一接口原子提交 Rebase；
- 覆盖 Cancel、Steer、Shutdown、Timeout、Late Result 和 Duplicate Result。

退出条件：Mid-turn Rebase Crash 后恢复不能重复提交或回到旧 Window；关闭 Inline 时
State Machine 和 Digest 与 Phase 4 一致。

### Phase 6：用户 Memory v2

- 引入 Record Store、Scope、Generation 和 CRUD Tools；
- 先实现 Lexical Retrieval 与同 Runtime 下一 Turn 刷新；
- 完成 Secret、Symlink、并发写和删除测试；
- Semantic Rerank 保持实验开关。

退出条件：不同 Workspace 不泄漏记录；删除后下一 Turn 不再注入；同一 Scope 的选择顺序
确定；Memory 永远不能扩大 Permission。

## 15. 测试与评测

### 15.1 Qualification Track

必须覆盖：

- Unit：Retention 排序、Tombstone、Omission、Narrative Validator、Manifest Codec；
- Admission：Mandatory Saturation、Model Downshift、最坏 Canonical 编码和拒绝前无状态改变；
- Property/Fuzz：Entity 顺序、重复 ID、损坏 Digest、截断 UTF-8、未知 Schema；
- Engine：Pre/Mid/Post-turn Compaction、Provider Overflow、Model Downshift；
- Control：Compaction 中的 Cancel、Steer、Shutdown、Late Result 和 Duplicate Result；
- Persistence：每个 CAS/SQLite Crash Point、幂等重试、Ref 泄漏；
- Checkpoint：Restore/Fork 时间点一致性、Profile 冲突、Busy Session；
- Workspace：Binding Match/Mismatch、Checkpoint 后文件变化、Shared/Isolated Fork；
- Security：Prompt Injection、Secret Memory、跨 Workspace Scope、Tool Disabled；
- Race：并发 Memory CRUD、Manifest Recovery、Fork/Restore Fence；
- Protocol：Go/Schema/TypeScript Trait 和 Golden 同步；
- Hosts：CLI、TUI、Web 投影同一 Receipt。

标准验证：

```bash
go test ./internal/runtime/agent/context
go test ./internal/runtime/agent/context
go test ./internal/runtime/agent/context
go test ./internal/runtime/agent/engine
go test ./internal/runtime/app
go test ./internal/persist/...
go test -race -p 1 ./internal/runtime/agent/... ./internal/runtime/app/...
make docs-check
make book-check
git diff --check
```

### 15.2 Discovery Track

探索评测单独寻找未知退化：

- 随机生成长对话、重复 Compaction、Model Up/Downshift 和 Fork Graph；
- 注入相互冲突的 User、Tool 和 Memory 文本；
- 生成大量已验证 Change、Facts 和 Content Handles；
- 在 Narrative Job、CAS Stage、Terminal Commit 各阶段随机中断；
- 在 Checkpoint 后随机修改已验证文件，确认旧 Authority 不会静默存活；
- 比较结构化-only 与结构化+Narrative 的重复读取、错误文件修改和用户纠正率。

Discovery 结果必须报告真实新状态空间和发现的问题，不能用 Qualification 全绿替代。

### 15.3 质量指标

核心指标：

```text
mandatory_fact_loss = 0
mandatory_admission_overflow_after_commit = 0
authority_digest_mismatch = 0
checkpoint_context_mismatch = 0
stale_workspace_claim_survival = 0
side_effect_replay = 0
context_resource_exhausted_rate
truth_bytes / context_window
live_physical_bytes_written / live_logical_new_bytes
audit_physical_bytes_written / committed_turn
cas_orphan_bytes_after_gc
narrative_accept / discard / stale rate
repeated_read_rate
repeated_equivalent_tool_call_rate
user_correction_rate
```

Token 降低不是单独成功标准。必须同时观察正确完成率、验证覆盖、错误编辑和重复工作。

## 16. Rollout 与回滚

1. Phase 0 指标先发布，不改变行为；
2. Admission 与 Bounded Truth 先以 Shadow Receipt 计算新旧结果，不改变实际 Context；
3. Owner Manifest 先双读单写仅限明确迁移窗口，稳定后删除旧读取路径；
4. Checkpoint v2 在专用 Format Version 下启用，并先以 Reconciliation Receipt 观察；
5. Narrative 默认关闭，按 Workspace/Profile 灰度；
6. Memory v2 导入必须显式执行，不能静默复制现有敏感文件。

每个 Phase 必须有独立 Feature Gate 或 Format Boundary。回滚时关闭该 Phase，不允许通过
回退到 LLM-only Summary 绕过 Truth Capsule。

## 17. 风险与缓解

| 风险 | 缓解 |
| --- | --- |
| Retention Policy 丢失有价值但可刷新事实 | Omission Receipt、CAS Handle、Discovery 长会话评测 |
| Mandatory Admission 过早阻塞正常任务 | Shadow 模式、按 Kind 指标、可执行收敛建议、Model Route 升级 |
| Narrative 引入幻觉或 Prompt Injection | 非权威标记、无 Tool Job、Source ID、Guard 独立 |
| Manifest 增加恢复复杂度 | 单调 Epoch/Revision、Canonical Digest、Crash Matrix |
| Checkpoint v2 被误解为 Workspace 回滚 | API 和 UI 明确 `side_effects_replayed=false` |
| 旧 Workspace Evidence 被误当作当前事实 | Workspace Binding、失效门禁、Reconciliation Receipt |
| Memory Scope 配置错误导致泄漏 | Canonical Workspace Identity、默认 User Memory 关闭 |
| 方案范围过大 | 严格按 Phase 交付，每阶段可独立停止 |

## 18. 最终验收标准

方案完成必须同时满足：

1. 480 Turn Session 中 Truth Capsule、Model Context 和每 Turn Live State 物理新增写入均有
   明确上界，Audit 存储按 Retention 保持 O(N)；
2. 会扩大 Mandatory State 的 Transition 先通过 Admission；所有已接纳 Mandatory Entity
   在重复压缩、重启和允许的 Model Downshift 后保持 Authority 等价；
3. Checkpoint Restore/Fork 不混入 Checkpoint 之后的 Context-only State，且旧 Workspace
   Claim 不会在 Binding 失效时继续保持 Authority；
4. Usage、Cost、Permission 和 Workspace Side Effect 不可通过 Restore 回退；
5. Narrative 关闭时保持确定性；开启时任何故障都不改变业务终态；
6. `post_turn` Narrative 具有可恢复的 Durable Input；`inline` 模式可以暂停、由单一
   Persistence Owner 原子 Rebase 并继续原 Turn，Cancel/Restart 不产生重复
   Compaction；
7. 用户 Memory 可按 Scope 查询、更新、删除，并在下一 Turn 生效；
8. 所有 Context 省略、降级和失效均可从 Receipt 解释；
9. Hermetic、Race、Architecture、Protocol、Web、Docs 和 Book 门禁全部通过。
