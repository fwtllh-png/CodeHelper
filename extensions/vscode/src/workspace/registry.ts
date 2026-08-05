import * as vscode from "vscode";

import { ContextBridge } from "../context/bridge.js";
import type { DecodedEvent } from "../protocol/decode.js";
import { RuntimeController } from "../runtime/controller.js";
import type { SupervisorSnapshot } from "../runtime/supervisor.js";

const selectedRootKey = "codehelper.selectedWorkspaceRoot.v1";
export const maxWorkspaceRoots = 8;

export interface WorkspaceRuntime {
  readonly rootId: string;
  readonly label: string;
  readonly folder: vscode.WorkspaceFolder;
  readonly controller: RuntimeController;
  readonly contextBridge: ContextBridge;
}

export interface WorkspaceRuntimeEvent {
  readonly root: WorkspaceRuntime;
  readonly sessionId: string;
  readonly event: DecodedEvent;
  readonly replayed: boolean;
}

export interface WorkspaceRuntimeState {
  readonly root: WorkspaceRuntime;
  readonly snapshot: SupervisorSnapshot;
}

interface ManagedRoot extends WorkspaceRuntime {
  readonly subscriptions: vscode.Disposable;
}

export class WorkspaceRuntimeRegistry implements vscode.Disposable {
  readonly #context: vscode.ExtensionContext;
  readonly #output: vscode.LogOutputChannel;
  readonly #roots = new Map<string, ManagedRoot>();
  readonly #stopping = new Map<string, Promise<void>>();
  readonly #rootChanges = new vscode.EventEmitter<void>();
  readonly #selectionChanges = new vscode.EventEmitter<WorkspaceRuntime>();
  readonly #runtimeEvents = new vscode.EventEmitter<WorkspaceRuntimeEvent>();
  readonly #runtimeStates = new vscode.EventEmitter<WorkspaceRuntimeState>();
  readonly #sessionChanges = new vscode.EventEmitter<WorkspaceRuntime>();
  readonly #subscriptions: vscode.Disposable[];
  #selectedRootId: string | undefined;
  #disposed = false;

