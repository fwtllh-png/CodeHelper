# Context Engineering 架构升级方案

简体中文 | [English](../en/context-engineering-architecture-upgrade.md)

> 状态：`proposed`。
>
> 基线：CodeHelper `f1b1ec1955f4a64149268cfe3bf91b00a0f05d06`；
> Codex 参考实现 `3bbf1fe75701c97fb190e0867002ba2d9dbda5db`。
>
> 范围：模型可见 Context 的构造、持久化、Diff、Token 核算、Tool Result
> 准入、Compaction、恢复、Provider 投影与验证。本文不改变 Host、Guard、
> Approval、Journal 或 Sandbox 的所有权。

实施进度：

- CE0：`accepted`，证据见
  [`context-engineering-ce0-evidence.json`](../context-engineering-ce0-evidence.json)；
- CE1：`accepted`，证据见
  [`context-engineering-ce1-evidence.json`](../context-engineering-ce1-evidence.json)；
- CE2：`accepted`，证据见
  [`context-engineering-ce2-evidence.json`](../context-engineering-ce2-evidence.json)；
- CE3：`accepted`，证据见
  [`context-engineering-ce3-evidence.json`](../context-engineering-ce3-evidence.json)；
- CE4：`accepted`，证据见
  [`context-engineering-ce4-evidence.json`](../context-engineering-ce4-evidence.json)；
- CE5：`accepted`，证据见
  [`context-engineering-ce5-evidence.json`](../context-engineering-ce5-evidence.json)；
- CE6：`accepted`，证据见
  [`context-engineering-ce6-evidence.json`](../context-engineering-ce6-evidence.json)；
- CE7：尚未开始。

## 1. 执行摘要

CodeHelper 已具备 Token-native Window、Tool Result Surface Pruning、稳定 Tool
Catalog、确定性 Compaction Summary、Provider Replay 隔离和逐 Sample Token
归因。当前主要问题不是缺少某个局部优化，而是模型可见 Context 的 Authority 分散在：

- `promptcontext.Context` 和 `TurnContextSnapshot`；
- Engine durable history；
- Turn Scope 中的 World State、Catalog 和 Receipt；
- `SessionDelta.History`；
- Event Log 重建；
- Provider-private Replay State。

这些组件各自正确，但没有一个对象能够完整回答：

> 某次 Sample 为什么看到了这些内容、这些内容基于哪个持久化版本、哪些内容可在下一次
> Sample 中只追加 Diff，以及 Compaction、Rollback 或 Restart 后哪个 Baseline 仍然
> 有效？

本方案引入一个 Runtime-owned、版本化、持久化的 `ContextLedger`，作为模型可见
Context 的唯一 Authority。所有 Prompt Partition、Conversation Item、World State
Patch、Tool Result Surface 和 Compaction Checkpoint 都先进入 Ledger，再由 Provider
Adapter 投影为具体协议请求。

目标不是照搬 Codex，也不是用模型摘要替换 CodeHelper 的确定性事实。目标组合是：

1. 学习 Codex 的统一 Transcript、Typed Fragment、World State Full/Patch、History
   Normalization 和真实窗口 Baseline；
2. 保留 CodeHelper 的确定性 Truth Summary、Content Store Handle、细粒度 Token
   Attribution、显式 Adapter Capability 和 Durable Turn Kernel；
3. 删除现有多路 Context Authority，不长期双写或维护第二条执行路径。

## 2. 当前实现机制

### 2.1 Turn 冻结

`internal/runtime/agent/engine/turncontext.go` 在 Turn 开始前冻结 `TurnSpec`，包括：

- Session、Turn 和 Profile Revision；
- Route、Provider、Model 和 Reasoning Capability；
- Policy、Mode、Posture 和 Sandbox Identity；
- Prompt Context、Tool Catalog、Skills、MCP 和 Extension Snapshot；
- Step、Output、Token 和 Cost Budget。

