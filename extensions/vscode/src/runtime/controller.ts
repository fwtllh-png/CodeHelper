import { mkdir } from "node:fs/promises";
import { isAbsolute, resolve } from "node:path";

import * as vscode from "vscode";

import {
  connectSession,
  negotiateRuntime,
  type ConnectedSession,
  type NegotiatedRuntime,
} from "./recovery.js";
import {
  launchRuntime,
  verifyBinary,
  type BinaryVersion,
  type RuntimeProcess,
} from "./process.js";
import {
  RuntimeSupervisor,
  type ManagedRuntime,
  type SupervisorSnapshot,
} from "./supervisor.js";
import { configuredBinaryPath, runtimePosture } from "../security/trust.js";
import { BindingStore } from "../state/store.js";
import type { RuntimeBinding } from "../state/store.js";
import type { DecodedEvent } from "../protocol/decode.js";
import {
  SessionCommands,
  type ApprovalDecision,
  type ApprovalScope,
  type EditorContextReference,
  type SubmitReceipt,
} from "./session.js";
import { assertCompatibleBinary } from "../compatibility/policy.js";
import {
  createWorkspaceIdentity,
  type WorkspaceIdentity,
} from "../workspace/identity.js";
import { assertWorkspaceExtensionHost } from "../workspace/host.js";
import { canonicalEditorURI } from "../workspace/uri.js";
import { testBuildEnabled } from "../test-mode.js";
import {
  resolveBinarySource,
  type ResolvedBinary,
} from "../binary/source.js";
import type { BinarySource } from "../binary/store.js";
import type { RpcNotification } from "./client.js";
import {
  chatTitleFromPrompt,
  isPlaceholderChatTitle,
} from "../chat/title.js";

export type {
  ApprovalDecision,
  ApprovalScope,
  SubmitReceipt,
} from "./session.js";

interface ActiveRuntime extends ManagedRuntime {
  readonly binaryPath: string;
  readonly binarySource: Exclude<BinarySource, "auto">;
  readonly binaryVersion: BinaryVersion;
  readonly negotiated: NegotiatedRuntime;
  readonly sessions: Map<string, ConnectedSession>;
  selectedSessionId: string;
  request(method: string, params?: unknown): Promise<unknown>;
  onNotification(listener: (notification: RpcNotification) => void): () => void;
  readonly pid: number | undefined;
}

export interface RuntimeIdentity {
  readonly sessionId: string;
  readonly threadId: string;
}

export interface ChatSessionSummary extends RuntimeIdentity {
  readonly title: string;
  readonly isolation: "worktree" | "shared";
  readonly selected: boolean;
  readonly replayedEvents: number;
}

export interface RuntimeHostSnapshot {
  readonly rootId: string;
  readonly editorURI: string;
  readonly runtimePath: string;
  readonly platform: NodeJS.Platform;
  readonly architecture: string;
  readonly extensionHostPID: number;
  readonly runtimePID?: number;
  readonly binaryTarget?: string;
  readonly binarySource?: Exclude<BinarySource, "auto">;
  readonly sessionId?: string;
  readonly threadId?: string;
  readonly replayedEvents?: number;
}

export class RuntimeController {
  readonly #context: vscode.ExtensionContext;
  readonly #workspace: vscode.WorkspaceFolder;
  readonly #output: vscode.OutputChannel;
  readonly #bindingStore: BindingStore;
  readonly #workspaceIdentity: WorkspaceIdentity;
  readonly #supervisor: RuntimeSupervisor<ActiveRuntime>;
  readonly #eventListeners = new Set<
  (
    sessionId: string,
    event: DecodedEvent,
    replayed: boolean,
  ) => void | Promise<void>
  >();
  readonly #sessionListeners = new Set<() => void>();
  readonly #stateListeners = new Set<(snapshot: SupervisorSnapshot) => void>();
  #lastSessionError: string | undefined;

