import assert from "node:assert/strict";
import test from "node:test";

import { BackgroundProjector } from "./model.js";
import type { DecodedEvent } from "../protocol/decode.js";

void test("BackgroundProjector joins read models and separates executable jobs", () => {
  const projector = new BackgroundProjector();
  projector.replaceThreads([{
    id: "thread_1", sessionId: "session_1", title: "Work",
    status: "open", updatedAt: "2026-08-04T10:00:00Z",
  }]);
  projector.replaceAgents([{
    id: "agent-1", path: "/root/inspect", revision: 3,
    workspace: "/workspace", sessionId: "session_1", threadId: "thread-agent-1",
    parentId: "", parentPath: "/root",
    role: "explore", status: "running", lastMessage: "",
    closed: false,
  }]);
  projector.replaceTasks([
    task("todo", "", "queued"),
    task("job", "agent_turn", "running"),
  ]);
  projector.replaceUsage({
    turns: 1, calls: 2, inputTokens: 10, outputTokens: 4,
    reasoningTokens: 1, cachedTokens: 3, totalTokens: 15,
    costMicrounits: 20, costKnown: true,
  });
  const snapshot = projector.snapshot();
  assert.equal(snapshot.threads[0]?.id, "thread_1");
  assert.equal(snapshot.agents[0]?.sessionId, "session_1");
  assert.equal(snapshot.integrations.length, 0);
  assert.deepEqual(snapshot.tasks.map((row) => row.id).sort(), ["job", "todo"]);
  assert.deepEqual(snapshot.jobs.map((row) => row.id), ["job"]);
  assert.equal(snapshot.usage.totalTokens, 15);
});

void test("BackgroundProjector restores approvals and deduplicates terminal notices", () => {
  const projector = new BackgroundProjector();
  projector.applyEvent(event(1, "approval.required", {
    request_id: "approval_1",
    call_id: "call_1",
    tool: "file_write",
    arguments_digest: "a".repeat(64),
    arguments: {},
    resources: [{ kind: "file", access: "write", path: "result.txt" }],
    allowed_scopes: ["once"],
    effect: "workspace.edit",
    risk: "high",
    reason_code: "approval_required",
    expires_at: "2026-08-04T12:00:00Z",
    replacement_allowed: false,
    modifiable_arguments: [],
    source: {
      kind: "agent",
      agent_id: "agent-1",
      agent_path: "/root/write_result",
      parent_path: "/root",
      role: "implementer",
      session_id: "session-1",
      workspace_root: "/workspace",
    },
  }), true);
  assert.equal(projector.snapshot().approvals.length, 1);
  assert.equal(
    projector.snapshot().approvals[0]?.agentPath,
    "/root/write_result",
  );
  projector.applyEvent(event(2, "approval.resolved", {
    request_id: "approval_1", decision: "approve",
  }), true);
  assert.equal(projector.snapshot().approvals.length, 0);

  assert.equal(
    projector.applyEvent(event(3, "agent.status", {
      agent_id: "agent-1", status: "completed",
    }), false).length,
    1,
  );
  assert.equal(
    projector.applyEvent(event(3, "agent.status", {
      agent_id: "agent-1", status: "completed",
    }), false).length,
    0,
  );
  assert.equal(
    projector.applyEvent(event(4, "turn.failed", { error: "boom" }), true).length,
    0,
  );
});

void test("BackgroundProjector retains integration candidate and receipt", () => {
  const projector = new BackgroundProjector();
  projector.applyEvent(event(1, "agent.integration", {
    agent_id: "agent-2",
    agent_path: "/root/implement",
    parent_path: "/root",
    workspace_root: "/workspace",
    session_id: "session_1",
    status: "applied",
    preview_digest: "a".repeat(64),
    paths: ["result.txt"],
    conflicts: [],
    message: "parent verification passed",
    detail: {
      receipt: {
        changed_paths: ["result.txt"],
        verification: {
          diagnostics: "not_evaluated",
          tests: "passed",
          verify: "passed",
        },
        applied_at: "2026-08-04T10:00:00Z",
      },
    },
  }), true);
  const row = projector.snapshot().integrations[0];
  assert.equal(row?.status, "applied");
  assert.equal(row.previewDigest, "a".repeat(64));
  assert.deepEqual(row.changedPaths, ["result.txt"]);
  assert.equal(row.verification, "passed");
});

