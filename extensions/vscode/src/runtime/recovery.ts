import {
  RpcError,
  type RpcNotification,
} from "./client.js";
import {
  decodeEvent,
  type DecodedEvent,
} from "../protocol/decode.js";
import type { BindingStore, RuntimeBinding } from "../state/store.js";
import { compatibility } from "../compatibility/generated.js";
import type { WorkspaceIdentity } from "../workspace/identity.js";
import { createWorkspaceIdentity } from "../workspace/identity.js";
import { pathToFileURL } from "node:url";
import { defaultChatTitle } from "../chat/title.js";

const acpProtocolVersion = compatibility.acp_protocol.max;
const replayLimit = 256;
const requiredFeatures = compatibility.required_features;
const requiredMethods = compatibility.required_methods;

type JsonObject = Readonly<Record<string, unknown>>;

export interface NegotiatedRuntime {
  readonly protocolVersion: number;
  readonly minSupportedVersion: number;
  readonly methods: ReadonlySet<string>;
  readonly features: ReadonlySet<string>;
  readonly operations: ReadonlySet<string>;
  readonly events: ReadonlySet<string>;
  readonly serverName: string;
  readonly serverVersion: string;
}

export interface ConnectedSession {
  binding: RuntimeBinding;
  readonly replayedEvents: number;
  dispose(): void;
  settled(): Promise<void>;
}

export interface ConnectSessionOptions {
  readonly binding?: RuntimeBinding;
  readonly create?: boolean;
  readonly title?: string;
  readonly isolation?: "worktree" | "shared";
}

export interface AcpTransport {
  request(method: string, params?: unknown): Promise<unknown>;
  onNotification(listener: (notification: RpcNotification) => void): () => void;
}

export class HistoryDesyncError extends Error {
  public constructor(
    message: string,
    public readonly oldestAvailable?: number,
  ) {
    super(message);
    this.name = "HistoryDesyncError";
  }
}

export async function negotiateRuntime(
  client: AcpTransport,
  workspaceIdentity?: WorkspaceIdentity,
): Promise<NegotiatedRuntime> {
  const raw = await client.request("initialize", {
    protocolVersion: acpProtocolVersion,
    clientInfo: { name: "codehelper-vscode", version: "0.0.1" },
    ...(workspaceIdentity === undefined
      ? {}
      : { workspaceIdentity }),
  });
  const value = requireObject(raw, "initialize result");
  const protocolVersion = requireInteger(value["protocolVersion"], "protocolVersion");
  const minSupportedVersion = requireInteger(
    value["minSupportedVersion"],
    "minSupportedVersion",
  );
  if (protocolVersion < acpProtocolVersion || minSupportedVersion > acpProtocolVersion) {
    throw new Error(
      `incompatible ACP protocol: server=${String(protocolVersion)}..` +
      `${String(minSupportedVersion)}, client=${String(acpProtocolVersion)}`,
    );
  }
  const methods = requireStringSet(value["methods"], "methods");
  for (const method of requiredMethods) {
    if (!methods.has(method)) {
      throw new Error(`runtime does not advertise required method ${method}`);
    }
  }
  const features = requireStringSet(value["features"], "features");
  for (const feature of requiredFeatures) {
    if (!features.has(feature)) {
      throw new Error(`runtime does not advertise required feature ${feature}`);
    }
  }
  const serverInfo = requireObject(value["serverInfo"], "serverInfo");
  if (workspaceIdentity !== undefined) {
    requireWorkspaceIdentity(
      value["workspaceIdentity"],
      workspaceIdentity,
    );
  }
  return {
    protocolVersion,
    minSupportedVersion,
    methods,
    features,
    operations: requireStringSet(value["operations"], "operations"),
    events: requireStringSet(value["events"], "events"),
    serverName: requireString(serverInfo["name"], "serverInfo.name"),
    serverVersion: requireString(serverInfo["version"], "serverInfo.version"),
  };
}

function requireWorkspaceIdentity(
  value: unknown,
  expected: WorkspaceIdentity,
): void {
  const identity = requireObject(value, "workspaceIdentity");
  if (identity["version"] !== expected.version ||
    identity["root_id"] !== expected.root_id ||
    identity["editor_uri"] !== expected.editor_uri ||
    identity["runtime_path"] !== expected.runtime_path ||
    identity["remote_name"] !== expected.remote_name) {
    throw new Error("Runtime workspace identity does not match the client");
  }
}

