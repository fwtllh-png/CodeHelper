import {cleanup, fireEvent, render, screen} from "@testing-library/react";
import {afterEach, describe, expect, it, vi} from "vitest";
import type {RuntimeEvent} from "../protocol";
import {Trajectory} from "./Trajectory";
import {experience, trajectoryDOMBudget} from "./experience";

afterEach(cleanup);

describe("Trajectory", () => {
  it("searches, folds, selects, and navigates one event ledger", () => {
    const onOpenChat = vi.fn();
    render(
      <Trajectory
        events={[
          event(1, "turn.started", {display_prompt: "inspect README"}),
          event(2, "tool.start", {
            call_id: "call-1",
            tool: "file_read",
            arguments: {path: "README.md"}
          }),
          event(3, "tool.result", {
            call_id: "call-1",
            tool: "file_read",
            output: "content",
            is_error: false
          }),
          event(4, "turn.completed", {text: "done"})
        ]}
        tracePhase="unavailable"
        traceProblem="not recorded"
        hasEarlier={false}
        onInspectConsumed={vi.fn()}
        onLoadEarlier={vi.fn(async () => 0)}
        onRetryTrace={vi.fn(async () => {})}
        onOpenChat={onOpenChat}
      />
    );

    expect(screen.getByRole("button", {name: /Duration/}).getAttribute("aria-pressed"))
      .toBe("true");
    fireEvent.click(screen.getByRole("button", {name: /Calls/}));
    expect(document.querySelectorAll(".ledgerRow[data-kind='tool']")).toHaveLength(0);
    fireEvent.click(screen.getByRole("button", {name: /Calls/}));

    fireEvent.change(screen.getByLabelText("Search trajectory"), {
      target: {value: "readme"}
    });
    expect(document.querySelectorAll(".ledgerRow")).toHaveLength(2);
    fireEvent.click(document.querySelectorAll<HTMLButtonElement>(".ledgerRow")[0]!);
    expect(screen.getByRole("complementary", {name: "Record inspector"}))
      .toBeTruthy();
    fireEvent.click(screen.getByRole("button", {name: "Show in chat"}));
    expect(onOpenChat).toHaveBeenCalledWith("turn-1", undefined);
  });

  it("derives the virtualized ledger budget from viewport and row height", () => {
    const events = Array.from({length: 500}, (_, index) =>
      event(index + 1, "host.command", {summary: `command ${index}`})
    );
    const {container} = render(
      <Trajectory
        events={events}
        tracePhase="idle"
        hasEarlier={false}
        onInspectConsumed={vi.fn()}
        onLoadEarlier={vi.fn(async () => 0)}
        onRetryTrace={vi.fn(async () => {})}
        onOpenChat={vi.fn()}
      />
    );

    expect(container.querySelectorAll(".ledgerRow").length).toBeLessThanOrEqual(
      trajectoryDOMBudget(experience.trajectory.initialViewportHeight)
    );
  });

  it("shows the average common prefix in the toolbar", () => {
    render(
      <Trajectory
        events={[
          event(1, "turn.started", {display_prompt: "inspect cache"}),
          event(2, "usage", {
            input_tokens: 200,
            cached_tokens: 150,
            context: {prefix_compared: true, prefix_common_tokens: 120}
          }),
          event(3, "turn.receipt", {
            latency: {first_token_ms: 320}
          }),
          event(4, "turn.completed", {text: "done"})
        ]}
        tracePhase="unavailable"
        hasEarlier={false}
        onInspectConsumed={vi.fn()}
        onLoadEarlier={vi.fn(async () => 0)}
        onRetryTrace={vi.fn(async () => {})}
        onOpenChat={vi.fn()}
      />
    );

    const metrics = screen.getByLabelText("Prefix metrics");
    expect(metrics.textContent).toContain("Prefix 120 tok");
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
    turn_id: sequence > 250 ? "turn-2" : "turn-1",
    item_id: `item-${sequence}`,
    sequence,
    created_at: new Date(Date.UTC(2026, 0, 1, 0, 0, sequence)).toISOString(),
    data
  };
}
