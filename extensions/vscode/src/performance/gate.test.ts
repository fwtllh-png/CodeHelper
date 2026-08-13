import assert from "node:assert/strict";
import { mkdirSync, writeFileSync } from "node:fs";
import test from "node:test";
import { performance } from "node:perf_hooks";

import { BackgroundProjector, type TaskRow } from "../background/model.js";
import {
  createChatPatchMessage,
  createChatSnapshotMessage,
} from "../chat/contract.js";
import { ChatProjector } from "../chat/projector.js";
import { projectChatResources } from "../chat/resources.js";
import {
  filterChatSessions,
  groupChatSessions,
} from "../chat/session-list.js";
import { computeVirtualWindow } from "../chat/virtual-list.js";
import {
  computeTranscriptWindow,
  restoredAnchorScrollTop,
} from "../chat/transcript-window.js";
import { decodeEvent } from "../protocol/decode.js";

const eventCount = 10_000;
const maxDurationMS = 1_000;
const maxHeapGrowthBytes = 32 << 20;
const metrics: Record<string, number> = {};

test.after(() => {
  mkdirSync("dist/performance", { recursive: true });
  writeFileSync(
    "dist/performance/projectors.json",
    `${JSON.stringify({ schema_version: 1, ...metrics }, null, 2)}\n`,
  );
});

void test("ChatProjector processes 10k deltas within the V1 budget", () => {
  const projector = new ChatProjector();
  projector.apply(event(1, "turn.started", {
    provider: "fixture", model: "fixture", prompt: "load",
  }));
  const beforeHeap = process.memoryUsage().heapUsed;
  const started = performance.now();
  for (let sequence = 2; sequence <= eventCount; sequence++) {
    projector.apply(event(sequence, "output.delta", { text: "0123456789abcdef" }));
  }
  const durationMS = performance.now() - started;
  const heapGrowth = Math.max(0, process.memoryUsage().heapUsed - beforeHeap);
  const snapshot = projector.snapshot();
  metrics["chat_10k_duration_ms"] = Number(durationMS.toFixed(1));
  metrics["chat_10k_heap_growth_bytes"] = heapGrowth;

  assert.ok(
    durationMS < maxDurationMS,
    `10k event projection took ${durationMS.toFixed(1)}ms`,
  );
  assert.ok(
    heapGrowth < maxHeapGrowthBytes,
    `10k event projection grew heap by ${String(heapGrowth)} bytes`,
  );
  assert.ok(snapshot.turns[0]?.output.endsWith("...[truncated]"));
});

void test("BackgroundProjector refreshes 1000 Tree rows within budget", () => {
  const projector = new BackgroundProjector();
  const rows: TaskRow[] = Array.from({ length: 1_000 }, (_, index) => ({
    id: `task-${String(index)}`,
    sessionId: "session-1",
    threadId: "thread-1",
    turnId: "turn-1",
    kind: "agent",
    state: "running",
    executor: index % 2 === 0 ? "agent_turn" : "",
    attempt: 1,
    maxAttempts: 3,
    failureReason: "",
    updatedAt: `2026-08-05T00:${String(index % 60).padStart(2, "0")}:00Z`,
  }));
  const started = performance.now();
  projector.replaceTasks(rows);
  const snapshot = projector.snapshot();
  const durationMS = performance.now() - started;
  metrics["tree_1000_duration_ms"] = Number(durationMS.toFixed(1));
  assert.equal(snapshot.tasks.length, 1_000);
  assert.equal(snapshot.jobs.length, 500);
  assert.ok(
    durationMS < 100,
    `1000-row Tree projection took ${durationMS.toFixed(1)}ms`,
  );
});