export async function connectSession(
  client: AcpTransport,
  store: BindingStore,
  workspace: WorkspaceIdentity | string,
  onEvent: (event: DecodedEvent, replayed: boolean) => void | Promise<void>,
  onError: (error: Error) => void,
  options: ConnectSessionOptions = {},
): Promise<ConnectedSession> {
  const workspaceIdentity = typeof workspace === "string"
    ? createWorkspaceIdentity(pathToFileURL(workspace).toString(), workspace)
    : workspace;
  let binding = options.binding ??
    (options.create === true ? undefined : store.load(workspaceIdentity));
  let queue = Promise.resolve();
  let disposed = false;
  let recovering = true;
  const bufferedEvents: DecodedEvent[] = [];

  const advance = async (
    event: DecodedEvent,
    replayed: boolean,
  ): Promise<void> => {
    if (binding === undefined ||
      (event.thread_id !== binding.threadId && !isWorkspaceEvent(event))) {
      return;
    }
    if (event.sequence <= binding.lastSeq) {
      return;
    }
    await onEvent(event, replayed);
    binding = { ...binding, lastSeq: event.sequence };
    await store.save(binding);
  };
  const enqueue = (task: () => Promise<void>): Promise<void> => {
    const operation = queue.then(task, task);
    queue = operation.catch((error: unknown) => {
      try {
        onError(asError(error));
      } catch {
        // Error reporting cannot be allowed to break event serialization.
      }
    });
    return operation;
  };
  const unsubscribe = client.onNotification((notification) => {
    if (disposed) {
      return;
    }
    if (notification.method === "session/desync") {
      void enqueue(() => Promise.reject(decodeDesync(notification)));
      return;
    }
    if (notification.method !== "session/update") {
      return;
    }
    void enqueue(async () => {
      const update = requireObject(notification.params, "session/update params");
      if (binding === undefined ||
        requireString(update["sessionId"], "sessionId") !== binding.sessionId) {
        return;
      }
      const event = decodeEvent(update["event"]);
      if (recovering) {
        bufferedEvents.push(event);
        return;
      }
      await advance(event, false);
    });
  });

  let replayedEvents = 0;
  try {
    if (binding === undefined) {
      const title = options.title?.trim() || defaultChatTitle;
      const isolation = options.isolation ?? "shared";
      const created = requireObject(await client.request("session/new", {
        cwd: workspaceIdentity.runtime_path,
        title,
        ...(isolation === "worktree" ? { isolation } : {}),
      }), "session/new result");
      binding = {
        version: 1,
        rootId: workspaceIdentity.root_id,
        workspaceURI: workspaceIdentity.editor_uri,
        workspaceRoot: workspaceIdentity.runtime_path,
        sessionId: requireString(created["sessionId"], "sessionId"),
        threadId: requireString(created["threadId"], "threadId"),
        lastSeq: 0,
      };
      await store.save(binding);
    } else {
      await client.request("session/load", {
        sessionId: binding.sessionId,
        threadId: binding.threadId,
      });
      let historySinceSeq: number | undefined;
      for (;;) {
        const history = requireObject(await client.request("session/history", {
          sessionId: binding.sessionId,
          turnLimit: 200,
          ...(historySinceSeq === undefined ? {} : { sinceSeq: historySinceSeq }),
        }), "session/history result");
        if (!Array.isArray(history["events"])) {
          throw new TypeError("session/history events must be an array");
        }
        for (const rawEvent of history["events"]) {
          const event = decodeEvent(rawEvent);
          if (event.thread_id !== binding.threadId) {
            throw new Error("session/history returned an event for another thread");
          }
          await onEvent(event, true);
          replayedEvents++;
        }
        const truncated = history["truncated"] === undefined
          ? false
          : requireBoolean(history["truncated"], "history truncated");
        if (!truncated) {
          break;
        }
        const nextSeq = requireNonNegativeInteger(
          history["nextSeq"],
          "history nextSeq",
        );
        if (historySinceSeq !== undefined && nextSeq <= historySinceSeq) {
          throw new Error("session/history did not advance its cursor");
        }
        historySinceSeq = nextSeq;
      }
      for (;;) {
        const sinceSeq = binding.lastSeq;
        const page = await replayPage(client, binding);
        if (page.nextSeq < sinceSeq ||
          (page.truncated && page.nextSeq === sinceSeq)) {
          throw new Error("session/replay did not advance its cursor");
        }
        for (const event of page.events) {
          await advance(event, true);
          replayedEvents++;
        }
        if (page.nextSeq > binding.lastSeq) {
          binding = { ...binding, lastSeq: page.nextSeq };
          await store.save(binding);
        }
        if (!page.truncated) {
          break;
        }
      }
    }
    for (;;) {
      await queue;
      if (bufferedEvents.length === 0) {
        recovering = false;
        break;
      }
      const batch = bufferedEvents.splice(0);
      batch.sort((left, right) => left.sequence - right.sequence);
      for (const event of batch) {
        await advance(event, false);
      }
    }
  } catch (error) {
    unsubscribe();
    if (error instanceof RpcError && error.code === -32001) {
      const data = isObject(error.data) ? error.data : undefined;
      throw new HistoryDesyncError(
        error.message,
        optionalInteger(data?.["oldestAvailable"]),
      );
    }
    throw error;
  }

  return {
    get binding(): RuntimeBinding {
      if (binding === undefined) {
        throw new Error("session binding is unavailable");
      }
      return binding;
    },
    set binding(value: RuntimeBinding) {
      binding = value;
    },
    replayedEvents,
    dispose(): void {
      disposed = true;
      unsubscribe();
    },
    async settled(): Promise<void> {
      await queue;
    },
  };
}

