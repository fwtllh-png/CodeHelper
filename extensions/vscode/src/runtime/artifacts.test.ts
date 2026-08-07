import * as assert from "node:assert/strict";
import test from "node:test";
import {
  decodeCheckpointList,
  decodeCheckpointRestore,
  decodeSessionPlan,
  SessionArtifactCommands,
} from "./artifacts.js";

const checkpoint = {
  version: 1,
  id: "checkpoint_1",
  session_id: "session_1",
  thread_id: "thread_1",
  turn_id: "turn_1",
  cursor: 12,
  status: "completed",
  summary: "Updated parser",
  profile_revision: 2,
  changed_files: 1,
  external_side_effects: true,
  side_effect_note: "Tool effects remain applied",
  can_restore: true,
  can_fork: true,
  created_at: "2026-08-07T00:00:00Z",
} as const;

void test("strictly decodes Checkpoints and rejects side-effect replay", () => {
  const list = decodeCheckpointList({
    version: 1,
    session_id: "session_1",
    checkpoints: [checkpoint],
  });
  assert.equal(list.checkpoints[0]?.changed_files, 1);
  assert.throws(() => decodeCheckpointRestore({
    version: 1,
    checkpoint,
    thread_id: "thread_1",
    restored_cursor: 12,
    side_effects_replayed: true,
  }), /replay side effects/);
});

void test("decodes an immutable structured Plan Artifact", () => {
  const result = decodeSessionPlan({
    version: 1,
    artifact: {
      version: 1,
      id: "plan_1",
      session_id: "session_1",
      thread_id: "thread_1",
      turn_id: "turn_1",
      cursor: 10,
      status: "ready",
      body: "1. Update parser",
      profile_revision: 2,
      can_implement: true,
      can_autopilot: true,
      created_at: "2026-08-07T00:00:00Z",
    },
  });
  assert.equal(result.artifact?.status, "ready");
});

void test("submits only bounded Checkpoint and Plan intents", async () => {
  const calls: Array<{ method: string; params: unknown }> = [];
  const commands = new SessionArtifactCommands({
    request: (method, params) => {
      calls.push({ method, params });
      return Promise.resolve({
        operationId: "operation_1",
        accepted: true,
        kind: "turn.start",
        threadId: "thread_1",
        turnId: "turn_2",
        itemId: "item_2",
      });
    },
  });
  await commands.implementPlan("session_1", "plan_1", "autopilot");
  assert.deepEqual(calls, [{
    method: "plan/implement",
    params: {
      sessionId: "session_1",
      planId: "plan_1",
      transition: "autopilot",
    },
  }]);
});
