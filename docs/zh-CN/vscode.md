# VS Code 插件

简体中文 | [English](../en/vscode.md)

## 设计

插件是运行在 Workspace Extension Host 中的 TypeScript Client。它负责编辑器上下文和
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
- Local、Remote SSH 与 Dev Container Workspace Host；
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
未知 ID 和过期 Diff Identity 都会 Fail Closed。Local、Multi-root、Remote SSH 与
Dev Container 使用同一边界。

## 内置 Setup 与 Repair

在已打开且受信任的工作区中执行 `CodeHelper: Setup Workspace`。引导流程会选择
Provider、Model 和 Credential Reference，将 `recommended` Profile 写入
`codehelper.toml`，更新工作区 Runtime 设置并重启 Runtime。凭证输入框只接受环境变量
名、受保护文件路径或 Keyring Key，不要输入 Secret 值。

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
