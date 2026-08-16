# Extension Ecosystem 架构升级方案

> 状态：EE0 `baseline_frozen`；EE1-EE7 `accepted`。
>
> CodeHelper 分析基线：`main`
> `30ae1f137dd99c6e309499728c4205580773a505`。
>
> Codex 参考实现：`3bbf1fe75701c97fb190e0867002ba2d9dbda5db`。
>
> 范围：Extension API、Plugin Package、Skill、MCP、Hook、Dynamic Tool、
> Extension Lifecycle、Runtime Protocol、Host Projection、供应链治理、上下文投影、
> 可观测性与迁移门禁。
>
> 本文中的收益数字均为实施目标，必须在 EE0 冻结基线后通过证据文件验证；在对应
> Evidence 被接受前，不得表述为已实现收益。

实施进度：

- EE0：`baseline_frozen`，证据见
  [`extension-ecosystem-ee0-baseline.json`](./extension-ecosystem-ee0-baseline.json)；
- EE1：`accepted`，证据见
  [`extension-ecosystem-ee1-evidence.json`](./extension-ecosystem-ee1-evidence.json)；
- EE2：`accepted`，证据见
  [`extension-ecosystem-ee2-evidence.json`](./extension-ecosystem-ee2-evidence.json)；
- EE3：`accepted`，证据见
  [`extension-ecosystem-ee3-evidence.json`](./extension-ecosystem-ee3-evidence.json)；
- EE4：`accepted`，证据见
  [`extension-ecosystem-ee4-evidence.json`](./extension-ecosystem-ee4-evidence.json)；
- EE5：`accepted`，证据见
  [`extension-ecosystem-ee5-evidence.json`](./extension-ecosystem-ee5-evidence.json)；
- EE6：`accepted`，证据见
  [`extension-ecosystem-ee6-evidence.json`](./extension-ecosystem-ee6-evidence.json)；
- EE7：`accepted`，证据见
  [`extension-ecosystem-ee7-evidence.json`](./extension-ecosystem-ee7-evidence.json)。

EE0 使用 0/10/100/1,000 Skill、0/10/100 Plugin 和
Plugin + Skill + MCP + Hook 组合 Fixture 冻结当前行为。基线确认：

- 1,000 Skill 的原始 Catalog 为 142,096 Bytes、35,524 Token；当前 12 KiB
  世界状态预算保留 3,052 Token，并发生截断；
- 1,000 Skill 的发现与单 Source 刷新 P50 分别为 363.366ms 和 361.342ms；
- 100 Plugin 的发现与刷新 P50 分别为 55.652ms 和 54.396ms；
- 组合 Fixture 暴露 `load_skill`、`plugin_run` 和 `result_get`，Input Schema 合计
  642 Bytes；
- Update/Drain、Rollback、Security Revoke、MCP Quarantine 和 Restart Golden
  全部建立并通过；
- 扩展管理能力当前只存在于 CLI，Runtime Protocol、TUI、VS Code 和 ACP 均未形成
  对等控制面；
- Typed Extension API、Capability Bundle、统一 Lifecycle、Runtime Control Plane、
  按需 Skill Catalog 和 Plugin Skill 生产 Wiring 六项差距已冻结。

EE1 已建立 Runtime-owned Typed Extension API，包括 Descriptor、Failure Policy、
Budget、Outcome、Receipt、Thread/Turn Lifecycle、Context、Tool、MCP Contributor、
Immutable Registry、Noop Registry 和 Extension Test Harness。Memory 已作为首个真实
内建扩展迁移，`remember` 仍进入原共享 Tool Registry 和 Guard，且组合 Golden、
Tool Schema 与 Host/Provider 行为均未变化。Architecture Ratchet 从 81 个目标扩展为
85 个目标并全部通过，旧 `contributors_extensions.go` 上限由 329 行收紧至 292 行。

EE2 已建立 Process、Session、Thread、Turn 四级 Scoped State，支持 Extension
Namespace、Schema Version、CAS、Typed Conflict、Typed Budget Failure 和 Scope
Cleanup。Resolver 和 Compiler 生成绑定 Source Digest 与 Permission Digest 的不可变
Plan；`TurnSpec` 只持有冻结 Plan，Plugin Source 或 Permission 变化仅影响下一 Turn。
Durable Plan Receipt 通过原子 Rename 与目录 Fsync 持久化，相同 Plan 跨重启恢复相同
Revision。Session 已删除 `pluginRegistry`、`pluginTools` 和
`contributionReceipts` 三个零散字段，Architecture Ratchet 提升至 87/87。

EE3 已建立 Manifest V2 和声明式 Capability Bundle，Plugin 可以独立贡献 Tool、
Skill、MCP 与 Hook。每个 Capability 具备独立 Source Digest、Permission Digest 和
Authority Token，支持 Capability 级启停；Plugin Skill 已进入生产 Plan，V1 Plugin
保持兼容。Package Root Escape、Symlink Resource 和 Digest Drift 均 Fail Closed，
Architecture Ratchet 提升至 90/90。Live Drain、Reconcile 和运行期资源回收明确留给
EE4，证据见
[`extension-ecosystem-ee3-evidence.json`](./extension-ecosystem-ee3-evidence.json)。

## 1. 执行摘要

CodeHelper 已经具备较强的扩展安全基础：

- Builtin、Plugin、Skill、MCP 和 Dynamic Tool 进入同一个 `tool.Registry`；
- consequential Tool 统一经过 Policy、Permission、Approval、Constitution、
  Journal、Claims、Sandbox 和 Egress；
- Plugin 具备签名、摘要校验、不可变 Staging、启停、更新、回滚和安全撤销；
- MCP 具备显式 Tool Binding、健康隔离、Circuit Breaker、Catalog Reconcile 和
  Quarantine；
- Skill 具备受限发现、依赖解析、版本兼容、Lockfile、Digest Revalidation 和
  Turn Allowlist；
- Hook 具备版本化配置、强 Sandbox、Timeout、Process Tree Cleanup 和有界输出；
- Context Engineering 已经提供 Turn Snapshot、Typed World State、Stable Prefix
  和 Token Attribution。

当前主要问题不是缺少某个扩展类型，而是这些能力没有共同的运行时契约：

1. EE7 已删除 Legacy `extensionContributor`，构造期使用
   `extensionActivation`，Host 不再获得 Plugin/Skill direct-control；
2. Plugin 被建模为一个受控二进制和一个通用 `plugin_*_run` Tool，无法声明式组合
   Skill、MCP、Hook、Tool 和界面元数据；
3. Plugin Skill Snapshot 已有底层实现，但没有接入生产 Wiring；
4. Skill、MCP、Plugin 和 Hook 分别维护发现、状态、刷新、健康和错误语义；
5. CLI 直接调用 Plugin/Skill 控制对象，安装、启停和回滚没有进入统一 Runtime
   Operation/Event；
6. 所有 Skill Metadata 默认进入模型可见 World State，生态规模扩大后会持续消耗
   Token；
7. Legacy Extension Receipt 仍只描述构造期新增 Tool；EE1 Typed Receipt 已绑定
   Extension、Contributor Kind、Outcome、Deadline 和输出身份，但尚未绑定 EE2
   Plan 与后续 Capability Authority。

本方案建立一个 Runtime-owned 的 `Extension Kernel`，统一管理扩展声明、解析、权限
编译、激活、快照、生命周期和观测，但不建立第二条执行控制面：

```text
Extension Sources
  builtin / package / workspace / user / managed / MCP
                         |
                         v
                 Extension Resolver
       discover -> verify -> resolve -> compile
                         |
                         v
              Immutable Extension Plan
                         |
                         v
                  Extension Kernel
       lifecycle / state / context / tools / events
          |            |          |          |
          v            v          v          v
    ContextLedger  Tool Registry  MCP Pool  Runtime Protocol
                         |
                         v
        Guard -> Approval -> Journal -> Sandbox
```

目标不是把所有组件改造成动态 Plugin，也不是复制 Codex 的远端执行和云市场复杂度。
目标是将 Codex 的 Typed Contributor、Capability Bundle、Scoped State、按 Turn
选择和协议化控制面，与 CodeHelper 更强的 Guard、Authority、Receipt、Lockfile、
Sandbox 和本地执行边界组合起来。

