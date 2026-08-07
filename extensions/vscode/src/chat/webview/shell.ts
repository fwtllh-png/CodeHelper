import { randomBytes } from "node:crypto";

import * as vscode from "vscode";

export function chatWebviewResourceRoot(
  extensionUri: vscode.Uri,
): vscode.Uri {
  return vscode.Uri.joinPath(extensionUri, "dist");
}

export function renderChatHTML(
  webview: vscode.Webview,
  extensionUri: vscode.Uri,
): string {
  const nonce = randomBytes(24).toString("base64");
  const resourceRoot = chatWebviewResourceRoot(extensionUri);
  const stylesheet = webview.asWebviewUri(
    vscode.Uri.joinPath(resourceRoot, "chat-webview.css"),
  );
  const script = webview.asWebviewUri(
    vscode.Uri.joinPath(resourceRoot, "chat-webview.js"),
  );
  const csp = [
    "default-src 'none'",
    `style-src ${webview.cspSource}`,
    `script-src 'nonce-${nonce}'`,
    "img-src data:",
  ].join("; ");
  return `<!doctype html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <meta http-equiv="Content-Security-Policy" content="${escapeAttribute(csp)}">
  <link rel="stylesheet" href="${escapeAttribute(stylesheet.toString())}">
  <title>CodeHelper Chat</title>
</head>
<body>
  <div id="app">
    <section id="chat-pane" aria-label="CodeHelper Chat">
      <header id="chat-header">
        <div class="header-identity">
          <span class="header-eyebrow">CHAT</span>
          <strong id="chat-title">CodeHelper</strong>
        </div>
        <div class="header-actions" role="toolbar" aria-label="Chat actions">
          <select id="root" aria-label="Workspace root"></select>
          <button type="button" id="new-chat" class="icon-button" title="New Session" aria-label="New Session">＋</button>
          <button type="button" id="merge-chat" class="compact-button" title="Review Chat changes">Merge</button>
          <button type="button" id="toggle-sessions" class="icon-button" title="Show Sessions" aria-label="Show Sessions" aria-controls="session-rail" aria-expanded="false">☰</button>
        </div>
      </header>

      <div id="chat-content">
        <section id="repair" role="status" aria-live="polite" hidden>
          <strong>CodeHelper Runtime needs attention</strong>
          <p id="repair-detail"></p>
          <button type="button" id="repair-runtime">Inspect and Repair</button>
          <button type="button" class="secondary" id="run-setup">Run Setup</button>
        </section>
        <section id="empty" hidden>
          <strong>Start a CodeHelper Chat</strong>
          <p>Describe a task below, or attach saved editor context with @file, @selection, @symbol, or @diagnostics.</p>
        </section>
        <main id="turns" aria-label="Chat transcript" tabindex="0"></main>
      </div>

      <footer id="composer-region">
        <form id="composer">
          <textarea id="prompt" aria-label="Prompt" placeholder="Ask CodeHelper or describe a coding task…"></textarea>
          <div id="composer-toolbar" role="toolbar" aria-label="Prompt controls">
            <div class="composer-controls">
              <button type="button" id="add-context" class="composer-control" title="Attach editor context">＋</button>
              <button type="button" id="mode-control" class="composer-control" disabled>Mode</button>
              <button type="button" id="provider-control" class="composer-control" disabled>Provider</button>
              <button type="button" id="model-control" class="composer-control" disabled>Model</button>
              <button type="button" id="thinking-control" class="composer-control" disabled>Thinking</button>
              <button type="button" id="credential-control" class="composer-control" disabled>Key</button>
              <button type="button" id="approval-control" class="composer-control" disabled>Approval</button>
              <span class="composer-control passive" title="Tool configuration follows in Session Profile">Tools</span>
            </div>
            <div class="composer-actions">
              <button type="button" id="stop" class="secondary" aria-keyshortcuts="Escape">Stop</button>
              <button type="submit" id="send" aria-keyshortcuts="Control+Enter Meta+Enter" title="Send">↵</button>
            </div>
          </div>
        </form>
        <div id="composer-status">
          <span id="environment">Local</span>
          <span id="approval-posture">Default approvals</span>
          <span id="runtime" role="status" aria-live="polite">CodeHelper Runtime: starting</span>
          <span id="journey-state" role="status" aria-live="polite">Loading · Runtime starting · Wait</span>
        </div>
      </footer>
    </section>

    <aside id="session-rail" aria-label="Sessions">
      <header class="session-header">
        <strong>SESSIONS</strong>
        <button type="button" id="close-sessions" class="icon-button" aria-label="Close Sessions">×</button>
      </header>
      <button type="button" id="rail-new-chat" class="new-session-button">New Session</button>
      <label class="session-search">
        <span class="visually-hidden">Search Sessions</span>
        <input id="session-search" type="search" placeholder="Search Sessions" autocomplete="off">
      </label>
      <div class="session-group-label">
        <span>RECENT</span>
        <span id="session-count">0</span>
      </div>
      <nav id="session-list" aria-label="Recent Sessions"></nav>
    </aside>
    <button type="button" id="session-scrim" aria-label="Close Sessions"></button>
  </div>
  <script nonce="${nonce}" src="${escapeAttribute(script.toString())}"></script>
</body>
</html>`;
}

function escapeAttribute(value: string): string {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll('"', "&quot;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;");
}
