# Web 使用指南

从源码安装一次：

```bash
make install
```

之后在任意项目目录直接启动：

```bash
cd /path/to/project
codehelper
```

当前目录自动成为 Workspace，并自动打开浏览器。服务只监听 `127.0.0.1`，默认选择
可用端口。若 Web Supervisor 已运行，再次从其他目录执行 `codehelper` 会把当前目录
注册到已有进程，并直接打开对应 Workspace。`make start` 仅作为源码开发入口保留。

## 启动参数

| 参数 | 说明 |
| --- | --- |
| `--workspace PATH` | Workspace 根目录；未设置时使用配置值 |
| `--config PATH` | TOML 配置文件 |
| `--data-dir PATH` | 持久状态目录 |
| `--host 127.0.0.1` | 监听地址；只接受 Loopback |
| `--port PORT` | 监听端口；`0` 自动选择 |
| `--open` | 启动后打开系统浏览器 |
| `--no-open` | 禁止自动打开浏览器 |
| `--enable-tools` | 启用内置 Workspace Tool |
| `--posture MODE` | `suggest`、`auto` 或 `never` |
| `--mcp-config PATH` | 版本化 MCP Stdio 配置 |
| `--provider ID` | 覆盖配置中的 Provider |
| `--model ID` | 覆盖配置中的 Model |
| `--api-key-env NAME` | 使用环境变量中的 Provider Credential |
| `--provider-fixture PATH` | 使用 Hermetic Provider Fixture |
| `--version` | 输出构建版本 |

`--host` 固定为 `127.0.0.1`，`bypass` 不允许作为 Web Posture。启动参数只负责构造
Web Host；会话、审批、输入、工具执行和持久化仍由 Runtime 负责。

## Web 工作流

页面启动后可完成：

- 添加和切换 Workspace，并创建、搜索、切换、重命名、置顶、归档和删除 Session；
- 提交 Prompt，查看流式 Text、Reasoning 和 Tool Activity；
- 处理 Approval 与结构化 Input；
- 检查 Diff、Plan、Checkpoint、Usage、Diagnostics、Task、Agent 和 Receipt；
- 浏览、搜索和预览 Workspace Resource；
- 查看三泳道 Trajectory 并在 Chat 与 Tool Record 间双向定位；
- 按 Turn、用户问题、Tool 和文件引用搜索长会话；
- 管理 Credential 与受支持的 Extension 状态。

Session 侧栏按 Workspace 分组，并将搜索、归档与行级操作渐进披露。默认标题会在首个
`turn.start` 被接受后，从用户可见 Prompt 生成单行、UTF-8 安全的短标题；已有
`New Chat` 会话在首次激活时按同一规则回填。显式重命名的标题不会被后续 Prompt 覆盖。

侧栏的文件夹加号打开 Workspace 管理界面。输入本机目录后，Supervisor 会规范化物理
路径、持久化 Registry，并为该目录构造独立 Runtime。HTTP RPC 和内容下载通过
`X-CodeHelper-Workspace-ID` 路由，WebSocket 在鉴权帧中携带 `workspace_id`；未知
Workspace、跨 Workspace Session 和内容句柄均拒绝访问。浏览器为每个 Workspace
分别保存事件 Cursor、选中 Session、草稿和反馈，后台 Workspace 的 Session 摘要以
低频轮询更新，当前 Workspace 使用实时事件流。裸 Supervisor URL 不隐式选择默认
Workspace；用户必须先选择一个 Ready Workspace，页面和 Host 才允许创建 Session。
从项目目录执行 `codehelper` 时，启动器会把该目录作为显式 Workspace 参数打开。
Git Workspace 会在侧栏显示当前本地分支，并可从本地分支列表直接切换。切换在沙箱内
执行，活动 Turn 或待处理 Operation 存在时拒绝；Git 自身仍负责拒绝会覆盖本地修改的
切换。

Composer 下方的 Stats 使用一条可整体省略的摘要展示 Turn、Tool、总耗时、模型耗时、
Tool 耗时、TTFT、Token、Cache 和 Cost；完整明细保留在 Tooltip 中，不逐项压缩。

Plan 模式只允许 Workspace Read 与有界的 Session Plan 状态更新。Agent 调研完成后通过
`submit_plan` 提交带步骤、依赖、预期证据和受影响文件的结构化 JSON 计划。Plan
Artifact 不接受 Markdown 或 XML 标签输出。计划显示在 Composer 上方，可选择
`Implement` 或 `Autopilot`。两种动作都复用持久化的 `turn.start` Acceptance；
Autopilot 的 `auto` Posture 只对该执行 Turn 生效，不修改 Session 默认值。提交计划时
会记录受影响文件摘要，执行前若文件已变化，Runtime 拒绝旧 Revision 并要求重新规划。

Act 模式可通过 `planning_policy` 将规划纳入同一工作流：

- `adaptive`（默认）允许简单、低风险的单文件操作直接执行；复杂或高风险操作先提交计划；
- `required` 要求所有有后果的操作先提交计划；
- `off` 关闭 Act 内的规划门，保留普通权限与审批检查。

`plan_approval=manual` 在提交计划后停止执行，由用户在对话中的 Plan 面板批准；
`plan_approval=auto` 允许当前 Turn 在 `submit_plan` 成功后继续。提交与批准状态只属于
当前 Turn，不写回 Session 默认审批姿态。独立 Plan 模式仍使用 Plan 模型路由；Act
内规划保持 Turn 已冻结的 Act 路由，不在一次回答中途切换模型。

