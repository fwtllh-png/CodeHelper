---
id: lab-vscode-feature
title: 完成 VS Code 端到端功能
audience:
  - contributor
prerequisites:
  - host-vscode
  - practice-test-layers
code_paths:
  - extensions/vscode/src
  - internal/host/runtimeapi/acp
test_paths:
  - extensions/vscode/src/runtime/integration.test.ts
  - extensions/vscode/src/security/gate.test.ts
source_of_truth:
  - extensions/vscode/src/runtime/controller.ts
  - extensions/vscode/src/context/bridge.ts
status: verified
last_verified: 2026-08-06
---

# 完成 VS Code 端到端功能

简体中文 | [English](../../en/13-hands-on-labs/08-vscode-feature.md)

## 目标与前置条件

追踪 Read-only Editor Command：Capture、ACP Operation、Runtime Event、Projection、安全 UI。

## 步骤

1. 在 Extension Host 选择/新增 Finite Command。
2. 携带 Workspace Identity 捕获 Bounded Editor Context。
3. 提交 Existing Typed Operation；必要时才扩展 Generated Protocol。
4. 投影 Known/Unknown Event，不信任 Display Content。
5. 平台交互优先使用 Native Diff、Quick Pick、Progress 或 Tree View；Webview 只承担
   Chat Presentation。
6. 定义 Setup、Loading、Streaming、Approval、Verification、Failure、Recovery、
   Completion Feedback，并覆盖 Keyboard-only 与 Screen-reader Path。
7. 添加 Unit、Architecture、Experience、Runtime Integration Test。

```bash
cd extensions/vscode
npm run check
npm test -- experience
```

有下载好的 VS Code Environment 时，可选运行 Electron Gate。

## Vertical Evidence Path

只保留 Sanitized Test Evidence：

```text
command id
 -> trusted owning workspace root
 -> canonical uri/version/digest/range
 -> acp request id + session/thread
 -> runtime context receipt
 -> event cursor
 -> projector state
 -> native ui/webview
```

增加 Negative Test：Untrusted Workspace、Stale Version/Digest、Forged Remote Authority、
Oversized Message、Unknown Event、Cursor Gap、Replay 中 Runtime Crash。

## Gate Matrix

| Gate | Claim |
| --- | --- |
| TS Unit | Capture/Decoder/Projector |
| Security Architecture | Trust/CSP/DOM/Root Routing |
| Generated Check | Protocol/Compatibility Sync |
| Runtime Integration | ACP/Replay/Runtime Revalidation |
| Electron/Remote | Real VS Code Platform |

若 4 个 Real-runtime Test Skip，前三层可 Pass，但 Cross-process Claim 仍是 Not Evaluated。

## 预期结果

Command 遵守 Workspace Trust；Runtime 保持 Authority；CSP/DOM Sink 安全；Generated File
最新；Restart/Replay 保持 State。

## 失败诊断

TypeScript 直接执行 Tool 违反 Ownership；只有 UI Trust Check 不够；没有 Runtime
Fixture 而 Skip 的 Integration Test 必须报告。

## 清理

```bash
npm run clean
```

## 复习问题

1. Runtime Integration Skip 时哪项 Claim 未证明？
2. Context 为什么需要 Version/Digest？
3. Unknown Event 为什么保留为 Read-only？

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `lab-vscode-feature` |
| 状态 | `verified` |