## 2. 优化收益

### 2.1 用户收益

#### 一致的扩展体验

安装、查看、启用、禁用、授权、回滚和诊断通过 Runtime Operation/Event 完成。CLI、
TUI、VS Code 和 ACP 不再各自实现扩展管理逻辑。

用户获得：

- 相同的插件列表、状态、风险和错误原因；
- 相同的审批语义；
- 相同的启停和回滚结果；
- 在一个 Host 中执行的操作可以被其他 Host 重放和观察；
- 不再出现 CLI 已启用而 VS Code Session 仍使用旧 Catalog 的状态分裂。

#### 可解释的能力来源

每个模型可见 Tool、Skill、MCP Server 和 Hook 都能回答：

- 来自哪个 Extension Package；
- Package 的 Publisher、Version、Digest 和 Generation；
- 来源是 Managed、User、Repository、Builtin 还是临时选择；
- 为什么在本 Turn 可用；
- 使用了哪个 Effective Permission Profile；
- 当前是 Active、Draining、Revoked、Quarantined 还是 Unhealthy。

#### 更可靠的更新和回滚

Extension Plan 在 Turn 开始时冻结。更新只影响后续 Turn；运行中的调用使用旧
Generation 完成 Drain，撤销则拒绝新调用并按策略取消或等待已有调用。用户不再承担
“更新中途 Tool 身份改变”或“回滚后旧进程仍有权限”的隐性风险。

### 2.2 Token 与模型质量收益

当前所有已启用 Skill Metadata 默认进入 Skill World State。生态规模增加后，即使
单条 Description 已截断，总 Catalog 仍会挤压稳定前缀和任务上下文。

升级后采用三级投影：

1. 显式提及的 Skill 或 Plugin 直接进入 Turn Selection；
2. 小规模高相关集合进入模型可见 Catalog；
3. 其余能力通过 `skills.list`、`skills.read` 和 `extension.inspect` 按需读取。

预期收益：

| 指标 | 目标 |
| --- | --- |
| 1,000 Skill 场景的 Catalog Prompt Token P50 | 相对 EE0 降低至少 80% |
| 无 Extension 任务的 Input Token P50 | 不高于 EE0 的 102% |
| 常用 Extension 连续三 Turn 的 Stable Prefix Cache Share | 不低于 95% |
| 未被选择的 Skill 正文进入模型上下文 | 0 |
| 单个 Extension Context Fragment | 不超过 1,000 Token |
| 单个 Capability Bundle 的全部自动注入内容 | 不超过 10,000 Token |

质量收益来自减少无关扩展说明对模型注意力的稀释，同时保留显式提及、按需读取和
Authority-bound Handle，避免模型根据同名 Skill 或陈旧路径加载错误内容。

### 2.3 架构收益

#### 从“多个子系统”收敛为“一个 Extension Kernel”

Plugin、Skill、MCP、Hook、Memory 和 Dynamic Tool 不再分别决定生命周期和运行时
投影。它们成为统一 Extension Plan 中的不同 Capability。

预期结果：

- `wire` 只负责构造和注册，不再包含扩展业务流程；
- 扩展状态不再散落在 Session 字段和多个 watcher 中；
- 新增扩展类型不需要修改 Engine、Host 和多个 Registry；
- Extension Snapshot 由 Kernel 一次生成并进入 `TurnSpec`；
- Context、Tool Catalog 和 Runtime Event 使用同一个 Extension Revision。

#### 降低新增能力的改动面

当前新增一种 Extension Capability 通常需要修改 Wiring、Session、Engine Snapshot、
Host 展示、配置和测试。升级后的正常路径是：

1. 定义 Capability Contract；
2. 实现 Resolver/Contributor；
3. 在 Composition Root 安装；
4. 通过统一 Projection 自动进入 Context、Tool、Event 和 Host。

目标是将新增内建扩展的核心 Runtime 修改文件数降低至少 50%，并让 Host-specific
业务逻辑新增量为零。

### 2.4 安全收益

#### Package 不是权限

Plugin Manifest 只声明请求的 Capability。真正权限由 `AuthorityCompiler` 将以下信息
求交集后生成：

```text
managed ceiling
  ∩ user policy
  ∩ repository policy
  ∩ package declaration
  ∩ capability descriptor
  ∩ platform enforcement
  + explicit approved grant
```

Package 不能通过自声明获得网络、进程或控制面权限。

#### 每个 Capability 独立授权

一个 Plugin 同时包含 Skill、MCP 和 executable Tool 时：

- Skill 只获得内容读取权限；
- MCP Server 使用自己的 Transport、Host、Port 和 Tool Binding；
- Hook 使用独立 Event、Matcher、Timeout 和输出限制；
- executable Tool 使用自己的 Tool Descriptor、Resource Claims 和 Sandbox Profile。

禁用或撤销某个 Capability 不要求扩大或重建其他 Capability 的权限。

#### 供应链事实绑定到执行事实

每次 Tool Attempt Receipt 至少绑定：

- Extension ID；
- Package Digest；
- Capability ID；
- Extension Plan Revision；
- Tool Catalog Binding；
- Effective Permission Profile Digest；
- Sandbox Enforcement Receipt。

这使“批准的是哪个版本、实际运行的是哪个版本、用了什么权限”可以被同一条证据链
回答。

### 2.5 性能和稳定性收益

统一 Kernel 可以对 Discovery、Verification、Resolution 和 Projection 分别缓存，并以
Source Revision 精确失效，避免每个子系统独立扫描文件和重复解析。

目标：

| 指标 | 目标 |
| --- | --- |
| 无 Extension 的 Runtime 构造 P95 | 相对 EE0 增幅不超过 5% |
| 100 个 Package 的冷启动 P95 | EE0 后冻结绝对预算，EE7 不回退 |
| 未变化 Source 的增量刷新 | 不重新读取 Package 正文 |
| 单 Source 更新到 Catalog 收敛 P95 | 小于 250ms，不含远端下载 |
| Extension Lifecycle 泄漏 | Tool Handle、Process、Connection、Subscription、Lease、Timer 均为 0 |
| 扩展故障影响范围 | 不越过所属 Capability 或 Source |

### 2.6 研发与运维收益

- Extension API 有独立测试夹具，不必构造完整 `wire.NewExec`；
- Capability Bundle 可以进行离线 lint、权限预览和兼容性检查；
- Started、Completed、Failed、Quarantined、Draining 等事件可以统一进入 Trace；
- VS Code 能展示 Package、Capability、权限和健康，而不是只展示 MCP Health；
- Support 可以通过一份 Extension Receipt 重建启用、更新、撤销和调用历史；
- Marketplace、离线镜像和企业 Managed Source 可以复用同一 Resolver，不需要修改
  Agent Loop。

## 3. 目标与非目标

### 3.1 目标

升级必须：

1. 建立一个 Runtime-owned、Typed、Immutable-after-build 的 Extension Registry；
2. 统一 Extension 的发现、解析、权限编译、激活、快照、Drain、撤销和隔离；
3. 将 Plugin 从单一 executable 扩展为声明式 Capability Bundle；
4. 保留 executable Plugin，但使其成为可选 Tool Capability，而不是 Plugin 的定义；
5. 将 Skill、MCP 和 Hook 接入统一 Package Identity 和 Authority；
6. 让 Turn 使用冻结的 Extension Plan，不在运行中重读可变配置；
7. 将扩展管理纳入 Runtime Operation/Event；
8. 让所有 Host 只提交操作和投影事件；
9. 保持所有 consequential execution 进入现有 Tool Guard；
10. 保持 ContextLedger 是模型可见上下文的唯一 Authority；
11. 保持 SG7 的权限摘要、EX5 的 Typed Execution 和 CE7 的 Token 指标不回退；
12. 由 Runtime-owned Effect Registry 统一拥有运行期扩展资源，支持可逆激活、
    Drain、撤销和故障回滚；
13. 为每阶段生成机器可读 Evidence。

### 3.2 非目标

本升级不：

