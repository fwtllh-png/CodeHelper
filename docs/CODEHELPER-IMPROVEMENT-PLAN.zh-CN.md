# CodeHelper 优化与体验升级计划

## 1. 目标与结论

本计划基于 `feat/improve` 分支当前实现、文档、测试、CLI 动态输出和本机验证结果。
目标不是继续扩大功能数量，而是把已有能力收敛为一个可上手、可判断、可恢复、可验证的
编码 Agent 产品。

当前 CodeHelper 已具备扎实的 Runtime、Guard、持久化、协议和扩展基础，但产品成熟度
不均衡：

- 架构边界和安全治理较强；
- 能力面很广，但首次成功路径、能力就绪判定和 Host 完整度不一致；
- 单元与契约测试丰富，但默认 CI、平台能力测试和用户旅程门禁尚未形成同一套绿色基线；
- Coding Intelligence 和 Affected Verification 仍是早期实现；
- TUI 与 VS Code 已有较完整功能，但缺少统一的视觉语言、信息架构和可用性验收；
- 核心文件过度集中，继续叠加功能会显著提高回归成本。

因此，升级顺序应是：

1. 先冻结行为并拆分高变化率单体模块，降低后续并行升级的冲突与回归成本；
2. 建立可信的就绪状态和稳定工程基线；
3. 同步建立 TUI 与 VS Code 的体验规范、界面基线和可用性指标；
4. 把首次配置到第一次成功修改缩短为一个清晰、美观的引导流程；
5. 提高“定位、修改、验证、修复/回滚、解释”闭环成功率；
6. 收敛 CLI、TUI、VS Code 和 ACP 的核心语义并建立首版发布门禁；
7. 最后再扩展浏览器、语义检索和复杂编排。

## 2. 当前实现全景

| 领域 | 当前实现 | 判断 |
| --- | --- | --- |
| Runtime | Operation/Event/Receipt/Projection、流式 Turn、取消、审批、恢复 | 基础完整，是后续升级主干 |
| Provider | OpenAI Chat/Responses、Anthropic、Fixture、用途路由 | 主流协议可用，路由仍有部分能力未接通 |
| Context | Repo Map、词法 Symbol Index、Working Set、Evidence、Compact | 有界且可观测，但跨文件语义精度有限 |
| Tool | 文件、Shell、Git、搜索、质量、MCP、Skill、Plugin、Agent 等 | 数量充足，当前不应继续追求扩张 |
| Security | Mode/Posture、Policy、Permission、Constitution、Guard、Sandbox | 设计较强，应改善状态解释和平台证据 |
| Persistence | SQLite、Event Log、CAS、Session、Snapshot、Journal | 能力完整，应强化崩溃恢复旅程测试 |
| Verification | Diagnostics、Repository、Affected、Repair/Revert | 闭环已存在，但自动发现与语言覆盖不足 |
| Orchestration | Task、Worker、Automation、Workflow、Lane、Fleet、Subagent | 广度高，应晚于核心编码体验继续投入 |
| CLI | 约 30 个顶层命令，机器输出与配置 Provenance | 能力丰富，发现性和命令一致性需要治理 |
| TUI | Chat、审批、成本、Session、后台面板、主题与动效、大量 Slash Command | 功能可用，但信息密度、视觉层级、窄屏适配和命令可发现性未形成统一标准 |
| VS Code | Chat Webview、Changes、Threads、Agents、Tasks、Jobs、Approval、Usage | 安全边界完整，但 Webview 与原生 Tree View 的体验语言、状态反馈和首次引导尚未收敛 |
| 工程门禁 | Go/文档/协议/安全/VS Code 多层测试和 Benchmark | 资产丰富，但默认 CI 未覆盖全部关键资产 |

### 产品入口定位

后续产品资源按以下优先级投入：

1. **VS Code：主要图形产品入口。** 承载完整的日常编码体验，包括 Editor Context、
   Chat、Changes、Native Diff、Diagnostics、Approval、Session、Task/Job、Usage、
   Setup、Recovery 和 Update。
