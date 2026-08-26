# 提升 Prompt Cache 命中率的优化方案

> 状态：设计文档 / 方案草稿（非已交付）。本文结合 DeepSeek 上下文缓存机制与 CodeHelper 现有实现，
> 给出「提升预填充缓存命中率、从而降低 TTFT」的落地思路。涉及改动的部分均标注文件与行为约束，
> 不引入未登记的固定阈值或启发式常量（遵循仓库硬规则）。

## 1. 背景与目标

CodeHelper 是一个 Coder 型 agent 运行时：每次调用模型前都会把较大的上下文（系统指令、工具定义、
历史、工作集、世界状态、证据、计划等）投影为单一请求。对于一个 1M 上下文模型，真实会话的硬输入容量
可达 ~640K token（见 `docs/zh-CN/fixed-threshold-audit.md` 的「真实会话验证」）。

在 `internal/observability/trace/trace.go:421`，TTFT 定义为 `firstToken.Sub(root.Started)`——即从
turn 开始（`NameTurn` 根 span）到第一个 output-bearing token 的耗时。其中很大一部分来自 provider 端
对超大 prompt 的 **prefill**。因此：

- **目标**：最大化 provider 端上下文缓存的命中比例，跳过尽可能多的 prefill，降低 TTFT；
- **不改变**：decode 吞吐（tok/s）、上下文正确性、Tool/Result 因果链、以及安全/治理约束；
- **底线**：所有新阈值/容量默认值必须带 Config 与 Provenance，不得新增硬编码魔法常量。

## 2. DeepSeek 上下文缓存机制

DeepSeek 提供**自动上下文缓存（Context Caching）**，其要点如下（精确计费/缓存语义请以 DeepSeek 官方
文档为准）：

- **缓存对象**：输入 prompt 在 prefill 阶段计算出的 KV cache。
- **命中判定**：**精确前缀匹配**（token 级）。从第一个 token 开始连续匹配；一旦遇到首个不一致处，
  匹配即停止，命中部分复用已有 KV cache，未命中尾巴重新 prefill。
- **计费与观测**：usage 中返回 `prompt_cache_hit_tokens` 与 `prompt_cache_miss_tokens`，命中 token
  计价更低。CodeHelper 已在 `internal/adapter/provider/openai/stream.go:331` 解析这两个字段，并在
  `:389` 处按 `nativeCache` 聚合。
- **效果**：命中的前缀越长，跳过的 prefill 越多，**TTFT 越低**；decode 速度不受缓存影响。
- **关键推论**：命中率由「**前缀 token 序列的稳定性**」决定，而非某个语义匹配。任何导致前缀 token
  序列变化的东西（改顺序、改措辞、插入时间戳、动态写入系统指令、重排工具定义）都会让缓存从变化点
  之后失效。

> CodeHelper 通过 `internal/adapter/provider/deepseek/stream.go` 走 OpenAI Responses 协议
> （`dsmlStream` 包装），并开启 `NativeCache: true / FinalUsage: true`，顺带在
> `internal/adapter/provider/openai/adapter.go:150` 注入 `prompt_cache_key`。DeepSeek 的缓存本身是
> 自动按前缀生效，因此「前缀稳定」是根本驱动；`prompt_cache_key` 作为附加的缓存作用域标识，同样需要保持
> 稳定，避免 key 漂移导致缓存条目失配。

## 3. CodeHelper 当前实现与缓存相关路径

### 3.1 Prompt 组装与分区

`internal/runtime/agent/context/store_store.go:25` 定义了模型可见消息的分区顺序：

```text
orderedKinds = [ Stable, History, Dynamic, Continuation ]
```

- `Stable`：系统指令等应保持稳定的部分；
- `History`：历史因果消息；
- `Dynamic`：工作集、证据、失败记录、计划、世界状态等随 turn 变化的部分；
- `Continuation`：本次续写。
- 工具定义（`Definitions`）在 `store_store.go` 中经 `cloneDefinitions` 复制，**其顺序直接继承
  `LedgerInput.Definitions`，未做确定性排序**。

`model_handler.go` 在请求前调用 `snapshot.Normalize`、`snapshot.Measure`（token 估算/归因，
`store_attribution.go:58`）、`prepareTokenWindow`、`checkBudget`，最后
`requestTools = snapshot.Definitions()`。

### 3.2 Cache Key 与 Revision

- `internal/runtime/agent/engine/engine.go:281`：`PromptCacheKey = fmt.Sprintf("%s-profile-%d", base, profile.PromptCacheRevision)`。
- `internal/adapter/provider/types.go:119` `StickyPromptCacheKey(key, route)`：仅当 key 非空且模型支持
  `CapPromptCache` 时才返回非空 key（`types.go:120`）。