- 引入通用的进程内第三方 Go ABI；
- 动态加载不受信任的 Go Plugin 或共享库；
- 把 Provider 改造成动态 Plugin；
- 允许 Extension 直接调用 Provider、Sandbox、Journal 或持久层实现；
- 允许 Extension 绕过 Tool Registry 和 Guard 注册或执行 Tool；
- 引入 Codex 的远端 Executor、云端 Session 或强依赖云服务的 Marketplace；
- 为当前未发布格式增加长期兼容迁移；
- 在第一阶段开放公共 SDK；
- 用“everything is a plugin”取代明确的 Runtime 所有权；
- 让 Host 执行 Extension 业务逻辑；
- 用模型选择结果直接扩大权限。

## 4. 当前实现机制

### 4.1 构造期 Contributor

`internal/runtime/app/wire/modules_extensions.go` 定义私有
`extensionContributor`：

```go
type extensionContributor interface {
    ID() string
    Contribute(context.Context, *tool.Registry) (ContributionReceipt, error)
}
```

Contributor 按固定顺序执行：

1. 单 Bundle Plugin；
2. Plugin Registry；
3. Skill；
4. Memory；
5. Dynamic Tool；
6. Hook；
7. MCP。

每个 Contributor 在执行前后读取 Tool Catalog Snapshot，用新增 Tool 名称和命名输出
生成 `ContributionReceipt`。

优点：

- 构造顺序确定；
- 所有 Tool 进入共享 Registry；
- 失败可触发 ResourceStack 回滚；
- Contributor 不直接获得完整 `buildState`。

限制：

- Contract 只适合构造期 Tool 注册；
- Receipt 不包含 Capability、Authority、Digest、Generation 或 Runtime Health；
- Contributor 输出通过 `extensionBuildState` 回写 Session，仍与 Wiring 强耦合；
- 不能表达 Thread/Turn Lifecycle、Context、Config、Event 或 Scoped State。

### 4.2 Plugin

Plugin Manifest V1 描述：

- Name、Version、Publisher 和 CodeHelper Compatibility；
- executable 相对路径和 SHA-256；
- Arguments、Generation 和 Capability Inventory。

当前 Capability 被严格限制为：

- 一个 `plugin_run` Tool；
- Workspace 文件树；
- Process；
- 无网络；
- Strong Sandbox。

Registry 已负责：

- Workspace、User、Builtin Root 的优先级发现；
- Local Review Receipt；
- Signed Registry Distribution；
- Immutable Staging；
- Enable、Disable、Update、Rollback、Revoke 和 Security Revoke；
- Durable Authority Watch；
- 新调用切换、旧调用 Drain；
- 生命周期快照。

这些安全和生命周期能力必须保留，但 Plugin 的表达能力过窄。它无法作为 Skill、MCP、
Hook 和 Tool 的共同 Package Identity。

### 4.3 Skill

Skill 支持：

- Workspace、Configured、User 和多个兼容目录；
- `SKILL.md` 和可选 `skill.toml`；
- Locale Description；
- Dependency DAG 和 Version Constraint；
- Lockfile、Digest 和 Runtime Compatibility；
- Enablement 和 Revocation；
- Plugin Skill Authority Snapshot；
- `load_skill` 的 Turn Allowlist。

当前生产 Wiring 没有把 Plugin Snapshot 传入 Skill Discovery，因此 Plugin Skill
Capability 只存在于底层实现和测试，没有形成端到端生产能力。

模型侧会自动收到全部已启用 Skill 的 Name、Description 和 Source。单条 Description
有界，但 Catalog 数量增长仍会线性增加 Token。

### 4.4 MCP

MCP 支持 stdio、HTTP、Streamable HTTP 和 SSE，并具备：

- 显式 Tool、Resource 和 Prompt Allowlist；
- 每 Tool Capability、Access、Parallel、Sandbox 和 Resource Binding；
- Permission Profile Ceiling；
- Health Tracker 和 Circuit Breaker；
- Catalog Change Notification；
- Source-scoped Registry Reconcile；
- Deferred Tool Materialization；
- Catalog Sync 失败后的 Quarantine；
- OAuth 和环境变量凭据引用。

问题在于 MCP 的配置、连接池、健康和 Catalog 生命周期是独立控制面。Plugin 无法通过
统一 Package Plan 声明 MCP Capability，Kernel 也无法在一个快照中证明 Package、
Server 和 Tool Binding 的共同来源。

### 4.5 Hook

Hook V1 支持 SessionStart、MessageSubmit、ToolCallBefore、ToolCallAfter、ShellEnv、
TurnEnd、PreCompact、PostCompact 和 PermissionRequest。

Hook Process 具有 Timeout、输出上限、Process Tree Cleanup、Sandbox 和 Audit。
ToolCallBefore 可以 Allow、Deny、Ask 或替换输入。

缺口：

- 只有 Command Handler；
- 没有明确 Sync/Async Mode；
- 没有 Thread/Turn Scope；
- 没有 Managed/User/Repository/Plugin Source Priority；
- 没有独立 Trust Status；
- 没有统一 Started/Completed Runtime Event；
- 不能由 Plugin Bundle 声明并保持 Package Provenance。

### 4.6 Host 控制面

CLI 当前直接打开 Plugin Control、调用 Registry，并直接执行 Skill Discovery、Lock 和
Enablement。这些路径不是 Runtime Operation，因此：

- 其他 Host 无法复用；
- Event Log 不能完整记录；
- Runtime Session 可能依赖 watcher 才能收敛；
- Host 层承担了不应拥有的业务逻辑；
- Protocol Schema 无法表达安装、授权和回滚结果。

## 5. Codex 可借鉴机制

### 5.1 Typed Extension Registry

Codex 使用 Builder 注册多个独立 Contributor Contract：

- Thread Lifecycle；
- Turn Lifecycle；
- Config；
- Token Usage；
- Skill Invocation；
- Context 和 World State；
- MCP Server；
- Turn Input；
- Tool；
- Tool Lifecycle；
- Turn Item；
- Approval Review。

Builder 完成后生成 Immutable Registry。Core 只读取相应 Contributor Slice，不需要了解
具体 Extension 类型。

可借鉴点：

- 按职责拆分 Contract；
- 构造后 Registry 不再变更；
- Extension 只获得窄能力；
- 一个 Extension 可以实现多个 Contributor；
- Context、Tool 和 Lifecycle 共享同一 Extension Identity。

不直接复制：

- CodeHelper Contributor 必须返回 Typed Error 和 Receipt；
- Contributor 必须有稳定 ID、优先级、失败策略和预算；
- 不能依赖未命名的注册顺序解决权限冲突；
- 不能让 Extension Event 绕过 Runtime 的持久化和序列分配。

### 5.2 Scoped Extension State

Codex 使用 Type-indexed Store 绑定 Session、Thread 和 Turn 状态。

CodeHelper 可以借鉴 Scope 概念，但不能直接复制无命名 `TypeId` Map。目标 Store 必须
包含：

- Extension ID Namespace；
- State Key 和 Schema Version；
- Scope；
- Size/Entry Budget；
- Persistence Policy；
- Revision；
- Cleanup/Close Ownership；
- Redaction Policy。

### 5.3 Capability Bundle

Codex Plugin Package 可以声明 Skills、MCP、Apps、Hooks 和 Interface Metadata，
并将资源路径绑定到拥有它的 Environment。

CodeHelper 应采用同样的“Package 是 Capability 集合”思想，同时增加：

- Publisher Signature；
- 每个资源 Digest；
- Runtime Compatibility；
- Capability Permission Request；
- Managed Policy Ceiling；
- Lockfile；
- Activation Receipt；
- Strong Sandbox Requirement。

### 5.4 Authority-bound Resource

Codex 在解析 Plugin 后，将资源从裸路径转换为 Environment-owned Locator，并拒绝
Package Root 外的资源。

CodeHelper 应把现有 canonical path、Plugin Authority、MCP Server Identity 和 Skill
Digest 统一为 `ExtensionResourceRef`，不允许后续阶段重新从裸字符串推断 Authority。

### 5.5 Skill Selection 和按需读取

Codex 提供：

- Explicit Mention；
- Authority-scoped Catalog；
- 低成本 Lexical Selector；
- `skills.list`；
- `skills.read`；
- Pagination 和 Response Byte Limit；
- World State Full/Patch。

