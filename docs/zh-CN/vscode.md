# VS Code 插件

简体中文 | [English](../en/vscode.md)

## 设计

插件是运行在本地 UI Extension Host 中的 TypeScript Client。它负责编辑器上下文和
呈现，不负责模型推理，也不直接修改工作区。

```text
VS Code UI 与 Context Bridge
        -> ACP Stdio
        -> CodeHelper Go Runtime
        -> Guard / Journal / Sandbox
```

因此 CLI、TUI 与 VS Code 共享审批、Event、持久化和安全语义。

## 能力

- Multi-root 与 Multi-chat Workspace Binding；
- Reasoning/Output 分离的流式 Chat；
- Selection 与 Diagnostic Action；
- Edit Plan Preview 与 Approval；
- Runtime 确认的 File、Range、Directory、Symbol、Diagnostic 和 Edit Plan Diff
  原生导航；
- Chat 内联 Mermaid 架构图，使用本地惰性渲染并支持源码回退；
- Background Task、Job、Agent、Extension、Usage 和 Change View；
- 本地 UI Extension Host 中的 `file:` Workspace；
- External、Managed 或 Bundled Runtime；
- Signed Managed Update、Rollback 与 Revocation。

## 工作台与键盘主路径

Chat 是主界面；Changes、Threads 和 Approvals 是主要 Review 导航；Agents、Tasks、
Jobs 和 Usage 默认折叠为 Detail View。Edit Plan 使用原生 Diff Editor，选项使用
Quick Pick，Setup 使用 Progress，持久化集合使用 Tree View。

使用 `Ctrl+Enter` 或 `Cmd+Enter` 发送，使用 `Escape` 停止活动 Turn。生命周期状态带
明确展示 Setup、Empty、Loading、Streaming、Approval、Verify、Failure、Recovery 和
Completed，并给出下一步动作。控件使用 VS Code Theme Token、可见键盘焦点、
Forced-color Border 和 Reduced Motion。活动详情默认折叠；展开思考详情后，长
Reasoning 正文仍提供独立的限高展开控制。

Turn 内容严格遵循 Runtime Event 顺序。模型输出继续作为普通 Markdown 展示。模型正文、
Approval 或待输入节点之间连续出现的所有非正文事件，统一收口为一个默认折叠的
`Activity · N items` 紧凑入口，避免重复的 Reasoning 和 Tool 行挤占正文空间。展开后仍按
原始顺序展示 Reasoning、Commands、Edited Files 和 Verification 节点；References 使用
独立的单个折叠入口。Approval 和待输入节点仍在原位置直接展示。Tool 节点在 `tool.start`
时固定位置，后续流式输出与终态结果
只更新该节点，不改变顺序。只有 Completed Turn 中最后一次活动之后的权威输出才标记为
`Final Result`。完成事件收口时不会删除用户已经看到的正文：已存在的最终后缀会被拆分为
独立结论，重新采样的最终回答则追加展示。
Runtime 不根据模型输出中的措辞推断是否完成。结构化 `max_tokens` 或 `incomplete`
Stop Reason 最多触发两次自动续写；已捕获的 Blocks 会作为上下文重放，并合并进权威
最终正文。第三次仍未完成时明确失败；Content Filter 停止则不续写并立即失败。可见
正文为空时触发有界收口修复；Engine 未发布终态便返回时按失败收口。Tool 或 Engine
Panic 以及类型化 Tool 错误会被限制在 Turn 边界，并投影为唯一且明确的失败终态。
权限、重试和终态只由类型化错误、协议字段与显式 Metadata 驱动。
命令节点默认折叠：标题只显示执行状态和有界的单行命令缩略内容；展开后分区展示完整
命令、附加参数与输出。
已完成的文件编辑节点按 Guard 实际观察到的文件逐行展示，包含语言感知的类型标识和
`+新增/-删除` 行数。点击文件名只提交不透明 Resource ID，并沿用其他 Chat Resource
相同的 Host 校验与原生导航路径。

## 原生资源导航

Runtime 确认的 Editor Context、Context Selection 和 Edit Plan 会投影为不透明的
Resource ID。Chat 中的文件引用通过 VS Code 原生 API 打开；Range 与 Diagnostic
定位对应选区；Symbol 使用 Definition Provider，并在无定义结果时回退到文件内范围；
Directory 在 Explorer 中定位；Plan 使用 Diff Editor 打开。

