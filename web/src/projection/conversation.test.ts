import {describe, expect, it} from "vitest";
import type {RuntimeEvent} from "../protocol";
import {
  ConversationProjection,
  projectConversation
} from "./conversation";

describe("ConversationProjection", () => {
  it("replaces intermediate verification with the final verdict", () => {
    const snapshot = projectConversation([
      event(1, "turn.verification", {status: "unavailable", action: "repair"}),
      event(2, "turn.verification", {status: "passed", action: "passed"})
    ]);
    const verification = snapshot.order
      .map((id) => snapshot.nodes.get(id))
      .filter((node) => node?.kind === "status");

    expect(verification).toMatchObject([
      {
        title: "Checks passed",
        text: "Changed files are covered by recorded checks.",
        failed: false
      }
    ]);
  });

  it("explains an unavailable verification without internal terminology", () => {
    const snapshot = projectConversation([
      event(1, "turn.verification", {
        status: "unavailable",
        message: "structured quality evidence does not cover every changed path"
      })
    ]);

    expect(snapshot.nodes.get(snapshot.order[0])).toMatchObject({
      title: "Not fully verified",
      text: "No structured check covered every changed file.",
      failed: false
    });
  });

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

  it("presents token admission failures without protocol jargon", () => {
    const snapshot = projectConversation([
      event(1, "turn.failed", {
        code: "resource_exhausted",
        message: "token budget exhausted: projected 1139573, limit 1048576",
        fault: {
          disposition: "resume_turn",
          side_effects: "draft"
        }
      })
    ]);

    expect(snapshot.nodes.get(snapshot.order[0])).toMatchObject({
      kind: "status",
      title: "Token limit reached",
      text: "The next model call would exceed this run's token limit (1,139,573 projected, 1,048,576 allowed)."
    });
  });

  it("presents declared incomplete turns as resumable blocked work", () => {
    const snapshot = projectConversation([
      event(1, "turn.failed", {
        code: "conflict",
        message: "turn declared incomplete with resumable pending actions",
        convergence: {
          cause: "declared_incomplete",
          used: 0,
          limit: 0,
          summary: "The environment is missing a required dependency.",
          pending_actions: ["Install the dependency and continue."]
        }
      })
    ]);

    expect(snapshot.nodes.get(snapshot.order[0])).toMatchObject({
      kind: "status",
      title: "Blocked",
      text: "The environment is missing a required dependency.",
      failed: false,
      blocked: true,
      recoverable: true
    });
  });

  it("presents recoverable Runtime faults as blocked work", () => {
    const snapshot = projectConversation([
      event(1, "turn.failed", {
        code: "unavailable",
        message: "workspace journal has a retained draft",
        fault: {
          disposition: "resume_turn",
          side_effects: "draft",
          recovery_action: "continue the retained draft"
        }
      })
    ]);

    expect(snapshot.nodes.get(snapshot.order[0])).toMatchObject({
      kind: "status",
      title: "Blocked",
      failed: false,
      blocked: true,
      recoverable: true
    });
  });

  it("offers recovery for current and legacy Provider retry faults", () => {
    for (const disposition of ["retry_step", "retry_turn"]) {
      const snapshot = projectConversation([
        event(1, "turn.failed", {
          code: "unavailable",
          message: "provider rate limit retry budget exhausted",
          fault: {
            disposition,
            side_effects: "draft",
            recovery_action: "retry the turn from its durable checkpoint"
          }
        })
      ]);

      expect(snapshot.nodes.get(snapshot.order[0])).toMatchObject({
        kind: "status",
        title: "Blocked",
        blocked: true,
        recoverable: true,
        recovery: {
          canRetry: true,
          canContinue: true,
          sideEffects: "draft"
        }
      });
    }
  });

  it("presents a user interruption as paused rather than failed", () => {
    const snapshot = projectConversation([
      event(1, "turn.canceled", {reason: "user_interrupted"})
    ]);

    expect(snapshot.nodes.get(snapshot.order[0])).toMatchObject({
      kind: "status",
      title: "Paused",
      text: "Paused by user.",
      failed: false,
      warning: true,
      recoverable: true
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

  it("finishes an active turn when its start operation is rejected", () => {
    const projection = new ConversationProjection();
    projection.apply(event(1, "turn.started", {display_prompt: "Push it"}));
    projection.apply(event(2, "reasoning.delta", {text: "Working"}));
    projection.apply(event(3, "approval.required", {request_id: "approval"}));
    projection.apply(event(4, "operation.rejected", {
      code: "unavailable",
      message: "terminal envelope could not be committed"
    }));

    expect(projection.snapshot()).toMatchObject({
      activeTurnID: "",
      pendingApproval: undefined
    });
    const reasoning = [...projection.snapshot().nodes.values()].find(
      (node) => node.kind === "reasoning"
    );
    expect(reasoning).toMatchObject({
      kind: "reasoning",
      running: false
    });
  });

  it("keeps the turn active when a child operation is rejected", () => {
    const projection = new ConversationProjection();
    projection.apply(event(1, "turn.started", {display_prompt: "Push it"}));
    projection.apply({
      ...event(2, "operation.rejected", {
        code: "conflict",
        message: "approval decision rejected"
      }),
      operation_id: "child-operation"
    });

    expect(projection.snapshot().activeTurnID).toBe("turn");
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

  it("keeps shell output visible when a command changes generated files", () => {
    const snapshot = projectConversation([
      event(1, "tool.start", {
        call_id: "compile-1",
        tool: "exec_command",
        arguments: {command: "c++ source.cpp -o build/test"}
      }),
      event(2, "tool.result", {
        call_id: "compile-1",
        tool: "exec_command",
        output: "compile_exit=0\ntest_exit=1",
        changes: [{
          path: "build/test",
          kind: "modified",
          added: 0,
          removed: 0
        }],
        is_error: true
      }),
      event(3, "command.execution", {
        call_id: "compile-1",
        command: "c++ source.cpp -o build/test && ./build/test",
        status: "failed",
        exit_code: 1
      })
    ]);

    expect(snapshot.nodes.get("tool-compile-1")).toMatchObject({
      kind: "tool",
      variant: "shell",
      state: "failed",
      summary: "compile_exit=0",
      errorSummary: "exit 1",
      output: "compile_exit=0\ntest_exit=1",
      changes: [{path: "build/test"}],
      command: {exitCode: 1}
    });
  });

  it("projects file_apply patch content into the edit card", () => {
    const snapshot = projectConversation([
      event(1, "tool.start", {
        call_id: "apply-1",
        tool: "file_apply",
        arguments: {
          changes: [{
            op: "edit",
            path: "main.go",
            old: "old\n",
            new: "new\n"
          }]
        }
      }),
      event(2, "tool.result", {
        call_id: "apply-1",
        tool: "file_apply",
        output: "modified main.go +1 -1",
        changes: [{path: "main.go", kind: "modified", added: 1, removed: 1}],
        is_error: false
      })
    ]);

    expect(snapshot.nodes.get("tool-apply-1")).toMatchObject({
      kind: "tool",
      variant: "diff",
      editPlan: {
        files: [{
          path: "main.go",
          before: "old\n",
          after: "new\n"
        }]
      }
    });
  });

  it("waits for an authoritative plan for file_apply write and move operations", () => {
    const snapshot = projectConversation([
      event(1, "tool.start", {
        call_id: "apply-write",
        tool: "file_apply",
        arguments: {
          changes: [{
            op: "write",
            path: "new.go",
            content: "package main\n"
          }]
        }
      }),
      event(2, "tool.start", {
        call_id: "apply-move",
        tool: "file_apply",
        arguments: {
          changes: [{
            op: "move",
            path: "old.go",
            to: "new.go"
          }]
        }
      })
    ]);

    const write = snapshot.nodes.get("tool-apply-write");
    const move = snapshot.nodes.get("tool-apply-move");
    expect(write?.kind).toBe("tool");
    expect(move?.kind).toBe("tool");
    if (write?.kind !== "tool" || move?.kind !== "tool") {
      throw new Error("file_apply calls were not projected as tools");
    }
    expect(write.editPlan).toBeUndefined();
    expect(move.editPlan).toBeUndefined();
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