  public constructor(
    context: vscode.ExtensionContext,
    folders: readonly vscode.WorkspaceFolder[],
    output: vscode.LogOutputChannel,
  ) {
    if (folders.length > maxWorkspaceRoots) {
      throw new Error(
        `CodeHelper supports at most ${String(maxWorkspaceRoots)} workspace roots`,
      );
    }
    this.#context = context;
    this.#output = output;
    for (const folder of folders) {
      this.#add(folder);
    }
    const stored = context.workspaceState.get<string>(selectedRootKey);
    this.#selectedRootId = this.#roots.has(stored ?? "") ? stored : undefined;
    this.#selectedRootId ??= this.#rootForURI(
      vscode.window.activeTextEditor?.document.uri,
    )?.rootId;
    this.#selectedRootId ??= this.roots[0]?.rootId;
    this.#subscriptions = [
      this.#rootChanges,
      this.#selectionChanges,
      this.#runtimeEvents,
      this.#runtimeStates,
      this.#sessionChanges,
      vscode.workspace.onDidChangeWorkspaceFolders((event) => {
        this.#workspaceFoldersChanged(event);
      }),
    ];
  }

  public readonly onDidChangeRoots = this.#rootChanges.event;
  public readonly onDidChangeSelection = this.#selectionChanges.event;
  public readonly onEvent = this.#runtimeEvents.event;
  public readonly onStateChange = this.#runtimeStates.event;
  public readonly onDidChangeSessions = this.#sessionChanges.event;

  public get roots(): readonly WorkspaceRuntime[] {
    return [...this.#roots.values()];
  }

  public get selected(): WorkspaceRuntime {
    const selected = this.#roots.get(this.#selectedRootId ?? "");
    if (selected === undefined) {
      throw new Error("CodeHelper has no selected workspace root");
    }
    return selected;
  }

  public find(rootId: string): WorkspaceRuntime | undefined {
    return this.#roots.get(rootId);
  }

  public forURI(uri: vscode.Uri | undefined): WorkspaceRuntime | undefined {
    return this.#rootForURI(uri);
  }

  public requireForURI(uri: vscode.Uri | undefined): WorkspaceRuntime {
    const root = this.#rootForURI(uri);
    if (root === undefined) {
      throw new Error("resource does not belong to a CodeHelper workspace root");
    }
    return root;
  }

  public async select(rootId: string): Promise<WorkspaceRuntime> {
    const root = this.#roots.get(rootId);
    if (root === undefined) {
      throw new Error("workspace root is unknown or no longer open");
    }
    if (this.#selectedRootId !== rootId) {
      this.#selectedRootId = rootId;
      await this.#context.workspaceState.update(selectedRootKey, rootId);
      this.#selectionChanges.fire(root);
    }
    return root;
  }

  public async pick(
    title: string,
    placeHolder = "Select the workspace root",
  ): Promise<WorkspaceRuntime | undefined> {
    if (this.roots.length === 1) {
      return this.roots[0];
    }
    const selected = await vscode.window.showQuickPick(
      this.roots.map((root) => ({
        label: root.label,
        description: root.rootId === this.#selectedRootId ? "selected" : "",
        detail: root.folder.uri.toString(),
        root,
      })),
      { title, placeHolder, ignoreFocusOut: true },
    );
    return selected?.root;
  }

  public async startAll(): Promise<void> {
    await Promise.all(this.roots.map(async (root) => root.controller.start()));
  }

  public async restartAll(): Promise<void> {
    await Promise.all(this.roots.map(async (root) => root.controller.restart()));
  }

  public async stopAll(): Promise<void> {
    await Promise.allSettled(
      [
        ...this.roots.map(async (root) => root.controller.stop()),
        ...this.#stopping.values(),
      ],
    );
  }

  public dispose(): void {
    this.#disposed = true;
    for (const subscription of this.#subscriptions.splice(0)) {
      subscription.dispose();
    }
    for (const root of this.#roots.values()) {
      root.subscriptions.dispose();
    }
    this.#roots.clear();
  }

  #add(folder: vscode.WorkspaceFolder): void {
    const controller = new RuntimeController(this.#context, folder, this.#output);
    if (this.#roots.has(controller.rootId)) {
      throw new Error(`duplicate workspace identity for ${folder.uri.toString()}`);
    }
    const root: ManagedRoot = {
      rootId: controller.rootId,
      label: folder.name,
      folder,
      controller,
      contextBridge: new ContextBridge(folder),
      subscriptions: vscode.Disposable.from(
        controller.onEvent((sessionId, event, replayed) => {
          this.#runtimeEvents.fire({ root, sessionId, event, replayed });
        }),
        controller.onStateChange((snapshot) => {
          this.#runtimeStates.fire({ root, snapshot });
        }),
        controller.onSessionsChange(() => {
          this.#sessionChanges.fire(root);
        }),
      ),
    };
    this.#roots.set(root.rootId, root);
  }

  #workspaceFoldersChanged(event: vscode.WorkspaceFoldersChangeEvent): void {
    if (this.#disposed) {
      return;
    }
    const resultingCount =
      this.#roots.size - event.removed.length + event.added.length;
    if (resultingCount > maxWorkspaceRoots) {
      void vscode.window.showErrorMessage(
        `CodeHelper cannot manage more than ${String(maxWorkspaceRoots)} roots`,
      );
      return;
    }
    for (const folder of event.removed) {
      const root = this.#rootByFolder(folder);
      if (root === undefined) {
        continue;
      }
      this.#roots.delete(root.rootId);
      root.subscriptions.dispose();
      const stopping = root.controller.stop()
        .catch((error: unknown) => {
          this.#output.error(
            `[runtime:${root.label}] stop failed: ${errorMessage(error)}`,
          );
        })
        .finally(() => {
          if (this.#stopping.get(root.rootId) === stopping) {
            this.#stopping.delete(root.rootId);
          }
        });
      this.#stopping.set(root.rootId, stopping);
    }
    for (const folder of event.added) {
      this.#add(folder);
      const root = this.#rootByFolder(folder);
      if (root !== undefined && autoStartEnabled(folder)) {
        void this.#startAfterPriorStop(root);
      }
    }
    if (!this.#roots.has(this.#selectedRootId ?? "")) {
      this.#selectedRootId = this.roots[0]?.rootId;
      const selected = this.roots[0];
      if (selected !== undefined) {
        void this.#context.workspaceState.update(selectedRootKey, selected.rootId);
        this.#selectionChanges.fire(selected);
      }
    }
    this.#rootChanges.fire();
  }

  #rootForURI(uri: vscode.Uri | undefined): ManagedRoot | undefined {
    if (uri === undefined) {
      return undefined;
    }
    const folder = vscode.workspace.getWorkspaceFolder(uri);
    return folder === undefined ? undefined : this.#rootByFolder(folder);
  }

  #rootByFolder(folder: vscode.WorkspaceFolder): ManagedRoot | undefined {
    const key = folder.uri.toString();
    return [...this.#roots.values()].find(
      (root) => root.folder.uri.toString() === key,
    );
  }

  async #startAfterPriorStop(root: ManagedRoot): Promise<void> {
    await this.#stopping.get(root.rootId);
    try {
      await root.controller.start();
    } catch (error) {
      this.#output.error(
        `[runtime:${root.label}] start failed: ${errorMessage(error)}`,
      );
    }
  }
}

export function autoStartEnabled(folder?: vscode.WorkspaceFolder): boolean {
  return vscode.workspace.getConfiguration("codehelper", folder?.uri)
    .get<boolean>("runtime.autoStart", true);
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
