# RFC-004：M5 V2 Coding Native 与 Context Bridge

> 状态：Implemented
> 关联：[ROADMAP §6](../ROADMAP.zh-CN.md)、[RFC-003](./RFC-003-vscode-transport.zh-CN.md)、[RFC-005](./RFC-005-edit-transaction.zh-CN.md)、[RFC-013](./RFC-013-vscode-companion.zh-CN.md)
> 影响面：`extensions/vscode`、`internal/runtime/protocol`、`internal/runtime/app`、`internal/host/runtimeapi/contract`

## 1. 目标

M5 V2 在已完成的 V1 Companion 上增加编辑器原生编码入口，但不复制 Runtime 的
Agent loop、Guard、编辑事务或验证状态机。

V2 必须完成以下闭环：

1. 从 active selection、当前位置 symbol 和 Problems diagnostics 显式构造上下文；
2. Runtime 复验文件身份、digest、range 和大小，并返回逐项 context receipt；
3. 用户可从编辑器选区发起解释、修改、重构和生成测试 turn；
4. diagnostic 可产生 `Fix with CodeHelper` Code Action，并进入同一 thread/turn；
5. edit plan 有可导航的原生文件审阅面，文件可打开 VS Code diff；
6. 所有修改仍经过 V1 的 plan-bound approval、drift check、journal 和 Verify Gate。

退出条件是：用户能从选区或 diagnostic 发起真实修复，在 VS Code 内检查发送的上下文、
浏览计划文件、批准修改并看到 diagnostics/verification 结果，全程无需切回终端。

## 2. 明确不做

- Remote SSH、WSL、Dev Container、Codespaces 和 multi-root，留到 V3；
- Marketplace/Open VSX、binary 下载、签名更新和自动升级，留到 V3；
- proposed VS Code API、内联补全 provider 或旁路模型调用；
- 自动收集 open tabs、recent files、terminal、SCM diff、notebook 或虚拟文档；
- 在扩展中使用 `WorkspaceEdit` 重放模型 patch；
- 根据 diagnostic message、symbol 文本或 Webview 消息直接执行命令或写文件；
- 构建第二套 thread、approval、edit plan、diagnostics 或 verification 状态机；
- 将 Runtime 的词法 Repo Index 冒充 VS Code language service 的类型语义。

## 3. 当前基础与缺口

### 3.1 已有基础

- V1 已有 single-root local workspace、ACP 生命周期、cursor replay 和 Workspace Trust；
- `turn.start.context` 已支持显式 `file` / `selection`，Runtime 会复验
  workspace path、file URI、SHA-256、UTF-16 range、UTF-8 和大小；
- Chat、approval/input、diagnostics/verification/receipt 已有事件投影；
- `tool.EditPlan`、plan identity、重新规划和 drift 零写入拒绝已落地；
- 扩展已有只读 virtual document 与 `vscode.diff`；
- VS Code 1.96.4 Electron Host、安全、性能和 VSIX 门禁已建立。

### 3.2 V2 缺口

1. ROADMAP 引用的 RFC-004 此前不存在，V2 协议与安全边界未冻结；
2. context kind 只有 `file` / `selection`，没有来源、symbol、diagnostics；
3. Runtime 只返回整体 prompt context section，没有逐项 editor context receipt；
4. selection 只能在 Chat 中写 `@selection`，没有 editor/title/context menu 原生入口；
5. 没有 diagnostic Code Action；
6. diff preview 逐文件依次打开，没有持久、可导航的 plan 文件模型；
7. 没有覆盖 symbol provider、Problems、native command 和 Code Action 的 Extension Host 集成。

## 4. 硬约束

### C1：Runtime 仍是唯一执行权威

inline edit 和 Code Action 只能提交 `turn.start`。扩展不得直接调用模型、执行 shell、
应用 patch 或根据模型文本构造 `WorkspaceEdit`。

### C2：上下文必须由用户显式触发

只有 composer directive、用户执行的 native command 或用户选择的 Code Action 能创建
引用。V2 不自动发送 open tabs、recent files、整个 Problems 列表或 workspace 内容。

### C3：编辑器元数据不等于文件授权

