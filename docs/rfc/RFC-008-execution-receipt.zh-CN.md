# RFC-008：ExecutionReceipt 的 usage / cost / latency / trace

> 状态：**已落地**。T1 / T2（`sample` 覆盖聚合、`cost_known`、schema v7）；T3 / T4（span 采集与延迟分区、预算分区、schema v8 `spans` 表），偏离记在 §4 与 §10；T5a（CLI / TUI / HTTP 三个读面共用一个折叠层与一个金额格式化函数），偏离记在 §11。**T5b（OTLP）取消**，理由见 D5——本 RFC 至此收尾
> 关联：[ROADMAP §8.4](../ROADMAP.zh-CN.md)、[ROADMAP §8.2](../ROADMAP.zh-CN.md)、[ROADMAP §8.3](../ROADMAP.zh-CN.md)、[RFC-006 ChildRuntime](./RFC-006-child-runtime.zh-CN.md)、[RFC-007 ExecutionService](./RFC-007-execution-service.zh-CN.md)、[RFC-003 VS Code Transport](./RFC-003-vscode-transport.zh-CN.md)
> 影响面：`internal/runtime/protocol`、`internal/runtime/app`（`receipt.go`、`application.go`）、`internal/runtime/agent/engine`、`internal/observability/{usage,telemetry,trace}`、`internal/persist/state/sqlite`、`internal/adapter/provider/{anthropic,openai}`、`internal/host/{tui,cli,runtimeapi/http}`

---

## 1. 问题

这条线的问题不是"没有账"，而是**账已经在记了，但记错了，而且错得看不出来**。缺功能可以从 maturity 里读到，记错的数字不会自报。

1. **usage 事件是"调用累计快照"，落库把它当增量求和。** 引擎每收到一次 provider usage 就 `usage.Add(...)` 然后发出**当前累计值**（`engine/engine.go:833`），投影却给每个事件插一行（`observability/usage/repository.go:135-146`），聚合用 `SUM`（同文件 `QueryAggregates`）。Anthropic 每次调用发两次 usage——`message_start` 只带 input、`message_delta` 只带 output（`provider/anthropic/stream.go:181-188`、`249-256`）——于是两行分别是 `{in:100,out:0}` 与 `{in:100,out:50}`，`SUM` 报出 200 input。**cost 跟着 token 走，所以金额同样翻倍。** 今天 `/v1/usage` 对 Anthropic 的读数是错的，对任何一次调用发多次 usage 的 provider 都是错的。
2. **`cost_known` 的判据是"金额大于零"，不是"价格已知"。** `receipt.go:126` 写的是 `if event.CostUSD > 0`，所以一个价格已知的免费模型报 unknown。而 `UsageData` 连这个位都没有（`protocol/message.go:403-411`），注释把负担推给消费者："consumers must not read zero as free"——一个消费者无法从数据里区分的区别，靠注释是维持不住的。§8.4 第 6 条要的"未知不能算成 0"因此是两头都缺：位没算对，位也没落库。
3. **`usage_turn_context` 把一个 turn 绑定到唯一的 provider/model**（`persist/state/sqlite/store.go:558-568`，来源是 `turn.started`），而 §8.2 要做 plan/act/verify/compact/vision 分路。分路一落地，一个 turn 会跨多个模型，这张表就开始说谎——而它正是 usage 行的 provider/model 来源。
4. **延迟只有一项，还只在收据上。** `ExecutionReceiptData.LatencyMS` 是 turn 墙钟（`receipt.go:175`）；首 token、tool、approval 等待、verify 全没有采集。`UsageData` 没有任何延迟字段。
5. **完全没有 trace。** 全仓没有 span 抽象、没有 trace id、没有 OTLP，§8.4 第 4 条是纯空地。
6. **`metrics` / `scorecard` 是文件透传，而 `MetricSnapshot` 里没有 token、cost、latency 任何一项**（`observability/telemetry/metrics.go:5-37`）。CLI 的措辞让人以为它是账单，它其实是进程内计数器的快照，`scorecard` 还自带 `"thin": true`。
7. **token 本身不齐，但不齐的地方与看起来的不同。** Anthropic 不解析 cached（`cache_read_input_tokens` / `cache_creation_input_tokens` 既不进 `InputTokens` 也不进 `CachedTokens`），所以**缓存读取的 token 与它们的钱今天完全不可见**。而 `provider.Usage.Total()` 只算 input+output 看似漏算 reasoning，实测两家的 reasoning 都取自输出明细字段、本就含在 output 里，所以它没错——只是没有任何地方写明这个不变量（D7）。

