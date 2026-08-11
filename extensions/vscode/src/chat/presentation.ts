import type { SupervisorState } from "../runtime/supervisor.js";
import type { ChatSnapshot } from "./projector.js";

export interface ChatPresentation {
  readonly journey: string;
  readonly runtimeReady: boolean;
  readonly promptEnabled: boolean;
  readonly sendEnabled: boolean;
  readonly stopEnabled: boolean;
  readonly newChatEnabled: boolean;
  readonly repairVisible: boolean;
  readonly emptyVisible: boolean;
}

export function deriveChatPresentation(
  runtimeState: SupervisorState,
  snapshot: ChatSnapshot,
  trusted: boolean,
): ChatPresentation {
  const runtimeReady = runtimeState === "ready";
  return {
    journey: journeyLabel(runtimeState, snapshot, trusted),
    runtimeReady,
    promptEnabled: runtimeReady,
    sendEnabled: runtimeReady,
    stopEnabled: runtimeReady && snapshot.activeTurnId !== undefined,
    newChatEnabled: runtimeReady,
    repairVisible: runtimeState === "failed" || runtimeState === "stopped",
    emptyVisible: runtimeReady && snapshot.turns.length === 0,
  };
}

function journeyLabel(
  runtimeState: SupervisorState,
  snapshot: ChatSnapshot,
  trusted: boolean,
): string {
  if (runtimeState === "recovering") {
    return "Recovery · Restoring Chat and cursor · Wait";
  }
  if (runtimeState === "starting") {
    return "Loading · Runtime starting · Wait";
  }
  if (runtimeState === "failed" || runtimeState === "stopped") {
    return "Failure · Runtime unavailable · Inspect and Repair";
  }
  if (!trusted) {
    return "Setup · Read-only workspace · Trust workspace or run Setup";
  }
  const active = snapshot.turns.find((turn) =>
    turn.id === snapshot.activeTurnId);
  if (active?.status === "awaiting_approval") {
    return "Approval · Review target and effect · Approve, Deny, or Cancel";
  }
  if (active?.status === "awaiting_input") {
    return "Input · Answer required · Choose or type a response";
  }
  if (active?.status === "running") {
    return "Streaming · Turn in progress · Stop is available";
  }
  const latest = snapshot.turns.at(-1);
  if (latest?.verification !== undefined) {
    return "Verify · Verdict available · Review checks and Receipt";
  }
  if (latest?.status === "failed" || latest?.status === "canceled") {
    return "Failure · Turn did not complete · Review reason and retry";
  }
  if (latest?.status === "completed") {
    return "Completed · Turn finished · Review changes and Receipt";
  }
  return "Empty · Ready for a task · Enter a prompt";
}