Agent Markdown 也可以直接引用工作区相对目录或代码位置。行内代码
`extensions/vscode`、`src/chat/view.ts:120-145`，以及
`src/protocol/generated.ts#L10-L20` 形式的链接，都会投影到相同的不透明 Resource
契约。只有现有上下文能够唯一确定资源时，才会链接不带目录的文件名。

`mermaid` Fenced Code Block 会在 Chat 内渲染为架构图。渲染器是本地惰性加载的
Bundle，运行于 Strict Mode，会清理 SVG 中的主动内容；渲染失败时回退为原始图表
源码。它不会从网络加载 Script、Style、Image 或图表数据。

Webview 永远不提交 URI、Path 或 Command。Extension Host 仅从当前 Snapshot 中解析
Opaque ID，校验 Exact Workspace Root 与相对路径，然后调用固定的 VS Code Action。
Absolute Path、Path Traversal、任意 URI Scheme、`command:`、Cross-root Definition、
未知 ID 和过期 Diff Identity 都会 Fail Closed。该边界适用于本地 Single-root 与
Multi-root Workspace。

## 本地工作区边界

CodeHelper 将 VS Code 插件声明为 UI Extension，并且只支持本地 `file:` Workspace。
Remote SSH、Dev Container、Codespaces、WSL Remote Workspace 和其他
`vscode-remote:` 环境不在产品范围内。插件会拒绝 Remote Activation，不会在文件系统
或 Workspace Identity 不匹配时启动 Runtime。

## Native Chat 契约决策

Native Chat 契约固定四项产品边界：

- Runtime 持久 Mode 是 `plan`、`act` 和 `operate`；Composer 分别显示 Plan、
  Implement 和 Operate。Ask 是 Prompt 意图，不是第四种持久 Mode；
- `execution_target` 当前只允许 `local`。在 Runtime 构造和 Policy 能真实应用前，
  不把 Sandbox 宣称为可选 Target；
- 同一个 `WebviewView` 可由用户在 Sidebar 与 Panel 之间移动。独立 Full Editor Chat
  不在当前产品范围；
- Checkpoint Restore 只恢复状态。Busy、Profile Revision 过期、不支持和
  Wrong-session 使用结构化 Problem Details；文件冲突属于显式 Revert 或 Merge，
  不属于 Restore。

## Session Profile 契约

Runtime 持有每个 Session 的 Mode、Provider、Model、Reasoning Effort、Enabled Tool
ID、Approval Posture、Execution Target、Step Limit、Revision 和 Prompt Cache
Revision。Extension Host 通过 `session/profile/get`/`session/profile/update` 访问；
Webview State 与 `workspaceState` 都不是 Profile Store。

推理模型统一使用 Runtime 选择的最高推理档位，Webview 不再提供 Session 级
Reasoning Effort 选择器。

更新必须携带已观察到的 Revision；过期写入或活动 Turn 都会失败。改变 Model-visible
Request Shape 的字段会推进 Prompt Cache Revision。Capabilities 只公开当前 Runtime
真正可应用的字段。当前 Runtime Route 不可热切换；Provider/Model 通过 Setup 后重启
本地 Runtime 完成切换。后续 Catalog-backed Runtime 将这些字段广告为 Mutable 后，
才能原地切换。

Composer 将该契约投影为原生风格的 Mode、Provider、Model、Thinking、Tools、
Credential 和 Approval 控件。Mode、Thinking、Tools、Approval 使用带 Revision 的
Profile Update；Provider、Model 使用 Setup + Local Runtime Restart。Runtime 未广告
对应 Capability 时，控件保持禁用。

生成的 Model Capability 契约包含展示名、Context/Output Limit、Tool Calling、Image、
Reasoning、Prompt Cache、Parallel Tool 支持状态、Reasoning 选项与默认值、
Credential 状态、Availability、不可用原因和 Selection Mode。`provider/list` 与
`model/list` 返回版本化 Catalog。当前单 Route 明确返回 `restart_required`，不宣称
支持热切换。

