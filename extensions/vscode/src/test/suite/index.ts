import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { performance } from "node:perf_hooks";

import * as vscode from "vscode";

import type { ExtensionAPI } from "../../extension.js";
import { ContextBridge } from "../../context/bridge.js";
import { isUnknownEvent } from "../../protocol/decode.js";
import { DiagnosticCodeActionProvider } from "../../diagnostics/actions.js";
import { canonicalEditorURI } from "../../workspace/uri.js";
import { ResourceNavigator } from "../../chat/resource-navigator.js";
import type { WorkspaceRuntime } from "../../workspace/registry.js";
import type { EditPlanPreview } from "../../edits/preview.js";

const extensionID = "codehelper.codehelper-vscode";
const expectedViews = [
  "codehelper.chat",
  "codehelper.changes",
  "codehelper.agents",
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
  const expectedHostArch = process.env["CODEHELPER_EXPECTED_HOST_ARCH"];
  assert.ok(
    api.activationDurationMS < (expectedHostArch === undefined ? 20 : 1_000),
    `extension activation took ${api.activationDurationMS.toFixed(1)}ms`,
  );
  const chatInteractiveStarted = performance.now();
  await vscode.commands.executeCommand("codehelper.chat.focus");

  assert.equal(
    vscode.env.remoteName,
    undefined,
    "CodeHelper VS Code test host must be local",
  );
  const scenario = process.env["CODEHELPER_ELECTRON_SCENARIO"];
  if (expectedHostArch !== undefined) {
    assert.equal(
      process.arch,
      expectedHostArch,
      "Electron Extension Host architecture does not match the matrix target",
    );
  }
  if (scenario !== "empty") {
    await waitFor(
      () => api.chatWebviewReady?.() === true,
      "Chat Webview client did not complete its ready handshake",
    );
  }
  const chatInteractiveMS = performance.now() - chatInteractiveStarted;
  if (scenario === "empty") {
    assert.equal(vscode.workspace.workspaceFolders, undefined);
    assert.equal(api.workspaceMode, "none");
    assert.equal(api.runtimeAutoStartScheduled, false);
  } else if (scenario === "workspace") {
    assert.equal(vscode.workspace.workspaceFolders?.length, 1);
    assert.equal(api.workspaceMode, "single");
    assert.equal(api.runtimeAutoStartScheduled, false);
    await verifyNativeContextCapture();
    await verifyResourceNavigation();
    await verifyThemeAndZoomAccessibility(api);
    const hiddenEvidence = await verifyHiddenProjectionSuspension(api);
    const captureDurationMS = await verifyContextCapturePerformance();
    const workspace = vscode.workspace.workspaceFolders[0];
    assert.ok(workspace);
    await vscode.workspace.fs.writeFile(
      vscode.Uri.joinPath(workspace.uri, ".codehelper-performance.json"),
      new TextEncoder().encode(JSON.stringify({
        schema_version: 1,
        activation_ms: Number(api.activationDurationMS.toFixed(1)),
        chat_interactive_ms: Number(chatInteractiveMS.toFixed(1)),
        capture_1mib_ms: Number(captureDurationMS.toFixed(1)),
        hidden_projection_posts: hiddenEvidence.hiddenPosts,
        hidden_resume_ms: Number(hiddenEvidence.resumeMS.toFixed(1)),
      })),
    );
  } else if (scenario === "accessibility") {
    assert.equal(vscode.workspace.workspaceFolders?.length, 1);
    assert.equal(api.workspaceMode, "single");
    assert.equal(api.runtimeAutoStartScheduled, false);
    await verifyForcedColorsAccessibility(api);
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
    assert.ok(
      (api.chatProjectionDiagnostics?.().patchPosts ?? 0) > 0,
      "streaming Runtime events did not produce incremental Chat Patches",
    );
    await verifyMultipleChats(api);
    await verifyCheckpoints(api);
    await verifySessionLifecycle(api);
    await verifySessionProfile(api);
    await verifyModelCatalog(api);
    await verifySessionToolCatalog(api);
    await verifyComposerCredential(api);
    await verifyResourceNavigation();
    if (expectedHostArch === "x64") {
      const host = api.runtimeHosts?.()[0];
      assert.ok(host);
      assert.equal(host.architecture, "x64");
      assert.equal(host.binaryTarget, "darwin/amd64");
    }
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
    await verifyResourceNavigation();
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
    ["ui"],
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

async function verifyHiddenProjectionSuspension(
  api: ExtensionAPI,
): Promise<{
  readonly hiddenPosts: number;
  readonly resumeMS: number;
}> {
  assert.ok(api.chatProjectionDiagnostics);
  assert.ok(api.testInvalidateChatProjection);
  await vscode.commands.executeCommand("codehelper.chat.focus");
  await waitFor(
    () => api.chatProjectionDiagnostics?.().visible === true,
    "Chat Webview did not become visible",
  );
  const visible = api.chatProjectionDiagnostics();
  const visiblePosts = visible.snapshotPosts;
  const visibleTotal = visible.snapshotPosts + visible.patchPosts;
  await vscode.commands.executeCommand("workbench.action.closeSidebar");
  await waitFor(
    () => api.chatProjectionDiagnostics?.().visible === false,
    "Chat Webview remained visible after closing the sidebar",
  );
  for (let attempt = 0; attempt < 20; attempt++) {
    api.testInvalidateChatProjection();
  }
  await new Promise((resolve) => setTimeout(resolve, 75));
  assert.equal(
    api.chatProjectionDiagnostics().snapshotPosts,
    visiblePosts,
    "hidden Chat Webview received a DOM Snapshot",
  );
  const hidden = api.chatProjectionDiagnostics();
  const hiddenPosts = hidden.snapshotPosts + hidden.patchPosts - visibleTotal;
  assert.equal(hiddenPosts, 0, "hidden Chat Webview received a Patch");
  const resumeStarted = performance.now();
  await vscode.commands.executeCommand("codehelper.chat.focus");
  await waitFor(
    () => {
      const diagnostics = api.chatProjectionDiagnostics?.();
      return diagnostics?.visible === true &&
        diagnostics.snapshotPosts > visiblePosts;
    },
    "visible Chat Webview did not receive the latest Projection",
  );
  return {
    hiddenPosts,
    resumeMS: performance.now() - resumeStarted,
  };
}

async function verifyThemeAndZoomAccessibility(
  api: ExtensionAPI,
): Promise<void> {
  const workbench = vscode.workspace.getConfiguration("workbench");
  const windowConfiguration = vscode.workspace.getConfiguration("window");
  const originalTheme = workbench.get<string>("colorTheme");
  const originalZoom = windowConfiguration.get<number>("zoomLevel");
  const themes = [
    ["Default Dark Modern", vscode.ColorThemeKind.Dark, "dark"],
    ["Default Light Modern", vscode.ColorThemeKind.Light, "light"],
    ["Default High Contrast", vscode.ColorThemeKind.HighContrast, "high-contrast"],
  ] as const;
  try {
    for (const [name, kind, clientKind] of themes) {
      await workbench.update(
        "colorTheme",
        name,
        vscode.ConfigurationTarget.Global,
      );
      await waitFor(
        () => vscode.window.activeColorTheme.kind === kind,
        `VS Code did not activate ${name}`,
      );
      await waitFor(
        () => api.chatClientEvidence?.()?.themeKind === clientKind,
        `Chat Webview did not project ${clientKind}`,
      );
      assert.equal(api.chatWebviewReady?.(), true);
      assert.equal(api.chatClientEvidence?.()?.imeGuardPassed, true);
    }
    await windowConfiguration.update(
      "zoomLevel",
      4,
      vscode.ConfigurationTarget.Global,
    );
    await waitFor(
      () => vscode.workspace.getConfiguration("window")
        .get<number>("zoomLevel") === 4,
      "VS Code did not apply the approximately 200% zoom level",
    );
    assert.equal(api.chatWebviewReady?.(), true);
    const clientEvidence = api.chatClientEvidence?.();
    assert.ok(clientEvidence);
    assert.equal(clientEvidence.imeGuardPassed, true);
    assert.equal(clientEvidence.forcedColorsActive, false);
    assert.ok(clientEvidence.viewportWidth > 0);
    assert.ok(clientEvidence.viewportHeight > 0);
    assert.ok(clientEvidence.devicePixelRatio > 0);
    await vscode.commands.executeCommand("codehelper.chat.focus");
  } finally {
    await workbench.update(
      "colorTheme",
      originalTheme,
      vscode.ConfigurationTarget.Global,
    );
    await windowConfiguration.update(
      "zoomLevel",
      originalZoom,
      vscode.ConfigurationTarget.Global,
    );
  }
}

async function verifyForcedColorsAccessibility(
  api: ExtensionAPI,
): Promise<void> {
  await waitFor(
    () => api.chatClientEvidence?.()?.forcedColorsActive === true,
    "Chat Webview did not activate the forced-colors media query",
  );
  const evidence = api.chatClientEvidence?.();
  assert.ok(evidence);
  assert.equal(evidence.imeGuardPassed, true);
  assert.notEqual(evidence.themeKind, "unknown");
  assert.ok(evidence.viewportWidth > 0);
  assert.ok(evidence.viewportHeight > 0);
}

async function verifyResourceNavigation(): Promise<void> {
  const folders = vscode.workspace.workspaceFolders ?? [];
  const folder = folders[0];
  assert.ok(folder);
  const roots = folders.map((candidate) => ({
    rootId: createHash("sha256")
      .update(canonicalEditorURI(candidate.uri))
      .digest("hex"),
    folder: candidate,
  } as unknown as WorkspaceRuntime));
  const root = roots[0];
  assert.ok(root);
  const rootId = root.rootId;
  let diffOpened = false;
  const navigator = new ResourceNavigator({
    find: (candidate) => roots.find((value) => value.rootId === candidate),
    forURI: (uri) => {
      if (uri === undefined) return undefined;
      const owner = vscode.workspace.getWorkspaceFolder(uri);
      return owner === undefined
        ? undefined
        : roots.find((value) => value.folder.uri.toString() === owner.uri.toString());
    },
  }, {
    showFile: () => {
      diffOpened = true;
      return Promise.resolve();
    },
  } as unknown as EditPlanPreview);
  const base = {
    id: "a".repeat(64),
    rootId,
    path: "context.ts",
  } as const;

  await navigator.open({ ...base, kind: "file" });
  assert.equal(vscode.window.activeTextEditor?.document.uri.path.endsWith(
    "/context.ts",
  ), true);
  const primaryColumn = vscode.window.activeTextEditor.viewColumn;
  assert.ok(primaryColumn);
  await navigator.open({ ...base, kind: "file" }, { side: true });
  assert.ok(vscode.window.activeTextEditor);
  const sideColumn = vscode.window.activeTextEditor.viewColumn;
  assert.ok(sideColumn);
  assert.notEqual(sideColumn, primaryColumn);
  assert.ok(sideColumn > primaryColumn);
  await navigator.copyRelativePath({ ...base, kind: "file" });
  assert.equal(await vscode.env.clipboard.readText(), "context.ts");
  await navigator.open({
    ...base,
    kind: "directory",
    path: ".vscode",
  });
  for (const candidate of roots.slice(1)) {
    await navigator.open({
      ...base,
      rootId: candidate.rootId,
      kind: "file",
    });
    const currentEditor: vscode.TextEditor | undefined =
      vscode.window.activeTextEditor;
    assert.ok(currentEditor);
    assert.equal(
      vscode.workspace.getWorkspaceFolder(
        currentEditor.document.uri,
      )?.uri.toString(),
      candidate.folder.uri.toString(),
    );
  }
  await navigator.open({
    ...base,
    kind: "range",
    range: {
      start: { line: 1, character: 2 },
      end: { line: 1, character: 10 },
    },
  });
  assert.equal(vscode.window.activeTextEditor.selection.start.line, 1);
  await navigator.open({
    ...base,
    kind: "symbol",
    symbol: "inner",
    range: {
      start: { line: 1, character: 11 },
      end: { line: 1, character: 16 },
    },
  });
  assert.equal(vscode.window.activeTextEditor.document.uri.path.endsWith(
    "/context.ts",
  ), true);

  const collection = vscode.languages.createDiagnosticCollection(
    "codehelper-resource-navigation",
  );
  try {
    const uri = vscode.Uri.joinPath(folder.uri, "context.ts");
    collection.set(uri, [new vscode.Diagnostic(
      new vscode.Range(2, 2, 2, 8),
      "resource diagnostic",
      vscode.DiagnosticSeverity.Warning,
    )]);
    await navigator.open({ ...base, kind: "diagnostic" });
    assert.equal(vscode.window.activeTextEditor.selection.start.line, 2);
  } finally {
    collection.dispose();
  }

  const plan = {
    id: "b".repeat(64),
    diff: "diff",
    files: [{
      path: "context.ts",
      kind: "modified" as const,
      before: "before",
      after: "after",
      beforeExists: true,
      afterExists: true,
      beforeDigest: "c".repeat(64),
      afterDigest: "d".repeat(64),
    }],
  };
  await navigator.open({
    ...base,
    kind: "diff",
    plan,
    fileIndex: 0,
  });
  assert.equal(diffOpened, true);

  await assert.rejects(navigator.open({
    ...base,
    rootId: "f".repeat(64),
    kind: "file",
  }), /root is no longer open/u);
  await assert.rejects(navigator.open({
    ...base,
    kind: "file",
    path: "../outside",
  }), /escapes the workspace/u);
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
        .update(canonicalEditorURI(folder.uri))
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
          .update(canonicalEditorURI(folder.uri))
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
    const recoveredDocument = await vscode.workspace.openTextDocument(
      vscode.Uri.joinPath(removed.uri, "context.ts"),
    );
    await submitMultiRootSelection(
      removed,
      recoveredDocument,
      ["codehelper.explainSelection"],
      turnRoots,
      terminals,
    );
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
    .update(canonicalEditorURI(folder.uri))
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
  const outputTurns = new Set<string>();
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
    } else if (event.kind === "output.delta") {
      outputTurns.add(event.turn_id);
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
        .update(canonicalEditorURI(workspace.uri))
        .digest("hex"),
      document,
      approvals,
      approvalWaiters,
      resolvedApprovals,
      terminals,
      waiters,
    );
    await verifyTurnRecovery(
      api,
      terminals,
      waiters,
      outputTurns,
    );
    await verifyPlanDestinations(api, terminals, waiters);
  } finally {
    subscription.dispose();
  }
}

