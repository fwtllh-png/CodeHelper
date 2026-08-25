import type {RuntimeEvent} from "../protocol";

export type ToolVariant =
  | "read"
  | "search"
  | "shell"
  | "write"
  | "diff"
  | "agent"
  | "generic";

export interface ProjectedEditPlanFile {
  readonly path: string;
  readonly kind: string;
  readonly before: string;
  readonly after: string;
  readonly beforeExists: boolean;
  readonly afterExists: boolean;
}

export interface ProjectedEditPlan {
  readonly id: string;
  readonly diff: string;
  readonly files: readonly ProjectedEditPlanFile[];
}

export interface ProjectedDeliverable {
  readonly path: string;
  readonly tool: string;
  readonly kind: string;
  readonly added: number;
  readonly removed: number;
  readonly summary: string;
  readonly callID?: string;
  readonly diff?: ProjectedEditPlanFile;
  readonly stale: boolean;
}

export type ConversationNode =
  | {
      readonly id: string;
      readonly kind: "user";
      readonly turnID: string;
      readonly sequence: number;
      readonly text: string;
      readonly steering?: boolean;
    }
  | {
      readonly id: string;
      readonly kind: "assistant";
      readonly turnID: string;
      readonly sequence: number;
      readonly text: string;
      readonly superseded?: boolean;
    }
  | {
      readonly id: string;
      readonly kind: "reasoning";
      readonly turnID: string;
      readonly sequence: number;
      readonly text: string;
      readonly summary: string;
      readonly running: boolean;
    }
  | {
      readonly id: string;
      readonly kind: "tool";
      readonly turnID: string;
      readonly sequence: number;
      readonly callID: string;
      readonly tool: string;
      readonly variant: ToolVariant;
      readonly title: string;
      readonly summary: string;
      readonly state: "running" | "completed" | "failed";
      readonly arguments: unknown;
      readonly output: string;
      readonly errorSummary: string;
      readonly execution?: Record<string, unknown>;
      readonly command?: {
        readonly command: string;
        readonly status: string;
        readonly exitCode?: number;
        readonly durationMS?: number;
      };
      readonly truncated: boolean;
      readonly changes: readonly Record<string, unknown>[];
      readonly editPlan?: ProjectedEditPlan;
      readonly approvalDecision?: string;
      readonly recovery?: Record<string, unknown>;
      readonly contextText?: string;
    }
  | {
      readonly id: string;
      readonly kind: "status";
      readonly turnID: string;
      readonly sequence: number;
      readonly title: string;
      readonly text: string;
      readonly failed: boolean;
      readonly recoverable: boolean;
      readonly recovery?: {
        readonly canRetry: boolean;
        readonly canContinue: boolean;
        readonly sideEffects: string;
        readonly action: string;
      };
    }
  | {
      readonly id: string;
      readonly kind: "receipt";
      readonly turnID: string;
      readonly sequence: number;
      readonly data: Readonly<Record<string, unknown>>;
    }
  | {
      readonly id: string;
      readonly kind: "deliverables";
      readonly turnID: string;
      readonly sequence: number;
      readonly files: readonly ProjectedDeliverable[];
      readonly verification: "passed" | "failed" | "unverified";
    }
  | {
      readonly id: string;
      readonly kind: "context";
      readonly turnID: string;
      readonly sequence: number;
      readonly title: string;
      readonly summary: string;
      readonly data: Readonly<Record<string, unknown>>;
    };

export interface ConversationSnapshot {
  readonly order: readonly string[];
  readonly nodes: ReadonlyMap<string, ConversationNode>;
  readonly activeTurnID: string;
  readonly pendingApproval?: RuntimeEvent;
  readonly pendingInput?: RuntimeEvent;
  readonly revision: number;
}

const emptyConversation: ConversationSnapshot = Object.freeze({
  order: Object.freeze([]),
  nodes: new Map(),
  activeTurnID: "",
  revision: 0
});

export function emptyConversationSnapshot(): ConversationSnapshot {
  return emptyConversation;
}

export function projectConversation(
  events: readonly RuntimeEvent[]
): ConversationSnapshot {
  const projection = new ConversationProjection();
  projection.applyAll(events);
  return projection.snapshot();
}

