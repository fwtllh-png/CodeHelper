# 快速开始

## 1. 前置条件

| 依赖 | 说明 |
| --- | --- |
| Go 1.26+ | Runtime 必需 |
| Git | 仓库工作流和 Worktree 隔离必需 |
| Make | 推荐的统一命令入口 |
| Node.js + npm | 仅开发 VS Code 插件时需要 |
| macOS/Linux | 推荐；Windows 的沙箱能力边界不同 |

检查环境：

```bash
go version
git --version
make --version
```

## 2. 从源码构建

```bash
git clone https://github.com/fwtllh-png/CodeHelper.git
cd CodeHelper
make build
./bin/codehelper version
./bin/codehelper help
```

二进制位于 `bin/codehelper`。需要写入版本信息时：

```bash
make build VERSION=0.1.0
```

## 3. 检查平台能力

```bash
./bin/codehelper doctor
./bin/codehelper sandbox status
./bin/codehelper sandbox probe
```

`doctor` 只报告能力，不会授予权限。强沙箱不可用时报告 `blocked`，因为修改型执行和
验证必须按 fail-closed 原则失败；可选依赖缺失时报告 `degraded`。两者 Exit Code
分别为 `2` 和 `1`，`ready` 返回 `0`。

## 4. 执行 Setup

进入希望 CodeHelper 操作的仓库：

```bash
/path/to/codehelper setup \
  --workspace . \
  --provider openai \
  --model gpt-4.1 \
  --credential-kind env \
  --credential-name OPENAI_API_KEY \
  --json
```

`setup` 只写入 Credential Reference，同时探测真实 Sandbox，并执行内置的无网络
Runtime Fixture。只有已提供可用的真实凭证引用且允许访问 Provider 网络时，才增加
`--probe-capabilities reasoning`。交互式流程使用：

```bash
/path/to/codehelper setup --workspace . --interactive
```

自动化可增加 `--require-ready`，让 Exit Code 与 `ready`、`degraded` 或 `blocked`
状态一致。`init` 仍保留为只创建最小文件的入口：

```bash
/path/to/codehelper init --workspace .
```

查看最终配置及每个字段来源：

```bash
/path/to/codehelper config check --config ./codehelper.toml
/path/to/codehelper config show --config ./codehelper.toml
```

完整字段见[配置说明](./configuration.md)。

### 无网络首次旅程

无需凭证和网络即可执行完整的受治理首轮：

```bash
./bin/codehelper quickstart --json
```

内置 Fixture 会创建临时工作区，依次执行结构化计划、文件读取、编辑预览、显式批准、
验证、Execution Receipt 和终态完成。增加 `--keep` 可保留生成工作区，也可通过
`--workspace EMPTY_DIR` 使用指定的空目录。

## 5. 配置凭证

首次配置推荐使用环境变量引用：

```bash
export OPENAI_API_KEY='...'
./bin/codehelper auth login \
  --config ./codehelper.toml \
  --kind env \
  --name OPENAI_API_KEY
```

TOML 只保存 `OPENAI_API_KEY` 这个名字，不保存值。也可以使用受保护文件或系统
Keyring：

```bash
./bin/codehelper auth set --help
./bin/codehelper auth status --config ./codehelper.toml
./bin/codehelper auth list
```

不要把原始 API Key 写入受 Git 跟踪的文档、源码、Git、Shell History、Issue 或普通
配置字段。唯一明确例外是仓库所有者要求的、权限为 `0600` 且被 Git 忽略的本机
DeepSeek Runbook；Agent 不得读取或输出它。

### 本机 DeepSeek 一键入口

在仓库所有者的 macOS 开发机上：

```bash
make deepseek-init
make deepseek-tui
make deepseek-vscode
```

这些 Target 会编译所需 Artifact、解析本机凭证、安装使用 Keychain 的配置，并启动
所选 Host。执行前阅读[本机 DeepSeek 一键配置与运行](./deepseek-local.md)。

## 6. 先用 Hermetic Fixture 验证

调用真实 Provider 前，先在无网络条件下验证 Runtime：

```bash
./bin/codehelper exec \
  --provider-fixture ./testdata/providers/openai \
  --provider openai \
  --model gpt-fixture \
  --workspace . \
  --output-format stream-json \
  "say hello"
```

Fixture 使用确定性的已记录响应，但会经过真实 Runtime 和事件流。

## 7. 启动真实会话

只读分析：

```bash
./bin/codehelper exec \
  --config ./codehelper.toml \
  --mode plan \
  --posture never \
  "解释架构并识别高风险模块"
```

交互编码：

```bash
./bin/codehelper tui \
  --config ./codehelper.toml \
  --workspace . \
  --mode act \
  --posture suggest \
  --enable-tools
```

带持久化状态的单次任务：

```bash
./bin/codehelper exec \
  --config ./codehelper.toml \
  --data-dir ./.codehelper \
  --workspace . \
  --enable-tools \
  --mode act \
  --posture suggest \
  "修复失败单测并验证结果"
```

恢复活跃 Thread：

```bash
./bin/codehelper exec \
  --config ./codehelper.toml \
  --data-dir ./.codehelper \
  --resume \
  "继续处理上一次结果"
```

## 8. 本地安装 VS Code 插件

开发流程：

```bash
make vscode-install
make vscode-check
make vscode-test
make vscode-build
```

构建、安装并完成当前 Host Target VSIX 的 Runtime Ready Handshake：

```bash
make vscode-package
```

`make vscode-package-universal` 仅用于静态 Universal Package；该产物不包含 Runtime
Executable。

macOS 还提供带固定 Provider 配置的一键脚本：

```bash
export DEEPSEEK_API_KEY='...'
make vscode-local-setup
unset DEEPSEEK_API_KEY
```

执行前请阅读 [VS Code 插件指南](./vscode.md)。脚本会安装到官方 VS Code，并把凭证
写入 macOS Keychain。

通用首次运行可从 Command Palette 执行 `CodeHelper: Setup Workspace`。Runtime 无法
启动时，Chat 失败面板和 `CodeHelper: Repair Runtime` 会直接展示结构化 Readiness
缺失项与修复动作，无需只依赖 Output Channel 排查。

## 9. 推荐的首次验证

```bash
make docs-check
make smoke
make test
```

`make verify` 是完整仓库门禁，部分测试依赖平台沙箱能力，明显重于首次验证。

## 10. 下一步

- 在[使用指南](./usage.md)中理解 Mode、Posture 和审批。
- 在[配置说明](./configuration.md)中设置预算、验证、上下文、Worker 和路由。
- 扩展 Runtime 前阅读[架构设计](./architecture.md)。
- 沙箱、Provider、状态和 VS Code 问题见[排障指南](./troubleshooting.md)。
