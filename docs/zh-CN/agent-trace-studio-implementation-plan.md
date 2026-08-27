# Agent Trace Studio 技术实现方案

> 状态：提案。本文定义一个可独立交付的 Local-first AI Agent Trace 分析桌面应用。
> 实施前应创建独立仓库；CodeHelper 只作为首个数据生产方，不把分析应用嵌入 Runtime。
> CodeHelper 的 `codehelper.observation-jsonl` v1 导出端点已在本仓库交付，Studio 实施
> 只需实现 Consumer，不再修改 CodeHelper。

## 1. 决策摘要

创建独立项目：

```text
github.com/fwtllh-png/agent-trace-studio
```

产品采用以下技术栈：

- Rust stable：领域模型、流式导入、分析、SQLite 持久化和 Tauri Command；
- Tauri 2：桌面容器、文件选择和受限 IPC；
- React 18 + TypeScript + Vite：交互界面；
- SQLite：本地持久化；
- Vitest + Testing Library + Playwright：Web 行为与视觉验收；
- Cargo Test + proptest：Rust 单元、属性和集成测试。

首个可用版本只做本地 Trace 分析，不调用模型、不上传数据、不执行被分析 Trace 中的
工具命令。它接收版本化的规范化 NDJSON、CodeHelper Observation JSONL 和 OTLP JSON，
将不同来源映射为统一事件模型，再提供时间线、成本、缓存、失败和确定性问题诊断。

首个版本不是通用 APM，也不是新的 Agent Runtime。其价值是回答：

1. 一次 Agent Run 做了什么；
2. 时间和 Token 花在哪里；
3. 是否发生重复工具调用、无效重试、未闭合 Span 或计量矛盾；
4. 两次 Run 的行为和成本为什么不同；
5. 哪些结论有原始证据支持。

## 2. 产品目标与成功标准

### 2.1 目标用户

- 调试 Coding Agent 的个人开发者；
- 开发 Agent Runtime、Tool、MCP 或 Skill 的工程师；
- 需要比较模型、Prompt 或工具策略效果的维护者。

### 2.2 核心用户路径

```text
打开应用
  -> 选择或拖入 Trace 文件
  -> 预检格式、隐私等级和资源需求
  -> 导入并建立本地索引
  -> 在 Run 列表选择一次执行
  -> 查看时间线、Usage 和诊断
  -> 选择另一 Run 进行对比
  -> 导出脱敏 Markdown 报告
```

### 2.3 可衡量成功标准

- 相同输入文件重复导入得到相同 Source Digest、Run ID 和分析结果；
- 导入过程保持流式处理，内存占用不随事件总数线性增长；
- 一个损坏记录不会产生部分可见 Run，用户可以定位失败行；
- Usage 汇总与底层事件逐项求和一致，不把未知成本显示为零；
- 每条诊断都能跳转到对应事件，并显示规则版本和证据；
- 取消导入后不留下可查询的半成品；
- 应用离线可用，默认不监听端口且不发起网络连接；
- macOS、Linux 和 Windows 至少完成构建，macOS 完成首发验收。

## 3. 范围

### 3.1 首发范围

- 文件导入：
  - Agent Trace Studio Canonical NDJSON v1；
  - CodeHelper Observation JSONL v1；
  - OTLP Trace JSON；
- Run Library：搜索、排序、筛选、删除和重新分析；
- Run Detail：
  - 可缩放时间线；
  - Span 树与事件列表；
  - Tool、Model、Approval、Verification 分类；
  - Token、缓存、成本和延迟汇总；
  - 原始事件检查器；
- 确定性诊断：
  - 重复工具调用；
  - 被明确禁止重试后的重复调用；
  - 未闭合、孤立或时间逆序 Span；
  - Usage 重复、回退或聚合不一致；
  - Terminal 之后仍出现业务事件；
- 两次 Run 对比；
- Markdown 报告导出；
- 本地设置与数据删除。

### 3.2 后续范围

- OpenAI Agents SDK、Claude Agent SDK 等版本化适配器；
- 本地 OTLP HTTP Receiver 和实时 Tail；
- 多 Run 趋势图；
- 用户定义诊断规则；
- 脱敏 Bundle 分享；
- CLI 批处理和 CI 回归门禁。

厂商适配器只有在存在稳定格式、版本探测和真实 Fixture 后才能进入支持列表。不得通过
猜测字段含义实现“尽力兼容”。

