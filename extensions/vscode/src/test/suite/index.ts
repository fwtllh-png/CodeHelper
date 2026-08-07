import assert from "node:assert/strict";
import { createHash } from "node:crypto";

import * as vscode from "vscode";

import type { ExtensionAPI } from "../../extension.js";
import { ContextBridge } from "../../context/bridge.js";
import { isUnknownEvent } from "../../protocol/decode.js";
import { DiagnosticCodeActionProvider } from "../../diagnostics/actions.js";
import { canonicalEditorURI } from "../../workspace/uri.js";

const extensionID = "codehelper.codehelper-vscode";
const expectedViews = [
  "codehelper.chat",
  "codehelper.changes",
  "codehelper.threads",
  "codehelper.agents",
  "codehelper.tasks",
  "codehelper.jobs",
  "codehelper.approvals",
  "codehelper.usage",
] as const;
const selectionCommands = [
  "codehelper.explainSelection",
  "codehelper.editSelection",
  "codehelper.refactorSelection",
  "codehelper.generateTestsForSelection",
] as const;

export async function run(): Promise<void> {
  const extension = vscode.extensions.getExtension<ExtensionAPI>(extensionID);
  assert.ok(extension, `${extensionID} was not installed in the test host`);
  const api = await extension.activate();
  assert.ok(
    api.activationDurationMS < 100,
    `extension activation took ${api.activationDurationMS.toFixed(1)}ms`,
  );
  await vscode.commands.executeCommand("codehelper.chat.focus");

  const scenario = process.env["CODEHELPER_ELECTRON_SCENARIO"] ??
    (vscode.env.remoteName === undefined
      ? undefined
      : vscode.workspace.workspaceFolders?.length === 2
        ? "remote-multi"
        : "remote");
  if (scenario === "empty") {
    assert.equal(vscode.workspace.workspaceFolders, undefined);
    assert.equal(api.workspaceMode, "none");
    assert.equal(api.runtimeAutoStartScheduled, false);
  } else if (scenario === "workspace") {
    assert.equal(vscode.workspace.workspaceFolders?.length, 1);
    assert.equal(api.workspaceMode, "single");
    assert.equal(api.runtimeAutoStartScheduled, false);
    await verifyNativeContextCapture();
    const captureDurationMS = await verifyContextCapturePerformance();
    const workspace = vscode.workspace.workspaceFolders[0];
    assert.ok(workspace);
    await vscode.workspace.fs.writeFile(
      vscode.Uri.joinPath(workspace.uri, ".codehelper-performance.json"),
      new TextEncoder().encode(JSON.stringify({
        schema_version: 1,
        activation_ms: Number(api.activationDurationMS.toFixed(1)),
        capture_1mib_ms: Number(captureDurationMS.toFixed(1)),
      })),
    );
  } else if (scenario === "native") {
    assert.equal(vscode.workspace.workspaceFolders?.length, 1);
    assert.equal(api.workspaceMode, "single");
    assert.equal(
      api.runtimeAutoStartScheduled,
      true,
      `native activation failed: ${api.activationError ?? "none"}; ` +
        `autoStart=${String(vscode.workspace.getConfiguration("codehelper")
          .get<boolean>("runtime.autoStart"))}`,
    );
    await verifyNativeFlows(api);
    await verifyMultipleChats(api);
  } else if (scenario === "bundled") {
    assert.equal(vscode.workspace.workspaceFolders?.length, 1);
    assert.equal(api.workspaceMode, "single");
    assert.equal(
      api.runtimeAutoStartScheduled,
      true,
      `bundled activation failed: ${api.activationError ?? "unknown"}`,
    );
    await waitFor(
      () => api.runtimeSnapshot?.().state === "ready",
      `bundled Runtime did not become ready: ${
        JSON.stringify(api.runtimeSnapshot?.())
      }`,
      30_000,
    );
    const host = api.runtimeHosts?.()[0];
    assert.ok(host);
    const os = process.platform === "win32" ? "windows" : process.platform;
    const arch = process.arch === "x64" ? "amd64" : process.arch;
    assert.equal(host.binaryTarget, `${os}/${arch}`);
    assert.equal(host.binarySource, "bundled");
    await vscode.commands.executeCommand("codehelper.restartRuntime");
    await waitFor(
      () => api.runtimeSnapshot?.().state === "ready",
      "bundled Runtime did not recover after restart",
    );
  } else if (scenario === "multi") {
    assert.equal(vscode.workspace.workspaceFolders?.length, 2);
    assert.equal(api.workspaceMode, "multi");
    assert.equal(api.runtimeAutoStartScheduled, true);
    await verifyMultiRootFlows(api);
  } else if (scenario === "remote") {
    const remoteName = vscode.env.remoteName ?? "";
    assert.match(
      remoteName,
      /^(?:ssh-remote|dev-container)$/u,
    );
    assert.equal(vscode.workspace.workspaceFolders?.length, 1);
    assert.equal(api.workspaceMode, "single");
    assert.equal(
      api.runtimeAutoStartScheduled,
      true,
      `remote activation failed: ${api.activationError ?? "unknown"}`,
    );
    assert.ok(api.runtimeHosts);
    await waitFor(
      () => api.runtimeSnapshot?.().state === "ready",
      `remote Runtime did not become ready: ${
        JSON.stringify(api.runtimeSnapshot?.())
      }`,
    );
    const host = api.runtimeHosts()[0];
    assert.ok(host);
    assert.equal(host.platform, "linux");
    assert.equal(host.architecture, process.arch);
    assert.equal(host.remoteName, remoteName);
    assert.equal(
      host.binaryTarget,
      `linux/${process.arch === "x64" ? "amd64" : process.arch}`,
    );
    assert.equal(
      host.binarySource,
      expectedRemoteBinarySource(),
    );
    assert.match(
      host.editorURI,
      new RegExp(
        `^vscode-remote://${remoteName}\\+` +
          "(?:[A-Za-z0-9._~-]+|[0-9a-f]{16})/",
        "u",
      ),
    );
    assert.ok(host.extensionHostPID > 0);
    assert.ok(host.runtimePID !== undefined && host.runtimePID > 0);
    assert.notEqual(host.extensionHostPID, host.runtimePID);
    await verifyNativeFlows(api);
    await vscode.commands.executeCommand("codehelper.restartRuntime");
    await waitFor(
      () => api.runtimeSnapshot?.().state === "ready",
      "remote Runtime did not recover after restart",
    );
  } else if (scenario === "remote-multi") {
    const remoteName = vscode.env.remoteName ?? "";
    assert.match(remoteName, /^(?:ssh-remote|dev-container)$/u);
    assert.equal(vscode.workspace.workspaceFolders?.length, 2);
    assert.equal(api.workspaceMode, "multi");
    assert.equal(api.runtimeAutoStartScheduled, true);
    assert.ok(api.runtimeHosts);
    await waitFor(() => {
      const hosts = api.runtimeHosts?.() ?? [];
      return hosts.length === 2 && hosts.every(
        (host) => host.runtimePID !== undefined,
      );
    }, "remote multi-root Runtime hosts did not become ready");
    const expectedSource = expectedRemoteBinarySource();
    for (const host of api.runtimeHosts()) {
      assert.equal(host.platform, "linux");
      assert.equal(host.architecture, process.arch);
      assert.equal(host.remoteName, remoteName);
      assert.equal(
        host.binaryTarget,
        `linux/${process.arch === "x64" ? "amd64" : process.arch}`,
      );
      assert.equal(host.binarySource, expectedSource);
      assert.notEqual(host.extensionHostPID, host.runtimePID);
    }
    await verifyMultiRootFlows(api);
  } else if (scenario === "remote-reconnect" ||
    scenario === "remote-reconnect-multi") {
    const roots = scenario === "remote-reconnect-multi" ? 2 : 1;
    assert.equal(vscode.workspace.workspaceFolders?.length, roots);
    assert.equal(api.workspaceMode, roots === 1 ? "single" : "multi");
    assert.ok(api.runtimeHosts);
    await waitFor(() => {
      const hosts = api.runtimeHosts?.() ?? [];
      return hosts.length === roots && hosts.every(
        (host) => host.runtimePID !== undefined &&
          host.sessionId !== undefined && host.threadId !== undefined,
      );
    }, "remote reconnect did not attach durable sessions: " +
      JSON.stringify({
        activationError: api.activationError,
        hosts: api.runtimeHosts(),
        snapshots: api.runtimeSnapshots?.(),
      }), 30_000);
    for (const host of api.runtimeHosts()) {
      assert.equal(host.remoteName, vscode.env.remoteName);
      assert.equal(host.binarySource, expectedRemoteBinarySource());
      assert.ok(host.sessionId);
      assert.ok(host.threadId);
      assert.ok(host.replayedEvents !== undefined && host.replayedEvents >= 0);
    }
  } else {
    throw new Error(`unknown Electron scenario ${String(scenario)}`);
  }

  const commands = new Set(await vscode.commands.getCommands(true));
  assert.equal(commands.has("codehelper.showStatus"), true);
  assert.equal(commands.has("codehelper.restartRuntime"), true);
  assert.equal(commands.has("codehelper.repairRuntime"), true);
  assert.equal(commands.has("codehelper.runSetup"), true);
  assert.equal(commands.has("codehelper.runQuickstart"), true);
  for (const command of selectionCommands) {
    assert.equal(commands.has(command), true);
  }
  const manifest = extension.packageJSON as unknown;
  assert.ok(typeof manifest === "object" && manifest !== null);
  assert.deepEqual(
    (manifest as Record<string, unknown>)["extensionKind"],
    ["workspace"],
  );
  const contributes = (manifest as Record<string, unknown>)["contributes"];
  assert.ok(typeof contributes === "object" && contributes !== null);
  const views = (contributes as Record<string, unknown>)["views"];
  assert.ok(typeof views === "object" && views !== null);
  const codehelperViews = (views as Record<string, unknown>)["codehelper"];
  assert.ok(Array.isArray(codehelperViews));
  assert.deepEqual(codehelperViews.map((value: unknown) => {
    assert.ok(typeof value === "object" && value !== null);
    return (value as Record<string, unknown>)["id"];
  }), expectedViews);
  const commandContributions = (contributes as Record<string, unknown>)["commands"];
  assert.ok(Array.isArray(commandContributions));
  const commandIDs = new Set(commandContributions.map((value: unknown) => {
    assert.ok(typeof value === "object" && value !== null);
    return (value as Record<string, unknown>)["command"];
  }));
  for (const command of selectionCommands) {
    assert.equal(commandIDs.has(command), true);
  }
  const menus = (contributes as Record<string, unknown>)["menus"];
  assert.ok(typeof menus === "object" && menus !== null);
  const editorContext = (menus as Record<string, unknown>)["editor/context"];
  assert.ok(Array.isArray(editorContext));
  assert.deepEqual(
    editorContext.map((value: unknown) => {
      assert.ok(typeof value === "object" && value !== null);
      return (value as Record<string, unknown>)["command"];
    }),
    selectionCommands,
  );
}

