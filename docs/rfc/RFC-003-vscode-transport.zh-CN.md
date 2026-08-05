# RFC-003：VS Code Host Transport

> 状态：Accepted。T0 已落地（V0 ACP+）；T1 已落地（生成式 JSON Schema + `internal/host/runtimeapi/contract` 双 Host 场景表，`make protocol-schema` / `make protocol-contract`），T2 随 M5 插件落地
> 关联：[ROADMAP §6](../ROADMAP.zh-CN.md)、[ARCHITECTURE](../ARCHITECTURE.zh-CN.md)
> 影响面：`internal/host/runtimeapi/acp`、`internal/host/runtimeapi/http`、`internal/runtime/protocol`

本 RFC 只决定 **VS Code 插件与本地 Runtime 之间用什么协议通信**，不涉及插件 UI 形态（那是 RFC-004 / RFC-005 的范围）。

---

## 1. 问题

插件必须是薄客户端：Agent 循环、上下文裁剪、Guard、审批、持久化都留在 Runtime。因此传输层需要能表达一次完整 turn 的全部交互——包括**双向**的审批与用户输入——并且能在 Runtime 崩溃或插件重载后恢复。

现有两个 Host 的实际覆盖度差距很大，这是本 RFC 必须先摆平的事实。

## 2. 现状（按代码核对，非规划）

### 2.1 ACP（`internal/host/runtimeapi/acp/server.go`）

T0 落地后已实现的 JSON-RPC 方法：

| 方法 | 说明 |
| --- | --- |
| `initialize` | 结构化协商：`protocolVersion: 2`、`minSupportedVersion: 1`、`methods`、`operations`、`events` |
| `provider/list`、`provider/select` | 单 provider，select 仅校验一致性 |
| `model/list`、`model/select` | 同上 |
| `thread/list`、`thread/get` | 有界、workspace-scoped thread 目录与 turn 明细（M5 T1） |
| `task/list`、`agent/list`、`usage/query` | 后台任务、Agent 与 RFC-008 usage 只读投影（M5 T1） |
| `session/new` | 建 workspace/session/thread 三元组并绑定事件路由 |
| `session/load` | 重启后用 `(sessionId, threadId)` 重绑，使 `session/replay` 跨进程有意义 |
| `session/submit` | 承载任意 `protocol.Operation`，缺失引用由宿主补全 |
| `session/replay` | 有界游标回放 `{events, nextSeq, truncated}` |
| `session/prompt`、`session/cancel` | 兼容糖，内部与 submit 共用同一条补全与提交路径 |
| `shutdown` | 关闭 |

通知：`session/update`（**直接携带完整 `protocol.Event`**）、`session/desync`（历史不完整告警）。

事件订阅是**会话级**的：`Serve` 启动时建立一条随进程生命周期的订阅，`pump` 按 `event.ThreadID` 找到会话后转发。这是 T0 的前置改动——原先每次 `session/prompt` 临时建订阅且丢弃 `turnID` 不匹配的事件，`thread.compact` / `thread.fork` / `turn.revert` 这些在两个 turn 之间提交的 Operation 产生的事件根本没有出口，只加 `session/submit` 会得到一个黑洞。

原先的缺口现在的状态：审批决策、输入回复、steer、compact/fork/revert 都经 `session/submit` 表达；游标续订经 `session/replay` + `session/load`；thread/task/agent/usage 读面随 M5 T1 补齐；capability 协商见 D4。仍未覆盖：附件。动态工具注册已随 M4 落地。

### 2.2 HTTP/SSE（`internal/host/runtimeapi/http/handler.go`）

已实现：threads create/list/get、turns start/steer/cancel/undo/retry、compact、**approval decision**、tasks、agents、snapshots、usage，事件流 `/v1/events` 支持 `since_seq` 与 `Last-Event-ID` 双通道游标续订（`sse.requestCursor`）。

**结论：HTTP/SSE 在语义覆盖上显著领先 ACP。** ACP 缺的能力，HTTP 大部分已经有了。

## 3. 决策

### D1：一份协议契约，两个传输

