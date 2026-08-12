import { isUnknownEvent, type DecodedEvent } from "../../protocol/decode.js";
import {
  appendBounded, appendReasoning, appendTimelineText, displayPrompt,
  projectContextReceipts, truncate,
} from "./helpers.js";
import type { MutableTurn } from "./model.js";
export function projectStream(event: DecodedEvent, turn: MutableTurn): boolean {
  if (isUnknownEvent(event)) return false;
  switch (event.kind) {
    case "turn.started":
      turn.user = displayPrompt(event.data.display_prompt ?? event.data.prompt ?? "");
      turn.status = "running";
      if (event.data.workspace !== undefined) turn.workspace = event.data.workspace;
      if (event.data.workspace_isolation !== undefined) {
        turn.workspaceIsolation = event.data.workspace_isolation;
      }
      turn.contextReceipts = projectContextReceipts(event.data.editor_context ?? []);
      return true;
    case "output.delta":
      turn.output = appendBounded(turn.output, event.data.text);
      appendTimelineText(turn, "output", event.sequence, event.data.text);
      turn.reasoningActive = false;
      return true;
    case "reasoning.delta":
      turn.reasoning = appendReasoning(turn.reasoning, event.data.text);
      appendTimelineText(turn, "reasoning", event.sequence, event.data.text);
      turn.reasoningActive = true;
      return true;
    case "reasoning.signature":
      turn.reasoningActive = false;
      return true;
    case "plan.delta": {
      const body = event.data.body ??
        appendBounded(turn.plan?.body ?? "", event.data.text ?? "");
      turn.plan = {
        ...(event.data.artifact_id === undefined ? {} : { id: event.data.artifact_id }),
        body: truncate(body),
        status: event.data.status === "ready" ? "ready" : "drafting",
        canImplement: event.data.can_implement === true,
        canAutopilot: event.data.can_autopilot === true,
      };
      return true;
    }
    default:
      return false;
  }
}
