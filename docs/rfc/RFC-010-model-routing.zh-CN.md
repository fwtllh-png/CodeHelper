# RFC-010：ModelRouting

> 状态：T1–T5 均已落地；§8.3 prompt cache 写面见 §16（Chat sticky key + Anthropic `cache_control`）；依赖 [RFC-011](./RFC-011-egress-broker.zh-CN.md) 的 egress Gate

> 关联：[ROADMAP §8.2](../ROADMAP.zh-CN.md)、[ROADMAP §8.3](../ROADMAP.zh-CN.md)、[RFC-008 ExecutionReceipt](./RFC-008-execution-receipt.zh-CN.md)、[RFC-009 ContextLifecycle](./RFC-009-context-lifecycle.zh-CN.md)、[RFC-001 RepoContext](./RFC-001-repo-context.zh-CN.md)
> 影响面：`internal/adapter/model`（catalog、resolver）、`internal/adapter/provider`（`types.go`、`httpclient`、`anthropic`、`openai`）、`internal/adapter/tool/interact`（vision）、`internal/runtime/agent/engine`、`internal/runtime/app/wire`、`internal/config`、`internal/persist/state/sqlite`、`internal/host/cli`

---

## 1. 问题

§8.2 的八条里，代码层面缺的只有一半；另一半是**已经写好却没接上，或者接的方式绕过了所有账本**。

1. **一个会话只有一条 route。** 引擎拿到唯一的 `model.ReadyRoute`（`wire/runtime.go:604-605`），plan / act 的区别只体现在 policy 与 prompt（`execution.mode`），用的是同一个模型。
2. **唯一的第二条 route 是 vision，而它绕过了 provider 抽象。** `interact.RouteVision` 自己拼 HTTP、自己发 data URL 到 `/chat/completions`（`tool/interact/vision.go:66-99`），不走 `provider.Message`、不走 `encodeRequest`。后果是它**没有 retry、没有 idempotency key、没有 usage、没有 cost、没有 trace**——RFC-008 想记的账，对 vision 全是盲区。协议也被写死成 Chat Completions，与会话实际协议无关。
3. **compact 与 verify 今天不调模型。** compact 是本地确定性摘要（`engine.go:1248-1310`，零次 provider 调用），verify 是 shell 与诊断（`Verify.Runner`）。所以"为 compact / verify 分路"这件事**今天没有消费者**——RFC-009 已经说明模型叙事摘要要等 §8.2 的独立 route。分槽位可以先做，但不能假装接上了。
4. **自动路由的代码在，wire 从不启用。** `Resolver.Resolve` 支持 `Auto`（`model/route.go:100-145`），但生产路径总是传显式 `ProviderID`，且 `Auto` 要求"恰好一个 provider 提供该 model"。同时**没有任何"锁定"机制**：配置就是锁，没有"自动挑但这次钉死"的表达。
5. **catalog 描述不了要路由的那些能力。** `Capabilities` 只有 `streaming` / `reasoning` / `tool_calls` / `native_search`，其中 `reasoning` **在运行时没有任何消费者**。没有 vision、没有 structured output、没有 prompt cache、没有 image/file 输入位。于是"按能力选模型"无从下手。
6. **没有 capability probe。** 所有判断来自静态 catalog 或 CLI 传入的元数据；`model list --live` 只列 ID，不问能力。
7. **structured output 完全没有。** 全仓没有 `response_format` / `json_schema`；已有的 JSON Schema 是**工具入参**（`ToolDefinition.InputSchema`），不是模型输出约束。
8. **Responses 协议代码齐全，bundled catalog 里一条都没有。** encode 与 parser 都实现了（`httpclient/client.go:263-308`、`openai/stream.go:204-316`），但 `catalog.v1.json` 里只有 `openai_chat` 与 `anthropic`，所以走 Responses 必须自带 endpoint 与元数据。
9. **retry 的幂等来源是隐式的。** `Idempotent=true` 时最多 3 次 pre-stream 重试（`httpclient/client.go:136-142`），引擎设了它（`engine.go:734`）；而 **RLM subquery 没设**，于是同样安全的调用只有 1 次尝试。stream 开始后不重放，这条是对的，但没写进任何面向用户的文档。

所以本 RFC 的形状是：**先把"一条 route"变成"具名槽位"，把 vision 收回主路径（否则 RFC-008 的账永远缺一块），再按能力而不是按猜测选模型。**

---

## 2. 硬约束

### C1：引擎只持一条 route，per-purpose 不能靠"再起一个引擎"

`agentengine.Options.Route` 是单值，`modelStep` 直接用它。per-purpose 要改的是**引擎按用途取路**，不是每个用途起一个引擎——后者会复制预算、历史与工具面（RFC-006 的教训：一个引擎一份账本）。

### C2：compact / verify 没有模型调用，槽位可以存在，接线不能假装

