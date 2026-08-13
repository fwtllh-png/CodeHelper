import {
  isUnknownEvent,
  type DecodedEvent,
} from "../protocol/decode.js";

export type BackgroundView =
  | "threads"
  | "agents"
  | "tasks"
  | "jobs"
  | "approvals"
  | "usage";

export interface ThreadRow {
  readonly id: string;
  readonly sessionId: string;
  readonly title: string;
  readonly status: string;
  readonly updatedAt: string;
}

export interface AgentRow {
  readonly id: string;
  readonly path: string;
  readonly revision: number;
  readonly workspace: string;
  readonly sessionId: string;
  readonly threadId: string;
  readonly parentId: string;
  readonly parentPath: string;
  readonly role: string;
  readonly status: string;
  readonly lastMessage: string;
  readonly closed: boolean;
}

export interface IntegrationRow {
  readonly agentId: string;
  readonly agentPath: string;
  readonly parentPath: string;
  readonly status: string;
  readonly previewDigest: string;
  readonly paths: readonly string[];
  readonly conflicts: readonly string[];
  readonly message: string;
  readonly changedPaths: readonly string[];
  readonly verification: string;
  readonly appliedAt: string;
}

export interface AgentTimelineRow {
  readonly sequence: number;
  readonly agentId: string;
  readonly agentPath: string;
  readonly kind: "spawn" | "status" | "message" | "approval" | "integration";
  readonly status: string;
  readonly message: string;
}

export interface TaskRow {
  readonly id: string;
  readonly sessionId: string;
  readonly threadId: string;
  readonly turnId: string;
  readonly kind: string;
  readonly state: string;
  readonly executor: string;
  readonly attempt: number;
  readonly maxAttempts: number;
  readonly failureReason: string;
  readonly updatedAt: string;
}

export interface ApprovalRow {
  readonly requestId: string;
  readonly turnId: string;
  readonly tool: string;
  readonly resources: readonly string[];
  readonly expiresAt: string;
  readonly agentPath: string;
  readonly agentRole: string;
  readonly parentPath: string;
}

export interface UsageRollup {
  readonly turns: number;
  readonly calls: number;
  readonly inputTokens: number;
  readonly outputTokens: number;
  readonly reasoningTokens: number;
  readonly cachedTokens: number;
  readonly totalTokens: number;
  readonly costMicrounits: number;
  readonly costKnown: boolean;
}

export interface BackgroundSnapshot {
  readonly threads: readonly ThreadRow[];
  readonly agents: readonly AgentRow[];
  readonly integrations: readonly IntegrationRow[];
  readonly agentTimeline: readonly AgentTimelineRow[];
  readonly tasks: readonly TaskRow[];
  readonly jobs: readonly TaskRow[];
  readonly approvals: readonly ApprovalRow[];
  readonly usage: UsageRollup;
}

export interface TerminalNotice {
  readonly key: string;
  readonly title: string;
  readonly detail: string;
  readonly failed: boolean;
}

const terminalTaskStates = new Set(["completed", "failed", "canceled"]);
const terminalAgentStates = new Set([
  "completed", "failed", "interrupted", "integrated",
  "integration_failed", "closed",
]);
const maxAgentTimeline = 512;
const maxIntegrationCandidates = 256;
const maxNoticeKeys = 1024;

export class BackgroundProjector {
  readonly #threads = new Map<string, ThreadRow>();
  readonly #agents = new Map<string, AgentRow>();
  readonly #integrations = new Map<string, IntegrationRow>();
  readonly #agentTimeline: AgentTimelineRow[] = [];
  readonly #tasks = new Map<string, TaskRow>();
  readonly #approvals = new Map<string, ApprovalRow>();
  readonly #notified = new Set<string>();
  #tasksInitialized = false;
  #lastWorkspaceSequence = 0;
  #usage: UsageRollup = emptyUsage();

