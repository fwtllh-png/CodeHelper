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
  const source = await sourceFile("chat", "view.ts");

  assert.match(source, /color-scheme: light dark/u);
  assert.match(source, /var\(--vscode-foreground\)/u);
  assert.match(source, /var\(--vscode-focusBorder\)|var\(--vscode-input-border\)/u);
  assert.doesNotMatch(source, /#[0-9a-fA-F]{3,8}\b/u);

  assert.match(source, /aria-label="Workspace root"/u);
  assert.match(source, /aria-label="Chat session"/u);
  assert.match(source, /aria-label="Chat transcript"/u);
  assert.match(source, /aria-label="Prompt"/u);
  assert.match(source, /aria-live="polite"/u);
  assert.match(source, /aria-keyshortcuts="Control\+Enter Meta\+Enter"/u);
  assert.match(source, /aria-keyshortcuts="Escape"/u);
  assert.match(source, /<form id="composer">/u);
  assert.match(source, /<button type="submit" id="send"/u);
  assert.match(source, /:focus-visible/u);
  assert.match(source, /prefers-reduced-motion: reduce/u);
  assert.match(source, /forced-colors: active/u);

  assert.match(source, /Start a CodeHelper Chat/u);
  assert.match(source, /CodeHelper Runtime: starting/u);
  assert.match(source, /CodeHelper Runtime needs attention/u);
  assert.match(source, /Inspect and Repair/u);
  assert.match(source, /Run Setup/u);
  for (const state of [
    "Setup", "Empty", "Loading", "Streaming", "Approval", "Verify",
    "Failure", "Recovery", "Completed",
  ]) {
    assert.match(source, new RegExp(`${state} ·`, "u"));
  }

  assert.match(source, /const runtimeReady = message\.runtime\.state === 'ready'/u);
  assert.match(source, /prompt\.disabled = !runtimeReady/u);
  assert.match(source, /send\.disabled = !runtimeReady/u);
  assert.match(source, /stop\.disabled = !runtimeReady \|\| !message\.snapshot\.activeTurnId/u);
  assert.match(source, /empty\.hidden = !runtimeReady/u);
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
  assert.match(background, /createTreeView/u);
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
