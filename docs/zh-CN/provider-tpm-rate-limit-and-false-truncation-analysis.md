# Provider TPM 限流与错误“消息截断”问题分析及优化方案

> 状态：问题与根因已通过持久化 Turn Fact、Event、Usage Context 和 Provider
> Transport Receipt 交叉验证；本文中的优化项除特别标注外均为待实现方案。
>
> 本文讨论长会话中工具调用后反复出现“消息被截断”叙述、Provider 429 重试和完整
> Context 重放的叠加问题。通用 Token 工作集治理见
> [长会话 Token 与调用开销优化方案](./token-cost-optimization-plan.md)，模型与 Provider
> 能力来源约束见
> [Provider 能力与 Compaction 修复交接](./provider-capability-and-compaction-recovery-plan.md)。

## 1. 摘要

现场问题不是单一的输出截断，而是两条相互影响、但必须分别处理的故障链：

1. 当前 Route 不支持增量响应传输。每次工具执行结束后，Runtime 都创建新的逻辑
   Sample，并通过 `complete_http_sse` 重新发送完整模型可见 Context。
2. 长会话单请求已经达到约 `690K～718K input tokens` 和 `2.6～2.72 MB`。Provider
   对这些请求频繁返回 HTTP 429 `inference tpm exhausted`。
3. 明确分类为 `rate_limit` 的失败不消耗普通 `provider_retry_limit`，因此一个 Sample
   可以发生多次透明 Transport Attempt 和长时间等待。
4. Provider 最终成功返回的结构化 `stop_reason` 是 `tool_use`，不是 `max_tokens`、
   `incomplete` 或 `content_filter`。Runtime 没有把这些响应判定为截断。
5. “Your message was cut off” 来自模型自己的 reasoning。该说法进入 assistant 历史后，
   后续完整请求持续携带它，模型逐渐把正常工具边界和 429 后的重新采样误解释为截断，
   形成自我强化。
6. 现场还暴露出一个独立的 Usage 观测缺陷：同一调用的累计 Usage 被再次相加，SQLite
   投影可显示约两倍输入。该缺陷会误导容量和成本分析，但不是“截断”叙述的起因。

准确的根因表述是：

```text
超大完整 Context
  × 工具驱动的多 Sample
  × Stateless/完整 HTTP 投影
  × Provider TPM 容量不足
  × 429 透明重试
  × 模型可见历史中的错误截断叙述
= 高延迟、重试风暴和错误“消息被截断”自我强化
```

Prompt 规则可以抑制模型继续误报，但不能减少请求 Token、改变 Provider TPM，也不能终止
透明重试，因此只能作为防御性修复，不能作为根因修复。

## 2. 范围、术语与证据原则

### 2.1 必须区分的三类现象

| 现象 | 权威判定 | Runtime 行为 |
| --- | --- | --- |
| Provider 输出真实不完整 | `stop_reason=max_tokens/incomplete/content_filter`，或流在产生有效内容后异常关闭 | 保留已完成 Block；安全时注入结构化 `[continue_after_incomplete]`；未闭合 Tool Fragment 不执行 |
| Provider 请求失败后重试 | Typed Failure、HTTP 状态、Retry Fact 和 Transport Attempt | 同一逻辑 Sample 等待并重新请求；不应被描述为模型输出截断 |
| 模型自行声称“消息被截断” | 仅见于 assistant text/reasoning，缺少对应结构化 Fact | 属于模型生成内容，不是 Runtime 或 Provider 事实 |

工具调用是正常 Sample 边界。`tool_use` 表示 Provider 已完整结束本次响应并请求执行工具，
不能由 UI、Prompt 或模型叙述重新分类为不完整输出。

### 2.2 证据优先级

排查时按以下顺序使用事实，不从截图或自然语言叙述反推 Runtime 状态：

1. `turn_domain_facts` 中的 Sample Ledger、Transport Attempt、Typed Failure、Assembly
   State 和 `stop_reason`；
2. Durable Event 中的 `usage`、`reasoning.completed`、`tool.start`、`tool.result` 和
   Turn Terminal Event；
3. Provider Projection Receipt 中的 Mode、Fallback Reason、Request Bytes 和 Digest；
4. Usage Context 中的 Window、Request、Prefix、Cache 和 Admission 字段；
5. Web Projection 和截图；
6. 模型在 reasoning 或 assistant text 中对系统行为的解释。