所以本 RFC 的第一目标不是"加字段"，而是**让已经存在的数字变成对的，并且让"不知道"能被表达**。

---

## 2. 硬约束

### C1：累计快照是事件流的正确形状，错的是聚合

事件流 append-only 且支持 cursor 续订（M2 契约）。累计快照对**晚加入的订阅者**友好：一个从中途接上来的 host 收到一个快照就知道总量。改成增量会让晚加入者永远缺前半段。所以"累计"要保留，**唯一要改的是把累计当增量加的那一处**。TUI 的 `noteUsage` 已经是替换语义（`host/tui/tools.go:353-364`），它与累计一致——这进一步说明落库是唯一的错误点，而不是事件语义要翻案。

### C2：usage 事件是持久化事件，会被重放

投影靠 `event_sequence` 唯一 + 冲突时逐字段比对（`repository.go:150-178`）来保证幂等。任何新语义必须保住"同一事件重放两次等价"。这条排除了"投影时做减法算增量"的做法：重放会把减法做第二次。

### C3：`turn.receipt` 是 per-turn 唯一权威摘要，承载不了 per-model 明细

收据在终态事件前发一次（`application.go:253-271`）。一个跨模型的 turn 只有一份收据，因此**明细必须留在 usage 事件与 usage 表里**，收据只做该 turn 的合计。这条决定了"分路路由"不能靠扩收据字段解决。

### C4：协议形状有生成式 JSON Schema 漂移测试与 ACP/HTTP 双 host 契约

M2 的硬门禁（ROADMAP §11.3）：任何协议字段变更要同步 `docs/protocol/runtime-protocol.schema.json`（`make protocol-schema`）与 `internal/host/runtimeapi/contract` 的场景表（`make protocol-contract`）。

### C5：unknown 已经存在于价格表，缺的只是把它带出来

`model.Pricing.Known`（`adapter/model/catalog.go:60-66`）已经是权威位，catalog 校验禁止 `known=false` 时带非零费率（`catalog.go:200-202`），成本预算也已经 fail-closed（`engine.go:1809-1814`：`cost budget requires known model pricing`）。所以 unknown 语义不需要新概念，只需要**一条从 `pricing.Known` 到事件、到表、到 UI 的通路**。

### C6：cached token 在两家 provider 的口径不同

OpenAI 的 `prompt_cache_key` 路径下，`cached_tokens` 是 `prompt_tokens` 的**子集**；Anthropic 的 `cache_read_input_tokens` / `cache_creation_input_tokens` 与 `input_tokens` **并列**、不含在内。直接把两家的字段填进同一个结构，得到的 input 口径不一致，加总与计价都会错。**归一化规则必须先定，再谈计价。**

---

## 3. 决策

### D1：保留累计语义，给它一个显式作用域，投影改为"分区内最后一次胜出"

`UsageData` 增加 `sample`（本 turn 内第几次 provider 调用，从 1 起）与 `provider` / `model`。投影从"每事件一行 + SUM"改为：

- 行的身份是 `(turn_id, sample)`，不再是 `event_sequence`；
- 同一 `(turn_id, sample)` 的后续事件**覆盖**前一行，且只在 `source_sequence` 更大时覆盖——与 `usage_turn_context` 已有的手法一致（`repository.go:99-104`），重放天然幂等（C2）；
- 聚合直接对行求和：每个分区只剩一行，行内已是该次调用的累计值。

这样 Anthropic 的两次事件落在同一 sample 上，后一次覆盖前一次，聚合得到 `{in:100, out:50}`。理由：**累计快照的正确聚合是"取最后"，而"取最后"需要知道累计到哪为止**——sample 就是那个边界。没有它，投影只能猜，而它今天猜错了。

`sample` 由引擎给出（`modelStep` 的调用序号），不是由投影推断：投影看不到 provider 流的边界。

### D2：unknown 是一个位，从价格表来，一路带到 UI

- `UsageData` 与 `ExecutionReceiptData` 的 `cost_known` **一律取自 `pricing.Known`**，不再从 `CostUSD > 0` 反推。价格已知的免费模型因此报 `cost_known=true, cost_microunits=0`——真的免费；价格未知报 `cost_known=false`，金额位无意义。
- usage 表加 `cost_known`。聚合返回 `cost_known_samples` 与 `cost_unknown_samples` 两个计数：**一次聚合里混了已知与未知时，金额是"至少这么多"而不是"就是这么多"**。
- HTTP 与 TUI 的展示规则：`cost_unknown_samples = 0` 才展示金额；否则展示金额加 `+unknown`（或直接 `unknown`，取决于已知部分是否为零）。**没有任何一层允许把 unknown 渲染成 `$0.00`。**