function expectedRemoteBinarySource(): "external" | "managed" | "bundled" {
  const configured = vscode.workspace.getConfiguration("codehelper")
    .get<string>("binarySource", "auto");
  return configured === "managed" || configured === "bundled" ||
    configured === "external"
    ? configured
    : "external";
}

async function verifyMultiRootFlows(api: ExtensionAPI): Promise<void> {
  assert.ok(api.runtimeSnapshots);
  assert.ok(api.onRootRuntimeEvent);
  await waitFor(() => {
    const snapshots = Object.values(api.runtimeSnapshots?.() ?? {});
    return snapshots.length === 2 &&
      snapshots.every((snapshot) => snapshot.state === "ready");
  }, "both CodeHelper Runtime roots did not become ready");

  const terminals = new Map<string, string>();
  const turnRoots = new Map<string, string>();
  const turnPaths = new Map<string, string>();
  const approvals = new Map<string, {
    readonly rootId: string;
    readonly requestId: string;
  }>();
  const subscription = api.onRootRuntimeEvent((rootId, event, replayed) => {
    if (replayed || isUnknownEvent(event)) return;
    if (event.kind === "turn.started") {
      turnRoots.set(event.turn_id, rootId);
      const context = event.data.editor_context;
      if (context !== undefined && context[0] !== undefined) {
        turnPaths.set(event.turn_id, context[0].path);
      }
    } else if (event.kind === "approval.required" &&
      event.data.edit_plan !== undefined) {
      approvals.set(event.turn_id, {
        rootId,
        requestId: event.data.request_id,
      });
    }
    if (event.kind === "turn.completed" ||
      event.kind === "turn.failed" ||
      event.kind === "turn.canceled") {
      terminals.set(`${rootId}:${event.turn_id}`, event.kind);
    }
  });
  try {
    const folders = vscode.workspace.workspaceFolders ?? [];
    const observedRoots = new Set<string>();
    for (const folder of folders) {
      const document = await vscode.workspace.openTextDocument(
        vscode.Uri.joinPath(folder.uri, "context.ts"),
      );
      const editor = await vscode.window.showTextDocument(document);
      editor.selection = new vscode.Selection(2, 4, 2, 17);
      const raw = await vscode.commands.executeCommand(
        "codehelper.explainSelection",
      );
      assert.ok(typeof raw === "object" && raw !== null);
      const turnId = (raw as Record<string, unknown>)["turnId"];
      assert.ok(typeof turnId === "string" && turnId.length > 0);
      await waitFor(
        () => turnRoots.has(turnId) &&
          terminals.get(`${turnRoots.get(turnId) ?? ""}:${turnId}`) !== undefined,
        `multi-root turn ${turnId} did not terminate`,
      );
      const rootId = turnRoots.get(turnId);
      assert.ok(rootId);
      const expectedRootId = createHash("sha256")
        .update(canonicalEditorURI(folder.uri, vscode.env.remoteName))
        .digest("hex");
      assert.equal(rootId, expectedRootId);
      observedRoots.add(rootId);
      assert.equal(terminals.get(`${rootId}:${turnId}`), "turn.completed");
      assert.equal(turnPaths.get(turnId), "context.ts");
    }
    assert.equal(observedRoots.size, 2);
    assert.deepEqual(
      new Set(Object.keys(api.runtimeSnapshots())),
      observedRoots,
    );
    for (const folder of folders) {
      const document = await vscode.workspace.openTextDocument(
        vscode.Uri.joinPath(folder.uri, "context.ts"),
      );
      for (const invocation of [
        ["codehelper.editSelection", "replace the return expression"],
        ["codehelper.refactorSelection", "extract a helper"],
        ["codehelper.generateTestsForSelection"],
      ] as const) {
        await submitMultiRootSelection(
          folder,
          document,
          invocation,
          turnRoots,
          terminals,
        );
      }
      const collection = vscode.languages.createDiagnosticCollection(
        `codehelper-multi-${String(folder.index)}`,
      );
      const diagnostic = new vscode.Diagnostic(
        new vscode.Range(2, 4, 2, 17),
        "fixture code action diagnostic",
        vscode.DiagnosticSeverity.Error,
      );
      diagnostic.code = "CDT100";
      diagnostic.source = "codehelper-electron";
      collection.set(document.uri, [diagnostic]);
      try {
        const actions = await registeredDiagnosticActions(
          document,
          diagnostic.range,
        );
        for (const title of ["Explain with CodeHelper", "Fix with CodeHelper"]) {
          const action = actions.find((candidate) => candidate.title === title);
          assert.ok(action);
          const raw = await invokeCodeAction(action);
          await waitForMultiRootReceipt(
            folder,
            raw,
            turnRoots,
            terminals,
          );
        }
      } finally {
        collection.dispose();
      }
    }
    for (const folder of folders) {
      const document = await vscode.workspace.openTextDocument(
        vscode.Uri.joinPath(folder.uri, "context.ts"),
      );
      const editor = await vscode.window.showTextDocument(document);
      editor.selection = new vscode.Selection(2, 4, 2, 17);
      const raw = await vscode.commands.executeCommand(
        "codehelper.generateTestsForSelection",
      );
      assert.ok(typeof raw === "object" && raw !== null);
      const turnId = (raw as Record<string, unknown>)["turnId"];
      assert.ok(typeof turnId === "string" && turnId.length > 0);
      await waitFor(
        () => approvals.has(turnId) || turnRoots.has(turnId) &&
          terminals.has(`${turnRoots.get(turnId) ?? ""}:${turnId}`),
        `multi-root turn ${turnId} did not request approval`,
      );
      const approval = approvals.get(turnId);
      assert.ok(
        approval,
        `multi-root approval turn terminated as ${
          terminals.get(`${turnRoots.get(turnId) ?? ""}:${turnId}`) ?? "unknown"
        }`,
      );
      assert.equal(
        approval.rootId,
        createHash("sha256")
          .update(canonicalEditorURI(folder.uri, vscode.env.remoteName))
          .digest("hex"),
      );
      assert.equal(await vscode.commands.executeCommand(
        "codehelper.denyPlan",
        {
          rootId: approval.rootId,
          requestId: approval.requestId,
          decision: "deny",
        },
      ), true);
      await waitFor(
        () => terminals.get(`${approval.rootId}:${turnId}`) ===
          "turn.completed",
        `multi-root approval ${approval.requestId} did not resume`,
      );
    }
    const removed = folders[1];
    assert.ok(removed);
    assert.equal(vscode.workspace.updateWorkspaceFolders(1, 1), true);
    await waitFor(
      () => Object.keys(api.runtimeSnapshots?.() ?? {}).length === 1,
      "removed Runtime root remained registered",
    );
    assert.equal(vscode.workspace.updateWorkspaceFolders(1, 0, {
      uri: removed.uri,
      name: removed.name,
    }), true);
    await waitFor(() => {
      const snapshots = Object.values(api.runtimeSnapshots?.() ?? {});
      return snapshots.length === 2 &&
        snapshots.every((snapshot) => snapshot.state === "ready");
    }, "re-added Runtime root did not recover");
  } finally {
    subscription.dispose();
  }
}