见 §1.3。任何"已支持 compact route"的说法必须同时有一个真的会调模型的 compact 路径，否则 maturity 与文档就在撒谎。本轮的取向是：**槽位定义在 config 与 resolver 层，act / plan / vision / subquery 四个真的接上，summary（compact 叙事）与 judge（模型 verify）只登记不接**，并在 `diagnostics` 的 maturity 里如实报。

### C3：vision 是 per-purpose 的第一个真实用例，也是 RFC-008 的前提

它已经是第二条 route，只是走了侧路。收敛它同时解决三件事：per-purpose 有真实消费者、vision 的 token 与成本入账、图片进入统一的 content block。

### C4：catalog 有 provenance 与一致性校验

`MetadataProvenance` 逐项记来源，校验禁止 `known=false` 时带非零费率（`catalog.go:200-202`）。新增能力位必须同样带 provenance，否则 bundled 值与 probe 值混在一起将无法追溯"这个 true 是谁说的"。

### C5：probe 是出网行为

任何 capability probe 都要发请求。它必须服从 §8.6 的出网治理（broker 与审批），因此**不能在会话启动时自动跑**——那会让"打开一个会话"变成一次未经审批的出网。

### C6：route 变成多值会让 usage 的归属改变

一个 turn 跨模型之后，`usage_turn_context`（turn → 单一 provider/model）不再成立。RFC-008 D1/D3 已经把 provider/model 下移到 usage 事件，**本 RFC 依赖那一步先落地**，否则分路一开，账本立刻错得更彻底。

### C7：协议契约门禁

route 若进 `turn.receipt` 或 usage 事件，要同步 JSON Schema 与 ACP/HTTP 双 host 契约（ROADMAP §11.3）。

---

## 3. 决策

### D1：route 是具名槽位 + 回落链，不是并列的多个必填项

配置形状：

```toml
[execution]
provider = "anthropic"          # 兼容今天：等价于 route.act
model = "claude-sonnet-4"

[route.plan]                     # 未配置则回落到 act
provider = "openai"
model = "o4-mini"

[route.vision]                   # 取代今天的 [vision]
provider = "openai"
model = "gpt-4o-mini"
```

用途枚举与本轮处置：

| purpose | 是否接线 | 说明 |
| --- | --- | --- |
| `act` | 是 | 主采样。等于今天的 `execution.provider/model`，**未配置任何 route 时行为不变** |
| `plan` | 是 | `mode=plan` 的采样走它；回落到 `act` |
| `vision` | 是 | 收敛 `interact.RouteVision`（D5） |
| `subquery` | 是 | RLM 子查询；回落到 `act` |
| `summary` | **只登记** | compact 的模型叙事摘要还不存在（C2） |
| `judge` | **只登记** | 模型 verify 还不存在（C2） |

回落链是"具名槽位 → `act`"，只有一级，不做多级继承。理由：两级以上的继承会让"我为什么用了这个模型"变成一道推理题，而这条线的目的正是让成本可解释。

### D2：自动路由收窄为"按能力过滤"，不做按复杂度猜；锁定是一等公民

§8.2 第 2 条原文包含"根据复杂度、上下文、工具、预算自动路由"。本轮**只做能力过滤**：要图片就必须是有 vision 能力的模型，要工具就必须 `tool_calls=true`，要 structured output 就必须支持它；候选不唯一时**报错而不是替用户挑**。

不做按复杂度自动切换的理由写在明处：**它会让同一个提示在不同时刻花不同的钱、产生不同的行为，而 M3 的整个主题是让账变准、让行为可解释。** 一个会自己换模型的路由器，会把 RFC-008 刚修好的成本归因重新变成谜。

锁定：`--lock-route` / `[route] lock = true` 时，任何回落与自动选择都被禁止，缺槽位直接报错。理由：这是"我在跑一次可复现的实验"的开关，没有它，回落链本身就是不可复现的来源。

### D3：capability 进 catalog；probe 只能收紧，不能放宽

catalog `Capabilities` 增加：`vision`、`image_input`、`file_input`、`prompt_cache`。**每一位都要有当轮就存在的消费者**——这是 `reasoning` 今天沦为死字段的教训，所以 `structured_output` 不在这批里（D4）。同时给 `reasoning` 补上消费者：`reasoning_effort` 只在 `reasoning=true` 时允许，否则请求期报错而不是让 provider 回 400。

probe（`codehelper model probe`，手动触发，C5）把观测结果写进 SQLite，**不改 catalog**：

- probe 说"不支持"而 catalog 说"支持" → **按不支持**（收紧）；
- probe 说"支持"而 catalog 说"不支持" → **仍按不支持**，除非用户显式 `--trust-probe`（放宽要人点头）。

