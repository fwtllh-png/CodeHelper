# 安全执行边界重构方案

> 状态：阶段 0-1 已交付；阶段 2-6 为提案。
>
> 本文描述 CodeHelper 对副作用执行边界的目标设计和渐进迁移方案，不代表当前实现已经
> 完成这些约束。当前已交付行为以[安全模型](./security.md)、源码和测试为准。

## 一、目标

CodeHelper 已具备 Tool Guard、Policy、Approval、Workspace Journal、Effective
Permission Profile、OS Sandbox、Egress Gate 和执行 Receipt。下一阶段不应推倒这些
能力重写，而应把仍然分散在 Hook、MCP、宿主工具和平台包中的副作用入口逐条收口。

目标执行链路是：

```text
不可信输入触发的外部副作用
    -> Operation
    -> Guard / Authority
    -> Execution Lease
    -> Process / File / Network / Artifact Broker
    -> Settlement
    -> Receipt
```

完成后，Tool、Hook、MCP、Plugin、Worker 和 Host Smoke 只描述执行意图，不能自行启动
外部进程、修改 Workspace、发布可执行 Artifact 或访问网络。Broker 是受控副作用的唯一
执行边界。

这里的“副作用”特指由用户、模型、Repository、Plugin、MCP 或其他不可信来源触发的
外部行为。Runtime 自身写入 SQLite、Event、Journal、CAS 和 Receipt 属于可信控制面
持久化，仍由各自 Owner 管理，不能递归经过 Guard。

## 二、背景与判断

当前架构的主要优势应继续保留：

- 所有内置 consequential tool 已经过统一 Tool Guard；
- Authority Profile 把 Policy、Approval、Resource 和 Sandbox Policy 编译为带摘要的
  单次执行权限；
- Workspace 写入支持精确路径、Read-before-write、Before Image 和冲突检查；
- macOS 与 Linux 提供 Strong Sandbox，网络访问可以通过受控代理或精确 Loopback
  Grant；
- Sandbox Denial、Permission Amendment 和 Attempt Receipt 已具有结构化表达；
- Workspace 文件操作使用 descriptor-relative API 抵抗 Symlink、Hardlink 和根目录
  替换。

当前主要结构性问题不是 Guard 能力不足，而是 Guard 只天然覆盖 Tool 调用。以下路径仍
可能直接拥有底层执行能力：

- Hook Executor；
- stdio MCP Server 生命周期；
- Desktop/GUI Process Smoke；
- Orchestration Lane 和部分 Host 平台集成；
- 未来新增的 Plugin、Worker 或本机集成。

如果继续在每个调用点分别补权限判断，会形成多套不一致的授权、取消、清理和审计语义。
因此需要保留现有 Guard 决策能力，同时把它提升为通用 Operation Authorizer，并建立唯一
Broker。

### 2.1 长 Session 实测对方案的修订依据

Agent Trace Studio 长 Session 暴露了以下并非单点 Tool Bug 的边界问题：

- 每 Turn 临时 Home 导致 Rust、Node 等工具链和构建缓存重复安装；改为 Workspace
  持久 Home 后，又形成了可被不可信进程跨 Turn 修改的新数据面；
- 只暴露可执行文件或安装目录不足以运行宿主工具链，Node 等程序还依赖递归 Mach-O
  Dynamic Library、RPATH 和精确配置文件；
- Strong Sandbox 无法提供 WindowServer，GUI Smoke 必须进入宿主执行边界，但原始
  构建产物位于可写 Sandbox Home；
- Path-only Approval 和执行前 `Lstat` 不能消除批准后、启动前替换 executable 或
  `.app` Bundle Resource 的竞争；
- 路径解析或进程启动发生在验证执行前时，失败曾绕过 Verification Evidence，后续普通
  `quality_verify` 因而可以错误完成；
- Pause、Continue、进程重启和 retained draft 要求 Journal 明确区分 Suspend、
  Resume、Rollback 与 Commit，不能只依赖进程内 Owner；
- `.git` 被正确排除在普通 Workspace 写权限外，但 Agent 仍可能把 Commit 当作完成步骤，
  形成无法成功且反复重试的操作；
- `exec_command`、质量工具和 Hook 曾分别解释 Loopback、Shell Exit、Toolchain 与
  Sandbox Denial，证明按调用点修补会持续产生语义漂移。

因此本方案不再把“引入通用 Broker”视为所有止血项完成后的远期工作。存储域、
Artifact Promotion、Process Handle 和验证失败 Settlement 必须先形成最小闭环，
Desktop Process Smoke 应作为首个迁移对象。

## 三、范围与非目标

### 3.1 范围

- 由不可信输入触发的进程创建、文件修改和网络访问；
- Hook、MCP、Plugin、Tool、Worker 与 Host Smoke 的 Subject Trust；
- Execution Lease 的签发、消费、撤销和审计；
- Sandbox 后端能力探测和逐项控制匹配；
- Workspace Journal 的控制面隔离；
- Runtime Control State、Sandbox Home 和可执行 Artifact 的存储域隔离；
- 构建产物从不可信可写目录进入宿主执行边界时的来源证明和不可变快照；
- 长进程启动、观察、输入、信号、取消和回收的统一生命周期；
- 生产代码直接调用系统副作用 API 的静态门禁。

