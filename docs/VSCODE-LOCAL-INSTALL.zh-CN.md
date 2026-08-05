# VS Code 插件本地安装指南

本文档用于在本机安装 CodeHelper VS Code 插件并进行功能体验。当前生成的是本地
dry-run 签名包，可用于安装测试，不能直接上传 Marketplace 或 Open VSX。

## 1. macOS 一键配置

### 1.1 前置条件

- 已安装 `/Applications/Visual Studio Code.app`。
- 已安装 Go、Node.js、npm 和 `make`。
- 本机是 macOS arm64 或 x64。
- 本地 `docs/DEEPSEEK-LIVE.zh-CN.md` 中已有 `DEEPSEEK_API_KEY`。
- 执行前通过 `Code > Quit Visual Studio Code` 完全退出 VS Code。

必须在 macOS 的 Terminal/iTerm 中执行。不要在 TRAE/Cursor 的受限集成终端中执行，
因为其 sandbox 可能禁止写 macOS Keychain 和 VS Code 扩展注册文件。

### 1.2 执行

在仓库根目录只需运行：

```bash
make vscode-local-setup
```

该命令会按顺序完成：

1. 构建 universal 和各平台 target VSIX。
2. 根据本机架构选择 `darwin-arm64` 或 `darwin-x64` target VSIX。
3. 安装 DeepSeek Responses TOML 到 `~/.config/codehelper/config.toml`。
4. 从本地 `DEEPSEEK-LIVE.zh-CN.md` 读取密钥并写入 macOS Keychain
   `deepseek/default`，不会打印密钥。
5. 增量更新官方 VS Code User Settings，保留已有字段和 JSONC 注释。
6. 使用官方 VS Code 的绝对 CLI 路径卸载旧 CodeHelper 并安装 target VSIX。
7. 验证 bundled catalog 中的 DeepSeek endpoint 和 `openai_responses` 协议。
8. 使用官方 VS Code 打开 `codehelper.code-workspace`。

脚本不会调用当前 `PATH` 中的 `code`，因此即使该命令指向 Cursor，也不会误装或误开
Cursor。

如果文档中没有密钥，脚本会安全提示输入；也可以预先通过环境变量提供：

```bash
read -r -s -p "DeepSeek API key: " DEEPSEEK_API_KEY
echo
export DEEPSEEK_API_KEY
make vscode-local-setup
unset DEEPSEEK_API_KEY
```

已经构建过分发产物时，可以跳过构建：

```bash
./scripts/setup-vscode-local.sh --skip-build
```

配置但不自动打开 VS Code：

```bash
./scripts/setup-vscode-local.sh --no-open
```

脚本可以重复运行。每次会刷新 DeepSeek TOML、Keychain 凭证、User Settings 和本地
VSIX。它不会删除工作区 Runtime 数据或会话记录。

### 1.3 配置结果

脚本完成后应有：

```text
~/.config/codehelper/config.toml
~/.vscode/extensions/codehelper.codehelper-vscode-0.0.1/
  bin/darwin-arm64/codehelper
  bin/release-manifest.json
```

VS Code User Settings 会包含：

```json
{
  "codehelper.binarySource": "auto",
  "codehelper.runtime.configPath": "/Users/<用户名>/.config/codehelper/config.toml"
}
```

首次打开后信任工作区，执行 `CodeHelper: Show Status`，应显示：

```text
CodeHelper Runtime: ready
```

## 2. 手动构建 VSIX

在仓库根目录执行：

```bash
make vscode-distribution
```

产物位于：

```text
extensions/vscode/dist/vscode-release/artifacts/
```

其中：

| 平台 | VSIX |
| --- | --- |
| macOS Apple Silicon | `codehelper-vscode-0.0.1-darwin-arm64.vsix` |
| macOS Intel | `codehelper-vscode-0.0.1-darwin-x64.vsix` |
| Linux x64 | `codehelper-vscode-0.0.1-linux-x64.vsix` |
| Linux arm64 | `codehelper-vscode-0.0.1-linux-arm64.vsix` |
| Windows x64 | `codehelper-vscode-0.0.1-win32-x64.vsix` |
| 不内置 Runtime | `codehelper-vscode-0.0.1-universal.vsix` |

本地体验推荐使用与系统匹配的 target VSIX，它已经内置对应平台的 CodeHelper Runtime。

## 3. 手动命令行安装

以下命令始终显式使用官方 VS Code CLI，避免 `code` 实际指向 Cursor。

### macOS Apple Silicon

```bash
"/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code" \
  --install-extension \
  extensions/vscode/dist/vscode-release/artifacts/codehelper-vscode-0.0.1-darwin-arm64.vsix \
  --force
```

### macOS Intel

```bash
"/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code" \
  --install-extension \
  extensions/vscode/dist/vscode-release/artifacts/codehelper-vscode-0.0.1-darwin-x64.vsix \
  --force
```

