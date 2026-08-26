# 长会话 Token 与成本优化方案

> 状态：设计文档 / 实施计划，尚未全部交付。
>
> 本文是长会话 Token、Provider 调用放大、Context Admission、Tool Result 生命周期、
> Prompt Cache 和成本治理的统一实施入口。当前 Context 正确性契约见
> [Session Context、Memory 与持久化](./session-context-optimization.md)，容量常量治理见
> [固定阈值审计与动态容量治理](./fixed-threshold-audit.md)，缓存专项设计见
> [提升 Prompt Cache 命中率的优化方案](./prompt-cache-hit-rate-optimization.md)。

## 1. 结论

CodeHelper 的主要问题不是单个 Prompt 片段过大，而是以下因素相乘：

```text
完整模型可见历史 × 单 Turn 多次 Provider Sample × 过晚的成本型压缩
```

Stateless Provider 每次接收完整的**模型可见上下文**是正确行为；不正确的是把接近完整的
Durable History 长期当作 Active Context，并在缺少累计 Turn/Session Budget 时允许多次
Tool、Repair 和 Verification Sample 重复消费它。

目标状态必须满足：

```text
Durable History != Active Model Context
Hard Context Capacity != Economic Working Set
Prompt Cache Optimization != Token Budget Governance
```

优化完成后，长会话累计输入应从接近随轮数二次增长，收敛为“有界 Active Context × 有界
Sample 数”的近似线性增长。基于第 2 节真实样本，预期异常长 Turn 的 Token 降低
`55%～75%`，整段长会话降低 `45%～65%`；第一阶段验收目标是不牺牲正确性的前提下至少
降低 `40%`。这些比例是该样本的容量规划目标，不是写入 Runtime 的固定策略常量。

## 2. 真实样本与证据等级

一次 DeepSeek 长会话调试得到以下账本：

| 指标 | 观测值 | 结论 |
| --- | ---: | --- |
| CodeHelper 本地累计 | 5,383,182 tokens | 与 Provider 基本一致 |
| Provider 侧累计 | 5,363,702 tokens | 实际消耗量级成立 |
| 差值 | 19,480 tokens / 约 0.36% | 需核对是否等于被 Web 重复相加的 Reasoning Tokens |
| 最近 5 个大 Turn | 4,100,593 tokens | 占 Provider 总量约 76.5% |
| 最近 5 个大 Turn Provider Calls | 48 | 平均约 9.6 Calls/Turn |
| 最后失败的实现 Turn | 1,039,349 tokens | 占总量约 19.4%，文件改动为 0 |
| 失败 Turn 单次输入 | 约 101K 增长到 128K | 同一 Turn 内 Context 继续增长并被重放 |
| 历史 Tool Result | 约 428 KiB | 可能构成 Active Context 的主要部分 |
| 历史图片 | 约 3.1 MiB | 是否成为 Provider 输入取决于 Route 图片能力 |
| Cache | 9 次中 7 次约 1%，2 次约 98%～99% | 缓存可工作，但多数请求前缀发生早期分歧 |

由此可直接计算：

```text
最近 5 个大 Turn：4,100,593 / 48 ≈ 85,429 tokens/call
最后失败 Turn：1,039,349 / 9 ≈ 115,483 tokens/call
```

证据等级：

- **已证实**：Usage 不是流式事件重复累加造成的假象；Provider Calls 和完整上下文重放是
  主要消耗来源。
- **已证实**：`turn_budget_tokens=0` 的当前语义是不设置累计 Turn Token 上限；Context Window
  只能保护单次请求容量。
- **高度相关**：428 KiB 历史 Tool Result 与 100K 级输入处于同一量级；准确贡献应以每个
  Sample 的 `history_tool_tokens` 为准。
- **待归因**：7 次低 Cache Hit 很可能来自非单调的 World State 投影，但需要相邻请求的首个
  Prefix Divergence 证明。
- **待归因**：3.1 MiB 图片若被投影给 Vision Route，会造成重复传输和图片输入；若 Route 不支持
  图片，Normalize 会将其替换为短占位符。

## 3. 增长模型

设第 `t` 个 Turn 开始时 Active Context 为 `H_t`，本 Turn 产生 `s_t` 次 Provider Sample，
第 `j` 次 Sample 前新增的 Tool Result、Feedback 和 Assistant Output 为 `D_t,j`，则输入近似为：