后两项只适合定位用户可见症状，不能替代前四项的结构化事实。

### 2.3 数据安全边界

本文只记录内容安全的计数、部分脱敏 ID、错误类别和容量数据，不记录原始请求、私有源码、
Credential、Provider Token 或完整 Tool Result。后续自动化诊断也必须沿用 Provider Dump、
Diagnostics 和持久化层既有的脱敏边界。

## 3. 现场证据

### 3.1 Turn 与截图对应关系

截图对应长任务 Turn `turn_12be…`。红框中的叙述来自以下两条
`reasoning.completed`：

- `turn-18-step-6`：模型声称环境在工具调用后截断消息；
- `turn-18-step-7`：模型继续声称用户消息被截断。

这两句话没有出现在用户消息、Tool Result 或 Runtime Feedback 中。目标 Turn 内也没有
任何 `[continue_after_incomplete]` 事件内容。

### 3.2 截图样本的请求、重试与终态

| Logical Sample | Transport Attempts | 429 重试 | Request Bytes | 最终 `stop_reason` |
| --- | ---: | ---: | ---: | --- |
| `turn-18-step-6` | 5 | 4 | 2,669,546 | `tool_use` |
| `turn-18-step-7` | 6 | 5 | 2,680,845 | `tool_use` |

两个 Sample 的 Provider Projection 都是：

```text
mode = complete_http_sse
fallback_reason = incremental_capability_disabled
logical_transport_equivalent = true
```

因此每个 Attempt 都发送完整逻辑输入；429 重试没有转换为 Delta 请求。

### 3.3 整个 Turn 的 429 分布

发生 429 的 Sample 及重试次数为：

| Sample | 重试次数 | 最大记录 `Retry-After` | 最大 Engine 有效延迟 |
| --- | ---: | ---: | ---: |
| step 1 | 3 | 9.771s | 9.771s |
| step 3 | 6 | 78.628s | 78.628s |
| step 5 | 8 | 448.997s | 120s |
| step 6 | 4 | 25.779s | 25.779s |
| step 7 | 5 | 64.926s | 64.926s |
| step 8 | 1 | 3.374s | 3.374s |
| step 14 | 3 | 11.302s | 11.302s |
| step 15 | 2 | 5.403s | 5.403s |
| step 18 | 2 | 5.734s | 5.734s |

所有失败都被分类为：

```text
HTTP status = 429
failure code = rate_limit
message = inference tpm exhausted
```

Provider 曾要求等待约 449 秒；Sample Fact 中记录的 Engine 单次有效延迟被
`MaxRetryDelay=2m` 封顶为 120 秒。共享 Rate Limit Controller 还可能在下一请求入口继续
执行 Route Cooldown，因此只看 Engine 的 `effective_delay_ms` 不能推导完整墙钟等待时间。
后续观测应同时展示 Provider Retry-After、Engine Delay、Route Cooldown 和实际 Attempt
开始时间。

### 3.4 单请求容量增长

目标 Turn 的成功请求从约 `690,023` 增长到 `717,660 input tokens`，请求体从约
`2.62 MB` 增长到 `2.72 MB`。典型 Usage Context 为：

| 字段 | 观测范围或值 |
| --- | --- |
| `window_hard_input_tokens` | 983,040 |
| `economic_granted_tokens` | 786,432 |
| `window_full_active_tokens` | 约 690K～718K |
| `message_count` | 约 750～790 |
| `pairing_pairs` | 约 371～393 |
| `history_assistant_tokens` | 约 367K～383K |
| `history_tool_tokens` | 约 175K～182K |
| `request_bytes` | 约 2.6～2.72 MB |
| `provider_projection.mode` | `complete_http_sse` |

这些请求没有超过模型硬输入容量，但模型 Context Capacity 只回答“单次请求能否被模型接收”，
不能回答“Provider 的滚动 TPM 配额是否允许现在发送该请求”。

### 3.5 Sample 编号跳跃的正确解释

Usage Sample 编号从 5 跳到 12、从 13 跳到 22，说明中间消耗过多个 Provider Attempt
序号，但编号跳跃本身不能证明错误类型或精确重试次数。精确值必须来自
`provider_retry_requested` Fact 和对应 Sample Ledger。

