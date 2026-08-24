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
      event(2, "reasoning.delta", {sample_id: "sample-1", text: "Find files\n"}),
      event(3, "reasoning.delta", {sample_id: "sample-1", text: "Read the manifest"}),
      event(4, "reasoning.completed", {
        sample_id: "sample-1",
        text: "Find files\nRead the manifest"
      }),
      event(5, "tool.start", {
        call_id: "call-1",
        tool: "file_read",
        arguments: {path: "README.md"}
      }),
      event(6, "tool.output", {call_id: "call-1", chunk: "# Code"}),
      event(7, "tool.result", {
        call_id: "call-1",
        output: "# CodeHelper",
        is_error: false
      }),
      event(8, "output.delta", {text: "Done"}),
      event(9, "turn.receipt", {
        outcome: "answered",
        input_tokens: 20,
        output_tokens: 4
      }),
      event(10, "turn.completed", {text: "Done", outcome: "answered"})
    ];
    const incremental = new ConversationProjection();
    for (const value of events) incremental.apply(value);

    expect(serializable(incremental.snapshot())).toEqual(
      serializable(projectConversation(events))
    );
  });

  it("replaces transient reasoning with one durable block per model sample", () => {
    const snapshot = projectConversation([
      event(1, "turn.started", {display_prompt: "Inspect"}),
      event(2, "reasoning.delta", {sample_id: "one", text: "First draft"}),
      event(3, "reasoning.completed", {sample_id: "one", text: "First thought"}),
      event(4, "tool.start", {
        call_id: "call",
        tool: "file_read",
        arguments: {path: "README.md"}
      }),
      event(5, "tool.result", {
        call_id: "call",
        output: "content",
        is_error: false
      }),
      event(6, "reasoning.completed", {sample_id: "two", text: "Second thought"}),
      event(7, "turn.completed", {text: "Done"})
    ]);
    const reasoning = snapshot.order.flatMap((id) => {
      const node = snapshot.nodes.get(id);
      return node?.kind === "reasoning" ? [node] : [];
    });

    expect(reasoning).toMatchObject([
      {text: "First thought", running: false},
      {text: "Second thought", running: false}
    ]);
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

  it("retains an approval edit plan on the associated tool result", () => {
    const snapshot = projectConversation([
      event(1, "tool.start", {
        call_id: "write-1",
        tool: "file_edit",
        arguments: {path: "main.go", old: "old", new: "new"}
      }),
      event(2, "approval.required", {
        request_id: "approval-1",
        call_id: "write-1",
        tool: "file_edit",
        edit_plan: {
          id: "plan-1",
          diff: "--- a/main.go\n+++ b/main.go\n-old\n+new\n",
          files: [{
            path: "main.go",
            kind: "modified",
            before: "old\n",
            after: "new\n",
            before_exists: true,
            after_exists: true
          }]
        }
      }),
      event(3, "approval.resolved", {
        request_id: "approval-1",
        decision: "approve"
      }),
      event(4, "tool.result", {
        call_id: "write-1",
        tool: "file_edit",
        output: "modified main.go +1 -1",
        changes: [{path: "main.go", kind: "modified", added: 1, removed: 1}],
        is_error: false
      })
    ]);

    expect(snapshot.nodes.get("tool-write-1")).toMatchObject({
      kind: "tool",
      variant: "diff",
      approvalDecision: "approve",
      editPlan: {
        id: "plan-1",
        files: [{
          path: "main.go",
          before: "old\n",
          after: "new\n"
        }]
      }
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
