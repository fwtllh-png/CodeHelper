import * as vscode from "vscode";

import { parseContextDirectives } from "../context/directives.js";
import type {
  ApprovalDecision,
  ApprovalScope,
} from "../runtime/session.js";
import type { SupervisorSnapshot } from "../runtime/supervisor.js";
import type { EditPlanPreview } from "../edits/preview.js";
import { decodeWebviewMessage } from "./messages.js";
import {
  createChatErrorMessage,
  createChatSnapshotMessage,
  type ChatHostMessage,
} from "./contract.js";
import {
  ChatProjector,
  type ApprovalCard,
  type InputCard,
} from "./projector.js";
import { isUnknownEvent, type DecodedEvent } from "../protocol/decode.js";
import type {
  WorkspaceRuntime,
  WorkspaceRuntimeRegistry,
} from "../workspace/registry.js";
import type { ChatSessionSummary } from "../runtime/controller.js";
import { projectEditPlan, type EditPlanCard } from "../edits/model.js";
import type { ApprovalRequiredData } from "../protocol/generated.js";
import { testBuildEnabled } from "../test-mode.js";
import {
  chatWebviewResourceRoot,
  renderChatHTML,
} from "./webview/shell.js";
import { ResourceNavigator } from "./resource-navigator.js";
import {
  projectChatResources,
  type ResourceReference,
} from "./resources.js";
import {
  projectComposer,
  type ComposerControl,
} from "./composer.js";
import type {
  SessionProfileSnapshot,
  SessionToolCatalog,
} from "../runtime/session.js";
import type { CredentialView } from "../security/credentials.js";

interface RootChatState {
  readonly projectors: Map<string, ChatProjector>;
  readonly composers: Map<string, SessionComposerState>;
  runtime: SupervisorSnapshot;
  sessionSearchQuery: string;
  sessionSearch: {
    readonly query: string;
    readonly sessionIds: readonly string[];
  } | undefined;
}

interface SessionComposerState {
  readonly profile?: SessionProfileSnapshot;
  readonly catalog?: SessionToolCatalog;
  readonly credential?: CredentialView;
  readonly error?: string;
}

export class ChatViewProvider implements vscode.WebviewViewProvider, vscode.Disposable {
  readonly #registry: WorkspaceRuntimeRegistry;
  readonly #extensionUri: vscode.Uri;
  readonly #roots = new Map<string, RootChatState>();
  readonly #editPreview: EditPlanPreview;
  readonly #resourceNavigator: ResourceNavigator;
  readonly #subscriptions: vscode.Disposable[];
  readonly #modalApprovals = new Set<string>();
  readonly #modalInputs = new Set<string>();
  readonly #submittedApprovals = new Set<string>();
  readonly #mergePlans = new Map<string, EditPlanCard>();
  readonly #resources = new Map<string, ResourceReference>();
  readonly #composerLoads = new Set<string>();
  #view: vscode.WebviewView | undefined;
  #flushTimer: NodeJS.Timeout | undefined;
  #webviewReady = false;
  #snapshotPosts = 0;
  #deferredError: ChatHostMessage | undefined;

