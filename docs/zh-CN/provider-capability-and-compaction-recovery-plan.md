# Provider 能力与 Compaction 修复交接

## 状态

- 当前 Runtime 已由用户暂停，不要直接恢复旧 Turn。
- 当前工作树包含多组未提交变更，不要整体回退，也不要覆盖无关修改。
- 阶段一 Provider Metadata Contract 已在当前工作树实现并通过独立审查。
- 阶段二 Compaction 事务与 continuation 生命周期已在当前工作树实现，最终独立审查、
  race、ratchet 和全仓回归已经完成；真实长 Turn 验证尚未执行。
- 本文把问题拆成两个独立阶段：
  1. 自定义 Provider 能力来源。
  2. Hybrid Compaction 与 Provider continuation 生命周期。
- 必须先完成并验证阶段一，再开始阶段二。

## 不可违反的原则

1. 不允许猜测 Context、Output、Token Budget、模型档位或其他能力阈值。
2. 能力值必须来自：
   - 权威 Provider/模型发现；
   - 内置版本化 Model Catalog；
   - 操作员显式配置。
3. 未知能力必须显式拒绝，不能静默使用“常见默认值”。
4. `adaptive delegation` 已完成且与本问题无因果关系，必须保留。
5. 已验证的 reasoning replay、HTTP 400 terminal semantics、429 cooldown retry、
   同 Session recovery 等修复不得因本次重构被整体撤回。
6. Compaction 必须是原子替换：durable commit 前不能改变 live 或 terminal history。
7. Logical Sample 与 Provider Transport Attempt 必须是两个不同的生命周期概念。

## 已确认的事实

当前 Session 使用：

```text
provider = openai-compatible
model = deepseek-v4-flash
base_url = https://token.sensenova.cn/v1
protocol = openai_chat
```

这不是内置 `deepseek-v4-flash` Provider Route。虽然 Model ID 相同，但 Endpoint、
能力、限制和协议行为都可能不同，不能借用内置 DeepSeek Catalog 的数值。

线上 Turn 曾报告：

```text
window_hard_input_tokens = 119808
max_output_tokens = 8192
```

这两个值来自：

```text
128000 - 8192 = 119808
```

而不是 SenseNova 返回的模型能力。

## 问题一：自定义 Provider 伪造固定能力

### 根因

`internal/host/web/setup.go` 为所有自定义 OpenAI-compatible Endpoint 固定声明：

```go
customContextTokens   = 128_000
customMaxOutputTokens = 8_192
```

`setupModelMetadata` 又固定声明：

```text
streaming,tool_calls
```

`internal/runtime/app/wire/model.go` 将这些数据当作
`model.ProvenanceStartup` 的权威模型元数据。随后：

- `ResolveCapacity` 计算出 `HardInputTokens = 119808`；
- 每个请求最多发送 `8192` output tokens；
- Reasoning、Reasoning Effort、Prompt Cache、Vision 等能力被错误关闭；
- Compaction 阈值建立在错误容量上。

### 影响

- 对真实容量更大的模型产生无依据限流和频繁 `max_tokens` continuation。
- 对真实容量更小的模型可能发送超限请求。
- Provider 相同 Model ID 在不同 Endpoint 上被错误视为同一种能力。
- 日志把猜测值标记成 `model_capability`，污染调度和问题定位。

### 目标设计

为自定义 Provider 引入显式的 `ModelMetadata` 契约：

```text
canonical_id
wire_id
context_tokens
max_output_tokens
capabilities
reasoning_efforts
metadata_provenance
```

能力来源按以下顺序解析：

1. Provider 返回经过校验的能力描述时，记录为 `provider_discovery`。
2. Endpoint 与内置 Provider 身份完全匹配时，可使用版本化内置 Catalog。
3. 其他自定义 Endpoint 要求用户显式提供模型元数据，记录为
   `operator_config`。
4. 任一必需字段未知时拒绝构造 Route。