### D3：provider / model 的归属下移到事件，`usage_turn_context` 降级

新行的 provider/model 以 usage 事件为准（D1 已带上）。`usage_turn_context` 保留，但语义收窄为"该 turn 的首个路由"：它仍然是 `turn.started` 的落点与旧库的兼容来源，但不再是明细的真相。理由是 C3 + §8.2：分路路由落地时不需要再改一次表结构，**这一步是给 RFC-010 让路**。

### D4：延迟做成分区对象，"缺失"与"零"必须可区分

收据上不再平铺加字段，而是一个分区：

```go
type ReceiptLatency struct {
	TotalMS        int64  `json:"total_ms"`
	FirstTokenMS   *int64 `json:"first_token_ms,omitempty"`
	ProviderMS     int64  `json:"provider_ms"`
	ToolMS         int64  `json:"tool_ms"`
	ApprovalWaitMS int64  `json:"approval_wait_ms"`
	VerifyMS       int64  `json:"verify_ms"`
}
```

"这个 turn 没有审批"与"我们没测审批等待"是两件事，前者是 `0`，后者必须能被表达——这是 unknown 语义的同一条要求的第二次出现。**落地时这条区分由分区自身的有无承担**：`ExecutionReceiptData.Latency` 是指针，缺分区就是"没测"，有分区就意味着五项都测过，于是各项的 `0` 只可能是"量过了，花了零"。`FirstTokenMS` 是唯一的例外与唯一保留的指针：一个模型没有产出的 turn 没有诚实的零可报，`0` 会被读成"首 token 瞬间就到了"。理由是一个永不为 nil 的指针不传达任何信息，只多一处解引用。

`LatencyMS` 作为顶层字段保留（兼容），值等于 `Total`。

approval 等待是真实墙钟（park 到 decision），不是审批次数——次数已经有 `ApprovalsRequested`，两者一起才能回答"这个 turn 慢是因为在等人"。

各项之间的关系必须写明，否则读者会把它们当饼图相加：`ApprovalWaitMS ⊆ ToolMS`（审批是在 `guard.Execute` 内部 park 的），而 `ToolMS` 是**按调用求和**，工具并发时可以**大于** turn 的墙钟。求和而非"处于工具阶段的时长"是刻意的：能解释账的是"这些工具花了九秒工作量"，它也正好能和逐条 tool span 对上。

`ProviderMS` 起初写的是 `⊆ TotalMS`（turn 内的 provider 调用串行）。RFC-010 T2 之后这条不再成立：会采样模型的工具（`image_analyze`、`sub_query`）也会开 `model_call` span，于是 provider 时间**与 tool 时间重叠**，多个工具同时采样时求和还可能超过墙钟。改口径而不是把这些时间藏起来——那是真的在等 provider。

### D5：trace 只做本地；OTLP 出局（原为 opt-in，2026-08 定为不做）

- 定义**最小 span 接口**在 `internal/observability/trace`，核心代码只依赖这个接口，不依赖 otel SDK。理由：核心路径上引入一个大依赖，换来的只是一个默认关闭的导出器。
- 本地 span 落 SQLite 一张 `spans` 表。**一次 trace = 一个 turn**，所以关联键就是 `turn_id`：`spans` 不加 `trace_id` 列、`turn.receipt` 与 usage 行也不加 `trace_id` 字段（原稿的写法见 §4 的说明）。一个只会重复 `turn_id` 的列，是多出来的一件要与它保持一致的东西；run 级 trace 见 §9 第 3 条。
- **OTLP 导出器不做**（原计划是独立包 + 运行时开关，排在 egress broker 之后）。这个产品是本地单用户的 Coding Agent：span 已经在本地 SQLite 里可查，而一个默认关闭、需要用户自己架 collector、还要先过出网审批的导出器，服务的是集群级可观测场景，不是这条线上的读者。真出现"要把 trace 送到公司 collector"的需求时，它是一个独立的出网集成，从 `spans` 表读就够，不需要在核心里预埋接口——所以 §8.4 第 4 条只落"本地 spans"这一半。现有的 `trace.Sink` 是 SQLite 的写入口，不要把它读成为导出预留的抽象。

### D6：`metrics` / `scorecard` 改读 SQLite，telemetry 文件保留但正名

