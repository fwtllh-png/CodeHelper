import * as vscode from "vscode";

import { parseContextDirectives } from "../context/directives.js";
import {
  pickNativeContext,
  type NativeContextAttachment,
} from "../context/picker.js";
import type {
  ApprovalDecision,
  ApprovalScope,
} from "../runtime/session.js";
import type { SupervisorSnapshot } from "../runtime/supervisor.js";
import type { EditPlanPreview } from "../edits/preview.js";
import {
  decodeWebviewMessage,
  type ChatClientEvidence,
} from "./messages.js";
import {
  createChatErrorMessage,
  createChatPatchMessage,
  createChatRecoveryStatusMessage,
  createChatSnapshotMessage,
  type ChatHostMessage,
  type ChatSnapshotMessage,
} from "./contract.js";
import {
  ChatProjector,
  type ApprovalCard,
  type InputCard,
} from "./projector.js";
import { approvalDialogContent } from "./approval-summary.js";
import { groupToolsForPicker } from "./tool-groups.js";
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
import { turnIntentForMode } from "./turn-intent.js";
import type {
  SessionProfileSnapshot,
  SessionToolCatalog,
} from "../runtime/session.js";
import type { CredentialView } from "../security/credentials.js";
import {
  createStructuredSessionReceipt,
  renderSessionMarkdown,
  validateStructuredSessionReceipt,
} from "./export.js";

interface RootChatState {
  readonly projectors: Map<string, ChatProjector>;
  readonly composers: Map<string, SessionComposerState>;
  runtime: SupervisorSnapshot;
  revealTurnId: string | undefined;
  sessionSearchQuery: string;
  sessionSearch: {
    readonly query: string;
    readonly sessionIds: readonly string[];
    readonly matches: readonly {
      readonly sessionId: string;
      readonly turnId: string;
      readonly kind: string;
      readonly snippet?: string;
    }[];
  } | undefined;
}

interface SessionComposerState {
  readonly profile?: SessionProfileSnapshot;
  readonly catalog?: SessionToolCatalog;
  readonly credential?: CredentialView;
  readonly contexts?: readonly NativeContextAttachment[];
  readonly error?: string;
}

interface ProviderPickItem extends vscode.QuickPickItem {
  readonly providerId: string;
}