### 3.3 明确不做

- 不执行 Trace 中记录的 Shell、Tool Call 或 URL；
- 不自动读取 CodeHelper SQLite 或其他应用私有数据库；
- 不上传 Trace 到云端；
- 不在首发版中调用 LLM 生成结论；
- 不提供团队账号、远程同步和权限系统；
- 不把统计相关性描述为根因；
- 不用模型名称猜测上下文容量、价格或 Token 上限；
- 不设置未公开的事件数、文件大小或异常判定阈值。

## 4. 交付边界

Agent Trace Studio 是独立仓库和独立进程：

```text
Trace File / Export Bundle
            |
            v
     Format Detector
            |
            v
   Adapter -> Canonical Event
            |
            v
  Validation -> Import Transaction -> SQLite
            |
            +-> Projection
            +-> Deterministic Analysis
            |
            v
       Tauri Commands
            |
            v
      React Projection
```

与 CodeHelper 的边界：

- CodeHelper 拥有运行事实、隐私准入和 Trace Export；
- Studio 拥有导入、离线分析和展示；
- 二者只共享版本化文件协议，不共享数据库，不互相 Import 内部包；
- Studio 的结论不能修改 CodeHelper Session、Turn 或 Workspace；
- CodeHelper 导出失败不能影响原 Turn 结果。

## 5. 仓库结构

```text
agent-trace-studio/
  Cargo.toml
  Cargo.lock
  rust-toolchain.toml
  package.json
  package-lock.json
  README.md
  LICENSE
  SECURITY.md
  Makefile
  docs/
    zh-CN/
      architecture.md
      import-formats.md
      privacy.md
      development.md
  crates/
    trace-domain/
      src/
        lib.rs
        event.rs
        run.rs
        usage.rs
        finding.rs
        validation.rs
    trace-ingest/
      src/
        lib.rs
        detector.rs
        canonical.rs
        codehelper.rs
        otlp.rs
        pipeline.rs
    trace-store/
      migrations/
      src/
        lib.rs
        repository.rs
        queries.rs
        transaction.rs
    trace-analysis/
      src/
        lib.rs
        projection.rs
        compare.rs
        rules/
  src-tauri/
    Cargo.toml
    capabilities/
    src/
      main.rs
      app.rs
      commands/
      error.rs
      state.rs
  web/
    src/
      app/
      components/
      features/
        library/
        trace/
        compare/
        settings/
      protocol/
        generated.ts
      test/
    index.html
    vite.config.ts
  fixtures/
    canonical/
    codehelper/
    otlp/
    malformed/
  scripts/
```

### 5.1 依赖规则

```text
trace-domain <- trace-ingest
trace-domain <- trace-store
trace-domain <- trace-analysis

trace-ingest -X-> trace-store
trace-analysis -X-> trace-store
all library crates -X-> Tauri
web -X-> Rust implementation details
```

- `trace-domain` 只定义值对象、验证和稳定错误，不执行 I/O；
- `trace-ingest` 把外部格式转换为 Canonical Event；
- `trace-store` 独占 SQL、Migration 和事务；
- `trace-analysis` 只消费 Canonical Event/Projection；
- `src-tauri` 是组合根和 IPC Adapter，不实现分析算法；
- Web 只消费生成的 TypeScript 契约，不拼接 SQL 或读取文件。

## 6. 规范化数据契约

### 6.1 Canonical NDJSON v1

文件首行必须是 Manifest，后续每行是一条 Event：

```json
{"record_type":"manifest","schema_version":1,"producer":{"name":"codehelper","version":"0.1.0"},"exported_at":"2026-08-27T12:00:00Z","payload_mode":"redacted"}
{"record_type":"event","schema_version":1,"event_id":"evt_01","sequence":1,"recorded_at":"2026-08-27T12:00:01Z","kind":"turn.started","identity":{"run_id":"run_01","session_id":"session_01","turn_id":"turn_01"},"trace":{"trace_id":"0123456789abcdef0123456789abcdef","span_id":"0123456789abcdef"},"data_policy":{"class":"operational","redaction":"not_required"},"attributes":{}}
```

Manifest：

```rust
pub struct Manifest {
    pub schema_version: u32,
    pub producer: Producer,
    pub exported_at: DateTime<Utc>,
    pub payload_mode: PayloadMode,
}
```

Event：

