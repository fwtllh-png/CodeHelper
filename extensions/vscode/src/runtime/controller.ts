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
  type SessionProfilePatch,
  type SessionProfileSnapshot,
  type SessionToolCatalog,
  type SessionProfileUpdate,
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
  defaultChatTitle,
  isPlaceholderChatTitle,
} from "../chat/title.js";
import {
  WorkspaceCredentialStore,
  type CredentialView,
} from "../security/credentials.js";
import {
  SessionLifecycleCommands,
  type SessionLifecyclePatch,
  type SessionLifecycleSummary,
  type SessionListOptions,
} from "./lifecycle.js";
import {
  SessionArtifactCommands,
  type AcceptedPlanTurn,
  type SessionCheckpoint,
} from "./artifacts.js";
import type {
  CheckpointList,
  CheckpointRestore,
  ModelCatalog,
  ProviderCatalog,
  SessionList,
  SessionPlan,
} from "../protocol/generated.js";
import {
  decodeModelCatalog,
  decodeProviderCatalog,
} from "./models.js";

export type {
  ApprovalDecision,
  ApprovalScope,
  SubmitReceipt,
} from "./session.js";

const lifecycleStatusEvents = new Set([
  "turn.started",
  "turn.completed",
  "turn.failed",
  "turn.canceled",
  "approval.required",
  "approval.resolved",
  "input.required",
  "input.resolved",
  "checkpoint.created",
  "checkpoint.restored",
  "checkpoint.forked",
]);

interface ActiveRuntime extends ManagedRuntime {
  readonly binaryPath: string;
  readonly binarySource: Exclude<BinarySource, "auto">;
  readonly binaryVersion: BinaryVersion;
  readonly negotiated: NegotiatedRuntime;
  readonly sessions: Map<string, ConnectedSession>;
  readonly summaries: Map<string, SessionLifecycleSummary>;
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
  readonly status: SessionLifecycleSummary["status"];
  readonly pinned: boolean;
  readonly archived: boolean;
  readonly workspaceLabel: string;
  readonly provider?: string;
  readonly model?: string;
  readonly mode?: string;
  readonly executionEnvironment: "local";
  readonly parentThreadId?: string;
  readonly latestTurnId?: string;
  readonly pendingApprovals: number;
  readonly pendingInputs: number;
  readonly checkpointCount: number;
  readonly changedFiles: number;
  readonly totalTokens: number;
  readonly costMicrounits: number;
  readonly costKnown: boolean;
  readonly createdAt: string;
  readonly updatedAt: string;
  readonly selected: boolean;
  readonly replayedEvents: number;
}

