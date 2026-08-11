import {
  isUnknownEvent,
  type DecodedEvent,
} from "../protocol/decode.js";
import type {
  DiagnosticsResultData,
  TurnReceiptData,
  TurnStartedData,
} from "../protocol/generated.js";
import {
  projectEditPlan,
  type EditPlanCard,
} from "../edits/model.js";
import {
  projectMarkdown,
  type MarkdownNode,
} from "./markdown.js";
import type { ResourceRange } from "./resources.js";

export type {
  EditPlanCard,
  EditPlanFileCard,
} from "../edits/model.js";

const maxCardText = 64 << 10;
const maxChatTurns = 200;
const expandedContextMarker = "\n\nExplicit editor context follows as JSON.";

export type TurnStatus =
  | "running"
  | "awaiting_approval"
  | "awaiting_input"
  | "completed"
  | "failed"
  | "canceled";

export interface ToolCard {
  readonly callId: string;
  readonly tool: string;
  readonly status: "running" | "completed" | "failed";
  readonly arguments?: string;
  readonly output: string;
  readonly changes: readonly FileChangeCard[];
}

export interface FileChangeCard {
  readonly path: string;
  readonly kind: "created" | "modified" | "deleted";
  readonly added: number;
  readonly removed: number;
  readonly resourceId?: string;
}

export interface ApprovalCard {
  readonly requestId: string;
  readonly turnId: string;
  readonly itemId: string;
  readonly tool: string;
  readonly arguments: string;
  readonly resources: readonly string[];
  readonly allowedScopes: readonly string[];
  readonly expiresAt: string;
  readonly reason?: string;
  readonly resolved?: string;
  readonly editPlan?: EditPlanCard;
}

export interface InputCard {
  readonly requestId: string;
  readonly turnId: string;
  readonly itemId: string;
  readonly prompt: string;
  readonly options: readonly string[];
  readonly expiresAt: string;
  readonly resolved?: string;
}

export interface PlanCard {
  readonly id?: string;
  readonly body: string;
  readonly bodyMarkdown: readonly MarkdownNode[];
  readonly status: "drafting" | "ready";
  readonly canImplement: boolean;
  readonly canAutopilot: boolean;
}

export interface WorkspaceChangeCard {
  readonly changedCount: number;
  readonly workspace?: string;
}

export type TurnTimelineItem =
  | {
      readonly id: string;
      readonly sequence: number;
      readonly kind: "output";
      readonly text: string;
      readonly markdown: readonly MarkdownNode[];
      readonly final: boolean;
    }
  | {
      readonly id: string;
      readonly sequence: number;
      readonly kind: "reasoning";
      readonly text: string;
      readonly markdown: readonly MarkdownNode[];
      readonly active: boolean;
    }
  | {
      readonly id: string;
      readonly sequence: number;
      readonly kind: "tool";
      readonly callId: string;
    }
  | {
      readonly id: string;
      readonly sequence: number;
      readonly kind: "approval";
      readonly requestId: string;
    }
  | {
      readonly id: string;
      readonly sequence: number;
      readonly kind: "input";
      readonly requestId: string;
    }
  | {
      readonly id: string;
      readonly sequence: number;
      readonly kind: "diagnostics";
      readonly messages: readonly string[];
    }
  | {
      readonly id: string;
      readonly sequence: number;
      readonly kind: "verification";
      readonly text: string;
    }
  | {
      readonly id: string;
      readonly sequence: number;
      readonly kind: "notice";
      readonly text: string;
    };

type EditorContextReceipt =
  NonNullable<TurnStartedData["editor_context"]>[number];

export interface ContextReceiptCard {
  readonly kind: EditorContextReceipt["kind"];
  readonly source?: EditorContextReceipt["source"];
  readonly path: string;
  readonly label?: string;
  readonly digest: string;
  readonly range?: string;
  readonly navigationRange?: ResourceRange;
  readonly symbol?: string;
  readonly symbolName?: string;
  readonly resourceId?: string;
  readonly diagnosticCount: number;
  readonly omittedDiagnostics: number;
  readonly originalBytes: number;
  readonly retainedBytes: number;
  readonly truncated: boolean;
}

type ContextSelection =
  NonNullable<TurnReceiptData["context_selections"]>[number];