本次精确 Fact 与编号跳跃一致，确认中间 Attempt 全部是
`inference tpm exhausted`，而不是输出截断、Tool 解析失败或 Runtime 丢失事件。

### 3.6 错误叙述的历史自我强化

同一 Thread 中至少有 25 条 `reasoning.completed` 包含 `cut off` 语义，分布在 Turn 9、
12、13、14、15、16 和 18。它们的共同模式是：

1. 正常工具调用完成；
2. 下一 Sample 看到完整 assistant 历史、工具结果和新的工作状态；
3. 模型把 Sample 边界或长时间等待解释为“上一条消息被截断”；
4. 该 reasoning 再次进入模型可见历史；
5. 后续 Sample 直接模仿已有解释，不再重新验证结构化终态。

这说明模型错误不是每次独立产生的随机文本，而是由历史投影维持的错误工作记忆。Runtime
仍应保留 Durable Audit，但 Active Model Context 不应把未经证实的模型系统诊断长期当作
权威事实。

### 3.7 Compaction 没有发生

目标 Thread 没有 `context_rebases` 记录，因此现场不存在“已经压缩但压缩无效”的证据；
准确说法是本次长会话没有发生 Context Rebase。

当前工作树正在评估从模型硬输入容量派生 `75%/80%/90%` 的 Prepare、Compact 和
Emergency 阈值。即使采用该方案：

```text
hard input = 983,040
prepare 75% = 737,280
compact 80% = 786,432
emergency 90% = 884,736
observed max input ≈ 717,660
```

现场请求仍未达到 Prepare 阈值。这组阈值只描述模型窗口压力，无法治理比模型窗口更早耗尽的
Provider TPM，不能作为本问题的完整修复。

### 3.8 Usage 累计翻倍缺陷

每个成功 Sample 在 Durable Event 中出现两条 Usage：

1. 第一条是 Provider 返回的真实累计快照，例如约 `690K input tokens`；
2. 第二条变成约 `1.38M`，Input、Output、Reasoning 和 Cached 均接近第一条的两倍。

SQLite `usage` 表按更晚 `source_sequence` 覆盖同一 `(turn_id, sample)`，因此最终保存了
翻倍值。实现契约存在冲突：

- `internal/observability/usage/repository.go` 把同一 Sample 的 Usage Event 定义为累计
  快照，后到事件应覆盖前一事件；
- `internal/adapter/provider/assembly/response_assembly.go` 对每个 Usage Event 调用
  `Usage.Add`，会把 Provider 的累计快照再次相加；
- Engine/Protocol 的 Sample 完成投影又可能发布已累计的结果。

该缺陷会污染：

- Turn/Session Token 汇总；
- Economic Budget 已用量；
- 成本估算和 Web 展示；
- “一次请求到底多大”的排障结论。

排障期间应以原始 Sample 的第一条 Provider Usage、Request Context 和 Provider 账单交叉
核对，不能直接把 SQLite 最终行当作单请求输入。长期必须统一 Usage Event 的 Delta/Cumulative
契约并增加跨层测试。

## 4. 当前实现机制

### 4.1 真实不完整输出

`provider.StopReason.Incomplete()` 只接受：

```text
max_tokens
content_filter
incomplete
```

Provider Assembly 在 `max_tokens` 或 `incomplete` 时返回 `IncompleteOutputError`；Engine
保留完整 Block 和未闭合 Tool Fragment，并通过
`prompt.IncompleteOutputFeedback` 注入：

```text
[continue_after_incomplete stop_reason=...]
```

`tool_use` 不进入这条路径。截图 Sample 的结构化终态因此正确，错误只存在于模型 reasoning。

### 4.2 Provider Retry

Provider Retry Policy 将失败分类后决定恢复动作：

- 5xx、Transport、Stream Closed 和 Timeout 使用 `provider_retry_limit`；
- Context Overflow 仅在 Runtime 已成功改变 Context 后重试；
- Empty Response 只允许一次恢复；
- Rate Limit 将 `MaxAttempts` 设为 0，持续等待，直到 Provider 恢复、Turn 取消或外层
  生命周期结束；
- Retry-After 缺失时使用本地 Backoff；存在时优先使用 Provider 值，但 Engine Delay 可由
  `MaxRetryDelay` 限制。

