import {describe, expect, it} from "vitest";
import type {RuntimeEvent} from "../protocol";
import {projectTranscript} from "./App";

const projectionBudgetMS = 1_000;

describe("Web projection performance gate", () => {
  it("projects ten thousand streaming deltas within budget", () => {
    const events = Array.from({length: 10_000}, (_, index) =>
      event(index + 1, "output.delta", {text: "0123456789abcdef"})
    );

    const started = performance.now();
    const transcript = projectTranscript(events);
    const duration = performance.now() - started;

    expect(duration).toBeLessThan(projectionBudgetMS);
    expect(transcript).toHaveLength(1);
    expect(transcript[0]?.text).toHaveLength(160_000);
  });

  it("projects the maximum retained transcript within budget", () => {
    const events: RuntimeEvent[] = [];
    for (let index = 0; index < 500; index += 1) {
      const turnID = `turn-${index}`;
      events.push(
        event(index * 2 + 1, "turn.started", {
          prompt: `inspect file ${index}`,
          display_prompt: `inspect file ${index}`
        }, turnID),
        event(index * 2 + 2, "turn.completed", {
          text: `result ${index}`,
          outcome: "answered"
        }, turnID)
      );
    }

    const started = performance.now();
    const transcript = projectTranscript(events);
    const duration = performance.now() - started;

    expect(duration).toBeLessThan(projectionBudgetMS);
    expect(transcript).toHaveLength(1_500);
  });
});

function event(
  sequence: number,
  kind: string,
  data: Record<string, unknown>,
  turnID = "turn"
): RuntimeEvent {
  return {
    version: 1,
    id: `event-${sequence}`,
    kind,
    operation_id: "operation",
    thread_id: "thread",
    turn_id: turnID,
    item_id: `item-${sequence}`,
    sequence,
    created_at: "2026-01-01T00:00:00Z",
    data
  };
}
