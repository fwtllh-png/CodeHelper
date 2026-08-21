# 排障指南

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
codehelper fleet inspect --data-dir ./.codehelper --id RUN_ID
```

检查 Run/Node/Attempt State、Executor、Lease Owner/Epoch/Expiry、Heartbeat、
Pending Effect、Attempt Count、`next_attempt_at`、Authority Digest 与 Worker
Budget。Live Lease 属于当前 Epoch，不能通过编辑数据库进行恢复。Fleet 是只读
Projection，不能 Resume 或 Settle Work。

## Extension State 未收敛

使用对应的 `plugin` 或 `skill` List Command，再通过 Runtime Client 或 VS Code
Extensions View 检查 Health 与 Receipt。重点核对 Source Identity、Trust、
Generation、Enabled State、Capability State 与最后一条 Operation Receipt。

客户端支持 Operation Identity 时，只使用同一 Identity 重试 Mutation。相同 Operation
ID 携带不同内容会被拒绝。不能通过修改 Staging Artifact 或 Durable Receipt Row
修复 Extension State；应通过 Control Plane 执行 Disable、Revoke、Verify、Rollback
或 Reinstall。

## Observation 或 OTLP 缺失

检查 Runtime Process Environment：

```bash
env | rg '^(CODEHELPER_OBSERVATION_CAPTURE|CODEHELPER_OTEL_EXPORTER|OTEL_EXPORTER_OTLP_)='
```

- 未设置 Capture 时默认为 `metadata`；
- `off` 会有意关闭所有记录；
- `metadata` 会有意省略 Raw Payload；
- `failure` 只为 Failure-like Observation 保留符合条件的 Payload；
- 无效 Capture Mode 会显式阻止 Runtime 构造；
- Remote OTLP Exporter 不可用时会回退 In-memory Projector。

Durable Observation Journal 位于所选 State Directory 下的
`observability/journal-v1`；修改 `--data-dir` 会改变该位置。Observation Payload
Retention 按时间管理，与 `state.event_retention` 不同。OTLP 缺失或 Observation
Drop 只影响 Observation Health，不改变权威 Turn Outcome。排查 Collector 时不能
重写 Runtime Event、Receipt 或 Terminal Envelope。

## VS Code Runtime 不可用

1. 执行 `CodeHelper: Show Status`。
2. 确认 Workspace Trust。
3. 检查 `codehelper.binarySource`。
4. 检查 Runtime Config Absolute Path。
5. 执行选中 Binary 的 `version --json`。
6. 与 `extensions/vscode/compatibility.json` 对比。
7. 从同一代码树重建 Runtime 与 Extension。

CodeHelper 只支持本地 `file:` Workspace。Remote SSH、Dev Container 和其他
`vscode-remote:` 环境会在 Activation 阶段被拒绝。

## 采集 VS Code Runtime 故障

复现前执行 `CodeHelper: Start Runtime Capture`，实测结束后执行
`CodeHelper: Stop Runtime Capture`。完成提示会给出 Extension 私有 Workspace
Storage 下的 JSONL 路径。

Capture 会关联全部 Live Protocol Event、Replay 标记、ACP Request 生命周期与 ID、
Runtime stderr、进程退出 Code 或 Signal、自动重启状态和 Session 同步错误。该功能
默认关闭；采集文件权限为 `0600`，因为 Model Output、Tool Arguments/Results 和
Diagnostics 可能包含敏感 Workspace 数据。分享前必须检查并脱敏。

该 VS Code Host Capture 与 Runtime Observation Journal 不同。Host Capture 覆盖 ACP
与 Process Supervision；Observation Journal 按 `CODEHELPER_OBSERVATION_CAPTURE`
记录 Runtime Evidence。

## 只在全仓并发测试中失败

部分 Fixture Lifecycle 与 Process Test 对资源敏感。单独重跑：

```bash
go test ./path/to/package -run TestName -count=1
```

单独通过时，应同时报告全仓和单独结果，并调查调度/资源压力，不能静默忽略全仓失败。

## 文档检查失败

`make docs-check` 会报告：

- 本地 Markdown Broken Link；
- 英文文档目录被重新引入；
- 指向已删除历史文档的链接。

应修复源文档或补充有内容的维护版本，不能只创建空文件满足检查。
