import assert from "node:assert/strict";
import test from "node:test";

import { RpcError, type RpcNotification } from "./client.js";
import {
  connectSession,
  HistoryDesyncError,
  negotiateRuntime,
  type AcpTransport,
} from "./recovery.js";
import { BindingStore, type Memento, type RuntimeBinding } from "../state/store.js";
import { createWorkspaceIdentity } from "../workspace/identity.js";

void test("negotiateRuntime requires the ACP surface and V2 context feature", async () => {
  const transport = new FakeTransport();
  transport.responses.set("initialize", [initializeResult()]);
  const negotiated = await negotiateRuntime(transport);
  assert.equal(negotiated.protocolVersion, 2);
  assert.equal(negotiated.methods.has("usage/query"), true);

  const incomplete = new FakeTransport();
  incomplete.responses.set("initialize", [{
    ...initializeResult(),
    methods: ["session/new"],
  }]);
  await assert.rejects(negotiateRuntime(incomplete), /required method session\/load/);

  const legacy = new FakeTransport();
  legacy.responses.set("initialize", [{
    ...initializeResult(),
    features: [],
  }]);
  await assert.rejects(
    negotiateRuntime(legacy),
    /required feature editor_context_v2/,
  );
});

void test("negotiateRuntime binds the exact workspace identity", async () => {
  const identity = createWorkspaceIdentity("file:///workspace", "/workspace");
  const transport = new FakeTransport();
  transport.responses.set("initialize", [{
    ...initializeResult(),
    workspaceIdentity: identity,
  }]);
  await negotiateRuntime(transport, identity);

  const forged = new FakeTransport();
  forged.responses.set("initialize", [{
    ...initializeResult(),
    workspaceIdentity: {
      ...identity,
      editor_uri: "file:///other",
    },
  }]);
  await assert.rejects(
    negotiateRuntime(forged, identity),
    /does not match/u,
  );
});

void test("connectSession creates and persists a new workspace binding", async () => {
  const transport = new FakeTransport();
  transport.responses.set("session/new", [{
    sessionId: "session_new",
    threadId: "thread_new",
  }]);
  const store = new BindingStore(new MemoryMemento());
  const connection = await connectSession(
    transport,
    store,
    "/workspace",
    async () => Promise.resolve(),
    () => undefined,
  );
  assert.deepEqual(store.load("/workspace"), connection.binding);
  assert.equal(connection.replayedEvents, 0);
  connection.dispose();
});

void test("connectSession keeps bindings free of Runtime business state", async () => {
  const transport = new FakeTransport();
  transport.responses.set("session/load", [{}]);
  transport.responses.set("session/history", [{
    events: [runtimeEvent(1)],
  }]);
  transport.responses.set("session/replay", [{
    events: [],
    nextSeq: 1,
    truncated: false,
  }]);
  const store = new BindingStore(new MemoryMemento());
  await store.save(binding(0));
  const connection = await connectSession(
    transport,
    store,
    "/workspace",
    async () => Promise.resolve(),
    () => undefined,
  );
  assert.equal("title" in connection.binding, false);
  assert.equal("isolation" in connection.binding, false);
  connection.dispose();
});

void test("connectSession loads, pages replay, and advances filtered cursors", async () => {
  const transport = new FakeTransport();
  transport.responses.set("session/load", [{}]);
  transport.responses.set("session/replay", [
    {
      events: [runtimeEvent(6)],
      nextSeq: 6,
      truncated: true,
    },
    {
      events: [],
      nextSeq: 10,
      truncated: false,
    },
  ]);
  const store = new BindingStore(new MemoryMemento());
  await store.save(binding(5));
  const sequences: number[] = [];
  const errors: Error[] = [];
  const connection = await connectSession(
    transport,
    store,
    "/workspace",
    (event) => {
      sequences.push(event.sequence);
      return Promise.resolve();
    },
    (error) => {
      errors.push(error);
    },
  );

  assert.deepEqual(sequences, [6]);
  assert.equal(connection.replayedEvents, 1);
  assert.equal(store.load("/workspace")?.lastSeq, 10);

  transport.notify({
    method: "session/update",
    params: {
      sessionId: "session_1",
      event: runtimeEvent(11),
    },
  });
  await connection.settled();
  assert.equal(store.load("/workspace")?.lastSeq, 11);
  assert.deepEqual(errors, []);
  connection.dispose();
});

