# Web 体验对齐 DeepSeek Harness 实施方案

> 状态：核心实现与自动化门禁已完成。双实例真实验收已覆盖 Read Analysis、
> Tool Chain 和 Long Trajectory；其余真实 Prompt 场景仍按本文作为发布前验收项。
>
> 参考仓库：`/Users/bytedance/flow/deepseek-harness`
>
> 冻结参考提交：`11bba5f4f11328745f250674d99252c0d23e8398`
>
> 基线日期：2026-08-24

## 目标

CodeHelper Web 的视觉风格、信息层级、交互流程、流式反馈、动画节奏、响应式行为和
最终展示效果，应与上述 DeepSeek Harness Web 参考实现达到 95% 以上的体验复刻度。

这里的“95%”不是主观评价，也不表示复制 Harness 的 Runtime。它是由本文定义的
场景、度量和阻断条件共同计算的产品验收结果：

- UI 外观和布局遵循同一套视觉语言；
- 相同用户意图经过相同的可见交互步骤；
- 流式执行的首个反馈、持续反馈和最终落点具有相同体验；
- Chat、Tool、Reasoning、Approval、Input、Stats 和 Trajectory 使用相同的信息层级；
- 动画类型、持续时间、缓动、触发条件和 Reduced Motion 行为一致；
- CodeHelper 的 Runtime、安全、持久化和可恢复性语义保持不变。

完成标准是全部阻断条件通过，且加权复刻分数不低于 95 分。任何单张截图“看起来接近”
都不能替代交互、性能、状态和可访问性证据。

## 产品边界

### 必须对齐

1. 三列应用壳层、可收起侧栏、中心工作区和按需详情栏。
2. 空会话的输入器中心态，以及开始执行后输入器移动到底部的连续体验。
3. Chat 中用户消息、Assistant 输出、Reasoning、Context 和 Tool 的信息层级。
4. Tool 默认折叠、单行摘要、运行状态、失败状态和按需展开内容。
5. 流式更新、贴底跟随、离底阅读、回到底部和历史前插锚定。
6. Composer 内的上下文入口、Mode、Approval、Model、Reasoning、Context Meter、
   Send 和 Stop。
7. Chat 与 Trajectory 双视图，以及由同一事实源产生的运行统计。
8. Sidebar、Details、Settings、菜单和浮层的打开、关闭及响应式行为。
9. 状态型动画、悬停反馈、焦点反馈、Reduced Motion 和 Still 行为。
10. Light、Dark、Forced Colors、键盘操作和 200% Zoom。
11. Harness 已有的输入区淡蓝光晕、状态渐变、时间轴颜色和层级阴影。
12. 品牌标记在 Sidebar、折叠 Rail、Empty Hero、Boot、Favicon 和应用图标中的角色、
    尺寸、对齐及交互反馈。

### 允许差异

只有下列差异不计入复刻损失：

- 产品名称继续使用 CodeHelper，鲸鱼图形替换为原创卡皮巴拉标记；
- CodeHelper 独有的 Checkpoint、Edit Plan、Task、Agent、Extension、Credential、
  Workspace Diagnostic 和 Recovery 能力继续存在；
- Harness 中 CodeHelper 不具备的多 Workspace、Queue 或其他能力不得制作无效入口；
- 面向用户的状态必须使用 CodeHelper 体验契约中的 Canonical Label；
- CodeHelper 的安全确认必须继续展示真实 Request、Scope、Effect 和 Runtime Identity；
- 为避免许可证和维护耦合，允许独立实现相同行为，不要求复制组件源码。

以上差异只允许改变内容，不允许破坏参考界面的布局密度、操作位置和反馈节奏。

### 明确不做

- 不引入 Harness 的 Cordis、Plugin Slot、Session Runtime 或 Host Transport；
- 不建立第二套 Agent Loop、Session Authority、权限状态机或持久化日志；
- 不让浏览器根据文案猜测 Turn、Tool、Approval、Verification 或 Receipt 状态；
- 不为了像素相似而隐藏失败、降级、安全边界或恢复操作；
- 不把 Runtime 原始事件直接当作长期 UI 组件接口；
- 不复制无关的 Workspace 管理和插件管理能力；
- 不新增参考实现之外的装饰性光球或大面积渐变；参考实现已有的输入区淡蓝光晕、
  状态渐变和时间轴渐变必须对齐；
- 不把本方案变成大量单组件目录或微型 Package。

## 已验证基线

2026-08-24 使用同一个只读任务分别体验了 Harness 和 CodeHelper：

```text
请只读分析当前工作区：概括项目用途、核心模块和主要技术栈，并引用关键文件；
不要修改任何文件。
```

### Harness

- 首屏 `DOMContentLoaded` 约 170ms，`load` 约 375ms；
- 本地开发构建加载约 72 个资源、4.6MB；
- 首 token 平均 0.6s，生成速度 162 tok/s；
- 该任务包含 1 Turn、8 Steps、LLM 28.6s、Tool 0.6s；
- Chat 中 Tool 和 Think 默认折叠，运行时逐行增加；
- 完成后停留在最终回答附近；
- Trajectory 以时间轴和表格呈现同一运行记录；
- Console 无错误。

### CodeHelper

- 首屏 `DOMContentLoaded` 约 17ms，`load` 约 18ms；
- 当前构建加载约 17 个资源、91KB；
- 提交后约 1.6s 内进入 `Working`，后端并非主要体验瓶颈；
- 该任务完成后有 11 个展开 Disclosure；
- Transcript 高约 6076px，完成时距底部约 5668px，用户停留在第一个 Tool 原文；
- `file_read` 与后续 `result_get` 将重复原文直接铺在 Chat 中；
- 页面只显示笼统的 `Working`，未利用已有 Receipt Latency 和 Usage 事实；
- Console 无错误。

结论：CodeHelper 已拥有更轻的首屏和足够快的 Runtime。主要差距位于浏览器 Projection、
React 更新粒度、渐进披露、滚动控制、布局稳定性和事实可视化。

## 参考实现中的关键做法

### 稳定的业务节点

Harness 不在每个流事件上重建整个消息数组。每个 Conversation Node 使用稳定业务
Identity，内容变化只更新对应节点，结构变化才更新顺序。Assistant Delta 最多每动画帧
发布一次，终态和控制事件立即发布。

CodeHelper 应采用同一原则，但继续以 Runtime Event 和 Cursor 为事实来源。Browser
Projection 是可丢弃缓存，不成为新的权威状态。

### 默认折叠的工具流

Tool 行固定为单行摘要：

```text
[状态或图标] [动作类型] · [目标、查询或命令摘要]
```

所有 Tool 默认折叠。运行中通过轻量扫光和可访问文本表达；失败时第一行错误替代普通
摘要。展开区按 Tool 类型呈现输入和输出，并在内部滚动，不占满 Chat。

### 单一滚动轴

Header 位于滚动区之外。Transcript、状态条和 Composer 位于同一滚动上下文中，
Composer 使用 Sticky 定位。滚动条槽位固定保留，避免内容从短变长时水平跳动。

系统显式记录用户是否贴底：

