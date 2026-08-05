# RFC-013：M5 V1 VS Code Companion 与后台可见

> 状态：Implemented
> 关联：[ROADMAP §6](../ROADMAP.zh-CN.md)、[RFC-003](./RFC-003-vscode-transport.zh-CN.md)、[RFC-005](./RFC-005-edit-transaction.zh-CN.md)、[RFC-007](./RFC-007-execution-service.zh-CN.md)、[RFC-008](./RFC-008-execution-receipt.zh-CN.md)
> 影响面：`extensions/vscode`、`internal/host/runtimeapi/acp`、`internal/host/runtimeapi/contract`、`internal/runtime/protocol`

## 1. 目标

M5 V1 交付一个薄 VS Code 客户端。Agent loop、工具执行、Guard、审批、持久化、后台调度和用量核算仍由本地 Runtime 负责；扩展只负责编辑器上下文、交互与展示。

V1 必须完成以下闭环：

1. 扩展启动并协商本地 `codehelper host --adapter acp`；
2. Chat 发起 turn 并展示流式正文、工具、审批、诊断和验证；
3. 支持基础 `@file` / `@selection`；
4. 修改前展示原生 diff，并在版本漂移时拒绝静默覆盖；
5. Runtime 或 extension host 重启后按 durable cursor 恢复；
6. 展示 Threads、Agents、Tasks/Jobs、Approvals 和 Usage；
7. Workspace Trust 未开启时保持只读。

退出条件是：用户能在 VS Code 内完成一次“理解 -> 修改 -> 验证 -> 审批”，并能观察跨窗口或 Runtime 重启仍存在的后台任务，无需切回终端。

## 2. 明确不做

- Remote SSH、WSL、Dev Container、Codespaces 和 multi-root 完整矩阵，留到 V3；
- Marketplace、Open VSX、企业 VSIX、binary 签名下载与自动升级，留到 V3；
- V2 的 symbol/diagnostics 深度 Context Bridge、inline edit 和 code action；
- 新造第二套 Agent、审批、任务或 usage 状态机；
- 根据模型文本直接执行 shell、修改文件或授予权限；
- 为了 Tree View 假装 Task/Job 已有实时事件。V1 使用权威查询投影加有界刷新，后续如增加事件必须走共享协议门禁。

## 3. 当前基础与缺口

### 3.1 已有基础

- ACP+ 已有结构化 `initialize`、Operation 信封、会话级事件订阅、cursor replay 和 durable `session/load`；
- `protocol.Operation` / `protocol.Event` 有生成式 JSON Schema；
- ACP 与 HTTP 已共用 `internal/host/runtimeapi/contract` 场景；
- Runtime 已产生流式输出、工具、审批、诊断、验证、Agent 和 receipt 事件；
- HTTP 已有 thread、task、usage、trace 查询；
- workspace journal、edit transaction、Verify Gate 和 Execution Receipt 已落地。

### 3.2 V1 前置缺口

1. ACP 没有 thread 目录、task、agent、usage 等只读查询面；
2. `session/load` 只能恢复事件路由，不能在原 thread 继续新 turn；
3. Task/Job 没有协议事件，后台视图不能只靠 `session/update`；
4. 现有文件工具在 Runtime 内直接写盘，没有宿主可消费的 plan/apply 两阶段；
5. 仓库没有 TypeScript、VS Code 扩展、Node 锁文件和扩展测试门禁；
6. 未知 event 的扩展降级路径尚无 fixture。

## 4. 硬约束

### C1：Runtime 是唯一执行权威

扩展不得依据模型文本、Webview 消息或本地推断直接执行副作用。所有副作用必须由版本化 Operation 或 Runtime 签发的 edit plan 驱动。

### C2：ACP 是 V1 的唯一生产传输

扩展只通过 stdio ACP+ 连接 Runtime，不开启 localhost HTTP 端口。新增读面使用版本化 JSON-RPC 方法，但返回对象必须复用与 HTTP 相同的投影类型和错误语义。

### C3：恢复以 durable cursor 为真相

扩展持久化 `(workspace, session_id, thread_id, last_seq)`。重连先 `session/load`，再从 `last_seq` 回放；不能完整恢复时必须显示 desync，不得假装历史完整。

### C4：Workspace Trust fail-closed

未信任 workspace 时不启动具备写能力的会话，不提交写审批，不应用 edit plan。只读能力也必须由 Runtime posture/ToolGuard 约束，而不只依赖 UI 隐藏按钮。

### C5：编辑应用必须防漂移

