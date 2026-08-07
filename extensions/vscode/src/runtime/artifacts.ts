import type {
  CheckpointFork,
  CheckpointList,
  CheckpointRestore,
  SessionPlan,
} from "../protocol/generated.js";

type JsonObject = Readonly<Record<string, unknown>>;
export type SessionCheckpoint = CheckpointList["checkpoints"][number];

export interface ArtifactTransport {
  request(method: string, params?: unknown): Promise<unknown>;
}

export interface AcceptedPlanTurn {
  readonly operationId: string;
  readonly accepted: true;
  readonly kind: "turn.start";
  readonly threadId: string;
  readonly turnId: string;
  readonly itemId: string;
}

export class SessionArtifactCommands {
  readonly #transport: ArtifactTransport;

  public constructor(transport: ArtifactTransport) {
    this.#transport = transport;
  }

  public async checkpoints(
    sessionId: string,
    limit = 100,
  ): Promise<CheckpointList> {
    return decodeCheckpointList(await this.#transport.request(
      "checkpoint/list",
      {
        sessionId: identifier(sessionId, "session id"),
        limit: integer(limit, "checkpoint limit", 1, 1000),
      },
    ));
  }

  public async checkpoint(
    sessionId: string,
    checkpointId: string,
  ): Promise<SessionCheckpoint> {
    return decodeCheckpoint(await this.#transport.request("checkpoint/get", {
      sessionId: identifier(sessionId, "session id"),
      checkpointId: identifier(checkpointId, "checkpoint id"),
    }));
  }

  public async restore(
    sessionId: string,
    checkpointId: string,
  ): Promise<CheckpointRestore> {
    return decodeCheckpointRestore(await this.#transport.request(
      "checkpoint/restore",
      {
        sessionId: identifier(sessionId, "session id"),
        checkpointId: identifier(checkpointId, "checkpoint id"),
      },
    ));
  }

  public async fork(
    sessionId: string,
    checkpointId: string,
    title: string,
  ): Promise<CheckpointFork> {
    return decodeCheckpointFork(await this.#transport.request(
      "checkpoint/fork",
      {
        sessionId: identifier(sessionId, "session id"),
        checkpointId: identifier(checkpointId, "checkpoint id"),
        title: boundedText(title.trim(), "Fork title", 256),
      },
    ));
  }

  public async plan(sessionId: string): Promise<SessionPlan> {
    return decodeSessionPlan(await this.#transport.request("plan/get", {
      sessionId: identifier(sessionId, "session id"),
    }));
  }

  public async implementPlan(
    sessionId: string,
    planId: string,
    transition: "implement" | "autopilot",
  ): Promise<AcceptedPlanTurn> {
    return decodeAcceptedPlanTurn(await this.#transport.request(
      "plan/implement",
      {
        sessionId: identifier(sessionId, "session id"),
        planId: identifier(planId, "Plan Artifact id"),
        transition,
      },
    ));
  }
}

export function decodeCheckpointList(value: unknown): CheckpointList {
  const object = requireObject(value, "Checkpoint list");
  requireKeys(object, ["version", "session_id", "checkpoints"]);
  const protocolVersion = version(object["version"]);
  if (!Array.isArray(object["checkpoints"]) ||
    object["checkpoints"].length > 1000) {
    throw new Error("Checkpoint list is invalid");
  }
  const sessionId = identifier(object["session_id"], "Checkpoint Session");
  const checkpoints = object["checkpoints"].map(decodeCheckpoint);
  if (checkpoints.some((checkpoint) => checkpoint.session_id !== sessionId) ||
    new Set(checkpoints.map((checkpoint) => checkpoint.id)).size !==
      checkpoints.length) {
    throw new Error("Checkpoint list identity is invalid");
  }
  return { version: protocolVersion, session_id: sessionId, checkpoints };
}

