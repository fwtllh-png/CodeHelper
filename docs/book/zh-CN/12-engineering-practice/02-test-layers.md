---
id: practice-test-layers
title: Unit、Contract、Integration 与 Browser Test
audience:
  - contributor
prerequisites:
  - practice-fixtures-smoke
  - host-web
code_paths:
  - internal
  - web
test_paths:
  - internal/host/runtimeapi/web/server_test.go
  - web/src/runtime/client.test.ts
source_of_truth:
  - Makefile
  - web/package.json
status: draft
last_verified: null
---

# Unit、Contract、Integration 与 Browser Test

## 学习目标

选择能证明行为的最低成本 Test Layer，并识别何时需要 Real Binary、Transport 或
Browser。

| Layer | 证明 | 命令 |
| --- | --- | --- |
| Unit/Package | Local Invariant | `go test ./path` |
| Contract | 通过 Web Transport 验证共享 Runtime 行为 | `make protocol-contract` |
| Binary Integration | Web Transport Framing/Process | `go test ./internal/host/runtimeapi/web` |
| Web Static/Runtime | TS/Real Runtime | `make web-check`、`make web-test` |
| Browser E2E | 真实 Binary、HTTP/WebSocket、Chromium | `make web-e2e` |
| Release Matrix | Journey/Artifact 完整性 | `make web-parity-report` |

Unit Test 只在 Ownership Boundary 使用 Fake。Web Transport Contract 验证共享 Runtime Scenario；
Binary Test 启动 Build Artifact。Playwright 使用 `--port 0` 启动同一发布 Binary，
通过 `Runtime Ready` 行取得地址，并验证真实 HTTP/WebSocket 主路径。

Repository Verification 通过四条 Lane 报告：

| Lane | Boundary |
| --- | --- |
| Hermetic | 不使用网络、真实凭证、GUI 或真实 HOME |
| Platform Capability | 显式 OS/Sandbox 前置条件和 Unavailable Evidence |
| Integration | 真实 Binary、Web Runtime Integration |
| Release | Cross-build、Race、Secret、Benchmark 与 Packaging |

Unavailable、Skipped、Failed、Passed 保持为不同结果。Lane Report 保留 Command、
Platform、Duration、Exit Code、Status 和 Reason。

## Risk-to-test Matrix

| Change Risk | Minimum Evidence |
| --- | --- |
| Parser/Formatter | Table Unit + Malformed Boundary |
| Protocol Shape | Schema/Golden + Web Transport 与 Host Journey Contract |
| Persistence | Restart + Corruption/Crash Window |
| Consequential Tool | Guard/Policy/Sandbox/Effect/Rollback |
| Concurrency/Lease | Forced Interleaving + Race |
| Process/Transport | Real Binary Lifecycle/Cancellation |
| Web Trust/Context | TS + Web Transport Integration + Playwright |
| Web Recovery/Projection | Runtime Artifact + Browser Retry/Continue/Plan + Snapshot/Event Resync |
| Release/Update | Artifact/Digest/Install/Rollback/Revoke |

Test Breadth 取决于 Changed Contract，不取决于 Changed Line Count。一行 Protocol/Security
修改可能比大型 Isolated Refactor 需要更多 Evidence。

## Test Double/Ownership

只在 Owner 暴露的 Contract 处 Fake Dependency。Fake Clock 证明 Time Decision；Fake
Transport 证明 Decoder，但不证明 OS Cleanup/Wire Framing。Fake 不应复制实现逻辑。

Skipped、Unavailable、Failed、Passed 是不同结果。外部环境 Gate 记录 Prerequisite 和
未执行原因。

## 失败边界

- Mock Transport 不能证明 Binary Framing。
- Skipped Environment Gate 不能称 Passed。
- Generated Protocol/Compatibility Drift 失败。
- Browser E2E 不替代 Runtime Package 与 Race Test。
- Remote SSH/Dev Container 结果不能替代 Local-only Product Matrix。

## Web Journey Matrix

Playwright 通过发布 Binary 的随机 loopback Port 验证 Bootstrap、Session、Prompt、
Approval/Input、Retry/Continue、Workspace Context、Credential、Profile、History、
Reconnect、Accessibility 和响应式布局。Parity Ledger 将每项旧 Host 能力绑定到精确的
Go/TypeScript/Playwright Selector；缺少 Selector、路由或已删除能力的负向证据时报告失败。

## 验证

```bash
go test ./...
make protocol-contract
make test-hermetic
cd web && npm run check && npm test
make web-build
make web-e2e
```

## 复习问题

1. Contract Risk 如何决定 Test Breadth？
2. Fake 何时弱于所声明的 Behavior？
3. 为什么 Skipped 不等于 Passed？
4. Journey Evidence 为什么不能由 Test Count 代替？

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `practice-test-layers` |
| 状态 | `draft` |
| 最后验证 | 尚未验证 |
