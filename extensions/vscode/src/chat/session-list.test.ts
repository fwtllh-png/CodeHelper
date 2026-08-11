import assert from "node:assert/strict";
import test from "node:test";

import type { ChatSessionView } from "./contract.js";
import {
  filterChatSessions,
  groupChatSessions,
  sessionStatusLabel,
} from "./session-list.js";

const sessions: readonly ChatSessionView[] = [
  {
    sessionId: "session_1",
    threadId: "thread_1",
    title: "Fix Login Recovery",
    isolation: "shared",
    status: "running",
    pinned: true,
    archived: false,
    workspaceLabel: "workspace",
    executionEnvironment: "local",
    pendingApprovals: 0,
    pendingInputs: 0,
    checkpointCount: 0,
    changedFiles: 0,
    totalTokens: 12,
    costMicrounits: 0,
    costKnown: false,
    createdAt: "2026-08-07T00:00:00Z",
    updatedAt: "2026-08-07T01:00:00Z",
    selected: true,
    replayedEvents: 12,
    active: true,
  },
  {
    sessionId: "session_2",
    threadId: "thread_2",
    title: "Review API Changes",
    isolation: "worktree",
    status: "completed",
    pinned: false,
    archived: false,
    workspaceLabel: "workspace",
    executionEnvironment: "local",
    pendingApprovals: 0,
    pendingInputs: 0,
    checkpointCount: 0,
    changedFiles: 0,
    totalTokens: 4,
    costMicrounits: 1,
    costKnown: true,
    createdAt: "2026-08-06T00:00:00Z",
    updatedAt: "2026-08-06T01:00:00Z",
    selected: false,
    replayedEvents: 4,
    active: false,
  },
];

void test("Session search is case-insensitive and preserves Runtime order", () => {
  assert.deepEqual(
    filterChatSessions(sessions, "  API  ").map((session) => session.sessionId),
    ["session_2"],
  );
  assert.equal(filterChatSessions(sessions, "missing").length, 0);
  assert.equal(filterChatSessions(sessions, ""), sessions);
});

void test("Session status distinguishes active and durable history", () => {
  const active = sessions[0];
  const idle = sessions[1];
  assert.ok(active);
  assert.ok(idle);
  assert.equal(sessionStatusLabel(active), "Running · shared");
  assert.equal(sessionStatusLabel(idle), "Completed · worktree");
});

void test("Session grouping keeps pinned and recent sessions distinct", () => {
  const now = new Date(2026, 7, 7, 12);
  const first = sessions[0];
  const second = sessions[1];
  assert.ok(first);
  assert.ok(second);
  const groupedSessions: readonly ChatSessionView[] = [
    first,
    { ...second, updatedAt: new Date(2026, 7, 6, 12).toISOString() },
  ];
  const groups = groupChatSessions(groupedSessions, now);
  assert.deepEqual(groups.map((group) => group.label), [
    "Pinned",
    "Yesterday",
  ]);
  assert.deepEqual(
    filterChatSessions(sessions, "", "completed").map(
      (session) => session.sessionId,
    ),
    ["session_2"],
  );
});

void test("Session Rail combines workspace, model, mode, and activity filters", () => {
  const first = sessions[0];
  const second = sessions[1];
  assert.ok(first);
  assert.ok(second);
  const values: readonly ChatSessionView[] = [
    {
      ...first,
      provider: "openai",
      model: "gpt",
      mode: "act",
      changedFiles: 3,
    },
    {
      ...second,
      workspaceLabel: "other",
      provider: "deepseek",
      model: "chat",
      mode: "plan",
      parentThreadId: "thread-parent",
    },
  ];
  assert.deepEqual(
    filterChatSessions(values, "", "all", {
      workspace: "workspace",
      model: "openai/gpt",
      mode: "act",
      activity: "changed",
    }).map((session) => session.sessionId),
    ["session_1"],
  );
  assert.deepEqual(
    filterChatSessions(values, "", "all", {
      workspace: "other",
      activity: "forked",
    }).map((session) => session.sessionId),
    ["session_2"],
  );
});
