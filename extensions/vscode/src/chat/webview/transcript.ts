import type {
  ApprovalCard,
  ChatSnapshot,
  ChatTurn,
  ContextReceiptCard,
  ContextSelectionCard,
  InputCard,
  TurnTimelineItem,
} from "../projector.js";
import { approvalCardContent } from "../approval-summary.js";
import { restoredViewportScrollTop } from "../transcript-window.js";
import { presentTool } from "../tool-presentation.js";
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
  readonly previewWorkspaceChanges: () => void;
  readonly applyWorkspaceChanges: () => void;
  readonly canApplyWorkspaceChanges: () => boolean;
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
    article.classList.toggle(
      "active-turn",
      turn.status === "running" ||
        turn.status === "awaiting_approval" ||
        turn.status === "awaiting_input",
    );
    article.dataset["turnId"] = turn.id;
    article.dataset["stateKey"] = `turn:${turn.id}`;
    appendText(article, "div", "user", turn.user || "(restored turn)");
    const status = turnStatus(turn);
    if (status !== undefined) {
      appendText(article, "div", "meta turn-status", status);
    }
    if (turn.contextReceipts.length > 0 ||
      turn.contextSelections.length > 0) {
      article.append(referenceGroup(turn, actions));
    }
    const stream = document.createElement("div");
    stream.className = "turn-stream";
    appendTimeline(stream, turn, trusted, actions);
    article.append(stream);
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
    if (turn.workspaceChange !== undefined) {
      const workspace = document.createElement("section");
      workspace.className = "workspace-change-card";
      const header = document.createElement("div");
      header.className = "workspace-change-header";
      const copy = document.createElement("div");
      copy.className = "workspace-change-copy";
      appendText(
        copy,
        "strong",
        "workspace-change-title",
        "Changes ready to review",
      );
      const fileLabel = turn.workspaceChange.changedCount === 1 ? "file" : "files";
      appendText(
        copy,
        "span",
        "workspace-change-summary",
        `${String(turn.workspaceChange.changedCount)} ${fileLabel} · Isolated worktree`,
      );
      header.append(copy);
      appendText(header, "span", "workspace-change-badge", "Pending apply");
      workspace.append(header);

      if (turn.workspaceChange.workspace !== undefined) {
        const details = document.createElement("details");
        details.className = "workspace-change-details";
        appendText(details, "summary", "", "Worktree details");
        appendText(
          details,
          "code",
          "workspace-change-path",
          turn.workspaceChange.workspace,
        );
        workspace.append(details);
      }

      const actionsRow = document.createElement("div");
      actionsRow.className = "workspace-change-actions";
      const preview = actionButton("Review Changes", () => {
        actions.previewWorkspaceChanges();
      });
      preview.className = "workspace-change-action primary";
      actionsRow.append(preview);
      const canApply = actions.canApplyWorkspaceChanges();
      if (canApply) {
        const apply = actionButton("Apply", () => {
          actions.applyWorkspaceChanges();
        });
        apply.className = "workspace-change-action secondary";
        apply.disabled = !trusted;
        apply.title = trusted
          ? "Apply the current previewed merge plan"
          : "Trust this workspace before applying changes";
        actionsRow.append(apply);
      }
      workspace.append(actionsRow);
      article.append(workspace);
    }
    if (turn.receipt !== undefined) {
      const receipt = document.createElement("details");
      receipt.className = "receipt";
      receipt.dataset["stateKey"] = `turn:${turn.id}:receipt`;
      appendText(receipt, "summary", "", "Run Details");
      appendText(receipt, "div", "meta", turn.receipt);
      article.append(receipt);
    }
    if (turn.error !== undefined) {
      appendText(article, "div", "error", turn.error);
    }
    if (turn.status === "failed" || turn.status === "canceled") {
      const recovery = document.createElement("div");
      recovery.className = "recovery-actions";
      recovery.dataset["recoveryTurnId"] = turn.id;
      const retry = actionButton("Retry", () => {
        actions.recover(turn.id, "retry");
      });
      retry.className = "recovery-action primary";
      retry.title = "Run the original request again";
      const resume = actionButton("Continue", () => {
        actions.recover(turn.id, "continue");
      });
      resume.className = "recovery-action secondary";
      resume.title = "Keep existing results and continue";
      recovery.append(retry, resume);
      const status = document.createElement("span");
      status.className = "recovery-status";
      status.setAttribute("role", "status");
      status.hidden = true;
      recovery.append(status);
      article.append(recovery);
    }
    for (const unknown of turn.unknownEvents) {
      const details = document.createElement("details");
      appendText(details, "summary", "", "Unknown event");
      appendText(details, "pre", "", unknown);
      article.append(details);
    }
    return article;
}