```rust
pub struct Event {
    pub schema_version: u32,
    pub event_id: EventId,
    pub sequence: u64,
    pub recorded_at: DateTime<Utc>,
    pub monotonic_ns: Option<u64>,
    pub kind: EventKind,
    pub identity: Identity,
    pub trace: Option<TraceContext>,
    pub causality: Option<Causality>,
    pub data_policy: DataPolicy,
    pub attributes: JsonObject,
    pub payload: Option<PayloadRef>,
}
```

`Identity` 支持：

- `runtime_id`、`session_id`、`thread_id`、`turn_id`、`run_id`；
- `operation_id`、`sample_id`、`call_id`、`attempt_id`；
- `node_id`、`effect_id`、`agent_id`。

`EventKind` 使用开放字符串承载来源扩展，但 Canonical Core Kind 必须版本化：

```text
run.started              run.finished
turn.started             turn.finished
model.started            model.finished
tool.started             tool.finished
approval.requested       approval.resolved
verification.started     verification.finished
usage.recorded
workspace.revised
diagnostic.recorded
```

未知 Kind 可以保存和显示，但不能参与未经定义的聚合。

### 6.2 顺序和身份规则

- `event_id` 在一个 Source 内唯一；
- `sequence` 在一个 Source 内严格递增；
- `run_id` 必填；无法从来源获得时，由 Adapter 使用 Source Digest 和稳定分组键派生；
- 时间统一保存为 UTC RFC3339Nano；
- 同一 Span 的开始和结束通过 `trace_id + span_id` 关联；
- `monotonic_ns` 只在同一 Producer Boot 内比较，不跨来源比较；
- 导入不得改写原始 `event_id`、时间或 Usage；
- Adapter 生成的字段必须记录在 `normalization_receipt`，不能伪装成来源事实。

### 6.3 Usage

`usage.recorded` 的 Attributes：

```json
{
  "provider": "deepseek",
  "model": "deepseek-chat",
  "input_tokens": 1200,
  "output_tokens": 230,
  "reasoning_tokens": 0,
  "cached_tokens": 900,
  "cost_microunits": 140,
  "cost_known": true,
  "currency": "USD",
  "sample": 2
}
```

规则：

- Cached Token 属于 Input Token，不重复计入 Total；
- Reasoning Token 属于 Output Token，不重复计入 Total；
- `cost_known=false` 时不能把缺失成本转换为零成本；
- Studio 首发版不内置模型价格表，只展示来源已给出的成本；
- 后续价格重算必须携带 Catalog Version、币种和生效时间。

### 6.4 Payload

- Manifest 声明 `none`、`redacted` 或 `included`；
- 首发 UI 默认只显示 Summary 和 Attributes；
- Payload 文件按 SHA-256 内容寻址，导入前验证摘要；
- `credential` 和 `restricted` 数据默认拒绝导入 Payload 正文；
- HTML、Markdown 和 ANSI 均视为不可信文本，不允许直接注入 DOM；
- 路径只允许 Bundle 根目录内的相对普通文件，拒绝绝对路径、`..`、链接逃逸和设备文件。

## 7. CodeHelper Observation JSONL v1

CodeHelper 提供只读 `POST /api/v1/trace/export` 下载端点，输出
`application/x-ndjson`。首行是 Manifest，后续每行是一条 Observation：

```json
{
  "record_type": "manifest",
  "format": "codehelper.observation-jsonl",
  "format_version": 1,
  "observation_schema_version": 1,
  "producer": "codehelper",
  "producer_version": "0.1.0",
  "session_id": "session_01",
  "through_sequence": 42,
  "event_count": 8,
  "usage_count": 2,
  "payload_mode": "omitted",
  "summary_mode": "safe_metadata",
  "records_sha256": "<hex>",
  "exported_at": "2026-08-27T12:00:00Z"
}
```

CodeHelper 集成约束：

- Exporter 从 `internal/observability/observation` 的公开 Envelope 投影，不直接导出表；
- Exporter 位于 Observability 所有权边界，Host 只提交 Session-scoped Export Request；
- Payload 始终省略，Credential、Restricted、Workspace 和 Conversation Summary 省略；
- 每条省略内容均记录 `payload_omitted` 或 `summary_omitted`；
- 导出在持久化锁内读取固定 `through_sequence` 水位；
- Observation 按 Session 的权威 Thread/Turn 归属筛选，不信任进程级 Session 标签；
- Usage 以 `record_type=usage` 聚合行导出，保留未定价调用数量；
- `records_sha256` 覆盖 Manifest 之后的全部原始行；
- 导出文件不包含 Workspace 绝对路径、Credential、环境变量或原始 Provider Header；
- Export 是观测行为，不得改变 Session、Turn 或 Usage 状态；
- 未授权、跨 Workspace 和跨 Session 请求必须拒绝；
- 新增 Golden Fixture、隐私测试和 Web 下载入口后才视为完成。