async function submitMultiRootSelection(
  folder: vscode.WorkspaceFolder,
  document: vscode.TextDocument,
  invocation: readonly [string] | readonly [string, string],
  turnRoots: ReadonlyMap<string, string>,
  terminals: ReadonlyMap<string, string>,
): Promise<void> {
  const editor = await vscode.window.showTextDocument(document);
  editor.selection = new vscode.Selection(2, 4, 2, 17);
  const raw = await vscode.commands.executeCommand(
    invocation[0],
    ...invocation.slice(1),
  );
  await waitForMultiRootReceipt(folder, raw, turnRoots, terminals);
}

async function waitForMultiRootReceipt(
  folder: vscode.WorkspaceFolder,
  raw: unknown,
  turnRoots: ReadonlyMap<string, string>,
  terminals: ReadonlyMap<string, string>,
): Promise<void> {
  assert.ok(typeof raw === "object" && raw !== null);
  const turnId = (raw as Record<string, unknown>)["turnId"];
  assert.ok(typeof turnId === "string" && turnId.length > 0);
  const expectedRoot = createHash("sha256")
    .update(canonicalEditorURI(folder.uri, vscode.env.remoteName))
    .digest("hex");
  await waitFor(
    () => turnRoots.get(turnId) === expectedRoot &&
      terminals.get(`${expectedRoot}:${turnId}`) === "turn.completed",
    `multi-root turn ${turnId} did not complete in ${folder.name}`,
  );
}