`metrics` 与 `scorecard` 查 usage 表与 spans 表；`scorecard` 不再报 `thin`。telemetry 的 JSON 快照**保留**——它测的是另一类东西（events published、subscribers dropped、compactions），那些不属于账单，也不该塞进 usage 表。要改的是 CLI 的措辞与文档，别让两者继续混为一谈。

### D7：token 口径归一化——两个"子集"不变量写死，Anthropic 补齐

四个字段的关系今天没有任何地方写明，于是"`Total()` 只算 input+output"看起来像漏算。实测两家 provider 的口径后，它其实是对的：

| 字段 | 含义 | 不变量 |
| --- | --- | --- |
| `InputTokens` | **全部**输入 token，含缓存命中的部分 | 总量 |
| `CachedTokens` | 其中命中缓存的部分 | `⊆ InputTokens` |
| `OutputTokens` | **全部**输出 token，含推理部分 | 总量 |
| `ReasoningTokens` | 其中的推理 token | `⊆ OutputTokens` |

依据：OpenAI Chat 取自 `completion_tokens_details.reasoning_tokens` 与 `prompt_tokens_details.cached_tokens`（`openai/stream.go:190-199`），Responses 取自 `output_tokens_details` / `input_tokens_details`（同文件 `283-291`）——两者都是**明细字段**，本身已含在总量里。

因此：

- **`Total() = InputTokens + OutputTokens` 保持不变，并补一句注释说明为什么不加另外两项**。今天它没有 bug，只是没有人能从代码里看出这一点，下一个读者仍会想去"修"它。
- **Anthropic 适配器要补齐（C6）**：`cache_read_input_tokens` 与 `cache_creation_input_tokens` 与 `input_tokens` 并列、不含在内，所以要**加进** `InputTokens`，并把 read 部分记进 `CachedTokens`。Anthropic 的 thinking token 计在 `output_tokens` 里且不单列，所以 `ReasoningTokens` 对 Anthropic 保持 0——**这不是缺口，是口径**。
- 两条不变量要有测试（`CachedTokens <= InputTokens`、`ReasoningTokens <= OutputTokens`），否则下一个适配器会再破一次。

一个诚实的副作用要说清楚：Anthropic 补齐之后，用缓存较多的会话报出的 input token 与成本会**变高**——因为今天那些 token 根本不可见。这比现在更接近真实账单，但仍不精确，因为缓存读取的实际单价更低（分级定价见 §8 明确不做）。

### D8：预算剩余进收据，因为它是"展示预算剩余"的唯一可信来源

§8.4 第 2 条要求各 host 展示预算剩余。预算状态今天只活在引擎内（`engine.go:1785-1830`），host 拿不到。收据加一个预算分区（已用 token / 上限、已用 cost / 上限，上限缺失表示无限制），host 直接渲染，不各自重算。子 Agent 与后台 turn 各有自己的账本（RFC-006 D7、RFC-007 D9），**收据报的是产出它的那个引擎的池子**，不做跨池合并。

### D9：旧行不假装能修，迁移时直接删掉

已有 usage 行是累计快照，且没有 sample 边界。一个 turn 有多次调用时，"只留最后一行"会漏掉前几次调用，"全部相加"就是今天那个翻倍的错数——**旧数据无法精确重建**。因此迁移 `DELETE FROM usage`，聚合从升级点重新开始。

代价是清楚的：升级前的历史聚合读数没了。可以接受的理由是**原始事件没丢**——usage 事件仍在 eventlog 里，审计链完整，丢的只是一张派生投影表。而保留一张"看起来精确、实际偏高"的表，会让升级后的每一次对账都要先判断这个数字是新的还是旧的。

`usage_turn_context` 不删：它绑定的是 turn 的路由，本身没有被 sample 语义污染。

---

## 4. schema

### v7：usage 可数（T2 已落地）

```sql
DELETE FROM usage;                                              -- 旧行无法重建（D9）

ALTER TABLE usage ADD COLUMN sample INTEGER NOT NULL DEFAULT 0;
ALTER TABLE usage ADD COLUMN cost_known INTEGER NOT NULL DEFAULT 0;
ALTER TABLE usage ADD COLUMN source_sequence INTEGER;           -- 覆盖判据（D1）

CREATE UNIQUE INDEX usage_turn_sample ON usage(turn_id, sample);
```

`event_sequence` 的 UNIQUE 约束保留：它仍然是"这个事件被投影过"的证据，只是不再是行的身份。

实际迁移是整表重建（`DROP TABLE usage` + 新建）而不是逐列 `ALTER`：约束要从 `event_sequence` 唯一改成 `(turn_id, sample)` 唯一，而旧行反正要删（D9），重建比先加列再改索引少一半语句。