```text
Input_t ≈ Σ(j=1..s_t) (H_t + D_t,j)
```

若每个 Turn 平均增加 `Δ` Token，平均产生 `s` 次 Sample，则 n 个 Turn 的累计输入近似：

```text
s × [nH_0 + Δ × n(n+1)/2]
```

这解释了长会话中总 Token 加速增长。Prompt Cache 可以降低命中前缀的 Prefill 费用与 TTFT，
但 Cache Hit Tokens 仍是 Input Tokens 的子集，不能改变这个 Token 计数模型。

目标模型是令 Active Context 有界为 `H`，每 Turn 的 Provider Sample 有界为 `S`：

```text
Input_t <= S × H
Input_1..n <= n × S × H
```

`H` 与 `S` 不能由模型名称或未登记的固定比例决定，必须由 Capability、Operator Config、显式
Budget、当前 Usage 和运行时观测共同推导。

## 4. 目标与非目标

### 4.1 目标

- 将 Durable History、Audit Payload 和 Active Model Context 明确分层；
- 在每次 Provider Call 前同时执行容量 Admission 与累计成本 Admission；
- 为最终收尾保留预算，避免在预算耗尽后再追加一次完整上下文 Sample；
- 降低 Tool、Completion Declaration、Verification 和 Failure Repair 导致的重复 Sample；
- 已消费 Tool Result 自动降级为 Facts、Receipt、Digest 与 Handle；
- 同一 Turn 内尽量保持 Provider 输入前缀单调，提升 DeepSeek 自动 Context Cache 命中；
- 图片等大对象保存在 CAS/Content Store，模型按能力和任务相关性获取有界投影；
- Usage、Cached、Reasoning 和 Cost 使用一致且可验证的统计口径；
- 所有经济阈值具有 Config、Provenance、校验、文档和边界测试。

### 4.2 非目标

- 不通过删除 Mandatory Fact、开放 Tool Pair、Pending Interaction 或未验证 Change 换取成本；
- 不把 Prompt Cache 当作 Token Budget；
- 不伪造 DeepSeek `previous_response_id` 或在 Stateless API 上只发送 Delta；
- 不根据 `deepseek-*`、`gpt-*` 等模型名称维护隐式容量档位；
- 不改变 Audit、Usage 和 Cost 的单调性；
- 不绕过 Tool Guard、Approval、Journal、Verification、Policy 或 Sandbox。

## 5. 目标架构

```text
Durable Event / History / CAS
        │
        ├── Accounting Plane ── Usage / Cost / Budget（只增不减）
        ├── Audit Plane ─────── Event / Receipt / Raw Tool Result / Image
        │
        ▼
Context Authority
Goal / Truth / Open Actions / Changes / Diagnostics / Handles
        │
        ▼
Context Compiler + Economic Admission
        ├── Stable Prefix
        ├── Current Goal + Mandatory Truth
        ├── Recent Raw Tail
        ├── Open Tool Causality
        ├── Retrieved Evidence
        ├── Current Tool Result Surface
        └── Tool Definitions
        │
        ▼
Bounded ActiveContextView
        │
        ├── Stateless Provider：每次发送完整的有界 View
        └── Stateful Provider：发送等价 Delta，必要时回退完整 View
```

建议将 Provider 可见输入提升为明确的 Runtime 数据结构，而不是允许调用点直接消费完整历史：

```go
type ActiveContextView struct {
    Stable        []provider.Message
    Goal          []provider.Message
    Truth         []provider.Message
    RecentTail    []provider.Message
    OpenCausality []provider.Message
    Evidence      []provider.Message
    ToolSurface   []provider.Message
    Dynamic       []provider.Message
    Continuation  []provider.Message
    Definitions   []provider.ToolDefinition
}
```

类型名称可以调整，但所有 Provider Adapter 必须消费同一个逻辑 View；增量传输只改变 Transport，不能
改变逻辑输入或绕过 Context Authority。

## 6. 两类边界

### 6.1 Hard Capacity

Hard Capacity 只保护正确性和 Provider Contract：

```text
hard_input_capacity =
  min(model_context_capacity, provider_transport_capacity)
  - actual_output_reservation
  - protocol_framing_reservation
```

超过 Hard Capacity 时必须 Prune、Compact 或返回结构化 `resource_exhausted`，不得继续调用。