async function verifyNativeFlows(api: ExtensionAPI): Promise<void> {
  assert.ok(api.runtimeSnapshot);
  assert.ok(api.onRuntimeEvent);
  try {
    await waitFor(
      () => api.runtimeSnapshot?.().state === "ready",
      "CodeHelper Runtime did not become ready",
    );
  } catch {
    throw new Error(
      `CodeHelper Runtime did not become ready: ${JSON.stringify(api.runtimeSnapshot())}`,
    );
  }
  const workspace = vscode.workspace.workspaceFolders?.[0];
  assert.ok(workspace);
  const uri = vscode.Uri.joinPath(workspace.uri, "context.ts");
  const document = await vscode.workspace.openTextDocument(uri);
  const started = new Map<string, unknown>();
  const receipts = new Map<string, unknown>();
  const terminals = new Map<string, string>();
  const waiters = new Map<string, (kind: string) => void>();
  const approvals = new Map<string, TestApproval>();
  const approvalWaiters = new Map<string, (approval: TestApproval) => void>();
  const resolvedApprovals = new Set<string>();
  const subscription = api.onRuntimeEvent((event, replayed) => {
    if (replayed || isUnknownEvent(event)) {
      return;
    }
    if (event.kind === "turn.started") {
      started.set(event.turn_id, event.data.editor_context);
    } else if (event.kind === "turn.receipt") {
      receipts.set(event.turn_id, event.data.editor_context);
    } else if (event.kind === "approval.required" &&
      event.data.edit_plan !== undefined) {
      const approval = {
        requestId: event.data.request_id,
        turnId: event.turn_id,
        planId: event.data.edit_plan.id,
        files: event.data.edit_plan.files.map((file) => ({
          path: file.path,
          kind: file.kind,
        })),
      };
      approvals.set(event.turn_id, approval);
      approvalWaiters.get(event.turn_id)?.(approval);
      approvalWaiters.delete(event.turn_id);
    } else if (event.kind === "approval.resolved") {
      resolvedApprovals.add(event.data.request_id);
    } else if (event.kind === "turn.completed" ||
      event.kind === "turn.failed" ||
      event.kind === "turn.canceled") {
      terminals.set(event.turn_id, event.kind);
      waiters.get(event.turn_id)?.(event.kind);
      waiters.delete(event.turn_id);
    }
  });
  const invocations = [
    ["codehelper.explainSelection"],
    ["codehelper.editSelection", "replace the return expression"],
    ["codehelper.refactorSelection", "extract a helper"],
    ["codehelper.generateTestsForSelection"],
  ] as const;
  try {
    for (const invocation of invocations) {
      const editor = await vscode.window.showTextDocument(document);
      editor.selection = new vscode.Selection(2, 4, 2, 17);
      const raw = await vscode.commands.executeCommand(
        invocation[0],
        ...invocation.slice(1),
      );
      assert.ok(typeof raw === "object" && raw !== null);
      const turnID = (raw as Record<string, unknown>)["turnId"];
      assert.ok(typeof turnID === "string" && turnID.length > 0);
      const terminal = await waitForTerminal(turnID, terminals, waiters);
      assert.equal(terminal, "turn.completed");
      const context = started.get(turnID);
      assert.ok(Array.isArray(context));
      assert.equal(context.length, 1);
      const reference: unknown = context[0];
      assert.ok(typeof reference === "object" && reference !== null);
      assert.equal(
        (reference as Record<string, unknown>)["kind"],
        "selection",
      );
      assert.equal(
        (reference as Record<string, unknown>)["source"],
        "selection_command",
      );
      assert.deepEqual(receipts.get(turnID), context);
    }
    await verifyDiagnosticActions(
      workspace,
      document,
      started,
      receipts,
      terminals,
      waiters,
    );
    await verifyChangesReview(
      createHash("sha256")
        .update(canonicalEditorURI(workspace.uri, vscode.env.remoteName))
        .digest("hex"),
      document,
      approvals,
      approvalWaiters,
      resolvedApprovals,
      terminals,
      waiters,
    );
  } finally {
    subscription.dispose();
  }
}

