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

## Unified Tool Catalog

`session/tool/catalog` 按 Session 投影 Runtime Registry。它统一返回 Built-in、MCP、
Plugin、Skill 和受信任 Dynamic Tool，并包含 Catalog Identity、Generation、Digest、
Source、Capability、Access Mode、Sandbox Requirement、Availability 与当前 Session
Enabled 状态。Registry Authority 和 Input Schema 不越过该只读边界。

Tool ID 与 Source Family 绑定，MCP Tool 还绑定 Server Identity；Tool 被 Revoke 后，
既有 Session Grant 不会转移给另一 Family 或另一 MCP Server 的同名 Tool。

Tools 控件打开按 Source 分组的原生可搜索多选框；Unavailable 和 Deferred 条目仍会展示
对应状态。空 `enabled_tool_ids` 表示 Runtime 兼容默认值“全部工具”，非空列表表示严格
Session Allowlist。Engine 在向 Model 广告 Tool Definition 和实际执行前都会应用该
Allowlist。选择 Tool 不授予权限：已启用调用仍经过 Guard、Policy、Approval、Journal
和 Sandbox。

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