2. **TUI：主要终端交互入口。** 面向终端重度用户、SSH/远程环境和低资源场景，保持
   完整核心 Turn 能力，但不复制 VS Code 的编辑器原生功能。
3. **CLI：自动化与可组合入口。** 面向脚本、CI、诊断和精确操作，机器输出优先。
4. **ACP：稳定编辑器集成协议。** 服务 VS Code 与兼容的 Agent/Editor Client。

CodeHelper 不再提供 `codehelper web` 或 Embedded Web UI。浏览器产品面由 VS Code 插件
替代；同时不再提供 HTTP/SSE Runtime API，避免维护无内部消费者的 REST/SSE 协议面。
“多 Host 语义一致”仍适用于 CLI、TUI、VS Code 和 ACP，共享 Operation、Event、
Receipt、安全边界与核心生命周期。

## 3. 关键问题与证据

### P0：就绪状态不代表真实可用

`doctor` 和 `diagnostics` 当前固定返回 `ok=true`，即使 Provider/Model 为空、策略文件
缺失、浏览器不可用、模型路由仅部分实现、索引仅词法级。`doctor` 的 Sandbox 字段也是
能力口号，而不是当前 Backend 的实际探测结论。

影响：用户无法回答“现在能否开始真实编码”“缺什么”“下一步执行什么”。

### P0：首次成功路径过长

`init/setup` 只生成 Workspace、State 和 Constitution 配置，不完成 Provider、Model、
Credential Reference、模型 Probe、Sandbox 检查解释和首轮 Fixture/真实 Turn 验证。
默认配置中 Provider/Model 为空、Tools 关闭，但命令面同时暴露近 200 个 TOML 字段。

影响：用户必须跨文档和多个命令自行拼装最小可用配置。

### P0：默认帮助和 CI 存在漂移

CLI 使用手写静态 Usage，已经遗漏 `diagnostics`、`features`、`execpolicy`、`sessions`、
`metrics`、`update`、`pr`、`scorecard` 等已注册命令。默认 GitHub CI 只执行文档和
Go Build/Test，没有执行 VS Code Check/Test、协议契约、Smoke 或跨平台构建。

影响：可发现性会随命令增长继续恶化，关键 Host 回归可能进入 `main`。

### P0：测试资产丰富，但本机绿色基线不稳定

本次验证中：

- 文档与 Book 门禁通过；
- VS Code 全量测试在并发负载下出现一次 5 秒超时，单独复跑 Runtime 测试通过；
- MCP 和 Lane 测试在全仓并发下超时，单独复跑通过；
- Coding Benchmark 在当前 macOS 环境为 18/23，5 项 Verify Gate Case 因强 Sandbox
  不可用失败；
- 4 项 VS Code 真实 Runtime 集成测试在普通 `npm test` 中跳过。

影响：环境限制、资源敏感和产品错误没有在测试结果中被清晰分层。

### P1：编码质量闭环的精度不足

Affected Verification 当前只对 Go 做 Package 推导、对 Python 使用显式映射，JS/TS、
Java、Rust 等进入 `unmapped`。Repo Index 明确是词法提取，不解析类型、Import 或
Call Graph。23 个 Benchmark 主要覆盖上下文、编辑事务、Symbol 和 Verify Gate，尚未
覆盖完整上手、跨文件修改、Host 恢复和真实项目拓扑。

影响：系统“能执行”强于“能稳定找到正确影响面并证明修改正确”。

### P1：Host 能力广度大于体验完整度

TUI 当前有 6 个 Slash Command 只提示改用 CLI，`redo` 和 `copy` 被解析但不广告也不
执行。ACP Contract 已覆盖核心协议行为，但尚未形成 CLI、TUI、VS Code 的核心用户旅程
一致性矩阵。

影响：同一 Runtime 在不同入口下的可达能力和反馈质量差异明显。

### P0：TUI 与 VS Code 缺少产品级体验基线