  public replaceThreads(rows: readonly ThreadRow[]): void {
    replace(this.#threads, rows, (row) => row.id);
  }

  public replaceAgents(rows: readonly AgentRow[]): void {
    replace(this.#agents, rows, (row) => row.id);
  }

  public replaceTasks(rows: readonly TaskRow[]): readonly TerminalNotice[] {
    const notices: TerminalNotice[] = [];
    if (this.#tasksInitialized) {
      for (const row of rows) {
        const previous = this.#tasks.get(row.id);
        if (previous !== undefined &&
          !terminalTaskStates.has(previous.state) &&
          terminalTaskStates.has(row.state)) {
          const notice = this.#notice(
            `task:${row.id}:${row.state}`,
            `CodeHelper task ${row.state}`,
            row.failureReason === "" ? `${row.kind} (${row.id})` : row.failureReason,
            row.state === "failed",
          );
          if (notice !== undefined) notices.push(notice);
        }
      }
    }
    replace(this.#tasks, rows, (row) => row.id);
    this.#tasksInitialized = true;
    return notices;
  }

  public replaceUsage(usage: UsageRollup): void {
    this.#usage = usage;
  }

  public applyEvent(
    event: DecodedEvent,
    replayed: boolean,
  ): readonly TerminalNotice[] {
    if (isUnknownEvent(event)) return [];
    if (isAgentWorkspaceEvent(event)) {
      if (event.sequence <= this.#lastWorkspaceSequence) return [];
      this.#lastWorkspaceSequence = event.sequence;
    }
    switch (event.kind) {
      case "approval.required":
        this.#approvals.set(event.data.request_id, {
          requestId: event.data.request_id,
          turnId: event.turn_id,
          tool: event.data.tool,
          resources: event.data.resources.map(
            (resource) =>
              `${resource.access}:${resource.path ?? resource.id ?? resource.kind}`,
          ),
          expiresAt: event.data.expires_at,
          agentPath: event.data.source?.agent_path ?? "",
          agentRole: event.data.source?.role ?? "",
          parentPath: event.data.source?.parent_path ?? "",
        });
        if (event.data.source?.kind === "agent") {
          this.#appendAgentTimeline({
            sequence: event.sequence,
            agentId: event.data.source.agent_id,
            agentPath: event.data.source.agent_path,
            kind: "approval",
            status: "required",
            message: event.data.tool,
          });
        }
        return [];
      case "approval.resolved":
        this.#approvals.delete(event.data.request_id);
        return [];
      case "turn.completed":
      case "turn.failed":
      case "turn.canceled": {
        if (replayed) return [];
        const state = event.kind.slice("turn.".length);
        const notice = this.#notice(
          `turn:${event.turn_id}:${state}`,
          `CodeHelper turn ${state}`,
          event.turn_id,
          event.kind === "turn.failed",
        );
        return notice === undefined ? [] : [notice];
      }
      case "agent.status": {
        const current = this.#agents.get(event.data.agent_id);
        const detail = eventAgentStatusDetail(event.data.detail);
        if (current !== undefined) {
          this.#agents.set(event.data.agent_id, {
            ...current,
            revision: detail.revision ?? current.revision,
            status: event.data.status,
            lastMessage: event.data.message ?? current.lastMessage,
            closed: event.data.status === "closed",
          });
        }
        this.#appendAgentTimeline({
          sequence: event.sequence,
          agentId: event.data.agent_id,
          agentPath: detail.path ?? current?.path ?? "",
          kind: "status",
          status: event.data.status,
          message: event.data.message ?? "",
        });
        if (replayed || !terminalAgentStates.has(event.data.status)) return [];
        const notice = this.#notice(
          `agent:${event.data.agent_id}:${event.data.status}`,
          `CodeHelper agent ${event.data.status}`,
          event.data.message === undefined || event.data.message === ""
            ? event.data.agent_id
            : event.data.message,
          event.data.status === "failed" ||
            event.data.status === "integration_failed",
        );
        return notice === undefined ? [] : [notice];
      }
      case "agent.spawned": {
        const row = eventAgentRow(event.data);
        this.#agents.set(row.id, row);
        this.#appendAgentTimeline({
          sequence: event.sequence,
          agentId: row.id,
          agentPath: row.path,
          kind: "spawn",
          status: row.status,
          message: row.role,
        });
        return [];
      }
      case "agent.message": {
        const agentId = event.data.from === "parent"
          ? event.data.to
          : event.data.from;
        this.#appendAgentTimeline({
          sequence: event.sequence,
          agentId,
          agentPath: this.#agents.get(agentId)?.path ?? "",
          kind: "message",
          status: "",
          message: eventMessage(event.data.body),
        });
        return [];
      }
      case "agent.integration": {
        const detail = integrationDetail(event.data.detail);
        this.#integrations.set(
          `${event.data.agent_id}:${event.data.preview_digest}`,
          {
            agentId: event.data.agent_id,
            agentPath: event.data.agent_path,
            parentPath: event.data.parent_path,
            status: event.data.status,
            previewDigest: event.data.preview_digest,
            paths: event.data.paths ?? [],
            conflicts: event.data.conflicts ?? [],
            message: event.data.message ?? "",
            changedPaths: detail.changedPaths,
            verification: detail.verification,
            appliedAt: detail.appliedAt,
          },
        );
        trimOldestMapEntries(this.#integrations, maxIntegrationCandidates);
        this.#appendAgentTimeline({
          sequence: event.sequence,
          agentId: event.data.agent_id,
          agentPath: event.data.agent_path,
          kind: "integration",
          status: event.data.status,
          message: event.data.message ?? "",
        });
        return [];
      }
      default:
        return [];
    }
  }

  public snapshot(): BackgroundSnapshot {
    const tasks = sorted(this.#tasks.values(), (row) => row.updatedAt);
    return {
      threads: sorted(this.#threads.values(), (row) => row.updatedAt),
      agents: sorted(this.#agents.values(), (row) => row.id),
      integrations: sorted(
        this.#integrations.values(),
        (row) => `${row.agentPath}:${row.previewDigest}`,
      ),
      agentTimeline: [...this.#agentTimeline],
      tasks,
      jobs: tasks.filter((row) => row.executor !== ""),
      approvals: sorted(this.#approvals.values(), (row) => row.expiresAt),
      usage: this.#usage,
    };
  }

  #notice(
    key: string,
    title: string,
    detail: string,
    failed: boolean,
  ): TerminalNotice | undefined {
    if (this.#notified.has(key)) return undefined;
    this.#notified.add(key);
    trimOldestSetEntries(this.#notified, maxNoticeKeys);
    return { key, title, detail, failed };
  }

  #appendAgentTimeline(row: AgentTimelineRow): void {
    this.#agentTimeline.push(row);
    if (this.#agentTimeline.length > maxAgentTimeline) {
      this.#agentTimeline.splice(
        0,
        this.#agentTimeline.length - maxAgentTimeline,
      );
    }
  }
}

function isAgentWorkspaceEvent(event: DecodedEvent): boolean {
  if (isUnknownEvent(event)) return false;
  switch (event.kind) {
    case "agent.spawned":
    case "agent.status":
    case "agent.message":
    case "agent.integration":
      return true;
    case "approval.required":
    case "approval.resolved":
      return event.data.source?.kind === "agent";
    default:
      return false;
  }
}

function integrationDetail(value: unknown): {
  readonly changedPaths: readonly string[];
  readonly verification: string;
  readonly appliedAt: string;
} {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return { changedPaths: [], verification: "", appliedAt: "" };
  }
  const candidate = value as Readonly<Record<string, unknown>>;
  const receipt = candidate["receipt"];
  if (typeof receipt !== "object" || receipt === null || Array.isArray(receipt)) {
    return {
      changedPaths: [],
      verification: verificationStatus(candidate["verification"]),
      appliedAt: "",
    };
  }
  const fields = receipt as Readonly<Record<string, unknown>>;
  const changed = fields["changed_paths"];
  return {
    changedPaths: Array.isArray(changed)
      ? changed.filter((path): path is string => typeof path === "string")
      : [],
    verification: verificationStatus(fields["verification"]),
    appliedAt: typeof fields["applied_at"] === "string"
      ? fields["applied_at"]
      : "",
  };
}

function verificationStatus(value: unknown): string {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return "";
  }
  const verify = (value as Readonly<Record<string, unknown>>)["verify"];
  return typeof verify === "string" ? verify : "";
}

function eventAgentRow(data: {
  readonly agent_id: string;
  readonly parent_id?: string;
  readonly workspace_root?: string;
  readonly session_id?: string;
  readonly role: string;
  readonly profile?: string;
  readonly stance?: string;
  readonly depth: number;
  readonly worktree?: string;
  readonly detail?: unknown;
}): AgentRow {
  const detail = record(data.detail);
  return {
    id: data.agent_id,
    path: text(detail?.["path"]) || `/root/${data.agent_id}`,
    revision: integer(detail?.["revision"]) ?? 1,
    workspace: text(detail?.["workspace"]) || data.workspace_root || "",
    sessionId: text(detail?.["session_id"]) || data.session_id || "",
    threadId: text(detail?.["thread_id"]),
    parentId: text(detail?.["parent_id"]) || data.parent_id || "",
    parentPath: text(detail?.["parent_path"]) || "/root",
    role: data.role,
    status: text(detail?.["status"]) || "requested",
    lastMessage: text(detail?.["last_message"]),
    closed: false,
  };
}

function eventAgentStatusDetail(value: unknown): {
  readonly path: string | undefined;
  readonly revision: number | undefined;
} {
  const detail = record(value);
  return {
    path: text(detail?.["path"]) || undefined,
    revision: integer(detail?.["expected_revision"]) === undefined
      ? undefined
      : (integer(detail?.["expected_revision"]) ?? 0) + 1,
  };
}

function eventMessage(value: unknown): string {
  const detail = record(value);
  const body = text(detail?.["body"]) || text(detail?.["summary"]);
  const rendered = body || (
    typeof value === "string" ? value : JSON.stringify(value)
  );
  return rendered.length <= 160 ? rendered : `${rendered.slice(0, 157)}...`;
}

function record(value: unknown): Readonly<Record<string, unknown>> | undefined {
  return typeof value === "object" && value !== null && !Array.isArray(value)
    ? value as Readonly<Record<string, unknown>>
    : undefined;
}

function text(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function integer(value: unknown): number | undefined {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0
    ? value
    : undefined;
}

function trimOldestMapEntries<K, V>(target: Map<K, V>, limit: number): void {
  while (target.size > limit) {
    const oldest = target.keys().next();
    if (oldest.done) return;
    target.delete(oldest.value);
  }
}

function trimOldestSetEntries<T>(target: Set<T>, limit: number): void {
  while (target.size > limit) {
    const oldest = target.values().next();
    if (oldest.done) return;
    target.delete(oldest.value);
  }
}

function replace<T>(
  target: Map<string, T>,
  rows: readonly T[],
  key: (row: T) => string,
): void {
  target.clear();
  for (const row of rows) target.set(key(row), row);
}

function sorted<T>(
  values: Iterable<T>,
  key: (row: T) => string,
): readonly T[] {
  return [...values].sort((left, right) => key(right).localeCompare(key(left)));
}

function emptyUsage(): UsageRollup {
  return {
    turns: 0, calls: 0, inputTokens: 0, outputTokens: 0,
    reasoningTokens: 0, cachedTokens: 0, totalTokens: 0,
    costMicrounits: 0, costKnown: true,
  };
}