原稿在这里还加了 `usage.trace_id` 与 `usage_trace` 索引，**没有实施**：一次 trace 就是一个 turn，`turn_id` 已经是关联键（D5）。

### v8：本地 trace（T4 已落地）

```sql
CREATE TABLE spans (
    turn_id TEXT NOT NULL REFERENCES turns(id) ON DELETE CASCADE,
    span_id INTEGER NOT NULL CHECK (span_id > 0),
    parent_span_id INTEGER,            -- 根 span 为 NULL
    name TEXT NOT NULL,                -- turn / model_call / tool / approval_wait / verify
    started_at TEXT NOT NULL,
    ended_at TEXT,
    duration_ms INTEGER CHECK (duration_ms IS NULL OR duration_ms >= 0),
    status TEXT NOT NULL,              -- ok / error / canceled / open
    attributes_json TEXT NOT NULL DEFAULT '{}',
    PRIMARY KEY (turn_id, span_id),
    CHECK (json_valid(attributes_json))
);
CREATE INDEX spans_turn_started ON spans(turn_id, started_at);
CREATE INDEX spans_name ON spans(name);
```

与原稿的三处不同，都是"一次 trace = 一个 turn"的推论：

- **没有 `trace_id`**，主键是 `(turn_id, span_id)`；
- **`span_id` 是 turn 内自增序号**（不是随机 id），所以 trace 按发生顺序读出，测试也能指名某个 span；
- **不再冗余存 `session_id` / `thread_id`**：`turn_id` 一路 CASCADE 到 session，删会话自动带走 span；两列都能从 `turns` join 出来，存两份就要防它们和 `turns` 说的不一样。

`status` 多出一个 `open`：turn 崩在半路时，span 拿到结束时间（于是有时长）但保留"没人正常关掉它"的状态，比补一个 `ok` 诚实。

---

## 5. 要改的既有断言

| 位置 | 现在断言什么 | 改成什么 |
| --- | --- | --- |
| `observability/usage/repository_test.go` | 多个 usage 事件的 `SUM` 是总量 | 同 sample 覆盖、跨 sample 求和 |
| `runtime/app/receipt_test.go` | `CostKnown` 由 `CostUSD > 0` 得出 | 由 `pricing.Known` 得出；免费但已知价格报 `known=true, 0` |
| `host/runtimeapi/http/serve_contract_test.go:82-84` | `/v1/usage` 只有 `input_tokens` | 四类 token + cost + `cost_known` 两个计数 |
| `host/cli/ops_test.go` | `metrics` / `scorecard` 读 JSON 文件 | 读 SQLite；文件模式作为显式降级保留 |
| `provider/anthropic/stream_test.go` | 只断言事件类型 | 断言 input 含 cache read/creation、`CachedTokens` 填值 |
| `docs/protocol/runtime-protocol.schema.json` + `runtimeapi/contract` | 当前字段集 | 新字段同步（C4） |

---

## 6. 分片顺序

| 分片 | 内容 | 完成的判据 |
| --- | --- | --- |
| T1 | 协议与收据：`sample` / `provider` / `model` / `cost_known`、延迟分区、预算分区；schema 与双 host 契约同步 | `make protocol-schema` 与 `make protocol-contract` 干净，字段有生产者 |
| T2 | 落库修正：schema v7（含旧行清空）、分区覆盖、`cost_known`；`/v1/usage` 与 CLI 读真实库 | Anthropic 两事件一次调用聚合出正确读数（回归测试） |
| T3 | 延迟采集：首 token、provider、tool、approval 等待、verify | 一个带审批的 turn 的收据里五项都有值，无审批时该项为 0 而非缺失 |
| T4 | 本地 trace：span 接口、`spans` 表、按 turn 查回 span 树 | 从一个 turn 的 trace 能查到它的 provider 调用与工具耗时 |
| T5a | SQLite 读面：`metrics` / `scorecard` 转查库、TUI cost 面板、HTTP 的 span 树与 usage 汇总 | 两个命令不再需要 `--file` 也能报账；三个面的金额都过同一个格式化函数 |
| ~~T5b~~ | ~~OTLP opt-in（依赖 egress broker）~~ | **取消**（D5）：本地 spans 已经够用，导出器服务的是集群级场景 |

T2 先于 T3 是因为**先把错的数字改对，再加新的数字**：在错的聚合上叠延迟，只会让排查时多一个变量。