export interface ContextSelectionCard {
  readonly path: string;
  readonly kind: string;
  readonly reasons: readonly string[];
  readonly evidence: readonly string[];
  readonly score: number;
  readonly critical: boolean;
  readonly included: boolean;
  readonly truncated: boolean;
  readonly truncationReason?: string;
  readonly resourceId?: string;
}

export interface ChatTurn {
  readonly id: string;
  readonly user: string;
  readonly status: TurnStatus;
  readonly output: string;
  readonly outputMarkdown: readonly MarkdownNode[];
  readonly reasoning: string;
  readonly reasoningMarkdown: readonly MarkdownNode[];
  readonly reasoningActive: boolean;
  readonly timeline: readonly TurnTimelineItem[];
  readonly tools: readonly ToolCard[];
  readonly approvals: readonly ApprovalCard[];
  readonly inputs: readonly InputCard[];
  readonly plan?: PlanCard;
  readonly contextReceipts: readonly ContextReceiptCard[];
  readonly contextSelections: readonly ContextSelectionCard[];
  readonly diagnostics: readonly string[];
  readonly verification?: string;
  readonly workspaceChange?: WorkspaceChangeCard;
  readonly receipt?: string;
  readonly error?: string;
  readonly unknownEvents: readonly string[];
}

export interface ChatSnapshot {
  readonly turns: readonly ChatTurn[];
  readonly activeTurnId?: string;
}

interface MutableTool {
  callId: string;
  tool: string;
  status: "running" | "completed" | "failed";
  arguments?: string;
  output: string;
  changes: FileChangeCard[];
}

interface MutableApproval {
  requestId: string;
  turnId: string;
  itemId: string;
  tool: string;
  arguments: string;
  resources: string[];
  allowedScopes: string[];
  expiresAt: string;
  reason?: string;
  resolved?: string;
  editPlan?: EditPlanCard;
}

interface MutableInput {
  requestId: string;
  turnId: string;
  itemId: string;
  prompt: string;
  options: string[];
  expiresAt: string;
  resolved?: string;
}

type DiagnosticReceipt = DiagnosticsResultData["receipts"][number];

type MutableTimelineItem =
  | {
      id: string;
      sequence: number;
      kind: "output";
      text: string;
    }
  | {
      id: string;
      sequence: number;
      kind: "reasoning";
      text: string;
    }
  | {
      id: string;
      sequence: number;
      kind: "tool";
      callId: string;
    }
  | {
      id: string;
      sequence: number;
      kind: "approval";
      requestId: string;
    }
  | {
      id: string;
      sequence: number;
      kind: "input";
      requestId: string;
    }
  | {
      id: string;
      sequence: number;
      kind: "diagnostics";
      messages: string[];
    }
  | {
      id: string;
      sequence: number;
      kind: "verification" | "notice";
      text: string;
    };

interface MutableTurn {
  id: string;
  user: string;
  status: TurnStatus;
  output: string;
  reasoning: string;
  reasoningActive: boolean;
  timeline: MutableTimelineItem[];
  tools: Map<string, MutableTool>;
  approvals: Map<string, MutableApproval>;
  inputs: Map<string, MutableInput>;
  plan?: Omit<PlanCard, "bodyMarkdown">;
  contextReceipts: ContextReceiptCard[];
  contextSelections: ContextSelectionCard[];
  diagnostics: Map<string, DiagnosticReceipt>;
  diagnosticNotices: string[];
  workspace?: string;
  workspaceIsolation?: string;
  workspaceChange?: WorkspaceChangeCard;
  verification?: string;
  receipt?: string;
  error?: string;
  unknownEvents: string[];
  lastSequence: number;
}

export class ChatProjector {
  readonly #turns = new Map<string, MutableTurn>();
  #lastSequence = 0;
  #activeTurnId: string | undefined;

