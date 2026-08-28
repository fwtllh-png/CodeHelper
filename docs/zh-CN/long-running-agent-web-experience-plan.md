# 长任务 Agent Web 体验深化方案

> 状态：第 1 至 9 项已完成
>
> 参考实现：`/Users/bytedance/flow/deepseek-harness`
>
> 冻结参考提交：`11bba5f4f11328745f250674d99252c0d23e8398`
>
> 基线：CodeHelper 已完成 Conversation、Tool、Approval、Context、Settings、
> Trajectory、Stats 与滚动体验的首轮 Harness 对齐。

## 目标

下一阶段不再以静态截图相似度为主要目标，而是提升长任务中的注意力管理、控制能力、
恢复能力和成果验收效率。Web 继续只是 Runtime 事实的 Projection：

- Host 只提交 Operation，不在浏览器执行 Agent、Tool、Policy 或 Queue 逻辑；
- 运行状态、排队状态、恢复状态和文件成果都必须来自 Runtime 权威事实；
- Chat 保持简洁，Trajectory 保留完整证据，两者不得维护互相矛盾的状态；
- 不以 `localStorage`、IndexedDB 或组件 State 冒充可恢复的业务账本；
- 不照搬 Harness 的 Cordis、Plugin Slot 或多 Workspace Runtime。

## 交付顺序

| 阶段 | 能力 | 优先级 | 状态 |
| --- | --- | --- | --- |
| 1A | 运行中 Direct Steering | P0 | 已完成 |
| 1B | Durable Follow-up Queue | P0 | 已完成 |
| 2 | 失败现场恢复 | P0 | 已完成 |
| 3 | Plan、Todo、Goal、Subagent 一等视图 | P0 | 已完成 |
| 4 | Produced Files / Deliverables | P1 | 已完成 |
| 5 | 成熟 Composer 与附件 | P1 | 已完成 |
| 6 | 可操作 Settings | P1 | 已完成 |
| 7 | 后台任务与通知 | P2 | 已完成 |
| 8 | 内容呈现深化 | P2 | 已完成 |
| 9 | 长会话导航 | P2 | 已完成 |

## 1. 运行中 Steering 与 Queue

### 用户目标

用户不需要先停止长任务再补充要求。运行中输入应提供两个明确意图：

- **Steer current**：立即注入当前 Turn，并中断正在进行的 Model Sample，使下一次
  Sample 使用补充指令；
- **Queue next**：保持当前 Turn 不变，将消息加入当前 Session 的 FIFO Follow-up
  Queue，在当前 Turn 结束后开始新的 Turn。

### 1A. Direct Steering

- Web Host 正式暴露已有的 `turn.steer` Operation；
- Composer 在运行时继续可编辑；
- `Mod+Enter` 直接 Steering，Send 按钮切换为 Steering 动作；
- 普通空闲态 `Enter` 仍开始新 Turn；
- 成功提交后清空当前 Session Draft；
- `turn.steered` 是接受事实，Trajectory 展示完整记录；
- Steering 失败时保留 Draft，并展示 Runtime Problem；
- Approval/Input Takeover 期间不提供普通 Composer，避免与待决交互争夺输入焦点。

### 1B. Durable Follow-up Queue

Queue 是 Runtime-owned 的持久化有序集合。Pending Projection 包含：

```text
queue_item_id
session_id
thread_id
prompt
display_prompt
context_references
ordinal
created_at
updated_at
```

必须提供 Enqueue、Edit、Remove、Claim 四类显式状态转换。Claim 与后续
`turn.start` 的接受需要使用稳定 Idempotency Key，保证崩溃发生在任意边界时都不会
重复执行或丢失消息。Claim 后的 `turn.started` / `turn.steered` 事件携带
`queue_id` 和实际 `turn_id`，构成历史审计事实。Web Queue Dock 只投影 Runtime Queue：

