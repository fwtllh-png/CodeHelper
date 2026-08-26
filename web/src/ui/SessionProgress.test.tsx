import {cleanup, fireEvent, render, screen} from "@testing-library/react";
import {afterEach, describe, expect, it, vi} from "vitest";

import {SessionProgress} from "./SessionProgress";

afterEach(cleanup);

describe("SessionProgress", () => {
  it("renders plan items, ordered tasks, subagents, and trajectory navigation", () => {
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

    expect(screen.queryByText("Parser rollout")).toBeNull();
    expect(screen.queryByText("Ship the parser fix")).toBeNull();
    expect(screen.queryByText("Revision 2")).toBeNull();
    expect(screen.getByText("Implement parser")).toBeTruthy();
    expect(screen.queryByText("Focused tests pass")).toBeNull();
    expect(screen.getByText("1/2 complete")).toBeTruthy();
    expect(screen.getByText("Checking the diff")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", {name: "Collapse plan"}));
    expect(screen.queryByText("Implement parser")).toBeNull();
    expect(screen.queryByRole("button", {name: "Implement"})).toBeNull();
    fireEvent.click(screen.getByRole("button", {name: "Expand plan"}));
    expect(screen.getByText("Implement parser")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", {name: "Open trajectory"}));
    expect(onOpenTrajectory).toHaveBeenCalledOnce();
    fireEvent.click(screen.getByRole("button", {name: "Implement"}));
    expect(onPlanTransition).toHaveBeenCalledWith("implement");
    expect(screen.getByRole("button", {name: "Autopilot"}).hasAttribute("disabled"))
      .toBe(true);
  });

  it("keeps plan transitions disabled while the source turn is settling", () => {
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
          body: `{"version":1,"revision":1,"steps":[]}`,
          document: {version: 1, revision: 1, steps: []},
          profile_revision: 1,
          can_implement: true,
          can_autopilot: true,
          created_at: "2026-01-01T00:00:00Z"
        }}
        tasks={[]}
        agents={[]}
        planBusy
        onPlanTransition={vi.fn()}
        onOpenTrajectory={vi.fn()}
      />
    );

    expect(screen.getByRole("button", {name: "Implement"}).hasAttribute("disabled"))
      .toBe(true);
    expect(screen.getByRole("button", {name: "Autopilot"}).hasAttribute("disabled"))
      .toBe(true);
  });

  it("does not render plan metadata when there are no task items", () => {
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

    expect(screen.queryByRole("region", {name: "Session progress"})).toBeNull();
    expect(screen.queryByText("Implementation plan")).toBeNull();
    expect(screen.queryByText(body)).toBeNull();
  });
});
