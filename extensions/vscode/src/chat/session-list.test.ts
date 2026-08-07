import assert from "node:assert/strict";
import test from "node:test";

import type { ChatSessionView } from "./contract.js";
import {
  filterChatSessions,
  sessionStatusLabel,
} from "./session-list.js";

const sessions: readonly ChatSessionView[] = [
  {
    sessionId: "session_1",
    threadId: "thread_1",
    title: "Fix Login Recovery",
    isolation: "shared",
    selected: true,
    replayedEvents: 12,
    active: true,
  },
  {
    sessionId: "session_2",
    threadId: "thread_2",
    title: "Review API Changes",
    isolation: "worktree",
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
  assert.equal(sessionStatusLabel(active), "Running");
  assert.equal(sessionStatusLabel(idle), "worktree · 4 events");
});