- 贴底时，流式尾部增长继续跟随；
- 用户离底后，不再抢滚动位置；
- 显示固定尺寸的“回到底部”图标按钮；
- 历史前插通过稳定业务节点和相对位置恢复阅读锚点；
- Tool 展开、Composer 增高和视图切换不得改变用户正在阅读的位置。

### 同一事实的不同视图

Chat 是用户工作流，默认隐藏实现噪声。Trajectory 是审计和诊断视图，完整展示
System、User、Context、Assistant、Tool、Usage 和时间边界。Stats 是压缩后的运行事实。

三者必须读取同一 Event、Usage 和 Receipt，不维护平行业务状态。

### 有目的的动画

参考动画只表达状态或空间变化：

| 动画 | 参考持续时间 | 用途 |
| --- | ---: | --- |
| Sidebar 列宽过渡 | 300ms | 表达面板展开和收起 |
| Sidebar 内容交叉淡入淡出 | 150ms | 避免列宽变化时文字重排 |
| Disclosure 图标切换 | 100ms | 图标与 Chevron 切换 |
| Running 文本 Shimmer | 1800ms | 表达 Agent 仍在生成 |
| Running Tool Sweep | 2600ms | 表达该 Tool 尚未结束 |
| Pending Dot | 1000ms | 表达提交或裁决尚未完成 |

所有无限动画必须在 `prefers-reduced-motion: reduce` 下变成静态状态。动效不能成为唯一的
状态信号。

## 目标信息架构

```text
AppFrame
├── Sidebar
│   ├── Brand / Collapse
│   ├── New Chat
│   ├── Search / View Options
│   ├── Workspace + Session Tree
│   └── Connection / Settings
├── Conversation
│   ├── Header
│   │   ├── Session Identity / Mode
│   │   ├── Session Actions
│   │   └── Chat / Trajectory Tabs
│   └── Shared Scrollport
│       ├── Active View
│       ├── Back To Bottom
│       └── Sticky Action Seat
│           ├── Todo / Queue / Context
│           ├── Approval 或 Input Takeover
│           ├── Composer
│           └── Stats
├── Settings Dialog
│   ├── General / Models
│   ├── Tools / Extensions
│   └── Agent Preset / Recovery
└── Context Dialog
    ├── Files / Symbols
    ├── Changes
    └── Diagnostics
```

### Sidebar 与会话管理

- 展开态依次呈现 Brand、整行 New Session、Workspace 工具栏、Workspace/Session Tree
  和底部连接状态；
- Search 与 Session 行动作按需展开，默认列表只保留标题、状态点和相对时间；
- Session 按 Workspace 缩进分组，Workspace 行默认显示目录图标，Hover 时原位切换
  Chevron；
- 首个用户 Prompt 被接受后，Runtime 生成并持久化短标题；已有默认标题在激活时回填，
  显式重命名不被后续 Prompt 覆盖；
- 成功与空闲状态不显示文字标签，运行、等待和失败状态通过颜色与可访问文本表达。

### 空状态

- 无可用 Provider 或 Credential 时，显示 CodeHelper Setup Surface；
- Runtime 已可用但没有 Session 时，中心区域显示创建 Chat 的明确动作；
- 已有空 Session 时，隐藏普通 Header，把同一个 Composer 移到中心；
- 首次提交后复用同一个 Textarea DOM，将 Composer 移到底部；
- 不显示重复的 `New Chat` 标题、路径和无意义的空 Details；
- 不出现仅仅置灰、却没有下一步动作的输入器。

### 活跃 Chat

- 内容列宽为 748px；
- Composer 最大宽度为内容列宽加 32px；
- User Bubble 最大宽度为内容列的约 70% 至 82%；
- Assistant 内容不放进装饰性 Card；
- Tool、Reasoning、Context、Compaction 和 Retry 使用同一 24px Disclosure Rhythm；
- 成功完成不插入独立 `Completed` 或 Receipt 行，只通过最终回答、Session 时间和底部
  Stats 表达；失败与取消仍保留可恢复提示；
- 最终 Assistant 输出是视觉主角，Tool 原文不能把它推离当前视口。

### Composer

- Composer 是稳定挂载的同一个组件，不因 Empty、Active、Approval 或 Input 状态重建；
- Textarea 自动增高，最高 14 行，超过后只滚动输入区域；
- 左侧放置 Context 入口和 Mode/Approval；
- 右侧放置 Model、Reasoning、Context Meter、Send/Stop；
- 控件空间不足时整组换行，不允许文本、图标或按钮重叠；
- Approval 和 Input 使用原位 Takeover，保持 Shell 和主动作位置稳定；
- Status/Stats 与 Composer 共用宽度轴；Stats 只做整行末尾省略，不逐项压缩，并通过
  Tooltip 显示完整 Token 明细。

### Tool 和 Reasoning

Tool View Model 至少包含：

```text
id
callID
tool
variant
title
summary
state
arguments
output
errorSummary
execution
changes
recovery
```

Tool Variant 至少覆盖：

| Variant | CodeHelper Tool | 默认摘要 |
| --- | --- | --- |
| Read | `file_read`、`result_get` | Workspace 相对路径或结果 Handle |
| Search | `text_search`、`file_search`、`symbol_search` | Query 或 Pattern |
| Shell | `shell_read`、`exec_command` | Description 优先，其次 Command 首行 |
| Write | `file_write`、`apply_patch` | 路径和变更规模 |
| Diff | 产生 `changes` 的 Tool | `+added -removed · files` |
| Agent | Subagent Tool | Role、状态和子任务摘要 |
| Generic | 未知 Tool | Tool 名和第一个可读参数 |

`result_get` 不得再次把完整结果作为默认可见正文。它应显示为“读取完整结果”摘要，并与
原调用通过 Handle、Call ID 或相邻运行事实关联；无法可靠关联时保持独立折叠行，不猜测。

Reasoning 默认折叠。运行中摘要显示最新非空行并跟随行尾；结束后恢复第一条稳定摘要。
完整 Reasoning 只在用户主动展开后进入普通文档流。每次模型 Sample 完成后必须生成
可持久化的 `reasoning.completed` 事实；瞬时 Delta 只负责流式体验，重载和切换 Session
时由完成事实恢复多个独立 Think 段。

Read、Shell 与 Search 不能回退为通用 Input/Output JSON：

- Read 展开后使用带原始行号、语言标签、头尾折叠和复制动作的文件面板；
- 文件摘要和 Search 结果路径通过受 Workspace 约束的 Host RPC 打开本机编辑器；
- Shell 展开后使用命令、工作目录、运行状态、退出码和输出组成的 Terminal 面板；
- Grep 按文件分组展示行号与匹配文本，Glob/File List 使用平铺路径列表；
- 所有卡片默认折叠，长内容在卡片内部滚动，不改变 Transcript 宽度。

### 运行状态和统计

运行中必须立即显示：

- 可见文本状态；
- Stop 操作；
- 当前 Tool 或模型生成阶段；
- 超过 15 秒后显示已运行时长。

Turn 结束后，Stats 至少显示已有事实中的：

