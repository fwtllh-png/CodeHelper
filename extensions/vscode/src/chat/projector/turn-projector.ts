import { isUnknownEvent, type DecodedEvent } from "../../protocol/decode.js";
import { eventTraits } from "../../protocol/generated.js";
import { projectEvidence } from "./evidence-projector.js";
import { projectInteraction } from "./interaction-projector.js";
import type { MutableTurn, TurnStatus } from "./model.js";
import { projectStream } from "./stream-projector.js";
import { projectTerminal } from "./terminal-projector.js";
import { projectTool } from "./tool-projector.js";
export type TurnProjection = { readonly kind: "handled" | "ignored" } |
  { readonly kind: "terminal"; readonly status: TurnStatus | "rejected" };
export function projectTurnEvent(
  event: DecodedEvent,
  turn: MutableTurn,
): TurnProjection {
  if (isUnknownEvent(event)) return { kind: "ignored" };
  const eventClass = eventTraits[event.kind].class;
  switch (eventClass) {
    case "terminal":
    case "terminal_operation": {
      const status = projectTerminal(event, turn);
      return status === undefined ? { kind: "ignored" } : { kind: "terminal", status };
    }
    case "interaction":
      return handled(projectInteraction(event, turn));
    case "evidence":
      return handled(projectEvidence(event, turn));
    case "stream":
    case "lifecycle":
    case "artifact_stream":
    case "audit":
      return handled(projectTool(event, turn) || projectStream(event, turn));
    case "accounting":
    case "artifact":
      return { kind: "handled" };
    case "orchestration":
      return { kind: "ignored" };
    default:
      return assertEventClass(eventClass);
  }
}
function handled(projected: boolean): TurnProjection {
  return { kind: projected ? "handled" : "ignored" };
}
function assertEventClass(value: never): never {
  throw new Error(`unhandled Event Class: ${String(value satisfies never)}`);
}