每个 symbol/diagnostics 引用仍必须携带 workspace-relative path、file URI、document
version 和完整文件 SHA-256。Runtime 必须重新解析磁盘文件并验证 digest；扩展声明的
range、symbol 和 diagnostics 不能扩大 workspace 文件读取边界。

### C4：diagnostics 与 symbol 文本是不可信数据

diagnostic message/code/source、symbol name/kind 只作为有界 JSON 数据加入模型上下文，
不得进入 shell、路径拼接、HTML 或命令 ID。Webview 继续只用 `textContent`。

### C5：脏文件不静默保存

native flow 遇到 dirty document 必须拒绝或明确询问 `Save and Continue`。用户未确认时
不保存、不提交 turn；保存后重新读取、计算 digest 和捕获 range。

### C6：统一 thread、approval 与 verification

Chat、inline edit 和 Code Action 复用当前 workspace 的 Runtime session/thread。并发活跃
turn 时明确拒绝或引导用户先停止，不创建隐藏 session，也不绕过 approval/Verify Gate。

### C7：diff 深度集成不改变 apply 权威

扩展可以保存只读 plan 投影、列出文件并打开原生 diff，但批准只提交
`approval.decision.plan_id`。Runtime 继续执行重新规划、journal 和原子提交。

### C8：只使用稳定 VS Code API

V2 以 `engines.vscode = ^1.96.0` 为最低版本，不依赖 proposed API。多文件审阅使用
Tree View、commands、`TextDocumentContentProvider` 和 `vscode.diff`。

### C9：键盘与可访问性是一等门禁

所有 native flow 必须可从 Command Palette 执行；Tree View 节点有稳定 label、
description、tooltip 和 command；不能把 hover 或鼠标作为唯一入口。

## 5. 协议与工程决策

### D1：扩展现有 EditorContextReference

`turn.start.context` 继续是共享协议字段，不新增 VS Code 私有 Operation。引用扩展为：

```text
kind: file | selection | symbol | diagnostics
source: composer | selection_command | code_action
uri + path + document_version + digest + explicit
range? + symbol? + diagnostics? + omitted_diagnostics?
```

校验矩阵：

| kind | range | symbol | diagnostics |
| --- | --- | --- | --- |
| `file` | 禁止 | 禁止 | 禁止 |
| `selection` | 必需且非空 | 禁止 | 禁止 |
| `symbol` | 必需且非空 | 必需 | 禁止 |
| `diagnostics` | 禁止 | 禁止 | 1–32 项 |

symbol 包含 `name`、`kind`、`range`、可选 `selection_range`；diagnostic 包含
`range`、`severity`、`code`、`message`、`source`。字符串和数组必须有协议上限。
单 turn 仍最多 8 个引用；单文件 1 MiB、单项渲染 64 KiB、总编码 128 KiB 的 V1
边界保持不变。

### D2：Runtime 返回逐项 Context Receipt

`resolveEditorContext` 改为同时返回模型 prompt 和逐项 receipt。receipt 至少包含：

```text
kind + source + path + digest + range?
symbol? + diagnostic_count + omitted_diagnostics
original_bytes + retained_bytes + truncated
```

receipt 进入 durable `turn.started` 和 terminal `turn.receipt`。扩展只展示 Runtime
确认后的 receipt，不能把“本地捕获成功”冒充“模型实际收到”。

无效 path/URI/digest/range、越界 diagnostic 或结构不一致的 symbol 字段必须拒绝整个 turn，
不得静默删除错误引用后继续。合法内容可按既有预算裁剪，并在 receipt 标明。
Runtime 只确认文件身份、范围和结构，不声称能复现 VS Code language service 的语义判断；
symbol name/kind 在 receipt 中保持为有来源标记的不可信编辑器元数据。

### D3：ContextBridge 捕获策略

- `@selection`：保持现有显式行为；
- `@symbol`：调用稳定的 `vscode.executeDocumentSymbolProvider`，选择包含 cursor/selection
  的最内层 symbol；无 provider 或无匹配 symbol 时明确不可用；
- `@diagnostics`：只捕获 active file 当前可见的 `vscode.languages.getDiagnostics(uri)`，
  按 severity/range 稳定排序，最多 32 项并记录 omitted count；
