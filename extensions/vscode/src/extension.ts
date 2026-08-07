import * as vscode from "vscode";
import { Buffer } from "node:buffer";
import { join } from "node:path";

import {
  BackgroundViews,
  unavailableBackgroundViews,
} from "./background/views.js";
import { ChatViewProvider } from "./chat/view.js";
import { registerSelectionCommands } from "./selection/commands.js";
import type { DecodedEvent } from "./protocol/decode.js";
import type { SupervisorSnapshot } from "./runtime/supervisor.js";
import type {
  ChatSessionSummary,
  RuntimeHostSnapshot,
} from "./runtime/controller.js";
import type {
  SessionProfilePatch,
  SessionProfileSnapshot,
  SessionToolCatalog,
  SessionProfileUpdate,
} from "./runtime/session.js";
import { registerDiagnosticActions } from "./diagnostics/actions.js";
import { ChangesView, unavailableChangesView } from "./edits/changes.js";
import { EditPlanPreview } from "./edits/preview.js";
import {
  autoStartEnabled,
  WorkspaceRuntimeRegistry,
} from "./workspace/registry.js";
import { testBuildEnabled } from "./test-mode.js";
import { ManagedBinaryStore } from "./binary/store.js";
import {
  BinaryUpdateClient,
  readTrustRoots,
  type ManifestCache,
} from "./binary/update.js";
import { registerSetupCommands } from "./setup/commands.js";
import type { CredentialView } from "./security/credentials.js";
import type {
  CheckpointList,
  CheckpointRestore,
  ModelCatalog,
  ProviderCatalog,
} from "./protocol/generated.js";

let registry: WorkspaceRuntimeRegistry | undefined;
let updateCheckInFlight: Promise<void> | undefined;

export interface ExtensionAPI {
  readonly activationDurationMS: number;
  readonly workspaceMode: "none" | "single" | "multi";
  readonly runtimeAutoStartScheduled: boolean;
  readonly activationError?: string;
  readonly runtimeSnapshot?: () => SupervisorSnapshot;
  readonly onRuntimeEvent?: (
    listener: (event: DecodedEvent, replayed: boolean) => void,
  ) => vscode.Disposable;
  readonly runtimeSnapshots?: () => Readonly<Record<string, SupervisorSnapshot>>;
  readonly runtimeHosts?: () => readonly RuntimeHostSnapshot[];
  readonly chatSessions?: () => readonly ChatSessionSummary[];
  readonly createChat?: () => Promise<ChatSessionSummary>;
  readonly duplicateChat?: (sessionId: string) => Promise<ChatSessionSummary>;
  readonly renameChat?: (sessionId: string, title: string) => Promise<void>;
  readonly pinChat?: (sessionId: string, pinned: boolean) => Promise<void>;
  readonly archiveChat?: (sessionId: string, archived: boolean) => Promise<void>;
  readonly deleteChat?: (sessionId: string) => Promise<void>;
  readonly checkpoints?: (sessionId: string) => Promise<CheckpointList>;
  readonly restoreCheckpoint?: (
    sessionId: string,
    checkpointId: string,
  ) => Promise<CheckpointRestore>;
  readonly forkCheckpoint?: (
    sessionId: string,
    checkpointId: string,
    title: string,
  ) => Promise<void>;
  readonly sessionProfile?: (sessionId: string) => Promise<SessionProfileSnapshot>;
  readonly sessionToolCatalog?: (
    sessionId: string,
  ) => Promise<SessionToolCatalog>;
  readonly providerCatalog?: () => Promise<ProviderCatalog>;
  readonly modelCatalog?: (provider?: string) => Promise<ModelCatalog>;
  readonly updateSessionProfile?: (
    sessionId: string,
    expectedRevision: number,
    patch: SessionProfilePatch,
  ) => Promise<SessionProfileUpdate>;
  readonly testCredentialStatus?: (provider: string) => Promise<CredentialView>;
  readonly testStoreCredential?: (
    provider: string,
    secret: string,
  ) => Promise<void>;
  readonly testRecordCredentialValidation?: (
    provider: string,
    validation: "valid" | "invalid",
  ) => Promise<void>;
  readonly chatWebviewReady?: () => boolean;
  readonly chatProjectionDiagnostics?: () => {
    readonly visible: boolean;
    readonly snapshotPosts: number;
  };
  readonly testInvalidateChatProjection?: () => void;
  readonly onRootRuntimeEvent?: (
    listener: (
      rootId: string,
      event: DecodedEvent,
      replayed: boolean,
    ) => void,
  ) => vscode.Disposable;
}