edit plan 必须包含路径、操作、before digest、after digest 和统一 diff。apply 前同时校验 plan 身份、有效期、workspace、文件版本和 digest；任何不一致都返回 precondition failure，磁盘零改动。

### C6：未知协议内容可见但不可执行

未知 event 降级为通用只读卡片并保留 kind、时间和安全截断后的 JSON。未知 Operation、edit plan 或 Webview command 一律拒绝。

### C7：协议变更保持三面一致

共享语义必须同时进入 ACP、HTTP 和 Extension contract fixture。只能由编辑器表达的行为进入 extension integration suite，不得污染 Runtime 通用协议。

### C8：Webview 是不可信边界

启用 nonce CSP，禁止远程脚本、`eval` 和任意资源加载。所有 Webview 消息严格校验；路径必须经 workspace URI 解析，不接受任意本地绝对路径。

## 5. 工程决策

### D1：目录与工具链

扩展放在 `extensions/vscode`，使用 npm 锁文件、严格 TypeScript、ESLint 和 esbuild。单元测试使用 Node test runner，VS Code 集成使用 `@vscode/test-electron`。

生成物规则：

- Go 端继续生成 `docs/protocol/runtime-protocol.schema.json`；
- 扩展生成并提交稳定 TypeScript protocol types；
- CI/Make 门禁重新生成后检查工作区无漂移；
- JSON 解码边界保留 `UnknownEvent`，不能让生成联合类型导致未知 kind 崩溃。

### D2：扩展结构

```text
extensions/vscode/src/
  extension.ts              激活、命令和依赖装配
  runtime/process.ts         binary 发现、启动、停止和重启
  runtime/client.ts          JSON-RPC、握手、请求与通知
  runtime/recovery.ts        session/load、cursor replay、desync
  protocol/generated.ts     生成类型
  protocol/decode.ts        严格解码与 UnknownEvent
  state/store.ts             workspace/session/thread/cursor
  chat/                      Chat Webview 与投影
  context/                   @file / @selection
  edits/                     plan、diff、apply 与版本校验
  views/                     Threads/Agents/Tasks/Approvals/Usage
```

Chat 使用 WebviewView；后台对象使用原生 Tree View；审批使用 Webview 卡片配合 VS Code modal。Webview 不直接持有 transport。

### D3：binary 生命周期

V1 查找顺序：

1. `codehelper.binaryPath`；
2. 开发模式下仓库 `bin/codehelper`；
3.当前 extension host 的 `PATH`。

启动参数固定包含：

```text
codehelper host --adapter acp --workspace <root> --data-dir <workspace-state-dir>
  --enable-tools --posture <suggest|never>
```

trusted workspace 使用 `suggest`，让副作用继续经过 Runtime approval；untrusted workspace
强制使用 `never`，且忽略 workspace/workspace-folder 级 `binaryPath`，防止仓库配置选择待执行程序。
授予 Trust 后重启 Runtime 才能改变 posture。V1 不下载 binary。启动前用
`version --json` 校验 binary 身份，随后用 `initialize` 校验 ACP 版本和必需方法；不兼容时拒绝启动。
异常退出按 250/500/1000 ms 最多自动重启三次。

### D4：ACP 查询扩展

V1 增加以下只读方法，全部在 `initialize.methods` 中协商：

```text
thread/list
thread/get
task/list
agent/list
usage/query
```

`thread/list`、`task/list`、`usage/query` 必须有 workspace/session 过滤和 `limit <= 1000`。`agent/list` 支持 parent、closed 与 limit 过滤；现有 durable Agent graph 没有 workspace/session 身份，不能声称具备不存在的隔离，见 M5-G013。`usage/query` 复用 RFC-008 聚合语义。查询面不进入 `protocol.Operation`，因为它们不改变 Runtime 状态。

`session/load` 完成后必须恢复可运行的 thread binding；若引擎上下文无法安全恢复，返回稳定的 `unavailable` Problem，而不是接受一个注定失败的新 turn。

### D5：Chat 投影

扩展按 `(thread_id, turn_id, item_id)` 投影事件：

- `output.delta` 累积正文；
- reasoning 文本增量显示在独立、可折叠的“推理过程”区域；不泄露签名、加密
  provider data 或其他隐藏内容；
- tool start/output/result 形成一张可折叠卡；
- approval/input/verify/diagnostics/receipt 使用专用卡；
- terminal event 封存 turn；
- replay 不含流式增量时，以持久化 terminal/receipt 重建稳定结果。