当前设计可以避免瞬时 429 直接导致 Turn 失败，但没有独立的“最大累计等待”“最大 429
Attempt”或“重试前 Token Admission”契约。对 700K 级完整请求，这会把限流转化成长时间透明
重试和重复传输。

### 4.3 Provider Projection

OpenAI Responses Route 只有同时满足以下条件时才具备增量 Session 投影资格：

1. Protocol 是 OpenAI Responses；
2. Model Capability 声明 `IncrementalResponses`；
3. 存在稳定 Prompt Cache Key；
4. Session 有已提交 Response Chain；
5. Route、Window、Request Property 和逻辑前缀没有破坏连续性。

当前现场因 Capability Disabled 在入口直接回退 `complete_http_sse`。Prompt Cache 命中可以
降低 Provider Prefill 计算，但不会减少 HTTP 请求字节，也不能假设所有 Provider 都从 TPM
中排除 Cached Tokens。

### 4.4 Context 与容量平面

当前 Runtime 已区分：

- Model Hard Input Capacity；
- Output Reserve；
- Operator Economic Ceiling；
- Turn/Session Token Budget；
- Provider Request Rate 和动态 Cooldown。

但尚未把 Provider 的 Token Throughput Capacity 纳入每次请求前的统一 Admission。结果是
请求在模型容量上合法、在当前 Economic Ceiling 内合法，却仍然超过 Provider 的滚动 TPM。

## 5. 根因分析

### 5.1 主根因：缺少 Provider Token Throughput Admission

Runtime 以模型 Context Window 判断单请求正确性，以可选 Budget 判断累计经济开销，以 RPS
和 Retry-After 调整请求时间，但没有在发送前回答：

```text
当前 Route 的滚动 TPM 剩余额度，是否足以接纳这次 projected input + output？
```

因此 690K～718K 的请求可以正常进入 Provider Transport，直到远端用 429 拒绝。

### 5.2 放大因素：工具边界后的完整 Context 重放

每次工具调用都要求模型消费 Tool Result 并决定下一步。对于 Stateless 或禁用增量能力的
Route，这意味着完整 Active Context 必须重发。一个长 Turn 的输入近似为：

```text
Turn Input ≈ Σ(每个业务 Sample 的完整 Active Context)
           + Σ(每个 429 Attempt 的完整 Transport Payload)
```

现有 Usage 只可靠记录成功调用，若失败 Attempt 没有 Provider Usage，实际网络负载和等待放大
不会完整反映在 Token 汇总里。

### 5.3 放大因素：429 不受普通重试预算限制

持续等待 429 的语义适合有明确短期恢复时间的小请求，但不适合在没有 TPM Admission 的情况下
无限重发超大请求。普通 `provider_retry_limit` 不适用于 Rate Limit 是当前公开行为，不能仅通过
调小该配置缓解本问题。

### 5.4 放大因素：错误 reasoning 被当作连续工作记忆

模型对 Runtime 状态的自然语言判断不是权威 Fact。完整重放未经验证的 reasoning，使错误诊断
获得与真实 Goal、Tool Result 和 Verification 相似的持续可见性，最终形成稳定模式。

### 5.5 非根因

以下现象与本次故障同时出现，但不是截图问题的直接根因：

- Runtime 没有产生 `max_tokens`/`incomplete` Stop Reason；
- Runtime 没有注入截断续写 Feedback；
- Web 没有把 `tool_use` 改写为不完整；
- Prompt Cache 命中率很高，但 Cache 不是 TPM Governance；
- 75% 模型窗口阈值尚未触发，且阈值本身不代表 Provider TPM；
- Usage 翻倍会污染观测，但不会让模型自动生成“cut off”。

## 6. 第三方审查结论的校正

| 第三方结论 | 校正后结论 |
| --- | --- |
| 输入约 690K～709K | 方向正确；目标 Turn 后段继续增长到约 718K。SQLite Usage 最终行约 1.4M 是累计翻倍缺陷，不是真实单请求大小 |
| Sample 编号跳跃说明大量 Provider 尝试 | 可作为线索，不能单独证明；Domain Fact 已确认精确重试次数和全部 429 错误 |
| 最长退避约 449 秒 | 449 秒是 Provider `Retry-After`；Engine Fact 的单次有效延迟上限为 120 秒，完整墙钟还需结合共享 Route Cooldown |
| step 6/7 是 `tool_use`，不是截断 | 已由 Assembly Fact 验证，结论正确 |
| “cut off” 只存在于 reasoning | 目标样本和目标 Turn 正确；同 Thread 可检出至少 25 条同语义 reasoning，具体数量取决于匹配词形 |
| 新 Prompt 规则不能解决根因 | 正确；它只阻止模型把普通 Tool Boundary 叙述为截断 |
| 完整 Context 重发打爆 TPM | 与所有 Projection Receipt 和 429 Fact 一致，是主故障链 |

