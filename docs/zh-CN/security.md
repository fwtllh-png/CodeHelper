# 安全模型与运维

## 安全目标

CodeHelper 会根据模型选择在源码上执行工具。目标不是让任意代码变得安全，而是让权限
显式、影响有界、凭证不进入模型可见状态，并让所有关键动作可检查。

## 威胁模型

以下输入都应视为不可信：

- 用户 Prompt 与粘贴内容；
- 仓库文件、生成代码、测试和构建脚本；
- 模型输出与 Tool Argument；
- Provider Response 与 Native Search Result；
- MCP Server、Plugin Package、Skill Content 与 Hook；
- HTTP/ACP Client Message；
- 从其他 Workspace 复制的持久化状态；
- Archive Path、Symlink、Environment 与 Process Output。

本地 Operator 和可信 Release Key 是 Authority Root，但 Operator 误操作和依赖被攻陷
仍在威胁范围内。

## 分层控制

| 层 | 作用 |
| --- | --- |
| Mode | 限制请求的工作类型 |
| Posture | 决定拒绝、审批或自动处理 |
| Workspace Permission | 把已记忆权限绑定到单一 Workspace |
| Constitution | 普通配置不能绕过的硬约束 |
| Tool Guard | Identity、Risk、Resource、Approval 与 Evidence 的统一决策 |
| Edit Journal | 记录 Before Image 与中断工作 |
| Verify Gate | Commit 前收集正确性证据 |
| OS Sandbox | 强制进程、文件系统和网络边界 |
| Egress Control | 约束远程 Endpoint 与出网 Client |
| Observability | 通过 Privacy、Retention 与有界 Export Policy 接收版本化证据 |

任何一层都不能被描述为另一层的替代品。

## Posture 建议

- `never`：首次检查仓库和不可信 Workspace 最安全。
- `suggest`：日常交互开发推荐。
- `auto`：适合已知 Policy 和确定性 Fixture；被拒绝的操作可能不弹审批。
- `bypass`：只用于有独立备份的隔离可信 Workspace。

`bypass` 仍受硬约束和 Sandbox Availability 限制。

## Workspace 与文件安全

- 相对配置 Workspace 解析并校验路径。
- 拒绝 Traversal、不安全 Symlink 和 Archive Escape。
- Tool Contract 要求时先读后写。
- 修改写入 Journal 并保持 Atomicity。
- 配置后，写入型 Subagent 使用 Worktree。
- 隔离 Worktree 仅可只读访问经过校验的自身 Git Administration Directory，以及
  Repository Common Git Directory 中必要的 Object、Ref 与配置路径；这不会授予
  Parent Worktree 或 Git Metadata 写权限。
- 使用 `apply --dry-run` 检查生成计划。
- 重要仓库必须纳入版本控制并维护备份。

## 进程执行

- Command 使用 Sanitized Environment。
- Working Directory 与 Executable Path 必须显式。
- 必须支持 Timeout、Cancel 和 Process Group Cleanup。
- PTY 与非 PTY 共享 Policy Boundary。
- `shell_read` 是检查类 Pipeline 的自动执行路径。Strong Sandbox 将 Workspace
  强制挂载为只读、禁用网络，只允许写入 Private Temporary Directory，并且绝不进行
  Unsandboxed Retry。
- 可增权的 Typed Sandbox Denial 可通过 Critical One-shot Approval 申请一个精确
  Path、Host/Port 或 Process Capability。重试使用递增 Revision 的 Permission
  Profile，并保持在同一 Strong Sandbox；Untyped 或重复 Denial 均 Fail Closed。
- macOS 上 `exec_command`、`quality_test` 与 `quality_verify` 的进程出口仅允许
  通过 Runtime-owned loopback proxy，并要求用 `network_targets` 显式声明 Host、
  Port、Protocol、传输 Method 和私网权限。HTTPS 目标必须使用 `CONNECT`，HTTP
  目标使用普通 HTTP Method。已声明的 Process Network Resource 会先于 Process
  Effect 被归类：`suggest` 必须经过人工 Network Approval，`auto` 才可以自动
  Review 精确的只读目标。Sandbox 只能连接代理端口，直连和未声明目标均 Fail
  Closed。该 Loopback Proxy 返回 CONNECT 403 表示目标未声明或未授权，并不表示
  远端服务不可达。Linux 在 namespace proxy bridge 交付前保持进程全禁网。
- 测试 Fixture 必须绑定并连接临时 Localhost 端口时，`quality_test` 与
  `quality_verify` 可声明 `allow_loopback`。该能力默认关闭，并单独触发 High-risk
  Network Approval。批准后 macOS Profile 只增加 Localhost Inbound/Outbound
  Seatbelt Rule；非 Loopback 流量仍必须声明精确 Proxy Target。Effective Profile
  与 Attempt Receipt 都会记录该 Loopback Grant。
- Linux Strong Sandbox 将 Landlock、`no_new_privs`、seccomp 与 `execve` 固定在
  同一个 OS Thread。Seccomp 拒绝 Tracing、跨进程内存访问、Namespace 创建、
  `clone3` 与 `io_uring`；Restricted Network Mode 只保留 AF_UNIX 进程内 IPC。
- Command Policy 使用 Bash AST 与 Static argv Segment。Managed Authority 定义
  Ceiling，Repository 只能收紧，User Approval 不能覆盖高权 Deny/Ask。Policy Reload
  原子发布新 Revision，并绑定到 Profile Provenance。