理由是失败代价不对称：错误地放宽会让请求被 provider 打回 400 或静默丢字段，错误地收紧最多损失一个功能，而且用户能从 `model resolve` 里看到是谁收紧的。

### D4：structured output 本轮不做，接口也不预留

§8.2 第 5 条要 `response_format` / JSON Schema 约束输出。**本轮不做**：全仓没有任何调用方需要受约束的模型输出——plan 与工具调用已经通过工具入参 schema 结构化了，compact 是本地派生，子 Agent 结果有自己的协议。

RFC-007 T5 补强后来为 Workflow `response_schema` 增加了**本地结果后置校验**：
production driver 仍发送普通模型请求，完成后要求唯一 JSON value 满足 schema。
这没有新增 `ModelRequest.ResponseFormat`、provider capability 或协议映射，因此不属于
本节所说的 constrained decoding，也不改变本决策。

也不提前加 `ResponseFormat` 字段或 `structured_output` 能力位。理由是这条线上已经有一个现成的反例：`capabilities.reasoning` 在 catalog 里躺了很久，运行时没有任何消费者，于是没人知道它是真的还是抄错的。**没有消费者的能力位不是"为将来准备好"，是一个未经验证的断言。**

真要做时的形状已经清楚（请求字段 + 三协议映射 + 能力缺失时 fail-closed 而不是静默降级成自由文本），等第一个真实消费者出现再开一次分片。

### D5：vision 收敛进主路径，图片成为一等 content block

- `provider.ContentType` 增加 `image` 与 `file`，三个编码器各自映射（OpenAI 的 `image_url` / Responses 的 `input_image`、Anthropic 的 `image` block）；
- `interact.RouteVision` 不再自己拼 HTTP，改为用 `vision` 槽位的 route 调 `provider.Provider`；
- 于是 vision 调用自动获得 retry、idempotency key、usage、cost 与 trace。

这一条是 RFC-008 的前提：**今天 vision 花的钱不在任何账本里**，先修账再修路会漏掉它。

### D6：Responses 进 bundled catalog，但不动默认协议

给 catalog 增加 Responses 形态的 provider 条目（§8.2 第 3 条），`execution.protocol` 的默认值仍是 `openai_chat`。理由：默认值一动就是一次全量行为变更，而 Responses 的价值是"可选得到"，不是"默认切换"。

### D7：幂等来源显式化；stream 中途仍然不重放

- `ModelRequest` 带 `Purpose`，`Idempotent` 由调用方显式给出而不是靠零值；
- 修正 RLM subquery 未设 `Idempotent` 的遗漏（同 body 同 key，安全）；
- **stream 开始后不重放**（现状，`client_test.go` 已钉住）。理由写进文档：已经吐出的 token 无法撤回，重放会让同一个 turn 出现两段互相矛盾的输出；要恢复应当在更上层（引擎重新采样）而不是在 HTTP 层。

### D8：route 进收据与 usage 事件

`turn.receipt` 增加 per-purpose 的路由摘要（哪个用途用了哪个 provider/model），usage 事件按 RFC-008 D1 自带 provider/model。于是"这次为什么花了这么多"可以答到模型粒度（C6、C7）。

---

## 4. schema 与配置变更

```sql
-- 与 RFC-008 的 schema v7 合并为一次迁移
CREATE TABLE provider_capabilities (
    provider_id TEXT NOT NULL,
    model_id TEXT NOT NULL,
    capability TEXT NOT NULL,      -- vision / structured_output / prompt_cache / ...
    supported INTEGER NOT NULL,
    source TEXT NOT NULL,          -- probe / user
    detail TEXT,                   -- probe 的判据（状态码、错误体摘要）
    observed_at TEXT NOT NULL,
    PRIMARY KEY (provider_id, model_id, capability)
);
```

catalog 侧是 `catalog.v1.json` 的能力位扩展（带 provenance，C4）。catalog 版本号是否要升到 v2 取决于是否有外部消费者读它，见 §9。

---

## 5. 要改的既有断言

| 位置 | 现在断言什么 | 改成什么 |
| --- | --- | --- |
| `adapter/model/route_test.go` | 单 route 解析与 `Auto` 唯一性 | 具名槽位解析、回落链、`lock` 下缺槽报错 |
| `adapter/model/golden_test.go` | `ReadyRoute` 的 JSON golden | 能力位扩展后的 golden |
| `adapter/tool/interact/interact_test.go` | `image_analyze` 依赖 `VisionClient` | 依赖 `vision` 槽位的 route；断言走 provider 抽象 |
| `adapter/provider/types_test.go` | `Validate` 按现有四个能力位 | 新能力位与 `reasoning` 的请求期校验 |
| `adapter/provider/httpclient/client_test.go` | 三协议 encode 现状 | image/file block 的编码 |
| `runtime/app/wire/route_test.go`、`model_test.go` | 单 route 的 wire 解析 | 槽位解析与回落，`[vision]` 的兼容映射 |
| `docs/protocol/runtime-protocol.schema.json` + `runtimeapi/contract` | 当前收据字段 | 路由摘要字段（C7） |