export function activate(context: vscode.ExtensionContext): ExtensionAPI {
  const activationStarted = performance.now();
  const output = vscode.window.createOutputChannel("CodeHelper", { log: true });
  const editPreview = new EditPlanPreview();
  context.subscriptions.push(output, editPreview);
  const folders = vscode.workspace.workspaceFolders ?? [];
  let registryError: string | undefined;
  let chatView: ChatViewProvider | undefined;
  if (folders.length > 0) {
    try {
      registry = new WorkspaceRuntimeRegistry(context, folders, output);
    } catch (error) {
      registryError = error instanceof Error ? error.message : String(error);
      output.error(`[registry] ${registryError}`);
    }
  }
  if (registry !== undefined) {
    const chat = new ChatViewProvider(
      registry,
      editPreview,
      context.extensionUri,
    );
    chatView = chat;
    context.subscriptions.push(
      registry,
      chat,
      new ChangesView(
        registry,
        editPreview,
        (rootId, requestId, decision) =>
          chat.decidePlan(rootId, requestId, decision),
        output,
      ),
      new BackgroundViews(registry, output),
      vscode.window.registerWebviewViewProvider("codehelper.chat", chat, {
        webviewOptions: { retainContextWhenHidden: true },
      }),
    );
  } else if (folders.length > 0) {
    const message = registryError ?? "CodeHelper workspace roots are unavailable.";
    context.subscriptions.push(
      unavailableChangesView(message),
      unavailableBackgroundViews(message),
      vscode.window.registerWebviewViewProvider(
        "codehelper.chat",
        new UnavailableChatViewProvider(message),
      ),
    );
  } else {
    context.subscriptions.push(
      unavailableChangesView("Open a folder to review CodeHelper changes."),
      unavailableBackgroundViews("Open a folder to start CodeHelper."),
      vscode.window.registerWebviewViewProvider(
        "codehelper.chat",
        new UnavailableChatViewProvider("Open a folder to start CodeHelper."),
      ),
    );
  }

  context.subscriptions.push(
    ...registerSelectionCommands(registry, output),
    ...registerDiagnosticActions(registry, output),
    ...registerSetupCommands(registry, output),
    vscode.commands.registerCommand("codehelper.selectWorkspaceRoot", async () => {
      const root = await registry?.pick("CodeHelper: Select Workspace Root");
      if (root !== undefined) await registry?.select(root.rootId);
    }),
    vscode.commands.registerCommand("codehelper.newChat", async () => {
      const root = await registry?.pick("CodeHelper: New Chat");
      if (root === undefined) return undefined;
      await registry?.select(root.rootId);
      const session = await root.controller.createChat();
      await vscode.commands.executeCommand("codehelper.chat.focus");
      return session;
    }),
    vscode.commands.registerCommand("codehelper.selectChat", async () => {
      const root = await registry?.pick("CodeHelper: Select Chat");
      if (root === undefined) return undefined;
      const selected = await vscode.window.showQuickPick(
        root.controller.sessions().map((session) => ({
          label: session.title,
          description: session.selected ? "selected" : session.isolation,
          session,
        })),
        {
          title: "CodeHelper: Select Chat",
          placeHolder: "Select the Chat session",
          ignoreFocusOut: true,
        },
      );
      if (selected === undefined) return undefined;
      await registry?.select(root.rootId);
      await root.controller.selectChat(selected.session.sessionId);
      await vscode.commands.executeCommand("codehelper.chat.focus");
      return selected.session;
    }),
    vscode.commands.registerCommand("codehelper.showStatus", async () => {
      const root = await registry?.pick("CodeHelper Runtime Status");
      void vscode.window.showInformationMessage(
        root === undefined
          ? "CodeHelper Runtime: no workspace"
          : `${root.label}: ${root.controller.statusMessage()}`,
      );
    }),
    vscode.commands.registerCommand("codehelper.restartRuntime", async () => {
      const root = await registry?.pick("CodeHelper: Restart Runtime");
      if (root === undefined) {
        void vscode.window.showErrorMessage(
          "CodeHelper Runtime requires an open workspace folder.",
        );
        return;
      }
      try {
        await root.controller.restart();
        void vscode.window.showInformationMessage(
          `${root.label}: ${root.controller.statusMessage()}`,
        );
      } catch (error) {
        reportStartError(output, error);
      }
    }),
    vscode.commands.registerCommand("codehelper.checkBinaryUpdates", async () => {
      await checkBinaryUpdate(context, output, true);
    }),
    vscode.workspace.onDidGrantWorkspaceTrust(() => {
      if (registry !== undefined) {
        void registry.restartAll().catch((error: unknown) => {
          reportStartError(output, error);
        });
      }
    }),
    vscode.workspace.onDidChangeConfiguration((event) => {
      if (registry === undefined ||
        (!event.affectsConfiguration("codehelper.binaryPath") &&
          !event.affectsConfiguration("codehelper.binarySource") &&
          !event.affectsConfiguration("codehelper.runtime.autoStart") &&
          !event.affectsConfiguration("codehelper.runtime.configPath") &&
          !event.affectsConfiguration("codehelper.runtime.maxSteps"))) {
        return;
      }
      for (const root of registry.roots) {
        if (!event.affectsConfiguration("codehelper", root.folder.uri)) continue;
        const action = autoStartEnabled(root.folder)
          ? root.controller.restart()
          : root.controller.stop();
        void action.catch((error: unknown) => {
          reportStartError(output, error);
        });
      }
    }),
  );

  const activeRegistry = registry;
  const autoStartRoots = activeRegistry?.roots.filter((root) =>
    autoStartEnabled(root.folder)) ?? [];
  const runtimeAutoStartScheduled = autoStartRoots.length > 0;
  for (const root of autoStartRoots) {
    void root.controller.start().catch((error: unknown) => {
      reportStartError(output, error);
    });
  }
  if (context.extensionMode === vscode.ExtensionMode.Production &&
    !testBuildEnabled) {
    const timer = setTimeout(() => {
      void checkBinaryUpdate(context, output, false);
    }, 30_000);
    context.subscriptions.push({ dispose: () => {
      clearTimeout(timer);
    } });
  }
  return {
    activationDurationMS: performance.now() - activationStarted,
    workspaceMode: folders.length === 0
      ? "none"
      : folders.length === 1 ? "single" : "multi",
    runtimeAutoStartScheduled,
    ...((context.extensionMode !== vscode.ExtensionMode.Production ||
      testBuildEnabled) && registryError !== undefined
      ? { activationError: registryError }
      : {}),
    ...(activeRegistry === undefined ||
      (context.extensionMode === vscode.ExtensionMode.Production &&
        !testBuildEnabled)
      ? {}
      : {
          runtimeSnapshot: () => activeRegistry.selected.controller.snapshot,
          onRuntimeEvent: (
            listener: (event: DecodedEvent, replayed: boolean) => void,
          ) => activeRegistry.onEvent(({ root, event, replayed }) => {
            if (root.rootId === activeRegistry.selected.rootId) {
              listener(event, replayed);
            }
          }),
          runtimeSnapshots: () => Object.fromEntries(
            activeRegistry.roots.map((root) => [
              root.rootId,
              root.controller.snapshot,
            ]),
          ),
          runtimeHosts: () => activeRegistry.roots.map(
            (root) => root.controller.hostSnapshot(),
          ),
          chatSessions: () => activeRegistry.selected.controller.sessions(),
          createChat: async () => activeRegistry.selected.controller.createChat(),
          duplicateChat: async (sessionId) =>
            activeRegistry.selected.controller.duplicateChat(sessionId),
          renameChat: async (sessionId, title) =>
            activeRegistry.selected.controller.renameChat(sessionId, title),
          pinChat: async (sessionId, pinned) =>
            activeRegistry.selected.controller.pinChat(sessionId, pinned),
          archiveChat: async (sessionId, archived) =>
            activeRegistry.selected.controller.archiveChat(sessionId, archived),
          deleteChat: async (sessionId) =>
            activeRegistry.selected.controller.deleteChat(sessionId),
          checkpoints: async (sessionId) =>
            activeRegistry.selected.controller.checkpoints(sessionId),
          restoreCheckpoint: async (sessionId, checkpointId) =>
            activeRegistry.selected.controller.restoreCheckpoint(
              sessionId,
              checkpointId,
            ),
          forkCheckpoint: async (sessionId, checkpointId, title) =>
            activeRegistry.selected.controller.forkCheckpoint(
              sessionId,
              checkpointId,
              title,
            ),
          sessionProfile: async (sessionId) =>
            activeRegistry.selected.controller.sessionProfile(sessionId),
          sessionToolCatalog: async (sessionId) =>
            activeRegistry.selected.controller.sessionToolCatalog(sessionId),
          providerCatalog: async () =>
            activeRegistry.selected.controller.providerCatalog(),
          modelCatalog: async (provider) =>
            activeRegistry.selected.controller.modelCatalog(provider),
          updateSessionProfile: async (sessionId, expectedRevision, patch) =>
            activeRegistry.selected.controller.updateSessionProfile(
              sessionId,
              expectedRevision,
              patch,
            ),
          testCredentialStatus: async (provider) =>
            activeRegistry.selected.controller.credentialStatus(provider),
          testStoreCredential: async (provider, secret) => {
            await activeRegistry.selected.controller.storeCredential(
              provider,
              secret,
            );
            await activeRegistry.selected.controller
              .activateCredentialProvider(provider);
          },
          testRecordCredentialValidation: async (provider, validation) =>
            activeRegistry.selected.controller.recordCredentialValidation(
              provider,
              validation,
              validation === "invalid" ? "authentication" : undefined,
            ),
          chatWebviewReady: () => chatView?.webviewReady ?? false,
          chatProjectionDiagnostics: () =>
            chatView?.projectionDiagnostics ?? {
              visible: false,
              snapshotPosts: 0,
            },
          testInvalidateChatProjection: () => {
            chatView?.invalidateProjection();
          },
          onRootRuntimeEvent: (
            listener: (
              rootId: string,
              event: DecodedEvent,
              replayed: boolean,
            ) => void,
          ) => activeRegistry.onEvent(({ root, event, replayed }) => {
            listener(root.rootId, event, replayed);
          }),
        }),
  };
}