TUI 已包含主题、状态条、动效、Transcript、审批和观察面板，VS Code 已包含 Chat
Webview 与 7 类辅助视图，但当前测试主要证明安全、状态投影和性能，没有系统验证：

- 首屏是否让用户立即理解当前 Workspace、Model、Mode、Posture 和下一步动作；
- Thinking、Tool、Approval、Verify、Failure、Recovery 是否有稳定的视觉层级；
- 空状态、加载状态、长任务、网络错误和恢复状态是否清晰且可操作；
- 80/120/160 列终端、VS Code 窄侧栏、浅色/深色/高对比度主题是否可读；
- 不依赖颜色、鼠标或动效时，键盘与屏幕阅读器路径是否完整；
- TUI 与 VS Code 的术语、图标、状态色和危险操作是否一致。

影响：功能虽已存在，用户仍可能“不知道该看哪里、当前发生什么、下一步做什么”，
这会直接降低信任、任务完成率和长期使用意愿。

### P0：高变化率模块过度集中

当前非测试文件中，`internal/host/tui/app.go` 为 3007 行、
`internal/runtime/agent/engine/engine.go` 为 2659 行、
`internal/runtime/protocol/message.go` 为 2135 行、
`internal/config/config.go` 为 1902 行，合计 9703 行、约 511 个类型/函数声明。
Agent Engine、TUI 状态处理、协议消息和配置解析已经成为升级并行开发的冲突热点。

影响：理解成本、Review 面积、测试定位和多人并行修改成本持续上升；若不先治理，
Readiness、Setup、TUI UX 和 VS Code Projection 会继续修改这些热点并放大风险。

## 4. 设计原则

1. **主路径优先**：优先保证真实编码任务，不以新增 Tool 数量衡量进展。
2. **状态必须可行动**：所有 `ready/degraded/blocked/unavailable` 都给出原因、影响和
   下一条安全命令。
3. **Runtime 是唯一事实源**：Host 只投影，不新增 Host 专属业务循环。
4. **失败是产品行为**：定义 Cancel、Timeout、Retry、Recovery、Repair 和 Rollback。
5. **证据先于绿灯**：Unavailable 不能伪装 Passed，环境限制不能伪装代码失败。
6. **渐进配置**：默认只暴露完成首轮任务所需字段，高级配置按领域展开。
7. **需求可追溯**：每项升级必须绑定需求 ID、代码归属、测试资产、文档和验收指标。
8. **体验属于正确性**：美观不是装饰；层级、反馈、可发现性、无障碍和一致性必须进入
   Requirement、Test 与 Release Gate。
9. **先稳定结构再叠加功能**：拆分先保持行为与 Package API 不变，由 Characterization、
   Contract、Golden 和 Race Test 证明等价；不把功能重写混入机械拆分。

## 5. 分阶段实施

### Stage 0：结构稳定、可信基线与可观测就绪

目标：先消除阻碍后续升级的高冲突模块，再让任何开发者都能判断当前环境是否可运行，
以及失败属于哪一层。Stage 0 按“行为冻结 → 机械拆分 → 边界门禁 → 功能基线”执行。