void test("connectSession reports cursor gaps without clearing state", async () => {
  const transport = new FakeTransport();
  transport.responses.set("session/load", [{}]);
  transport.responses.set("session/replay", [
    new RpcError(-32001, "cursor gap", { oldestAvailable: 20 }),
  ]);
  const store = new BindingStore(new MemoryMemento());
  await store.save(binding(5));

  await assert.rejects(
    connectSession(
      transport,
      store,
      "/workspace",
      async () => Promise.resolve(),
      () => undefined,
    ),
    (error: unknown) => {
      assert.ok(error instanceof HistoryDesyncError);
      assert.equal(error.oldestAvailable, 20);
      return true;
    },
  );
  assert.equal(store.load("/workspace")?.lastSeq, 5);
});

void test("connectSession orders live events after an in-flight replay", async () => {
  const transport = new FakeTransport();
  const replay = deferred<unknown>();
  transport.responses.set("session/load", [{}]);
  transport.responses.set("session/replay", [replay.promise]);
  const store = new BindingStore(new MemoryMemento());
  await store.save(binding(5));
  const sequences: number[] = [];

  const connecting = connectSession(
    transport,
    store,
    "/workspace",
    (event) => {
      sequences.push(event.sequence);
      return Promise.resolve();
    },
    () => undefined,
  );
  await Promise.resolve();
  transport.notify({
    method: "session/update",
    params: { sessionId: "session_1", event: runtimeEvent(7) },
  });
  replay.resolve({
    events: [runtimeEvent(6)],
    nextSeq: 6,
    truncated: false,
  });
  const connection = await connecting;

  assert.deepEqual(sequences, [6, 7]);
  assert.equal(store.load("/workspace")?.lastSeq, 7);
  connection.dispose();
});

void test("connectSession projects workspace agent events across graph threads", async () => {
  const transport = new FakeTransport();
  transport.responses.set("session/load", [{}]);
  transport.responses.set("session/replay", [{
    events: [agentEvent(6)],
    nextSeq: 6,
    truncated: false,
  }]);
  const store = new BindingStore(new MemoryMemento());
  await store.save(binding(5));
  const kinds: string[] = [];
  const connection = await connectSession(
    transport,
    store,
    "/workspace",
    (event) => {
      kinds.push(event.kind);
      return Promise.resolve();
    },
    () => undefined,
  );
  transport.notify({
    method: "session/update",
    params: { sessionId: "session_1", event: agentEvent(7) },
  });
  await connection.settled();
  assert.deepEqual(kinds, ["agent.status", "agent.status"]);
  assert.equal(store.load("/workspace")?.lastSeq, 7);
  connection.dispose();
});

void test("connectSession projects child approvals across agent threads", async () => {
  const transport = new FakeTransport();
  transport.responses.set("session/load", [{}]);
  transport.responses.set("session/replay", [{
    events: [agentApprovalEvent(6, "approval.required")],
    nextSeq: 6,
    truncated: false,
  }]);
  const store = new BindingStore(new MemoryMemento());
  await store.save(binding(5));
  const kinds: string[] = [];
  const connection = await connectSession(
    transport,
    store,
    "/workspace",
    (event) => {
      kinds.push(event.kind);
      return Promise.resolve();
    },
    () => undefined,
  );
  transport.notify({
    method: "session/update",
    params: {
      sessionId: "session_1",
      event: agentApprovalEvent(7, "approval.resolved"),
    },
  });
  await connection.settled();
  assert.deepEqual(kinds, ["approval.required", "approval.resolved"]);
  assert.equal(store.load("/workspace")?.lastSeq, 7);
  connection.dispose();
});

void test("connectSession projects live events from spawned Child Threads", async () => {
  const transport = new FakeTransport();
  transport.responses.set("session/load", [{}]);
  transport.responses.set("session/replay", [{
    events: [],
    nextSeq: 5,
    truncated: false,
  }]);
  const store = new BindingStore(new MemoryMemento());
  await store.save(binding(5));
  const seen: Array<[number, string]> = [];
  const connection = await connectSession(
    transport,
    store,
    "/workspace",
    (event) => {
      seen.push([event.sequence, event.thread_id]);
      return Promise.resolve();
    },
    () => undefined,
  );
  transport.notify({
    method: "session/update",
    params: {
      sessionId: "session_1",
      event: agentSpawnedEvent(6, "session_1", "thread-agent-1"),
    },
  });
  transport.notify({
    method: "session/update",
    params: {
      sessionId: "session_1",
      event: childTurnEvent(7, "thread-agent-1"),
    },
  });
  await connection.settled();
  assert.deepEqual(seen, [
    [6, "thread_agent_graph"],
    [7, "thread-agent-1"],
  ]);
  assert.equal(store.load("/workspace")?.lastSeq, 7);
  connection.dispose();
});

