# Web 使用指南

从源码安装一次：

```bash
make install
```

之后在任意项目目录直接启动：

```bash
cd /path/to/project
qcode
```

当前目录自动成为 Workspace，并自动打开浏览器。服务默认监听 `127.0.0.1:6732`。
若 Web Supervisor 已运行，再次从其他目录执行 `qcode` 会把当前目录
注册到已有进程，并直接打开对应 Workspace。`make start` 仅作为源码开发入口保留。
它会使用 `--replace-owner` 比较构建身份并重启旧的开发 Supervisor；直接执行已安装的
`qcode` 仍复用现有 Supervisor。

## 启动参数

| 参数 | 说明 |
| --- | --- |
| `--workspace PATH` | Workspace 根目录；未设置时使用配置值 |
| `--replace-owner` | 构建身份变化时重启已有 Web Owner；仅供源码开发启动使用 |
| `--config PATH` | TOML 配置文件 |
| `--data-dir PATH` | 持久状态目录 |
| `--host 127.0.0.1` | 监听地址；只接受 Loopback |
| `--port PORT` | 监听端口；默认 `6732`，`0` 仅用于测试或临时隔离 |
| `--open` | 启动后打开系统浏览器 |
| `--no-open` | 禁止自动打开浏览器 |
| `--enable-tools` | 启用内置 Workspace Tool |
| `--posture MODE` | `suggest`、`auto` 或 `never` |
| `--mcp-config PATH` | State Directory 内的版本化 MCP 配置；stdio Server 还需显式 `host_trusted=true` |
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

侧栏的文件夹加号打开 Workspace 管理界面。点击 `Choose folder` 后由本地 Host 打开
操作系统目录选择器；用户选中的目录由 Supervisor 规范化物理路径、持久化 Registry，
并为该目录构造独立 Runtime，不需要在浏览器中手工输入路径。HTTP RPC 和内容下载通过
`X-QCode-Workspace-ID` 路由，WebSocket 在鉴权帧中携带 `workspace_id`；未知
Workspace、跨 Workspace Session 和内容句柄均拒绝访问。浏览器为每个 Workspace
分别保存事件 Cursor、选中 Session、草稿和反馈。当前 Workspace 使用实时事件流；
Workspace Catalog 和 Session 摘要在页面重新可见时刷新，不持续轮询 Git 状态。
Trajectory 也由新 Runtime Event 驱动增量 Trace 查询。裸 Supervisor URL 不隐式选择
默认 Workspace；用户必须先选择一个 Ready Workspace，页面和 Host 才允许创建 Session。
从项目目录执行 `qcode` 时，启动器会把该目录作为显式 Workspace 参数打开。
Workspace 管理界面可以移除任意 Workspace。移除只会注销并关闭对应 Runtime，不会
删除本机目录、Git 内容或持久化 Session。移除当前 Workspace 后，Web 自动切换到另一
个 Ready Workspace；移除最后一个后进入 Workspace 选择空态。所有 Runtime HTTP RPC、
内容下载和 WebSocket 鉴权都必须携带显式 Workspace ID，不存在默认 Workspace 回退。
Git Workspace 会在侧栏显示当前本地分支，并可从本地分支列表直接切换。切换在沙箱内
执行，活动 Turn 或待处理 Operation 存在时拒绝；Git 自身仍负责拒绝会覆盖本地修改的
切换。

Agent 不通过 `exec_command` 写 `.git`。提交与同步工作流使用结构化的 `git_add`、
`git_commit`、`git_switch`、`git_fetch`、`git_pull` 和 `git_push`；参数由 VCS
Broker 白名单校验，其中 pull 只允许 fast-forward，push 不允许 force refspec，并且
远端写入要求单次审批。`git_status`、`git_diff`、`git_log`、`git_remote`、
`git_branch`、`git_show` 和 `git_blame` 保持只读。
分支协作使用 `git_merge`、`git_rebase`、`git_cherry_pick`、`git_restore`、
`git_stash`、`git_tag` 和 `git_amend`。这些工具只接受固定参数结构；merge、rebase、
cherry-pick、restore、stash 和 amend 可能改变历史、产生冲突或丢弃未提交内容，因此
要求结构化 Plan 和单次审批。冲突不会被自动掩盖，Git 保留冲突状态供后续检查和处理。
冲突处理使用 `git_conflict`，仅允许 merge/rebase/cherry-pick 的 continue 或 abort；
不开放 `reset --hard`，continue 也不会启动交互式编辑器。
本地 `git_add`、`git_commit` 和 `git_switch` 属于有界本地变更，不会单独触发自适应
Plan；`git_pull` 和 `git_push` 仍按网络或外部变更要求 Plan。