  public apply(event: DecodedEvent): boolean {
    if (event.sequence <= this.#lastSequence) {
      return false;
    }
    this.#lastSequence = event.sequence;
    if (!isUnknownEvent(event) &&
      (event.kind === "agent.spawned" ||
        event.kind === "agent.status" ||
        event.kind === "agent.message")) {
      return false;
    }
    if (!isUnknownEvent(event) && event.kind === "checkpoint.restored") {
      for (const [turnId, candidate] of this.#turns) {
        if (candidate.lastSequence > event.data.source_cursor) {
          this.#turns.delete(turnId);
        }
      }
      this.#activeTurnId = undefined;
      return true;
    }
    if (!isUnknownEvent(event) &&
      (event.kind === "checkpoint.created" ||
        event.kind === "checkpoint.forked")) {
      return true;
    }
    const turn = this.#turn(event.turn_id);
    if (isUnknownEvent(event)) {
      turn.unknownEvents.push(truncate(JSON.stringify(event.raw)));
      return true;
    }
    switch (event.kind) {
      case "turn.started":
        turn.user = displayPrompt(event.data.display_prompt ?? event.data.prompt ?? "");
        turn.status = "running";
        if (event.data.workspace !== undefined) {
          turn.workspace = event.data.workspace;
        }
        if (event.data.workspace_isolation !== undefined) {
          turn.workspaceIsolation = event.data.workspace_isolation;
        }
        turn.contextReceipts = projectContextReceipts(
          event.data.editor_context ?? [],
        );
        this.#activeTurnId = event.turn_id;
        break;
      case "output.delta":
        turn.output = appendBounded(turn.output, event.data.text);
        appendTimelineText(turn, "output", event.sequence, event.data.text);
        turn.reasoningActive = false;
        break;
      case "reasoning.delta":
        turn.reasoning = appendReasoning(turn.reasoning, event.data.text);
        appendTimelineText(turn, "reasoning", event.sequence, event.data.text);
        turn.reasoningActive = true;
        break;
      case "reasoning.signature":
        turn.reasoningActive = false;
        break;
      case "tool.start": {
        turn.reasoningActive = false;
        turn.tools.set(event.data.call_id, {
          callId: event.data.call_id,
          tool: event.data.tool,
          status: "running",
          ...(event.data.arguments === undefined
            ? {}
            : { arguments: stringify(event.data.arguments) }),
          output: "",
          changes: [],
        });
        ensureToolTimelineItem(turn, event.sequence, event.data.call_id);
        break;
      }
      case "tool.output": {
        const tool = this.#tool(turn, event.data.call_id, event.data.tool);
        ensureToolTimelineItem(turn, event.sequence, event.data.call_id);
        tool.output = appendBounded(tool.output, event.data.chunk);
        break;
      }
      case "tool.result": {
        const tool = this.#tool(turn, event.data.call_id, event.data.tool);
        ensureToolTimelineItem(turn, event.sequence, event.data.call_id);
        tool.output = truncate(event.data.output);
        tool.status = event.data.is_error ? "failed" : "completed";
        tool.changes = (event.data.changes ?? []).flatMap((change) => {
          const kind = fileChangeKind(change.kind);
          return kind === undefined ||
            change.added < 0 ||
            change.removed < 0
            ? []
            : [{
                path: truncateField(change.path, 4096),
                kind,
                added: change.added,
                removed: change.removed,
              }];
        });
        break;
      }
      case "approval.required":
        turn.reasoningActive = false;
        turn.status = "awaiting_approval";
        turn.approvals.set(event.data.request_id, {
          requestId: event.data.request_id,
          turnId: event.turn_id,
          itemId: event.item_id,
          tool: event.data.tool,
          arguments: stringify(event.data.arguments),
          resources: [
            ...event.data.resources.map((resource) =>
              `${resource.access}:${resource.path ?? resource.id ?? resource.kind}`),
            ...(event.data.network === undefined
              ? []
              : [`network:${event.data.network.protocol}://${event.data.network.host} ` +
                `(${event.data.network.mode})`]),
          ],
          allowedScopes: [...event.data.allowed_scopes],
          expiresAt: event.data.expires_at,
          ...(event.data.reason === undefined ? {} : { reason: event.data.reason }),
          ...(event.data.edit_plan === undefined
            ? {}
            : {
                editPlan: projectEditPlan(event.data.edit_plan),
              }),
        });
        turn.timeline.push({
          id: `approval:${event.data.request_id}`,
          sequence: event.sequence,
          kind: "approval",
          requestId: event.data.request_id,
        });
        break;
      case "approval.resolved": {
        const approval = turn.approvals.get(event.data.request_id);
        if (approval !== undefined) {
          approval.resolved = event.data.decision;
        }
        turn.status = "running";
        break;
      }
      case "input.required":
        turn.reasoningActive = false;
        turn.status = "awaiting_input";
        turn.inputs.set(event.data.request_id, {
          requestId: event.data.request_id,
          turnId: event.turn_id,
          itemId: event.item_id,
          prompt: event.data.prompt,
          options: [...(event.data.options ?? [])],
          expiresAt: event.data.expires_at,
        });
        turn.timeline.push({
          id: `input:${event.data.request_id}`,
          sequence: event.sequence,
          kind: "input",
          requestId: event.data.request_id,
        });
        break;
      case "input.resolved": {
        const input = turn.inputs.get(event.data.request_id);
        if (input !== undefined) {
          input.resolved = event.data.answer ?? "";
        }
        turn.status = "running";
        break;
      }
      case "diagnostics.result":
        for (const receipt of event.data.receipts) {
          const detail = receipt.message ??
            `${String(receipt.diagnostics.length)} diagnostics`;
          turn.diagnostics.set(
            `${receipt.path}\u0000${receipt.status}\u0000${detail}`,
            receipt,
          );
        }
        turn.timeline.push({
          id: `diagnostics:${String(event.sequence)}`,
          sequence: event.sequence,
          kind: "diagnostics",
          messages: summarizeDiagnostics(event.data.receipts),
        });
        break;
      case "turn.verification": {
        const verification = truncate(
          `${event.data.status}: ${(event.data.checks ?? []).map((check) => {
            const command = check.command ?? check.name;
            const category = check.category === undefined
              ? ""
              : ` [${check.category}]`;
            const reason = check.reason === undefined ? "" : ` because ${check.reason}`;
            return `${command}=${check.status}${category}${reason}`;
          }).join(", ")}`,
        );
        turn.verification = verification;
        turn.timeline.push({
          id: `verification:${String(event.sequence)}`,
          sequence: event.sequence,
          kind: "verification",
          text: verification,
        });
        break;
      }
      case "turn.receipt": {
        turn.receipt = `tokens ${String(event.data.input_tokens)} in / ` +
          `${String(event.data.output_tokens)} out, cost ` +
          `${event.data.cost_known ? String(event.data.cost_microunits) : "unknown"} µ`;
        if (event.data.latency !== undefined) {
          turn.receipt += `; latency total=${String(event.data.latency.total_ms)}ms` +
            ` provider=${String(event.data.latency.provider_ms)}ms` +
            ` tools=${String(event.data.latency.tool_ms)}ms` +
            ` approval=${String(event.data.latency.approval_wait_ms)}ms`;
        }
        const risks = event.data.evidence?.risks ?? [];
        if (risks.length > 0) {
          turn.receipt += `; risks ${risks.map((risk) =>
            `${risk.kind}:${risk.path}`).join(",")}`;
        }
        if (event.data.verification_detail !== undefined) {
          const verification = event.data.verification_detail;
          turn.receipt += `; verify ${verification.final_status}` +
            ` action=${verification.action}` +
            ` repairs=${String(verification.repair_steps)}`;
        }
        if (event.data.workspace_outcome !== undefined) {
          turn.receipt += `; workspace ${event.data.workspace_outcome.status}`;
          const conflicts = event.data.workspace_outcome.conflicts ?? [];
          if (conflicts.length > 0) {
            turn.receipt += ` conflicts=${conflicts.join(",")}`;
          }
          if (turn.workspaceIsolation === "worktree" &&
            event.data.workspace_outcome.status === "changed") {
            turn.workspaceChange = {
              changedCount: event.data.workspace_outcome.changed?.length ?? 0,
              ...(turn.workspace === undefined
                ? {}
                : { workspace: turn.workspace }),
            };
            turn.receipt += "; isolated changes pending Merge → Apply";
            if (turn.workspace !== undefined) {
              turn.receipt += `; worktree ${turn.workspace}`;
            }
          }
        }
        turn.receipt = truncate(turn.receipt);
        if (event.data.editor_context !== undefined) {
          turn.contextReceipts = projectContextReceipts(event.data.editor_context);
        }
        turn.contextSelections = projectContextSelections(
          event.data.context_selections ?? [],
        );
        break;
      }
      case "plan.delta": {
        const body = event.data.body ??
          appendBounded(turn.plan?.body ?? "", event.data.text ?? "");
        turn.plan = {
          ...(event.data.artifact_id === undefined
            ? {}
            : { id: event.data.artifact_id }),
          body: truncate(body),
          status: event.data.status === "ready" ? "ready" : "drafting",
          canImplement: event.data.can_implement === true,
          canAutopilot: event.data.can_autopilot === true,
        };
        break;
      }
      case "turn.completed":
        if (event.data.text.length > 0) {
          reconcileCompletedOutput(turn, event.sequence, event.data.text);
        }
        this.#terminal(turn, "completed");
        break;
      case "turn.failed":
        turn.error = truncate(`${event.data.code}: ${event.data.message}`);
        this.#terminal(turn, "failed");
        break;
      case "turn.canceled":
        turn.error = truncate(event.data.reason);
        this.#terminal(turn, "canceled");
        break;
      case "operation.rejected":
        turn.error = truncate(`${event.data.code}: ${event.data.message}`);
        break;
      case "tool.state":
        if (event.data.text !== undefined) {
          const notice = truncate(`tool: ${event.data.state} ${event.data.text}`);
          turn.diagnosticNotices.push(notice);
          turn.timeline.push({
            id: `notice:${String(event.sequence)}`,
            sequence: event.sequence,
            kind: "notice",
            text: notice,
          });
        }
        break;
      default:
        break;
    }
    turn.lastSequence = event.sequence;
    return true;
  }

