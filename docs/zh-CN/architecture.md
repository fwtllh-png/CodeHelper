# 架构与安全设计

简体中文 | [English](../en/architecture.md)

## 架构目标

CodeHelper 保持一个权威执行 Runtime，同时允许多种呈现和集成入口。Host 提交
Operation 并观察 Event，不复制 Agent 循环，也不直接执行特权工具。

受支持的产品 Host 是 CLI、TUI、VS Code 和 ACP。项目不提供 `codehelper web`、
`codehelper serve`、Embedded Browser UI、Pairing/QR Flow 或 REST/SSE Host。MCP
Stdio Serving 和内部 Loopback Helper 属于集成机制，不是产品 HTTP Host。

```text
CLI / TUI / VS Code / ACP
                 |
           Operation / Event
                 |
        Runtime Application State
                 |
             Agent Engine
        /          |           \
   Context      Provider     Guarded Tool
                               |
       Policy -> Approval -> Journal -> Sandbox
                 |
       Persistence + Observability
```

## 包分层

| 层 | 路径 | 职责 |
| --- | --- | --- |
| 入口 | `cmd/codehelper` | 进程上下文和 CLI 入口 |
| Host | `internal/host` | 用户/客户端 I/O 与呈现 |
| Runtime | `internal/runtime` | 协议、应用状态、Agent 循环、装配 |
| Adapter | `internal/adapter` | 模型、Provider、Tool、MCP、Skill、Plugin、Hook |
| Security | `internal/security` | Policy、Permission、Constitution、Sandbox |
| Orchestration | `internal/orchestration` | Task、Worker、Automation、Workflow、Lane、Fleet |
| Persistence | `internal/persist` | 关系状态、Event、CAS、Session、Snapshot、Journal |
| Observability | `internal/observability` | Usage、Trace、Diagnostics、Verify、Telemetry |
| Platform | `internal/platform` | 进程、PTY、操作系统差异 |
| Configuration | `internal/config` | 默认值、TOML、环境变量、校验、Provenance |

## 硬依赖规则

1. `runtime/protocol` 不依赖其他实现包。
2. Host 不直接 Import 并调用 Provider、Tool、Sandbox 或 Agent Engine 实现。
3. Model/Tool/Security 的构造属于 `runtime/app/wire`。
4. Turn 业务循环属于 `runtime/agent`。
5. 所有有副作用工具都经过 `adapter/tool/guard`。
6. UI State 是 Projection，不是 Runtime 事实来源。
7. 持久化写入在所属边界内使用事务或 Journal。

Architecture Test 会检查重要 Import 限制。需要违反这些规则的设计必须先进行显式架构
调整，不能用局部捷径绕过。

## Runtime 协议

协议定义位于 `internal/runtime/protocol`，生成后的公开 Schema 位于
`docs/protocol/runtime-protocol.schema.json`。

概念模型：

- **Operation**：请求的状态转换，例如开始或取消 Turn。
- **Event**：状态转换产生的不可变事实。
- **Receipt**：上下文、工具、变更、审批、验证或成本的结构化证据。
- **Projection**：由 Event 和关系记录重建的查询状态。

ACP 是共享模型的编辑器 Transport Envelope。除非是有意的 Host 呈现差异，只在一种
Host 中存在的功能都不完整。

## Turn 数据流

1. Host 校验用户输入并提交 Operation。
2. Application 解析 Session、Thread、Workspace 和 Policy。
3. Prompt Context 组装 Repo Map、Pin 文件、Working Set、Evidence、Policy 与压缩历史。
4. Provider 流式返回模型输出与工具请求。
5. Tool Request 进入 Registry 和 Guard。
6. Guard 评估 Mode、Posture、Permission、Constitution、Approval 与 Sandbox。
7. 修改型工具通过 Journal/事务 Adapter 写入。
8. Verify 收集诊断或执行仓库检查。
9. Event 与 Receipt 被持久化，并投影到各 Host。

Cancel 和 Failure 是明确终态，不是“没有返回数据”。

## 持久化

Durable State 由多个明确组件组合：

| 组件 | 用途 |
| --- | --- |
| SQLite | Session、Turn、Task、Usage、Trace、Workflow 关系 Projection |
| Event Log | 有序 Runtime 事实 |
| CAS | 不可变内容寻址 Payload |
| Session Metadata | 面向用户的 Session/Thread 组织 |
| Workspace Journal | Before Image 与编辑恢复 |
| Snapshot | 显式 Thread 状态检查点 |