该设计保证运行中的 Turn 不会重新读取可变 Profile 或 Tool Registry，是后续统一
Context Ledger 必须保留的边界。

### 2.2 Sample 组装

`internal/runtime/agent/engine/model_handler.go` 将模型输入划分为：

```text
stable context
  + durable history
  + dynamic turn context
  + output continuation
  + provider tool definitions
```

`promptcontext.MeasureSample` 分别统计 Stable、User、Assistant、Tool、Dynamic、
Continuation 和 Tool Definition Token，并记录 Logical Request 与 Transport Payload
Digest。

### 2.3 World State Diff

Repo Map、Working Set、Evidence 和 Plan 通过 Receipt Digest 判断变化。在同一个 Turn
中，第一次 Sample 将完整 World Context 放入 Stable Prefix，后续 Sample 将变化追加到
History。

限制是该 Baseline 存在于 Turn Scope。新 Turn、Restart、Rollback 或 History
Replacement 后，Runtime 没有一个持久化的 Typed World State Baseline 可以继续计算
Patch。

### 2.4 Token Window

当前 Gate：

- 使用 Provider Model Context Limit 作为硬上限；
- 默认在 65% 自动 Compaction；
- 55% 注入收敛提示；
- 85% 进入 Finish-only；
- 用上一 Sample 的实际 Input Usage 校准当前估算；
- 支持 `total` 与 `body_after_prefix` 两种 Scope。

`body_after_prefix` 当前通过“总估算减 Stable 估算”计算，不是服务端观测到的当前
Compaction Window Prefill。

### 2.5 Tool Result 与 Compaction

Tool Layer 将大结果写入 Content Store，并返回 `result_get` Handle。Window Gate 在
Summary Replacement 前从旧到新裁剪已关闭的 Tool Result Surface；如果裁剪已经恢复
窗口，则跳过 Summary。

Summary 由 Plan、Failure、Change、Critical Path、Evidence Fact 和被删除消息的
Digest 确定性生成。该方案不会让模型伪造“已验证”事实，但多次 Compaction 会整体
Carry 旧 Summary，且 Transcript Digest 只能保留每条消息的有限平铺文本。

### 2.6 持久化与恢复

`SessionDelta` 原子提交 History、Usage、Cost、Working Set、Evidence、Failure、
Compaction 和 Plan。Event Log 可从最新 Compaction、Fork 或 Checkpoint 开始重建
Model-visible History，并过滤失败 Turn 和未闭合 Tool Pair。

当前 Delta 没有持久化 Typed Context Baseline、History Revision、Window Prefill 或
每个 Context Item 的稳定 Identity。

## 3. Codex 中值得学习的机制

本节只描述可迁移的设计原则，不把 Codex 的 OpenAI-specific 行为当成 CodeHelper 的
目标协议。

### 3.1 单一 ContextManager

Codex `codex-rs/core/src/context_manager/history.rs` 统一拥有：

- Copy-on-write Response Item Transcript；
- History Version；
- Provider Token Usage；
- Turn Context Reference Baseline；
- World State Baseline；
- Prompt 前的 Normalization。

任何 Compaction 或 Rollback Rewrite 都增加 History Version，并使不再可信的 Baseline
失效。

### 3.2 Typed Contextual Fragment

Codex 的每种注入内容都由 Struct 实现统一 Fragment Contract，显式声明：

- Role；
- Marker；
- Body；
- 是否必须使用独立 Message；
- 如何识别历史中的同类 Fragment。

这让 Context Filtering、Rollback 和 Compaction 不需要猜测任意文本是否属于 Runtime
注入。

### 3.3 World State Full/Patch

新窗口写入 Full Snapshot；后续 State Change 计算 Typed Diff，并按以下顺序提交：

1. 生成 Model-visible Fragment；
2. 将 Fragment 写入 Conversation History；
3. 持久化 World State Patch；
4. 推进 Baseline。

因此 Baseline 不会领先于模型实际看到的 Context。