function turnStatus(turn: ChatTurn): string | undefined {
  switch (turn.status) {
    case "running":
      return turn.reasoning.length === 0 && turn.tools.length === 0
        ? "Preparing…"
        : undefined;
    case "awaiting_approval":
      return "Waiting for approval";
    case "awaiting_input":
      return "Waiting for input";
    case "failed":
      return "Incomplete";
    case "canceled":
      return "Canceled";
    case "completed":
      return undefined;
  }
}

function appendTimeline(
  stream: HTMLElement,
  turn: ChatTurn,
  trusted: boolean,
  actions: TranscriptActions,
): void {
  let group: HTMLDetailsElement | undefined;
  let groupBody: HTMLElement | undefined;
  let groupItems: TurnTimelineItem[] = [];
  const pendingApprovals = turn.approvals.filter((approval) =>
    approval.resolved === undefined);
  const firstPendingApproval = pendingApprovals[0]?.requestId;
  for (const item of turn.timeline) {
    let node: HTMLElement | undefined;
    if (item.kind === "approval" && pendingApprovals.length > 1 &&
      pendingApprovals.some((approval) => approval.requestId === item.requestId)) {
      if (item.requestId !== firstPendingApproval) continue;
      node = approvalCarousel(pendingApprovals, trusted, actions);
    } else {
      node = timelineItem(item, turn, trusted, actions);
    }
    if (node === undefined) continue;
    if (item.kind === "output" || item.kind === "approval" ||
      item.kind === "input") {
      group = undefined;
      groupBody = undefined;
      groupItems = [];
      stream.append(node);
      continue;
    }
    if (group === undefined || groupBody === undefined) {
      group = document.createElement("details");
      group.className = "activity-group";
      group.dataset["stateKey"] =
        `turn:${turn.id}:activity:${item.id}`;
      appendText(group, "summary", "activity-group-summary", "");
      groupBody = document.createElement("div");
      groupBody.className = "activity-group-body";
      group.append(groupBody);
      stream.append(group);
      groupItems = [];
    }
    groupBody.append(node);
    groupItems.push(item);
    const summary = group.querySelector("summary");
    if (summary !== null) {
      summary.textContent = activityGroupLabel(groupItems, turn);
    }
    group.classList.toggle(
      "running",
      groupItems.some((candidate) => timelineItemActive(candidate, turn)),
    );
    group.classList.toggle(
      "failed",
      groupItems.some((candidate) => timelineItemFailed(candidate, turn)),
    );
  }
}

function activityGroupLabel(
  items: readonly TurnTimelineItem[],
  turn: ChatTurn,
): string {
  const active = items.some((item) => timelineItemActive(item, turn));
  const failed = items.some((item) => timelineItemFailed(item, turn));
  const count = `${String(items.length)} item${items.length === 1 ? "" : "s"}`;
  if (failed) return `Activity · ${count} · Issues found`;
  return `${active ? "Working" : "Activity"} · ${count}`;
}

function timelineItemActive(item: TurnTimelineItem, turn: ChatTurn): boolean {
  if (item.kind === "reasoning") return item.active;
  if (item.kind !== "tool") return false;
  return turn.tools.some((tool) =>
    tool.callId === item.callId && tool.status === "running");
}

function timelineItemFailed(item: TurnTimelineItem, turn: ChatTurn): boolean {
  if (item.kind === "diagnostics") {
    return item.messages.some((message) => message.includes(": failed "));
  }
  if (item.kind === "verification") return item.text.startsWith("failed:");
  if (item.kind !== "tool") return false;
  return turn.tools.some((tool) =>
    tool.callId === item.callId && tool.status === "failed");
}

