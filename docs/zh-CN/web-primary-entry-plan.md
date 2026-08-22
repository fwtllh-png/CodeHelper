# Web 主入口与 VS Code/ACP 退役技术实施方案

简体中文

> 状态：已交付。Web Host 已成为主交互入口，VS Code/ACP 已退役；第 20 节记录逐项
> 验收证据。当前产品入口和实现边界仍以 [架构设计](./architecture.md)、
> [快速开始](./getting-started.md)、生成契约和代码为准。

## 1. 决策摘要

CodeHelper 将增加本机浏览器入口 `codehelper web`，并在功能、可靠性、安全和发布门禁
达到要求后，把它提升为默认的人机交互入口。CLI、TUI、Worker 和自动化继续保留；
VS Code 插件与 ACP Host 不进入长期兼容期。在独立迁移分支固化其行为证据后直接删除，
随后只建设 Web 路径；主分支和正式发布只接收完成全部门禁后的最终切换。

目标实现采用以下结构：

```text
Browser
  |
  +-- loopback HTTP
  |     +-- static assets
  |     +-- typed unary RPC
  |     +-- bounded content download
  |
  +-- authenticated WebSocket downlink
            |
            v
internal/host/runtimeapi/web
            |
            v
runtime application services
            |
     Operation / Event / Query
            |
            v
internal/runtime/app
            |
    Agent / Guard / Persistence
```

关键决策如下：

1. `codehelper web` 是一个 Go 进程，直接构造现有持久化 Runtime，并同时提供静态前端、
   一元 API 和事件流。浏览器不启动第二套 Agent Runtime。
2. 浏览器上行使用严格类型化的 HTTP RPC，下行使用一条只承载 Runtime Event 的
   WebSocket。第一版不使用 SSE，不把 ACP stdio 原样封装进 WebSocket。
3. 首版只绑定 `127.0.0.1`。不支持 `0.0.0.0`、局域网、远程 Host、反向代理或公网部署；
   这些能力必须在独立认证与 TLS 方案完成后重新设计。
4. 不建设 ACP/Web 双栈共享 Facade。删除 ACP 前只把其中属于业务 Authority 的 Session
   创建、冷恢复、ID 分配、Operation 准备、History 分页和事件归属逻辑迁入
   `internal/runtime/app`；ACP Transport 随后立即删除。
5. 前端使用 TypeScript、React 和 Vite。业务状态与重连逻辑位于不依赖 React 的对象层，
   React 只负责呈现和交互。
6. 前端是第一方静态组合，不在第一版引入 `deepseek-harness` 的 Cordis 浏览器插件系统。
   CodeHelper 的 Plugin、Skill、MCP 仍由 Runtime 管理，Web 只投影其状态。
7. Runtime Event、Session Profile、Receipt、Approval、Input、Checkpoint 和 Plan 继续是
   权威事实。浏览器缓存和 UI Store 都是可丢弃 Projection。
8. VS Code 和 ACP 在迁移分支早期删除，但该分支不得在 Web 完成前合入或发布。删除前
   必须先把关键行为转换为 Transport-neutral Contract、Fixture 和 Feature Ledger。
9. ACP 不作为 Web 的兼容基线或运行时依赖。任何现有消费者都必须迁移到 Web API 或
   明确停止支持，不保留 ACP Shim、Adapter 或双协议测试。
10. 页面 Style、交互几何和 Motion System 深度参考 `deepseek-harness` 的 Web 实现，
    但使用 CodeHelper 自己的 Token、品牌和组件边界。视觉与动画质量是 Release Gate，
    不是功能完成后的装饰工作。
11. CLI/TUI 保留为非并发的 Secondary Interactive Host；Web 持有 Interactive Owner
    Lease 时它们 Fail Fast。Worker/Automation 通过独立 Durable Lease 与 Web 并存。

## 2. 背景与当前基线

### 2.1 可直接复用的能力

当前 Runtime 已具备 Web Host 所需的大部分后端语义：

| 能力 | 当前所有者 | Web 复用方式 |
| --- | --- | --- |
| Operation 校验与提交 | `internal/runtime/protocol`、`internal/runtime/app` | 原样调用 `SubmitWithKey` |
| 有序事件、回放和慢消费者处理 | `internal/runtime/app/eventhub` | 通过 `EventsLimited` 与 `ReplayEvents` 暴露 |
| Session Lifecycle/Profile | `internal/runtime/app`、`internal/persist/session` | 通过窄化 Runtime Service 查询和修改 |
| Thread、Task、Usage、Agent Read Model | `internal/host/runtimeapi/view` 及各 Repository | 通过类型化 Query 暴露 |
| Approval、Input、Cancel、Steer | Runtime Operation | 浏览器只提交绑定身份的 Operation |
| Checkpoint、Plan、Turn Recovery | `ArtifactService` | 通过 Runtime Service 暴露 |
| Edit Plan、Workspace Journal、Sandbox | Runtime/Adapter/Security | Web 只展示和提交决策 |
| Provider/Model/Tool Catalog | `wire.Session`、`SessionService` | 通过只读 API 暴露 |
| OS Keyring | `internal/security/keyring` | 替代 VS Code SecretStorage |
| Event Trait 和 JSON Schema | `internal/runtime/protocol` | 生成浏览器类型与 Exhaustive Projector |

### 2.2 必须先消除的结构问题

ACP Server 目前不仅负责 NDJSON JSON-RPC，还持有一部分所有 Host 都需要的行为：

- 创建和加载 Session；
- 创建 Workspace/Session/Thread Seed；
- 恢复隔离 Worktree 与 Session Profile；
- 为薄客户端补齐 Thread、Turn、Item 和 Operation Identity；
- 校验 Operation 与 Session/Thread 的归属；
- 分页 Session History；
- 将 Event 归属到 Session 和 Child Thread；
- 将 `protocol.Problem` 映射为 Transport Error。

若删除 ACP 时直接丢弃这些逻辑，Web 会遗漏关键功能；若把它们复制进 Web，又会形成
第二套 Session 控制面。正确做法是在删除 ACP 的同一迁移分支中，先用
Transport-neutral Test 固化行为，再把业务 Authority 移入 `internal/runtime/app`，
随后删除 ACP，仅让 Web 调用新的窄化 Runtime Service。

### 2.3 VS Code 能力不能按 API 名称迁移

VS Code 插件同时承担了四类职责：

1. Runtime 进程发现、启动、重启和 ACP 协商；
2. Session/Turn 的 Client State 与 Event Projection；
3. Chat、Diff、Approval、Input、Profile、Extension 等 UI；
4. 活动编辑器、Selection、Symbol、Diagnostic、SecretStorage 和原生导航。

Web 入口在同一个 Go 进程中，不需要复制 VS Code Supervisor 或 ACP Client；但必须为
第 3、4 类能力提供浏览器适配。迁移目标是语义等价，不是 VS Code API 等价：

| VS Code 能力 | Web 替代 |
| --- | --- |
| Extension Host 启动 Runtime | `codehelper web` 进程直接装配 Runtime |
| `workspaceState` Cursor | 浏览器 IndexedDB 缓存，Server Snapshot 可独立重建 |
| SecretStorage | OS Keyring，浏览器只提交一次 Secret Value |
| Native File/Selection Picker | Runtime Workspace Browser + 只读代码视图范围选择 |
| VS Code Diagnostics | Runtime Verification/Diagnostic Receipt；LSP 诊断后续接入 |
| Native Diff | 浏览器 Diff Viewer，数据来自 Runtime Edit Plan/Journal |
| `vscode.open` | Web 内查看；可选的固定本机 Editor Opener 单独受控 |
| Theme Token | CodeHelper Web Semantic Token + 系统明暗/高对比偏好 |

## 3. 目标与非目标

### 3.1 目标

- 提供一个无需编辑器插件即可完成日常 Coding Agent 工作的本机 Web UI；
- 保持一个 Runtime Authority，所有写入继续经过 Guard、Policy、Approval、
  Constitution、Journal 和 Sandbox；
- 页面刷新、浏览器崩溃和 WebSocket 重连不重复提交 Prompt 或 Side Effect；
- 支持 Session 创建、搜索、切换、重命名、置顶、归档、删除和恢复；
- 支持流式输出、Reasoning、Tool、Approval、Input、Verify、Receipt 和 Terminal；
- 支持 Profile、Provider、Model、Tool Allowlist、Checkpoint、Plan、Usage、
  Task、Subagent、Extension 和 Runtime Health；
- 支持 Workspace 文件浏览、搜索、只读预览、范围选择、变更 Diff 和资源定位；
- 提供稳定、紧凑、适合长时间工程工作的视觉系统和状态驱动动效；
- 让前端协议、事件分类、容量限制和兼容范围可生成、可测试、可回放；
- 将 Web 静态资源打入发布二进制，不要求最终用户安装 Node.js；
- 在独立迁移分支固化行为证据后删除 VS Code 实现及其发布链；
- 在同一分支删除 ACP Server、CLI Adapter、兼容清单和专用测试；
- 未达到全部 Web 验收门禁时，整个迁移分支不得合入主分支或进入 Release。

### 3.2 非目标

- 第一版不提供远程访问、账号体系、多用户隔离或云端 Control Plane；
- 第一版不绑定 `0.0.0.0`，不接受 `--trusted-host` 作为认证替代；
- 第一版不提供完整 IDE、通用文件编辑器、Debugger 或 Git GUI；
- 不从浏览器直接执行 Shell、写文件或调用 Provider；
- 不把浏览器 Local Storage、IndexedDB 或 React Store 作为恢复事实来源；
- 不把 ACP 作为浏览器内部 Transport；
- 不为 Web 建立独立 Tool、Approval、Session 或 Compaction 状态机；
- 不在第一版实现动态前端 Plugin/HMR Runtime；
- 不把“页面可打开”和“能够完成一次聊天”视为替代 VS Code 的完成标准。

## 4. 对 `deepseek-harness` Web 实现的借鉴

### 4.1 应直接借鉴的做法

| `deepseek-harness` 做法 | CodeHelper 采用方式 |
| --- | --- |
| Web Command 启动 Host 与浏览器表层 | 增加 `codehelper web` |
| Host 同时提供静态资源与 API | Go HTTP Server 同源提供 |
| Unary 调用与 Event Downlink 分离 | HTTP RPC 上行，WebSocket 下行 |
| Web Server 不理解 Agent 业务 | `web.Server` 只负责路由、限制和生命周期 |
| API Gateway 才连接业务 Service | `webapi.Handler` 调用窄化 Runtime Application Service |
| Host 图注入 Boot Manifest | `/api/v1/bootstrap` 返回运行期只读信息 |
| 前端先建立对象层，再接 React | Connection/Session/Projection 与组件分离 |
| 先加载基础设施，再显示完整 App | Bootstrap、协议协商、Snapshot 完成后进入主界面 |
| 静态文件 Traversal 拒绝与 SPA Fallback | `fs.Sub`/Manifest 查找，未知 API 不回退 HTML |
| Loopback 默认和 Host/Origin 防护 | 更严格地固定 `127.0.0.1` 并增加 Capability Token |
| Semantic Token、明暗主题和分层 Surface | 建立 CodeHelper 自有 `--ch-*` Token |
| 三栏可拖动布局和 Panel 让步链 | Session Rail、Transcript、Detail 使用稳定 Track |
| Sticky Composer 与单一主滚动区 | 输入区不随 Transcript 滚走，避免多层滚动竞争 |
| Running Shimmer、Disclosure、Panel Transition | 建立状态驱动 Motion Matrix |
| `prefers-reduced-motion` 全覆盖 | Full、Reduced、Still 三种 Motion Mode |
| Hover 控件不改变布局 | 使用 Opacity/Overlay 保留稳定几何 |
| Tool、Terminal、Read、Diff 专用呈现 | 按 Runtime 结构化数据选择 Renderer |
| 浏览器 E2E 使用真实 Host 和真实 HTTP | Playwright 驱动编译后的 `codehelper web` |
| Replay Fixture 驱动大量 UI 场景 | 复用 Runtime Event Golden 进行 Keyless Replay |

### 4.2 不应照搬的部分

1. **Cordis Browser Plugin Graph**：CodeHelper 当前没有前端插件产品契约。引入动态
   Browser Plugin 会同时扩大加载、签名、HMR、CSS 隔离和版本协商范围。第一版使用
   静态 Feature Module 和显式 Route/Panel Registry。