export class ConversationProjection {
  private readonly order: string[] = [];
  private readonly nodes = new Map<string, ConversationNode>();
  private readonly outputByTurn = new Map<string, string>();
  private readonly reasoningByTurn = new Map<string, string>();
  private readonly toolByCall = new Map<string, string>();
  private readonly activeTurns = new Set<string>();
  private readonly approvals = new Map<string, RuntimeEvent>();
  private readonly inputs = new Map<string, RuntimeEvent>();
  private readonly receipts = new Map<string, Readonly<Record<string, unknown>>>();
  private readonly deliverablesByPath = new Map<string, Set<string>>();
  private revision = 0;
  private dirty = false;
  private current: ConversationSnapshot = emptyConversation;

  applyAll(events: readonly RuntimeEvent[]): void {
    for (const event of events) this.apply(event);
  }

  apply(event: RuntimeEvent): void {
    const data = event.data;
    switch (event.kind) {
      case "turn.started":
        this.activeTurns.add(event.turn_id);
        this.put({
          id: event.id,
          kind: "user",
          turnID: event.turn_id,
          sequence: event.sequence,
          text: stringValue(data.display_prompt ?? data.prompt)
        });
        break;
      case "turn.steered":
        {
          const outputID = this.outputByTurn.get(event.turn_id);
          const output = outputID ? this.nodes.get(outputID) : undefined;
          if (output?.kind === "assistant") {
            this.put({...output, superseded: true});
          }
        }
        this.put({
          id: event.id,
          kind: "user",
          turnID: event.turn_id,
          sequence: event.sequence,
          text: stringValue(data.prompt),
          steering: true
        });
        this.outputByTurn.set(
          event.turn_id,
          `output-${event.turn_id}-after-${event.id}`
        );
        break;
      case "output.delta":
        this.appendAssistant(event, stringValue(data.text));
        break;
      case "reasoning.delta":
        this.appendReasoning(event, stringValue(data.text));
        break;
      case "reasoning.completed":
        this.finishReasoning(event, stringValue(data.text));
        break;
      case "tool.start":
        this.startTool(event);
        break;
      case "tool.output":
        this.appendToolOutput(event);
        break;
      case "tool.result":
        this.finishTool(event);
        break;
      case "command.execution":
        this.updateCommandExecution(event);
        break;
      case "approval.required":
        this.approvals.set(requestID(event), event);
        this.applyApproval(event);
        this.touch();
        break;
      case "approval.resolved": {
        const pending = this.approvals.get(requestID(event));
        const callID = stringValue(pending?.data.call_id);
        const node = callID ? this.toolNodeForCall(callID) : undefined;
        if (node) {
          this.put({
            ...node,
            approvalDecision: stringValue(data.decision) || "resolved"
          });
        }
        this.approvals.delete(requestID(event));
        this.touch();
        break;
      }
      case "input.required":
        this.inputs.set(requestID(event), event);
        this.touch();
        break;
      case "input.resolved":
        this.inputs.delete(requestID(event));
        this.touch();
        break;
      case "turn.completed":
        this.finishTurn(event, false);
        break;
      case "turn.failed":
        this.finishTurn(event, true);
        break;
      case "turn.canceled":
        this.finishTurn(event, true);
        break;
      case "operation.rejected":
        this.put({
          id: event.id,
          kind: "status",
          turnID: event.turn_id,
          sequence: event.sequence,
          title: "Rejected",
          text: stringValue(data.message ?? data.code ?? "Operation rejected"),
          failed: true,
          recoverable: false
        });
        break;
      case "turn.verification":
        this.put({
          id: event.id,
          kind: "status",
          turnID: event.turn_id,
          sequence: event.sequence,
          title: "Verification",
          text: stringValue(data.verdict ?? data.status ?? "Recorded"),
          failed: data.verdict === "failed" || data.status === "failed",
          recoverable: false
        });
        break;
      case "turn.receipt":
        this.receipts.set(event.turn_id, Object.freeze({...data}));
        this.markPriorDeliverablesStale(
          event.thread_id,
          event.turn_id,
          data.changes
        );
        this.put({
          id: event.id,
          kind: "receipt",
          turnID: event.turn_id,
          sequence: event.sequence,
          data: Object.freeze({...data})
        });
        break;
      case "thread.compacted":
      case "turn.compaction":
        this.put({
          id: event.id,
          kind: "context",
          turnID: event.turn_id,
          sequence: event.sequence,
          title: event.kind === "thread.compacted" ? "Context compacted" : "Compaction",
          summary: stringValue(data.summary ?? data.reason ?? "Context window updated"),
          data: Object.freeze({...data})
        });
        break;
    }
  }