void test("BackgroundProjector processes 10k Agent events within budget", () => {
  const projector = new BackgroundProjector();
  const beforeHeap = process.memoryUsage().heapUsed;
  const started = performance.now();
  projector.applyEvent(event(1, "agent.spawned", {
    agent_id: "agent-1",
    role: "explore",
    depth: 0,
    detail: {
      path: "/root/inspect",
      revision: 1,
      workspace: "/workspace",
      session_id: "session-1",
      thread_id: "thread-agent-1",
      parent_path: "/root",
      status: "requested",
    },
  }), true);
  for (let sequence = 2; sequence <= eventCount; sequence += 1) {
    projector.applyEvent(event(sequence, "agent.status", {
      agent_id: "agent-1",
      status: sequence === eventCount ? "completed" : "running",
      message: `transition ${String(sequence)}`,
      detail: {
        path: "/root/inspect",
        revision: sequence,
      },
    }), true);
  }
  const durationMS = performance.now() - started;
  const heapGrowth = Math.max(0, process.memoryUsage().heapUsed - beforeHeap);
  const snapshot = projector.snapshot();
  metrics["agent_10k_duration_ms"] = Number(durationMS.toFixed(1));
  metrics["agent_10k_heap_growth_bytes"] = heapGrowth;
  metrics["agent_timeline_retained"] = snapshot.agentTimeline.length;

  assert.equal(snapshot.agents[0]?.status, "completed");
  assert.equal(snapshot.agentTimeline.length, 512);
  assert.ok(
    durationMS < maxDurationMS,
    `10k Agent event projection took ${durationMS.toFixed(1)}ms`,
  );
  assert.ok(
    heapGrowth < maxHeapGrowthBytes,
    `10k Agent event projection grew heap by ${String(heapGrowth)} bytes`,
  );
});

void test("ChatProjector ignores workspace agent events in the Chat transcript", () => {
  const projector = new ChatProjector();
  assert.equal(projector.apply(event(1, "agent.status", {
    agent_id: "agent-1",
    workspace_root: "/workspace",
    session_id: "session-1",
    status: "completed",
  })), false);
  assert.equal(projector.snapshot().turns.length, 0);
});

void test("Chat assembles the maximum V1 transcript and Session list within budget", () => {
  const projector = new ChatProjector();
  let sequence = 0;
  for (let index = 0; index < 200; index++) {
    const turnID = `turn_${String(index)}`;
    projector.apply(event(++sequence, "turn.started", {
      provider: "fixture",
      model: "fixture",
      display_prompt: `inspect file ${String(index)}`,
    }, turnID));
    projector.apply(event(++sequence, "turn.completed", {
      text: `result ${String(index)}`,
    }, turnID));
  }
  const sessions = Array.from({ length: 32 }, (_, index) => ({
    sessionId: `session_${String(index)}`,
    threadId: `thread_${String(index)}`,
    title: `Session ${String(index)}`,
    isolation: index === 0 ? "shared" as const : "worktree" as const,
    status: index === 1 ? "running" : "completed",
    pinned: index === 0,
    archived: false,
    workspaceLabel: "workspace",
    executionEnvironment: "local" as const,
    pendingApprovals: 0,
    pendingInputs: 0,
    checkpointCount: 0,
    changedFiles: 0,
    totalTokens: index,
    costMicrounits: 0,
    costKnown: false,
    createdAt: "2026-08-07T00:00:00Z",
    updatedAt: "2026-08-07T01:00:00Z",
    selected: index === 0,
    replayedEvents: index,
    active: index === 1,
  }));

  const started = performance.now();
  const resourceProjection = projectChatResources({
    ...projector.snapshot(),
    turns: projector.snapshot().turns.map((turn, index) => ({
      ...turn,
      contextSelections: [{
        path: `src/file-${String(index)}.ts`,
        kind: "source",
        reasons: ["search"],
        evidence: ["search"],
        score: 1,
        critical: false,
        included: true,
        truncated: false,
      }],
    })),
  }, "a".repeat(64), "session_0");
  const message = createChatSnapshotMessage({
    revision: 1,
    snapshot: resourceProjection.snapshot,
    resources: resourceProjection.views,
    state: "ready",
    trusted: true,
    selectedRootId: "a".repeat(64),
    selectedRootLabel: "workspace",
    sessions,
    roots: [{ id: "a".repeat(64), label: "workspace" }],
  });
  const durationMS = performance.now() - started;
  const bytes = Buffer.byteLength(JSON.stringify(message));
  metrics["chat_200_turn_snapshot_ms"] = Number(durationMS.toFixed(1));
  metrics["chat_200_turn_snapshot_bytes"] = bytes;
  metrics["chat_200_resource_count"] = resourceProjection.references.length;

  assert.equal(message.snapshot.turns.length, 200);
  assert.equal(message.runtime.sessions.length, 32);
  assert.equal(message.resources.length, 200);
  assert.ok(
    durationMS < 100,
    `200-turn Chat snapshot assembly took ${durationMS.toFixed(1)}ms`,
  );
  assert.ok(
    bytes < 2 << 20,
    `200-turn Chat snapshot is ${String(bytes)} bytes`,
  );
  const latest = message.snapshot.turns.at(-1);
  assert.ok(latest);
  const patchStarted = performance.now();
  const next = {
    ...message,
    revision: 2,
    snapshot: {
      ...message.snapshot,
      turns: [
        ...message.snapshot.turns.slice(0, -1),
        { ...latest, output: `${latest.output} patched` },
      ],
    },
  };
  const patch = createChatPatchMessage(message, next);
  const patchDurationMS = performance.now() - patchStarted;
  assert.ok(patch);
  const patchBytes = Buffer.byteLength(JSON.stringify(patch));
  metrics["chat_200_turn_patch_ms"] = Number(patchDurationMS.toFixed(1));
  metrics["chat_200_turn_patch_bytes"] = patchBytes;
  metrics["chat_200_turn_patch_operations"] = patch.operations.length;
  metrics["chat_200_turn_affected_dom_nodes"] = patch.operations.filter(
    (operation) =>
      operation.kind === "turn.upsert" || operation.kind === "turn.remove",
  ).length;
  metrics["chat_200_turn_virtual_dom_nodes"] =
    computeTranscriptWindow(200, 18_000, 720).end -
    computeTranscriptWindow(200, 18_000, 720).start;
  const restoredScroll = restoredAnchorScrollTop(9_000, 45, 15);
  metrics["chat_scroll_anchor_error_px"] = Math.abs(
    15 - (45 - (restoredScroll - 9_000)),
  );
  assert.ok(patchDurationMS < 100);
  assert.ok(patchBytes < bytes / 4);
  assert.equal(metrics["chat_200_turn_affected_dom_nodes"], 1);
  assert.ok(metrics["chat_200_turn_virtual_dom_nodes"] <= 20);
  assert.equal(metrics["chat_scroll_anchor_error_px"], 0);
});

