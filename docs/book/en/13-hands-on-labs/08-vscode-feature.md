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
  - extensions/vscode/src/runtime/integration.test.ts
  - extensions/vscode/src/security/gate.test.ts
source_of_truth:
  - extensions/vscode/src/runtime/controller.ts
  - extensions/vscode/src/context/bridge.ts
status: verified
last_verified: 2026-08-06
---

# Complete a VS Code Feature End to End

English | [简体中文](../../zh-CN/13-hands-on-labs/08-vscode-feature.md)

## Goal and Prerequisites

Trace a read-only editor command from capture through ACP Operation, Runtime
Event, projection, and safe Webview/native UI rendering.

## Procedure

1. Add or select a finite command in the Extension Host.
2. Capture bounded editor context with Workspace identity.
3. Submit an existing typed Operation; extend generated Protocol only if needed.
4. Project known and unknown Events without trusting display content.
5. Add unit, architecture, and Runtime integration tests.

```bash
cd extensions/vscode
npm run check
npm test
```

Optionally run the Electron gate when its downloaded VS Code environment is
available.

## Vertical Evidence Path

Capture and retain only sanitized test evidence for:

```text
command ID
 -> trusted owning Workspace root
 -> canonical URI/version/digest/range
 -> ACP request ID + Session/Thread
 -> Runtime editor-context receipt
 -> Event Cursor
 -> projector state
 -> native UI/Webview output
```

Add negative tests for untrusted Workspace, stale document version/digest,
forged remote authority, oversized Webview message, unknown Event, Cursor gap,
and Runtime crash during replay.

## Gate Matrix

| Gate | Required claim |
| --- | --- |
| TypeScript unit | capture/decoder/projector state |
| security architecture | Trust, CSP, DOM, root-bound routing |
| generated checks | protocol/compatibility synchronization |
| real Runtime integration | ACP framing, replay, Runtime revalidation |
| Electron/remote | actual VS Code/Remote API behavior |

If the four real-Runtime tests are skipped, the first three gates may pass but
the cross-process claim remains not evaluated.

## Expected Result

The command respects Workspace Trust, Runtime remains authority, CSP/DOM sinks
stay safe, generated files are current, and restart/replay preserves state.

## Failure Diagnosis

Direct Tool execution in TypeScript violates ownership. UI-only trust checks
are insufficient. Integration tests skipped without a Runtime fixture must be
reported as skipped.

## Cleanup

Remove temporary VSIX/test output with `npm run clean`.

## Review Questions

1. Which context is safe to submit?
2. Where is authority enforced?
3. Why preserve unknown Events?
4. Which claim remains unproven when Runtime integration is skipped?

## Sources and Verification

| Item | Value |
| --- | --- |
| Catalog ID | `lab-vscode-feature` |
| Status | `verified` |