  snapshot(): ConversationSnapshot {
    if (!this.dirty) return this.current;
    this.revision += 1;
    this.current = Object.freeze({
      order: Object.freeze([...this.order]),
      nodes: new Map(this.nodes),
      activeTurnID: [...this.activeTurns].at(-1) ?? "",
      pendingApproval: [...this.approvals.values()].at(-1),
      pendingInput: [...this.inputs.values()].at(-1),
      revision: this.revision
    });
    this.dirty = false;
    return this.current;
  }

  private appendAssistant(event: RuntimeEvent, delta: string): void {
    const id = this.outputByTurn.get(event.turn_id) ?? `output-${event.turn_id}`;
    const previous = this.nodes.get(id);
    const text = previous?.kind === "assistant" ? previous.text + delta : delta;
    this.outputByTurn.set(event.turn_id, id);
    this.put({
      id,
      kind: "assistant",
      turnID: event.turn_id,
      sequence: previous?.sequence ?? event.sequence,
      text
    });
  }

  private appendReasoning(event: RuntimeEvent, delta: string): void {
    const key = reasoningKey(event);
    const id = this.reasoningByTurn.get(key) ?? `reasoning-${key}`;
    const previous = this.nodes.get(id);
    const text = previous?.kind === "reasoning" ? previous.text + delta : delta;
    this.reasoningByTurn.set(key, id);
    this.put({
      id,
      kind: "reasoning",
      turnID: event.turn_id,
      sequence: previous?.sequence ?? event.sequence,
      text,
      summary: lastNonEmptyLine(text) || "Thinking",
      running: this.activeTurns.has(event.turn_id)
    });
  }

  private finishReasoning(event: RuntimeEvent, text: string): void {
    const key = reasoningKey(event);
    const currentID = this.reasoningByTurn.get(key);
    const current = currentID ? this.nodes.get(currentID) : undefined;
    const id = current?.kind === "reasoning"
      ? current.id
      : `reasoning-${event.id}`;
    this.put({
      id,
      kind: "reasoning",
      turnID: event.turn_id,
      sequence: current?.sequence ?? event.sequence,
      text,
      summary: firstNonEmptyLine(text) || "Thinking",
      running: false
    });
    this.reasoningByTurn.delete(key);
  }

  private startTool(event: RuntimeEvent): void {
    const tool = stringValue(event.data.tool) || "Tool";
    if (tool === "turn_complete" || tool === "request_user_input") return;
    const callID = stringValue(event.data.call_id) || event.id;
    const id = `tool-${callID}`;
    const args = event.data.arguments;
    const editPlan = editPlanFromArguments(tool, args);
    this.toolByCall.set(callID, id);
    this.put({
      id,
      kind: "tool",
      turnID: event.turn_id,
      sequence: event.sequence,
      callID,
      tool,
      variant: editPlan ? "diff" : toolVariant(tool, event.data),
      title: toolTitle(tool),
      summary: toolSummary(tool, args),
      state: "running",
      arguments: args,
      output: "",
      errorSummary: "",
      truncated: false,
      changes: [],
      ...(editPlan ? {editPlan} : {})
    });
  }

  private appendToolOutput(event: RuntimeEvent): void {
    const node = this.toolNode(event);
    if (!node) return;
    this.put({
      ...node,
      output: node.output + stringValue(event.data.chunk)
    });
  }

  private finishTool(event: RuntimeEvent): void {
    const node = this.toolNode(event);
    if (!node) return;
    const output = stringValue(event.data.output) || node.output;
    const failed = Boolean(event.data.is_error);
    const changes = Array.isArray(event.data.changes)
      ? event.data.changes.filter(isRecord)
      : [];
    this.put({
      ...node,
      variant: changes.length > 0 ? "diff" : node.variant,
      summary: failed
        ? firstNonEmptyLine(output) || "Tool failed"
        : changes.length > 0
          ? changeSummary(changes)
          : node.summary,
      state: failed ? "failed" : "completed",
      output,
      errorSummary: failed ? firstNonEmptyLine(output) || "Tool failed" : "",
      execution: isRecord(event.data.execution) ? event.data.execution : undefined,
      truncated: Boolean(event.data.truncated),
      changes,
      recovery: isRecord(event.data.recovery) ? event.data.recovery : undefined,
      contextText: output || undefined
    });
  }