Studio 的 `codehelper` Adapter 负责把 Observation Kind 映射到 Canonical Core Kind，保留：

- 原始 Kind；
- Observation ID 和 Sequence；
- Identity、Trace、Causality、Data Policy；
- Payload/Summary Omission Receipt；
- Mapping Version 和未映射字段。

## 8. 持久化设计

SQLite 启用 Foreign Key 和 WAL。Migration 是唯一建表方式。

### 8.1 核心表

```sql
CREATE TABLE sources (
  id TEXT PRIMARY KEY,
  content_sha256 TEXT NOT NULL UNIQUE,
  format TEXT NOT NULL,
  format_version INTEGER NOT NULL,
  producer_name TEXT NOT NULL,
  producer_version TEXT,
  imported_at TEXT NOT NULL,
  source_name TEXT NOT NULL,
  byte_size INTEGER NOT NULL,
  status TEXT NOT NULL,
  error_json TEXT
);

CREATE TABLE runs (
  id TEXT PRIMARY KEY,
  source_id TEXT NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
  external_run_id TEXT NOT NULL,
  session_id TEXT,
  started_at TEXT,
  ended_at TEXT,
  status TEXT NOT NULL,
  title TEXT,
  event_count INTEGER NOT NULL,
  analysis_version TEXT,
  UNIQUE(source_id, external_run_id)
);

CREATE TABLE events (
  id TEXT PRIMARY KEY,
  source_id TEXT NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
  run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  source_event_id TEXT NOT NULL,
  sequence INTEGER NOT NULL,
  recorded_at TEXT NOT NULL,
  monotonic_ns INTEGER,
  kind TEXT NOT NULL,
  original_kind TEXT,
  identity_json TEXT NOT NULL,
  trace_id TEXT,
  span_id TEXT,
  parent_span_id TEXT,
  data_class TEXT NOT NULL,
  redaction TEXT NOT NULL,
  attributes_json TEXT NOT NULL,
  payload_digest TEXT,
  normalization_json TEXT NOT NULL,
  UNIQUE(source_id, source_event_id),
  UNIQUE(source_id, sequence)
);

CREATE TABLE usage_samples (
  event_id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
  run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  turn_id TEXT,
  sample INTEGER,
  provider TEXT,
  model TEXT,
  input_tokens INTEGER NOT NULL,
  output_tokens INTEGER NOT NULL,
  reasoning_tokens INTEGER NOT NULL,
  cached_tokens INTEGER NOT NULL,
  cost_microunits INTEGER,
  cost_known INTEGER NOT NULL,
  currency TEXT
);

CREATE TABLE findings (
  id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  rule_id TEXT NOT NULL,
  rule_version INTEGER NOT NULL,
  severity TEXT NOT NULL,
  title TEXT NOT NULL,
  evidence_json TEXT NOT NULL,
  fingerprint TEXT NOT NULL,
  UNIQUE(run_id, fingerprint)
);

CREATE TABLE import_jobs (
  id TEXT PRIMARY KEY,
  source_name TEXT NOT NULL,
  state TEXT NOT NULL,
  bytes_read INTEGER NOT NULL,
  records_read INTEGER NOT NULL,
  started_at TEXT NOT NULL,
  finished_at TEXT,
  error_json TEXT
);
```

必要索引：

```text
events(run_id, sequence)
events(run_id, recorded_at)
events(trace_id, span_id)
events(run_id, kind)
usage_samples(run_id, turn_id, sample)
findings(run_id, severity, rule_id)
runs(started_at)
```

### 8.2 事务和幂等

- Source ID 由文件内容 SHA-256 派生；
- 已成功导入的相同 Digest 返回现有 Source，不重复写入；
- 导入写入独立临时数据库或同库 Staging 表；
- 解析、验证、投影和基础分析全部成功后，在一个事务中发布 Source；
- 应用崩溃后将未完成 Job 标记为 `interrupted`，其 Staging 数据可安全清理；
- 删除 Source 依赖 Foreign Key Cascade，并在事务后清理无引用 Payload；
- Migration 使用单调 Schema Version，不在运行时猜测旧结构。

