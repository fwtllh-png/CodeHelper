import type { TurnIntent } from "../runtime/session.js";

export function turnIntentForMode(mode: string | undefined): TurnIntent {
  switch (mode) {
    case "plan":
      return "plan";
    case "operate":
      return "operation";
    default:
      return "answer";
  }
}