### 3.4 History Normalization

发送请求前统一执行：

- 为缺失的 Tool Call 补充失败 Output；
- 删除 Orphan Output；
- 保持 Call/Output Pair；
- 根据模型 Input Modality 移除不支持的 Image 或 Audio；
- 在 Tool Output 进入 History 时执行 Token-based Truncation。

### 3.5 Compaction Window Baseline

Codex 为每个 Auto-compaction Window 维护 ID、Window Number 和 Prefill Input Tokens。
收到 Provider Usage 后，Server-observed Prefill 会替换恢复时的估算值。

### 3.6 严格的增量传输

Codex 只在以下条件同时满足时复用 WebSocket `previous_response_id`：

- 所有非 Input Request Property 相等；
- 当前 Input 严格扩展上一 Request Input 与上一 Response Items；
- Response ID 有效；
- Turn-scoped Sticky Routing State 没有跨 Turn 泄漏。

该机制压缩 Transport，不改变完整 Logical Transcript。

## 4. 差距与根因

| 优先级 | 差距 | 根因 | 可观察影响 |
| --- | --- | --- | --- |
| P0 | Context Authority 分散 | Stable、History、Scope 和 Receipt 分别维护状态 | Restart 与在线路径可能使用不同 Baseline |
| P0 | World State Baseline 不跨 Turn 持久化 | Digest Skip 是 Turn-local | 新 Turn 重发 Full State，Prefix 增长或变化 |
| P0 | History Rewrite 缺少统一 Revision | Replace、Compact、Fork 各自处理 | 派生 Cache、Review Cursor 或 Diff 可能引用旧历史 |
| P1 | Fragment Contract 不完整 | 只有 Skills 和 Constitution 有 Marker | Plan、Policy、Evidence 等无法统一识别和失效 |
| P1 | Window Prefill 主要依赖估算 | 没有 Server-observed Window Ledger | `body_after_prefix` 可能过早或过晚 Compaction |
| P1 | Tool Output Policy 不是统一准入层 | Result Store 与 Window Pruning 分属不同阶段 | 大结果可先污染多个 Sample，再被压力裁剪 |
| P1 | Normalization 分散 | Engine、恢复和 Provider 分别检查 Pair | 新协议或恢复路径容易遗漏边界条件 |
| P2 | 多次 Summary Carry 不做结构化合并 | Summary 是有 Marker 的文本块 | 旧 Summary 挤占新事实，信息逐代衰减 |
| P2 | 无 Compaction Compatibility | Window 只看 Token Limit | Model Downshift 或 Prompt Contract 变化后才被动失败 |
| P2 | 多模态成本缺少统一估算 | 通用估算主要针对文本 | Image、Audio、Encrypted Content 归因不稳定 |

## 5. 设计原则

1. **一个 Authority**：只有 `ContextLedger` 可以改变 Model-visible Context。
2. **Append by default**：正常 Sample 只 Append；Rewrite 仅允许 Compact、Rollback、
   Restore、Fork 和显式 Migration。
3. **Rewrite increments revision**：任何 Rewrite 都推进 Revision 并使派生 Baseline
   失效。
4. **Typed before rendered**：Context 先以 Typed Item 存储，最后才投影为文本或
   Provider Item。
5. **Persist after visibility**：模型可见 Item 成功写入 Ledger 后才能推进 World State
   Baseline。
6. **Admission before retention**：大小、Capability 和 Pairing 在 Item 进入 Ledger 前
   处理。
7. **Truth is deterministic**：Goal、Change、Failure、Verification 等事实只来自
   Runtime Ledger。
8. **Transport is not authority**：`previous_response_id`、Connection 和 Sticky Header
   不能进入 Runtime Context Authority。
9. **Provider capability is explicit**：不根据 Provider 名称猜测 Incremental、
   Modality 或 Remote Compaction。