function isWorkspaceEvent(event: DecodedEvent): boolean {
  return event.kind === "agent.spawned" ||
    event.kind === "agent.status" ||
    event.kind === "agent.message";
}

async function replayPage(
  client: AcpTransport,
  binding: RuntimeBinding,
): Promise<{
  readonly events: DecodedEvent[];
  readonly nextSeq: number;
  readonly truncated: boolean;
}> {
  const raw = await client.request("session/replay", {
    sessionId: binding.sessionId,
    sinceSeq: binding.lastSeq,
    limit: replayLimit,
  });
  const value = requireObject(raw, "session/replay result");
  if (!Array.isArray(value["events"])) {
    throw new TypeError("session/replay events must be an array");
  }
  return {
    events: value["events"].map((event) => decodeEvent(event)),
    nextSeq: requireNonNegativeInteger(value["nextSeq"], "nextSeq"),
    truncated: requireBoolean(value["truncated"], "truncated"),
  };
}

function decodeDesync(notification: RpcNotification): HistoryDesyncError {
  const value = requireObject(notification.params, "session/desync params");
  return new HistoryDesyncError(
    requireString(value["reason"], "reason"),
    optionalInteger(value["oldestAvailable"]),
  );
}

function requireObject(value: unknown, name: string): JsonObject {
  if (!isObject(value)) {
    throw new TypeError(`${name} must be an object`);
  }
  return value;
}

function requireString(value: unknown, name: string): string {
  if (typeof value !== "string" || value.length === 0) {
    throw new TypeError(`${name} must be a non-empty string`);
  }
  return value;
}

function requireInteger(value: unknown, name: string): number {
  if (typeof value !== "number" || !Number.isSafeInteger(value)) {
    throw new TypeError(`${name} must be an integer`);
  }
  return value;
}

function requireNonNegativeInteger(value: unknown, name: string): number {
  const result = requireInteger(value, name);
  if (result < 0) {
    throw new TypeError(`${name} must be non-negative`);
  }
  return result;
}

function optionalInteger(value: unknown): number | undefined {
  return typeof value === "number" && Number.isSafeInteger(value) ? value : undefined;
}

function requireBoolean(value: unknown, name: string): boolean {
  if (typeof value !== "boolean") {
    throw new TypeError(`${name} must be a boolean`);
  }
  return value;
}

function requireStringSet(value: unknown, name: string): ReadonlySet<string> {
  if (!Array.isArray(value)) {
    throw new TypeError(`${name} must be a string array`);
  }
  const result = new Set<string>();
  for (const item of value) {
    if (typeof item !== "string") {
      throw new TypeError(`${name} must be a string array`);
    }
    result.add(item);
  }
  return result;
}

function isObject(value: unknown): value is JsonObject {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function asError(value: unknown): Error {
  return value instanceof Error ? value : new Error(String(value));
}