  private applyApproval(event: RuntimeEvent): void {
    const callID = stringValue(event.data.call_id);
    const node = this.toolNodeForCall(callID);
    if (!node) return;
    const editPlan = projectEditPlan(event.data.edit_plan);
    this.put({
      ...node,
      variant: editPlan ? "diff" : node.variant,
      ...(editPlan ? {editPlan} : {})
    });
  }

  private toolNodeForCall(callID: string) {
    const id = this.toolByCall.get(callID);
    const node = id ? this.nodes.get(id) : undefined;
    return node?.kind === "tool" ? node : undefined;
  }

  private updateCommandExecution(event: RuntimeEvent): void {
    const node = this.toolNode(event);
    if (!node) return;
    const exitCode = numberValue(event.data.exit_code);
    const durationMS = numberValue(event.data.duration_ms);
    this.put({
      ...node,
      command: {
        command: stringValue(event.data.command),
        status: stringValue(event.data.status),
        ...(exitCode === undefined ? {} : {exitCode}),
        ...(durationMS === undefined ? {} : {durationMS})
      }
    });
  }

  private finishTurn(event: RuntimeEvent, failed: boolean): void {
    this.activeTurns.delete(event.turn_id);
    for (const [key, pending] of this.approvals) {
      if (pending.turn_id === event.turn_id) this.approvals.delete(key);
    }
    for (const [key, pending] of this.inputs) {
      if (pending.turn_id === event.turn_id) this.inputs.delete(key);
    }
    for (const [key, reasoningID] of this.reasoningByTurn) {
      const reasoning = this.nodes.get(reasoningID);
      if (reasoning?.kind !== "reasoning" ||
          reasoning.turnID !== event.turn_id) {
        continue;
      }
      this.put({...reasoning, running: false});
      this.reasoningByTurn.delete(key);
    }
    if (event.kind === "turn.completed") {
      const finalText = stringValue(event.data.text ?? event.data.summary);
      const outputID = this.outputByTurn.get(event.turn_id);
      const output = outputID ? this.nodes.get(outputID) : undefined;
      if (output?.kind === "assistant" && finalText) {
        this.put({...output, text: finalText});
      } else if (finalText) {
        const id = `output-${event.turn_id}`;
        this.outputByTurn.set(event.turn_id, id);
        this.put({
          id,
          kind: "assistant",
          turnID: event.turn_id,
          sequence: event.sequence,
          text: finalText
        });
      }
      this.putDeliverables(event);
      this.touch();
      return;
    }
    this.put({
      id: `${event.id}-status`,
      kind: "status",
      turnID: event.turn_id,
      sequence: event.sequence,
      title: event.kind === "turn.canceled" ? "Canceled" : "Failed",
      text: stringValue(
        event.data.outcome ??
        event.data.message ??
        event.data.reason ??
        "Turn did not complete"
      ),
      failed,
      recoverable: failed,
      recovery: failed ? recoveryOptions(
        event,
        this.receipts.get(event.turn_id)
      ) : undefined
    });
    this.putDeliverables(event);
  }

  private markPriorDeliverablesStale(
    threadID: string,
    turnID: string,
    value: unknown
  ): void {
    if (!Array.isArray(value)) return;
    for (const change of value) {
      if (!isRecord(change)) continue;
      const path = stringValue(change.path);
      if (!path) continue;
      for (const id of this.deliverablesByPath.get(
        deliverablePathKey(threadID, path)
      ) ?? []) {
        const node = this.nodes.get(id);
        if (node?.kind !== "deliverables" || node.turnID === turnID) continue;
        this.put({
          ...node,
          files: Object.freeze(node.files.map((file) =>
            file.path === path ? Object.freeze({...file, stale: true}) : file
          ))
        });
      }
    }
  }

