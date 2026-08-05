import assert from "node:assert/strict";
import { mkdirSync, writeFileSync } from "node:fs";
import test from "node:test";
import { performance } from "node:perf_hooks";

import { BackgroundProjector, type TaskRow } from "../background/model.js";
import { ChatProjector } from "../chat/projector.js";
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

function event(sequence: number, kind: string, data: unknown) {
  return decodeEvent({
    version: 1,
    id: `event_${String(sequence)}`,
    sequence,
    operation_id: "operation_1",
    thread_id: "thread_1",
    turn_id: "turn_1",
    item_id: "item_1",
    kind,
    created_at: "2026-08-04T00:00:00Z",
    data,
  });
}