function timelineItem(
  item: TurnTimelineItem,
  turn: ChatTurn,
  trusted: boolean,
  actions: TranscriptActions,
): HTMLElement | undefined {
  switch (item.kind) {
    case "output": {
      const output = document.createElement("section");
      output.className = item.final
        ? "timeline-output final-output"
        : "timeline-output";
      if (item.final) {
        appendText(output, "div", "section-label", "Final Result");
      }
      appendMarkdown(
        output,
        item.markdown,
        "assistant",
        actions.openResource,
      );
      return output;
    }
    case "reasoning": {
      const reasoning = timelineDetails(
        turn,
        item,
        item.active ? "Thinking…" : "Reasoning",
        `reasoning ${item.active ? "running" : "completed"}`,
      );
      reasoning.open = true;
      const content = document.createElement("div");
      content.className = "reasoning-content";
      content.dataset["expandedKey"] =
        `turn:${turn.id}:timeline:${item.id}:reasoning`;
      const long = reasoningIsLong(item.text);
      if (long) content.classList.add("long");
      appendMarkdown(
        content,
        item.markdown,
        "reasoning-body",
        actions.openResource,
      );
      if (long) {
        const toggle = document.createElement("button");
        toggle.type = "button";
        toggle.className = "reasoning-toggle";
        toggle.addEventListener("click", () => {
          setReasoningExpanded(
            content,
            !content.classList.contains("expanded"),
          );
        });
        content.append(toggle);
        setReasoningExpanded(content, false);
      }
      reasoning.append(content);
      return reasoning;
    }
    case "tool": {
      const tool = turn.tools.find((candidate) =>
        candidate.callId === item.callId);
      if (tool === undefined) return undefined;
      const presentation = presentTool(tool);
      const details = timelineDetails(
        turn,
        item,
        presentation.label,
        `tool ${tool.status}`,
      );
      const summary = details.querySelector("summary");
      if (summary === null) return details;
      if (presentation.target !== undefined) {
        appendText(summary, "code", "tool-target", presentation.target);
      }
      if (presentation.files !== undefined) {
        details.classList.add("file-change");
        details.open = true;
        const list = document.createElement("div");
        list.className = "file-change-list";
        for (const file of presentation.files) {
          const row = document.createElement("div");
          row.className = `file-change-row ${file.kind}`;
          const icon = document.createElement("span");
          icon.className = `file-type-icon ${fileTypeClass(file.path)}`;
          icon.textContent = fileTypeLabel(file.path);
          icon.setAttribute("aria-hidden", "true");
          row.append(icon);
          let name: HTMLElement;
          if (file.resourceId === undefined) {
            name = document.createElement("span");
            name.textContent = fileName(file.path);
          } else {
            name = resourceButton(
              file.resourceId,
              actions,
              fileName(file.path),
            );
          }
          name.classList.add("file-change-name");
          name.title = file.path;
          row.append(name);
          appendText(row, "span", "file-lines added", `+${String(file.added)}`);
          appendText(row, "span", "file-lines removed", `-${String(file.removed)}`);
          list.append(row);
        }
        details.append(list);
      } else if (presentation.fileOperation === true) {
        if (presentation.detail !== undefined) {
          appendText(
            details,
            "div",
            `tool-message ${tool.status}`,
            presentation.detail,
          );
        }
      } else if (presentation.command !== undefined) {
        const body = document.createElement("div");
        body.className = "command-detail";
        appendText(body, "div", "command-section-label", "Full Command");
        appendText(body, "pre", "command-full", `$ ${presentation.command}`);
        if (presentation.detail !== undefined) {
          appendText(body, "div", "command-section-label", "Arguments");
          appendText(body, "pre", "command-metadata", presentation.detail);
        }
        appendText(body, "div", "command-section-label", "Output");
        appendText(
          body,
          "pre",
          "command-output",
          tool.output.length > 0 ? tool.output : commandOutputPlaceholder(tool.status),
        );
        details.append(body);
      } else {
        appendText(
          summary,
          "span",
          `tool-status ${tool.status}`,
          toolStatus(tool.status),
        );
        if (tool.arguments !== undefined) {
          appendText(details, "pre", "tool-raw", tool.arguments);
        }
        if (tool.output.length > 0) {
          appendText(details, "pre", "tool-raw", tool.output);
        }
      }
      return details;
    }
    case "approval": {
      const approval = turn.approvals.find((candidate) =>
        candidate.requestId === item.requestId);
      if (approval === undefined) return undefined;
      const card = approvalCard(approval, trusted, actions);
      card.classList.add("timeline-item", "approval");
      card.dataset["timelineId"] = item.id;
      return card;
    }
    case "input": {
      const input = turn.inputs.find((candidate) =>
        candidate.requestId === item.requestId);
      if (input === undefined) return undefined;
      const card = inputCard(input, actions);
      card.classList.add("timeline-item", "input");
      card.dataset["timelineId"] = item.id;
      return card;
    }
    case "diagnostics": {
      const failed = item.messages.some((message) =>
        message.includes(": failed "));
      const details = timelineDetails(
        turn,
        item,
        failed ? "Diagnostics found issues" : "Diagnostics completed",
        `diagnostics ${failed ? "failed" : "completed"}`,
      );
      for (const message of item.messages) {
        appendText(details, "div", "meta timeline-message", message);
      }
      return details;
    }
    case "verification": {
      const failed = item.text.startsWith("failed:");
      const details = timelineDetails(
        turn,
        item,
        failed ? "Verification failed" : "Verification completed",
        `verification ${failed ? "failed" : "completed"}`,
      );
      appendText(details, "div", "meta timeline-message", item.text);
      return details;
    }
    case "notice": {
      const details = timelineDetails(
        turn,
        item,
        "Run Status",
        "notice completed",
      );
      appendText(details, "div", "meta timeline-message", item.text);
      return details;
    }
  }
}