Tool Catalog 的 `discovery_terms` 保存不授予权限的多语言检索词。首轮投影先保留核心与
当前已物化工具，再按相关度排序其他工具，并在模型声明的 Tool Definition 与 Schema
容量内填充，不再使用固定的“相关工具数量”。因此中文的 Git、Web、LSP、格式化、调试
和依赖工作流不需要先失败一次再通过 `tool_search` 补载。

`lsp_diagnostics`、`lsp_hover`、`lsp_format_edits`、`lsp_code_actions` 和
`lsp_rename_edits` 按文件类型选择已安装的 `gopls`、`clangd`、
`rust-analyzer`、`pyright-langserver`、`typescript-language-server` 或 `jdtls`。
注册状态来自实际二进制探测；同一次调用不能混用不同 Language Server。
format/code-action/rename 只返回结构化 edits，不直接修改文件，应用 edits 仍通过受
Journal 保护的文件工具完成。

`quality_verify` 依据仓库 Manifest 自动发现 Go、Rust、Node、Python、CMake、Bazel、
Maven 和 Gradle 验证入口。CMake 配置与构建目录位于沙箱私有 `$TMPDIR`，不会向
Workspace 写入生成文件。`format_code` 只格式化显式路径且进入 before-image Journal；
`debug_run` 使用 LLDB 的固定批处理参数，Workspace 保持只读；`dependency_resolve`
以禁用脚本、Workspace 只读的方式解析依赖，并要求显式声明网络目标。

安装 Chromium/Chrome 后，`web_run` 使用隔离临时 Profile 和 CDP 提供真实
navigate、DOM snapshot、click 与 fill；`QCODE_BROWSER_BINARY` 可覆盖自动探测。
本地开发地址必须显式传入 `allow_loopback`，不要把 `localhost` 或端口 `0` 写进
`network_targets`。`quality_process_smoke` 只表示宿主进程活过声明时长，不能代替
`quality_test` / `quality_verify`。`exec_command` 第一次只等到 `yield_time_ms`；
进程还在跑时会返回 `session_id`，用 `write_stdin` 继续收输出或关闭，并可用
`timeout_ms` 杀掉进程组。`http_request` 支持结构化
GET/POST/PUT/PATCH/DELETE/HEAD、响应状态断言和有界 Body；它拒绝
Authorization、Cookie、API Key 等会被持久化进 Tool Call 的敏感 Header。

安装并授权 GitHub CLI 后，`github_pr_list`、`github_pr_view`、
`github_ci_status` 和 `github_pr_create` 提供固定参数的 PR/CI 操作。创建 PR 属于
不可逆外部变更，要求 Plan 和单次审批。GitLab、内部代码托管平台和企业认证流程继续
通过 MCP 或 Skill 提供，不把平台凭据写入通用 Tool 参数。

Composer 下方的 Stats 使用一条可整体省略的摘要展示 Turn、Tool、总耗时、模型耗时、
Tool 耗时、TTFT、Token、Cache 和 Cost；完整明细保留在 Tooltip 中，不逐项压缩。

Plan 模式只允许 Workspace Read 与有界的 Session Plan 状态更新。Agent 调研完成后通过
`submit_plan` 提交带步骤、依赖、预期证据和受影响文件的结构化 JSON 计划。Plan
Artifact 不接受 Markdown 或 XML 标签输出。计划显示在 Composer 上方，并在当前 Turn
内自动继续执行，不设置额外的 Plan 审批或执行按钮。提交计划时会记录受影响文件摘要，
执行前若文件已变化，Runtime 拒绝旧 Revision 并要求重新规划。

Mode 只提供 `plan`、`act`、`operate` 三项。`act` 与 `operate` 固定使用自适应规划：
非高风险且非不可逆的 Workspace 操作直接执行，不按文件数量升级；高风险、不可逆、
网络写、外部写或 Agent 生命周期操作先提交计划。界面不再暴露独立的 Planning
Policy，避免用户同时选择模式和规划策略。

Plan 提交后始终自动批准；用户无需选择 `Implement` 或 `Autopilot`。提交状态只属于
当前 Turn，不写回 Session 默认工具审批姿态。独立 Plan 模式仍使用 Plan 模型路由；
Act 内规划保持 Turn 已冻结的 Act 路由，不在一次回答中途切换模型。新 Session 默认
使用 `approval_posture=auto`。Plan Artifact 以执行配置摘要而不是整个 Session
Profile Revision 判断是否过期；模型、工具集、审批姿态或执行目标等执行配置变化仍会
要求重新规划。

