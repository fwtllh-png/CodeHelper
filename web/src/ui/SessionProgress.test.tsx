import {fireEvent, render, screen} from "@testing-library/react";
import {describe, expect, it, vi} from "vitest";

import {SessionProgress} from "./SessionProgress";

describe("SessionProgress", () => {
  it("renders goal, ordered tasks, subagents, and trajectory navigation", () => {
    const onOpenTrajectory = vi.fn();
    render(
      <SessionProgress
        plan={{
          version: 1,
          id: "plan",
          session_id: "session",
          thread_id: "thread",
          turn_id: "turn",
          cursor: 1,
          status: "ready",
          body: "Ship the parser fix",
          profile_revision: 1,
          can_implement: true,
          can_autopilot: false,
          created_at: "2026-01-01T00:00:00Z"
        }}
        tasks={[
          {id: "done", kind: "Inspect parser", state: "succeeded"},
          {id: "active", kind: "Run tests", state: "running", reason: "focused suite"}
        ]}
        agents={[
          {
            id: "agent",
            role: "reviewer",
            status: "running",
            last_message: "Checking the diff"
          }
        ]}
        onOpenTrajectory={onOpenTrajectory}
      />
    );

    expect(screen.getAllByText("Ship the parser fix")).toHaveLength(2);
    expect(screen.getByText("1 done")).toBeTruthy();
    expect(screen.getByText("Checking the diff")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", {name: "Open trajectory"}));
    expect(onOpenTrajectory).toHaveBeenCalledOnce();
  });
});