async function verifyMultipleChats(api: ExtensionAPI): Promise<void> {
  assert.ok(api.chatSessions);
  assert.ok(api.createChat);
  await waitFor(
    () => {
      const title = api.chatSessions?.()[0]?.title;
      return title !== undefined && !/^Chat [0-9]+$/u.test(title);
    },
    "Chat title was not generated from the first prompt",
  );
  const generatedTitle = api.chatSessions()[0]?.title;
  assert.ok(generatedTitle);
  assert.doesNotMatch(generatedTitle, /^Chat [0-9]+$/u);
  const created = await api.createChat();
  assert.equal(created.isolation, "worktree");
  assert.equal(api.chatSessions().length, 2);
  assert.equal(api.chatSessions().filter((session) => session.selected).length, 1);

  await vscode.commands.executeCommand("codehelper.restartRuntime");
  await waitFor(
    () => api.runtimeSnapshot?.().state === "ready" &&
      api.chatSessions?.().length === 2,
    "multiple Chat sessions did not recover after Runtime restart",
  );
  const selected = api.chatSessions().find((session) => session.selected);
  assert.ok(selected);
  assert.equal(selected.sessionId, created.sessionId);
  assert.ok(
    api.chatSessions().some((session) => session.title === generatedTitle),
    `generated title was not restored: ${JSON.stringify(api.chatSessions())}`,
  );
}

interface TestApproval {
  readonly requestId: string;
  readonly turnId: string;
  readonly planId: string;
  readonly files: readonly {
    readonly path: string;
    readonly kind: string;
  }[];
}