## 7. 优化目标与非目标

### 7.1 目标

- Provider 请求前同时校验模型 Context、经济预算和 Provider Token Throughput；
- 让 Rate Limit 等待、Attempt 和预计恢复时间对用户可见且可取消；
- 避免对明确无法立即接纳的超大请求进行透明重发；
- Stateless Route 继续保持逻辑等价，但 Active Context 必须是有界工作集；
- 支持增量的 Route 优先发送等价 Delta，并保留安全 Full Fallback；
- Runtime 只依据结构化 Stop Reason 触发不完整续写；
- 未经证实的模型系统诊断不能长期污染 Active Context；
- Usage、Transport Attempt、Token Admission 和 Provider 账单保持可核对；
- 所有容量和等待上限来自 Capability、Provider Header、显式配置或公开协议字段，保留
  Provenance、Validation 和边界测试。

### 7.2 非目标

- 不通过删除用户 Goal、开放 Tool Pair、未验证 Change 或 Mandatory Fact 降低输入；
- 不伪造 Stateless Provider 的增量能力或 `previous_response_id`；
- 不把 Cached Tokens 当作免费 Token，除非 Provider Contract 明确声明 TPM 计费语义；
- 不按 Model 名称硬编码 TPM、Context 档位或退避常量；
- 不让 Prompt 文本替代 Runtime State Machine；
- 不因优化重试而绕过 Guard、Approval、Journal、Verification、Policy 或 Sandbox。

## 8. 目标架构

### 8.1 三类容量必须分离

```text
Model Capacity Plane
  └── Context Window / Output Limit / Transport Payload Hard Limit

Economic Capacity Plane
  └── Turn Budget / Session Budget / Operator Working Set / Cost

Provider Throughput Plane
  └── TPM / Remaining / Reset / Retry-After / Cached Token Semantics
```

一次 Provider Call 必须同时通过三类 Admission，不能用其中一个维度替代另一个。

### 8.2 Provider Route Token Governor

建议在 `internal/adapter/provider/ratelimit` 所有权内扩展 Route Governor，并由 Runtime
在 Model Effect 前消费只读 Admission Result。概念输入包括：

```text
route identity
projected input tokens
reserved output tokens
cached input estimate and provider counting semantics
observed committed tokens in rolling window
provider limit / remaining / reset / retry-after
operator-configured throughput contract and provenance
turn cancellation / execution lease
```

概念输出包括：

```text
admitted
wait_until
required_tokens
available_tokens
reason
source
provenance
```

Governor 不应自行裁剪 Context。它只负责容量决策：立即准入、可取消等待，或返回结构化
Resource Exhaustion。Context 如何安全缩小仍由 Context Authority 和 Compaction/Rebase
所有权处理。

### 8.3 Header 与显式配置

Provider 返回可信 Token Limit Header 时，Runtime 应保存有界、低基数事实。Provider 只返回
`Retry-After` 而不返回 TPM 时，只能建立 Cooldown，不能倒推出永久 TPM 值。

自定义 Endpoint 若需要预先 Admission，应由 Operator 显式配置 Token Throughput Metadata，
并标记 `operator_config` Provenance。未知值保持未知；Runtime 可以在首次 429 后按 Header
等待，但不得生成隐藏的“常见 TPM”默认值。

### 8.4 Active Context 与 Durable History

Durable History 继续保留完整 Audit。Provider 可见 Context 由 Context Authority 编译为：

```text
Mandatory Goal + Truth + Open Causality + Current Tool Result
+ Verified Change/Diagnostics
+ Bounded Recent Tail
+ Retrieved Evidence/Handles
+ Tool Definitions
```

历史 reasoning 可保留在 Durable Event 中，但进入 Active Context 前应具备明确用途。模型关于
Runtime、Provider 或 UI 状态的自然语言判断不能升级为 Truth；Compaction Summary 也不得引用
这类判断作为截断证据。