- Turn 数和 Tool Call 数；
- 总耗时、Provider 耗时和 Tool 耗时；
- 首 Token 延迟；
- Input、Output、Reasoning 和 Cached Token；
- Cache Share；
- Cost 或 `Unpriced`；
- Context 使用率，仅在分子和容量都已知时显示。

这些值必须来自 `usage` 和 `turn.receipt`。不得从输出文案、DOM 时间或局部可见窗口推断。

### Trajectory

Trajectory 是本次体验对齐的核心交付，不是可选诊断增强，也不能用普通 Event JSON
列表代替。最终界面必须包含以下三个连续区域：

```text
Trajectory Toolbar
  -> Input / Model / Tools Timeline
  -> Virtualized Event Ledger
       -> Optional Record Inspector
```

#### Toolbar

Toolbar 固定在 Trajectory 顶部，内容滚动时保持可见：

- `Duration`：在真实持续时间和等宽事件块之间切换，并通过 `aria-pressed` 暴露状态；
- `Turns`：一次折叠或展开所有可折叠 Turn；
- `Calls`：一次折叠或展开所有 Assistant 下的 Tool Calls；
- `Search`：实时搜索当前已加载 Ledger 的类型、摘要、Tool、路径、参数和结果；
- 控件采用 20px 高度的紧凑无框按钮，Search 位于右侧；
- 窄宽度下 Search 先缩小，随后换到独立一行，不能覆盖左侧操作。

#### Timeline

时间轴固定显示三条与参考实现一致的泳道：

| 泳道 | 数据 | 表现 |
| --- | --- | --- |
| Input | User、Context、Compaction 和输入准备边界 | Neutral、Blue、Green |
| Model | Provider Sample、TTFT 和 Decode | Purple，TTFT 使用较浅前段 |
| Tools | Tool、Approval Wait 和 Verification | Amber，失败覆盖为 Red |

行为要求：

- `Duration` 开启时按真实时间比例绘制，关闭时按操作顺序等宽绘制；
- 每个 Span 最小可见宽度为 2px，不能因持续时间过短而消失；
- Hover 显示类型、开始时间、总耗时、TTFT 和 Decode Time；
- 单击 Span 选择对应 Ledger 行并打开 Inspector；
- 单击空白区域聚焦最近事件；
- 左键拖拽选择时间范围，范围外 Ledger 行降低透明度但不删除；
- 滚轮以指针位置为中心缩放；
- 平移手势在放大后的时间轴内移动可见窗口；
- 双击或按 Escape 清除范围并恢复完整时间域；
- Search 结果同步强调匹配 Span，非匹配 Span 降低透明度；
- 更早历史未加载时在时间轴左边显示边界入口，点击后加载上一页并保持当前视点。

时间轴高度、泳道位置、颜色、选区遮罩、Hover Line、边界线和状态透明度必须进入视觉
Golden，不能只测试元素存在。

#### Event Ledger

Ledger 按 `sequence` 保持权威顺序，并使用 Turn 和 Sample 边界组织内容：

| 类型 | 标签 | 默认内容 |
| --- | --- | --- |
| System | `SYSTEM` | Prompt 来源、版本、Digest 和 Token，不默认暴露完整 Prompt |
| User | `USER` | `display_prompt` |
| Context | `CONTEXT` | 来源、类型、路径、裁剪原因和 Token |
| Assistant | `ASSISTANT` | Reasoning 或 Output 的单行摘要 |
| Tool | `TOOL` | Tool 名、参数摘要、箭头和结果摘要 |
| Verification | `VERIFY` | Scope、Verdict 和证据摘要 |
| Receipt | `RECEIPT` | Outcome、Latency、Usage、Changes 和 Cost |
| Unknown | `UNKNOWN` | Kind、Identity 和安全裁剪后的 Payload |

行级要求：

- 普通内容行固定为 30px，折叠摘要行为 20px，Turn 边界单独占位；
- Event Kind 使用固定宽度标签和语义色，正文保持单行省略；
- Tool 行采用 `arguments -> result` 布局，不把原始长文本铺开；
- Assistant 行与其 Tool Calls 构成可折叠组；
- Turn 起点、Sample 起点和终点使用独立边界，不依赖相邻文案推断；
- 行 Hover 只改变背景，选中行使用稳定选中色；
- 行选择、时间轴选择和 Chat 中 Tool 的 `Inspect` 操作共享同一个 Record ID；
- Search、折叠和时间范围只改变可见 Projection，不改变底层事件集合；
- 加载旧历史后，已存在行的 React Key、选中状态和视口锚点保持不变。

虚拟化阈值不得散落为固定魔数。由机器契约中的 `rowHeight`、当前 Viewport 可见行数、
Overscan 倍数和一次 Commit 的 DOM Budget 推导；参考实现的 100 行只作为默认比较样本。

#### Record Inspector

选中 Ledger 行后，在右侧 Inspector 中显示结构化事实：

- `Summary`：Kind、Status、Turn、Sample、Call、Sequence、时间和 Duration；
- `Input`：经过授权的参数或 Prompt Metadata；
- `Output`：经过裁剪的结果、Content Handle 和 Truncation 状态；
- `Usage`：Input、Output、Reasoning、Cached Token 和 Cost；
- `Timing`：Started、Total、TTFT、Decode、Tool、Approval Wait 和 Verify；
- `Changes`：文件、Kind、Added、Removed 和 Verification；
- `Raw`：只显示已通过 Web Contract 和 Privacy Admission 的 JSON；
- `Schema`：存在 Tool Schema 时显示；
- Previous/Next 在当前过滤结果中移动，并同步时间轴选中态；
- Inspector 宽度可调整，关闭后保留当前 Record 选择，重新打开时恢复；
- Chat 中展开 Tool 后提供 `Inspect` 操作，可直接切换到 Trajectory 并定位同一 Call。

#### 数据事实来源

Trajectory 使用两类事实，职责不能混合：

1. Runtime Event 提供顺序、业务类型、状态、消息和关联 Identity；
2. Trace/Receipt 提供开始时间、结束时间、持续时间、TTFT 和阶段归属。

现有 `turn.receipt.latency` 支持 Turn 级汇总，`ToolResultData.Execution` 支持 Tool
Attempt 时间，`internal/observability/trace.Repository.QueryByTurn` 已持久化逐 Turn
Span。为达到参考时间轴能力，必须增加一个窄化、只读、脱敏的 `trace/query`：

```text
TraceQuery
  session_id
  turn_ids[]
  through_sequence

TraceSnapshot
  version
  session_id
  through_sequence
  turns[]

TraceTurn
  turn_id
  started_at
  ended_at?
  status
  spans[]

TraceSpan
  id
  parent_id?
  kind
  status
  started_at
  ended_at?
  duration_ms?
  sample?
  call_id?
```

约束：