### 3.2 非目标

- 不重写 Turn Kernel、Operation/Event Runtime 或 WorkGraph；
- 不移除现有 `Guard.ExecuteBound`，迁移期间将其保留为兼容 Facade；
- 不把内部数据库、Event、Observation 或 Receipt 写入纳入 Tool Permission；
- 不用粗粒度 `workspace-write` 替代精确文件资源授权；
- 不允许一次审批把后续执行降级为 Unsandboxed；
- 不为未发布版本设计旧 Workspace Journal 的复杂兼容迁移。

## 四、安全不变量

以下不变量在对应阶段交付时同步进入 `security.md`、Architecture Test 和相关 Package
Test。本文中的目标声明不能替代自动化证明。

### 4.1 外部进程必须持有 Lease

未经 Authority 签发且 Broker 验证通过的 Execution Lease，任何不可信输入触发的路径
都不能启动外部进程。生产代码中的 `exec.Command`、`exec.CommandContext`、
`os.StartProcess`、`syscall.Exec` 和平台等价 API 只能存在于：

- Process Broker 实现；
- Broker 自有可信 Helper；
- 明确列入静态门禁 Allowlist 的纯宿主启动器；
- Test Fixture。

### 4.2 不可信来源只能收紧权限

Repository、Plugin、MCP 和动态工具提交的是 Requested Effects，不是最终权限。
Repository Hook 和 Permission Hook 可以返回 Deny 或 Ask，但不能把 Guard 的 Deny/Ask
提升为 Allow。最终 Capability、Effect、Required Controls 和 Resource Binding 由可信
注册层决定。

### 4.3 Workspace 不能承载安全控制状态

Workspace 内容全部视为不可信。以下状态必须位于 Runtime State Directory：

- Workspace Journal Ledger 和 Before Image；
- Approval、Lease 和 Attempt 状态；
- Plugin Trust、Digest、Revocation 和安装记录；
- MCP Lifecycle Grant；
- Broker Receipt 和恢复状态。

Workspace 中可以存在供人阅读的配置请求，但不能仅凭该文件扩大权限。

### 4.4 Capability 与 Effect 由可信绑定确定

扩展只能声明希望执行的操作。可信 Binding Factory 根据来源、注册渠道和 Operator
Policy 生成最终约束。任何 Descriptor 自报都不能单独证明：

- Read-only；
- 无进程；
- 无网络；
- 可逆写入；
- 已 Journal；
- 已由 Strong Sandbox 覆盖。

### 4.5 Required Controls 必须逐项满足

`Strong` 只能作为 UI 或兼容层的派生摘要，不能继续作为内部授权依据。执行前必须证明：

```text
RequiredControls is satisfied by EffectiveControls
```

控制项需要分别表达文件读取、文件写入、网络、进程树、跨进程访问、系统调用、IPC 和
路径身份安全。不同控制值可能不可比较，不能用一个整数等级代替集合判断。

### 4.6 可写数据面不能成为控制面或可信执行来源

Runtime State Directory 必须进一步划分为互不重叠的存储域：

```text
ControlStateRoot
    Journal / Lease / Approval / Receipt / Trust Record

SandboxHomeRoot
    Tool Cache / Package Cache / Build Output / Temporary User State

ArtifactStagingRoot
    Broker-owned Immutable Execution Snapshot
```

`ControlStateRoot` 不能映射为 Sandbox 可写目录。`SandboxHomeRoot` 虽然位于 Runtime
管理目录且可以跨 Turn 持久化，但其内容由不可信 Workspace 代码和工具共同产生，必须
始终按不可信数据处理。宿主进程不得仅因路径位于 `SandboxHomeRoot` 就直接执行其中的
文件。

需要进入宿主执行边界的构建产物必须先由 Artifact Broker 复制到
`ArtifactStagingRoot`，生成完整 Manifest，并将来源 Workspace、Mutation Revision、
生产 Operation、文件摘要和目录结构绑定到后续 Lease。控制状态、Sandbox Home、
Artifact Staging 和 Workspace 必须执行双向 Non-overlap 校验。

### 4.7 失败也是必须持久化的安全事实

路径解析、身份校验、Sandbox Prepare、进程启动和 Teardown 发生在不同阶段。任何阶段
失败都必须形成 Settlement；验证型 Operation 还必须形成对应 Kind 的失败 Evidence。
不能因为失败发生在 Executor 启动前就绕过 Evidence Ledger，也不能由其他 Kind 的成功
Evidence 覆盖。

## 五、信任模型

### 5.1 Subject

每个 Operation 必须绑定发起主体：

```go
type Subject struct {
    Kind       SubjectKind
    ID         string
    Trust      TrustLevel
    Digest     string
    Generation uint64
}
```

建议的 Subject Kind：

- `builtin`
- `repository_hook`
- `plugin`
- `mcp_server`
- `mcp_tool`
- `workflow`
- `worker`
- `host`

`ID` 只用于定位；授权必须同时绑定不可变 Digest 和 Generation。Plugin 更新、Hook
配置变化或 MCP Server 重启后，旧 Lease 自动失效。