| ID | 工作项 | 主要归属 | 验收 |
| --- | --- | --- | --- |
| IMP-001 | 统一 Readiness 模型，替代分散的 Doctor/Features/Diagnostics 结论 | `internal/runtime/protocol`、`internal/host/cli`、`internal/runtime/app/wire` | 输出 `ready/degraded/blocked`、原因、影响、修复动作；Exit Code 与状态一致 |
| IMP-002 | 从 Cobra Command Tree 生成 Root Help 和文档命令清单 | `internal/host/cli`、`scripts` | 已注册命令 100% 出现在动态帮助；增加 Drift Test |
| IMP-003 | 将测试分为 Hermetic、Platform Capability、Integration、Release 四条 Lane | `Makefile`、`.github/workflows`、测试 Harness | `make test` 在支持环境稳定复跑；环境限制以结构化 Skip/Unavailable 报告 |
| IMP-004 | 把 VS Code Check/Test、Protocol Contract、Smoke 纳入 PR CI | `.github/workflows`、`Makefile` | PR 必须覆盖 Go、文档、VS Code 和共享协议；真实集成门禁单独可见 |
| IMP-005 | 建立升级基线报告 | `internal/host/bench`、`scripts` | 记录任务成功率、P50/P95 时延、重试、验证覆盖、未知成本和恢复成功率 |
| IMP-006 | 冻结热点模块行为与依赖边界 | 四个热点模块及其邻近测试 | 补齐 Characterization、Golden、State Transition、Schema Drift、Race Test；生成依赖与职责清单 |
| IMP-007 | 拆分 TUI 状态与呈现职责 | `internal/host/tui` | `app.go` 只保留 Model 编排；Reducer、Projection、Panel、Command Adapter 分离，现有键盘与 Golden 行为不变 |
| IMP-008 | 按 Turn 阶段拆分 Engine Handler | `internal/runtime/agent/engine` | Start/Model/Tool/Approval/Verify/Terminal Handler 分离；状态转换、取消、终态和 Race 不变量保持 |
| IMP-009 | 拆分 Config Schema、Load、Validate 与 Provenance | `internal/config`、`scripts` | 配置字段保持单一事实源；CLI/TOML/Env 默认值与错误文本兼容；Drift Test 通过 |
| IMP-010 | 按 Operation、Event、Receipt 拆分 Protocol Message | `internal/runtime/protocol` | Schema 与生成文件保持兼容；Decode/Validate/Redaction 边界独立且无 Import Cycle |

退出条件：四个热点不再由单文件承载多个领域职责，新增架构门禁阻止重新集中；所有
Characterization/Contract/Race Test 保持全绿；主分支存在可重复的绿色 Hermetic 基线，
Readiness 不再出现“`ok=true` 但核心执行条件缺失”。

### Stage 1：首次配置与体验设计基线

目标：新用户从二进制到第一次成功受治理修改不超过一个引导流程，同时先建立 TUI 与
VS Code 共同遵循的界面和交互基线。

| ID | 工作项 | 主要归属 | 验收 |
| --- | --- | --- | --- |
| IMP-101 | 将 `setup` 升级为可交互/可脚本化 Setup Flow | `internal/host/cli`、`internal/config`、`runtime/app/wire` | 选择 Provider/Model/Credential Reference，执行 Probe、Sandbox 与 Fixture 验证 |
| IMP-102 | 提供 `minimal`、`recommended`、`advanced` 配置 Profile | `internal/config`、双语文档 | 默认配置只显示主路径字段；`config explain FIELD` 返回来源、默认值和风险 |
| IMP-103 | 增加 `quickstart` Fixture 旅程 | CLI、`testdata/providers` | 无 Secret、无网络完成“读取、计划、编辑预览、批准、验证、Receipt” |
| IMP-104 | VS Code 内置 Setup/Repair 引导 | `extensions/vscode` | Runtime 未就绪时展示缺失项和修复动作，不只显示 Output Channel 错误 |
| IMP-105 | 建立 TUI/VS Code 统一体验规范 | `internal/host/tui`、`extensions/vscode`、设计文档 | 定义信息架构、语义色、排版、间距、图标、状态、术语、危险操作和动效原则 |
| IMP-106 | 建立界面与可用性基线 Harness | TUI Golden、VS Code Electron、Accessibility Test | 固化关键界面截图/文本快照、键盘路径、主题、窄屏和空/载入/失败状态 |

退出条件：全新临时工作区可通过一条命令或 VS Code 引导完成 Fixture 首轮；真实
Provider 仅需提供 Credential Reference；TUI 与 VS Code 的后续改版有共同设计规范和
自动化体验基线。

### Stage 2：提升编码任务成功率

目标：把“修改完成”升级为“影响面明确、验证有效、失败可修复”。