CodeHelper 应复用已有 ContextLedger 和 Content Store，而不是引入另一套 History。

### 5.6 Hook Protocol

Codex Hook 将 Event、Handler Type、Execution Mode、Scope、Source、Trust Status、
Running/Completed Status 和 Output Entry 都投影为协议数据，并支持并发执行后按配置
顺序合并。

CodeHelper 应采用这些可观察语义，但保留更严格的 Guard、Sandbox、Permission Hook
和 Audit Redaction。

## 6. 差距与根因

| 优先级 | 差距 | 根因 | 影响 |
| --- | --- | --- | --- |
| P0 | Host 直接执行 Plugin/Skill 控制逻辑 | 管理操作未进入 Runtime Protocol | Host 语义分裂、审计不完整 |
| P0 | Plugin 只能暴露 executable Tool | Manifest 以进程为中心，不是 Capability Bundle | Skill/MCP/Hook 无法形成统一生态 |
| P0 | Plugin Skill 未接入生产 | Skill Snapshot 与 Plugin Lifecycle 没有共同 Plan | 已实现能力不可用 |
| P1 | Extension API 仅为 Wiring 私有接口 | Contributor 围绕 Tool Registry 设计 | 新能力持续修改 Wire 和 Session |
| P1 | 生命周期分散 | 各子系统独立 watcher、state 和 reload | 更新、撤销、恢复难以证明一致 |
| P1 | Receipt 只记录构造输出 | 缺少统一 Extension Identity 和 Revision | 无法绑定权限与实际执行 |
| P1 | Skill Catalog 全量注入 | 缺少选择器和按 Authority 分页读取 | 大生态持续增加 Token |
| P1 | Hook 缺少来源和运行事件 | Hook 是本地配置辅助功能 | 无法作为 Package Capability 治理 |
| P2 | Control-plane 状态格式分散 | Plugin、Skill、MCP 各自持久化 | 原子更新和恢复复杂 |
| P2 | 缺少 Extension 级诊断 | 只投影 Plugin Lifecycle 和 MCP Health | 操作者看不到完整因果链 |
| P2 | 内建扩展没有统一测试夹具 | 依赖完整 Wiring 或子系统测试 | 新扩展开发成本高 |

## 7. 设计原则

1. **一个 Kernel**：Extension Kernel 是扩展状态和生命周期的唯一 Authority。
2. **一个执行入口**：所有 consequential Tool 继续进入 Tool Registry 和 Guard。
3. **一个 Context Authority**：所有模型可见扩展内容先进入 ContextLedger。
4. **Package 不等于权限**：Manifest 只声明请求，AuthorityCompiler 决定有效权限。
5. **Resolve before activate**：发现和解析不会启动进程、连接网络或注册 Tool。
6. **Immutable Turn Plan**：Turn 只使用开始时冻结的 Extension Plan。
7. **Source-scoped reconcile**：更新一个 Source 不得重建无关 Source。
8. **Capability isolation**：一个 Capability 失败不默认隔离整个 Package。
9. **Fail closed by authority**：身份、Digest、Binding 或 Permission 不一致时撤销可见性。
10. **Bound every surface**：Discovery、Manifest、State、Context、Schema、Output 和 Event
    均有硬上限。
11. **Receipt before claim**：没有可持久化 Receipt 的生命周期变化不视为成功。
12. **Hosts project only**：Host 不执行 Extension 解析、授权、安装或生命周期逻辑。
13. **No permanent dual path**：迁移完成后删除旧 Wiring 和 Host direct-control 路径。
14. **No premature public ABI**：内部 Contract 稳定并经过至少两个内建扩展验证后再讨论
    公共 SDK。

## 8. 目标架构

### 8.1 分层

```text
Host
  submit Extension Operation / project Extension Event
                         |
                         v
Runtime Application
  dispatch / durable commit / event sequencing
                         |
                         v
Extension Kernel
  source registry / resolver / plan / lifecycle / scoped state
       |             |             |             |
       v             v             v             v
  Capability     Context       Tool Catalog    Health
  Compiler       Contributor   Reconcile       Projection
       |
       v
Security Authority Compiler
       |
       v
Effective Permission Profile
```

### 8.2 Package 与 Runtime Extension 分离

必须区分：

#### Extension Package

磁盘或远端分发单元，包含 Manifest、资源和签名。它是非活动声明。

#### Resolved Extension

经过来源解析、Digest 校验、兼容性检查和 Authority Binding 的不可变描述，不启动任何
运行时资源。

#### Extension Plan

将多个 Resolved Extension 与 Managed/User/Repository Policy 编译后的有效计划，包含
Capability、优先级、权限摘要、冲突决议和预算。

#### Active Extension

Kernel 根据 Plan 激活的运行时句柄。它只能获得显式 Capability Port。

### 8.3 Extension Identity

```go
type ExtensionID struct {
    Name      string
    Publisher string
}

type ExtensionRevision struct {
    Version    string
    Generation uint64
    Digest     string
}

type ExtensionRef struct {
    ID       ExtensionID
    Revision ExtensionRevision
    Source   ExtensionSourceRef
}
```

`Name` 不能作为唯一 Authority。所有 Runtime Binding 至少包含 Publisher、Digest 和
Generation。

### 8.4 Capability

第一版支持：

```go
type CapabilityKind string

const (
    CapabilityTool  CapabilityKind = "tool"
    CapabilitySkill CapabilityKind = "skill"
    CapabilityMCP   CapabilityKind = "mcp"
    CapabilityHook  CapabilityKind = "hook"
)
```

Memory、Workflow、Automation 和 Provider 不在第一版 Package Capability 中。内建
Memory 可以通过 Extension API 验证 Contract，但不立即开放为第三方 Package 能力。

### 8.5 Manifest V2

建议保留 `plugin.toml`，升级 Schema：

```toml
schema_version = 2
name = "review-suite"
version = "1.2.0"
publisher = "example"
codehelper = ">=0.2.0 <0.3.0"
generation = 7

[interface]
display_name = "Review Suite"
description = "Repository review capabilities"
category = "quality"

[[skills]]
root = "skills/review"
digest = "sha256:..."

[[mcp]]
config = "mcp.json"
digest = "sha256:..."

[[hooks]]
config = "hooks.json"
digest = "sha256:..."

[[tools]]
id = "scan"
executable = "bin/scan"
executable_sha256 = "..."
descriptor = "tools/scan.json"
descriptor_sha256 = "..."
```

每个 Capability 独立校验、启停、授权和隔离。V1 executable Plugin 在迁移期编译为
一个 V2 Tool Capability，但不长期维护两套执行器。

### 8.6 Typed Contributor API

建议将 Contract 放在新的窄 Package 中，并由 `wire` 安装实现：

```go
type Extension interface {
    Descriptor() Descriptor
}

type ThreadLifecycleContributor interface {
    OnThreadStart(context.Context, ThreadStartInput) Outcome
    OnThreadResume(context.Context, ThreadResumeInput) Outcome
    OnThreadStop(context.Context, ThreadStopInput) Outcome
}

type TurnLifecycleContributor interface {
    OnTurnStart(context.Context, TurnStartInput) Outcome
    OnTurnStop(context.Context, TurnStopInput) Outcome
    OnTurnAbort(context.Context, TurnAbortInput) Outcome
}

type ContextContributor interface {
    ContributeContext(context.Context, ContextInput) ([]ContextItem, Outcome)
}

type ToolContributor interface {
    ContributeTools(context.Context, ToolInput) ([]ToolRegistration, Outcome)
}

type MCPContributor interface {
    ContributeMCP(context.Context, MCPInput) ([]MCPContribution, Outcome)
}
```

所有接口必须：

- 有稳定 Contributor ID；
- 返回 Typed Outcome；
- 声明 Failure Policy；
- 接受 Deadline；
- 产生 Receipt；
- 不能获得 `buildState`、Runtime 实现或 Sandbox 实现；
- 不能直接持久化 Runtime 事实。

### 8.7 Scoped Extension State

```go
type ExtensionStateKey struct {
    Extension ExtensionID
    Scope     Scope
    Name      string
    Version   uint32
}

type ExtensionStateStore interface {
    Load(context.Context, ExtensionStateKey) (StateValue, bool, error)
    CompareAndSwap(context.Context, ExtensionStateKey, uint64, StateValue) error
    Delete(context.Context, ExtensionStateKey, uint64) error
}
```

