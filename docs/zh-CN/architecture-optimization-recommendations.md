# QCode 架构与模块优化建议

> 评审基线：`c370fd2f`，2026-09-04。
>
> 本文面向 QCode 维护者，讨论下一阶段架构演进，不把建议描述为已交付能力。对 Codex、
> Cursor 和 Claude Code 的分析只采用其公开文档所证明的产品契约与行为；闭源产品的内部
> 实现不可见，因此本文不推断其内部代码结构。

## 结论

QCode 当前最有价值的资产不是工具数量，而是已经成形的受治理 Runtime：单一
Operation/Event 控制面、Turn Kernel、Guard、单次 Execution Lease、Workspace Journal、
原子 Terminal Envelope、有界上下文以及共享同一运行语义的 Subagent。这套底座在安全、
恢复和证据完整性上，已经超过多数 Coding Agent 原型。

下一阶段不宜再横向堆叠 Agent Loop 能力。更值得投入的是把现有底座转化为五个产品闭环：

1. 以真实任务评测驱动架构决策；
2. 把“计划”提升为可审阅、可修改、可绑定执行的正式契约；
3. 把词法索引和按次启动的 LSP 升级为 Workspace 级代码智能服务；
4. 把 Diff、验证、Review 和回滚整合成一个 ChangeSet；
5. 把 Agent Preset、Skill、MCP、规则和生命周期策略组合为受治理的 Agent Definition。

后台任务、企业策略分发和扩展市场可以随后建设，但应继续复用 Runtime Turn，不在 Host、
Hook 或 Plugin 中建立第二条执行路径。

## 评审方法与边界

本次评审重点读取了以下代码事实来源：

- Runtime 与 Turn：`internal/runtime/app`、`internal/runtime/agent/engine`、
  `internal/runtime/agent/turnkernel`；
- 构造：`internal/runtime/app/wire`；
- 上下文与仓库理解：`internal/runtime/agent/contextview`、
  `internal/runtime/agent/repository`、`internal/persist/repoindex`、
  `internal/adapter/lsp`、`internal/adapter/tool/search`；
- 执行安全：`internal/adapter/tool/guard`、`internal/security/authority`、
  `internal/security/*broker`、`internal/security/sandbox`；
- Subagent：`internal/orchestration/subagent`、`internal/orchestration/admission`；
- 持久化与证据：`internal/persist/state`、`internal/persist/workspacejournal`、
  `internal/observability`；
- 产品与质量门禁：`web/src`、`internal/host/bench`、
  `testdata/contracts/benchmark-v2.json`、`Makefile`。

公开产品对照主要使用：