### 5.2 外部声明与可信绑定

Descriptor 拆为两层：

```go
type ExternalDescriptor struct {
    Name        string
    Description string
    Schema      JSONSchema
    Requested   RequestedEffects
}

type TrustedBinding struct {
    Subject     Subject
    Capability Capability
    Effects     EffectContract
    Required    RequiredControls
}
```

外部来源只能产生 `ExternalDescriptor`。Builtin Registry、受信 Publisher Policy 或
显式 Operator 配置负责产生 `TrustedBinding`。

可信绑定至少执行以下跨字段校验：

- Process Resource 必须对应 Process 或 Plugin Capability；
- Host/URL Resource 必须对应 Network、Process 或 Plugin Capability；
- Write Resource 不能绑定 Read-only Capability；
- Journaled Write 必须绑定 Workspace Transaction；
- 外部来源不能自行选择 `SandboxNone`；
- Required Controls 非空时必须存在可证明满足要求的 Backend。

## 六、Operation

Operation 是授权输入，不是 Runtime 的用户请求协议，也不替代现有
`runtime/protocol.Operation`。为避免重名，代码落地时建议使用
`authority.ExecutionOperation`：

```go
type ExecutionOperation struct {
    ID          string
    WorkspaceID string
    WorkspaceGeneration uint64
    Subject     Subject
    Effect      EffectContract
    Resources   []Resource
    Process     *ProcessIntent
    Network     *NetworkIntent
    File        *FileIntent
    Artifact    *ArtifactIntent
}
```

Operation 必须规范化后再计算摘要：

- 路径不能继续隐含为 Workspace Path，而要解析为
  `namespace + root_id + relative_path + root_generation + file_identity`；
- Namespace 至少区分 `workspace`、`sandbox_home`、`broker_artifact`、
  `host_toolchain` 和 `control_state`；
- Workspace Path 使用 Workspace Resolver；Sandbox Home 和 Artifact Path 使用各自
  Root Resolver，不能回退为任意宿主绝对路径；
- URL 拆为 protocol、host、port 和 method；
- argv 保留元素边界，不能先拼成 Shell String；
- Environment 只保留允许传给 Broker 的名称和值摘要；
- Resource 排序和去重规则固定；
- 未知字段、未知枚举和冲突字段拒绝。

`EffectContract` 替代按工具名判断的 `mediatedFileWriter(toolName)`：

```go
type EffectContract struct {
    Kind                   EffectKind
    Reversibility          Reversibility
    Risk                   RiskLevel
    WorkspaceTransaction   WorkspaceTransaction
    RequireReadBeforeWrite bool
}
```

## 七、Execution Lease

### 7.1 数据

```go
type ExecutionLease struct {
    operationDigest  string
    workspaceID      string
    workspaceEpoch   uint64
    subjectDigest    string
    policyRevision   uint64
    sandboxPolicyID  string
    resourceBindings []ResourceBinding
    artifactDigest   string
    profile          EffectivePermissionProfile
    attempt          uint64
    nonce            string
    expiresAt        time.Time
}
```

字段保持私有。只有 `internal/security/authority` 可以创建 Lease；调用方只能通过只读
方法获得必要信息。Broker 必须调用 Authority Validator，而不能直接信任字段。

如果 Lease 需要跨进程传递，应使用 Runtime 临时密钥生成 MAC，并绑定完整 Operation
Digest。单进程内不需要引入签名开销，可以由 Authority Registry 保存 nonce 状态。

### 7.2 生命周期

Lease 状态至少包括：

```text
issued -> consumed -> settled
       \-> expired
       \-> revoked
```

约束：

- 默认单次消费；
- Attempt Revision 必须单调递增；
- Amendment 必须引用前一 Profile Digest；
- Policy、Workspace 或 Subject Generation 变化后拒绝消费；
- Resource Root、Sandbox Home 或 Artifact Generation 变化后拒绝消费；
- Expiry 是公开配置或调用协议的一部分，不能使用隐藏固定阈值；
- 已启动进程的取消由 Broker 接管，不能通过 Lease 过期放弃回收；
- Settlement 和 Receipt 完成后才能释放 consequential resource claim。

启动型 Lease 被消费后，由 Broker 签发更窄的 `ProcessHandleCapability`。该 Capability
只允许对一个已启动 Process Group 执行声明过的 `observe`、`stdin`、`signal`、
`wait` 或 `cancel`，并绑定 Session、Turn、Operation、Process Identity 和
Generation。Terminal 操作必须幂等；旧 Turn、旧 Generation 或已完成 Process 的 Handle
不能复用。

### 7.3 兼容入口

迁移期间保留：

```text
Guard.ExecuteBound(toolCall)
    -> resolve trusted binding
    -> build ExecutionOperation
    -> authorize
    -> issue ExecutionLease
    -> dispatch to Broker
```

旧入口只允许委托到新链路，不能继续保留独立执行实现。新增 Hook、MCP、Smoke 或 Tool
不得接入旧旁路。

## 八、Broker

### 8.1 Process Broker