async function verifyTurnRecovery(
  api: ExtensionAPI,
  terminals: ReadonlyMap<string, string>,
  waiters: Map<string, (kind: string) => void>,
  outputTurns: ReadonlySet<string>,
): Promise<void> {
  assert.ok(api.chatSessions);
  assert.ok(api.testSubmitPrompt);
  assert.ok(api.testCancelTurn);
  assert.ok(api.testRecoverTurn);
  assert.ok(api.testSearchChats);
  const selected = api.chatSessions().find((session) => session.selected);
  assert.ok(selected);
  const source = await api.testSubmitPrompt(
    selected.sessionId,
    "Electron recovery source",
  );
  await waitFor(
    () => outputTurns.has(source.turnId),
    "recovery source did not begin streaming",
  );
  await api.testCancelTurn(selected.sessionId, source.turnId);
  assert.equal(
    await waitForTerminal(source.turnId, terminals, waiters),
    "turn.canceled",
  );
  const retry = await api.testRecoverTurn(
    selected.sessionId,
    source.turnId,
    "retry",
  );
  assert.equal(
    await waitForTerminal(retry.turnId, terminals, waiters),
    "turn.completed",
  );
  const continued = await api.testRecoverTurn(
    selected.sessionId,
    source.turnId,
    "continue",
    "Inspect the current parser state",
  );
  assert.equal(
    await waitForTerminal(continued.turnId, terminals, waiters),
    "turn.completed",
  );
  assert.notEqual(retry.turnId, source.turnId);
  assert.notEqual(continued.turnId, source.turnId);
  const search = await api.testSearchChats("Inspect the current parser state");
  assert.ok(search.matches.some((match) =>
    match.session_id === selected.sessionId &&
    match.turn_id === continued.turnId &&
    match.kind === "user_request"));
}

