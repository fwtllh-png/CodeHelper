import assert from "node:assert/strict";
import test from "node:test";

import { deriveChatPresentation } from "./presentation.js";
import type { ChatSnapshot, ChatTurn, TurnStatus } from "./projector.js";
import type { SupervisorState } from "../runtime/supervisor.js";

void test("Chat presentation freezes every lifecycle label", () => {
  const cases: readonly {
    readonly state: SupervisorState;
    readonly trusted: boolean;
    readonly snapshot: ChatSnapshot;
    readonly label: string;
  }[] = [
    {
      state: "recovering",
      trusted: true,
      snapshot: { turns: [] },
      label: "Recovery · Restoring Chat and cursor · Wait",
    },
    {
      state: "starting",
      trusted: true,
      snapshot: { turns: [] },
      label: "Loading · Runtime starting · Wait",
    },
    {
      state: "failed",
      trusted: true,
      snapshot: { turns: [] },
      label: "Failure · Runtime unavailable · Inspect and Repair",
    },
    {
      state: "ready",
      trusted: false,
      snapshot: { turns: [] },
      label: "Setup · Read-only workspace · Trust workspace or run Setup",
    },
    {
      state: "ready",
      trusted: true,
      snapshot: active("awaiting_approval"),
      label: "Approval · Review target and effect · Approve, Deny, or Cancel",
    },
    {
      state: "ready",
      trusted: true,
      snapshot: active("awaiting_input"),
      label: "Input · Answer required · Choose or type a response",
    },
    {
      state: "ready",
      trusted: true,
      snapshot: active("running"),
      label: "Streaming · Turn in progress · Stop is available",
    },
    {
      state: "ready",
      trusted: true,
      snapshot: { turns: [turn("completed", { verification: "passed" })] },
      label: "Verify · Verdict available · Review checks and Receipt",
    },
    {
      state: "ready",
      trusted: true,
      snapshot: { turns: [turn("canceled")] },
      label: "Failure · Turn did not complete · Review reason and retry",
    },
    {
      state: "ready",
      trusted: true,
      snapshot: { turns: [turn("completed")] },
      label: "Completed · Turn finished · Review changes and Receipt",
    },
    {
      state: "ready",
      trusted: true,
      snapshot: { turns: [] },
      label: "Empty · Ready for a task · Enter a prompt",
    },
  ];

  for (const value of cases) {
    assert.equal(
      deriveChatPresentation(value.state, value.snapshot, value.trusted).journey,
      value.label,
    );
  }
});

void test("Chat presentation freezes Composer and recovery visibility", () => {
  assert.deepEqual(deriveChatPresentation(
    "ready",
    active("running"),
    true,
  ), {
    journey: "Streaming · Turn in progress · Stop is available",
    runtimeReady: true,
    promptEnabled: true,
    sendEnabled: true,
    stopEnabled: true,
    newChatEnabled: true,
    repairVisible: false,
    emptyVisible: false,
  });
  assert.deepEqual(deriveChatPresentation(
    "failed",
    { turns: [] },
    true,
  ), {
    journey: "Failure · Runtime unavailable · Inspect and Repair",
    runtimeReady: false,
    promptEnabled: false,
    sendEnabled: false,
    stopEnabled: false,
    newChatEnabled: false,
    repairVisible: true,
    emptyVisible: false,
  });
  assert.equal(
    deriveChatPresentation("ready", { turns: [] }, true).emptyVisible,
    true,
  );
});

function active(status: TurnStatus): ChatSnapshot {
  return {
    turns: [turn(status)],
    activeTurnId: "turn_1",
  };
}

function turn(
  status: TurnStatus,
  overrides: Partial<ChatTurn> = {},
): ChatTurn {
  return {
    id: "turn_1",
    user: "inspect",
    status,
    output: "",
    outputMarkdown: [],
    reasoning: "",
    reasoningMarkdown: [],
    reasoningActive: false,
    tools: [],
    approvals: [],
    inputs: [],
    contextReceipts: [],
    contextSelections: [],
    diagnostics: [],
    unknownEvents: [],
    ...overrides,
  };
}