```go
type ProcessBroker interface {
    Start(
        context.Context,
        authority.ExecutionLease,
        ProcessSpec,
    ) (*Process, ProcessReceipt, error)
}
```

Broker 在启动前验证：

- Lease 尚未消费、撤销或过期；
- Operation Digest、Attempt 和 Policy Revision 一致；
- executable、argv、cwd 和 Environment 与 Process Intent 一致；
- executable 的路径、device/inode、摘要及可用时的平台签名；
- executable 来自 `host_toolchain` 时，验证冻结的 Runtime Dependency Manifest；
- executable 来自 `sandbox_home` 时，要求 Artifact Broker 产生的不可变快照和
  Provenance，禁止直接执行原始可写路径；
- Workspace Read/Write Scope；
- Network Scope；
- Required Controls 被当前 Backend 的 Effective Controls 满足；
- Sandbox Prepared Policy ID 和 Authority Digest 一致。

启动后由 Broker 独占：

- Process Group；
- Timeout 和 Cancel；
- stdout/stderr 有界采集；
- Sandbox Runner 故障归因；
- Kill、Wait 和 Reap；
- Settlement 与 Receipt。

Shell Script 与 argv Process 必须是不同的 Process Intent。Shell Intent 绑定
Interpreter、Script Bytes Digest、工作目录和失败传播语义；不能依赖尾部命令的退出码
代表整段验证成功。优先使用结构化 argv，只有确实需要 Shell 语义时才使用 Shell Intent。

### 8.2 File Broker

File Broker 负责所有由不可信输入触发的 Workspace 修改：

- descriptor-relative open/create/rename/delete；
- Symlink、Hardlink、Device 和 Root Identity 检查；
- Read-before-write；
- Before Image；
- Expected Fingerprint；
- Atomic Publication；
- Change Receipt。

已有 `sandbox.Workspace`、`workspacejournal.Manager` 和文件 Tool 实现应逐步下沉到该
Broker，而不是重新实现。

### 8.3 Network Broker

Network Broker 统一处理：

- HTTP Client；
- Process Managed Proxy；
- Loopback；
- DNS 解析与私网地址判断；
- Redirect；
- Method、Protocol、Host 和 Port；
- Connection Receipt。

进程网络仍通过 OS Sandbox 限制到 Runtime-owned Proxy。应用层 Gate 不能替代 OS
Sandbox，OS Sandbox 也不能替代请求级 Target 校验。

Loopback 必须进一步区分 `bind` 和 `connect`，绑定地址族、Runtime 分配端口、服务
Owner、Process Handle 和生命周期。只声明 `loopback=true` 不能代表任意本机网络权限；
进程结束或 Lease 撤销后必须同时撤销端口 Grant。

### 8.4 Artifact Broker

Artifact Broker 负责把不可信可写目录中的构建产物提升为可供宿主执行或发布的不可变
快照：

- 输入只能来自绑定 Workspace 或 Sandbox Home Namespace；
- 递归拒绝 Symlink、Hardlink、Device Boundary 和特殊文件；
- 生成目录 Manifest、文件摘要、可执行位和平台签名事实；
- 绑定生产 Operation、Workspace Digest、Mutation Revision 和验证证据；
- 复制到 Broker-only `ArtifactStagingRoot` 后重新校验；
- 后续 Process/Release Lease 只引用 Artifact ID 和 Manifest Digest，不重新接受原始路径。

构建缓存可以跨 Turn 复用，但不能作为 Artifact Provenance。缓存命中只影响性能，不提高
信任等级。

### 8.5 Settlement

Sandbox Prepare 成功不等于命令已经在正确边界内执行。Broker 在进程结束后必须区分：

- `command_succeeded`
- `command_failed`
- `sandbox_denied`
- `sandbox_runner_failed`
- `sandbox_enforcement_degraded`
- `canceled`
- `teardown_failed`

自有 Helper 优先使用结构化控制通道或保留退出码报告 runner 状态。对于
`sandbox-exec` 等无法提供结构化通道的平台工具，只能使用后端专属、严格限定的
classifier；普通命令输出中的通用 `"Operation not permitted"` 不能自动转成可增权
Denial。

Settlement 必须在成功和失败路径都包含 Operation、Lease、Attempt、Resource Binding、
Effective Controls 和 Artifact Digest。验证型 Operation 的 Settlement 还要生成
`EvidenceContract` 指定的 Kind、Coverage、Mutation Revision 和 Command/Artifact
Digest。只有同 Kind、同 Revision 且覆盖要求相容的后续成功 Evidence 才能解除失败。

## 九、控制矩阵

建议使用离散能力而不是单一强度：

```go
type Controls struct {
    FilesystemRead  FilesystemReadControl
    FilesystemWrite FilesystemWriteControl
    Network         NetworkControl
    ProcessTree     ProcessTreeControl
    CrossProcess    CrossProcessControl
    Syscall         SyscallControl
    IPC             IPCControl
    PathIdentity    PathIdentityControl
    ArtifactOrigin  ArtifactOriginControl
    DurableRecovery DurableRecoveryControl
}
```

示例值：