- Code Action：只捕获触发 action 的 diagnostic，执行 command 时重新校验 URI、range、
  当前 diagnostic 和 document digest，拒绝陈旧 action；
- 所有引用只允许 single-root workspace 内的 `file:` document。

Chat 在 `turn.started` 后显示 context receipt chips。chips 是审计结果，不提供修改已提交
turn 的假按钮；用户要减少上下文，必须在提交前删除 directive 或取消 native flow。

### D4：Native Selection Flow

注册以下 commands，并放入 Command Palette 与 editor context menu：

```text
CodeHelper: Explain Selection
CodeHelper: Edit Selection
CodeHelper: Refactor Selection
CodeHelper: Generate Tests for Selection
```

Explain 可在 trusted/untrusted workspace 运行，但 Runtime posture 决定工具能力；
Edit/Refactor/Generate Tests 在客户端先拒绝 untrusted workspace，Runtime 的 `never`
posture 仍是最终边界。Edit/Refactor 通过 `showInputBox` 获取用户指令，所有命令都提交
带 selection 引用的普通 turn，并聚焦既有 Chat View 展示进度。

### D5：Diagnostic Code Action

注册 `CodeActionProvider`，只为 workspace 内 `file:` document 的 diagnostics 提供：

```text
Fix with CodeHelper
Explain with CodeHelper
```

provider 本身不读文件、不启动 Runtime，只构造 command，因此保持低延迟；untrusted
workspace 只提供 Explain。command
执行时由 ContextBridge 重新捕获文件和 diagnostic，并提交统一 turn。Code Action
不声明 `WorkspaceEdit`，也不标记为 preferred，避免被 VS Code 自动应用。

### D6：可导航 Edit Plan Review

V1 的 `EditPlanPreview` 从 Chat 私有对象提升为 extension 级服务，并增加一个
`CodeHelper Changes` Tree View：

- 根节点是当前未解决或最近一次 edit plan；
- 文件节点显示 path、created/modified/deleted 和 approval 状态；
- 选择文件执行 `codehelper.openPlanDiff`，只打开该文件的原生 diff；
- hunk 导航复用 VS Code diff editor 的稳定内建能力，不解析或重放 patch；
- diagnostics/verification event 按 workspace-relative path 关联到计划文件；
- replay 可恢复只读 plan 状态，但不重复弹 modal 或自动打开 diff。

Tree View 中的 approve/deny 仍通过 Chat 持有的 pending approval identity 和
`SessionCommands`；未知、过期或已解决 plan 不可提交 decision。

### D7：兼容与降级

- 新 context kind 和 receipt 进入 Go Schema、生成式 TypeScript 和 ACP/HTTP contract；
- V1 客户端遇到新增字段保持兼容；V2 客户端启动时仍通过 `initialize` 校验协议能力；
- symbol provider 不可用、diagnostics 为空或 document 非本地文件时显示明确错误；
- unknown event 继续只读降级，不能触发 native action；
- V2 不改变现有 `file_patch` 在强制计划模式下 fail-closed 的决定。

## 6. 实施分片

| 分片 | 内容 | 退出条件 | 状态 |
| --- | --- | --- | --- |
| T0 | Context kind、source、receipt 共享协议与 Runtime 复验 | Go/Schema/TS 无漂移，双 Host contract 覆盖 | completed |
| T1 | ContextBridge symbol/diagnostics 捕获与 receipt UI | unit + Electron 证明显式、有界、可审计 | completed |
| T2 | Native selection commands 与统一 turn | 四个命令从真实 editor selection 完成 turn | completed |
| T3 | Diagnostic Code Action | stale diagnostic 拒绝，fix/explain 进入同一 thread | completed |
| T4 | CodeHelper Changes 与可导航 diff review | 多文件 plan 可逐文件打开且不能绕过 approval | completed |
| T5 | 集成、安全、可访问性、性能、VSIX 与文档 | V2 全部退出条件通过 | completed |

执行必须严格按 T0 → T5，一次只推进一个分片。每完成一项同步本表、Gap Ledger、
ROADMAP、测试结果和偏离；不得在 V2 中提前实现 V3。