原生 Provider/Model Quick Pick 直接消费这些 Runtime Catalog，支持搜索，并展示
Availability、Capability 和 Restart Required。选择非 Hot Route 会进入受保护的 Setup
流程并重建本地 Runtime；Webview 不提交 Provider Capability 或 Profile Patch。

Add Context 控件通过原生 Host Quick Pick 选择已保存的 Workspace File、活动 Selection、
Symbol、Diagnostic、Image、显式 Clipboard Terminal Output 和当前 VS Code Git Diff。
Webview 只提交 Add/Remove Intent，并接收不具权威性的显示 Chip；Path、Byte、Range 与
Digest 均由 Extension Host 捕获。Runtime 随后重新解析 Workspace File、校验 Canonical
Identity 与 SHA-256、限制 Text/Image 大小，并为每个接受项生成 Durable Receipt。PNG、
JPEG、GIF 与 WebP 仅在当前模型广告 Image Input 时通过 Provider 原生 Image Content
Block 发送。Terminal Text 必须经 Clipboard Modal 显式确认；Git Diff 来自 VS Code
内置 Git API。

Execution Environment 从 Session Profile 投影并固定为 Local。统一 Keyboard Router
忽略 IME Composition；Escape 先关闭最上层 Session Drawer，再处理活动 Turn；
Cmd/Ctrl+N 创建新 Chat。

## Unified Tool Catalog

`session/tool/catalog` 按 Session 投影 Runtime Registry。它统一返回 Built-in、MCP、
Plugin、Skill 和受信任 Dynamic Tool，并包含 Catalog Identity、Generation、Digest、
Source、Capability、Access Mode、Sandbox Requirement、Availability 与当前 Session
Enabled 状态。Registry Authority 和 Input Schema 不越过该只读边界。

每个条目还返回静态 Risk Level，以及带原因的 Policy/Constitution State。Catalog
Projection 在裁决依赖已校验调用参数和资源时返回 `deferred`。这些字段只用于解释；
最终决定仍由 Tool Guard 作出。

Tool ID 与 Source Family 绑定，MCP Tool 还绑定 Server Identity；Tool 被 Revoke 后，
既有 Session Grant 不会转移给另一 Family 或另一 MCP Server 的同名 Tool。

Tools 控件打开按 Source 分组的原生可搜索多选框；Unavailable 和 Deferred 条目仍会展示
对应状态。空 `enabled_tool_ids` 表示 Runtime 兼容默认值“全部工具”，非空列表表示严格
Session Allowlist。Engine 在向 Model 广告 Tool Definition 和实际执行前都会应用该
Allowlist。选择 Tool 不授予权限：已启用调用仍经过 Guard、Policy、Approval、Journal
和 Sandbox。

在 Act Mode 与 `suggest` Posture 下，受 Workspace 约束的 `file_write` 默认不再请求
交互式审批。它仍然经过 Resource 校验、Repository Rule、已有文件的
Read-before-write、Journal 与原子提交。显式 Repository ask/deny Rule、Plan Mode、
Granular Deny 和可选的强制 Edit Plan 模式仍具有最终约束力。其他写入和高风险操作
继续采用原有审批行为。
只读检查 Pipeline 使用 `shell_read`：OS Strong Sandbox 强制 Workspace 只读并禁用
网络，因此 Auto Posture 可在不审批的情况下执行。通用 Command 使用
`exec_command`，持续交互的 Pipe 或 PTY Session 使用 `write_stdin`；二者继续保留
Process Approval 边界。

Chat Approval 卡片只展示有界的语义摘要。文件正文、Patch 和其他长参数以目标及大小
表示，不再完整展开；Edit Plan Approval 仍保留原生 Diff Preview 操作。

## Runtime-owned Extension Control

Extensions Tree 会对每个 Workspace Runtime 查询 `extension/list`，展示 Plugin/Skill
Name、Version、Trust、Enabled State 与 Runtime Health。Enable/Disable 会携带唯一
Operation ID 提交版本化 `extension/control` Operation。Extension Host 不扫描 Plugin
或 Skill Directory，也不直接修改 Enablement File。

Runtime Control Plane 拥有 Source Resolution、Trust、Generation、Capability、
Lifecycle Effect、Durable Receipt、Idempotency 与 Restart Reconciliation。相同
Operation ID 加相同 Payload 会返回已提交结果；同一 ID 携带不同内容会失败。Disable
会 Drain 所属 Effect，Revoke/Security Revoke 会 Fence 已加载 Generation。Tool
Catalog Refresh 是独立 Runtime Projection，因此 Extension Action 不会授予 Tool
Permission，也不能绕过 Guard。

