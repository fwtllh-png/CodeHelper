# Web 主入口架构

> 状态：已交付。本文描述当前 Web Host 契约。VS Code/ACP 已退出产品架构，不再保留
> 迁移计划或兼容叙事。

## 产品边界

`codehelper web` 是默认交互入口。它在一个 Go 进程内构造持久化 Runtime，提供：

- 编译进 Binary 的 React 静态资源；
- 严格类型化的同源 HTTP RPC；
- 只下发 Runtime Event 的 WebSocket；
- Runtime 启动失败时仍可访问的 Boot Surface。

Web 只绑定 `127.0.0.1`。当前不支持 LAN、公网部署、反向代理、多用户认证或通用
REST/SSE Host。CLI 和 TUI 仍使用同一 Runtime 语义；Worker/Automation 使用独立的
Durable Lease。

## 组件边界

```text
Browser
  -> web/src/runtime RuntimeClient
  -> HTTP RPC + authenticated WebSocket
  -> internal/host/runtimeapi/web
  -> narrow runtime application services
  -> Operation / Query / Event
  -> internal/runtime/app
  -> Agent / Guard / Persistence
```

浏览器只提交 Intent 并维护可丢弃 Projection。它不直接调用 Provider、Tool 或
Sandbox，不创建第二套 Session、Approval、Compaction 或 Recovery 状态机。

`internal/host/runtimeapi/web` 只负责：

- Loopback、Host、Origin 与 Capability Token 校验；
- 请求大小、连接数、读写 Deadline 和 Content 下载边界；
- JSON 解码、Method 路由与 Problem 映射；
- Runtime Service 调用；
- Event Replay 与 WebSocket 生命周期；
- 静态资源和 Boot Readiness。

业务校验、身份归属、幂等、终态和恢复都属于 Runtime。

## 启动与 Readiness

Web Host 分两阶段启动：

1. 先建立最小 Boot Surface 和随机 Capability Token；
2. 构造 Runtime、恢复 Durable State、启动后台服务；
3. Readiness 成功后开放 Mutation 和完整应用。

Runtime 构造失败时页面仍能展示结构化错误，但所有业务 Mutation 保持关闭。关闭时先
停止 Admission，再关闭 Listener/WebSocket，最后按 `ResourceStack` 逆序关闭 Runtime。

## 安全

Web 安全边界包括：

- 只监听 Loopback；
- 拒绝不匹配的 Host 与 Origin；
- 每进程随机 Capability Token；
- WebSocket 建立后必须先发送认证帧；
- 不启用 CORS；
- 未分类 Operation 默认不暴露；
- Workspace Path、Session、Thread、Turn、Request 和 Plan Identity 均由服务端重解析；
- Credential Value 只进入 OS Keyring，不进入 Event、日志或 Browser Storage；
- Content 下载只允许 Runtime 已授权的资源。

`bypass` 也不能绕过 Constitution、Workspace Fence 或 Sandbox 硬边界。

## Snapshot、Replay 与 Hydration

浏览器状态可完全由 Runtime 重建：

1. 打开 WebSocket，并开始暂存 Live Event；
2. 获取带 `through_sequence` 的 `SessionPresentationSnapshot`；
3. 丢弃不高于该水位的重复 Live Event；
4. 合并更高 Sequence 的暂存事件；
5. 提交全局 Cursor，进入 Live Projection。

Sequence 只要求严格单调，不要求连续。只有服务端明确返回 Retention Gap 时客户端才
清空 Projection 并重新 Hydrate；合法的跨 Session Sequence 空洞不能被误判为 Desync。

Mutation 使用 Idempotency Key。刷新、重连和浏览器崩溃只触发 Query/Replay，不重新提交
Prompt、Approval 或 Side Effect。

## Session 列表与高频更新