  public constructor(
    context: vscode.ExtensionContext,
    workspace: vscode.WorkspaceFolder,
    output: vscode.OutputChannel,
  ) {
    if (vscode.env.remoteName !== undefined) {
      throw new Error(
        "CodeHelper VS Code does not support Remote SSH or Dev Containers",
      );
    }
    if (context.extension.extensionKind !== vscode.ExtensionKind.UI) {
      throw new Error("CodeHelper must run in the local UI Extension Host");
    }
    this.#context = context;
    this.#workspace = workspace;
    this.#output = output;
    this.#workspaceIdentity = createWorkspaceIdentity(
      canonicalEditorURI(workspace.uri),
      workspace.uri.fsPath,
    );
    this.#bindingStore = new BindingStore(context.workspaceState);
    this.#supervisor = new RuntimeSupervisor(
      async () => this.#launch(),
      {
        onStateChange: (snapshot) => {
          this.#writeState(snapshot);
        },
      },
    );
  }

  public get snapshot(): SupervisorSnapshot {
    return this.#supervisor.snapshot;
  }

  public get workspace(): vscode.WorkspaceFolder {
    return this.#workspace;
  }

  public get rootId(): string {
    return this.#workspaceIdentity.root_id;
  }

  public async start(): Promise<void> {
    await this.#supervisor.start();
  }

  public async restart(): Promise<void> {
    this.#lastSessionError = undefined;
    await this.#supervisor.restart();
  }

  public async stop(): Promise<void> {
    await this.#supervisor.stop();
  }

  public async resolveBinary(): Promise<ResolvedBinary> {
    const configuration = vscode.workspace.getConfiguration(
      "codehelper",
      this.#workspace.uri,
    );
    const configured = configuredBinaryPath(
      configuration.inspect<string>("binaryPath"),
      vscode.workspace.isTrusted,
    );
    const binarySource = configuration.get<BinarySource>("binarySource", "auto");
    if (!["auto", "external", "managed", "bundled"].includes(binarySource)) {
      throw new Error("codehelper.binarySource is invalid");
    }
    const developmentRoot = this.#context.extensionMode ===
      vscode.ExtensionMode.Development
      ? resolve(this.#context.extensionPath, "..", "..")
      : undefined;
    return resolveBinarySource({
      source: binarySource,
      ...(configured === undefined ? {} : { configuredPath: configured }),
      ...(developmentRoot === undefined ? {} : { developmentRoot }),
      extensionPath: this.#context.extensionPath,
      ...(this.#context.globalStorageUri.scheme === "file"
        ? { storageRoot: this.#context.globalStorageUri.fsPath }
        : {}),
    });
  }

  public hostSnapshot(): RuntimeHostSnapshot {
    const runtime = this.#supervisor.runtime;
    const selectedSession = runtime?.sessions.get(
      runtime.selectedSessionId,
    );
    return {
      rootId: this.#workspaceIdentity.root_id,
      editorURI: this.#workspaceIdentity.editor_uri,
      runtimePath: this.#workspaceIdentity.runtime_path,
      platform: process.platform,
      architecture: process.arch,
      extensionHostPID: process.pid,
      ...(runtime?.pid === undefined ? {} : { runtimePID: runtime.pid }),
      ...(runtime === undefined
        ? {}
        : {
            binaryTarget:
              `${runtime.binaryVersion.os}/${runtime.binaryVersion.arch}`,
            binarySource: runtime.binarySource,
            sessionId: runtime.selectedSessionId,
            ...(selectedSession === undefined
              ? {}
              : { threadId: selectedSession.binding.threadId }),
            replayedEvents: [...runtime.sessions.values()].reduce(
              (total, session) => total + session.replayedEvents,
              0,
            ),
          }),
    };
  }

  public onEvent(
    listener: (
      sessionId: string,
      event: DecodedEvent,
      replayed: boolean,
    ) => void | Promise<void>,
  ): vscode.Disposable {
    this.#eventListeners.add(listener);
    return {
      dispose: () => {
        this.#eventListeners.delete(listener);
      },
    };
  }

  public onSessionsChange(listener: () => void): vscode.Disposable {
    this.#sessionListeners.add(listener);
    return { dispose: () => {
      this.#sessionListeners.delete(listener);
    } };
  }

  public sessions(): readonly ChatSessionSummary[] {
    const runtime = this.#readyRuntime();
    return [...runtime.sessions.values()].map((session) => ({
      sessionId: session.binding.sessionId,
      threadId: session.binding.threadId,
      title: session.binding.title,
      isolation: session.binding.isolation,
      selected: session.binding.sessionId === runtime.selectedSessionId,
      replayedEvents: session.replayedEvents,
    }));
  }

  public async createChat(title?: string): Promise<ChatSessionSummary> {
    const runtime = this.#readyRuntime();
    const ordinal = runtime.sessions.size + 1;
    const resolvedTitle = title?.trim() || `Chat ${String(ordinal)}`;
    let sessionId = "";
    const session = await connectSession(
      runtime,
      this.#bindingStore,
      this.#workspaceIdentity,
      async (event, replayed) => {
        if (sessionId === "") return;
        await this.#emitEvent(sessionId, event, replayed);
      },
      (error) => {
        this.#reportSessionError(error);
      },
      {
        create: true,
        title: resolvedTitle,
        isolation: "worktree",
      },
    );
    sessionId = session.binding.sessionId;
    runtime.sessions.set(sessionId, session);
    runtime.selectedSessionId = sessionId;
    await this.#bindingStore.select(this.rootId, sessionId);
    this.#emitSessionsChange();
    return {
      sessionId,
      threadId: session.binding.threadId,
      title: session.binding.title,
      isolation: session.binding.isolation,
      selected: true,
      replayedEvents: session.replayedEvents,
    };
  }

  public async selectChat(sessionId: string): Promise<void> {
    const runtime = this.#readyRuntime();
    if (!runtime.sessions.has(sessionId)) {
      throw new Error("Chat session is unavailable");
    }
    runtime.selectedSessionId = sessionId;
    await this.#bindingStore.select(this.rootId, sessionId);
    this.#emitSessionsChange();
  }

  public onStateChange(
    listener: (snapshot: SupervisorSnapshot) => void,
  ): vscode.Disposable {
    this.#stateListeners.add(listener);
    return {
      dispose: () => {
        this.#stateListeners.delete(listener);
      },
    };
  }

  public async submitPrompt(
    sessionId: string,
    prompt: string,
    context: readonly EditorContextReference[],
  ): Promise<SubmitReceipt> {
    const receipt = await this.#commands(sessionId).submitPrompt(prompt, context);
    const runtime = this.#readyRuntime();
    const session = this.#session(runtime, sessionId);
    if (isPlaceholderChatTitle(session.binding.title)) {
      const title = chatTitleFromPrompt(prompt);
      if (title !== undefined) {
        void this.#renameChat(runtime, session, title);
      }
    }
    return receipt;
  }

  public async cancelTurn(sessionId: string, turnId: string): Promise<SubmitReceipt> {
    return this.#commands(sessionId).cancelTurn(turnId);
  }

  public async decideApproval(
    sessionId: string,
    turnId: string,
    requestId: string,
    decision: ApprovalDecision,
    scope: ApprovalScope,
    expiresAt: string,
    planId?: string,
  ): Promise<SubmitReceipt> {
    return this.#commands(sessionId).decideApproval(
      turnId, requestId, decision, scope, expiresAt, planId,
    );
  }

  public async replyInput(
    sessionId: string,
    turnId: string,
    requestId: string,
    answer: string,
  ): Promise<SubmitReceipt> {
    return this.#commands(sessionId).replyInput(turnId, requestId, answer);
  }

  public async mergeChat(
    sessionId: string,
    action: "preview" | "apply",
    planId?: string,
  ): Promise<unknown> {
    const runtime = this.#readyRuntime();
    if (!runtime.sessions.has(sessionId)) {
      throw new Error("Chat session is unavailable");
    }
    return runtime.request("session/merge", {
      sessionId,
      action,
      ...(planId === undefined ? {} : { planId }),
    });
  }

  public statusMessage(): string {
    const snapshot = this.#supervisor.snapshot;
    const runtime = this.#supervisor.runtime;
    if (runtime === undefined) {
      const detail = this.#lastSessionError ?? snapshot.error;
      return detail === undefined
        ? `CodeHelper Runtime: ${snapshot.state}`
        : `CodeHelper Runtime: ${snapshot.state} (${detail})`;
    }
    const trust = vscode.workspace.isTrusted ? "trusted" : "read-only";
    const recovery = this.#lastSessionError === undefined
      ? ""
      : `, recovery error: ${this.#lastSessionError}`;
    return `CodeHelper Runtime: ${snapshot.state}, ${trust}, ` +
      `${runtime.binaryVersion.version}, ${String(runtime.sessions.size)} Chat session(s)` +
      recovery;
  }

  public identity(): RuntimeIdentity {
    const runtime = this.#readyRuntime();
    const session = this.#session(runtime, runtime.selectedSessionId);
    return {
      sessionId: session.binding.sessionId,
      threadId: session.binding.threadId,
    };
  }

  public query(method: string, params?: unknown): Promise<unknown> {
    return this.#readyRuntime().request(method, params);
  }

  async #launch(): Promise<ActiveRuntime> {
    const storage = this.#context.storageUri;
    if (storage === undefined) {
      throw new Error("CodeHelper Runtime requires Extension Host workspace storage");
    }
    assertWorkspaceExtensionHost({
      workspaceScheme: this.#workspace.uri.scheme,
      workspaceAuthority: this.#workspace.uri.authority,
      storageScheme: storage.scheme,
    });
    const workspaceRoot = this.#workspace.uri.fsPath;
    if (!isAbsolute(workspaceRoot) ||
      !isAbsolute(storage.fsPath)) {
      throw new Error("Local Extension Host paths must be absolute");
    }
    const dataDirectory = resolve(
      storage.fsPath,
      "runtime",
      this.#workspaceIdentity.root_id,
    );
    await mkdir(dataDirectory, { recursive: true });

    const configuration = vscode.workspace.getConfiguration(
      "codehelper",
      this.#workspace.uri,
    );
    const resolvedBinary = await this.resolveBinary();
    const binaryPath = resolvedBinary.path;
    const binaryVersion = await verifyBinary(binaryPath);
    assertCompatibleBinary(
      binaryVersion,
      this.#context.extensionMode !== vscode.ExtensionMode.Production ||
        testBuildEnabled,
    );
    const maxSteps = configuration.get<number>("runtime.maxSteps", 8);
    if (!Number.isInteger(maxSteps) || maxSteps < 1 || maxSteps > 128) {
      throw new Error("codehelper.runtime.maxSteps must be an integer between 1 and 128");
    }
    const configuredConfigPath = configuration
      .get<string>("runtime.configPath", "")
      .trim();
    if (configuredConfigPath.length > 0 && !isAbsolute(configuredConfigPath)) {
      throw new Error("codehelper.runtime.configPath must be a Host-local absolute path");
    }
    this.#output.appendLine(
      `[runtime:${this.#workspace.name}] launching ${binaryPath} ` +
      `(${binaryVersion.version}, ` +
      `source=${resolvedBinary.source}, ` +
      "host=local, " +
      `target=${process.platform}/${process.arch}, ` +
      `posture=${runtimePosture(vscode.workspace.isTrusted)})`,
    );
    const runtimeProcess = await launchRuntime({
      binaryPath,
      workspaceRoot,
      dataDirectory,
      ...(configuredConfigPath.length === 0
        ? {}
        : { configPath: configuredConfigPath }),
      posture: runtimePosture(vscode.workspace.isTrusted),
      maxSteps,
      workspaceIdentity: this.#workspaceIdentity,
      diagnostics: (text) => {
        this.#output.append(text);
      },
    });
    try {
      const negotiated = await negotiateRuntime(
        runtimeProcess.client,
        this.#workspaceIdentity,
      );
      const stored = this.#bindingStore.loadAll(this.#workspaceIdentity);
      const sessions = new Map<string, ConnectedSession>();
      const attach = async (binding?: RuntimeBinding): Promise<void> => {
        let sessionId = binding?.sessionId ?? "";
        const session = await connectSession(
          runtimeProcess.client,
          this.#bindingStore,
          this.#workspaceIdentity,
          async (event, replayed) => {
            if (sessionId === "") return;
            await this.#emitEvent(sessionId, event, replayed);
          },
          (error) => {
            this.#reportSessionError(error);
          },
          binding === undefined
            ? { create: true, title: "Chat 1", isolation: "shared" }
            : { binding },
        );
        sessionId = session.binding.sessionId;
        sessions.set(sessionId, session);
      };
      if (stored.length === 0) {
        await attach();
      } else {
        for (const binding of stored) await attach(binding);
      }
      const selectedSessionId = this.#bindingStore.load(this.#workspaceIdentity)
        ?.sessionId ?? sessions.keys().next().value;
      if (selectedSessionId === undefined) {
        throw new Error("Runtime attached no Chat sessions");
      }
      this.#output.appendLine(
        `[runtime:${this.#workspace.name}] attached ` +
        `${String(sessions.size)} Chat session(s), selected=${selectedSessionId}`,
      );
      if (resolvedBinary.managedDigest !== undefined) {
        await resolvedBinary.managedStore?.markHealthy(
          resolvedBinary.managedDigest,
        );
      }
      return new RuntimeConnection(
        runtimeProcess,
        binaryPath,
        resolvedBinary.source,
        binaryVersion,
        negotiated,
        sessions,
        selectedSessionId,
      );
    } catch (error) {
      const startupError = await runtimeProcess.startupFailure(error);
      await runtimeProcess.stop();
      await resolvedBinary.managedStore?.rollbackPending();
      throw startupError;
    }
  }

  #writeState(snapshot: SupervisorSnapshot): void {
    const suffix = snapshot.error === undefined ? "" : `: ${snapshot.error}`;
    this.#output.appendLine(
      `[runtime:${this.#workspace.name}] state=${snapshot.state} ` +
      `restart=${String(snapshot.restartAttempt)}${suffix}`,
    );
    for (const listener of this.#stateListeners) {
      listener(snapshot);
    }
  }

  #commands(sessionId: string): SessionCommands {
    const runtime = this.#readyRuntime();
    this.#session(runtime, sessionId);
    return new SessionCommands(
      runtime,
      sessionId,
      () => vscode.workspace.isTrusted,
      this.#workspaceIdentity,
    );
  }

  #session(runtime: ActiveRuntime, sessionId: string): ConnectedSession {
    const session = runtime.sessions.get(sessionId);
    if (session === undefined) throw new Error("Chat session is unavailable");
    return session;
  }

  async #renameChat(
    runtime: ActiveRuntime,
    session: ConnectedSession,
    title: string,
  ): Promise<void> {
    try {
      await runtime.request("session/rename", {
        sessionId: session.binding.sessionId,
        title,
      });
      await this.#bindingStore.rename(
        this.rootId,
        session.binding.sessionId,
        title,
      );
      session.binding = { ...session.binding, title };
      this.#emitSessionsChange();
    } catch (error) {
      this.#output.appendLine(
        `[runtime:${this.#workspace.name}] Chat title update failed: ` +
          (error instanceof Error ? error.message : String(error)),
      );
    }
  }

  async #emitEvent(
    sessionId: string,
    event: DecodedEvent,
    replayed: boolean,
  ): Promise<void> {
    for (const listener of this.#eventListeners) {
      await listener(sessionId, event, replayed);
    }
  }

  #emitSessionsChange(): void {
    for (const listener of this.#sessionListeners) listener();
  }

  #readyRuntime(): ActiveRuntime {
    const runtime = this.#supervisor.runtime;
    if (runtime === undefined || this.#supervisor.snapshot.state !== "ready") {
      throw new Error("CodeHelper Runtime is not ready");
    }
    return runtime;
  }

  #reportSessionError(error: Error): void {
    const firstReport = this.#lastSessionError === undefined;
    this.#lastSessionError = error.message;
    this.#output.appendLine(
      `[runtime:${this.#workspace.name}] session recovery failed: ${error.message}`,
    );
    if (firstReport) {
      void vscode.window.showErrorMessage(
        `CodeHelper session is desynchronized: ${error.message}`,
        "Show Output",
      ).then((selection) => {
        if (selection === "Show Output") {
          this.#output.show(true);
        }
      });
    }
  }
}

class RuntimeConnection implements ActiveRuntime {
  public readonly exited;
  readonly #process: RuntimeProcess;

  public constructor(
    process: RuntimeProcess,
    public readonly binaryPath: string,
    public readonly binarySource: Exclude<BinarySource, "auto">,
    public readonly binaryVersion: BinaryVersion,
    public readonly negotiated: NegotiatedRuntime,
    public readonly sessions: Map<string, ConnectedSession>,
    public selectedSessionId: string,
  ) {
    this.#process = process;
    this.exited = process.exited.then(async () => {
      for (const session of sessions.values()) session.dispose();
      await Promise.all([...sessions.values()].map(
        async (session) => session.settled(),
      ));
    });
  }

  public async stop(): Promise<void> {
    for (const session of this.sessions.values()) session.dispose();
    await Promise.all([...this.sessions.values()].map(
      async (session) => session.settled(),
    ));
    await this.#process.stop();
  }

  public get pid(): number | undefined {
    return this.#process.pid;
  }

  public request(method: string, params?: unknown): Promise<unknown> {
    return this.#process.client.request(method, params);
  }

  public onNotification(
    listener: (notification: RpcNotification) => void,
  ): () => void {
    return this.#process.client.onNotification(listener);
  }
}