Scope：

- Process：只允许 Kernel 内部缓存；
- Session：连接池、共享只读 Catalog；
- Thread：用户选择、Extension 私有线程状态；
- Turn：冻结的选择、调用去重和临时预算。

持久化只允许显式声明的 Thread State。Tool、Hook 或 Plugin Process 不能直接获得 Store。

### 8.8 Extension Plan

```go
type ExtensionPlan struct {
    Revision       uint64
    Digest         string
    Extensions     []ResolvedExtension
    Capabilities   []ResolvedCapability
    Conflicts      []ConflictResolution
    Permissions    []PermissionBinding
    ContextBudget  Budget
    ToolBudget     Budget
    CreatedAt      time.Time
}
```

Plan 构造后不可变。TurnSpec 只保存 Plan Ref、Capability Snapshot 和必要的模型可见
投影，不保存可变 Registry 指针。

### 8.9 冲突规则

优先级沿用治理顺序：

```text
Managed > User > Repository
```

Builtin 不通过名称覆盖争夺优先级。Builtin Tool 和外部 Capability 使用不同 Source
Namespace。

冲突处理：

- 相同 Extension ID 不同 Revision：按有效 Source Policy 选择一个；
- 相同 Tool 模型名：必须 Namespaced，禁止静默覆盖；
- 相同 Skill Name：模型 Handle 包含 Authority，裸名称只在唯一时可用；
- 相同 MCP Server Name：Package Namespace 参与真实身份；
- 相同 Hook ID：以 Extension ID + Capability ID + Hook ID 组成全局身份；
- Managed Deny 不可被低优先级 Source 覆盖。

### 8.10 生命周期状态机

```text
Discovered
    |
    v
Verified -> Rejected
    |
    v
Resolved -> Incompatible
    |
    v
Staged
    |
    v
Active -> Unhealthy -> Quarantined
    |
    v
Draining
    |
    +-> Inactive
    +-> Revoked
    +-> RolledBack
```

规则：

- Discovery 不执行 Package 内容；
- Verification 不访问未授权网络；
- Active 前必须完成 Plan Commit；
- 新 Plan Commit 后才切换新调用；
- 旧 Tool Handle 在 Drain 完成后关闭；
- Security Revoke 可以立即拒绝新调用并取消旧调用；
- Quarantine 默认仅移除故障 Capability；
- Package Identity 或权限摘要失效时隔离整个 Package；
- Restart 根据 Durable Plan 和 Lifecycle Receipt 恢复，不依赖 watcher 猜测状态。

#### 8.10.1 Extension-owned Effect Registry

生命周期状态不能只描述“应该处于什么状态”，还必须拥有“实际创建了哪些运行期
资源”。EE4 在 `runtime/extension` 建立 Runtime-owned Effect Registry。它与
`runtime/app/wire/assembly.ResourceStack` 边界严格分离：

- `assembly.ResourceStack` 只负责 Runtime 构造失败和进程关闭；
- Effect Registry 负责 Extension、Generation 和 Capability 的运行期激活、Drain、
  Revoke、Rollback 与 Quarantine；
- Effect Registry 不是 Service Locator，不向 Extension 暴露任意 Runtime 服务；
- Tool Guard、Authority、Journal 和 Sandbox 仍是执行控制面，Effect Registry 只拥有
  生命周期，不产生或放宽权限。

每个 Effect 必须绑定不可变 Owner：

```go
type EffectOwner struct {
    ExtensionID    string
    PlanRevision   uint64
    Generation     uint64
    CapabilityID   string
    CapabilityKind ContributorKind
}

type EffectDescriptor struct {
    ID        string
    Kind      EffectKind
    Owner     EffectOwner
    DependsOn []string
}
```

Effect 至少覆盖 Tool Registration、Plugin Process、MCP Connection/Pool、Hook
Subscription/Runner、Lease 和 Timer。注册成功返回不透明 Handle；Handle 的 Cancel、
Drain 和 Close 回调只存在于进程内，不得写入 Durable Receipt。Receipt 只记录脱敏后的
Owner、Effect ID/Kind、状态、时序、Reason Code 和 Outcome，不记录闭包、内存地址、
Raw Path、Command、Credential 或 Secret。

激活和回收遵循以下不变量：

1. Activate 在私有 Scope 内逐项注册 Effect；全部成功且 Plan 已提交后才能发布为
   Active；
2. 任一激活步骤失败时，已注册 Effect 按反向依赖顺序全部回滚，不能发布部分 Active
   Capability；
3. Update/Rollback 先提交新 Authority 并切断旧 Generation 的新调用，再 Seal 旧
   Scope，等待 In-flight 调用，最后按反向依赖顺序 Close；
4. Security Revoke 先撤销 Authority 并主动 Cancel In-flight，再执行有界 Close；
5. Drain 后拒绝新增 Effect；Owner、Plan Revision 或 Generation 不匹配时 Fail
   Closed；
6. Drain、Cancel 和 Close 必须幂等且支持并发调用；单个 Close 失败不能阻止其余
   Effect 回收，错误最终合并进入 Receipt；
7. Drain 超时或资源关闭失败时进入 Quarantined，生成 Durable Failure Receipt，不能
   伪装为 Inactive；
8. Restart 只根据 Durable Plan 和 Receipt 重建所需 Effect，不恢复进程内 Handle，
   不重复激活已经存在的资源；
9. Capability 级故障只关闭其拥有的 Effect；共享 Effect 必须有显式 Owner 和引用
   模型，禁止依赖隐式全局单例。

### 8.11 Runtime Protocol

新增 Operation 建议：

- `extension.list`；
- `extension.inspect`；
- `extension.install`；
- `extension.update`；
- `extension.trust`；
- `extension.enable`；
- `extension.disable`；
- `extension.rollback`；
- `extension.revoke`；
- `extension.security_revoke`；
- `extension.refresh`。

新增 Event 建议：

- `extension.discovered`；
- `extension.verified`；
- `extension.plan.committed`；
- `extension.activated`；
- `extension.draining`；
- `extension.deactivated`；
- `extension.health.changed`；
- `extension.quarantined`；
- `extension.revoked`；
- `extension.operation.completed`。

所有 Event 必须进入 `event_traits.json`，声明 Class、Item Owner、Durability、
Correlation 和 Terminal Trait，并生成 Go/TypeScript/Schema。

### 8.12 Skill 投影

Skill Catalog 分三层：

#### Kernel Catalog

完整、Authority-bound、不可直接进入模型。

#### Turn Candidate Set

来源包括：

- 用户显式 Skill Mention；
- 用户显式 Plugin Mention；
- 当前任务文本的低成本相关性选择；
- Managed Required Skill；
- 已经在当前 Thread 使用且仍兼容的 Skill。

#### Model-visible Catalog

只包含有界 Metadata：

- Authority Handle；
- Package Handle；
- Name；
- 有界 Description；
- Main Resource Handle。

Skill 正文只通过 `skills.read` 或兼容的 `load_skill` 读取。读取结果进入 Content Store
Admission，并记录 Skill Invocation Receipt。

第一版保留 `load_skill` 作为兼容入口，内部转调新的 Authority-bound Resolver。EE7
完成后评估是否删除裸名称协议。

### 8.13 MCP 投影

MCP Server 可以来自：

- 显式 Runtime Config；
- Extension Package；
- Managed Source。

所有来源编译为统一 `MCPContribution`，包含：

- Extension Ref；
- Server ID；
- Transport；
- Credential References；
- Permission Ceiling；
- Tool/Resource/Prompt Binding；
- Health Policy；
- Config Digest。

MCP Pool 仍负责 Transport 和 Connection；Extension Kernel 负责来源、Plan 和生命周期。
Tool Adapter 继续按 Server Source Reconcile，并保留当前 Quarantine 行为。

### 8.14 Hook V2

Hook V2 增加：

- Event；
- Handler Type：第一版只开放 Command，保留扩展枚举；
- Execution Mode：Sync/Async；
- Scope：Thread/Turn；
- Source；
- Trust Status；
- Matcher；
- Timeout；
- Output Budget；
- Failure Policy；
- Capability ID。