async function checkBinaryUpdate(
  context: vscode.ExtensionContext,
  output: vscode.OutputChannel,
  interactive: boolean,
): Promise<void> {
  if (updateCheckInFlight !== undefined) {
    await updateCheckInFlight;
    return;
  }
  const operation = performBinaryUpdate(context, output, interactive);
  updateCheckInFlight = operation;
  try {
    await operation;
  } finally {
    if (updateCheckInFlight === operation) updateCheckInFlight = undefined;
  }
}

async function performBinaryUpdate(
  context: vscode.ExtensionContext,
  output: vscode.OutputChannel,
  interactive: boolean,
): Promise<void> {
  try {
    if (context.globalStorageUri.scheme !== "file") {
      throw new Error("binary updates require Host-local file storage");
    }
    const configuration = vscode.workspace.getConfiguration("codehelper");
    const policy = configuration.get<"off" | "notify" | "auto">(
      "update.policy",
      "notify",
    );
    if (policy === "off" && !interactive) return;
    const lastCheckKey = "codehelper.binaryUpdate.lastCheck.v1";
    const now = Date.now();
    const lastCheck = context.globalState.get<number>(lastCheckKey, 0);
    if (!interactive && now - lastCheck < 24 * 60 * 60 * 1_000) return;
    await context.globalState.update(lastCheckKey, now);
    const roots = await readTrustRoots(join(
      context.extensionPath,
      "resources",
      "release-trust-roots.json",
    ));
    const store = new ManagedBinaryStore(join(
      context.globalStorageUri.fsPath,
      "managed-binary",
    ));
    const client = new BinaryUpdateClient({
      store,
      trustRoots: roots,
      manifestCache: manifestCache(context.globalState),
    });
    const channel = configuration.get<"stable" | "preview">(
      "update.channel",
      "stable",
    );
    const release = await client.check(channel);
    const active = await store.resolve().catch((error: unknown) => {
      if (error instanceof Error && error.message.includes("revoked")) {
        return undefined;
      }
      throw error;
    });
    if (active?.digest === release.artifact.sha256) {
      if (interactive) {
        void vscode.window.showInformationMessage(
          `CodeHelper binary ${active.version} is up to date.`,
        );
      }
      return;
    }
    let install = policy === "auto";
    if (!install) {
      install = await vscode.window.showInformationMessage(
        `CodeHelper binary ${release.artifact.version} is available.`,
        "Install",
      ) === "Install";
    }
    if (!install) return;
    const installed = await client.install(
      release.manifest,
      release.artifact,
      context.extensionMode !== vscode.ExtensionMode.Production,
    );
    output.appendLine(
      `[update] installed ${installed.version} digest=${installed.digest}`,
    );
    await registry?.restartAll();
    void vscode.window.showInformationMessage(
      `CodeHelper binary ${installed.version} installed.`,
    );
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    output.appendLine(`[update] ${message}`);
    if (interactive) {
      void vscode.window.showErrorMessage(
        `CodeHelper binary update failed: ${message}`,
      );
    }
  }
}

