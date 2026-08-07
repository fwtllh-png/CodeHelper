/// <reference lib="dom" />

import type {
  ChatErrorMessage,
  ChatHostMessage,
  ChatSessionView,
  ChatSnapshotMessage,
} from "../contract.js";
import {
  filterChatSessions,
  groupChatSessions,
  sessionStatusLabel,
  type SessionStatusFilter,
} from "../session-list.js";
import { element } from "./dom.js";
import {
  renderTranscript,
  type TranscriptActions,
} from "./transcript.js";
import {
  computeVirtualWindow,
  virtualItemOffset,
} from "../virtual-list.js";
import { routeChatKeyboard } from "../keyboard.js";

interface VSCodeAPI {
  postMessage(message: unknown): void;
}

declare function acquireVsCodeApi(): VSCodeAPI;

const vscode = acquireVsCodeApi();
const root = element("root") as HTMLSelectElement;
const chatTitle = element("chat-title");
const sessionList = element("session-list");
const sessionSearch = element("session-search") as HTMLInputElement;
const sessionFilter = element("session-filter") as HTMLSelectElement;
const sessionCount = element("session-count");
const sessionRail = element("session-rail");
const sessionScrim = element("session-scrim") as HTMLButtonElement;
const toggleSessions = element("toggle-sessions") as HTMLButtonElement;
const runtime = element("runtime");
const journeyState = element("journey-state");
const repair = element("repair");
const repairDetail = element("repair-detail");
const empty = element("empty");
const turns = element("turns");
const prompt = element("prompt") as HTMLTextAreaElement;
const composerContexts = element("composer-contexts");
const send = element("send") as HTMLButtonElement;
const stop = element("stop") as HTMLButtonElement;
const newChat = element("new-chat") as HTMLButtonElement;
const mergeChat = element("merge-chat") as HTMLButtonElement;
const railNewChat = element("rail-new-chat") as HTMLButtonElement;
const environment = element("environment");
const approvalPosture = element("approval-posture");
const modeControl = element("mode-control") as HTMLButtonElement;
const providerControl = element("provider-control") as HTMLButtonElement;
const modelControl = element("model-control") as HTMLButtonElement;
const thinkingControl = element("thinking-control") as HTMLButtonElement;
const toolsControl = element("tools-control") as HTMLButtonElement;
const credentialControl = element("credential-control") as HTMLButtonElement;
const approvalControl = element("approval-control") as HTMLButtonElement;
const narrowMedia = window.matchMedia("(max-width: 720px)");
let trusted = false;
let messageMergePlanId: string | undefined;
let sessions: readonly ChatSessionView[] = [];
let sessionSearchProjection: ChatSnapshotMessage["runtime"]["sessionSearch"];
let sessionSearchTimer: ReturnType<typeof setTimeout> | undefined;
let sessionRenderFrame: number | undefined;

type SessionVirtualItem =
  | {
      readonly kind: "heading";
      readonly key: string;
      readonly label: string;
      readonly height: 24;
    }
  | {
      readonly kind: "session";
      readonly key: string;
      readonly session: ChatSessionView;
      readonly position: number;
      readonly setSize: number;
      readonly height: 52;
    };

let sessionVirtualItems: readonly SessionVirtualItem[] = [];

const transcriptActions: TranscriptActions = {
  openResource: (resourceId) => {
    post({ type: "open-resource", resourceId });
  },
  preview: (requestId) => {
    post({ type: "preview", requestId });
  },
  approve: (requestId, scope, planId) => {
    post({
      type: "approval",
      requestId,
      decision: "approve",
      scope,
      ...(planId === undefined ? {} : { planId }),
    });
  },
  deny: (requestId) => {
    post({
      type: "approval",
      requestId,
      decision: "deny",
      scope: "once",
    });
  },
  cancel: (requestId) => {
    post({
      type: "approval",
      requestId,
      decision: "cancel",
      scope: "once",
    });
  },
  answer: (requestId, answer) => {
    post({ type: "input", requestId, answer });
  },
  plan: (planId, action) => {
    post({ type: "plan-action", planId, action });
  },
};

