import type {
  SessionDelete,
  SessionLifecyclePatch,
  SessionLifecycleUpdate,
  SessionList,
} from "../protocol/generated.js";

type JsonObject = Readonly<Record<string, unknown>>;

const lifecycleStatuses = new Set([
  "idle",
  "running",
  "awaiting_approval",
  "awaiting_input",
  "completed",
  "failed",
  "interrupted",
]);

export type SessionLifecycleSummary = SessionList["sessions"][number];
export type {
  SessionDelete,
  SessionLifecyclePatch,
  SessionLifecycleUpdate,
  SessionList,
};

export interface SessionListOptions {
  readonly query?: string;
  readonly includeArchived?: boolean;
  readonly pinnedOnly?: boolean;
  readonly status?: string;
  readonly limit?: number;
}

export interface SessionLifecycleTransport {
  request(method: string, params?: unknown): Promise<unknown>;
}

export class SessionLifecycleCommands {
  readonly #transport: SessionLifecycleTransport;

  public constructor(transport: SessionLifecycleTransport) {
    this.#transport = transport;
  }

  public async list(options: SessionListOptions = {}): Promise<SessionList> {
    const query = options.query?.trim() ?? "";
    if (query.length > 256 || query.includes("\0")) {
      throw new Error("session query is invalid");
    }
    const limit = options.limit ?? 100;
    if (!Number.isSafeInteger(limit) || limit < 1 || limit > 1000) {
      throw new Error("session list limit is invalid");
    }
    if (options.status !== undefined &&
      !lifecycleStatuses.has(options.status)) {
      throw new Error("session status filter is invalid");
    }
    return decodeSessionList(await this.#transport.request("session/list", {
      query,
      includeArchived: options.includeArchived ?? false,
      pinnedOnly: options.pinnedOnly ?? false,
      ...(options.status === undefined ? {} : { status: options.status }),
      limit,
    }));
  }

  public async status(sessionId: string): Promise<SessionLifecycleSummary> {
    return decodeSessionSummary(await this.#transport.request(
      "session/status",
      { sessionId: identifier(sessionId, "session id") },
    ));
  }

  public async update(
    sessionId: string,
    expectedRevision: number,
    patch: SessionLifecyclePatch,
  ): Promise<SessionLifecycleUpdate> {
    return decodeSessionLifecycleUpdate(await this.#transport.request(
      "session/lifecycle/update",
      {
        sessionId: identifier(sessionId, "session id"),
        expectedRevision: positiveInteger(
          expectedRevision,
          "expected lifecycle revision",
        ),
        patch: validateLifecyclePatch(patch),
      },
    ));
  }

  public async delete(
    sessionId: string,
    expectedRevision: number,
  ): Promise<SessionDelete> {
    return decodeSessionDelete(await this.#transport.request(
      "session/delete",
      {
        sessionId: identifier(sessionId, "session id"),
        expectedRevision: positiveInteger(
          expectedRevision,
          "expected lifecycle revision",
        ),
      },
    ));
  }
}

export function decodeSessionList(value: unknown): SessionList {
  const object = requireObject(value, "session list");
  requireKeys(object, ["version", "sessions"], ["query", "matches"]);
  if (!Array.isArray(object["sessions"]) || object["sessions"].length > 1000) {
    throw new Error("session list entries are invalid");
  }
  const sessions = object["sessions"].map(decodeSessionSummary);
  if (new Set(sessions.map((session) => session.session_id)).size !==
    sessions.length) {
    throw new Error("session list contains duplicate identities");
  }
  const matches = object["matches"] === undefined
    ? []
    : decodeSearchMatches(object["matches"], sessions);
  const query = object["query"] === undefined
    ? ""
    : boundedText(object["query"], "session query", 256, true);
  if (query.trim().length === 0 && matches.length > 0) {
    throw new Error("session list without a query cannot contain matches");
  }
  return {
    version: lifecycleVersion(object["version"]),
    ...(object["query"] === undefined ? {} : { query }),
    sessions,
    ...(matches.length === 0 ? {} : { matches }),
  };
}

export function decodeSessionSummary(
  value: unknown,
): SessionLifecycleSummary {
  const object = requireObject(value, "session summary");
  requireKeys(object, [
    "version", "revision", "session_id", "thread_id", "title", "status",
    "pinned", "archived", "isolation", "workspace_root", "workspace_label",
    "latest_sequence", "pending_approvals", "pending_inputs", "total_tokens",
    "checkpoint_count", "cost_microunits", "cost_known", "created_at",
    "updated_at",
  ], [
    "provider", "model", "mode", "parent_thread_id", "latest_turn_id",
  ]);
  const status = boundedText(object["status"], "session status", 64);
  if (!lifecycleStatuses.has(status)) {
    throw new Error("session status is invalid");
  }
  const isolation = boundedText(object["isolation"], "session isolation", 32);
  if (isolation !== "shared" && isolation !== "worktree") {
    throw new Error("session isolation is invalid");
  }
  return {
    version: lifecycleVersion(object["version"]),
    revision: positiveInteger(object["revision"], "session revision"),
    session_id: identifier(object["session_id"], "session id"),
    thread_id: identifier(object["thread_id"], "thread id"),
    title: boundedText(object["title"], "session title", 256),
    status,
    pinned: boolean(object["pinned"], "session pinned"),
    archived: boolean(object["archived"], "session archived"),
    isolation,
    workspace_root: boundedText(
      object["workspace_root"], "session workspace root", 4096,
    ),
    workspace_label: boundedText(
      object["workspace_label"], "session workspace label", 256,
    ),
    ...optionalString(object, "provider"),
    ...optionalString(object, "model"),
    ...optionalString(object, "mode"),
    ...optionalString(object, "parent_thread_id"),
    ...optionalString(object, "latest_turn_id"),
    latest_sequence: nonNegativeInteger(
      object["latest_sequence"], "latest sequence",
    ),
    pending_approvals: nonNegativeInteger(
      object["pending_approvals"], "pending approvals",
    ),
    pending_inputs: nonNegativeInteger(
      object["pending_inputs"], "pending inputs",
    ),
    checkpoint_count: nonNegativeInteger(
      object["checkpoint_count"], "checkpoint count",
    ),
    total_tokens: nonNegativeInteger(object["total_tokens"], "total tokens"),
    cost_microunits: nonNegativeInteger(
      object["cost_microunits"], "cost microunits",
    ),
    cost_known: boolean(object["cost_known"], "cost known"),
    created_at: timestamp(object["created_at"], "session created_at"),
    updated_at: timestamp(object["updated_at"], "session updated_at"),
  };
}