T3 实际实现使用 `WebviewView`，事件先进入纯 `ChatProjector`，最多每 16 ms
向 Webview 发送一次 snapshot。Webview 启用 nonce CSP、禁用远程资源，动态内容只通过
`textContent` / `createTextNode` 和有限 DOM 标签白名单写入；消息面只有
submit/stop/approval/input 四种严格结构。`reasoning.delta` 文本以 64 KiB 上限保留在
当前 live 投影中，与最终结论分区显示；`reasoning.signature` 和加密 provider data
永不进入 Webview。reasoning 增量不落 durable eventlog，因此恢复后不会伪造不可重放
的推理过程。

### D6：基础 Context Bridge

V1 只支持：

- `@file`：workspace URI、相对路径、document version、content digest；
- `@selection`：URI、range、document version、content digest、用户显式标记。

Runtime 收到结构化引用后读取并裁剪内容。扩展不得默认发送整个 workspace，也不得把 workspace 外文件包装成 workspace 引用。

`turn.start.context` 是最多 8 个引用的结构化数组。digest 为完整文件的 64 位小写
SHA-256，range 使用 VS Code 0-based UTF-16 position。Extension 只从已保存的 active
editor 构造引用；Runtime 使用 sandbox workspace 重新解析 path/URI、拒绝 link、逃逸、
digest 漂移、非 UTF-8 和超限文件，并将单项裁到 64 KiB、总编码限制到 128 KiB。
模型可见 prompt 持久化用于恢复，`turn.started.display_prompt` 只保留原始用户文本供 UI 展示。

### D7：edit plan/apply

Runtime 在写工具执行前生成一次性 edit plan，进入审批事件。扩展用 VS Code diff editor 展示计划内容，用户确认后提交 apply decision。Runtime 仍负责 Guard、journal、原子写入和 receipt；扩展不自行重放 patch。

这与路线图中“插件通过 WorkspaceEdit 应用”的字面方案不同。当前 Runtime 已拥有 sandbox、journal、read-before-edit 和多文件原子事务；把最终写入搬到扩展会绕过这些边界。V1 使用 VS Code 原生 diff 做展示，但 apply 仍由 Runtime 执行。若后续必须使用 `WorkspaceEdit`，需要单独 RFC 证明 journal 与原子性不退化。

T4 通过 ACP Host 专用 `--edit-plan-approvals` 强制执行该边界。`file_write`、
`file_edit`、`file_apply` 在 Guard 审批前无副作用 compose，生成 plan ID、统一 diff、
每个文件的 before/after、存在性与 SHA-256。审批只允许 `once`，decision 必须回传
`plan_id`；批准后 Guard 立即重新 plan，再进入原有 fingerprint、journal 与 atomic
commit。plan mismatch 或 workspace drift 返回稳定拒绝且零写入。扩展以
`TextDocumentContentProvider` 和 `vscode.diff` 展示 before/after，不使用
`WorkspaceEdit`。任意 unified `file_patch` 暂不能可靠生成完整 before/after，在该
模式下 fail-closed，模型应改用上述三个可规划 writer。

### D8：后台视图

- Agent 状态优先读持久化 graph，并用 `agent.*` 事件增量刷新；
- Task/Job 读 `task/list`，只在视图可见或收到相关 turn/agent 事件时有界刷新；
- Approvals 从未解决的 `approval.required` 投影；
- Usage 读 `usage/query`，并用 `usage` / `turn.receipt` 触发刷新；
- 后台完成通知按 terminal transition 去重。

T5 使用六个原生 Tree View：Threads、Agents、Tasks、Jobs、Approvals、Usage。
Threads/Agents/Tasks/Usage 只读 ACP workspace-scoped query；Jobs 是具有 executor 的
durable Task 子集；Approvals 来自 durable replay + live event。没有固定轮询：view
变为可见、Runtime ready 或相关 event 时才做 50 ms 合并刷新，隐藏 view 不自行刷新。
Task/Agent terminal transition 去重通知；replay 只恢复状态，不弹历史通知。

Agent graph schema v10 增加 `workspace_root`、`session_id`，主键改为
`(workspace_root, child_agent_id)`；旧无身份行迁移到不可见 legacy scope。
`agent.*` 是 workspace-visible event：ACP 对本 workspace 的 session 广播并纳入
`session/replay`，但不向其他 workspace 暴露。普通 turn event 仍按 session/thread
隔离。

### D9：多 Chat session、隔离并行与恢复