10. **No permanent dual path**：阶段内可使用 Characterization Adapter，阶段结束必须
    删除被替代的 Authority。

## 6. 目标架构

```text
TurnSpec
  |
  v
Context Contributors -----> Typed Context Items
  |                               |
  |                               v
  +-----------------------> ContextLedger
                                  |
                 +----------------+----------------+
                 |                |                |
                 v                v                v
          Token Window      Compaction       Durable Delta
                 |                |                |
                 +----------------+----------------+
                                  |
                                  v
                         Provider Projection
                                  |
                         Logical Request Digest
                                  |
                     HTTP/SSE or verified incremental
```

### 6.1 ContextItem

建议的逻辑结构：

```go
type ContextItem struct {
    ID         string
    Kind       Kind
    Role       provider.Role
    Source     Source
    Lifetime   Lifetime
    Turn       uint64
    Revision   uint64
    WorldKey   string
    Content    Content
    Digest     string
    TokenCost  TokenCost
    Provenance Provenance
}
```

要求：

- `ID` 在 Persist、Replay 和 Provider Projection 中稳定；
- `Kind` 必须是枚举，禁止新增 Stringly Typed 分支；
- `Lifetime` 至少区分 Session Prefix、Window State、Turn Input、Conversation、
  Ephemeral Feedback；
- `WorldKey` 标识可被后续 Patch 替代的状态；
- `Content` 支持 Text、Image、Tool Call、Tool Result、Reasoning Replay 和 Handle；
- 每个 Item 的 Model-visible Surface 硬上限为 10,000 Token。

### 6.2 ContextLedger

Ledger 最少拥有：

```text
items
history_revision
world_state_baseline
turn_context_baseline
window_number
window_ids
window_prefill
provider_usage
normalization_receipt
```

核心操作：

```text
Append(items)
Snapshot(model_capabilities)
Replace(reason, items)
Rollback(turns)
Fork(checkpoint)
ApplyWorldState(snapshot)
Compact(policy)
RecordProviderUsage(usage)
```

`Snapshot` 是 Provider Sampling 的唯一输入；Provider Adapter 不再接收分别组装的
Stable、History 和 Dynamic Authority。

### 6.3 World State

首批迁移 Section：

1. Model、Mode 和 Reasoning Policy；
2. Permission、Sandbox 与 Execution Target；
3. AGENTS/Repository Instructions；
4. Tool Catalog、Materialized Tool 和 Deferred Tool 状态；
5. Repo Map；
6. Working Set；
7. Evidence、Risk 与 Reminder；
8. Plan；
9. Skills、MCP 和 Extension Health。

每个 Section 实现：

```text
StableID
Snapshot
RenderFull
Diff(previous)
RecognizeRetained
TokenLimit
```

Full 只允许在以下情况出现：

- 新 Session 或新 Compaction Window；
- Baseline 不存在；
- History Revision 不匹配；
- Rollback 删除了建立 Baseline 的 Item；
- Model Capability 或 Context Contract 不兼容。

### 6.4 Admission 与 Normalization

Item 写入前执行：

1. 验证 Role、Kind 和 Content；
2. 校验 Tool Call ID 唯一性；
3. 保证 Call/Result Pair；
4. 按 Model Capability 投影 Image、Audio 和 Reasoning；
5. 应用 Per-kind Token Budget；
6. 大 Tool Result 写入 Content Store；
7. 生成有界 Head/Tail Surface 与 Handle；
8. 记录 Original、Retained、Reason 和 Digest。

Window Pressure 下的 Tool Result Pruning 保留为第二道防线，但正常路径不应依赖它控制
单项大小。

### 6.5 Token Window Ledger

每个 Window 维护：