`[vision]` 配置段保留为 `[route.vision]` 的别名一个版本周期，不直接删——删掉会让现有配置在升级后静默失去 vision。

---

## 6. 分片顺序

| 分片 | 内容 | 完成的判据 |
| --- | --- | --- |
| T1 | 具名槽位 + 回落链 + `lock`；`[vision]` 兼容映射；route 进收据 | **已落地**（§11）：不配任何 route 时行为不变；配了 `plan` 槽位后 plan 采样走另一个模型 |
| T2 | image/file content block + vision 收敛进主路径 | **已落地**（§12）：vision 调用出现在 usage、cost、收据与 trace 里 |
| T3 | catalog 能力位 + `reasoning` 请求期校验 + 能力过滤式选路 | **已落地**（§13）：要 vision 却挑了无 vision 的模型时在采样前报错 |
| T4 | `codehelper model probe` + `provider_capabilities` + 收紧规则 | **已落地**（§15）：probe 说不支持后采样前被拒；`--trust-probe` 才放宽 |
| T5 | Responses 进 bundled catalog | **已落地**（§14）：不带自定义 endpoint 也能走 Responses；默认协议仍为 `openai_chat` |

T2 排在 T3 之前是因为它是**修账**（vision 的钱现在丢了），T3 之后才是"选得更好"。

---

## 7. 测试

hermetic：全部走 `provider/fixture` 服务器，不联网。

- **回落链**：只配 `execution.provider/model` 时，四个用途解析到同一 route；配了 `plan` 后只有 plan 变；
- **lock**：`lock=true` 且缺 `vision` 槽位 → 采样前报错，不回落；
- **vision 入账**：fixture 跑一次 `image_analyze`，断言 usage 事件与 trace 里有它，且 provider/model 是 `vision` 槽位的（今天这条测试会证明旧实现完全无账）；
- **image block 编码**：三协议各断言一次图片块的 wire 形状；
- **能力过滤**：无 vision 能力的模型配进 `vision` 槽位 → 解析期报错；`reasoning=false` 的模型带 `reasoning_effort` → 请求期报错；
- **probe 收紧**：probe 写入 `supported=0` 后请求被拒；`--trust-probe` 下放行；probe 与 catalog 一致时不改变行为；
- **幂等**：subquery 现在带 idempotency key 且 429 后重试（改现有断言）；stream 开始后仍不重放（保持）。

---

## 8. 明确不做

- **不做按复杂度/预算自动切模型**（D2）。这是对 §8.2 第 2 条的自觉收窄，理由已在 D2 写明。
- **不做跨 provider 的自动 failover**。一个模型失败自动换另一个，会让成本与行为归因彻底失效，且掩盖真实故障。
- **不做 stream 中途重放**（D7）。
- **不接 compact / verify 的模型路由**（C2）：槽位登记，接线等有真实需求。
- **不做 structured output**（D4）：连 `ResponseFormat` 字段与 `structured_output` 能力位都不预留。
- **不做模型质量评估或自动挑选（router 打分）**。没有本地评测集之前，任何打分都是编的。
- **不改 `execution.protocol` 的默认值**（D6）。

---

## 9. 未决问题

1. **`plan` 槽位的边界**。`mode=plan` 是策略与提示，一个 turn 内可能同时有规划与执行（模型自己决定）。按 turn 的 mode 取路最简单，但"turn 内先规划再执行"就分不开。是否需要按采样而不是按 turn 取路，未定。
2. ~~**structured output 的首个消费者**~~（已决定：本轮不做，见 D4）。重开的触发条件是出现一个真的需要受约束输出的调用方——目前最可能的是 compact 的模型叙事摘要（要先接 `summary` 槽位）。
3. **catalog 版本号**。能力位扩展是否要 `catalog.v2.json`，取决于是否有外部读者依赖 v1 的形状；仅内部使用则原地扩展 + provenance 足够。
4. **probe 的判据形状**。用一次最小请求探测（例如发一个带 `response_format` 的极小请求看是否 400）代价低但可能产生费用；用 provider 的元数据端点更干净但各家不一致。倾向前者并把探测成本记进 usage，未定。
5. **`vision` 槽位与 `image_input` 能力的关系**。有 vision 槽位但主模型自己支持图片时，图片应该进主采样还是仍然走 vision 工具？前者更自然，但会让主采样的上下文突然变大且更贵。倾向：主模型支持时也仍然走 vision 工具，除非用户显式打开"原生多模态"，未定。
6. **subagent 的 route 继承**。子 Agent 今天复制父引擎的 route（`childEngineOptions`）。子 Agent 是否应该有自己的槽位（例如便宜模型跑探索型子 Agent），与 RFC-006 的预算隔离是同一类问题，未定。

