import {cleanup, fireEvent, render, screen} from "@testing-library/react";
import {afterEach, describe, expect, it, vi} from "vitest";

import type {ConversationNode} from "../projection/conversation";
import {AgentDisclosure} from "./TranscriptCards";

afterEach(cleanup);

describe("AgentDisclosure", () => {
  it("shows a running subagent execution and opens tool inspection", () => {
    const onInspect = vi.fn();
    const entry: Extract<ConversationNode, {kind: "agent"}> = {
      id: "agent-agent-1",
      kind: "agent",
      turnID: "turn_external",
      sequence: 1,
      agentID: "agent-1",
      role: "review",
      taskName: "Review persistence",
      status: "running",
      summary: "Reading the history service",
      state: "running",
      activities: [{
        id: "activity-1",
        sequence: 2,
        kind: "tool",
        title: "Read",
        summary: "internal/persist/history/service.go",
        state: "running",
        callID: "call-1"
      }]
    };

    render(<AgentDisclosure entry={entry} onInspect={onInspect} />);

    expect(screen.getByRole("button", {name: /Review · agent-1/})
      .getAttribute("aria-expanded")).toBe("true");
    expect(screen.getByText("internal/persist/history/service.go")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", {name: "Inspect Read"}));
    expect(onInspect).toHaveBeenCalledWith("call-1");
  });
});