- 默认折叠为 `N queued messages`；
- 展开后支持编辑、删除和将某一项提升为当前 Steering；
- Stop 只停止当前 Turn，不清空 Queue；
- Queue 按 FIFO 自动推进；
- Session 切换和进程重启后保持一致；
- Context 在 Enqueue 时冻结，不读取执行时已经变化的浏览器选择。

### 已实现

- `turn.enqueue`、`turn.queue.update`、`turn.queue.remove` 和
  `turn.queue.promote` 统一经过 Operation Lifecycle；
- `turn.queued`、`turn.queue.updated`、`turn.queue.removed` 为 retained Event；
- 自动推进使用由 `queue_id` 派生的稳定 Operation、Turn 和 Item Identity；
- Runtime 重启从 retained Event 重建 Pending Queue，并继续未 Claim 项；
- `turn.started.queue_id` 和 `turn.steered.queue_id` 是唯一 Claim 事实；
- Queue Dock 支持折叠、编辑、删除和提升为当前 Steering；
- 运行中 `Enter` 加入 Queue，`Mod+Enter` 立即 Steering；
- Stop 只终止当前 Turn，终态发布后继续 FIFO Queue。

### 验收

- 运行中 Steering 只产生一次 `turn.steered`，并被下一次 Sample 消费；
- 网络重试不会重复 Steering；
- Queue 编辑、删除按 `queue_item_id` 精确作用于一个 occurrence；
- 连续三个 Queue 项严格 FIFO；
- Stop、刷新和进程重启不丢 Queue；
- Approval/Input 等待期间 Queue 保留，Steering 的行为由 Runtime Phase 决定；
- 390px、200% Zoom 和键盘路径可达。

## 2. 失败现场恢复

失败、取消或不完整 Turn 的尾部直接展示可用动作，而不是要求用户进入 Settings：

- Retry：以相同意图重新执行，使用新的 Turn Identity；
- Continue：携带用户补充 Guidance；
- Restore：恢复到可用 Checkpoint；
- Fork：从 Checkpoint 创建新 Thread；
- 对可能已经发生副作用的 Tool 明确显示 `unknown`、`committed` 或 `rolled_back`。

恢复按钮必须来自 Runtime 的 `retryable`、`recovery_action`、Checkpoint Capability
和 Journal Receipt，不能根据错误文案猜测。

### 已实现

- Retry、Continue 由 `turn.failed.fault.disposition` 与 canceled 事实决定；
- Continue 使用内联 Guidance，不再调用浏览器原生 Prompt；
- 匹配 Source Turn 的最新 Checkpoint 在失败卡片就地提供 Restore/Fork；
- Restore/Fork 严格遵守 Runtime 返回的 `can_restore` / `can_fork`；
- Fork 成功后自动切换到 Runtime 返回的新 Session；
- Side Effects 显示 `none`、`unchanged`、`draft`、`committed`、
  `rolled back` 或 `unknown`，优先使用 Fault Metadata，缺失时使用
  Execution Receipt 的观察事实；
- `committed` 和 `unknown` 使用警示色，提醒用户恢复前检查 Workspace；
- 恢复失败保留在当前 Turn 现场，并通过统一 Problem 区域展示。

## 3. Plan、Todo、Goal、Subagent 一等视图

Composer 上方使用同一窄列依次展示 Todo、Goal、Queue：

- Goal 显示当前目标、预算和完成/阻塞状态；
- Todo 显示步骤、当前项和进度，不展示无意义的完成噪声；
- Subagent 显示角色、任务、运行状态、等待原因和合并状态；
- Chat 只展示摘要，点击后定位 Trajectory 或专用详情；
- 所有状态来自 WorkGraph、Plan Artifact 和 Agent Graph。

### 已实现

- Composer 上方新增可折叠 Session Progress 窄列；
- Goal 直接投影当前 Session Plan Artifact，不解析对话正文；
- Todo 直接投影 Task Read Model，运行项优先并显示完成计数、状态和原因；
- Subagent 直接投影 Agent Graph，显示角色、状态和最后消息；
- Subagent 区可一键进入当前 Session 的 Trajectory；
- `plan.delta`、Agent、Run、Node、Attempt 和 Turn 终态触发权威读模型刷新；
- Session 切换和 Runtime 重连仍以 `plan/get`、`task/list`、`agent/list`
  的结果重建，不持久化浏览器侧副本；
