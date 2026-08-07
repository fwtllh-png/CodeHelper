/// <reference lib="dom" />

import type {
  ChatErrorMessage,
  ChatHostMessage,
  ChatSessionView,
  ChatSnapshotMessage,
} from "../contract.js";
import {
  filterChatSessions,
  sessionStatusLabel,
} from "../session-list.js";
import { element } from "./dom.js";
import {
  renderTranscript,
  type TranscriptActions,
} from "./transcript.js";

interface VSCodeAPI {
  postMessage(message: unknown): void;
}

declare function acquireVsCodeApi(): VSCodeAPI;

const vscode = acquireVsCodeApi();
const root = element("root") as HTMLSelectElement;
const chatTitle = element("chat-title");
const sessionList = element("session-list");
const sessionSearch = element("session-search") as HTMLInputElement;
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
const send = element("send") as HTMLButtonElement;
const stop = element("stop") as HTMLButtonElement;
const newChat = element("new-chat") as HTMLButtonElement;
const mergeChat = element("merge-chat") as HTMLButtonElement;
const railNewChat = element("rail-new-chat") as HTMLButtonElement;
const environment = element("environment");
const approvalPosture = element("approval-posture");
let trusted = false;
let messageMergePlanId: string | undefined;
let sessions: readonly ChatSessionView[] = [];

const transcriptActions: TranscriptActions = {
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
};

(element("composer") as HTMLFormElement).addEventListener("submit", (event) => {
  event.preventDefault();
  if (prompt.value.trim().length === 0) return;
  post({ type: "submit", text: prompt.value });
  prompt.value = "";
});

prompt.addEventListener("keydown", (event) => {
  if (event.key === "Enter" && (event.ctrlKey || event.metaKey)) {
    event.preventDefault();
    send.click();
  } else if (event.key === "Escape" && !stop.disabled) {
    event.preventDefault();
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
  const prefix = prompt.value.length === 0 || /\s$/u.test(prompt.value)
    ? ""
    : " ";
  prompt.setRangeText(
    `${prefix}@file `,
    prompt.selectionStart,
    prompt.selectionEnd,
    "end",
  );
  prompt.focus();
});
toggleSessions.addEventListener("click", () => {
  setSessionsOpen(!document.body.classList.contains("sessions-open"));
});
element("close-sessions").addEventListener("click", () => {
  setSessionsOpen(false);
});
sessionScrim.addEventListener("click", () => {
  setSessionsOpen(false);
});
sessionSearch.addEventListener("input", () => {
  renderSessionList();
});
sessionList.addEventListener("keydown", (event) => {
  if (!(event instanceof KeyboardEvent) ||
    !["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) {
    return;
  }
  const buttons = [...sessionList.querySelectorAll<HTMLButtonElement>(
    ".session-item",
  )];
  if (buttons.length === 0) return;
  const current = buttons.indexOf(document.activeElement as HTMLButtonElement);
  let next = current;
  if (event.key === "Home") next = 0;
  if (event.key === "End") next = buttons.length - 1;
  if (event.key === "ArrowDown") next = Math.min(buttons.length - 1, current + 1);
  if (event.key === "ArrowUp") next = Math.max(0, current - 1);
  event.preventDefault();
  buttons[next]?.focus();
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
  environment.textContent = message.runtime.roots.length > 1
    ? message.runtime.selectedRootLabel
    : "Local";
  approvalPosture.textContent = trusted
    ? "Default approvals"
    : "Read-only";
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

function renderSessionList(): void {
  const visible = filterChatSessions(sessions, sessionSearch.value);
  sessionCount.textContent = String(visible.length);
  sessionList.replaceChildren(...visible.map((session) => {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "session-item";
    button.dataset["sessionId"] = session.sessionId;
    button.setAttribute(
      "aria-current",
      session.selected ? "page" : "false",
    );
    const title = document.createElement("span");
    title.className = "session-item-title";
    const indicator = document.createElement("span");
    indicator.className = session.active
      ? "session-indicator active"
      : "session-indicator";
    indicator.textContent = "•";
    title.append(indicator, document.createTextNode(session.title));
    const meta = document.createElement("span");
    meta.className = "session-item-meta";
    meta.textContent = sessionStatusLabel(session);
    button.append(title, meta);
    button.addEventListener("click", () => {
      post({ type: "select-chat", sessionId: session.sessionId });
      setSessionsOpen(false);
    });
    return button;
  }));
}

function setSessionsOpen(open: boolean): void {
  document.body.classList.toggle("sessions-open", open);
  toggleSessions.setAttribute("aria-expanded", String(open));
  sessionRail.setAttribute("aria-hidden", String(!open && isNarrow()));
  if (open && isNarrow()) sessionSearch.focus();
}

function isNarrow(): boolean {
  return window.matchMedia("(max-width: 720px)").matches;
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