并产生：

- Hook Started；
- Hook Completed；
- Hook Blocked；
- Hook Failed；
- Hook Output Entry。

并发 Hook 可以并行执行，但决策必须按稳定配置顺序合并：

- 任意 Deny 胜出；
- 无 Deny 时 Ask 胜出；
- Input Rewrite 逐个应用，并在每次 Rewrite 后重新 Prepare；
- Async Hook 不能改变当前 Tool 授权；
- Managed Hook 的不可变 Deny 不能被低优先级 Hook 覆盖。

### 8.15 Extension Event Sink

Extension 不直接写 Event Log。它只能提交 `ExtensionSignal`：

```go
type ExtensionSignal struct {
    Extension ExtensionRef
    Capability CapabilityRef
    Kind       SignalKind
    Correlation Correlation
    Payload    TypedPayload
}
```

Runtime 校验、分配 Sequence、持久化并投影为 Protocol Event。Extension 不能选择
Durability 或伪造 Terminal Event。

### 8.16 Observability

每个 Extension Trace 至少包含：

- Source Discovery Duration；
- Verification Duration；
- Resolution Cache Hit；
- Plan Revision 和 Digest；
- Activation Duration；
- Capability Count；
- Context Token；
- Tool Schema Bytes；
- MCP Health；
- Hook Duration 和 Outcome；
- Drain Duration；
- Quarantine Cause；
- Permission Profile Digest；
- Receipt ID。

默认日志只记录身份、大小、状态和错误分类，不记录 Skill 正文、Hook Payload、
Credential、Tool Argument 或完整远端错误。

## 9. 安全模型

### 9.1 信任边界

可信：

- Runtime-owned Extension Kernel；
- AuthorityCompiler；
- 签名验证和 Digest 验证结果；
- Host 明确提交并由 Runtime 接收的审批响应；
- Platform Enforcement Receipt。

不可信：

- Workspace、User 和远端 Package；
- Manifest Description 和 UI Metadata；
- Skill 正文和资源；
- MCP Server、Tool Schema 和 Tool Output；
- Hook Command 和 Output；
- executable Plugin；
- Marketplace Index，除非通过已配置 Publisher 验证；
- 模型产生的 Extension、Skill 或 Tool Handle。

### 9.2 必须保持的约束

- Extension 不能写 `.codehelper`、`.git` 等控制面；
- executable Tool 默认无网络；
- MCP 网络必须通过 Runtime-owned Egress；
- Extension 安装网络与 Extension 执行网络分离授权；
- Manifest 和资源路径拒绝 Symlink Escape、Hardlink Ambiguity 和 Root Escape；
- Package 更新后旧权限 Grant 因 Digest 变化自动失效；
- Skill Lock 和 Package Lock 不允许以裸路径作为唯一身份；
- Security Revoke 必须跨进程传播并撤销现有 Authority；
- Hook 不得成为 Policy 或 Sandbox 的替代入口；
- UI Metadata 永远不能进入 Developer Policy Slot。

## 10. 分阶段实施

### EE0：基线与 Golden

#### EE0 目标

冻结当前生态行为、性能、Token 和安全基线，避免后续以主观判断替代收益证据。

#### EE0 工作项

- 新增 Extension Ecosystem Baseline 工具；
- 记录构造、发现、刷新、Tool Schema、Skill Token 和生命周期指标；
- 建立 0、10、100、1,000 Skill Fixture；
- 建立 0、10、100 Plugin Fixture；
- 建立 Plugin + Skill + MCP + Hook 组合 Golden；
- 建立更新、Drain、Revoke、Quarantine 和 Restart 场景；
- 建立 CLI、TUI、VS Code、ACP Host Parity 基线；
- 记录当前生产 Plugin Skill 不可达事实。

#### EE0 产物

- `docs/extension-ecosystem-ee0-baseline.json`；
- `scripts/extensionecosystembaseline/`；
- `make extension-ecosystem-ee0`。

#### EE0 Gate

- Fixture 全部 Hermetic；
- 基线可重复运行；
- 5 次样本 MAD 不超过 Median 的 15%，否则扩展到 10 次；
- 不修改生产行为。

### EE1：Typed Extension API

#### EE1 目标

将私有构造期 Contributor 提升为窄、Typed、可测试的内部 Extension Contract。

#### EE1 工作项

- 定义 Descriptor、Contributor ID、Outcome、Receipt 和 Failure Policy；
- 定义 Thread、Turn、Context、Tool、MCP 和 Lifecycle Contributor；
- 建立 Immutable Registry Builder；
- 建立 Noop Registry；
- 建立 Extension Test Harness；
- 将 Memory 或 Dynamic Tool 作为首个迁移样本；
- `wire` 只负责安装 Extension。

#### EE1 Gate

- Contributor 不接受 `buildState`；
- Extension API 不导入 Host；
- Extension API 不持有 Sandbox、Provider 或 Persist 实现；
- 同一 Contributor ID 冲突时构造失败；
- 每个 Contributor 有预算、Deadline 和 Receipt；
- Architecture Ratchet 不低于当前基线。

### EE2：Scoped State 与 Immutable Plan

#### EE2 目标

建立统一 Extension State 和 Turn-frozen Extension Plan。

#### EE2 工作项

- 实现 Process/Session/Thread/Turn Scope；
- 实现 Namespace、Revision、CAS 和 Size Budget；
- 实现 Extension Resolver 和 Plan Compiler；
- Plan Digest 绑定 Source Revision 和 Permission Digest；
- TurnSpec 只引用冻结 Plan；
- Restart 从 Durable Plan Receipt 恢复；
- 删除 Session 上可由 Kernel 拥有的零散扩展字段。

#### EE2 Gate

- Turn 运行期间修改 Extension Source 不改变当前 Turn；
- Restart 后 Plan Digest 一致；
- State Budget 超限 Typed Fail；
- State Key 不允许跨 Extension 读取；
- 无双写状态 Authority。

### EE3：Capability Bundle 与 Plugin V2

#### EE3 目标

让 Plugin 成为声明式 Capability Bundle，并打通 Plugin Skill 生产路径。

#### EE3 工作项

- 实现 Manifest V2 Parser 和 Linter；
- 实现 Skill、MCP、Hook、Tool Capability Resolver；
- 每个资源独立 Digest；
- 将 V1 executable 编译为 V2 Tool Capability；
- Plugin Skill Snapshot 进入统一 Plan；
- Capability 级 Enable/Disable；
- Interface Metadata 独立于模型 Policy；
- Package Lockfile 绑定 Publisher、Version、Digest 和 Capability Digest。

#### EE3 Gate

- 一个 Fixture Plugin 同时贡献 Skill、MCP、Hook 和 Tool；
- 禁用 Hook 不影响 Skill；
- MCP 故障不撤销无关 Skill；
- Package Root Escape 和 Digest Drift 全部拒绝；
- V1 行为在迁移期保持；
- 新路径不绕过 Guard。

### EE4：统一生命周期与故障隔离

#### EE4 目标

统一 Activate、Drain、Revoke、Quarantine、Rollback 和 Recovery，并以
Extension-owned Effect Registry 建立运行期资源所有权，保证状态迁移与实际资源回收
一致。

#### EE4 工作项

- 实现 Extension Lifecycle State Machine；
- 实现 Runtime-owned Effect Registry、Effect Scope 和不透明 Effect Handle；
- Effect Owner 绑定 Extension、Plan Revision、Generation、Capability ID 和 Kind；
- 实现激活失败的反向依赖回滚，以及幂等、并发安全的 Cancel、Drain 和 Close；
- 实现 Source-scoped Reconcile；
- 实现 Capability Health；
- 实现新调用切换和旧调用 Drain；
- Security Revoke 支持主动取消；
- 生命周期和 Effect 变化生成脱敏 Durable Receipt，但不持久化进程内 Handle；
- Tool Registration、MCP、Plugin Process、Hook、Subscription、Lease 和 Timer
  迁移到统一 Lifecycle；
- Restart 根据 Durable Plan/Reconciliation 重建 Effect，禁止恢复闭包或重复激活；
- watcher 只触发 Refresh Operation，不直接改变 Runtime Authority。

