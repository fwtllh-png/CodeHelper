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
- Background Task、Job、Agent、Usage 和 Change View；
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
Forced-color Border 和 Reduced Motion。Reasoning 与 Tool Detail 默认折叠，仅在活动
状态或用户打开时展开。

## 原生资源导航

Runtime 确认的 Editor Context、Context Selection 和 Edit Plan 会投影为不透明的
Resource ID。Chat 中的文件引用通过 VS Code 原生 API 打开；Range 与 Diagnostic
定位对应选区；Symbol 使用 Definition Provider，并在无定义结果时回退到文件内范围；
Directory 在 Explorer 中定位；Plan 使用 Diff Editor 打开。

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
和 Delete 都会失败。删除 Workspace 中最后一个 Session 或删除仍有未合并 Worktree
变更的隔离 Session 也会失败。隔离 Session 删除成功后，由 Runtime-owned Lifecycle
路径清理 Worktree；所有检查继续遵守 Guard、Journal、Sandbox 与持久化 Revision CAS
边界。

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
Ceiling、Guard、Policy、Journal 和 Sandbox 仍是最终权威。

共享协议同时冻结后续 Workflow Request。Retry 与 Continue 始终使用 Idempotency Key
创建新 Turn，绝不重放历史 Tool Operation。Plan Transition 显式指定当前 Session、
新 Session 或 Checkpoint Fork。这些 Request Contract 不表示当前 Chat UI 已暴露全部
动作。

Host/Webview 增量契约已冻结但尚未激活。Full Snapshot 携带单调 Projection Revision；
后续 Patch 引用 Base Revision，并且只允许类型化 Turn、Runtime、Composer 和 Resource
操作。在 Webview Store 能原子应用 Patch 前，活动 Message Set 仍是 Snapshot 与
Terminal Error。

## 性能与可访问性契约

Session Rail 对分组搜索结果进行虚拟化：Runtime-owned 列表和搜索投影保持完整，
Webview 只为可见行和有界 Overscan Window 创建 DOM。Transcript Article 使用浏览器
Content Visibility，屏幕外 Turn 不参与 Layout 和 Paint。Chat View 隐藏时，
Extension Host 继续更新 Runtime Projection，但停止组装和发送 DOM Snapshot；重新
显示时只发送一次最新 Projection。

发布性能门禁要求：

- Extension Activation 新增开销不超过 20 ms；
- 首次 Chat 可交互时间不超过 300 ms，不含 Runtime 启动；
- 200 Turn Session Snapshot 不超过 100 ms；
- 1000 Session 搜索和虚拟首屏不超过 150 ms；
- 流式 Snapshot 每 16 ms Frame 最多一次。

Electron Journey 会动态切换 Default Dark Modern、Default Light Modern 和 Default
High Contrast Theme，应用 Zoom Level 4（约 200%），并验证 Webview 仍可交互。
键盘证据覆盖可见 Focus、虚拟行间的 Session Home/End/Arrow 导航、Escape、窄 Rail
Focus Trap、Reduced Motion 和 Forced-color Border。

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

```bash
make vscode-package
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
