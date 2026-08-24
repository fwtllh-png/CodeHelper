import {describe, expect, it} from "vitest";
import type {RuntimeEvent, TraceSnapshot} from "../protocol";
import {projectTrajectory} from "./trajectory";

describe("projectTrajectory", () => {
  it("coalesces streaming records and links trace spans to tool calls", () => {
    const events = [
      event(1, "turn.started", {display_prompt: "Read README"}),
      event(2, "reasoning.delta", {text: "Find "}),
      event(3, "reasoning.delta", {text: "the file"}),
      event(4, "tool.start", {
        call_id: "call-1",
        tool: "file_read",
        arguments: {path: "README.md"}
      }),
      event(5, "tool.result", {
        call_id: "call-1",
        tool: "file_read",
        output: "# Project",
        is_error: false
      }),
      event(6, "output.delta", {text: "Summary"})
    ];
    const trace: TraceSnapshot = {
      version: 1,
      session_id: "session",
      through_sequence: 6,
      turns: [{
        turn_id: "turn",
        status: "ok",
        spans: [{
          id: 2,
          parent_id: 1,
          kind: "tool",
          status: "ok",
          started_at: "2026-01-01T00:00:04Z",
          ended_at: "2026-01-01T00:00:05Z",
          duration_ms: 1_000,
          call_id: "call-1"
        }]
      }]
    };

    const projection = projectTrajectory(events, trace);

    expect(projection.records.map((record) => record.label)).toEqual([
      "USER", "THINK", "TOOL", "ASSISTANT"
    ]);
    expect(projection.records.find((record) => record.callID === "call-1"))
      .toMatchObject({summary: "file_read · README.md -> # Project"});
    expect(projection.spans.find((span) => span.recordID === "tool-call-1"))
      .toMatchObject({
      recordID: "tool-call-1",
      lane: "tools",
      durationMS: 1_000
      });
    expect(projection.timingAvailable).toBe(true);
  });

  it("keeps event rows usable when trace timing is unavailable", () => {
    const projection = projectTrajectory([
      event(1, "turn.started", {display_prompt: "Hello"}),
      event(2, "turn.completed", {text: "Hi"})
    ]);

    expect(projection.timingAvailable).toBe(false);
    expect(projection.records.map((record) => record.label)).toEqual([
      "USER", "ASSISTANT", "TURN"
    ]);
    expect(projection.records[1]).toMatchObject({output: "Hi"});
    expect(projection.spans).toHaveLength(3);
    expect(projection.spans.every((span) => span.durationMS === undefined)).toBe(true);
  });

  it("uses event pairs when trace only exposes aggregate tool timing", () => {
    const events = [
      event(1, "turn.started", {display_prompt: "Inspect files"}),
      event(2, "tool.start", {
        call_id: "call-1",
        tool: "file_read",
        arguments: {path: "README.md"}
      }),
      event(3, "tool.result", {
        call_id: "call-1",
        tool: "file_read",
        output: "# Project",
        is_error: false
      }),
      event(4, "turn.completed", {text: "Done"})
    ];
    const trace: TraceSnapshot = {
      version: 1,
      session_id: "session",
      through_sequence: 4,
      turns: [{
        turn_id: "turn",
        status: "ok",
        spans: [{
          id: 2,
          parent_id: 1,
          kind: "tool",
          status: "ok",
          started_at: "2026-01-01T00:00:02Z",
          ended_at: "2026-01-01T00:00:03Z",
          duration_ms: 1_000
        }, {
          id: 3,
          parent_id: 1,
          kind: "verification",
          status: "ok",
          started_at: "2026-01-01T00:00:04Z",
          ended_at: "2026-01-01T00:00:04Z",
          duration_ms: 0
        }]
      }]
    };

    const projection = projectTrajectory(events, trace);
    const toolSpans = projection.spans.filter((span) => span.lane === "tools");

    expect(toolSpans).toHaveLength(1);
    expect(toolSpans[0]).toMatchObject({
      recordID: "tool-call-1",
      durationMS: 1_000
    });
    expect(projection.spans.some((span) => span.kind === "verification")).toBe(false);
    expect(projection.records.some(
      (record) => record.kind === "assistant" && record.output === "Done"
    )).toBe(true);
  });

  it("exposes approval semantics and model TTFT in the inspector projection", () => {
    const projection = projectTrajectory([
      event(1, "turn.started", {display_prompt: "Edit a file"}),
      event(2, "output.delta", {text: "Preparing"}),
      event(3, "approval.required", {
        request_id: "approval",
        call_id: "write",
        tool: "file_write",
        effect: "workspace_write"
      }),
      event(4, "usage", {
        provider: "fixture",
        input_tokens: 40,
        output_tokens: 8
      }),
      event(5, "turn.receipt", {
        outcome: "changed",
        latency: {first_token_ms: 250, provider_ms: 900}
      }),
      event(6, "turn.completed", {text: "Done"})
    ]);

    expect(projection.records.find((record) => record.label === "APPROVAL"))
      .toMatchObject({summary: "file_write · workspace_write"});
    expect(projection.records.find((record) => record.label === "USAGE"))
      .toBeTruthy();
    expect(projection.spans.find((span) => span.recordID === "output-turn"))
      .toMatchObject({ttftMS: 250});
  });
});

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