  public snapshot(): ChatSnapshot {
    const turns = [...this.#turns.values()].map((turn): ChatTurn => ({
      id: turn.id,
      user: turn.user,
      status: turn.status,
      output: turn.output,
      outputMarkdown: projectMarkdown(turn.output),
      reasoning: turn.reasoning,
      reasoningMarkdown: projectMarkdown(turn.reasoning),
      reasoningActive: turn.reasoningActive,
      timeline: projectTimeline(turn),
      tools: [...turn.tools.values()].map((tool) => ({
        ...tool,
        changes: tool.changes.map((change) => ({ ...change })),
      })),
      approvals: [...turn.approvals.values()].map((approval) => ({ ...approval })),
      inputs: [...turn.inputs.values()].map((input) => ({ ...input })),
      ...(turn.plan === undefined
        ? {}
        : {
            plan: {
              ...turn.plan,
              bodyMarkdown: projectMarkdown(turn.plan.body),
            },
          }),
      contextReceipts: turn.contextReceipts.map((receipt) => ({ ...receipt })),
      contextSelections: turn.contextSelections.map((selection) => ({
        ...selection,
        reasons: [...selection.reasons],
        evidence: [...selection.evidence],
      })),
      diagnostics: [
        ...turn.diagnosticNotices,
        ...summarizeDiagnostics(turn.diagnostics.values()),
      ],
      ...(turn.verification === undefined ? {} : { verification: turn.verification }),
      ...(turn.workspaceChange === undefined
        ? {}
        : { workspaceChange: { ...turn.workspaceChange } }),
      ...(turn.receipt === undefined ? {} : { receipt: turn.receipt }),
      ...(turn.error === undefined ? {} : { error: turn.error }),
      unknownEvents: [...turn.unknownEvents],
    }));
    return {
      turns,
      ...(this.#activeTurnId === undefined ? {} : { activeTurnId: this.#activeTurnId }),
    };
  }

  public pendingApprovals(): readonly ApprovalCard[] {
    return [...this.#turns.values()].flatMap((turn) =>
      [...turn.approvals.values()]
        .filter((approval) => approval.resolved === undefined)
        .map((approval) => ({ ...approval })));
  }

  public pendingInputs(): readonly InputCard[] {
    return [...this.#turns.values()].flatMap((turn) =>
      [...turn.inputs.values()]
        .filter((input) => input.resolved === undefined)
        .map((input) => ({ ...input })));
  }

  #turn(id: string): MutableTurn {
    let turn = this.#turns.get(id);
    if (turn === undefined) {
      turn = {
        id,
        user: "",
        status: "running",
        output: "",
        reasoning: "",
        reasoningActive: false,
        timeline: [],
        tools: new Map(),
        approvals: new Map(),
        inputs: new Map(),
        contextReceipts: [],
        contextSelections: [],
        diagnostics: new Map(),
        diagnosticNotices: [],
        unknownEvents: [],
        lastSequence: 0,
      };
      this.#turns.set(id, turn);
      if (this.#turns.size > maxChatTurns) {
        const removable = [...this.#turns.values()].find(
          (candidate) => candidate.id !== this.#activeTurnId &&
            candidate.status !== "running" &&
            candidate.status !== "awaiting_approval" &&
            candidate.status !== "awaiting_input",
        );
        if (removable !== undefined) this.#turns.delete(removable.id);
      }
    }
    return turn;
  }

  #tool(turn: MutableTurn, callId: string, toolName: string): MutableTool {
    let tool = turn.tools.get(callId);
    if (tool === undefined) {
      tool = {
        callId,
        tool: toolName,
        status: "running",
        output: "",
        changes: [],
      };
      turn.tools.set(callId, tool);
    }
    return tool;
  }

  #terminal(turn: MutableTurn, status: TurnStatus): void {
    turn.status = status;
    turn.reasoningActive = false;
    if (this.#activeTurnId === turn.id) {
      this.#activeTurnId = undefined;
    }
  }
}