- Codex 的 App Server 将 Thread、Turn、Steer、Interrupt、Fork 和事件流公开为生命周期
  契约；Worktree 用于隔离并行工作；权限审批与 Sandbox 是两个独立控制面。
  参见 [Codex App Server](https://learn.chatgpt.com/docs/app-server)、
  [Codex Worktrees](https://learn.chatgpt.com/docs/environments/git-worktrees) 和
  [Codex Sandboxing](https://learn.chatgpt.com/docs/sandboxing)。
- Cursor 把可编辑 Plan、专用 Agent Review、精确搜索与语义搜索组合、独立 Agent 上下文
  以及插件化定制作为直接产品能力。参见
  [Cursor Plan Mode](https://cursor.com/docs/agent/plan-mode)、
  [Cursor Agent Review](https://cursor.com/docs/agent/agent-review)、
  [Understanding your codebase](https://cursor.com/learn/understanding-your-codebase) 和
  [Customize Cursor](https://cursor.com/docs/customize-cursor)。
- Claude Code 的 Subagent 定义可绑定工具、模型、Skill、MCP、Memory、Hook 和隔离方式；
  企业设置可以分别约束 Permission、Sandbox、MCP 与 Hook 来源。参见
  [Claude Code Subagents](https://code.claude.com/docs/en/sub-agents)、
  [Hooks](https://code.claude.com/docs/en/hooks-guide) 和
  [Admin setup](https://code.claude.com/docs/en/admin-setup)。

这些资料证明的是外部契约，不证明各产品内部使用了相同的包划分、数据库或状态机。

## 当前架构判断

### 已经正确的主干

| 领域 | QCode 当前实现 | 判断 |
| --- | --- | --- |
| 控制面 | Host 只提交 Operation、消费 Event；`app.Runtime` 组合窄 Service | 应保持 |
| Turn 状态 | `turnkernel.Reducer.Apply` 是状态转换入口，Effect 先持久化 | 应保持 |
| 副作用 | Trusted Binding、Guard、Execution Operation、单次 Lease、Broker | 是核心优势 |
| 终态 | Terminal Envelope 原子提交 Receipt、Usage、Session Delta、Outbox | 是核心优势 |
| 上下文 | Truth Capsule、Working Set、Evidence、Tail、Result Handle | 方向正确 |
| 多 Agent | Agent Graph、Budget、Mailbox、Worktree、Typed Settlement | 方向正确 |
| Provider | 不可变 Route、Capability、Replay Provenance、受治理 Egress | 方向正确 |
| Web | Snapshot Hydration + Event Projection，不复制业务循环 | 应保持 |

Codex 公布的 App Server 同样把 Thread/Turn 生命周期、动态工具、Fork、Steer 与终态事件
做成协议；这说明 QCode 选择“稳定 Runtime 协议，而不是 Host 内嵌 Agent 逻辑”的方向是
合理的。[Codex App Server](https://learn.chatgpt.com/docs/app-server)

### 主要结构性缺口

| 缺口 | 代码证据 | 直接影响 |
| --- | --- | --- |
| 质量评测仍以 Fixture 为主 | `internal/host/bench`、`testdata/benchmarks` | 难以判断复杂度增加是否真的提升任务成功率 |
| Plan 与执行意图耦合 | `submit_plan` 成功后在当前 Turn 自动继续 | 用户难以修改、比较或复用计划后再执行 |
| 代码智能生命周期短 | `internal/adapter/lsp/semantic.go` 每次查询启动并关闭 Language Server | 延迟高，无法持续维护增量语义状态 |
| 依赖/影响图仍较弱 | `repoindex` 主要保存词法 Symbol，Affected Test 依赖规则与命名关系 | 跨文件定位和最小验证集合精度受限 |
| Review 不是一等聚合 | Diff、Receipt、Verification、Subagent Result 分散在不同投影 | 用户缺少一个稳定的“可交付变更”对象 |
| Agent Preset 表达力有限 | `AgentPresetProfile` 主要包含模型、模式、工具、审批和步数 | 无法复用完整的角色、验证、Skill/MCP 与记忆策略 |
| 扩展缺少受治理生命周期切点 | Skill Control 有 Enable/Lock/Verify，但没有通用的 typed interceptor | 团队无法安全注入格式化、审计和完成门禁 |
| 长任务只有 Turn/Subagent | 架构明确不提供后台 Scheduler/Job | 尚无面向用户的异步排队、接管和恢复体验 |

## 建议的目标架构

```text
Web / Future Host Adapter
        |
        v
Operation / Event / Read Model
        |
        +-------------------- Plan Service -------------------+
        |                         |                            |
        v                         v                            v
Turn Service ------------ Execution Binding ------------ ChangeSet Service
        |                         |                            |
        v                         v                            v
Turn Kernel <------ Workspace Intelligence -------- Review / Verification
        |
        v
Guard -> Authority Lease -> Broker -> Journal -> Sandbox
        |
        +------------- Typed Lifecycle Interceptor -----------+

Evaluation Plane reads task corpus + Terminal Receipt + ChangeSet,
but never becomes execution authority.

Optional Work Controller owns scheduling only; every run is still a Runtime Turn.
```

这里新增的是可替换的 Service 和 Read Model，不是第二套 Agent Runtime。Turn Kernel、Guard、
Authority、Broker、Journal 与 Terminal Envelope 继续作为唯一执行主干。

## 优先级建议

### P0：先建立真实任务 Evaluation Plane

#### 为什么优先

当前 `benchmark-v2` 能验证用户旅程是否存在，Fixture Provider 能稳定验证 Runtime 接线、
恢复和工具语义，但它不能回答以下问题：

- 新的检索策略是否减少无关文件读取；
- LSP 或语义检索是否提高跨文件修改成功率；
- Agent Definition 是否降低模型方差；
- Subagent 是否提高成功率，还是只增加 Token 与冲突；
- 同一能力在不同 Provider、模型能力和 Context Window 下是否退化。

成功产品普遍把“Agent 能否自行验证工作”放在核心流程中。Cursor 的官方工作流明确把
计划拆分为独立可验证步骤，并强调测试、类型检查、Lint 与浏览器反馈组成闭环。
[Developing Features](https://cursor.com/learn/creating-features)

#### 模块设计

建议新增独立但只读执行事实的评测域：

```text
internal/evaluation/
  corpus/       任务、初始仓库、成功条件、风险标签
  runner/       通过受支持 Host/Runtime 提交普通 Operation
  grader/       确定性检查、结构化 Judge、人工标注接口
  report/       成功率、成本、延迟、干预、回滚与证据完整性
  experiment/   基线、候选、Capability/Config 摘要、可复现种子
```

如果不希望立即引入顶层 Package，可先把 `internal/host/bench` 的 Corpus、Runner 与 Report
拆开；Host 只负责启动，Runner 仍调用真实 Runtime，不直接执行 Provider 或 Tool。

每个 Case 至少记录：

- Repository Revision、Provider/Model/Capability Digest、Runtime Config Digest；
- 期望文件和禁止修改范围；
- 功能测试、静态检查和 Diff 约束；
- Turn 数、工具调用、上下文输入、缓存、Token、成本与 Wall Time；
- Approval/Input 次数、恢复次数、Rollback 结果；
- Terminal Receipt 与 ChangeSet Digest。

评测分为三条 Lane：Hermetic Contract、受控 Live Model、发布前 Canary。模型选择和通过阈值
必须来自显式实验配置和基线数据，不能按模型名硬编码档位。

#### 验收标准

- 同一 Case 可在 Fixture 与至少一个真实 Provider 上运行；
- 任意候选策略都能与固定基线做任务级对比；
- 报告能区分 Runtime 故障、模型行为失败、环境不可用和 Grader 不确定；
- Release Gate 可以只阻止统计显著或明确的契约退化，不以单次模型波动误杀发布。

### P0：把 Plan 改造成可审阅的执行契约

#### 当前问题

QCode 已有版本化 Structured Plan、文件摘要、Supersedes、Verification Evidence 和执行配置
摘要，这是很好的基础。但当前架构规定 `submit_plan` 成功后自动批准并继续同一 Turn，
使“计划产物”和“开始执行的用户意图”难以独立演进。

Cursor 的 Plan Mode 明确提供“研究 → 生成计划 → 用户审阅/编辑 → 点击构建”的边界。
[Cursor Plan Mode](https://cursor.com/docs/agent/plan-mode) 这不是要求 QCode 复制 UI，
而是说明 Plan 值得成为一等、可修改、可绑定执行的对象。

#### 契约设计

建议将计划生命周期表达为：

```text
draft -> reviewed -> approved -> executing -> completed
                   \-> superseded
                   \-> stale
```

核心对象：

- `PlanDraft`：目标、假设、步骤、依赖、风险、验证条件；
- `PlanRevision`：内容摘要、父 Revision、修改来源；
- `PlanApproval`：批准者、批准范围、过期条件；
- `ExecutionBinding`：绑定 Plan Revision、Workspace Revision、Profile Revision、
  Tool Catalog Digest、Authority Digest；
- `PlanDrift`：执行前和每个有风险步骤前计算的结构化漂移原因。

行为上区分两类：

- `plan` 模式产出 Draft 后结束 Turn，由 Web 提供编辑、批准、执行入口；
- `act/operate` 的 `adaptive` 规划可以继续使用 Turn-local 自动执行，但仍生成可追溯的
  Execution Binding。

建议落点：`internal/persist/artifact/structured_plan.go`、
`internal/adapter/tool/interact/submitted_plan.go`、`internal/security/plandrift`、
`internal/runtime/protocol`、`web/src/projection`。

#### 验收标准

- 修改计划不会伪装成新的自然语言 Turn；
- 执行只接受一个精确 Revision，旧批准不能授权新 Revision；
- Workspace、Profile、Tool 或 Authority 漂移时 fail closed，并给出可操作原因；
- 恢复后仍能区分“计划已批准但未执行”和“执行到第 N 步”。

### P1：建设 Workspace Intelligence Service

#### 当前问题

QCode 已经具备增量词法 Repository Index、Repo Map、Symbol/Definition/References 工具和
LSP Provenance。问题在于两套能力尚未形成长期服务：

- `repoindex` 的 Symbol 与 Related Test 仍主要是词法事实；
- `internal/adapter/lsp/semantic.go` 为一次查询启动、初始化、打开文档、关闭 Server；
- Working Set 选择、Affected Test、LSP 与 Repo Map 没有共享统一的代码关系图；
- 编辑后的 `didChange`、诊断、引用变化无法形成持续增量事实。

Cursor 公开的代码理解流程把精确搜索、语义搜索和后续 Grep 组合使用，并让 Explore
Subagent 在独立 Context 中压缩发现，而不是用单一检索手段替代全部代码理解。
[Understanding your codebase](https://cursor.com/learn/understanding-your-codebase)

#### 模块设计

建议定义 Runtime 所需的窄 Port，由 Adapter 实现：

```text
internal/runtime/agent/codeintel/
  QueryPort       Symbol、Definition、Reference、Call/Import、Related Test
  Snapshot        revision、freshness、provider、confidence、omission
  Selection       为 Working Set 和 Verify 提供有解释的候选

internal/adapter/lsp/session/
  Manager         每 Workspace/Language 复用 Language Server
  DocumentSync    didOpen/didChange/didClose 与 Journal Revision 对齐
  Health          crash、restart、timeout、capability negotiation

internal/persist/codegraph/
  Node/Edge       file、symbol、import、reference、test、build target
  Provenance      lexical、LSP、build metadata、observed test
```

查询策略保持“精确事实优先”：Symbol/LSP/Build Graph → 文本搜索 → 可选语义召回。语义检索
只做候选生成，不直接产生 `verified/current` Evidence；所有结果携带 Snapshot Revision、
Provider、Confidence 和 Omission。

LSP 进程由 `wire` 的 Platform/Background Module 构造和关闭，但 LSP 仍是 Adapter，不能让
Language Server 进入 Runtime Authority。进程必须继续经过 Sandbox 与 Process Broker。

#### 验收标准

- 连续查询复用 Language Server，且 Crash 后可重启；
- 文件编辑、回滚、Worktree 切换会使旧 Snapshot 明确 stale；
- Affected Test 能解释每条“文件 → 测试/目标”边；
- 在真实任务基线上证明读取文件数、定位延迟或验证精度至少一项改善，否则不默认启用
  Semantic Retrieval。

### P1：以 ChangeSet 统一变更、验证与 Review

#### 当前问题

QCode 已经分别保存 Workspace Journal、Diff、Verification Receipt、Terminal Receipt、
Artifact 和 Subagent Integration，但用户最终要判断的是一个问题：“这组变更是否可以
接受？”当前缺少对应的一等聚合对象。

Codex 和 Cursor 都把 Review 作为独立、只读的产品流程：Codex `/review` 对选定 Diff
运行专用 Reviewer 且不修改工作树；Cursor Agent Review 可以手动或自动运行，并能按整个
本地变更集而非最后一次编辑审查。参见
[Codex Code Review](https://learn.chatgpt.com/docs/code-review) 和
[Cursor Agent Review](https://cursor.com/docs/agent/agent-review)。

#### 模块设计

建议新增 `ChangeSet` Read Model：

```text
ChangeSet
  identity: workspace + baseline + mutation revision
  intent: goal + plan/execution binding
  changes: files + hunks + generated artifacts
  verification: commands + inputs + results + coverage gaps
  review: findings + severity + file/line + disposition
  safety: approvals + leases + non-file side effects
  recovery: journal draft + rollback eligibility/conflicts
  provenance: terminal envelope + receipt digests
```

`ChangeSetService` 只聚合权威事实，不直接执行 Git 或工具。Review 作为普通只读 Turn 或
`RoleReview` Child 执行，输入冻结到 ChangeSet Digest，输出 Typed Findings。自动 Review
应由显式 Profile/Policy 开启，并受成本预算约束。

Web 围绕 ChangeSet 提供文件级接受/拒绝、Finding 处置、验证证据和回滚入口。真正写入仍
通过已有 VCS/File Broker，不允许 Projection 直接改 Workspace。

#### 验收标准

- Review 输入可以按 Digest 重放；
- Finding 可稳定关联文件和行，过期 Diff 自动标记 stale；
- “测试通过但覆盖不完整”“Review 无问题但验证未运行”保持不同状态；
- 用户可以从 ChangeSet 追溯到 Plan、Operation、Lease、Journal 和 Terminal Receipt。

### P1：把 Agent Preset 升级为 Agent Definition

#### 当前问题

`AgentPresetProfile` 已覆盖 Mode、Provider、Model、Reasoning、Tool、Approval、Target 和
MaxSteps；`DefaultRoleCatalog` 则在代码中固定角色、指令、工具类别与是否允许委派。Skill、
MCP、Memory 和验证策略分属其他配置面。用户无法保存“完整的调试 Agent”或“只读安全
Review Agent”并可靠复用。

Codex 将 `AGENTS.md`、Memory、Skill、MCP 与 Subagent 视为互补层；Claude Code 的
Subagent Definition 也能分别声明工具、模型、Skill、MCP、Memory、Hook 和 Worktree
隔离。参见 [Codex Customization](https://learn.chatgpt.com/docs/customization/overview)
和 [Claude Code Subagents](https://code.claude.com/docs/en/sub-agents)。

#### 契约设计

建议新增版本化 `AgentDefinition`：

- Identity：ID、Revision、Scope、Source、Digest、签名状态；
- Behavior：Instruction References、Output Contract；
- Capability：Tool Selector、Required Skill、MCP Dependency；
- Runtime：Purpose-based Model Selector、Reasoning、Budget；
- Context：Memory Scope、Context Fork Mode、Repository Rule Scope；
- Quality：Verification Policy、Review Policy；
- Orchestration：Role、Delegation Policy、Worktree Stance；
- Security：请求的能力，不是最终授权。

最终权限按交集计算：

```text
Definition Request
  ∩ Parent Authority
  ∩ Workspace Policy
  ∩ Trusted Tool Binding
  ∩ Platform Capability
= Effective Agent Profile
```

Definition 不得携带可扩大权限的原始 Sandbox 开关。Skill/MCP 缺失时，`required` 依赖应
阻止启动；可选依赖应进入 Degraded 状态。Codex App Server 对 Required MCP 初始化失败
采用拒绝启动的公开语义，也支持恢复 Thread 时保留动态工具元数据。
[Codex App Server](https://learn.chatgpt.com/docs/app-server)

建议落点：`internal/runtime/protocol/agent_definition.go`、
`internal/persist/agentpreset`（迁移为 definition store）、
`internal/runtime/app/wire`、`internal/orchestration/subagent`、Web Settings。

#### 验收标准

- Main Agent 与 Child Agent 使用同一 Definition 编译器；
- Definition 只能收窄 Authority；
- 每个 Turn Receipt 记录 Definition ID、Revision 与 Effective Profile Digest；
- Skill/MCP/Model 能力变化后，恢复行为确定且可解释。

### P2：提供受治理的 Typed Lifecycle Interceptor

#### 目标

团队常见需求包括：编辑后格式化、运行组织检查、提交前扫描、Tool 审计、完成前确认测试、
Subagent 启停记录。Cursor 和 Claude Code 都公开了生命周期 Hook；Claude Code 还区分
确定性 Command Hook、模型判断 Hook 和 Agent Hook，并建议生产流程优先使用确定性 Hook。
[Claude Code Hooks](https://code.claude.com/docs/en/hooks-guide)

QCode 不应直接复制“任意脚本插入 Agent Loop”。这会绕过已有 Guard、Journal 和 Sandbox。
建议定义有限事件和三种返回类型：

| 类型 | 能力 | 是否能阻止流程 |
| --- | --- | --- |
| Observer | 接收脱敏事件，写审计或指标 | 否 |
| Advisor | 返回结构化提示或诊断 | 否 |
| Gate | 返回 allow/deny/retry + reason | 是，仅限声明的 Gate Point |

首批 Gate Point 只包括 `before_tool`、`after_mutation`、`before_terminal`、
`before_integration`。Interceptor 的任何命令都注册成受信 Tool Binding，继续经过 Guard、
Lease、Broker、Journal 和 Sandbox；它没有独立 Shell 通道。

每个 Interceptor 必须声明输入 Schema、Timeout、Failure Policy、Required Controls、
Output Schema 与版本。默认 Fail-open 只适用于 Observer；安全与质量 Gate 应显式选择
Fail-closed，并把失败写入 Receipt。

### P2：将后台工作放在 Runtime 外围，而不是复制 Runtime

Codex 用 Worktree 隔离并行 Chat，并允许前后台之间交接；Cursor Cloud Agent 使用独立
环境运行、验证并产出 PR/Artifact。参见
[Codex Worktrees](https://learn.chatgpt.com/docs/environments/git-worktrees) 和
[Cursor Cloud Agents](https://cursor.com/docs/cloud-agent)。

QCode 的“Runtime 内不建立 Scheduler/Job Queue”约束应保留。若要支持异步长任务，建议
增加一个可选 Work Controller：

```text
WorkRequest -> Controller queue/lease -> Workspace/Worktree allocation
            -> ordinary Runtime Operation/Turn -> Terminal Envelope
            -> ChangeSet/Artifact -> notification/handoff
```

Controller 只拥有调度、租约、通知和环境准备，不拥有 Model/Tool/Sandbox 逻辑。运行身份、
网络、Secret、构建缓存和 Worktree 都通过显式 `EnvironmentManifest` 冻结到执行记录。
前台接管不迁移未提交的内存状态，只通过 Thread/Checkpoint/ChangeSet 恢复。

初期可以只实现本机 Supervisor 内的“排队并在专用 Worktree 执行”，不引入远端控制面。
没有真实任务数据证明需求前，不建议建设 Cloud Fleet。

### P2：补齐扩展与企业治理的供应链契约

`Skill Catalog + Lock + Verify`、MCP `host_trusted` 和 Trusted Binding 已经提供良好基础。
下一步应统一 Extension Manifest：

- 来源、版本、内容摘要、签名与更新通道；
- Skill、MCP、Agent Definition、Interceptor 的依赖图；
- 请求的 Tool/Network/File/Process 能力；
- 安装、启用、撤销、升级和回滚 Receipt；
- Workspace/User/Managed Scope 与优先级；
- 离线镜像和允许来源。

Cursor 将 Plugin 作为 Rules、Skills、Subagents、Commands、MCP 与 Hooks 的组合包；Claude
Code 的企业配置则分别限制 Permission、Sandbox、MCP、Plugin Source 与 Hook Source。
参见 [Customize Cursor](https://cursor.com/docs/customize-cursor) 和
[Claude Code Admin setup](https://code.claude.com/docs/en/admin-setup)。

QCode 的 Manifest 应只描述请求能力，安装成功也不代表获得执行权限。Managed Policy 与
用户配置冲突时，编译结果必须展示来源和拒绝原因，Receipt 中保留 Effective Controls。

## 模块边界调整建议

| 现有模块 | 建议 | 原因 |
| --- | --- | --- |
| `internal/runtime/agent/engine` | 继续只做 Turn 协调；通过 Port 使用 Plan、CodeIntel、Review | 防止 Engine 再次吸收产品域逻辑 |
| `internal/runtime/app` | 增加 Plan/ChangeSet/Definition 的窄 Service | 统一 Operation、幂等和 Host Query |
| `internal/runtime/app/wire` | 只编译并注入新 Service，不执行评测或 Hook 业务 | 保持 Composition Root 纯粹 |
| `internal/adapter/lsp` | 升级为可复用 Session Manager | 降低查询延迟并维护增量语义状态 |
| `internal/persist/repoindex` | 保留词法事实；关系图独立存储或显式版本升级 | 避免把多来源边强塞进 Symbol 表 |
| `internal/observability/verify` | 消费 CodeGraph 和 ChangeSet，但不拥有执行 | Verification 仍是证据判定域 |
| `internal/orchestration/subagent` | 消费 Effective Agent Definition | Main/Child 配置与权限收窄一致 |
| `internal/adapter/tool/guard` | 增加 Interceptor Gate 的统一授权入口 | 不产生 Hook 旁路 |
| `web/src/projection` | 只投影 Plan/ChangeSet/Work 状态 | Web 不成为事实来源 |

不建议为了缩短文件而机械拆分 `runtime.go` 或 `turn_handler.go`。应先形成明确的新 Owner，
再把对应职责迁移出去；现有 Hotspot Baseline 继续检查职责归属，而不是恢复行数棘轮。

## 小步实施路线

### 拆分原则

下面每一步都按“一个可独立 Review、验证和回滚的变更”设计，不要求一个版本完成整条主线。
实施时遵守以下约束：

- 每个变更只引入一个主要概念；协议、持久化、Runtime 接线和 Web 默认分开提交；
- 先增加不改变现有行为的类型、只读接口和观测，再切换写路径或默认行为；
- 新旧路径并存时使用显式 Capability 或配置选择，不根据模型名、仓库规模做隐式判断；
- 数据结构先提供版本、校验和摘要，再增加 Store；有持久化格式变化时单独评审兼容策略；
- 每一步都运行最近包测试和 `git diff --check`；涉及 Runtime、Web 或文档时再运行对应门禁；
- 任一步的指标没有改善，或证据链出现退化，就停在当前可工作的状态，不继续扩大改动。

### 里程碑 A：冻结基线，不改变 Agent 行为

#### A1：固化现有 Benchmark 报告样本

- 状态：已完成。基线见
  [`testdata/benchmarks/baseline-v1.json`](../../testdata/benchmarks/baseline-v1.json)，生成和刷新方法见
  [`testdata/benchmarks/README.md`](../../testdata/benchmarks/README.md)。
- 改动：为当前 `internal/host/bench` 报告补充一个脱敏的基线样本和生成说明；记录任务数、
  成功条件、Turn、工具调用、Token/成本字段的当前可用性。
- 主要落点：`internal/host/bench`、`testdata/benchmarks`。
- 验证：`make benchmark-v2`；连续运行两次，确定性字段一致。
- 不做：不改 Runner、不接真实模型、不新增发布阈值。

#### A2：定义 Evaluation Case 与 Result 纯类型

- 状态：已完成。协议与校验位于 `internal/evaluation/protocol`，当前未接入任何执行或持久化路径。
- 改动：新增只包含结构体、版本和 `Validate` 的 `internal/evaluation/protocol`；字段先覆盖现有
  Fixture 能提供的事实。
- 验证：表驱动测试覆盖合法值、未知版本、缺少 Revision/Digest 和非法状态。
- 不做：不接数据库、不改 `internal/host/bench`。

#### A3：增加现有 Task 到 Evaluation Case 的适配器

- 改动：把现有 `bench.Task` 映射到新 Case；保持原 Task JSON 和执行路径不变。
- 验证：对全部 Fixture 做 round-trip/等价断言，原 `make benchmark-v2` 结果不变。
- 不做：不删除旧类型，不调整 Benchmark 清单。

#### A4：从现有 Receipt 生成 Evaluation Result

- 改动：新增纯聚合函数，从 Terminal Receipt 和 Benchmark 结果生成 Result；缺失指标显式写成
  unavailable，而不是填零。
- 验证：Fixture 测试分别覆盖成功、工具失败、预算耗尽、恢复和证据缺失。
- 不做：不新增 Grader，不改变 Runtime 终态提交。

#### A5：输出机器可比较的 Evaluation Report

- 改动：在现有 Benchmark 命令旁增加可选 JSON Report；排序稳定，包含 Case/Config/Result
  Digest。
- 验证：Golden Test 加同一输入输出稳定性检查；`make benchmark-v2` 继续兼容旧调用。
- 不做：不把单次结果设成 Release Gate。

**里程碑退出条件：** 已能用同一格式描述现有 Fixture 结果，且 A1 基线没有行为变化。后续所有
架构优化都先声明要改善的 Case 和指标。

### 里程碑 B：先建立只读 ChangeSet

#### B1：定义 ChangeSet V1 协议

- 改动：只定义 `ChangeSetID`、Baseline、Mutation Revision、File Change、Verification Summary、
  Receipt Digest 和 Staleness；增加版本化校验。
- 主要落点：`internal/runtime/protocol`。
- 验证：协议 Schema/JSON round-trip/Validate 测试。
- 不做：不加入 Review Finding、回滚命令或 Web UI。

#### B2：实现 ChangeSet 纯聚合器

- 改动：从已有 Workspace Journal、Diff、Verification 与 Terminal Receipt 的只读快照构造
  ChangeSet；聚合器不访问文件系统、不执行 Git。
- 主要落点：建议新增 `internal/runtime/agent/changeset`。
- 验证：使用内存 Fixture 验证排序、Digest、缺失证据与 stale 判断。
- 不做：不持久化新的事实副本。

#### B3：增加 ChangeSet 查询 Service

- 改动：在 `internal/runtime/app` 暴露按 Workspace/Turn 查询 ChangeSet 的窄 Service，由
  `wire` 注入现有只读 Store。
- 验证：App Service 测试覆盖不存在、已完成、恢复后和权限不可见场景。
- 不做：不增加新的写 Operation。

#### B4：增加 Web 协议与客户端查询

- 改动：只增加 HTTP/Runtime Client DTO 与查询方法；TypeScript 类型与 Go 协议保持一致。
- 验证：`internal/host/runtimeapi/web` 测试和 `web/src/runtime/client.test.ts`。
- 不做：不渲染新面板。

#### B5：显示最小 ChangeSet 摘要

- 改动：Web 只显示文件列表、验证状态和 Receipt 可追溯链接；复用现有 Diff 组件。
- 验证：组件测试覆盖空变更、部分验证、stale 和完成态；执行 Web 资产检查。
- 不做：不提供接受、拒绝、回滚或自动 Review。

**里程碑退出条件：** 用户能从一个完成 Turn 进入稳定的 ChangeSet 摘要，并追溯现有权威
事实；删除 ChangeSet Read Model 不影响 Runtime 执行。

### 里程碑 C：把 Plan 与执行意图分离

#### C1：为当前 Structured Plan 补 Revision 契约测试

- 改动：只补充当前创建、Supersedes、恢复、摘要和 `submit_plan` 后续行为的 Characterization
  Test，先冻结真实语义。
- 主要落点：`internal/persist/artifact`、`internal/adapter/tool/interact` 及最近的 Engine 测试。
- 验证：新测试在不改生产代码时通过。
- 不做：不改变自动继续行为。

#### C2：定义 Plan Status 与合法转换

- 改动：新增纯状态机 `draft/reviewed/approved/executing/completed/superseded/stale`；当前 Plan
  在适配层映射为兼容状态。
- 验证：Reducer 表驱动测试覆盖全部合法和非法转换。
- 不做：不写 Store、不新增用户入口。

#### C3：定义 Execution Binding

- 改动：新增版本化类型，绑定 Plan Revision、Workspace Revision、Profile Revision、Tool
  Catalog Digest 与 Authority Digest。
- 验证：Validate/Digest 测试证明任一输入变化都会改变 Digest。
- 不做：Runtime 暂不要求 Binding。

#### C4：实现 Plan Drift 纯判定器

- 改动：比较 Binding 与当前快照，返回结构化 Reason Code；无副作用。
- 主要落点：`internal/security/plandrift`。
- 验证：分别覆盖 Workspace、Profile、Tool、Authority 漂移及完全一致。
- 不做：不阻止执行。

#### C5：让自适应执行记录 Binding

- 改动：保持 `act/operate` 当前体验，只在计划自动继续时生成 Execution Binding，并写入现有
  Artifact/Receipt 可追溯字段。
- 验证：Engine 恢复与 Terminal Envelope 测试；旧请求的终态保持兼容。
- 不做：不启用 drift fail-closed。

#### C6：增加 Draft/Approve/Execute Operation

- 改动：协议和 App Service 支持三个独立 Operation；先由测试客户端调用，不接 Web。
- 验证：幂等、Revision Conflict、旧批准不能授权新 Revision、恢复边界测试。
- 不做：不删除 `submit_plan` 兼容路径。

#### C7：在独立 Plan 模式启用 Drift 拒绝

- 改动：只对新 Plan Operation 执行 fail-closed；`act/operate` 保持既有自适应语义。
- 验证：每种 Reason Code 都有 Runtime 事件和可操作错误；确认没有副作用发生。
- 不做：不全局切换默认行为。

#### C8：增加最小 Web 审阅流程

- 改动：先支持查看 Revision、批准和执行，再单独增加编辑与 Supersede。
- 验证：UI 测试覆盖并发 Revision Conflict、stale、刷新恢复和重复点击。
- 不做：不在同一变更中重做 Plan 编辑器样式。

#### C9：评估并收敛旧 `submit_plan` 路径

- 改动：用 Evaluation Report 比较新旧路径；只有兼容调用已迁移后，才单独提交弃用或语义
  收敛变更。
- 验证：全量 Runtime Contract、Web Journey 和恢复测试。
- 不做：没有迁移证据时不删除旧协议。

**里程碑退出条件：** `plan` 模式可以停在可编辑 Draft，批准精确绑定 Revision，发生漂移时
在任何工具副作用前拒绝；`act/operate` 没有意外回归。

### 里程碑 D：逐步持久化 LSP 会话

#### D1：记录当前 LSP 冷启动基线

- 改动：只增加初始化、查询和关闭耗时观测，不改变一次查询一次进程的行为。
- 验证：Fake Server 测试事件完整；真实可选测试记录冷启动与查询延迟。
- 不做：不设未经实验的超时或性能阈值。

#### D2：抽取 LSP Session 接口

- 改动：把 initialize/open/query/shutdown 包成窄 Session Port；现有实现原样适配。
- 验证：原 Semantic Tool 测试不变，新增 Fake Session 调用顺序测试。
- 不做：不复用进程。

#### D3：实现单语言单 Workspace 的 Session Manager

- 改动：只支持已有的一种生命周期键；Manager 负责 acquire/release 和 Runtime 关闭时 shutdown。
- 验证：并发 acquire 只初始化一次，关闭恰好一次；运行 race test。
- 不做：不处理崩溃重启、不做多 Worktree 共享。

#### D4：接入只读查询复用

- 改动：Definition/Reference/Hover 走 Manager；编辑同步仍使用旧的按次打开方式或显式刷新。
- 验证：连续查询结果一致，初始化次数下降；Evaluation 对比定位延迟。
- 不做：不实现 `didChange`。

#### D5：增加 Document Revision 与同步

- 改动：把 Journal/Workspace Revision 映射到 `didOpen/didChange/didClose`，每个查询返回 Snapshot
  Revision。
- 验证：编辑、回滚和外部文件变化使旧结果 stale；Fake Server 检查消息顺序。
- 不做：不建立持久 CodeGraph。

#### D6：增加 Crash/Restart 状态机

- 改动：进程退出后标记 unavailable/stale；下一次显式 acquire 可以重启，失败原因写入观测。
- 验证：注入崩溃、半初始化、超时和 Runtime 关闭竞态；运行 race test。
- 不做：不无限重试，重试策略来自显式配置。

#### D7：通过 Capability 灰度启用 Session Manager

- 改动：`wire` 根据显式 Capability 选择 legacy/session 实现，默认值由评测结果决定。
- 验证：两条实现跑同一语义工具契约；关闭能力即可回退。
- 不做：不删除 legacy 实现。

**里程碑退出条件：** 在真实任务上证明初始化次数和查询延迟改善，编辑/回滚不会返回未标记
的旧结果，崩溃不影响 Runtime 终态完整性。

### 里程碑 E：从只读 Review 开始扩展 ChangeSet

#### E1：定义 Review Input 与 Finding 协议

- 改动：Input 冻结 ChangeSet Digest；Finding 只含 Severity、Confidence、File/Line、Body、
  Evidence 和 Disposition。
- 验证：版本、定位范围、Digest 和 stale 测试。
- 不做：不运行 Reviewer。

#### E2：实现只读 Reviewer Profile

- 改动：复用现有 Role/Authority，编译一个不能获得变更工具的 Profile；Review 仍是普通 Turn。
- 验证：权限交集测试证明请求写工具也不会进入 Effective Profile。
- 不做：不新增旁路模型调用。

#### E3：运行手动 Review 并回写聚合结果

- 改动：新增显式 Review Operation；输出作为 Artifact/Typed Result，由 ChangeSet 聚合，不修改
  Workspace。
- 验证：幂等、预算耗尽、输入 Digest 过期、Reviewer 失败和恢复测试。
- 不做：不自动触发。

#### E4：在 Web 显示和处置 Finding

- 改动：先显示 Finding 和 stale 状态，再单独增加 accept/dismiss/fixed Disposition。
- 验证：行号映射、Diff 变化、刷新和无 Finding 场景。
- 不做：Finding 处置不直接编辑代码。

#### E5：用评测决定是否提供自动 Review

- 改动：先记录手动 Review 的命中率、误报率和成本；自动触发仅作为显式 Profile Policy。
- 验证：Evaluation Report 分开展示任务成功、Finding 质量和额外成本。
- 不做：不默认对每个 Turn 自动 Review。

### 里程碑 F：最后开放 Agent Definition 与扩展点

#### F1：定义 Agent Definition V1，但只覆盖现有 Preset 字段

- 改动：增加版本、Scope、Source、Digest，把 `AgentPresetProfile` 无损编译为 Definition。
- 验证：全部现有 Preset Fixture round-trip，Effective Session Profile 不变。
- 不做：不增加 Skill、MCP、Memory 或 Hook。

#### F2：让 Main 与 Child 共用纯 Definition Compiler

- 改动：先抽取“请求能力与父权限求交”的纯函数；旧入口调用它，但输出必须与当前一致。
- 验证：Main/Child 表驱动测试、权限单调收窄 Property Test。
- 不做：不改默认角色目录。

#### F3：每次只增加一种可选声明

- 改动顺序：Required Skill → MCP Dependency → Verification Policy → Memory Scope → Delegation
  Policy；每种声明单独增加协议、编译、Receipt 和恢复测试。
- 验证：缺失 required 依赖 fail closed，可选依赖进入 degraded；Definition 不能扩大 Authority。
- 不做：不把五种声明放在同一 Schema 变更中。

#### F4：先实现 Observer Interceptor

- 改动：只发送脱敏的 `after_tool`/`after_terminal` 事件，禁止返回决策和执行命令。
- 验证：顺序、超时、失败隔离和 Receipt 关联测试。
- 不做：不实现 Advisor/Gate。

#### F5：增加无副作用 Advisor

- 改动：允许返回结构化诊断，由 Engine 决定是否放入上下文；输出有 Schema、来源和预算。
- 验证：无效输出、超时和上下文预算测试。
- 不做：Advisor 不能阻止流程。

#### F6：逐个增加 Gate Point

- 改动顺序：`before_terminal` → `after_mutation` → `before_integration` → `before_tool`；每个
  Gate 单独提交，命令型实现必须注册 Tool Binding 并经过 Guard。
- 验证：allow/deny/retry、fail-open/fail-closed、恢复和无旁路副作用测试。
- 不做：不提供任意 Shell Hook。

#### F7：统一 Extension Manifest

- 改动：在 Definition 和 Interceptor 稳定后，再把 Skill/MCP/Definition/Interceptor 的来源、
  Digest、签名和请求能力放入统一 Manifest；安装与授权保持分离。
- 验证：来源优先级、撤销、升级、降级和 Effective Controls Receipt。
- 不做：不同时建设远端市场或自动更新服务。

#### F8：用数据决定是否建设 Work Controller

- 改动：先从现有 Turn 记录统计排队、长运行、接管和 Worktree 冲突需求；若成立，只提交
  WorkRequest/WorkStatus 协议和内存 Fake Controller。
- 验证：Controller 只能调度普通 Runtime Operation，不能直接访问 Provider/Tool/Sandbox。
- 不做：首个版本不引入远端 Fleet、独立 Agent Loop 或第二套 Journal。

### 每个小步的统一完成定义

每个变更在合并前都应满足：

1. 变更说明明确一个 Owner、一个主要行为和明确的非目标；
2. 新协议具有版本、Validate、稳定排序或 Digest 测试；
3. 新副作用能够追溯到 Operation、Lease、Journal 与 Terminal Receipt；
4. 新能力默认关闭或与旧行为等价，直到 Evaluation 证明可以切换；
5. 最近包测试、`go test ./internal/...`、`git diff --check` 通过；Web 变更增加前端测试并运行
   `make web-assets-check`，协议/旅程变化运行 `make benchmark-v2`；
6. 若仓库级门禁因既有基线问题失败，记录精确失败项，并证明本变更没有新增失败；
7. 回滚该提交不会要求数据修复，也不会破坏旧客户端或未完成 Turn 的恢复。

## 需要配套的 Architecture Decision Record

建议在实现前分别记录以下决策，避免多个大功能一起改写 Runtime：

1. Plan Draft 与 Execution Binding 的状态机、过期与恢复语义；
2. Code Intelligence Snapshot 的 Revision 和 Staleness 模型；
3. ChangeSet 的身份、基线和多 Turn 聚合边界；
4. Agent Definition 的 Scope、优先级和 Authority Intersection；
5. Interceptor 的 Gate Point、失败策略和副作用路径；
6. Evaluation Case、Grader 与统计比较契约；
7. Work Controller 与 Runtime Turn 的所有权边界。

## 不建议照搬的设计

- 不默认引入联网、自动执行所有命令的远端 Agent。Cursor 也明确说明网络开放和自动运行
  会增加 Prompt Injection 与数据外泄风险；QCode 应继续以显式 Egress 和 Sandbox 为准。
  [Cursor Cloud Agent Secrets & Network](https://cursor.com/docs/cloud-agent/security-network)
- 不把向量检索当成代码理解的唯一入口。精确 Symbol、Reference、Build Graph 和文本搜索
  更容易验证，语义检索适合补召回。
- 不允许 Hook 直接调用宿主 Shell。任何副作用必须继续经过 Trusted Binding 与 Guard。
- 不让 Plugin 安装等同于获得权限。安装、启用、授权和执行应是四个不同事实。
- 不为后台任务复制一套 Task State Machine、Tool Executor 或 Persistence Journal。
- 不根据模型名称硬编码路由档位、上下文阈值或 Subagent 数量；使用 Capability、显式配置、
  评测结果与运行时状态。

## 成功指标

建议用以下指标判断架构优化是否有效：

| 目标 | 指标 |
| --- | --- |
| 仓库理解 | 首次正确定位率、读取文件数、检索 P95、过期结果率 |
| 修改质量 | 任务成功率、无关文件修改率、回滚率、人工修正轮次 |
| 验证质量 | Affected Test 命中率、漏测率、不可验证任务比例 |
| 计划质量 | Plan 修改率、漂移拒绝准确率、按计划完成率 |
| Review | 有效 Finding 命中率、误报率、Finding 修复率 |
| 长任务 | 恢复成功率、重复副作用率、平均接管时间 |
| 多 Agent | 成功率增益、Token 增量、冲突率、空转/重复调查率 |
| 安全 | 越权拦截率、误批准率、Egress 拒绝可解释率、Receipt 完整率 |
| 经济性 | 每个成功任务的 Token、成本、Wall Time 与缓存收益 |

指标必须按任务类型、Provider Capability、模型、仓库规模和执行环境分层，不以一个总分
掩盖结构性退化。

## 最终建议

QCode 已经有一条可信的执行主干。未来竞争力主要取决于：能否更快找到正确代码、能否在
执行前把意图变成可审阅契约、能否把变更与验证组织成可接受的交付物，以及能否用真实
任务证明确实变得更好。

因此，建议把“Evaluation Plane + Plan Contract + Workspace Intelligence + ChangeSet”
作为下一阶段主线；Agent Definition、Typed Interceptor 和后台 Work Controller 建立在这
条主线上。这样既能吸收 Codex、Cursor、Claude Code 的成功产品经验，又不会牺牲 QCode
现有最独特的 Guard、Journal、Recovery 与单一 Runtime 架构。