- `internal/runtime/protocol/session_profile.go` 的 `Patch`：只有 `mode / planning_policy /
  plan_approval / provider / model / reasoning_effort / enabled_tool_ids` 变化时才递增
  `PromptCacheRevision` 并置 `PromptCacheReset`；`ApprovalPosture / ExecutionTarget / MaxSteps` 等
  只递增 `Revision`。

**现状结论**：Cache Key 的收敛已经做得较细（只对「真正影响 prompt 前缀」的字段 bump），这是一个优点；
但仍有改进空间（见第 5 节）。

### 3.3 Usage 的 cache hit/miss

`internal/adapter/provider/openai/stream.go:331` 已解析 `PromptCacheHitTokens` / `PromptCacheMissTokens`，
并在 `:389` 处校验。这为「数据驱动的命中率优化」提供了信号来源。

### 3.4 已有稳定化处理

- `session_profile.go` 对 `EnabledToolIDs` 做了 `slices.Sort`（保持集合顺序稳定）；
- `StickyPromptCacheKey` 用模型 capability 门控，避免向不支持的模型发缓存 key。

## 4. 命中率低的根因分析

结合上述实现，导致命中率低（TTFT 偏高）的主要来源：

1. **工具定义顺序不稳定**：`Definitions` 未排序，若同一套工具在不同调用中以不同顺序输出，会破坏前缀。
   即便顺序偶然一致，任意一次重排都会让缓存从该点之后失效。
2. **易变内容混入稳定前缀**：若世界 digest、workspace/journal revision、repository head、当前 git 状态等
   被投影到 `Stable` 或靠前位置，那么每轮变化都会让前缀在很早的位置失效，命中前缀变得极短。
3. **`PromptCacheRevision` 与真实前缀不一致**：虽然已做字段筛选，但任何与 cache 相关字段变化都会整体
   重置 key；若这些字段（如 `reasoning_effort`、`enabled_tool_ids` 排序后集合）频繁变化，key 会反复
   漂移 → 缓存条目失配。
4. **跨模型/跨 provider 切换**：更换 model/provider 会改变 tokenizer 与缓存体系，缓存必然失效（合理，
   不可优化）。
5. **缺少命中率观测闭环**：虽然解析了 hit/miss token，但未在 receipt/telemetry 中形成可对比的
   「cache hit rate / 平均命中前缀长度」信号，难以判断优化是否有效。
6. **Coder 上下文天然动态**：工作集、证据、plans 每轮都会变，若这些落在前缀前段，命中率自然低。

## 5. 优化方案（分阶段）

### 5.1 P0：确定性排序与稳定前缀（收益最大、风险最低）

- **工具定义按稳定 ID 排序**：在 `Definitions` 进入 `Ledger`/`ModelRequest` 前，按
  `ToolDefinition` 的稳定标识排序，保证同一套工具在任何调用中顺序一致。
- **稳定分区内容纯净化**：确保 `KindStable` 只包含真正静态的内容（系统指令、固定规则、角色设定）；
  把任何带时间戳 / 随机数 / 会话相关值的文本从 `Stable` 移到 `Dynamic`。
- **确定性序列化**：`MessageSnapshot.Messages()` 与 `Definitions()` 的输出顺序必须确定；为
  `Messages()`/`Definitions()` 增加契约测试，验证「相同内容 → 相同字节序列」。

**落地文件**：`internal/runtime/agent/context/store_store.go`（排序 + 分区约束）、
`internal/runtime/agent/engine/model_handler.go`（请求前组装）。

### 5.2 P1：前缀 / 易变内容的布局

- 维持 `[Stable, History, Dynamic, Continuation]` 顺序，并**把最易变的内容尽量后置**：
  世界状态、workspace binding、journal revision、repository head、当前 diff、运行中的工具结果、
  plan 增量等应落在 `Dynamic`/`Continuation`，不应进入 `Stable` 或 `History` 前段。
- 由于 DeepSeek 的命中是「最长公共前缀」，只要前段（系统指令 + 工具 + 历史 + 重复上下文）稳定，
  每轮变化集中在尾部，命中前缀就能保持很长。
- 若某个工作区头部信息必须在开头（例如 system role 需要绑定 workspace），考虑把它作为**稳定但独立于
  世界 digest 的标识**，**不要**把每轮刷新的 revisions 嵌入其中。

**落地文件**：`internal/runtime/agent/context` 的分区构造（`LedgerInput`/`Project`）、
`internal/runtime/app/wire` 的上下文装配处。

### 5.3 P1：Cache Key 与 Revision 收敛

