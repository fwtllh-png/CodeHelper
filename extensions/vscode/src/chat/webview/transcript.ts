import type {
  ApprovalCard,
  ChatSnapshot,
  ChatTurn,
  ContextReceiptCard,
  ContextSelectionCard,
  InputCard,
} from "../projector.js";
import { restoredAnchorScrollTop } from "../transcript-window.js";
import { appendMarkdown, appendText } from "./dom.js";

export interface TranscriptActions {
  readonly openResource: (resourceId: string) => void;
  readonly preview: (requestId: string) => void;
  readonly approve: (
    requestId: string,
    scope: string,
    planId?: string,
  ) => void;
  readonly deny: (requestId: string) => void;
  readonly cancel: (requestId: string) => void;
  readonly answer: (requestId: string, answer: string) => void;
  readonly plan: (
    planId: string,
    action: "implement" | "autopilot" | "open",
  ) => void;
  readonly recover: (
    turnId: string,
    action: "retry" | "continue",
  ) => void;
}

export function renderTranscript(
  container: HTMLElement,
  snapshot: ChatSnapshot,
  trusted: boolean,
  actions: TranscriptActions,
): void {
  const fragment = document.createDocumentFragment();
  for (const turn of snapshot.turns) {
    fragment.append(renderTurn(turn, trusted, actions));
  }
  container.replaceChildren(fragment);
}

export function patchTranscript(
  container: HTMLElement,
  snapshot: ChatSnapshot,
  changedTurnIds: ReadonlySet<string>,
  removedTurnIds: ReadonlySet<string>,
  trusted: boolean,
  actions: TranscriptActions,
): void {
  const state = captureTranscriptState(container);
  for (const turnId of removedTurnIds) {
    turnArticle(container, turnId)?.remove();
  }
  for (const turn of snapshot.turns) {
    if (!changedTurnIds.has(turn.id)) continue;
    const next = renderTurn(turn, trusted, actions);
    const current = turnArticle(container, turn.id);
    if (current === undefined) {
      const nextTurn = snapshot.turns[
        snapshot.turns.findIndex((candidate) => candidate.id === turn.id) + 1
      ];
      const nextArticle = nextTurn === undefined
        ? undefined
        : turnArticle(container, nextTurn.id);
      const bottom = container.querySelector<HTMLElement>(
        ".transcript-bottom-spacer",
      );
      container.insertBefore(next, nextArticle ?? bottom ?? null);
    } else {
      current.replaceWith(next);
    }
  }
  restoreTranscriptState(container, state);
}