| 字段 | 含义 |
| --- | --- |
| `full_active_tokens` | Provider 最近报告的完整 Input Context |
| `prefill_tokens` | 当前 Window 首个 Request 的 Input Baseline |
| `body_tokens` | `full_active_tokens - prefill_tokens` |
| `tool_definition_tokens` | 当前 Tool Definitions 成本 |
| `pending_tokens` | Provider 尚未观测的新 Item 估算 |
| `output_reserve` | 当前 Reasoning/Finish Policy 的输出预留 |
| `auto_compact_limit` | 自动压缩阈值 |
| `hard_limit` | 模型硬 Context Window |

Provider Usage 优先，Estimator 只计算 Provider Usage 之后新增的 Item。不得再用一个
全局 Ratio 同时校准 Text、Tool Schema 和 Image。

### 6.6 Compaction

采用两层产物：

1. **Truth Capsule，权威**
   - Goal；
   - Open/Done Todo；
   - Changed Files 与 Verification State；
   - Failed Attempts；
   - Critical Facts 与 Source；
   - Pending Approval/Input；
   - Content Handles。
2. **Narrative Summary，可选、非权威**
   - 仅补充难以结构化的讨论与决策上下文；
   - 不能生成 Verified、Changed、Passed 等事实字段；
   - 失败、超预算或 Provider 不支持时直接省略。

重复 Compaction 对 Truth Capsule 按 Stable Entity ID 合并，不再整体 Carry 上一代
Summary。Narrative 可以重新生成，但不得覆盖 Truth。

### 6.7 Provider 投影

Provider Adapter 只负责：

- 将 Ledger Snapshot 转为 Chat、Responses 或 Messages 协议；
- 过滤同 Adapter 的合法 Replay State；
- 计算 Logical Request 与 Transport Payload Digest；
- 在 Capability 明确支持时尝试 Incremental Transport；
- 不满足严格扩展时回退完整请求。

DeepSeek 保持：

```text
incremental_responses = false
complete logical request
HTTP/SSE
server-side prompt cache
```

## 7. 核心收益与验证方式

收益必须由同一 Canonical Workload 的 Baseline/Candidate 配对结果证明。

| 收益 | 机制 | 主指标 | 验收门禁 |
| --- | --- | --- | --- |
| 降低高价格输入 | 跨 Turn World State Patch、稳定 Prefix | `uncached_input_tokens` P50 | 相对基线下降 >=20% |
| 降低累计输入 | 减少 Full Reinjection 与重复 Dynamic Tail | `input_tokens` P50 | 下降 >=15% |
| 提高 Cache 连续性 | Append-only Context 与稳定 Tool Contract | 第三次 Sample 后 `cached_share` | >=95%，且不低于基线 |
| 避免错误 Compaction | Server-observed Prefill | Context Rejection、Compaction Timing Error | Rejection=0，触发误差 <=5% |
| 提升恢复正确性 | 持久化 Baseline、Revision 和 Window | Restart Semantic Digest | 100% 匹配 |
| 提升 Tool History 正确性 | 统一 Normalization | Pairing/Orphan Count | Pairing=100%，Orphan=0 |
| 提升长会话质量 | Structured Truth Merge | 三次 Compaction Fact Retention | Critical Fact=100% |
| 降低单项爆炸风险 | Admission Token Cap | Max Item Tokens | 每项 <=10,000 |
| 提高可诊断性 | Item Receipt 与 Attribution | Unknown Token Share | <=2% |
| 降低维护成本 | 删除多路 Authority | Authority Count、重复实现数量 | Authority=1，无遗留双写路径 |

### 7.1 指标定义

```text
uncached_input_tokens = input_tokens - cached_tokens
cached_share = cached_tokens / input_tokens
context_estimation_error =
  abs(estimated_input_tokens - provider_input_tokens) / provider_input_tokens
world_state_full_rate =
  full_world_state_items / model_samples
unknown_token_share =
  unattributed_input_tokens / provider_input_tokens
```

Token 汇总不得将 Cached 重复加到 Input，也不得将 Reasoning 重复加到 Output。

### 7.2 Correctness Hard Gates

以下门禁失败时，不允许用 Token 收益覆盖：