一个 VS Code workspace root 最多保留 32 个 live Chat。扩展在同一个 ACP Runtime
进程中为每个 Chat 建立独立 `(session_id, thread_id)` binding；`turn.start`、
`turn.cancel`、approval 和 input reply 必须显式路由到所属 session，不能通过当前
选中 Chat 猜测。不同 Chat 可同时存在 active turn。

新建 Chat 默认请求 `session/new.isolation = "worktree"`。Runtime 必须为其创建独立
Git worktree、sandbox、tool registry 和 workspace journal，并在该 thread 首次执行前
注册独立 Engine。worktree 以创建时主 workspace 的 tracked/untracked、非 ignored
内容为基线；主 workspace 不满足 Git worktree 前置条件时创建失败，不降级为共享写。
不同隔离 Chat 不共享 `WorkspaceTurnGate`，因此 provider、工具和验证可真实并行。
扩展首次激活时自动引导的 `Chat 1` 保持 legacy shared 语义，使非 Git workspace
仍可启动；只有用户显式 New Chat 才进入上述 worktree 门禁。

worktree 身份写入 durable session metadata。`session/load` 必须验证 metadata、派生
路径和 worktree HEAD，并在接受下一轮 turn 前重新注册隔离 Engine；路径缺失或身份
不一致时返回 `unavailable`，不得指向主 workspace 继续执行。legacy shared session
保持原语义，可继续恢复，但不声称与同 root 的其他 shared session 真并行。

隔离 Chat 的改动不会自动进入主 workspace。`session/merge` 分为 preview/apply：

1. preview 比较 worktree HEAD 与当前内容，忽略 `.codehelper` 和 Git ignored 文件；
2. 每个待合并路径必须证明主 workspace 仍等于 Chat 基线；
3. Runtime 返回有限 `EditPlan`，plan ID 绑定 before/after、统一 diff 和路径集合；
4. apply 必须携带该 plan ID，在主 workspace gate 内重新规划并验证 ID；
5. 写入复用 expected fingerprint、workspace journal 和原子 file transaction；
6. 成功后刷新 worktree 基线；冲突或 stale plan 为零写入。

扩展 `BindingStore` 升级为 v3：每个 `root_id` 保存 binding map 和
`selected_session_id`，v2 单 binding 首次读取时无损迁移。扩展启动时 load 全部
binding，并从 durable replay 重建 Chat 投影；持久化 cursor 只用于断线接续，不能
替代新 Extension Host 的 UI hydration。每个 Chat 最多展示最近 200 个 turn，裁剪只
影响 Webview 投影，不删除 Runtime durable history。reasoning delta 仍不持久化，
恢复后只展示可重放的 user/output/tool/approval/receipt。

默认 `Chat N` 标题在首个 prompt 被接受后由 Extension Host 根据用户文本本地生成，
不发起额外模型请求。`session/rename` 更新 durable primary thread，BindingStore v3
同步 title；标题最多 48 个 Unicode 字符，失败不得影响已经提交的 turn。

## 6. 实施分片

| 分片 | 内容 | 退出条件 | 状态 |
| --- | --- | --- | --- |
| T0 | RFC、扩展工程、协议类型生成、基础门禁 | 扩展可构建、单测，生成类型无漂移 | completed |
| T1 | ACP 查询面与原 thread 恢复 | 真 binary 可列出状态并在重启后继续 turn | completed |
| T2 | Runtime 生命周期、握手、重连、Workspace Trust | 崩溃恢复不重放已消费事件，未信任时写能力关闭 | completed |
| T3 | Chat、stream、approval、基础 `@file` / `@selection` | 带审批 turn 可完全在 VS Code 交互 | completed |
| T4 | edit plan、原生 diff、受控 apply | 单文件修改可预览；漂移时零写入拒绝 | completed |
| T5 | Threads/Agents/Tasks/Jobs/Approvals/Usage 与通知 | 可观察跨窗口、跨 Runtime 重启后台任务 | completed |
| T6 | 集成、安全、性能、文档与发布门禁 | V1 全部退出条件通过 | completed |
| T7 | isolated session Runtime/ACP | 两个 worktree Chat 真并行，load 后继续原 thread | completed |
| T8 | BindingStore v3 与多 session controller | v2 无损迁移，32 个 binding 可创建/切换/独立路由 | completed |
| T9 | Chat selector、200-turn hydration 与 merge UX | 重启恢复、并行状态和 plan-bound merge 可用 | completed |
| T10 | Electron/发布回归与文档 | 官方 VS Code 覆盖创建、并发、恢复、冲突和 VSIX | completed |