## 9. 分阶段实施方案

### 9.1 P0：修复观测事实

#### P0-1：统一 Usage Delta/Cumulative 契约

1. 明确 `provider.StreamEvent{Type: usage}` 是 Delta 还是当前调用累计快照；推荐统一为累计
   快照，并在类型和测试中写明。
2. Response Assembly 对累计快照执行 replace/max-consistent merge，而不是无条件 `Add`。
3. Engine 不得把同一累计值以“更大累计值”再次发布到相同 Sample。
4. SQLite Projection 继续按 `source_sequence` 幂等覆盖，但增加防回退与不可能倍增检查。
5. 对 OpenAI Chat、OpenAI Responses、Anthropic 和 Fixture 分别覆盖：输入先到、输出后到、
   最终 Usage 重复、崩溃恢复和 Event Replay。
6. 用 Provider 原始 Usage、Durable Event、SQLite Row、Turn Rollup 和 Web 展示做端到端等值测试。

#### P0-2：公开 Transport Attempt 事实

每个逻辑 Sample 应公开内容安全的 Attempt Receipt：

- Attempt 序号；
- Failure Code 和 HTTP Status；
- Provider Retry-After；
- Engine Effective Delay；
- Route Cooldown 等待；
- Request Bytes 和 Projected Tokens；
- Projection Mode/Fallback Reason；
- Attempt Started/Finished 时间；
- 最终 Stop Reason。

用户不应再通过 Usage Sample 编号跳跃推断重试次数。

#### P0-3：Web 明确区分三种状态

Web Timeline 分开展示：

```text
Provider 限流，等待重试
Provider 输出不完整，正在安全续写
工具调用已完成，继续同一 Turn
```

UI 只能消费结构化 Event，不解析 reasoning 中的 “cut off” 文本来改变状态。

### 9.2 P0：控制 429 重试生命周期

#### P0-4：公开 Rate Limit Recovery Budget

在保留 Provider Cooldown 的同时，引入显式、可配置、可观察的恢复边界，例如：

- 最大累计 Rate Limit 等待时间；
- 最大 Rate Limit Attempts；
- 是否在达到边界时转为可恢复 Blocked Outcome；
- 用户继续/取消入口。

字段名称应在实现设计阶段确定，必须进入 Config Schema、Provenance、中文配置文档和边界测试。
默认值不能来自未记录经验常量；可以由 Turn Execution Lease、Provider Reset、Operator 配置或
公开产品契约派生。

达到边界后不得把 429 伪装成模型截断，也不得丢失已完成 Tool Side Effect。Runtime 应从 Durable
Checkpoint 返回可恢复状态。

#### P0-5：避免已知不可接纳请求立即重发

当 Provider 已返回 Remaining/Reset/Retry-After 时，同 Route 的下一 Attempt 必须先通过共享
Governor。等待期间不占用 Provider 并发槽，且能被 Turn Cancel 唤醒。

若 projected request 本身超过已知 Route Token Burst/TPM Contract，应在 Provider I/O 前：

1. 请求 Context Authority 尝试安全 Rebase；
2. 重新测量请求；
3. 仍不满足时返回结构化 Resource Exhaustion 或等待下一 Window；
4. 不循环发送相同 Digest 的请求探测 Provider。

### 9.3 P1：按 Provider Throughput 控制工作集

现有 Hard Capacity 和 Economic Admission 之外，增加 Throughput Admission：

```text
required = projected_input + provider_counted_output_reserve
available = route_window_remaining
```

Cached Input 是否计入 `required` 必须来自 Provider Contract 或真实 Header 语义。未知时采取保守且
可配置的计数方式，并在 Receipt 中显示来源，不能按 Provider 名称猜测。

Context Rebase 的触发原因新增 `provider_throughput`，与 `context_capacity`、
`economic_budget` 分开记录。这样可以解释“模型窗口尚有空间但仍提前缩小工作集”的来源。

### 9.4 P1：阻断错误“截断”历史强化

当前工作树增加的 Tool Instruction 是必要防线：只有收到结构化
`[continue_after_incomplete]` 才允许模型推理输出截断。

还应补充：