| 维度 | 示例 |
| --- | --- |
| FilesystemRead | `unrestricted`、`declared_roots`、`exact_paths` |
| FilesystemWrite | `denied`、`exact_paths`、`workspace_tree`、`unrestricted` |
| Network | `denied`、`loopback_exact`、`proxy_targets`、`direct` |
| ProcessTree | `unmanaged`、`group_kill`、`job_object`、`pid_namespace` |
| CrossProcess | `unrestricted`、`restricted`、`isolated` |
| Syscall | `unrestricted`、`deny_dangerous`、`allowlist` |
| IPC | `unrestricted`、`unix_only`、`private_namespace` |
| PathIdentity | `lexical`、`canonical`、`descriptor_relative` |
| ArtifactOrigin | `unverified_path`、`verified_manifest`、`broker_snapshot` |
| DurableRecovery | `memory_only`、`external_journal`、`resumable_transaction` |

每个取值需要定义满足关系；不同维度和不可比较值不能通过整数排序。Backend Probe 产生
`EffectiveControls`，Trusted Binding 声明 `RequiredControls`，Authority 执行逐项集合
判断。兼容字段 `Strength` 由矩阵派生，仅用于 UI、诊断和旧协议。

## 十、Workspace Journal

### 10.1 存储位置

目标位置：

```text
<data-dir>/workspaces/<workspace-id>/journal/
```

`workspace-id` 使用 Workspace Registry 的持久 ID。Journal Owner Record 额外绑定：

- canonical workspace root；
- Workspace generation；
- 启动时 root device/inode 或平台等价身份；
- Journal schema version。

设备和 inode 用于检测身份变化，不单独作为稳定 Workspace ID。
Sandbox Home、Journal、Job Log 和 Child Runtime State 必须统一从同一个
`WorkspaceIdentity.RootID` 派生，不能分别使用 Editor URI Hash、Canonical Path Hash
或调用点自建 ID。Child Workspace 使用父 Workspace ID、Child ID 和 Generation
共同派生。

### 10.2 路径安全

Ledger 中的路径只能是规范化 Workspace-relative path。恢复时：

1. 通过已固定的 Workspace Root Handle 打开路径；
2. 逐组件拒绝 Symlink/Reparse Point；
3. 拒绝 Device Boundary；
4. 拒绝 Multiply-linked Regular File；
5. 校验 Before/After Fingerprint；
6. 通过 descriptor-relative API 执行恢复。

禁止直接对 Ledger 提供的绝对路径调用 `os.Remove`、`os.Rename` 或 `os.WriteFile`。

Journal Transaction 生命周期必须显式表达：

```text
open -> suspended -> resumed -> committed
                 \-> rolled_back -> settled
```

`Pause` 只能进入 `suspended`，不能隐式 Rollback。恢复必须绑定原 Session、Turn、
Workspace Generation 和授权动作；普通新 Turn 不能接管 retained draft。配置要求
Durable Journal 但外部 State Root 不可用时必须 Fail Closed，不能静默降级为内存
Journal。

项目仍处于 pre-release，切换后不读取旧 `.codehelper/journal`。如果发现旧目录，只返回
不包含内容的诊断提示，不自动迁移或删除。

## 十一、旁路迁移

### 11.1 Hook

过渡期默认：

```text
Repository Hook:
  filesystem = workspace read-only
  network = denied
  permission effect = deny or ask only
```

在 Operator 确认 Workspace Trust 前不运行 SessionStart Hook。Hook 不能读取或修改
`.git`、`.codehelper`、`.agents`、Credential Store、Journal 或其他控制面路径。

目标链路：

```text
Hook Config
    -> Hook ExecutionOperation
    -> Guard
    -> ExecutionLease
    -> Process Broker
    -> Hook Protocol Parser
```

Hook Adapter 只拥有配置解码、输入输出协议和结果解释。

### 11.2 stdio MCP

MCP 分为两次独立授权：

```text
Server Lifecycle Operation
    -> Long-lived Lease
    -> Sandboxed MCP Process

MCP Tool Call
    -> Tool Operation
    -> Guard
    -> RPC Call
```

Lifecycle Lease 绑定 binary、argv、cwd、Environment Allowlist、Server Manifest、
Workspace、Subject Digest 和最大资源上界。MCP Tool Call 不能超过 Lifecycle Lease。

长期方案优先让 MCP Server 保持最小 Sandbox，通过 Runtime Broker 访问文件和网络。
无法代理的 Server 才考虑短生命周期 Worker。

完整 Broker 落地前：

- stdio MCP 默认关闭；
- 只能由位于外部 State Directory 的 Operator 配置显式信任；
- 信任记录必须绑定 Server 配置 Digest，不能只是可被 Workspace 修改的布尔值；
- UI 明示 Server 进程拥有的宿主权限；
- MCP Tool Guard 只能保护 RPC Tool Call，不能被描述为保护 Server 自身。

### 11.3 Process Smoke

Desktop/GUI Smoke 是明确的宿主信任操作：

```text
Runtime
    -> ApprovalOnce
    -> Host Process Lease
    -> Artifact Broker Snapshot
    -> Desktop Broker
    -> manifest/identity/digest/signature validation
    -> launch
    -> survival check
    -> process-tree teardown
    -> Receipt
```