仅通过 `/models` 返回 Model ID 不足以证明 Context 或 Output 限制，不能据此补默认值。
同名 Model 也不能跨 Endpoint 继承能力。

### 实现范围

- 扩展 Web Setup Request、Selection 和持久化结构，保存非敏感 Model Metadata。
- UI 为自定义 Endpoint 提供模型能力配置，不对字段预填猜测值。
- `setupModelMetadata` 删除 `128000/8192` 和固定 capability 集合。
- `wire.resolveModelMetadata` 保持完整校验，并保留来源信息。
- Session/Turn Snapshot、Usage 和 UI 明确展示能力来源。
- 不引入兼容性猜测迁移；旧的无来源自定义 Selection 应进入 Setup Required。

### 阶段一实现状态

当前工作树已经：

- 删除 Web Setup 的固定 `128000/8192` 与固定 capability；
- 要求自定义 Endpoint 和内置目录外 Model 提交完整、结构化 metadata；
- 将 metadata 与 `operator_config` provenance 持久化到版本 2 Setup Record；
- 让旧版无 metadata 的自定义 Selection 返回 Setup Required；
- 按 Provider、Endpoint、Protocol、Adapter 生成 Connection Identity，隔离 probe；
- probe 进一步按 Provider Wire ID 隔离，并在来源混合时标记 `mixed`；
- 删除未声明 reasoning efforts 时的隐式档位；
- 要求所有 capability 布尔字段显式出现，并校验 reasoning、cache 和 protocol 依赖；
- 将 provenance 投影到 Session Model Capabilities、Turn、Usage、Receipt 和 UI，
  Usage 通过 SQLite v4 持久化且 summary 调用独立记账；
- 兼容恢复不含 metadata 的旧 Receipt，新生成的 Receipt 必须记录 route provenance；
- Provider 重配凭证采用 Stage、Activate、Commit/Restore，失败和崩溃恢复不会提前
  删除旧 secret，也不会让未提交的 staged secret 生效；
- Usage 明细分页不再截断 scope rollup；
- 禁止固定 Connection 在 Session 内切换到没有 metadata 的 Model。

两轮独立审查发现的阶段一遗留项已在当前工作树修复；阶段二不再依赖阶段一的临时
兼容路径。

### 阶段一测试

必须覆盖：

1. 自定义 Endpoint 未提供必需 metadata 时拒绝启动。
2. 显式提供的 Context/Output 精确进入 `TurnSpec` 和请求。
3. Reasoning 与 Reasoning Effort 按声明启用，未声明时拒绝。
4. 同 Model ID、不同 Endpoint 不共享能力。
5. `HardInputTokens = ContextTokens - OutputCeiling` 使用真实元数据计算。
6. UI 保存、刷新、重启后 metadata 与 provenance 不丢失。
7. 边界校验：
   - Context 和 Output 必须为正；
   - Output 不得大于 Context；
   - Reasoning Effort 必须属于声明集合；
   - 非法或不完整 capability 拒绝。

阶段一验证通过前，不修改 Compaction 策略。

## 问题二：Hybrid Compaction 事务与生命周期混乱

### 已确认缺陷

#### 1. Candidate 在 commit 前改写 live history

`compactHistoryWithPolicy` 当前先执行：

```go
*history = selected.History
```

之后才生成 Narrative 并提交 durable rebase。Generation、kernel effect 或 commit
失败时，当前 Turn 已经使用半完成的 deterministic history。

#### 2. 失败 Turn 可覆盖已成功提交的 rebase

`finalizeTerminalContext` 无法区分：

- 仅 prepared、尚未提交的 candidate；
- 已 durable commit 的 compaction。

失败路径恢复旧 `e.history` 和旧 compaction state，可能用更高 revision 覆盖刚提交的
rebase。

#### 3. Fence 没有绑定具体 history

当前只检查：

- `TargetWindowID`；
- 首消息中存在任意 Truth Capsule。