void test("connectSession hydrates Child Threads before restart replay", async () => {
  const transport = new FakeTransport();
  transport.responses.set("session/load", [{}]);
  transport.responses.set("agent/list", [{
    agents: [{
      session_id: "session_1",
      thread_id: "thread-agent-1",
    }],
  }]);
  transport.responses.set("session/replay", [{
    events: [childTurnEvent(6, "thread-agent-1")],
    nextSeq: 6,
    truncated: false,
  }]);
  const store = new BindingStore(new MemoryMemento());
  await store.save(binding(5));
  const seen: string[] = [];
  const connection = await connectSession(
    transport,
    store,
    "/workspace",
    (event) => {
      seen.push(event.thread_id);
      return Promise.resolve();
    },
    () => undefined,
  );
  assert.deepEqual(seen, ["thread-agent-1"]);
  assert.equal(connection.replayedEvents, 1);
  assert.equal(store.load("/workspace")?.lastSeq, 6);
  connection.dispose();
});

void test("connectSession rejects foreign Agent hydration", async () => {
  const transport = new FakeTransport();
  transport.responses.set("session/load", [{}]);
  transport.responses.set("agent/list", [{
    agents: [{
      session_id: "session_other",
      thread_id: "thread-agent-foreign",
    }],
  }]);
  const store = new BindingStore(new MemoryMemento());
  await store.save(binding(5));
  await assert.rejects(
    connectSession(
      transport,
      store,
      "/workspace",
      async () => Promise.resolve(),
      () => undefined,
    ),
    /Agent for another Session/u,
  );
});

void test("connectSession hydrates paginated session history", async () => {
  const transport = new FakeTransport();
  transport.responses.set("session/load", [{}]);
  transport.responses.set("session/history", [
    {
      events: [runtimeEvent(1)],
      nextSeq: 1,
      truncated: true,
    },
    {
      events: [runtimeEvent(2)],
      nextSeq: 2,
      truncated: false,
    },
  ]);
  transport.responses.set("session/replay", [{
    events: [],
    nextSeq: 2,
    truncated: false,
  }]);
  const store = new BindingStore(new MemoryMemento());
  await store.save(binding(0));
  const seen: number[] = [];

  const connection = await connectSession(
    transport,
    store,
    "/workspace",
    (event) => {
      seen.push(event.sequence);
      return Promise.resolve();
    },
    () => undefined,
  );

  assert.deepEqual(seen, [1, 2]);
  assert.equal(connection.replayedEvents, 2);
  connection.dispose();
});

class FakeTransport implements AcpTransport {
  public readonly responses = new Map<string, unknown[]>();
  readonly #listeners = new Set<(notification: RpcNotification) => void>();

  public request(method: string): Promise<unknown> {
    const responses = this.responses.get(method);
    if (responses === undefined || responses.length === 0) {
      if (method === "session/history") {
        return Promise.resolve({ events: [] });
      }
      if (method === "agent/list") {
        return Promise.resolve({ agents: [] });
      }
      return Promise.reject(new Error(`unexpected request ${method}`));
    }
    const response = responses.shift();
    if (response instanceof Error) {
      return Promise.reject(response);
    }
    return Promise.resolve(response);
  }

  public onNotification(listener: (notification: RpcNotification) => void): () => void {
    this.#listeners.add(listener);
    return () => {
      this.#listeners.delete(listener);
    };
  }

  public notify(notification: RpcNotification): void {
    for (const listener of this.#listeners) {
      listener(notification);
    }
  }
}

class MemoryMemento implements Memento {
  readonly #values = new Map<string, unknown>();

  public get(key: string): unknown {
    return this.#values.get(key);
  }

  public update(key: string, value: unknown): Promise<void> {
    if (value === undefined) {
      this.#values.delete(key);
    } else {
      this.#values.set(key, value);
    }
    return Promise.resolve();
  }
}

function binding(lastSeq: number): RuntimeBinding {
  const identity = createWorkspaceIdentity("file:///workspace", "/workspace");
  return {
    version: 1,
    rootId: identity.root_id,
    workspaceURI: identity.editor_uri,
    workspaceRoot: "/workspace",
    sessionId: "session_1",
    threadId: "thread_1",
    lastSeq,
  };
}