---

## 10. 验收

- 不配任何 `[route.*]` 的会话，行为与本 RFC 之前逐字节一致；
- 配了 `[route.plan]` 后，plan 采样在 usage 里以另一个模型出现，收据能说出每个用途用了谁；
- 一次 `image_analyze` 的 token 与成本出现在 usage 与 trace 里；
- 把无 vision 能力的模型配进 `vision` 槽位，会在采样前报错而不是被 provider 打回；
- `lock=true` 时缺槽位报错，不静默回落；
- probe 说不支持后请求被收紧，`--trust-probe` 才放宽；
- 不带自定义 endpoint 也能选到 Responses 协议的模型。

---

## 11. T1 实施结果与偏离

落地面：`adapter/model`（`purpose.go`、`routeset.go`）、`config`（`[route.*]`）、`runtime/app/wire`（`routeset.go`）、`runtime/agent/engine`（按 turn 取路）、`runtime/protocol` 与 `runtime/app/receipt.go`（路由摘要）、`host/cli`（`--lock-route`）。

### 已落地

- **`model.Purpose` 与 `model.RouteSet`**。六个用途按 D1 登记，`Wired()` 区分"接线了"与"只登记"。`NewRouteSet` 拒绝把未接线的用途（`summary` / `judge`）当槽位，也拒绝把 `act` 当槽位——act 就是 `execution.provider/model`。`For(purpose)` 实现一级回落；`lock` 下缺槽位返回错误而不是回落。
- **配置**。`[route.plan]` / `[route.vision]` / `[route.subquery]` 与 `[route] lock`，全段 trusted-only（与 `execution.provider` 同理：仓库内配置不得改变往哪发请求）。半个槽位（只给 provider 或只给 model）在 `Validate` 阶段报错。`[vision]` 仍然可用，作为 `[route.vision]` 的别名折叠进来，provenance 保留原来的字段名，于是 `config show` 说得出这条路由是谁给的。
- **引擎按 turn 取路**。`SnapshotTurnContext` 在 turn 开头把 `policy.Mode` 映射成 purpose 并解析出该 turn 的 route，冻结在 `TurnContext` 里；引擎的采样、pricing、`MaxOutputTokens` 与上下文上限都改读这条 route。路由错误（`lock` 缺槽）在到达 provider 之前就让 `StartTurn` 失败。
- **RLM subquery**。`RouteSubQuery` 带上 `Unavailable`，于是"因为 lock 而没有 subquery 路"这件事是一条能读的错误而不是静默退化；同时按 D7 显式设 `Idempotent: true`。
- **收据**。`turn.receipt` 增加 `routes`（purpose / provider / model），由 `Preparing` 事件累积，protocol schema 与双 Host 契约同步。
- **maturity**。`model_route` 报 `partial`，理由写在里面：`summary` / `judge` 只登记未接线。

### 偏离

1. **`mode=operate` 归到 `act` purpose，不单独设槽位。** operate 与 act 的差别是权限与提示，不是"该换个模型"；给它一个槽位只会多一个没人配的旋钮。§9.1 的"按采样而不是按 turn 取路"仍然未决，本轮按 turn。
2. **新形状里"配了 vision 槽位"就是开关本身，没有第二个 `enabled`。** `image_analyze` 现在按 `[route.vision]` 是否存在注册；`[vision] enabled=true` 连同 provider/model 会折叠成这个槽位，于是旧配置行为不变。别名是单向的（`[vision]` → `[route.vision]`），不回写 `vision.enabled`：`config show` 应当显示用户实际写下的东西，而不是被工具倒填出来的字段。
3. **槽位只能落在 bundled catalog 或 fixture 上。** 槽位只说 provider 与 model，而 `--base-url` 会话的元数据（endpoint、协议、限额、费率）是给主模型一个人准备的，因此这类会话拒绝第二个模型而不是猜元数据。fixture 会话则强制槽位仍走 fixture provider：否则一个号称 hermetic 的测试会安静地拨出去，把它的核心断言变成假话。
4. **`purpose` 进了 trace span 与 `Preparing` 事件，没有进 usage 行。** usage 事件已按 RFC-008 D1 自带 provider/model，purpose 可以由 turn 的收据反查；给 usage 表加一列会需要一次迁移，收益只是省一次 join。
5. **子 Agent 继承父会话的整张路由表**（§9.6 未决）。于是一个 plan 模式的只读子 Agent 会走 plan 槽位。这是当前形状的自然结果，不是决策；真要给子 Agent 独立槽位需要连着 RFC-006 的预算隔离一起想。
6. **vision 路由解析失败现在让会话起不来，而不是被吞掉。** 旧实现把解析错误静默忽略，于是 provider 名写错的后果是"一个看不见图片但不报错的会话"。这是一次刻意的行为变更：配了却没生效必须响。
7. **`--lock-route` 只加在 `exec` 上。** 它服务的是"可复现的一次运行"，而 TUI 会话是交互式的；`[route] lock = true` 对两者都有效。