- 移动端自动切为单列，避免 Goal、Todo 和 Subagent 相互挤压。

## 4. Produced Files / Deliverables

每个 Turn 结束后生成结构化成果区：

- 汇总新增、修改、删除和重命名文件；
- 支持打开文件、下载、查看 Diff、定位产生文件的 Tool；
- 区分工作区真实文件、临时产物和外部 Artifact；
- 标记验证状态与 Stale 状态；
- 大量文件按类型折叠，不在 Chat 中展开全部内容。

### 已实现

- 每个带 `turn.receipt.changes` 的终态 Turn 在最终回答后生成 Produced Files；
- 文件按 created、modified、deleted 分组，并展示累计增删行；
- 来源明确标记为 Workspace，不将临时输出冒充真实文件；
- Open 通过 Workspace-bound Host RPC 打开本地编辑器；
- Download 先获取短期 Content Handle，再通过受权下载端点读取内容；
- Diff 优先复用产生文件的 Tool call-time Edit Plan；缺少精确 Plan 时明确展示
  Current Workspace Diff，不伪造历史 before/after；
- Inspect 根据 Receipt Change 与 Tool Result 的路径和 Tool Identity 定位 Trajectory；
- Verification 由 Receipt 的 diagnostics、tests、verify 事实聚合；
- 后续 Turn 再次修改同一路径时，旧 Turn 的成果自动标记 stale；
- 删除文件不提供 Open/Download，但仍保留 Diff 和 Tool 审计入口。

## 5. 成熟 Composer 与附件

- 图片粘贴、拖放和文件选择进入同一 Attachment Pipeline；
- 附件显示类型、大小、来源、上传/解析状态和移除动作；
- Slash Command 支持搜索、参数提示、最近使用和完整键盘导航；
- Draft 在 Session 间隔离，并覆盖长文本、IME、移动端键盘和内部滚动；
- 发送前明确显示实际提交的 Context 与 Attachment，不上传未选择内容。

### 已实现

- 文件选择、拖放和剪贴板粘贴统一进入同一个异步 Attachment Pipeline；
- 文本附件限定为 UTF-8 和 64 KiB，图片限定为 PNG、JPEG、GIF、WebP，
  单次 Prompt 的内联附件总量限制为 5 MiB；
- 浏览器计算 SHA-256，Web Host 重新校验摘要、大小和图片实际 Content-Type，
  Runtime Prompt Resolver 再校验后才生成 Provider 原生图片 Content Block；
- 文本和图片附件作为 `EditorContextReference` 随 `turn.start` 或
  `turn.enqueue` 提交，Queue Event 冻结完整 Context，刷新或 Runtime 重启不依赖
  浏览器组件 State；
- 附件卡片展示类型、大小、picker/drop/paste 来源以及 processing、ready、error
  状态；失败项必须移除后才能发送，避免静默遗漏；
- Session 切换使用 Generation 隔离未完成的异步解析，已移除附件不会在解析结束后回流；
- `/` 可从 Composer 打开命令搜索，支持参数提示、最近使用、方向键、
  Home/End、Enter 和 Escape；
- IME Composition 期间 Enter 不发送；长 Draft 固定最大高度并内部滚动，
  `visualViewport` 变化时保持输入框可见；
- 相关验证覆盖协议、Host 二次校验、Provider Prompt、Runtime Queue 恢复、
  浏览器 Pipeline、Session 隔离以及真实 Playwright 上传和移动端路径。

## 6. 可操作 Settings

Settings 从只读 Catalog 深化为受控管理面：

- Credential 创建、验证、失效诊断和删除；
- Tool、Skill 的启停、来源、健康、信任与错误详情；
- Agent Preset 创建、复制、编辑、校验和生效范围；
- Unsaved Changes、Restart Required 和 Apply Result 使用明确状态；
- Secret Value 不进入浏览器持久化、日志、截图或 Runtime Event。