## 7. 测试与发布门禁

### 7.1 常设命令

沿用 V1：

```text
make vscode-check
make vscode-test
make vscode-security
make vscode-performance
make vscode-runtime-integration
make vscode-integration
make vscode-package
make protocol-contract
make security-test
```

### 7.2 V2 必测场景

- symbol provider 缺失、返回嵌套 symbol、range 漂移；
- diagnostics 空、超过 32 项、message/code/source 超限；
- dirty document 拒绝与显式保存后重新捕获；
- workspace 外 URI、虚拟文档、symlink、digest/range 漂移；
- context receipt 的 retained/truncated/omitted 与模型实际 prompt 一致；
- active turn 冲突、Runtime crash/replay 后 native flow 状态；
- stale Code Action、foreign diagnostic、untrusted workspace 写拒绝；
- 多文件 plan 文件导航、过期 decision、replay 不弹历史 modal；
- keyboard-only commands、Tree View label/tooltip、light/dark/high-contrast；
- Webview 伪造 context/plan message 不产生文件读取或 approval。

### 7.3 性能预算

- V1 activation `< 100 ms` 与 Runtime ready p50/p95 门禁不退化；
- Code Action `provideCodeActions` 不做 I/O，目标 `< 20 ms`；
- 1 MiB 文件 context capture 目标 `< 100 ms`；
- 单次最多 8 references、每个 diagnostics reference 最多 32 项；
- Changes View 无固定轮询，只由 plan/approval/diagnostics/verification event 刷新；
- context receipt 与 plan projection 都有数量和字符串上限。

## 8. Gap Ledger

| ID | 缺口 | 风险 | 分片 | 状态 | 自动化证据 |
| --- | --- | --- | --- | --- | --- |
| M5-G014 | RFC-004 缺失，V2 边界未冻结 | 原生 UI 绕过 Runtime 权威 | 规划 | closed | 本 RFC |
| M5-G015 | context 无 source/symbol/diagnostics | 原生入口只能拼文本 | T0 | closed | protocol/schema/contract |
| M5-G016 | 无逐项 Runtime context receipt | UI 无法证明模型实际收到什么 | T0 | closed | resolver + replay fixture |
| M5-G017 | 无 native selection flow | 选区任务仍依赖手写 directive | T2 | closed | Electron command suite |
| M5-G018 | 无 diagnostic Code Action | Problems 不能发起统一修复 turn | T3 | closed | provider + stale action suite |
| M5-G019 | edit plan 无可导航文件模型 | 多文件审阅摩擦高且状态易丢 | T4 | closed | Changes View + replay suite |
| M5-G020 | 无 V2 accessibility/security/performance 门禁 | 原生入口可能退化或越权 | T5 | closed | release matrix |

M5-G014 在本规划冻结后关闭；其余缺口必须由对应实现和自动化证据关闭。

## 9. 状态更新规则

每完成一个分片必须同时：

1. 将 §6 对应状态改为 `completed`；
2. 关闭 §8 中已有自动化证据的缺口；
3. 记录真实命令、测试数量、性能数据和环境限制；
4. 更新 ROADMAP M5 当前进度；
5. 协议变化同步 RFC-003，编辑权威变化同步 RFC-005/ARCHITECTURE；
6. 未完成或只在 mock 中通过的能力保持 `open`。

## 10. 进度记录

### 2026-08-04：规划冻结

- 审计 ROADMAP §6、RFC-003/005/013、当前协议和 `extensions/vscode`；
- 确认 V1 已具备 Runtime、Trust、approval、edit plan 和发布地基；
- 确认 RFC-004 文件此前缺失，symbol/diagnostics context、逐项 receipt、native command、
  Code Action 和 plan navigation 是 V2 的真实结构性缺口；
- 冻结 T0–T5 顺序，M5-G014 关闭；T0 尚未开始。

### 2026-08-04：T0 完成