1. 为 Runtime Feedback 使用不可与普通用户文本混淆的结构化 Provenance；
2. Context Compiler 不把旧 reasoning 中的系统状态猜测提升为 Truth；
3. Semantic Narrative 只能依据 Stop Reason、Failure Fact 和 Tool/Verification Fact 描述恢复；
4. 受控测试注入历史 “Your message was cut off”，但当前 Sample 为 `tool_use`，断言 Prompt 和
   Context Truth 明确否定截断；
5. UI 可在 reasoning 旁显示“模型生成，不代表 Runtime 状态”的语义边界。

不能简单删除全部 reasoning：部分 Provider 的 Reasoning Replay、签名和 Continuation 具有协议
要求。优化应发生在 Provider Replay Contract 和 Context Authority 内，而不是在 Web 层字符串过滤。

### 9.5 P2：优先使用真实增量传输

对支持 Stateful Responses 的 Provider：

1. 通过 Capability 明确声明 `IncrementalResponses`；
2. 绑定稳定 Cache/Session Key 和已提交 Response Chain；
3. 仅发送能重建相同 Logical Input 的 Delta；
4. Connection Reset、Route Change、Compaction、Resume 和 Property Change 时安全回退 Full；
5. Retry Attempt 不得复用未提交 Provider Response；
6. Receipt 必须证明 `logical_transport_equivalent=true`。

对 OpenAI-compatible Chat/Stateless Endpoint，不得伪造增量能力。其优化重点仍是有界 Active
Context、减少 Sample 和 Provider Throughput Admission。

## 10. 测试方案

### 10.1 Provider Retry Matrix

覆盖：

- 429 带 Retry-After；
- 429 只有 Reset；
- 429 无 Header；
- Account Hard Quota；
- 5xx/Timeout 与 429 使用不同预算；
- 大请求在发送前被 Token Governor 等待或拒绝；
- Cancel 可终止等待；
- 重启后 Cooldown/Recovery State 不重复已完成 Side Effect。

### 10.2 Output Completeness Matrix

| Stop Reason | Tool Call | 期望 |
| --- | --- | --- |
| `tool_use` | 完整 | 执行工具，不注入 incomplete feedback |
| `max_tokens` | 无 | 保留 Block 并结构化续写 |
| `max_tokens` | 未闭合 Fragment | 保留 Fragment，不执行，要求重新发完整 Tool Call |
| `incomplete` | 任意 | 结构化续写 |
| `content_filter` | 任意 | Fail Closed，不继续猜测 |
| Stream EOF after meaningful output | 任意 | 分类为 incomplete/stream failure，不执行未闭合 Tool |

### 10.3 Usage Contract Matrix

- 单 Usage Event；
- Input 与 Output 分两次累计报告；
- 相同最终 Usage 重复报告；
- Provider 错误后成功 Attempt；
- 崩溃恢复重复 Replay；
- Tool 内部模型调用与主模型调用使用不同 Sample；
- SQLite、Rollup、Receipt 和 Web Total 完全一致；
- `CachedTokens <= InputTokens`、`ReasoningTokens <= OutputTokens` 始终成立。

### 10.4 长会话回放

构造或脱敏回放以下形态：

- 700K 级逻辑 Context；
- 20 个连续 Tool Boundary；
- Provider 在指定 Attempt 返回 429；
- 历史 reasoning 含错误 “cut off” 叙述；
- Route 分别使用 Stateless Full 和 Stateful Incremental。

断言：

- Logical Sample 与 Transport Attempt 分开计数；
- 所有 Full Attempt Digest 可解释；
- Incremental Route 的 Delta 能重建同一 Logical Input；
- `tool_use` 永不触发 incomplete feedback；
- 达到 Recovery Budget 后进入可恢复状态；
- Usage 不重复累计；
- Compaction 不丢失 Goal、Tool Pair、Change、Verification 或 Pending Interaction。

## 11. 验收标准

### 11.1 正确性

- Runtime 只依据结构化 Stop Reason 判定输出不完整；
- 所有 Tool Call/Result Pair 闭合；
- 429 等待和终止不重放已完成 Side Effect；
- Stateful 增量投影与完整逻辑输入等价；
- Stateless Route 不发送伪造 Delta；
- Context Rebase 保留 Mandatory Truth 和可恢复状态。

### 11.2 容量与延迟

