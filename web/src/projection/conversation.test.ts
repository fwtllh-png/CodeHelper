import {describe, expect, it} from "vitest";
import type {RuntimeEvent} from "../protocol";
import {
  ConversationProjection,
  projectConversation
} from "./conversation";

describe("ConversationProjection", () => {
  it("matches a full rebuild after incremental application", () => {
    const events = [
      event(1, "turn.started", {display_prompt: "Inspect the workspace"}),
      event(2, "reasoning.delta", {text: "Find files\n"}),
      event(3, "reasoning.delta", {text: "Read the manifest"}),
      event(4, "tool.start", {
        call_id: "call-1",
        tool: "file_read",
        arguments: {path: "README.md"}
      }),
      event(5, "tool.output", {call_id: "call-1", chunk: "# Code"}),
      event(6, "tool.result", {
        call_id: "call-1",
        output: "# CodeHelper",
        is_error: false
      }),
      event(7, "output.delta", {text: "Done"}),
      event(8, "turn.receipt", {
        outcome: "answered",
        input_tokens: 20,
        output_tokens: 4
      }),
      event(9, "turn.completed", {text: "Done", outcome: "answered"})
    ];
    const incremental = new ConversationProjection();
    for (const value of events) incremental.apply(value);

    expect(serializable(incremental.snapshot())).toEqual(
      serializable(projectConversation(events))
    );
  });

  it("keeps settled node references stable when a different node streams", () => {
    const projection = new ConversationProjection();
    projection.apply(event(1, "turn.started", {display_prompt: "Question"}));
    projection.apply(event(2, "tool.start", {
      call_id: "call",
      tool: "file_read",
      arguments: {path: "README.md"}
    }));
    projection.apply(event(3, "tool.result", {
      call_id: "call",
      output: "result",
      is_error: false
    }));
    const settled = projection.snapshot().nodes.get("tool-call");

    projection.apply(event(4, "output.delta", {text: "A"}));
    projection.apply(event(5, "output.delta", {text: "B"}));

    expect(projection.snapshot().nodes.get("tool-call")).toBe(settled);
  });

  it("summarizes result handles without exposing output by default", () => {
    const snapshot = projectConversation([
      event(1, "tool.start", {
        call_id: "result",
        tool: "result_get",
        arguments: {handle: "sha256:abc"}
      }),
      event(2, "tool.result", {
        call_id: "result",
        output: "long result",
        is_error: false
      })
    ]);
    const node = snapshot.nodes.get("tool-result");

    expect(node).toMatchObject({
      kind: "tool",
      summary: "Read full result · sha256:abc",
      state: "completed"
    });
  });

  it("tracks pending interaction and active turn state from facts", () => {
    const projection = new ConversationProjection();
    projection.apply(event(1, "turn.started", {display_prompt: "Change it"}));
    projection.apply(event(2, "approval.required", {request_id: "approval"}));
    expect(projection.snapshot()).toMatchObject({
      activeTurnID: "turn",
      pendingApproval: {id: "event-2"}
    });

    projection.apply(event(3, "approval.resolved", {request_id: "approval"}));
    projection.apply(event(4, "turn.completed", {text: "Changed"}));
    expect(projection.snapshot()).toMatchObject({
      activeTurnID: "",
      pendingApproval: undefined
    });
  });
});

function serializable(snapshot: ReturnType<typeof projectConversation>) {
  return {
    order: snapshot.order,
    nodes: [...snapshot.nodes],
    activeTurnID: snapshot.activeTurnID,
    pendingApproval: snapshot.pendingApproval,
    pendingInput: snapshot.pendingInput
  };
}

function event(
  sequence: number,
  kind: string,
  data: Record<string, unknown>
): RuntimeEvent {
  return {
    version: 1,
    id: `event-${sequence}`,
    kind,
    operation_id: "operation",
    thread_id: "thread",
    turn_id: "turn",
    item_id: `item-${sequence}`,
    sequence,
    created_at: `2026-01-01T00:00:0${sequence}Z`,
    data
  };
}