- Query 必须在 Runtime Service 中校验 Session、Thread 和 Turn 归属；
- Web Host 只做授权、解码和序列化，不查询 SQLite 或解释 Span；
- Span `kind` 使用受控枚举，不直接透出内部名称；
- Attributes 使用 Allowlist，不返回任意 `map[string]any`；
- 不返回原始 Prompt、Secret、环境变量或完整 Tool Output；
- `through_sequence` 与 Session Snapshot 对齐，旧响应不能覆盖更新的 Event Projection；
- 运行中的 Span 来自 Runtime 的只读 Snapshot，终态后由 Durable Repository 接管；
- Active 和 Durable 交接必须按 Turn ID 幂等，不能产生重复时间块；
- Trace 不可用时保留 Event Ledger，并明确显示“Timing unavailable”，不得伪造零耗时；
- Observation 或 Trace 查询失败不能改变 Turn 结果。

#### 空、加载和失败状态

- 无 Event 时显示空 Trajectory，但 Composer 保持可用；
- 初次加载使用稳定骨架，不闪现空表；
- 更早历史加载只显示局部 Loading Row；
- Trace 尚未返回时先显示 Event Ledger，时间轴使用已知事件边界；
- Trace 返回后原位补全时长，不重排 Ledger；
- Query 失败显示非阻塞错误和 Retry，Chat 不受影响；
- 重连时保留已验证的 Snapshot，直到新 Watermark 完成替换。

## 视觉规范

### CodeHelper 卡皮巴拉 Logo

CodeHelper 使用原创卡皮巴拉作为统一品牌标记。它在 Harness 鲸鱼出现的位置承担相同
角色，但不能修改、描摹或变形复用鲸鱼路径。

#### 造型

- 主体为面向右侧、低重心站立的卡皮巴拉侧面剪影；
- 轮廓包含宽背、短颈、方圆口鼻、小圆耳和短腿；
- 不绘制可见尾巴，避免破坏物种识别；
- 眼睛使用一个简单负空间或小圆点；
- 不加入显示器、代码括号、闪电、机器人眼睛等第二主题；
- 不使用卡通笑脸、毛发纹理、写实阴影或多色插画；
- 轮廓在 16px 下仍能与熊、犬、河马和普通圆角方块区分；
- 视觉重量、水平占位和基线关系与 Harness 鲸鱼接近。

设计关键词：

```text
calm
capable
local
compact
approachable
```

#### 几何和变体

Logo 使用一个规范化 ViewBox 和同源路径生成全部尺寸：

| 变体 | 用途 | 尺寸 |
| --- | --- | ---: |
| Compact Mark | Sidebar Rail、按钮 | 24x18 |
| Hero Mark | Empty Session 标题 | 34x26 |
| Small Mark | 状态和 About | 16x12 |
| Favicon | Browser | 32x32 |
| App Icon | PWA 或安装入口 | 192x192、512x512 |
| Wordmark | 展开 Sidebar | Mark + `CodeHelper` |

约束：

- Compact、Hero 和 Small 必须使用同一轮廓，不维护三个手工近似版本；
- Favicon 和 App Icon 允许增加方形安全区，但不能改变主体比例；
- SVG 使用 `currentColor`，Light 为主文字色，Dark 为反转后的主文字色；
- 默认不使用绿色底板，品牌识别来自轮廓和 Wordmark；
- Wordmark 使用 UI 字体、18px、600 字重和 24px 行高；
- 可选 Build Revision 使用独立小型等宽 Badge，不进入 Logo 路径；
- SVG 无文本轮廓，产品名继续由真实 DOM Text 渲染；
- 与文字同时出现时 Logo 使用 `aria-hidden`；单独作为按钮时由按钮提供稳定
  Accessible Name 和 Tooltip。

#### 使用位置

- 展开 Sidebar：`CapybaraMark + CodeHelper + optional revision`；
- 折叠 Rail：静止时显示 Capybara Mark，Hover 时切换为展开侧栏图标；
- Empty Hero：`CapybaraMark + CodeHelper` 居中显示，输入区位于其下；
- Boot：加载时保留 Capybara Mark，加载状态另用状态指示器表达；
- Error Boot：Logo 不变，错误语义由 Red 状态图标和文案表达；
- Favicon、Manifest 和应用图标统一使用同一资产源；
- Tool、Terminal 和普通功能按钮继续使用 Lucide，不滥用品牌标记。

#### Logo 动效

Hero Mark 只在支持 Hover 且未启用 Reduced Motion 时执行一次轻微“探头”：

```text
0%   translate(0, 0)   rotate(0deg)
35%  translate(-1px, -1px) rotate(-3deg)
70%  translate(1px, 0) rotate(2deg)
100% translate(0, 0)   rotate(0deg)
```

- 总时长 300ms；
- 缓动使用统一 `--ch-ease-in-out`；
- 不自动循环，不在 Idle 状态持续吸引注意；
- Rail 的 Logo 到 Panel Icon 使用 100ms Crossfade；
- Reduced Motion 和 Still 下完全静止，只保留图标替换；
- 动画不能改变布局盒尺寸或触发相邻内容重排。

#### 设计验收

- 在 16px、24px、34px、192px 和 512px 下分别导出并检查；
- Light、Dark、Forced Colors 下轮廓完整；
- 1x 和 2x DPR 下边缘无明显糊边；
- 16px 轮廓识别测试中不得依赖颜色或文字；
- Sidebar、Rail、Hero 和 Favicon 截图进入 Visual Matrix；
- 品牌图形本身作为允许差异不与鲸鱼逐像素比较，但其外接框、基线、留白、颜色角色、
  Hover 反馈和切换时序必须达到同样标准。

建议实现边界：

```text
web/src/ui/brand/CapybaraMark.tsx
web/src/ui/brand/CodeHelperWordmark.tsx
web/public/favicon.svg
web/public/icon-192.png
web/public/icon-512.png
```

矢量母版是唯一事实来源，位图资产由仓库命令生成，不允许手工维护漂移版本。实现时新增
Logo Snapshot、SVG Geometry 和 Asset Generation Test。

### 布局

| 项目 | 目标 |
| --- | --- |
| Sidebar 折叠宽度 | 56px |
| Sidebar 默认宽度 | 280px |
| Sidebar 可调整范围 | 264px 至 420px |
| Center 理想最小宽度 | 640px |
| Details 默认宽度 | 360px |
| Details 可调整范围 | 300px 至 520px |
| Chat 内容宽度 | 748px |
| Composer 内容宽度 | 780px |
| Tool/Reasoning 摘要行高 | 24px |
| Trajectory 普通行高 | 30px |

这些值必须集中定义为布局 Token 或具名常量。响应式决策应由容器实际宽度和列宽求解，
不得在组件中散落匿名阈值。

列宽让步顺序：

1. 缩小 Details；
2. 自动关闭 Details；
3. 自动收起 Sidebar；
4. Center 才允许低于理想最小宽度。

用户手动设置的宽度偏好不因临时窄窗口被覆盖；窗口恢复后应恢复原偏好。

### 色彩

- 基础背景使用白色和冷中性灰；
- 品牌标记可以保留 CodeHelper Emerald；
- 交互选择和运行中信息使用 Blue；
- 成功使用 Green；
- 等待或风险提示使用 Amber；
- 失败和拒绝使用 Red；
- Reasoning 和 Assistant 轨迹允许使用克制的 Purple；
- 颜色必须来自 `--ch-*` 语义 Token，不允许组件写死业务色值；
- 颜色不能成为唯一状态信号。

