---
id: practice-test-layers
title: Unit、Contract、Integration 与 Electron Test
audience:
  - contributor
prerequisites:
  - practice-fixtures-smoke
  - host-vscode
code_paths:
  - internal
  - extensions/vscode
test_paths:
  - internal/host/runtimeapi/http/contract_test.go
  - extensions/vscode/src/runtime/integration.test.ts
source_of_truth:
  - Makefile
  - extensions/vscode/package.json
status: verified
last_verified: 2026-08-06
---

# Unit、Contract、Integration 与 Electron Test

简体中文 | [English](../../en/12-engineering-practice/02-test-layers.md)

## 学习目标

选择能证明行为的最低成本 Test Layer，并识别何时需要 Real Binary、Transport、
Extension Host 或 Electron。

| Layer | 证明 | 命令 |
| --- | --- | --- |
| Unit/Package | Local Invariant | `go test ./path` |
| Contract | Transport 一致行为 | `make protocol-contract` |
| Binary Integration | Framing/Process | `make api-contract`、`make acp-interop` |
| VS Code Static/Runtime | TS/Real Runtime | `make vscode-check`、Runtime Integration |
| Electron/Remote | Real VS Code Platform | Matrix Target |

Unit Test 只在 Ownership Boundary 使用 Fake。Contract 在 HTTP/ACP 复用 Scenario；Binary
Test 启动 Build Artifact。Electron/Remote SSH/Dev Container 因下载外部 Runtime 而显式
分离。

## Risk-to-test Matrix

| Change Risk | Minimum Evidence |
| --- | --- |
| Parser/Formatter | Table Unit + Malformed Boundary |
| Protocol Shape | Schema/Golden + HTTP/ACP Contract |
| Persistence | Restart + Corruption/Crash Window |
| Consequential Tool | Guard/Policy/Sandbox/Effect/Rollback |
| Concurrency/Lease | Forced Interleaving + Race |
| Process/Transport | Real Binary Lifecycle/Cancellation |
| VS Code Trust/Context | TS + ACP Integration；Platform API 用 Electron |
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
- Electron 不进入普通 `verify`。

## 验证

```bash
go test ./...
make protocol-contract
cd extensions/vscode && npm run check && npm test
```

## 复习问题

1. Contract Risk 如何决定 Test Breadth？
2. Fake 何时弱于所声明的 Behavior？
3. 为什么 Skipped 不等于 Passed？

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `practice-test-layers` |
| 状态 | `verified` |
| 最后验证 | 2026-08-06 |