  private putDeliverables(event: RuntimeEvent): void {
    const receipt = this.receipts.get(event.turn_id);
    if (!receipt || !Array.isArray(receipt.changes) || receipt.changes.length === 0) {
      return;
    }
    const id = `deliverables-${event.turn_id}`;
    const files = receipt.changes.flatMap((change) => {
      if (!isRecord(change)) return [];
      const path = stringValue(change.path);
      if (!path) return [];
      const tool = this.toolForChange(event.turn_id, path, stringValue(change.tool));
      return [Object.freeze({
        path,
        tool: stringValue(change.tool) || tool?.tool || "workspace",
        kind: stringValue(change.kind) || "modified",
        added: numberValue(change.added) ?? 0,
        removed: numberValue(change.removed) ?? 0,
        summary: stringValue(change.summary),
        callID: tool?.callID,
        diff: tool?.editPlan?.files.find((file) => file.path === path),
        stale: false
      })];
    });
    if (files.length === 0) return;
    this.put({
      id,
      kind: "deliverables",
      turnID: event.turn_id,
      sequence: event.sequence,
      files: Object.freeze(files),
      verification: verificationState(receipt.verification)
    });
    for (const file of files) {
      const key = deliverablePathKey(event.thread_id, file.path);
      const ids = this.deliverablesByPath.get(key) ?? new Set<string>();
      ids.add(id);
      this.deliverablesByPath.set(key, ids);
    }
  }

  private toolForChange(turnID: string, path: string, tool: string) {
    return [...this.nodes.values()].reverse().find((node) =>
      node.kind === "tool" &&
      node.turnID === turnID &&
      (!tool || node.tool === tool) &&
      (
        node.changes.some((change) => stringValue(change.path) === path) ||
        node.editPlan?.files.some((file) => file.path === path)
      )
    ) as Extract<ConversationNode, {kind: "tool"}> | undefined;
  }

  private toolNode(event: RuntimeEvent): Extract<ConversationNode, {kind: "tool"}> | undefined {
    const callID = stringValue(event.data.call_id);
    const id = this.toolByCall.get(callID);
    const node = id ? this.nodes.get(id) : undefined;
    return node?.kind === "tool" ? node : undefined;
  }

  private put(node: ConversationNode): void {
    if (!this.nodes.has(node.id)) this.order.push(node.id);
    this.nodes.set(node.id, Object.freeze(node));
    this.touch();
  }

  private touch(): void {
    this.dirty = true;
  }
}

function requestID(event: RuntimeEvent): string {
  return stringValue(event.data.request_id) || event.id;
}

function reasoningKey(event: RuntimeEvent): string {
  const sampleID = stringValue(event.data.sample_id);
  return `${event.turn_id}:${sampleID || "active"}`;
}

function deliverablePathKey(threadID: string, path: string): string {
  return `${threadID}\u0000${path}`;
}

function stringValue(value: unknown): string {
  return value === undefined || value === null ? "" : String(value);
}

function numberValue(value: unknown): number | undefined {
  return typeof value === "number" && Number.isFinite(value)
    ? value
    : undefined;
}

function firstNonEmptyLine(value: string): string {
  return value.split(/\r?\n/).map((line) => line.trim()).find(Boolean) ?? "";
}

