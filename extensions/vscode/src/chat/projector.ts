import {
  isUnknownEvent,
  type DecodedEvent,
} from "../protocol/decode.js";
import { eventTraits } from "../protocol/generated.js";
import type {
  ApprovalCard, ChatSnapshot, InputCard, MutableTurn, TurnStatus,
} from "./projector/model.js";
import { maxChatTurns } from "./projector/model.js";
import { truncate } from "./projector/helpers.js";
import { projectEvidence } from "./projector/evidence-projector.js";
import { projectInteraction } from "./projector/interaction-projector.js";
import { projectStream } from "./projector/stream-projector.js";
import { projectTerminal } from "./projector/terminal-projector.js";
import { projectTool } from "./projector/tool-projector.js";
import { projectSnapshot } from "./projector/snapshot.js";

export type {
  ApprovalCard, ChatSnapshot, ChatTurn, ContextReceiptCard,
  ContextSelectionCard, FileChangeCard, InputCard, PlanCard, ToolCard,
  TurnStatus, TurnTimelineItem, WorkspaceChangeCard,
} from "./projector/model.js";
export type { EditPlanCard, EditPlanFileCard } from "../edits/model.js";

export class ChatProjector {
  readonly #turns = new Map<string, MutableTurn>();
  #lastSequence = 0;
  #activeTurnId: string | undefined;

  public apply(event: DecodedEvent): boolean {
    if (event.sequence <= this.#lastSequence) {
      return false;
    }
    this.#lastSequence = event.sequence;
    if (!isUnknownEvent(event) && eventTraits[event.kind].class === "orchestration") {
      return false;
    }
    if (!isUnknownEvent(event) && event.kind === "checkpoint.restored") {
      for (const [turnId, candidate] of this.#turns) {
        if (candidate.lastSequence > event.data.source_cursor) {
          this.#turns.delete(turnId);
        }
      }
      this.#activeTurnId = undefined;
      return true;
    }
    if (!isUnknownEvent(event) &&
      (event.kind === "checkpoint.created" ||
        event.kind === "checkpoint.forked")) {
      return true;
    }
    const turn = this.#turn(event.turn_id);
    if (isUnknownEvent(event)) {
      turn.unknownEvents.push(truncate(JSON.stringify(event.raw)));
      return true;
    }
    const terminal = projectTerminal(event, turn);
    if (terminal !== undefined) {
      if (terminal !== "rejected") this.#terminal(turn, terminal);
      turn.lastSequence = event.sequence;
      return true;
    }
    if (projectInteraction(event, turn) || projectEvidence(event, turn) ||
      projectTool(event, turn) || projectStream(event, turn)) {
      if (event.kind === "turn.started") this.#activeTurnId = event.turn_id;
      turn.lastSequence = event.sequence;
      return true;
    }
    turn.lastSequence = event.sequence;
    return true;
  }

  public snapshot(): ChatSnapshot {
    return projectSnapshot(this.#turns, this.#activeTurnId);
  }

  public pendingApprovals(): readonly ApprovalCard[] {
    return [...this.#turns.values()].flatMap((turn) =>
      [...turn.approvals.values()]
        .filter((approval) => approval.resolved === undefined)
        .map((approval) => ({ ...approval })));
  }

  public pendingInputs(): readonly InputCard[] {
    return [...this.#turns.values()].flatMap((turn) =>
      [...turn.inputs.values()]
        .filter((input) => input.resolved === undefined)
        .map((input) => ({ ...input })));
  }

  #turn(id: string): MutableTurn {
    let turn = this.#turns.get(id);
    if (turn === undefined) {
      turn = {
        id,
        user: "",
        status: "running",
        output: "",
        reasoning: "",
        reasoningActive: false,
        timeline: [],
        tools: new Map(),
        approvals: new Map(),
        inputs: new Map(),
        contextReceipts: [],
        contextSelections: [],
        diagnostics: new Map(),
        diagnosticNotices: [],
        unknownEvents: [],
        lastSequence: 0,
      };
      this.#turns.set(id, turn);
      if (this.#turns.size > maxChatTurns) {
        const removable = [...this.#turns.values()].find(
          (candidate) => candidate.id !== this.#activeTurnId &&
            candidate.status !== "running" &&
            candidate.status !== "awaiting_approval" &&
            candidate.status !== "awaiting_input",
        );
        if (removable !== undefined) this.#turns.delete(removable.id);
      }
    }
    return turn;
  }

  #terminal(turn: MutableTurn, status: TurnStatus): void {
    turn.status = status;
    turn.reasoningActive = false;
    if (this.#activeTurnId === turn.id) {
      this.#activeTurnId = undefined;
    }
  }
}