(element("composer") as HTMLFormElement).addEventListener("submit", (event) => {
  event.preventDefault();
  if (prompt.value.trim().length === 0) return;
  post({ type: "submit", text: prompt.value });
  prompt.value = "";
});

prompt.addEventListener("keydown", (event) => {
  const action = keyboardAction(event);
  if (action !== "none") event.preventDefault();
  if (action === "new-chat") {
    post({ type: "new-chat" });
  } else if (action === "send") {
    send.click();
  } else if (action === "stop") {
    stop.click();
  }
});

stop.addEventListener("click", () => {
  post({ type: "stop" });
});
element("repair-runtime").addEventListener("click", () => {
  post({ type: "repair-runtime" });
});
element("run-setup").addEventListener("click", () => {
  post({ type: "run-setup" });
});
root.addEventListener("change", () => {
  post({ type: "select-root", rootId: root.value });
});
newChat.addEventListener("click", () => {
  post({ type: "new-chat" });
});
railNewChat.addEventListener("click", () => {
  post({ type: "new-chat" });
});
mergeChat.addEventListener("click", () => {
  post(messageMergePlanId === undefined
    ? { type: "merge-chat" }
    : { type: "merge-chat", planId: messageMergePlanId });
});
element("add-context").addEventListener("click", () => {
  post({ type: "add-context" });
});
composerContexts.addEventListener("click", (event) => {
  const target = event.target;
  if (!(target instanceof HTMLButtonElement)) return;
  const contextId = target.dataset["contextId"];
  if (contextId !== undefined) {
    post({ type: "remove-context", contextId });
  }
});
for (const [button, control] of [
  [modeControl, "mode"],
  [providerControl, "provider"],
  [modelControl, "model"],
  [thinkingControl, "thinking"],
  [toolsControl, "tools"],
  [credentialControl, "credential"],
  [approvalControl, "approval"],
] as const) {
  button.addEventListener("click", () => {
    post({ type: "configure-composer", control });
  });
}
toggleSessions.addEventListener("click", () => {
  setSessionsOpen(!document.body.classList.contains("sessions-open"));
});
element("close-sessions").addEventListener("click", () => {
  setSessionsOpen(false);
});
sessionScrim.addEventListener("click", () => {
  setSessionsOpen(false);
});
narrowMedia.addEventListener("change", () => {
  setSessionsOpen(document.body.classList.contains("sessions-open"));
});
document.addEventListener("keydown", (event) => {
  if (event.key === "Escape" &&
    keyboardAction(event) === "close-sessions") {
    event.preventDefault();
    event.stopPropagation();
    setSessionsOpen(false);
    toggleSessions.focus();
    return;
  }
  if (event.key !== "Tab" || !isNarrow() ||
    !document.body.classList.contains("sessions-open")) {
    return;
  }
  const focusable = [...sessionRail.querySelectorAll<HTMLElement>(
    "button:not(:disabled), input:not(:disabled), select:not(:disabled), " +
      "[tabindex]:not([tabindex='-1'])",
  )].filter((candidate) => !candidate.hasAttribute("hidden"));
  const first = focusable[0];
  const last = focusable.at(-1);
  if (first === undefined || last === undefined) return;
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault();
    last.focus();
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault();
    first.focus();
  }
}, { capture: true });

function keyboardAction(event: KeyboardEvent) {
  return routeChatKeyboard({
    key: event.key,
    ctrlKey: event.ctrlKey,
    metaKey: event.metaKey,
    isComposing: event.isComposing,
    sessionsOpen: document.body.classList.contains("sessions-open"),
    turnActive: !stop.disabled,
  });
}
sessionSearch.addEventListener("input", () => {
  renderSessionList();
  if (sessionSearchTimer !== undefined) clearTimeout(sessionSearchTimer);
  sessionSearchTimer = setTimeout(() => {
    sessionSearchTimer = undefined;
    post({ type: "search-chats", query: sessionSearch.value });
  }, 150);
});
sessionFilter.addEventListener("change", () => {
  renderSessionList();
});
sessionList.addEventListener("scroll", () => {
  scheduleVirtualSessionRender();
}, { passive: true });
new ResizeObserver(() => {
  scheduleVirtualSessionRender();
}).observe(sessionList);

