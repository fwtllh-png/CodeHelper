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
  <div id="status">
    <select id="root" aria-label="Workspace root"></select>
    <select id="chat" aria-label="Chat session"></select>
    <button type="button" id="new-chat" title="Create isolated Chat">New</button>
    <button type="button" id="merge-chat" title="Review Chat changes">Merge</button>
    <span id="runtime" role="status" aria-live="polite">CodeHelper Runtime: starting</span>
    <span id="journey-state" role="status" aria-live="polite">Loading · Runtime starting · Wait</span>
  </div>
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
  <main id="turns" aria-label="Chat transcript"></main>
  <form id="composer">
    <textarea id="prompt" aria-label="Prompt" placeholder="Ask CodeHelper. Attach @file, @selection, @symbol, or @diagnostics."></textarea>
    <div>
      <button type="submit" id="send" aria-keyshortcuts="Control+Enter Meta+Enter">Send</button>
      <button type="button" class="secondary" id="stop" aria-keyshortcuts="Escape">Stop</button>
    </div>
    <div class="hint">Editor context is explicit and must come from the saved active file.</div>
  </form>
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
