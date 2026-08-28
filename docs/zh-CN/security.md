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
- MCP Server、Skill Content 与 Hook；
- HTTP/Web Transport Client Message；
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
| Execution Authority | 将授权结果绑定为单次 Operation Lease，并校验 Generation 与 Controls |
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

Web 不接受 `bypass`；该内部 Posture 仅用于受控测试与隔离执行，并仍受硬约束和
Sandbox Availability 限制。

Web Markdown 不执行原始 HTML 或危险 URL。同源图片可以直接显示；跨域图片只有在
用户点击加载后才会请求，并且只允许 HTTPS、使用 `no-referrer`，避免模型输出静默
泄露页面来源或触发明文媒体请求。

## Workspace 与文件安全

- 相对配置 Workspace 解析并校验路径。
- 拒绝 Traversal、不安全 Symlink 和 Archive Escape。
- Durable Workspace Journal、Process Job Journal 和 Job Log 位于
  `<data-dir>/workspaces/<workspace-id>/control`，不再从 Workspace 内的旧
  `.codehelper/journal` 恢复。旧目录只产生诊断提示。
- Workspace State 分为互不重叠的 `control`、`sandbox-home` 和 `artifacts`；
  在这三个状态域中，Sandbox 只获得 `sandbox-home` 写权限。
- Tool Contract 要求时先读后写。
- Tool Catalog 将模型可见的 `ExternalDescriptor` 与 Registry 可信的
  `TrustedBinding` 分开冻结。MCP、Dynamic Tool 等外部来源只能提交
  Requested Effects；Capability、Resource Resolver、Access、Sandbox、Effect、
  Required Controls、Journal 和验证证据资格由可信 Binding 决定。
- Guard、Policy 和 Authority 不从工具名或 External Requested Effects 推导授权。
  Deferred Loader 改变 Trusted Binding、Schema 或 Alias 会 Fail Closed；替换 Binding
  会更换 Revision/Authority，使采样时的旧 Catalog Binding 失效。
- Trusted Binding 和 ExecutionOperation 使用十维 Required Controls：
  Filesystem Read/Write、Network、Process Tree、Cross Process、Syscall、IPC、
  Path Identity、Artifact Origin 与 Durable Recovery。Sandbox Probe、Policy 和具体
  Command 共同产生 Effective Controls，Lease 只在每个要求都被满足时签发。
- Backend 完成 `Prepare` 后，Process Owner 再次核对本次命令的 Prepared Controls。
  旧 `Strength` 能力与 Receipt 字段已删除，不能单独证明或授予执行权限。
- 副作用 Inventory 同时检查系统进程 API 和 `internal/platform/process` 的构造入口；
  新调用方必须登记为 Guard 授权执行、Lease-consuming Broker 或可信 Runtime/Host Owner。
- Web 分支切换不直接执行 `git switch`，由 VCS Broker 校验固定参数、仓库身份和
  Execution Lease；未注入 Broker 时 fail closed。
- `file_write`、`file_edit`、`file_apply`、`file_patch`、`integrate_agent`、隔离
  Chat Merge 和 `document_convert` 的最终 Workspace 输出统一生成不可变 File Plan。
  Guard 或 Runtime Authority 签发绑定 Plan Digest、Workspace Generation 和精确
  Path Resource 的单次 Lease，File Broker 是提交这些 Plan 的唯一 Owner。
- File Broker 在 Journal Before Image 后再次校验文件内容、身份和父目录，使用
  descriptor-relative API 先写后删。写入、最终快照或 Journal Settlement 失败时
  逆序恢复；恢复冲突或失败明确报告 Partial Change，不伪造原子成功。
- File Broker 拒绝 Symlink、Hardlink、Device Boundary、Root/Parent Replacement，
  并在自身边界拒绝 `.git`、`.codehelper`、`.codehelper-worktree`、`.agents` 和
  `.codex`。Unified Diff 先解析为 File Plan，不调用 `git apply` 修改 Workspace。