async function verifyChangesReview(
  rootId: string,
  document: vscode.TextDocument,
  approvals: ReadonlyMap<string, TestApproval>,
  approvalWaiters: Map<string, (approval: TestApproval) => void>,
  resolvedApprovals: ReadonlySet<string>,
  terminals: ReadonlyMap<string, string>,
  terminalWaiters: Map<string, (kind: string) => void>,
): Promise<void> {
  const first = await startChangesTurn(document, approvals, approvalWaiters);
  assert.deepEqual(first.approval.files, [
    { path: "alpha.txt", kind: "created" },
    { path: "nested/beta.txt", kind: "created" },
  ]);

  const resolvedBeforeForgery = resolvedApprovals.size;
  assert.equal(await vscode.commands.executeCommand("codehelper.approvePlan", {
    rootId,
    requestId: "forged-request",
    decision: "approve",
  }), false);
  assert.equal(resolvedApprovals.size, resolvedBeforeForgery);

  await waitFor(
    () => {
      const input = vscode.window.tabGroups.activeTabGroup.activeTab?.input;
      return input instanceof vscode.TabInputTextDiff &&
        input.modified.query.includes("nested%2Fbeta.txt");
    },
    "V1 edit plan preview did not open",
  );
  await vscode.commands.executeCommand("workbench.action.closeAllEditors");
  assert.equal(await vscode.commands.executeCommand("codehelper.openPlanDiff", {
    rootId,
    planId: first.approval.planId,
    fileIndex: 1,
  }), true);
  const diffTabs = vscode.window.tabGroups.all.flatMap((group) =>
    group.tabs.filter((tab) => tab.input instanceof vscode.TabInputTextDiff));
  assert.equal(diffTabs.length, 1);
  const diffInput = diffTabs[0]?.input;
  assert.ok(diffInput instanceof vscode.TabInputTextDiff);
  assert.equal(diffInput.modified.scheme, "codehelper-edit-plan");
  assert.equal(
    diffInput.modified.authority,
    `${rootId}-${first.approval.planId}`,
  );
  assert.match(diffInput.modified.query, /nested%2Fbeta\.txt/u);

  assert.equal(await vscode.commands.executeCommand("codehelper.approvePlan", {
    rootId,
    requestId: first.approval.requestId,
    decision: "approve",
  }), true);
  assert.equal(
    await waitForTerminal(first.turnId, terminals, terminalWaiters),
    "turn.completed",
  );
  assert.equal(resolvedApprovals.has(first.approval.requestId), true);
  const workspace = vscode.workspace.workspaceFolders?.[0];
  assert.ok(workspace);
  assert.equal(
    new TextDecoder().decode(
      await vscode.workspace.fs.readFile(vscode.Uri.joinPath(workspace.uri, "alpha.txt")),
    ),
    "alpha\n",
  );
  assert.equal(
    new TextDecoder().decode(
      await vscode.workspace.fs.readFile(
        vscode.Uri.joinPath(workspace.uri, "nested", "beta.txt"),
      ),
    ),
    "beta\n",
  );
  const resolvedAfterApproval = resolvedApprovals.size;
  assert.equal(await vscode.commands.executeCommand("codehelper.approvePlan", {
    rootId,
    requestId: first.approval.requestId,
    decision: "approve",
  }), false);
  assert.equal(resolvedApprovals.size, resolvedAfterApproval);

  const second = await startChangesTurn(document, approvals, approvalWaiters);
  assert.equal(await vscode.commands.executeCommand("codehelper.denyPlan", {
    rootId,
    requestId: second.approval.requestId,
    decision: "deny",
  }), true);
  assert.equal(
    await waitForTerminal(second.turnId, terminals, terminalWaiters),
    "turn.completed",
  );
  assert.equal(resolvedApprovals.has(second.approval.requestId), true);
  await assert.rejects(async () => vscode.workspace.fs.stat(
    vscode.Uri.joinPath(workspace.uri, "denied-alpha.txt"),
  ));
  await assert.rejects(async () => vscode.workspace.fs.stat(
    vscode.Uri.joinPath(workspace.uri, "nested", "denied-beta.txt"),
  ));
}

async function startChangesTurn(
  document: vscode.TextDocument,
  approvals: ReadonlyMap<string, TestApproval>,
  waiters: Map<string, (approval: TestApproval) => void>,
): Promise<{ readonly turnId: string; readonly approval: TestApproval }> {
  const editor = await vscode.window.showTextDocument(document);
  editor.selection = new vscode.Selection(2, 4, 2, 17);
  const raw = await vscode.commands.executeCommand(
    "codehelper.generateTestsForSelection",
  );
  assert.ok(typeof raw === "object" && raw !== null);
  const turnId = (raw as Record<string, unknown>)["turnId"];
  assert.ok(typeof turnId === "string" && turnId.length > 0);
  const existing = approvals.get(turnId);
  const approval = existing ?? await Promise.race([
    new Promise<TestApproval>((resolve) => {
      waiters.set(turnId, resolve);
    }),
    new Promise<TestApproval>((_resolve, reject) => {
      setTimeout(() => {
        reject(new Error(`turn ${turnId} did not request edit plan approval`));
      }, 10_000);
    }),
  ]);
  return { turnId, approval };
}