当前 Tree 提供 List、Enable、Disable 与 Refresh。Install、Update、Rollback、Trust、
Capability Control、Health、Permission 与 Receipt 在对应 CLI 或 Shared Runtime
Protocol 支持时使用；Webview 不创建不完整的平行实现。

## Session Lifecycle

Runtime 是 Session 发现和生命周期状态的持久化权威。
`session/list`、`session/status`、`session/lifecycle/update` 和
`session/delete` 提供带 Revision CAS 的 Summary 与变更操作。Search 匹配标题、
用户请求、Agent 输出、路径和符号证据；Status 将持久化 Turn 状态与 Session 下全部 Thread 的
实时活动合并，包括待处理 Approval 和 Input。

Durable Session Summary 不携带瞬时 Search State。`session/list` 单独返回按 Session
与 Turn 标识的 Match、Match Kind 和可选 Snippet。Host 可以缓存 Durable Summary，
但 Query 变化时必须丢弃或替换 Match。

Summary 还投影最新不可变 Checkpoint 的 Changed File 数量、本地 Execution
Environment、Parent Fork Thread 和 Latest Turn Identity。Rail 可组合 Workspace、
Model、Mode、Status、Pending/Changed/Fork Activity 与文本过滤。选择 Match 后先切换
Session，再聚焦 Runtime 标识的稳定 Turn Article。

Session 操作通过 VS Code 原生 UI 提供 Duplicate、Open to Side、Markdown 或带完整性
校验的 Structured Receipt Export，以及 Reveal Pending Approval。Duplicate 只将
Runtime-owned Profile 复制到新 Session，绝不重放历史 Tool、Command、Network 或 File
副作用。Export 仅从 Host Projection 生成，不读取 Webview DOM。

Resource Action 继续只提交 Opaque Resource ID。Open、Open to Side 和 Copy Relative
Path 在调用 VS Code API 前都由 Extension Host 重新校验 Root、Scheme、Canonical Path、
Range 和 Diff Membership。

Session Rail 按 Pinned、Today、Yesterday、Previous 7 Days、Older 和 Archived 分组，
支持状态筛选，并展示 Workspace、Provider/Model、生命周期状态、Usage 与待处理交互数。
Rename 使用原生 InputBox；Pin、Archive、Restore 和 Delete 使用原生 Quick Pick；
Delete 还必须经过 Modal Confirmation。

VS Code `workspaceState` 只保存连接身份、Replay Cursor 和当前选中的 Session。Title、
Isolation、Profile、Status、Pin 和 Archive 都在每次重启后从 Runtime 查询。Archived
Session 仍可发现，但 Restore 前不会建立连接。

任一 Session Thread 处于 Running、Awaiting Approval 或 Awaiting Input 时，Archive
和 Delete 都会失败。删除 Workspace 中最后一个 Session 时，扩展会先创建并选中一个
新的空 Session，再删除目标 Session；如果删除失败，则回滚新建的替代 Session。删除
仍有未合并 Worktree 变更的隔离 Session 依然会失败。隔离 Session 删除成功后，由
Runtime-owned Lifecycle 路径清理 Worktree；所有检查继续遵守 Guard、Journal、
Sandbox 与持久化 Revision CAS 边界。

隔离 Chat Worktree 内的 Git Command 仅保留对已校验 Worktree Administration
Directory 和 Repository Common Git Directory 最小必要路径的只读访问。Strong
Sandbox 仍拒绝写入 Parent Workspace，并拒绝通过 `.git` 或 `commondir` 越出可信
Repository Metadata Root。

## Checkpoint、Fork 与 Plan

Completed Turn 和安全的 User-interrupted Turn 会创建不可变 Session Checkpoint。
`checkpoint/list`、`checkpoint/get`、`checkpoint/restore` 与 `checkpoint/fork`
暴露 Runtime-owned 对象。Metadata 记录 Session、Thread、Turn、Event Cursor、
Profile Revision、Parent Checkpoint、Changed File 数量和保守的 Side-effect 状态；
Model-visible Replacement History 与 Profile Snapshot 存入 CAS，使用前必须完成
完整性校验。
存在 Turn Receipt 时，Checkpoint Metadata 保存 Event ID、Turn ID 和 Cursor 的不可变
引用，不复制可变展示文本。