活动 Plan 的状态变化通过 `update_plan` 立即生成新的 `plan.delta`。步骤签名未变的
重写会被拒绝，不产生新的 delta。Runtime 不根据文件写入猜测业务步骤是否完成。
Plan 正文进入 Session State，下一 Turn 仍可 `update_plan` 或按步骤继续实现。仅交付
计划、没有 Workspace Mutation 的 Turn 可以在步骤仍为 pending 时结束；已经开始改
仓库时，未完成步骤仍会拒绝普通 `turn_complete`，此时应继续做完剩余步骤或声明
`incomplete`，而不是反复改同一份计划。

创建新 Session 时，Web 会继承当前 Session 的 Approval Posture；因此用户选择 `auto`
后，新建 Session 不会重新回到 `suggest`。显式的新建参数仍优先于继承值。

内置 `deepseek-v4-flash-vision-exp` 模型声明 Image Input 与 Vision 能力，并通过
DeepSeek Responses 协议发送图片。支持图片的 Session 会在模型上下文中明确声明该能力，
避免模型仅凭通用身份说明误判为纯文本环境。实际交给模型的图片同时随
`turn.started` 持久化为用户消息附件，因此发送后、刷新页面或重新进入 Session 时仍可
在对话中查看。

模型推理在 Chat 中显示为可折叠的 `Think` 行。运行时摘要跟随最新内容，每次模型
Sample 完成后持久化完整推理，因此重载页面或切换 Session 后仍可恢复多个独立 Think
段。Read、Bash、Grep/Glob 分别使用带行号的文件面板、Terminal 面板和分组搜索面板；
文件名与搜索结果路径可通过仅接受当前 Workspace 普通文件的 Host 接口在本机编辑器中
打开。macOS 默认优先使用 Visual Studio Code；未安装或无法启动 VS Code 时回退到
系统文本编辑器。Windows 和 Linux 继续使用各自的系统文件打开机制。

Turn 完成后，Chat 默认只保留用户问题和最终结论；推理、Tool、验证和交付记录收进
可展开的 `Execution details`。运行中的 Turn 保持完整展开。通过会话搜索或 Trajectory
定位某个 Tool 或文件时，所属 Turn 的执行过程会自动展开。

最终回答支持 GFM 表格、CJK 相邻强调、行内与块级数学公式、引用、嵌套列表、图片和
带语言标识的代码块。宽表格与长代码只在各自区域滚动；Markdown 文件链接通过
Workspace-bound `workspace/open` 打开。同源图片可直接显示，跨域图片必须由用户
显式加载且只允许 HTTPS；图片提供尺寸约束、加载失败、重试和下载动作。

Conversation Header 显示当前用户问题位置，并提供上一个、下一个问题和会话内搜索动作。
搜索面板可按 Turn、问题、Tool 或文件过滤；命中项使用 Runtime 派生的稳定
Entry、Turn、Call 和 Path Identity 定位。Chat 与 Trajectory 往返、切换 Session、
加载更早历史或展开 Tool 时，页面会保留当前语义阅读锚点。Transcript 使用最多
200 个业务节点的重叠分页窗口，避免长会话无限扩张 DOM。

Runtime 连接中断时，页面立即停止当前 Turn 的运行计时和操作控件，显示连接中断提示，
并禁止继续提交。自动重连会重新读取 Runtime 的持久化状态，以确认该 Turn 实际为
继续、完成或失败；Browser 不会自行伪造业务终态。

启用 Subagent 时，终态 `turn.receipt` 会记录冻结的委派模式、Spawn 尝试数、成功数和
观测结果。`delegated` 表示至少成功创建一个 Child，`blocked` 表示 Spawn 均失败，
`retained_parent` 表示 Adaptive Turn 已执行模型采样但未尝试 Spawn，
`not_evaluated` 表示没有足够的执行事实。Trajectory 直接显示该结果；它不推测或伪造
模型未委派的自然语言理由。

Chat 会把每个 Child 的状态、推理摘要、Tool 调用和最终结果聚合为可展开的 Subagent
执行块；运行中或失败的执行块默认展开，完成后可折叠。失败卡展示稳定原因
（如 `budget exhausted`、`provider rate limited`）以及输入/输出 token 用量，避免只
留下 `2 unresolved` 这类摘要。刷新或重连时，这些内容从同一组 Runtime Event 恢复。
限流较频繁的模型上，Parent 与 Child 的采样会排队而不是并行打同一 Provider；若
Child 因 `provider rate limited` 失败且标为 `retryable`，应 `wait_agent` 后再
`followup_task`，不要同时再开一批审查。
Trajectory 继续提供完整时序和 Tool Record 检查入口。
Review 子代理只能使用读文件、搜索和 `shell_read` 这类只读 process；`exec_command`
不会出现在它的工具目录里。需要编译或跑测试时应另开 Verifier，而不是让 Review
去调 Bash。

## Session 与恢复