async function verifyPlanDestinations(
  api: ExtensionAPI,
  terminals: ReadonlyMap<string, string>,
  waiters: Map<string, (kind: string) => void>,
): Promise<void> {
  assert.ok(api.chatSessions);
  assert.ok(api.sessionProfile);
  assert.ok(api.updateSessionProfile);
  assert.ok(api.testSubmitPrompt);
  assert.ok(api.testSessionPlan);
  assert.ok(api.testImplementPlan);
  assert.ok(api.duplicateChat);
  assert.ok(api.checkpoints);
  assert.ok(api.forkCheckpoint);
  const source = api.chatSessions().find((session) => session.selected);
  assert.ok(source);
  await setSessionMode(api, source.sessionId, "plan");
  const planTurn = await api.testSubmitPrompt(
    source.sessionId,
    "Electron plan destination A",
  );
  assert.equal(
    await waitForTerminal(planTurn.turnId, terminals, waiters),
    "turn.completed",
  );
  const firstPlan = (await api.testSessionPlan(source.sessionId)).artifact;
  assert.ok(firstPlan);
  assert.equal(firstPlan.turn_id, planTurn.turnId);

  const newSession = await api.duplicateChat(source.sessionId);
  const newTurn = await api.testImplementPlan(
    newSession.sessionId,
    firstPlan.id,
    "implement",
    source.sessionId,
  );
  assert.equal(
    await waitForTerminal(newTurn.turnId, terminals, waiters),
    "turn.completed",
  );
  const newProfile = await api.sessionProfile(newSession.sessionId);
  assert.equal(newProfile.profile.mode, "act");

  const checkpoints = await api.checkpoints(source.sessionId);
  const checkpoint = checkpoints.checkpoints.find(
    (candidate) => candidate.turn_id === planTurn.turnId &&
      candidate.can_fork,
  );
  assert.ok(checkpoint);
  const parentThread = source.threadId;
  await api.forkCheckpoint(
    source.sessionId,
    checkpoint.id,
    "Electron Plan Destination Fork",
  );
  const forked = api.chatSessions().find(
    (session) => session.sessionId === source.sessionId,
  );
  assert.ok(forked);
  assert.notEqual(forked.threadId, parentThread);
  assert.equal(forked.parentThreadId, parentThread);
  const forkTurn = await api.testImplementPlan(
    source.sessionId,
    firstPlan.id,
    "implement",
    source.sessionId,
  );
  assert.equal(
    await waitForTerminal(forkTurn.turnId, terminals, waiters),
    "turn.completed",
  );

  await setSessionMode(api, source.sessionId, "plan");
  const currentPlanTurn = await api.testSubmitPrompt(
    source.sessionId,
    "Electron plan destination B",
  );
  assert.equal(
    await waitForTerminal(currentPlanTurn.turnId, terminals, waiters),
    "turn.completed",
  );
  const currentPlan = (await api.testSessionPlan(source.sessionId)).artifact;
  assert.ok(currentPlan);
  assert.equal(currentPlan.turn_id, currentPlanTurn.turnId);
  const currentTurn = await api.testImplementPlan(
    source.sessionId,
    currentPlan.id,
    "implement",
  );
  assert.equal(
    await waitForTerminal(currentTurn.turnId, terminals, waiters),
    "turn.completed",
  );
}