export interface ChatSearchResult {
  readonly sessions: readonly ChatSessionSummary[];
  readonly matches: ReadonlyArray<
  NonNullable<SessionList["matches"]>[number]
  >;
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
  readonly #credentialStore: WorkspaceCredentialStore;
  readonly #credentialProviderKey: string;
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
    this.#credentialStore = new WorkspaceCredentialStore(
      context.secrets,
      this.#workspaceIdentity.root_id,
    );
    this.#credentialProviderKey =
      `codehelper.credentialProvider.v1:${this.#workspaceIdentity.root_id}`;
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
    return [...runtime.summaries.values()].map(
      (summary) => this.#chatSummary(runtime, summary),
    );
  }

  public async createChat(title?: string): Promise<ChatSessionSummary> {
    const runtime = this.#readyRuntime();
    const resolvedTitle = title?.trim() || defaultChatTitle;
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
    const summary = await new SessionLifecycleCommands(runtime).status(sessionId);
    runtime.summaries.set(sessionId, summary);
    runtime.selectedSessionId = sessionId;
    await this.#bindingStore.select(this.rootId, sessionId);
    this.#emitSessionsChange();
    return this.#chatSummary(runtime, summary);
  }

  public async duplicateChat(sessionId: string): Promise<ChatSessionSummary> {
    const source = this.sessions().find((session) => session.sessionId === sessionId);
    if (source === undefined) throw new Error("Chat session is unavailable");
    const sourceProfile = await this.sessionProfile(sessionId);
    const created = await this.createChat(`${source.title} · Copy`);
    try {
      const target = await this.sessionProfile(created.sessionId);
      const mutable = new Set(target.capabilities.mutable_fields);
      const profile = sourceProfile.profile;
      const patch: SessionProfilePatch = {
        ...(mutable.has("mode") ? { mode: profile.mode } : {}),
        ...(mutable.has("reasoning_effort") &&
          profile.reasoning_effort !== undefined
          ? { reasoning_effort: profile.reasoning_effort }
          : {}),
        ...(mutable.has("enabled_tool_ids")
          ? { enabled_tool_ids: profile.enabled_tool_ids ?? [] }
          : {}),
        ...(mutable.has("approval_posture")
          ? { approval_posture: profile.approval_posture }
          : {}),
        ...(mutable.has("max_steps") ? { max_steps: profile.max_steps } : {}),
      };
      if (Object.keys(patch).length > 0) {
        await this.updateSessionProfile(
          created.sessionId,
          target.profile.revision,
          patch,
        );
      }
      return created;
    } catch (error) {
      await this.deleteChat(created.sessionId).catch(() => undefined);
      throw error;
    }
  }

  public async selectChat(sessionId: string): Promise<void> {
    const runtime = this.#readyRuntime();
    const summary = runtime.summaries.get(sessionId);
    if (summary === undefined || summary.archived ||
      !runtime.sessions.has(sessionId)) {
      throw new Error("Chat session is unavailable");
    }
    runtime.selectedSessionId = sessionId;
    await this.#bindingStore.select(this.rootId, sessionId);
    this.#emitSessionsChange();
  }

  public async searchChats(
    options: SessionListOptions,
  ): Promise<ChatSearchResult> {
    const runtime = this.#readyRuntime();
    const list = await new SessionLifecycleCommands(runtime).list({
      ...options,
      includeArchived: options.includeArchived ?? true,
      limit: options.limit ?? 100,
    });
    for (const summary of list.sessions) {
      runtime.summaries.set(summary.session_id, summary);
    }
    return {
      sessions: list.sessions.map((summary) => this.#chatSummary(runtime, summary)),
      matches: list.matches ?? [],
    };
  }

  public async renameChat(sessionId: string, title: string): Promise<void> {
    await this.#renameChat(this.#readyRuntime(), sessionId, title);
  }

  public async pinChat(sessionId: string, pinned: boolean): Promise<void> {
    await this.#updateChat(sessionId, { pinned });
  }

  public async archiveChat(
    sessionId: string,
    archived: boolean,
  ): Promise<void> {
    const runtime = this.#readyRuntime();
    const updated = await this.#updateChat(sessionId, { archived });
    if (archived) {
      const connected = runtime.sessions.get(sessionId);
      if (connected !== undefined) {
        connected.dispose();
        await connected.settled();
        runtime.sessions.delete(sessionId);
      }
      await this.#bindingStore.clear(this.rootId, sessionId);
      if (runtime.selectedSessionId === sessionId) {
        const replacement = runtime.sessions.keys().next().value;
        if (replacement === undefined) {
          await this.createChat();
          return;
        }
        runtime.selectedSessionId = replacement;
        await this.#bindingStore.select(this.rootId, replacement);
      }
    } else {
      await this.#attachSummary(runtime, updated);
      runtime.selectedSessionId = sessionId;
      await this.#bindingStore.select(this.rootId, sessionId);
    }
    this.#emitSessionsChange();
  }

  public async deleteChat(sessionId: string): Promise<void> {
    const runtime = this.#readyRuntime();
    const summary = this.#requireSummary(runtime, sessionId);
    const replacement = runtime.summaries.size === 1
      ? await this.createChat()
      : undefined;
    try {
      await new SessionLifecycleCommands(runtime).delete(
        sessionId,
        summary.revision,
      );
    } catch (error) {
      if (replacement !== undefined) {
        await this.deleteChat(replacement.sessionId).catch(() => undefined);
      }
      throw error;
    }
    const connected = runtime.sessions.get(sessionId);
    if (connected !== undefined) {
      connected.dispose();
      await connected.settled();
      runtime.sessions.delete(sessionId);
    }
    runtime.summaries.delete(sessionId);
    await this.#bindingStore.clear(this.rootId, sessionId);
    if (runtime.selectedSessionId === sessionId) {
      const replacement = runtime.sessions.keys().next().value;
      if (replacement === undefined) {
        await this.createChat();
        return;
      }
      runtime.selectedSessionId = replacement;
      await this.#bindingStore.select(this.rootId, replacement);
    }
    this.#emitSessionsChange();
  }

  public async checkpoints(sessionId: string): Promise<CheckpointList> {
    const runtime = this.#readyRuntime();
    this.#session(runtime, sessionId);
    return new SessionArtifactCommands(runtime).checkpoints(sessionId);
  }

  public async restoreCheckpoint(
    sessionId: string,
    checkpointId: SessionCheckpoint["id"],
  ): Promise<CheckpointRestore> {
    const runtime = this.#readyRuntime();
    this.#session(runtime, sessionId);
    const result = await new SessionArtifactCommands(runtime).restore(
      sessionId,
      checkpointId,
    );
    runtime.summaries.set(
      sessionId,
      await new SessionLifecycleCommands(runtime).status(sessionId),
    );
    this.#emitSessionsChange();
    return result;
  }

  public async forkCheckpoint(
    sessionId: string,
    checkpointId: SessionCheckpoint["id"],
    title: string,
  ): Promise<void> {
    const runtime = this.#readyRuntime();
    const connected = this.#session(runtime, sessionId);
    const result = await new SessionArtifactCommands(runtime).fork(
      sessionId,
      checkpointId,
      title,
    );
    connected.binding = {
      ...connected.binding,
      threadId: result.thread_id,
    };
    await this.#bindingStore.save(connected.binding);
    runtime.summaries.set(
      sessionId,
      await new SessionLifecycleCommands(runtime).status(sessionId),
    );
    this.#emitSessionsChange();
  }

  public async sessionPlan(sessionId: string): Promise<SessionPlan> {
    const runtime = this.#readyRuntime();
    this.#session(runtime, sessionId);
    return new SessionArtifactCommands(runtime).plan(sessionId);
  }

  public async implementPlan(
    sessionId: string,
    planId: string,
    transition: "implement" | "autopilot",
    sourceSessionId?: string,
  ): Promise<AcceptedPlanTurn> {
    const runtime = this.#readyRuntime();
    this.#session(runtime, sessionId);
    return new SessionArtifactCommands(runtime).implementPlan(
      sessionId,
      planId,
      transition,
      sourceSessionId,
    );
  }

  public async recoverTurn(
    sessionId: string,
    sourceTurnId: string,
    action: "retry" | "continue",
    guidance?: string,
  ): Promise<AcceptedPlanTurn> {
    const runtime = this.#readyRuntime();
    this.#session(runtime, sessionId);
    return new SessionArtifactCommands(runtime).recoverTurn(
      sessionId,
      sourceTurnId,
      action,
      guidance,
    );
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
    this.#session(runtime, sessionId);
    const summary = runtime.summaries.get(sessionId);
    if (summary !== undefined && isPlaceholderChatTitle(summary.title)) {
      const title = chatTitleFromPrompt(prompt);
      if (title !== undefined) {
        void this.#renameChat(runtime, sessionId, title).catch((error: unknown) => {
          this.#output.appendLine(
            `[runtime:${this.#workspace.name}] Chat title update failed: ` +
              (error instanceof Error ? error.message : String(error)),
          );
        });
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

  public async sessionProfile(
    sessionId: string,
  ): Promise<SessionProfileSnapshot> {
    return this.#commands(sessionId).profile();
  }

  public async sessionToolCatalog(
    sessionId: string,
  ): Promise<SessionToolCatalog> {
    return this.#commands(sessionId).toolCatalog();
  }

  public async providerCatalog(): Promise<ProviderCatalog> {
    return decodeProviderCatalog(
      await this.#readyRuntime().request("provider/list", {}),
    );
  }

  public async modelCatalog(provider?: string): Promise<ModelCatalog> {
    return decodeModelCatalog(
      await this.#readyRuntime().request(
        "model/list",
        provider === undefined ? {} : { provider },
      ),
    );
  }

  public async updateSessionProfile(
    sessionId: string,
    expectedRevision: number,
    patch: SessionProfilePatch,
  ): Promise<SessionProfileUpdate> {
    return this.#commands(sessionId).updateProfile(expectedRevision, patch);
  }

  public credentialReference(provider: string): string {
    return this.#credentialStore.reference(provider);
  }

  public async credentialStatus(provider: string): Promise<CredentialView> {
    const managedProvider = this.#context.workspaceState.get<string>(
      this.#credentialProviderKey,
    );
    const status = await this.#credentialStore.status(
      provider,
      managedProvider !== provider,
    );
    const validation = this.#context.workspaceState.get<{
      readonly validation: "valid" | "invalid";
      readonly validatedAt: string;
      readonly validationFailure?: "authentication" | "network" | "provider" | "unknown";
    }>(this.#credentialValidationKey(provider));
    if (status.status !== "configured" || validation === undefined ||
      Number.isNaN(Date.parse(validation.validatedAt))) {
      return status;
    }
    return { ...status, ...validation };
  }

  public async storeCredential(
    provider: string,
    secret: string,
  ): Promise<void> {
    await this.#credentialStore.store(provider, secret);
    await this.#context.workspaceState.update(
      this.#credentialValidationKey(provider),
      undefined,
    );
  }

  public async credentialEnvironment(
    provider: string,
  ): Promise<Readonly<Record<string, string>>> {
    return this.#credentialStore.environment(provider);
  }

  public async recordCredentialValidation(
    provider: string,
    validation: "valid" | "invalid",
    validationFailure?: "authentication" | "network" | "provider" | "unknown",
  ): Promise<void> {
    this.#credentialStore.reference(provider);
    await this.#context.workspaceState.update(
      this.#credentialValidationKey(provider),
      {
        validation,
        validatedAt: new Date().toISOString(),
        ...(validationFailure === undefined ? {} : { validationFailure }),
      },
    );
  }

  public async activateCredentialProvider(provider?: string): Promise<void> {
    if (provider !== undefined) {
      this.#credentialStore.reference(provider);
    }
    await this.#context.workspaceState.update(
      this.#credentialProviderKey,
      provider,
    );
  }

  #credentialValidationKey(provider: string): string {
    return `codehelper.credentialValidation.v1:${this.#workspaceIdentity.root_id}:${provider}`;
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
    const maxSteps = configuration.get<number>("runtime.maxSteps", 64);
    if (!Number.isInteger(maxSteps) || maxSteps < 1 || maxSteps > 128) {
      throw new Error("codehelper.runtime.maxSteps must be an integer between 1 and 128");
    }
    const configuredConfigPath = configuration
      .get<string>("runtime.configPath", "")
      .trim();
    if (configuredConfigPath.length > 0 && !isAbsolute(configuredConfigPath)) {
      throw new Error("codehelper.runtime.configPath must be a Host-local absolute path");
    }
    const credentialProvider = this.#context.workspaceState.get<string>(
      this.#credentialProviderKey,
    );
    const credentialEnvironment = credentialProvider === undefined
      ? {}
      : await this.#credentialStore.environment(credentialProvider);
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
      environment: credentialEnvironment,
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
      const storedBySession = new Map(
        stored.map((binding) => [binding.sessionId, binding]),
      );
      const lifecycle = new SessionLifecycleCommands(runtimeProcess.client);
      const durable = await lifecycle.list({
        includeArchived: true,
        limit: 1000,
      });
      const summaries = new Map(
        durable.sessions.map((summary) => [summary.session_id, summary]),
      );
      const sessions = new Map<string, ConnectedSession>();
      const attach = async (
        binding?: RuntimeBinding,
        create = false,
      ): Promise<void> => {
        let sessionId = binding?.sessionId ?? "";
        if (!create && binding === undefined) {
          throw new Error("Runtime session binding is required");
        }
        const connectOptions = create
          ? { create: true, title: defaultChatTitle, isolation: "shared" as const }
          : { binding: binding as RuntimeBinding };
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
          connectOptions,
        );
        sessionId = session.binding.sessionId;
        sessions.set(sessionId, session);
        if (!summaries.has(sessionId)) {
          summaries.set(sessionId, await lifecycle.status(sessionId));
        }
      };
      for (const summary of durable.sessions) {
        if (summary.archived) continue;
        const saved = storedBySession.get(summary.session_id);
        await attach({
          version: 1,
          rootId: this.rootId,
          workspaceURI: this.#workspaceIdentity.editor_uri,
          workspaceRoot: this.#workspaceIdentity.runtime_path,
          sessionId: summary.session_id,
          threadId: summary.thread_id,
          lastSeq: saved?.threadId === summary.thread_id
            ? saved.lastSeq
            : 0,
        });
      }
      for (const binding of stored) {
        if (!summaries.has(binding.sessionId)) {
          await this.#bindingStore.clear(this.rootId, binding.sessionId);
        }
      }
      if (sessions.size === 0) await attach(undefined, true);
      const selectedSessionId = this.#bindingStore.load(this.#workspaceIdentity)
        ?.sessionId;
      const resolvedSelected = selectedSessionId !== undefined &&
        sessions.has(selectedSessionId)
        ? selectedSessionId
        : sessions.keys().next().value;
      if (resolvedSelected === undefined) {
        throw new Error("Runtime attached no Chat sessions");
      }
      await this.#bindingStore.select(this.rootId, resolvedSelected);
      this.#output.appendLine(
        `[runtime:${this.#workspace.name}] attached ` +
        `${String(sessions.size)} Chat session(s), selected=${resolvedSelected}`,
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
        summaries,
        resolvedSelected,
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
    sessionId: string,
    title: string,
  ): Promise<void> {
    const resolved = title.trim();
    if (resolved.length === 0 || resolved.length > 256) {
      throw new Error("Chat title must contain between 1 and 256 characters");
    }
    await this.#updateChat(sessionId, { title: resolved }, runtime);
  }

  async #updateChat(
    sessionId: string,
    patch: SessionLifecyclePatch,
    runtime = this.#readyRuntime(),
  ): Promise<SessionLifecycleSummary> {
    const summary = this.#requireSummary(runtime, sessionId);
    const updated = await new SessionLifecycleCommands(runtime).update(
      sessionId,
      summary.revision,
      patch,
    );
    runtime.summaries.set(sessionId, updated.session);
    this.#emitSessionsChange();
    return updated.session;
  }

  async #attachSummary(
    runtime: ActiveRuntime,
    summary: SessionLifecycleSummary,
  ): Promise<void> {
    if (summary.archived) {
      throw new Error("Archived Chat must be restored before connecting");
    }
    if (runtime.sessions.has(summary.session_id)) return;
    let sessionId = summary.session_id;
    const connected = await connectSession(
      runtime,
      this.#bindingStore,
      this.#workspaceIdentity,
      async (event, replayed) => {
        await this.#emitEvent(sessionId, event, replayed);
      },
      (error) => {
        this.#reportSessionError(error);
      },
      {
        binding: {
          version: 1,
          rootId: this.rootId,
          workspaceURI: this.#workspaceIdentity.editor_uri,
          workspaceRoot: this.#workspaceIdentity.runtime_path,
          sessionId,
          threadId: summary.thread_id,
          lastSeq: 0,
        },
      },
    );
    sessionId = connected.binding.sessionId;
    runtime.sessions.set(sessionId, connected);
    runtime.summaries.set(sessionId, summary);
  }

  #requireSummary(
    runtime: ActiveRuntime,
    sessionId: string,
  ): SessionLifecycleSummary {
    const summary = runtime.summaries.get(sessionId);
    if (summary === undefined) throw new Error("Chat session is unavailable");
    return summary;
  }

  #chatSummary(
    runtime: ActiveRuntime,
    summary: SessionLifecycleSummary,
  ): ChatSessionSummary {
    return {
      sessionId: summary.session_id,
      threadId: summary.thread_id,
      title: isPlaceholderChatTitle(summary.title)
        ? defaultChatTitle
        : summary.title,
      isolation: summary.isolation as "worktree" | "shared",
      status: summary.status,
      pinned: summary.pinned,
      archived: summary.archived,
      workspaceLabel: summary.workspace_label,
      ...(summary.provider === undefined ? {} : { provider: summary.provider }),
      ...(summary.model === undefined ? {} : { model: summary.model }),
      ...(summary.mode === undefined ? {} : { mode: summary.mode }),
      executionEnvironment: "local",
      ...(summary.parent_thread_id === undefined
        ? {}
        : { parentThreadId: summary.parent_thread_id }),
      ...(summary.latest_turn_id === undefined
        ? {}
        : { latestTurnId: summary.latest_turn_id }),
      pendingApprovals: summary.pending_approvals,
      pendingInputs: summary.pending_inputs,
      checkpointCount: summary.checkpoint_count,
      changedFiles: summary.changed_files,
      totalTokens: summary.total_tokens,
      costMicrounits: summary.cost_microunits,
      costKnown: summary.cost_known,
      createdAt: summary.created_at,
      updatedAt: summary.updated_at,
      selected: summary.session_id === runtime.selectedSessionId,
      replayedEvents: runtime.sessions.get(summary.session_id)
        ?.replayedEvents ?? 0,
    };
  }

  async #emitEvent(
    sessionId: string,
    event: DecodedEvent,
    replayed: boolean,
  ): Promise<void> {
    for (const listener of this.#eventListeners) {
      await listener(sessionId, event, replayed);
    }
    if (!replayed && lifecycleStatusEvents.has(event.kind)) {
      const runtime = this.#supervisor.runtime;
      if (runtime !== undefined && runtime.summaries.has(sessionId)) {
        try {
          const summary = await new SessionLifecycleCommands(runtime)
            .status(sessionId);
          runtime.summaries.set(sessionId, summary);
          this.#emitSessionsChange();
        } catch (error) {
          this.#output.appendLine(
            `[runtime:${this.#workspace.name}] lifecycle refresh failed: ` +
            (error instanceof Error ? error.message : String(error)),
          );
        }
      }
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
    public readonly summaries: Map<string, SessionLifecycleSummary>,
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