## 7. 测试与发布门禁

### 7.1 常设命令

常设命令：

```text
make vscode-install
make vscode-check
make vscode-test
make vscode-security
make vscode-performance
make vscode-runtime-integration
make vscode-build
make vscode-integration
make vscode-package
```

`make verify` 纳入不需要下载 Electron 的 `vscode-check` 与 `vscode-test`；Electron 集成单独运行，避免普通 Go 验证隐式联网。
真 Go binary/stdio 生命周期使用 `vscode-runtime-integration`；Electron
`vscode-integration` 与 `vscode-package` 在 T6 引入，不用空目标伪装尚不存在的发布能力。

### 7.2 必测场景

- binary 不存在、启动失败、握手不兼容、异常退出；
- stdio 拆帧、并发请求、乱序响应、未知通知；
- cursor replay、分页、retention desync、重复事件去重；
- approval approve/deny/cancel 与过期；
- 未信任 workspace 的读写边界；
- Webview 伪造消息、路径逃逸、CSP；
- edit plan 过期、文件版本漂移、digest 冲突；
- unknown event 降级；
- Task/Agent terminal 通知去重；
- extension activation time、Runtime ready time、事件吞吐和内存。

## 8. 性能预算

- 无 workspace 时激活不启动 Runtime；
- 有 workspace 且视图未打开时，扩展激活目标 `< 100 ms`，不计 VS Code 自身调度；
- Runtime ready 单独记录 p50/p95，不把进程启动隐藏在 activation 指标中；
- Webview 每帧最多一次批量刷新，流式 token 不逐条触发完整 DOM 重绘；
- Tree View 不以低于 1 秒的固定周期轮询；隐藏时停止刷新；
- 单次 replay 默认 256、最大 1000；客户端持续分页但每页让出事件循环；
- Webview 和 transport 日志默认截断单条 64 KiB，完整工具结果仍由 Runtime 持久化。

## 9. Gap Ledger

| ID | 缺口 | 风险 | 分片 | 状态 | 自动化证据 |
| --- | --- | --- | --- | --- | --- |
| M5-G001 | 无 VS Code 工程与构建门禁 | 无法持续交付 | T0 | closed | `make vscode-check vscode-test vscode-build` |
| M5-G002 | TypeScript 协议类型可能漂移 | 客户端误解事件 | T0 | closed | `make vscode-protocol-check`（Go Schema + TypeScript 生成物） |
| M5-G003 | ACP 缺 thread/task/agent/usage 查询 | 后台视图无数据源 | T1 | closed | ACP/HTTP 共享 read-model contract + `TestBinaryInteropReadQueries` |
| M5-G004 | `session/load` 后不能继续 turn | 重启恢复名不副实 | T1 | closed | `TestBinaryInteropRestartLoadsSessionAndReplays` 校验重建历史后的第二个 turn |
| M5-G005 | 无 binary 生命周期与兼容矩阵 | 启动和升级不可判定 | T2 | closed | process/client/supervisor unit + `make vscode-runtime-integration` |
| M5-G006 | Workspace Trust 未接 Runtime 权限 | 未信任目录可写 | T2 | closed | trust unit + `never` posture 真 binary 写拒绝 fixture |
| M5-G007 | Chat/审批无扩展消费者 | V1 主流程缺失 | T3 | closed | projector/Webview tests + 真 binary approval round-trip |
| M5-G008 | 基础编辑器引用无结构化契约 | 上下文泄漏或漂移 | T3 | closed | Runtime resolver tests + 真 binary editor-context fixture |
| M5-G009 | 无 approval-before-apply diff plan | 审批无法判断真实改动 | T4 | closed | Guard plan identity/drift tests + 真 binary zero-write conflict fixture |
| M5-G010 | 后台对象无统一扩展投影 | 状态丢失或重复通知 | T5 | closed | background projector/query tests + 41/41 真 binary restart |
| M5-G011 | unknown event 无降级 UI | 协议新增导致插件崩溃 | T3 | closed | decode + projector read-only unknown-event tests |
| M5-G012 | 无扩展安全/性能发布门禁 | Webview 与激活退化 | T6 | closed | security/performance + pinned Electron + audited/installed VSIX |
| M5-G013 | durable Agent graph 无 workspace/session 身份 | 共享 data-dir 时 Agent 无法安全按 workspace 过滤 | T5 | closed | v9→v10 migration + colliding agent ID 双 workspace fixture |

## 10. 状态更新规则

每完成一个分片必须同时：

