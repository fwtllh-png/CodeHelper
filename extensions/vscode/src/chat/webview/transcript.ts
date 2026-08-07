import type {
  ApprovalCard,
  ChatSnapshot,
  ContextReceiptCard,
  ContextSelectionCard,
  InputCard,
} from "../projector.js";
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
}

export function renderTranscript(
  container: HTMLElement,
  snapshot: ChatSnapshot,
  trusted: boolean,
  actions: TranscriptActions,
): void {
  const fragment = document.createDocumentFragment();
  for (const turn of snapshot.turns) {
    const article = document.createElement("article");
    article.dataset["turnId"] = turn.id;
    appendText(article, "div", "user", turn.user || "(restored turn)");
    appendText(article, "div", "meta turn-status", turn.status);
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
    for (const unknown of turn.unknownEvents) {
      const details = document.createElement("details");
      appendText(details, "summary", "", "Unknown event");
      appendText(details, "pre", "", unknown);
      article.append(details);
    }
    fragment.append(article);
  }
  container.replaceChildren(fragment);
}

function contextReceiptCard(
  receipt: ContextReceiptCard,
  actions: TranscriptActions,
): HTMLElement {
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
  return actionButton(label, () => {
    actions.openResource(resourceId);
  });
}