### 形状和层级

- 普通 Panel、菜单、Tool Body 和设置区使用不超过 8px 的圆角；
- Composer 和 User Bubble 作为对话主控件，可使用参考实现的 22px 专用半径；
- 不创建 Card 套 Card；
- Assistant 正文、Tool 摘要和 Trajectory 表格保持无框或单层结构；
- 阴影只用于 Composer、浮层和临时悬浮按钮；
- Header、Sidebar 和 Details 使用边框区分，不使用浮动 Card。

### 排版

- 使用系统 UI 字体和独立 Code 字体栈；
- Header Title 为 14px，正文为 14px 至 16px；
- Tool、Reasoning 和 Metadata 为 12px 至 14px；
- 数字使用 Tabular Numerals；
- 不使用负 Letter Spacing；
- 长标题、路径、摘要和统计必须单行省略，并提供可访问的完整值。

## 前端状态与性能设计

### 当前问题

当前 `RuntimeClient.applyEvent` 对每个 Event：

1. 复制完整 `events` 数组；
2. 创建新的顶层 Snapshot；
3. 通知全部 Listener；
4. 触发顶层 `App` 重渲染；
5. 重新执行 `projectTranscript(snapshot.events)`；
6. 为每个 Cursor 变更串行写 Browser Storage。

这使长会话的开销随历史长度和 Delta 数量增长，并造成不必要的 IndexedDB 写放大。

### 目标结构

```text
WebSocket Event
  -> Cursor Validation
  -> Raw Event Window
  -> ConversationProjection.apply(event)
       -> keyed node update
       -> order update only when structure changes
  -> Notification Scheduler
       -> immediate: terminal / approval / input / error
       -> animation frame: output / reasoning / tool output
  -> slice subscribers
       -> Sidebar
       -> Header
       -> Chat Node
       -> Composer
       -> Details
       -> Trajectory
```

实现边界：

- `web/src/runtime`：Transport、Cursor、Hydration、Replay 和批量通知；
- `web/src/projection`：由 Event 构建不可变、只读、可重建 View Model；
- `web/src/ui`：只消费 Projection，不扫描原始 Event 推断业务状态；
- `internal/runtime/eventview`：继续是 Go Host 的 Typed Event Interpretation；
- Runtime Protocol 和 Durable Event 仍是最终事实来源。

### 发布策略

- 高频 Delta 每动画帧最多发布一次；
- Terminal、Approval、Input、Failure、Desync 和 Recovery 先冲刷已有 Delta，再同步发布；
- 组件只订阅需要的 Slice；
- 节点对象未变化时保持引用稳定；
- Cursor 每帧或短暂空闲时只持久化最新值；
- `pagehide` 和 `visibilitychange` 时刷新待写 Cursor；
- Browser Storage 丢失时允许重放，不得影响 Runtime 正确性；
- Hydration、Live Append 和 History Prepend 必须调用同一个 Projection Reducer。

## 建议代码边界

保持目录克制，不复制 Harness 的 Package 数量：

```text
web/src/
├── runtime/
│   ├── client.ts
│   ├── notifier.ts
│   └── storage.ts
├── projection/
│   ├── conversation.ts
│   ├── tool.ts
│   └── trajectory.ts
└── ui/
    ├── App.tsx
    ├── Shell.tsx
    ├── Conversation.tsx
    ├── Composer.tsx
    ├── ToolRow.tsx
    ├── Trajectory.tsx
    └── styles.css
```

`App.tsx` 只负责组合和 Overlay。单独组件必须对应真实的状态所有权或重渲染边界，
不能为了降低行数机械拆分。

## 交付顺序

### Conversation Flow

目标：先解决用户可见的主要问题，并建立后续界面的正确数据基础。

工作：

- 建立增量 Conversation Projection；
- 实现帧级 Delta 批处理和 Cursor Write-behind；
- Tool、Reasoning 和 Context 默认折叠；
- 为常用 Tool 建立结构化摘要；
- 修复贴底跟随、离底锁定、回到底部和历史前插；
- 把 Receipt 从大型正文状态 Card 改成 Turn 尾部紧凑证据；
- 增加真实 React Commit 和滚动行为测试。

完成后，同类只读任务必须直接停在最终回答处，默认不展开任何成功 Tool。

### Adaptive Workspace Shell

目标：让空状态、导航和 Composer 达到参考实现的空间关系。

工作：

- 实现卡皮巴拉 Mark、Wordmark、Favicon 和 App Icon 资产生成；
- 实现可收起 Sidebar Rail 和宽度偏好；
- 保持 Sidebar + Center 两列主布局，Context 和 Settings 通过模态层渐进披露；
- 统一 Empty 和 Active Composer；
- 将高频 Profile 控件移动到 Composer；
- 将文件、Diff 和诊断迁移到独立 Context Dialog；
- 把 Settings 改为同参考实现一致的分区模态框，并承载模型、工具、扩展和 Agent 配置；
- 补齐窄屏、宽屏、Zoom 和恢复测试。

### Runtime Feedback

目标：让用户持续知道 Agent 正在做什么，以及时间花在哪里。

工作：

- 增加 `Deep diving...` 等 Canonical Working 文案和运行时长；
- 增加 Tool Sweep、Reasoning Tail 和 Pending Dot；
- 在 Composer 下方显示基于 Receipt/Usage 的 Stats；
- 加入 Context Meter；
- 为所有动画实现 Reduced Motion 和静态替代。

### Execution Trajectory

目标：完整复刻参考实现的 Trajectory 工作台，把审计信息从 Chat 噪声中分离出来。
这是最终对齐版本的必交付能力，不允许降级为 JSON Dump、普通日志列表或跳转到外部
Observability 页面。

工作：

- 增加 Chat/Trajectory Tab；
- 建立包含 Duration、Turns、Calls 和 Search 的 Sticky Toolbar；
- 建立 Input、Model、Tools 三泳道 Timeline；
- 实现真实时长与等宽模式、拖拽选区、缩放、平移、清除和历史边界加载；
- 建立 Event Ledger、搜索、Turn/Call 折叠和 Details Inspector；
- 打通 Chat Tool `Inspect` 到 Trajectory Record 的双向定位；
- 增加脱敏的 `trace/query` 和 Active/Durable Trace 合并；
- 对长列表启用按 Viewport 和 DOM Budget 推导的虚拟化；
- 补齐 Event-only、Timing unavailable、Loading、Failure 和 Reconnect 状态。

完成后，用户必须能从宏观时间分布定位某个耗时阶段，再从时间块进入对应 Ledger 行，
查看其结构化输入、输出、Usage、Timing 和关联 Identity，并能无损返回 Chat。

### Experience Consolidation

目标：删除旧路径并让契约、测试和文档成为唯一标准。

工作：

- 删除旧的全量 `projectTranscript` 和默认展开样式；
- 更新 `web-experience-contract.json`；
- 更新 Web Visual Goldens、Feature Parity 和中文文档；
- 运行完整 Web、Protocol、Docs 和 Release Gate；
- 对真实 DeepSeek 会话执行手工验收并保留 CI Artifact。

