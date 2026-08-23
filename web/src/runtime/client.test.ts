import {afterEach, beforeEach, describe, expect, it, vi} from "vitest";
import {RuntimeClient} from "./client";
import type {
  BrowserProjectionState,
  BrowserStorage
} from "./storage";

class FakeWebSocket {
  static readonly OPEN = 1;
  static instances: FakeWebSocket[] = [];
  readonly readyState = FakeWebSocket.OPEN;
  readonly sent: string[] = [];
  private listeners = new Map<string, Array<(event: MessageEvent | Event) => void>>();

  constructor() {
    FakeWebSocket.instances.push(this);
  }

  addEventListener(type: string, listener: (event: MessageEvent | Event) => void): void {
    const listeners = this.listeners.get(type) ?? [];
    listeners.push(listener);
    this.listeners.set(type, listeners);
  }

  close(): void {}

  send(value: string): void {
    this.sent.push(value);
  }

  emit(type: string, value?: unknown): void {
    const event =
      type === "message"
        ? new MessageEvent("message", {data: JSON.stringify(value)})
        : new Event(type);
    for (const listener of this.listeners.get(type) ?? []) {
      listener(event);
    }
  }
}

async function startClient(client: RuntimeClient): Promise<FakeWebSocket> {
  const previousCount = FakeWebSocket.instances.length;
  const started = client.start();
  await vi.waitFor(() => {
    expect(FakeWebSocket.instances.length).toBeGreaterThan(previousCount);
  });
  const socket = FakeWebSocket.instances[previousCount];
  if (!socket) throw new Error("missing WebSocket");
  socket.emit("open");
  const authentication = JSON.parse(socket.sent.at(-1) ?? "{}") as {cursor?: number};
  socket.emit("message", {
    type: "hello",
    protocol_version: 1,
    sequence: authentication.cursor ?? 0
  });
  await started;
  return socket;
}