### 6.2 Economic Working Set

Economic Working Set 控制日常成本和延迟。设：

- `B_remaining`：当前 Turn 剩余累计 Token Budget；
- `N_remaining`：当前动作图中预计还需的业务 Provider Calls，至少包含当前调用，不包含单独保留的必要收尾；
- `O_reserve`：每个剩余调用的有效输出保留；
- `F_reserve`：终态声明、必要 Verification 或失败报告的保留；
- `H_operator`：Operator 配置的 Active Context Ceiling；未配置时取 `hard_input_capacity`，不能以零参与最小值计算；

则下一次调用允许的平均 Active Context 为：

```text
H_budget = floor(
  (B_remaining - N_remaining × O_reserve - F_reserve)
  / max(1, N_remaining)
)

H_allowed = min(hard_input_capacity, H_operator, H_budget)
```

当缺少显式 Turn/Session Budget 时，Runtime 不能假装 Context Window 是成本预算。它应：

- 明确记录 `budget_unbounded`；
- 在 Web 和 Receipt 中展示未设置累计保护；
- 只执行 Hard Capacity Admission；
- 由 Operator Profile 或请求级配置选择是否启用 Economic Admission。

如果 Provider Pricing 已知，可在 Token Budget 之外增加 USD 计算：

```text
expected_input_price =
  cache_hit_share × cached_input_price
  + (1 - cache_hit_share) × uncached_input_price
```

Cache Hit Share 必须来自当前 Route 的观测，不得在没有样本时臆造。Pricing 未知时 Token Budget 保持
权威，`budget_usd` 不得给出虚假的保护承诺。

## 7. 初始 Operator Profile

以下配置用于当前 1M Context DeepSeek 长会话的首轮 A/B 验证，来源是第 2 节 100K～128K 单次输入、
约 9.6 Calls/Turn 的真实观测。它不是新的全局默认值，也不能按模型名称写入代码：

```toml
[execution]
max_output_tokens = 16384
max_steps = 12
turn_budget_tokens = 600000

[context.compact]
scope = "total"
prepare_tokens = 65536
auto_compact_tokens = 98304
emergency_tokens = 131072
recent_tail_turns = 2
recent_tail_max_tokens = 32768
summary_max_bytes = 32768
```

含义：

- 64K：开始收敛探索并准备 Context Rebase；
- 96K：该 Operator Profile 的经济工作集 Ceiling；
- 128K：进入 `finish_only` 的 Operator Emergency Ceiling；
- 600K：允许约 4～6 次 64K～96K 级调用并保留收尾；
- 12 Steps：状态机的异常上限，不是成本额度；实际调用仍由 Turn Budget 更早收窄。

后续不应长期维护这组人工值。积累足够成功任务样本后，从当前 Route 的 Provider Calls、Active Tokens、
Output Tokens、Cache Hit、TTFT、Handle Reopen 和正确完成率推导 Operator 建议值；最终决定仍由显式配置
确认，并记录 Provenance。

## 8. P0：立即控制真实 Token

### 8.1 P0-1：修正 Usage 展示与成本事实

当前后端定义：

```text
TotalTokens = InputTokens + OutputTokens
CachedTokens subset of InputTokens
ReasoningTokens subset of OutputTokens
```

Web 不得再次把 `ReasoningTokens` 加入 Total。实施项：

1. 修正 `web/src/ui/App.tsx` 和 `web/src/projection/trajectory.ts`；
2. 增加 `input + output` 的契约测试；
3. 用真实样本核对 19,480 Token 差值是否等于聚合 Reasoning Tokens；
4. Turn、Session、Trajectory 和 Provider 截图使用同一统计口径；
5. 同时展示 Cached Share 与 Uncached Input，不把 Cache Hit 说成 Token 消失。

该项只修复观测，不会显著降低实际 Token，但它是所有后续 A/B 的前提。

### 8.2 P0-2：Provider Call 前的累计 Spend Governor

每次 Sample 前执行：

1. 获取已提交 Session Usage、当前 Turn Usage 和当前 Sample 已观察 Usage；
2. 计算下一次 Active Context、Output Reserve 和必要 Finalization Reserve；
3. 同时检查 Hard Capacity、Turn Token、Session Token 和已知情况下的 USD Budget；
4. 达到收敛阶段时禁止探索性 Tool Definitions；
5. 无法同时支付当前 Sample 与必要收尾时，不再发起当前探索 Sample；
6. 返回结构化 Budget Exhaustion，并保留可恢复状态。