不得长期保留旧版和新版 Chat 的运行时开关。短期开发开关只能存在于未合入分支，
正式合入前必须删除。

## 95% 复刻度计算

后续实现必须新增
`testdata/contracts/web-harness-parity.json`，将下表拆成可机器检查的检查项。
每项只记通过或失败，不进行主观部分得分。

| 领域 | 分值 | 必须覆盖 |
| --- | ---: | --- |
| Shell、品牌与响应式布局 | 12 | 卡皮巴拉品牌、三列、Rail、让步顺序、面板恢复 |
| Empty 与 Composer | 12 | 中心态、底部态、原位 Takeover、控件布局 |
| Chat 展示 | 18 | User、Assistant、Reasoning、Context、Tool、Receipt |
| 滚动与交互流程 | 13 | Follow、离底、回底、前插、展开、键盘 |
| 流式速度与渲染成本 | 15 | 首反馈、帧批处理、节点级更新、写放大 |
| Trajectory 与 Stats | 20 | 工具栏、三泳道时间轴、事件表、Inspector、联动、统计、虚拟化 |
| 动画与状态反馈 | 5 | 时长、缓动、触发条件、Reduced Motion |
| Theme、A11y 与 Zoom | 5 | Light、Dark、Forced Colors、Axe、200% |
| 合计 | 100 |  |

通过条件：

```text
score = passed_applicable_points / applicable_points * 100
score >= 95
```

以下任一失败都会阻断交付，即使总分达到 95：

- Runtime、安全、权限、审批、持久化或恢复语义回归；
- 最终回答在用户未主动离底时不可见；
- 成功 Tool 默认展开；
- 一个高频 Delta 导致一次完整 App Commit；
- Hydration、Replay 或重连产生重复消息或重复 Operation；
- 缺少卡皮巴拉 Mark、Wordmark 或 Favicon，或者 16px 下无法辨认主体轮廓；
- 缺少 Trajectory Tab、三泳道 Timeline、Event Ledger、Search、Turn/Call 折叠、
  Record Inspector 或 Chat Tool 定位中的任一核心能力；
- Trajectory 使用解析 Assistant 文案得到的伪事实，或把不可用 Timing 显示为零；
- 被验收模块没有 CodeHelper/Harness 双实例真实 Prompt 对比 Evidence；
- 390px、200% Zoom 或键盘路径出现不可达主操作；
- Light、Dark 或 Forced Colors 出现 WCAG A/AA 违规；
- Reduced Motion 下仍存在无限动画；
- 通过文案推断终态、权限、验证或 Receipt；
- 原始 Secret、未授权路径或敏感 Tool Output 进入新增日志或截图。

## 双实例真实验收

每个可独立验收的 Web 模块完成后，必须同时启动冻结参考提交的 DeepSeek Harness Web
和当前 CodeHelper Web，并对该模块执行真实对比。只运行 CodeHelper、只查看静态截图或
只使用 Fixture，均不能完成模块验收。

### 启动约束

参考实例：

```bash
cd /Users/bytedance/flow/deepseek-harness
test "$(git rev-parse HEAD)" = "11bba5f4f11328745f250674d99252c0d23e8398"
pnpm run build
pnpm dsh --profile web --port 3080
```

CodeHelper 实例：

```bash
cd /Users/bytedance/flow/CodeHelper
CODEHELPER_LOCAL_WORKSPACE="$PARITY_WORKSPACE" \
CODEHELPER_LOCAL_POSTURE=suggest \
make deepseek-web
```

执行要求：

- 两个 Web 必须在同一台机器、同一浏览器版本和同一显示设置下保持运行；
- Harness 固定使用 `127.0.0.1:3080`，CodeHelper 使用启动输出中的实际 Loopback URL；
- 两边均选择 `DeepSeek-V4-Flash` 和 `High` Reasoning；
- 两边使用语义相同的 `Workspace Write` 和需要审批的安全姿态；
- Browser Automation 必须先列出 Tab、读取 Snapshot，再执行交互；
- 不允许由自动化输入 Credential；两边只能使用各自已有的安全凭证来源；
- 启动失败、模型不可用或凭证不可用必须明确记录，不能回退到 Mock 后声称真实验收通过；
- 验收结束后关闭本轮新建的进程，不终止用户原本正在使用的实例。

### 工作区隔离

只读 Prompt 可以让两个实例读取同一个固定工作区。写入、删除、测试修复和 Approval
Prompt 必须使用两个从同一 Git Commit 创建的独立临时工作区：

```text
.tmp/web-parity/<run-id>/workspace-harness
.tmp/web-parity/<run-id>/workspace-codehelper
```

要求：

- 两个工作区的初始 Tree Digest 必须一致；
- Prompt 中不使用绝对路径，只描述相同任务；
- 每次写入场景结束后比较文件树、Diff、测试结果和终态 Receipt；
- 不在当前存在未提交工作的 CodeHelper 主工作区执行破坏性 Prompt；
- 临时工作区、真实 Session Log、Network Trace 和模型输出不得提交到 Git。

### 真实 Prompt 套件

每个模块选择与其风险相符的子集，整体验收必须覆盖全部类别：

| 类别 | 目标 | 必须观察 |
| --- | --- | --- |
| Read Analysis | 多文件只读分析 | 首反馈、Reasoning、Search/Read、最终落点 |
| Tool Chain | 连续搜索与读取 | Tool 摘要、折叠、运行动画、滚动 |
| Write Change | 小型代码修改与测试 | Approval、Diff、Changes、Receipt |
| Multi-turn | 基于上一回答继续追问 | Context、Draft、Session、Cache |
| Mid-stream Control | 运行时 Stop 或后续指导 | Stop、终态、恢复和滚动稳定 |
| Failure/Recovery | 可控失败后重试 | Error、Retry、Trace 和最终一致性 |
| Long Trajectory | 多 Sample、多 Tool、长输出 | Timeline、Ledger、虚拟化、Inspector |

Prompt 文本允许真实发送。Prompt 本身必须：

- 明确限定目标 Workspace 和是否允许修改；
- 不包含 Credential、个人数据或内部未授权内容；
- 对两个产品保持逐字一致；
- 不指定某个产品的 UI、工具名或内部实现；
- 写入任务必须能在临时工作区内确定性验证；
- 失败场景使用安全、可恢复的失败，不破坏宿主环境。

### 对比流程

每个模块验收按固定顺序执行：

1. 记录 Harness Commit、CodeHelper Commit、两边工作区 Digest、模型和浏览器版本；
2. 启动两个 Web，确认 Console 无启动错误；
3. 设置相同 Viewport、Theme、Locale、Reasoning 和访问模式；
4. 分别执行一次不计分的 Warm-up；
5. 交错执行相同 Prompt，避免固定先后顺序把 Provider Cache 或瞬时负载偏差归给产品；
6. 从点击 Send 前开始记录，到最终状态和最后一次 Paint 后结束；
7. 对目标模块执行相同点击、键盘、Hover、滚动、Resize 和切换步骤；
8. 收集截图、视频、DOM Geometry、Long Task、React Commit、Network Timing 和可见状态；
9. 对 Trajectory 同时核对 Event、Receipt、Trace、Timeline Span 和 Ledger Row Identity；
10. 运行该模块的 Fixture Regression，确认真实运行差异不是不可重复的 Provider 波动；
11. 生成差异报告并按本文评分；
12. 关闭本轮新建的实例和临时工作区。