async function verifyDiagnosticActions(
  workspace: vscode.WorkspaceFolder,
  document: vscode.TextDocument,
  started: Map<string, unknown>,
  receipts: ReadonlyMap<string, unknown>,
  terminals: ReadonlyMap<string, string>,
  waiters: Map<string, (kind: string) => void>,
): Promise<void> {
  const collection = vscode.languages.createDiagnosticCollection(
    "codehelper-code-action-fixture",
  );
  const diagnostic = new vscode.Diagnostic(
    new vscode.Range(2, 4, 2, 17),
    "fixture code action diagnostic",
    vscode.DiagnosticSeverity.Error,
  );
  diagnostic.code = "CDT100";
  diagnostic.source = "codehelper-electron";
  collection.set(document.uri, [diagnostic]);
  try {
    const provider = new DiagnosticCodeActionProvider(workspace);
    const cancellation = new vscode.CancellationTokenSource();
    const providerStarted = performance.now();
    const direct = provider.provideCodeActions(
      document,
      diagnostic.range,
      {
        diagnostics: [diagnostic],
        only: vscode.CodeActionKind.QuickFix,
        triggerKind: vscode.CodeActionTriggerKind.Invoke,
      },
      cancellation.token,
    );
    cancellation.dispose();
    const providerDuration = performance.now() - providerStarted;
    assert.ok(
      providerDuration < 20,
      `Diagnostic Code Action provider took ${providerDuration.toFixed(1)}ms`,
    );
    assert.deepEqual(
      direct.map((action) => action.title),
      ["Fix with CodeHelper", "Explain with CodeHelper"],
    );
    const unrelatedCancellation = new vscode.CancellationTokenSource();
    assert.deepEqual(provider.provideCodeActions(
      document,
      diagnostic.range,
      {
        diagnostics: [diagnostic],
        only: vscode.CodeActionKind.SourceOrganizeImports,
        triggerKind: vscode.CodeActionTriggerKind.Invoke,
      },
      unrelatedCancellation.token,
    ), []);
    unrelatedCancellation.dispose();
    for (const action of direct) {
      assert.equal(action.edit, undefined);
      assert.notEqual(action.isPreferred, true);
    }
    const untrustedCancellation = new vscode.CancellationTokenSource();
    const untrusted = new DiagnosticCodeActionProvider(
      workspace,
      () => false,
    ).provideCodeActions(
      document,
      diagnostic.range,
      {
        diagnostics: [diagnostic],
        only: vscode.CodeActionKind.QuickFix,
        triggerKind: vscode.CodeActionTriggerKind.Invoke,
      },
      untrustedCancellation.token,
    );
    untrustedCancellation.dispose();
    assert.deepEqual(
      untrusted.map((action) => action.title),
      ["Explain with CodeHelper"],
    );

    const offered = await registeredDiagnosticActions(document, diagnostic.range);
    const staleExplain = offered.find(
      (action) => action.title === "Explain with CodeHelper",
    );
    assert.ok(staleExplain);
    assert.ok(staleExplain.command);
    const actionArguments: readonly unknown[] =
      staleExplain.command.arguments ?? [];
    const actionSnapshot = actionArguments[0];
    assert.ok(typeof actionSnapshot === "object" && actionSnapshot !== null);
    const turnsBeforeForeignAction = started.size;
    const foreign = {
      ...(actionSnapshot as Readonly<Record<string, unknown>>),
      uri: vscode.Uri.file("/tmp/codehelper-foreign-diagnostic.ts").toString(),
    };
    assert.equal(
      await vscode.commands.executeCommand(
        staleExplain.command.command,
        foreign,
      ),
      undefined,
    );
    assert.equal(started.size, turnsBeforeForeignAction);
    collection.set(document.uri, [new vscode.Diagnostic(
      diagnostic.range,
      "changed diagnostic",
      vscode.DiagnosticSeverity.Error,
    )]);
    const turnsBeforeStaleAction = started.size;
    assert.equal(await invokeCodeAction(staleExplain), undefined);
    assert.equal(started.size, turnsBeforeStaleAction);

    collection.set(document.uri, [diagnostic]);
    const fresh = await registeredDiagnosticActions(document, diagnostic.range);
    for (const title of ["Explain with CodeHelper", "Fix with CodeHelper"]) {
      const action = fresh.find((candidate) => candidate.title === title);
      assert.ok(action);
      const raw = await invokeCodeAction(action);
      assert.ok(typeof raw === "object" && raw !== null);
      const turnID = (raw as Record<string, unknown>)["turnId"];
      assert.ok(typeof turnID === "string" && turnID.length > 0);
      assert.equal(
        await waitForTerminal(turnID, terminals, waiters),
        "turn.completed",
      );
      const context = started.get(turnID);
      assert.ok(Array.isArray(context));
      assert.equal(context.length, 1);
      const reference: unknown = context[0];
      assert.ok(typeof reference === "object" && reference !== null);
      assert.equal(
        (reference as Record<string, unknown>)["kind"],
        "diagnostics",
      );
      assert.equal(
        (reference as Record<string, unknown>)["source"],
        "code_action",
      );
      assert.equal(
        (reference as Record<string, unknown>)["diagnostic_count"],
        1,
      );
      assert.deepEqual(receipts.get(turnID), context);
    }
  } finally {
    collection.dispose();
  }
}