`turn_budget_tokens=0` 继续表示 Operator 未设置累计上限，不能重新从 Context Window 派生。生产 Web
Setup 应明确提示风险，并提供显式 Operator Profile，而不是静默选择无限预算。

重点文件：

- `internal/runtime/agent/engine/budget.go`
- `internal/runtime/agent/context/budget.go`
- `internal/runtime/agent/engine/model_handler.go`
- `internal/runtime/app/wire/modules_runtime.go`
- `internal/config`

### 8.3 P0-3：减少单 Turn Provider Samples

当前工具启用后要求 `turn_complete`。协议保留，但不能让格式错误无限放大成本：

1. `DeclarationRepairLimit=0` 不得再形成没有独立预算的重复 Declaration Repair；
2. Declaration、Completion、Workspace 和 Verification Repair 分别有显式、可审计的 Budget；
3. 每次 Repair 必须记录 `reason`、`progress_key`、已用次数和新增事实；
4. 无进展 Repair 直接进入 Convergence，不因改变反馈文本重置进展；
5. 为最终结构化收尾预留一次调用；
6. 对无 Mutation、无 Pending Tool、输出非空的 Answer/Plan Turn，评估由 Runtime 接受合法
   `end_turn` 并合成安全终态，避免为了形式再采样；
7. Workspace Change、Operation 和有 Side Effect 的 Turn 仍必须经过结构化完成与 Verification，
   不得因降本放宽正确性门禁；
8. Tool Selection 继续鼓励批量独立只读调用，避免一文件一 Sample。

重点文件：

- `internal/runtime/agent/turnkernel`
- `internal/runtime/agent/engine/turn_handler.go`
- `internal/runtime/agent/engine/completion_declaration.go`
- `internal/runtime/agent/prompt/tool_instructions.go`

### 8.4 P0-4：Economic Context Admission

现有 Compaction 继续负责 Hard Capacity；新增成本型 Admission：

1. 每次 Sample 前根据第 6.2 节计算 `H_allowed`；
2. 先投影 Mandatory Goal、当前用户请求、Pending Interaction、开放 Tool Pair、未验证 Change 和
   Verification Fact；
3. 再分配 Recent Tail、Working Set、Evidence、Memory、Skills 和辅助信息；
4. 超过 `H_allowed` 时先降级可重新获取 Surface，再选择安全历史边界；
5. 所有决策记录 `requested`、`granted`、`reason`、`source` 与 `provenance`；
6. Mandatory 内容无法容纳时拒绝继续，不能用摘要猜测；
7. 压缩后重新测量 Provider 实际 Input，并校准当前 Window Ledger。

Economic Compaction 可以比模型硬窗口更早发生，但必须来自 Operator Budget/SLA 或观测状态；不能在代码
里增加另一套隐藏的固定百分比。

### 8.5 P0-5：Tool Result 消费后降级

Tool Result 生命周期调整为：

```text
Produced
  -> Latest Batch（当前下一次 Sample 可见）
  -> Consumed（模型成功处理该 Batch）
  -> Receipt + Facts + Digest + Handle
  -> 按需 result_get / handle_read
```

约束：

- 最新 Tool Batch 在被模型成功消费前保持闭合且可见；
- 旧 Result 原文保存在 Content Store/CAS；
- 降级投影保留 Tool Name、Call ID、Outcome、关键文件变化、错误类别、Verification Facts 和 Handle；
- Retrieval Result 本身不能再次无限内联；
- 当前 Turn 开放因果链与最新失败恢复信息不得裁剪；
- 累计 Tool Surface 使用本次 `H_allowed` 的剩余容量，而不是另设模型无关固定档位。

重点文件：

- `internal/adapter/tool/tool.go`
- `internal/adapter/tool/result/prune.go`
- `internal/runtime/agent/engine/tool_result_pruning.go`
- `internal/runtime/agent/turnkernel/tool_effect.go`
- `internal/runtime/agent/context`

## 9. P1：降低未缓存费用与长期尾部

### 9.1 P1-1：同一 Turn 内冻结可替换 World Snapshot

