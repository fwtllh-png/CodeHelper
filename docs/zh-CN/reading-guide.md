# 阅读 CodeHelper 源码

[English](../en/reading-guide.md) | 简体中文

CodeHelper 是一个体量很大的 Go 代码库。这份指南为你提供一条渐进式的阅读路线，
让你能从“不知从哪下手”走到“端到端理解一次完整的工作回合（turn）”，而不必按顺序
读完 1800+ 个文件。

请先读 [架构设计](./architecture.md)。这份指南假设你已经理解了分层模型和硬性依赖
规则；它回答的实际问题是“下一个该打开哪个文件”。

> 提示：把下面每个包和它对应的 `*_test.go` 配对阅读。CodeHelper 的测试是非常好的
> 文档：它们锁定文档里只描述未写实的契约。

## 分层一览

```text
cmd/codehelper            进程入口
internal/host             CLI、TUI、ACP 展示层
internal/runtime          协议、应用状态、Agent 循环、装配
internal/adapter          Provider、模型、工具、MCP、技能、插件
internal/security         策略、权限、宪法、沙箱
internal/orchestration    任务、worker、工作流、lane、fleet、子代理
internal/persist          SQLite、事件、会话、日志
internal/observability    用量、trace、验证、诊断
internal/platform         进程、PTY、OS 集成
internal/config           默认值、TOML、环境变量、校验
extensions/vscode         TypeScript 编辑器扩展
```

## 路线 1 — 入口与装配（最小表面）

从进程如何启动、所有东西如何被装配起来开始。

1. `cmd/codehelper/main.go` —— 只有信号处理和对 CLI 的一次调用。
2. `internal/host/cli/host.go`、`internal/host/cli/cobra.go` —— 命令树。
   `exec.go` 是单次执行命令，是最佳的端到端示例。
3. `internal/runtime/app/wire/` —— 组合根。先读 `modules_core.go` 再读
   `modules_runtime.go`。模块顺序在 `architecture.md` 中有说明；这些文件就是它背后
   的代码。
4. `internal/runtime/app/wire/runtime.go` 与 `background_executors.go` —— 外观
   （facade）与后台服务（MCP 刷新、终端 outbox、待恢复回合、worker 调度器）如何
   诞生。

这条路线回答：“一个二进制是如何变成一个运行中的 runtime 的？”

## 路线 2 — 一次操作端到端（核心循环）

这是系统的核心。沿着一条用户请求（Operation）贯穿整个 runtime。

1. `internal/runtime/app/operation_dispatch.go` —— 将类型化的 Operation 映射到
   handler。
2. `internal/runtime/app/active_turn_registry.go` —— 原子地预留 Thread 与 Turn
   身份。
3. `internal/runtime/app/runtime.go` —— Runtime 外观。文件较大但它是中心枢纽，
   先浏览方法名。
4. `internal/runtime/agent/engine/engine.go` —— Agent 引擎构建不可变的 `TurnSpec`。
5. `internal/runtime/agent/engine/turn_kernel.go` —— 推进 Turn 的 reducer；
   `turn_scope.go` 保存 Turn 局部状态。
6. `internal/runtime/agent/engine/turn_handler.go` 与 `model_handler.go` —— 实际
   的模型调用循环。
7. `internal/runtime/app/terminal_publisher.go` —— 原子化的终端提交与 outbox 发布。
8. `internal/runtime/app/eventhub/` —— 序号分配、追加、回放、订阅扇出。

请同时跟进 `application_e2e_test.go` 与 `engine_test.go` 来观察循环的实际运行。

## 路线 3 — 受守护的工具执行（安全，差异化优势）

CodeHelper 的核心承诺是每一次有副作用的工具调用都必须经过守护管线。在路线 2 之后
阅读此路线。

1. `internal/adapter/tool/tool.go` 与 `catalog.go` —— 工具身份与注册表/目录。
2. `internal/adapter/tool/execution.go` —— 进入守护的执行路径。
3. `internal/adapter/tool/guard/guard.go` —— 管线：
   policy -> approval -> journal -> sandbox。
4. `internal/adapter/tool/guard/pipeline_attempt.go` 与
   `pipeline_authorize.go` —— 步骤实现。
5. `internal/security/policy/`、`internal/security/permissions/` —— 规则与权限
   存储所在。
6. `internal/security/constitution/` —— 不可绕过的硬性规则。
7. `internal/security/sandbox/` —— OS 隔离：`policy.go`、`backend.go`，以及平台相关
   的 `workspace_fs_unix.go` / `seccomp_linux.go`。

然后阅读模型与 Provider 如何接入：

- `internal/adapter/provider/` —— Provider 抽象；读 `types.go`，再读一个具体实现，
  如 `openai/`。
- `internal/adapter/model/` —— 模型目录与探测。
- `internal/adapter/mcp/`、`internal/adapter/skill/`、`internal/adapter/plugin/`
  —— 扩展 runtime 的受治理适配器。

## 路线 4 — 状态、持久化与可观测性

Runtime 状态必须能存活且可被检查。

1. `internal/persist/` —— SQLite 基础（`sqlkit`）、`contentstore`、`joblog`、
   `session`、`snapshot`、`workspacejournal`。
2. `internal/runtime/app/persistence/` —— 仓库与生命周期恢复。
3. `internal/runtime/app/receipt.go` 与 `tool_execution_receipt.go` —— 结构化证据
   （上下文、工具、变更、审批、验证）。
4. `internal/observability/` —— 观测平面：`observation/`（证据 schema）、
   `router/`（持久路由）、`usage/`、`trace/`、`verify/`、`diagnostics/`。
5. `internal/runtime/eventview/` —— Go 主机消费的 Event 载荷的单一类型化解释。

## 路线 5 — 编排与 VS Code 扩展

面向更长生命周期、多步骤的产品侧。

1. `internal/orchestration/` —— 先读 `kernel/`（持久的 Run/Node/Attempt/Lease
   转换）与 `store/`，再读 `worker/`、`workflow/`、`lane/`、`fleet/`、
   `subagent/`、`automation/`。
2. `extensions/vscode/src/chat/projector/` —— `index.ts` 拥有序列与 Turn 身份；
   `turn-projector.ts` 穷尽分发每一个 Event Class。
3. `internal/host/tui/` 与 `internal/host/runtimeapi/` —— 其它主机表面。

## 建议的阅读顺序

- 第 1 次（1–2 小时）：README -> architecture.md -> 路线 1。
- 第 2 次：路线 2，跟着 e2e 测试走。
- 第 3 次：路线 3 —— 守护管线与沙箱。
- 第 4 次：路线 4 —— 持久化与可观测性。
- 第 5 次：路线 5 —— 编排，然后是 VS Code。

## 让这件事更容易的工具

- `go doc <package>` 查看签名。
- `go test ./path/to/package` 运行一个包的契约测试。
- 在已索引的编辑器里，对符号（`TurnCoordinator`、`operationDispatcher`、
  `guard.Guard`、`TerminalPublisher`）使用 `search_definition` /
  `search_references` 追踪归属。
- `go list -deps ./cmd/codehelper` 查看完整依赖图。