唯一权威语义是 `protocol.Operation` / `protocol.Event`。ACP 与 HTTP 都只是**信封**，不得各自重新建模 turn 语义。

理由：ACP 的 `session/update` 已经在直接透传 `protocol.Event`，这个方向是对的；错误的是请求方向被重新发明成一套窄方法。补齐 ACP 的正确做法不是加十几个语义方法，而是让它能承载 Operation。

### D2：VS Code 默认走 stdio ACP+，不走本地 HTTP 端口

即使 HTTP 覆盖更全，仍选 ACP：

- 不占端口、不需要本地认证与 CORS，避免把一个可写工作区的 Agent 暴露在 localhost；
- 生命周期随 extension host 进程，窗口关闭即回收；
- Remote SSH / WSL / Dev Container 下 stdio 天然跟随 extension 实际运行端，不必解决端口转发。

HTTP/SSE 保持其定位：运维控制面与外部集成。**不为 VS Code 维护第二套完整协议。**

### D3：ACP+ 用两个通用方法承载 Operation，而不是逐个加方法

```text
session/submit   { sessionId, operation }        → { operationId, accepted }
session/replay   { sessionId, sinceSeq, limit }  → { events[], nextSeq, truncated }
```

- `session/submit` 接受任意 `protocol.Operation`（`turn.start`、`turn.steer`、`turn.cancel`、`approval.decision`、`input.reply`、`thread.compact`、`thread.fork`、`turn.revert`），一次补齐全部缺口；
- `session/replay` 复用 SSE 已有的游标语义，实现断线续订；
- 现有 `session/prompt` 等窄方法保留为兼容糖，标记 deprecated，内部转成 Operation。

落地偏离（V0 实际形态）：

- **`session/load` 纳入 T0。** stdio 下「断线」就是子进程被重启，内存里的会话绑定全丢，客户端手上只有 `(sessionId, threadId, lastSeq)`。没有重绑，`session/replay` 只在同进程内有意义，T0 的退出条件名存实亡。
- **回放不复用 `Runtime.EventsLimited`。** 它会顺带建一个订阅者，与 pump 的长订阅重复投递，且超限时返回错误而不是分页。改为新增只读的 `Runtime.ReplayEvents(ctx, cursor, limit) ([]Event, more, error)`。
- **引用由服务端补全。** 薄客户端不该负责铸 `thread_id`/`turn_id`/`item_id`。thread 取自会话绑定（payload 指向别的 thread 直接拒绝），turn 缺省取当前活跃 turn（`turn.start` 新铸，两个 turn 之间的 Operation 必须显式给 `turn_id`），item 每次新铸，`thread.fork` 的 `new_thread_id` 缺省新铸并把新 thread 绑到同一会话。带 `idempotencyKey` 时这些 ID 由 key 派生，保证重试的 canonical payload 一致，落到 `SubmitWithKey` 的既有幂等语义上。
- **经 submit 启动的 turn 是 detached 的。** submit 立刻返回收据，终止状态只走事件流；只有 `session/prompt` 会把 RPC 挂到 turn 终止上。
- **未绑定 thread 的事件默认丢弃并计数**（写 `Diagnostics`，关闭时汇总）。
  唯一例外是携带匹配 host `workspace_root` 的 `agent.*`：durable graph 使用专用
  thread，这类事件对本 workspace 的全部绑定 session 可见，并同样进入 replay；
  foreign workspace 仍丢弃。