async function setSessionMode(
  api: ExtensionAPI,
  sessionId: string,
  mode: "plan" | "act",
): Promise<void> {
  assert.ok(api.sessionProfile);
  assert.ok(api.updateSessionProfile);
  const profile = await api.sessionProfile(sessionId);
  if (profile.profile.mode === mode) return;
  await api.updateSessionProfile(
    sessionId,
    profile.profile.revision,
    { mode },
  );
}

async function verifyMultipleChats(api: ExtensionAPI): Promise<void> {
  assert.ok(api.chatSessions);
  assert.ok(api.createChat);
  assert.ok(api.duplicateChat);
  await waitFor(
    () => {
      const title = api.chatSessions?.()[0]?.title;
      return title !== undefined &&
        title !== "New Chat" &&
        !/^Chat [0-9]+$/u.test(title);
    },
    "Chat title was not generated from the first prompt",
  );
  const generatedTitle = api.chatSessions()[0]?.title;
  assert.ok(generatedTitle);
  assert.notEqual(generatedTitle, "New Chat");
  assert.doesNotMatch(generatedTitle, /^Chat [0-9]+$/u);
  const initialCount = api.chatSessions().length;
  const created = await api.createChat();
  assert.equal(created.title, "New Chat");
  assert.equal(created.isolation, "worktree");
  const duplicate = await api.duplicateChat(created.sessionId);
  assert.equal(duplicate.title, `${created.title} · Copy`);
  assert.equal(duplicate.executionEnvironment, "local");
  assert.equal(api.chatSessions().length, initialCount + 2);
  assert.equal(api.chatSessions().filter((session) => session.selected).length, 1);

  await vscode.commands.executeCommand("codehelper.restartRuntime");
  await waitFor(
    () => api.runtimeSnapshot?.().state === "ready" &&
      api.chatSessions?.().length === initialCount + 2,
    "multiple Chat sessions did not recover after Runtime restart",
  );
  const selected = api.chatSessions().find((session) => session.selected);
  assert.ok(selected);
  assert.equal(selected.sessionId, duplicate.sessionId);
  assert.ok(
    api.chatSessions().some((session) => session.title === generatedTitle),
    `generated title was not restored: ${JSON.stringify(api.chatSessions())}`,
  );
}

