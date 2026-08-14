import { isUnknownEvent, type DecodedEvent } from "../../protocol/decode.js";
import {
  projectContextReceipts, projectContextSelections, summarizeDiagnostics,
  truncate,
} from "./helpers.js";
import type { MutableTurn } from "./model.js";
export function projectEvidence(event: DecodedEvent, turn: MutableTurn): boolean {
  if (isUnknownEvent(event)) return false;
  switch (event.kind) {
    case "diagnostics.result":
      for (const receipt of event.data.receipts) {
        const detail = receipt.message ??
          `${String(receipt.diagnostics.length)} diagnostics`;
        turn.diagnostics.set(
          `${receipt.path}\u0000${receipt.status}\u0000${detail}`,
          receipt,
        );
      }
      turn.timeline.push({
        id: `diagnostics:${String(event.sequence)}`,
        sequence: event.sequence,
        kind: "diagnostics",
        messages: summarizeDiagnostics(event.data.receipts),
      });
      return true;
    case "turn.verification": {
      const verification = truncate(
        `${event.data.status}: ${(event.data.checks ?? []).map((check) => {
          const command = check.command ?? check.name;
          const category = check.category === undefined
            ? ""
            : ` [${check.category}]`;
          const reason = check.reason === undefined ? "" : ` because ${check.reason}`;
          return `${command}=${check.status}${category}${reason}`;
        }).join(", ")}`,
      );
      turn.verification = verification;
      turn.verificationBlocked = event.data.action === "blocked";
      turn.timeline.push({
        id: `verification:${String(event.sequence)}`,
        sequence: event.sequence,
        kind: "verification",
        text: verification,
      });
      return true;
    }
    case "turn.receipt": {
      turn.receipt = `tokens ${String(event.data.input_tokens)} in / ` +
        `${String(event.data.output_tokens)} out, cost ` +
        `${event.data.cost_known ? String(event.data.cost_microunits) : "unknown"} µ`;
      if (event.data.latency !== undefined) {
        turn.receipt += `; latency total=${String(event.data.latency.total_ms)}ms` +
          ` provider=${String(event.data.latency.provider_ms)}ms` +
          ` tools=${String(event.data.latency.tool_ms)}ms` +
          ` approval=${String(event.data.latency.approval_wait_ms)}ms`;
      }
      const risks = event.data.evidence?.risks ?? [];
      if (risks.length > 0) {
        turn.receipt += `; risks ${risks.map((risk) =>
          `${risk.kind}:${risk.path}`).join(",")}`;
      }
      if (event.data.verification_detail !== undefined) {
        const verification = event.data.verification_detail;
        turn.receipt += `; verify ${verification.final_status}` +
          ` action=${verification.action}` +
          ` repairs=${String(verification.repair_steps)}`;
      }
      if (event.data.workspace_outcome !== undefined) {
        turn.receipt += `; workspace ${event.data.workspace_outcome.status}`;
        const conflicts = event.data.workspace_outcome.conflicts ?? [];
        if (conflicts.length > 0) turn.receipt += ` conflicts=${conflicts.join(",")}`;
        if (turn.workspaceIsolation === "worktree" &&
          event.data.workspace_outcome.status === "changed") {
          turn.workspaceChange = {
            changedCount: event.data.workspace_outcome.changed?.length ?? 0,
            ...(turn.workspace === undefined ? {} : { workspace: turn.workspace }),
          };
          turn.receipt += "; isolated changes pending Merge → Apply";
          if (turn.workspace !== undefined) turn.receipt += `; worktree ${turn.workspace}`;
        }
      }
      turn.receipt = truncate(turn.receipt);
      if (event.data.editor_context !== undefined) {
        turn.contextReceipts = projectContextReceipts(event.data.editor_context);
      }
      turn.contextSelections = projectContextSelections(
        event.data.context_selections ?? [],
      );
      return true;
    }
    default:
      return false;
  }
}
