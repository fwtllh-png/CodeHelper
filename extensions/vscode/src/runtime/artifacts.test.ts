import * as assert from "node:assert/strict";
import test from "node:test";
import {
  decodeCheckpointFork,
  decodeCheckpointList,
  decodeCheckpointRestore,
  decodeSessionPlan,
  SessionArtifactCommands,
} from "./artifacts.js";

const checkpoint = {
  version: 2,
  id: "checkpoint_1",
  session_id: "session_1",
  thread_id: "thread_1",
  turn_id: "turn_1",
  cursor: 12,
  status: "completed",
  summary: "Updated parser",
  profile_revision: 2,
  state_epoch: 3,
  context_digest: "sha256:context",
  workspace_digest: "sha256:workspace",
  changed_files: 1,
  external_side_effects: true,
  side_effect_note: "Tool effects remain applied",
  can_restore: true,
  can_fork: true,
  created_at: "2026-08-07T00:00:00Z",
} as const;

void test("strictly decodes Checkpoints and rejects side-effect replay", () => {
  const list = decodeCheckpointList({
    version: 2,
    session_id: "session_1",
    checkpoints: [checkpoint],
  });
  assert.equal(list.checkpoints[0]?.changed_files, 1);
  const restored = decodeCheckpointRestore({
    version: 2,
    checkpoint,
    thread_id: "thread_1",
    restored_cursor: 12,
    side_effects_replayed: false,
    exact_context: true,
    workspace_claims_valid: false,
    invalidated_claims: 1,
    stale_claims: 1,
  });
  assert.equal(restored.exact_context, true);
  assert.equal(restored.workspace_claims_valid, false);
  assert.equal(restored.stale_claims, 1);
  assert.throws(() => decodeCheckpointRestore({
    version: 2,
    checkpoint,
    thread_id: "thread_1",
    restored_cursor: 12,
    side_effects_replayed: true,
    exact_context: true,
    workspace_claims_valid: true,
  }), /replay side effects/);
  assert.throws(() => decodeCheckpointFork({
    version: 2,
    checkpoint,
    session_id: "session_1",
    thread_id: "thread_2",
    parent_thread_id: "thread_1",
    exact_context: false,
    workspace_claims_valid: true,
  }), /require exact context/);
});

void test("decodes an immutable structured Plan Artifact", () => {
  const result = decodeSessionPlan({
    version: 2,
    artifact: {
      version: 2,
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
  await commands.implementPlan(
    "session_2",
    "plan_1",
    "implement",
    "session_1",
  );
  await commands.implementPlan(
    "session_1",
    "plan_1",
    "implement",
    "session_1",
  );
  await commands.recoverTurn(
    "session_1",
    "turn_1",
    "continue",
    "Run focused tests",
  );
  assert.deepEqual(calls.map((call) => call.method), [
    "plan/implement",
    "plan/implement",
    "plan/implement",
    "turn/recover",
  ]);
  assert.deepEqual(calls[0]?.params, {
    sessionId: "session_1",
    planId: "plan_1",
    transition: "autopilot",
  });
  assert.deepEqual(calls[1]?.params, {
    sessionId: "session_2",
    sourceSessionId: "session_1",
    planId: "plan_1",
    transition: "implement",
  });
  assert.deepEqual(calls[2]?.params, {
    sessionId: "session_1",
    sourceSessionId: "session_1",
    planId: "plan_1",
    transition: "implement",
  });
  assert.deepEqual(calls[3]?.params, {
    sessionId: "session_1",
    sourceTurnId: "turn_1",
    action: "continue",
    guidance: "Run focused tests",
    idempotencyKey: (calls[3]?.params as {
      readonly idempotencyKey: string;
    }).idempotencyKey,
  });
  assert.match(
    (calls[3].params as { readonly idempotencyKey: string }).idempotencyKey,
    /^recover-/u,
  );
});
