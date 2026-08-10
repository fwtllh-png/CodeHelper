import assert from "node:assert/strict";
import test from "node:test";

import {
  createChatPatchMessage,
  createChatSnapshotMessage,
} from "../contract.js";
import {
  ChatWebviewStore,
  transcriptTurnsAffectedByPatch,
} from "./store.js";

function snapshot(revision: number, output = "") {
  return createChatSnapshotMessage({
    revision,
    snapshot: {
      turns: output === ""
        ? []
        : [{
            id: "turn_1",
            user: "request",
            status: "running",
            output,
            outputMarkdown: [],
            reasoning: "",
            reasoningMarkdown: [],
            reasoningActive: false,
            timeline: [],
            tools: [],
            approvals: [],
            inputs: [],
            contextReceipts: [],
            contextSelections: [],
            diagnostics: [],
            unknownEvents: [],
          }],
      ...(output === "" ? {} : { activeTurnId: "turn_1" }),
    },
    state: "ready",
    trusted: true,
    selectedRootId: "a".repeat(64),
    selectedRootLabel: "workspace",
    sessions: [],
    roots: [{ id: "a".repeat(64), label: "workspace" }],
  });
}

void test("Webview Store hydrates then atomically applies a matching Patch", () => {
  const store = new ChatWebviewStore();
  const base = snapshot(1);
  const next = snapshot(2, "delta");
  store.apply(base);
  const patch = createChatPatchMessage(base, next);
  assert.ok(patch);
  const current = store.apply(patch);
  assert.equal(current.revision, 2);
  assert.equal(current.snapshot.turns[0]?.output, "delta");
});

void test("Webview Store rejects stale Patch without changing state", () => {
  const store = new ChatWebviewStore();
  const base = snapshot(1);
  store.apply(base);
  assert.throws(() => store.apply({
    type: "patch",
    version: 1,
    baseRevision: 0,
    revision: 2,
    operations: [],
  }), /base Revision is stale/u);
  assert.equal(store.current()?.revision, 1);
});

void test("Runtime merge-plan patches invalidate workspace change cards", () => {
  const base = createChatSnapshotMessage({
    revision: 1,
    snapshot: {
      turns: [{
        id: "turn_1",
        user: "request",
        status: "completed",
        output: "",
        outputMarkdown: [],
        reasoning: "",
        reasoningMarkdown: [],
        reasoningActive: false,
        timeline: [],
        tools: [],
        approvals: [],
        inputs: [],
        contextReceipts: [],
        contextSelections: [],
        diagnostics: [],
        workspaceChange: { changedCount: 170 },
        unknownEvents: [],
      }],
    },
    state: "ready",
    trusted: true,
    selectedRootId: "a".repeat(64),
    selectedRootLabel: "workspace",
    sessions: [],
    roots: [{ id: "a".repeat(64), label: "workspace" }],
  });
  const current = createChatSnapshotMessage({
    revision: 2,
    snapshot: base.snapshot,
    state: "ready",
    trusted: true,
    selectedRootId: "a".repeat(64),
    selectedRootLabel: "workspace",
    sessions: [],
    mergePlanId: "b".repeat(64),
    roots: [{ id: "a".repeat(64), label: "workspace" }],
  });
  const patch = createChatPatchMessage(base, current);
  assert.ok(patch);
  assert.deepEqual(
    [...transcriptTurnsAffectedByPatch(current, patch)],
    ["turn_1"],
  );
});