Restore 只恢复状态：它替换 Model-visible Runtime History 并产生持久化
`checkpoint.restored` Event，但绝不重放或回滚已完成的 File、Tool、Command 或
Network Effect。Profile Revision 过期、活动 Turn、待处理 Approval/Input、CAS 损坏
或 Cross-session Identity 都会 Fail Closed；重启重建使用相同 Restore Baseline。
ACP 会在 JSON-RPC Error Data 中保留 Runtime Problem，包括机器可读 Reason 以及相关
Status 或 Revision。

Fork 从 Checkpoint 创建独立 Engine History，并持久化 Parent Thread 与 Parent
Checkpoint 血缘。当前 Active Thread 写入 Session Lifecycle Metadata，因此重启后
不会回退到 Root Thread。Session 菜单使用原生 Checkpoint Quick Pick 提供 Restore
和 Fork。

完成的流式 Plan 会保存为结构化 Plan Artifact，而不是由 Webview 从 Markdown 推断。
Plan Card 支持在原生 Editor 打开、开始实现或请求 Autopilot。两种实现操作都会校验
Artifact 与 Profile Revision，通过 Runtime Profile Contract 切换状态并提交一个新的
Turn。Autopilot 请求 `act` Mode 与 `auto` Approval Posture，但 Host Permission
Ceiling、Guard、Policy、Journal 和 Sandbox 仍是最终权威。低风险操作自动执行，
可修改状态的 Process、Network 和 Plugin Tool 则暂停并请求审批。沙箱化
`shell_read` 检查会自动执行，因为 Workspace Write 与 Network Access 在机制上不可用。

Retry 与 Continue 已成为可执行 Runtime Workflow。两者始终使用 Idempotency Key 创建
新 Turn，绝不重放历史 Tool、Command、Network 或 File Operation。Retry 复用源 Turn
的持久化 Model-visible Request；Continue 使用 Terminal History 与可选 Guidance。提交后
按钮立即禁用。Required Verification 的无进展 Repair Budget 用尽时，源 Turn 以
`blocked` 失败，但 Journal 会保留为未验证 Draft。`Continue Repair` 将同一 Journal
原子重绑定到 Recovery Turn，并保留最初 Before Image；`Discard & Retry` 则在重新执行
Request 前显式撤销 Draft。Runtime 接受恢复后，按钮替换为 `Retry started` 或
`Continue started`；提交失败则恢复按钮并显示真实错误。
Plan Transition 可显式选择 Current Session、保留 Profile 的 New Session 或 State-only
Checkpoint Fork。Runtime 校验源 Artifact 并构造 Implementation Prompt，Webview 不做
反向推断。

Host/Webview 增量契约已激活。Full Snapshot Hydration 携带单调 Projection Revision；
后续 Patch 精确引用 Base Revision，并且只携带类型化 Turn、Runtime、Composer 和
Resource Operation。Webview Store 原子应用 Patch，Revision 不匹配时请求 Full Snapshot
Resync。Turn、Call、Request 与 Session Stable Identity 用于在更新后保持展开状态、
Focus 与 Scroll Anchor。

## 性能与可访问性契约

Session Rail 对分组搜索结果进行虚拟化：Runtime-owned 列表和搜索投影保持完整，
Webview 只为可见行和有界 Overscan Window 创建 DOM。Transcript Store 保留完整投影，
但 DOM 只创建 Viewport Turn Window 与有界 Overscan。Chat View 隐藏时，
Extension Host 继续更新 Runtime Projection，但停止组装和发送 DOM Snapshot；重新
显示时只发送一次最新 Projection。

发布性能门禁要求：

- Extension Activation 新增开销不超过 20 ms；
- 首次 Chat 可交互时间不超过 300 ms，不含 Runtime 启动；
- 200 Turn Session Snapshot 不超过 100 ms；
- 单 Turn Patch 不超过 100 ms，且 Payload 小于 Full Snapshot 的四分之一；
- 每次测量 Patch 影响的 Turn DOM 节点不超过 2 个、虚拟 Turn 节点不超过 30 个、
  Scroll Anchor 误差不超过 1 px；