2. **无认证的 LAN 模式**：Host/Origin 校验能防 DNS Rebinding 和普通 Cross-site
   请求，但不是身份认证。CodeHelper 的 API 可以批准命令和写文件，因此第一版只允许
   Loopback，并增加每进程随机 Capability Token。
3. **超大统一请求体**：CodeHelper 当前 Prompt、Context、Image 都有较小的明确上限。
   Web 继续使用领域上限，不采用 100 MiB 级别的通用 JSON Buffer。
4. **Host-specific System Prompt**：第一版不把随机端口或浏览器实现细节写入稳定
   Prompt Prefix。若需要表达“当前交互表层”，应作为结构化、可持久化的 Turn Context，
   而不是不可恢复的隐式文本。
5. **前端承担业务扩展组合**：CodeHelper Plugin/Skill/MCP 的启停和能力事实继续属于
   Runtime。Web 只消费 Catalog、Health 和 Receipt。

## 5. 目标架构

```text
cmd/codehelper
    |
    +-> cli/web command
            |
            +-> config.Load
            +-> state.Open
            +-> wire.NewExec
            +-> runtime/app narrow services
            +-> runtimeapi/web.Server
                    |
                    +-> Static Asset Handler
                    +-> Bootstrap/Auth Handler
                    +-> Unary API Handler
                    +-> Event WebSocket Handler
                    +-> Content/Resource Handler

Browser
    |
    +-> connection controller
    +-> session manager
    +-> event projector
    +-> React view model adapter
    +-> feature views
```

### 5.1 不变量

1. Web Handler 不 Import Provider、Tool Executor、Agent Engine 或 Sandbox 实现。
2. 所有 Mutation 最终成为 `protocol.Operation` 或调用 Runtime-owned Service。
3. 所有有副作用的 Tool 仍只从 Agent Engine 进入 Guard。
4. Session、Thread、Turn、Request、Plan、Checkpoint、Resource 都使用稳定 ID。
5. 页面刷新只执行 Query、Hydration 和 Cursor Replay，不自动重发 Mutation。
6. Event Sequence 是跨连接顺序依据；HTTP Request ID 只关联一次调用。
7. Browser Cache 可随时删除，删除后仍能从 Runtime 重建当前可用状态。
8. Browser 断开不取消 Turn；进程关闭才执行受控 Drain/Cancel。
9. UI 不能从文本推断 Pending、Terminal、Verified 或 Changed 状态。
10. Web Host Failure 不能把已经提交的 Runtime Terminal 改写为失败。

### 5.2 Runtime 数量

首版一个 `codehelper web` 进程只绑定一个 Workspace，并构造一个持久化 Runtime：

```bash
codehelper web \
  --workspace . \
  --config ./codehelper.toml \
  --data-dir ~/.codehelper/v1 \
  --host 127.0.0.1 \
  --port 0
```

理由：

- 当前 `wire.NewExec`、Workspace Identity、Sandbox、Repo Index 和 Session Worktree 都以
  一个 Workspace 为清晰边界；
- 多 Workspace 会要求一个 Host 同时管理多个 Runtime、Store、Extension Generation、
  Scheduler 和 Shutdown 生命周期，风险明显高于入口迁移本身；
- 用户可以在不同仓库启动独立进程，操作边界和故障域更清楚。

未来若要支持一个页面管理多个 Workspace，应新增显式 `RuntimeRegistry`，每个 Workspace
仍拥有独立 Runtime 和 ResourceStack，不能让一个 Runtime 动态切换根目录。

### 5.3 Interactive Runtime Owner Lease

Web 成为长期运行入口后，同一 Data Dir 和 Workspace 不能被两个**交互型 Runtime**
同时接管。这里的交互型 Owner 是会激活用户 Session、恢复普通 Pending Turn 并接受
即时 Operation 的 Web、CLI `exec` 或 TUI，不包括只按 Durable Task Claim 执行的
Worker。

启动交互型 Runtime 时获取一个由 `data_dir + workspace_root_id` 定位的进程租约：

- 使用操作系统文件锁持有整个进程生命周期；
- 锁文件权限为 `0600`，只记录 PID、Start Time、Build、Owner Kind 和公开 URL，
  不记录 Token；
- 获取失败时执行 Readiness Probe，若已有健康 Web 实例则报告其 URL，不能再构造
  第二个交互型 Runtime；
- 第一版不建设 CLI/TUI 到 Web 的隐藏 Remote Client。Web 持有租约时，执行型 CLI/TUI
  Fail Fast 并提示关闭 Web 或在该页面继续；Web 未运行时，它们仍可直接构造 Runtime；
- 读取型 CLI 可继续访问 Durable Read Model，但不得触发 Recovery、Claim 或写 Journal；
- 只有确认文件锁已释放时才能接管，不能仅根据 PID 文件文本判断 Stale；
- `SIGINT/SIGTERM`、启动回滚和正常关闭都通过 ResourceStack 释放。

Worker 和 Automation 使用不同的所有权模型：

- `worker run` 不获取 Interactive Runtime Owner Lease，仍通过 Task Lease、Turn Lease、
  WorkGraph Epoch 和 Workspace Turn Gate 与 Web 并存；
- `wire.NewExec` 增加显式 `interactive`/`worker` Runtime Role。Worker Role 只能恢复
  绑定到自身 Durable Task Claim 的 Turn，不能扫描或接管普通交互 Session；
- Automation Tick、Task Enqueue/List/Pause 可以在 SQLite Transaction 下与 Web 并存，
  但真正执行仍必须先获得 Task 与 Workspace Fence；
- 必须增加 Web + Worker 同时运行、Worker Crash Takeover、Interactive Owner 冲突和
  Automation Exactly-once 的多进程测试。

Interactive Lease 只解决同一用户进程之间的交互执行 Owner 冲突，不替代 SQLite
Transaction、Task/Turn Lease、WorkGraph Epoch Fence 或 Workspace 写冲突控制。

## 6. 代码所有权与建议目录

| 路径 | 职责 |
| --- | --- |
| `internal/host/cli/web.go` | `codehelper web` 参数、启动、信号和退出码 |
| `internal/host/runtimeapi/web` | HTTP Server、路由、认证、限制、WebSocket 和 Shutdown |
| `internal/host/runtimeapi/web/webcontract` | Web Request/Response/Event Envelope 和严格 Codec |
| `internal/host/runtimeapi/web/webassets` | 内嵌前端资源、Manifest、ETag 和 MIME |
| `internal/runtime/app` | Session、Operation、History、Artifact 和 Activation Service |
| `internal/runtime/protocol` | Operation/Event/Receipt 和 Event Trait |
| `web/src/runtime` | React-free Connection、Session、Cursor、Projection |
| `web/src/ui` | React 页面、布局和 Feature View |
| `web/src/protocol` | 生成的类型、Trait 和边界 Decoder |
| `web/tests` | Unit、Component、Replay、Playwright E2E |
| `scripts/webprotocolgen` | Go Schema 到 TypeScript/Web Contract 生成 |
| `docs/protocol/web-host.schema.json` | Web Host 传输契约 |

不建议把 Web Server 放入 `internal/runtime/app`。HTTP、Header、Origin、WebSocket 和静态
文件都属于 Host Transport；Runtime 只提供可调用的 Operation/Event/Query 能力。

## 7. Runtime Host Port

### 7.1 目标

不新增汇总全部 Repository 的 `runtimeapi/control.Service`。ACP 删除后只有 Web 需要
HTTP Application Facade，长期保留一个“多 Transport 通用层”只会把旧架构换一个名字。

应在 `internal/runtime/app` 中补齐窄化 Service：

```go
type SessionService interface {
    CreateSession(context.Context, CreateSessionRequest) (SessionBinding, error)
    ActivateSession(context.Context, SessionID) (SessionBinding, error)
    ListSessions(context.Context, protocol.SessionListQuery) (protocol.SessionList, error)
    UpdateSession(context.Context, UpdateSessionRequest) (protocol.SessionSummary, error)
}

type OperationService interface {
    SubmitForSession(context.Context, SubmitSessionOperation) (OperationReceipt, error)
}

type HistoryService interface {
    Snapshot(context.Context, SessionID) (SessionPresentationSnapshot, error)
    History(context.Context, SessionHistoryQuery) (SessionHistoryPage, error)
    Events(context.Context, protocol.Cursor, int) (<-chan protocol.Event, error)
}
```

这是职责示意，不要求为每个接口新建 Package。已有 `app.Runtime`、`SessionService` 和
`ArtifactService` 可以按职责扩展；Web Handler 只依赖其使用的方法。Task、Usage、
Agent 和 Extension 查询同样通过窄接口注入，不能暴露 Repository 集合或
`wire.Session` 作为 Service Locator。

### 7.2 删除 ACP 前必须迁入 Runtime 的行为

- `CreateSession`、`ActivateSession`、`CloseSession`；
- Session/Thread/Workspace 归属校验；
- Worktree Provision/Restore/Discard；
- Session Profile Restore；
- `SubmitSessionOperation`；
- Thread、Turn、Item、Operation ID 的生成和幂等派生；
- Approval/Input Request 的绑定校验；
- Session History 和 Runtime Replay 分页；
- Child Thread 与 Session 的归属解析；
- Session、Task、Agent、Usage、Extension Query；
- 可恢复的 `protocol.Problem` 语义。

迁移规则：

1. 先把每项行为写成 `internal/runtime/app` 的 Unit/Integration Test；
2. 再移动实现，测试不得通过 ACP Frame 调用；
3. 确认 `internal/runtime/app` 不 Import ACP、HTTP 或 Browser 类型；
4. 删除 ACP Server 和全部 JSON-RPC Adapter；
5. Web 只实现 HTTP/WebSocket 到 Runtime Service 的机械映射。

Web 第一版不暴露 Dynamic Tool 注册。浏览器是产品 UI，不是受信任的 Tool Executor。
ACP 删除时 Dynamic Tool Client Bridge 一并删除；Runtime 内部 Dynamic Tool Manager
是否保留，由仍存在的实际 Consumer 决定，不能因历史 ACP API 自动保留。

### 7.3 Runtime 所有权修正

真正改变 Durable Session State 的行为必须归入 `app.SessionService`：

- 创建 Session/Thread Seed；
- 激活 Active Thread；
- 恢复 Profile 和 Worktree；
- 删除前的 Quiescence 与未合并变更检查；
- Session Presentation Snapshot 的提交与读取。

Web Handler 负责把 Transport DTO 转成这些调用，并维护 Connection-local Subscription；
它不能生成业务 ID、判断 Session 状态或成为新的业务状态机。

### 7.4 Session Activation Manager

Web 页面可能在多个 Tab 中打开，也可能只查看而不执行。Runtime Application 增加
`SessionActivationManager`：

- 按 `session_id` 对 Cold Restore 去重；
- 校验 Session 属于当前 Workspace；
- 恢复 Active Thread、Profile 和隔离 Worktree；
- 对已激活 Session 返回相同 Binding；
- 只在 Session Quiescent 时执行 LRU Release；
- 不因浏览器 Tab 关闭释放或取消 Active Turn；
- 进程关闭时统一 Drain。

该 Manager 的状态是进程缓存，不是 Durable Authority。重启后从 Repository 重建。

## 8. Web 传输契约

### 8.1 Endpoint

| Endpoint | 方法 | 用途 |
| --- | --- | --- |
| `/`、`/assets/*` | GET/HEAD | 内嵌 SPA |
| `/healthz` | GET | 只返回进程 Ready/Draining，不含 Workspace 数据 |
| `/api/v1/bootstrap` | GET | Protocol、Feature、Workspace、当前进程连接配置 |
| `/api/v1/<namespace>/<method>` | POST | 严格类型化 Unary RPC |
| `/api/v1/events` | WebSocket | Replay + Live Runtime Event Downlink |
| `/api/v1/content/<handle>` | GET | 经授权、有界的 Runtime Content/Artifact 下载 |

API 路径必须在 SPA Fallback 之前匹配。未知 `/api/*` 返回 JSON `404`，不能返回
`index.html`。

### 8.2 Unary RPC

