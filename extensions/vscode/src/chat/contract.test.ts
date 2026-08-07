import assert from "node:assert/strict";
import test from "node:test";

import {
  chatHostMessageTypes,
  chatViewProtocolVersion,
  createChatErrorMessage,
  createChatSnapshotMessage,
} from "./contract.js";
import { projectComposer } from "./composer.js";

void test("Chat V1 host protocol is snapshot-only between terminal errors", () => {
  assert.deepEqual(chatHostMessageTypes, ["snapshot", "error"]);
});

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
        status: "running",
        pinned: true,
        archived: false,
        workspaceLabel: "workspace",
        pendingApprovals: 0,
        pendingInputs: 0,
        totalTokens: 42,
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
        title: "Background task",
        isolation: "worktree",
        status: "completed",
        pinned: false,
        archived: false,
        workspaceLabel: "workspace",
        pendingApprovals: 0,
        pendingInputs: 0,
        totalTokens: 12,
        costMicrounits: 2,
        costKnown: true,
        createdAt: "2026-08-06T00:00:00Z",
        updatedAt: "2026-08-06T01:00:00Z",
        selected: false,
        replayedEvents: 4,
        active: false,
      },
    ],
    sessionSearch: {
      query: "background",
      sessionIds: ["session_2"],
    },
    mergePlanId: "b".repeat(64),
    roots: [
      { id: "a".repeat(64), label: "workspace" },
      { id: "c".repeat(64), label: "library" },
    ],
    composer: projectComposer({
      profile: {
        version: 1,
        revision: 2,
        mode: "act",
        provider: "fixture",
        model: "fixture-model",
        approval_posture: "suggest",
        execution_target: "local",
        max_steps: 8,
        prompt_cache_revision: 1,
      },
      capabilities: {
        provider: "fixture",
        model: "fixture-model",
        model_capabilities: {
          streaming: true,
          reasoning: false,
          tool_calls: true,
          native_search: false,
          vision: false,
          image_input: false,
          prompt_cache: false,
        },
        mutable_fields: ["mode", "approval_posture"],
      },
    }, {
      version: 1,
      catalog_id: "catalog-1",
      generation: 1,
      digest: "digest-1",
      tools: [],
    }, {
      status: "configured",
      provider: "fixture",
      source: "external",
    }, true),
  });

  assert.equal(message.type, "snapshot");
  assert.equal(message.version, chatViewProtocolVersion);
  assert.equal(message.runtime.state, "ready");
  assert.equal(message.runtime.selectedSessionId, "session_1");
  assert.equal(message.runtime.sessions[0]?.active, true);
  assert.equal(message.runtime.sessions[1]?.active, false);
  assert.equal(message.runtime.mergePlanId, "b".repeat(64));
  assert.deepEqual(message.runtime.sessionSearch, {
    query: "background",
    sessionIds: ["session_2"],
  });
  assert.equal(message.presentation.stopEnabled, true);
  assert.equal(message.composer?.model.value, "fixture-model");
  assert.equal(
    message.presentation.journey,
    "Empty · Ready for a task · Enter a prompt",
  );
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
  assert.equal("sessionSearch" in message.runtime, false);
});

void test("Chat host errors use the same versioned finite protocol", () => {
  assert.deepEqual(createChatErrorMessage("runtime unavailable"), {
    type: "error",
    version: chatViewProtocolVersion,
    message: "runtime unavailable",
  });
});
