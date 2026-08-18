import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import test from "node:test";

void test("shared experience contract exposes the required baseline", async () => {
  const value = JSON.parse(await readFile(
    join(
      process.cwd(), "..", "..", "testdata", "contracts",
      "experience-contract.json",
    ),
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
  assert.match(shell, /aria-keyshortcuts="Enter"/u);
  assert.match(shell, /<form id="composer">/u);
  assert.match(shell, /<button type="button" id="send"/u);
  assert.doesNotMatch(shell, /id="stop"/u);
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
  assert.match(client, /turnActive = message\.presentation\.stopEnabled/u);
  assert.match(
    client,
    /send\.disabled = turnActive\s*\? !message\.presentation\.stopEnabled\s*: !message\.presentation\.sendEnabled/su,
  );
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
  assert.deepEqual(views.slice(0, 3).map((view) => view.id), [
    "codehelper.chat",
    "codehelper.changes",
    "codehelper.agents",
  ]);
  for (const id of ["codehelper.agents", "codehelper.usage"]) {
    assert.equal(views.find((view) => view.id === id)?.visibility, "collapsed");
  }
  for (const id of [
    "codehelper.threads", "codehelper.tasks", "codehelper.jobs",
  ]) {
    assert.equal(views.some((view) => view.id === id), false);
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

void test("VS Code turns default to an unset step budget", async () => {
  const manifest = JSON.parse(await readFile(
    join(process.cwd(), "package.json"),
    "utf8",
  )) as {
    readonly contributes: {
      readonly configuration: {
        readonly properties: Readonly<Record<string, {
          readonly default?: unknown;
          readonly scope?: string;
        }>>;
      };
    };
  };
  assert.equal(
    manifest.contributes.configuration.properties[
      "codehelper.runtime.maxSteps"
    ]?.default,
    0,
  );
  const controller = await sourceFile("runtime", "controller.ts");
  assert.match(
    controller,
    /configuration\.get<number>\("runtime\.maxSteps", 0\)/u,
  );
  assert.equal(
    manifest.contributes.configuration.properties[
      "codehelper.runtime.configPath"
    ]?.scope,
    "machine-overridable",
  );
});

void test("VS Code forwards trusted Workspace MCP configuration to ACP", async () => {
  const manifest = JSON.parse(await readFile(
    join(process.cwd(), "package.json"),
    "utf8",
  )) as {
    readonly contributes: {
      readonly configuration: {
        readonly properties: Readonly<Record<string, {
          readonly default?: unknown;
          readonly scope?: string;
        }>>;
      };
    };
  };
  const setting = manifest.contributes.configuration.properties[
    "codehelper.runtime.mcpConfigPath"
  ];
  assert.equal(setting?.default, "");
  assert.equal(setting.scope, "resource");

  const controller = await sourceFile("runtime", "controller.ts");
  const processSource = await sourceFile("runtime", "process.ts");
  const extension = await sourceFile("extension.ts");
  assert.match(controller, /vscode\.workspace\.isTrusted\s*\?\s*resolveMCPConfigPath/u);
  assert.match(processSource, /\["--mcp-config", options\.mcpConfigPath\]/u);
  assert.match(
    extension,
    /affectsConfiguration\("codehelper\.runtime\.mcpConfigPath"\)/u,
  );
});

void test("deleting the last Session replaces it with an empty Session", async () => {
  const controller = await sourceFile("runtime", "controller.ts");
  const start = controller.indexOf("public async deleteChat(");
  const end = controller.indexOf("public async checkpoints(", start);
  assert.ok(start >= 0 && end > start);
  const deletion = controller.slice(start, end);
  assert.match(
    deletion,
    /runtime\.summaries\.size === 1\s*\? await this\.createChat\(\)/u,
  );
  assert.match(
    deletion,
    /await this\.deleteChat\(replacement\.sessionId\)\.catch/u,
  );
  assert.ok(
    deletion.indexOf("await this.createChat()") <
      deletion.indexOf("SessionLifecycleCommands(runtime).delete"),
  );
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
    "prompt", "send", "session-rail", "session-list", "session-search",
  ]) {
    assert.match(shell, new RegExp(`id="${id}"`, "u"));
  }
  assert.match(shell, /role="status" aria-live="polite"/u);
  assert.match(transcript, /document\.createDocumentFragment\(\)/u);
  assert.match(transcript, /container\.replaceChildren\(fragment\)/u);
  assert.doesNotMatch(client, /\.innerHTML\s*=/u);
  assert.doesNotMatch(client, /insertAdjacentHTML/u);
});

void test("Chat exposes a collapsible responsive Sessions layout", async () => {
  const shell = await sourceFile("chat", "webview", "shell.ts");
  const client = await sourceFile("chat", "webview", "client.ts");
  const transcript = await sourceFile("chat", "webview", "transcript.ts");
  const styles = await sourceFile("chat", "webview", "styles.css");

  assert.match(shell, /id="chat-header"/u);
  assert.match(shell, /id="composer-toolbar"/u);
  assert.match(shell, /id="composer-status"/u);
  assert.match(shell, /id="session-rail" aria-label="Sessions"/u);
  assert.match(shell, /id="session-search" type="search"/u);
  assert.match(shell, /id="session-list" aria-label="Recent Sessions"/u);
  assert.doesNotMatch(shell, /session-filter/u);
  assert.doesNotMatch(shell, /session-count/u);
  assert.doesNotMatch(shell, /header-eyebrow/u);
  assert.match(styles, /body\.sessions-open #app/u);
  assert.match(styles, /grid-template-columns: minmax\(0, 1fr\)/u);
  assert.match(styles, /grid-template-rows: minmax\(0, 1fr\)/u);
  assert.match(styles, /@media \(max-width: 900px\)/u);
  assert.doesNotMatch(styles, /#app\s*\{\s*display: block/u);
  assert.match(styles, /body\.sessions-open #session-rail/u);
  assert.match(
    styles,
    /grid-template-rows: auto auto auto minmax\(0, 1fr\)/u,
  );
  assert.match(styles, /var\(--vscode-list-activeSelectionBackground\)/u);
  assert.match(client, /sessionRail\.setAttribute\("aria-hidden", String\(!open\)\)/u);
  assert.match(client, /if \(open\) scheduleVirtualSessionRender\(\)/u);
  assert.match(styles, /\.composer-controls\s*\{[^}]*flex-wrap: wrap/su);
  assert.match(styles, /#environment,\s*#approval-posture\s*\{\s*display: none/su);
  assert.match(client, /\["ArrowDown", "ArrowUp", "Home", "End"\]/u);
  assert.match(client, /aria-current/u);
  assert.match(client, /function isNearBottom\(\)/u);
  assert.match(client, /followLatest = true;\n\s{2}scrollTranscriptToBottom\(\)/u);
  assert.match(client, /turns\.addEventListener\("wheel", suspendTranscriptFollow/u);
  assert.match(client, /function scheduleTranscriptPatch\(/u);
  assert.match(client, /\}, followLatest \? 50 : 100\);/u);
  assert.match(client, /transcriptInteracting = false;\n\s{4}flushPendingTranscriptPatch/u);
  assert.match(client, /!\s*existingIDs\.has\(id\)/u);
  assert.match(styles, /#turns\s*\{[^}]*overflow-anchor: none/su);
  assert.match(transcript, /"active-turn",\s*turn\.status === "running"/su);
  assert.match(
    styles,
    /#turns > article\.active-turn\s*\{[^}]*content-visibility: visible/su,
  );
  assert.doesNotMatch(client, /const shouldFollowLatest = followLatest/u);
  assert.match(
    client,
    /if \(followLatest\) turns\.scrollTop = turns\.scrollHeight/u,
  );
  assert.doesNotMatch(client, /window\.scrollTo/u);
});

void test("Chat composer grows with content and resets after submit", async () => {
  const client = await sourceFile("chat", "webview", "client.ts");
  const styles = await sourceFile("chat", "webview", "styles.css");

  assert.match(client, /prompt\.addEventListener\("input", resizePrompt\)/u);
  assert.match(client, /prompt\.style\.height = "auto"/u);
  assert.match(client, /Math\.min\(window\.innerHeight \* 0\.4, 320\)/u);
  assert.match(client, /prompt\.value = "";\n\s{2}resizePrompt\(\);/u);
  assert.match(styles, /max-height: min\(40vh, 320px\)/u);
  assert.match(styles, /resize: none/u);
});

void test("Chat composer sends on Enter and uses one stateful action", async () => {
  const shell = await sourceFile("chat", "webview", "shell.ts");
  const client = await sourceFile("chat", "webview", "client.ts");
  const keyboard = await sourceFile("chat", "keyboard.ts");

  assert.doesNotMatch(shell, /id="stop"/u);
  assert.match(keyboard, /state\.key === "Enter" && !state\.shiftKey/u);
  assert.match(client, /if \(turnActive\) \{\s*post\(\{ type: "stop" \}\)/su);
  assert.match(client, /send\.classList\.toggle\("stop-state", turnActive\)/u);
  assert.match(client, /turnActive \? "Stop \(Escape\)" : "Send \(Enter\)"/u);
});

void test("Chat presents Provider and Model as one route control", async () => {
  const shell = await sourceFile("chat", "webview", "shell.ts");
  const client = await sourceFile("chat", "webview", "client.ts");
  const composer = await sourceFile("chat", "composer.ts");

  assert.match(shell, /id="provider-control"[^>]*>Provider · Model</u);
  assert.doesNotMatch(shell, /id="model-control"/u);
  assert.doesNotMatch(client, /element\("model-control"\)/u);
  assert.match(composer, /return "DeepSeek"/u);
  assert.match(composer, /`\$\{providerLabel\(profile\.provider\)\} · \$\{profile\.model\}`/u);
  assert.doesNotMatch(shell, /id="thinking-control"/u);
  assert.doesNotMatch(client, /configure-composer", control: "thinking"/u);
});

void test("Chat renders process activity as an ordered collapsible timeline", async () => {
  const transcript = await sourceFile("chat", "webview", "transcript.ts");
  const client = await sourceFile("chat", "webview", "client.ts");
  const projectorModel = await sourceFile("chat", "projector", "model.ts");
  const streamProjector = await sourceFile(
    "chat", "projector", "stream-projector.ts",
  );
  const toolProjector = await sourceFile(
    "chat", "projector", "tool-projector.ts",
  );
  const styles = await sourceFile("chat", "webview", "styles.css");

  assert.match(
    projectorModel,
    /readonly timeline: readonly TurnTimelineItem\[\]/u,
  );
  assert.match(streamProjector, /appendTimelineText\(turn, "output"/u);
  assert.match(streamProjector, /appendTimelineText\(turn, "reasoning"/u);
  assert.match(toolProjector, /ensureToolTimelineItem\(turn/u);
  assert.match(transcript, /appendTimeline\(stream, turn, trusted, actions\)/u);
  assert.match(transcript, /className = "turn-stream"/u);
  assert.match(transcript, /group\.className = "activity-group"/u);
  assert.match(
    transcript,
    /item\.kind === "output" \|\| item\.kind === "approval" \|\|/u,
  );
  assert.match(transcript, /item\.kind === "input"/u);
  assert.match(transcript, /groupItems\.push\(item\)/u);
  assert.match(transcript, /`Activity · \$\{count\} · Issues found`/u);
  assert.match(transcript, /`\$\{active \? "Working" : "Activity"\} · \$\{count\}`/u);
  assert.doesNotMatch(transcript, /group\.open\s*=/u);
  assert.match(transcript, /item\.active \? "Thinking…" : "Reasoning"/u);
  assert.match(transcript, /reasoning\.open = true/u);
  assert.match(transcript, /function reasoningIsLong\(text: string\)/u);
  assert.match(transcript, /text\.length > 1200/u);
  assert.match(transcript, /content\.classList\.add\("long"\)/u);
  assert.match(transcript, /setReasoningExpanded\(content, false\)/u);
  assert.match(transcript, /readonly expanded: ReadonlySet<string>/u);
  assert.match(styles, /\.reasoning-content\.long:not\(\.expanded\) \.reasoning-body/u);
  assert.match(styles, /\.reasoning-toggle\s*\{/u);
  assert.match(transcript, /recovery\.className = "recovery-actions"/u);
  assert.match(transcript, /recovery\.dataset\["recoveryTurnId"\] = turn\.id/u);
  assert.match(transcript, /retainedDraft \? "Discard & Retry" : "Retry"/u);
  assert.match(transcript, /retainedDraft \? "Continue Repair" : "Continue"/u);
  assert.match(transcript, /Workspace changes are retained for repair/u);
  assert.match(transcript, /status\.className = "recovery-status"/u);
  assert.match(client, /recoveryStates\.set\(turnId, \{ action, status: "pending" \}\)/u);
  assert.match(client, /state\.status === "accepted"/u);
  assert.match(client, /"Retry started"/u);
  assert.match(client, /"Continue started"/u);
  assert.match(client, /button\.disabled = state\.status !== "failed"/u);
  assert.match(styles, /\.recovery-action\.primary\s*\{/u);
  assert.match(styles, /var\(--vscode-button-background\)/u);
  assert.match(styles, /\.recovery-action\.secondary\s*\{/u);
  assert.match(styles, /\.recovery-action:disabled\s*\{/u);
  assert.match(styles, /\.recovery-status\.error\s*\{/u);
  assert.match(transcript, /receipt\.className = "receipt"/u);
  assert.match(transcript, /appendText\(receipt, "summary", "", "Run Details"\)/u);
  assert.match(
    transcript,
    /if \(item\.final\) \{\s*appendText\(output, "div", "section-label", "Final Result"\)/su,
  );
  assert.doesNotMatch(transcript, /"meta turn-status", turn\.status/u);
  assert.match(transcript, /group\.className = "reference-group"/u);
  assert.match(transcript, /`References\$\{count > 1/u);
  assert.match(styles, /\.reference-group-body\s*\{/u);
  assert.match(styles, /\.activity-group,\s*\.reference-group\s*\{/u);
  assert.match(styles, /\.activity-group > summary::after/u);
  assert.match(styles, /\.activity-group\.running > summary::before\s*\{/u);
  assert.match(styles, /\.timeline-item::before\s*\{[^}]*background: var\(--vscode-panel-border\)/su);
  assert.match(styles, /\.timeline-item > summary::before\s*\{/u);
  assert.match(styles, /\.timeline-item\.running > summary::before\s*\{/u);
  assert.match(styles, /@keyframes timeline-spin/u);
  assert.match(styles, /\.tool-target\s*\{/u);
  assert.match(transcript, /"command-section-label", "Full Command"/u);
  assert.match(transcript, /"command-section-label", "Output"/u);
  assert.match(transcript, /"pre", "command-full", `\$ \$\{presentation\.command\}`/u);
  assert.doesNotMatch(
    transcript,
    /presentation\.command[\s\S]{0,500}details\.open\s*=/u,
  );
  assert.match(styles, /\.command-full,\s*\.command-output,\s*\.command-metadata/u);
  assert.match(transcript, /presentation\.files !== undefined/u);
  assert.match(transcript, /resourceButton\(\s*file\.resourceId/su);
  assert.match(transcript, /"file-lines added", `\+\$\{String\(file\.added\)\}`/u);
  assert.match(transcript, /"file-lines removed", `-\$\{String\(file\.removed\)\}`/u);
  assert.match(styles, /\.file-change-row\s*\{/u);
  assert.match(styles, /\.file-type-icon\.type-ts/u);
  assert.match(styles, /button\.file-change-name:hover/u);
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

void test("Approval uses one accessible inline decision surface", async () => {
  const view = await sourceFile("chat", "view.ts");
  const client = await sourceFile("chat", "webview", "client.ts");
  const styles = await sourceFile("chat", "webview", "styles.css");
  const transcript = await sourceFile("chat", "webview", "transcript.ts");

  assert.doesNotMatch(view, /#showApproval|#modalApprovals|approvalDialogContent/u);
  assert.match(transcript, /setAttribute\("role", "region"\)/u);
  assert.match(transcript, /"Allow once"/u);
  assert.match(transcript, /"Skip"/u);
  assert.match(transcript, /"More"/u);
  assert.match(transcript, /"Request details"/u);
  assert.match(transcript, /trusted \? reusable : \[\]/u);
  assert.match(transcript, /pending approvals; scroll horizontally/u);
  assert.match(transcript, /box\.tabIndex = -1/u);
  assert.match(client, /function latestPendingApproval\(/u);
  assert.match(
    client,
    /const identity = `\$\{sessionId \?\? ""\}:\$\{approval\.requestId\}`/u,
  );
  assert.match(
    client,
    /const revealIndex = pendingApproval !== undefined \|\|[\s\S]*\? -1/u,
  );
  assert.match(
    client,
    /lastRevealedApproval === identity &&\s*lastRevealedApprovalElement === target/u,
  );
  assert.doesNotMatch(client, /const shouldFollowLatest = followLatest/u);
  assert.match(client, /if \(followLatest\) turns\.scrollTop = turns\.scrollHeight/u);
  assert.match(client, /followLatest = false/u);
  assert.match(
    client,
    /\.approval-actions button:not\(:disabled\)/u,
  );
  assert.match(client, /focus\.focus\(\{ preventScroll: true \}\)/u);
  assert.match(
    client,
    /turns\.scrollTop \+ bounds\.top - viewport\.top - centeredOffset/u,
  );
  assert.match(
    styles,
    /\.approval-card\.approval-revealed\s*\{[^}]*border-color: var\(--vscode-focusBorder\)/su,
  );
  assert.match(styles, /@media \(max-height: 320px\)[\s\S]+approval-card:has\(\.approval-actions\)/u);
  assert.match(
    view,
    /const revealTurnId = state\.revealTurnId \?\?[\s\S]+projector\.pendingApprovals\(\)\.at\(-1\)\?\.turnId/u,
  );
});

void test("Required input uses one complete inline answer surface", async () => {
  const view = await sourceFile("chat", "view.ts");
  const styles = await sourceFile("chat", "webview", "styles.css");
  const transcript = await sourceFile("chat", "webview", "transcript.ts");

  assert.doesNotMatch(view, /#showInput|#modalInputs/u);
  assert.match(transcript, /`Input required: \$\{input\.prompt\}`/u);
  assert.match(transcript, /"input-question-label", "Question"/u);
  assert.match(transcript, /inputOptionButton\(index \+ 1/u);
  assert.match(transcript, /inputOptionButton\(customIndex, "Other answer"/u);
  assert.match(transcript, /document\.createElement\("textarea"\)/u);
  assert.match(transcript, /customInput\.maxLength = 64 << 10/u);
  assert.match(transcript, /actions\.answer\(input\.requestId, answer\)/u);
  assert.match(styles, /\.input-option-copy\s*\{[^}]*overflow-wrap: anywhere;[^}]*white-space: pre-wrap/su);
  assert.match(styles, /\.input-card\s*\{[^}]*border-left: 3px solid var\(--vscode-focusBorder\)/su);
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
