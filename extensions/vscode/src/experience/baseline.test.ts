import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import test from "node:test";

void test("shared experience contract exposes the required baseline", async () => {
  const value = JSON.parse(await readFile(
    join(process.cwd(), "..", "..", "docs", "experience-contract.json"),
    "utf8",
  )) as Readonly<Record<string, unknown>>;
  assert.equal(value["version"], 1);
  assert.deepEqual(value["scope"], ["tui", "vscode"]);
  assert.deepEqual(
    arrayObjects(value["states"]).map((state) => state["id"]),
    ["idle", "working", "waiting", "succeeded", "degraded", "failed", "blocked"],
  );
  assert.deepEqual(
    arrayObjects(value["lifecycle_feedback"]).map((state) => state["id"]),
    [
      "setup", "empty", "loading", "streaming", "approval", "verify",
      "failure", "recovery", "completed",
    ],
  );
  assert.deepEqual(
    Object.keys(object(object(value["tokens"])["semantic_color"])).sort(),
    ["danger", "focus", "info", "neutral", "success", "warning"],
  );
  assert.equal(array(value["review_checklist"]).length, 11);
});

void test("Chat covers theme, keyboard, focus, and explicit state baselines", async () => {
  const shell = await sourceFile("chat", "webview", "shell.ts");
  const client = await sourceFile("chat", "webview", "client.ts");
  const styles = await sourceFile("chat", "webview", "styles.css");
  const presentation = await sourceFile("chat", "presentation.ts");

  assert.match(styles, /color-scheme: light dark/u);
  assert.match(styles, /var\(--vscode-foreground\)/u);
  assert.match(
    styles,
    /var\(--vscode-focusBorder\)|var\(--vscode-input-border\)/u,
  );
  assert.doesNotMatch(styles, /#[0-9a-fA-F]{3,8}\b/u);

  assert.match(shell, /aria-label="Workspace root"/u);
  assert.match(shell, /aria-label="Sessions"/u);
  assert.match(shell, /aria-label="Chat transcript"/u);
  assert.match(shell, /aria-label="Prompt"/u);
  assert.match(shell, /aria-live="polite"/u);
  assert.match(shell, /aria-keyshortcuts="Control\+Enter Meta\+Enter"/u);
  assert.match(shell, /aria-keyshortcuts="Escape"/u);
  assert.match(shell, /<form id="composer">/u);
  assert.match(shell, /<button type="submit" id="send"/u);
  assert.match(styles, /:focus-visible/u);
  assert.match(styles, /prefers-reduced-motion: reduce/u);
  assert.match(styles, /forced-colors: active/u);

  assert.match(shell, /Start a CodeHelper Chat/u);
  assert.match(shell, /CodeHelper Runtime: starting/u);
  assert.match(shell, /CodeHelper Runtime needs attention/u);
  assert.match(shell, /Inspect and Repair/u);
  assert.match(shell, /Run Setup/u);
  for (const state of [
    "Setup", "Empty", "Loading", "Streaming", "Approval", "Verify",
    "Failure", "Recovery", "Completed",
  ]) {
    assert.match(presentation, new RegExp(`${state} ·`, "u"));
  }

  assert.match(
    client,
    /prompt\.disabled = !message\.presentation\.promptEnabled/u,
  );
  assert.match(client, /send\.disabled = !message\.presentation\.sendEnabled/u);
  assert.match(client, /stop\.disabled = !message\.presentation\.stopEnabled/u);
  assert.match(client, /empty\.hidden = !message\.presentation\.emptyVisible/u);
  assert.match(
    presentation,
    /const runtimeReady = runtimeState === "ready"/u,
  );
});

void test("workbench keeps primary views prominent and uses native controls", async () => {
  const manifest = JSON.parse(await readFile(
    join(process.cwd(), "package.json"),
    "utf8",
  )) as {
    readonly contributes: {
      readonly views: {
        readonly codehelper: readonly {
          readonly id: string;
          readonly visibility?: string;
        }[];
      };
    };
  };
  const views = manifest.contributes.views.codehelper;
  assert.deepEqual(views.slice(0, 4).map((view) => view.id), [
    "codehelper.chat",
    "codehelper.changes",
    "codehelper.threads",
    "codehelper.agents",
  ]);
  for (const id of [
    "codehelper.agents", "codehelper.tasks", "codehelper.jobs", "codehelper.usage",
  ]) {
    assert.equal(views.find((view) => view.id === id)?.visibility, "collapsed");
  }

  const preview = await sourceFile("edits", "preview.ts");
  const setup = await sourceFile("setup", "commands.ts");
  const chat = await sourceFile("chat", "view.ts");
  const background = await sourceFile("background", "views.ts");
  assert.match(preview, /"vscode\.diff"/u);
  assert.match(setup, /withProgress/u);
  assert.match(chat, /showQuickPick/u);
  assert.match(chat, /createQuickPick<ToolPickItem>/u);
  assert.match(background, /createTreeView/u);
});

void test("Chat DOM keeps the pre-refactor journey structure and safe sinks", async () => {
  const shell = await sourceFile("chat", "webview", "shell.ts");
  const client = await sourceFile("chat", "webview", "client.ts");
  const transcript = await sourceFile("chat", "webview", "transcript.ts");
  const ordered = [
    '<header id="chat-header">',
    '<div id="chat-content">',
    '<section id="repair"',
    '<section id="empty"',
    '<main id="turns" aria-label="Chat transcript"',
    '<footer id="composer-region">',
    '<form id="composer">',
    '<aside id="session-rail"',
  ];
  let cursor = -1;
  for (const marker of ordered) {
    const next = shell.indexOf(marker);
    assert.ok(next > cursor, `${marker} must retain its DOM order`);
    cursor = next;
  }
  for (const id of [
    "root", "new-chat", "merge-chat", "toggle-sessions", "runtime",
    "journey-state", "repair-runtime", "run-setup", "turns", "composer",
    "prompt", "send", "stop", "session-rail", "session-list", "session-search",
  ]) {
    assert.match(shell, new RegExp(`id="${id}"`, "u"));
  }
  assert.match(shell, /role="status" aria-live="polite"/u);
  assert.match(transcript, /document\.createDocumentFragment\(\)/u);
  assert.match(transcript, /container\.replaceChildren\(fragment\)/u);
  assert.doesNotMatch(client, /\.innerHTML\s*=/u);
  assert.doesNotMatch(client, /insertAdjacentHTML/u);
});

void test("Chat exposes the native two-pane responsive layout contract", async () => {
  const shell = await sourceFile("chat", "webview", "shell.ts");
  const client = await sourceFile("chat", "webview", "client.ts");
  const styles = await sourceFile("chat", "webview", "styles.css");

  assert.match(shell, /id="chat-header"/u);
  assert.match(shell, /id="composer-toolbar"/u);
  assert.match(shell, /id="composer-status"/u);
  assert.match(shell, /id="session-rail" aria-label="Sessions"/u);
  assert.match(shell, /id="session-search" type="search"/u);
  assert.match(shell, /id="session-list" aria-label="Recent Sessions"/u);
  assert.match(styles, /grid-template-columns: minmax\(0, 1fr\)/u);
  assert.match(styles, /@media \(max-width: 720px\)/u);
  assert.match(styles, /body\.sessions-open #session-rail/u);
  assert.match(styles, /var\(--vscode-list-activeSelectionBackground\)/u);
  assert.match(client, /\["ArrowDown", "ArrowUp", "Home", "End"\]/u);
  assert.match(client, /aria-current/u);
  assert.match(client, /function isNearBottom\(\)/u);
  assert.match(client, /if \(stickToBottom\) turns\.scrollTo/u);
  assert.doesNotMatch(client, /window\.scrollTo/u);
});

void test("Chat hardening virtualizes Sessions and pauses hidden DOM work", async () => {
  const client = await sourceFile("chat", "webview", "client.ts");
  const styles = await sourceFile("chat", "webview", "styles.css");
  const view = await sourceFile("chat", "view.ts");

  assert.match(client, /computeVirtualWindow/u);
  assert.match(client, /sessionVirtualItems/u);
  assert.match(client, /aria-posinset/u);
  assert.match(client, /aria-setsize/u);
  assert.match(client, /indicator\.setAttribute\("aria-hidden", "true"\)/u);
  assert.match(client, /event\.key === "Escape"/u);
  assert.match(client, /event\.key !== "Tab"/u);
  assert.match(client, /virtualItemOffset/u);
  assert.match(styles, /contain: strict/u);
  assert.match(styles, /content-visibility: auto/u);
  assert.match(styles, /contain-intrinsic-size/u);

  assert.match(view, /onDidChangeVisibility/u);
  assert.match(view, /!this\.#view\.visible/u);
  assert.match(view, /this\.#view\?\.visible === true/u);
  assert.match(view, /#deferredError/u);
  assert.match(view, /\}, 16\);/u);
});

void test("release matrix and RC consume one local job manifest", async () => {
  const matrix = await readFile(
    join(process.cwd(), "scripts", "matrix", "report.mjs"),
    "utf8",
  );
  const jobs = await readFile(
    join(process.cwd(), "scripts", "matrix", "jobs.mjs"),
    "utf8",
  );
  const journeys = await readFile(
    join(process.cwd(), "scripts", "matrix", "journeys.mjs"),
    "utf8",
  );
  const evidence = await readFile(
    join(process.cwd(), "RELEASE-EVIDENCE.md"),
    "utf8",
  );
  const rc = await readFile(
    join(process.cwd(), "scripts", "release", "rc-report.mjs"),
    "utf8",
  );
  const packaging = await readFile(
    join(process.cwd(), "scripts", "release", "vscode-matrix.mjs"),
    "utf8",
  );
  assert.match(matrix, /matrixJobs as expected/u);
  assert.match(matrix, /journeyEvidence/u);
  assert.match(matrix, /missing_journeys/u);
  assert.match(rc, /requiredMatrixJobNames/u);
  assert.match(rc, /journeyEvidence/u);
  assert.match(rc, /requiredMatrixJobs\.size/u);
  assert.doesNotMatch(rc, /15\/15/u);
  assert.match(jobs, /local-darwin-arm64-external/u);
  assert.match(jobs, /local-darwin-x64-external/u);
  assert.match(journeys, /runtime\.retry-continue/u);
  assert.match(journeys, /surface\.panel-move/u);
  assert.match(evidence, /`surface\.panel-move`/u);
  assert.match(rc, /Remote SSH/u);
  assert.match(rc, /WSL remote workspaces/u);
  assert.match(packaging, /const extensionBundleFiles/u);
  assert.match(packaging, /"dist\/chat-webview\.js"/u);
  assert.match(packaging, /"dist\/chat-webview\.css"/u);
  assert.match(
    packaging,
    /for \(const file of extensionBundleFiles\)[\s\S]*copyFile/u,
  );
});

void test("Setup and Repair preserve trust and consequential-action rules", async () => {
  const source = await sourceFile("setup", "commands.ts");
  const messages = await sourceFile("chat", "messages.ts");

  assert.match(source, /if \(!vscode\.workspace\.isTrusted\)/u);
  assert.match(source, /Do not enter a secret value/u);
  assert.match(source, /showWarningMessage\(\s*repairMessage/u);
  assert.match(source, /\{ modal: true \}/u);
  assert.match(source, /"Run Setup"/u);
  assert.match(source, /"Restart Runtime"/u);
  assert.match(messages, /case "repair-runtime":/u);
  assert.match(messages, /case "run-setup":/u);
});

async function sourceFile(...segments: string[]): Promise<string> {
  return readFile(join(process.cwd(), "src", ...segments), "utf8");
}

function array(value: unknown): readonly unknown[] {
  assert.ok(Array.isArray(value));
  return value;
}

function arrayObjects(value: unknown): readonly Readonly<Record<string, unknown>>[] {
  return array(value).map((entry) => object(entry));
}

function object(value: unknown): Readonly<Record<string, unknown>> {
  assert.ok(typeof value === "object" && value !== null && !Array.isArray(value));
  return value as Readonly<Record<string, unknown>>;
}