function projectContextSelections(
  selections: readonly ContextSelection[],
): ContextSelectionCard[] {
  return selections.map((selection) => ({
    path: selection.path,
    kind: selection.kind,
    reasons: [...selection.reasons],
    evidence: (selection.evidence ?? []).map((fact) => {
      const tool = fact.tool === undefined ? "" : `/${fact.tool}`;
      const symbol = fact.symbol === undefined ? "" : ` ${fact.symbol}`;
      return `${fact.kind}${tool}${symbol}`;
    }),
    score: selection.score,
    critical: selection.critical ?? false,
    included: selection.included,
    truncated: selection.truncated ?? false,
    ...(selection.truncation_reason === undefined
      ? {}
      : { truncationReason: selection.truncation_reason }),
  }));
}

function projectContextReceipts(
  values: readonly EditorContextReceipt[],
): ContextReceiptCard[] {
  return values.slice(0, 8).map((value) => ({
    kind: value.kind,
    ...(value.source === undefined ? {} : { source: value.source }),
    path: truncateField(value.path, 4096),
    ...(value.label === undefined
      ? {}
      : { label: truncateField(value.label, 512) }),
    digest: truncateField(value.digest, 64),
    ...(value.range === undefined
      ? {}
      : {
          range: `${String(value.range.start.line + 1)}:` +
            `${String(value.range.start.character + 1)}-` +
            `${String(value.range.end.line + 1)}:` +
            String(value.range.end.character + 1),
          navigationRange: {
            start: { ...value.range.start },
            end: { ...value.range.end },
          },
        }),
    ...(value.symbol === undefined
      ? {}
      : {
          symbol: truncateField(
            `${value.symbol.kind} ${value.symbol.name}`,
            640,
          ),
          symbolName: truncateField(value.symbol.name, 512),
        }),
    diagnosticCount: value.diagnostic_count ?? 0,
    omittedDiagnostics: value.omitted_diagnostics ?? 0,
    originalBytes: value.original_bytes,
    retainedBytes: value.retained_bytes,
    truncated: value.truncated ?? false,
  }));
}