---

## 12. T2 实施结果与偏离

落地面：`adapter/provider`（`ContentImage` 与 `Attachment`、三协议 encode）、`adapter/tool/interact`（`RouteVision` 重写）、`runtime/agent/engine`（`toolsample.go` 与 turn 账本）、`runtime/app`（工具期 usage 事件不再被丢）、`runtime/app/wire`（工具采样器接线）。

### 已落地

- **图片成为一等 content block。** `provider.ContentImage` + `Attachment{MediaType, Data, Name}`，`Validate` 要求非空字节与 `image/*` 的媒体类型——媒体类型写错在这里报，而不是变成一条不说清是哪张图的 provider 400。三协议各自的 wire 形状：Chat Completions 的 `image_url` 数据 URL、Responses 的 `input_image`（与提问同处一个 input item，否则模型看见图却没被问任何问题）、Anthropic 的 `image` + `source.base64`。**纯文本消息保持原来的字符串 `content`**：请求体就是 prompt cache 的 key，为了少数带图消息把全部流量改成数组形状会让每个已缓存前缀失效。
- **`RouteVision` 走 provider 抽象。** 自拼 HTTP、写死 `/chat/completions`、自己解 `choices[0]` 的那一段删掉了，改成一次普通 `provider.ModelRequest`（`Purpose: vision`、`Idempotent: true`）。于是它一次性拿到 retry、idempotency key、并发与限流、错误分类，协议也跟着 route 走而不再假设 Chat Completions。
- **工具采样进 turn 的账本。** 新的 `engine.ToolSampler` 是一个 `provider.Provider` 装饰器：归属信息走引擎运行工具时注入的 context（不是装饰器上的字段——一个 registry 服务整个会话的所有 thread，字段只能指向一个 turn），于是每次工具采样会**领一个本 turn 的 sample 号、开一个挂在该工具 span 下的 `model_call` span、把 usage 以累计语义发成引擎事件、并按自己模型的价目把成本折进 turn**。
- **成本不再被错价。** turn 自身的采样与工具的采样分开累计：token 合成一个完整的 turn 总量（预算与收据因此数得对），钱各按各自模型的价目换算后再相加。`CostKnown` 变成"两边都有价目"的与。`InputTokenDelta` 仍只对自身采样算——估的是自己那几次请求，把工具的 input 折进来会让一次完美的估算看起来是错的。
- **工具期的 usage 事件不再被丢。** `RunningTools` 分支以前只认 tool output / start / result，一条工具采样的 usage 事件走到底会被 `return nil` 吃掉。现在它照常变成 `usage` 协议事件，于是 SQLite 的 usage 行、`codehelper usage`、TUI 成本面板、HTTP 读面全都自动包含它——读面一行代码没改。
- **收据的路由摘要覆盖工具。** `observeRoute` 不再只看 turn 开头那条事件：任何带 purpose 的事件都参与，于是 `turn.receipt.routes` 里会出现 `vision` 用了谁。
- **subquery 顺手一起入账。** `rlm.RouteSubQuery` 也改走采样器。它和 vision 是同一个洞的两半，只补一半会让账仍然是错的。

### 偏离

1. **只做 image，不做 file。** RFC 原文写的是"image/file content block"。但今天没有任何调用方会发文件块——`--file` 是把内容当文本注入提示，vision 工具只发图片。一个没有消费者的 file 块等于对三家 provider 的 wire 形状做三条无人验证的断言，这正是 D4 拒绝 structured output 的同一条理由。出现真实调用方时再加。
2. **`--attach-image` 仍然走 `image_analyze`，没有把图片塞进主采样的用户消息。** 主模型能不能看图要等 T3 的能力位；在没有能力校验之前把图片直接送进 act 采样，只会把"配置不当"变成一次 provider 400。所以本轮收敛的是**通路**（provider 抽象、编码、账本、trace），图片进主 turn 的用户消息留给 T3。§9.5 那条未决也因此仍然未决。
3. **usage 行仍然不带 purpose 列。** 沿用 T1 偏离 4：usage 事件自带 provider/model，收据自带 per-purpose 路由摘要。代价写清楚：如果 `vision` 槽位指向的模型与 act 相同（例如 fixture 会话），rollup 里这两类调用无法区分——但同模型同价目，区分它们只影响归因而不影响金额。
4. **`Purpose` 在 `ModelRequest` 上但不发给 provider。** D7 要求请求带 purpose；它是本地归属字段，`json:"purpose,omitempty"` 只影响记录，`encodeRequest` 不会把它写进任何 provider 的请求体。
5. **`Provider ⊆ Total` 这条延迟不变式松动了。** `model_call` span 现在也可能出现在工具里，而工具是并发的，所以 provider 时间既会与 tool 时间重叠，也可能在多个工具同时采样时超过 turn 的墙钟。这是真实情况，不是记错：注释与 RFC-008 的不变式一并改了口径，而不是把重叠的时间藏起来。
6. **没有账本时采样照样放行。** `ToolSampler` 在 context 里找不到账本（引擎之外的调用方、测试夹具）时直接透传。它是会计而不是闸门：把"跑在别的宿主里"变成一次工具失败，比少记一笔账更糟。
7. **失败的工具阶段也收账。** `runTools` 返回错误时仍然先把工具花掉的 token 收进 turn——已经买下的 token 不会因为工具后来失败而退回来。