async function registeredDiagnosticActions(
  document: vscode.TextDocument,
  range: vscode.Range,
): Promise<vscode.CodeAction[]> {
  const actions = await vscode.commands.executeCommand<
    (vscode.CodeAction | vscode.Command)[]
  >(
    "vscode.executeCodeActionProvider",
    document.uri,
    range,
    vscode.CodeActionKind.QuickFix.value,
  );
  return actions.filter(
    (value): value is vscode.CodeAction =>
      value instanceof vscode.CodeAction &&
      (value.title === "Fix with CodeHelper" ||
        value.title === "Explain with CodeHelper"),
  );
}

async function invokeCodeAction(
  action: vscode.CodeAction,
): Promise<unknown> {
  assert.ok(action.command);
  const arguments_: readonly unknown[] = action.command.arguments ?? [];
  return vscode.commands.executeCommand(
    action.command.command,
    ...arguments_,
  );
}

async function waitForTerminal(
  turnID: string,
  terminals: ReadonlyMap<string, string>,
  waiters: Map<string, (kind: string) => void>,
): Promise<string> {
  const terminal = terminals.get(turnID);
  if (terminal !== undefined) {
    return terminal;
  }
  return Promise.race([
    new Promise<string>((resolve) => {
      waiters.set(turnID, resolve);
    }),
    new Promise<string>((_resolve, reject) => {
      setTimeout(() => {
        reject(new Error(`turn ${turnID} did not reach a terminal event`));
      }, 10_000);
    }),
  ]);
}

async function waitFor(
  condition: () => boolean,
  message: string,
  timeoutMS = 10_000,
): Promise<void> {
  const deadline = Date.now() + timeoutMS;
  while (!condition()) {
    if (Date.now() >= deadline) {
      throw new Error(message);
    }
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
}

async function verifyNativeContextCapture(): Promise<void> {
  const workspace = vscode.workspace.workspaceFolders?.[0];
  assert.ok(workspace);
  const uri = vscode.Uri.joinPath(workspace.uri, "context.ts");
  const document = await vscode.workspace.openTextDocument(uri);
  const editor = await vscode.window.showTextDocument(document);
  editor.selection = new vscode.Selection(2, 8, 2, 8);
  await waitForSymbols(uri);

  const diagnostics = vscode.languages.createDiagnosticCollection(
    "codehelper-electron-fixture",
  );
  diagnostics.set(uri, Array.from({ length: 40 }, (_, index) => {
    const diagnostic = new vscode.Diagnostic(
      new vscode.Range(index % 5, 0, index % 5, 1),
      `fixture diagnostic ${String(index)}`,
      index % 2 === 0
        ? vscode.DiagnosticSeverity.Warning
        : vscode.DiagnosticSeverity.Error,
    );
    diagnostic.code = `fixture-${String(index)}`;
    diagnostic.source = "codehelper-electron";
    return diagnostic;
  }));
  try {
    const bridge = new ContextBridge(workspace);
    const references = await bridge.capture(new Set(["symbol", "diagnostics"]));
    const symbol = references.find((reference) => reference.kind === "symbol");
    assert.ok(symbol);
    assert.equal(symbol.source, "composer");
    assert.equal(symbol.symbol?.name, "inner");
    assert.equal(symbol.path, "context.ts");
    const diagnostic = references.find(
      (reference) => reference.kind === "diagnostics",
    );
    assert.ok(diagnostic);
    assert.ok(diagnostic.diagnostics);
    assert.equal(diagnostic.diagnostics.length, 32);
    assert.equal(diagnostic.omitted_diagnostics, 8);
    assert.equal(diagnostic.diagnostics[0]?.severity, "error");

    await editor.edit((builder) => {
      builder.insert(new vscode.Position(0, 0), "// dirty\n");
    });
    assert.equal(document.isDirty, true);
    await assert.rejects(
      bridge.capture(new Set(["diagnostics"])),
      /save the active file/,
    );
    assert.equal(await document.save(), true);
    const saved = await bridge.capture(new Set(["diagnostics"]));
    assert.equal(saved[0]?.kind, "diagnostics");
  } finally {
    diagnostics.dispose();
  }
}

async function verifyContextCapturePerformance(): Promise<number> {
  const workspace = vscode.workspace.workspaceFolders?.[0];
  assert.ok(workspace);
  const uri = vscode.Uri.joinPath(workspace.uri, "context-1mib.txt");
  await vscode.workspace.fs.writeFile(uri, new Uint8Array(1 << 20).fill(97));
  const document = await vscode.workspace.openTextDocument(uri);
  await vscode.window.showTextDocument(document);
  const started = performance.now();
  const references = await new ContextBridge(workspace).capture(new Set(["file"]));
  const durationMS = performance.now() - started;
  assert.equal(references[0]?.kind, "file");
  assert.ok(
    durationMS < 100,
    `1 MiB context capture took ${durationMS.toFixed(1)}ms`,
  );
  return durationMS;
}

async function waitForSymbols(uri: vscode.Uri): Promise<void> {
  const deadline = Date.now() + 10_000;
  while (Date.now() < deadline) {
    const symbols = await vscode.commands.executeCommand<
      readonly (vscode.DocumentSymbol | vscode.SymbolInformation)[] | undefined
    >("vscode.executeDocumentSymbolProvider", uri);
    if ((symbols?.length ?? 0) > 0) {
      return;
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error("TypeScript document symbol provider did not become ready");
}