| ID | 工作项 | 主要归属 | 验收 |
| --- | --- | --- | --- |
| IMP-201 | 建立 Repository Test Topology | `internal/observability/verify`、`persist/repoindex` | 支持 Go、JS/TS、Python、Rust 的受影响测试；每条命令解释推导依据 |
| IMP-202 | 引入语言级 Symbol/Reference Provider | `internal/adapter/lsp`、`platform/symbols`、`persist/repoindex` | 词法降级保留；语义结果记录来源、版本和置信度 |
| IMP-203 | 统一 Verify/Repair/Rollback Receipt | `runtime/agent/engine`、`runtime/protocol`、各 Host | 用户可见验证命令、失败归因、修复轮次、最终 Workspace 状态 |
| IMP-204 | Benchmark V2 用户旅程 | `internal/host/bench`、`testdata/benchmarks` | 增加跨文件修改、测试选择、崩溃恢复、审批、预算和 Host Replay Case |
| IMP-205 | Context Selection Explainability | `promptcontext`、`evidence`、各 Host | 每个 Working Set 文件和测试说明进入原因、证据与截断情况 |

退出条件：受支持语言的变更不再静默进入 `unmapped`；Benchmark 按环境能力分层后全绿，
并能阻止任务成功率和验证覆盖回退。

### Stage 3：TUI 与 VS Code 体验全面升级

目标：CLI、TUI、VS Code 对核心 Turn 生命周期提供一致且完整的反馈，并让 TUI 与
VS Code 达到清晰、美观、高效、可访问的日常使用标准。本阶段的设计和基线工作在
Stage 1 即启动，不等待 Stage 2 全部完成。

| ID | 工作项 | 主要归属 | 验收 |
| --- | --- | --- | --- |
| IMP-301 | 建立 Host Journey Contract | `internal/host/runtimeapi/contract`、CLI/TUI/VS Code 测试 | Start、Stream、Approve、Input、Cancel、Verify、Recover、Receipt 跨 Host 一致 |
| IMP-302 | 重构 TUI 信息架构与视觉层级 | `internal/host/tui` | Header、Transcript、Composer、Context/Task Panel 职责清晰；80/120/160 列可用；Thinking/Tool 默认渐进披露；Slash Command 要么可操作要么不注册 |
| IMP-303 | 重构 VS Code 工作台体验 | `extensions/vscode` | Chat、Changes、Threads、Approvals、Tasks/Jobs 形成主次导航；优先使用 Native Diff、Quick Pick、Progress、Tree View；Webview 保持主题一致 |
| IMP-305 | 统一关键状态与反馈组件 | TUI、VS Code | Setup、Empty、Loading、Streaming、Approval、Verify、Failure、Recovery、Completed 均有一致文案、视觉语义和下一步动作 |
| IMP-306 | 完成键盘、无障碍与低干扰模式 | TUI、VS Code | 全主路径可仅键盘完成；不只依赖颜色；支持高对比度、Reduced Motion、焦点可见和屏幕阅读器标签 |
| IMP-307 | 建立可用性评审与用户测试机制 | `scripts`、测试报告 | 每个里程碑完成任务走查；记录完成率、耗时、阻塞点、误操作和主观清晰度并驱动修复 |
| IMP-304 | 移除非主线 Web 与 HTTP/SSE 产品面 | CLI、`internal/host`、双语文档 | `codehelper web`、`codehelper serve`、Embedded UI、Pairing/QR、REST/SSE Handler 不再存在；ACP 保持独立 |

退出条件：核心 Journey Contract 在 CLI、TUI、VS Code 全部通过；代表性任务能仅用
键盘完成；主题、窄屏、空/载入/失败状态的基线测试通过；Host 不直接实现 Provider、
Tool 或 Sandbox 逻辑。

### Stage 4：持续结构治理

目标：防止 Stage 0 已拆分的职责重新集中，并用变化数据持续识别新的热点。