1. 将 §6 对应状态更新为 `completed`；
2. 关闭 §9 中有自动化证据的缺口；
3. 记录真实命令、测试结果、性能数据和偏离；
4. 更新 ROADMAP M5 当前进度；
5. 若实现改变协议或安全边界，同步 RFC-003/RFC-005/ARCHITECTURE；
6. 未完成或受环境限制的验证保持 `open`，不得以“代码已写”代替退出条件。

## 11. 进度记录

### 2026-08-04：计划冻结

- 对照 ROADMAP、RFC-003、RFC-005、RFC-007、RFC-008 与生产代码完成基线审计；
- 确认 ACP 查询、原 thread 恢复、plan/apply 和扩展工程是 V1 的四个结构性前置；
- T0 开始实施。

### 2026-08-04：T0 完成

- 建立 `extensions/vscode`：npm lock、严格 TypeScript、typed ESLint、esbuild 和 Node test runner；
- 增加仓库内协议生成器，从 `runtime-protocol.schema.json` 生成 8 种 Operation、36 种 Event 的 tagged union，总计 643 行稳定 TypeScript；
- 增加事件运行时解码边界，已知事件进入 typed union，未知事件保留原始信封供后续通用 UI 降级，畸形信封 fail-closed；
- 根 Makefile 新增 `vscode-install`、`vscode-protocol-check`、`vscode-check`、`vscode-test`、`vscode-build`，其中非 Electron 门禁已接入 `make verify`；
- `extensions/vscode/go.mod` 仅作为根 Go module 的遍历边界，防止 `go test ./...` 进入被 Git 忽略的 `node_modules`；
- `make vscode-check vscode-test vscode-build` 通过：Go Schema 与 TypeScript 生成物无漂移，`tsc --noEmit`、ESLint、3/3 单测和 esbuild 全绿；bundle 为 2.1 KiB（source map 20.9 KiB）；
- `npm audit --omit=dev` 为 0 个漏洞；
- M5-G001、M5-G002 关闭。T1 尚未开始。

### 2026-08-04：T1 完成

- ACP 新增并协商 `thread/list`、`thread/get`、`task/list`、`agent/list`、`usage/query`；列表统一限制为最多 1000 项；
- 新增 `internal/host/runtimeapi/view` 共享 Thread/Turn/Task/Agent/Usage DTO，HTTP 增加 `GET /v1/threads` 与 `GET /v1/agents`，既有 task/usage 读面改用同一投影；
- Thread、Task、Usage 仓储增加 workspace 过滤，ACP 默认锁定 Host workspace；跨 workspace 的 `session/new.cwd` 与 `session/load` fail-closed；
- 生产 ThreadManager 原本已通过 `LatestThreadHistorySeed` 从 durable eventlog 重建完整历史，本阶段没有新造恢复器，而是用双 fixture 真 binary 测试证明重启后第二个 provider 请求包含第一轮 user/assistant 历史；
- `make protocol-contract`、`make acp-interop`、`make api-contract` 通过，覆盖共享 read-model、真实查询、跨进程继续 turn 和 HTTP binary 兼容；
- `go vet ./...`、`make security-test`、受影响包 `-race -p 1`、VS Code T0 门禁通过；`go test ./...` 除既有 macOS Seatbelt 导致的 bench 18/23 外其余包通过；
- M5-G003、M5-G004 关闭；新增 M5-G013，记录 Agent graph 缺少 workspace/session 身份的既有结构性缺口。T2 尚未开始。

### 2026-08-04：T2 完成

- 扩展增加 binary 发现、`version --json` 身份检查、ACP `initialize` 方法/版本协商、stdio JSON-RPC 拆帧与 4 MiB 上限、请求超时和有界 stderr；
- Runtime supervisor 支持优雅 shutdown、250/500/1000 ms 三次 crash recovery、手动重启和配置变更重启；V1 明确只支持 single-root local workspace；
- extension activation 只做同步装配，Runtime start 在后台进行，ready 延迟不计入 activation；
- workspace state 持久化 `(workspace, session, thread, lastSeq)`；重启执行 `session/load` 与 256 项分页 replay，恢复期间缓冲 live event 并按 sequence 接续，retention gap 显式 desync 且不清空 binding；
- Event JSON Schema 补齐 `sequence` 与 operation/thread/turn/item 引用，TypeScript 生成类型不再为 cursor 恢复手写隐式字段；
- trusted workspace 使用 `suggest` posture；untrusted workspace 强制 `never`，并忽略 repository-controlled binary 配置；授予 Trust 后重启 Runtime；
- `npm run check`、20 个常规 Node 测试、esbuild 和 `make vscode-runtime-integration` 通过；真 binary fixture 证明 untrusted 写入被拒，重启保持同一 session/cursor 且不重放已消费事件；
- M5-G005、M5-G006 关闭。T3 尚未开始。

