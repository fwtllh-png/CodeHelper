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
- Background Task、Job、Agent、Usage 和 Change View；
- Local、Remote SSH 与 Dev Container Workspace Host；
- External、Managed 或 Bundled Runtime；
- Signed Managed Update、Rollback 与 Revocation。

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