所有写操作通过 Runtime Control Plane，浏览器不直接编辑配置文件。

### 已实现

- Model、Reasoning、Mode、Approval、Execution 和 Tool allowlist 先进入统一
  Settings Draft，再通过一次 `profile/update` 原子应用；
- Dirty、Discard、关闭前确认、Apply Result、Prompt Cache Reset 和
  Restart Required 都有明确状态，不再用控件瞬时值冒充 Runtime 已生效事实；
- Credential 支持 Keyring 创建或轮换、在线校验和二次确认删除，并展示引用、
  校验时间、失败原因及重启要求；Secret 不写入浏览器存储或 Runtime Event；
- Tool Catalog 展示来源、Capability、Access、Risk、Sandbox、Policy、
  Constitution 和不可用原因，启停变更随 Profile Draft 一次提交；
- Skill 展示来源、版本、健康、信任、权限、Capability 和摘要，
  支持启停、Detail 与 Verify；
- 新增 Runtime-owned Workspace Agent Preset，支持创建、更新、复制、删除、
  载入 Draft 和应用到当前 Session；
- Preset 使用版本化 CAS 更新和原子文件替换，按 Workspace 隔离并跨进程重启恢复；
- `agent-preset/list`、`save`、`delete`、`apply` 已纳入 Web Host 契约和生成 Schema。

## 7. 后台任务与通知

- Sidebar 同时表达运行、等待审批、失败和完成；
- 切换 Session 不影响后台 Turn；
- 页面标题和可选系统通知投影同一状态；
- 通知点击后定位到对应 Session、Turn 和待处理动作；
- 浏览器通知默认关闭，启用前说明范围，不包含 Prompt 或 Tool Output。

### 已实现

- Session Rail 直接投影 Runtime `SessionSummary`，以图标、文字和颜色同时区分
  Running、Approval/Input Required、Failed/Interrupted 与 Completed；
- WebSocket 收到任意 Session 的 Turn 或交互状态事件后，都会合并刷新权威
  Session List；切换 Session 不取消或接管后台 Turn；
- 页面标题按 Runtime Session 状态聚合 Action Required、Failed 和 Working 数量；
- 桌面通知默认关闭，只能在 General Settings 中经用户操作并获得浏览器权限后启用；
- 通知内容只包含通用状态，不包含 Session 标题、Prompt、Tool 名称或 Tool Output；
- 通知点击后激活对应 Session，并按 Runtime 的 `latest_turn_id` 定位最新 Turn；
  Approval/Input 通知会优先滚动并聚焦待处理控件；
- 状态转换去重基于前后两次 Runtime Session Projection，不会在首次 Hydration、
  重连或普通流式 Delta 时重复通知。

## 8. 内容呈现深化

- 数学公式、图片、宽表格、超长代码块和文件引用具有专用 Presenter；
- 图片支持尺寸约束、加载失败和下载；
- 宽表格与代码块只在自身区域横向滚动；
- CJK 强调、链接、引用和嵌套列表保持稳定排版；
- Tool、Reasoning、Context 的 Disclosure Rhythm 与状态动画保持一致。

### 已实现

- 最终回答改用懒加载 Markdown Presenter，并通过 `remark-math` 与 KaTeX MathML
  渲染行内和块级公式；流式阶段保留轻量 GFM 路径，终态后切换完整语义；
- CJK 强调使用专用 Remark Parser Extension，不通过字符串替换修正文法；
- 代码块提供语言栏、复制动作、固定最大高度和独立横向/纵向滚动区域；
- 表格保持自然列宽并在自身区域横向滚动，窄屏不会撑宽 Conversation；
- Markdown 文件链接显示为文件操作按钮，并继续通过 Workspace-bound
  `workspace/open` 交给 Runtime 校验；
- 同源图片直接加载；跨域图片先显示来源和显式加载动作。图片统一限制尺寸，并提供
  Loading、Error、Retry、Download 和外部打开状态；