### 2026-08-04：T3 完成

- 增加 CodeHelper Activity Bar 与 Chat `WebviewView`，支持 send/stop、流式正文、tool 卡片、diagnostics/verification/receipt、approval/input 卡片及原生 modal；
- Chat 投影按 turn/call/request identity 去重；reasoning 文本与最终结论分区、有界展示，
  unknown event 以 64 KiB 上限只读展示；Webview 使用 nonce CSP、无远程资源、严格
  command schema 和有限 Markdown DOM 白名单；
- `SessionCommands` 统一提交 `turn.start`、`turn.cancel`、`approval.decision`、`input.reply`，untrusted workspace 在客户端和 Runtime posture 两层拒绝 approve；
- `turn.start` 增加结构化 editor context，HTTP 与 ACP Operation 共用同一 payload；Runtime 校验 canonical workspace path、file URI、SHA-256、UTF-16 range、UTF-8 和大小边界，再有界展开到模型 prompt；
- `turn.started` 增加 `display_prompt`，durable `prompt` 保留模型实际看到的 context 以支持原 thread 恢复，Chat 不展示被展开的文件正文；
- 常规 Node 测试 30/30、Go protocol/app/ACP/HTTP 定向测试与 vet 通过；`make vscode-runtime-integration` 33/33，覆盖 untrusted 写拒绝、approval approve 完整闭环和 editor context 真 provider 展开；
- `make protocol-contract`、受影响包 `-race -p 1` 与 `make security-test` 通过；`go test ./...` 仍仅有既有 macOS Seatbelt 导致的 bench 18/23；
- M5-G007、M5-G008、M5-G011 关闭。T4 尚未开始。

### 2026-08-04：T4 完成

- 增加 `tool.EditPlanner` 与 side-effect-free file transaction compose；`file_write`、`file_edit`、`file_apply` 生成 plan ID、统一 diff、before/after、存在性和 SHA-256，总计划上限 1 MiB；
- `approval.required.edit_plan` 与 `approval.decision.plan_id` 进入生成式协议；计划审批仅允许 `once` 且禁止 replacement arguments；
- VS Code ACP Host 固定启用 `--edit-plan-approvals`，已有 repository allow 或 approval cache 不能绕过逐次计划；`never` deny 仍优先；
- Guard 在批准后重新 plan，ID 不同返回 `edit_plan_stale`，plan ID 不匹配返回 `edit_plan_mismatch`；通过后继续复用 read-before-edit、expected fingerprint、journal 和 atomic commit；
- 扩展新增 `EditPlanPreview`，用只读 `TextDocumentContentProvider` 提供 before/after，并调用原生 `vscode.diff`；Webview 只提交 request/plan identity，不执行写入；
- `file_patch` 在强制计划模式下 fail-closed，因为任意 patch 尚无可靠的完整 before/after composer；V1 使用可规划的 write/edit/apply；
- Go 定向测试覆盖正常计划、broader grant 强制询问、错误 plan identity 与 workspace drift 零写入；`make vscode-runtime-integration` 35/35，真 binary 覆盖正常 apply 与预览后外部修改不被覆盖；
- `npm run package`、`npm audit --omit=dev`、`go vet ./...`、双 Host `make protocol-contract`、受影响包 `-race -p 1` 与 `make security-test` 通过；
- `go test ./...` 仍有既有 macOS Seatbelt benchmark 18/23；并发全仓运行另有 MCP fixture 5 秒启动超时和 shell archive 时序各一次，两个失败测试隔离 `-count=1` 重跑均通过；
- M5-G009 关闭。T5 尚未开始。

### 2026-08-04：T5 完成