function truncateField(value: string, maximum: number): string {
  return value.length <= maximum ? value : `${value.slice(0, maximum - 1)}…`;
}

function fileChangeKind(
  value: string,
): FileChangeCard["kind"] | undefined {
  return value === "created" || value === "modified" || value === "deleted"
    ? value
    : undefined;
}

function summarizeDiagnostics(
  receipts: Iterable<DiagnosticReceipt>,
): string[] {
  const result: string[] = [];
  const unconfigured = new Map<string, Set<string>>();
  for (const receipt of receipts) {
    const extension = unconfiguredDiagnosticsExtension(receipt);
    if (extension !== undefined) {
      const paths = unconfigured.get(extension) ?? new Set<string>();
      paths.add(receipt.path);
      unconfigured.set(extension, paths);
      continue;
    }
    const detail = receipt.message ??
      `${String(receipt.diagnostics.length)} diagnostics`;
    result.push(truncate(`${receipt.path}: ${receipt.status} (${detail})`));
  }
  for (const [extension, paths] of unconfigured) {
    result.push(
      `${extension} 文件未配置编辑后诊断（${String(paths.size)} 个文件）`,
    );
  }
  return result;
}

function unconfiguredDiagnosticsExtension(
  receipt: DiagnosticReceipt,
): string | undefined {
  if (receipt.status !== "unavailable" || receipt.message === undefined) {
    return undefined;
  }
  const matched = /^no post-edit diagnostics command is configured for (\.[a-z0-9]+)$/iu
    .exec(receipt.message);
  return matched?.[1]?.toLowerCase();
}