创建新 Session 时，Web 会继承当前 Session 的 Approval Posture；因此用户选择 `auto`
后，新建 Session 不会重新回到 `suggest`。显式的新建参数仍优先于继承值。

内置 `deepseek-v4-flash-vision-exp` 模型声明 Image Input 与 Vision 能力，并通过
DeepSeek Responses 协议发送图片。支持图片的 Session 会在模型上下文中明确声明该能力，
避免模型仅凭通用身份说明误判为纯文本环境。

模型推理在 Chat 中显示为可折叠的 `Think` 行。运行时摘要跟随最新内容，每次模型
Sample 完成后持久化完整推理，因此重载页面或切换 Session 后仍可恢复多个独立 Think
段。Read、Bash、Grep/Glob 分别使用带行号的文件面板、Terminal 面板和分组搜索面板；
文件名与搜索结果路径可通过仅接受当前 Workspace 普通文件的 Host 接口在本机编辑器中
打开。

最终回答支持 GFM 表格、CJK 相邻强调、行内与块级数学公式、引用、嵌套列表、图片和
带语言标识的代码块。宽表格与长代码只在各自区域滚动；Markdown 文件链接通过
Workspace-bound `workspace/open` 打开。同源图片可直接显示，跨域图片必须由用户
显式加载且只允许 HTTPS；图片提供尺寸约束、加载失败、重试和下载动作。

Conversation Header 显示当前用户问题位置，并提供上一个、下一个问题和会话内搜索动作。
搜索面板可按 Turn、问题、Tool 或文件过滤；命中项使用 Runtime 派生的稳定
Entry、Turn、Call 和 Path Identity 定位。Chat 与 Trajectory 往返、切换 Session、
加载更早历史或展开 Tool 时，页面会保留当前语义阅读锚点。Transcript 使用最多
200 个业务节点的重叠分页窗口，避免长会话无限扩张 DOM。

## Session 与恢复

Browser State 是可丢弃 Projection，不是事实来源。页面先为当前 Workspace 建立
WebSocket，再获取带 `through_sequence` 的 Session Snapshot，并合并水位之后的 Live
Event。每个 Workspace 使用独立 Cursor；刷新、重连或切换 Workspace 不会重新提交
Prompt。

删除 Session 时会要求显式确认。对于已失去执行者的未完成 Turn 或隔离 Worktree，
确认删除表示同时丢弃其未完成状态和隔离改动；仍有内存执行者或恢复中 Operation 的
Session 会拒绝删除，必须先停止执行。

## 配置与凭证

首次进入且尚未完成 Runtime Setup 时，Web 不提供默认 Provider 或 Model。用户必须
选择 OpenAI、Anthropic、DeepSeek 或自定义 OpenAI-Compatible 服务，并输入准确的
Model ID；Model 不使用内置下拉枚举。自定义服务同时填写 Base URL 与
`openai_chat` / `openai_responses` 协议。Credential Value 只发送到本机 Loopback
Runtime，由 Credential Control 写入操作系统 Keyring 加密保存；浏览器不持久化原始
值，也不要求用户创建或编辑配置文件。

Web Settings 将 Workspace Connection 与 Session 配置分开：Connection 展示固定的
Provider、Endpoint、Protocol 和 Keyring Credential；Models、Reasoning、Mode、
Approval、执行目标和 Tool allowlist 属于当前 Session。Session 配置先进入 Draft，
点击 Apply 后才通过 Runtime `profile/update` 原子生效，并显示具体变更摘要。

每个 Session 独立持久化准确的 Model ID；Composer 可快速切换当前 Workspace
其他 Session 已使用的模型，Settings 也可输入新的 Model ID。同一 Workspace 中的模型
切换复用既有 Provider Connection、Endpoint 和 Keyring Credential，不扩大 Egress
范围，也不影响其他 Session。模型变化会重置该 Session 的 Prompt Cache，Active Turn
期间拒绝修改。未进入内置目录的模型只展示 Connection Baseline，并标记为未验证；
`Test connection` 检查 Endpoint、Credential 和启动模型，`Test model` 检查 Provider
模型目录是否包含当前填写的准确 Model ID。

Composer 内的 Reasoning 菜单直接采用当前模型目录声明的档位。DeepSeek 显示
Off、Low、High、Max，默认 High；其他模型保留各自完整档位，不做跨档位折算。

Credential 支持创建或轮换、在线校验和二次确认删除。Settings 还可查看 Tool 的
Policy、Constitution 和 Sandbox 信息，以及 Skill/Plugin 的来源、健康、信任、权限
和 Runtime Control 操作结果。

Agent Preset 保存经过 Runtime 校验的 Session Profile，不包含 Credential。Preset
按 Workspace 隔离并持久化，可创建、更新、复制、删除、载入 Draft，或直接应用到当前
Session；浏览器刷新和 Runtime 重启后仍可恢复。

General Settings 中可选择启用桌面通知。通知默认关闭，并且必须获得浏览器权限；
只报告后台 Session 等待审批、等待输入、失败、中断或完成，不包含 Prompt、Tool 名称
或 Tool Output。点击通知会切换到对应 Session，并定位最新 Turn 或待处理控件。
页面标题和 Session Rail 始终直接投影 Runtime Session 状态，不依赖通知权限。

## 验证

开发时使用：

```bash
make web-check
make web-test
make web-e2e
make docs-check
```

完整门禁使用 `make verify`。