- **`session/replay` 的 `events` 可能短于扫描窗口。** 为与实时流一致，回放按会话 thread 过滤；`nextSeq` 因此取扫描窗口最后一条的 sequence，而不是返回列表最后一条，否则整页被过滤时客户端会死循环。
- **回放不含流式增量。** `eventlog.ShouldPersist` 不落盘 `output.delta` / `reasoning.delta` / `tool.state` / `turn.compaction`，所以回放是实时流的子序列。UI 需要按已落盘事件重建，不能指望逐 token 重放。
- **`turn.start.context` 是跨 Host 的结构化字段。** HTTP start body 与 ACP 通用 Operation 都使用同一 `EditorContextReference`；Runtime 校验 workspace/path/URI/digest/range 后才展开正文。`turn.started.prompt` 保存模型可见内容用于恢复，`display_prompt` 给 UI 保留原始用户文本。
- **V2 editor context 保持同一信封。** `EditorContextReference` 现支持
  `file|selection|symbol|diagnostics` 与显式 source；symbol/diagnostic metadata 有界且
  仍以完整文件 digest 和 Runtime range 复验为权限边界。Runtime 确认后的逐项
  `editor_context` receipt 同时进入 `turn.started` 与 `turn.receipt`，两个 Host 的共享
  contract 验证 live、terminal 和 durable history 一致；ACP initialize 以字符串 feature
  集广告 `editor_context_v2`，VS Code 启动时强制协商；不新增 VS Code 私有 Operation。
- **edit plan 仍走通用 approval Operation/Event。** `approval.required.edit_plan`
  携带 plan ID、diff 与 before/after，`approval.decision.plan_id` 将 UI 展示内容绑定到
  apply；Runtime 在批准后重算 plan identity，扩展无需新增宿主专用写方法。
- **`agent.*` 是 workspace-visible event。** graph schema v10 在事件和投影中保存
  workspace/session identity；客户端允许这三类事件跨 graph thread 推进同一全局
  cursor，其他事件仍必须匹配绑定 thread。
- **V3 workspace identity 同时绑定 editor URI 与 Runtime path。** `WorkspaceIdentity v1`
  使用 canonical editor URI 的 SHA-256 作为 root ID，携带 editor URI、Runtime 绝对路径
  和 remote kind；CLI launch、ACP initialize 和 `turn.start.workspace_identity` 必须一致。
  Runtime 对 `vscode-remote` authority/root-relative URI 复验后才映射到 Runtime path，
  再走原 sandbox/symlink/digest/range 边界。HTTP start body 使用同一协议字段，双 Host
  contract 以 remote-style editor context 验证 receipt 一致。
- **兼容范围由机读 manifest 驱动。** `internal/compatibility/compatibility.json` 是
  extension/binary range、ACP v1–v2、operation schema、required methods/features 和
  target/channel 的唯一源；Go embed、TypeScript 生成物和 VSIX packaged JSON 都有漂移
  检查。ACP `serverInfo.version` 返回真实 build version，`version --json` 同时报告
  target 与协议范围；VS Code 固定请求 v2，但 server 保留既有 v1–v2 协商范围。

代价与接受理由：通用信封会削弱 JSON-RPC 层的静态可发现性。用 D4 的 capability 协商 + 从 protocol 类型生成 JSON Schema 来补偿，这比长期维护两套会漂移的语义定义更划算。

### D4：版本与 capability 协商必须可判定

`initialize` 返回结构化协商结果，而不是布尔开关：

```json
{
  "protocolVersion": 2,
  "minSupportedVersion": 1,
  "operations": ["turn.start", "turn.steer", "approval.decision", "input.reply"],
  "events": ["turn.started", "turn.receipt", "approval.required"],
  "extensions": { "codehelper.workspaceTrust": 1 }
}
```

- `operations`/`events` 从 protocol 类型枚举生成，避免手写漂移；
- 插件遇到未知 event kind 必须降级展示为通用条目，不得报错崩溃；
- Runtime 遇到未知 operation 返回 `CodeInvalidArgument` 型 Problem，不做猜测执行；
- 跨不兼容主版本不自动升级 binary。
- 自动更新最多每 24 小时检查一次，manifest 使用 ETag/`If-None-Match` 缓存；`304`
  只能复用本地有界缓存，缓存 bytes 仍重新执行签名、channel、revocation 与兼容校验。
- release provenance 绑定 commit 与完整 source tree fingerprint；正式签名构建拒绝
  dirty worktree，dry-run 必须显式记录 `source_state=dirty` 与 `uploaded=false`。

落地偏离：