function scheduleVirtualSessionRender(): void {
  if (sessionRenderFrame !== undefined) return;
  sessionRenderFrame = requestAnimationFrame(() => {
    sessionRenderFrame = undefined;
    renderVirtualSessionWindow();
  });
}

sessionList.addEventListener("keydown", (event) => {
  if (!(event instanceof KeyboardEvent) ||
    !["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) {
    return;
  }
  const indices = sessionVirtualItems.flatMap((item, index) =>
    item.kind === "session" && !item.session.archived ? [index] : []);
  if (indices.length === 0) return;
  const active = document.activeElement instanceof Element
    ? document.activeElement.closest<HTMLElement>(".session-row")
    : null;
  const currentIndex = Number(active?.dataset["virtualIndex"] ?? "-1");
  const currentPosition = indices.indexOf(currentIndex);
  let targetPosition = currentPosition;
  if (event.key === "Home") targetPosition = 0;
  if (event.key === "End") targetPosition = indices.length - 1;
  if (event.key === "ArrowDown") {
    targetPosition = Math.min(
      indices.length - 1,
      currentPosition < 0 ? 0 : currentPosition + 1,
    );
  }
  if (event.key === "ArrowUp") {
    targetPosition = Math.max(
      0,
      currentPosition < 0 ? indices.length - 1 : currentPosition - 1,
    );
  }
  const targetIndex = indices[targetPosition];
  if (targetIndex === undefined) return;
  event.preventDefault();
  const top = virtualItemOffset(sessionVirtualItems, targetIndex);
  const height = sessionVirtualItems[targetIndex]?.height ?? 0;
  if (top < sessionList.scrollTop) {
    sessionList.scrollTop = top;
  } else if (top + height >
    sessionList.scrollTop + sessionList.clientHeight) {
    sessionList.scrollTop = top + height - sessionList.clientHeight;
  }
  renderVirtualSessionWindow();
  requestAnimationFrame(() => {
    sessionList.querySelector<HTMLButtonElement>(
      `.session-row[data-virtual-index="${String(targetIndex)}"] ` +
        ".session-item",
    )?.focus();
  });
});

window.addEventListener("message", (event: MessageEvent<unknown>) => {
  const message = event.data;
  if (!isChatHostMessage(message)) return;
  if (message.type === "error") {
    renderError(message);
    return;
  }
  renderSnapshot(message);
});

function renderError(message: ChatErrorMessage): void {
  const node = document.createElement("div");
  node.className = "error";
  node.textContent = message.message;
  turns.append(node);
}

function renderSnapshot(message: ChatSnapshotMessage): void {
  trusted = message.runtime.trusted;
  root.replaceChildren(...message.runtime.roots.map((candidate) => {
    const option = document.createElement("option");
    option.value = candidate.id;
    option.textContent = candidate.label;
    option.selected = candidate.id === message.runtime.selectedRootId;
    return option;
  }));
  root.hidden = message.runtime.roots.length < 2;
  sessions = message.runtime.sessions;
  sessionSearchProjection = message.runtime.sessionSearch;
  renderSessionList();
  const selected = sessions.find((candidate) => candidate.selected);
  chatTitle.textContent = selected?.title ?? "CodeHelper";
  messageMergePlanId = message.runtime.mergePlanId;
  mergeChat.textContent = messageMergePlanId === undefined ? "Merge" : "Apply";
  mergeChat.disabled = !message.presentation.runtimeReady ||
    (messageMergePlanId !== undefined && !trusted);
  prompt.disabled = !message.presentation.promptEnabled;
  send.disabled = !message.presentation.sendEnabled;
  stop.disabled = !message.presentation.stopEnabled;
  newChat.disabled = !message.presentation.newChatEnabled;
  railNewChat.disabled = !message.presentation.newChatEnabled;
  environment.textContent = message.composer?.environment.label ??
    (message.runtime.roots.length > 1
      ? message.runtime.selectedRootLabel
      : "Local");
  approvalPosture.textContent = trusted
    ? message.composer?.approval.label ?? "Loading profile"
    : "Read-only";
  renderComposer(message);
  runtime.textContent = `CodeHelper Runtime: ${message.runtime.state}` +
    (trusted ? " · trusted" : " · read-only") +
    ` · ${String(message.runtime.sessions.length)} chats`;
  journeyState.textContent = message.presentation.journey;
  repair.hidden = !message.presentation.repairVisible;
  repairDetail.textContent = message.runtime.error ??
    "Run readiness checks to identify missing configuration or capabilities.";
  empty.hidden = !message.presentation.emptyVisible;
  const stickToBottom = isNearBottom();
  renderTranscript(turns, message.snapshot, trusted, transcriptActions);
  if (stickToBottom) turns.scrollTo({ top: turns.scrollHeight });
}

function renderComposer(message: ChatSnapshotMessage): void {
  const composer = message.composer;
  if (composer === undefined) {
    composerContexts.replaceChildren();
    for (const button of [
      modeControl,
      providerControl,
      modelControl,
      thinkingControl,
      toolsControl,
      credentialControl,
      approvalControl,
    ]) {
      button.disabled = true;
    }
    return;
  }
  setControl(modeControl, composer.mode);
  setControl(providerControl, composer.provider);
  setControl(modelControl, composer.model);
  if (composer.thinking === undefined) {
    thinkingControl.hidden = true;
    thinkingControl.disabled = true;
  } else {
    thinkingControl.hidden = false;
    setControl(thinkingControl, composer.thinking);
  }
  setControl(credentialControl, composer.credential);
  setControl(toolsControl, composer.tools);
  setControl(approvalControl, composer.approval);
  composerContexts.replaceChildren(...composer.contexts.map((context) => {
    const chip = document.createElement("span");
    chip.className = "context-chip";
    chip.textContent = context.label;
    const remove = document.createElement("button");
    remove.type = "button";
    remove.className = "context-remove";
    remove.dataset["contextId"] = context.id;
    remove.setAttribute("aria-label", `Remove ${context.label}`);
    remove.textContent = "×";
    chip.append(remove);
    return chip;
  }));
}

function setControl(
  button: HTMLButtonElement,
  control: {
    readonly label: string;
    readonly enabled: boolean;
    readonly title: string;
  },
): void {
  button.textContent = control.label;
  button.disabled = !control.enabled;
  button.title = control.title;
}

function renderSessionList(): void {
  const query = sessionSearch.value.trim();
  const matchingProjection = query.length > 0 &&
    sessionSearchProjection?.query === query
    ? sessionSearchProjection
    : undefined;
  const matchIDs = matchingProjection === undefined
    ? undefined
    : new Set(matchingProjection.sessionIds);
  const serverMatches = matchingProjection !== undefined;
  const source = matchIDs === undefined
    ? sessions
    : sessions.filter((session) => matchIDs.has(session.sessionId));
  const visible = filterChatSessions(
    source,
    serverMatches ? "" : query,
    sessionFilter.value as SessionStatusFilter,
  );
  sessionCount.textContent = String(visible.length);
  const groups = groupChatSessions(visible);
  let position = 0;
  sessionVirtualItems = groups.flatMap((group): SessionVirtualItem[] => [
    {
      kind: "heading",
      key: `heading:${group.id}`,
      label: group.label.toLocaleUpperCase(),
      height: 24,
    },
    ...group.sessions.map((session): SessionVirtualItem => ({
      kind: "session",
      key: `session:${session.sessionId}`,
      session,
      position: ++position,
      setSize: visible.length,
      height: 52,
    })),
  ]);
  renderVirtualSessionWindow();
}

function renderVirtualSessionWindow(): void {
  if (sessionVirtualItems.length === 0) {
    sessionList.replaceChildren();
    return;
  }
  const window = computeVirtualWindow(
    sessionVirtualItems,
    sessionList.scrollTop,
    sessionList.clientHeight || 480,
    260,
  );
  const fragment = document.createDocumentFragment();
  fragment.append(virtualSpacer(window.paddingBefore));
  for (let index = window.start; index < window.end; index++) {
    const item = sessionVirtualItems[index];
    if (item === undefined) continue;
    fragment.append(item.kind === "heading"
      ? sessionHeading(item.label)
      : sessionRow(item, index));
  }
  fragment.append(virtualSpacer(window.paddingAfter));
  sessionList.replaceChildren(fragment);
}

function virtualSpacer(height: number): HTMLElement {
  const spacer = document.createElement("div");
  spacer.className = "session-virtual-spacer";
  spacer.style.height = `${String(height)}px`;
  spacer.setAttribute("aria-hidden", "true");
  return spacer;
}

function sessionHeading(label: string): HTMLElement {
  const heading = document.createElement("div");
  heading.className = "session-group-label";
  heading.textContent = label;
  heading.setAttribute("role", "heading");
  heading.setAttribute("aria-level", "3");
  return heading;
}

function sessionRow(
  item: Extract<SessionVirtualItem, { readonly kind: "session" }>,
  virtualIndex: number,
): HTMLElement {
  const { session } = item;
  const row = document.createElement("div");
  row.className = "session-row";
  row.dataset["virtualIndex"] = String(virtualIndex);
  const button = document.createElement("button");
  button.type = "button";
  button.className = "session-item";
  button.dataset["sessionId"] = session.sessionId;
  button.setAttribute("aria-current", session.selected ? "page" : "false");
  button.setAttribute("aria-posinset", String(item.position));
  button.setAttribute("aria-setsize", String(item.setSize));
  const status = sessionStatusLabel(session);
  button.setAttribute("aria-label", `${session.title}, ${status}`);
  const title = document.createElement("span");
  title.className = "session-item-title";
  const indicator = document.createElement("span");
  indicator.className = [
    "running", "awaiting_approval", "awaiting_input",
  ].includes(session.status)
    ? "session-indicator active"
    : "session-indicator";
  indicator.textContent = "•";
  indicator.setAttribute("aria-hidden", "true");
  title.append(
    indicator,
    document.createTextNode(`${session.pinned ? "★ " : ""}${session.title}`),
  );
  const meta = document.createElement("span");
  meta.className = "session-item-meta";
  meta.textContent = status;
  button.append(title, meta);
  button.disabled = session.archived;
  button.addEventListener("click", () => {
    post({ type: "select-chat", sessionId: session.sessionId });
    setSessionsOpen(false);
  });
  const manage = document.createElement("button");
  manage.type = "button";
  manage.className = "session-manage icon-button";
  manage.title = `Manage ${session.title}`;
  manage.setAttribute("aria-label", `Manage ${session.title}`);
  manage.textContent = "…";
  manage.addEventListener("click", () => {
    post({
      type: "manage-chat",
      sessionId: session.sessionId,
      action: "menu",
    });
  });
  row.append(button, manage);
  return row;
}

function setSessionsOpen(open: boolean): void {
  document.body.classList.toggle("sessions-open", open);
  toggleSessions.setAttribute("aria-expanded", String(open));
  sessionRail.setAttribute("aria-hidden", String(!open && isNarrow()));
  if (open && isNarrow()) {
    sessionSearch.focus();
  }
}

function isNarrow(): boolean {
  return narrowMedia.matches;
}

function isNearBottom(): boolean {
  return turns.scrollHeight - turns.scrollTop - turns.clientHeight < 48;
}

function post(message: unknown): void {
  vscode.postMessage(message);
}

function isChatHostMessage(value: unknown): value is ChatHostMessage {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return false;
  }
  const candidate = value as Readonly<Record<string, unknown>>;
  return candidate["version"] === 1 &&
    (candidate["type"] === "snapshot" || candidate["type"] === "error");
}

setSessionsOpen(false);
post({ type: "ready" });