## 9. 导入流水线

### 9.1 阶段

```text
Inspect
  -> Detect
  -> Resource Admission
  -> Stream Decode
  -> Validate
  -> Normalize
  -> Stage
  -> Project
  -> Analyze
  -> Commit
```

### 9.2 Format Detection

检测只能读取前缀并使用结构化解析：

1. 目录含 `manifest.json` 时严格解析 Bundle Manifest；
2. JSON 对象含 OTLP `resourceSpans` 时选择 OTLP Adapter；
3. NDJSON 首行含 Canonical Manifest 时选择 Canonical Adapter；
4. 无法唯一判断时返回候选和证据，不继续猜测。

不得仅根据扩展名决定格式。

### 9.3 资源策略

`ResourcePolicy` 是公开配置：

```rust
pub struct ResourcePolicy {
    pub max_import_bytes: Option<u64>,
    pub max_record_bytes: Option<u64>,
    pub max_payload_bytes: Option<u64>,
    pub max_concurrent_imports: NonZeroUsize,
    pub source: PolicySource,
}
```

- 不在 Parser 内保留第二套隐藏限制；
- Desktop 默认策略从可用内存、磁盘空间、文件元数据和用户配置计算并显示 Provenance；
- 用户显式上限优先，但不能超过平台地址空间和 SQLite 能安全表示的范围；
- 所有格式使用流式 Reader，进度以实际读取字节计算；
- Resource Admission 失败在读取正文前返回所需与可用资源；
- 安全所需绝对上限必须成为配置 Schema 字段，并提供边界测试和文档。

### 9.4 错误模型

```rust
pub struct AppError {
    pub code: ErrorCode,
    pub message: String,
    pub retryable: bool,
    pub source_name: Option<String>,
    pub record_number: Option<u64>,
    pub json_pointer: Option<String>,
    pub action: RecoveryAction,
}
```

稳定 Error Code：

```text
unsupported_format
unsupported_version
invalid_manifest
invalid_record
sequence_conflict
digest_mismatch
privacy_rejected
resource_exhausted
storage_unavailable
import_canceled
internal
```

UI 根据 Code 呈现，不解析 `message`。

## 10. 分析引擎

### 10.1 原则

- 分析是纯函数：`Projection + RuleSet -> Findings`；
- 每条规则有稳定 ID、版本、证据和确定性 Fingerprint；
- 规则只基于可观察事实，不推断模型心理或业务根因；
- 没有统计依据时显示指标，不贴“异常”标签；
- 新规则不能改变历史结果而不提升 Rule Version；
- 重新分析在事务中替换同版本 Findings。

### 10.2 首发规则

| Rule ID | 触发事实 | 严重度 |
| --- | --- | --- |
| `trace.open_span` | Run 已结束但 Span 没有结束事件 | warning |
| `trace.orphan_span` | Parent Span 不存在且不是 Root | warning |
| `trace.time_inversion` | End 早于 Start 或 Child 超出 Parent 且来源声明同一时钟 | error |
| `tool.exact_duplicate` | 同 Turn 中 Tool Name 与 Canonical Args Digest 相同，且中间没有 Workspace Revision | info |
| `tool.forbidden_retry` | 来源明确记录 `retry_original=false` 后同一调用再次出现 | warning |
| `usage.duplicate_sample` | 同 Turn、Provider、Model、Sample 出现冲突记录 | error |
| `usage.cached_exceeds_input` | Cached Token 大于 Input Token | error |
| `usage.cost_unknown` | 存在未定价调用 | info |
| `run.event_after_terminal` | Terminal 事实后出现非 Cleanup 业务事件 | warning |

`tool.exact_duplicate` 只报告事实，不自动断言调用无用。Args Digest 使用 Canonical JSON：
对象 Key 排序、保留数组顺序、数值按 JSON 语义编码，不对字符串做模糊归一化。

### 10.3 汇总指标

- Run/Turn Wall Time；
- Model、Tool、Approval、Verification Span Duration；
- Input、Output、Reasoning、Cached Token；
- `cached_share = cached_tokens / input_tokens`；
- Tool Call 数、失败数和取消数；
- Provider Call 数；
- 已知成本、未定价调用数；
- Open Span 和 Finding 数。