- `EditorContextReference` 增加 `symbol` / `diagnostics` kind、composer/selection-command/code-action source、symbol metadata、最多 32 条 diagnostic 与 omitted count；旧 V1 file/selection 缺省 source 仍兼容；
- 协议对 URI/path、source、非空 UTF-16 range、symbol selection containment、diagnostic severity/message/code/source 和数量上限 fail-closed；
- Runtime 对四种 kind 统一复验 canonical workspace path、file URI、完整 SHA-256、UTF-8、UTF-16 range 和 1 MiB 文件边界；symbol 与 diagnostic metadata 只作为不可信 JSON，不冒充 Runtime 语义解析；
- `resolveEditorContext` 同时产生模型 prompt 与逐项 `EditorContextReceipt`，记录 kind/source/path/digest/range/symbol、diagnostic/omitted 数量、原始/保留字节和截断；相同 receipt 进入 durable `turn.started` 与 terminal `turn.receipt`；
- receipt replay 解码再次校验 kind/source、digest、range、字节/截断关系、symbol 和 diagnostic count，防止畸形历史被客户端当作 Runtime 确认；
- JSON Schema 为 context kind/source 生成 enum，TypeScript 生成物同步；V1 `ContextBridge` 开始发送 `source: composer`；
- ACP initialize 通过字符串 feature 集广告 `editor_context_v2`，VS Code 握手强制校验；旧 Runtime 在启动阶段明确拒绝，不把协议不兼容延迟到首次原生操作；
- ACP/HTTP 共享 contract 各 12 个场景通过，新场景验证 diagnostic context 经两个 Host 到达模型、started/receipt 一致且 durable history 可回放；
- 相关 Go race、协议/Runtime 定向测试、TypeScript check、46 项扩展测试通过；`make vscode-runtime-integration` 46/46，真实 binary 验证扩展收到一致的 started/terminal context receipt；
- M5-G015、M5-G016 关闭。T1 尚未开始。

### 2026-08-04：T1 完成

- Chat composer 新增显式 `@symbol` / `@diagnostics` directive；仍只允许 single-root
  workspace 内已保存的本地 `file:` document，不接受 Webview 指定路径；
- `@symbol` 使用稳定的 `vscode.executeDocumentSymbolProvider`，在最多 4096 个 provider
  节点中选择完整包含 cursor/selection 的最内层 symbol；range、selection range、
  UTF-8 name/kind 上限不一致时 fail-closed；
- `@diagnostics` 只读取 active URI 的 `vscode.languages.getDiagnostics`，按
  severity/range/message/code/source 的 locale-independent 顺序稳定排序，最多发送 32
  项并记录 omitted count；空 message、无 diagnostics、超过协议 UTF-8 字节上限或
  provider 超过 4096 项时明确拒绝；
- 异步 symbol 查询和磁盘读取后重新检查 active editor、document version 与
  cursor/selection 快照，dirty document 或捕获期漂移拒绝；用户显式保存后可重新捕获并
  计算新的完整文件 SHA-256；
- Chat projector 只从 Runtime `turn.started.editor_context` 和
  `turn.receipt.editor_context` 生成结构化 receipt cards，展示 kind/source/path、
  SHA-256、range/symbol、diagnostic/omitted 数量、retained/original bytes 和
  truncated；卡片只有安全文本 sink，没有 postMessage/action；
- Node 测试 52 项通过，新增 nested/missing symbol、40→32+8 diagnostics、空值/UTF-8
  超限、Runtime receipt started/terminal 投影和只读 DOM 门禁；安全门禁 6/6；
- VS Code 1.96.4 Electron empty/workspace 两场景通过，workspace 场景使用真实
  TypeScript symbol provider 和 DiagnosticCollection 验证 `inner` symbol、diagnostic
  omission、dirty 拒绝及保存后重捕获；
- 真 Runtime 集成 52/52；性能门禁通过，10k delta 13.2 ms，Runtime ready p50
  40.1 ms、p95 712.3 ms。T2 尚未开始，M5-G017 保持 open。

### 2026-08-04：T2 完成

- 注册 Explain/Edit/Refactor/Generate Tests 四个 selection commands，同时进入 Command
  Palette 与 `editor/context` menu；manifest 使用 `resourceScheme == file`、
  `editorHasSelection`，三种修改动作额外使用 `isWorkspaceTrusted`；