API 使用名字空间化的 POST Endpoint，而不是把 ACP JSON-RPC Frame 直接搬到 HTTP：

```text
POST /api/v1/session/list
POST /api/v1/session/create
POST /api/v1/session/update
POST /api/v1/operation/submit
POST /api/v1/checkpoint/restore
POST /api/v1/turn/recover
```

每个 Request：

- `Content-Type` 必须是 `application/json`；
- 使用 `DisallowUnknownFields`；
- 只允许一个 JSON Value；
- 带 `X-CodeHelper-Request-ID`；
- Mutation 带最长 256 Bytes 的 `Idempotency-Key`；
- Body 在读取前检查 `Content-Length`，读取时再次执行硬上限；
- Client Disconnect 取消 Query，但不能撤销已被 Runtime 接受的 Operation。

成功 Response：

```json
{
  "version": 1,
  "request_id": "req_...",
  "result": {}
}
```

失败 Response：

```json
{
  "version": 1,
  "request_id": "req_...",
  "problem": {
    "code": "conflict",
    "message": "session already has an active turn",
    "retryable": true,
    "details": {}
  }
}
```

HTTP Status 只表达 Transport Class：

- `400`：Malformed 或 Invalid Argument；
- `401`：Capability Token 缺失或错误；
- `403`：Host/Origin/Workspace Trust 拒绝；
- `404`：未知 Endpoint 或 Resource；
- `409`：Revision、Busy、Identity Conflict；
- `413`：请求体超限；
- `429`：Admission/Queue/Rate Limit；
- `503`：Runtime Draining 或暂不可用；
- `500`：未分类内部错误。

前端行为以 `problem.code` 和结构化 Details 为准，不解析错误文本。

### 8.3 API 方法组

第一版最小完整集合：

| Namespace | 方法 |
| --- | --- |
| `system` | `describe`、`readiness`、`diagnostics` |
| `session` | `create`、`activate`、`list`、`status`、`update`、`delete`、`history`、`merge-preview`、`merge-apply`、`export` |
| `operation` | `submit` |
| `profile` | `get`、`update` |
| `provider` | `list` |
| `model` | `list` |
| `tool` | `catalog` |
| `mcp` | `health` |
| `checkpoint` | `list`、`get`、`restore`、`fork` |
| `plan` | `get`、`implement` |
| `turn` | `recover` |
| `workspace` | `browse`、`search`、`resource`、`diff` |
| `task` | `list` |
| `agent` | `list` |
| `usage` | `query` |
| `extension` | `list`、`control` |
| `credential` | `status`、`set-keyring`、`clear-keyring`、`validate` |

`session/list` 的 `SessionListQuery.Query` 同时承担 Session Search，不再增加同义
Endpoint。Automation 的创建、运行和暂停继续由 CLI 管理；Web 只投影其产生的
Task/Run 状态。Builtin Memory 继续通过 Extension 状态和 Tool Catalog 暴露，第一版
不增加绕过 Tool Guard 的 Memory CRUD 页面。

`operation/submit` 只接收 Web Contract 中显式允许的 Operation Intent，不直接反序列化
任意当前版本 `protocol.Operation`。Server 负责生成 Operation ID、Created At 和允许
省略的 Thread/Turn/Item Identity，再调用 `SubmitForSession`。

新增机器可读 `web-operation-exposure.json`：

```text
operation_kind
disposition        exposed | denied
web_intent_schema
identity_binding
admission_policy
required_surface
qualification
```

- Start、Cancel、Steer、Approval、Input、Compact、Fork、Revert 只有在登记为
  `exposed` 且存在对应页面状态时才能提交；
- WorkGraph Operation 默认 `denied`，只有 Feature Ledger 和权限策略完成后逐项开放；
- 新增 `protocol.OperationKind` 而未在该表显式选择 `exposed` 或 `denied` 时，生成与
  CI 必须失败；
- Session/Workspace 归属校验、Request Binding、Revision 和 Runtime Admission
  仍在每次提交时执行，Operation Allowlist 不能替代这些校验。

### 8.4 Event WebSocket

连接请求携带最后成功应用的 Cursor。WebSocket 建立后：

1. Client 必须在短超时内发送唯一一条 `authenticate` Frame；
2. Server 验证 Capability Token 后才创建 Runtime Subscription；
3. 后续 Client Data Frame 一律以 Policy Violation 关闭；
4. Server 发送 `hello`，随后发送 Replay Event，再无缝进入 Live Event；
5. 每个 Event Frame 携带 `sequence`、`session_id` 和原始 `protocol.Event`；
6. Client 只有在 Reducer 成功提交后才持久化 Cursor；
7. 同一或更小 Sequence 幂等忽略；更大的 Sequence 只要求严格单调，不要求数值相邻；
8. 慢消费者使有界队列关闭连接，Client 从已提交 Cursor 重连；
9. 只有 Server 从 Durable Store 得到 `CursorGapError` 或 Retention Boundary 时，才返回
   结构化 `desync` Frame 后关闭；Client 不根据 Sequence 数值空洞自行推断丢失。

Runtime 会为不持久化的 Streaming Noise、失败 Append 或中断 Reservation 永久保留
Sequence，因此合法回放可以从 `41` 跳到 `44`。Cursor 语义是“已应用到该全局水位”，
不是无空洞的数组下标。Replay/Live 切换由 Server 的原子订阅和明确 Watermark 证明，
而不是由浏览器检查 `next == current + 1`。

示意：

```json
{
  "type": "event",
  "protocol_version": 1,
  "session_id": "session_...",
  "sequence": 42,
  "event": {}
}
```

WebSocket Close Code 建议：

| Code | 语义 |
| --- | --- |
| `1000` | 正常进程关闭 |
| `1008` | Auth、Origin 或协议违规 |
| `1011` | Server 内部错误 |
| `1012` | Runtime 重启 |
| `1013` | 慢消费者或暂时过载，按 Cursor 重连 |

### 8.5 Snapshot 与 Hydration

只靠浏览器缓存无法支撑主入口。新增 Transport-neutral
`SessionPresentationSnapshot`：

```text
projection_version
session_id / thread_id
through_sequence
turn summaries
pending approval/input identities
latest terminal and receipt references
checkpoint/plan summary
history_truncated_before
```

它是从 Durable Event 和 Read Model 派生的可重建 Projection，不是业务 Authority。
“一致读取”必须实现为明确的 As-of Watermark Contract：

1. 从 Durable Event Store 捕获高水位 `H`；
2. Snapshot Builder 只重放或读取 `source_sequence <= H` 的 Session Presentation
   数据，Pending Identity 必须由同一范围内的 Durable Fact 重建；
3. 没有 Source Sequence 或 Revision Fence 的 Read Model 不能混入 Snapshot，必须先
   补齐可验证水位，或作为 Snapshot 之外的独立 Revisioned Query 返回；
4. Snapshot 返回 `through_sequence = H`；
5. Server 使用 `EventsLimited(H, limit)` 原子进入 Replay + Live；构建 Snapshot 期间
   产生的 Event 从 `H` 之后补齐；
6. 若构建期间发生 Retention Gap，丢弃结果并从新水位重建，不能返回混合代际数据。

Snapshot Builder 不读取无法按 `H` 截断的 Runtime Mutable Map。若后续为了性能增加
Materialized Presentation Snapshot，它必须携带 Source Sequence、Projection Version
和内容 Digest，并由 Durable Reconcile Test 证明 Crash 后不会越过已提交 Event。

当 Event Retention 已删除更旧历史时，Snapshot 必须带
`history_truncated_before`，UI 显示明确边界。不得用 LLM Summary 伪造历史 Transcript。

## 9. 安全设计

### 9.1 威胁模型

即使只监听 localhost，也必须假设：

- 用户浏览器中存在恶意站点；
- 攻击者尝试 CSRF、DNS Rebinding 和 WebSocket Hijacking；
- 请求可构造超大 Body、慢速 Body、非法 Header、路径遍历和压缩炸弹；
- 模型输出包含恶意 Markdown、HTML、URI 或伪造文件路径；
- 多 Tab 会并发提交重复或冲突操作；
- 日志、Crash Report 和浏览器 Storage 可能泄漏 Workspace 数据或 Credential。

### 9.2 Network 与 Browser Trust

第一版硬规则：

1. `--host` 只接受 `127.0.0.1`；即使传入 `localhost`、`::1` 或 `0.0.0.0` 也拒绝，
   避免解析和双栈差异。
2. Server 启动生成 256-bit 随机 Capability Token，只存在内存。
3. `/api/v1/bootstrap` 只在 Host Header 为当前 `127.0.0.1:<port>`，且
   `Sec-Fetch-Site` 不是 `cross-site` 时返回 Token。
4. 所有其他 API 要求 `Authorization: Bearer <token>`。
5. WebSocket 在开始订阅前校验同一 Token。
6. 有 `Origin` 时必须精确等于当前 Origin；`Origin: null` 和 Cross-site 一律拒绝。
7. 不发送 `Access-Control-Allow-Origin`，不支持 JSONP 或 Form 编码。
8. Index/Bootstrap 使用 `Cache-Control: no-store`；Hash Asset 使用 Immutable Cache。
9. API 日志永远不记录 Authorization、Token、Cookie、Prompt、Secret 或原始 Body。

Capability Token 不是远程认证方案。它只与 Loopback、Host/Origin Fence 共同降低本机
浏览器攻击面，也不构成针对同一操作系统用户下恶意进程的安全边界。Token 只能保存在
浏览器内存，不能写入 URL、Local Storage、IndexedDB、日志或 Crash Report。

### 9.3 HTTP Server Hardening

- 设置 `ReadHeaderTimeout`、`ReadTimeout`、`WriteTimeout`、`IdleTimeout` 和
  `MaxHeaderBytes`；
- 不允许 H2C 和明文反向代理信任头；
- 静态资源只从嵌入 Manifest 查找，不把 URL 直接拼接到文件系统；
- `GET`/`HEAD` 之外的静态请求返回 `405`；
- Content Handle 必须是 Runtime 签发、Scope-bound、短期或不可变 Digest；
- 大内容使用流式读取和写出，不在 Handler 中无界 Buffer；
- Server Panic 被逐请求恢复、记录脱敏错误并返回 `500`，不能终止 Runtime；
- Shutdown 时先关闭 Admission，再关闭 Listener 和 WebSocket，最后关闭 Runtime。

### 9.4 浏览器内容安全

建议 Header：

```text
Content-Security-Policy:
  default-src 'self';
  script-src 'self';
  style-src 'self';
  img-src 'self' data: blob:;
  connect-src 'self' ws://127.0.0.1:<bound-port>;
  object-src 'none';
  base-uri 'none';
  frame-ancestors 'none';
  form-action 'none'
X-Content-Type-Options: nosniff
Referrer-Policy: no-referrer
Cross-Origin-Opener-Policy: same-origin
```

- Markdown 禁止 Raw HTML；
- Link 只允许 `http`、`https` 和经过 Resource Resolver 的本地资源；
- 外链使用 `noopener noreferrer`，且不自动请求外部图片；
- Mermaid 等可执行渲染器默认关闭，后续启用时必须使用严格 Security Level；
- Tool Output、File Content、Diff 和 Terminal Text 只进入 Text Node 或安全高亮器；
- 浏览器错误上报只包含结构化 Error Code 和脱敏 Metadata。

### 9.5 Credential

Web Credential Flow 只支持 Keyring 写入：

1. Secret 在 `<input type="password">` 中输入；
2. 通过经过 Token/Origin 校验的单次本机 Loopback POST 发送；
3. Handler 直接调用 `keyring.Store.Set`，不写 SQLite、Config、Event、Log 或 Browser
   Storage；
4. Config 只写入 `{kind="keyring", name="..."}` Reference；
5. Response 只返回 `configured/valid/invalid`；
6. 前端在请求完成后立即清空字段和组件状态；
7. Request Capture、Access Log、Panic Dump 和 Telemetry 显式排除该 Endpoint Body。

环境变量和文件型 Credential 只允许在 UI 中选择已有 Reference，不允许浏览器读取值。

若用户需要把当前 Provider 从其他 Reference 切换为 Keyring，不能由 Handler 分别调用
Keyring 和配置写入。Keyring 与文件系统不能组成真正的原子事务，应增加 Runtime-owned
`CredentialControl` 和不含 Secret 的 Durable Recovery Intent：