void test("1000 Session search and virtual first paint stay within budget", () => {
  const sessions = Array.from({ length: 1_000 }, (_, index) => ({
    sessionId: `session_${String(index)}`,
    threadId: `thread_${String(index)}`,
    title: `Session ${String(index)} parser review`,
    isolation: "worktree" as const,
    status: index % 23 === 0 ? "running" as const : "completed" as const,
    pinned: index < 5,
    archived: index > 980,
    workspaceLabel: "workspace",
    executionEnvironment: "local" as const,
    provider: "fixture",
    model: "fixture-model",
    mode: "act",
    pendingApprovals: 0,
    pendingInputs: 0,
    checkpointCount: index % 4,
    changedFiles: index % 7,
    totalTokens: index,
    costMicrounits: 0,
    costKnown: false,
    createdAt: "2026-08-01T00:00:00Z",
    updatedAt: `2026-08-07T00:${
      String(index % 60).padStart(2, "0")
    }:00Z`,
    selected: index === 0,
    replayedEvents: index,
    active: index % 23 === 0,
  }));
  const started = performance.now();
  const filtered = filterChatSessions(sessions, "parser", "completed", {
    workspace: "workspace",
    model: "fixture/fixture-model",
    mode: "act",
    activity: "changed",
  });
  const groups = groupChatSessions(filtered, new Date("2026-08-07T12:00:00Z"));
  const items = groups.flatMap((group) => [
    { height: 24 },
    ...group.sessions.map(() => ({ height: 52 })),
  ]);
  const window = computeVirtualWindow(items, 18_000, 600, 260);
  const durationMS = performance.now() - started;
  metrics["session_1000_search_virtual_ms"] = Number(durationMS.toFixed(1));
  metrics["session_1000_virtual_dom_items"] = window.end - window.start;

  assert.equal(filtered.length, 820);
  assert.ok(
    durationMS < 150,
    `1000 Session search/virtualization took ${durationMS.toFixed(1)}ms`,
  );
  assert.ok(
    window.end - window.start < 30,
    `virtual Session DOM includes ${String(window.end - window.start)} rows`,
  );
});

function event(
  sequence: number,
  kind: string,
  data: unknown,
  turnID = "turn_1",
) {
  return decodeEvent({
    version: 1,
    id: `event_${String(sequence)}`,
    sequence,
    operation_id: "operation_1",
    thread_id: "thread_1",
    turn_id: turnID,
    item_id: "item_1",
    kind,
    created_at: "2026-08-04T00:00:00Z",
    data,
  });
}