function renderTurn(
  turn: ChatTurn,
  trusted: boolean,
  actions: TranscriptActions,
): HTMLElement {
    const article = document.createElement("article");
    article.dataset["turnId"] = turn.id;
    article.dataset["stateKey"] = `turn:${turn.id}`;
    appendText(article, "div", "user", turn.user || "(restored turn)");
    appendText(article, "div", "meta turn-status", turn.status);
    if (turn.reasoning.length > 0) {
      const reasoning = document.createElement("details");
      reasoning.className = "reasoning-panel";
      reasoning.dataset["stateKey"] = `turn:${turn.id}:reasoning`;
      reasoning.open = turn.reasoningActive;
      appendText(
        reasoning,
        "summary",
        "",
        turn.reasoningActive ? "推理过程 · 生成中" : "推理过程",
      );
      appendMarkdown(
        reasoning,
        turn.reasoningMarkdown,
        "reasoning-body",
        actions.openResource,
      );
      article.append(reasoning);
    }
    if (turn.output.length > 0) {
      appendText(article, "div", "section-label", "最终结论");
      appendMarkdown(
        article,
        turn.outputMarkdown,
        "assistant",
        actions.openResource,
      );
    }
    for (const receipt of turn.contextReceipts) {
      article.append(contextReceiptCard(receipt, actions));
    }
    for (const selection of turn.contextSelections) {
      article.append(contextSelectionCard(selection, actions));
    }
    for (const tool of turn.tools) {
      const details = document.createElement("details");
      details.className = "tool-card";
      details.dataset["stateKey"] = `turn:${turn.id}:tool:${tool.callId}`;
      appendText(details, "summary", "", `${tool.tool} · ${tool.status}`);
      if (tool.arguments !== undefined) {
        appendText(details, "pre", "", tool.arguments);
      }
      if (tool.output.length > 0) appendText(details, "pre", "", tool.output);
      article.append(details);
    }
    for (const approval of turn.approvals) {
      article.append(approvalCard(approval, trusted, actions));
    }
    for (const input of turn.inputs) {
      article.append(inputCard(input, actions));
    }
    if (turn.plan !== undefined) {
      const plan = document.createElement("section");
      plan.className = "plan-card";
      appendText(
        plan,
        "strong",
        "plan-card-title",
        turn.plan.status === "ready" ? "Implementation Plan" : "Drafting Plan",
      );
      appendMarkdown(
        plan,
        turn.plan.bodyMarkdown,
        "plan-card-body",
        actions.openResource,
      );
      if (turn.plan.id !== undefined && turn.plan.status === "ready") {
        const planId = turn.plan.id;
        plan.append(actionButton("Open in Editor", () => {
          actions.plan(planId, "open");
        }));
        if (turn.plan.canImplement) {
          plan.append(actionButton("Start Implementation", () => {
            actions.plan(planId, "implement");
          }));
        }
        if (trusted && turn.plan.canAutopilot) {
          plan.append(actionButton("Start with Autopilot", () => {
            actions.plan(planId, "autopilot");
          }));
        }
      }
      article.append(plan);
    }
    for (const diagnostic of turn.diagnostics) {
      appendText(article, "div", "meta", diagnostic);
    }
    if (turn.verification !== undefined) {
      appendText(article, "div", "meta", `Verify: ${turn.verification}`);
    }
    if (turn.receipt !== undefined) {
      appendText(article, "div", "meta receipt", turn.receipt);
    }
    if (turn.error !== undefined) {
      appendText(article, "div", "error", turn.error);
    }
    if (turn.status === "failed" || turn.status === "canceled") {
      article.append(actionButton("Retry", () => {
        actions.recover(turn.id, "retry");
      }));
      article.append(actionButton("Continue", () => {
        actions.recover(turn.id, "continue");
      }));
    }
    for (const unknown of turn.unknownEvents) {
      const details = document.createElement("details");
      appendText(details, "summary", "", "Unknown event");
      appendText(details, "pre", "", unknown);
      article.append(details);
    }
    return article;
}

function contextReceiptCard(
  receipt: ContextReceiptCard,
  actions: TranscriptActions,
): HTMLElement {
  const card = document.createElement("details");
  card.className = "context-receipt";
  card.dataset["stateKey"] = `context:${receipt.kind}:${receipt.digest}`;
  let label = `Context: ${receipt.kind} · ${receipt.label ?? receipt.path}`;
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
  if (receipt.resourceId !== undefined) {
    card.append(resourceButton(receipt.resourceId, actions));
  }
  return card;
}

function contextSelectionCard(
  selection: ContextSelectionCard,
  actions: TranscriptActions,
): HTMLElement {
  const card = document.createElement("details");
  card.className = "context-selection";
  card.dataset["stateKey"] = `selection:${selection.kind}:${selection.path}`;
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
  if (selection.resourceId !== undefined) {
    card.append(resourceButton(selection.resourceId, actions));
  }
  return card;
}

function approvalCard(
  approval: ApprovalCard,
  trusted: boolean,
  actions: TranscriptActions,
): HTMLElement {
  const box = document.createElement("details");
  box.className = "approval-card";
  box.dataset["stateKey"] = `approval:${approval.requestId}`;
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
      actions.preview(approval.requestId);
    }));
    for (const file of approval.editPlan.files) {
      if (file.resourceId !== undefined) {
        box.append(resourceButton(file.resourceId, actions, `Open ${file.path} diff`));
      }
    }
  }
  if (trusted) {
    for (const scope of approval.allowedScopes) {
      box.append(actionButton(`Approve ${scope}`, () => {
        actions.approve(
          approval.requestId,
          scope,
          approval.editPlan?.id,
        );
      }));
    }
  }
  box.append(actionButton("Deny", () => {
    actions.deny(approval.requestId);
  }));
  box.append(actionButton("Cancel turn", () => {
    actions.cancel(approval.requestId);
  }));
  return box;
}