### 速度比较

真实模型端到端速度受 Provider 波动影响，因此必须同时报告两组数据：

| 数据组 | 作用 |
| --- | --- |
| Fixture Replay | 衡量浏览器事件处理、Projection、React Commit、Paint 和滚动成本 |
| Real Prompt | 衡量真实首反馈、TTFT、持续输出、Tool 反馈和最终可用时间 |

Real Prompt 至少执行五组交错样本，报告 P50 和 P95，不使用单次最快结果。计时点：

- `submit_to_ack`：Send 到用户消息和 Working 可见；
- `event_to_paint`：浏览器收到事件到对应状态完成 Paint；
- `submit_to_first_output`：Send 到第一个可见 Reasoning 或 Output；
- `tool_start_to_visible`：Tool Start 到 Tool Row 可见；
- `terminal_to_final_view`：Terminal 到最终回答、Stats 和可用 Composer 同时稳定；
- `trajectory_open`：点击 Trajectory 到 Toolbar、Timeline 和首屏 Ledger 可交互；
- `trajectory_select`：点击 Span 到 Ledger 定位和 Inspector 可见。

CodeHelper 的 UI 路径必须达到本文性能评分，Provider 总耗时只作为背景数据，不得用于
掩盖 Browser Regression。

### 模块级验收

每个实现提交或 MR 必须声明本次覆盖的模块，并执行对应双实例场景：

| 模块 | 最小真实对比 |
| --- | --- |
| Conversation Projection | Read Analysis、Tool Chain、Long Trajectory |
| Tool Flow | Tool Chain、Write Change、Failure/Recovery |
| Scroll | Tool Chain、Mid-stream Control、Long Trajectory |
| Shell | Empty、Blank Session、窗口 Resize、Details |
| Composer | Empty、首次提交、Approval、Input、Multi-turn |
| Runtime Feedback | Read Analysis、Tool Chain、Write Change |
| Trajectory | Tool Chain、Failure/Recovery、Long Trajectory |
| Logo/Theme | Empty、Sidebar 展开/收起、Dark、Forced Colors |

涉及共享 Projection、Shell 或 Composer 的变更不能只验收单个局部组件，必须重跑受影响的
完整用户路径。

### Evidence

每次真实对比将 Evidence 写到：

```text
.tmp/web-parity/<run-id>/
├── manifest.json
├── prompts.json
├── harness/
│   ├── screenshots/
│   ├── metrics.json
│   └── browser-trace.zip
├── codehelper/
│   ├── screenshots/
│   ├── metrics.json
│   └── browser-trace.zip
└── comparison.json
```

`manifest.json` 记录 Commit、命令名称、URL、Workspace Digest、模型、Viewport、Theme
和开始结束时间，但不记录 Secret。`comparison.json` 记录原始度量、每项 Pass/Fail、
总分和失败原因。报告不得只给结论而省略测量值。

## 视觉对比方法

### 参考场景

视觉验收使用冻结 Harness 提交与 CodeHelper 的确定性 Fixture 构造语义相同的场景；
真实 Prompt 双实例运行负责验证同一场景在真实流式执行中的行为。两套证据都必须通过：

1. 首次可用的 Empty；
2. 已创建但未发送的 Blank Session；
3. 首次提交；
4. Reasoning Streaming；
5. 单个 Running Tool；
6. 连续多个成功 Tool；
7. Tool Failure；
8. Approval；
9. Structured Input；
10. Assistant Streaming；
11. Completed；
12. 用户离底；
13. Tool Expanded；
14. Details Open；
15. Trajectory；
16. Settings；
17. Long History；
18. Reconnect；
19. Dark；
20. Compact Viewport。

### 截图评分

- Viewport 使用 `390x844`、`1024x768`、`1440x900`、`1920x1080`；
- 浏览器使用 Chromium，DPR、字体、Locale 和 Reduced Motion 固定；
- 品牌名称、Logo 路径内部、仓库路径、时间戳和动态 Token 数可以 Mask；
- Logo 的外接框、基线、留白、颜色角色和 Hover 切换区域不能 Mask；
- 其余区域逐场景比较，像素一致率不得低于 95%；
- Header、Composer、Sidebar、Tool Row 和主内容轴的关键边界误差不得超过 3px；
- 文本换行、遮挡、溢出、滚动条跳动或主操作位移视为场景失败；
- 参考截图只作为 `.tmp` 或 CI Artifact，不提交未经治理的外部截图；
- CodeHelper 自身批准后的 Golden 继续提交到现有 Playwright Snapshot 目录。

### 交互评分

每个参考场景以动作序列断言，不以 DOM Snapshot 文本代替：

- 点击、键盘、焦点恢复和 Escape 关闭；
- Sidebar、Details、Tool 和 Reasoning 展开状态；
- Send、Stop、Approval、Input、Retry 和 Recovery；
- Chat/Trajectory 切换后 Composer 和滚动位置；
- Timeline 的 Duration、拖拽范围、缩放、平移、Span 选择和 Escape 清除；
- Trajectory Search、Turn/Call 折叠、Previous/Next 和 Inspector 恢复；
- Chat Tool `Inspect` 与 Trajectory Record 的双向定位；
- Session 切换后 Draft、Details 和面板偏好；
- 断线、重连、History Prepend 和 Frozen Tab 恢复。

全部关键路径必须 100% 通过。95% 只允许非关键视觉差异，不允许交互缺失。

### 性能评分

性能使用同机、同浏览器、同 Fixture Event Stream 的 Harness 与 CodeHelper 对照：

- 首个可见反馈的 P50 不慢于 Harness 的 1.10 倍；
- 首个可见反馈的 P95 不慢于 Harness 的 1.20 倍；
- 高频 Delta 对 React 的发布频率不超过每动画帧一次；
- 已稳定节点不得因其他节点 Delta 重渲染；
- 10,000 Delta 场景不得产生 10,000 次 Browser Storage 写；
- 20 个连续 Tool 的默认 DOM 高度不得超过 Harness 的 1.10 倍；
- 首屏 gzip/brotli 体积不得相对改造前 CodeHelper 基线退化超过 20%；
- 长历史和 Trajectory 滚动不得出现超过 50ms 的重复 Long Task。
- Timeline Pointer Move、拖拽选区和面板 Resize 每动画帧最多提交一次视觉更新；
- Trajectory 的 Event-only 首屏不得等待 Trace Query 才可操作。

阈值必须写入机器契约，不得在测试中散落匿名数字。CI 报告 P50、P95、React Commit、
Storage Write、DOM Node、Long Task 和 Bundle Size 原始数据。

## 测试矩阵

### Projection