- 布尔 `capabilities`（`{sessions:{new,prompt,cancel}, ...}`）被**删除**而不是保留并存。它无法表达版本，且仓库里还没有消费它的 VS Code 插件，留着只会让客户端在两套不一致的协商结果间猜。
- `operations`/`events` 由 `protocol.OperationKinds()` / `protocol.EventKinds()` 生成，这两个函数与解码用的工厂表同源——一个 kind 不可能既能解码又不被公示。
- 另加 `methods`：客户端不必靠探测 `-32601` 判断宿主是否支持 `session/submit`。
- `extensions` 暂缺（V0 没有扩展命名空间的消费者）。
- 客户端传入越界 `protocolVersion` 返回 `-32602`，`data` 携带 `{protocolVersion, minSupportedVersion}`。

### D5：Runtime 崩溃恢复以 durable event cursor 为唯一真相

插件持久化 `(threadId, lastSeq)`。重连后 `session/replay` 从 `lastSeq` 续读；durable 模式下 eventlog 是权威来源。`turn.receipt` 已落盘（不在 `eventlog.ShouldPersist` 过滤名单内），因此恢复后仍能还原每个 turn 的执行结论。

Ephemeral 模式（无 `--data-dir`）不承诺跨进程恢复，插件必须在 UI 上区分这两种模式，不能假装恢复成功。持久化 ACP host 强制要求 `--data-dir`，所以 ACP 恒为 durable 模式。

落地补充：恢复分两步——`session/load` 先用 `Sessions.Get` + workspace-scoped `Threads.GetInWorkspace` 校验 thread 归属该 session（重复绑定报 conflict），返回 `latestSeq`（该 thread 已落盘的最大 sequence）与 `runtimeSeq`（全局高水位，含未落盘的流式增量）；随后 `session/replay` 从客户端自己的 `lastSeq` 续读。M5 T1 已用真 binary fixture 钉住原 thread 继续执行：`ThreadManager` 首次访问旧 thread 时通过 `LatestThreadHistorySeed` 从 durable eventlog 重建完整模型历史，第二个 provider 请求必须同时包含重启前的 user/assistant 历史。

M5 T2 已落地第一个真实外部客户端的恢复器：VS Code workspace state 保存
`(workspace, session, thread, lastSeq)`，按 256 项分页 replay；恢复期间先缓冲
`session/update`，durable history 完成后再按 sequence 接续 live event，防止新事件
抢先推进 cursor。`session/desync` 与 `-32001` 都显式报错且不清空 binding。

## 4. 安全约束

1. 插件不得依据模型文本直接执行 shell 或改文件——所有副作用经 Operation 进 Guard；
2. 审批必须展示 `approval.required` 携带的 capability / resources / sandbox / network / scopes 原文，不得简化成"是否允许"；
3. Workspace Trust 未授予时，仅允许只读 Operation；
4. stdio 通道不承载凭据，provider 密钥仍由 Runtime 侧 credential 机制解析；
5. 远程场景下 sandbox 能力按 Runtime 实际所在机器判定，不看 UI 所在机器。

## 5. 分期

| 阶段 | 内容 | 退出条件 | 状态 |
| --- | --- | --- | --- |
| T0 | 会话级事件订阅 + `session/submit` + `session/replay` + `session/load` + 结构化 `initialize` | ACP 能完成一次带审批的完整 turn 并断线续订 | 已落地，`make acp-interop` 覆盖 |
| T1 | protocol → JSON Schema 生成，ACP/HTTP 共用 contract tests | 两个 Host 对同一 Operation/Problem 表现一致 | 已落地，`make protocol-contract` 与 `make protocol-schema` 覆盖 |
| T2 | 窄方法标记 deprecated，插件全量切到 Operation 信封 | 无 Host 依赖 `session/prompt` 语义 | M5，随插件落地 |