- 四个命令共用一个 `SelectionFlow`：有界策略构造 prompt，调用
  `ContextBridge.capture(selection, selection_command)`，聚焦既有 Chat View，再通过
  `RuntimeController.submitPrompt` 提交普通 `turn.start`；没有新增 Operation、thread、
  approval 或 apply 路径；
- Edit/Refactor 使用 `showInputBox` 收集最多 4096 字符的非空指令，取消输入不读取文件、
  不聚焦 Chat、不提交 turn；程序化 command 参数同样经过边界校验；
- Explain 在 trusted/untrusted workspace 均可调用，Runtime posture 仍决定实际工具能力；
  Edit/Refactor/Generate Tests 在 menu enablement 和执行 flow 两层拒绝 untrusted，
  direct command invocation 不能绕过；
- capture 完成后先聚焦 Chat 再提交，避免 submit 已成功但 focus 失败被报告为可重试失败；
  Runtime active-turn 冲突继续由共享 session/Runtime 权威拒绝，不在扩展内排队或复制状态机；
- Node 测试 58 项通过，覆盖四动作统一 turn、prompt/指令上限、取消和 untrusted
  pre-capture 拒绝；安全门禁 7/7，确认不使用 `WorkspaceEdit` 或任何直接写文件路径；
- `make vscode-integration` 现在构建真实 binary 并运行 empty/workspace/native 三个 VS
  Code 1.96.4 Electron 场景；native 场景依次执行四个真实 command，逐个等待
  `turn.completed`，并断言 started/terminal receipt 均为同一
  `source=selection_command` selection；
- 性能门禁通过，10k delta 13.0 ms，Runtime ready p50 36.4 ms、p95 699.9 ms；
  M5-G017 关闭。T3 尚未开始。

### 2026-08-05：T3 完成

- 为 single-root workspace 内的本地 `file:` document 注册稳定
  `CodeActionProvider`，每次最多处理 32 条 VS Code 已提供的 diagnostics，生成
  `Fix with CodeHelper` 与 `Explain with CodeHelper`；untrusted workspace 只生成 Explain；
- provider 只构造 `QuickFix` command，尊重 `context.only` 与 cancellation，不读取文件、
  不启动 Runtime、不设置 `WorkspaceEdit`/`edit`/`isPreferred`，直接 Electron 测量满足
  `<20 ms` 预算；
- command snapshot 严格绑定 canonical URI、document version、range、severity、
  message、code、source，拒绝未知字段、非 plain object、非法位置和超出协议 UTF-8
  上限的 metadata；
- `ContextBridge.captureDiagnostic` 要求目标文档仍打开、位于当前 workspace、已保存且
  version 未漂移；磁盘读取前后两次确认当前 `vscode.languages.getDiagnostics` 中仍有
  全字段相同的 diagnostic，并重新计算完整文件 SHA-256；
- stale message/range/version、foreign URI、dirty/closed document 均在 focus/submit
  前失败，不产生 turn；外部磁盘内容与 document text 不一致同样 fail-closed，Runtime
  继续最终复验 path/digest/range；
- Fix/Explain 共用 `DiagnosticFlow`，分别构造固定 prompt，提交单项
  `kind=diagnostics`、`source=code_action` 的普通 `turn.start`，复用现有 session/thread、
  Chat、approval 和 Verify Gate；Fix direct command 在 untrusted 也无法绕过；
- Node 测试 65 项、真 stdio 65/65、安全门禁 8/8 通过；
  `make vscode-integration` 的 empty/workspace/native 三个 VS Code 1.96.4 场景通过，
  native 场景验证 provider action 形状、untrusted 只读降级、foreign/stale 无 turn，
  以及 fresh Explain/Fix 均到达 `turn.completed` 且 started/terminal receipt 一致；
- 性能门禁通过，10k delta 13.1 ms，Runtime ready p50 43.6 ms、p95 717.7 ms；
  M5-G018 关闭。T4 尚未开始。

### 2026-08-05：T4 完成

- 将 `EditPlanPreview` 从 Chat 私有实例提升为 extension 级共享服务；保留 V1
  `show(plan)`，新增 `showFile(plan,index)`，Tree 选择文件时只调用一次稳定
  `vscode.diff`，不解析或重放 patch；