function displayPrompt(prompt: string): string {
  const marker = prompt.indexOf(expandedContextMarker);
  return marker < 0 ? truncate(prompt) : truncate(prompt.slice(0, marker));
}

function appendBounded(current: string, addition: string): string {
  return truncate(`${current}${addition}`);
}

function appendTimelineText(
  turn: MutableTurn,
  kind: "output" | "reasoning",
  sequence: number,
  text: string,
): void {
  const last = turn.timeline.at(-1);
  if (last?.kind === kind) {
    last.text = kind === "reasoning"
      ? appendReasoning(last.text, text)
      : appendBounded(last.text, text);
    return;
  }
  turn.timeline.push({
    id: `${kind}:${String(sequence)}`,
    sequence,
    kind,
    text: truncate(text),
  });
}

function reconcileCompletedOutput(
  turn: MutableTurn,
  sequence: number,
  completedText: string,
): void {
  const completed = truncate(completedText);
  if (completed === turn.output) {
    if (!turn.timeline.some((item) => item.kind === "output")) {
      appendTimelineText(turn, "output", sequence, completed);
    }
    return;
  }
  let lastOutputIndex = -1;
  for (let index = turn.timeline.length - 1; index >= 0; index -= 1) {
    if (turn.timeline[index]?.kind === "output") {
      lastOutputIndex = index;
      break;
    }
  }
  const last = turn.timeline[lastOutputIndex];
  if (last?.kind === "output" && last.text.endsWith(completed)) {
    const progress = last.text.slice(0, -completed.length);
    if (progress.length > 0) {
      last.text = progress;
      turn.timeline.splice(lastOutputIndex + 1, 0, {
        id: `output:${String(sequence)}`,
        sequence,
        kind: "output",
        text: completed,
      });
    }
  } else if (last?.kind !== "output" || last.text !== completed) {
    turn.timeline.push({
      id: `output:${String(sequence)}`,
      sequence,
      kind: "output",
      text: completed,
    });
  }
  if (turn.output.length === 0) {
    turn.output = completed;
  } else if (!turn.output.endsWith(completed)) {
    const separator = turn.output.endsWith("\n") ? "" : "\n\n";
    turn.output = appendBounded(turn.output, `${separator}${completed}`);
  }
}

function ensureToolTimelineItem(
  turn: MutableTurn,
  sequence: number,
  callId: string,
): void {
  if (turn.timeline.some(
    (item) => item.kind === "tool" && item.callId === callId,
  )) {
    return;
  }
  turn.timeline.push({
    id: `tool:${callId}`,
    sequence,
    kind: "tool",
    callId,
  });
}

function projectTimeline(turn: MutableTurn): TurnTimelineItem[] {
  let lastActivity = -1;
  let lastOutput = -1;
  for (let index = 0; index < turn.timeline.length; index += 1) {
    if (turn.timeline[index]?.kind === "output") {
      lastOutput = index;
    } else {
      lastActivity = index;
    }
  }
  return turn.timeline.map((item, index): TurnTimelineItem => {
    switch (item.kind) {
      case "output":
        return {
          ...item,
          markdown: projectMarkdown(item.text),
          final: turn.status === "completed" &&
            index === lastOutput &&
            index > lastActivity,
        };
      case "reasoning":
        return {
          ...item,
          markdown: projectMarkdown(item.text),
          active: turn.reasoningActive && index === turn.timeline.length - 1,
        };
      case "diagnostics":
        return { ...item, messages: [...item.messages] };
      default:
        return { ...item };
    }
  });
}

function appendReasoning(current: string, addition: string): string {
  if (addition === current) return current;
  if (current.length > 0 && addition.startsWith(current)) {
    return truncate(addition);
  }
  return appendBounded(current, addition);
}

function stringify(value: unknown): string {
  try {
    return truncate(JSON.stringify(value, null, 2));
  } catch {
    return "[unserializable]";
  }
}

function truncate(value: string): string {
  if (value.length <= maxCardText) {
    return value;
  }
  return `${value.slice(0, maxCardText)}\n...[truncated]`;
}