DeepSeek 官方 API 当前是 Stateless API，不支持 `previous_response_id`、`conversation` 或 `store`；客户端
必须发送完整逻辑历史。它提供自动 Context Cache，命中依赖精确公共前缀：

- <https://api-docs.deepseek.com/guides/responses_api/>
- <https://api-docs.deepseek.com/guides/kv_cache/>

当前 `ProjectWorld` 会追加新版本 World Message，`ProjectStatelessHistory` 又会从请求中删除旧版本，仅保留
最新版本。若旧版本位于历史前部，下一次请求会在很早的位置发生删除和替换，破坏 Prefix Monotonicity。

优先方案：

1. Turn Admission 时冻结 Stable、World Snapshot、Tool Catalog 和影响 Prefix 的 Request Properties；
2. Turn 内 Tool 产生的新事实通过尾部 Tool Result 进入模型，不重写前部 World Snapshot；
3. Working Set、Evidence 和 Repository Context 的 Authority 仍实时更新，但统一在下一 Turn 或安全 Rebase
   点生成新 World Snapshot；
4. 若当前 Turn 必须看到新的 Runtime Fact，以只追加的 Dynamic Patch 放在尾部；
5. 每次 Rebase 允许一次预期 Cache Miss，随后恢复尾部追加；
6. 不为缓存保留错误或已撤销的 Authority；安全和正确性高于命中率。

备选方案是保留 Append-only World Patch 并周期性 Rebase。禁止只删除历史片段却不提供等价最新状态。

### 9.2 P1-2：内容安全的 Prefix Divergence 观测

现有 Context Digest 不足以说明缓存从哪里断开。每个相邻 Sample 增加不含原文的 Prefix Manifest：

```text
sample_id
route_digest
request_property_digest
ordered_item_kind
ordered_item_digest
ordered_item_tokens
common_prefix_items
common_prefix_tokens
first_divergence_index
first_divergence_kind
world_changed_sections
tool_definition_digest
projected_images
history_tool_tokens
cached_tokens
uncached_tokens
```

Manifest 只保存类型、计数和加盐/内容安全摘要，不记录 Prompt、Tool Result、图片或 Secret 原文。

要验证的首要假设：

```text
cache_hit ≈ 1% 的 Sample
是否同时满足 world_changed_sections > 0
且 first_divergence_kind = world_state
```

若不成立，再检查图片位置、Reasoning Replay、Tool Catalog、Profile Revision、Provider Request Property 和
Compaction/Rebase。

### 9.3 P1-3：图片生命周期

输入协议的 5 MiB 附件上限属于 DoS/协议边界，应保留；它不是模型可见生命周期策略。实施项：

1. Durable History 与 Event 保存图片 CAS Handle、Digest、Media Type、尺寸和来源，避免重复内联大字节；
2. 当前用户 Turn 首次使用图片时，按 Route Capability 投影；
3. 不支持图片的 Route 继续使用明确占位符；
4. 图片被消费后保留结构化视觉事实与 Handle，后续只有明确引用或检索才重新投影；
5. Image Token Estimator 从 Model/Provider Capability 获取，不使用跨模型统一估算；
6. Receipt 区分 Durable Bytes、Transport Bytes 和 Provider Image Tokens。

### 9.4 P1-4：DeepSeek Cache 与 Pricing 闭环

DeepSeek Context Cache 是自动前缀缓存，官方 Responses API 当前不支持 `prompt_cache_key`。Adapter 不应依赖
该字段实现 DeepSeek 缓存，并应按 Provider Contract 省略不支持的 Request Property。

实施项：

1. 将 `prompt_cache_hit_tokens`、`prompt_cache_miss_tokens` 投影到 Sample、Turn、Session Rollup；
2. 展示 Cached、Uncached、Output 和 Reasoning 的非重叠口径；
3. 从权威 Provider Metadata 或显式 Operator Config 加载 DeepSeek Pricing 与 Provenance；
4. Pricing 未知时显示 `Unpriced`，Token Budget 仍可执行；
5. Pricing 已知后启用 USD Budget，并区分 Cached/Uncached Input 单价；
6. 关联 Cache Hit、TTFT、Transport Bytes 与 Cost，不以 Token 总量代替实际费用。

### 9.5 P1-5：从观测派生 Operator 建议值

按 Route、Mode、Intent 和是否包含图片分组，使用成功且正确完成的 Turn 观测：