**T3 与 T4 合并成一个分片实施。** 原因是它们量的是同一段时间：如果 T3 先铺一套计时变量、T4 再铺一套 span，同一段墙钟会被量两次，两处还会慢慢对不上。落地形态是**一个 per-turn span 收集器**（`trace.Recorder`），收据的延迟分区是它的聚合视图（`Recorder.Latency()`），`spans` 表是它的另一个出口（`trace.Sink`）——两个读者，一个来源。

---

## 7. 测试

hermetic：不联网、不依赖真实时钟长等待。

- **累计聚合**：一次调用发两次 usage（Anthropic fixture 形状），断言表里一行、聚合不翻倍；重放同一事件两次仍等价（C2）；
- **多次调用**：一个 turn 两次 provider 调用，断言两行两个 sample、聚合是二者之和；
- **迁移**：一个装了旧 usage 行的 v6 库升级后，`usage` 为空且能继续正常投影新事件（旧行清空是刻意的，不是 bug，测试要钉住）；
- **unknown 成本**：价格未知的模型 → `cost_known=false`；价格已知且为零 → `cost_known=true, cost=0`；聚合混合两者时 unknown 计数非零；
- **UI 不撒谎**：HTTP DTO 与 TUI 渲染在 unknown 计数非零时不输出 `$0.00`（字符串断言）；
- **token 归一化**：Anthropic fixture 带 `cache_read_input_tokens`，断言 `InputTokens` 含它；两条子集不变量（`CachedTokens ⊆ InputTokens`、`ReasoningTokens ⊆ OutputTokens`）各有一个断言；
- **延迟**：注入时钟，断言首 token 与审批等待；无审批的 turn 该项为 0；
- **trace**：一个 turn 的 span 树父子关系与 turn/tool 时长对得上（原先还有一条"OTLP 默认关闭时无出网尝试"，随 D5 取消导出器一并去掉——没有导出器就没有这条路径要守）。

---

## 8. 明确不做

- **不做账单系统**：不做跨机聚合、不做配额扣减、不做实时价格拉取。价格以 catalog 为准，覆盖靠现有的 metadata option。
- **不做 cached token 的分级定价**（Anthropic 的 cache 写入 1.25x / 读取 0.1x）。本轮先把口径归一化与 unknown 语义做对；分级定价需要价格表增加两个费率位，属于 RFC-010 的 catalog 变更。
- **不引入 otel SDK 到核心依赖**，且**不做 OTLP 导出器**（D5）。trace 只在本地可查；跨机上报是一条出网集成，等真有读者时从 `spans` 表另起。
- **不给 usage 加新的协议事件面**：`usage` 与 `turn.receipt` 两个事件足够，新增事件面要付双 host 契约的代价（C4）。
- **不做 span 的保留策略与配额**：spans 表会长大，清理规则属于 §8.5 的 retention 议题（实施后仍是缺口，见 §10）。
- **不做 compaction span**，也不把子 Agent 的 span 串到父 turn 上（理由见 §9 第 3 条）。

---

## 9. 未决问题

1. ~~**`sample` 的边界定义**~~（已定：一次 provider 流 = 一个 sample，重试算两个）。重试确实花了那些 token，把它折进上一次会让重试的成本消失；`model_call` span 带 `attempt` 属性，于是"这是第二次尝试"仍然读得出来，不需要额外的 `retry` 位。
2. **子 Agent 与后台 turn 的 usage 归属**。今天子 turn 有自己的 thread/turn，行会落在自己名下。父 turn 的收据是否要报"含子 Agent 合计"，还是只报自己的池子（D8 的取向）？后者更诚实，但用户问"这次任务花了多少"时要跨行汇总，需要 UI 侧给出 thread 树的合计。
3. **run 级 trace**。已定的部分：**一次 trace = 一个 turn**，`turn_id` 就是关联键，没有独立的 `trace_id`（D5、§4 v8）。未定的是 workflow run 跨多个 turn 时要不要一个父 trace——那需要 `spans` 加一列父 trace 键，等 RFC-007 的 run 真有人看它的时序时再扩；先加一个没有读者的列，只会让它一直是 NULL。子 Agent 的 turn 同理：它有自己的 turn 行，所以有自己的 trace，与父 turn 不串联（与上一条 D8 的取向一致）。
4. **旧 `usage_turn_context` 的退役时机**。D3 只降级不删除；真正退役取决于分路路由（RFC-010）落地后是否还有读者。
5. ~~**`ReasoningTokens` 与 `OutputTokens` 的包含关系**~~（已确认：两家都是 output 的子集，见 D7 的依据）。剩下的真问题是第三方 OpenAI 兼容端点是否守同一口径——它们只在 `--base-url` 路径上出现，catalog 管不到，只能在遇到时按 provider 记一条已知偏差。