#### EE4 Gate

- 更新期间无 Stale Tool 执行；
- Revoke 后新调用拒绝率 100%；
- 对每个激活步骤执行故障注入后，Tool Handle、Process、Connection、Subscription、
  Lease 和 Timer 均为 0；
- Drain 后所属 Effect 数为 0，且旧 Generation 不能注册新 Effect；
- Drain、Cancel、Close 重复或并发执行结果一致；
- Owner、Plan Revision 或 Generation 不匹配时 Fail Closed；
- Drain 超时或 Close 失败进入 Quarantined，并生成 Durable Failure Receipt；
- 一个 Capability 故障不影响无关 Capability；
- Restart 不重复激活或泄漏资源；
- Race Test 通过。

### EE5：Skill Selection 与 Context 优化

#### EE5 目标

将 Skill Catalog 从全量注入改为选择、分页和按需读取。

#### EE5 工作项

- 建立 Authority-bound Skill Handle；
- 实现 `skills.list` 和 `skills.read`；
- 实现 Explicit Mention；
- 实现确定性低成本 Selector；
- Selector 先 Shadow，不改变模型可见 Catalog；
- 记录 Recall、Precision、Selection Size 和 Token Savings；
- 达标后启用 Candidate Projection；
- 使用 ContextLedger World State Full/Patch；
- Skill 正文进入 Content Store Admission。

#### EE5 Gate

- 1,000 Skill Catalog Token P50 降低至少 80%；
- Golden 任务 Critical Skill Recall 为 100%；
- 未选择 Skill 正文进入上下文数量为 0；
- Selector 输出确定且有界；
- CE7 Input Token P50 不回退超过 2%；
- Stable Prefix Cache Share 不低于 95%。

### EE6：Runtime Control Plane 与 Host Parity

#### EE6 目标

删除 Host direct-control，使所有扩展管理通过 Runtime Protocol。

#### EE6 工作项

- 增加 Extension Operations 和 Events；
- 增加 Event Traits 和生成 Schema；
- CLI 改为提交 Operation；
- TUI、VS Code、ACP 增加统一 Projection；
- 增加 Extension List、Detail、Health、Permission 和 Receipt 查询；
- 管理操作支持幂等 Operation ID；
- 安装、更新、回滚和撤销使用 Journal/事务边界；
- 删除 CLI 对 Plugin Registry 和 Skill Store 的直接业务调用。

#### EE6 Gate

- 四个 Host 对同一 Fixture 的状态投影一致；
- 重放 Event 可以重建相同状态；
- 重复 Operation 不重复安装或撤销；
- Host 不导入 Extension 实现 Package；
- Protocol Contract 和 VS Code Generated Types 通过；
- 慢消费者不阻塞 Extension Kernel。

#### EE6 验收结果

- Runtime Protocol、Durable Operation Journal 和 Control Plane 已落地；
- Operation 使用 `prepared -> committed`，崩溃中间态通过 `reconciled`
  Receipt 对账，同一 Operation ID 不重复执行副作用；
- CLI、TUI、VS Code 和 ACP 读取同一 Projection；
- Event Replay、Receipt Query、冲突拒绝和慢消费者断开测试通过；
- 证据见 `docs/extension-ecosystem-ee6-evidence.json`。

### EE7：Hook V2、观测、迁移与最终验收

#### EE7 目标

完成 Hook、诊断、旧路径删除、攻击测试和真实 Host 验收。

#### EE7 工作项

- Hook V2 Source、Trust、Scope、Mode 和 Runtime Event；
- Extension Trace、Metrics 和 Receipt；
- VS Code Extension 面板；
- 删除旧 `extensionContributor` 和 direct-control 路径；
- 删除重复 watcher 和状态 Authority；
- 增加供应链、路径、竞态、撤销和 Sandbox 攻击测试；
- 使用 VS Code + DeepSeek 做真实长 Session 验收；
- 与 `.tmp/runtime-monitor` 历史数据比较。

#### EE7 Gate

- Security Ratchet 不低于 SG7；
- Architecture Ratchet 不低于 67/67；
- 全量 Hermetic、Race、Docs、Book、VS Code 检查通过；
- Extension 生命周期零残留；
- Host Parity 100%；
- 1,000 Skill Token Gate 通过；
- 无 Extension 场景构造 P95 增幅不超过 5%；
- 生成最终 Evidence 和升级报告。

#### EE7 验收结果

- Hook V2 已增加 Source、Trust、Scope 和 Mode，V1 配置单向规范化为 V2；
- Observe Hook 不能改变 Tool/Permission 决策，Plugin Hook 继续执行实时 Authority
  校验并使用同一 Sandbox Executor；
- Control Plane Health 提供有界 Trace、Metrics 和聚合 Alert；
- VS Code Extension Panel 已接入 ACP Projection；
- 旧 `extensionContributor` 和导出的 Plugin direct-control 已删除；
- Architecture Ratchet 为 103/103，全 Go、目标 Race、Sandbox、Docs、Book 和
  VS Code 234 项门禁通过；
- 1,000 Skill Candidate 为 1,448 Token，Critical Recall 100%，无 Extension
  P95 未回退；
- 官方 VS Code + DeepSeek 实测发现并修复了 Engine 丢弃 Hook-aware Guard 的生产
  接线缺陷；改为每个 Engine 独立构造绑定最终 Registry、Policy、Workspace、
  Journal 和 Hook Manager 的 Guard，隔离 Worktree 不继承宿主 Workspace Hook；
- 修复后的 Shared Turn 持久化 13 条脱敏 `hook.execution`，Plugin Hook 投影为
  `plugin/workspace/turn/observe`；Skill、MCP、Plugin Process Tool、一次性审批和
  Seatbelt strong sandbox 均有真实执行证据；
- 最终 Turn 已完成，Control Journal 无 `prepared` 残留，Terminal Outbox、Active
  Lease、Open Span 和短进程残留均为 0，SQLite 完整性为 `ok`；
- 实测报告见
  `.tmp/runtime-monitor/r49-extension-ecosystem-ee7-live/report.md`，修复前失败证据
  保留在同目录的 `failure-hook-audit-missing/`；
- 证据见 `docs/extension-ecosystem-ee7-evidence.json`。

## 11. 迁移策略

### 11.1 迁移顺序

建议顺序：

1. Memory：验证 Context + Tool Contributor；
2. Dynamic Tool：验证 Tool Contributor 和 Host Capability；
3. Skill：验证 Context、Scoped State 和按需读取；
4. MCP：验证动态 Catalog 和 Health；
5. Plugin Registry：验证 Package、Plan 和 Lifecycle；
6. Hook：验证策略交互和 Runtime Event；
7. Host Control Plane：最后切断旧入口。

### 11.2 V1 Plugin

迁移期：

- V1 Manifest 仍可解析；
- Resolver 将其规范化为一个 V2 executable Tool Capability；
- 生成明确 `legacy_v1` Receipt；
- 不增加 V1 新功能；
- EE7 删除生产双路径，只保留一个规范化执行器；
- 首个稳定版本前可以直接删除 V1 格式，不建立长期 Migration Database。

### 11.3 `load_skill`

- EE5 前保持现状；
- EE5 内部转为 Authority-bound Resolver；
- 裸名称只在当前 Turn 唯一时接受；
- 多 Authority 同名返回 Typed Ambiguous Error；
- Receipt 记录真实 Package 和 Digest；
- 是否删除 `load_skill` 在 EE7 根据 Provider Tool Surface 和兼容性决定。

### 11.4 MCP Config

现有独立 MCP Config 保留为一种 Source。Plugin MCP 不复制配置到全局文件，而是编译为
Plan 中的 Source-owned Contribution。

### 11.5 Hook Config

现有 `--hooks-config` 保留为 Repository Source。Plugin Hook 使用同一个 Hook V2
Compiler，不建立第二套执行器。

## 12. 测试策略

### 12.1 单元测试

- Manifest 严格解析；
- Identity、Digest 和 Namespace；
- Source Priority；
- Conflict Resolution；
- Plan Digest；
- State Scope 和 CAS；
- Selector Determinism；
- Hook Merge；
- Receipt Canonical Encoding。

### 12.2 契约测试