- 1000 Session 搜索和虚拟首屏不超过 150 ms；
- Webview 隐藏期间 Post 数为 0，恢复不超过 300 ms；
- 流式 Patch 每 16 ms Frame 最多一次。

Electron Journey 会动态切换 Default Dark Modern、Default Light Modern 和 Default
High Contrast Theme，应用 Zoom Level 4（约 200%），并验证 Webview 上报的 Theme
Class。独立 Chromium High Contrast 进程验证 `forced-colors` Media Query 已激活。
Bundled Webview 通过严格解码且不含敏感数据的 Evidence Message 上报 IME Composition
Guard、Viewport 与 Device Scale。

Release Matrix 汇总 Electron Evidence 中的必需 Journey ID；RC Gate 拒绝缺失的 Journey
或性能字段。唯一保留的人工 Journey 是将同一个 `WebviewView` 在 Sidebar 与 Panel
之间移动；产品不提供 Full Editor Chat。复现步骤随
`extensions/vscode/RELEASE-EVIDENCE.md` 一同打包。

## 内置 Setup 与 Repair

在已打开且受信任的工作区中执行 `CodeHelper: Setup Workspace`。引导流程会选择
Provider、Model 和 Credential Source，将 `recommended` Profile 写入
`codehelper.toml`，更新工作区 Runtime 设置并重启 Runtime。推荐选项使用 Password
InputBox 与 VS Code SecretStorage；Runtime 配置只保存生成的 Environment Reference，
Extension Host 仅向本地 Runtime Process 注入 Secret。Chat Webview、Profile、Log 与
Setting 都不会接收 Secret。仍支持外部环境变量、受保护文件和 OS Keyring Reference。

`CodeHelper: Configure Credential` 可替换当前 Provider 在 SecretStorage 中的凭证，
不会向 Webview 暴露。Untrusted Workspace 不能配置 Credential 或提升 Approval
Posture。以 Read-only Posture 启动的 Runtime 还会把恢复后的 Profile Clamp 到
`never`，历史持久化的 `bypass` 不能跨越 Host Trust Boundary。
Setup 或替换凭证后，Host 会通过 Runtime Credential 与 Egress 实现显式调用 Provider
Model-list Endpoint。系统只持久化 Validation Result、Timestamp 和有界 Failure
Category；Secret 与原始 Provider Response 不进入 Webview State 或 Log。

Runtime 启动失败或 Readiness 降级时，执行 `CodeHelper: Repair Runtime`。该命令会合并
VS Code Supervisor 启动错误与 `doctor --json`，逐项展示缺失能力的状态、原因、影响和
修复动作。Binary 解析失败时可直接进入 Settings、托管更新或 Output。Chat 失败面板也
提供 Setup 和 Repair 按钮。

`CodeHelper: Start Runtime Capture` 会把 Host/ACP/Process Supervision 视角写入私有
Workspace Storage；`Stop Runtime Capture` 以 mode `0600` 关闭文件。它与 Go Runtime
Observation Journal 及其 `CODEHELPER_OBSERVATION_CAPTURE` Policy 不同。两种 Artifact
即使经过 Redaction 也可能包含敏感 Workspace 数据，分享前都必须检查。

`CodeHelper: Run Quickstart` 可从 VS Code 启动内置无网络首轮旅程，不修改当前选中的
工作区。

## 开发配置

```bash
make build
make vscode-install
make vscode-check
make vscode-test
make vscode-build
```

编译入口为 `extensions/vscode/dist/extension.js`。

不启动 Electron 的真实 Runtime Integration：

```bash
make vscode-runtime-integration
```

官方 VS Code Electron Integration：

```bash
make vscode-integration
```

Electron 首次使用时下载，并有意不放入默认 Verify。

## 构建 VSIX

构建、安装并完成当前 Host Target 的 Runtime Ready Handshake：

```bash
make vscode-package
```

静态 Universal Package 有意不包含 Runtime，使用独立的安装审计入口：

```bash
make vscode-package-universal
```

多目标 Dry-run Artifact：

```bash
make vscode-distribution
```

Release 输出位于 `extensions/vscode/dist/`。

## macOS 一键本地配置