function decodeSearchMatches(
  value: unknown,
  sessions: SessionList["sessions"],
): NonNullable<SessionList["matches"]> {
  if (!Array.isArray(value) || value.length > 1000) {
    throw new Error("session search matches are invalid");
  }
  const sessionIDs = new Set(sessions.map((session) => session.session_id));
  const keys = new Set<string>();
  return value.map((candidate) => {
    const object = requireObject(candidate, "session search match");
    requireKeys(object, ["session_id", "turn_id", "kind"], ["snippet"]);
    const sessionId = identifier(object["session_id"], "search match session");
    const turnId = identifier(object["turn_id"], "search match turn");
    if (!sessionIDs.has(sessionId)) {
      throw new Error("session search match has no listed Session");
    }
    const key = `${sessionId}\0${turnId}`;
    if (keys.has(key)) throw new Error("session search match is duplicated");
    keys.add(key);
    const kind = boundedText(object["kind"], "search match kind", 32);
    if (!new Set([
      "title", "user_request", "agent_output", "path", "symbol", "content",
    ]).has(kind)) {
      throw new Error("session search match kind is invalid");
    }
    return {
      session_id: sessionId,
      turn_id: turnId,
      kind,
      ...(object["snippet"] === undefined
        ? {}
        : {
            snippet: boundedText(
              object["snippet"], "search match snippet", 2048, true,
            ),
          }),
    };
  });
}

export function decodeSessionLifecycleUpdate(
  value: unknown,
): SessionLifecycleUpdate {
  const object = requireObject(value, "session lifecycle update");
  requireKeys(object, ["session"]);
  return { session: decodeSessionSummary(object["session"]) };
}

export function decodeSessionDelete(value: unknown): SessionDelete {
  const object = requireObject(value, "session delete");
  requireKeys(object, ["version", "session_id", "thread_id", "deleted_at"]);
  return {
    version: lifecycleVersion(object["version"]),
    session_id: identifier(object["session_id"], "deleted session id"),
    thread_id: identifier(object["thread_id"], "deleted thread id"),
    deleted_at: timestamp(object["deleted_at"], "deleted_at"),
  };
}

export function validateLifecyclePatch(
  patch: SessionLifecyclePatch,
): SessionLifecyclePatch {
  if (Object.keys(patch).length === 0) {
    throw new Error("session lifecycle patch must not be empty");
  }
  if (patch.title !== undefined) {
    boundedText(patch.title.trim(), "session title", 256);
  }
  return patch;
}

function optionalString(
  object: JsonObject,
  key: string,
): Readonly<Record<string, string>> {
  return object[key] === undefined
    ? {}
    : { [key]: boundedText(object[key], key, 256) };
}

function requireObject(value: unknown, name: string): JsonObject {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error(`${name} is invalid`);
  }
  return value as JsonObject;
}

function requireKeys(
  value: JsonObject,
  required: readonly string[],
  optional: readonly string[] = [],
): void {
  const allowed = new Set([...required, ...optional]);
  if (Object.keys(value).some((key) => !allowed.has(key))) {
    throw new Error("session lifecycle contains unknown fields");
  }
  for (const key of required) {
    if (!Object.hasOwn(value, key)) {
      throw new Error(`session lifecycle is missing ${key}`);
    }
  }
}

function boundedText(
  value: unknown,
  name: string,
  maximum: number,
  allowEmpty = false,
): string {
  if (typeof value !== "string" || value.length > maximum ||
    (!allowEmpty && value.trim().length === 0) || value.includes("\0")) {
    throw new Error(`${name} is invalid`);
  }
  return value;
}

function identifier(value: unknown, name: string): string {
  return boundedText(value, name, 256);
}

function positiveInteger(value: unknown, name: string): number {
  const result = nonNegativeInteger(value, name);
  if (result === 0) throw new Error(`${name} is invalid`);
  return result;
}

function lifecycleVersion(value: unknown): 1 {
  if (value !== 1) throw new Error("session lifecycle version is unsupported");
  return value;
}

function nonNegativeInteger(value: unknown, name: string): number {
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0) {
    throw new Error(`${name} is invalid`);
  }
  return value;
}

function boolean(value: unknown, name: string): boolean {
  if (typeof value !== "boolean") throw new Error(`${name} is invalid`);
  return value;
}

function timestamp(value: unknown, name: string): string {
  const result = boundedText(value, name, 64);
  if (!Number.isFinite(Date.parse(result))) {
    throw new Error(`${name} is invalid`);
  }
  return result;
}