describe("RuntimeClient", () => {
  const requests: Array<{
    route: string;
    body: Record<string, unknown>;
    headers: Headers;
  }> = [];
  let bootstrapToken = "token";
  let snapshotSequence = 0;
  let snapshotGate: Promise<void> | undefined;
  let profileGate: Promise<void> | undefined;
  let failNextCreate = false;
  let failToolCatalog = false;
  let currentProvider = "fixture";
  let currentModel = "fixture";
  let snapshotEvents: ReturnType<typeof runtimeEvent>[] | null = null;
  let snapshotTruncatedBefore = 0;
  let earlierEvents: ReturnType<typeof runtimeEvent>[] = [];
  let moreBefore = false;

  beforeEach(() => {
    requests.length = 0;
    FakeWebSocket.instances.length = 0;
    bootstrapToken = "token";
    snapshotSequence = 0;
    snapshotGate = undefined;
    profileGate = undefined;
    failNextCreate = false;
    failToolCatalog = false;
    currentProvider = "fixture";
    currentModel = "fixture";
    snapshotEvents = null;
    snapshotTruncatedBefore = 0;
    earlierEvents = [];
    moreBefore = false;
    vi.stubGlobal("WebSocket", FakeWebSocket);
    vi.stubGlobal("crypto", {
      randomUUID: () => "request-id",
      subtle: {
        digest: vi.fn(async () => new Uint8Array(32).fill(0xab).buffer)
      }
    });
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const route = String(input);
      if (route.endsWith("/bootstrap")) {
        return response({
          protocol_version: 1,
          server_build: "build",
          token: bootstrapToken,
          ready: true,
          draining: false,
          workspace_root: "/workspace",
          workspace: {
            version: 1,
            root_id: "workspace-id",
            editor_uri: "file:///workspace",
            runtime_path: "/workspace"
          }
        });
      }
      if (route.includes("/api/v1/content/")) {
        requests.push({
          route,
          body: {},
          headers: new Headers(init?.headers)
        });
        return new Response("package main\n", {
          status: 200,
          headers: {"Content-Type": "text/plain; charset=utf-8"}
        });
      }
      const body = JSON.parse(String(init?.body ?? "{}")) as Record<string, unknown>;
      requests.push({route, body, headers: new Headers(init?.headers)});
      if (route.endsWith("/provider/list")) {
        return envelope({
          version: 1,
          providers: [
            {
              id: "fixture",
              display_name: "Fixture",
              selected: true,
              availability: "available"
            },
            {
              id: "offline",
              display_name: "Offline",
              selected: false,
              availability: "unavailable",
              reason: "Credential missing"
            }
          ]
        });
      }
      if (route.endsWith("/model/list")) {
        return envelope({
          version: 1,
          models: [
            {
              provider: "fixture",
              id: "fixture",
              selected: true,
              capabilities: modelCapabilities("Fixture")
            },
            {
              provider: "fixture",
              id: "reasoner",
              selected: false,
              capabilities: {
                ...modelCapabilities("Reasoner"),
                reasoning: true,
                reasoning_efforts: ["low", "high"]
              }
            }
          ]
        });
      }
      if (route.endsWith("/session/create")) {
        if (failNextCreate) {
          failNextCreate = false;
          throw new TypeError("connection reset");
        }
        return envelope({
          session_id: "session-new",
          thread_id: "thread-new",
          workspace_root: "/workspace",
          provider: "fixture",
          model: "fixture",
          isolation: body.isolation
        });
      }
      if (route.endsWith("/session/list")) {
        return envelope({
          version: 1,
          sessions: [{
            version: 1,
            revision: 1,
            session_id: "session",
            thread_id: "thread",
            title: "Chat",
            status: "idle",
            pinned: false,
            archived: false,
            isolation: "shared",
            workspace_root: "/workspace",
            workspace_label: "workspace",
            latest_sequence: 0,
            pending_approvals: 0,
            pending_inputs: 0,
            checkpoint_count: 0,
            changed_files: 0,
            total_tokens: 0,
            cost_microunits: 0,
            cost_known: true,
            created_at: "2026-01-01T00:00:00Z",
            updated_at: "2026-01-01T00:00:00Z"
          }]
        });
      }
      if (route.endsWith("/session/activate")) {
        return envelope({
          session_id: "session",
          thread_id: "thread",
          workspace_root: "/workspace",
          provider: "fixture",
          model: "fixture",
          isolation: "shared"
        });
      }
      if (route.endsWith("/session/snapshot")) {
        await snapshotGate;
        return envelope({
          version: 1,
          session_id: "session",
          thread_id: "thread",
          session_revision: 1,
          through_sequence: snapshotSequence,
          events: snapshotEvents,
          history_truncated_before: snapshotTruncatedBefore
        });
      }
      if (route.endsWith("/session/history")) {
        return envelope({
          session_id: "session",
          events: earlierEvents,
          next_sequence: earlierEvents.at(-1)?.sequence ?? 0,
          more: false,
          previous_sequence: earlierEvents[0]?.sequence ?? 0,
          more_before: moreBefore
        });
      }
      if (route.endsWith("/profile/get")) {
        await profileGate;
        return envelope({
          profile: {
            version: 1,
            revision: 1,
            mode: "act",
            provider: currentProvider,
            model: currentModel,
            approval_posture: "suggest",
            execution_target: "local",
            max_steps: 0,
            prompt_cache_revision: 1
          },
          capabilities: {
            provider: currentProvider,
            model: currentModel,
            mutable_fields: ["mode", "provider", "model"],
            model_capabilities: modelCapabilities(
              currentModel === "reasoner" ? "Reasoner" : "Fixture"
            )
          }
        });
      }
      if (route.endsWith("/profile/update")) {
        const patch = body.patch as Record<string, unknown>;
        currentProvider = String(patch.provider ?? currentProvider);
        currentModel = String(patch.model ?? currentModel);
        return envelope({
          profile: {
            version: 1,
            revision: 2,
            mode: "act",
            provider: currentProvider,
            model: currentModel,
            approval_posture: "suggest",
            execution_target: "local",
            max_steps: 0
          },
          prompt_cache_reset: true
        });
      }
      if (route.endsWith("/tool/catalog")) {
        if (failToolCatalog) {
          return envelopeProblem("unavailable", "catalog unavailable", true);
        }
        return envelope({
          version: 1,
          catalog_id: "catalog",
          generation: 1,
          digest: "digest",
          tools: null
        });
      }
      if (route.endsWith("/checkpoint/list")) {
        return envelope({version: 1, session_id: "session", checkpoints: null});
      }
      if (route.endsWith("/plan/get")) {
        return envelope({version: 1});
      }
      if (route.endsWith("/task/list")) {
        return envelope({tasks: []});
      }
      if (route.endsWith("/agent/list")) {
        return envelope({agents: []});
      }
      if (route.endsWith("/usage/query")) {
        return envelope({
          usage: [],
          rollup: {
            turns: 0,
            calls: 0,
            total_tokens: 0,
            cost_microunits: 0,
            cost_known: true
          }
        });
      }
      if (route.endsWith("/extension/list")) {
        return envelope({revision: 1, extensions: []});
      }
      if (route.endsWith("/workspace/resource")) {
        return envelope({
          path: "src/main.go",
          uri: "file:///workspace/src/main.go",
          document_version: 1,
          content_handle: "signed-content-handle",
          content: "package main\n",
          digest: "a".repeat(64),
          bytes: 13
        });
      }
      if (route.endsWith("/workspace/image")) {
        return envelope({
          path: "diagram.png",
          uri: "file:///workspace/diagram.png",
          document_version: 1,
          content_handle: "signed-image-handle",
          digest: "c".repeat(64),
          bytes: 16,
          media_type: "image/png",
          label: "diagram.png"
        });
      }
      if (route.endsWith("/workspace/symbols")) {
        return envelope({
          query: "main",
          status: "ready",
          symbols: [{
            path: "src/main.go",
            name: "main",
            kind: "function",
            line: 3,
            uri: "file:///workspace/src/main.go",
            document_version: 1,
            digest: "a".repeat(64),
            range: {
              start: {line: 2, character: 0},
              end: {line: 2, character: 14}
            },
            selection_range: {
              start: {line: 2, character: 5},
              end: {line: 2, character: 9}
            }
          }]
        });
      }
      if (route.endsWith("/workspace/diagnostics")) {
        return envelope({
          session_id: "session",
          thread_id: "thread",
          diagnostics: [{
            call_id: "call-1",
            tool: "exec_command",
            status: "failed",
            context: {
              kind: "diagnostics",
              source: "code_action",
              uri: "file:///workspace/src/main.go",
              path: "src/main.go",
              document_version: 1,
              digest: "a".repeat(64),
              diagnostics: [{
                range: {
                  start: {line: 2, character: 0},
                  end: {line: 2, character: 4}
                },
                severity: "error",
                message: "broken",
                source: "fixture"
              }],
              explicit: true
            }
          }]
        });
      }
      if (route.endsWith("/workspace/diff")) {
        return envelope({
          session_id: "session",
          thread_id: "thread",
          diff: "diff --git a/main.go b/main.go\n",
          digest: "b".repeat(64)
        });
      }
      if (route.endsWith("/operation/submit")) {
        return envelope({
          operation_id: "operation",
          kind: "turn.start",
          thread_id: "thread",
          turn_id: "turn",
          item_id: "item",
          accepted: true
        });
      }
      throw new Error(`unexpected route ${route}`);
    }));
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("normalizes empty collections and submits act prompts as answers", async () => {
    const client = new RuntimeClient();
    await startClient(client);

    expect(client.getSnapshot().events).toEqual([]);
    expect(client.getSnapshot().providers.map((provider) => provider.id)).toEqual([
      "fixture",
      "offline"
    ]);
    expect(client.getSnapshot().models.map((model) => model.id)).toEqual([
      "fixture",
      "reasoner"
    ]);
    expect(client.getSnapshot().tools).toEqual([]);
    expect(client.getSnapshot().checkpoints).toEqual([]);

    await client.submitPrompt("say hello");
    const submit = requests.find((request) => request.route.endsWith("/operation/submit"));
    expect(submit?.body).toMatchObject({
      kind: "turn.start",
      payload: {
        prompt: "say hello",
        display_prompt: "say hello",
        intent: "answer"
      }
    });
    client.stop();
  });

  it("refreshes authoritative capabilities after changing the model", async () => {
    const client = new RuntimeClient();
    await startClient(client);

    await client.updateProfile({model: "reasoner"});

    expect(client.getSnapshot().profile).toMatchObject({
      profile: {provider: "fixture", model: "reasoner"},
      capabilities: {
        provider: "fixture",
        model: "reasoner",
        model_capabilities: {display_name: "Reasoner"}
      }
    });
    expect(
      requests.filter((request) => request.route.endsWith("/profile/get"))
    ).toHaveLength(2);
    client.stop();
  });

  it("keeps the selected session usable when an auxiliary query fails", async () => {
    failToolCatalog = true;
    const client = new RuntimeClient();
    await startClient(client);

    expect(client.getSnapshot()).toMatchObject({
      phase: "ready",
      selectedSessionID: "session",
      tools: []
    });
    client.stop();
  });

  it("restores scoped cursor, selection, and drafts without persisting tokens", async () => {
    const storage = new MemoryBrowserStorage();
    storage.values.set("v1:build:workspace-id", {
      cursor: 41,
      selectedSessionID: "session",
      drafts: {session: "unfinished prompt"}
    });
    const client = new RuntimeClient(storage);
    await startClient(client);

    const socket = FakeWebSocket.instances.at(-1);
    socket?.emit("open");
    expect(JSON.parse(socket?.sent[0] ?? "{}")).toEqual({
      type: "authenticate",
      token: "token",
      cursor: 41
    });
    expect(await client.loadDraft()).toBe("unfinished prompt");

    socket?.emit("message", {
      type: "watermark",
      protocol_version: 1,
      sequence: 42
    });
    client.saveDraft("next prompt");
    await vi.waitFor(() => {
      expect(storage.values.get("v1:build:workspace-id")).toMatchObject({
        cursor: 42,
        selectedSessionID: "session",
        drafts: {session: "next prompt"}
      });
    });
    expect(JSON.stringify(storage.values)).not.toContain("token");
    client.stop();
  });

  it("discards browser projection state when the cache scope changes", async () => {
    const storage = new MemoryBrowserStorage();
    storage.values.set("v1:old-build:workspace-id", {
      cursor: 99,
      selectedSessionID: "old-session",
      drafts: {"old-session": "stale"}
    });
    const client = new RuntimeClient(storage);
    await startClient(client);

    const socket = FakeWebSocket.instances.at(-1);
    socket?.emit("open");
    expect(JSON.parse(socket?.sent[0] ?? "{}")).toMatchObject({cursor: 0});
    expect(client.getSnapshot().selectedSessionID).toBe("session");
    expect(await client.loadDraft()).toBe("");
    client.stop();
  });

  it("retries session creation with one stable idempotency key", async () => {
    const client = new RuntimeClient();
    await startClient(client);
    failNextCreate = true;

    await client.createSession();

    const creates = requests.filter((request) => request.route.endsWith("/session/create"));
    expect(creates).toHaveLength(2);
    expect(creates[0]?.body).toMatchObject({
      session_id: "session_web_request-id",
      isolation: "shared"
    });
    expect(creates[1]?.body).toMatchObject({
      session_id: "session_web_request-id"
    });
    expect(creates[0]?.headers.get("Idempotency-Key")).toBe("request-id");
    expect(creates[1]?.headers.get("Idempotency-Key")).toBe("request-id");
    client.stop();
  });

  it("submits server-issued workspace context and clears it after acceptance", async () => {
    const client = new RuntimeClient();
    await startClient(client);
    const resource = await client.readWorkspaceResource("src/main.go");
    client.addWorkspaceContext(resource);

    await client.submitPrompt("review this file");

    const submit = requests.find((request) => request.route.endsWith("/operation/submit"));
    expect(submit?.body).toMatchObject({
      payload: {
        context: [{
          kind: "file",
          source: "composer",
          uri: "file:///workspace/src/main.go",
          path: "src/main.go",
          document_version: 1,
          digest: "a".repeat(64),
          explicit: true
        }]
      }
    });
    expect(client.getSnapshot().contextResources).toEqual([]);
    client.stop();
  });

  it("submits a server-issued selection range without trusting a browser path", async () => {
    const client = new RuntimeClient();
    await startClient(client);
    const resource = await client.readWorkspaceResource("src/main.go");
    client.addWorkspaceContext(resource, {
      start: {line: 0, character: 0},
      end: {line: 0, character: 7}
    });

    await client.submitPrompt("review this selection");

    const submit = requests.find((request) => request.route.endsWith("/operation/submit"));
    expect(submit?.body).toMatchObject({
      payload: {
        context: [{
          kind: "selection",
          source: "composer",
          uri: "file:///workspace/src/main.go",
          path: "src/main.go",
          document_version: 1,
          digest: "a".repeat(64),
          range: {
            start: {line: 0, character: 0},
            end: {line: 0, character: 7}
          },
          explicit: true
        }]
      }
    });
    client.stop();
  });

  it("submits only server-issued image metadata as native context", async () => {
    const client = new RuntimeClient();
    await startClient(client);
    const image = await client.readWorkspaceImage("diagram.png");
    client.addImageContext(image);

    await client.submitPrompt("inspect this image");

    const submit = requests.find((request) => request.route.endsWith("/operation/submit"));
    expect(submit?.body).toMatchObject({
      payload: {
        context: [{
          kind: "image",
          source: "native_picker",
          uri: "file:///workspace/diagram.png",
          path: "diagram.png",
          document_version: 1,
          digest: "c".repeat(64),
          label: "diagram.png",
          media_type: "image/png",
          explicit: true
        }]
      }
    });
    client.stop();
  });

  it("submits only a server-issued repository symbol range", async () => {
    const client = new RuntimeClient();
    await startClient(client);
    const result = await client.searchWorkspaceSymbols("main");
    client.addSymbolContext(result.symbols[0]!);

    await client.submitPrompt("inspect this symbol");

    const submit = requests.find((request) => request.route.endsWith("/operation/submit"));
    expect(submit?.body).toMatchObject({
      payload: {
        context: [{
          kind: "symbol",
          source: "native_picker",
          path: "src/main.go",
          digest: "a".repeat(64),
          symbol: {
            name: "main",
            kind: "function"
          }
        }]
      }
    });
    client.stop();
  });

  it("submits only a persisted diagnostic receipt context", async () => {
    const client = new RuntimeClient();
    await startClient(client);
    const result = await client.workspaceDiagnostics();
    client.addDiagnosticsContext(result.diagnostics[0]!);

    await client.submitPrompt("fix this diagnostic");

    const submit = requests.find((request) => request.route.endsWith("/operation/submit"));
    expect(submit?.body).toMatchObject({
      payload: {
        context: [{
          kind: "diagnostics",
          source: "code_action",
          path: "src/main.go",
          diagnostics: [{
            severity: "error",
            message: "broken"
          }]
        }]
      }
    });
    client.stop();
  });

  it("downloads a signed content handle with the in-memory capability token", async () => {
    const client = new RuntimeClient();
    await startClient(client);

    const content = await client.downloadWorkspaceContent("signed.handle");

    expect(content.size).toBe(13);
    expect(content.type).toBe("text/plain;charset=utf-8");
    const request = requests.find((item) => item.route.includes("/api/v1/content/"));
    expect(request?.route).toContain("signed.handle");
    expect(request?.headers.get("Authorization")).toBe("Bearer token");
    client.stop();
  });

  it("submits only the server-issued Git diff as inline context", async () => {
    const client = new RuntimeClient();
    await startClient(client);
    const diff = await client.workspaceDiff();
    client.addGitDiffContext(diff);

    await client.submitPrompt("review this diff");

    const submit = requests.find((request) => request.route.endsWith("/operation/submit"));
    expect(submit?.body).toMatchObject({
      payload: {
        context: [{
          kind: "git_diff",
          source: "composer",
          digest: "b".repeat(64),
          label: "Current workspace diff",
          media_type: "text/plain",
          content: "diff --git a/main.go b/main.go\n",
          explicit: true
        }]
      }
    });
    client.stop();
  });

  it("binds terminal context to the projected tool call and output digest", async () => {
    const client = new RuntimeClient();
    await startClient(client);

    await client.addTerminalContext("call-1", "terminal output");
    await client.submitPrompt("inspect this output");

    const submit = requests.find((request) => request.route.endsWith("/operation/submit"));
    expect(submit?.body).toMatchObject({
      payload: {
        context: [{
          kind: "terminal",
          source: "composer",
          digest: "ab".repeat(32),
          label: "call-1",
          media_type: "text/plain",
          content: "terminal output",
          explicit: true
        }]
      }
    });
    client.stop();
  });

  it("prepends an older history page without duplicating snapshot events", async () => {
    snapshotSequence = 12;
    snapshotTruncatedBefore = 9;
    snapshotEvents = [
      runtimeEvent(10, "output.delta"),
      runtimeEvent(11, "turn.receipt"),
      runtimeEvent(12, "turn.completed")
    ];
    earlierEvents = [
      runtimeEvent(8, "output.delta"),
      runtimeEvent(9, "turn.completed"),
      runtimeEvent(10, "output.delta")
    ];
    moreBefore = true;
    const client = new RuntimeClient();
    await startClient(client);

    expect(client.getSnapshot().historyMoreBefore).toBe(true);
    expect(await client.loadEarlierHistory()).toBe(2);
    expect(client.getSnapshot().events.map((event) => event.sequence)).toEqual([
      8, 9, 10, 11, 12
    ]);
    expect(client.getSnapshot().historyMoreBefore).toBe(true);
    expect(requests.find((request) => request.route.endsWith("/session/history"))?.body)
      .toMatchObject({
        session_id: "session",
        before_sequence: 10,
        limit: 200
      });
    client.stop();
  });

  it("merges live events received while a session snapshot hydrates", async () => {
    const client = new RuntimeClient();
    await startClient(client);
    const socket = FakeWebSocket.instances.at(-1);
    if (!socket) throw new Error("missing WebSocket");
    snapshotSequence = 5;
    let releaseProfile = (): void => {};
    profileGate = new Promise<void>((resolve) => {
      releaseProfile = resolve;
    });

    const selecting = client.selectSession("session");
    await vi.waitFor(() => {
      expect(
        requests.filter((request) => request.route.endsWith("/profile/get"))
      ).toHaveLength(2);
    });
    socket.emit("message", {
      type: "event",
      protocol_version: 1,
      session_id: "session",
      sequence: 6,
      event: runtimeEvent(6, "output.delta")
    });
    releaseProfile();
    await selecting;

    expect(client.getSnapshot().events.map((event) => event.sequence)).toEqual([6]);
    client.stop();
  });

  it("coalesces session-list invalidation for foreign lifecycle events", async () => {
    const client = new RuntimeClient();
    await startClient(client);
    const socket = FakeWebSocket.instances.at(-1);
    if (!socket) throw new Error("missing WebSocket");
    const before = requests.filter(
      (request) => request.route.endsWith("/session/list")
    ).length;
    for (let sequence = 20; sequence < 70; sequence += 1) {
      socket.emit("message", {
        type: "event",
        protocol_version: 1,
        session_id: "foreign-session",
        sequence,
        event: runtimeEvent(sequence, "output.delta")
      });
    }
    socket.emit("message", {
      type: "event",
      protocol_version: 1,
      session_id: "foreign-session",
      sequence: 70,
      event: runtimeEvent(70, "turn.started")
    });
    for (let sequence = 71; sequence < 75; sequence += 1) {
      socket.emit("message", {
        type: "event",
        protocol_version: 1,
        session_id: "session",
        sequence,
        event: runtimeEvent(sequence, "approval.resolved")
      });
    }
    socket.emit("message", {
      type: "event",
      protocol_version: 1,
      session_id: "foreign-session",
      sequence: 75,
      event: runtimeEvent(75, "turn.completed")
    });
    await vi.waitFor(() => {
      expect(
        requests.filter((request) => request.route.endsWith("/session/list"))
          .length
      ).toBe(before + 1);
    });
    const after = requests.filter(
      (request) => request.route.endsWith("/session/list")
    ).length;
    expect(after - before).toBe(1);
    client.stop();
  });

  it("buffers live events before the session snapshot returns", async () => {
    const client = new RuntimeClient();
    await startClient(client);
    const socket = FakeWebSocket.instances.at(-1);
    if (!socket) throw new Error("missing WebSocket");
    snapshotSequence = 5;
    let releaseSnapshot = (): void => {};
    snapshotGate = new Promise<void>((resolve) => {
      releaseSnapshot = resolve;
    });

    const selecting = client.selectSession("session");
    await vi.waitFor(() => {
      expect(
        requests.filter((request) => request.route.endsWith("/session/snapshot"))
      ).toHaveLength(2);
    });
    socket.emit("message", {
      type: "event",
      protocol_version: 1,
      session_id: "session",
      sequence: 6,
      event: runtimeEvent(6, "output.delta")
    });
    releaseSnapshot();
    await selecting;

    expect(client.getSnapshot().events.map((event) => event.sequence)).toEqual([6]);
    client.stop();
  });

  it("changes operation ownership immediately while a Session hydrates", async () => {
    const client = new RuntimeClient();
    await startClient(client);
    let releaseSnapshot = (): void => {};
    snapshotGate = new Promise<void>((resolve) => {
      releaseSnapshot = resolve;
    });

    const selecting = client.selectSession("session-b");
    expect(client.getSnapshot()).toMatchObject({
      selectedSessionID: "session-b",
      hydratingSessionID: "session-b",
      events: [],
      profile: undefined
    });
    await expect(client.submitPrompt("must not reach session-a")).rejects.toThrow(
      "Session is still loading"
    );

    releaseSnapshot();
    await selecting;
    expect(client.getSnapshot().hydratingSessionID).toBe("");
    client.stop();
  });

  it("advances the replay cursor from payload-free watermark frames", async () => {
    vi.useFakeTimers();
    const client = new RuntimeClient();
    await startClient(client);
    const first = FakeWebSocket.instances.at(-1);
    if (!first) throw new Error("missing WebSocket");
    first.emit("message", {
      type: "watermark",
      protocol_version: 1,
      sequence: 19
    });
    first.emit("close");
    await vi.advanceTimersByTimeAsync(700);
    const second = FakeWebSocket.instances[1];
    second?.emit("open");

    expect(JSON.parse(second?.sent[0] ?? "{}")).toMatchObject({cursor: 19});
    client.stop();
  });

  it("bootstraps a fresh token before reconnecting the socket", async () => {
    vi.useFakeTimers();
    const client = new RuntimeClient();
    await startClient(client);
    const first = FakeWebSocket.instances.at(-1);
    if (!first) throw new Error("missing first WebSocket");
    bootstrapToken = "rotated-token";

    first.emit("close");
    await vi.advanceTimersByTimeAsync(700);
    await vi.waitFor(() => {
      expect(FakeWebSocket.instances).toHaveLength(2);
    });
    const second = FakeWebSocket.instances[1];
    second?.emit("open");

    expect(JSON.parse(second?.sent[0] ?? "{}")).toMatchObject({
      type: "authenticate",
      token: "rotated-token"
    });
    client.stop();
  });

  it("authenticates the event stream before requesting hydration data", async () => {
    const client = new RuntimeClient();
    const starting = client.start();
    await vi.waitFor(() => {
      expect(FakeWebSocket.instances).toHaveLength(1);
    });
    expect(requests).toEqual([]);

    const socket = FakeWebSocket.instances[0]!;
    socket.emit("open");
    socket.emit("message", {
      type: "hello",
      protocol_version: 1,
      sequence: 0
    });
    await starting;

    expect(requests.some((request) => request.route.endsWith("/session/snapshot")))
      .toBe(true);
    client.stop();
  });

  it("clears an expired cursor before reconnecting after desync", async () => {
    const storage = new MemoryBrowserStorage();
    storage.values.set("v1:build:workspace-id", {
      cursor: 41,
      selectedSessionID: "session",
      drafts: {}
    });
    const client = new RuntimeClient(storage);
    const first = await startClient(client);

    first.emit("message", {
      type: "desync",
      protocol_version: 1,
      sequence: 41,
      problem: {
        version: 1,
        code: "conflict",
        message: "cursor history expired",
        retryable: false
      }
    });
    expect(client.getSnapshot().phase).toBe("desynchronized");

    const second = await startClient(client);
    expect(JSON.parse(second.sent[0] ?? "{}")).toMatchObject({cursor: 0});
    expect(client.getSnapshot().phase).toBe("ready");
    client.stop();
  });

  it("fails closed on unknown protocol versions and event kinds", async () => {
    const client = new RuntimeClient();
    const socket = await startClient(client);

    socket.emit("message", {
      type: "event",
      protocol_version: 2,
      session_id: "session",
      sequence: 9,
      event: runtimeEvent(9, "future.event")
    });

    expect(client.getSnapshot().phase).toBe("desynchronized");
    const next = await startClient(client);
    expect(JSON.parse(next.sent[0] ?? "{}")).toMatchObject({cursor: 0});
    next.emit("message", {
      type: "event",
      protocol_version: 1,
      session_id: "session",
      sequence: 10,
      event: runtimeEvent(10, "future.event")
    });
    expect(client.getSnapshot().phase).toBe("desynchronized");
    client.stop();
  });
});