并行 Span 的 Duration 可以重叠，因此分类 Duration 不应相加后称为 Total。

### 10.4 Run 对比

对比顺序：

1. 用户明确选择 Baseline 和 Candidate；
2. 先按稳定 Turn/Call ID 对齐；
3. 没有共享 ID 时按 `kind + ordinal + canonical identity` 对齐；
4. 未匹配项单独显示，不强行配对；
5. 展示绝对值和差值，不用隐藏阈值决定好坏；
6. Cost 含未知项时显示已知下界和未知调用数。

## 11. Tauri Command 契约

首发 Command：

```rust
inspect_source(path) -> SourceInspection
start_import(request) -> ImportJob
cancel_import(job_id) -> CommandReceipt
get_import_job(job_id) -> ImportJob
list_runs(query) -> Page<RunSummary>
get_run(run_id) -> RunDetail
list_events(query) -> Page<EventSummary>
get_event(event_id) -> EventDetail
get_timeline(query) -> TimelineSlice
list_findings(run_id) -> Vec<Finding>
reanalyze_run(run_id) -> AnalysisReceipt
compare_runs(request) -> RunComparison
export_report(request) -> ExportReceipt
delete_source(source_id) -> CommandReceipt
get_settings() -> Settings
update_settings(patch) -> Settings
```

约束：

- List 接口使用稳定 Cursor，不使用 Offset；
- Cursor 绑定 Query Digest，禁止跨 Query 复用；
- 分页容量来自请求和公开 Resource Policy，不在 Handler 中硬编码；
- 长任务立即返回 Job ID，通过 Tauri Event 推送进度；
- Event Payload 只包含 Job 状态，不发送完整 Trace；
- Command 支持取消，取消状态可重复查询；
- Rust 类型通过 `ts-rs` 生成 `web/src/protocol/generated.ts`；
- 生成文件由 CI 漂移检查，禁止手工维护双份类型。

## 12. Web 体验

### 12.1 信息架构

应用启动后直接进入 Run Library，不创建营销首页：

```text
左侧：Run Library
主区：Trace Detail / Compare
右侧：Event Inspector，可折叠
顶部：导入、搜索、筛选、对比、导出
```

主要界面：

1. **Run Library**：紧凑列表，显示时间、状态、Provider/Model、Token 和 Findings；
2. **Trace Detail**：时间线、汇总条、Span/Event 双视图；
3. **Compare**：Baseline/Candidate 并排指标和对齐事件；
4. **Settings**：数据目录、Resource Policy、默认脱敏和主题。

### 12.2 交互要求

- 导入使用按钮和拖放区，导入前显示格式、版本、大小和隐私预检；
- 时间线支持缩放、平移、类型过滤和点击定位；
- 大列表使用虚拟化，不能把所有 Event 同时挂载到 DOM；
- Tool、Model、Approval、Verification 使用图标和非单色编码；
- 色彩不是状态的唯一表达，必须同时显示图标或文本；
- 原始 JSON 使用按需展开，不默认渲染 Payload；
- Copy、Delete、Close、Zoom 使用 Lucide 图标和 Tooltip；
- 组件卡片圆角不超过 8px，不嵌套卡片；
- 紧凑面板使用适配容器的字号，不使用 Hero 排版；
- 窄屏时 Inspector 变为 Drawer，文本不得溢出或遮挡；
- 动画尊重 `prefers-reduced-motion`；
- 键盘可完成 Run 选择、筛选、事件导航和关闭 Inspector。

### 12.3 前端状态

- Server State 由一个类型化 Command Client 管理；
- 页面查询状态进入 URL 或 Router State；
- Import Job 使用事件更新，同时在窗口恢复时主动查询；
- 不把 SQLite 数据全集复制到全局 Store；
- 组件测试使用 Fake Command Client，不 Mock Tauri 全局对象；
- 所有时间、Token 和成本格式化集中在纯函数模块。

## 13. 安全与隐私

威胁输入包括恶意 JSON、超大记录、路径穿越、Symlink、HTML Payload、公式注入和畸形
时间/整数。

必须满足：

