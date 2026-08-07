/// <reference lib="dom" />

import type {
  ChatErrorMessage,
  ChatHostMessage,
  ChatSnapshotMessage,
} from "../contract.js";
import type {
  ApprovalCard,
  ChatSnapshot,
  ContextReceiptCard,
  ContextSelectionCard,
  InputCard,
} from "../projector.js";
import type { MarkdownNode } from "../markdown.js";

interface VSCodeAPI {
  postMessage(message: unknown): void;
}

declare function acquireVsCodeApi(): VSCodeAPI;

const vscode = acquireVsCodeApi();
const root = element("root") as HTMLSelectElement;
const chat = element("chat") as HTMLSelectElement;
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
let trusted = false;
let messageMergePlanId: string | undefined;

(element("composer") as HTMLFormElement).addEventListener("submit", (event) => {
  event.preventDefault();
  if (prompt.value.trim().length === 0) return;
  vscode.postMessage({ type: "submit", text: prompt.value });
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
  vscode.postMessage({ type: "stop" });
});
(element("repair-runtime") as HTMLButtonElement).addEventListener("click", () => {
  vscode.postMessage({ type: "repair-runtime" });
});
(element("run-setup") as HTMLButtonElement).addEventListener("click", () => {
  vscode.postMessage({ type: "run-setup" });
});
root.addEventListener("change", () => {
  vscode.postMessage({ type: "select-root", rootId: root.value });
});
chat.addEventListener("change", () => {
  vscode.postMessage({ type: "select-chat", sessionId: chat.value });
});
newChat.addEventListener("click", () => {
  vscode.postMessage({ type: "new-chat" });
});
mergeChat.addEventListener("click", () => {
  vscode.postMessage(messageMergePlanId === undefined
    ? { type: "merge-chat" }
    : { type: "merge-chat", planId: messageMergePlanId });
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
  chat.replaceChildren(...message.runtime.sessions.map((candidate) => {
    const option = document.createElement("option");
    option.value = candidate.sessionId;
    option.textContent = `${candidate.active ? "● " : ""}${candidate.title}`;
    option.selected = candidate.sessionId === message.runtime.selectedSessionId;
    return option;
  }));
  messageMergePlanId = message.runtime.mergePlanId;
  mergeChat.textContent = messageMergePlanId === undefined ? "Merge" : "Apply";
  mergeChat.disabled = !message.presentation.runtimeReady ||
    (messageMergePlanId !== undefined && !trusted);
  prompt.disabled = !message.presentation.promptEnabled;
  send.disabled = !message.presentation.sendEnabled;
  stop.disabled = !message.presentation.stopEnabled;
  newChat.disabled = !message.presentation.newChatEnabled;
  runtime.textContent = `CodeHelper Runtime: ${message.runtime.state}` +
    (trusted ? " · trusted" : " · read-only") +
    ` · ${String(message.runtime.sessions.length)} chats`;
  journeyState.textContent = message.presentation.journey;
  repair.hidden = !message.presentation.repairVisible;
  repairDetail.textContent = message.runtime.error ??
    "Run readiness checks to identify missing configuration or capabilities.";
  empty.hidden = !message.presentation.emptyVisible;
  renderTranscript(message.snapshot);
}

function renderTranscript(snapshot: ChatSnapshot): void {
  const fragment = document.createDocumentFragment();
  for (const turn of snapshot.turns) {
    const article = document.createElement("article");
    appendText(article, "div", "user", turn.user || "(restored turn)");
    appendText(article, "div", "meta", turn.status);
    if (turn.reasoning.length > 0) {
      const reasoning = document.createElement("details");
      reasoning.className = "reasoning-panel";
      reasoning.open = turn.reasoningActive;
      appendText(
        reasoning,
        "summary",
        "",
        turn.reasoningActive ? "推理过程 · 生成中" : "推理过程",
      );
      appendMarkdown(reasoning, turn.reasoningMarkdown, "reasoning-body");
      article.append(reasoning);
    }
    if (turn.output.length > 0) {
      appendText(article, "div", "section-label", "最终结论");
      appendMarkdown(article, turn.outputMarkdown, "assistant");
    }
    for (const receipt of turn.contextReceipts) {
      article.append(contextReceiptCard(receipt));
    }
    for (const selection of turn.contextSelections) {
      article.append(contextSelectionCard(selection));
    }
    for (const tool of turn.tools) {
      const details = document.createElement("details");
      appendText(details, "summary", "", `${tool.tool} · ${tool.status}`);
      if (tool.arguments !== undefined) {
        appendText(details, "pre", "", tool.arguments);
      }
      if (tool.output.length > 0) appendText(details, "pre", "", tool.output);
      article.append(details);
    }
    for (const approval of turn.approvals) {
      article.append(approvalCard(approval, trusted));
    }
    for (const input of turn.inputs) article.append(inputCard(input));
    for (const diagnostic of turn.diagnostics) {
      appendText(article, "div", "meta", diagnostic);
    }
    if (turn.verification !== undefined) {
      appendText(article, "div", "meta", `Verify: ${turn.verification}`);
    }
    if (turn.receipt !== undefined) {
      appendText(article, "div", "meta", turn.receipt);
    }
    if (turn.error !== undefined) {
      appendText(article, "div", "error", turn.error);
    }
    for (const unknown of turn.unknownEvents) {
      const details = document.createElement("details");
      appendText(details, "summary", "", "Unknown event");
      appendText(details, "pre", "", unknown);
      article.append(details);
    }
    fragment.append(article);
  }
  turns.replaceChildren(fragment);
  window.scrollTo(0, document.body.scrollHeight);
}

function contextReceiptCard(receipt: ContextReceiptCard): HTMLElement {
  const card = document.createElement("details");
  card.className = "context-receipt";
  let label = `Context: ${receipt.kind} · ${receipt.path}`;
  if (receipt.symbol !== undefined) label += ` · ${receipt.symbol}`;
  if (receipt.diagnosticCount > 0) {
    label += ` · ${String(receipt.diagnosticCount)} diagnostics`;
  }
  appendText(card, "summary", "", label);
  appendText(card, "div", "meta", `Source: ${receipt.source ?? "legacy"}`);
  appendText(card, "div", "meta", `SHA-256: ${receipt.digest}`);
  if (receipt.range !== undefined) {
    appendText(card, "div", "meta", `Range: ${receipt.range}`);
  }
  let bytes = `Bytes: ${String(receipt.retainedBytes)}/` +
    String(receipt.originalBytes);
  if (receipt.truncated) bytes += " · truncated";
  appendText(card, "div", "meta", bytes);
  if (receipt.omittedDiagnostics > 0) {
    appendText(
      card,
      "div",
      "meta",
      `Omitted diagnostics: ${String(receipt.omittedDiagnostics)}`,
    );
  }
  return card;
}

function contextSelectionCard(selection: ContextSelectionCard): HTMLElement {
  const card = document.createElement("details");
  card.className = "context-selection";
  let label = `Selected ${selection.kind}: ${selection.path}`;
  if (selection.truncated) label += " · cut";
  appendText(card, "summary", "", label);
  appendText(card, "div", "meta", `Why: ${selection.reasons.join(", ")}`);
  appendText(
    card,
    "div",
    "meta",
    `Score: ${String(selection.score)}${selection.critical ? " · critical" : ""}`,
  );
  if (selection.evidence.length > 0) {
    appendText(
      card,
      "div",
      "meta",
      `Evidence: ${selection.evidence.join(", ")}`,
    );
  }
  appendText(
    card,
    "div",
    "meta",
    selection.included
      ? "Included in model context"
      : `Not included: ${selection.truncationReason ?? "context budget"}`,
  );
  return card;
}

function approvalCard(
  approval: ApprovalCard,
  workspaceTrusted: boolean,
): HTMLElement {
  const box = document.createElement("details");
  box.open = approval.resolved === undefined;
  appendText(
    box,
    "summary",
    "",
    `Approval: ${approval.tool}` +
      (approval.resolved === undefined ? "" : ` · ${approval.resolved}`),
  );
  appendText(box, "pre", "", approval.arguments);
  appendText(box, "div", "meta", approval.resources.join(", "));
  if (approval.reason !== undefined) {
    appendText(box, "div", "meta", approval.reason);
  }
  if (approval.resolved !== undefined ||
    Date.parse(approval.expiresAt) <= Date.now()) {
    return box;
  }
  if (approval.editPlan !== undefined) {
    box.append(actionButton("Preview diff", () => {
      vscode.postMessage({
        type: "preview",
        requestId: approval.requestId,
      });
    }));
  }
  if (workspaceTrusted) {
    for (const scope of approval.allowedScopes) {
      box.append(actionButton(`Approve ${scope}`, () => {
        vscode.postMessage({
          type: "approval",
          requestId: approval.requestId,
          decision: "approve",
          scope,
          ...(approval.editPlan === undefined
            ? {}
            : { planId: approval.editPlan.id }),
        });
      }));
    }
  }
  box.append(actionButton("Deny", () => {
    vscode.postMessage({
      type: "approval",
      requestId: approval.requestId,
      decision: "deny",
      scope: "once",
    });
  }));
  box.append(actionButton("Cancel turn", () => {
    vscode.postMessage({
      type: "approval",
      requestId: approval.requestId,
      decision: "cancel",
      scope: "once",
    });
  }));
  return box;
}

function inputCard(input: InputCard): HTMLElement {
  const box = document.createElement("details");
  box.open = input.resolved === undefined;
  appendText(box, "summary", "", "Input required");
  appendText(box, "div", "", input.prompt);
  if (input.resolved === undefined) {
    for (const option of input.options) {
      box.append(actionButton(option, () => {
        vscode.postMessage({
          type: "input",
          requestId: input.requestId,
          answer: option,
        });
      }));
    }
  }
  return box;
}

function actionButton(label: string, handler: () => void): HTMLButtonElement {
  const button = document.createElement("button");
  button.type = "button";
  button.textContent = label;
  button.addEventListener("click", handler);
  return button;
}

const markdownTags = new Set([
  "a", "blockquote", "br", "code", "del", "em",
  "h1", "h2", "h3", "h4", "h5", "h6", "hr",
  "li", "ol", "p", "pre", "span", "strong",
  "table", "tbody", "td", "th", "thead", "tr", "ul",
]);

function appendMarkdown(
  parent: HTMLElement,
  nodes: readonly MarkdownNode[],
  className: string,
): void {
  const container = document.createElement("div");
  container.className = `markdown ${className}`;
  for (const node of nodes) container.append(markdownNode(node));
  parent.append(container);
}

function markdownNode(value: MarkdownNode): Node {
  if (value.kind === "text") return document.createTextNode(value.text);
  if (!markdownTags.has(value.tag)) return document.createTextNode("");
  const node = document.createElement(value.tag);
  if (value.tag === "a" && value.href !== undefined &&
    /^(?:https?:|mailto:)/u.test(value.href)) {
    node.setAttribute("href", value.href);
    node.setAttribute("target", "_blank");
    node.setAttribute("rel", "noreferrer noopener");
  }
  if (value.tag === "ol" && value.start !== undefined &&
    Number.isSafeInteger(value.start) && value.start > 1 &&
    value.start <= 1_000_000) {
    node.setAttribute("start", String(value.start));
  }
  if (value.tag === "code" && value.language !== undefined &&
    /^[\w+.-]{1,64}$/u.test(value.language)) {
    node.className = `language-${value.language}`;
  }
  for (const child of value.children) node.append(markdownNode(child));
  return node;
}

function appendText(
  parent: HTMLElement,
  tag: string,
  className: string,
  text: string,
): void {
  const node = document.createElement(tag);
  if (className.length > 0) node.className = className;
  node.textContent = text;
  parent.append(node);
}

function element(id: string): HTMLElement {
  const value = document.getElementById(id);
  if (value === null) throw new Error(`missing Webview element ${id}`);
  return value;
}

function isChatHostMessage(value: unknown): value is ChatHostMessage {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return false;
  }
  const candidate = value as Readonly<Record<string, unknown>>;
  return candidate["version"] === 1 &&
    (candidate["type"] === "snapshot" || candidate["type"] === "error");
}