export function decodeCheckpoint(value: unknown): SessionCheckpoint {
  const object = requireObject(value, "Checkpoint");
  requireKeys(object, [
    "version", "id", "session_id", "thread_id", "turn_id", "cursor", "status",
    "summary", "profile_revision", "changed_files", "external_side_effects",
    "can_restore", "can_fork", "created_at",
  ], ["parent_checkpoint_id", "change_receipt", "side_effect_note"]);
  const status = boundedText(object["status"], "Checkpoint status", 32);
  if (status !== "completed" && status !== "interrupted") {
    throw new Error("Checkpoint status is invalid");
  }
  return {
    version: version(object["version"]),
    id: identifier(object["id"], "Checkpoint id"),
    session_id: identifier(object["session_id"], "Checkpoint Session id"),
    thread_id: identifier(object["thread_id"], "Checkpoint Thread id"),
    turn_id: identifier(object["turn_id"], "Checkpoint Turn id"),
    cursor: integer(object["cursor"], "Checkpoint cursor", 0),
    status,
    summary: boundedText(object["summary"], "Checkpoint summary", 2048),
    profile_revision: integer(
      object["profile_revision"], "Checkpoint Profile Revision", 1,
    ),
    ...(object["parent_checkpoint_id"] === undefined
      ? {}
      : {
          parent_checkpoint_id: identifier(
            object["parent_checkpoint_id"], "parent Checkpoint id",
          ),
        }),
    ...(object["change_receipt"] === undefined
      ? {}
      : {
          change_receipt: decodeReceiptReference(
            object["change_receipt"],
            object["turn_id"],
            object["cursor"],
          ),
        }),
    changed_files: integer(object["changed_files"], "changed files", 0),
    external_side_effects: boolean(
      object["external_side_effects"], "external side effects",
    ),
    ...(object["side_effect_note"] === undefined
      ? {}
      : {
          side_effect_note: boundedText(
            object["side_effect_note"], "side-effect note", 2048, true,
          ),
        }),
    can_restore: boolean(object["can_restore"], "can restore"),
    can_fork: boolean(object["can_fork"], "can Fork"),
    created_at: timestamp(object["created_at"], "Checkpoint created_at"),
  };
}

function decodeReceiptReference(
  value: unknown,
  checkpointTurn: unknown,
  checkpointCursor: unknown,
): NonNullable<SessionCheckpoint["change_receipt"]> {
  const object = requireObject(value, "Checkpoint receipt reference");
  requireKeys(object, ["event_id", "turn_id", "cursor"]);
  const turnId = identifier(object["turn_id"], "receipt Turn id");
  const cursor = integer(object["cursor"], "receipt cursor", 1);
  if (turnId !== checkpointTurn ||
    typeof checkpointCursor !== "number" ||
    cursor > checkpointCursor) {
    throw new Error("Checkpoint receipt reference is inconsistent");
  }
  return {
    event_id: identifier(object["event_id"], "receipt Event id"),
    turn_id: turnId,
    cursor,
  };
}

export function decodeCheckpointRestore(value: unknown): CheckpointRestore {
  const object = requireObject(value, "Checkpoint restore");
  requireKeys(object, [
    "version", "checkpoint", "thread_id", "restored_cursor",
    "side_effects_replayed",
  ]);
  const replayed = boolean(
    object["side_effects_replayed"], "side effects replayed",
  );
  if (replayed) {
    throw new Error("Checkpoint Restore attempted to replay side effects");
  }
  return {
    version: version(object["version"]),
    checkpoint: decodeCheckpoint(object["checkpoint"]),
    thread_id: identifier(object["thread_id"], "restored Thread id"),
    restored_cursor: integer(
      object["restored_cursor"], "restored cursor", 0,
    ),
    side_effects_replayed: false,
  };
}

export function decodeCheckpointFork(value: unknown): CheckpointFork {
  const object = requireObject(value, "Checkpoint Fork");
  requireKeys(object, [
    "version", "checkpoint", "session_id", "thread_id", "parent_thread_id",
  ]);
  const checkpoint = decodeCheckpoint(object["checkpoint"]);
  const sessionId = identifier(object["session_id"], "Fork Session id");
  if (checkpoint.session_id !== sessionId) {
    throw new Error("Checkpoint Fork crosses Session identity");
  }
  return {
    version: version(object["version"]),
    checkpoint,
    session_id: sessionId,
    thread_id: identifier(object["thread_id"], "Fork Thread id"),
    parent_thread_id: identifier(
      object["parent_thread_id"], "parent Thread id",
    ),
  };
}