interface ModelPickItem extends vscode.QuickPickItem {
  readonly modelId: string;
  readonly selectionMode: string;
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
  readonly #submittedRecoveries = new Set<string>();
  readonly #mergePlans = new Map<string, EditPlanCard>();
  readonly #mergeOperations = new Set<string>();
  readonly #resources = new Map<string, ResourceReference>();
  readonly #composerLoads = new Set<string>();
  #view: vscode.WebviewView | undefined;
  #flushTimer: NodeJS.Timeout | undefined;
  #webviewReady = false;
  #clientEvidence: ChatClientEvidence | undefined;
  #snapshotPosts = 0;
  #patchPosts = 0;
  #projectionRevision = 0;
  #lastProjection: ChatSnapshotMessage | undefined;
  #deferredError: ChatHostMessage | undefined;
  readonly #deferredRecoveries = new Map<string, ChatHostMessage>();

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
    this.#clientEvidence = undefined;
    this.#projectionRevision = 0;
    this.#lastProjection = undefined;
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
          this.#clientEvidence = undefined;
        }
      }),
      view.onDidChangeVisibility(() => {
        if (view.visible) {
          this.#lastProjection = undefined;
          const deferredError = this.#deferredError;
          this.#deferredError = undefined;
          if (deferredError !== undefined) {
            this.#post(deferredError);
          }
          for (const recovery of this.#deferredRecoveries.values()) {
            this.#post(recovery);
          }
          this.#deferredRecoveries.clear();
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

  public get clientEvidence(): ChatClientEvidence | undefined {
    return this.#clientEvidence;
  }

  public get projectionDiagnostics(): {
    readonly visible: boolean;
    readonly snapshotPosts: number;
    readonly patchPosts: number;
  } {
    return {
      visible: this.#view?.visible ?? false,
      snapshotPosts: this.#snapshotPosts,
      patchPosts: this.#patchPosts,
    };
  }

  public invalidateProjection(): void {
    this.#scheduleFlush();
  }

  public receiveTestIntent(value: unknown): Promise<void> {
    return this.#receive(value);
  }

  public async approvePendingForTest(): Promise<boolean> {
    const root = this.#registry.selected;
    const session = this.#selectedSession(root);
    const approval = this.#projector(
      root.rootId,
      session.sessionId,
    ).pendingApprovals()[0];
    if (approval === undefined) return false;
    await this.#decide(root, session.sessionId, approval, "approve", "once");
    return true;
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
          this.#lastProjection = undefined;
          break;
        case "client-evidence":
          this.#clientEvidence = message;
          break;
        case "resync":
          this.#lastProjection = undefined;
          this.#scheduleFlush();
          break;
        case "open-resource": {
          const reference = this.#resources.get(message.resourceId);
          if (reference === undefined) {
            throw new Error("resource reference is unknown or stale");
          }
          await this.#resourceNavigator.open(reference);
          break;
        }
        case "resource-action": {
          const reference = this.#resources.get(message.resourceId);
          if (reference === undefined) {
            throw new Error("resource reference is unknown or stale");
          }
          let action = message.action;
          if (action === "menu") {
            const choice = await vscode.window.showQuickPick([
              { label: "$(go-to-file) Open", action: "open" as const },
              { label: "$(split-horizontal) Open to Side", action: "open-to-side" as const },
              { label: "$(copy) Copy Relative Path", action: "copy-relative-path" as const },
            ], {
              title: reference.path,
              placeHolder: "Choose a Resource action",
            });
            if (choice === undefined) break;
            action = choice.action;
          }
          if (action === "copy-relative-path") {
            await this.#resourceNavigator.copyRelativePath(reference);
          } else {
            await this.#resourceNavigator.open(reference, {
              side: action === "open-to-side",
            });
          }
          break;
        }
        case "select-root":
          await this.#registry.select(message.rootId);
          break;
        case "select-chat":
          await root.controller.selectChat(message.sessionId);
          this.#state(root.rootId).revealTurnId = message.turnId;
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
          const result = await root.controller.searchChats({
            query,
            includeArchived: true,
            limit: 1000,
          });
          if (state.sessionSearchQuery === query) {
            state.sessionSearch = {
              query,
              sessionIds: result.sessions.map((session) => session.sessionId),
              matches: result.matches.map((match) => ({
                sessionId: match.session_id,
                turnId: match.turn_id,
                kind: match.kind,
                ...(match.snippet === undefined
                  ? {}
                  : { snippet: match.snippet }),
              })),
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
          const destination = await vscode.window.showQuickPick([
            {
              label: "$(play) Current Session",
              description: "Use the active Session and Profile",
              value: "current" as const,
            },
            {
              label: "$(add) New Session",
              description: "Copy the Session Profile, then implement",
              value: "new" as const,
            },
            {
              label: "$(git-branch) Checkpoint Fork",
              description: "Fork state-only history before implementation",
              value: "fork" as const,
            },
          ], {
            title: "Plan Destination",
            placeHolder: "Choose where implementation starts",
          });
          if (destination === undefined) break;
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
          let targetSessionId = session.sessionId;
          let sourceSessionId: string | undefined;
          if (destination.value === "new") {
            const created = await root.controller.duplicateChat(
              session.sessionId,
            );
            targetSessionId = created.sessionId;
            sourceSessionId = session.sessionId;
          } else if (destination.value === "fork") {
            const checkpoints = await root.controller.checkpoints(
              session.sessionId,
            );
            const selected = await vscode.window.showQuickPick(
              checkpoints.checkpoints.filter((checkpoint) => checkpoint.can_fork)
                .map((checkpoint) => ({
                  label: checkpoint.summary,
                  description: new Date(
                    checkpoint.created_at,
                  ).toLocaleString(),
                  checkpoint,
                })),
              {
                title: "Plan Checkpoint Fork",
                placeHolder: "Choose a compatible Checkpoint",
              },
            );
            if (selected === undefined) break;
            await root.controller.forkCheckpoint(
              session.sessionId,
              selected.checkpoint.id,
              `${session.title} · Plan`,
            );
            sourceSessionId = session.sessionId;
          }
          await root.controller.implementPlan(
            targetSessionId,
            message.planId,
            message.action,
            sourceSessionId,
          );
          break;
        }
        case "turn-recovery": {
          const session = this.#selectedSession(root);
          const recoveryKey = sessionKey(
            root.rootId,
            session.sessionId,
            `recovery:${message.turnId}`,
          );
          if (this.#submittedRecoveries.has(recoveryKey)) {
            this.#post(createChatRecoveryStatusMessage(
              message.turnId,
              message.action,
              "failed",
              { message: "Recovery is already in progress" },
            ));
            break;
          }
          const turn = this.#projector(
            root.rootId,
            session.sessionId,
          ).snapshot().turns.find((candidate) => candidate.id === message.turnId);
          if (turn === undefined ||
            (turn.status !== "failed" && turn.status !== "canceled")) {
            this.#post(createChatRecoveryStatusMessage(
              message.turnId,
              message.action,
              "failed",
              { message: "Source Turn is unavailable or not recoverable" },
            ));
            break;
          }
          this.#submittedRecoveries.add(recoveryKey);
          let guidance: string | undefined;
          if (message.action === "continue") {
            guidance = await vscode.window.showInputBox({
              title: "Continue Turn",
              prompt: "Optional guidance for the new Turn",
              placeHolder: "Continue from current workspace state",
              validateInput: (value) => value.length > 64 << 10
                ? "Guidance must be at most 65536 characters"
                : null,
            });
            if (guidance === undefined) {
              this.#submittedRecoveries.delete(recoveryKey);
              this.#post(createChatRecoveryStatusMessage(
                turn.id,
                message.action,
                "canceled",
              ));
              break;
            }
          }
          try {
            const accepted = await root.controller.recoverTurn(
              session.sessionId,
              turn.id,
              message.action,
              guidance,
            );
            this.#post(createChatRecoveryStatusMessage(
              turn.id,
              message.action,
              "accepted",
              { newTurnId: accepted.turnId },
            ));
          } catch (error) {
            this.#post(createChatRecoveryStatusMessage(
              turn.id,
              message.action,
              "failed",
              {
                message: error instanceof Error
                  ? error.message
                  : String(error),
              },
            ));
          } finally {
            this.#submittedRecoveries.delete(recoveryKey);
          }
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
        case "add-context": {
          const session = this.#selectedSession(root);
          const state = this.#state(root.rootId).composers.get(session.sessionId);
          if (state?.profile === undefined) {
            throw new Error("Session Profile is unavailable");
          }
          const attachment = await pickNativeContext(
            root.folder,
            root.contextBridge,
            state.profile,
          );
          if (attachment === undefined) break;
          const contexts = state.contexts ?? [];
          if (contexts.length >= 8) {
            throw new Error("a turn can attach at most 8 context items");
          }
          if (contexts.some((candidate) =>
            candidate.reference.kind === attachment.reference.kind &&
            candidate.reference.digest === attachment.reference.digest)) {
            throw new Error("this context is already attached");
          }
          this.#state(root.rootId).composers.set(session.sessionId, {
            ...state,
            contexts: [...contexts, attachment],
          });
          this.#scheduleFlush();
          break;
        }
        case "remove-context": {
          const session = this.#selectedSession(root);
          const state = this.#state(root.rootId).composers.get(session.sessionId);
          if (state === undefined) break;
          this.#state(root.rootId).composers.set(session.sessionId, {
            ...state,
            contexts: (state.contexts ?? []).filter(
              (context) => context.id !== message.contextId,
            ),
          });
          this.#scheduleFlush();
          break;
        }
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
          const context = [
            ...(await root.contextBridge.capture(parsed.directives)),
            ...((this.#state(root.rootId).composers.get(session.sessionId)
              ?.contexts ?? []).map((attachment) => attachment.reference)),
          ];
          if (context.length > 8) {
            throw new Error("a turn can attach at most 8 context items");
          }
          await root.controller.submitPrompt(
            session.sessionId,
            parsed.prompt,
            context,
            turnIntentForMode(
              this.#state(root.rootId).composers.get(session.sessionId)
                ?.profile?.profile.mode,
            ),
          );
          const composer = this.#state(root.rootId).composers.get(session.sessionId);
          if (composer !== undefined) {
            this.#state(root.rootId).composers.set(session.sessionId, {
              ...composer,
              contexts: [],
            });
            this.#scheduleFlush();
          }
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
          if (this.#mergeOperations.has(key)) {
            throw new Error("Chat merge is already in progress");
          }
          this.#mergeOperations.add(key);
          try {
            if (message.planId === undefined) {
              const result = await vscode.window.withProgress(
                {
                  location: vscode.ProgressLocation.Notification,
                  title: "Preparing Chat merge preview...",
                },
                async () => root.controller.mergeChat(
                  session.sessionId, "preview",
                ),
              );
              const plan = decodeMergePlan(result);
              this.#mergePlans.set(key, plan);
              await this.#editPreview.showPatch(plan, root.rootId);
            } else {
              const plan = this.#mergePlans.get(key);
              if (plan?.id !== message.planId) {
                throw new Error(
                  "Chat merge plan is unknown or stale; review changes again",
                );
              }
              await vscode.window.withProgress(
                {
                  location: vscode.ProgressLocation.Notification,
                  title: `Applying ${String(plan.files.length)} Chat changes...`,
                },
                async () => root.controller.mergeChat(
                  session.sessionId, "apply", message.planId,
                ),
              );
              this.#mergePlans.delete(key);
              void vscode.window.showInformationMessage(
                `Applied ${String(plan.files.length)} Chat changes to the workspace.`,
              );
            }
          } catch (error) {
            const detail = error instanceof Error
              ? error.message
              : String(error);
            if (detail.includes("unknown or stale")) {
              this.#mergePlans.delete(key);
            }
            void vscode.window.showErrorMessage(
              `CodeHelper Chat merge failed: ${detail}`,
            );
            throw error;
          } finally {
            this.#mergeOperations.delete(key);
            this.#scheduleFlush();
          }
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
    action: "menu" | "rename" | "pin" | "unpin" | "archive" | "restore" |
      "delete" | "checkpoints" | "duplicate" | "open-to-side" | "export" |
      "reveal-approval",
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
          { label: "$(copy) Duplicate", action: "duplicate" as const },
          { label: "$(split-horizontal) Open to Side", action: "open-to-side" as const },
          { label: "$(export) Export", action: "export" as const },
          ...(session.pendingApprovals > 0
            ? [{
                label: "$(shield) Reveal Pending Approval",
                action: "reveal-approval" as const,
              }]
            : []),
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
      case "duplicate":
        await root.controller.duplicateChat(sessionId);
        break;
      case "open-to-side": {
        const markdown = renderSessionMarkdown(
          session,
          this.#projector(root.rootId, sessionId).snapshot(),
        );
        const document = await vscode.workspace.openTextDocument({
          language: "markdown",
          content: markdown,
        });
        await vscode.window.showTextDocument(document, {
          preview: true,
          viewColumn: vscode.ViewColumn.Beside,
        });
        break;
      }
      case "export": {
        const snapshot = this.#projector(root.rootId, sessionId).snapshot();
        const format = await vscode.window.showQuickPick([
          { label: "Markdown", extension: "md" as const },
          { label: "Structured Receipt", extension: "json" as const },
        ], {
          title: `Export · ${session.title}`,
          placeHolder: "Choose an export format",
        });
        if (format === undefined) break;
        const receipt = createStructuredSessionReceipt(session, snapshot);
        validateStructuredSessionReceipt(receipt);
        const content = format.extension === "md"
          ? renderSessionMarkdown(session, snapshot)
          : `${JSON.stringify(receipt, null, 2)}\n`;
        const destination = await vscode.window.showSaveDialog({
          title: `Export ${session.title}`,
          filters: format.extension === "md"
            ? { Markdown: ["md"] }
            : { JSON: ["json"] },
          defaultUri: vscode.Uri.joinPath(
            root.folder.uri,
            `${safeExportName(session.title)}.${format.extension}`,
          ),
        });
        if (destination !== undefined) {
          await vscode.workspace.fs.writeFile(
            destination,
            new TextEncoder().encode(content),
          );
        }
        break;
      }
      case "reveal-approval": {
        await root.controller.selectChat(sessionId);
        const approval = this.#projector(
          root.rootId,
          sessionId,
        ).pendingApprovals()[0];
        if (approval === undefined) {
          throw new Error("Session has no pending Approval");
        }
        this.#state(root.rootId).revealTurnId = approval.turnId;
        await this.#showApproval(root, sessionId, approval);
        break;
      }
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
      choices.push("Deny request", "Stop turn");
      const content = approvalDialogContent(approval);
      const selected = await vscode.window.showWarningMessage(
        `${root.label}: ${content.title}`,
        { modal: true, detail: content.detail },
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
            (composerState.contexts ?? []).map((context) => ({
              id: context.id, kind: context.kind, label: context.label,
            })),
          );
      const projection = createChatSnapshotMessage({
        revision: ++this.#projectionRevision,
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
        ...(state.revealTurnId === undefined
          ? {}
          : { revealTurnId: state.revealTurnId }),
        ...(state.sessionSearch === undefined
          ? {}
          : { sessionSearch: state.sessionSearch }),
        ...(mergePlan === undefined ? {} : { mergePlanId: mergePlan.id }),
        roots: this.#registry.roots.map((candidate) => ({
          id: candidate.rootId,
          label: candidate.label,
        })),
        ...(composer === undefined ? {} : { composer }),
      });
      const previous = this.#lastProjection;
      const selectedChanged = previous?.runtime.selectedRootId !==
          projection.runtime.selectedRootId ||
        previous.runtime.selectedSessionId !==
          projection.runtime.selectedSessionId;
      if (previous === undefined || selectedChanged) {
        this.#post(projection);
        this.#lastProjection = projection;
      } else {
        const patch = createChatPatchMessage(previous, projection);
        if (patch !== undefined) {
          this.#post(patch);
          this.#lastProjection = projection;
        }
      }
      state.revealTurnId = undefined;
    }, 16);
  }

  #post(value: ChatHostMessage): void {
    if (this.#view?.visible === true) {
      if (value.type === "snapshot") {
        this.#snapshotPosts++;
      } else if (value.type === "patch") {
        this.#patchPosts++;
      }
      void this.#view.webview.postMessage(value);
    } else if (value.type === "error") {
      this.#deferredError = value;
    } else if (value.type === "recovery-status") {
      this.#deferredRecoveries.set(value.turnId, value);
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
          revealTurnId: undefined,
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
        contexts: this.#state(root.rootId).composers.get(sessionId)?.contexts ?? [],
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
      const providers = await root.controller.providerCatalog();
      let provider = state.profile.profile.provider;
      if (control === "provider") {
        const providerItems: ProviderPickItem[] = [
          ...providers.providers.map((candidate) => ({
            label: candidate.display_name,
            description: candidate.selected ? "Current" : candidate.availability,
            ...(candidate.reason === undefined ? {} : { detail: candidate.reason }),
            providerId: candidate.id,
          })),
          {
            label: "$(gear) Configure another Provider",
            description: "Runs local Setup and restarts Runtime",
            providerId: "",
          },
        ];
        const picked = await vscode.window.showQuickPick<ProviderPickItem>(providerItems, {
          title: "CodeHelper: Provider Catalog",
          placeHolder: "Search Runtime-advertised Providers",
          ignoreFocusOut: true,
        });
        if (picked === undefined) return;
        if (picked.providerId === "") {
          await vscode.commands.executeCommand("codehelper.runSetup", root.rootId);
          this.#state(root.rootId).composers.delete(session.sessionId);
          this.#scheduleFlush();
          return;
        }
        provider = picked.providerId;
      }
      const models = await root.controller.modelCatalog(provider);
      const modelItems: ModelPickItem[] = [
        ...models.models.map((candidate) => ({
          label: candidate.capabilities.display_name,
          description: candidate.selected
            ? "Current"
            : candidate.capabilities.selection_mode === "restart_required"
              ? "Restart Required"
              : candidate.capabilities.availability,
          detail: modelCapabilityDetail(candidate.capabilities),
          modelId: candidate.id,
          selectionMode: candidate.capabilities.selection_mode,
        })),
        {
          label: "$(gear) Configure another Model",
          description: "Runs local Setup and restarts Runtime",
          detail: "",
          modelId: "",
          selectionMode: "restart_required",
        },
      ];
      const picked = await vscode.window.showQuickPick<ModelPickItem>(modelItems, {
        title: "CodeHelper: Model Catalog",
        placeHolder: "Search by Model name or capability",
        matchOnDescription: true,
        matchOnDetail: true,
        ignoreFocusOut: true,
      });
      if (picked === undefined) return;
      if (picked.modelId === "") {
        await vscode.commands.executeCommand(
          "codehelper.runSetup", root.rootId, provider,
        );
        this.#state(root.rootId).composers.delete(session.sessionId);
        this.#scheduleFlush();
        return;
      }
      if (provider !== state.profile.profile.provider ||
        picked.modelId !== state.profile.profile.model) {
        if (picked.selectionMode !== "hot") {
          await vscode.commands.executeCommand(
            "codehelper.runSetup", root.rootId, provider,
          );
          this.#state(root.rootId).composers.delete(session.sessionId);
          this.#scheduleFlush();
          return;
        }
        throw new Error("Runtime advertised hot selection without a mutable route");
      }
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
          contexts: state.contexts ?? [],
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
    let field: "mode" | "approval_posture";
    let value: string | undefined;
    if (control === "mode") {
      field = "mode";
      value = await pickValue(
        "CodeHelper: Agent Mode",
        ["plan", "act", "operate"],
        snapshot.profile.mode,
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
          : { approval_posture: value },
      );
      this.#state(root.rootId).composers.set(session.sessionId, {
        profile: { ...snapshot, profile: update.profile },
        catalog: state.catalog,
        credential: state.credential,
        contexts: state.contexts ?? [],
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

function modelCapabilityDetail(
  capabilities: SessionProfileSnapshot["capabilities"]["model_capabilities"],
): string {
  const features = [
    `${String(capabilities.context_window)} context`,
    `${String(capabilities.max_output_tokens)} max output`,
    capabilities.reasoning ? "reasoning" : undefined,
    capabilities.image_input || capabilities.vision ? "image input" : undefined,
    capabilities.tool_calls ? "tools" : undefined,
    capabilities.parallel_tool_calls === "supported" ? "parallel tools" : undefined,
  ].filter((value): value is string => value !== undefined);
  return features.join(" · ");
}

interface ToolPickItem extends vscode.QuickPickItem {
  readonly toolID?: string;
  readonly groupID?: string;
  readonly memberIDs?: readonly string[];
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
  const initialEnabled = new Set(
    catalog.tools.filter((tool) => tool.enabled).map((tool) => tool.id),
  );
  let enabled = new Set(initialEnabled);
  const expandedGroups = new Set<string>();
  const expandButton: vscode.QuickInputButton = {
    iconPath: new vscode.ThemeIcon("chevron-right"),
    tooltip: "Show operations",
  };
  const collapseButton: vscode.QuickInputButton = {
    iconPath: new vscode.ThemeIcon("chevron-down"),
    tooltip: "Hide operations",
  };
  let synchronizing = false;
  const render = (): void => {
    const items: ToolPickItem[] = [];
    for (const source of sourceOrder) {
      const tools = catalog.tools.filter((tool) => tool.source_kind === source);
      if (tools.length === 0) continue;
      items.push({
        label: source.toUpperCase(),
        kind: vscode.QuickPickItemKind.Separator,
      });
      for (const entry of groupToolsForPicker(tools)) {
        if (entry.kind === "tool") {
          items.push(toolPickItem(entry.tool, enabled.has(entry.tool.id)));
          continue;
        }
        const memberIDs = entry.group.tools.map((tool) => tool.id);
        const enabledCount = memberIDs.filter((id) => enabled.has(id)).length;
        const expanded = expandedGroups.has(entry.group.id);
        items.push({
          groupID: entry.group.id,
          memberIDs,
          label: entry.group.label,
          description: `${String(entry.group.tools.length)} operations · ` +
            `${entry.group.capabilityLabel} · ${String(enabledCount)} enabled`,
          detail: "Spawn and manage child agents. Expand for individual operations.",
          picked: enabledCount === memberIDs.length,
          alwaysShow: true,
          buttons: [expanded ? collapseButton : expandButton],
        });
        if (expanded) {
          for (const tool of entry.group.tools) {
            items.push({
              ...toolPickItem(tool, enabled.has(tool.id)),
              groupID: entry.group.id,
              label: `$(symbol-method) ${tool.name}`,
            });
          }
        }
      }
    }
    synchronizing = true;
    picker.items = items;
    picker.selectedItems = items.filter(
      (item) => isSelectableToolItem(item) && item.picked === true,
    );
    synchronizing = false;
  };
  render();
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
        enabled = button === allButton
          ? new Set(catalog.tools.map((tool) => tool.id))
          : new Set(initialEnabled);
        render();
      }),
      picker.onDidTriggerItemButton((event) => {
        if (event.item.groupID === undefined ||
          event.item.memberIDs === undefined) return;
        if (expandedGroups.has(event.item.groupID)) {
          expandedGroups.delete(event.item.groupID);
        } else {
          expandedGroups.add(event.item.groupID);
        }
        render();
      }),
      picker.onDidChangeSelection((selection) => {
        if (synchronizing) return;
        const selected = new Set(selection);
        const toggledGroups = new Set<string>();
        for (const item of picker.items) {
          if (item.groupID === undefined || item.memberIDs === undefined) continue;
          const wasAllEnabled = item.memberIDs.every((id) => enabled.has(id));
          const isSelected = selected.has(item);
          if (wasAllEnabled === isSelected) continue;
          for (const id of item.memberIDs) {
            if (isSelected) enabled.add(id);
            else enabled.delete(id);
          }
          toggledGroups.add(item.groupID);
        }
        for (const item of picker.items) {
          if (item.toolID === undefined ||
            (item.groupID !== undefined && toggledGroups.has(item.groupID))) {
            continue;
          }
          if (selected.has(item)) enabled.add(item.toolID);
          else enabled.delete(item.toolID);
        }
        render();
      }),
      picker.onDidAccept(() => {
        if (enabled.size === 0) {
          void vscode.window.showWarningMessage(
            "Select at least one tool; an empty Runtime allowlist means all tools.",
          );
          return;
        }
        accepted = true;
        const selected = [...enabled].sort();
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

function isSelectableToolItem(
  item: ToolPickItem,
): boolean {
  return typeof item.toolID === "string" || typeof item.groupID === "string";
}

function toolPickItem(
  tool: SessionToolCatalog["tools"][number],
  picked: boolean,
): ToolPickItem {
  const status = tool.availability === "available"
    ? tool.state
    : `${tool.availability}: ${tool.unavailable_reason ?? tool.state}`;
  return {
    toolID: tool.id,
    label: tool.availability === "unavailable"
      ? `$(warning) ${tool.name}`
      : tool.name,
    description: `${tool.source_label} · ${tool.capability}`,
    detail: `${tool.description} · ${status} · Guarded`,
    picked,
    alwaysShow: true,
  };
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
    case "Deny request":
      return { decision: "deny", scope: "once" };
    case "Stop turn":
      return { decision: "cancel", scope: "once" };
    default:
      return undefined;
  }
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

function safeExportName(value: string): string {
  const safe = value.trim()
    .replaceAll(/[^a-zA-Z0-9._-]+/gu, "-")
    .replaceAll(/^-+|-+$/gu, "");
  return safe.slice(0, 96) || "codehelper-session";
}