  public constructor(
    registry: WorkspaceRuntimeRegistry,
    editPreview: EditPlanPreview,
    extensionUri: vscode.Uri,
  ) {
    this.#registry = registry;
    this.#editPreview = editPreview;
    this.#resourceNavigator = new ResourceNavigator(registry, editPreview);
    this.#extensionUri = extensionUri;
    this.#syncRoots();
    this.#subscriptions = [
      registry.onEvent(({ root, sessionId, event, replayed }) => {
        this.#onEvent(root, sessionId, event, replayed);
      }),
      registry.onStateChange(({ root, snapshot }) => {
        this.#state(root.rootId).runtime = snapshot;
        if (snapshot.state !== "ready") {
          this.#state(root.rootId).composers.clear();
        }
        this.#scheduleFlush();
      }),
      registry.onDidChangeRoots(() => {
        this.#syncRoots();
        this.#scheduleFlush();
      }),
      registry.onDidChangeSelection(() => {
        this.#scheduleFlush();
      }),
      registry.onDidChangeSessions((root) => {
        this.#syncSessions(root);
        this.#scheduleFlush();
      }),
    ];
  }

  public resolveWebviewView(view: vscode.WebviewView): void {
    this.#view = view;
    this.#webviewReady = false;
    view.webview.options = {
      enableScripts: true,
      localResourceRoots: [chatWebviewResourceRoot(this.#extensionUri)],
    };
    view.webview.html = renderChatHTML(view.webview, this.#extensionUri);
    this.#subscriptions.push(
      view.webview.onDidReceiveMessage((value: unknown) => {
        void this.#receive(value);
      }),
      view.onDidDispose(() => {
        if (this.#view === view) {
          this.#view = undefined;
          this.#webviewReady = false;
        }
      }),
      view.onDidChangeVisibility(() => {
        if (view.visible) {
          const deferredError = this.#deferredError;
          this.#deferredError = undefined;
          if (deferredError !== undefined) {
            this.#post(deferredError);
          }
          this.#scheduleFlush();
        } else if (this.#flushTimer !== undefined) {
          clearTimeout(this.#flushTimer);
          this.#flushTimer = undefined;
        }
      }),
    );
    this.#scheduleFlush();
  }

  public dispose(): void {
    if (this.#flushTimer !== undefined) {
      clearTimeout(this.#flushTimer);
      this.#flushTimer = undefined;
    }
    for (const subscription of this.#subscriptions.splice(0)) {
      subscription.dispose();
    }
  }

  public get webviewReady(): boolean {
    return this.#webviewReady;
  }

  public get projectionDiagnostics(): {
    readonly visible: boolean;
    readonly snapshotPosts: number;
  } {
    return {
      visible: this.#view?.visible ?? false,
      snapshotPosts: this.#snapshotPosts,
    };
  }

  public invalidateProjection(): void {
    this.#scheduleFlush();
  }

  public async decidePlan(
    rootId: string,
    requestId: string,
    decision: "approve" | "deny",
  ): Promise<void> {
    const root = this.#root(rootId);
    const match = [...this.#state(rootId).projectors.entries()]
      .map(([sessionId, projector]) => ({
        sessionId,
        approval: projector.pendingApprovals().find(
          (candidate) => candidate.requestId === requestId,
        ),
      }))
      .find((candidate) => candidate.approval !== undefined);
    if (match?.approval === undefined) {
      throw new Error("approval request is unknown or already resolved");
    }
    const approval = match.approval;
    if (approval.editPlan === undefined) {
      throw new Error("approval has no edit plan");
    }
    await this.#decide(root, match.sessionId, approval, decision, "once");
  }

  #onEvent(
    root: WorkspaceRuntime,
    sessionId: string,
    event: DecodedEvent,
    replayed: boolean,
  ): void {
    const projector = this.#projector(root.rootId, sessionId);
    projector.apply(event);
    if (!isUnknownEvent(event) && event.kind === "tool.catalog.changed") {
      this.#state(root.rootId).composers.clear();
    }
    this.#scheduleFlush();
    if (replayed || isUnknownEvent(event)) {
      return;
    }
    if (event.kind === "approval.required") {
      const card = projector.pendingApprovals().find(
        (approval) => approval.requestId === event.data.request_id,
      );
      if (card !== undefined) {
        void this.#showApproval(root, sessionId, card);
      }
    } else if (event.kind === "approval.resolved") {
      this.#submittedApprovals.delete(
        sessionKey(root.rootId, sessionId, event.data.request_id),
      );
    } else if (event.kind === "input.required") {
      const card = projector.pendingInputs().find(
        (input) => input.requestId === event.data.request_id,
      );
      if (card !== undefined) {
        void this.#showInput(root, sessionId, card);
      }
    }
  }

  async #receive(value: unknown): Promise<void> {
    try {
      const message = decodeWebviewMessage(value);
      const root = this.#registry.selected;
      switch (message.type) {
        case "ready":
          this.#webviewReady = true;
          break;
        case "open-resource": {
          const reference = this.#resources.get(message.resourceId);
          if (reference === undefined) {
            throw new Error("resource reference is unknown or stale");
          }
          await this.#resourceNavigator.open(reference);
          break;
        }
        case "select-root":
          await this.#registry.select(message.rootId);
          break;
        case "select-chat":
          await root.controller.selectChat(message.sessionId);
          break;
        case "search-chats": {
          const state = this.#state(root.rootId);
          const query = message.query.trim();
          state.sessionSearchQuery = query;
          if (query.length === 0) {
            state.sessionSearch = undefined;
            this.#scheduleFlush();
            break;
          }
          const matches = await root.controller.searchChats({
            query,
            includeArchived: true,
            limit: 1000,
          });
          if (state.sessionSearchQuery === query) {
            state.sessionSearch = {
              query,
              sessionIds: matches.map((session) => session.sessionId),
            };
            this.#scheduleFlush();
          }
          break;
        }
        case "manage-chat":
          await this.#manageChat(root, message.sessionId, message.action);
          break;
        case "plan-action": {
          const session = this.#selectedSession(root);
          const plan = await root.controller.sessionPlan(session.sessionId);
          if (plan.artifact === undefined ||
            plan.artifact.id !== message.planId) {
            throw new Error("Plan Artifact is unknown or stale");
          }
          if (message.action === "open") {
            const document = await vscode.workspace.openTextDocument({
              language: "markdown",
              content: plan.artifact.body,
            });
            await vscode.window.showTextDocument(document, {
              preview: false,
              preserveFocus: false,
            });
            break;
          }
          if (message.action === "autopilot") {
            const confirmation = await vscode.window.showWarningMessage(
              "Start this Plan with Autopilot?",
              {
                modal: true,
                detail: "The Runtime may approve eligible actions automatically, " +
                  "but Host permission ceilings, Guard, Policy, and Sandbox remain active.",
              },
              "Start Autopilot",
            );
            if (confirmation !== "Start Autopilot") break;
          }
          await root.controller.implementPlan(
            session.sessionId,
            message.planId,
            message.action,
          );
          break;
        }
        case "new-chat":
          await root.controller.createChat();
          break;
        case "repair-runtime":
          await vscode.commands.executeCommand("codehelper.repairRuntime");
          break;
        case "run-setup":
          await vscode.commands.executeCommand("codehelper.runSetup");
          break;
        case "configure-composer": {
          const session = this.#selectedSession(root);
          await this.#configureComposer(root, session, message.control);
          break;
        }
        case "submit": {
          const session = this.#selectedSession(root);
          const parsed = parseContextDirectives(message.text);
          if (parsed.prompt.length === 0) {
            throw new Error("enter a prompt in addition to the context directive");
          }
          const context = await root.contextBridge.capture(parsed.directives);
          await root.controller.submitPrompt(
            session.sessionId, parsed.prompt, context,
          );
          break;
        }
        case "stop": {
          const session = this.#selectedSession(root);
          const turnId = this.#projector(
            root.rootId, session.sessionId,
          ).snapshot().activeTurnId;
          if (turnId === undefined) {
            throw new Error("there is no active turn to stop");
          }
          await root.controller.cancelTurn(session.sessionId, turnId);
          break;
        }
        case "approval": {
          const session = this.#selectedSession(root);
          const approval = this.#approval(
            root.rootId, session.sessionId, message.requestId,
          );
          await this.#decide(
            root,
            session.sessionId,
            approval,
            message.decision,
            message.decision === "approve" ? message.scope : "once",
          );
          break;
        }
        case "preview": {
          const session = this.#selectedSession(root);
          const approval = this.#approval(
            root.rootId, session.sessionId, message.requestId,
          );
          if (approval.editPlan === undefined) {
            throw new Error("approval has no edit plan");
          }
          await this.#editPreview.show(approval.editPlan, root.rootId);
          break;
        }
        case "input": {
          const session = this.#selectedSession(root);
          const input = this.#input(
            root.rootId, session.sessionId, message.requestId,
          );
          await root.controller.replyInput(
            session.sessionId,
            input.turnId,
            input.requestId,
            message.answer,
          );
          break;
        }
        case "merge-chat": {
          const session = this.#selectedSession(root);
          const key = sessionKey(root.rootId, session.sessionId, "merge");
          if (message.planId === undefined) {
            const result = await root.controller.mergeChat(
              session.sessionId, "preview",
            );
            const plan = decodeMergePlan(result);
            this.#mergePlans.set(key, plan);
            await this.#editPreview.show(plan, root.rootId);
          } else {
            if (this.#mergePlans.get(key)?.id !== message.planId) {
              throw new Error("Chat merge plan is unknown or stale");
            }
            await root.controller.mergeChat(
              session.sessionId, "apply", message.planId,
            );
            this.#mergePlans.delete(key);
          }
          this.#scheduleFlush();
          break;
        }
      }
    } catch (error) {
      this.#post(createChatErrorMessage(
        error instanceof Error ? error.message : String(error),
      ));
    }
  }

  async #manageChat(
    root: WorkspaceRuntime,
    sessionId: string,
    action: "menu" | "rename" | "pin" | "unpin" | "archive" | "restore" | "delete" | "checkpoints",
  ): Promise<void> {
    const session = root.controller.sessions().find(
      (candidate) => candidate.sessionId === sessionId,
    );
    if (session === undefined) throw new Error("Chat session is unavailable");
    switch (action) {
      case "menu": {
        const choice = await vscode.window.showQuickPick([
          { label: "$(edit) Rename", action: "rename" as const },
          {
            label: session.pinned ? "$(pinned) Unpin" : "$(pin) Pin",
            action: session.pinned ? "unpin" as const : "pin" as const,
          },
          {
            label: session.archived ? "$(history) Restore" : "$(archive) Archive",
            action: session.archived ? "restore" as const : "archive" as const,
          },
          {
            label: `$(history) Checkpoints (${String(session.checkpointCount)})`,
            action: "checkpoints" as const,
          },
          { label: "$(trash) Delete", action: "delete" as const },
        ], {
          title: session.title,
          placeHolder: "Manage Session",
        });
        if (choice !== undefined) {
          await this.#manageChat(root, sessionId, choice.action);
        }
        break;
      }
      case "rename": {
        const title = await vscode.window.showInputBox({
          title: "Rename Chat Session",
          value: session.title,
          prompt: "Session title is stored by the CodeHelper Runtime",
          validateInput: (value) => value.trim().length === 0
            ? "Enter a session title"
            : value.length > 256 ? "Title must be at most 256 characters" : null,
        });
        if (title !== undefined) {
          await root.controller.renameChat(sessionId, title);
        }
        break;
      }
      case "pin":
      case "unpin":
        await root.controller.pinChat(sessionId, action === "pin");
        break;
      case "archive":
      case "restore":
        await root.controller.archiveChat(sessionId, action === "archive");
        break;
      case "delete": {
        const confirmation = await vscode.window.showWarningMessage(
          `Delete "${session.title}" permanently?`,
          { modal: true, detail: "Runtime history and isolated workspace data will be removed." },
          "Delete",
        );
        if (confirmation === "Delete") {
          await root.controller.deleteChat(sessionId);
        }
        break;
      }
      case "checkpoints": {
        const list = await root.controller.checkpoints(sessionId);
        if (list.checkpoints.length === 0) {
          await vscode.window.showInformationMessage(
            "This Session has no restorable Checkpoints.",
          );
          break;
        }
        const selection = await vscode.window.showQuickPick(
          list.checkpoints.map((checkpoint) => ({
            label: checkpoint.summary,
            description: `${checkpoint.status} · ${
              new Date(checkpoint.created_at).toLocaleString()
            }`,
            detail: `${String(checkpoint.changed_files)} changed files` +
              (checkpoint.external_side_effects
                ? " · completed Tool effects remain applied"
                : ""),
            checkpoint,
          })),
          {
            title: `Checkpoints · ${session.title}`,
            placeHolder: "Select a Checkpoint",
          },
        );
        if (selection === undefined) break;
        const choices = [
          ...(selection.checkpoint.can_restore
            ? [{ label: "$(discard) Restore state", action: "restore" as const }]
            : []),
          ...(selection.checkpoint.can_fork
            ? [{ label: "$(git-branch) Fork Session", action: "fork" as const }]
            : []),
        ];
        if (choices.length === 0) {
          throw new Error(
            "Checkpoint is stale for the current Session Profile Revision",
          );
        }
        const choice = await vscode.window.showQuickPick(choices, {
          title: selection.checkpoint.summary,
          placeHolder: "Choose a Checkpoint operation",
        });
        if (choice?.action === "restore") {
          const confirmation = await vscode.window.showWarningMessage(
            "Restore Runtime state to this Checkpoint?",
            {
              modal: true,
              detail: "Completed file, command, Tool, and network effects are " +
                "not reversed or replayed. Only model-visible Runtime state is restored.",
            },
            "Restore State",
          );
          if (confirmation === "Restore State") {
            await root.controller.restoreCheckpoint(
              sessionId,
              selection.checkpoint.id,
            );
          }
        } else if (choice?.action === "fork") {
          const title = await vscode.window.showInputBox({
            title: "Fork Checkpoint",
            value: `${session.title} · Fork`,
            validateInput: (value) => value.trim().length === 0
              ? "Enter a Fork title"
              : value.length > 256 ? "Title must be at most 256 characters" : null,
          });
          if (title !== undefined) {
            await root.controller.forkCheckpoint(
              sessionId,
              selection.checkpoint.id,
              title,
            );
          }
        }
        break;
      }
    }
    this.#scheduleFlush();
  }

  async #showApproval(
    root: WorkspaceRuntime,
    sessionId: string,
    approval: ApprovalCard,
  ): Promise<void> {
    const key = sessionKey(root.rootId, sessionId, approval.requestId);
    if (this.#modalApprovals.has(key) || isExpired(approval.expiresAt)) {
      return;
    }
    this.#modalApprovals.add(key);
    try {
      if (approval.editPlan !== undefined) {
        await this.#editPreview.show(approval.editPlan, root.rootId);
      }
      if (testBuildEnabled) return;
      const choices: string[] = [];
      if (vscode.workspace.isTrusted) {
        if (approval.allowedScopes.includes("once")) {
          choices.push("Approve once");
        }
        if (approval.allowedScopes.includes("session")) {
          choices.push("Approve for session");
        }
        if (approval.allowedScopes.includes("always")) {
          choices.push("Always approve");
        }
      }
      choices.push("Deny", "Cancel turn");
      const selected = await vscode.window.showWarningMessage(
        `${root.label}: ${approvalSummary(approval)}`,
        { modal: true, detail: approval.arguments },
        ...choices,
      );
      const decision = modalDecision(selected);
      if (decision !== undefined) {
        await this.#decide(
          root,
          sessionId,
          approval,
          decision.decision,
          decision.scope,
        );
      }
    } catch (error) {
      this.#post(createChatErrorMessage(
        error instanceof Error ? error.message : String(error),
      ));
    } finally {
      this.#modalApprovals.delete(key);
    }
  }

  async #showInput(
    root: WorkspaceRuntime,
    sessionId: string,
    input: InputCard,
  ): Promise<void> {
    const key = sessionKey(root.rootId, sessionId, input.requestId);
    if (this.#modalInputs.has(key) || isExpired(input.expiresAt)) {
      return;
    }
    this.#modalInputs.add(key);
    try {
      const answer = input.options.length > 0
        ? await vscode.window.showQuickPick([...input.options], {
            title: `${root.label}: ${input.prompt}`,
            ignoreFocusOut: true,
          })
        : await vscode.window.showInputBox({
            title: `${root.label}: ${input.prompt}`,
            ignoreFocusOut: true,
          });
      if (answer !== undefined) {
        await root.controller.replyInput(
          sessionId, input.turnId, input.requestId, answer,
        );
      }
    } catch (error) {
      this.#post(createChatErrorMessage(
        error instanceof Error ? error.message : String(error),
      ));
    } finally {
      this.#modalInputs.delete(key);
    }
  }

  async #decide(
    root: WorkspaceRuntime,
    sessionId: string,
    approval: ApprovalCard,
    decision: ApprovalDecision,
    scope: ApprovalScope,
  ): Promise<void> {
    const current = this.#approval(
      root.rootId, sessionId, approval.requestId,
    );
    if (current.turnId !== approval.turnId ||
      current.itemId !== approval.itemId ||
      current.editPlan?.id !== approval.editPlan?.id) {
      throw new Error("approval identity changed");
    }
    const key = sessionKey(root.rootId, sessionId, current.requestId);
    if (this.#submittedApprovals.has(key)) {
      throw new Error("approval decision is already submitted");
    }
    if (isExpired(current.expiresAt)) {
      throw new Error("approval request has expired");
    }
    if (decision === "approve" && !current.allowedScopes.includes(scope)) {
      throw new Error(`approval scope ${scope} is not allowed`);
    }
    this.#submittedApprovals.add(key);
    try {
      await root.controller.decideApproval(
        sessionId,
        current.turnId,
        current.requestId,
        decision,
        scope,
        current.expiresAt,
        current.editPlan?.id,
      );
    } catch (error) {
      this.#submittedApprovals.delete(key);
      throw error;
    }
  }

  #approval(rootId: string, sessionId: string, requestId: string): ApprovalCard {
    const approval = this.#projector(rootId, sessionId).pendingApprovals().find(
      (candidate) => candidate.requestId === requestId,
    );
    if (approval === undefined) {
      throw new Error("approval request is unknown or already resolved");
    }
    return approval;
  }

  #input(rootId: string, sessionId: string, requestId: string): InputCard {
    const input = this.#projector(rootId, sessionId).pendingInputs().find(
      (candidate) => candidate.requestId === requestId,
    );
    if (input === undefined) {
      throw new Error("input request is unknown or already resolved");
    }
    return input;
  }

  #scheduleFlush(): void {
    if (this.#flushTimer !== undefined ||
      this.#view === undefined ||
      !this.#view.visible) {
      return;
    }
    this.#flushTimer = setTimeout(() => {
      this.#flushTimer = undefined;
      if (this.#view === undefined || !this.#view.visible) {
        return;
      }
      const root = this.#registry.selected;
      const state = this.#state(root.rootId);
      const sessions = this.#availableSessions(root).map((session) => ({
        ...session,
        active: this.#projector(
          root.rootId, session.sessionId,
        ).snapshot().activeTurnId !== undefined,
      }));
      const selected = sessions.find((session) => session.selected);
      const projector = selected === undefined
        ? new ChatProjector()
        : this.#projector(root.rootId, selected.sessionId);
      const resources = projectChatResources(
        projector.snapshot(),
        root.rootId,
        selected?.sessionId ?? "unavailable",
      );
      this.#resources.clear();
      for (const reference of resources.references) {
        this.#resources.set(reference.id, reference);
      }
      const mergePlan = selected === undefined
        ? undefined
        : this.#mergePlans.get(
            sessionKey(root.rootId, selected.sessionId, "merge"),
          );
      const composerState = selected === undefined
        ? undefined
        : state.composers.get(selected.sessionId);
      if (selected !== undefined &&
        state.runtime.state === "ready" &&
        composerState === undefined) {
        void this.#loadComposer(root, selected.sessionId);
      }
      const composer = composerState?.profile === undefined ||
        composerState.catalog === undefined ||
        composerState.credential === undefined
        ? undefined
        : projectComposer(
            composerState.profile,
            composerState.catalog,
            composerState.credential,
            vscode.workspace.isTrusted,
          );
      this.#post(createChatSnapshotMessage({
        snapshot: resources.snapshot,
        resources: resources.views,
        state: state.runtime.state,
        ...(state.runtime.error === undefined
          ? {}
          : { error: state.runtime.error }),
        trusted: vscode.workspace.isTrusted,
        selectedRootId: root.rootId,
        selectedRootLabel: root.label,
        sessions,
        ...(state.sessionSearch === undefined
          ? {}
          : { sessionSearch: state.sessionSearch }),
        ...(mergePlan === undefined ? {} : { mergePlanId: mergePlan.id }),
        roots: this.#registry.roots.map((candidate) => ({
          id: candidate.rootId,
          label: candidate.label,
        })),
        ...(composer === undefined ? {} : { composer }),
      }));
    }, 16);
  }

  #post(value: ChatHostMessage): void {
    if (this.#view?.visible === true) {
      if (value.type === "snapshot") {
        this.#snapshotPosts++;
      }
      void this.#view.webview.postMessage(value);
    } else if (value.type === "error") {
      this.#deferredError = value;
    }
  }

  #syncRoots(): void {
    const live = new Set(this.#registry.roots.map((root) => root.rootId));
    for (const root of this.#registry.roots) {
      if (!this.#roots.has(root.rootId)) {
        this.#roots.set(root.rootId, {
          projectors: new Map(),
          composers: new Map(),
          runtime: root.controller.snapshot,
          sessionSearchQuery: "",
          sessionSearch: undefined,
        });
      }
      this.#syncSessions(root);
    }
    for (const rootId of this.#roots.keys()) {
      if (!live.has(rootId)) {
        this.#roots.delete(rootId);
      }
    }
  }

  #state(rootId: string): RootChatState {
    const state = this.#roots.get(rootId);
    if (state === undefined) {
      throw new Error("workspace root projection is unavailable");
    }
    return state;
  }

  #syncSessions(root: WorkspaceRuntime): void {
    const state = this.#state(root.rootId);
    const sessions = this.#availableSessions(root);
    const live = new Set(sessions.map((session) => session.sessionId));
    for (const session of sessions) {
      if (!state.projectors.has(session.sessionId)) {
        state.projectors.set(session.sessionId, new ChatProjector());
      }
    }
    for (const sessionId of state.projectors.keys()) {
      if (!live.has(sessionId)) state.projectors.delete(sessionId);
    }
    for (const sessionId of state.composers.keys()) {
      if (!live.has(sessionId)) state.composers.delete(sessionId);
    }
  }

  async #loadComposer(
    root: WorkspaceRuntime,
    sessionId: string,
  ): Promise<void> {
    const key = sessionKey(root.rootId, sessionId, "composer");
    if (this.#composerLoads.has(key)) return;
    this.#composerLoads.add(key);
    try {
      const [profile, catalog] = await Promise.all([
        root.controller.sessionProfile(sessionId),
        root.controller.sessionToolCatalog(sessionId),
      ]);
      const credential = await root.controller.credentialStatus(
        profile.profile.provider,
      );
      this.#state(root.rootId).composers.set(sessionId, {
        profile,
        catalog,
        credential,
      });
    } catch (error) {
      this.#state(root.rootId).composers.set(sessionId, {
        error: error instanceof Error ? error.message : String(error),
      });
    } finally {
      this.#composerLoads.delete(key);
      this.#scheduleFlush();
    }
  }

  async #configureComposer(
    root: WorkspaceRuntime,
    session: ChatSessionSummary,
    control: ComposerControl,
  ): Promise<void> {
    let state = this.#state(root.rootId).composers.get(session.sessionId);
    if (state?.profile === undefined ||
      state.catalog === undefined ||
      state.credential === undefined) {
      this.#state(root.rootId).composers.delete(session.sessionId);
      await this.#loadComposer(root, session.sessionId);
      state = this.#state(root.rootId).composers.get(session.sessionId);
    }
    if (state?.profile === undefined ||
      state.catalog === undefined ||
      state.credential === undefined) {
      throw new Error(state?.error ?? "Session Profile is unavailable");
    }
    if (control === "provider" || control === "model") {
      await vscode.commands.executeCommand(
        "codehelper.runSetup",
        root.rootId,
        ...(control === "model"
          ? [state.profile.profile.provider]
          : []),
      );
      this.#state(root.rootId).composers.delete(session.sessionId);
      this.#scheduleFlush();
      return;
    }
    if (control === "credential") {
      await vscode.commands.executeCommand(
        "codehelper.configureCredential",
        root.rootId,
        session.sessionId,
      );
      this.#state(root.rootId).composers.delete(session.sessionId);
      this.#scheduleFlush();
      return;
    }

    const snapshot = state.profile;
    const mutable = new Set(snapshot.capabilities.mutable_fields);
    if (control === "tools") {
      if (!mutable.has("enabled_tool_ids")) {
        throw new Error(
          "Session Profile field enabled_tool_ids is fixed by this Runtime",
        );
      }
      const selected = await pickTools(state.catalog);
      if (selected === undefined) return;
      try {
        const enabledToolIDs = selected.length === state.catalog.tools.length
          ? []
          : selected;
        const update = await root.controller.updateSessionProfile(
          session.sessionId,
          snapshot.profile.revision,
          { enabled_tool_ids: enabledToolIDs },
        );
        const catalog = await root.controller.sessionToolCatalog(
          session.sessionId,
        );
        this.#state(root.rootId).composers.set(session.sessionId, {
          profile: { ...snapshot, profile: update.profile },
          catalog,
          credential: state.credential,
        });
        if (update.prompt_cache_reset) {
          void vscode.window.showInformationMessage(
            "CodeHelper prompt cache reset for the updated Tool selection.",
          );
        }
        this.#scheduleFlush();
      } catch (error) {
        this.#state(root.rootId).composers.delete(session.sessionId);
        await this.#loadComposer(root, session.sessionId);
        throw error;
      }
      return;
    }
    let field: "mode" | "reasoning_effort" | "approval_posture";
    let value: string | undefined;
    if (control === "mode") {
      field = "mode";
      value = await pickValue(
        "CodeHelper: Agent Mode",
        ["plan", "act", "operate"],
        snapshot.profile.mode,
      );
    } else if (control === "thinking") {
      field = "reasoning_effort";
      const efforts = snapshot.capabilities.model_capabilities.reasoning_efforts;
      if (efforts === undefined || efforts.length === 0) {
        throw new Error("The current Model exposes no thinking effort options");
      }
      value = await pickValue(
        "CodeHelper: Thinking Effort",
        ["", ...efforts],
        snapshot.profile.reasoning_effort ?? "",
      );
    } else {
      field = "approval_posture";
      const values = vscode.workspace.isTrusted
        ? ["never", "suggest", "auto", "bypass"]
        : ["never", "suggest"];
      value = await pickValue(
        "CodeHelper: Approval Posture",
        values,
        snapshot.profile.approval_posture,
      );
    }
    if (value === undefined) return;
    if (!mutable.has(field)) {
      throw new Error(`Session Profile field ${field} is fixed by this Runtime`);
    }
    try {
      const update = await root.controller.updateSessionProfile(
        session.sessionId,
        snapshot.profile.revision,
        field === "mode"
          ? { mode: value }
          : field === "reasoning_effort"
            ? { reasoning_effort: value }
            : { approval_posture: value },
      );
      this.#state(root.rootId).composers.set(session.sessionId, {
        profile: { ...snapshot, profile: update.profile },
        catalog: state.catalog,
        credential: state.credential,
      });
      if (update.prompt_cache_reset) {
        void vscode.window.showInformationMessage(
          "CodeHelper prompt cache reset for the updated Session Profile.",
        );
      }
      this.#scheduleFlush();
    } catch (error) {
      this.#state(root.rootId).composers.delete(session.sessionId);
      await this.#loadComposer(root, session.sessionId);
      throw error;
    }
  }

  #availableSessions(root: WorkspaceRuntime): readonly ChatSessionSummary[] {
    return root.controller.snapshot.state === "ready"
      ? root.controller.sessions()
      : [];
  }

  #selectedSession(root: WorkspaceRuntime): ChatSessionSummary {
    const session = this.#availableSessions(root).find(
      (candidate) => candidate.selected,
    );
    if (session === undefined) throw new Error("no Chat session is selected");
    return session;
  }

  #projector(rootId: string, sessionId: string): ChatProjector {
    const state = this.#state(rootId);
    let projector = state.projectors.get(sessionId);
    if (projector === undefined) {
      projector = new ChatProjector();
      state.projectors.set(sessionId, projector);
    }
    return projector;
  }

  #root(rootId: string): WorkspaceRuntime {
    const root = this.#registry.find(rootId);
    if (root === undefined) {
      throw new Error("workspace root is unknown or no longer open");
    }
    return root;
  }
}