Browser State 是可丢弃 Projection，不是事实来源。页面先为当前 Workspace 建立
WebSocket，再获取带 `through_sequence` 的 Session Snapshot，并合并水位之后的 Live
Event。每个 Workspace 使用独立 Cursor；刷新、重连或切换 Workspace 不会重新提交
Prompt。

删除 Session 时会要求显式确认。对于已失去执行者的未完成 Turn、Workspace Journal
草稿或隔离 Worktree，确认删除表示同时丢弃其未完成状态并回滚该 Session 留下的
Journal 草稿；仍有内存执行者或恢复中 Operation 的 Session 会拒绝删除，必须先停止
执行。若旧 Session 已被删除但工作区仍锁着孤儿草稿，任意剩余 Session 的
`Continue` 会接管该草稿，`Retry` 会先回滚再开新 Turn。Journal 准入失败的
Turn 即使没有 `turn.started`，这两类恢复仍然有效。

Agent 明确声明任务尚未完成并提供后续动作时，Session 显示为黄色 `Blocked`，保留
Workspace 变更并允许 `Continue`。该状态不同于红色 `Failed`，也不同于用户主动暂停
产生的 `Paused`。Blocked Session 没有活动 Turn 时，Composer 的发送动作显示为
`Continue`，输入内容作为新 Turn 的真实 User Prompt 与 Work Item Goal，并通过
Source Turn 关系绑定到最新可恢复 Turn；模型上下文只注入短胶囊（源 Turn、
terminal、Known/Open、工具结论），不会递归拼接旧输入或把源请求整封当作本轮
Goal。源 Turn 已读路径在开局写入 KnownReads，整文件重读与 git 巡视会被拒绝。
恢复请求提交后按钮保持 Pending，直到 Runtime 发布新 Turn 或明确拒绝请求。

## 配置与凭证

首次进入且尚未完成 Runtime Setup 时，Web 不提供默认 Provider 或 Model。用户必须
选择 OpenAI、Anthropic、DeepSeek 或自定义 OpenAI-Compatible 服务，并输入准确的
Model ID。自定义 Endpoint 或未进入内置目录的 Model 还必须填写 Base URL（自定义
Provider）、`openai_chat` / `openai_responses` 协议，以及 Canonical ID、Wire ID、
Context、Max Output 和完整 Capability 声明。字段为空或不一致时 Runtime 拒绝构造
Route，不会按 Model 名称或 `/models` 列表猜测能力。Credential Value 只发送到本机
Loopback Runtime，由 Credential Control 写入操作系统 Keyring 加密保存；浏览器不
持久化原始值。

Web Settings 将 Workspace Connection 与 Session 配置分开：Connection 展示固定的
Provider、Endpoint、Protocol 和 Keyring Credential；Models、Reasoning、Mode、
Approval、执行目标和 Tool allowlist 属于当前 Session。Session 配置先进入 Draft，
点击 Apply 后才通过 Runtime `profile/update` 原子生效，并显示具体变更摘要。

每个 Session 独立持久化准确的 Model ID，并可在 Composer 中切换当前连接已验证的
Catalog Model。Composer 的 `New model...` 打开独立模型配置弹窗；探测并确认元数据后，
新模型追加到当前 Connection 的模型注册表，不替换默认模型，也不迁移其他 Session。
同一 Provider、Endpoint、Protocol 和 Credential 下的注册模型可在 Turn 之间热切换；
更换连接仍由 Connection 设置负责。
模型变化会重置该 Session 的 Prompt Cache，Active Turn 期间拒绝修改。Settings 明确
显示 Limits 与 Capabilities 的来源；`Test connection` 检查 Endpoint、Credential 和
启动模型，`Test model` 只检查 Provider 模型目录是否包含 Model ID，不把该结果视为
容量或能力证明。

Composer 内的 Reasoning 菜单直接采用当前模型目录声明的档位。DeepSeek 显示
Off、Low、High、Max，默认 High；其他模型保留各自完整档位，不做跨档位折算。

Credential 支持创建或轮换、在线校验和二次确认删除。Settings 还可查看 Tool 的
Policy、Constitution 和 Sandbox 信息，以及 Skill 的来源、健康、信任、权限
和 Runtime Control 操作结果。

Agent Preset 保存经过 Runtime 校验的 Session Profile，不包含 Credential。Preset
按 Workspace 隔离并持久化，可创建、更新、复制、删除、载入 Draft，或直接应用到当前
Session；浏览器刷新和 Runtime 重启后仍可恢复。

General Settings 中可选择启用桌面通知。通知默认关闭，并且必须获得浏览器权限；
只报告后台 Session 等待审批、等待输入、阻塞、失败、暂停或完成，不包含 Prompt、Tool 名称
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
