# 排障指南

简体中文 | [English](../en/troubleshooting.md)

## 首轮检查

修改配置前先收集事实：

```bash
codehelper version --json
codehelper doctor --json
codehelper config check --config ./codehelper.toml
codehelper config show --config ./codehelper.toml
codehelper sandbox status --json
git status --short
```

可复现报告应包含命令、脱敏后的 Config Provenance、平台、Terminal Event 和相关脱敏
日志，绝不能包含 Credential。

## 配置没有生效

现象：

- 使用了意外的 Provider/Model；
- Workspace 或 Timeout 与 TOML 不一致；
- 某个字段像是被忽略。

处理：

1. 运行 `config show` 检查 Provenance。
2. 检查 `CODEHELPER_*` 环境变量。
3. 检查最高优先级的命令行参数。
4. 确认 Host 使用的是同一个 Config Path。
5. 未知字段报错应修正，不能绕过。

## Provider 鉴权失败

1. 运行 `codehelper auth status --config ...`。
2. 确认 Reference Kind 和 Name，不检查或打印 Secret Value。
3. `env`：确保同一进程环境已 Export。
4. `file`：检查存在性、Owner 和限制性权限。
5. `keyring`：在非受限终端中执行，并检查系统 Keyring 权限。
6. 使用 `model resolve` 验证 Provider/Model。

不要为了“快速测试”改成 Inline Secret。

## Provider 或 Model 未知

```bash
codehelper model list
codehelper model resolve --provider PROVIDER --model MODEL --json
```

同一个 Model ID 可能属于多个 Provider，必须同时指定。自定义 Base URL 在无法安全推导
时还需要显式 Model Metadata/Capabilities。

## Tool 被拒绝

检查：

- Mode（`plan`、`act`、`operate`）；
- Posture（`never`、`suggest`、`auto`、`bypass`）；
- Workspace Trust/Permission；
- Approval Response；
- Repository Rule；
- Constitution Hold；
- 所需 Sandbox Strength。

`auto` 可能有意静默拒绝高风险 Tool。需要交互审批时使用 `suggest`。不能用 `bypass`
掩盖 Policy 设计问题。

## Sandbox 不可用

```bash
codehelper sandbox status
codehelper sandbox probe
codehelper doctor
```

平台能力属于环境条件。安装/配置支持的 Backend，或改为只读工作流。不能修改测试或
Policy，让缺少 Backend 的平台“看起来有强隔离”。

macOS 的受限内嵌终端可能影响 Seatbelt、Keychain 或 Application Directory。适合时
应在 Terminal/iTerm 复现。

## Verify 失败或不可用

1. 从 `config show` 查看 Verify Scope 与 Command。
2. 在同一 Workspace 手工运行该命令。
3. 确认 Sandbox 环境中存在依赖。
4. 建立确定性检查期间先使用 `soft`。
5. 只有希望失败后 Fail/Revert Turn 时才使用 `hard`。

Benchmark 中的 `sandbox_unavailable` 不是 Application Logic 失败证据，应明确报告为
环境前置条件失败。

## 持久化数据库无法打开

- 确认进程对 Data Directory 有读写权限。
- 确认只有支持当前预发布 Schema 的二进制访问。
- 检查磁盘空间和文件锁。
- 破坏性恢复前先保留目录。
- 使用新 Data Directory 区分数据损坏与 Runtime 配置问题。

Corruption Error 会显式报告，不应手工编辑 SQLite Bytes。Schema Reset 后，预发布开发
数据库可能需要重建。

## Session 无法恢复

检查：

- 是否使用相同 `--data-dir`；
- 是否存在 Active Thread（`thread list`）；
- 显式 `--thread-id` 是否覆盖 Resume Lookup；
- Workspace Identity 是否一致；
- 上一次 Terminal Event 是否已持久化。

修改 Metadata 前先使用 `thread read`。

## MCP Server 不健康

```bash
codehelper mcp validate --config ./mcp.json
codehelper mcp status --config ./mcp.json
codehelper mcp tools --config ./mcp.json
```

检查 Command Path、Environment Allowlist、Startup Timeout、Protocol Version、OAuth
Config 与 Circuit Breaker。单独测试一个 Server。

## Worker Task 卡住

```bash
codehelper worker list --data-dir ./.codehelper
codehelper automation list --data-dir ./.codehelper
```

检查 Task State、Executor、Lease Owner/Expiry、Heartbeat、Attempt、
`next_attempt_at` 与 Worker Budget。不能通过手改数据库把不可执行 Work-board Task
变成可执行任务。

## VS Code Runtime 不可用

1. 执行 `CodeHelper: Show Status`。
2. 确认 Workspace Trust。
3. 检查 `codehelper.binarySource`。
4. 检查 Runtime Config Absolute Path。
5. 执行选中 Binary 的 `version --json`。
6. 与 `extensions/vscode/compatibility.json` 对比。
7. 从同一代码树重建 Runtime 与 Extension。

Remote Workspace 在 Workspace Extension Host 中运行，本地 Binary Path 不会自动存在
于 Remote SSH 或 Container。

## 只在全仓并发测试中失败

部分 Fixture Lifecycle 与 Process Test 对资源敏感。单独重跑：

```bash
go test ./path/to/package -run TestName -count=1
```

单独通过时，应同时报告全仓和单独结果，并调查调度/资源压力，不能静默忽略全仓失败。

## 文档检查失败

`make docs-check` 会报告：

- 本地 Markdown Broken Link；
- 缺少中英文镜像；
- 指向已删除历史文档的链接。

应修复源文档或补充有内容的维护版本，不能只创建空文件满足检查。