- Tool Call/Result Pairing 100%；
- Restart、Fork、Rollback 和 Checkpoint Restore 后无 Stale World State；
- Failed Turn 不进入恢复后的 Model-visible History；
- Compaction 后 Pending Approval/Input 不被伪造为已解决；
- Truth Capsule 不生成没有 Runtime Evidence 的 Verification Claim；
- DeepSeek 不发送 `previous_response_id`；
- Provider Route 或 Adapter 改变后旧 Replay State 不可见；
- Guard、Approval、Journal 和 Sandbox 路径不变。

### 7.3 Quality Gates

Canonical Task 的结果质量必须同时满足：

- 任务完成率不低于 Baseline；
- Workspace Diff 与预期一致；
- Verification Pass Rate 不低于 Baseline；
- Sample Count P50 不增加；
- Tool Call Count P50 增幅不超过 5%；
- Provider Error 和 Repair Sample 不增加；
- P95 Turn Latency 增幅不超过 5%，除非 Token 降幅超过 30% 且报告明确说明。

## 8. 验证工作负载

至少覆盖以下 Scenario：

| Scenario | 验证重点 |
| --- | --- |
| Long read-only analysis | Stable Prefix、Cache 与无变更完成 |
| Multi-file edit and verify | Working Set、Evidence、Change Truth |
| Tool-heavy search | Result Admission、Handle 与 Pairing |
| Mid-turn world-state change | Typed Patch 的顺序与持久化 |
| Restart during model/tool wait | Baseline、Pending Effect 与 Replay |
| Rollback and fork | History Revision 与 Baseline Invalidation |
| Three consecutive compactions | Truth Retention 与摘要代际衰减 |
| Model switch/downshift | Compaction Compatibility |
| Image input | Modality Projection 与 Token Estimate |
| DeepSeek live | Full SSE、Cache Share、DSML Completion Correctness |

每个 Scenario 使用固定：

- Prompt Bytes；
- Workspace Fixture；
- Session Profile；
- Model 和 Provider；
- Tool Catalog Snapshot；
- Max Steps 与 Budget；
- Cache Arm。

## 9. 实验与证据协议

### 9.1 执行顺序

1. Baseline A/A 两批，确认测量噪声；
2. Candidate A/A 两批，确认实现稳定；
3. Baseline/Candidate 配对 Hermetic；
4. Cache-disabled Live Arm；
5. Cache-enabled Live Arm；
6. Restart、Rollback、Compaction Fault Injection；
7. 生成 Comparison 与 Gate Result。

### 9.2 样本量

- Hermetic：每个 Fixture 全量通过；
- Benchmark：每个 Arm 至少 10 次；
- Live：每个 Arm 至少 10 个成功样本；
- 如果 P50 差异小于 10%，增加到 20 个成功样本；
- 不得删除不利样本；仅可按预先定义的基础设施失败码剔除，并单独报告。

### 9.3 证据目录

```text
.tmp/context-engineering/
  baseline/
    config.json
    requests.ndjson
    usage.ndjson
    context-items.ndjson
    compactions.ndjson
    scenarios.json
  candidate/
    ...
  comparison.json
  gates.json
  report.md
```

提交阶段证据时，将稳定摘要复制到：

```text
docs/context-engineering-ceN-evidence.json
```

Evidence 至少包含 Commit、Dirty State、Model、Provider、Prompt Digest、Fixture Digest、
Sample Count、P50/P95、Gate Status 和失败原因。

### 9.4 防止基准污染

- Cache-enabled 与 Cache-disabled 使用不同 Stable Cache Key；
- Baseline 与 Candidate 使用不同 Cache Namespace；
- Warmup 不计入 Sample，但必须记录；
- Logical Request Digest 不同的样本不得配对；
- Retry、Repair 和 Continuation 必须作为独立 Sample 计数；
- Transport Byte Saving 不得声明为 Token Saving；
- 未知 Provider Pricing 不得推导 Cost Saving。