Lease 至少绑定 Artifact ID、Manifest Digest、canonical snapshot executable、
SHA-256、device/inode、argv、cwd、Workspace Generation、Sandbox Home Generation、
有效期和单次 nonce。批准后原始文件或 Snapshot 被替换必须拒绝。`.app` 等目录型
Artifact 必须验证完整 Bundle Manifest，不能只验证主 executable。

如果平台不能限制该进程写 Workspace 或访问网络，Receipt 和 UI 必须明确报告
`host_unrestricted`，不能声称这些权限已被 Sandbox 限制。

### 11.4 Orchestration 与 Host

Orchestration Lane 中执行用户命令、启动 tmux 或 Worker 的路径必须迁入 Broker。
打开浏览器、系统目录选择器等纯 Host UX 行为可以保留专用 Host Broker，但必须有明确
Allowlist，不能接受模型或 Repository 直接控制的任意 argv。

### 11.5 Git 与 Repository Metadata

`.git`、Worktree Metadata、Index Lock、Refs 和 Object Database 不属于普通 Workspace
File Write。若产品支持 Agent 自动 Commit、Branch 或 Merge，应使用独立 VCS Broker：

- Operation 明确区分 `stage`、`commit`、`branch`、`merge` 和 `worktree`；
- Lease 绑定 Repository Identity、HEAD、Index Fingerprint、目标 Ref 和允许的 Path Set；
- 执行前后检查 HEAD/Index 漂移；
- 不允许通过普通 Shell Write 或 File Broker 获得 `.git` 写权限；
- 不支持自动 VCS 操作时，Plan Admission 必须把该步骤标记为宿主待办，避免在 Sandbox
  中重复尝试注定失败的命令。

## 十二、迁移阶段

### 阶段 0：建立基线并止血

当前状态：已完成。阶段 0 只建立收口前的安全基线，不引入可被未来入口绕过的临时
Lease 或 Broker。

目标：

- 建立生产副作用入口 Inventory；
- 增加静态门禁；
- Journal 移出 Workspace；
- 修复 Journal Escape、Symlink、Hardlink 和 Root Replacement；
- Repository Hook 默认只读、禁网且只能收紧权限；
- stdio MCP 默认关闭并要求外部 Operator Trust；
- Process Smoke 限制为 `ApprovalOnce`，禁止 Hook 自动批准；在 Artifact Snapshot 和
  Desktop Broker 完成前默认不可用；
- 建立 Control State、Sandbox Home、Artifact Staging 和 Workspace 的存储拓扑，
  统一 Workspace/Child Identity 派生。

退出标准：

- Inventory 覆盖所有生产 `os/exec` 和直接文件/网络副作用入口；
- 陌生 Workspace 启动不会自动执行 Workspace 代码；
- 伪造 Journal 不能影响 Workspace 外文件；
- 旧 Journal 只产生诊断，不被读取。
- Sandbox 可写目录不能覆盖 Control State 或 Artifact Staging；
- 当前生产代码不存在从可写 Sandbox Home 直接启动宿主进程的路径。

实现落点：

| 能力 | 当前实现 |
| --- | --- |
| 副作用 Inventory | `scripts/securityeffects` 与 `testdata/contracts/security-side-effect-entrypoints.json` |
| Workspace State Topology | `internal/runtime/app/wire/workspace_state.go` |
| Journal 外移与绑定 | `internal/persist/workspacejournal`、`internal/runtime/app/wire/journal.go` |
| Hook 止血 | `internal/adapter/hooks`、`internal/runtime/app/wire/contributors_capabilities.go` |
| stdio MCP 止血 | `internal/adapter/mcp/config.go` 与 MCP Contributor |
| Process Smoke 关闭 | `internal/adapter/tool/quality/process_smoke.go` |

### 阶段 1：引入资源命名空间、Artifact Provenance、Operation 和 Lease

目标：

- 在 `internal/security/authority` 增加 ExecutionOperation、Subject、
  EffectContract、RequiredControls 和 ExecutionLease；
- 增加 Resource Namespace、Root Generation、Artifact Manifest 和
  `ProcessHandleCapability`；
- 将现有 Tool Invocation 转换为 Operation；
- 保持现有 Policy 决策和用户行为不变。

Golden Test 固定：

- Policy Decision；
- Approval Scope；
- Effective Permission Profile Digest；
- Receipt 字段；
- Typed Denial；
- Amendment；
- Policy Revision 和 Workspace Generation 失配。
- Resource Root、Artifact、Process Handle 和 Sandbox Home Generation 失配；
- Preflight Failure 与 Verification Evidence 的失败闭环。

实现状态：

| 能力 | 当前实现 |
| --- | --- |
| Operation、Subject、Effect 与 Resource Namespace | `internal/security/authority/operation.go` |
| 单次 Lease、撤销、过期、消费与 Settlement | `internal/security/authority/lease.go` |
| Artifact Manifest 与 Bundle Tree Digest | `internal/security/authority/artifact.go` |
| Process Handle Capability | `internal/security/authority/process_handle.go` |
| Tool Guard 兼容 Facade | `internal/adapter/tool/guard/authority.go` 与 `pipeline_attempt.go` |
| Durable Receipt Projection | `internal/adapter/tool/execution.go`、`internal/runtime/protocol/tool_execution_receipt.go` |
| 公开 Lease TTL | `execution.lease_timeout` / `CODEHELPER_LEASE_TIMEOUT` |

