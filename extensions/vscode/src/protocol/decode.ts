import {
  eventKinds,
  protocolVersion,
  type EventKind,
  type KnownEvent,
} from "./generated.js";

type JsonObject = Readonly<Record<string, unknown>>;

export interface UnknownEvent {
  readonly version: number;
  readonly id: string;
  readonly sequence: number;
  readonly operation_id: string;
  readonly thread_id: string;
  readonly turn_id: string;
  readonly item_id: string;
  readonly kind: string;
  readonly created_at: string;
  readonly data: unknown;
  readonly raw: JsonObject;
}

export type DecodedEvent = KnownEvent | UnknownEvent;

const knownEventKinds: ReadonlySet<string> = new Set<EventKind>(eventKinds);

function isObject(value: unknown): value is JsonObject {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function requireString(value: unknown, field: string): string {
  if (typeof value !== "string" || value.length === 0) {
    throw new TypeError(`event.${field} must be a non-empty string`);
  }
  return value;
}

function requirePresentString(value: unknown, field: string): string {
  if (typeof value !== "string") {
    throw new TypeError(`event.${field} must be a string`);
  }
  return value;
}

export function decodeEvent(value: unknown): DecodedEvent {
  if (!isObject(value)) {
    throw new TypeError("event must be an object");
  }
  const version = value["version"];
  if (version !== protocolVersion) {
    throw new TypeError(
      `event.version ${String(version)} is unsupported; expected ${String(protocolVersion)}`,
    );
  }
  const id = requireString(value["id"], "id");
  const sequence = value["sequence"];
  if (typeof sequence !== "number" ||
    !Number.isSafeInteger(sequence) ||
    sequence < 1) {
    throw new TypeError("event.sequence must be a positive integer");
  }
  const operationID = requirePresentString(value["operation_id"], "operation_id");
  const threadID = requirePresentString(value["thread_id"], "thread_id");
  const turnID = requirePresentString(value["turn_id"], "turn_id");
  const itemID = requirePresentString(value["item_id"], "item_id");
  const kind = requireString(value["kind"], "kind");
  const createdAt = requireString(value["created_at"], "created_at");
  if (!Object.hasOwn(value, "data") || value["data"] === null) {
    throw new TypeError("event.data must be present and non-null");
  }

  if (knownEventKinds.has(kind)) {
    return value as KnownEvent;
  }
  return {
    version,
    id,
    sequence,
    operation_id: operationID,
    thread_id: threadID,
    turn_id: turnID,
    item_id: itemID,
    kind,
    created_at: createdAt,
    data: value["data"],
    raw: value,
  };
}

export function isUnknownEvent(event: DecodedEvent): event is UnknownEvent {
  return "raw" in event;
}