- SQLite schema 升至 v10，Agent graph 增加 workspace/session identity 与 workspace 复合主键；legacy 行保留在空 scope，workspace reader 永不暴露；
- graph adapter、subagent manager 与 `runtimeview.Agent` 贯通 identity；双 workspace 可各自持有同名 `agent-1`，重启 hydrate 只读取当前 workspace；
- `agent.spawned`、`agent.status`、`agent.message` 成为 ACP workspace-visible event，支持 live notification 与 `session/replay`，foreign workspace fail-closed；
- 扩展新增严格 `BackgroundQuery` 与统一 `BackgroundProjector`，聚合 Threads/Agents/Tasks/Jobs/Approvals/Usage；Jobs 仅展示 executable durable tasks；
- Activity Bar 新增六个原生 Tree View，按 visibility、Runtime ready 和相关 event 合并刷新，无固定轮询；Task/Agent terminal transition 通知去重，replay 不弹历史通知；
- Node 常规测试 41 项通过，覆盖 read model 解码、投影、approval 恢复、task/agent terminal 去重和 graph-thread replay；`make vscode-runtime-integration` 41/41，覆盖 Runtime 重启后的后台查询；
- Go 定向测试覆盖 v9→v10 migration、双 workspace ID collision、workspace-visible agent event 与 restart hydrate；
- `npm run package`、`npm audit --omit=dev`、`go vet ./...`、双 Host `make protocol-contract`、受影响包 `-race -p 1` 与 `make security-test` 通过；
- `go test ./...` 仍仅有既有 macOS Seatbelt benchmark 18/23；
- M5-G010、M5-G013 关闭。T6 尚未开始。

### 2026-08-04：T6 完成

- 新增独立安全门禁，机械检查 nonce-only CSP、`default-src 'none'`、无远程 Webview 资源、安全 DOM sink、有限消息协议、argv 子进程启动、64 KiB diagnostics 和 editor context 大小边界；`make vscode-security` 5/5 通过；
- `ChatProjector` 去重改为单调 sequence，不再随长会话永久保存 event ID；10,000 条流式 delta 投影为 12.8 ms，低于 1,000 ms 预算，heap 增长低于 32 MiB；
- 真实 Runtime ready 独立采样 7 次，本轮 p50 37.5 ms、p95 711 ms，低于 5,000 ms p95 预算；进程提前退出时会清理 shutdown timeout；
- 固定 VS Code 1.96.4 的 Electron Extension Host 门禁覆盖 empty workspace 与 single-root/autoStart=false；两场景均验证 activation `< 100 ms`、不误启动 Runtime、命令和 Chat/六个 Tree View 注册，并使用 in-memory SecretStorage 隔离用户凭据；
- `@vscode/vsce` 生成 `dist/codehelper-vscode-0.0.1.vsix`，发布物仅 7 个 manifest/runtime 资产、25.47 KiB，不含源码、脚本、`node_modules`、source map、`.env` 或 Runtime binary；同一 pinned VS Code CLI 已实际安装并列出 `codehelper.codehelper-vscode@0.0.1`；
- `make vscode-runtime-integration` 46/46、`make protocol-contract` 双 Host、受影响包 race、`make security-test` 与 `go vet ./...` 通过；未提交变更安全审查未发现可利用问题，`npm audit` 为 0 个漏洞；
- `go test ./...` 唯一红项仍为已知 macOS Seatbelt 不可用导致的 hermetic coding benchmark 18/23，其余包通过；该环境例外不改变 VS Code V1 的独立安全、Runtime、Electron 和发布门禁结论；
- M5-G012 关闭，T0–T6 全部完成。M5 的 V1 Companion + 后台可见收口；V2 Coding Native 尚未开始。

### 2026-08-05：T7–T9 完成，T10 进行中

- ACP 增加 worktree isolation、`session/history` 与 plan-bound `session/merge`；每个隔离
  Chat 拥有独立 Engine、sandbox、tool registry 和 journal，两个延迟 turn 已证明可在
  任一终态前同时进入 started；
- Chat worktree 从主 workspace tracked dirty diff 与 untracked、非 ignored 文件建立
  baseline；merge 对 baseline drift、stale plan 和 read-only posture 均零写入拒绝；
- BindingStore v3 支持每 root 最多 32 个 binding、当前选择、独立 cursor 和 v1/v2
  迁移；controller 的 submit/cancel/approval/input 全部显式绑定 session；
- Chat header、Command Palette 和 Threads Tree 支持创建/选择 Chat；每 session 使用独立
  Chat/Changes projector，重启 hydration 最近 200 个 turn；
- Node 103 项中 99 pass、4 个无 binary 时预期 skip；双 Host protocol contract、
  security race 及 worktree 并行/merge/restore 定向测试通过；
- 官方 VS Code 1.96.4 Electron 覆盖 worktree Chat 创建、选择及多 session Runtime
  重启恢复；真 binary 103/103 覆盖 history hydration 和 cursor 接续；
- universal 与五个 target VSIX 已重建，dry-run release 保持
  `publishable=false, uploaded=false`；T7–T10 全部完成。