- 每次实际执行的 Tool Attempt 都记录准确的 Effective Permission Profile
  Revision/Digest、Enforcement Backend、Filesystem Root、Network Mode、Grant
  Provenance，以及 Typed Denial 或 One-shot Amendment。Amendment Receipt 将 Base
  Digest 与获批后的 Replacement Digest 绑定，重试不会覆盖前一次 Attempt 的证据。
  `tool.result.execution` 将这条证据链持久投影到 Runtime Event；对话历史重建只消费
  Tool Output，不把该审计字段送回 Model Context。
- `exec_command` 与 `write_stdin` 保留 Process Capability 和原有 Approval
  行为。`exec_command` 是唯一通用 Command Start 路径；`write_stdin` 在每次
  Session 交互前校验当前 Thread Lease。
- Process Tool 通过有界 Fair Budget 与精确 Resource Claim Admission。不同 Session
  与无关 Path 可并发，冲突 Claim 保持顺序。
- Cancellation Terminal Ownership 遵循声明的 Execution Disposition；Process
  Teardown 必须在释放 Consequential Claim 前终止并回收完整 Process Group。
- 缺少所需 Strong Sandbox 是失败，不是允许 Unsandboxed Execution。

## 凭证

配置只允许 Reference：

```toml
[credential]
kind = "env"
name = "OPENAI_API_KEY"
```

运维规则：

- 不提交 Secret Value；
- 不在 Prompt 或 Command Argument 中传 Secret；
- 桌面端优先 OS Keyring；
- CI 优先由 Secret Manager 注入 Environment；
- Secret File 使用限制性权限；
- 怀疑泄漏后立即 Rotation；
- 即使开启 Redaction，Log、Receipt、Crash Dump 与 Support Bundle 仍视为敏感。

运行：

```bash
make secret-leak-test
```

## 网络与服务暴露

- 服务默认监听 `127.0.0.1`。
- 非 Loopback 部署必须使用经过 Review 的认证网关。
- Provider Base URL 与 Redirect 属于安全敏感配置。
- Native/Web Search Result 仍是不可信内容。
- 不向不可信 Client 开放 Trusted Dynamic Tool Registration。
- 可记录 Endpoint Inventory，但不能记录 Credential。

## MCP、Plugin、Skill 与 Hook 供应链

### MCP

Review Executable、Argument、Environment Allowlist、OAuth Config 与 Endpoint，使用
Health Isolation 和有界 Timeout。

### Plugin

要求 Trusted Publisher、Signed Registry、Digest Verification、Immutable Staging、
Receipt 与 Revocation。Rollback 必须选择历史已验证 Artifact。

### Skill

锁定最终 Source/Version，并 Review Instruction/Resource。Skill 是 Agent 解释的内容，
存在 Prompt Injection 风险。

### Hook

Hook 应保持最小、有界、显式，不能成为绕过 Tool Policy 的隐藏路径。

## Log 与 Diagnostics

Redaction 降低意外泄漏，但不会让 Log 变成公开数据。应限制访问并设置 Retention。结构化
Error 在 Remote/Filesystem Error 可能含 Secret 时，不应原样输出。
Attempt Receipt 包含 Canonical Path 和 Network Target；即使其中没有 Credential
Value，也必须作为受限 Audit Record 处理。Runtime Capture 会包含
`tool.result.execution`，因此 Capture 与 Event Log 必须采用相同的访问控制和
Retention。

Durable Observation Router 会在任何 Observation Journal 或 CAS 写入前应用 Capture
Policy：

- `off`：不持久化 Observation；
- `metadata`：默认值，只保留脱敏 Summary；
- `failure`：为 Failure-like Observation 保留符合条件的脱敏 Payload；
- `full`：为 Trait 允许的 Observation Kind 保留符合条件的脱敏 Payload。

所有模式都禁止 Credential 与 Restricted Payload。Secret-bearing JSON Key、配置
Credential Value、State Root 与 Config Path 会在持久化前脱敏。Sensitive Payload
Reference 比 Audit Metadata 更早过期；只有 Reference Count 降为 0 后，CAS GC 才会
删除 Object。

OTLP Export 是脱敏 Observation Envelope 的独立有界 Projection。Metric Label 使用
固定低基数 Allowlist，绝不能包含 Prompt、Path、Argument、Resource ID 或 Raw Error。
Collector Endpoint 与 Header 是安全敏感环境配置。Exporter Failure 会反映在
Observation Health 中，但不能改变业务 Turn Result。

内部 Support Bundle Builder 会读取 Observation Journal，对所有 Summary/Payload
再次脱敏，默认不包含 Payload，以独占方式创建 Archive，并强制 mode `0600`。Bundle
仍是敏感数据，分享前必须人工检查。

## 安全测试

```bash
make security-test
make sandbox-attack-test
make secret-leak-test
make vscode-security
```

安全变更应覆盖：

- Allow；
- Deny；
- Malformed Input；
- Cancel 与 Cleanup；
- Concurrent Access；
- Redaction；
- Unsupported Platform。

## 事件处理

Secret 或 Signing Key 可能泄漏时：

1. 停止受影响 Runtime/Update Distribution；
2. Revoke 并 Rotate Credential/Key；
3. 使受影响 Plugin/Binary Artifact 失效；
4. 保留脱敏证据；
5. 检查 Event/Receipt/Log 影响范围；
6. 适用时发布更高 Sequence 的 Revocation Manifest；
7. 修复控制并增加 Regression Test；
8. 准确通知受影响版本与修复方法。

Workspace Integrity 不确定时，应停止执行，保留 State 与 Journal，检查 Git/Diff，并从
可信 Revision 或 Backup 恢复。

## 报告

公开报告中不能包含 Secret 或私有源码。应提供 Version、Platform、Command Shape、
Sanitized Config Provenance、预期/实际 Security Decision，以及可行时的可复现 Fixture。
