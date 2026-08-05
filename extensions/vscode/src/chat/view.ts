import { randomBytes } from "node:crypto";

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

interface RootChatState {
  readonly projectors: Map<string, ChatProjector>;
  runtime: SupervisorSnapshot;
}

export class ChatViewProvider implements vscode.WebviewViewProvider, vscode.Disposable {
  readonly #registry: WorkspaceRuntimeRegistry;
  readonly #roots = new Map<string, RootChatState>();
  readonly #editPreview: EditPlanPreview;
  readonly #subscriptions: vscode.Disposable[];
  readonly #modalApprovals = new Set<string>();
  readonly #modalInputs = new Set<string>();
  readonly #submittedApprovals = new Set<string>();
  readonly #mergePlans = new Map<string, EditPlanCard>();
  #view: vscode.WebviewView | undefined;
  #flushTimer: NodeJS.Timeout | undefined;

  public constructor(
    registry: WorkspaceRuntimeRegistry,
    editPreview: EditPlanPreview,
  ) {
    this.#registry = registry;
    this.#editPreview = editPreview;
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
    view.webview.options = {
      enableScripts: true,
      localResourceRoots: [],
    };
    view.webview.html = renderHTML();
    this.#subscriptions.push(
      view.webview.onDidReceiveMessage((value: unknown) => {
        void this.#receive(value);
      }),
      view.onDidDispose(() => {
        if (this.#view === view) {
          this.#view = undefined;
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
        case "select-root":
          await this.#registry.select(message.rootId);
          break;
        case "select-chat":
          await root.controller.selectChat(message.sessionId);
          break;
        case "new-chat":
          await root.controller.createChat();
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
      this.#post({
        type: "error",
        message: error instanceof Error ? error.message : String(error),
      });
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
      this.#post({
        type: "error",
        message: error instanceof Error ? error.message : String(error),
      });
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
      this.#post({
        type: "error",
        message: error instanceof Error ? error.message : String(error),
      });
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
      this.#post({
        type: "snapshot",
        snapshot: projector.snapshot(),
        runtime: {
          state: state.runtime.state,
          trusted: vscode.workspace.isTrusted,
          selectedRootId: root.rootId,
          selectedRootLabel: root.label,
          selectedSessionId: selected?.sessionId,
          sessions,
          mergePlanId: mergePlan?.id,
          roots: this.#registry.roots.map((candidate) => ({
            id: candidate.rootId,
            label: candidate.label,
          })),
        },
      });
    }, 16);
  }

  #post(value: unknown): void {
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