该脚本是有明确假设的一键流程：

1. 要求官方 `/Applications/Visual Studio Code.app`；
2. 构建并选择当前 macOS Target VSIX；
3. 安装仓库示例配置；
4. 从环境变量或安全终端输入读取 `DEEPSEEK_API_KEY`；
5. 写入 macOS Keychain 的 `deepseek/default`；
6. 更新官方 VS Code User Settings；
7. 安装扩展，并可选打开 Workspace。

仓库所有者已有 DeepSeek 本机环境时，使用完整的编译、配置、安装和启动入口：

```bash
make deepseek-vscode
```

凭证查找顺序与 Agent 约束见
[本机 DeepSeek 一键配置与运行](./deepseek-local.md)。

使用环境变量直接调用底层安装流程：

```bash
export DEEPSEEK_API_KEY='...'
make vscode-local-setup
unset DEEPSEEK_API_KEY
```

选项：

```bash
./scripts/setup-vscode-local.sh --skip-build
./scripts/setup-vscode-local.sh --no-open
```

运行前完全退出 VS Code。不要在会阻止 Keychain 或 Extension Directory 写入的受限
内嵌终端中执行。

## 手动 Runtime 配置

VS Code Settings：

```json
{
  "codehelper.binarySource": "auto",
  "codehelper.runtime.configPath": "/absolute/path/to/config.toml",
  "codehelper.runtime.autoStart": true,
  "codehelper.runtime.maxSteps": 64
}
```

Binary Source：

- `auto`：按 Verified Bundled、Managed、Configured External 选择；
- `external`：用户指定 Runtime；
- `managed`：Signed Update Store；
- `bundled`：Target VSIX 内 Runtime。

Workspace Trust 会影响权限。不可信工作区被强制为只读，也不能选择任意可执行文件。

## 兼容契约

事实来源为 `extensions/vscode/compatibility.json`，约束：

- Extension/Binary Version Range；
- ACP Protocol Range；
- Operation Schema Version；
- Required Runtime Feature；
- Target/Platform Identity。

通过以下命令刷新生成文件：

```bash
cd extensions/vscode
npm run generate:compatibility
```

## 发布门禁

```bash
make vscode-rc
```

RC 流程包含 Static Check、Protocol Drift、Runtime Integration、Security、
Performance、Electron、Update、Distribution、Matrix Evidence、SBOM、Provenance 与
Report。

必需的本地 Matrix 包含 7 个具名 Job：macOS arm64 External Single/Multi-root、
macOS arm64 Bundled、Rosetta 下的 macOS x64 External、Update Integration、
Distribution、Security 和 Performance。由于仓库没有 Windows Electron Runner，
Windows x64 Package Evidence 为 Optional。Remote SSH、Dev Container、Codespaces
与 WSL Remote Workspace 是不支持的产品环境，不是缺失的发布 Job。

在 Apple Silicon 发布主机上，可独立运行必需的 Rosetta Evidence：

```bash
make vscode-rosetta-integration
```

该命令构建 amd64 Runtime，在固定版本的 x64 VS Code Extension Host 中执行 Journey，
并在记录 Evidence 前同时断言 Host 与 Binary Architecture。

正式签名材料必须位于仓库外：

```bash
export CODEHELPER_RELEASE_PRIVATE_KEY=/secure/release-private.pem
export CODEHELPER_RELEASE_TRUST_ROOTS=/secure/release-trust-roots.json
export CODEHELPER_RELEASE_KEY_ID=release-key-id
export CODEHELPER_RELEASE_VERSION=0.1.0
export CODEHELPER_RELEASE_SEQUENCE=1
```

Dry-run Artifact 不能作为可发布证明。

## 排障

- `Runtime unavailable`：检查 `binarySource`、Config Path 和 Workspace Host。
- `Workspace identity mismatch`：不要在不相关 Editor URI 间复制状态。
- 写入被拒绝：Trust Workspace，再检查 Posture 与 Approval。
- Compatibility Failure：从同一代码树重新构建 Runtime 与 Extension。
- VSIX 装进错误编辑器：使用官方 VS Code CLI 绝对路径。
- Managed Update 被拒绝：检查 Signature、Sequence、Revocation、Digest 与 Target。

更多场景见[排障指南](./troubleshooting.md)。