- Tauri Capability 只开放所需 Dialog、文件读取和保存能力；
- 文件路径由原生 Dialog 返回，Web 不能提交任意路径字符串绕过授权；
- 导入文件只读打开，不跟随 Bundle 内链接；
- SQL 全部参数化；
- JSON Integer 转换检查溢出，不经过 JavaScript Number 保存 Token；
- Token 和 Microunits 在 IPC 中使用十进制字符串或安全范围检查；
- Markdown 报告转义 HTML，CSV 后续支持时防公式注入；
- Payload 默认不加载、不索引、不导出；
- 删除 Source 后执行引用清理，并提供“删除全部本地数据”；
- 日志不记录原始 Payload、完整 Prompt、Credential 或用户绝对路径；
- 崩溃报告默认关闭；
- 首发版本没有网络权限；
- 依赖锁文件纳入版本控制并运行许可证与漏洞扫描。

## 14. 并发、取消和恢复

- SQLite 写入由 Repository 层串行化，读取使用独立连接；
- 一个 Source Digest 同时只能有一个 Import Job；
- 多个不同 Source 是否并发由公开 `ResourcePolicy` 决定；
- Import 每完成一批流式记录检查 Cancellation Token；
- Analysis 可取消，取消不替换旧 Findings；
- 应用关闭时停止接收新任务，等待事务回滚并写入 Job 状态；
- 下次启动把 `running` Job 转为 `interrupted`，不自动重跑；
- Export 使用临时文件和原子 Rename；
- 所有 Job 状态转换都进行 Compare-and-set，迟到结果不能覆盖 canceled/failed。

Job 状态：

```text
queued -> inspecting -> importing -> projecting -> analyzing -> completed
   |          |            |             |             |
   +----------+------------+-------------+-----------> failed
   +----------+------------+-------------+-----------> canceled

running state --restart--> interrupted
```

## 15. 测试策略

### 15.1 Rust

- Domain Validation：ID、时间、Usage、Data Policy；
- Adapter Golden：Canonical、CodeHelper、OTLP；
- Malformed Fixture：未知版本、坏摘要、重复 Sequence、路径逃逸；
- Property Test：Canonical JSON、任意 Event 顺序拒绝、聚合守恒；
- Store Test：Migration、事务回滚、重复导入、删除级联、重开数据库；
- Analysis Rule Test：每条规则 Positive/Negative/Boundary；
- Cancellation Test：Decode、Stage、Analyze、Export 各阶段；
- Crash Recovery Test：遗留 Import Job 和 Staging 数据；
- Cross-platform Path Test。

### 15.2 Web

- Protocol Decoder 和格式化函数单测；
- Run Library 筛选、空态、错误态；
- Timeline 缩放、过滤、虚拟化和选中同步；
- Event Inspector 的隐私默认值；
- Compare 对齐和未知成本显示；
- Import Job 中断后恢复；
- Keyboard 和 Axe 可访问性测试。

### 15.3 端到端

Playwright 通过 Fake Native Bridge 驱动确定性 Fixture：

- 首次打开和导入；
- 导入失败定位；
- Run Detail 浏览；
- Finding 跳转证据；
- Run Compare；
- Markdown 导出；
- 删除和重启恢复。

桌面 Smoke Test 使用打包后的 Tauri 应用验证：

- 启动；
- 选择 Fixture；
- 导入完成；
- SQLite 重开后 Run 仍存在；
- 无网络连接；
- 窗口关闭后无后台进程泄漏。

视觉验收覆盖：

```text
1440x900
1024x768
390x844
```

检查时间线非空、文字不重叠、Drawer 可用、长 Model/Tool 名不撑破布局。

## 16. 实施切片

每个切片必须能独立测试和提交，不创建长期空壳。

### 16.1 Bootstrap And Contracts

- 创建 Cargo Workspace、Tauri、React/Vite；
- 配置 Formatter、Lint、Test 和 CI；
- 实现 Domain 类型、Error Code 和 TypeScript 生成；
- 加入 Canonical v1 Schema 与 Golden Fixture；
- 建立 Makefile 的 `check`、`test`、`build`。

验收：

```bash
cargo fmt --check
cargo clippy --workspace --all-targets -- -D warnings
cargo test --workspace
npm --prefix web run check
npm --prefix web test
```

### 16.2 Transactional Import

- Format Detector；
- Canonical NDJSON Adapter；
- SQLite Migration 和 Repository；
- Import Job、取消、幂等和恢复；
- Import Dialog 与进度 UI。

验收：同一 Fixture 导入两次只产生一个 Source；格式错误不产生可见 Run。

### 16.3 Trace Projection