function renderHTML(): string {
  const nonce = randomBytes(24).toString("base64");
  const csp = [
    "default-src 'none'",
    `style-src 'nonce-${nonce}'`,
    `script-src 'nonce-${nonce}'`,
    "img-src data:",
  ].join("; ");
  return `<!doctype html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <meta http-equiv="Content-Security-Policy" content="${csp}">
  <style nonce="${nonce}">
    :root { color-scheme: light dark; }
    body { margin: 0; color: var(--vscode-foreground); background: var(--vscode-sideBar-background); font: var(--vscode-font-size)/1.45 var(--vscode-font-family); }
    #status { position: sticky; top: 0; z-index: 2; padding: 6px 10px; border-bottom: 1px solid var(--vscode-panel-border); background: var(--vscode-sideBar-background); color: var(--vscode-descriptionForeground); }
    #root, #chat { max-width: 42%; margin-right: 6px; color: var(--vscode-dropdown-foreground); background: var(--vscode-dropdown-background); border: 1px solid var(--vscode-dropdown-border); }
    #status button { padding: 2px 6px; }
    #turns { padding: 8px 10px 150px; }
    article { border-bottom: 1px solid var(--vscode-panel-border); padding: 10px 0; }
    .user { white-space: pre-wrap; font-weight: 600; }
    .assistant { margin: 6px 0 8px; }
    .meta { color: var(--vscode-descriptionForeground); }
    .section-label { margin-top: 10px; color: var(--vscode-descriptionForeground); font-size: 0.9em; font-weight: 600; text-transform: uppercase; letter-spacing: 0.04em; }
    details.reasoning-panel { border-left: 3px solid var(--vscode-textLink-foreground); color: var(--vscode-descriptionForeground); }
    details.reasoning-panel > summary { cursor: pointer; font-weight: 600; }
    .reasoning-body { margin-top: 6px; }
    .markdown { overflow-wrap: anywhere; }
    .markdown > :first-child { margin-top: 0; }
    .markdown > :last-child { margin-bottom: 0; }
    .markdown p { margin: 0.55em 0; }
    .markdown h1, .markdown h2, .markdown h3, .markdown h4, .markdown h5, .markdown h6 { margin: 0.9em 0 0.4em; line-height: 1.25; }
    .markdown h1 { font-size: 1.45em; }
    .markdown h2 { font-size: 1.3em; }
    .markdown h3 { font-size: 1.15em; }
    .markdown ul, .markdown ol { margin: 0.5em 0; padding-left: 1.8em; }
    .markdown blockquote { margin: 0.6em 0; padding-left: 0.8em; border-left: 3px solid var(--vscode-panel-border); color: var(--vscode-descriptionForeground); }
    .markdown code { padding: 0.1em 0.3em; border-radius: 3px; font-family: var(--vscode-editor-font-family); background: var(--vscode-textCodeBlock-background); }
    .markdown pre { padding: 8px; overflow: auto; white-space: pre; background: var(--vscode-textCodeBlock-background); border: 1px solid var(--vscode-panel-border); border-radius: 4px; }
    .markdown pre code { padding: 0; background: transparent; }
    .markdown table { display: block; max-width: 100%; overflow-x: auto; border-collapse: collapse; }
    .markdown th, .markdown td { padding: 4px 8px; border: 1px solid var(--vscode-panel-border); text-align: left; }
    .markdown a { color: var(--vscode-textLink-foreground); }
    .markdown hr { border: 0; border-top: 1px solid var(--vscode-panel-border); }
    details { margin: 6px 0; padding: 4px 6px; background: var(--vscode-textCodeBlock-background); border-radius: 3px; }
    details.context-receipt { display: inline-block; max-width: 100%; margin-right: 5px; border: 1px solid var(--vscode-panel-border); overflow-wrap: anywhere; }
    details.context-receipt[open] { display: block; }
    pre { white-space: pre-wrap; overflow-wrap: anywhere; }
    .error { color: var(--vscode-errorForeground); white-space: pre-wrap; }
    button { margin: 3px 4px 3px 0; color: var(--vscode-button-foreground); background: var(--vscode-button-background); border: 0; padding: 5px 8px; cursor: pointer; }
    button.secondary { color: var(--vscode-button-secondaryForeground); background: var(--vscode-button-secondaryBackground); }
    #composer { position: fixed; bottom: 0; left: 0; right: 0; padding: 8px; border-top: 1px solid var(--vscode-panel-border); background: var(--vscode-sideBar-background); }
    textarea { box-sizing: border-box; width: 100%; min-height: 64px; resize: vertical; color: var(--vscode-input-foreground); background: var(--vscode-input-background); border: 1px solid var(--vscode-input-border); padding: 6px; }
    .hint { color: var(--vscode-descriptionForeground); font-size: 0.9em; }
  </style>
</head>
<body>
  <div id="status">
    <select id="root" aria-label="Workspace root"></select>
    <select id="chat" aria-label="Chat session"></select>
    <button type="button" id="new-chat" title="Create isolated Chat">New</button>
    <button type="button" id="merge-chat" title="Review Chat changes">Merge</button>
    <span id="runtime">CodeHelper Runtime: starting</span>
  </div>
  <main id="turns"></main>
  <form id="composer">
    <textarea id="prompt" aria-label="Prompt" placeholder="Ask CodeHelper. Attach @file, @selection, @symbol, or @diagnostics."></textarea>
    <div>
      <button type="submit">Send</button>
      <button type="button" class="secondary" id="stop">Stop</button>
    </div>
    <div class="hint">Editor context is explicit and must come from the saved active file.</div>
  </form>
  <script nonce="${nonce}">
    const vscode = acquireVsCodeApi();
    const root = document.getElementById('root');
    const chat = document.getElementById('chat');
    const runtime = document.getElementById('runtime');
    const turns = document.getElementById('turns');
    const prompt = document.getElementById('prompt');
    let trusted = false;
    document.getElementById('composer').addEventListener('submit', event => {
      event.preventDefault();
      vscode.postMessage({ type: 'submit', text: prompt.value });
      prompt.value = '';
    });
    document.getElementById('stop').addEventListener('click', () => {
      vscode.postMessage({ type: 'stop' });
    });
    root.addEventListener('change', () => {
      vscode.postMessage({ type: 'select-root', rootId: root.value });
    });
    chat.addEventListener('change', () => {
      vscode.postMessage({ type: 'select-chat', sessionId: chat.value });
    });
    document.getElementById('new-chat').addEventListener('click', () => {
      vscode.postMessage({ type: 'new-chat' });
    });
    document.getElementById('merge-chat').addEventListener('click', () => {
      vscode.postMessage(messageMergePlanId ?
        { type: 'merge-chat', planId: messageMergePlanId } :
        { type: 'merge-chat' });
    });
    let messageMergePlanId;
    window.addEventListener('message', event => {
      const message = event.data;
      if (message.type === 'error') {
        const node = document.createElement('div');
        node.className = 'error';
        node.textContent = message.message;
        turns.append(node);
        return;
      }
      if (message.type !== 'snapshot') return;
      trusted = message.runtime.trusted;
      root.replaceChildren(...message.runtime.roots.map(candidate => {
        const option = document.createElement('option');
        option.value = candidate.id;
        option.textContent = candidate.label;
        option.selected = candidate.id === message.runtime.selectedRootId;
        return option;
      }));
      root.hidden = message.runtime.roots.length < 2;
      chat.replaceChildren(...message.runtime.sessions.map(candidate => {
        const option = document.createElement('option');
        option.value = candidate.sessionId;
        option.textContent = (candidate.active ? '● ' : '') + candidate.title;
        option.selected = candidate.sessionId === message.runtime.selectedSessionId;
        return option;
      }));
      messageMergePlanId = message.runtime.mergePlanId;
      const mergeButton = document.getElementById('merge-chat');
      mergeButton.textContent =
        messageMergePlanId ? 'Apply' : 'Merge';
      mergeButton.disabled = Boolean(messageMergePlanId) && !trusted;
      runtime.textContent = 'CodeHelper Runtime: ' + message.runtime.state +
        (trusted ? ' · trusted' : ' · read-only') +
        ' · ' + message.runtime.sessions.length + ' chats';
      render(message.snapshot);
    });
    function render(snapshot) {
      const fragment = document.createDocumentFragment();
      for (const turn of snapshot.turns) {
        const article = document.createElement('article');
        appendText(article, 'div', 'user', turn.user || '(restored turn)');
        appendText(article, 'div', 'meta', turn.status);
        if (turn.reasoning) {
          const reasoning = document.createElement('details');
          reasoning.className = 'reasoning-panel';
          reasoning.open = turn.reasoningActive;
          appendText(reasoning, 'summary', '', turn.reasoningActive ?
            '推理过程 · 生成中' : '推理过程');
          appendMarkdown(reasoning, turn.reasoningMarkdown, 'reasoning-body');
          article.append(reasoning);
        }
        if (turn.output) {
          appendText(article, 'div', 'section-label', '最终结论');
          appendMarkdown(article, turn.outputMarkdown, 'assistant');
        }
        for (const receipt of turn.contextReceipts) {
          article.append(contextReceiptCard(receipt));
        }
        for (const tool of turn.tools) {
          const details = document.createElement('details');
          appendText(details, 'summary', '', tool.tool + ' · ' + tool.status);
          if (tool.arguments) appendText(details, 'pre', '', tool.arguments);
          if (tool.output) appendText(details, 'pre', '', tool.output);
          article.append(details);
        }
        for (const approval of turn.approvals) article.append(approvalCard(approval, trusted));
        for (const input of turn.inputs) article.append(inputCard(input));
        for (const diagnostic of turn.diagnostics) appendText(article, 'div', 'meta', diagnostic);
        if (turn.verification) appendText(article, 'div', 'meta', 'Verify: ' + turn.verification);
        if (turn.receipt) appendText(article, 'div', 'meta', turn.receipt);
        if (turn.error) appendText(article, 'div', 'error', turn.error);
        for (const unknown of turn.unknownEvents) {
          const details = document.createElement('details');
          appendText(details, 'summary', '', 'Unknown event');
          appendText(details, 'pre', '', unknown);
          article.append(details);
        }
        fragment.append(article);
      }
      turns.replaceChildren(fragment);
      window.scrollTo(0, document.body.scrollHeight);
    }
    function contextReceiptCard(receipt) {
      const card = document.createElement('details');
      card.className = 'context-receipt';
      let label = 'Context: ' + receipt.kind + ' · ' + receipt.path;
      if (receipt.symbol) label += ' · ' + receipt.symbol;
      if (receipt.diagnosticCount) {
        label += ' · ' + receipt.diagnosticCount + ' diagnostics';
      }
      appendText(card, 'summary', '', label);
      appendText(card, 'div', 'meta', 'Source: ' + (receipt.source || 'legacy'));
      appendText(card, 'div', 'meta', 'SHA-256: ' + receipt.digest);
      if (receipt.range) appendText(card, 'div', 'meta', 'Range: ' + receipt.range);
      let bytes = 'Bytes: ' + receipt.retainedBytes + '/' + receipt.originalBytes;
      if (receipt.truncated) bytes += ' · truncated';
      appendText(card, 'div', 'meta', bytes);
      if (receipt.omittedDiagnostics) {
        appendText(card, 'div', 'meta',
          'Omitted diagnostics: ' + receipt.omittedDiagnostics);
      }
      return card;
    }
    function approvalCard(approval, workspaceTrusted) {
      const box = document.createElement('details');
      box.open = !approval.resolved;
      appendText(box, 'summary', '', 'Approval: ' + approval.tool +
        (approval.resolved ? ' · ' + approval.resolved : ''));
      appendText(box, 'pre', '', approval.arguments);
      appendText(box, 'div', 'meta', approval.resources.join(', '));
      if (approval.reason) appendText(box, 'div', 'meta', approval.reason);
      if (!approval.resolved && Date.parse(approval.expiresAt) > Date.now()) {
        if (approval.editPlan) {
          box.append(actionButton('Preview diff', () =>
            vscode.postMessage({ type: 'preview', requestId: approval.requestId })));
        }
        if (workspaceTrusted) {
          for (const scope of approval.allowedScopes) {
            const button = actionButton('Approve ' + scope, () =>
              vscode.postMessage({ type: 'approval', requestId: approval.requestId,
                decision: 'approve', scope, planId: approval.editPlan && approval.editPlan.id }));
            box.append(button);
          }
        }
        box.append(actionButton('Deny', () =>
          vscode.postMessage({ type: 'approval', requestId: approval.requestId, decision: 'deny', scope: 'once' })));
        box.append(actionButton('Cancel turn', () =>
          vscode.postMessage({ type: 'approval', requestId: approval.requestId, decision: 'cancel', scope: 'once' })));
      }
      return box;
    }
    function inputCard(input) {
      const box = document.createElement('details');
      box.open = !input.resolved;
      appendText(box, 'summary', '', 'Input required');
      appendText(box, 'div', '', input.prompt);
      if (!input.resolved) {
        for (const option of input.options) {
          box.append(actionButton(option, () =>
            vscode.postMessage({ type: 'input', requestId: input.requestId, answer: option })));
        }
      }
      return box;
    }
    function actionButton(label, handler) {
      const button = document.createElement('button');
      button.type = 'button';
      button.textContent = label;
      button.addEventListener('click', handler);
      return button;
    }
    const markdownTags = new Set([
      'a', 'blockquote', 'br', 'code', 'del', 'em',
      'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'hr',
      'li', 'ol', 'p', 'pre', 'span', 'strong',
      'table', 'tbody', 'td', 'th', 'thead', 'tr', 'ul'
    ]);
    function appendMarkdown(parent, nodes, className) {
      const container = document.createElement('div');
      container.className = 'markdown ' + className;
      for (const node of nodes) container.append(markdownNode(node));
      parent.append(container);
    }
    function markdownNode(value) {
      if (!value || typeof value !== 'object') {
        return document.createTextNode('');
      }
      if (value.kind === 'text') {
        return document.createTextNode(typeof value.text === 'string' ? value.text : '');
      }
      if (value.kind !== 'element' || !markdownTags.has(value.tag)) {
        return document.createTextNode('');
      }
      const node = document.createElement(value.tag);
      if (value.tag === 'a' && typeof value.href === 'string' &&
        /^(https?:|mailto:)/.test(value.href)) {
        node.setAttribute('href', value.href);
        node.setAttribute('target', '_blank');
        node.setAttribute('rel', 'noreferrer noopener');
      }
      if (value.tag === 'ol' && Number.isSafeInteger(value.start) &&
        value.start > 1 && value.start <= 1000000) {
        node.setAttribute('start', String(value.start));
      }
      if (value.tag === 'code' && typeof value.language === 'string' &&
        /^[\\w+.-]{1,64}$/.test(value.language)) {
        node.className = 'language-' + value.language;
      }
      if (Array.isArray(value.children)) {
        for (const child of value.children) node.append(markdownNode(child));
      }
      return node;
    }
    function appendText(parent, tag, className, text) {
      const node = document.createElement(tag);
      if (className) node.className = className;
      node.textContent = text;
      parent.append(node);
    }
  </script>
</body>
</html>`;
}