---

## 13. T3 实施结果与偏离

落地面：`adapter/model`（`Capabilities` 扩展、`capability.go`、`RouteRequest.Require`、`RouteSet` 校验）、`adapter/provider`（`ModelRequest.Validate`）、`runtime/app/wire`（槽位解析带 Require）、`catalog.v1.json`。

### 已落地

- **能力位进 catalog。** `Capabilities` 增加 `vision` / `image_input` / `prompt_cache`。每一位都有当轮消费者：`vision` 由 vision 槽位与 `PurposeVision` 请求校验；`image_input` 由带 `ContentImage` 的请求校验；`prompt_cache` 由 Responses 协议下非空 `PromptCacheKey` 校验。bundled catalog 里确知支持多模态的模型标了 `vision`+`image_input`（`gpt-4.1`、`claude-sonnet`、`gpt-4o`、`gemini-2.0-flash`），OpenAI 族与 Anthropic 标了 `prompt_cache`。
- **`reasoning` 终于有消费者。** `ModelRequest.Validate` 在 `reasoning_effort != ""` 且 `capabilities.reasoning == false` 时拒绝请求。这是 D3 写明的那条教训的兑现：能力位没有消费者就等于未经验证的断言。
- **能力过滤式选路。** `PurposeRequiredCapabilities(vision) = [vision]`；`NewRouteSet` 拒绝把无 vision 的模型配进 vision 槽位；`For(vision)` 回落到无 vision 的 act 时同样拒绝；`Resolver.Resolve` 接受 `Require`，显式解析与 Auto 都按它过滤（Auto 在数候选之前就丢掉缺能力的 provider，于是"唯一能看图的那家"不会被另一家无能力的同名模型挤成"找到了两个"）。wire 的 `resolveSlotRoute` 把 purpose 的 Require 传进 Resolve，于是错误在会话启动时报，而不是第一次 `image_analyze`。
- **请求期双保险。** 即便路由层漏了，带着图片块或 `Purpose: vision` 的 `ModelRequest` 也会在 `Validate` 被拦下。

### 偏离

1. **不做 `file_input`。** D3 原文列了它，但 T2 已经以"没有消费者"为由推迟了 file content block。给一个没有 ContentFile 的能力位标 true/false，就是再造一个 `reasoning` 式的死字段。等 file 块有真实调用方时再加。
2. **`--attach-image` 仍然走 `image_analyze`，不进主采样。** §9.5 未决仍在：主模型有 `image_input` 时是否原生多模态。T3 只提供了判断所需要的能力位与校验，没有打开开关——打开它是一次产品决策，不是能力位落地的附带动作。
3. **catalog 的 vision 标注是保守的白名单。** 只标了确知支持的四个；其余默认 `false`。漏标的代价是"配了会在启动时报错，用户可以换一个标了的或等 catalog 更新"；错标的代价是 provider 400。按 D3 的不对称原则取前者。
4. **`prompt_cache` 在任意协议上对显式 key fail-closed；会话 sticky 按能力省略。** `Validate` 拒绝「非空 `PromptCacheKey` + `prompt_cache=false`」；引擎经 `StickyPromptCacheKey` 在无能力时丢弃会话默认 sticky key——否则自定义 endpoint（默认能力不含 `prompt_cache`，如 DeepSeek）整轮起不来。Chat Completions 与 Responses 的 encode 都只在有能力时发出 `prompt_cache_key`（§8.3 写面）。
5. **不做"按能力自动挑一个模型"。** `Require` 是过滤器不是选择器：Auto 仍然要求 model id 唯一匹配。D2 拒绝的复杂度路由不会借能力位的名义溜回来。

---

## 14. T5 实施结果与偏离

落地面：`adapter/model/catalog.v1.json`（`openai-responses` provider）、`host/cli`（`model resolve` 输出 `protocol=`）、相关单元与 CLI 测试。

### 已落地

