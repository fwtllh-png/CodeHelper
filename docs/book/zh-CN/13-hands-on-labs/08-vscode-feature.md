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
  - extensions/vscode/src/test/suite/index.ts
  - extensions/vscode/src/security/gate.test.ts
  - extensions/vscode/src/performance/gate.test.ts
source_of_truth:
  - extensions/vscode/src/chat/view.ts
  - extensions/vscode/src/runtime/controller.ts
  - extensions/vscode/src/context/bridge.ts
  - extensions/vscode/scripts/matrix/journeys.mjs
status: draft
last_verified: null
---

# 完成 VS Code 端到端功能

简体中文 | [English](../../en/13-hands-on-labs/08-vscode-feature.md)

## 目标与前置条件

新增一条有界 Native Chat Journey，贯穿 Host Intent、ACP、Runtime Authority、增量
Projection、Native Interaction 与 Release Evidence。

## 步骤

1. 定义 Finite Webview Intent 或 Native Command，不从 Display Content 接受 URI、Path、
   Workspace Identity 或 Executable Authority。
2. Extension Host 解析选中的 Local Root，并按需捕获 Bounded Native Context。
3. 提交已有 Typed Runtime/ACP Operation；只有行为真正跨 Host 共享时才扩展协议。
4. 投影 Immutable Runtime State；增量变化使用 Revision Patch，Stale Base 使用 Full
   Snapshot Resync。
5. 平台交互优先使用 Native Diff、Quick Pick、InputBox、Progress、Tree View 或 Editor
   API；Webview 只承担 Presentation。
6. 定义 Loading、Streaming、Approval、Verification、Failure、Retry/Continue、
   Checkpoint/Plan 与 Completion，并覆盖 Keyboard、IME、Accessibility。
7. 添加 Unit、Security、Runtime Integration、Electron Journey、Performance 与
   Release Evidence。

```bash
make vscode-check
make vscode-runtime-integration
make vscode-integration
```

Release-relevant Journey 还要运行 `make vscode-rosetta-integration` 和
`make vscode-rc`。

## Vertical Evidence Path

只保留 Sanitized Test Evidence：

```text
command id
 -> trusted owning workspace root
 -> canonical uri/version/digest/range
 -> acp request id + session/thread
 -> runtime context receipt
 -> event cursor
 -> immutable host projection + revision
 -> atomic webview patch or native ui
 -> machine-readable journey id
```

增加 Negative Test：Untrusted Workspace、Stale Version/Digest、Forged Remote Authority、
Oversized Message、Stale Patch Base、Unknown Event、Cursor Gap、Replay 中 Runtime
Crash。对 Restore、Retry、Continue、Duplicate、Fork，还要证明不重放历史 Side Effect。

## Gate Matrix

| Gate | Claim |
| --- | --- |
| TS Unit | Capture/Decoder/Projector |
| Security Architecture | Trust/CSP/DOM/Root Routing |
| Generated Check | Protocol/Compatibility Sync |
| Runtime Integration | ACP/Replay/Runtime Revalidation |
| Electron | Local VS Code、Theme、Zoom、IME、Native Control |
| Matrix/RC | Journey 完整性、Performance Field、Package Provenance |

缺少 Real Runtime 或 Electron Evidence 时，Static Test 可以 Pass，但 Cross-process/
Platform Claim 仍是 Not Evaluated。Remote SSH、Dev Container 不属于 Local-only
产品边界。

## 预期结果

Journey 遵守 Workspace Trust；Runtime 保持 Authority；Stale Patch 原子 Resync；
CSP/DOM Sink 安全；Generated File 最新；Restart/Replay 保持 State 且不重复 Effect。

## 失败诊断

TypeScript 直接执行 Tool 违反 Ownership；只有 UI Trust Check 不够；把 Stale Patch
Best-effort 应用会破坏 Projection Identity；缺少 Runtime/Electron Evidence 时必须报告。

## 清理

```bash
npm run clean
```

## 复习问题

1. Runtime Integration Skip 时哪项 Claim 未证明？
2. Context 为什么需要 Version/Digest？
3. Unknown Event 为什么保留为 Read-only？
4. 哪个 Journey ID 和 RC Field 证明 Release-time 行为？

## 事实来源与验证

| 项目 | 值 |
| --- | --- |
| Catalog ID | `lab-vscode-feature` |
| 状态 | `verified` |