- Provider Calls/Turn；
- Active Input Tokens/Sample；
- Output 与 Reasoning Tokens；
- Cached/Uncached Input；
- Tool Result Retained Tokens；
- Compaction 与 Handle Reopen；
- Completion/Verification Repair；
- TTFT 与完成质量。

系统可以生成建议值，但不得静默修改运行配置。建议必须展示样本范围、观测时间、Route、当前值、建议值和
预期影响，由 Operator 接受后写入配置并记录 Provenance。

## 10. P2：Provider 增量传输

DeepSeek 官方当前不支持服务端会话续接，因此不能通过本地 Adapter 伪造。短期正确方案仍是发送完整的有界
ActiveContextView，并依赖自动 Prefix Cache。

未来 Provider 宣布支持后，可以复用 OpenAI Responses SessionAdapter 的契约：

- 保存上一 Response ID；
- 验证 Route、Window、Recovery、Property 和逻辑 Prefix Digest；
- 只发送等价 Delta；
- Retry、Compaction、Resume、Route Change 和 Connection Reset 自动回退完整请求；
- Transport Delta 与 Logical Input 分别计量；
- 增量失败不能改变模型逻辑输入。

普通反向代理只能替客户端保存历史，调用 Stateless DeepSeek 时仍需发送完整上下文；只有持有推理 KV Cache
的 Provider 或自托管推理服务才能提供真正的服务端增量。

## 11. 配置与协议建议

优先复用现有配置：

- `execution.budget_tokens`
- `execution.turn_budget_tokens`
- `execution.budget_usd`
- `execution.max_output_tokens`
- `execution.max_steps`
- `context.compact.prepare_tokens`
- `context.compact.auto_compact_tokens`
- `context.compact.emergency_tokens`
- `context.compact.recent_tail_turns`
- `context.compact.recent_tail_max_tokens`
- `context.compact.summary_max_bytes`

若现有字段无法表达 Tool/Image/Finalization 生命周期，再增加公开配置或协议字段。每个新增字段必须包含：

- 单位和零值语义；
- Capability/Operator/Protocol/Observation 来源；
- 合法范围与错误信息；
- JSON/TOML/环境变量映射；
- Provenance；
- Web 展示与修改入口；
- 边界、序列化和兼容性测试。

不增加预发布兼容迁移；首次稳定发布前直接维护单一正确语义。

## 12. 可观测性

### 12.1 每个 Sample

必须能回答：

- 为什么调用模型；
- 调用前 Active Context 各 Partition 占多少 Token；
- 当前 Turn 已调用多少次、消耗多少；
- 此次调用剩余 Budget 与 Finalization Reserve；
- Tool Definitions、Tool Result、Image 分别占多少；
- Cache 从哪里开始失配；
- 此次调用是普通 Sample、Repair、Continuation、Retry 还是 Finalization。

### 12.2 每个 Turn

至少聚合：

```text
provider_calls
input_tokens
output_tokens
reasoning_tokens
cached_tokens
uncached_tokens
tool_result_tokens
image_tokens
transport_bytes
completion_repairs
declaration_repairs
verification_repairs
compactions
pruned_tool_results
handle_reopens
workspace_changes
terminal_outcome
```

### 12.3 每个 Session

提供 Turn 排名、增长曲线和归因，而不仅是一个 Total：

- Token/Cost 最高的 Turn；
- 无 Workspace Change 但高消耗的失败 Turn；
- Calls/Turn、Input/Call 和 Uncached/Call；
- Active Context 随 Turn 的变化；
- Compaction 前后重复工作率；
- Cache Hit 与 TTFT；
- 图片和 Tool Result 的长期残留。

## 13. 测试计划

### 13.1 Unit Tests

- Budget：当前 Sample + Finalization Reserve 恰好等于、低于和高于剩余预算；
- Repair：Declaration/Completion/Verification 各自耗尽且不互相重置；
- Tool Result：最新 Batch 保留、消费后降级、Handle 可重取、Tool Pair 始终闭合；
- Context：Mandatory Fact 无损、Recent Tail 有界、重复 Compaction 保留 Goal；
- Prefix：同一 Turn 尾部追加保持公共前缀，World Patch 不改写前部；
- Image：Vision、非 Vision、CAS 丢失、超协议上限和重复引用；
- Usage：Reasoning/Cached 不重复计入 Total；
- Pricing：Unknown、Cached Price 缺失和完整 Pricing 的 Budget 行为。