没有完整核对：

- `SourceContextDigest`；
- Authority Digest；
- Removed Message IDs；
- Tail Message IDs；
- Deterministic Result Digest。

因此同一 Window 内 history 变化后，旧 Narrative 仍可能应用到错误 Tail。

#### 4. Summary Route 不可用时可绕过 tool-heavy fail-closed

`PrepareCompactionState` 在构建 `NarrativeInput` 前处理 `RouteFailure`。此时还不知道被
裁剪内容是否包含 Tool Result，也没有 `RequiredKinds`。后续 fallback 可以提交
Truth-only rebase，违反“tool-heavy checkpoint 缺少工作记忆时不得提交”的约束。

#### 5. Required Kinds 推导过宽

当前只要 removed 区间存在任意 Tool Result，就强制要求：

```text
current_work
file_and_code
next_step
```

已完成的只读查询不一定存在 current work 或 next step。诚实 Summary 会被拒绝，
不诚实 Summary 则只能编造未完成工作。Required Kinds 应来自权威未完成状态和工具
语义，而不是仅根据 `RoleTool`。

#### 6. `max_tokens` continuation 没有 quiescent boundary

429 retry 会把 Provider Effect 恢复为 requested 并清除 `ActiveSampleID`。
`max_tokens/incomplete` continuation 则直接进入下一次 transport，仍保持 active
sample。

下一轮先运行 Compaction Gate；一旦需要 semantic compaction，kernel 会拒绝：

```text
context compaction requires a quiescent sample boundary
```

#### 7. 完整 Assembly 的崩溃恢复路径不完整

如果完整 Provider Response 已 checkpoint，但 `ModelSampleResultReceived` 尚未持久化：

1. 重启后 Provider Effect 恢复为 requested。
2. `modelStep` 发现 `ResponseComplete` 后直接返回。
3. 新增的 `beginAttempt` 不会执行。
4. `FinishModelSample` resolve 一个未启动 Effect，返回：

```text
effect has not started
```

#### 8. Continuation 内容不属于可压缩 history

当前 partial reasoning 保存在 `continuationMessages`，而 compaction candidate 只从
`history` 构建。Window Measurement 能看到 continuation，但 candidate 无法裁剪或总结
它。这会出现“压力由 continuation 引起，但压缩只处理旧 history”的不一致。

#### 9. reasoning 豁免基于过滤前 Tool Call

`store_normalize.go` 在孤立 Tool Call 被删除前计算 `hasToolCall`。最终消息只剩
reasoning 时，非 reasoning Route 仍可能收到 reasoning-only assistant message。

### 阶段二实现状态

当前工作树已经：

- 引入 immutable `PreparedCompaction`，冻结 Source/Target Window、Context/History
  Digest、Authority、Removed/Tail Message IDs、确定性结果和 token accounting；
- 在 Prepare、Narrative Generate 和 Validate 期间保持 live history 不变，只在 durable
  Context Rebase commit 成功后一次性应用 Snapshot、Window、Compaction Count 和 History；
- 为 Context Rebase 增加 `BaseRevision` CAS，拒绝基于旧 baseline 的提交；
- 让失败 Turn 从最新 committed context 生成 terminal state，不再回写旧 history 覆盖
  已提交 rebase；
- 使用同一 Provider-visible 投影、Stable Prefix、Tool Definitions、Output Reserve 和
  Token Estimator 验证 candidate 严格缩减；
- 在 Summary Route 不可用时仍完整构造 plan 和 RequiredKinds；存在必须保留的未完成
  工作事实时禁止 Truth-only fallback；
- 根据权威未完成 Todo、Changes 和 Critical Paths 推导 RequiredKinds，已完成的只读
  Tool Exchange 不再被迫生成虚假 next step；