function timelineDetails(
  turn: ChatTurn,
  item: Exclude<TurnTimelineItem, { readonly kind: "output" }>,
  label: string,
  variant: string,
): HTMLDetailsElement {
  const details = document.createElement("details");
  details.className = `timeline-item ${variant}`;
  details.dataset["stateKey"] = `turn:${turn.id}:timeline:${item.id}`;
  details.dataset["timelineId"] = item.id;
  appendText(details, "summary", "", label);
  return details;
}

function reasoningIsLong(text: string): boolean {
  return text.length > 1200 || text.split("\n").length > 14;
}

function setReasoningExpanded(
  content: HTMLElement,
  expanded: boolean,
): void {
  content.classList.toggle("expanded", expanded);
  const toggle = content.querySelector<HTMLButtonElement>(".reasoning-toggle");
  if (toggle === null) return;
  toggle.textContent = expanded ? "Collapse" : "Expand";
  toggle.setAttribute("aria-expanded", String(expanded));
}

function fileName(path: string): string {
  return path.split("/").at(-1) ?? path;
}

function fileTypeLabel(path: string): string {
  const extension = path.split(".").at(-1)?.toLowerCase() ?? "";
  const labels: Readonly<Record<string, string>> = {
    ts: "TS",
    tsx: "TS",
    js: "JS",
    jsx: "JS",
    go: "GO",
    py: "PY",
    rs: "RS",
    java: "JV",
    json: "{}",
    md: "MD",
    css: "#",
    html: "<>",
    yaml: "Y",
    yml: "Y",
    toml: "T",
  };
  return labels[extension] ?? "·";
}

function fileTypeClass(path: string): string {
  const extension = path.split(".").at(-1)?.toLowerCase() ?? "file";
  return `type-${extension.replaceAll(/[^a-z0-9]/gu, "") || "file"}`;
}

function toolStatus(status: "running" | "completed" | "failed"): string {
  switch (status) {
    case "running":
      return "Running";
    case "failed":
      return "Failed";
    case "completed":
      return "Completed";
  }
}