**T1 的实施结果与偏离。** 场景表用协议词汇写（`Host` 接口只有 `StartTurn` / `Cancel` / `Decide` / `Live` / `History`），两个 driver 负责把它翻成路由或 RPC 方法；refusal 由 driver 把自己的错误信封映射成 `protocol.ErrorCode` + retryable，映射刻意保持机械——需要「思考」才能作答的 driver 等于把差异藏起来了。第一个真实产出是一处真差异：`session/submit` 一个 cancel 到不存在的 turn，ACP 原先回 `accepted: true` 并把拒绝异步塞进 `operation.rejected`，HTTP 则同步 404。已改为 ACP 在提交前查 turn（只对 cancel / approval.decision / input.reply 三种「作用于已存在 turn」的 kind；steer 与 retry 例外，它们可能创建自己命名的 turn），两侧都同步拒绝、都报 `invalid_argument`；协议词表没有 `not_found`，故不新增错误码，只把 JSON-RPC 侧的 `ErrNotFound` 从 `-32603` 纠正为 `-32602`（前者暗示可重试）。另有一条契约被明文化：live 流带 `eventlog.ShouldPersist` 过滤掉的增量事件，history 只带落盘事件，两个 Host 必须把这条线画在同一处。

T1 原先排在插件之后，现在提前：[ROADMAP §10](../ROADMAP.zh-CN.md) 把 VS Code 整体后置到 M5，中间的 M2（多 Agent 与后台执行）、M3（经济性与追踪）、M4（工具目录治理）都会往协议里加面，而第一个真实外部消费者要到 M5 才出现。没有双 Host contract fixture，这三个里程碑的协议改动就没有任何东西把关——[ROADMAP §11.3](../ROADMAP.zh-CN.md) 已把它写成硬门禁。T2 仍留在 M5，因为「无 Host 依赖 `session/prompt` 语义」这个退出条件需要插件先全量切到 Operation 信封才能验证。

## 6. 未决问题

1. `session/submit` 是否需要背压信号（Runtime 队列满时的显式 Problem vs 阻塞）——仍未决，V0 直接透传 `ErrQueueFull`；
2. 大附件（图片、日志）走 inline base64 还是 CAS handle 引用——倾向后者，需与 RFC-004 对齐；
3. 多 root workspace 是一个 session 多 thread，还是多 session；
4. ~~`session/replay` 的 `truncated` 语义与 event retention 的交互~~ **已回答**：`truncated` 只表示「本页之后还有」，与 retention 无关。retention 裁掉历史后，回放游标落在窗口之前会返回 `-32001` 且 `data` 带 `oldestAvailable`；实时订阅被丢弃后按 `lastSeq` 重订遇到同样情况，宿主主动推 `session/desync {sessionId, threadId, lastSeq, oldestAvailable, reason}`，而不是假装恢复成功。

## 7. 验收

- ACP 与 HTTP 通过同一套 protocol contract tests（已覆盖：`TestACPHostMeetsTheProtocolContract`、`TestHTTPHostMeetsTheProtocolContract`，同一 fixture 与同一场景表）；
- 一次含审批的 turn 可在 ACP 上完整走通（已覆盖：`TestBinaryInteropApprovalRoundTripThroughSubmit`；用户输入与 steer 走同一条 submit 路径，尚无专门 fixture）；
- 杀掉 Runtime 后客户端按 cursor 恢复，且不重复展示已消费事件；原 thread 可在历史重建后继续新 turn（已覆盖：`TestBinaryInteropRestartLoadsSessionAndReplays`、`TestBinaryInteropReplayPagesMatchLiveStream`、`make vscode-runtime-integration`）；
- thread/task/agent/usage 读模型在 ACP 与 HTTP 同源，真实 ACP binary 可查询（已覆盖共享 `read models expose...` 场景与 `TestBinaryInteropReadQueries`）；
- VS Code SessionCommands 可经真 ACP binary 完成 approval round-trip，结构化 editor context 确实进入 provider 且 Chat 只显示 `display_prompt`（已覆盖 `make vscode-runtime-integration`）；
- 未知 event kind 的插件降级路径有 fixture 覆盖（待插件落地）。Host 侧只做到了「不按白名单过滤」：合成一个未知 kind 需要在 `protocol` 里注册一个仅供测试的类型（`Event.UnmarshalJSON` 按 kind 查工厂表，未注册的 kind 根本解不出来），那是为测试污染协议。契约改为双向比对——凡落盘的事件都必须曾经实时到达过客户端，凡实时到达的落盘类事件都必须在 history 里，宿主因此无法悄悄丢掉一类事件。插件侧的降级 UI 留到 M5。