function manifestCache(state: vscode.Memento): ManifestCache {
  const key = "codehelper.binaryUpdate.manifestCache.v1";
  return {
    load: () => {
      const value = state.get<unknown>(key);
      if (!isObject(value) || typeof value["etag"] !== "string" ||
        typeof value["data"] !== "string" ||
        value["data"].length > 2 << 20 ||
        !/^[A-Za-z0-9+/]+={0,2}$/u.test(value["data"])) {
        return Promise.resolve(undefined);
      }
      return Promise.resolve({
        etag: value["etag"],
        bytes: Buffer.from(value["data"], "base64"),
      });
    },
    save: async (entry) => {
      await state.update(key, {
        etag: entry.etag,
        data: Buffer.from(entry.bytes).toString("base64"),
      });
    },
  };
}

function isObject(value: unknown): value is Readonly<Record<string, unknown>> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export async function deactivate(): Promise<void> {
  await registry?.stopAll();
  registry?.dispose();
  registry = undefined;
}

function reportStartError(output: vscode.OutputChannel, value: unknown): void {
  const error = value instanceof Error ? value : new Error(String(value));
  output.appendLine(`[runtime] ${error.message}`);
  void vscode.window.showErrorMessage(
    `CodeHelper Runtime failed: ${error.message}`,
    "Repair Runtime",
    "Run Setup",
    "Show Output",
  ).then((selection) => {
    if (selection === "Repair Runtime") {
      void vscode.commands.executeCommand("codehelper.repairRuntime");
    } else if (selection === "Run Setup") {
      void vscode.commands.executeCommand("codehelper.runSetup");
    } else if (selection === "Show Output") {
      output.show(true);
    }
  });
}

class UnavailableChatViewProvider implements vscode.WebviewViewProvider {
  readonly #message: string;

  public constructor(message: string) {
    this.#message = message;
  }

  public resolveWebviewView(view: vscode.WebviewView): void {
    view.webview.options = { enableScripts: false, localResourceRoots: [] };
    view.webview.html =
      '<!doctype html><html><head><meta charset="UTF-8">' +
      '<meta http-equiv="Content-Security-Policy" content="default-src \'none\'">' +
      `</head><body><p>${escapeHTML(this.#message)}</p></body></html>`;
  }
}

function escapeHTML(value: string): string {
  return value.replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}