function commandOutputPlaceholder(
  status: "running" | "completed" | "failed",
): string {
  switch (status) {
    case "running":
      return "Waiting for command output…";
    case "failed":
      return "The command returned no output";
    case "completed":
      return "The command produced no output";
  }
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

function referenceGroup(
  turn: ChatTurn,
  actions: TranscriptActions,
): HTMLElement {
  const group = document.createElement("details");
  group.className = "reference-group";
  group.dataset["stateKey"] = `turn:${turn.id}:references`;
  const included = turn.contextSelections.filter(
    (selection) => selection.included,
  ).length;
  const count = turn.contextReceipts.length + turn.contextSelections.length;
  appendText(
    group,
    "summary",
    "",
    `References${count > 1 ? ` · ${String(count)} items` : ""}`,
  );
  const body = document.createElement("div");
  body.className = "reference-group-body";
  for (const receipt of turn.contextReceipts) {
    body.append(contextReceiptCard(receipt, actions));
  }
  for (const selection of turn.contextSelections) {
    body.append(contextSelectionCard(selection, actions));
  }
  if (included !== turn.contextSelections.length) {
    appendText(
      body,
      "div",
      "meta reference-omission",
      `${String(included)}/${String(turn.contextSelections.length)} items included`,
    );
  }
  group.append(body);
  return group;
}

function approvalCard(
  approval: ApprovalCard,
  trusted: boolean,
  actions: TranscriptActions,
): HTMLElement {
  const content = approvalCardContent(approval);
  const box = document.createElement("section");
  box.className = "approval-card";
  box.dataset["stateKey"] = `approval:${approval.requestId}`;
  box.setAttribute("role", "region");
  box.setAttribute("aria-label", `${content.risk}: ${content.title}`);
  const header = document.createElement("div");
  header.className = "approval-header";
  appendText(header, "span", `approval-risk ${approval.risk}`, content.risk);
  appendText(header, "strong", "approval-title", content.title);
  box.append(header);
  appendText(box, "p", "approval-consequence", content.consequence);
  if (content.source !== undefined) {
    appendText(box, "div", "approval-source", content.source);
  }
  if (content.target !== undefined) {
    appendText(box, "pre", "approval-target", content.target);
  }
  const expired = Date.parse(approval.expiresAt) <= Date.now();
  if (approval.resolved !== undefined || expired) {
    appendText(
      box,
      "div",
      "approval-outcome",
      approval.resolved ?? "Expired",
    );
    return box;
  }
  const row = document.createElement("div");
  row.className = "approval-actions";
  if (approval.editPlan !== undefined) {
    const preview = actionButton("Review changes", () => {
      actions.preview(approval.requestId);
    });
    preview.className = "approval-action";
    row.append(preview);
    for (const file of approval.editPlan.files) {
      if (file.resourceId !== undefined) {
        box.append(resourceButton(file.resourceId, actions, `Open ${file.path} diff`));
      }
    }
  }
  if (trusted && approval.allowedScopes.includes("once")) {
    const allow = actionButton("Allow once", () => {
      actions.approve(approval.requestId, "once", approval.editPlan?.id);
    });
    allow.className = "approval-action primary";
    row.append(allow);
  }
  const deny = actionButton("Skip", () => {
    actions.deny(approval.requestId);
  });
  deny.className = "approval-action";
  row.append(deny);
  const reusable = approval.allowedScopes.filter((scope) => scope !== "once");
  const more = document.createElement("details");
  more.className = "approval-more";
  appendText(more, "summary", "", "More");
  for (const scope of trusted ? reusable : []) {
    more.append(actionButton(
      scope === "always" ? "Always allow" : "Allow for session",
      () => {
        actions.approve(approval.requestId, scope, approval.editPlan?.id);
      },
    ));
  }
  more.append(actionButton("Stop turn", () => {
    actions.cancel(approval.requestId);
  }));
  row.append(more);
  box.append(row);
  const info = document.createElement("details");
  info.className = "approval-info";
  appendText(info, "summary", "", "Request details");
  appendText(info, "div", "approval-summary", content.detail);
  box.append(info);
  return box;
}

function approvalCarousel(
  approvals: readonly ApprovalCard[],
  trusted: boolean,
  actions: TranscriptActions,
): HTMLElement {
  const carousel = document.createElement("section");
  carousel.className = "approval-carousel timeline-item approval";
  carousel.dataset["stateKey"] = `approval-carousel:${approvals[0]?.requestId ?? ""}`;
  carousel.tabIndex = 0;
  carousel.setAttribute(
    "aria-label",
    `${String(approvals.length)} pending approvals; scroll horizontally`,
  );
  carousel.append(...approvals.map((approval) =>
    approvalCard(approval, trusted, actions)));
  return carousel;
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
  readonly details: ReadonlySet<string>;
  readonly open: ReadonlySet<string>;
  readonly expanded: ReadonlySet<string>;
  readonly scrollTop: number;
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
  const details = new Set(
    [...container.querySelectorAll<HTMLDetailsElement>(
      "details[data-state-key]",
    )].flatMap((value) =>
      value.dataset["stateKey"] === undefined
        ? []
        : [value.dataset["stateKey"]]),
  );
  const open = new Set(
    [...container.querySelectorAll<HTMLDetailsElement>(
      "details[data-state-key][open]",
    )].flatMap((value) =>
      value.dataset["stateKey"] === undefined
        ? []
        : [value.dataset["stateKey"]]),
  );
  const expanded = new Set(
    [...container.querySelectorAll<HTMLElement>(
      "[data-expanded-key].expanded",
    )].flatMap((value) =>
      value.dataset["expandedKey"] === undefined
        ? []
        : [value.dataset["expandedKey"]]),
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
    details,
    open,
    expanded,
    scrollTop: container.scrollTop,
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
    if (key !== undefined && state.details.has(key)) {
      details.open = state.open.has(key);
    }
  }
  for (const content of container.querySelectorAll<HTMLElement>(
    "[data-expanded-key]",
  )) {
    const key = content.dataset["expandedKey"];
    if (key !== undefined && state.expanded.has(key)) {
      setReasoningExpanded(content, true);
    }
  }
  // Replacing the only large active Turn can temporarily collapse its layout
  // under content-visibility. Preserve the exact viewport first so a missing
  // Article anchor never resets the transcript to the user Prompt.
  container.scrollTop = state.scrollTop;
  if (state.anchor !== undefined) {
    const anchor = turnArticle(container, state.anchor.turnId);
    if (anchor !== undefined) {
      const currentOffset = anchor.getBoundingClientRect().top -
        container.getBoundingClientRect().top;
      container.scrollTop = restoredViewportScrollTop(
        state.scrollTop,
        container.clientHeight,
        currentOffset,
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