- **让 Cache Key 反映「真实前缀内容」而非「profile 元数据变化」**：可考虑将 `PromptCacheKey` 推导为
  「稳定前缀内容摘要 + profile 中影响前缀的少数维度」，使得与无关元数据（如 `MaxSteps`、
  `ExecutionTarget`、`ApprovalPosture`）完全解耦（现状已部分解耦）。
- **最小化 `PromptCacheRevision` 触发面**：复核 `session_profile.go` 的 `setCacheReason` 字段集，
  确认没有把「会频繁变化但对前缀影响可忽略」的字段纳入；对 `enabled_tool_ids` 这类集合，已排序后仍
  应按「集合是否变化」而非「顺序是否变化」判定。
- **明确跨 model/provider 行为**：切换 model/provider 时缓存失效属预期，文档中标注；不要为迎合缓存而
  静默替换路由。

**落地文件**：`internal/runtime/protocol/session_profile.go`（Revision 触发面）、
`internal/runtime/agent/engine/engine.go`（key 推导）。

### 5.4 P1：观测与命中率度量

- 在 usage/receipt 中暴露 `prompt_cache_hit_tokens` / `prompt_cache_miss_tokens`（已在
  `openai/stream.go:331` 解析），并**新增聚合指标**：cache hit rate、平均命中前缀长度、
  TTFT 与 hit rate 的关联。
- 为 turn receipt / observable 增加字段，供 web 层与运维对比，确保优化可被数据验证，而非凭感觉。

**落地文件**：`internal/observability/receipt`、`internal/runtime/protocol/receipt.go`、
`internal/observability/trace`（TTFT 关联）。

### 5.5 P2：配置化与 Provenance

- 任何涉及「是否启用缓存 / 命中前缀最小长度 / 缓存 TTL / 是否强制固定前缀」的选项，一律作为 **Config 字段
  并带 Provenance**，不得作为硬编码常量（遵循 `fixed-threshold-audit.md` 的硬边界要求）。
- 保留必要的安全/协议硬上限（如前缀大小、输入验证），这些属于公开契约，不参与动态调优。

**落地文件**：`internal/config`、`internal/adapter/provider`（Provider Contract）、
`internal/runtime/runtimeapi/web/capacity.go`（容量与连接限额）。

## 6. 验收标准 / 验证计划

- **前缀稳定性**：对相同内容重复构建 `ModelRequest`，`Messages()`/`Definitions()` 字节序列一致；
  增加契约/回归测试覆盖「工具重排」「动态字段注入」不再破坏摘要。
- **命中率提升**：在 1M 上下文模型上跑真实会话，观测 `prompt_cache_hit_tokens` 占比上升、TTFT 下降；
  记录优化前后对比。
- **不回归正确性**：`PRUNE`/`COMPACT` 发生率不上升，Tool/Result 因果链完整，使用 `make ratchet-fast`、
  受影响包 `go test`、`git diff --check` 验证；跨 Package 改动按风险放宽验证面。
- **无魔法常量**：新增阈值/预算均有 Config + Provenance；`docs-check` / `book-check` 通过。

## 7. 风险与权衡

- **前缀稳定 vs 上下文新鲜度**：把易变内容后置可能让模型「更晚看到」最新世界状态。需结合
  `Context Admission` 与 compaction 权衡；不为缓存牺牲工具结果或因果链。
- **缓存命中与正确性不可强绑**：同一 prefix 在不同 model/provider 下不可复用（tokenizer 不同），
  仅在 route 不变时追求命中。
- **超大前缀的尾部仍可能超窗**：命中前缀再长，若尾部（未命中部分）仍然庞大，TTFT 只是被削减而非消失。
- **provider 侧语义差异**：DeepSeek 缓存是自动按前缀生效的，`prompt_cache_key` 在部分 provider/协议下
  作用不同；应以各 provider 文档为准，避免把「key 稳定」当成「一定命中」。

## 8. 相关代码位置索引

| 主题 | 位置 |
| --- | --- |
| Prompt 分区顺序 | `internal/runtime/agent/context/store_store.go:25` |
| Ledger / Snapshot / Definitions | `internal/runtime/agent/context/store_store.go` |
| 请求前投影/估算 | `internal/runtime/agent/engine/model_handler.go` |
| Cache Key 推导 | `internal/runtime/agent/engine/engine.go:281` |
| StickyCacheKey 门控 | `internal/adapter/provider/types.go:119` |
| PromptCache capability | `internal/adapter/model/capability.go:21` |
| hit/miss 解析 | `internal/adapter/provider/openai/stream.go:331` |
| DeepSeek 流（Responses + NativeCache） | `internal/adapter/provider/deepseek/stream.go` |
| Revision 触发面 | `internal/runtime/protocol/session_profile.go` |
| TTFT 定义 | `internal/observability/trace/trace.go:421` |