async function verifySessionLifecycle(api: ExtensionAPI): Promise<void> {
  assert.ok(api.chatSessions);
  assert.ok(api.renameChat);
  assert.ok(api.pinChat);
  assert.ok(api.archiveChat);
  assert.ok(api.deleteChat);
  const selected = api.chatSessions().find((session) => session.selected);
  assert.ok(selected);
  const sessionID = selected.sessionId;
  await api.renameChat(sessionID, "Pinned lifecycle session");
  await api.pinChat(sessionID, true);
  let lifecycle = api.chatSessions().find(
    (session) => session.sessionId === sessionID,
  );
  assert.ok(lifecycle);
  assert.equal(lifecycle.title, "Pinned lifecycle session");
  assert.equal(lifecycle.pinned, true);
  await api.archiveChat(sessionID, true);
  lifecycle = api.chatSessions().find(
    (session) => session.sessionId === sessionID,
  );
  assert.ok(lifecycle);
  assert.equal(lifecycle.archived, true);
  assert.equal(lifecycle.selected, false);

  await vscode.commands.executeCommand("codehelper.restartRuntime");
  await waitFor(
    () => api.runtimeSnapshot?.().state === "ready" &&
      api.chatSessions?.().some(
        (session) => session.sessionId === sessionID &&
          session.archived && session.pinned,
      ) === true,
    "archived Session lifecycle did not recover after Runtime restart",
  );
  await api.archiveChat(sessionID, false);
  lifecycle = api.chatSessions().find(
    (session) => session.sessionId === sessionID,
  );
  assert.ok(lifecycle);
  assert.equal(lifecycle.archived, false);
  assert.equal(lifecycle.selected, true);
  await api.deleteChat(sessionID);
  assert.equal(
    api.chatSessions().some((session) => session.sessionId === sessionID),
    false,
  );
  assert.equal(api.chatSessions().length >= 1, true);
}