interface ToolPickItem extends vscode.QuickPickItem {
  readonly toolID?: string;
}

async function pickTools(
  catalog: SessionToolCatalog,
): Promise<readonly string[] | undefined> {
  const picker = vscode.window.createQuickPick<ToolPickItem>();
  picker.title = "CodeHelper: Session Tools";
  picker.placeholder = "Search by tool, source, capability, or availability";
  picker.canSelectMany = true;
  picker.ignoreFocusOut = true;
  picker.matchOnDescription = true;
  picker.matchOnDetail = true;
  const sourceOrder = ["builtin", "mcp", "plugin", "skill", "dynamic"];
  const items: ToolPickItem[] = [];
  for (const source of sourceOrder) {
    const tools = catalog.tools.filter((tool) => tool.source_kind === source);
    if (tools.length === 0) continue;
    items.push({
      label: source.toUpperCase(),
      kind: vscode.QuickPickItemKind.Separator,
    });
    for (const tool of tools) {
      const status = tool.availability === "available"
        ? tool.state
        : `${tool.availability}: ${tool.unavailable_reason ?? tool.state}`;
      items.push({
        toolID: tool.id,
        label: tool.availability === "unavailable"
          ? `$(warning) ${tool.name}`
          : tool.name,
        description: `${tool.source_label} · ${tool.capability}`,
        detail: `${tool.description} · ${status} · Guarded`,
        picked: tool.enabled,
        alwaysShow: true,
      });
    }
  }
  picker.items = items;
  const toolItems = items.filter(isToolPickItem);
  const initial = toolItems.filter((item) => item.picked === true);
  picker.selectedItems = initial;
  const resetButton: vscode.QuickInputButton = {
    iconPath: new vscode.ThemeIcon("discard"),
    tooltip: "Reset current selection",
  };
  const allButton: vscode.QuickInputButton = {
    iconPath: new vscode.ThemeIcon("check-all"),
    tooltip: "Select all tools",
  };
  picker.buttons = [resetButton, allButton];
  return new Promise((resolve) => {
    let accepted = false;
    const disposables = [
      picker.onDidTriggerButton((button) => {
        picker.selectedItems = button === allButton ? toolItems : initial;
      }),
      picker.onDidAccept(() => {
        if (picker.selectedItems.length === 0) {
          void vscode.window.showWarningMessage(
            "Select at least one tool; an empty Runtime allowlist means all tools.",
          );
          return;
        }
        accepted = true;
        const selected = picker.selectedItems
          .filter(isToolPickItem)
          .map((item) => item.toolID)
          .sort();
        picker.hide();
        resolve(selected);
      }),
      picker.onDidHide(() => {
        if (!accepted) resolve(undefined);
        for (const disposable of disposables) disposable.dispose();
        picker.dispose();
      }),
    ];
    picker.show();
  });
}

