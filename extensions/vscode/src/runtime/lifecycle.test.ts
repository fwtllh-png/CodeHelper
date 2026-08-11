import assert from "node:assert/strict";
import test from "node:test";

import {
  decodeSessionList,
  decodeSessionLifecycleUpdate,
} from "./lifecycle.js";

const summary = {
  version: 1,
  revision: 2,
  session_id: "session-1",
  thread_id: "thread-1",
  title: "Fix login",
  status: "awaiting_approval",
  pinned: true,
  archived: false,
  isolation: "worktree",
  workspace_root: "/workspace",
  workspace_label: "workspace",
  provider: "fixture",
  model: "fixture-model",
  mode: "act",
  execution_target: "local",
  latest_sequence: 9,
  pending_approvals: 1,
  pending_inputs: 0,
  checkpoint_count: 2,
  changed_files: 3,
  total_tokens: 42,
  cost_microunits: 3,
  cost_known: true,
  created_at: "2026-08-07T00:00:00Z",
  updated_at: "2026-08-07T01:00:00Z",
};

void test("session lifecycle decoder preserves Runtime-owned status", () => {
  const list = decodeSessionList({
    version: 1,
    query: "login",
    sessions: [summary],
    matches: [{
      session_id: "session-1",
      turn_id: "turn-1",
      kind: "content",
      snippet: "Fix login",
    }],
  });
  const decoded = list.sessions[0];
  assert.ok(decoded);
  assert.equal(decoded.status, "awaiting_approval");
  assert.equal(decoded.pinned, true);
  assert.equal(list.matches?.[0]?.turn_id, "turn-1");
  const update = decodeSessionLifecycleUpdate({ session: summary });
  assert.equal(update.session.revision, 2);
});

void test("session lifecycle decoder rejects drift and forged status", () => {
  assert.throws(() => decodeSessionList({
    version: 1,
    sessions: [{ ...summary, status: "forged" }],
  }), /status is invalid/u);
  assert.throws(() => decodeSessionList({
    version: 1,
    sessions: [{ ...summary, active: true }],
  }), /unknown fields/u);
  assert.throws(() => decodeSessionList({
    version: 1,
    query: "login",
    sessions: [summary],
    matches: [{
      session_id: "other-session",
      turn_id: "turn-1",
      kind: "content",
    }],
  }), /no listed Session/u);
});