1. 生成与 Workspace/Provider 绑定的新 Keyring Entry Name；
2. 以 Operation ID、Provider、旧/新 Reference 和预期 Config Generation 写入
   `prepared` Intent，不记录 Secret；
3. 写入新的 Keyring Entry；
4. 通过临时文件、File `fsync`、Rename 和 Parent Directory `fsync`，以 Generation
   CAS 更新用户配置中的非 Secret Reference；
5. 将 Intent 标记为 `config_committed`，增加 Config Generation，并让后续 Turn 冻结
   新 Reference；
6. 执行不回显 Secret 的 Provider Probe；
7. 成功后只删除已经确认无其他 Config/Session 引用的旧 Entry，再标记 `completed`；
8. 启动恢复扫描未完成 Intent：配置仍是旧 Generation 时删除新 Orphan；配置已指向新
   Reference 时保留新 Entry、完成旧 Entry 引用检查和清理；
9. Active Turn 继续使用其已冻结的 Route，不在中途切换 Credential。

`clear-keyring` 不是裸 `Delete(name)`。它必须在 Config Generation CAS 和全引用扫描后
执行；仍被 Config、Session Profile 或 Active Route 引用时返回 Conflict。

该流程返回 `applied`、`restart_required` 或结构化失败。Web 不通过自行重启、重写环境变量
或向 Provider 直发 Probe 来补齐 Runtime 能力。

## 10. Workspace Context 与资源访问

### 10.1 浏览器不提交任意绝对路径

Web Context Picker 先从 Server 获取 Workspace-owned Resource：

```text
workspace/browse or workspace/search
  -> resource_id + relative_path + digest + kind + metadata
  -> browser preview / range selection
  -> operation/submit(context_handle + selected range)
  -> Runtime resolves handle and revalidates digest
```

浏览器提交的 `resource_id` 必须绑定：

- Workspace Root ID；
- Canonical Relative Path；
- File Identity 或 Digest；
- Resource Kind；
- 创建时间和可选过期时间。

Server 在组装 `EditorContextReference` 前重新打开文件、拒绝 Symlink Escape、校验大小、
UTF-8/Image Signature、Digest 和 Range。浏览器显示的 Path 不是 Authority。

### 10.2 首版 Context

| Context | 首版实现 |
| --- | --- |
| File | Workspace Browser/Search 选择 |
| Selection | 只读 CodeMirror View 中选择 Range |
| Symbol | Runtime Symbol Index 返回范围 |
| Diagnostics | Verification/Diagnostic Receipt 选择 |
| Image | 选择 Workspace 内受支持图片 |
| Terminal | 选择 Runtime-owned Process/Tool Output 片段 |
| Git Diff | 选择 Runtime/Journal 生成的 Diff |

外部本机文件上传不是首版必需能力。后续若增加，必须先流式写入私有 CAS，再由
Attachment Handle 引用，不能把 Base64 大对象放进 Operation JSON。

### 10.3 资源呈现

- 文件预览只读，不提供绕过 Tool Guard 的保存按钮；
- Diff 使用结构化 File Change 和 Hunk，不能从 Markdown Code Fence 推断；
- 点击模型输出中的普通路径文本不直接打开文件；
- 只有 Runtime Receipt、Context Selection、Edit Plan 或 Search Result 生成的
  Resource ID 可导航；
- 二进制文件默认显示 Metadata，不尝试作为文本解码；
- Content 下载使用 `Content-Disposition: attachment` 和安全文件名。

## 11. 前端架构

### 11.1 技术栈

- TypeScript `strict`；
- React；
- Vite；
- CSS Modules 和全局 Semantic Token；
- `lucide-react` 图标；
- `react-markdown` + GFM +严格 Sanitization；
- CodeMirror 6 用于只读代码、范围选择和 Diff；
- Playwright 用于真实浏览器 E2E；
- Vitest 和 Testing Library 用于对象层与组件测试。

不引入大型组件库或 Tailwind。界面是高频工程工具，应保持安静、紧凑、可扫描。

### 11.2 三层结构

```text
web/src/runtime
  ConnectionController
  SessionManager
  TranscriptProjector
  ResourceClient
  ObservableSnapshot
          |
web/src/bindings
  useSyncExternalStore adapters
          |
web/src/ui
  pure React components
```

#### Runtime Object Layer

- 不 Import React；
- 持有 WebSocket Generation、Cursor、Reconnect、Session Cache 和 Event Reducer；
- Snapshot 在状态未变化时保持引用稳定；
- Event Apply 成功后再更新 Cursor；
- Streaming Chunk 使用 Animation Frame 合并通知；
- 结构变化使用 Microtask Batch；
- 不把 Session/Event 复制进多个 Store。

#### React Binding Layer

- 只把 Observable Snapshot 绑定到 `useSyncExternalStore`；
- 不包含 Session 或 Tool 业务规则；
- 负责 Error Boundary、Suspense/Lazy Chunk 和 Focus Restore。

#### Presentation Layer

- 组件只接收 Plain Data 与 Callback；
- 不直接 Fetch、不创建 WebSocket、不读 Runtime Singleton；
- 不通过文本推断业务状态；
- 功能目录可以独立测试和删除。

### 11.3 Event Projector

从现有 VS Code Projector 迁移语义，不直接复制 VS Code 依赖：

- `stream`：Output/Reasoning；
- `tool`：Start/Output/Result 和结构化 Tool Card；
- `interaction`：Approval/Input；
- `evidence`：Context、Diagnostics、Verification、Receipt；
- `terminal`：Completed/Failed/Canceled；
- `snapshot`：Compaction、Checkpoint、恢复边界；
- `orchestration`：Task、Workflow、Subagent、Job。

`event_traits.json` 继续是 Event Class 的唯一来源。生成的 TypeScript Discriminated Union
必须做 Exhaustive Dispatch。新增 Event Class 未选择“处理或显式忽略”时，前端编译
失败。

### 11.4 Browser Storage

IndexedDB 只保存：

- 选中的 Session ID；
- 每个 Workspace 的最后已应用 Cursor；
- 未发送 Composer Draft；
- 展开/折叠和 Panel 宽度；
- 可丢弃的最近 Projection Cache。

`localStorage` 只允许保存一个非敏感枚举 `ch.theme = light|dark|system`，供首屏阻塞式
Theme Bootstrap 使用。不得在其中保存 Workspace、Session、Cursor、Token 或 Draft。

不得保存：

- Credential Value；
- Approval Grant；
- Permission；
- Raw Config；
- 未脱敏 Provider Request；
- Runtime 不可重建的唯一历史。

Cache Key 必须包含 `protocol_version + server_build + workspace_root_id`。任一不匹配即
丢弃 Cache 并重新 Hydrate。

### 11.5 视觉系统

视觉实现深度参考以下 `deepseek-harness` 代码，而不是只参考截图：

- `packages/client/ui-theme/src/styles/`：Static Scale、Semantic Alias、Typography、
  Shadow、Scrollbar 和 Shiki Token；
- `packages/client/ui-layout/src/client/AppFrame.*`：三栏 Grid、拖动、折叠和让步；
- `packages/client/ui-conversation/src/client/skeleton/`：Transcript、Sticky Composer、
  Approval Takeover 和 Empty State；
- `packages/client/ui-conversation/src/client/chat/`：Message、Reasoning、Running State
  和 Back-to-bottom；
- `packages/client/ui-primitives/src/`：Menu、Modal、Tooltip、Toast、Terminal、Read、
  Search、Diff 和 Markdown；
- `packages/client/ui-workspace/src/client/`：Session Rail、Hover Action 和稳定滚动条。

借鉴的是信息密度、状态层级、布局纪律和动效语义，不复制 DeepSeek 名称、Logo、鱼形
Glyph、Figma 专属值或 `--dsw-*` 变量。CodeHelper 使用 `--ch-*` 命名。

#### Token 分层

Token 分成两层：

```text
Static Scale
  color.neutral.*
  color.blue.*
  color.green.*
  color.amber.*
  color.red.*
  space.*
  radius.*
  typography.*
  shadow.*
  motion.*
        |
        v
Semantic Alias
  bg.canvas / bg.surface / bg.raised / bg.overlay
  text.primary / text.secondary / text.muted / text.inverse
  border.subtle / border.default / border.strong / border.focus
  action.hover / action.active / action.disabled
  state.info / state.success / state.warning / state.danger
  code.background / diff.add / diff.delete
```

功能组件只能消费 Semantic Alias。除 Syntax Highlight、ANSI 真彩色和经过设计评审的新
Static Scale 外，组件 CSS 不允许颜色字面量。

#### Palette

- Canvas、Sidebar、Transcript 和 Detail 使用不同明度的 Neutral Surface，不能用同一
  蓝色系填满整个产品；
- Blue 只表达 Focus、Selection、Link 和 Active Work；
- Green、Amber、Red 分别表达 Verified Success、Attention/Approval 和
  Failure/Destructive；
- Dark Theme 不是简单反色，每层 Surface、Border、Text 和 Hover 都有独立 Alias；
- High Contrast 优先使用系统色与清晰 Border，不依赖 Shadow；
- 不使用装饰性渐变、光球、玻璃拟态或大面积品牌色背景；
- 功能性 Running Shimmer 可以使用局部渐变，但只能出现在当前活动行。

#### Typography

建议建立固定角色，不根据 Viewport 连续缩放：

| Role | 建议尺寸/行高 | 用途 |
| --- | --- | --- |
| `title` | `20/28` | 页面或当前 Session 标题 |
| `section` | `14/22`、Medium | Panel 标题和分组 |
| `body` | `14/22` 或 `15/24` | 常规 UI 与消息辅助内容 |
| `markdown` | `16/28` | Assistant 正文 |
| `caption` | `12/18` | 时间、状态、用量和辅助信息 |
| `code` | `13/22` | Code、Terminal 和 Diff |

字体栈优先系统 UI Font；代码字体显式包含 macOS、Windows、Linux 和 CJK Fallback。
数字指标使用 `font-variant-numeric: tabular-nums`，避免 Streaming 时宽度跳动。Letter
Spacing 保持 `0`，仅产品字标可以有单独评审的例外。

#### Geometry

- 基础间距使用 `4/8/12/16/24/32px`；
- 普通控件高度使用 `28/32/36px`，主输入和确认控件可以使用 `40/44px`；
- 普通卡片、Dialog、Tool Detail 和 Diff Surface 圆角不超过 `8px`；
- 只有 Circular Icon Button、Status Dot 和语义 Pill 使用完全圆角；
- Border 以 `1px` 为主，Shadow 只用于真正悬浮的 Menu、Tooltip、Dialog 和 Composer；
- 页面 Section 不绘制成浮动 Card，Sidebar、Transcript、Detail 是连续布局区域；
- 不在 Card 内嵌套 Card。Tool Row 的折叠摘要是普通行，只有展开内容成为一个 Surface；
- 所有固定格式控件使用确定的 Width、Height、Grid Track 或 `aspect-ratio`，Hover、
  Loading 和动态 Label 不能推动周围布局。

#### Theme

- `light`、`dark`、`system` 是持久偏好；
- `system` 通过 `prefers-color-scheme` 解析；
- 在 Stylesheet 之前加载同源、固定 Hash 的微型 `theme-bootstrap.js`，同步读取唯一允许
  的 `ch.theme` 枚举，并在首次 HTML Paint 前设置 `color-scheme` 和 Theme Attribute；
- Theme Runtime 只发布不可变 Snapshot，DOM 写入集中在 Theme Presenter；
- Theme 切换改变 Token，不让功能组件包含 Theme Selector；
- Scrollbar 由全局样式统一实现，基础 Surface 和 Raised Surface 通过 Alias 重绑定；
- `forced-colors: active` 下禁用 Shadow、Shimmer 和非必要背景，保留 Border、Text 和
  System Focus。

### 11.6 Motion System

动效必须表达 State Transition 或空间关系，不能作为持续装饰。全局 Motion Token：

```text
--ch-motion-fast: 100ms
--ch-motion-normal: 180ms
--ch-motion-slow: 300ms
--ch-motion-ease-standard: cubic-bezier(0.4, 0, 0.2, 1)
--ch-motion-ease-enter: cubic-bezier(0.16, 1, 0.3, 1)
```