| ID | 工作项 | 主要归属 | 验收 |
| --- | --- | --- | --- |
| IMP-401 | 建立模块职责与体积预算门禁 | `scripts`、Architecture Test | 新增职责、跨域依赖或热点文件增长必须显式说明；禁止反向依赖与 Import Cycle |
| IMP-402 | 生成变更集中度与冲突趋势报告 | CI、Git History Analysis | 按模块记录 Change Frequency、同文件并行修改、Review 面积和回滚率 |
| IMP-403 | 定期清理过渡 Adapter 与失效兼容层 | 各 Owner Package | 删除已无调用方的桥接代码；公开 Package API 与实际消费者一致 |

退出条件：每次改动能定位到单一 Owner；Stage 0 拆分边界没有重新集中，行为测试保持全绿。

### Stage 5：首版发布硬化

目标：形成可重复发布、可回滚、声明与证据一致的初始稳定版。

| ID | 工作项 | 主要归属 | 验收 |
| --- | --- | --- | --- |
| IMP-501 | 建立 Linux/macOS/Windows 能力矩阵 | CI、Sandbox、Release Script | Build、Sandbox Strength、Fail-closed 和已知限制均有动态证据 |
| IMP-502 | 建立 Release SLO 与 RC 报告 | Benchmark、Telemetry、Release | 冷启动、首 Token、Turn 完成、恢复、内存和大仓性能不超预算 |
| IMP-503 | 固化兼容与迁移策略 | Protocol、Persistence、文档 | 第二版 Schema 前明确协议、配置、数据库、插件兼容窗口 |
| IMP-504 | 生成可审计发布物 | Release Script | CLI/VSIX、Checksum、SBOM、Provenance、Compatibility、Rollback 一次生成 |

## 6. 需求到测试的追溯矩阵

每个 PR 至少维护以下关系：

| 需求 | 代码分支/路径 | 必须新增或更新的资产 | 核心断言 |
| --- | --- | --- | --- |
| IMP-001 | Readiness Protocol、CLI Projection | Unit + CLI Golden + Protocol Schema | 状态、原因、动作、Exit Code 一致 |
| IMP-006 | Hotspot Characterization | Golden + State Transition + Schema Drift + Race | 拆分前后外部行为、错误、顺序和并发语义一致 |
| IMP-007 | TUI 拆分 | Multi-width Golden + Reducer Transition + Keyboard Journey | Panel/Command 改动不再修改中心状态编排文件 |
| IMP-008 | Engine 拆分 | State Transition + Race + Cancellation | 无双终态、无泄漏 Goroutine、取消可恢复 |
| IMP-009 | Config 拆分 | Load/Validate/Provenance Matrix + Drift | TOML/Env/CLI 默认值、错误和来源解释一致 |
| IMP-010 | Protocol 拆分 | Schema Golden + Decode/Validate/Redaction | Wire Schema 无漂移，未知类型 Fail Closed |
| IMP-101 | Setup Flow | CLI Integration + Fixture Journey | 空工作区可达首轮成功，不写入 Raw Secret |
| IMP-105 | Experience Spec | Design Token/State Catalog + Review Checklist | TUI 与 VS Code 的状态、术语、危险操作和层级规则一致 |
| IMP-201 | Test Topology | 每语言 Fixture + Affected Contract | 不漏掉已知受影响测试；未知明确 Unavailable |
| IMP-203 | Verify Receipt | Engine State + Host Projection Contract | Repair/Revert 后 Workspace 与 Receipt 一致 |
| IMP-301 | Host Journey | CLI/TUI/ACP/VS Code 同场景 | Terminal Event 唯一、Cursor 无 Gap、审批身份绑定 |
| IMP-302 | TUI UX | 80/120/160 列 Golden + Theme + Keyboard Journey | 不截断关键动作；层级稳定；主路径无需鼠标 |
| IMP-303 | VS Code UX | Electron Screenshot + Theme + Accessibility Journey | 原生与 Webview 状态一致；焦点、标签和动作可达 |
| IMP-307 | Usability | 固定任务脚本 + 评审报告 | 记录完成率、耗时、阻塞与误操作，回归必须说明变化 |
| IMP-401 | Architecture Budget | Dependency/Ownership/Size Guard | 不出现反向依赖、循环依赖或职责重新集中 |
| IMP-501 | 平台矩阵 | Capability Probe + Attack Corpus | 强能力不可用时 Fail Closed，声明不高于证据 |