SQLite 当前是初始 Schema。未来公开版本变更必须使用显式 Migration；首次基线前的开发
迁移历史已经有意压缩。

Session Checkpoint 与 Plan Artifact 复用 Snapshot Index 和 CAS。Checkpoint 只保存
经过校验的 Model-visible History Baseline 与 Profile Snapshot；Restore 不能执行历史
Event。持久化 Restore/Fork Event 保证重启重建结果确定。Fork 血缘与当前 Active
Session Thread 属于关系型 Lifecycle State，而不是 Host-local State。

## 上下文架构

上下文按稳定性和用途拆分：

- 稳定 Coding Policy 与系统约束；
- Repo Map 与 Symbol Index；
- 用户 Pin 文件；
- 持续演进的 Working Set；
- Evidence 与未解决风险；
- 最近 Event History 或结构化 Compact Summary。

上限是正确性的一部分。无界上下文最终会变贵、变慢并降低一致性。

Execution Receipt 会逐条解释入选的 Working Set 文件或测试，包括选择来源、支撑
Evidence、相关性分数和单条预算结果。`included=false` 加截断原因表示 Selector 选择了
该路径，但渲染后的上下文预算裁掉了对应行。各 Host 投影同一份 Receipt，不自行反推
选择原因。

## 安全模型

安全采用分层结构，因为单一控制无法回答所有问题。

### 1. Mode 与 Posture

描述当前 Host/Session 的意图和审批行为。

### 2. Workspace Permission

记录用户对特定工作区允许的操作，必须绑定工作区，不能变成全局授权。

### 3. Constitution

普通 Session 配置不能绕过的硬约束。

### 4. Tool Guard

Tool Identity、Risk、Approval、Repository Policy 与 Edit Evidence 的统一决策点。

### 5. Edit Journal 与 Verify

授权写入后提供可恢复性和正确性证据。
Execution Receipt 会保留每次 Verification Attempt、命令推导原因、失败分类、Repair
次数、最终 Gate Action 和最终 Workspace Outcome。Rollback 会区分已恢复路径、冲突和
无法回滚的非文件副作用；原有 Pass/Fail 聚合字段只作为兼容摘要保留。

### 6. OS Sandbox

在操作系统边界限制进程、文件系统和网络。不同平台 Backend 强度不同；所需边界不可用
时，执行必须 fail closed。

## Secret 与网络边界

- Config 保存 Secret Reference，不保存 Secret Value。
- Provider 与 Web 出网使用受治理 Client 和显式 Endpoint。
- Log 与 Report 会脱敏，但仍属于敏感工程数据。
- MCP 与 Plugin 是供应链边界，不是可信文本文件。
- 服务默认监听 Loopback。
- Dynamic Tool 需要可信客户端和显式开启。

## 扩展架构

### MCP

外部 Server 通过协议 Adapter 暴露 Tool。Health、Timeout、Circuit Breaker 和 Tool
Binding 隔离避免单个 Server 故障污染全部工具。

### Skill

Skill 打包指令和资源。Discovery、Manifest、Lock 与 Enablement State 让最终内容可见。

### Plugin

Plugin 增加可执行能力。Registry Signature、Publisher Trust、Immutable Staging、
Receipt、Enablement、Rollback 与 Revocation 构成激活链。

### Hook

Hook 观察或拦截生命周期点，必须有界，不能成为另一条无 Guard 执行路径。

## 编排架构

- **Task Repository**：Durable State 与 Lease。
- **Worker**：Claim 并执行符合条件的 Task。
- **Automation**：调度 Task Template。
- **Workflow**：经过校验的 DAG/IR 和 Node Checkpoint。
- **Lane**：管理 Inline 或 tmux Worker Process。
- **Fleet Ledger**：分布式 Run/Task Event 的 Read Model。
- **Subagent**：具有 Depth、Budget 与 Workspace Isolation 的 Child Runtime。

所有编排最终仍回到 Runtime、Tool 和 Security 边界。

## 架构变更检查表

1. 确认所属层。
2. 判断是否改变协议或持久化状态。
3. 定义 Cancel、Retry 与 Terminal 行为。
4. 保持 Guard 与 Sandbox 路径。
5. 增加 Contract 或 Architecture Test。
6. 同步更新中英文文档。
7. 必要时重新生成 Protocol/Compatibility Artifact。