function runtimeEvent(sequence: number, kind: string) {
  return {
    version: 1,
    id: `event-${sequence}`,
    kind,
    operation_id: "operation",
    thread_id: "thread",
    turn_id: "turn",
    item_id: "item",
    sequence,
    created_at: "2026-01-01T00:00:00Z",
    data: {}
  };
}

function modelCapabilities(displayName: string) {
  return {
    display_name: displayName,
    context_window: 128_000,
    max_output_tokens: 8_192,
    streaming: true,
    reasoning: false,
    tool_calls: true,
    parallel_tool_calls: "unknown",
    native_search: false,
    vision: false,
    image_input: false,
    prompt_cache: true,
    credential_status: "configured",
    availability: "available",
    selection_mode: "hot"
  };
}

function envelope(result: unknown): Response {
  return response({version: 1, result});
}

function envelopeProblem(code: string, message: string, retryable: boolean): Response {
  return new Response(JSON.stringify({
    version: 1,
    problem: {version: 1, code, message, retryable}
  }), {
    status: 503,
    headers: {"Content-Type": "application/json"}
  });
}

class MemoryBrowserStorage implements BrowserStorage {
  readonly values = new Map<string, BrowserProjectionState>();

  async load(scope: string): Promise<BrowserProjectionState | undefined> {
    return this.values.get(scope);
  }

  async save(scope: string, state: BrowserProjectionState): Promise<void> {
    this.values.set(scope, structuredClone(state));
  }
}

function response(value: unknown): Response {
  return new Response(JSON.stringify(value), {
    status: 200,
    headers: {"Content-Type": "application/json"}
  });
}