- Span 配对和 Run/Turn Projection；
- Usage Projection；
- Run Library；
- Timeline 和 Event Inspector；
- Cursor Pagination 与虚拟列表。

验收：Golden Fixture 的事件数、Token、Cached Share、成本和 Duration 精确匹配。

### 16.4 Deterministic Findings

- Rule Engine、Rule Version 和 Fingerprint；
- 首发九条规则；
- Finding 列表和证据跳转；
- Reanalyze Command。

验收：同一 Projection 重复分析得到字节级一致的 Finding JSON。

### 16.5 Compare And Export

- Run 对齐和差异；
- Compare UI；
- 脱敏 Markdown Report；
- 原子导出和取消。

验收：未知成本保持未知；报告中的每条 Finding 包含证据 ID。

### 16.6 CodeHelper Consumer

- CodeHelper Observation JSONL Adapter；
- Manifest、摘要和事件数校验；
- Observation 到 Canonical Event 的版本化映射；
- CodeHelper Golden Consumer Test。

验收：Hermetic CodeHelper Session 导出后可被 Studio 导入，事件身份、Usage 和 Trace
摘要与来源一致，导出文件不包含 Workspace 绝对路径或 Credential。

### 16.7 OTLP And Release

- OTLP JSON Adapter；
- 三平台 Build；
- macOS 签名外的本地安装 Smoke Test；
- SBOM、Checksum、许可证和安全文档；
- 性能基线与发布检查清单。

## 17. CI 与质量门禁

每个 PR：

```bash
cargo fmt --check
cargo clippy --workspace --all-targets -- -D warnings
cargo test --workspace
npm --prefix web ci
npm --prefix web run check
npm --prefix web test
npm --prefix web run build
git diff --check
```

涉及 UI 时增加：

```bash
npm --prefix web run test:e2e
```

发布门禁：

- Release Build 三平台成功；
- Migration 从所有已发布 Schema 升级成功；
- Golden Fixture 向后兼容；
- Desktop Smoke Test；
- SBOM 和依赖漏洞扫描；
- 安装、数据目录、卸载和回滚文档；
- 未声明网络连接检查；
- CodeHelper 最新稳定 JSONL Consumer Test。

性能不使用脱离机器环境的绝对耗时作为通过条件。记录 Fixture 规模、机器信息、吞吐、
峰值内存和 UI Long Task，相对基线回归策略必须显式配置并保留来源。

## 18. Definition Of Done

首个可用版本完成必须同时满足：

- 可通过文件选择和拖放导入 Canonical、CodeHelper 和 OTLP Trace；
- 导入具有进度、取消、幂等、事务和崩溃恢复；
- Run Library、Trace Detail、Finding、Compare 和 Export 可从 UI 到达；
- Usage、成本和 Span 聚合由 Golden Test 固定；
- 九条首发规则具有正反例和证据跳转；
- 未知格式、未知版本、恶意路径、坏摘要和资源不足均 Fail Closed；
- 默认不访问网络，不执行导入内容；
- macOS 打包应用通过 Smoke Test；
- Rust、Web、E2E、文档和依赖门禁通过；
- README 提供真实截图、安装方式和三分钟上手流程。

## 19. CodeHelper 执行约束

将本文交给 CodeHelper 实施时，应附加以下指令：

```text
按照 Agent Trace Studio 技术实现方案实施独立项目。

要求：
1. 先建立基线并检查 Rust、Node、Tauri 系统依赖。
2. 严格按实施切片推进，每个切片完成后运行聚焦测试。
3. 不修改或读取其他应用的私有数据库。
4. 不引入未写入公开 ResourcePolicy 的固定容量或异常阈值。
5. 不手写 Rust/TypeScript 双份协议类型，使用生成和漂移检查。
6. 所有导入格式先提供 Schema、Golden、Malformed Fixture，再实现 UI。
7. 所有长任务实现取消、事务回滚和重启恢复。
8. 默认无网络、Payload 不展示、未知成本不显示为零。
9. 完成前运行全量 Rust、Web、Playwright 和桌面 Smoke Test。
10. 每个切片报告变更、验证证据和剩余风险，不提前宣称项目完成。
```

首次执行只完成 `Bootstrap And Contracts` 与 `Transactional Import`。确认契约、导入事务
和错误模型稳定后，再进入 Trace Projection，避免 UI 先于数据事实形成第二套语义。
