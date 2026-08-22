---
id: lab-web-feature
title: 完成 Web 端到端功能
audience:
  - contributor
prerequisites:
  - host-web
  - practice-test-layers
code_paths:
  - web/src
  - internal/host/runtimeapi/web
test_paths:
  - web/src/runtime/client.test.ts
  - internal/host/runtimeapi/web/server_test.go
source_of_truth:
  - web/src/runtime/client.ts
  - web/src/ui/App.tsx
  - testdata/contracts/web-feature-parity.json
status: draft
last_verified: null
---

# 完成 Web 端到端功能

## 目标

新增一条从浏览器 Intent 到 Runtime Event、持久化恢复和 UI Projection 的完整功能，
同时保持 Host 不拥有业务执行权。

## 步骤

1. 在 Feature Ledger 中声明能力、Runtime Owner、Endpoint、页面位置和验证证据。
2. 优先在 `internal/runtime/app` 或所属子系统实现业务语义。
3. 在 Web Server 增加严格请求 DTO、鉴权和错误映射。
4. 在 React-free `RuntimeClient` 增加调用与状态投影。
5. 在 UI 中补齐 Empty、Loading、Success、Failure 和恢复动作。
6. 添加 Go Contract Test、TypeScript Unit Test 和真实浏览器 Journey。
7. 验证窄屏、键盘、Reduced Motion、CSP 和 Secret Redaction。

```bash
go test ./internal/host/runtimeapi/web ./internal/runtime/app
npm --prefix web run check
npm --prefix web test
make web-build
make web-parity-check
```

## 证据链

```text
browser intent
 -> authenticated typed request
 -> workspace/session identity validation
 -> Runtime operation or query
 -> durable event/read model
 -> cursor-bound client projection
 -> visible state and recovery action
```

关键负向测试包括错误 Token、错误 Origin、未知字段、超大 Body、路径穿越、重复
Idempotency Key、Stale Revision、Stale Approval/Input 和 Server-declared Cursor Gap。

## 复习问题

1. 哪些逻辑属于 Runtime，哪些只属于 Web Presentation？
2. 为什么 Mutation 需要 Runtime Identity 和 Idempotency Key？
3. 为什么 Browser E2E 不能由纯 Unit Mock 代替？

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `lab-web-feature` |
| 状态 | `draft` |
| 最后验证 | 尚未验证 |