建议 Motion Matrix：

| 交互 | Full Motion | Reduced/Still |
| --- | --- | --- |
| Sidebar 折叠/展开 | Grid Track 300ms；内容先 Fade，再切换 Rail | 立即切换，保留 Focus |
| Detail 打开/关闭 | Width/Translate 180ms | 立即切换 |
| 拖动 Panel | Pointer Cadence 直接更新，禁用 Transition | 相同 |
| Disclosure | Chevron Rotate 100ms；内容不做高度补间 | 立即切换 |
| Menu/Tooltip | Opacity + 轻微 Translate 100-150ms | 直接出现 |
| Toast | 进入 160ms，停留后淡出 | 无位移，仅延迟淡出 |
| 当前 Turn | 单行文字 Shimmer 1.8s | 静态 Info 色加状态文本 |
| 当前 Tool/Reasoning | 局部 Sweep 2.6s，仅当前可见行 | 静态状态标识 |
| Pending Submit | 1s Opacity Pulse | 静态 Dot 加文本 |
| Streaming Text | 每 Animation Frame 批量提交，无逐 Token Fade | 相同 |
| Session Row 新增 | Opacity/Translate 150ms | 立即出现 |
| Back-to-bottom | Opacity 100ms，不改变 Scroll Height | 立即出现 |

实现约束：

1. 每个无限动画必须绑定真实 `working` 状态，并在 Terminal、Hidden Tab 或 Offscreen 时
   停止；
2. 同一 Viewport 最多允许当前 Turn 和一个当前子步骤显示持续动画；
3. 不对 Transcript 每个 Token 做 Fade、Scale 或 Position Animation；
4. 优先动画 `transform` 和 `opacity`；除明确的 Panel Track 外，不动画会触发大范围
   Reflow 的属性；
5. Hover Action 通过 Opacity 显示但预留空间，避免 Message 行宽改变；
6. 拖动过程中关闭 Easing，Pointer Release 后才恢复 Transition；
7. `prefers-reduced-motion: reduce` 必须关闭 Shimmer、Sweep、Spin、Slide 和 Layout
   Transition；
8. Still Mode 关闭全部非必要 Transition，但保留状态文本、图标和进度数值；
9. Animation End 不是业务 Commit Point，Reducer State 才是；
10. Error、Approval 和 Destructive 状态不使用循环动画吸引注意。

## 12. 页面信息架构

首屏直接是工作界面，不增加 Landing Page。

### 12.1 宽屏

```text
+----------------+--------------------------------+----------------------+
| Workspace/Chat | Transcript                     | Detail               |
| Rail           |                                | Changes / Plan       |
|                |                                | Tasks / Agents       |
|                |                                | Usage / Diagnostics  |
+----------------+--------------------------------+----------------------+
| Runtime / Profile / Composer / Pending Action                          |
+------------------------------------------------------------------------+
```

### 12.2 紧凑窗口

- 保留 Workspace/Session、当前状态和 Composer；
- Detail 变为可切换 Drawer；
- Session Rail 可折叠；
- Approval/Input 永远不能被 Drawer 遮挡；
- 不通过缩放 Font Size 适配，使用稳定列宽和断点；
- 触屏不是首要场景，但 390px 宽度不能出现文本和控件重叠。

### 12.3 核心工作流

1. **启动**：显示 Runtime Readiness，完成 Bootstrap 后列出当前 Workspace Session。
2. **新建 Chat**：默认 Worktree Isolation，可显式选择 Shared。
3. **提交任务**：选择 Mode、Model、Reasoning、Tool Allowlist 和 Context 后提交。
4. **执行中**：增量显示输出、Reasoning 摘要、Tool、验证和 Stop。
5. **审批**：展示 Tool、Target、Effect、Risk、Plan ID、过期时间和 Runtime Identity。
6. **输入**：显示问题、选项、自由文本和已绑定 Request ID。
7. **完成**：显示 Final Output、Changed Files、Verification、Usage 和 Receipt。
8. **恢复**：刷新或断线后 Hydrate + Replay；失败 Turn 提供 Retry/Continue。
9. **Review**：Diff、Checkpoint、Plan 和资源导航共享同一 Detail 区。
10. **设置**：Provider/Model、Credential Reference、Tools、Extensions 和 Diagnostics。

### 12.4 首版必须完整的状态

- Setup；
- Empty；
- Loading；
- Ready/Idle；
- Streaming；
- Approval Required；
- Input Required；
- Verifying；
- Completed；
- Failed；
- Canceled；
- Recovering；
- Desynchronized；
- Degraded；
- Runtime Draining。

每个状态都必须有文本、下一步动作和键盘路径。颜色和动画不能是唯一信号。

### 12.5 布局与交互细节

#### 三栏让步链

宽屏默认几何：

| 区域 | 默认 | 最小 | 最大/行为 |
| --- | --- | --- | --- |
| Session Rail | `264px` | `220px` | `360px`；折叠后 `56px` |
| Transcript | 自适应 | `520px` | 消息内容最大约 `760px` |
| Detail | `360px` | `300px` | `520px`；可关闭到 `0` |

让步顺序固定：

1. Detail 先缩到最小；
2. Detail 关闭为 Drawer；
3. Session Rail 收缩；
4. Session Rail 折叠为 Icon Rail；
5. Transcript 保留 Composer、Pending Action 和当前状态。

窗口重新变宽时按逆序恢复用户最近宽度。拖动 Handle 使用至少 `8px` Hit Target，视觉线
可以更细；键盘用户可以通过命令调整 Panel，不依赖 Drag。

#### Transcript 与 Composer

- Transcript 只有一个主纵向 Scroll Owner；
- Composer 固定在 Transcript 底部，并使用稳定高度上限；
- Draft 自动增长到约 14 行，之后只滚动 Draft 自身；
- Approval、Question 和 Plan Review 复用 Composer Seat，属于内容替换而不是新增浮层，
  因此主动作位置不跳动；
- Pending Panel 的正文可以内部滚动，但 Action Row 始终可见；
- 用户主动上滚后停止自动跟随，并显示不增加 `scrollHeight` 的 Back-to-bottom 控件；
- 用户仍在底部时，Streaming 更新保持底部锚定；
- History Prepend 保持第一条可见消息的相对像素位置；
- Virtualized Row 使用 Session/Turn/Item ID，不使用数组下标。

#### Message 与工具呈现

- User Message 右对齐并限制最大宽度；Assistant 正文全宽；
- Reasoning、Tool、Context、Retry 和 Compaction 使用一致的 Disclosure Row；
- Read、Search、Terminal 和 Diff 各自使用结构化 Renderer；
- Code、Terminal、Diff 保留空格并横向滚动，不为适应 Card 强制折行；
- 大结果默认展示 Head/Tail 和截断数量，展开后仍有高度上限；
- Code Banner、Copy 和 Status 固定在可见位置，不随内容横向滚走；
- Running、Success、Warning、Failure 都显示文字，Dot/Icon 只辅助；
- Message Hover Action 在键盘 Focus 和无 Hover 设备上同样可达。

#### 控件

- 常见动作使用 `lucide-react` 图标；
- Undo、Redo、Copy、Close、Expand、Collapse、Stop、Refresh 等不使用带文字的圆角矩形
  代替熟悉图标；
- 不熟悉图标提供 Tooltip 和 Accessible Name；
- Mode、View 和 Diff 展示方式使用 Segmented Control 或 Tab；
- Boolean 设置使用 Switch/Checkbox；
- Model、Provider、Toolset 使用 Menu/Combobox；
- 数字预算使用 Input/Stepper，不用自由文本命令；
- Primary Action 在一个状态中只有一个，Destructive Action 与其分离。

#### Empty、Loading 与 Failure

- Empty State 只包含 CodeHelper 标识、当前 Workspace、Composer 和必要 Setup Action，
  不做营销 Hero；
- Boot Shell 自给自足，Runtime 构造失败时仍能显示结构化诊断和精确的 CLI
  Setup/Doctor 修复命令；第一版不在半初始化 Host 中直接改写损坏的 Config；
- 不在依赖未就绪时显示半残缺主界面；
- Skeleton 保留最终几何，避免 Hydration 后大幅 Layout Shift；
- Failure 保留用户 Draft、当前 Session 和已投影 Transcript；
- Reconnect Banner 不遮挡 Composer 或 Approval。

## 13. 进程生命周期

### 13.1 启动顺序

启动分成 Boot Surface 和 Runtime Activation 两段，避免 Config、Store 或 Recovery 失败时
连错误页面都打不开：

1. Parse `web` Transport 参数，拒绝非 Loopback Host；
2. 加载并校验嵌入资源 Manifest；
3. 生成 Capability Token；
4. 以可返回结构化错误的方式解析 Config、Data Dir 和 Workspace Identity；
5. Config 有效时获取 Interactive Runtime Owner Lease；已有健康 Web Owner 时报告并
   打开其 URL，然后退出当前进程；
6. 绑定 `127.0.0.1:<port>`，只注册 Static、Bootstrap、Health 和只读 Diagnostics；
7. 打印 `Listening` Line，并根据 `--open` 打开 Boot Shell；
8. 若 Config 无效，保持 `boot_failed`，所有 Runtime Mutation 返回 `503`，页面展示
   脱敏错误和精确 CLI Repair 命令；
9. Config 有效时 Open Persistent Store；
10. `wire.NewExec` 以 `interactive` Role 完成 Runtime、Recovery 和 Background Service；
11. 从 Runtime Session 暴露窄化 Application Service；
12. 原子安装完整 API/WebSocket Route Table，并执行内部 Readiness Probe；
13. Readiness 从 `initializing` 切换为 `ready`，打印 `Runtime Ready` Line。

两个信号含义不同：

```text
CodeHelper Web Listening: http://127.0.0.1:54321/
CodeHelper Runtime Ready: http://127.0.0.1:54321/
```

浏览器 E2E 通过 `Listening` 获得 URL，但只有 `Runtime Ready` 才允许提交业务
Mutation。Supervisor 必须使用 `/healthz` 或第二行判断执行就绪。Static Manifest、
Token 或 Listener 失败时不能打印 URL；Runtime 构造失败则保留可诊断 Boot Surface，
不能伪装为 Ready。

### 13.2 参数

建议首版参数：

```text
--workspace PATH
--config PATH
--data-dir PATH
--host 127.0.0.1
--port PORT
--open
--no-open
--enable-tools
--posture suggest|auto|never
--mcp-config PATH
--provider ID
--model ID
--api-key-env NAME
--provider-fixture PATH
```

- `--port 0` 为默认，避免端口冲突；
- TTY 启动默认 `--open`，非 TTY 默认 `--no-open`；
- `--host` 保留是为了让拒绝行为显式，首版唯一合法值是 `127.0.0.1`；
- `--data-dir` 为空时使用 Config 默认值；
- `--posture bypass` 不从 Web Command 暴露；若 Config 的最终有效值为 `bypass`，Web
  保持 `boot_failed` 且不开放 Mutation，避免浏览器入口成为宽松权限捷径；
- Repository Trust 未建立时，Web 强制 `never` 或只读能力。

### 13.3 关闭

- Browser Tab 关闭：只释放该连接，不取消 Turn；
- `SIGINT/SIGTERM`：停止新 HTTP Mutation，广播 `server.draining`，受控取消或结算
  Active Interaction，关闭 WebSocket，再调用 `wire.Session.Close`；
- 进程崩溃：依赖现有 Durable Turn Recovery，下一次启动恢复；
- 关闭超时：返回非零退出码并保留未完成 Durable State，不能伪造 Terminal。

## 14. 构建、静态资源与发布

### 14.1 前端产物

建议在根目录新增 `web/`：

```text
web/
  package.json
  package-lock.json
  tsconfig.json
  vite.config.ts
  src/
  tests/
  dist/                 generated and embedded
  embed.go
```

`embed.go` 使用 `go:embed` 暴露只读 `fs.FS`。`dist` 作为生成产物由仓库命令维护：

```bash
make web-install
make web-build
make web-assets-check
```

`web-assets-check` 在临时目录重新构建并比较 Manifest、文件名和内容 Hash，防止源码与
嵌入产物漂移。最终用户运行二进制不需要 Node.js。

### 14.2 Cache 与压缩

