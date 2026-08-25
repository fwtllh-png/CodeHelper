import {describe, expect, it} from "vitest";

import type {RuntimeEvent} from "../protocol";
import {projectTurnQueue} from "./turnQueue";

describe("turn queue projection", () => {
  it("adds, updates, and claims queued turns in event order", () => {
    const queued = projectTurnQueue([], [
      event(1, "turn.queued", {
        queue_id: "queue-1",
        prompt: "First"
      }),
      event(2, "turn.queued", {
        queue_id: "queue-2",
        prompt: "Second"
      }),
      event(3, "turn.queue.updated", {
        queue_id: "queue-1",
        prompt: "First revised"
      }),
      event(4, "turn.started", {
        queue_id: "queue-1",
        provider: "fixture",
        model: "fixture"
      })
    ]);

    expect(queued).toMatchObject([
      {queue_id: "queue-2", prompt: "Second", added_sequence: 2}
    ]);
  });

  it("removes promoted and deleted queue items", () => {
    const initial = projectTurnQueue([], [
      event(1, "turn.queued", {queue_id: "queue-1", prompt: "First"}),
      event(2, "turn.queued", {queue_id: "queue-2", prompt: "Second"})
    ]);
    const queued = projectTurnQueue(initial, [
      event(3, "turn.steered", {queue_id: "queue-1", prompt: "First"}),
      event(4, "turn.queue.removed", {queue_id: "queue-2", reason: "user"})
    ]);

    expect(queued).toEqual([]);
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
    operation_id: `operation-${sequence}`,
    thread_id: "thread",
    turn_id: "turn",
    item_id: `item-${sequence}`,
    kind,
    sequence,
    created_at: `2026-01-01T00:00:0${sequence}Z`,
    data
  };
}