## 10. 分阶段实施

### CE0：Characterization 与 Baseline

交付：

- Canonical Model-visible Context Dump；
- Context Item/Partition Attribution；
- Restart、Rollback、Fork、Compaction Golden；
- Baseline Evidence。

退出条件：

- 当前行为可由 Fixture 重放；
- Token Attribution 覆盖率 >=98%；
- A/A P50 漂移 <=5%；
- 不修改生产行为。

### CE1：ContextLedger Skeleton

交付：

- `internal/runtime/agent/contextstore`；
- Typed `ContextItem`、Snapshot、Revision；
- 现有 Prompt/History 到 Ledger 的单向适配；
- Provider Request Golden 等价。

退出条件：

- 所有生产 Sample 只从一个 Snapshot 构造；
- Logical Request Digest 与 Baseline 一致；
- 不新增第二个 Provider 调用路径；
- 被替代的组装 Helper 同阶段删除。

### CE2：Typed World State

交付：

- Section Contract；
- Full/Patch Snapshot；
- Session Delta 中的 Baseline 和 Revision；
- Policy、Tools、Repo Map、Working Set、Evidence、Plan、Skills 迁移。

退出条件：

- 每 Window 最多一次 Full Injection；
- Unchanged State 产生零 Model-visible Item；
- Restart 后第一个 Sample 不无条件 Full Reinjection；
- Rollback 删除 Baseline 时自动回退 Full。

### CE3：Token Window Ledger

交付：

- Window ID、Number 和 Server-observed Prefill；
- Per-kind Estimator；
- Pending Item Cost；
- Full、Body、Tools、Reserve 分账。

退出条件：

- Text Estimator P95 Error <=5%；
- Multimodal P95 Error <=10%；
- Context Rejection=0；
- Compaction Trigger Error <=5%。

### CE4：Admission 与 Normalization

交付：

- 所有 Tool Output 的 Token-native Admission；
- Pair/Orphan Normalizer；
- Modality Projection；
- Content Store Handle 与 Receipt 统一。

退出条件：

- Pairing=100%，Orphan=0；
- 任一 Model-visible Item <=10,000 Token；
- 100 KiB Tool Result 的 Surface 至少减少 80%；
- 原始内容可通过 Handle 完整取回。

### CE5：Structured Compaction

交付：

- Stable Entity ID Truth Capsule；
- 多代 Merge；
- 可选 Narrative；
- Compatibility Hash 与 Model Downshift Policy。

退出条件：

- 连续三次 Compaction 后 Critical Fact Retention=100%；
- Summary 不包含虚构 Verification；
- Retained Tokens 低于 Target；
- Compaction Failure 不破坏原 History。

### CE6：Provider Projection 与 Cache

交付：

- Exhaustive Request Property Comparison；
- Stable Prefix Digest；
- Incremental Eligibility Receipt；
- Full Fallback Reason。

退出条件：

- 支持的 Provider 严格扩展时使用 Incremental；
- Property、Route、Retry、Compaction、Resume 变化时 Full Fallback；
- DeepSeek 始终 Full HTTP/SSE；
- Logical History 与 Transport Delta 等价。

### CE7：收敛与删除旧路径

交付：

- 删除旧 Stable/History/Dynamic Authority；
- 删除旧 Receipt-only Baseline；
- 文档、协议、监控与 Architecture Ratchet 更新；
- 最终 Evidence。

退出条件：

- Context Authority Count=1；
- 生产代码增长有明确所有权和测试证据，不保留被替代的重复路径；
- 无 Feature Flag 和长期双写；
- 所有 Hard Gate、Efficiency Gate 和 Architecture Gate 通过。

## 11. 测试矩阵

