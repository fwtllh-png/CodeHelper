import assert from "node:assert/strict";
import test from "node:test";
import { readFile } from "node:fs/promises";
import { join } from "node:path";

void test("Chat Webview keeps a nonce-only CSP and safe DOM sinks", async () => {
  const provider = await sourceFile("chat", "view.ts");
  const shell = await sourceFile("chat", "webview", "shell.ts");
  const client = await sourceFile("chat", "webview", "client.ts");
  const dom = await sourceFile("chat", "webview", "dom.ts");
  const markdown = await sourceFile("chat", "markdown.ts");
  assert.match(shell, /"default-src 'none'"/u);
  assert.match(shell, /`style-src \$\{webview\.cspSource\}`/u);
  assert.match(shell, /`script-src 'nonce-\$\{nonce\}'`/u);
  assert.match(shell, /"img-src data:"/u);
  assert.match(
    provider,
    /localResourceRoots: \[chatWebviewResourceRoot\(this\.#extensionUri\)\]/u,
  );
  assert.match(client, /\.textContent =/u);
  assert.doesNotMatch(client, /\.innerHTML\s*=/u);
  assert.doesNotMatch(client, /\beval\s*\(/u);
  assert.doesNotMatch(client, /\bnew Function\s*\(/u);
  assert.doesNotMatch(shell, /https?:\/\//u);
  assert.match(dom, /const markdownTags = new Set/u);
  assert.match(dom, /\^\(\?:https\?:\|mailto:\)/u);
  assert.match(markdown, /html: false/u);
  assert.match(markdown, /const maxMarkdownNodes = 8192/u);
  assert.match(markdown, /\["http:", "https:", "mailto:"\]/u);
  assert.doesNotMatch(markdown, /innerHTML/u);
});

void test("Runtime launch uses argv spawning and bounded diagnostics", async () => {
  const source = await sourceFile("runtime", "process.ts");
  assert.match(source, /spawn\(options\.binaryPath, args,/u);
  assert.doesNotMatch(source, /\bshell:\s*true/u);
  assert.match(source, /diagnostics\(chunk\.slice\(-stderrLimit\)\)/u);
  assert.match(source, /const stderrLimit = 64 << 10/u);
});

void test("Webview messages and editor context retain explicit size boundaries", async () => {
  const messages = await sourceFile("chat", "messages.ts");
  const context = await sourceFile("context", "bridge.ts");
  const native = await sourceFile("context", "native.ts");
  const projector = await sourceFile("chat", "projector.ts");
  assert.match(messages, /64 << 10/u);
  assert.match(context, /const maxContextFileBytes = 1 << 20/u);
  assert.match(context, /const maxProviderSymbols = 4096/u);
  assert.match(context, /const maxProviderDiagnostics = 4096/u);
  assert.match(context, /isWorkspaceDocumentScheme\(document\.uri\.scheme\)/u);
  assert.match(context, /value === "file" \|\| value === "vscode-remote"/u);
  assert.match(context, /document\.isDirty/u);
  assert.match(native, /const maxDiagnostics = 32/u);
  assert.match(native, /const maxDiagnosticMessageBytes = 8192/u);
  assert.match(native, /const maxDiagnosticMetadataBytes = 256/u);
  assert.match(projector, /values\.slice\(0, 8\)/u);
});

void test("remote execution remains inside the Workspace Extension Host", async () => {
  const controller = await sourceFile("runtime", "controller.ts");
  const processSource = await sourceFile("runtime", "process.ts");
  const compatibility = await sourceFile("compatibility", "policy.ts");
  const host = await sourceFile("workspace", "host.ts");
  const manifest = JSON.parse(await readFile(
    join(process.cwd(), "package.json"),
    "utf8",
  )) as Readonly<Record<string, unknown>>;
  assert.deepEqual(manifest["extensionKind"], ["workspace"]);
  assert.match(controller, /vscode\.ExtensionKind\.Workspace/u);
  assert.match(controller, /assertWorkspaceExtensionHost/u);
  assert.match(host, /environment\.storageScheme !== "file"/u);
  assert.match(host, /authorityMatchesRemoteName/u);
  assert.match(compatibility, /binary\.os !== expectedOS/u);
  assert.doesNotMatch(controller, /\bssh\b/u);
  assert.doesNotMatch(processSource, /\bssh\b/u);
});

void test("workspace identity and compatibility remain launch-bound", async () => {
  const process = await sourceFile("runtime", "process.ts");
  const recovery = await sourceFile("runtime", "recovery.ts");
  const session = await sourceFile("runtime", "session.ts");
  const identity = await sourceFile("workspace", "identity.ts");
  const compatibility = await sourceFile("compatibility", "policy.ts");
  const store = await sourceFile("state", "store.ts");
  assert.match(process, /"--workspace-uri"/u);
  assert.match(process, /"--workspace-root-id"/u);
  assert.match(recovery, /workspaceIdentity/u);
  assert.match(recovery, /requiredFeatures = compatibility\.required_features/u);
  assert.match(session, /workspace_identity: this\.#workspaceIdentity/u);
  assert.match(identity, /createHash\("sha256"\)\.update\(editorURI\)/u);
  assert.match(identity, /parsed\.protocol === "vscode-remote:"/u);
  assert.match(compatibility, /development CodeHelper binary is unavailable in production/u);
  assert.match(store, /codehelper\.runtimeBindings\.v1/u);
});

void test("multi-root routing remains root-bound and bounded", async () => {
  const registry = await sourceFile("workspace", "registry.ts");
  const controller = await sourceFile("runtime", "controller.ts");
  const extension = await sourceFile("extension.ts");
  const chat = await sourceFile("chat", "view.ts");
  const changes = await sourceFile("edits", "changes.ts");
  const preview = await sourceFile("edits", "preview.ts");
  const background = await sourceFile("background", "views.ts");
  assert.match(registry, /export const maxWorkspaceRoots = 8/u);
  assert.match(registry, /readonly #roots = new Map<string, ManagedRoot>/u);
  assert.match(registry, /vscode\.workspace\.getWorkspaceFolder\(uri\)/u);
  assert.match(controller, /"runtime",\s*this\.#workspaceIdentity\.root_id/u);
  assert.doesNotMatch(extension, /let controller:/u);
  assert.match(chat, /readonly #roots = new Map<string, RootChatState>/u);
  assert.match(
    changes,
    /new Map<string, Map<string, EditPlanProjector>>/u,
  );
  assert.match(background, /new Map<string, RootBackground>/u);
  assert.match(preview, /`\$\{rootId\}-\$\{planId\}`/u);
});

void test("Runtime context receipts render as read-only text", async () => {
  const source = await sourceFile("chat", "webview", "transcript.ts");
  const start = source.indexOf("function contextReceiptCard(");
  const end = source.indexOf("function approvalCard(", start);
  assert.ok(start >= 0 && end > start);
  const receiptRenderer = source.slice(start, end);
  assert.match(receiptRenderer, /appendText\(/u);
  assert.doesNotMatch(receiptRenderer, /postMessage/u);
  assert.doesNotMatch(receiptRenderer, /actionButton/u);
});

void test("Native selection commands retain trust and Runtime authority", async () => {
  const commands = await sourceFile("selection", "commands.ts");
  const flow = await sourceFile("selection", "flow.ts");
  const policy = await sourceFile("selection", "policy.ts");
  const extension = await sourceFile("extension.ts");
  assert.match(commands, /"selection_command"/u);
  assert.match(commands, /contextBridge\.capture/u);
  assert.match(commands, /controller\.submitPrompt/u);
  assert.match(flow, /spec\.requiresTrust/u);
  assert.match(flow, /isTrusted\(\)/u);
  assert.match(policy, /const maxInstructionLength = 4096/u);
  assert.doesNotMatch(commands, /WorkspaceEdit/u);
  assert.doesNotMatch(commands, /writeFile/u);
  assert.doesNotMatch(commands, /file_(?:write|edit|apply)/u);
  assert.match(
    extension,
    /context\.extensionMode === vscode\.ExtensionMode\.Production/u,
  );
});

void test("Diagnostic Code Action provider stays read-only and lazy", async () => {
  const actions = await sourceFile("diagnostics", "actions.ts");
  const flow = await sourceFile("diagnostics", "flow.ts");
  const start = actions.indexOf("export class DiagnosticCodeActionProvider");
  const end = actions.indexOf("function registerActionCommand(", start);
  assert.ok(start >= 0 && end > start);
  const provider = actions.slice(start, end);
  assert.doesNotMatch(provider, /workspace\.fs/u);
  assert.doesNotMatch(provider, /readFile/u);
  assert.doesNotMatch(provider, /submitPrompt/u);
  assert.doesNotMatch(provider, /WorkspaceEdit/u);
  assert.doesNotMatch(provider, /isPreferred/u);
  assert.match(provider, /context\.diagnostics\.slice\(0, maxCodeActionDiagnostics\)/u);
  assert.match(flow, /action === "fix"/u);
  assert.match(flow, /isTrusted\(\)/u);
});

void test("Changes review cannot bypass plan-bound Runtime approval", async () => {
  const changes = await sourceFile("edits", "changes.ts");
  const model = await sourceFile("edits", "model.ts");
  const preview = await sourceFile("edits", "preview.ts");
  const chat = await sourceFile("chat", "view.ts");
  assert.match(changes, /decodePlanFileTarget/u);
  assert.match(changes, /decodePlanDecisionTarget/u);
  assert.match(
    changes,
    /this\.#decidePlan\(target\.rootId, review\.requestId, target\.decision\)/u,
  );
  assert.match(
    chat,
    /this\.#approval\(\s*root\.rootId, session\.sessionId, message\.requestId/u,
  );
  assert.match(chat, /sessionKey\(root\.rootId, sessionId, current\.requestId\)/u);
  assert.match(changes, /this\.#find\(\s*target\.rootId/u);
  assert.match(changes, /this\.#projector\(root\.rootId, sessionId\)/u);
  assert.match(chat, /current\.editPlan\?\.id/u);
  assert.match(model, /const maxPlanFiles = 128/u);
  assert.match(model, /const maxPlanHistory = 16/u);
  assert.match(model, /const maxPlanTotalBytes = 8 << 20/u);
  assert.match(preview, /const maxPreviewDocuments = 256/u);
  assert.match(preview, /const maxPreviewBytes = 16 << 20/u);
  assert.match(preview, /"vscode\.diff"/u);
  assert.doesNotMatch(preview, /WorkspaceEdit/u);
  assert.doesNotMatch(preview, /workspace\.fs\.(?:writeFile|delete|rename)/u);
  assert.doesNotMatch(changes, /setInterval/u);
});

void test("Changes Tree keeps keyboard labels and theme-aware visuals", async () => {
  const changes = await sourceFile("edits", "changes.ts");
  assert.match(changes, /"Approve once"/u);
  assert.match(changes, /"Deny"/u);
  assert.match(changes, /item\.tooltip =/u);
  assert.match(changes, /item\.command =/u);
  assert.match(changes, /new vscode\.ThemeIcon/u);
  assert.match(changes, /"diff-added"/u);
  assert.match(changes, /"diff-modified"/u);
  assert.match(changes, /"diff-removed"/u);
  assert.doesNotMatch(changes, /new vscode\.ThemeColor/u);
});

void test("binary updates retain cache, concurrency, and disk budgets", async () => {
  const extension = await sourceFile("extension.ts");
  const update = await sourceFile("binary", "update.ts");
  const store = await sourceFile("binary", "store.ts");
  assert.match(extension, /updateCheckInFlight/u);
  assert.match(extension, /manifestCache\(context\.globalState\)/u);
  assert.match(update, /"if-none-match"/u);
  assert.match(update, /response\.status === 304/u);
  assert.match(update, /AbortSignal\.timeout\(30_000\)/u);
  assert.match(update, /maximumBytes/u);
  assert.match(store, /content\.byteLength \* 2 \+ \(16 << 20\)/u);
  assert.match(store, /statfs\(path\)/u);
});

async function sourceFile(...segments: string[]): Promise<string> {
  return readFile(join(process.cwd(), "src", ...segments), "utf8");
}