- `index.html` 与 Bootstrap 不缓存；
- Asset 文件名包含 Content Hash；
- Go Server 根据 `Accept-Encoding` 提供构建期生成的 gzip/brotli 版本；
- ETag 使用内容 Digest；
- Source Map 不进入 Release Binary；
- 未知扩展名使用 `application/octet-stream`；
- SPA Fallback 只服务非 API 的 GET/HEAD。

### 14.3 依赖与供应链

- 使用独立 `web/package-lock.json` 和 `npm ci`；
- License、Known Vulnerability 和 Bundle Size 进入 Release Gate；
- 不在构建时下载远程字体、图片或 Runtime Script；
- 图标、字体和代码高亮资源随 Binary 发布；
- Release Archive 继续以单个 `codehelper` Binary 为核心。

## 15. 可观测性与容量

新增低基数 Metric：

```text
web_http_requests_total{route,status_class}
web_http_request_duration_seconds{route}
web_ws_connections
web_ws_reconnects_total{reason}
web_ws_slow_consumer_total
web_snapshot_build_duration_seconds
web_snapshot_bytes
web_event_replay_count
web_event_replay_bytes
web_protocol_rejections_total{reason}
web_auth_rejections_total{reason}
```

禁止 Label：

- Workspace Path；
- Session/Turn/Request ID；
- Prompt；
- Tool Argument；
- Resource ID；
- Credential Reference 或 Value。

初始容量边界：

| 项目 | 上限 |
| --- | --- |
| Prompt | 64 KiB 字符边界沿用现有约束 |
| Inline Text Context | 64 KiB |
| Workspace File Context | 1 MiB |
| Workspace Image | 5 MiB |
| 普通 JSON Request | 1 MiB |
| 单 History/Snapshot Response | 2 MiB，使用分页 |
| WebSocket Event Frame | 4 MiB |
| 单连接待发送队列 | 64 Events，满时断开并 Replay |
| 单进程 Browser Connections | 16 |
| 激活 Session | 32 |
| Session History 首屏 | 最近 200 Turns，继续向前分页 |

这些值在实现时应集中到一个 Web Capacity Config，并通过 Exact-limit、Over-limit、
Multibyte 和 Slow Consumer Test 验证，不能散落在 Handler 和前端常量中。

## 16. 关键功能与测试策略

### 16.1 Feature Completeness Ledger

在删除 VS Code/ACP 前新增机器可读
`testdata/contracts/web-feature-parity.json`。每个条目包含：

```text
id
disposition              required | retained_secondary | intentional_drop
legacy_inventory_ids     删除前生成的封闭能力清单 ID
legacy_evidence          Host-neutral Test、Fixture 或行为说明
runtime_owner            权威实现路径
web_api                  Web Endpoint 或 Event
web_surface              页面、Panel 或明确的非 UI Consumer
required_qualifications  Unit / Contract / Replay / E2E Test ID
secondary_surface        仅 retained_secondary 必填
drop_rationale           仅 intentional_drop 必填
replacement              替代路径
```

Ledger 只声明要求，不能声明自己的完成状态。`captured`、`implemented`、`verified` 由
`scripts/webparitycheck` 根据当前 Commit 的 Inventory、文件、Test Manifest 和 CI Result
计算，并生成不可手填的 `web-feature-parity-report.json`。Report 至少记录 Commit SHA、
Input Digest、执行命令、Test Result、Artifact Digest 和计算状态。

删除旧 Host 前先生成封闭的
`testdata/contracts/legacy-capability-inventory.json`。生成器必须枚举并冻结：

- VS Code `package.json` Contribution、Command Registration、Chat Action、Context
  Provider、Release Journey 和 Binary/Secret/Update 能力；
- ACP Method Registry、Compatibility Manifest 和 Contract Scenario；
- Host Journey、Experience Contract、Make Target、发布脚本和产品文档入口；
- CLI/TUI/Worker/Automation 中明确继续保留的 Secondary Surface。

Inventory 记录 Source Path、Stable ID 和 Source Digest；每个 Inventory ID 必须且只能
映射到一个 Ledger 条目。删除旧目录后，CI 使用冻结 Inventory 和 Host-neutral Evidence，
不再把已删除文件路径当作可执行证据。

Gate 必须验证：

1. 每个 `required` 条目都有 Runtime Owner、Web API、Web Surface 和至少一个真实 Host
   Qualification；
2. `retained_secondary` 有仍存在的 Owner、命令/表层和 Qualification，不要求伪造
   Web API；
3. `intentional_drop` 有明确理由与用户替代路径；
4. Inventory 和 Ledger 双向全覆盖，没有未分类或重复分类的 ID；
5. Report 引用的文件、测试和 Artifact 真实存在，且 Test Result 属于当前 Commit 和
   Input Digest；
6. 不能用 Unit Mock 代替关键工作流的真实 Web E2E；
7. 新增 Product Surface 或 Protocol Operation 必须同步进入 Inventory/Ledger；
8. Release 只接受所有 `required` 为计算出的 `verified`，所有
   `retained_secondary` 为 `verified`，所有 Drop 已从 Help/UI/Build 中消失。

初始关键能力清单：

| ID | 能力 | Web 完整性要求 |
| --- | --- | --- |
| `runtime-readiness` | Runtime 启动、恢复、降级、关闭 | Readiness 页面、结构化故障、受控 Shutdown |
| `workspace-binding` | Workspace Identity 与 Trust | 页面持续显示 Root/Trust，所有操作服务端复验 |
| `session-lifecycle` | 创建、激活、重命名、置顶、归档、删除 | Revision-aware API 和 Session Rail |
| `session-search` | 标题、请求、输出、路径和符号搜索 | Search API、结果定位和空/失败状态 |
| `session-isolation` | Shared/Worktree Session | 创建时明确选择，恢复和删除保持 Worktree 语义 |
| `profile` | Mode、Model、Reasoning、Tool Allowlist、Posture | Capability-aware 控件与 Revision Conflict |
| `turn-submit` | Start Turn 与幂等受理 | Composer、Receipt Identity、重复提交测试 |
| `turn-stream` | Output、Reasoning、Tool 增量 | 有界批量渲染、顺序和 Terminal Reconciliation |
| `turn-control` | Cancel、Steer、Queue | 明确状态、Request Identity 和竞态测试 |
| `approval` | Approve、Deny、Cancel、Scope、Expiry、Plan Binding | Composer Takeover 和 Stale Decision 拒绝 |
| `input` | 文本、选项和结构化值 | Keyboard 完整路径和重复回答拒绝 |
| `workspace-context` | File、Selection、Symbol、Diagnostics、Image、Terminal、Git Diff | Resource Handle 与 Runtime Revalidation |
| `tool-presentation` | Read、Search、Terminal、Diff 和 Generic Tool | 结构化 Renderer、截断与完整结果入口 |
| `verification` | Command、Scope、Attempt 和 Verdict | Verification Timeline 与 Receipt |
| `changes` | Edit Plan、Changed Files、Preview、Merge/Apply | Detail Diff，不从文本推断 |
| `checkpoint` | List、Get、Restore、Fork | Lineage、Busy Fence、零 Side-effect Replay |
| `plan-recovery` | Plan Get/Implement、Retry、Continue | 新 Turn Identity 和 Guidance |
| `task-agent` | Task、Subagent、Job、Workflow 状态 | Detail View、层级和 Terminal |
| `extension` | Plugin/Skill 状态与控制 | Runtime-owned Generation、Health 和 Receipt |
| `usage-receipt` | Token、Cost、Context、Change、Verification | 可展开 Receipt 和 Export |
| `credential` | Status、Keyring Set/Clear、Validation | Secret 不持久化、不回显 |
| `history-reconnect` | Snapshot、Replay、Gap、Restart | Refresh 不重发，无 Committed Event 丢失或重复 |
| `resource-navigation` | File/Range/Diff/Artifact 打开 | Opaque ID、Workspace Fence、Stale Handle |
| `accessibility` | Keyboard、Focus、Screen Reader、Contrast、Motion | 自动化规则和 Playwright Matrix |

明确删除而非遗漏的能力：

| ID | 删除内容 | 替代 |
| --- | --- | --- |
| `vscode-multiroot` | 单窗口多 Workspace Runtime | 每个 Workspace 启动独立 `codehelper web` |
| `vscode-code-action` | VS Code Diagnostic Code Action | Web Diagnostic/Verification Context |
| `vscode-native-editor` | VS Code Editor、Quick Pick、Diff API | Web File Browser、Code Viewer 和 Diff |
| `vscode-secret-storage` | Extension SecretStorage | OS Keyring |
| `vscode-binary-manager` | VSIX 内 Binary 安装/回滚 | CodeHelper Binary Release/Update |
| `acp-editor-integration` | 第三方 ACP Editor 接入 | Web API；不承诺编辑器内嵌 |
| `acp-dynamic-tools` | Client 注册并执行 Dynamic Tool | 删除；不向浏览器授予 Tool Executor 身份 |

明确保留在 Secondary Surface、而非要求搬进 Web 的能力：

| ID | 保留表层 | Web 关系 |
| --- | --- | --- |
| `cli-host` | `codehelper exec` 和直接命令 | Web 未持有 Interactive Lease 时可运行 |
| `tui-host` | `codehelper tui` | Web 未持有 Interactive Lease 时可运行 |
| `worker-execution` | `codehelper worker run` | 可与 Web 并存，使用 Task/Turn/Workspace Fence |
| `automation-management` | `codehelper automation list/run/pause` | Web 只展示产生的 Task/Run |
| `mcp-management` | `codehelper mcp add/enable/disable/remove/test` | Web 首版只展示 Runtime MCP Health |
| `boot-repair` | CLI Setup/Doctor 和结构化诊断 | Boot Shell 展示失败与精确修复命令，不直接改坏配置 |

这里的“完整”是关键用户工作流完整，不是保留每个旧 Host API。任何没有进入
`retained_secondary` 或 `intentional_drop` 的旧能力都默认是 `required`，不能在实现
过程中临时降级。

### 16.2 Go Unit 与 Contract

- HTTP Method、Content-Type、Unknown Field、Body Limit；
- Host、Origin、Fetch Metadata、Capability Token；
- Static Traversal、MIME、Cache、ETag、SPA Fallback；
- WebSocket Auth、Replay、合法 Sequence 空洞、Server-declared Gap、Slow Consumer、
  Close；
- Snapshot As-of Watermark、Read Model Source Sequence 和 Retention 竞态；
- Session Activation 并发去重与 Workspace Fence；
- Operation Exposure 默认拒绝、ID/Idempotency、Approval/Input Binding；
- Credential Endpoint 不记录 Secret，Recovery Intent 可在每个 Crash Point 收敛；
- Interactive Owner 与 Worker 并存、Role-scoped Recovery；
- Boot Surface 两阶段启动、Shutdown、Partial Boot 和 Resource Cleanup。

扩展 `runtimeapi/runtimecontract.Host`，补齐当前缺少的：

- `ReplyInput`；
- `Steer`；
- `RecoverTurn`；
- `GetPlan/ImplementPlan`；
- `Extension List/Control`；
- 明确的 Replay Page 与 Cursor Gap。

删除 ACP 前，把现有 Scenario 改为直接驱动 Runtime Application Service 的 In-memory
Contract Driver。ACP 删除后，Web 与 In-memory Driver 继续运行同一组 Scenario；
Transport-only Test 只验证 Web 的 Framing、安全和 Shutdown。

### 16.3 Frontend Unit 与 Component

- Event Union Exhaustive Projection；
- Duplicate、Out-of-order、合法 Sequence 空洞、Server-declared Gap 和 Unknown Event；
- Snapshot + Live Event 合并；
- Streaming 合并和 Terminal Reconciliation；
- Approval/Input 的 Stale、Double-submit 和 Expiry；
- Session Revision Conflict；
- Browser Cache Version 失配；
- Markdown/XSS/Unsafe URL；
- Keyboard、Focus、ARIA、Reduced Motion 和 High Contrast；
- Feature CSS 只消费 `--ch-*` Semantic Alias；
- Hover/Focus/Loading 前后组件外框尺寸不变；
- Full、Reduced、Still Motion Matrix；
- 200 Turn Virtualization 和长 Tool Output。

### 16.4 Visual 与 Motion Contract