- 外部图片仅允许 HTTPS，页面继续使用 `no-referrer`，原始 HTML 和危险 URL 不进入
  DOM；
- Tool、Reasoning、Context 共用同一 Disclosure 图标切换、展开时长和 Reduced
  Motion 行为。

## 9. 长会话导航

- 按 Turn、用户问题、Tool 和文件搜索；
- 上一个/下一个用户问题快捷导航；
- Chat、Trajectory、Diff、Produced File 双向定位；
- 历史分页、展开 Tool 和切换视图时保持阅读锚点；
- 搜索结果引用稳定 Identity，不引用易变 DOM Index。

### 已实现

- Conversation Header 提供当前问题位置和上一个/下一个问题动作，浮动搜索面板按
  Turn、用户问题、Tool 和结构化文件引用过滤，不引入常驻右侧栏；
- 搜索索引由现有 Conversation Projection 派生，结果使用 `entry.id`、`turnID`、
  `callID` 和 Workspace Path 组成的稳定 Identity，不依赖渲染顺序或 DOM Index；
- 搜索命中会自动切换 Chat、选择包含目标的有界 Transcript Page，并定位具体 Tool
  或 Produced File；Trajectory Inspector 可按 Turn/Call 返回对应 Chat 节点；
- Transcript 保持最多 200 个业务节点，并使用带重叠的分页窗口；加载历史前记录
  稳定语义锚点，而不是只记录易受内容高度影响的绝对滚动距离；
- Chat/Trajectory 往返、Session 切换、Tool 展开、Markdown 或图片异步变高时都会
  恢复同一阅读锚点；只有读者仍位于底部时才继续跟随流式输出；
- 搜索面板支持键盘选择、Focus Trap、Escape 关闭、Axe、Forced Colors、
  Reduced Motion、390px 窄屏和 200% Zoom。

## 横向工程要求

### 状态与持久化

- 状态转换唯一入口仍为 `turnkernel.Reducer.Apply` 或对应领域 Kernel；
- 新增 Event 必须声明 Trait、Owner、Durability、Correlation 和 Retention；
- 高风险操作继续通过 Guard、Approval、Journal 与 Sandbox；
- Queue、Goal、Todo、Deliverable 不得各自发明重复的 Turn 终态。

### 性能

- 高频 Delta 继续按 `requestAnimationFrame` 合并；
- Queue、Todo、Goal 更新不得触发完整 Transcript 重建；
- Trajectory 首屏和大列表继续遵守 DOM Budget；
- 新增功能优先懒加载，不放宽现有首屏 gzip/brotli 预算。

### 可访问性

- 所有图标动作具有可访问名称和 Tooltip；
- 完整支持键盘、Forced Colors、Reduced Motion 和 200% Zoom；
- 状态不能只依赖颜色或动画；
- Takeover、Dialog 和 Menu 正确管理 Focus。

## 验收矩阵

每项功能必须同时具备：

1. Projection 或纯状态转换单元测试；
2. Runtime/Host Contract Test；
3. Fixture Playwright 路径；
4. 至少一次冻结 Harness 与 CodeHelper 的同 Prompt 双实例对比；
5. `web-check`、`web-test`、`web-e2e`、`docs-check` 和 `git diff --check`；
6. 涉及 Runtime 并发或持久化时执行对应 Go Test 与 `go test -race`。

优先补齐以下真实场景：

| 场景 | 核心观察 |
| --- | --- |
| Multi-turn | Context、Draft、Queue、Session 连续性 |
| Mid-stream Control | Steering、Stop、Queue、滚动稳定 |
| Failure/Recovery | Retry、Continue、Checkpoint、副作用状态 |
| Background Work | Session 切换、通知、审批等待 |
| Long Trajectory | Timeline、Ledger、Inspector、虚拟化 |

## 完成定义

九项全部完成时，用户应能在一个长任务中持续补充要求、安排后续工作、理解当前进度、
处理审批和失败、检查最终文件，并在刷新或重启后恢复到同一事实状态。视觉相似度仍是
门禁，但不再替代真实工作流证据。