| 层级 | 必测内容 |
| --- | --- |
| Unit | Item Validation、Diff、Revision、Estimator、Admission、Merge |
| Property/Fuzz | UTF-8 Truncation、Pair Normalization、Patch Round-trip |
| Engine | Sample Layout、Window Gate、Compaction、Repair、Continuation |
| Persistence | Atomic Delta、Restart、Revision Conflict、Checkpoint |
| Provider | Chat/Responses/Messages Projection、Replay Filtering |
| Runtime | Pending Model、Tool、Approval、Input Recovery |
| Multi-Agent | Task Capsule、Fork、Child Budget、Result Merge |
| VS Code/ACP | Context Receipt、Replay、Compact、Fork 展示一致 |
| Live | DeepSeek Cache、Usage、SSE、DSML Tool Completion |

阶段验证至少运行：

```bash
go test ./internal/runtime/agent/...
go test ./internal/runtime/app/...
go test ./internal/adapter/provider/...
go test ./internal/adapter/tool/...
go test ./internal/persist/...
go test -race ./internal/runtime/agent/...
make architecture-ratchet
make token-bench
make docs-check
make book-check
git diff --check
```

涉及 VS Code Protocol 或 Receipt 时追加：

```bash
cd extensions/vscode
npm run check
npm test
```

## 12. 风险与控制

| 风险 | 控制 |
| --- | --- |
| Ledger 成为新的大型 God Object | Core 只保存状态；Contributor、Normalizer、Budgeter、Compactor 分包 |
| 迁移期出现双 Authority | 每阶段只允许单向 Adapter；退出前删除旧写路径 |
| Typed Item 增加协议体积 | Persist Snapshot/Patch，不重复存 Rendered Text 与 Typed Payload |
| 过早 Admission 丢失关键信息 | 原文进入 Content Store，Surface 保留 Head/Tail 与 Handle |
| Model Narrative 幻觉 | Narrative 非权威，Truth 字段只由 Runtime 生成 |
| Patch Baseline 漂移 | Revision 和 Baseline Digest 同时校验，不匹配时 Full Reinjection |
| Token 优化降低质量 | Correctness Gate 优先于 Efficiency Gate |
| Cache 实验互相预热 | Namespace 隔离和配对 Digest |
| Provider-specific 逻辑泄漏 | Capability 驱动，Adapter 只投影 |
| DeepSeek 被误开增量 | Capability Test 和 Live Request Dump 双门禁 |
| 代码量持续增长 | 每阶段报告代码趋势并删除替代路径，以所有权和逻辑闭包而非零增长为门禁 |

## 13. 非目标

本方案不包括：

- 修改 Guard、Approval、Constitution、Journal 或 Sandbox 顺序；
- 将 Provider Connection 或 Response ID 持久化为 Runtime Authority；
- 为 DeepSeek 模拟 `previous_response_id`；
- 用 Vector Database 替代 Durable History；
- 自动删除用户明确 Pin 的 Context；
- 仅为降低 Token 而减少必要 Verification；
- 保留旧 Context Engine 的长期兼容模式。

## 14. Definition of Done

只有同时满足以下条件，升级才算完成：

1. 所有 Model-visible Context 由一个版本化 Ledger 构造；
2. World State Full/Patch 可跨 Turn、Restart、Fork 和 Compaction 恢复；
3. 所有 Context Item Typed、有界、可归因；
4. Tool Call/Result Pairing 100%，Orphan=0；
5. 连续三次 Compaction 后 Runtime Truth 100% 保留；
6. DeepSeek 保持完整 HTTP/SSE，Replay 与 Adapter 隔离；
7. `uncached_input_tokens` P50 至少下降 20%；
8. `input_tokens` P50 至少下降 15%；
9. 第三次 Sample 后 Cache Share >=95%；
10. Task Completion、Verification、Sample Count 和 Error Rate 不回退；
11. Architecture Ratchet、Race、Hermetic、Live 和文档门禁全部通过；
12. 旧 Context Authority、Feature Flag 和双写路径全部删除；
13. 所有收益都有 Raw Artifact、Comparison 和 Gate Evidence，不能只由文档声明。