- `exec_command` 的写权限只授予显式 `write_paths`。目标可以是现有普通文件，或位于
  已存在父目录下的待创建文件；Guard 在执行前完成 Preflight，Strong Sandbox 只为
  这些精确路径物化最小占位。目录、Symlink、重复路径和执行前发生的身份漂移均拒绝。
- 配置后，写入型 Subagent 使用 Worktree。
- 隔离 Worktree 仅可只读访问经过校验的自身 Git Administration Directory，以及
  Repository Common Git Directory 中必要的 Object、Ref 与配置路径；这不会授予
  Parent Worktree 或 Git Metadata 写权限。
- Git Worktree Registration、Index 和 Ref 不属于普通 Workspace 文件。Child
  Worktree Add/Remove/Prune 与 Chat Baseline `add`/`commit` 只能由 VCS Broker
  执行。每次白名单 Mutation 绑定 Common Git Directory Identity、目标 Worktree
  HEAD/Ref、Index Digest 和 Worktree Registration Digest；执行接管前发生漂移即拒绝。
- 使用 `apply --dry-run` 检查生成计划。
- 重要仓库必须纳入版本控制并维护备份。

## 进程执行

- Guard 在现有 Policy、Permission Hook 和人工审批完成后，把冻结的 Tool Invocation
  规范化为 `ExecutionOperation`。Operation 绑定 Workspace/Subject Generation、
  Resource Namespace、Effect Contract、Required Controls、参数摘要和 Artifact
  Provenance；资源排序、去重后计算稳定 Digest。
- 每次实际 Attempt 使用共享 `LeaseAuthority` 签发并消费一个不可伪造、单次使用的
  Execution Lease。Lease 绑定 Operation Digest、Permission Profile Digest、Policy
  Revision、Sandbox Policy、Workspace/Subject Generation、Artifact Digest 和 Attempt。
  过期、撤销、重复消费或任一 Generation 漂移都会 Fail Closed。
- `execution.lease_timeout` 是授权到执行接管之间的显式配置上限；更早的调用 Context
  Deadline 会收紧它。Lease 消费后，运行中资源的回收不受 Lease 到期影响。
- Attempt Receipt 持久记录 Operation Digest、Lease ID/State、Effect、Workspace、
  Subject、Policy 和 Sandbox 绑定。当前兼容 Facade 保持原有 Policy Decision、
  Approval Scope、Typed Denial 与 Amendment 语义不变。
- Artifact Broker 只接受 Workspace 或 Sandbox Home 内的常规可执行文件，拒绝
  Symlink、Hardlink、特殊文件与 Device Boundary 变化，并复制到 Broker-only
  Artifact Staging。复制前后复核源身份，Manifest 绑定 Workspace Generation、
  Producer Operation 与内容摘要。
- Process Broker 验证 Artifact Manifest 和最终 Operation，单次消费 Execution Lease，
  签发绑定 Session/Thread/Turn 与 Process Generation 的 Process Handle，并独占
  Start、Cancel、Wait、Reap 和 Settlement。Runner Failure 与提前退出分别记录为
  `runner_failure` 和 `command_exited_early`。
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
- 测试 Fixture 或本地开发服务必须绑定并连接临时 Localhost 端口时，
  `exec_command`、`quality_test` 与 `quality_verify` 可声明 `allow_loopback`。
  该能力默认关闭；Strong Sandbox 内仅包含精确 Localhost Grant 且没有 Workspace
  写入的调用按有界 Network Read 评估，`suggest` 要求审批，`auto` 自动 Review。
  macOS Profile 只增加 Localhost Inbound/Outbound Seatbelt Rule；非 Loopback
  流量仍必须声明精确 Proxy Target。Effective Profile 与 Attempt Receipt 都会记录
  该 Loopback Grant。