async function verifyCheckpoints(api: ExtensionAPI): Promise<void> {
  assert.ok(api.chatSessions);
  assert.ok(api.checkpoints);
  assert.ok(api.restoreCheckpoint);
  assert.ok(api.forkCheckpoint);
  await waitFor(
    () => api.chatSessions?.().some((session) => session.checkpointCount > 0) ===
      true,
    "completed Turn did not create a Session Checkpoint",
  );
  const selected = api.chatSessions().find(
    (session) => session.checkpointCount > 0,
  );
  assert.ok(selected);
  const list = await api.checkpoints(selected.sessionId);
  const checkpoint = list.checkpoints[0];
  assert.ok(checkpoint);
  const restored = await api.restoreCheckpoint(
    selected.sessionId,
    checkpoint.id,
  );
  assert.equal(restored.side_effects_replayed, false);
  const parentThreadID = selected.threadId;
  await api.forkCheckpoint(
    selected.sessionId,
    checkpoint.id,
    "Electron Checkpoint Fork",
  );
  let forked = api.chatSessions().find(
    (session) => session.sessionId === selected.sessionId,
  );
  assert.ok(forked);
  assert.notEqual(forked.threadId, parentThreadID);
  const forkThreadID = forked.threadId;
  await vscode.commands.executeCommand("codehelper.restartRuntime");
  await waitFor(
    () => api.runtimeSnapshot?.().state === "ready" &&
      api.chatSessions?.().some(
        (session) => session.sessionId === selected.sessionId &&
          session.threadId === forkThreadID,
      ) === true,
    "active Checkpoint Fork did not recover after Runtime restart",
  );
  forked = api.chatSessions().find(
    (session) => session.sessionId === selected.sessionId,
  );
  assert.equal(forked?.threadId, forkThreadID);
}