### 13.2 Integration Tests

构造至少以下场景：

1. 多次只读 Tool Call 后正常回答；
2. Workspace Change + Verification + `turn_complete`；
3. Tool Failure 后恢复；
4. 模型遗漏 Completion Declaration；
5. 100K 级历史上的失败实现 Turn；
6. 多个 32 KiB Tool Result 累积；
7. 历史图片跨多个 Turn；
8. World State 每个 Tool Step 变化；
9. DeepSeek Stateless + Prefix Cache；
10. Stateful Provider Incremental Fallback。

每个场景同时断言终态正确性、Tool Pair、Authority Digest、Workspace Journal、Provider Calls、Token
Budget、Prefix Manifest 和 Usage 单调性。

### 13.3 真实会话 A/B

固定 Workspace、Prompt、Route、Tool Catalog 和输入附件，分别运行 Baseline 与 Candidate。比较：

- 总 Token 与 Uncached Input；
- Provider Calls/Turn；
- 平均与最大 Input/Call；
- Cache Hit 与 TTFT；
- 任务完成、验证、失败恢复和重复读取；
- 最终 Diff、测试结果和用户可见答案。

不能只比较一次随机模型输出。使用多次运行与成功任务集合，保留原始 Receipt 和内容安全 Digest。

### 13.4 仓库验证

```bash
make ratchet-fast
go test ./internal/runtime/agent/context
go test ./internal/runtime/agent/engine
go test ./internal/runtime/agent/turnkernel
go test ./internal/observability/usage
go test ./internal/adapter/provider/...
go test ./internal/adapter/tool/...
npm --prefix web run check
npm --prefix web test
make docs-check
make book-check
git diff --check
```

根据实现风险扩大 Race、Runtime、Persistence、Web E2E 和真实 Provider Harness 验证。

## 14. 灰度计划

### 阶段 A：观测修正

- 修复 Web Total Token；
- 增加 Uncached、Repair、Tool Result、Image 和 Prefix Divergence 指标；
- 不改变 Context 或终态行为；
- 建立可重复 Baseline。

### 阶段 B：Audit-only Governor

- 计算 Economic Admission 与 Finalization Reserve；
- 只记录“本应 Converge/Compact/Reject”的决策，不执行；
- 对比真实后续调用是否产生有效进展；
- 校准 Operator Profile。

### 阶段 C：显式 Profile 强制

- 仅对显式启用的 Session 执行 Turn Budget、早期 Compaction 和 Tool Result 降级；
- 保留一键回退到 Hard Capacity-only；
- 重点监控 Completion、Handle Reopen 和验证失败。

### 阶段 D：前缀稳定与图片 CAS

- 同一 Turn 冻结 World Snapshot；
- 启用 Prefix Manifest 与 Cache A/B；
- 图片迁移到 CAS 引用；
- 确认不引入 Authority Staleness。

### 阶段 E：生产默认

- 只有当正确性、恢复和安全验收通过后，才把经过 Operator 确认的 Profile 作为生产推荐；
- 保留显式无限预算语义，但 Web 必须清楚标识风险；
- 发布说明记录默认行为、配置来源和回滚方法。

## 15. 验收标准

### 15.1 成本与 Token

基于第 2 节样本：

| 等级 | 整段 Session Token 降幅 | 异常长 Turn Token 降幅 |
| --- | ---: | ---: |
| 第一阶段 | 至少 40% | 至少 50% |
| 目标 | 50%～60% | 55%～70% |
| 调优充分 | 60% 以上 | 70%～75% |

同时满足：

- Token Total 使用 `input + output`；
- Cache 优化单独报告 Uncached Input、Cost 和 TTFT，不伪装成 Token 降幅；
- 不再出现未显式授权的百万 Token、零 Workspace Change 失败 Turn；
- 每次预算收敛都能解释 Used、Remaining、Reserve、Action 和 Scope。

### 15.2 正确性

- Goal、当前用户请求、Pending Interaction、开放 Tool Pair 和 Mandatory Truth 零丢失；
- Workspace Change 不绕过 Mutation 与 Verification；
- 压缩前后最终任务意图一致；
- Handle Reopen 可以恢复被降级原文；
- Restore/Fork 后 Usage 不回退，旧 Workspace Fact 正确失效；
- 缓存布局变化不改变 Provider 逻辑输入。