function runtimeEvent(sequence: number): Readonly<Record<string, unknown>> {
  return {
    version: 1,
    id: `event_${String(sequence)}`,
    sequence,
    operation_id: "operation_1",
    thread_id: "thread_1",
    turn_id: "turn_1",
    item_id: "",
    kind: "turn.started",
    created_at: "2026-08-04T00:00:00Z",
    data: { prompt: "test" },
  };
}

function agentEvent(sequence: number): Readonly<Record<string, unknown>> {
  return {
    version: 1,
    id: `agent_event_${String(sequence)}`,
    sequence,
    operation_id: "op_agent_graph",
    thread_id: "thread_agent_graph",
    turn_id: "turn_agent_graph",
    item_id: "item_agent",
    kind: "agent.status",
    created_at: "2026-08-04T00:00:00Z",
    data: {
      agent_id: "agent-1",
      workspace_root: "/workspace",
      session_id: "runtime-session",
      status: "completed",
    },
  };
}

function agentSpawnedEvent(
  sequence: number,
  sessionId: string,
  threadId: string,
): Readonly<Record<string, unknown>> {
  return {
    version: 1,
    id: `agent_spawned_${String(sequence)}`,
    sequence,
    operation_id: "op_agent_graph",
    thread_id: "thread_agent_graph",
    turn_id: "turn_agent_graph",
    item_id: "item_agent",
    kind: "agent.spawned",
    created_at: "2026-08-04T00:00:00Z",
    data: {
      agent_id: "agent-1",
      workspace_root: "/workspace",
      session_id: sessionId,
      role: "explore",
      depth: 0,
      detail: { thread_id: threadId },
    },
  };
}

function childTurnEvent(
  sequence: number,
  threadId: string,
): Readonly<Record<string, unknown>> {
  return {
    ...runtimeEvent(sequence),
    id: `child_event_${String(sequence)}`,
    thread_id: threadId,
  };
}

function agentApprovalEvent(
  sequence: number,
  kind: "approval.required" | "approval.resolved",
): Readonly<Record<string, unknown>> {
  const source = {
    kind: "agent",
    agent_id: "agent-1",
    agent_path: "/root/write",
    parent_path: "/root",
    role: "implementer",
    session_id: "runtime-session",
    workspace_root: "/workspace",
  };
  return {
    version: 1,
    id: `approval_event_${String(sequence)}`,
    sequence,
    operation_id: "op_agent",
    thread_id: "thread-agent-1",
    turn_id: "turn_agent",
    item_id: "item_agent",
    kind,
    created_at: "2026-08-04T00:00:00Z",
    data: kind === "approval.required"
      ? {
        request_id: "approval-1",
        call_id: "call-1",
        tool: "file_write",
        arguments: {},
        arguments_digest: "digest",
        resources: [],
        allowed_scopes: ["once"],
        expires_at: "2026-08-04T01:00:00Z",
        replacement_allowed: false,
        modifiable_arguments: [],
        source,
      }
      : {
        request_id: "approval-1",
        decision: "approve",
        source,
      },
  };
}

function initializeResult(): Readonly<Record<string, unknown>> {
  return {
    protocolVersion: 2,
    minSupportedVersion: 2,
    serverInfo: { name: "codehelper", version: "1" },
    methods: [
      "session/new",
      "session/load",
      "session/submit",
      "session/replay",
      "session/history",
      "session/list",
      "session/status",
      "session/lifecycle/update",
      "checkpoint/list",
      "checkpoint/get",
      "checkpoint/restore",
      "checkpoint/fork",
      "plan/get",
      "plan/implement",
      "session/delete",
      "session/merge",
      "session/rename",
      "session/profile/get",
      "session/profile/update",
      "session/tool/catalog",
      "thread/list",
      "thread/get",
      "task/list",
      "agent/list",
      "usage/query",
      "shutdown",
    ],
    features: [
      "editor_context_v2",
      "session_profile_v1",
      "session_lifecycle_v1",
      "checkpoint_plan_v1",
      "unified_tool_catalog_v1",
      "workspace_identity_v1",
    ],
    operations: ["turn.start"],
    events: ["turn.started"],
  };
}

function deferred<T>(): {
  readonly promise: Promise<T>;
  readonly resolve: (value: T) => void;
} {
  let resolvePromise: ((value: T) => void) | undefined;
  const promise = new Promise<T>((resolve) => {
    resolvePromise = resolve;
  });
  if (resolvePromise === undefined) {
    throw new Error("failed to initialize deferred promise");
  }
  return { promise, resolve: resolvePromise };
}
