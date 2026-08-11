# VS Code Release Evidence

This file maps the acceptance journeys to reproducible evidence. Generated
Matrix and RC reports live under `dist/matrix` and `dist/rc`.

## Automated Journeys

| Journey | Evidence |
| --- | --- |
| Empty and Loading | Electron `empty` and `workspace`; `presentation.test.ts` |
| Streaming, Stop, Retry, Continue | Electron `native`; ACP `turn/recover` contract |
| Provider, Model, Thinking | Electron `verifyModelCatalog` and `verifySessionProfile` |
| Tools search, selection, reset | Electron `verifySessionToolCatalog`; Composer tests |
| Four approval postures | Profile and Presentation exhaustive tests; trusted Electron profile update |
| Approval, Input, Verification, Receipt | Projector/Presentation tests and Electron native approval journey |
| File, Symbol, Diagnostic, Diff | Electron context capture and resource navigation |
| Session New, Switch, Background, Fork, Archive, Delete | Electron native lifecycle journey |
| Light, Dark, High Contrast | Electron Webview Client Evidence |
| Forced Colors | Electron `accessibility` with Chromium `--force-high-contrast` |
| Approximately 200% zoom | Electron Workspace journey |
| IME composition | Bundled Webview self-check plus `keyboard.test.ts` |
| Local Multi-root | Electron `multi` and Rosetta `multi` |
| Incremental Transcript | Performance gate and native Patch counter |
| Hidden Webview resume | Electron Workspace zero-post and resume-latency evidence |

The shared machine-readable IDs are defined in
`scripts/matrix/journeys.mjs`. Matrix generation fails when an automated ID is
absent.

## Panel Surface

Journey ID: `surface.panel-move`.

D-03 contributes one `WebviewView`. VS Code users may move that same view
between Sidebar and Panel. CodeHelper intentionally does not register a second
Full Editor Chat.

Reproduce the retained Panel surface:

1. Run `make vscode-package`.
2. Install `extensions/vscode/dist/codehelper-vscode-0.0.1.vsix` in a clean
   local VS Code profile.
3. Open a local `file:` workspace and focus **CodeHelper: Chat**.
4. From the Chat view header context menu, choose **Move View** and select
   **Panel**.
5. Confirm the same Session title, Transcript, Composer, Runtime status, and
   Session Rail remain interactive.
6. Move the view back to the CodeHelper Sidebar and confirm the selected
   Session is unchanged.

This manual step covers only VS Code-owned view-container movement. Runtime,
Webview, and workflow behavior are covered by automated Electron journeys.