### 15.3 质量平衡

优化不能仅通过“少调用模型”达标。A/B 必须同时检查：

- 正确完成率；
- 验证通过率；
- 重复 Tool Call 与重复文件读取；
- Repair 后成功率；
- Context Compaction 后的 Handle Reopen；
- 用户请求覆盖和最终答案完整性。

若 Token 降低但重复读取、失败率或未完成任务明显上升，应回退 Candidate，并用观测结果调整 Context
Selection 或 Budget，而不是直接扩大隐藏阈值。

## 16. 风险与回退

| 风险 | 防护与回退 |
| --- | --- |
| 压缩丢失用户意图 | Mandatory Goal/Request 由 Authority 生成并做 Digest 校验 |
| Tool Result 过早降级 | 最新 Batch 消费确认后才降级；原文可通过 Handle 读取 |
| Turn Budget 过小 | 显式 Profile、Audit-only 阶段、结构化 Budget Exhaustion |
| Completion Repair 过严 | 为有效进展保留独立 Repair Budget，不放宽安全终态 |
| World Snapshot 冻结导致过期 | 新事实通过尾部 Tool Result/Patch 可见；下一 Turn 或安全点 Rebase |
| Cache 优化掩盖逻辑差异 | 比较 Logical Input Digest，Transport 优化不得改变逻辑输入 |
| 图片 CAS 不可用 | 当前 Turn 保留安全投影；CAS 校验失败返回明确 Problem |
| DeepSeek Pricing 变化 | Pricing 带来源与更新时间；未知时回退 Token Budget |
| Semantic Narrative 失败 | 回退 Truth + Recent Tail，不影响业务终态 |

所有灰度功能必须能按 Session/Profile 回退。回退只改变后续 Context Projection，不回滚 Usage、Audit、
Workspace Side Effect 或已提交 Session State。

## 17. 实施顺序与代码地图

| 顺序 | 工作项 | 主要路径 |
| --- | --- | --- |
| 1 | 修复 Token Total 与补齐 Sample/Turn 归因 | `web/src`、`internal/observability/usage`、`internal/runtime/protocol` |
| 2 | Finalization Reserve 与累计 Spend Governor | `internal/runtime/agent/engine`、`internal/runtime/agent/context` |
| 3 | Repair Budget 与终态收敛 | `internal/runtime/agent/turnkernel`、`internal/runtime/agent/engine` |
| 4 | Economic Context Admission | `internal/runtime/agent/context`、`internal/runtime/agent/prompt` |
| 5 | Tool Result 消费后降级 | `internal/adapter/tool`、`internal/runtime/agent/engine` |
| 6 | Prefix Manifest 与 World Snapshot 冻结 | `internal/runtime/agent/context`、`internal/runtime/agent/engine` |
| 7 | 图片 CAS 与能力化 Token 估算 | `internal/runtime/protocol`、`internal/persist`、Provider Adapter |
| 8 | DeepSeek Pricing/Cache 闭环 | `internal/adapter/model`、`internal/adapter/provider`、Usage/Web |
| 9 | 观测驱动的 Operator 建议 | `internal/observability`、`internal/config`、Web Settings |

每个工作项独立提交、独立验证，不在一次变更中同时修改 Budget、Compaction、Terminal 和 Provider
Transport。先建立可信观测，再开启强制策略；先减少无效 Sample，再收窄 Active Context；最后优化 Cache
和 Transport。

## 18. 完成定义

本方案只有在以下条件全部满足时才算完成：

1. 长会话不再因为 Durable History 增长而无界扩大每 Turn 输入；
2. 每次 Provider Call 都经过 Hard Capacity 与显式 Economic Budget Admission；
3. Tool Result、Image 和 World State 具有明确的模型可见生命周期；
4. Completion/Verification Repair 不会形成无预算的完整上下文循环；
5. DeepSeek Prefix Cache 的命中和失配点可以逐 Sample 解释；
6. Token、Cached、Reasoning、Uncached 和 Cost 口径不重叠；
7. 真实会话达到第 15 节目标，同时正确性、恢复、安全和验证不回归；
8. 所有新增阈值均有 Config、Provenance、文档和边界测试；
9. `make ratchet-fast`、相关 Go/Web 测试、文档检查和 Diff 检查全部通过。
