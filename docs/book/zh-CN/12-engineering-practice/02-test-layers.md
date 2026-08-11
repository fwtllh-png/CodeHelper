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
  - internal/host/runtimeapi/acp/contract_test.go
  - extensions/vscode/src/test/suite/index.ts
  - extensions/vscode/src/performance/gate.test.ts
source_of_truth:
  - Makefile
  - extensions/vscode/package.json
  - extensions/vscode/scripts/matrix/journeys.mjs
status: draft
last_verified: null
---

# Unit、Contract、Integration 与 Electron Test

简体中文 | [English](../../en/12-engineering-practice/02-test-layers.md)

## 学习目标

选择能证明行为的最低成本 Test Layer，并识别何时需要 Real Binary、Transport、
Extension Host 或 Electron。

| Layer | 证明 | 命令 |
| --- | --- | --- |
| Unit/Package | Local Invariant | `go test ./path` |
| Contract | 通过 ACP 验证共享 Runtime 行为 | `make protocol-contract` |
| Binary Integration | ACP Framing/Process | `make acp-interop` |
| VS Code Static/Runtime | TS/Real Runtime | `make vscode-check`、Runtime Integration |
| Electron | 本地 Real VS Code Platform | Integration/Rosetta Target |
| Release Matrix | Journey/Artifact 完整性 | `make vscode-rc` |

Unit Test 只在 Ownership Boundary 使用 Fake。ACP Contract 验证共享 Runtime Scenario；
Binary Test 启动 Build Artifact。Electron 因下载外部 Runtime 而显式分离。
当前 VS Code Suite 有 173 项测试；Runtime Integration Lane 会实际执行 4 个
Cross-process Case，而不是接受其 Skipped 形式。

Repository Verification 通过四条 Lane 报告：

| Lane | Boundary |
| --- | --- |
| Hermetic | 不使用网络、真实凭证、GUI 或真实 HOME |
| Platform Capability | 显式 OS/Sandbox 前置条件和 Unavailable Evidence |
| Integration | 真实 Binary、ACP、VS Code Runtime Integration |
| Release | Cross-build、Race、Secret、Benchmark 与 Packaging |

Unavailable、Skipped、Failed、Passed 保持为不同结果。Lane Report 保留 Command、
Platform、Duration、Exit Code、Status 和 Reason。

## Risk-to-test Matrix

| Change Risk | Minimum Evidence |
| --- | --- |
| Parser/Formatter | Table Unit + Malformed Boundary |
| Protocol Shape | Schema/Golden + ACP 与 Host Journey Contract |
| Persistence | Restart + Corruption/Crash Window |
| Consequential Tool | Guard/Policy/Sandbox/Effect/Rollback |
| Concurrency/Lease | Forced Interleaving + Race |
| Process/Transport | Real Binary Lifecycle/Cancellation |
| VS Code Trust/Context | TS + ACP Integration；Platform API 用 Electron |
| VS Code Recovery/Projection | Runtime Artifact + Electron Retry/Continue/Plan + Patch Resync |
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
- Remote SSH/Dev Container 结果不能替代 Local-only Product Matrix。

## Native Chat Journey Matrix

Electron ARM64 覆盖 Empty、Local Workspace、Forced Colors、Native Runtime 与 Local
Multi-root；Rosetta x64 覆盖 Native Runtime/Multi-root。共享 Journey Manifest 包含
19 个 Automated ID 和 1 个 Documented Manual Panel-move Journey。缺少 Automated ID
时 Matrix 失败；Manual Journey 未记录时 RC 失败。

动态证据覆盖七类 Native Context、Resource Navigation、Light/Dark/High Contrast、
Forced Colors、约 200% Zoom、IME、Hidden Resume、Streaming Cancel、Retry/Continue、
Model Picker、Thinking、Tools、Credential Validation、Approval/Verification Receipt、
Session Lifecycle/Search 与三种 Plan Destination。

## 验证

```bash
go test ./...
make protocol-contract
make test-hermetic
cd extensions/vscode && npm run check && npm test
make vscode-rc
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