export function decodeSessionPlan(value: unknown): SessionPlan {
  const object = requireObject(value, "Session Plan");
  requireKeys(object, ["version"], ["artifact"]);
  if (object["artifact"] === undefined) {
    return { version: version(object["version"]) };
  }
  const artifact = requireObject(object["artifact"], "Plan Artifact");
  requireKeys(artifact, [
    "version", "id", "session_id", "thread_id", "turn_id", "cursor", "status",
    "body", "profile_revision", "can_implement", "can_autopilot", "created_at",
  ]);
  const status = boundedText(artifact["status"], "Plan status", 32);
  if (status !== "ready") throw new Error("Plan status is invalid");
  return {
    version: version(object["version"]),
    artifact: {
      version: version(artifact["version"]),
      id: identifier(artifact["id"], "Plan id"),
      session_id: identifier(artifact["session_id"], "Plan Session id"),
      thread_id: identifier(artifact["thread_id"], "Plan Thread id"),
      turn_id: identifier(artifact["turn_id"], "Plan Turn id"),
      cursor: integer(artifact["cursor"], "Plan cursor", 0),
      status,
      body: boundedText(artifact["body"], "Plan body", 64 << 10),
      profile_revision: integer(
        artifact["profile_revision"], "Plan Profile Revision", 1,
      ),
      can_implement: boolean(artifact["can_implement"], "can implement"),
      can_autopilot: boolean(artifact["can_autopilot"], "can Autopilot"),
      created_at: timestamp(artifact["created_at"], "Plan created_at"),
    },
  };
}

function decodeAcceptedPlanTurn(value: unknown): AcceptedPlanTurn {
  const object = requireObject(value, "accepted Plan Turn");
  requireKeys(object, [
    "operationId", "accepted", "kind", "threadId", "turnId", "itemId",
  ]);
  if (object["accepted"] !== true || object["kind"] !== "turn.start") {
    throw new Error("Plan Turn was not accepted");
  }
  return {
    operationId: identifier(object["operationId"], "operation id"),
    accepted: true,
    kind: "turn.start",
    threadId: identifier(object["threadId"], "Thread id"),
    turnId: identifier(object["turnId"], "Turn id"),
    itemId: identifier(object["itemId"], "item id"),
  };
}

function requireObject(value: unknown, name: string): JsonObject {
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    throw new Error(`${name} is invalid`);
  }
  return value as JsonObject;
}

function requireKeys(
  object: JsonObject,
  required: readonly string[],
  optional: readonly string[] = [],
): void {
  const allowed = new Set([...required, ...optional]);
  if (Object.keys(object).some((key) => !allowed.has(key)) ||
    required.some((key) => !Object.hasOwn(object, key))) {
    throw new Error("Artifact contract contains unknown or missing fields");
  }
}

function identifier(value: unknown, name: string): string {
  return boundedText(value, name, 256);
}

function boundedText(
  value: unknown,
  name: string,
  maximum: number,
  allowEmpty = false,
): string {
  if (typeof value !== "string" || value.length > maximum ||
    value.includes("\0") || (!allowEmpty && value.length === 0)) {
    throw new Error(`${name} is invalid`);
  }
  return value;
}

function integer(
  value: unknown,
  name: string,
  minimum: number,
  maximum = Number.MAX_SAFE_INTEGER,
): number {
  if (!Number.isSafeInteger(value) ||
    (value as number) < minimum ||
    (value as number) > maximum) {
    throw new Error(`${name} is invalid`);
  }
  return value as number;
}

function boolean(value: unknown, name: string): boolean {
  if (typeof value !== "boolean") throw new Error(`${name} is invalid`);
  return value;
}

function timestamp(value: unknown, name: string): string {
  const text = boundedText(value, name, 64);
  if (Number.isNaN(Date.parse(text))) throw new Error(`${name} is invalid`);
  return text;
}

function version(value: unknown): 1 {
  if (value !== 1) throw new Error("Artifact version is unsupported");
  return 1;
}