如果终端找不到 `code`，可直接使用 VS Code 自带的 CLI：

```bash
"/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code" \
  --install-extension \
  extensions/vscode/dist/vscode-release/artifacts/codehelper-vscode-0.0.1-darwin-arm64.vsix \
  --force
```

也可以在 VS Code 中执行 `Shell Command: Install 'code' command in PATH`，之后重新打开
终端。

## 4. GUI 安装

1. 打开 VS Code。
2. 打开命令面板。
3. 执行 `Extensions: Install from VSIX...`。
4. 选择与本机平台匹配的 VSIX。
5. 安装完成后执行 `Developer: Reload Window`。

## 5. 手动配置模型与凭证

CodeHelper Runtime 启动时必须有 provider 和 model。插件不会把 API key 写入 VS Code
settings。当前本地体验使用 `DEEPSEEK-LIVE.zh-CN.md` 已验证的 DeepSeek Responses
路由：

| 配置 | 值 |
| --- | --- |
| Provider | `deepseek-v4-flash` |
| Model | `deepseek-v4-flash` |
| Endpoint | `https://api.deepseek.com` |
| Protocol | `openai_responses` |
| Keychain reference | `deepseek/default` |

先安装配置文件，再将 DeepSeek API key 写入 macOS Keychain：

```bash
cd /Users/bytedance/go/src/code.byted.org/fuweiting.pro/flow/codehelper

mkdir -p "$HOME/.config/codehelper"
cp docs/examples/codehelper-vscode.toml \
  "$HOME/.config/codehelper/config.toml"

export DEEPSEEK_API_KEY='使用 DEEPSEEK-LIVE.zh-CN.md 中的实际密钥'
"$HOME/.vscode/extensions/codehelper.codehelper-vscode-0.0.1/bin/darwin-arm64/codehelper" \
  auth login \
  --config "$HOME/.config/codehelper/config.toml" \
  --kind keyring \
  --name deepseek/default \
  --from-env DEEPSEEK_API_KEY
unset DEEPSEEK_API_KEY
```

检查配置、catalog 路由和非敏感凭证状态：

```bash
"$HOME/.vscode/extensions/codehelper.codehelper-vscode-0.0.1/bin/darwin-arm64/codehelper" \
  config check --config "$HOME/.config/codehelper/config.toml"

"$HOME/.vscode/extensions/codehelper.codehelper-vscode-0.0.1/bin/darwin-arm64/codehelper" \
  model resolve \
  --provider deepseek-v4-flash \
  --model deepseek-v4-flash \
  --json

"$HOME/.vscode/extensions/codehelper.codehelper-vscode-0.0.1/bin/darwin-arm64/codehelper" \
  auth status --config "$HOME/.config/codehelper/config.toml"
```

在 VS Code 的 User Settings JSON 中设置 Host-local 绝对路径。不要使用 `~`：

```json
{
  "codehelper.binarySource": "auto",
  "codehelper.runtime.configPath": "/Users/<用户名>/.config/codehelper/config.toml"
}
```

`codehelper.runtime.configPath` 是 machine-scope 设置。Remote SSH 或 Dev Container 中必须配置
Remote Extension Host 内可访问的绝对路径，不能填写本机路径。

