import {fireEvent, render, screen} from "@testing-library/react";
import {describe, expect, it, vi} from "vitest";

import {SessionProgress} from "./SessionProgress";

describe("SessionProgress", () => {
  it("renders goal, ordered tasks, subagents, and trajectory navigation", () => {
    const onOpenTrajectory = vi.fn();
    const onPlanTransition = vi.fn();
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
          body: `{"version":1,"revision":2,"objective":"Ship the parser fix",` +
            `"steps":[{"id":"implement","title":"Implement parser",` +
            `"status":"pending","expected_evidence":"Focused tests pass"}]}`,
          document: {
            version: 1,
            revision: 2,
            title: "Parser rollout",
            objective: "Ship the parser fix",
            steps: [{
              id: "implement",
              title: "Implement parser",
              status: "pending",
              expected_evidence: "Focused tests pass"
            }]
          },
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
        onPlanTransition={onPlanTransition}
        onOpenTrajectory={onOpenTrajectory}
      />
    );

    expect(screen.getByText("Parser rollout")).toBeTruthy();
    expect(screen.getByText("Ship the parser fix")).toBeTruthy();
    expect(screen.getByText("Revision 2")).toBeTruthy();
    expect(screen.getByText("Focused tests pass")).toBeTruthy();
    expect(screen.getByText("1/2 complete")).toBeTruthy();
    expect(screen.getByText("Checking the diff")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", {name: "Open trajectory"}));
    expect(onOpenTrajectory).toHaveBeenCalledOnce();
    fireEvent.click(screen.getByRole("button", {name: "Implement"}));
    expect(onPlanTransition).toHaveBeenCalledWith("implement");
    expect(screen.getByRole("button", {name: "Autopilot"}).hasAttribute("disabled"))
      .toBe(true);
  });

  it("never exposes serialized plan JSON as the summary title", () => {
    const body = `{"version":1,"revision":1,` +
      `"constraints":["Keep the prefix stable"],"steps":[]}`;
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
          body,
          document: {version: 1, revision: 1, steps: []},
          profile_revision: 1,
          can_implement: true,
          can_autopilot: true,
          created_at: "2026-01-01T00:00:00Z"
        }}
        tasks={[]}
        agents={[]}
        onOpenTrajectory={vi.fn()}
      />
    );

    expect(screen.getByText("Implementation plan")).toBeTruthy();
    expect(screen.queryByText(body)).toBeNull();
  });
});