---

## 10. T3 + T4 的实施结果与偏离

**采集点与谁来量。** turn / `model_call` / `tool` / `verify` 四种 span 都在引擎里开合（`engine.go`、`verify.go`），时钟是新增的 `Options.Now`。`approval_wait` 是唯一的例外：park 与 decision 两个边界都在 `guard.waitForApproval` 内部，引擎只看得见 park 那一头，从外面量出来的是"等人 + 干活"。所以 guard 新增 `SetApprovalWaitObserver`，在 select 返回处报出等待时长与结局（decided / expired / canceled），引擎把它落成挂在对应 tool span 下的子 span。等待从**请求发出前**起算，于是一个慢在展示审批的 host 算作等待而不是免费。

**一个时钟。** 引擎自己构造 guard 时把 `Options.Now` 传下去（`toolguard.Options.Now`）。两个时钟会让一段等待落在容不下它的 tool span 里，而 `ApprovalWaitMS ⊆ ToolMS` 是我们要求读者相信的不变量。

**首 token 的判据是"模型产出了东西"**，与 `consume` 里原有的 `meaningful` 是同一个事件（文本、推理、签名、搜索/引用、工具调用增量），**`EventUsage` 与 `EventMessageStart` 不算**——Anthropic 在 `message_start` 就发 usage，在那里打戳会报出一个还没生成的首 token。

**tool span 从准入之后开始**：排在并发闸门后面的排队时间不是这个工具花的时间，混进去会让一个快工具在闸门繁忙时看起来很慢。

**没有名字的 turn 只在内存里量。** `engine.Run`（测试与内存 runtime 路径）没有 durable turn 行，spans 表的外键无处落，所以它照样量、照样能报延迟，只是不落库——比让写库失败或放宽外键都好。写库失败只记 `Metrics.Error()`，绝不改变 turn 的结果：一条写不进去的 trace 不该改变已经报出去的结论。

**usage 写失败与 trace 写失败不是同一等级。** 工具内部的模型采样（vision / subquery）先进入 turn 内存账本，再通过同一 Runtime sink 发 `usage` 事件。该 emit 失败时 `ToolSampler` 现在会让 stream 返回错误，错误作为不可恢复的工具错误终止 turn；已经消费的 token 仍留在失败 turn 的内存合计里。旧实现 `_ = emit(event)` 会让 turn 成功并生成包含该采样的 receipt，却没有对应 durable usage event/projection，直接破坏 C2/C3 的同源关系。故障注入测试钉住 emit error 必须穿透且 spend 不得被抹掉。

**顺带把 D8 的预算分区一起做了**（`ReceiptBudget`）。它本来在 T1 里，代码量很小，而协议改字段要付 `make protocol-schema` + `make protocol-contract` 的门禁代价（C4）——按批次付一次比下一条线再付一次便宜，而下一条线（读面）要渲染的就是它。引擎的池子只在 turn 完成后才把该 turn 折进去，所以收据把自己这一 turn 的用量加上再报，否则每份收据都会少算自己。

**读面没做，是刻意的。** 本轮只有 `QueryByTurn` 与测试；CLI / HTTP / TUI 的展示属于 T5a 那条线（已落地，见 §11）。提前造一个没人读的 UI，和提前加一个没人读的列是同一种错。

**OTLP 没做，而且不会做了**（D5）：原本是排在 §8.6 broker 之后的 T5b，后来直接取消——本地 spans 已经能回答这条线要回答的问题，一个需要用户自架 collector 的默认关闭导出器服务的是集群级场景。

**已知缺口：spans 没有保留策略。** 只有 turn 被删时 CASCADE 带走它的 span。这与事件日志今天的缺口是同一个（本 RFC §8 明确不做，归 ROADMAP §8.5 的 retention 议题），一个长期运行的库会一直长大。

---

## 11. T5a（SQLite 读面）的实施结果与偏离

**一个折叠层，三个面。** `usage.Rollup` / `usage.Fold` / `usage.FormatCost` 与 `trace.Rollup` / `trace.QueryRollup` 是唯一的读取层，CLI、TUI、HTTP 都只做呈现。这是为了 §8.4 第 6 条那一条规则：unknown 不能显示成 `$0.00`。如果每个面各写一遍格式化，这条规则要守三遍，迟早漏一处。

