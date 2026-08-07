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

interface RootChatState {
  readonly projectors: Map<string, ChatProjector>;
  runtime: SupervisorSnapshot;
}

export class ChatViewProvider implements vscode.WebviewViewProvider, vscode.Disposable {
  readonly #registry: WorkspaceRuntimeRegistry;
  readonly #extensionUri: vscode.Uri;
  readonly #roots = new Map<string, RootChatState>();
  readonly #editPreview: EditPlanPreview;
  readonly #subscriptions: vscode.Disposable[];
  readonly #modalApprovals = new Set<string>();
  readonly #modalInputs = new Set<string>();
  readonly #submittedApprovals = new Set<string>();
  readonly #mergePlans = new Map<string, EditPlanCard>();
  #view: vscode.WebviewView | undefined;
  #flushTimer: NodeJS.Timeout | undefined;
  #webviewReady = false;

  public constructor(
    registry: WorkspaceRuntimeRegistry,
    editPreview: EditPlanPreview,
    extensionUri: vscode.Uri,
  ) {
    this.#registry = registry;
    this.#editPreview = editPreview;
    this.#extensionUri = extensionUri;
    this.#syncRoots();
    this.#subscriptions = [
      registry.onEvent(({ root, sessionId, event, replayed }) => {
        this.#onEvent(root, sessionId, event, replayed);
      }),
      registry.onStateChange(({ root, snapshot }) => {
        this.#state(root.rootId).runtime = snapshot;
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
        case "select-root":
          await this.#registry.select(message.rootId);
          break;
        case "select-chat":
          await root.controller.selectChat(message.sessionId);
          break;
        case "new-chat":
          await root.controller.createChat();
          break;
        case "repair-runtime":
          await vscode.commands.executeCommand("codehelper.repairRuntime");
          break;
        case "run-setup":
          await vscode.commands.executeCommand("codehelper.runSetup");
          break;
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
    if (this.#flushTimer !== undefined) {
      return;
    }
    this.#flushTimer = setTimeout(() => {
      this.#flushTimer = undefined;
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
      const mergePlan = selected === undefined
        ? undefined
        : this.#mergePlans.get(
            sessionKey(root.rootId, selected.sessionId, "merge"),
          );
      this.#post(createChatSnapshotMessage({
        snapshot: projector.snapshot(),
        state: state.runtime.state,
        ...(state.runtime.error === undefined
          ? {}
          : { error: state.runtime.error }),
        trusted: vscode.workspace.isTrusted,
        selectedRootId: root.rootId,
        selectedRootLabel: root.label,
        sessions,
        ...(mergePlan === undefined ? {} : { mergePlanId: mergePlan.id }),
        roots: this.#registry.roots.map((candidate) => ({
          id: candidate.rootId,
          label: candidate.label,
        })),
      }));
    }, 16);
  }

  #post(value: ChatHostMessage): void {
    void this.#view?.webview.postMessage(value);
  }

  #syncRoots(): void {
    const live = new Set(this.#registry.roots.map((root) => root.rootId));
    for (const root of this.#registry.roots) {
      if (!this.#roots.has(root.rootId)) {
        this.#roots.set(root.rootId, {
          projectors: new Map(),
          runtime: root.controller.snapshot,
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
