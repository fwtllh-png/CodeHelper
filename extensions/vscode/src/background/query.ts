import type {
  AgentRow,
  TaskRow,
  ThreadRow,
  UsageRollup,
} from "./model.js";

export interface BackgroundTransport {
  request(method: string, params?: unknown): Promise<unknown>;
}

export class BackgroundQuery {
  readonly #transport: BackgroundTransport;

  public constructor(transport: BackgroundTransport) {
    this.#transport = transport;
  }

  public async threads(): Promise<readonly ThreadRow[]> {
    const value = object(await this.#transport.request("thread/list", {
      limit: 1000,
    }), "thread/list result");
    return array(value["threads"], "threads").map(decodeThread);
  }

  public async agents(): Promise<readonly AgentRow[]> {
    const value = object(await this.#transport.request("agent/list", {
      includeClosed: true, limit: 1000,
    }), "agent/list result");
    return array(value["agents"], "agents").map(decodeAgent);
  }

  public async tasks(): Promise<readonly TaskRow[]> {
    const value = object(await this.#transport.request("task/list", {
      limit: 1000,
    }), "task/list result");
    return array(value["tasks"], "tasks").map(decodeTask);
  }

  public async usage(): Promise<UsageRollup> {
    const value = object(await this.#transport.request("usage/query", {
      limit: 1000,
    }), "usage/query result");
    return decodeUsage(object(value["rollup"], "usage rollup"));
  }
}

function decodeThread(value: unknown): ThreadRow {
  const row = object(value, "thread");
  return {
    id: string(row["id"], "thread.id"),
    sessionId: string(row["session_id"], "thread.session_id"),
    title: optionalString(row["title"], "thread.title"),
    status: string(row["status"], "thread.status"),
    updatedAt: string(row["updated_at"], "thread.updated_at"),
  };
}

function decodeAgent(value: unknown): AgentRow {
  const row = object(value, "agent");
  return {
    id: string(row["id"], "agent.id"),
    path: string(row["path"], "agent.path"),
    revision: number(row["revision"], "agent.revision"),
    workspace: string(row["workspace"], "agent.workspace"),
    sessionId: string(row["session_id"], "agent.session_id"),
    threadId: string(row["thread_id"], "agent.thread_id"),
    parentId: optionalString(row["parent_id"], "agent.parent_id"),
    parentPath: string(row["parent_path"], "agent.parent_path"),
    role: string(row["role"], "agent.role"),
    status: string(row["status"], "agent.status"),
    lastMessage: optionalString(row["last_message"], "agent.last_message"),
    closed: boolean(row["closed"], "agent.closed"),
  };
}

function decodeTask(value: unknown): TaskRow {
  const row = object(value, "task");
  return {
    id: string(row["id"], "task.id"),
    sessionId: string(row["session_id"], "task.session_id"),
    threadId: optionalString(row["thread_id"], "task.thread_id"),
    turnId: optionalString(row["turn_id"], "task.turn_id"),
    kind: string(row["kind"], "task.kind"),
    state: string(row["state"], "task.state"),
    executor: optionalString(row["executor"], "task.executor"),
    attempt: number(row["attempt"], "task.attempt"),
    maxAttempts: number(row["max_attempts"], "task.max_attempts"),
    failureReason: optionalString(
      row["failure_reason"], "task.failure_reason",
    ),
    updatedAt: string(row["updated_at"], "task.updated_at"),
  };
}

function decodeUsage(row: Readonly<Record<string, unknown>>): UsageRollup {
  return {
    turns: number(row["turns"], "usage.turns"),
    calls: number(row["calls"], "usage.calls"),
    inputTokens: number(row["input_tokens"], "usage.input_tokens"),
    outputTokens: number(row["output_tokens"], "usage.output_tokens"),
    reasoningTokens: number(row["reasoning_tokens"], "usage.reasoning_tokens"),
    cachedTokens: number(row["cached_tokens"], "usage.cached_tokens"),
    totalTokens: number(row["total_tokens"], "usage.total_tokens"),
    costMicrounits: number(row["cost_microunits"], "usage.cost_microunits"),
    costKnown: boolean(row["cost_known"], "usage.cost_known"),
  };
}

function object(
  value: unknown,
  name: string,
): Readonly<Record<string, unknown>> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error(`${name} must be an object`);
  }
  return value as Readonly<Record<string, unknown>>;
}

function array(value: unknown, name: string): readonly unknown[] {
  if (!Array.isArray(value)) throw new Error(`${name} must be an array`);
  return value;
}

function string(value: unknown, name: string): string {
  if (typeof value !== "string" || value === "") {
    throw new Error(`${name} must be a non-empty string`);
  }
  return value;
}

function optionalString(value: unknown, name: string): string {
  if (value === undefined) return "";
  if (typeof value !== "string") throw new Error(`${name} must be a string`);
  return value;
}

function number(value: unknown, name: string): number {
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0) {
    throw new Error(`${name} must be a non-negative safe integer`);
  }
  return value;
}

function boolean(value: unknown, name: string): boolean {
  if (typeof value !== "boolean") throw new Error(`${name} must be a boolean`);
  return value;
}