每类 Contributor 使用统一 Fixture 验证：

- 构造不产生副作用；
- Deadline；
- Typed Error；
- Receipt；
- Budget；
- Close 幂等；
- 不可访问未授予 Capability。

### 12.3 集成测试

- Package 同时包含四类 Capability；
- Install -> Enable -> Run -> Update -> Drain -> Rollback；
- Security Revoke；
- Restart Recovery；
- MCP Catalog Change；
- Skill Digest Drift；
- Hook Input Rewrite；
- Partial Capability Quarantine；
- Host Event Replay。

### 12.4 并发测试

- Update 与 Tool Call 竞争；
- Revoke 与 Tool Call 竞争；
- Refresh 与 Restart 竞争；
- 多 Source 同名 Capability；
- Hook 并发执行和确定性合并；
- State CAS；
- MCP Notification Storm；
- Slow Host Subscriber。

### 12.5 攻击测试

- Symlink 和 Hardlink Escape；
- TOCTOU Package Replacement；
- Manifest Zip/Tar Traversal；
- Digest Swap；
- Publisher Confusion；
- Tool Name Collision；
- Skill Handle Forgery；
- MCP Server Identity Forgery；
- Hook ID Collision；
- Permission Digest Drift；
- Control-plane Write；
- Revoked Process 继续运行；
- DNS Rebinding 和 Redirect；
- 超大 Manifest、Schema、Description、Hook Output 和 Skill Resource。

### 12.6 性能测试

- 0/10/100/1,000 Skill；
- 0/10/100 Package；
- 0/10/100 MCP Tool；
- 冷启动和热启动；
- Source 增量刷新；
- Plan Compile；
- Context Token；
- Tool Schema Bytes；
- Extension Event Fanout；
- Drain 和 Revoke Latency。

## 13. 指标与证据

每阶段 Evidence 至少包含：

```json
{
  "schema_version": 1,
  "stage": "EE5",
  "base_commit": "...",
  "candidate_commit": "...",
  "architecture_ratchet": {},
  "security_ratchet": {},
  "extension_metrics": {
    "startup_p50_ms": 0,
    "startup_p95_ms": 0,
    "refresh_p95_ms": 0,
    "plan_compile_p95_ms": 0,
    "skill_catalog_tokens_p50": 0,
    "tool_schema_bytes_p50": 0,
    "stable_prefix_cache_share": 0,
    "critical_skill_recall": 0,
    "lifecycle_leaks": 0
  },
  "validation": {}
}
```

禁止只记录聚合 Pass。必须保留：

- Fixture 参数；
- 样本数；
- Median、P95、MAD；
- 失败分类；
- 基线与候选原始 Artifact 路径；
- Git Commit；
- 操作系统和 Sandbox 能力；
- Provider/Model；
- Host 类型。

## 14. 风险与控制

| 风险 | 表现 | 控制 |
| --- | --- | --- |
| Kernel 成为新 God Object | 所有扩展逻辑集中 | Kernel 只编排，Capability 实现在 Adapter |
| 双路径长期存在 | 旧 Registry 与新 Plan 同时生效 | 每阶段定义删除 Gate |
| Extension API 过早公共化 | 被兼容性冻结 | 第一轮仅内部 Contract |
| Selector 漏掉关键 Skill | 模型无法发现能力 | Shadow、Golden Recall、显式 Mention |
| Package 能力扩大攻击面 | 多种资源共同分发 | Capability 独立 Digest 和权限 |
| 动态更新破坏 Turn 一致性 | Catalog 中途变化 | Immutable Turn Plan |
| Host 控制面迁移引入回归 | CLI 行为变化 | Protocol Fixture 和 Host Parity |
| Hook 并发改变决策顺序 | 非确定授权 | 并发执行、稳定顺序合并 |
| State Store 变成任意持久化 | Extension 写入不透明状态 | Namespace、Schema、Budget、Policy |
| Token 优化损害质量 | 相关 Skill 未投影 | Recall/质量 Gate 优先于 Token |
| 新抽象增加启动开销 | 无扩展也变慢 | Noop Fast Path 和 5% P95 Gate |

## 15. 预期代码边界

建议新增或调整的所有权：

| 责任 | 路径 |
| --- | --- |
| Typed Extension Contract | `internal/runtime/extension` |
| Extension Effect Registry 与运行期 Scope | `internal/runtime/extension` |
| Extension Kernel 编排 | `internal/runtime/app/extension` |
| Composition Root 安装 | `internal/runtime/app/wire` |
| Package/Manifest/Resolver | `internal/adapter/extension` |
| Plugin Distribution | `internal/adapter/plugin` |
| Skill Capability | `internal/adapter/skill` |
| MCP Capability | `internal/adapter/mcp` |
| Hook Capability | `internal/adapter/hooks` |
| Tool Reconcile | `internal/adapter/tool` |
| Authority 编译 | `internal/security/authority` |
| Durable Plan/Receipt | `internal/persist` |
| Operation/Event | `internal/runtime/protocol` |
| Host Projection | `internal/runtime/eventview`、`extensions/vscode` |
| Metrics/Trace | `internal/observability` |

依赖约束：

- `runtime/protocol` 不导入 Extension 实现；
- `runtime/extension` 不导入 Host、具体 Provider 或具体 Sandbox；
- `adapter/extension` 不拥有 Agent Loop；
- `wire` 不保留 Extension 业务状态；
- Host 不导入 `adapter/plugin`、`adapter/skill`、`adapter/mcp` 或
  `adapter/hooks`；
- Extension Package 不能直接依赖 Go Runtime Contract；
- 所有 Tool Registration 最终进入现有 `tool.Registry`。

## 16. 最终验收标准

只有同时满足以下条件，EE0-EE7 才可标记为完成：

1. Extension Kernel 是扩展计划和生命周期唯一 Authority；
2. Plugin V2 能声明并隔离 Skill、MCP、Hook 和 Tool；
3. Plugin Skill 在生产路径可用；
4. 所有 Host 通过 Runtime Protocol 管理扩展；
5. CLI 不再直接执行 Plugin/Skill 业务逻辑；
6. Turn 使用冻结 Extension Plan；
7. Receipt 绑定 Package、Capability、Catalog 和 Permission Digest；
8. Security Revoke 后无新调用成功；
9. 每个运行期 Effect 都绑定唯一 Extension、Plan Revision、Generation 和 Capability
   Owner；
10. 部分激活失败、更新、回滚、撤销和 Restart 后零资源残留；
11. Drain、Cancel 和 Close 幂等，超时或失败不会被投影为成功 Inactive；
12. 1,000 Skill Catalog Token P50 至少降低 80%；
13. Critical Skill Recall 为 100%；
14. 无 Extension 场景启动 P95 增幅不超过 5%；
15. Stable Prefix Cache Share 不低于 95%；
16. Security Ratchet 不低于 SG7；
17. Architecture Ratchet 不低于 67/67；
18. Hermetic、Race、Protocol、Docs、Book、VS Code 和攻击测试通过；
19. 旧 Contributor、旧 Host direct-control 和重复生命周期路径已删除；
20. 最终 Evidence 能从 Source 追踪到 Attempt Receipt 和 Effect Outcome。

## 17. 建议执行顺序

在当前 Security Governance 工作完成并形成干净提交后，从独立分支开始 EE0。不要在
未提交的 SG 工作树上同时实施 Extension Kernel，因为 EE3、EE4 和 EE6 会修改 Plugin、
Guard、Protocol、Wire 和 Host，冲突面较大。

推荐：

```text
refactor/security-governance
  -> 完成并提交 SG
  -> 创建 refactor/extension-ecosystem
  -> EE0 baseline
  -> EE1 typed API
  -> EE2 plan/state
  -> EE3 capability bundle
  -> EE4 lifecycle
  -> EE5 token/context
  -> EE6 control plane
  -> EE7 final acceptance
```

EE1-EE4 解决架构正确性，EE5 获取主要 Token 收益，EE6 获取 Host 一致性和运维收益，
EE7 完成可观测性、安全和迁移收口。阶段顺序不应倒置：在没有统一 Identity、Plan 和
Lifecycle 前先建设 Marketplace 或复杂 UI，只会扩大现有状态分裂。