- `quality_test`、`quality_diagnostics`、`quality_review` 和 `quality_verify`
  使用 POSIX `set -e` 的 Fail-fast 语义，不能由尾部日志命令覆盖前序检查的非零
  退出码。需要有意接受失败时必须在 Command 中显式表达。
- `quality_process_smoke` 仅在持久化 Workspace State 可提供 Artifact Staging 和
  Process Broker 时开放。原始 Workspace 或 Sandbox Home 路径只作为 Snapshot 输入，
  实际进程只能从 Broker-owned Snapshot 启动；Guard 强制 `ApprovalOnce`、禁止
  Permission Hook 自动批准，且不提供 Session/Always Grant。
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
- 即使开启 Redaction，Log、Receipt、Crash Dump 与导出的诊断材料仍视为敏感。

Web 写入凭证时会创建 Workspace/Provider 隔离的新 Keyring Entry，并以不含 Secret 的
`prepared`、`config_committed`、`completed` Intent 和 Generation CAS 提交 Reference。
切换后当前 Runtime 继续使用 Turn 已冻结的旧 Route，页面显示需要重启；下次启动会清理
未提交的新 Orphan，并只在扫描 data-dir 内全部托管 Reference 后删除无引用的旧托管
Entry。用户自定义 Keyring Name 无法完成全局引用证明，因此不会被自动删除。

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

## MCP、Skill 与 Hook 供应链

### MCP

Review Executable、Argument、Environment Allowlist、OAuth Config 与 Endpoint，使用
Health Isolation 和有界 Timeout。stdio MCP 默认关闭；启用时配置必须来自外部 State
Directory，并显式声明 `host_trusted=true`。该标记会
进入 Tool Catalog 描述和 Tool Result Metadata，并只允许 Runtime 创建 Lifecycle
Operation，不直接授予进程启动能力。Server 配置摘要和每次启动的 Generation 进入
Subject；Process Broker 消费单次 Lease 并签发绑定 Workspace/Server/Generation 的
Handle。Reload、Disable、Crash 和 Shutdown 会终结 Handle、Settlement 并释放 Lease。

### Skill

锁定最终 Source/Version，并 Review Instruction/Resource。Skill 是 Agent 解释的内容，
存在 Prompt Injection 风险。

### Hook

Workspace 中的默认 Hook 配置不自动加载；Repository Hook 必须由 Operator 显式指定。
所有 Hook 进程使用 Workspace Read-only、Network Denied 的 Strong Sandbox，并隐藏
`.git`、`.codehelper`、`.codehelper-worktree`、`.agents` 和 `.codex`。每次调用先
生成绑定 Hook 配置、Event、Workspace Generation 和 Process Spec 的 Operation，再由
Process Broker 消费短生命周期 Lease，并执行 Start、Cancel、Wait、Reap 和 Settlement。
Repository Permission Hook 只能返回 Deny 或 Ask，不能把 Guard 的决定提升为 Allow。

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
Collector Endpoint 与 Header 是安全敏感环境配置。Exporter Failure 会通过
`Flush`/`Shutdown` 返回，但不能改变业务 Turn Result。

导出 Observation Journal 诊断材料前必须再次经过 Privacy Policy，默认不包含
Payload，并以独占方式创建 mode `0600` 的文件。导出结果仍是敏感数据，分享前必须
人工检查。

## 安全测试

```bash
make security-side-effect-check
make security-test
make sandbox-attack-test
make secret-leak-test
make web-build
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
3. 使受影响 Binary Artifact 失效；
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

[安全执行边界重构方案](./security-execution-boundary-refactoring-plan.md)的阶段 0-6
已交付 State Domain、Operation/Lease、Artifact/Process/File/VCS Broker、Process
Smoke、Hook、stdio MCP Lifecycle、Workspace Write、Git Metadata Mutation 收口，以及
External Descriptor/Trusted Binding 分离和 Required/Effective Controls 能力矩阵。
后续演进必须继续通过同一 Operation、Lease、Broker 和矩阵契约扩展，不能恢复旁路。