void test("BackgroundProjector rebuilds Agent nodes and bounds large replay", () => {
  const projector = new BackgroundProjector();
  projector.applyEvent(event(1, "agent.spawned", {
    agent_id: "agent-1",
    role: "explorer",
    depth: 0,
    detail: {
      path: "/root/inspect",
      revision: 1,
      workspace: "/workspace",
      session_id: "session_1",
      thread_id: "thread-agent-1",
      parent_path: "/root",
      status: "requested",
    },
  }), true);
  for (let sequence = 2; sequence <= 10_000; sequence += 1) {
    projector.applyEvent(event(sequence, "agent.status", {
      agent_id: "agent-1",
      status: sequence === 10_000 ? "completed" : "running",
      message: `transition ${String(sequence)}`,
      detail: {
        path: "/root/inspect",
        expected_revision: sequence - 1,
      },
    }), true);
  }
  for (let attempt = 0; attempt < 300; attempt += 1) {
    projector.applyEvent(event(10_001 + attempt, "agent.integration", {
      agent_id: "agent-1",
      agent_path: "/root/inspect",
      parent_path: "/root",
      workspace_root: "/workspace",
      session_id: "session_1",
      status: "failed",
      preview_digest: attempt.toString(16).padStart(64, "0"),
    }), true);
  }
  const snapshot = projector.snapshot();
  assert.equal(snapshot.agents[0]?.path, "/root/inspect");
  assert.equal(snapshot.agents[0].status, "completed");
  assert.equal(snapshot.agentTimeline.length, 512);
  assert.equal(snapshot.agentTimeline.at(-1)?.sequence, 10_300);
  assert.equal(snapshot.integrations.length, 256);
});

void test("BackgroundProjector keeps Agent replay and live overlap monotonic", () => {
  const projector = new BackgroundProjector();
  projector.applyEvent(event(1, "agent.spawned", {
    agent_id: "agent-1",
    role: "explore",
    depth: 0,
    detail: {
      path: "/root/inspect",
      revision: 1,
      workspace: "/workspace",
      session_id: "session_1",
      thread_id: "thread-agent-1",
      parent_path: "/root",
      status: "requested",
    },
  }), true);
  projector.applyEvent(event(2, "agent.status", {
    agent_id: "agent-1",
    status: "running",
    detail: { path: "/root/inspect", expected_revision: 1 },
  }), true);

  // Restart replay can overlap the first buffered live page.
  projector.applyEvent(event(2, "agent.status", {
    agent_id: "agent-1",
    status: "running",
    detail: { path: "/root/inspect", expected_revision: 1 },
  }), false);
  projector.applyEvent(event(3, "agent.status", {
    agent_id: "agent-1",
    status: "completed",
    detail: { path: "/root/inspect", expected_revision: 2 },
  }), false);
  // A stale replay page must not regress the terminal node.
  projector.applyEvent(event(1, "agent.spawned", {
    agent_id: "agent-1",
    role: "explore",
    depth: 0,
  }), true);

  const snapshot = projector.snapshot();
  assert.equal(snapshot.agents[0]?.status, "completed");
  assert.equal(snapshot.agents[0].revision, 3);
  assert.deepEqual(
    snapshot.agentTimeline.map((row) => row.sequence),
    [1, 2, 3],
  );
});

void test("BackgroundProjector notifies only real task terminal transitions", () => {
  const projector = new BackgroundProjector();
  assert.equal(projector.replaceTasks([task("old", "agent_turn", "completed")]).length, 0);
  projector.replaceTasks([task("job", "agent_turn", "running")]);
  const notices = projector.replaceTasks([task("job", "agent_turn", "failed")]);
  assert.equal(notices.length, 1);
  assert.equal(notices[0]?.failed, true);
  assert.equal(
    projector.replaceTasks([task("job", "agent_turn", "failed")]).length,
    0,
  );
});

function task(id: string, executor: string, state: string) {
  return {
    id, sessionId: "session_1", threadId: "thread_1", turnId: "turn_1",
    kind: "agent", state, executor, attempt: 1, maxAttempts: 3,
    failureReason: state === "failed" ? "boom" : "",
    updatedAt: `2026-08-04T10:00:0${id === "job" ? "2" : "1"}Z`,
  };
}

function event(
  sequence: number,
  kind: string,
  data: Readonly<Record<string, unknown>>,
): DecodedEvent {
  return {
    version: 1,
    id: `event_${String(sequence).padStart(32, "0")}`,
    sequence,
    operation_id: "operation_1",
    thread_id: "thread_1",
    turn_id: "turn_1",
    item_id: "item_1",
    kind,
    created_at: "2026-08-04T10:00:00Z",
    data,
  } as DecodedEvent;
}