建立机器可读 `testdata/contracts/web-experience-contract.json`，至少定义：

- Layout Region、默认/最小/最大 Track；
- Compact、Regular、Wide 让步顺序；
- Semantic Color、Typography、Spacing、Radius 和 Motion Token；
- 每个 Canonical State 允许的 Motion；
- Focus Order、Accessible Name 和 Keyboard Command；
- Composer、Approval、Input 和 Detail 的稳定几何；
- Transcript Auto-follow、Back-to-bottom 和 History Prepend Anchor；
- Reduced Motion、Forced Colors 和 200% Zoom 行为。

静态门禁：

- 扫描 Feature CSS，拒绝未登记 Color、Shadow、Duration 和 Z-index Literal；
- 拒绝 `transition: all`；
- 拒绝没有 `prefers-reduced-motion` 降级的无限动画；
- 拒绝普通 Card 超过 `8px` 圆角；
- 拒绝 Button 使用手绘 SVG，而仓库已有对应 Lucide Icon；
- 检查 Z-index Layer Registry，避免局部组件任意抬高层级；
- 检查所有固定 Toolbar、Grid、Counter 和 Icon Button 有稳定尺寸。

浏览器门禁：

- 在 `390x844`、`1024x768`、`1440x900` 和 `1920x1080` 截图；
- 每个尺寸覆盖 Light、Dark，关键流程再覆盖 High Contrast；
- 对 Empty、Streaming、Approval、Input、Failure、Completed、Diff 和 Settings 保存
  Golden；
- 使用 Playwright `reducedMotion: reduce` 证明无限 Animation 为零；
- 在 Sidebar/Detail 动画开始、中点和结束检查 Track、Handle 和内容没有重叠；
- 在 200% Zoom、长中文、长英文单词、长 Path 和长 Command 下检查文本不越界；
- 检查 Composer 增长、Approval Takeover 和 Reconnect Banner 不产生非预期 Layout
  Shift；
- 使用 DOM Geometry Assertion 检查主动作始终位于 Viewport 内，不能只依赖截图肉眼。

视觉 Golden 固定最终静止帧。动画时序使用 State、Computed Style 和 Geometry 断言，
避免因截图采样到不同 Animation Frame 产生 Flaky Test。

### 16.5 Keyless Replay

从 Runtime Fixture 录制稳定 Event 序列：

- Answer；
- Tool Read/Search/Edit；
- Approval Approve/Deny/Cancel；
- Input；
- Verify Pass/Fail/Repair；
- Cancel；
- Provider Failure；
- Checkpoint Restore/Fork；
- Plan Implement；
- Subagent/Task；
- Compaction；
- 合法 Sequence 空洞、Cursor Retention Gap 和 Restart。

删除旧实现前，把 VS Code/ACP 中可复用的录制和断言迁到 Host-neutral Fixture。之后
同一录制驱动 Go `eventview`、Web Projector 和 In-memory Driver，比较 Canonical
State、Pending Identity、Terminal、Receipt 和 Changed Files，而不是比较像素。

### 16.6 Playwright E2E

测试必须启动真实编译后的 `codehelper web`，通过 `Listening` Line 获得 URL，等待
`Runtime Ready` 或预期的 `boot_failed`，并使用真实 HTTP/WebSocket：

- 首次启动和空状态；
- Config/Store/Runtime 构造失败时 Boot Shell 仍可诊断，Mutation 保持关闭；
- 创建 Session、提交、流式输出和完成；
- Approval/Input；
- Stop、Retry、Continue；
- Refresh during Turn；
- Server Restart 后恢复；
- 多 Tab 冲突；
- Workspace File/Selection Context；
- Diff、Plan、Checkpoint；
- Credential Keyring Mock；
- Extension/Task/Agent/Usage；
- 390px、1024px、1440px 布局；
- Light、Dark、High Contrast、Reduced Motion；
- 键盘完整主路径和 Focus Restore；
- Sidebar/Detail 拖动、折叠和让步链；
- Sticky Composer、自动跟随、上滚保护和 Back-to-bottom；
- Tool/Reasoning Disclosure、长 Terminal、Read 和 Diff；
- Hover Action、Tooltip、Toast 和 Pending State Motion；
- Browser `visibilitychange` 后持续动画暂停和恢复。

截图只验证布局和视觉回归；业务正确性仍由 DOM 语义、Protocol Frame 和 Runtime
Evidence 断言。

### 16.7 性能与耐久

- 10,000 Event Replay；
- 500 Turn Session Hydration；
- 1 小时持续 Streaming；
- 16 Browser Connection；
- 32 激活 Session；
- 事件发布期间反复刷新；
- Slow/Paused Tab；
- Web 与独立 Worker/Automation 同时运行；
- Runtime 与 Web Server Race Test；
- Heap、Goroutine、WebSocket 和 File Descriptor 泄漏检查。

性能门禁先记录真实基线，再设置 Regression Ratio；不在没有测量前写虚假绝对数字。
应独立设置 Initial Bundle、Hydration、First Interaction、Steady Streaming 和
Long-session Memory 指标。前端还应记录 Streaming 期间的 Long Task、Dropped Frame
和 Layout Shift；持续动效只能作用于当前可见工作项，不能随历史长度线性增加。

## 17. 分阶段实施

实施只在独立的 `web-primary` 迁移分支进行。该分支可以包含多个可审查、可回退的内部
Commit，但不会形成同时支持两套产品架构的 Release。阶段名称描述可验证结果，不表示
对外兼容窗口。

### 17.1 Capability Capture

工作：

- 以一个已知全绿的基线 Commit 创建迁移分支和回滚 Tag；
- 生成封闭的 `legacy-capability-inventory.json`，建立 `web-feature-parity.json`；
- 将 VS Code/ACP 测试中的业务断言迁入 Host-neutral Contract；
- 将 Event Recording、Golden、Fixture 和 Protocol Generator 移出即将删除的目录；
- 为每个 `required` 能力记录 Runtime Fact、输入、输出、失败和恢复语义；
- 将 CLI/TUI/Worker/Automation/MCP 管理登记为 `retained_secondary`；
- 把 Multi-root、Native Code Action、ACP Dynamic Tool 等明确登记为
  `intentional_drop`。

退出条件：

- Inventory 生成器覆盖 VS Code Contribution/Command/Action、ACP Method、Host Journey、
  Make/Release/Documentation Entry 和 Secondary Surface；
- Inventory ID 与 Ledger 双向一一对应；
- 每个 Required 项都有可执行的 Legacy Evidence；
- `make verify` 在基线 Commit 通过；
- 删除旧代码后仍需要的 Fixture 不再位于 `extensions/vscode` 或 ACP Package 内。

### 17.2 Runtime Authority And Legacy Removal

在一个不可发布的迁移检查点内完成：

- 将 Session 创建/激活、Worktree 恢复、Operation Identity、History 分页和事件归属
  迁入 `internal/runtime/app` 的窄化 Service；
- 为 `wire.NewExec` 增加 Runtime Role，证明 Worker 不接管普通交互 Turn；
- 将 Shared Host Contract 改为直接驱动这些 Service；
- 删除 `extensions/vscode/`；
- 删除 `internal/host/runtimeapi/acp/`；
- 删除 `codehelper host --adapter acp`；若 `host` 不再有其他 Adapter，则删除整个命令；
- 删除 VSIX、Electron、Rosetta、ACP Interop、Compatibility 和对应生成链；
- 删除仅服务旧 Host 的 Dynamic Tool Client Bridge；
- 更新 Architecture Test，禁止 Web Host 重新获得 Repository 或执行层 Authority。

不允许出现以下中间设计：

- `runtimeapi/control.Service` 汇总所有 Repository；
- ACP/Web 双协议转换器；
- Web 调用 ACP；
- 为保持旧测试而留下的 ACP Stub；
- VS Code 跳转到 Web 的空壳插件；
- 新旧 Session、History 或 Projection 双写。

退出条件：

- 仓库中不存在 VS Code 或 ACP 生产入口；
- In-memory Contract Driver 覆盖 Start、Stream、Approval、Input、Cancel、Recover、
  Receipt 和 Cursor；
- Runtime App Test 证明迁入行为的 Durable、Identity 和 Recovery 语义；
- CLI、TUI、Worker、Automation 和全部非旧 Host Package 可编译，Secondary
  Qualification 通过；
- 此检查点明确标记为不可发布。

### 17.3 Secure Web Transport

工作：

- 增加 `codehelper web`；
- 完成两阶段 Boot Surface、Bootstrap、Token、Host/Origin Fence；
- 完成 Unary API、Event WebSocket、Static Handler；
- 增加 Web Schema、Operation Exposure Registry 和 TypeScript Generator；
- 用最小测试 Client 跑 Shared Host Contract。

退出条件：

- 无浏览器 UI 也能通过 Web Contract；
- Reload/Disconnect 不取消 Turn；
- Slow Consumer 可从 Cursor 恢复；
- 合法 Sequence 空洞不触发 Desync，只有 Server-declared Gap 才进入恢复；
- Cross-site、Rebinding、Malformed 和 Oversized 请求全部 Fail Closed；
- Config/Store/Runtime 失败只打印 `Listening`，不打印 `Runtime Ready`，Boot Shell 可显示
  脱敏诊断。

### 17.4 Core Web Workspace

工作：

- 建立 React-free Runtime Object Layer；
- 建立 `--ch-*` Static/Semantic Token、Light/Dark/High Contrast Theme；
- 实现三栏让步链、Sticky Composer、稳定 Scroll Owner 和 Responsive Drawer；
- 按 Motion Matrix 实现 Panel、Disclosure、Running 和 Pending 动效；
- 完成 Session Rail、Transcript、Composer、Profile；
- 完成流式输出、Tool、Approval、Input、Cancel、Terminal 和 Receipt；
- 完成 Browser Cache 与 Hydration；
- 增加 Keyless Replay 和首批 Playwright E2E。

退出条件：

- 用户可仅通过 Web 完成真实 Read/Edit/Verify 工作流；
- 页面刷新不重发 Prompt；
- 所有 Shared Journey 在 Web 通过；
- UI 可键盘操作且无关键可访问性错误；
- Visual/Motion Contract、四种 Viewport 和 Reduced Motion Golden 通过；
- 长 Transcript Streaming 时持续动画数量有界，主滚动和 Composer 无 Layout Shift。

### 17.5 Feature Closure

按 `web-feature-parity.json` 逐项关闭剩余能力：

- Workspace Browser/Search；
- File/Selection/Symbol/Diagnostic/Image/Terminal/Git Diff Context；
- Diff/Plan/Checkpoint/Recovery；
- Session Merge Preview/Apply、Export 和 Long-history Pagination；
- Credential Keyring Flow；
- Extension、MCP Health、Task、Subagent、Job、Workflow、Usage 和 Diagnostics；
- Resource Navigation；
- Release Binary 内嵌前端。

退出条件：

- 当前 Commit 生成的 Parity Report 将每个 `required` 和 `retained_secondary` 计算为
  `verified`；
- 每个 `intentional_drop` 均有文档化替代和测试，且已从 UI、Help 和 Build 中移除；
- 不存在 `TODO`、Disabled Control 或 Mock-only Path 冒充已交付能力；
- Clean Install 只安装 Binary 即可启动完整 Web；
- Web Browser、Security、Race、Durability 和 Performance Matrix 全绿。

### 17.6 Atomic Cutover

只在前述门禁全部完成后，把迁移分支作为一个产品切换合入：

- README、Getting Started、Usage、Architecture、Security、Development、Experience、
  Roadmap 和 Book 全部切换到 Web-first；
- `make build` 默认构建并嵌入 Web；
- Release Gate 使用 Web Browser Matrix；
- `rg` 确认生产构建、发布和文档不再依赖 `extensions/vscode` 或
  `internal/host/runtimeapi/acp`；
- 发布说明明确移除 VS Code 插件和 ACP Host；
- CLI/TUI 继续作为 Secondary Host，Worker 和自动化继续作为非交互入口。

主分支不会经历“旧入口已删但 Web 尚不可用”的状态，发布产物也不会同时包含旧、新两套
主入口。

## 18. Rollout 与回滚

### 18.1 Rollout