- 仅根据 normalization 后实际保留的闭合 Tool Call 决定 reasoning replay 豁免；
- 将每次 `max_tokens` continuation 的 Provider transport 持久化退回 requested，清除
  `ActiveSampleID`，在下一个 transport 前形成可执行 compaction 的 quiescent boundary；
- 区分 429 transport retry、`max_tokens` continuation 和 logical sample completion；
- 支持完整 Response Assembly 在结果提交前崩溃后直接恢复，不重放 Provider transport；
- 对 partial continuation 单独造成且无法通过 source history 安全缩减的 hard pressure
  返回明确的 Context Maintenance Fault。

两路最终独立审查发现的 RequiredKinds、post-turn commit/adopt 竞态、版本化 replay、
完整 tool assembly Catalog 绑定、硬配额 429 分类、Assembly 防篡改和深拷贝问题也已
修复。聚焦测试、完整相关包 race、`make ratchet-fast`、协议、文档、Book、安全与 Web
检查均已通过。全仓 Go 测试仅剩仓库既有 architecture metrics 超限；真实长 Turn 验证
仍按下文执行，验证前不恢复旧 Runtime。

## Compaction 目标架构

### 两阶段事务

```text
Frozen Source Snapshot
        |
        v
Prepare Candidate
  - source digest
  - authority digest
  - removed IDs
  - tail IDs
  - deterministic result
        |
        v
Generate Checkpoint
  - independent summary route
  - no tools / no native search
  - source citations
        |
        v
Validate
  - exact source fence
  - required semantic facts
  - tool-pair closure
  - real token reduction
        |
        v
Atomic Durable Commit
        |
        v
Swap Live History
```

`Prepare` 和 `Generate` 期间不得修改 `*history`、`e.history` 或 durable snapshot。
只有 durable commit 成功后，才能一次性切换 live state。

### Candidate 数据要求

Candidate 必须携带并验证：

- Source Window ID；
- Source Context Digest；
- Source History Digest；
- Authority Digest；
- Removed Message IDs；
- Tail Message IDs；
- Deterministic Result Digest；
- 原始和候选的 token accounting。

`SourceBytes` 只能作为诊断，不能作为唯一缩减判据。最终应使用同一 Token Estimator、
同一 Stable Prefix、同一 Tool Definitions 和同一 Output Reserve 比较 source 与
candidate。

### Sample 与 Transport 生命周期

状态模型应明确区分：

```text
Logical Sample
  Transport Attempt 1 -> max_tokens
  Quiescent continuation checkpoint
  Optional compaction
  Transport Attempt 2 -> tool_use / complete
```

每个 Transport Attempt 都必须有独立、durable 的 requested/running/finished 状态。
`max_tokens` 不是 Provider retry，也不是 Logical Sample 完成；它应结束当前 transport，
持久化 partial assembly，回到 quiescent boundary，再决定是否启动下一 transport。

禁止继续用 `sampleAttemptActive bool` 表达 durable effect 状态。

### Failure 语义

- Candidate generation 失败且原始 history 仍在 Hard Limit 内：
  - 保留原 history；
  - 记录失败；
  - 下一安全边界重试；
  - 不让业务 Turn 因维护任务失败而立即失败。
- 原始 history 已无法满足 Provider Hard Limit：
  - 不允许提交缺少必要工作记忆的 fallback；
  - 返回明确的 Context Maintenance Fault。
- durable commit 失败：
  - live history 保持原值；
  - 使用 idempotent commit recovery；
  - 不生成更高 revision 覆盖已提交 rebase。

## 阶段二测试

### Compaction 原子性

1. Narrative generation 失败后 history 字节级不变。
2. Context effect 被拒后 history 和 window revision 不变。
3. Durable commit 失败后 live history 不变。
4. Commit 成功、业务 Turn 随后失败时，terminal snapshot 保留新 rebase。
5. 同 Window 不同 history digest 拒绝旧 Narrative。
6. Route 不可用时，tool-heavy candidate 不得 truth-only commit。

### Continuation 生命周期

