---
id: host-web
title: React Web 工作区与 Runtime Authority
audience:
  - contributor
  - operator
  - agent
prerequisites:
  - host-web-transport
  - context-workspace-index-editor
code_paths:
  - web/src
  - internal/host/runtimeapi/web
test_paths:
  - web/src/runtime/client.test.ts
  - internal/host/runtimeapi/web/server_test.go
source_of_truth:
  - web/src/runtime/client.ts
  - web/src/ui/App.tsx
  - web/src/ui/styles.css
status: draft
last_verified: null
---

# React Web 工作区与 Runtime Authority

## 学习目标

理解 React 工作区如何投影 Runtime 的 Session 和 Event，而不建立第二套业务状态机。

## 页面结构

Web 首屏是实际工作区，不是营销页。宽屏使用 Session Rail、Transcript/Composer 和
Detail 三栏；窄屏压缩 Session Rail，并把 Detail 变成覆盖面板。Composer 固定在主
工作流底部，Approval 和 Input 会接管该位置。

`RuntimeClient` 不依赖 React。它负责 Bootstrap、Unary RPC、Snapshot Hydration、
WebSocket Cursor、重连和有限本地 Projection；React 通过
`useSyncExternalStore` 订阅不可变快照。

## 用户工作流

- Session Rail 支持创建、选择和搜索持久化 Session。
- Transcript 展示用户输入、Markdown 输出、Reasoning、Tool 和 Terminal State。
- Composer 支持提交、停止、Approval Decision 与 Input Reply。
- Detail 展示 Profile、Changes、Checkpoint、Plan、Task、Agent、Usage 和 Extension。
- Workspace 面板提供受边界限制的搜索与文本资源查看。
- Session Export 下载带 SHA-256 完整性字段的 JSON。
- Settings 提供主题、Credential 状态与 Keyring 操作、Runtime Diagnostics。

所有 consequential action 仍经过 Runtime Policy、Approval、Journal 与 Sandbox。UI
按钮只发出 Intent，不直接执行工具或修改工作区。

## 安全与可访问性

生产资源嵌入 Go Binary，构建不包含 Source Map。页面使用严格 CSP、同源连接和
Capability Token。Markdown 不启用原始 HTML。文件资源由服务端重新校验，Secret
不进入日志、浏览器持久化或 API 响应。

控件使用稳定 Accessible Name 和键盘路径；状态同时使用文字、图标与颜色。Light、
Dark、窄屏和 `prefers-reduced-motion` 共享 `--ch-*` Token。

## 测试与验证

```bash
npm --prefix web run check
npm --prefix web test
npm --prefix web run build
go test ./internal/host/runtimeapi/web
make experience-baseline
```

真实浏览器回归至少覆盖创建 Session、提交 Turn、流式输出、完成状态、Approval/Input、
窄屏布局、主题、Workspace Resource 和重连。

## 复习问题

1. 为什么 `RuntimeClient` 必须与 React 解耦？
2. 为什么客户端不能根据 Sequence 数值空洞自行判定 Desync？
3. Approval Composer 为什么必须绑定 Runtime Request ID？
4. 为什么 Credential Secret 不能进入 Client Snapshot？

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `host-web` |
| 状态 | `draft` |
| 最后验证 | 尚未验证 |