- Full Rebuild 与 Incremental Apply 对任意合法 Event 序列结果一致；
- Duplicate、Out-of-order、Hydration Buffer、Replay 和 Prepend 保持 Identity；
- Tool Start/Output/Result、Reasoning、Terminal 和 Receipt 正确关联；
- Unknown Event 可读但不产生业务推断；
- `result_get` 关联失败时安全降级为独立折叠行。

### React

- Stream Delta 只更新对应 Chat Node；
- Sidebar、Header、Details 和已稳定历史行不随 Token 重渲染；
- Empty 到 Active 保持 Textarea DOM Identity 和 Draft；
- Approval/Input Takeover 不改变 Shell 尺寸；
- Tool、Reasoning 和 Context 默认折叠；
- Stats 只显示结构化事实。

### Browser

- 完整执行、审批、输入、停止、失败、恢复和完成；
- 用户贴底、离底、重新回底和 History Prepend；
- Sidebar/Details 拖拽、收起、自动让步和窗口恢复；
- Chat/Trajectory 切换、搜索、折叠和虚拟化；
- Timeline Duration/Sequence 模式、拖拽范围、缩放、平移和跨视图定位；
- Trace Loading、Unavailable、Retry、Active/Durable 交接和旧响应丢弃；
- Light、Dark、Forced Colors、Reduced Motion 和 200% Zoom；
- Axe WCAG A/AA；
- Console Error 为零。

### Runtime 与协议

Conversation Flow 和 Adaptive Workspace Shell 不修改 Runtime Protocol。
Execution Trajectory 必须增加 `trace/query`：

1. 在 `internal/host/runtimeapi/web` 增加只读 Route；
2. 在 `internal/runtime/app` 暴露窄化 Query Service；
3. 使用 `internal/observability/trace` 的 Active 和 Durable 事实；
4. 更新生成 Contract 和 TypeScript；
5. 增加 Privacy、Authorization、Ownership、Capacity 和 Replay 测试；
6. 运行 Protocol Schema、Web Parity 和 Host Journey Gate。

## 验证命令

每个交付至少运行：

```bash
npm --prefix web run check
npm --prefix web test
npm --prefix web run build
make web-e2e
make web-performance
make web-experience-check
make web-parity-check
make web-assets-check
make docs-check
git diff --check
```

修改 Web Transport 或 Protocol 时追加：

```bash
make web-protocol-check
go test ./internal/host/runtimeapi/web
go test ./internal/runtime/protocol
make host-journey-contract
```

完成全部对齐后追加：

```bash
make verify
```

真实模型验收必须执行“双实例真实验收”章节规定的完整流程。`make deepseek-web`
是 CodeHelper 的唯一受支持启动入口；Harness 使用冻结提交的 `web` Profile。不得把
凭证、Session Log、Network Authorization Header 或原始模型输出提交到仓库。

## 实施约束

1. 当前工作树包含未提交变更，实施时必须在其基础上增量修改，禁止 Reset 或覆盖。
2. 每个交付使用独立、可 Review 的 Commit；不得把全量 UI 重写压进一个提交。
3. 先建立 Projection 和性能边界，再替换视图，避免新皮肤继续依赖旧的全量重建。
4. 先完成 Chat 主路径，再做 Trajectory；不能用诊断视图延迟修复主流程。
5. Harness 的实现可以作为行为和视觉参考；直接复制源码时必须核对 MIT License、
   保留必要 Attribution，并记录在第三方声明中。
6. CSS Token、布局常量、动画时长和性能预算必须集中管理。
7. 所有新 UI 状态必须能由 Runtime Event、Receipt 或 Read Model 重建。
8. Details 保留 CodeHelper 的差异化能力，但默认不得挤压主流程。
9. 不以增加依赖替代简单实现。Trajectory 虚拟化允许使用
   `@tanstack/react-virtual`，其他依赖需单独证明收益。
10. 合入前删除临时双轨实现和开发 Feature Flag。
11. 任何降低复刻范围、改变允许差异或跳过验收项的实现偏离，都必须先更新本文并获得
    用户明确批准，不能在代码 Review 中静默改变目标。

## 完成定义

只有同时满足以下条件，才能宣布“与 DeepSeek Harness Web 体验对齐完成”：

- `web-harness-parity.json` 得分不低于 95；
- 所有阻断条件通过；
- 卡皮巴拉 Mark、Wordmark、Favicon 和 App Icon 使用同一个矢量母版；
- 20 个参考场景均有可重放 Fixture；
- 视觉矩阵、交互矩阵和性能矩阵全部通过；
- 每个模块均完成对应的 CodeHelper/Harness 双实例真实 Prompt 对比；
- 真实 DeepSeek 会话完成 Read Analysis、长 Tool Chain、Write Change、Multi-turn、
  Mid-stream Control、Failure/Recovery 和 Long Trajectory 验收；
- `.tmp/web-parity/<run-id>/comparison.json` 保留完整原始测量和评分；
- Runtime、安全和持久化测试无回归；
- 新 UI 没有复制业务 Authority；
- 文档、体验契约、Feature Parity 和 Golden 已同步；
- `make verify` 通过，或环境限制被明确记录且没有隐藏失败。

## 实施记录

2026-08-24 在分支 `feat/deepseek-harness-web-parity` 完成核心实现：

- Conversation Projection、帧级流式发布、Cursor Write-behind 和单滚动轴；
- Harness 风格 Shell、Composer、渐进披露、Stats、品牌资产和响应式状态；
- 最终回答尾部的复制与本地持久化赞踩、`+` 命令菜单和真实 `thread.compact`；
- 基于 `usage.context` 的 Context Meter 与 System、Tool、Message、Provider 分类明细；
- GFM 标题、列表、引用、代码和可横向滚动表格，以及按当前值收缩的 Profile 下拉框；
- 移除混合多种职责的全局右侧栏，将 Context 与 Settings 拆成独立模态工作台；
- 审批卡直接展示 Runtime Edit Plan，完成后的写入工具行保留文件级 Diff 摘要；
- Trajectory Ledger 增加类型图标和 Turn 标识，Inspector 改为可调宽多标签详情；
- Chat/Trajectory 双视图、三泳道 Timeline、虚拟化 Ledger、Inspector 和双向定位；
- 受 Session/Turn 归属与 Watermark 约束的脱敏 `trace/query`；
- Trajectory 独立 JS/CSS Chunk、首屏 gzip/brotli 预算和 100 分机器化复刻契约。

自动化验证通过：

- `make web-e2e`：27 个 Playwright 场景；
- `make web-performance`、`make web-experience-check`、`make web-parity-check`；
- `make web-assets-check`、`make web-supply-chain-check`、`make docs-check`；
- `make verify`，包括 99 项架构 Ratchet、Hermetic 全仓测试和 `go test -race`。

真实双实例证据保存在 `.tmp/web-parity/20260824-read-analysis/`。冻结 Harness
提交、CodeHelper、浏览器、Viewport、Theme、Reasoning 和工作区保持一致；该样本覆盖
Read Analysis、20 次只读 Tool 调用和 Long Trajectory。Write Change、Multi-turn、
Mid-stream Control 与 Failure/Recovery 尚未执行双实例真实 Prompt，因此在这些证据
补齐前不宣称满足本节的完整完成定义。
