---
id: lab-vscode-feature
title: Complete a VS Code Feature End to End
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

# Complete a VS Code Feature End to End

English | [简体中文](../../zh-CN/13-hands-on-labs/08-vscode-feature.md)

## Goal and Prerequisites

Add one bounded native Chat journey from Host intent through ACP, Runtime
authority, incremental projection, native interaction, and release evidence.

## Procedure

1. Define a finite Webview intent or native command. Do not accept URI, path,
   Workspace identity, or executable authority from display content.
2. Resolve the selected local root in Extension Host and capture bounded native
   context where required.
3. Submit an existing typed Runtime/ACP operation; extend generated protocol
   only when the behavior is truly shared.
4. Project immutable Runtime state. Use a typed Revision Patch for incremental
   changes and Full Snapshot resync for stale bases.
5. Prefer Native Diff, Quick Pick, InputBox, Progress, Tree View, or editor APIs
   for platform interaction; keep Webview code presentation-only.
6. Define loading, streaming, approval, verification, failure, Retry/Continue,
   Checkpoint/Plan, and completion behavior with keyboard, IME, and
   accessibility paths.
7. Add unit, security, Runtime integration, Electron Journey, performance, and
   release-evidence coverage.

```bash
make vscode-check
make vscode-runtime-integration
make vscode-integration
```

For a release-relevant Journey, also run `make vscode-rosetta-integration` and
`make vscode-rc`.

## Vertical Evidence Path

Capture and retain only sanitized test evidence for:

```text
command ID
 -> trusted owning Workspace root
 -> canonical URI/version/digest/range
 -> ACP request ID + Session/Thread
 -> Runtime editor-context receipt
 -> Event Cursor
 -> immutable Host projection + Revision
 -> atomic Webview Patch or native UI
 -> machine-readable Journey ID
```

Add negative tests for untrusted Workspace, stale document version/digest,
forged remote authority, oversized Webview message, stale Patch base, unknown
Event, Cursor gap, and Runtime crash during replay. For Restore, Retry,
Continue, Duplicate, or Fork, prove that historical side effects are not
replayed.

## Gate Matrix

| Gate | Required claim |
| --- | --- |
| TypeScript unit | capture/decoder/projector state |
| security architecture | Trust, CSP, DOM, root-bound routing |
| generated checks | protocol/compatibility synchronization |
| real Runtime integration | ACP framing, replay, Runtime revalidation |
| Electron | actual local VS Code, themes, zoom, IME, native controls |
| Matrix/RC | Journey completeness, performance fields, package provenance |

If real-Runtime or Electron evidence is missing, static tests may pass but the
cross-process or platform claim remains not evaluated. Remote SSH and Dev
Container behavior are not part of this local-only product boundary.

## Expected Result

The journey respects Workspace Trust, Runtime remains authority, stale Patches
resync atomically, CSP/DOM sinks stay safe, generated files are current, and
restart/replay preserves state without repeating effects.

## Failure Diagnosis

Direct Tool execution in TypeScript violates ownership. UI-only trust checks
are insufficient. Treating a stale Patch as best-effort state corrupts
projection identity. Missing Runtime/Electron evidence must be reported rather
than inferred from unit tests.

## Cleanup

Remove temporary VSIX/test output with `npm run clean`.

## Review Questions

1. Which context is safe to submit?
2. Where is authority enforced?
3. Why preserve unknown Events?
4. Which claim remains unproven when Runtime integration is skipped?
5. Which Journey ID and RC field prove the feature at release time?

## Sources and Verification

| Item | Value |
| --- | --- |
| Catalog ID | `lab-vscode-feature` |
| Status | `draft` |
| Last verified | Not yet verified |