**偏离一：金额格式化落在 `usage` 包，不落在 host。** `FormatCost(microunits, priced, unpriced)` 与 `PricedCalls` / `UnpricedCalls` 放在同一个文件里——它守的正是那两个计数注释里已经写下的要求，放在不变量旁边，第二个消费者才不容易绕过它。三个答案是三个不同的字符串：全部有价 → `$0.0123`（**已知的零就该是 `$0.00`**），全部无价 → `unknown`，混合 → `$0.0123+ (2 calls unpriced)`（明说是下限），一次调用都没有 → `n/a`。金额用整数算而不是先除成 float：microunits 是存储单位，除完再定宽四舍五入会把 `$0.00015` 悄悄报成 `$0.0001`，一个读起来偏低的成本比不报更糟。币种是 USD 不是假设——wiring 对已知价格只接受 USD（`wire/model.go`）。

**偏离二：跨 turn 的 span 汇总走 join，不给 `spans` 加列。** spans 只有 `turn_id`（一次 trace = 一个 turn，§9 第 3 条），所以 session / thread 汇总走 `spans → turns → threads`，键都在且全 CASCADE。加两列冗余键会多两个要维护的一致性点，而这个规模下 join 的代价可以忽略。分位数与折叠在 Go 里做而不是 SQL：SQLite 算分位数要绕一大圈，而要折叠的行数就是这个 scope 的 span 数。

**未闭合的 span 计次不计时。** `duration_ms IS NULL` 进 `Phase.Calls` 与 `Phase.Unfinished`，不进 `TotalMS`。**`Unfinished` 是独立字段而不是折进 `Errors`**（原计划想合并）：一个没关掉的 span 是"没量完"，不是"失败了"，合在一起会让 "provider errors=3" 变成一句没法据以行动的话。

**`QueryTurnInThread` 用两条语句而不是一条。** 归属校验（`turns.thread_id`）单独一条，因为 span 为空是一个真实答案——一个 turn 可以什么都没值得记就结束了——把两件事折进一条 join 会把它报成"这个 turn 不存在"。HTTP 侧因此能把 404 只留给真正不属于该 thread 的 turn。

**`metrics` 与 `scorecard` 的分工写进了 help，因为它们原本完全一样。** `metrics` 出明细（按 provider/model 一行、按 span name 一行），`scorecard` 出一屏一行一指标。`--file` 保留为显式降级并正名为 **counters**（D6：telemetry 文件保留但不再看起来像账单），`scorecard` 的 `"thin": true` 去掉。scope 内没有 span 时报 `latency: not recorded`，不报零——一个从 v7 升上来的库有 usage 行却没有 span，报零会说那些 turn 是瞬间完成的。

**TUI 的 turn 级数字来自收据，thread / session 级来自库。** 收据是唯一能给出预算池与延迟分区的地方（前者跨 turn、只有引擎知道），而 thread 合计跨越本进程没跑过的 turn，只能查库。`SessionHost.Accounting` 里唯一容易错的一步是 session id：`AttachStore` 拿到的是显示用标签，不是 `sessions` 表的键，所以新增 `state.Store.SessionForThread` 由 thread 反查。读库是同步的，与 `/search`、`/task` 同一个体例——为 `/cost` 单独造第一条异步 slash 通路等于给同一件事留两种写法，而面板本来就有 Enter 刷新。

**预算剩余不新开端点。** 它在 `turn.receipt` 上，HTTP 与 ACP 客户端都已能从事件流拿到。`/v1/usage` 只多一个 `rollup` 对象（含 `cost_known` 与 `cached_share`），服务端仍然返回 microunits 不格式化金额——机器 API 该给机器数字，但"这个数是不是全额"要显式说出来，否则每个客户端都要自己从两个计数推，其中一个迟早推错。

**cached 占比顺带补上了 ROADMAP §8.3 的一半**（cache hit 可观测）：它就是 `CachedTokens / InputTokens` 一次除法，和金额同一个读取层。

---

## 12. 验收

- Anthropic 跑一个 turn，`/v1/usage` 的 input token 与 provider 报的一致（今天是两倍）；
- 一个 turn 两次调用，聚合等于两次之和，且重放事件流不改变读数；
- 价格未知的模型：CLI、HTTP、TUI 三处都显示 unknown，没有任何一处显示 `$0.00`；
- 价格已知的免费模型显示 `$0.00` 且不标 unknown；
- 一个带审批与 verify 的 turn，收据里五项延迟齐全，且能从 trace 里看出时间花在哪一段；
- `metrics` 与 `scorecard` 不给 `--file` 也能报出 token 与 cost；
- 升级一个 v6 库：迁移成功，历史聚合从零开始，新读数精确，eventlog 里的原始 usage 事件未受影响。