阶段 1 保持原有 Policy Decision、Approval Scope、Typed Denial 和 Amendment 语义。
Lease 已在每个实际 Tool Attempt 前签发并单次消费，但 Process/Artifact Broker 尚未
接管进程启动；因此 Broker Enforcement、Desktop Smoke 和完整失败 Settlement Ledger
仍属于阶段 2。

### 阶段 2：建立 Process/Artifact Broker 并先迁移 Process Smoke

先迁移当前风险最高的 Desktop Process Smoke，证明：

- Lease 单次消费；
- Sandbox Home Artifact 提升为 Broker-owned Immutable Snapshot；
- Bundle Manifest、生产来源和可执行身份绑定；
- Sandbox Prepare 和 Settlement；
- Cancel、Kill、Wait、Reap；
- Process Handle 的 observe/stdin/signal/cancel 权限和幂等终态；
- Runner Failure 与 Command Failure 区分；
- 失败路径的 Receipt 和 Verification Evidence 完整性。

随后迁移一条普通 Sandboxed Process Tool，再迁移 Hook。旧执行路径在每条迁移完成后
立即删除。

### 阶段 3：迁移 Hook 和 stdio MCP

目标：

- Hook 不再直接调用 `process.NewCommand`；
- Server Lifecycle 经过独立 Operation 和 Lease；
- MCP Server 在最小 Sandbox 中运行；
- Tool Call 权限不超过 Server 上界；
- Restart、Crash、Disable 和 Config Digest 变化会撤销旧 Lease；
- stdout/stderr、Process Tree 和 Credential Environment 有界。

### 阶段 4：建立 File Broker 和 VCS Broker

目标：

- Workspace File Write 全部迁移到 descriptor-relative File Broker；
- Journal Transaction 与 File Lease、Suspend/Resume 和 Settlement 绑定；
- Git Metadata 不再通过普通 Workspace Write 或 Shell 绕过；
- 未启用 VCS Broker 时，Agent 不会重复尝试被策略禁止的 `.git` 写入。

### 阶段 5：替换 Descriptor 自报

目标：

- ExternalDescriptor 与 TrustedBinding 分离；
- 删除按工具名判断安全语义的逻辑；
- Journal、Read-before-write 和 Sandbox Requirement 来自 EffectContract；
- 外部扩展只能请求权限，不能定义最终 Capability。

### 阶段 6：控制矩阵替代 Strong

目标：

- Backend Probe 返回 Effective Controls；
- Tool Binding 声明 Required Controls；
- Authority 执行集合匹配；
- `Strong` 降为派生兼容字段；
- macOS、Linux、Windows 的平台差异被准确报告。

## 十三、PR 拆分

建议按以下顺序提交，每个 PR 独立可回滚：

1. `security: inventory and gate direct side-effect entrypoints`
2. `security: move workspace journal outside workspace`
3. `security: reject hostile journal paths and identities`
4. `hooks: default repository hooks to readonly and deny network`
5. `hooks: prevent untrusted permission widening`
6. `authority: introduce execution operations and leases`
7. `security: introduce resource namespaces and artifact provenance`
8. `process: introduce authorized process and artifact brokers`
9. `quality: migrate process smoke to immutable desktop artifacts`
10. `process: migrate one sandboxed builtin and process handles`
11. `hooks: migrate execution to process broker`
12. `mcp: authorize and sandbox stdio lifecycle`
13. `file: migrate workspace writes and journal transactions`
14. `git: introduce repository metadata broker`
15. `tool: replace name-based mediation with effect contracts`
16. `sandbox: enforce required control matrices`

一个 PR 不应同时修改 Policy 语义、Sandbox Backend 和 Runtime 生命周期。需要协议或
持久化变化时，单独提交生成文件和迁移测试。

## 十四、自动化门禁

### 14.1 静态门禁

使用 `go/analysis` 或 AST 工具检查生产代码，不使用纯文本 Grep 作为最终实现。至少识别：

```text
os/exec.Command
os/exec.CommandContext
os.StartProcess
syscall.Exec
unix.Exec
plugin.Open
net.Dial*
http.Client.Do
os.WriteFile
os.Remove*
os.Rename
```

Allowlist 按 Package、Symbol 和用途声明，只允许 Broker、可信 Helper、持久层 Owner
和 Test Fixture。新增命中默认失败。

### 14.2 行为测试

每个 Broker 必须覆盖：

- Allow；
- Deny；
- Malformed Operation；
- Forged、Expired、Consumed 和 Revoked Lease；
- Policy、Workspace 与 Subject Generation 变化；
- Cancel、Timeout、Kill、Wait 和 Cleanup；
- Concurrent Claims；
- Runner Failure；
- Sandbox Denial；
- Receipt Persistence Failure；
- Unsupported 或 Partial Platform。

### 14.3 Attack Corpus