1. `max_tokens` 后 kernel 的 `ActiveSampleID` 为空。
2. 第二个 transport 启动前允许完成 inline compaction。
3. 429 retry 与 max-token continuation 使用不同状态转换。
4. 完整 assembly checkpoint 后崩溃可无 Provider 重放地提交结果。
5. Partial assembly checkpoint 后崩溃从下一 transport 安全恢复。
6. continuation 自身导致压力时，candidate 能包含或显式处理 partial output。

### Semantic Checkpoint

1. Tool Call/Result 成对进入 source artifact。
2. 未完成代码任务必须保留 file/code、current work、next action。
3. 已完成的只读工具调用不强制生成虚假 next action。
4. 每个 Narrative Item 引用已知 Message ID 和 Digest。
5. Checkpoint 在统一 token accounting 下严格缩减。

### 回归

- reasoning tool-call replay；
- 非 reasoning Route normalization；
- HTTP 400 fail-turn；
- 429 dynamic cooldown；
- failed Turn recovery；
- 同 Session 跨 Thread Continue；
- Provider prefix cache；
- terminal commit recovery。

## 实施顺序

1. 完成 Provider Metadata Contract 和 Web Setup。
2. 运行 Provider、配置、Web 与 capacity policy 测试。
3. 删除固定 `128000/8192`。
4. 冻结现有 compaction 行为，不继续补局部条件。
5. 引入 immutable `PreparedCompaction`。
6. 将 durable commit 与 live swap 合并为唯一成功出口。
7. 重建 Transport Attempt kernel 生命周期。
8. 将 partial continuation 纳入 source snapshot。
9. 修正 Required Kinds 推导与 fallback 语义。
10. 通过 crash matrix、race、focused tests 和真实长 Turn 验证。
11. 用户确认修复后清理调试插桩和调试文件。

## 必须保留的既有成果

- `adaptive delegation` 默认行为。
- Tool-call `reasoning_content` replay。
- Finish-only reasoning effort 按模型能力选择。
- HTTP 400 不再错误重试。
- 429 动态 cooldown 和可取消等待。
- Grep 正则识别与 Search Expression 展示。
- 自定义 listbox。
- 同 Session recovery 与 retained draft 接管。

## 应从本次问题中移出的改动

Builtin Skill、UI 样式、Grep、Plan Gate、Receipt 展示等改动不属于 Provider Capacity
或 Compaction 修复。实现时不要重写或回退这些模块；提交应按功能拆分，避免再次形成
无法审查的大型混合 diff。

## 验证命令

阶段一至少执行：

```bash
go test ./internal/host/web ./internal/runtime/app/wire \
  ./internal/adapter/model ./internal/runtime/agent/context -count=1
npm --prefix web run check
npm --prefix web test
make capacity-policy-check
git diff --check
```

阶段二至少执行：

```bash
go test ./internal/runtime/agent/context \
  ./internal/runtime/agent/engine \
  ./internal/runtime/agent/turnkernel \
  ./internal/runtime/app \
  ./internal/persist/artifact -count=1
go test -race ./internal/runtime/agent/context \
  ./internal/runtime/agent/engine \
  ./internal/runtime/agent/turnkernel
make ratchet-fast
git diff --check
```

最终必须重新部署，通过 Web Continue 执行真实长 Turn，并验证：

```text
Provider metadata source 正确
无固定 128000/8192
max_tokens 后存在 quiescent boundary
checkpoint 包含真实 continuation state
retained tokens < source tokens
checkpoint 后开始文件写入而非重复读取
失败或重启不会覆盖 committed rebase
```

## 下一 Session 的第一步

1. 阅读本文和当前 `git diff`。
2. 不启动旧 Runtime，不点击 Continue。
3. 先确认阶段二最终独立审查、race、ratchet 和全仓回归结果。
4. 所有静态验证通过后再部署新 Runtime，使用 Web Continue 执行真实长 Turn。
