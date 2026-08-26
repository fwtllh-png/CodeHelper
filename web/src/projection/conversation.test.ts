import {describe, expect, it} from "vitest";
import type {RuntimeEvent} from "../protocol";
import {
  ConversationProjection,
  projectConversation
} from "./conversation";

describe("ConversationProjection", () => {
  it("derives recovery actions and side effects from Runtime facts", () => {
    const snapshot = projectConversation([
      event(1, "turn.failed", {
        code: "unavailable",
        message: "provider unavailable",
        fault: {
          disposition: "resume_turn",
          side_effects: "rolled_back",
          recovery_action: "continue from durable state"
        }
      })
    ]);

    expect(snapshot.nodes.get(snapshot.order[0])).toMatchObject({
      kind: "status",
      recovery: {
        canRetry: false,
        canContinue: true,
        sideEffects: "rolled_back",
        action: "continue from durable state"
      }
    });
  });

  it("projects produced files and marks older paths stale", () => {
    const first = [
      turnEvent(1, "turn-one", "turn.started", {display_prompt: "Edit"}),
      turnEvent(2, "turn-one", "tool.start", {
        call_id: "edit-one",
        tool: "file_edit",
        arguments: {path: "main.go", old: "old\n", new: "new\n"}
      }),
      turnEvent(3, "turn-one", "tool.result", {
        call_id: "edit-one",
        tool: "file_edit",
        output: "updated",
        changes: [{path: "main.go", kind: "modified", added: 1, removed: 1}]
      }),
      turnEvent(4, "turn-one", "turn.receipt", {
        changes: [{
          path: "main.go",
          tool: "file_edit",
          kind: "modified",
          added: 1,
          removed: 1
        }],
        verification: {tests: "passed", diagnostics: "not_evaluated"}
      }),
      turnEvent(5, "turn-one", "turn.completed", {text: "First"})
    ];
    const snapshot = projectConversation([
      ...first,
      turnEvent(6, "turn-two", "turn.started", {display_prompt: "Edit again"}),
      turnEvent(7, "turn-two", "turn.receipt", {
        changes: [{
          path: "main.go",
          tool: "file_write",
          kind: "modified",
          added: 2,
          removed: 1
        }],
        verification: {tests: "not_evaluated"}
      }),
      turnEvent(8, "turn-two", "turn.completed", {text: "Second"})
    ]);
    const deliverables = snapshot.order.flatMap((id) => {
      const node = snapshot.nodes.get(id);
      return node?.kind === "deliverables" ? [node] : [];
    });

    expect(deliverables).toMatchObject([
      {
        turnID: "turn-one",
        verification: "passed",
        files: [{
          path: "main.go",
          callID: "edit-one",
          stale: true,
          diff: {before: "old\n", after: "new\n"}
        }]
      },
      {
        turnID: "turn-two",
        verification: "unverified",
        files: [{path: "main.go", stale: false}]
      }
    ]);
  });

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

  it("projects accepted steering as a durable user interjection", () => {
    const snapshot = projectConversation([
      event(1, "turn.started", {display_prompt: "Inspect"}),
      event(2, "output.delta", {text: "Initial direction"}),
      event(3, "turn.steered", {prompt: "Focus on the parser"}),
      event(4, "output.delta", {text: "Revised direction"}),
      event(5, "turn.completed", {text: "Done"})
    ]);

    expect(snapshot.order.map((id) => snapshot.nodes.get(id))).toMatchObject([
      {kind: "user", text: "Inspect", images: []},
      {kind: "assistant", text: "Initial direction", superseded: true},
      {kind: "user", text: "Focus on the parser", images: [], steering: true},
      {kind: "assistant", text: "Done"}
    ]);
  });

  it("projects durable image inputs onto the user message", () => {
    const snapshot = projectConversation([
      event(1, "turn.started", {
        display_prompt: "Describe this image",
        images: [{
          label: "lake.png",
          media_type: "image/png",
          content: "aW1hZ2U="
        }]
      })
    ]);

    expect(snapshot.nodes.get(snapshot.order[0])).toMatchObject({
      kind: "user",
      text: "Describe this image",
      images: [{
        label: "lake.png",
        mediaType: "image/png",
        content: "aW1hZ2U="
      }]
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

function turnEvent(
  sequence: number,
  turnID: string,
  kind: string,
  data: Record<string, unknown>
): RuntimeEvent {
  return {
    ...event(sequence, kind, data),
    id: `${turnID}-event-${sequence}`,
    turn_id: turnID
  };
}