function isToolPickItem(
  item: ToolPickItem,
): item is ToolPickItem & { readonly toolID: string } {
  return typeof item.toolID === "string";
}

function sessionKey(
  rootId: string,
  sessionId: string,
  requestId: string,
): string {
  return `${rootId}:${sessionId}:${requestId}`;
}

type WireEditPlan = NonNullable<ApprovalRequiredData["edit_plan"]>;

function decodeMergePlan(value: unknown): EditPlanCard {
  if (!isObject(value)) throw new Error("session/merge result must be an object");
  const plan = value["plan"];
  if (!isObject(plan) ||
    typeof plan["id"] !== "string" ||
    !/^[0-9a-f]{64}$/u.test(plan["id"]) ||
    typeof plan["diff"] !== "string" ||
    !Array.isArray(plan["files"])) {
    throw new Error("session/merge returned an invalid edit plan");
  }
  return projectEditPlan(plan as unknown as WireEditPlan);
}

function isObject(value: unknown): value is Readonly<Record<string, unknown>> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function modalDecision(
  value: string | undefined,
): { readonly decision: ApprovalDecision; readonly scope: ApprovalScope } | undefined {
  switch (value) {
    case "Approve once":
      return { decision: "approve", scope: "once" };
    case "Approve for session":
      return { decision: "approve", scope: "session" };
    case "Always approve":
      return { decision: "approve", scope: "always" };
    case "Deny":
      return { decision: "deny", scope: "once" };
    case "Cancel turn":
      return { decision: "cancel", scope: "once" };
    default:
      return undefined;
  }
}

function approvalSummary(approval: ApprovalCard): string {
  const resources = approval.resources.length === 0
    ? "no declared resources"
    : approval.resources.join(", ");
  return `${approval.tool} requests approval for ${resources}`;
}

function isExpired(value: string): boolean {
  const timestamp = Date.parse(value);
  return !Number.isFinite(timestamp) || timestamp <= Date.now();
}

async function pickValue(
  title: string,
  values: readonly string[],
  current: string,
): Promise<string | undefined> {
  const selected = await vscode.window.showQuickPick(
    values.map((value) => ({
      label: value === ""
        ? "Default"
        : `${value[0]?.toUpperCase() ?? ""}${value.slice(1)}`,
      ...(value === current ? { description: "Current" } : {}),
      value,
    })),
    {
      title,
      placeHolder: "Select a Session Profile value",
      ignoreFocusOut: true,
    },
  );
  return selected?.value;
}
