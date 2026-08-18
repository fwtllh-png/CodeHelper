import assert from "node:assert/strict";
import test from "node:test";

import type { ChatSessionSummary } from "../runtime/controller.js";
import {
  createStructuredSessionReceipt,
  renderSessionMarkdown,
  validateStructuredSessionReceipt,
} from "./export.js";

const session: ChatSessionSummary = {
  sessionId: "session-1",
  threadId: "thread-1",
  title: "Parser Review",
  isolation: "shared",
  status: "completed",
  pinned: false,
  archived: false,
  workspaceLabel: "workspace",
  provider: "fixture",
  model: "fixture-model",
  mode: "act",
  executionEnvironment: "local",
  pendingApprovals: 0,
  pendingInputs: 0,
  checkpointCount: 1,
  changedFiles: 2,
  totalTokens: 42,
  costMicrounits: 0,
  costKnown: false,
  createdAt: "2026-08-07T00:00:00Z",
  updatedAt: "2026-08-07T01:00:00Z",
  selected: true,
  replayedEvents: 4,
};

const snapshot = {
  turns: [{
    id: "turn-1",
    user: "Review parser",
    status: "completed" as const,
    output: "Parser is valid.",
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
    verificationUncoveredPaths: [],
    unknownEvents: [],
  }],
};

void test("Structured Session Receipt is deterministic and validates", () => {
  const receipt = createStructuredSessionReceipt(
    session,
    snapshot,
    "2026-08-07T02:00:00Z",
  );
  validateStructuredSessionReceipt(receipt);
  assert.equal(receipt.integrity.digest.length, 64);
  assert.throws(() => {
    validateStructuredSessionReceipt({
      ...receipt,
      session: { ...receipt.session, title: "tampered" },
    });
  });
});

void test("Markdown export is built from the Host projection", () => {
  const markdown = renderSessionMarkdown(session, snapshot);
  assert.match(markdown, /# Parser Review/u);
  assert.match(markdown, /Review parser/u);
  assert.match(markdown, /Parser is valid\./u);
});