Session 列表不是按固定短周期轮询。Client 通过 Runtime Lifecycle Event 判断列表是否
失效，并合并同一批次中的刷新请求；当前 Session 的 Transcript 则直接消费 Event
Projection。Hydration 期间到达的 Event 先缓冲，避免 Snapshot 与 Live State 交叉覆盖。

这一区分很重要：

- Event 是高频增量通道；
- Snapshot 是有水位的一致重建；
- Session List 是低频 Query Projection；
- Browser Cache 不是 Authority。

## Runtime Health 与 Usage

`system/diagnostics.runtime_health` 从 Runtime Active Registry 和各 Thread Engine 的
内存 Recorder 读取 Active Turn、Provider Call、Tool Execution 与 Pending
Interaction。终态 Trace 的 Durable Source 是 Terminal Envelope 中冻结的
Measurement；原始 `spans` 表不是健康判断的唯一依据。

Web 查询 Session Usage 时默认 `include_children=true`，因此父 Session 汇总包含
`agent_nodes` 下 Child Turn 的消耗。关闭该参数可读取 Direct Usage；两种视图都保留
原始 Child Session/Thread/Turn 归属。

## 前端状态边界

`web/src/runtime` 负责 Transport、Cursor、Hydration、缓存和重连；
`web/src/projection` 负责由 Event 生成只读 View Model；`web/src/ui` 负责 React 呈现。

UI 必须覆盖：

- Session 创建、搜索、切换、重命名、置顶、归档和删除；
- Text、Reasoning、Tool、Approval、Input、Verification 与 Terminal；
- Profile、Provider、Model、Tool、MCP 与 Extension 状态；
- Diff、Plan、Checkpoint、Task、Agent、Usage 和 Receipt；
- Workspace 浏览、搜索、只读预览和资源定位；
- Runtime 不可用、断线、恢复、空状态和容量错误。

UI 不能根据文案猜测 Turn 是否完成，也不能从局部 Event 推断权限或验证结论。

## 容量与背压

容量由 `internal/host/runtimeapi/web/capacity.go` 和生成契约定义。Server 对请求体、
WebSocket Frame、Replay 页、连接、Session、下载和静态资源设置明确边界。Slow
Consumer 会被关闭并释放连接槽，不得阻塞 Runtime Event 排序路径。

Transcript 使用有界 Projection 和分页历史。高频 Delta 在提交 React 前按事件批次合并，
避免每个 Token 触发全页面渲染。

## 契约事实来源

| 内容 | 事实来源 |
| --- | --- |
| API Surface | `internal/host/runtimeapi/web/contract.go` |
| JSON/TS Shape | `docs/protocol/web-host.contract.json`、`web/src/protocol/web-host.generated.ts` |
| Operation Exposure | `internal/host/runtimeapi/web/web-operation-exposure.json` |
| Feature Parity | `testdata/contracts/web-feature-parity.json` |
| Host Journey | `testdata/contracts/host-journey-contract.json` |
| 体验约束 | `testdata/contracts/experience-contract.json` |
| Runtime Client | `web/src/runtime/client.ts` |
| Web Server | `internal/host/runtimeapi/web/server.go` |

## 验证

```bash
make web-protocol-check
make web-parity-check
npm --prefix web run check
npm --prefix web test
make web-performance
make web-assets-check
```

发布候选还需执行真实 Browser E2E、持续 Streaming、供应链检查和 Release Drill；具体
入口以 `Makefile` 为准。

## Review 检查表

- 新 API 是否只调用窄化 Runtime Service？
- 新 Operation 是否在 Exposure Contract 中显式分类？
- Snapshot 是否绑定明确 Watermark？
- 重连是否可能重复 Mutation？
- Session 列表刷新是否由 Event 失效驱动并合并？
- Workspace/Credential 输入是否在服务端重新校验？
- Slow Consumer、关闭和启动失败是否释放资源？
- 新 UI 状态是否来自结构化 Event/Receipt，而不是文本推断？
