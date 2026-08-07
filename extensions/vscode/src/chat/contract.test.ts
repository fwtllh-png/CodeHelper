import assert from "node:assert/strict";
import test from "node:test";

import {
  chatViewProtocolVersion,
  createChatErrorMessage,
  createChatSnapshotMessage,
} from "./contract.js";

void test("Chat host snapshot freezes the current Runtime and Session projection", () => {
  const message = createChatSnapshotMessage({
    snapshot: {
      turns: [],
      activeTurnId: "turn_1",
    },
    state: "ready",
    trusted: true,
    selectedRootId: "a".repeat(64),
    selectedRootLabel: "workspace",
    sessions: [
      {
        sessionId: "session_1",
        threadId: "thread_1",
        title: "Selected task",
        isolation: "shared",
        selected: true,
        replayedEvents: 12,
        active: true,
      },
      {
        sessionId: "session_2",
        threadId: "thread_2",
        title: "Background task",
        isolation: "worktree",
        selected: false,
        replayedEvents: 4,
        active: false,
      },
    ],
    mergePlanId: "b".repeat(64),
    roots: [
      { id: "a".repeat(64), label: "workspace" },
      { id: "c".repeat(64), label: "library" },
    ],
  });

  assert.equal(message.type, "snapshot");
  assert.equal(message.version, chatViewProtocolVersion);
  assert.equal(message.runtime.state, "ready");
  assert.equal(message.runtime.selectedSessionId, "session_1");
  assert.equal(message.runtime.sessions[0]?.active, true);
  assert.equal(message.runtime.sessions[1]?.active, false);
  assert.equal(message.runtime.mergePlanId, "b".repeat(64));
  assert.deepEqual(
    message.runtime.roots.map((root) => root.label),
    ["workspace", "library"],
  );
});

void test("Chat host snapshot omits absent optional state", () => {
  const message = createChatSnapshotMessage({
    snapshot: { turns: [] },
    state: "starting",
    trusted: false,
    selectedRootId: "a".repeat(64),
    selectedRootLabel: "workspace",
    sessions: [],
    roots: [{ id: "a".repeat(64), label: "workspace" }],
  });

  assert.equal("error" in message.runtime, false);
  assert.equal("selectedSessionId" in message.runtime, false);
  assert.equal("mergePlanId" in message.runtime, false);
});

void test("Chat host errors use the same versioned finite protocol", () => {
  assert.deepEqual(createChatErrorMessage("runtime unavailable"), {
    type: "error",
    version: chatViewProtocolVersion,
    message: "runtime unavailable",
  });
});
