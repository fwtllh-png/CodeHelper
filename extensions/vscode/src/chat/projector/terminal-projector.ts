import { isUnknownEvent, type DecodedEvent } from "../../protocol/decode.js";
import { reconcileCompletedOutput, truncate } from "./helpers.js";
import type { MutableTurn, TurnStatus } from "./model.js";
export function projectTerminal(
  event: DecodedEvent,
  turn: MutableTurn,
): TurnStatus | "rejected" | undefined {
  if (isUnknownEvent(event)) return undefined;
  switch (event.kind) {
    case "turn.completed":
      if (event.data.text.length > 0) {
        reconcileCompletedOutput(turn, event.sequence, event.data.text);
      }
      return "completed";
    case "turn.failed":
      if (event.data.convergence !== undefined) {
        turn.convergence = {
          ...event.data.convergence,
          pending_actions: [...event.data.convergence.pending_actions],
        };
        delete turn.error;
        return "incomplete";
      }
      turn.error = truncate(`${event.data.code}: ${event.data.message}`);
      return "failed";
    case "turn.canceled":
      turn.error = truncate(event.data.reason);
      return "canceled";
    case "operation.rejected":
      turn.error = truncate(`${event.data.code}: ${event.data.message}`);
      return "rejected";
    default:
      return undefined;
  }
}