function inputCard(
  input: InputCard,
  actions: TranscriptActions,
): HTMLElement {
  const box = document.createElement("details");
  box.className = "input-card";
  box.dataset["stateKey"] = `input:${input.requestId}`;
  box.open = input.resolved === undefined;
  appendText(box, "summary", "", "Input required");
  appendText(box, "div", "", input.prompt);
  if (input.resolved === undefined) {
    for (const option of input.options) {
      box.append(actionButton(option, () => {
        actions.answer(input.requestId, option);
      }));
    }
  }
  return box;
}

interface TranscriptState {
  readonly open: ReadonlySet<string>;
  readonly focused?: {
    readonly owner: string;
    readonly index: number;
  };
  readonly anchor?: {
    readonly turnId: string;
    readonly offset: number;
  };
}

function captureTranscriptState(container: HTMLElement): TranscriptState {
  const open = new Set(
    [...container.querySelectorAll<HTMLDetailsElement>(
      "details[data-state-key][open]",
    )].flatMap((value) =>
      value.dataset["stateKey"] === undefined
        ? []
        : [value.dataset["stateKey"]]),
  );
  const active = document.activeElement instanceof HTMLElement &&
    container.contains(document.activeElement)
    ? document.activeElement
    : undefined;
  const focusedOwner = active?.closest<HTMLElement>("[data-state-key]");
  const focusedOwnerKey = focusedOwner?.dataset["stateKey"];
  const focusedIndex = active === undefined || focusedOwner === null ||
    focusedOwner === undefined
    ? -1
    : focusableElements(focusedOwner).indexOf(active);
  const focused = focusedOwnerKey === undefined || focusedIndex < 0
    ? undefined
    : { owner: focusedOwnerKey, index: focusedIndex };
  const articles = [...container.querySelectorAll<HTMLElement>(
    "article[data-turn-id]",
  )];
  const anchor = articles.find((article) =>
    article.getBoundingClientRect().bottom >=
      container.getBoundingClientRect().top);
  const turnId = anchor?.dataset["turnId"];
  return {
    open,
    ...(focused === undefined ? {} : { focused }),
    ...(anchor === undefined || turnId === undefined
      ? {}
      : {
          anchor: {
            turnId,
            offset: anchor.getBoundingClientRect().top -
              container.getBoundingClientRect().top,
          },
        }),
  };
}

function restoreTranscriptState(
  container: HTMLElement,
  state: TranscriptState,
): void {
  for (const details of container.querySelectorAll<HTMLDetailsElement>(
    "details[data-state-key]",
  )) {
    const key = details.dataset["stateKey"];
    if (key !== undefined && state.open.has(key)) details.open = true;
  }
  if (state.anchor !== undefined) {
    const anchor = turnArticle(container, state.anchor.turnId);
    if (anchor !== undefined) {
      container.scrollTop = restoredAnchorScrollTop(
        container.scrollTop,
        anchor.getBoundingClientRect().top -
          container.getBoundingClientRect().top,
        state.anchor.offset,
      );
    }
  }
  if (state.focused !== undefined) {
    const owner = [...container.querySelectorAll<HTMLElement>(
      "[data-state-key]",
    )].find((value) => value.dataset["stateKey"] === state.focused?.owner);
    focusableElements(owner)[state.focused.index]?.focus({
      preventScroll: true,
    });
  }
}

function focusableElements(
  container: HTMLElement | undefined,
): HTMLElement[] {
  return container === undefined
    ? []
    : [...container.querySelectorAll<HTMLElement>(
        "button, input, textarea, select, summary, " +
        "[tabindex]:not([tabindex='-1'])",
      )];
}

function turnArticle(
  container: HTMLElement,
  turnId: string,
): HTMLElement | undefined {
  return [...container.querySelectorAll<HTMLElement>("article[data-turn-id]")]
    .find((candidate) => candidate.dataset["turnId"] === turnId);
}

function actionButton(label: string, handler: () => void): HTMLButtonElement {
  const button = document.createElement("button");
  button.type = "button";
  button.textContent = label;
  button.addEventListener("click", handler);
  return button;
}

function resourceButton(
  resourceId: string,
  actions: TranscriptActions,
  label = "Open in Editor",
): HTMLButtonElement {
  const button = actionButton(label, () => {
    actions.openResource(resourceId);
  });
  button.dataset["resourceId"] = resourceId;
  button.title = "Open in Editor. Right-click for more actions.";
  return button;
}