期望 `model resolve` 返回 `protocol=openai_responses` 对应的 JSON 路由，`auth status`
返回 `credential_kind=keyring configured=true`。其他 provider、model、协议与凭证配置见
[使用指南](./USAGE.zh-CN.md#2-配置)。

## 6. 首次启动

1. 使用 VS Code 打开一个文件夹。
2. 确认该工作区可信；不可信工作区只允许只读操作。
3. 在设置中将 `codehelper.binarySource` 设为 `auto` 或 `bundled`。
4. 确认 `codehelper.runtime.configPath` 指向上一节创建的 TOML。
5. 执行 `Developer: Reload Window`。
6. 打开左侧 Activity Bar 中的 CodeHelper 图标。
7. 执行 `CodeHelper: Show Status`。

正常状态应包含：

```text
CodeHelper Runtime: ready
```

如果状态为 `starting` 或 `failed`，打开 `View: Toggle Output`，在输出频道中选择
`CodeHelper` 查看诊断信息。

### 提示 `CodeHelper binary was not found`

该提示表示插件没有找到 bundled、managed 或 external Runtime。先检查本机是否误装了
不含 Runtime 的 universal VSIX：

```bash
find "$HOME/.vscode/extensions/codehelper.codehelper-vscode-0.0.1" \
  -path "*/bin/*" -type f
```

如果没有输出，完全退出所有 VS Code 窗口，然后覆盖安装对应平台的 target VSIX：

```bash
"/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code" \
  --install-extension \
  extensions/vscode/dist/vscode-release/artifacts/codehelper-vscode-0.0.1-darwin-arm64.vsix \
  --force
```

安装后应存在：

```text
~/.vscode/extensions/codehelper.codehelper-vscode-0.0.1/
  bin/darwin-arm64/codehelper
  bin/release-manifest.json
```

如果 CLI 提示 `Please restart VS Code before reinstalling CodeHelper`，说明扩展仍被某个
VS Code 窗口占用。使用 `Code > Quit Visual Studio Code` 完全退出，而不只是关闭当前
窗口，再执行安装命令。

### 提示 `workspace file ... has multiple hard links`

npm 可能把同一个平台二进制硬链接到多个 `node_modules` 路径，例如 esbuild 同时位于：

```text
node_modules/esbuild/bin/esbuild
node_modules/@esbuild/darwin-arm64/bin/esbuild
```

CodeHelper 会允许所有链接都可在 workspace 内完整证明的文件；只要文件系统链接数大于
workspace 内观察到的链接数，就表示至少存在 workspace 外链接，`shell_run` 仍会
fail-closed。旧版 bundled Runtime 无法区分这两种情况，会误拒绝 npm 的内部链接。

完全退出 VS Code，然后重新构建并安装当前版本：

```bash
make vscode-local-setup
```

不要通过跳过 `node_modules` 扫描或关闭 sandbox 绕过该错误，这会放松硬链接逃逸防护。

### 提示 `No tool call found for tool output with call_id`

该错误表示 OpenAI Responses 请求含有 `function_call_output`，但缺少同一 call ID 的
前置 `function_call`。旧版 Runtime 在重启后从 durable eventlog 恢复 thread 时只恢复
`tool.result`，没有恢复 `tool.start`，会产生这种孤立 output。

当前 Runtime 按 turn 暂存恢复事件，只提交 completed turn，并将成对的
`tool.start` / `tool.result` 恢复为 function call/output；failed、canceled 和中断的
半截 turn 会被丢弃。Responses encoder 在发送 HTTP 前还会再次校验配对。

完全退出 VS Code 并重新安装当前 target VSIX 即可，不需要删除 Runtime 数据或新建
thread：

```bash
./scripts/setup-vscode-local.sh --skip-build
```

## 7. 基础体验

建议依次验证：

1. 在 Chat 中发送一个只读问题。
2. 选中一段代码，执行 `CodeHelper: Explain Selection`。
3. 对诊断项执行 CodeHelper Code Action。
4. 请求一个多文件修改，检查 `CodeHelper Changes` 中的 diff。
5. 分别体验 approve 和 deny。
6. 点击 Chat header 的 `New` 创建两个隔离 Chat，同时提交任务，确认可并行运行。
7. 确认首次发送消息后，`Chat 1` / `Chat 2` 自动变为基于内容的简短标题。
8. 在隔离 Chat 点击 `Merge` 预览，再点击 `Apply` 合入主 workspace。
9. 执行 `CodeHelper: Restart Runtime`，确认多 Chat、标题及最近 200 个 turn 已恢复。

插件支持最多 8 个 workspace root。multi-root 工作区中，每个 root 使用独立 Runtime、
Chat sessions、approval 和 Changes 状态。每个 root 最多保存 32 个 live Chat；
显式新建 Chat 需要当前 root 是 Git worktree，自动创建的 `Chat 1` 在非 Git workspace
仍可使用。

## 8. 更新本地安装

代码修改后重新构建并覆盖安装：

```bash
make vscode-distribution

"/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code" \
  --install-extension \
  extensions/vscode/dist/vscode-release/artifacts/codehelper-vscode-0.0.1-darwin-arm64.vsix \
  --force
```

随后执行 `Developer: Reload Window`。

## 9. 卸载

```bash
"/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code" \
  --uninstall-extension codehelper.codehelper-vscode
```

也可以在 Extensions 页面找到 CodeHelper 并选择 `Uninstall`。

## 10. Universal VSIX

Universal VSIX 不内置 Runtime。使用它时，需要先构建 CodeHelper：

```bash
make build
```

然后将 `codehelper.binarySource` 设置为 `external`，并配置绝对路径：

```json
{
  "codehelper.binarySource": "external",
  "codehelper.binaryPath": "/absolute/path/to/codehelper/bin/codehelper"
}
```

`codehelper.binaryPath` 属于可执行文件选择边界。在不可信工作区中，workspace 配置的路径
不会被采用。

## 11. 发布限制

`make vscode-distribution` 默认生成：

```text
dry_run=true
uploaded=false
signing_key_id=dry-run-only
```

这些包仅用于本地体验和发布流程验证。正式市场发布必须使用 clean worktree、仓库外的
生产私钥与匹配的 public trust root，并通过：

```bash
make vscode-rc
```

完整发布步骤见 [VS Code Extension Release Runbook](./RELEASE-VSCODE.zh-CN.md)。