async function verifySessionProfile(api: ExtensionAPI): Promise<void> {
  assert.ok(api.chatSessions);
  assert.ok(api.sessionProfile);
  assert.ok(api.updateSessionProfile);
  const selected = api.chatSessions().find((session) => session.selected);
  assert.ok(selected);
  const initial = await api.sessionProfile(selected.sessionId);
  assert.equal(initial.profile.version, 1);
  assert.equal(initial.profile.revision >= 1, true);
  assert.equal(
    initial.capabilities.provider,
    initial.profile.provider,
  );
  const maxSteps = initial.profile.max_steps + 1;
  const approvalPosture = initial.profile.approval_posture === "never"
    ? "suggest"
    : "never";
  const updated = await api.updateSessionProfile(
    selected.sessionId,
    initial.profile.revision,
    {
      max_steps: maxSteps,
      approval_posture: approvalPosture,
    },
  );
  assert.equal(updated.profile.max_steps, maxSteps);
  assert.equal(updated.profile.approval_posture, approvalPosture);
  assert.equal(updated.profile.revision, initial.profile.revision + 1);
  assert.equal(updated.prompt_cache_reset, false);
  let expected = updated.profile;
  const efforts =
    initial.capabilities.model_capabilities.reasoning_efforts ?? [];
  if (initial.capabilities.mutable_fields.includes("reasoning_effort") &&
    efforts.length > 1) {
    const effort = efforts.find(
      (candidate) => candidate !== updated.profile.reasoning_effort,
    );
    assert.ok(effort);
    const reasoning = await api.updateSessionProfile(
      selected.sessionId,
      updated.profile.revision,
      { reasoning_effort: effort },
    );
    assert.equal(reasoning.profile.reasoning_effort, effort);
    assert.equal(reasoning.prompt_cache_reset, true);
    assert.equal(
      reasoning.profile.prompt_cache_revision,
      updated.profile.prompt_cache_revision + 1,
    );
    expected = reasoning.profile;
  }

  await vscode.commands.executeCommand("codehelper.restartRuntime");
  await waitFor(
    () => api.runtimeSnapshot?.().state === "ready",
    "Runtime did not recover after Session Profile update",
  );
  const recovered = await api.sessionProfile(selected.sessionId);
  assert.equal(recovered.profile.revision, expected.revision);
  assert.equal(recovered.profile.max_steps, maxSteps);
  assert.equal(recovered.profile.approval_posture, approvalPosture);
  assert.equal(
    recovered.profile.reasoning_effort,
    expected.reasoning_effort,
  );
}

async function verifyModelCatalog(api: ExtensionAPI): Promise<void> {
  assert.ok(api.providerCatalog);
  assert.ok(api.modelCatalog);
  assert.ok(api.testDispatchChatIntent);
  const providers = await api.providerCatalog();
  const selectedProvider = providers.providers.find(
    (provider) => provider.selected,
  );
  assert.ok(selectedProvider);
  const models = await api.modelCatalog(selectedProvider.id);
  const selectedModel = models.models.find((model) => model.selected);
  assert.ok(selectedModel);
  assert.equal(selectedModel.provider, selectedProvider.id);
  assert.equal(selectedModel.capabilities.selection_mode, "restart_required");
  assert.ok(models.models.length >= 1);
  const picker = api.testDispatchChatIntent({
    type: "configure-composer",
    control: "model",
  });
  await new Promise((resolve) => setTimeout(resolve, 150));
  await vscode.commands.executeCommand(
    "workbench.action.acceptSelectedQuickOpenItem",
  );
  await Promise.race([
    picker,
    new Promise<never>((_resolve, reject) => {
      setTimeout(() => {
        reject(new Error("native Model Picker did not accept its current item"));
      }, 5_000);
    }),
  ]);
}

