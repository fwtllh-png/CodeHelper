# 安全模型与运维

简体中文 | [English](../en/security.md)

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
| Observability | 记录脱敏 Event、Receipt、Usage 与 Trace |

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