1. 从已知全绿 Commit 建立 `web-primary` 分支并冻结旧 Host；
2. 捕获 Feature Ledger、Contract 和 Fixture；
3. 在迁移分支删除 VS Code/ACP，并完成 Runtime Authority 归位；
4. 只在新 Web 架构上迭代，不向旧 Host 回填功能；
5. 每个内部 Commit 运行当前可用的窄门禁，持续运行完整迁移分支 CI；
6. 当前 Commit 的生成 Report 将所有 Required/Secondary Feature 计算为 Verified 后，
   执行 Clean Install 和 Release Candidate；
7. 通过一个 Atomic Cutover 合入主分支并发布。

迁移分支不是产品双轨。旧 Release 继续服务现有用户，新分支只生成内部测试 Artifact；
两者不共享运行时状态、不双写数据，也不承诺跨版本热切换。

### 18.2 回滚

- Cutover 前只回滚迁移分支内部 Commit，不把残缺状态合入主分支；
- Cutover 后回滚完整 Release 和完整 Commit，但 Binary 回滚与 Durable Data 降级必须
  分开验证；
- 前端 Asset 可随 Binary 回滚，不从 CDN 热更新；
- 保留最后一个旧 Binary 和已签名 VSIX Release 作为独立回滚 Artifact；
- 不在新代码中保留 ACP/VS Code Compatibility Shim；
- Web 迁移不应为 Transport/UI 引入新的 Durable Event Kind；Presentation Snapshot
  优先保持可重建，必要的新表采用 Expand-only Schema；
- Cutover Gate 必须让“最后旧 Binary”打开一份由 Web RC 实际运行后生成的数据副本，
  完成 Session List、History Read 和至少一个可恢复 Turn，证明一个 Release 窗口内可
  原地降级；
- 若任何 Schema、Event、Snapshot 或 Config 变化无法通过该 Downgrade Matrix，Cutover
  必须阻断，不能用“已有备份”替代兼容性证明；
- 发布前在 Quiescent Point 创建 Data Dir 一致性备份，记录 Schema Version、Event
  Watermark、文件清单和 Digest，并完成隔离目录 Restore Drill。该备份是灾难恢复路径，
  不是常规 Binary Rollback；
- 不支持新旧进程同时打开同一 Data Dir。

## 19. 风险与缓解

| 风险 | 缓解 |
| --- | --- |
| 删除旧 Host 时遗漏功能 | 封闭 Inventory 与 Ledger 双向覆盖，生成 Report 而非手填状态 |
| 大分支集成风险 | 可验证内部 Commit、持续 Rebase/CI、最终 Atomic Cutover |
| 旧实现被当作新架构模板 | 只迁移 Runtime 事实和 Fixture，不迁移 Transport/Framework |
| 新 Runtime Service 变成万能 Facade | 按 Session、Operation、History、Artifact 拆分窄接口 |
| 新 Operation 被意外暴露到浏览器 | Web Operation Exposure 默认拒绝，新增 Kind 未分类即失败 |
| Web 与 Worker 争抢恢复权 | Interactive Owner Lease + Runtime Role + Durable Task/Turn Fence |
| Localhost 被恶意网页调用 | Loopback-only、Host/Origin Fence、Capability Token、无 CORS |
| Browser Cache 被误当 Authority | Cache 可丢弃，Snapshot + Cursor 可独立重建 |
| 刷新导致重复执行 | Mutation Idempotency Key；Reload 只 Query/Replay |
| Snapshot 混合不同水位 | As-of Watermark、Source Sequence 和 Retention 竞态重建 |
| 合法 Sequence 空洞被误判 | Client 只检查单调性，Desync 只接受 Server 明确信号 |
| 大 Transcript 卡死页面 | 分页、Virtualization、Chunk 合并和对象引用稳定 |
| Web 变成第二个 IDE | 只读资源视图；写入继续由 Agent Tool 完成 |
| Credential 跨存储崩溃留下半状态 | 非 Secret Recovery Intent、Config CAS、启动 Reconcile |
| Runtime 初始化失败时页面不可达 | 两阶段 Boot Surface，完整 Mutation 仅在 Runtime Ready 后开放 |
| 旧 Binary 无法读取新数据 | Expand-only、真实 Downgrade Matrix、一致性 Backup/Restore Drill |
| 静态资源与 Go Binary 漂移 | Content Hash、生成检查、Release Manifest |
| 过早删除 VS Code/ACP | 只在迁移分支删除，主分支和 Release 保持上一个完整版本 |
| 前端架构过度复制 Harness | 只采用 Transport、状态分层和安全模式，不引入其插件框架 |
| 视觉参考退化为品牌复制 | 复用交互原则，使用 CodeHelper Token、Icon、文案和视觉资产 |
| Streaming 动画造成卡顿 | 动画数量有界、只用 Compositor 属性、Hidden/Offscreen 暂停 |

## 20. 最终验收标准

方案完成必须同时满足：

1. `codehelper web` 从单个发布 Binary 启动完整本机 UI，并只监听 `127.0.0.1`；
2. Web Host 不直接执行 Provider、Tool 或 Sandbox，所有 Mutation 经过现有 Runtime；
3. Web 与 In-memory Driver 直接验证 Runtime Session/Operation/History Contract，
   目标态不存在 ACP Transport 或通用 Control Facade；
4. Web Operation Exposure 对所有 Operation Kind 显式选择 Exposed/Denied，未知 Kind
   Fail Closed；
5. 页面刷新、Socket 断开、慢消费者和浏览器崩溃不会重复 Prompt 或 Side Effect；
6. 合法 Sequence 空洞不会触发 Desync，Retention Gap 由 Server 明确报告；
7. Presentation Snapshot 严格绑定 As-of Watermark，不能混入未来 Read Model 状态；
8. Runtime 重启后 Session、Pending Interaction、Checkpoint、Plan 和 Cursor 可恢复；
9. Approval/Input 绑定准确 Request、Turn、Item 和 Plan Identity；
10. Web 可完成 Read、Edit、Approve、Verify、Review Diff、Merge/Apply、Export、Recover
    和 Receipt 主流程；
11. Workspace Context 全部由 Runtime 重解析和校验，浏览器 Path 不具 Authority；
12. Credential Value 只进入 OS Keyring，不进入持久化、日志、Event 或 Browser
    Storage，Crash Recovery Intent 可收敛所有半状态；
13. Security、Contract、Replay、Browser E2E、Accessibility、Performance 和 Race
    Gate 通过；
14. 页面在四种目标 Viewport、Light/Dark/High Contrast、Reduced Motion 和 200% Zoom
    下无重叠、不可达主动作或非预期 Layout Shift；
15. 当前 Commit 生成的 Parity Report 证明 Inventory/Ledger 双向覆盖，全部 Required
    和 Retained Secondary 为 Verified，全部 Intentional Drop 已从产品移除；
16. Boot Surface 在 Runtime 构造失败时仍可达，但只在 `Runtime Ready` 后开放 Mutation；
17. 最后旧 Binary 通过 Web RC Data Dir 的真实 Downgrade Matrix，备份恢复演练通过；
18. README、中文产品文档、Book 和 Release Pipeline 以 Web 为主入口；
19. `extensions/vscode`、`internal/host/runtimeapi/acp` 及其命令、构建、发布、测试和
    文档依赖已删除；
20. CLI/TUI 在没有 Web Owner 时继续作为交互入口；Worker/Automation 可与 Web 并存，
    且不会跨 Runtime Role 接管普通交互 Turn。

### 20.1 验收证据索引

以下证据必须来自同一 clean Commit。`.tmp/web-feature-parity-report.json` 和
`.tmp/test-lanes/release.json` 是运行产物，不作为源码提交；Release Gate 在生成报告、
重建 Web Asset 和完成降级演练后再次检查工作树，任何 `qualified_dirty` 都会失败。

| 项 | 权威证据 |
| --- | --- |
| 1 | `TestBootstrapIsLoopbackFencedAndDoesNotCacheToken`、`TestWebRejectsNonLoopbackAndBypassPosture`、`boots the real Runtime with an accessible empty state` |
| 2 | `TestWebHostMeetsTheRuntimeContract` 和 Architecture Ratchet；Web Handler 只依赖 Runtime Application Service |
| 3 | `TestRuntimeApplicationMeetsTheHostContract`、`TestWebHostMeetsTheRuntimeContract` |
| 4 | `TestWebOperationExposureClassifiesEveryProtocolOperation`、`web-operation-exposure.json` |
| 5 | `repeated reloads do not resubmit an active streaming turn`、`a frozen tab converges after streaming completes`、`TestWebSocketDisconnectStormReleasesSlotsGoroutinesAndDescriptors` |
| 6 | `TestRuntimeReplayEventsSurfacesCursorGap`、`advances the replay cursor from payload-free watermark frames` |
| 7 | `TestPresentationReadFenceBindsLifecycleThreadsAndEventWatermark`、`HistoryService.buildSnapshot` 的 `ThroughSequence` Fence |
| 8 | `TestPersistentRuntimeRestoresPendingWithoutReplayingEngine`、`TestCheckpointRestoreIsStateOnlyAndForkPreservesLineage`、`TestExtensionSessionRestoresPlanAndAdvancesNextSnapshot` |
| 9 | Runtime Contract 的 Approval/Input 场景、`TestRuntimeApprovalPauseResumeE2E`、`TestRuntimeInputPauseResumeE2E` |
| 10 | `TestWebHostMeetsTheRuntimeContract`、`web-feature-parity.json` 的 Journey、真实 Web E2E 和 Visual Fixture |
| 11 | `TestServiceDoesNotExposeGitIgnoredFiles`、四个 `TestValidateWeb*Context`、`TestWebInlineContextAcceptsOnlyPersistedToolResultForThread` |
| 12 | `TestKeyringLifecycleNeverReturnsSecret`、`TestCredentialControlRecoversRotationAndDeferredCleanup`、`secret-leak-test` |
| 13 | `make verify`、`make web-e2e`、`make benchmark-v2`、`passes the WCAG A and AA accessibility scan`、`make test-release` |
| 14 | `captures viewport theme contrast and zoom matrix`、18 张 PNG Golden、`web-experience-contract.json` |
| 15 | `web-parity-check` 的 104 项 Inventory/36 项 Feature 双向覆盖，以及 clean Commit 上的 `verified` Parity Report |
| 16 | `TestBootFailureAndBootstrapScriptAreNotCached`、`TestUnaryRequiresTokenAndRuntimeReadiness` |
| 17 | `make web-release-drill` 和 `.tmp/release/web-downgrade-drill.json` |
| 18 | `make docs-check book-check`、README、中文产品文档、Book 与 `.github/workflows/ci.yml` |
| 19 | 删除 `extensions/vscode`、`internal/host/runtimeapi/acp` 和旧构建入口；Parity 中两个 Intentional Drop 为 `verified_drop` |
| 20 | `TestAcquireRejectsConcurrentOwnerAndAllowsTakeoverAfterClose`、`TestWebWorkerAndAutomationShareDurableStateWithoutOwnerConflict`、CLI/TUI Host Journey |

完整发布验收命令：

```bash
make verify
make web-e2e
make test-release PREVIOUS_RELEASE_REF=<上一发布提交>
```

其中 Release Lane 强制运行 1 小时持续 Streaming、10,000 Event Replay、16 Browser
Connections、32 Active Sessions、500 Turn Window、Web/Worker/Automation 共存和真实
Backup/Restore Downgrade Drill。

## 21. 推荐的首个实现切片

第一个迁移分支检查点只做 Capability Capture，不修改生产路径：

1. 新增封闭的 `legacy-capability-inventory.json`、`web-feature-parity.json` 及
   生成式校验器；
2. 枚举 VS Code Contribution/Command/Action、ACP Method、Host Journey、
   Make/Release/Documentation Entry 和 Retained Secondary Surface；
3. 将 Runtime Event Recording 和 Golden 迁到 Host-neutral `testdata/contracts`；
4. 增加直接驱动 Runtime Application Service 的 In-memory Contract Driver；
5. 生成带 Source Digest 的 Capture Report，运行并保存删除前的完整基线，不新增 Web、
   HTTP 或 React。

第二个检查点把必须保留的业务逻辑直接归入 `internal/runtime/app`，并在同一检查点删除
VS Code 与 ACP。之后只允许沿 Web 单轨继续建设。这样保留的是可执行行为契约，不是旧
实现或双架构。