- 已知 TPM Contract 时，超过当前 Window Remaining 的请求不会立即进入 Provider；
- 相同 Request Digest 不再形成不可见的无限 429 重试；
- Web 能显示累计等待、下一恢复时间和取消入口；
- 长会话的 Active Context 与 Provider Calls/Turn 均有明确来源的上限；
- 具体目标值来自 Provider/Operator Contract 或 A/B 验收配置，不写入隐藏模型档位。

### 11.3 可观测性

- 单次调用 Token 可由 Provider Event、SQLite、Receipt 和 Web 交叉核对；
- Transport Attempt 数无需从 Sample 编号推断；
- Retry-After、Engine Delay、Route Cooldown 和实际墙钟等待可区分；
- Projection Mode、Fallback Reason、Request Bytes、Projected Tokens 和 Cached Tokens
  在同一 Sample Receipt 中可查询；
- 模型 reasoning 不作为 Runtime 状态来源。

## 12. 当前版本的排障与临时缓解

在完整优化落地前，遇到类似现象时按以下顺序处理。

### 12.1 确认是否真实截断

只读查询目标 Turn 的 Sample Fact，核对：

```text
assembly.state
segments[].stop_reason
provider_retries
last_failure.code/message/http_status
transport.request_bytes
transport.projection.mode/fallback_reason
```

若 `stop_reason=tool_use` 且不存在 `[continue_after_incomplete]`，则“消息被截断”只是模型叙述。

### 12.2 确认是否 TPM 限流

检查 Typed Failure 是否为：

```text
code=rate_limit
http_status=429
message=inference tpm exhausted
```

同时检查每次请求的 `window_full_active_tokens`、`request_bytes` 和 Projection Mode。不要使用
SQLite 中可能已经累计翻倍的最终 Usage Row 作为唯一证据。

### 12.3 临时缓解

- 取消不再值得等待的 Turn，避免无界透明等待；
- 使用显式 Turn Token Budget 和更小的 Operator Active Context Ceiling；
- 在安全边界执行 Context Compaction/Rebase，确认产生新的 `context_rebases`；
- 批量独立只读工具，减少一工具一 Sample；
- Provider 确实支持时选择 Stateful Incremental Route；
- 新建 Session 只能作为应急隔离手段，不能替代 Durable History 与 Active Context 的长期治理；
- 不通过修改 Prompt 声称“问题已解决”，也不要手工改写 SQLite 或 Event Log。

## 13. 实现所有权与预计影响面

| 工作项 | 所有权路径 |
| --- | --- |
| Usage 语义与 Provider Stream | `internal/adapter/provider/assembly`、各 Provider Adapter |
| Retry Policy 与 Typed Failure | `internal/adapter/provider/wire` |
| Route TPM Governor | `internal/adapter/provider/ratelimit` |
| Model/Tool 主循环与 Feedback | `internal/runtime/agent/engine`、`internal/runtime/agent/prompt` |
| Context Rebase 与工作集 | `internal/runtime/agent/context`、`contextview` |
| Config、Provenance、Validation | `internal/config` |
| Durable Attempt/Usage Fact | `internal/runtime/agent/turnkernel`、`internal/persist`、`internal/observability` |
| Protocol/Event/Receipt | `internal/runtime/protocol` |
| Web 状态呈现 | `web/src/projection`、`web/src/ui` |
| 构造与依赖注入 | `internal/runtime/app/wire` |

Host 只提交 Operation 和投影 Event，不得把 TPM Governor、Retry Loop 或截断判定复制到 Web
控制面。

## 14. 实施顺序

建议按以下顺序交付，每一阶段独立测试和灰度：

1. 修复 Usage 累计契约，建立可信基线；
2. 公开 Transport Attempt、Rate Limit Wait 和最终 Stop Reason；
3. 引入可配置 Rate Limit Recovery Budget，避免无界透明等待；
4. 引入 Provider Token Throughput Admission；
5. 将 Throughput Pressure 接入 Context Authority 的安全 Rebase；
6. 阻断未经证实的截断 reasoning 污染 Active Context；
7. 对声明支持的 Route 启用 Stateful Incremental Transport；
8. 使用真实 Provider Harness 做长会话 A/B，并根据权威数据调整 Operator Profile。

每个阶段都必须保持 Logical Sample、Transport Attempt、Tool Effect 和 Turn Terminal State
的现有所有权，不以局部性能修复绕过 Kernel、Persistence、Guard 或 Recovery Contract。
