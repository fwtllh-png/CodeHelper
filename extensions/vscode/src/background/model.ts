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

export class BackgroundProjector {
  readonly #threads = new Map<string, ThreadRow>();
  readonly #agents = new Map<string, AgentRow>();
  readonly #integrations = new Map<string, IntegrationRow>();
  readonly #tasks = new Map<string, TaskRow>();
  readonly #approvals = new Map<string, ApprovalRow>();
  readonly #notified = new Set<string>();
  #tasksInitialized = false;
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
    return { key, title, detail, failed };
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