- **bundled catalog 增加 `openai-responses`。** 与 `openai` 共用 endpoint（`https://api.openai.com/v1`）与凭证（`OPENAI_API_KEY`），协议为 `openai_responses`，模型 `gpt-4.1` 标了 `vision` / `image_input` / `prompt_cache`。解析不需要 `--base-url` 或自定义元数据：`--provider openai-responses --model gpt-4.1`（或等价配置）即可。
- **默认协议不动（D6）。** `execution.protocol` 仍默认 `openai_chat`。选 Responses 是显式选 provider，不是悄悄切换默认路径。
- **catalog 会话的协议来自 provider 条目。** 内置条目自带 `protocol`；`--protocol` flag 主要服务自定义 endpoint 会话，不会把 `openai` 条目改写成 Responses。
- **Auto 对 `gpt-4.1` 故意歧义。** chat 与 Responses 两个 provider 共享同一 model id，`Auto: true` 必须报"恰好一家"失败，而不是替用户猜协议——这是 D6 的可测后果。
- **encode 路径可证明。** catalog 解析出的 Responses route 经 `encodeRequest` 走 `/responses`，请求体用 `input` 而非 `messages`，并带上 sticky `prompt_cache_key`。
- **CLI 读面。** `codehelper model list` 列出 `openai-responses`；`model resolve` 文本与 JSON 都说出 `protocol=openai_responses`。

### 偏离

1. **不迁默认协议。** D6 原文就是"可选得到，不是默认切换"。把默认改成 Responses 是一次全量行为变更，本轮明确不做。
2. **不删也不合并 `openai` 条目。** 两条并存：想要 Chat Completions 仍选 `openai`，想要 Responses 选 `openai-responses`。合并成"一个 provider 两个协议"会让 Auto 与收据归因更糊。
3. **Responses 条目目前只挂 `gpt-4.1`。** 其它 OpenAI 模型仍可通过自定义 endpoint 走 Responses；bundled 白名单先覆盖 T3 已标能力的那条主路径，避免未经验证的第二套能力断言。
4. **`prompt_cache` 写面已扩到 Chat Completions 与 Anthropic（§8.3）。** T5 当时只保证 Responses catalog 路径能发出 sticky key；后续 §8.3 让 Chat Completions 在有能力时同样发 `prompt_cache_key`，Anthropic 稳定前缀发 `cache_control`。本分片的验收仍只钉 Responses。

---

## 15. T4 实施结果与偏离

落地面：`persist/state/sqlite` schema v9（`provider_capabilities`）、`adapter/model`（`ApplyProbe` / `CapabilityRepository`）、`runtime/app/wire`（`ProbeModelCapabilities`、会话 overlay、`--trust-probe`）、`host/cli`（`model probe`）。

### 已落地

- **手动 probe，不出在会话启动。** `codehelper model probe --provider --model --data-dir` 经 egress Gate 发最小请求（vision：1×1 PNG；reasoning：`reasoning_effort=low`；可选 `prompt_cache`），结果写入 SQLite，不改 catalog。
- **收紧默认、放宽要点头（D3）。** `ApplyProbe`：`supported=0` 清掉能力位；`supported=1` 只有 `--trust-probe` 才置位。wire 在 `resolveRouteSet` 之后叠加观测；vision 槽位因此会在会话启动时报错。
- **vision 探测同时记 `image_input`。** 同一次多模态请求覆盖两位。
- **schema v9。** RFC 原文写与 v7 合并；当时已到 v8（spans），本轮进 v9。

### 偏离

1. **探测成本不记 usage。** §9.4 倾向记进 usage 未决；本轮 CLI 探针没有 turn/session 账本可挂，只把判据写在 `detail`。
2. **未探测的能力保持 catalog。** 没有观测行就不改；不会把"没探过"当成"不支持"。
3. **`--trust-probe` 只在 exec（及同路径的 wire 选项）。** 不单独做成配置段——放宽是一次运行的显式选择。

---

## 16. §8.3 Prompt Cache 写面（M3 收尾）

落地面：`adapter/provider/httpclient`（Chat `prompt_cache_key`、Anthropic system blocks + `cache_control`）、`adapter/provider`（`StickyPromptCacheKey` / `Validate` 按能力统一）。

### 已落地

- **Chat Completions** 在 `Capabilities.PromptCache` 为真时发送 sticky `prompt_cache_key`（与 Responses 同一门禁）。
- **Anthropic** `system` 改为 text block 数组：第一个非 system 角色之前的消息为稳定前缀，末块在有能力时带 `cache_control: {type: ephemeral}`；history 之后的 turn 尾块不加断点。
- **`StickyPromptCacheKey` / `Validate`** 对任意协议：无能力则省略 sticky / 拒绝显式 key。

### 明确后置

- `summary` / `judge` 独立预算（无模型消费者，见 ROADMAP §8.3）。