建议新增 `docs/improvement-traceability.json` 作为机器可读台账，字段至少包括：
`requirement_id`、`owner`、`implementation_paths`、`test_assets`、`metrics`、`status` 和
`evidence_command`。CI 校验路径和测试资产存在，避免计划与实现脱节。

## 7. 指标与发布阈值

第一轮先采集基线，再冻结阈值，避免无依据设定数字。必须覆盖：

- **首次成功**：安装到 Fixture 首轮成功耗时、步骤数、错误恢复次数；
- **任务质量**：Benchmark 通过率、首轮通过率、验证覆盖率、Rollback 成功率；
- **可靠性**：Crash Recovery 成功率、Event Replay Gap、重复 Terminal Event；
- **性能**：Runtime Ready、首 Token、P50/P95 Turn、Context Build、Tool Queue；
- **成本**：输入/输出/缓存 Token、未知价格调用、每个成功任务成本；
- **可用性**：首次任务完成率、完成耗时、审批决策耗时、误操作率、放弃率；
- **清晰度**：用户识别当前状态和下一步动作的正确率、无动作错误占比、修复步骤数；
- **视觉质量**：关键状态基线覆盖率、非预期 Screenshot/Golden Diff、主题与窄屏缺陷数；
- **可访问性**：纯键盘任务通过率、焦点顺序、屏幕阅读器标签、高对比度与 Reduced Motion；
- **一致性**：同一 Runtime Event 在 TUI/VS Code 的术语、状态语义和危险操作差异数；
- **结构健康**：热点文件体积、职责数、Change Concentration、并行冲突率、平均 Review 面积；
- **工程质量**：Flaky Rate、Race Failure、受影响测试命中率、Host Contract 覆盖率。

## 8. 暂缓事项

在 Stage 0 到 Stage 2 完成前，不建议优先投入：

- 新增大量 Built-in Tool 或 Provider；
- 把当前 Fake Browser 包装成真实浏览器能力；
- 增加新的 Workflow/Orchestration 抽象；
- 为未发布的开发数据增加兼容迁移；
- 无评估数据支撑的向量检索或更大 Context Window。

## 9. 推荐执行顺序

首批按以下顺序推进，Stage 0 内部也设置硬门禁：

1. **Stage 0A - 行为冻结**：IMP-003、IMP-004、IMP-006；
2. **Stage 0B - 热点拆分**：IMP-007、IMP-008、IMP-009、IMP-010；
3. **Stage 0C - 可信基线**：IMP-001、IMP-002、IMP-005；
4. **Onboarding**：IMP-101、IMP-102、IMP-103、IMP-104；
5. **Coding Quality**：IMP-201、IMP-203、IMP-204；
6. **TUI/VS Code UX**：IMP-105、IMP-106、IMP-301 至 IMP-307。

体验研究、信息架构和用户任务设计可与 Stage 0B 并行，但对热点模块的功能改造必须等
Stage 0B 完成。所有拆分只做行为保持型变更，由既有 Contract、Golden、Race Test 和
Benchmark 保护。首个里程碑不以新增能力计数，而以以下结果验收：

- 四个热点模块完成职责拆分，架构预算门禁生效；
- 新用户能完成首轮；
- 就绪状态可信；
- TUI 与 VS Code 的首屏、任务状态和下一步动作清晰一致；
- 代表性任务可仅用键盘完成，主题和窄屏基线通过；
- 默认 CI 覆盖主产品面；
- 核心 Benchmark 在能力匹配的环境全绿；
- 修改后的验证和恢复结果可从 Receipt 解释。