macOS 与 Linux 至少覆盖：

- Workspace 外读写；
- Symlink、Hardlink、Mount/Device 和 Root Replacement；
- `/proc`、跨进程访问和 Debug/Trace；
- Namespace 与危险 Syscall；
- 直连网络、Redirect、DNS Rebinding、Private IP 和 Loopback；
- Sandbox Runner 启动失败；
- 进程树逃逸和取消残留；
- executable 在批准后被替换。
- Sandbox Home 中的二进制在批准前后被替换；
- `.app` 主 executable 不变但 Bundle Resource 被替换；
- Artifact Snapshot 与 Manifest 不一致；
- Sandbox Home、Control State、Artifact Staging 或 Child Root 发生路径重叠；
- 旧 Turn 使用 Process Handle 观察、输入或终止新 Generation 的进程；
- Pause 后未授权的新 Turn 尝试接管 retained draft；
- 普通 Workspace Write 或 Shell 尝试修改 `.git` 元数据。

Windows 在 Partial Backend 落地后维护独立 Corpus，并明确不能证明的控制项。

## 十五、完成标准

重构完成需要同时满足：

- 生产代码除 Broker 和精确 Allowlist 外不存在直接进程启动；
- 所有外部进程都有 Operation Digest、Permission Profile Digest、Lease ID 和 Attempt
  Receipt；
- 打开未信任 Workspace 不会自动运行 Workspace 代码；
- Repository Hook 不能扩大权限，且没有精确写 Lease 时不能修改 Workspace；
- MCP Server 初始化不能读取 Lifecycle Profile 外的宿主文件；
- Journal 中的绝对路径、Traversal、Symlink、Hardlink 和错误 Workspace Identity
  不能影响 Workspace 外文件；
- `PermissionNever` 下不存在通过伪造 Read Capability 执行副作用的路径；
- Process Smoke 批准后替换 binary 会被拒绝；
- Process Smoke 只能从 Broker-owned Artifact Snapshot 启动，且 Bundle Manifest 与
  构建来源可追溯；
- Sandbox Home 中的缓存和构建输出永远不会被当成控制状态或可信 Artifact；
- Process Start、Observe、Input、Signal、Cancel、Wait 和 Reap 使用同一可验证生命周期；
- Pause/Continue 的 Journal Transaction 保持 Session/Turn/Workspace Generation 绑定；
- 验证型 Operation 的 Preflight、启动和 Teardown 失败都会产生不可被其他 Kind 成功
  Evidence 覆盖的失败事实；
- `.git` 写入只能由 VCS Broker 执行，或被明确投影为宿主待办；
- Required Controls 未被 Backend 完整满足时执行失败；
- macOS、Linux 和受支持的 Windows 能力均由真实平台测试证明；
- 所有迁移路径生成同一格式的 Settlement 和 Receipt。

## 十六、主要风险与控制

| 风险 | 控制 |
| --- | --- |
| 新旧执行链长期并存 | 每迁移一个入口立即删除旧实现，并由静态门禁阻止回归 |
| Lease 变成可伪造 DTO | 私有字段、Authority Validator、单次 nonce；跨进程才使用 MAC |
| Broker 成为超大 Service | Process、File、Network、Desktop 分开，由共享 Authority 组合 |
| Capability Matrix 过度复杂 | 先覆盖现有真实控制，不为未来平台预设虚构等级 |
| 迁移改变用户审批语义 | 阶段 1 使用 Golden Test 固定现有决策和 Receipt |
| Sandbox 探测与执行状态漂移 | 每次 Prepared Execution 携带 Backend Facts，并在 Settlement 复核 |
| 持久 Sandbox Home 被跨 Turn 污染 | 将其定义为不可信可写数据面；宿主执行前必须提升为 Broker Snapshot |
| Workspace、Sandbox 与 Journal 使用不同身份 | 统一从 Workspace Identity 和 Generation 派生 Root |
| Path-only Approval 遭遇 TOCTOU | Lease 绑定不可变 Artifact Manifest，并从 Broker-only Snapshot 启动 |
| 长进程控制权跨 Turn 泄漏 | 使用绑定 Session/Turn/Generation 的 Process Handle Capability |
| 长驻 MCP Lease 无限有效 | 绑定 Server Generation、Config Digest、撤销和有界续租 |
| 为兼容旧状态削弱边界 | Pre-release 直接拒绝旧安全状态，只提供诊断 |

## 十七、推荐起点

第一批实施处理五个目标：

1. 建立副作用入口 Inventory 和静态门禁；
2. 将 Workspace Journal 移到 Runtime State Directory，并完成路径攻击测试；
3. 建立四类存储域和统一 Workspace Identity，明确 Sandbox Home 为不可信可写数据面；
4. 临时收紧 Repository Hook 和 stdio MCP；
5. 在 Artifact Snapshot 与 Desktop Broker 完成前关闭 Process Smoke；随后将它作为首个
   Broker 迁移对象。

阶段 0 先降低真实风险，阶段 1 已在不改变 Tool Guard 决策语义的前提下引入
Operation/Lease。下一步由阶段 2 的 Process/Artifact Broker 接管真实进程生命周期。
