# Web 使用指南

CodeHelper 只提供本机 Web 产品入口。二进制直接启动 Web，不再包含子命令树：

```bash
codehelper --workspace . --config ./codehelper.toml --enable-tools
```

服务只监听 `127.0.0.1`。默认选择可用端口；增加 `--open` 可自动打开浏览器，
`--no-open` 可明确禁止自动打开。

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

- 创建、搜索、切换、重命名、置顶、归档和删除 Session；
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

Composer 下方的 Stats 使用一条可整体省略的摘要展示 Turn、Tool、总耗时、模型耗时、
Tool 耗时、TTFT、Token、Cache 和 Cost；完整明细保留在 Tooltip 中，不逐项压缩。

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

Browser State 是可丢弃 Projection，不是事实来源。页面先建立 WebSocket，再获取带
`through_sequence` 的 Session Snapshot，并合并水位之后的 Live Event。刷新或重连
不会重新提交 Prompt。

删除 Session 时会要求显式确认。对于已失去执行者的未完成 Turn 或隔离 Worktree，
确认删除表示同时丢弃其未完成状态和隔离改动；仍有内存执行者或恢复中 Operation 的
Session 会拒绝删除，必须先停止执行。

## 配置与凭证

Runtime 配置使用 TOML。Credential 必须是环境变量、受保护文件或 OS Keyring 引用，
不能把 Secret 值写入配置：

```toml
[credential]
kind = "env"
name = "OPENAI_API_KEY"

[execution]
provider = "openai"
model = "gpt-4.1"
workspace = "."
tools = true
```

Web Settings 将 Provider、Model、Reasoning、Mode、Approval、执行目标和 Tool
allowlist 暂存为一个 Draft，只有点击 Apply 后才通过 Runtime `profile/update`
原子生效。界面会明确显示未保存修改、Prompt Cache Reset、需要重启的模型或凭证变更
以及 Apply 结果。

Credential 支持创建或轮换、在线校验和二次确认删除。Credential Value 只发送到本机
Loopback Runtime，并由 Credential Control 写入系统 Keyring；浏览器不持久化原始值。
Settings 还可查看 Tool 的 Policy、Constitution 和 Sandbox 信息，以及 Skill/Plugin
的来源、健康、信任、权限和 Runtime Control 操作结果。

Agent Preset 保存经过 Runtime 校验的 Session Profile，不包含 Credential。Preset
按 Workspace 隔离并持久化，可创建、更新、复制、删除、载入 Draft，或直接应用到当前
Session；浏览器刷新和 Runtime 重启后仍可恢复。

General Settings 中可选择启用桌面通知。通知默认关闭，并且必须获得浏览器权限；
只报告后台 Session 等待审批、等待输入、失败、中断或完成，不包含 Prompt、Tool 名称
或 Tool Output。点击通知会切换到对应 Session，并定位最新 Turn 或待处理控件。
页面标题和 Session Rail 始终直接投影 Runtime Session 状态，不依赖通知权限。

## 本机 DeepSeek

仓库所有者可使用：

```bash
make deepseek-web
```

该命令构建 Binary、安装本机配置、加载安全凭证并启动 Web。详细约束见
[本机 DeepSeek 一键配置与运行](./deepseek-local.md)。

## 验证

开发时使用：

```bash
make web-check
make web-test
make web-e2e
make docs-check
```

完整门禁使用 `make verify`。