- 新增 `CodeHelper Changes` Tree View：根节点展示当前全部 pending plan，若无 pending
  则保留最近一次 resolved plan；文件节点显示 canonical path、created/modified/deleted、
  approval 状态、before/after digest，并可展开 diagnostics/verification path evidence；
- `EditPlanProjector` 从 durable approval/diagnostics/verification event 恢复只读状态，
  replay 只刷新 Tree，不自动打开 diff、modal 或提交 decision；无固定轮询；
- plan 投影最多保留 16 个记录、128 个文件、单侧 1 MiB、总计 8 MiB、diff 4 MiB，
  虚拟文档最多 256 个/16 MiB；拒绝重复/绝对/反斜杠/`.`/`..` path、未知 kind、
  existence/digest/content 自相矛盾和 forged command 字段；
- Tree 的 `openPlanDiff` 只接受 plan SHA-256 与 file index；approve/deny 只携带
  request identity，必须回到 Chat 当前 pending approval，重新校验 turn/item/plan ID、
  allowed scope、expiry 和 workspace Trust 后才调用现有 `SessionCommands`；
- Chat 增加 approval request 级已提交锁，Tree、Webview 和原生 modal 不能并发提交同一
  decision；resolved 后再次操作、unknown/expired request 和 command/target decision
  不一致均在客户端拒绝，Runtime 仍是最终权威；
- Node 测试 69 项、真 stdio 69/69、安全门禁 9/9 通过；model suite 覆盖 replay
  restoration、pending/recent 选择、path evidence 和 strict target；
- VS Code 1.96.4 Electron empty/workspace/native 三场景通过；native fixture 生成两个
  两文件 `file_apply` plan，验证按 index 只打开 `nested/beta.txt` 一个 diff、forged 与
  resolved decision 无效、approve 原子写入两文件、deny 后两文件均不存在；
- 性能门禁通过，10k delta 13.1 ms，Runtime ready p50 36.9 ms、p95 707.8 ms；
  M5-G019 关闭。T5 尚未开始。

### 2026-08-05：T5 完成

- Electron workspace 场景新增恰好 1 MiB UTF-8 文件的真实 ContextBridge capture gate，
  覆盖 `workspace.fs.readFile`、document text 一致性与 SHA-256，要求 `<100 ms`；
  activation 继续要求 `<100 ms`，Diagnostic Code Action provider 继续要求 `<20 ms`；
- 安全门禁增至 10/10，新增 Changes Tree 的明确 Approve/Deny label、tooltip、keyboard
  command 与 `ThemeIcon` 机械检查；light/dark/high-contrast 不使用硬编码颜色；
- Node 常规测试 70 项通过；真 CodeHelper binary/stdin ACP 测试 70/70，覆盖 crash/replay、
  untrusted write deny、approval、editor context 与 workspace drift；
- 固定 VS Code 1.96.4 Electron empty/workspace/native 三场景通过，覆盖 1 MiB capture、
  四个 selection command、Diagnostic Code Action、multi-file diff/approve/deny；
- 性能门禁通过：10k delta 13.4 ms，Runtime ready 7 次采样 p50 59.8 ms、
  p95 749.1 ms；Changes View 继续无固定轮询；
- `make protocol-contract` 的 ACP/HTTP 各 12 个共享场景通过；`make security-test` 的
  security/guard/plugin/CLI/engine/app/process race 矩阵通过；`go vet ./...` 通过，
  `npm audit --omit=dev` 为 0 个漏洞；
- VSIX 内容审计与安装通过：`codehelper-vscode-0.0.1.vsix` 为 35.6 KiB、仅 7 个允许文件，
  不含源码、脚本、依赖树、source map、`.env` 或 Runtime binary，并由同一 pinned
  VS Code CLI 实际安装和列出；
- `go test ./...` 除既有 macOS Seatbelt 不可用导致的 hermetic benchmark 18/23 外，
  其余包通过；该环境限制不改变独立 V2 release matrix 结论；
- M5-G020 关闭，T0–T5 全部完成，M5 V2 Coding Native 收口；V3 Production 不在本 RFC
  范围内。