async function verifyComposerCredential(api: ExtensionAPI): Promise<void> {
  assert.ok(api.chatSessions);
  assert.ok(api.sessionProfile);
  assert.ok(api.testCredentialStatus);
  assert.ok(api.testStoreCredential);
  assert.ok(api.testRecordCredentialValidation);
  const selected = api.chatSessions().find((session) => session.selected);
  assert.ok(selected);
  const profile = await api.sessionProfile(selected.sessionId);
  const secret = "electron-secret-canary";
  await api.testStoreCredential(profile.profile.provider, secret);
  const configured = await api.testCredentialStatus(profile.profile.provider);
  assert.equal(configured.status, "configured");
  assert.equal(configured.source, "secret-storage");
  assert.equal(JSON.stringify(configured).includes(secret), false);
  await api.testRecordCredentialValidation(profile.profile.provider, "valid");
  const validated = await api.testCredentialStatus(profile.profile.provider);
  assert.equal(validated.validation, "valid");
  assert.ok(validated.validatedAt);
  assert.equal(JSON.stringify(validated).includes(secret), false);

  await vscode.commands.executeCommand("codehelper.restartRuntime");
  await waitFor(
    () => api.runtimeSnapshot?.().state === "ready",
    "Runtime did not recover with the SecretStorage credential environment",
  );
  const recovered = await api.testCredentialStatus(profile.profile.provider);
  assert.deepEqual(recovered, validated);
}

async function verifySessionToolCatalog(api: ExtensionAPI): Promise<void> {
  assert.ok(api.chatSessions);
  assert.ok(api.sessionProfile);
  assert.ok(api.sessionToolCatalog);
  assert.ok(api.updateSessionProfile);
  const selected = api.chatSessions().find((session) => session.selected);
  assert.ok(selected);
  const profile = await api.sessionProfile(selected.sessionId);
  const catalog = await api.sessionToolCatalog(selected.sessionId);
  assert.equal(catalog.version, 1);
  assert.equal(catalog.tools.length > 1, true);
  assert.equal(catalog.tools.every((tool) => tool.guarded), true);
  const enabled = catalog.tools[0];
  assert.ok(enabled);
  const updated = await api.updateSessionProfile(
    selected.sessionId,
    profile.profile.revision,
    { enabled_tool_ids: [enabled.id] },
  );
  assert.equal(updated.prompt_cache_reset, true);
  assert.equal(updated.reset_reason, "enabled_tool_ids");
  let selectedCatalog = await api.sessionToolCatalog(selected.sessionId);
  assert.deepEqual(
    selectedCatalog.tools.filter((tool) => tool.enabled).map((tool) => tool.id),
    [enabled.id],
  );

  await vscode.commands.executeCommand("codehelper.restartRuntime");
  await waitFor(
    () => api.runtimeSnapshot?.().state === "ready",
    "Runtime did not recover after Session Tool Allowlist update",
  );
  selectedCatalog = await api.sessionToolCatalog(selected.sessionId);
  assert.deepEqual(
    selectedCatalog.tools.filter((tool) => tool.enabled).map((tool) => tool.id),
    [enabled.id],
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
    editor.selection = new vscode.Selection(2, 4, 2, 17);
    const fileAndSelection = await bridge.capture(
      new Set(["file", "selection"]),
    );
    assert.deepEqual(
      fileAndSelection.map((reference) => reference.kind),
      ["file", "selection"],
    );
    const imageURI = vscode.Uri.joinPath(workspace.uri, "context.png");
    await vscode.workspace.fs.writeFile(
      imageURI,
      Uint8Array.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    );
    const image = (await bridge.captureWorkspaceFile(imageURI, "image"))[0];
    assert.ok(image);
    assert.equal(image.kind, "image");
    assert.equal(image.media_type, "image/png");

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
    const terminal = bridge.captureInline(
      "terminal",
      "Electron terminal",
      "go test ./...\nPASS",
    )[0];
    assert.ok(terminal);
    assert.equal(terminal.source, "native_picker");
    assert.equal(terminal.kind, "terminal");
    assert.equal(terminal.path, "");
    const gitDiff = bridge.captureInline(
      "git_diff",
      "Electron Git diff",
      "diff --git a/context.ts b/context.ts\n+// evidence",
    )[0];
    assert.ok(gitDiff);
    assert.equal(gitDiff.kind, "git_diff");
    assert.equal(gitDiff.source, "native_picker");
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