function lastNonEmptyLine(value: string): string {
  const lines = value.split(/\r?\n/);
  for (let index = lines.length - 1; index >= 0; index -= 1) {
    const line = lines[index]?.trim();
    if (line) return line;
  }
  return "";
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function recoveryOptions(
  event: RuntimeEvent,
  receipt: Readonly<Record<string, unknown>> | undefined
) {
  const fault = isRecord(event.data.fault) ? event.data.fault : undefined;
  const disposition = stringValue(fault?.disposition);
  let sideEffects = stringValue(fault?.side_effects) || "unknown";
  if ((sideEffects === "unknown" || !sideEffects) && receipt) {
    const changes = Array.isArray(receipt.changes) ? receipt.changes : [];
    const workspace = isRecord(receipt.workspace_outcome)
      ? receipt.workspace_outcome
      : undefined;
    if (changes.length > 0 || workspace?.changed === true) {
      sideEffects = "committed";
    }
  }
  const legacy = !disposition;
  return Object.freeze({
    canRetry: event.kind === "turn.canceled" ||
      disposition === "retry_turn" ||
      legacy,
    canContinue: event.kind === "turn.canceled" ||
      disposition === "resume_turn" ||
      disposition === "retry_turn" ||
      disposition === "fail_turn" ||
      legacy,
    sideEffects,
    action: stringValue(fault?.recovery_action)
  });
}

function verificationState(
  value: unknown
): "passed" | "failed" | "unverified" {
  if (!isRecord(value)) return "unverified";
  const states = ["diagnostics", "tests", "verify"]
    .map((key) => stringValue(value[key]))
    .filter(Boolean);
  if (states.includes("failed")) return "failed";
  return states.includes("passed") ? "passed" : "unverified";
}

export function projectEditPlan(value: unknown): ProjectedEditPlan | undefined {
  if (!isRecord(value) || !Array.isArray(value.files)) return undefined;
  const files = value.files.flatMap((entry) => {
    if (!isRecord(entry) || typeof entry.path !== "string") return [];
    return [{
      path: entry.path,
      kind: stringValue(entry.kind) || "modified",
      before: stringValue(entry.before),
      after: stringValue(entry.after),
      beforeExists: Boolean(entry.before_exists),
      afterExists: Boolean(entry.after_exists)
    }];
  });
  if (files.length === 0) return undefined;
  return Object.freeze({
    id: stringValue(value.id),
    diff: stringValue(value.diff),
    files: Object.freeze(files)
  });
}

function editPlanFromArguments(
  tool: string,
  value: unknown
): ProjectedEditPlan | undefined {
  if (tool !== "file_edit" && tool !== "edit_file") return undefined;
  const input = isRecord(value) ? value : undefined;
  const path = stringValue(input?.path);
  const before = stringValue(input?.old);
  const after = stringValue(input?.new);
  if (!path || !before || before === after) return undefined;
  return Object.freeze({
    id: "",
    diff: "",
    files: Object.freeze([{
      path,
      kind: "modified",
      before,
      after,
      beforeExists: true,
      afterExists: true
    }])
  });
}

function toolVariant(tool: string, data: Record<string, unknown>): ToolVariant {
  if (Array.isArray(data.changes) && data.changes.length > 0) return "diff";
  switch (tool) {
    case "file_read":
    case "result_get":
    case "read":
    case "read_file":
      return "read";
    case "text_search":
    case "search_text":
    case "search_project":
    case "search_files":
    case "file_list":
    case "file_search":
    case "symbol_search":
    case "grep":
    case "glob":
      return "search";
    case "shell_read":
    case "exec_command":
    case "shell":
      return "shell";
    case "file_write":
    case "write_file":
    case "file_edit":
    case "edit_file":
    case "apply_patch":
      return "write";
  }
  if (tool.includes("agent")) return "agent";
  return "generic";
}

function toolTitle(tool: string): string {
  switch (tool) {
    case "file_read":
    case "read":
    case "read_file":
      return "Read";
    case "exec_command":
    case "shell":
    case "shell_read":
      return "Bash";
    case "text_search":
    case "search_text":
    case "search_project":
    case "grep":
      return "Grep";
    case "file_search":
    case "search_files":
    case "file_list":
    case "glob":
      return "Glob";
    case "file_edit":
    case "edit_file":
    case "file_apply":
    case "file_patch":
    case "apply_patch":
      return "Edit";
    case "file_write":
    case "write_file":
      return "Write";
  }
  return tool
    .replaceAll("_", " ")
    .replace(/\b\w/g, (value) => value.toUpperCase());
}

function toolSummary(tool: string, value: unknown): string {
  const args = isRecord(value) ? value : {};
  if (tool === "result_get") {
    return `Read full result · ${readableArg(args, ["handle", "result_id", "id"]) || "result handle"}`;
  }
  const summary = readableArg(args, [
    "description", "path", "file_path", "query", "pattern", "cmd", "command",
    "symbol", "name", "role", "task"
  ]);
  if (summary) return summary.split(/\r?\n/, 1)[0]!;
  const values = Object.values(args);
  const first = values.find((entry) =>
    typeof entry === "string" || typeof entry === "number"
  );
  return first === undefined ? "Waiting for result" : String(first);
}

function readableArg(
  args: Record<string, unknown>,
  keys: readonly string[]
): string {
  for (const key of keys) {
    const value = args[key];
    if (typeof value === "string" && value.trim()) return value.trim();
    if (typeof value === "number") return String(value);
  }
  return "";
}

function changeSummary(changes: readonly Record<string, unknown>[]): string {
  let added = 0;
  let removed = 0;
  for (const change of changes) {
    added += Number(change.added ?? change.added_lines ?? 0);
    removed += Number(change.removed ?? change.removed_lines ?? 0);
  }
  return `+${added} -${removed} · ${changes.length} file${changes.length === 1 ? "" : "s"}`;
}
