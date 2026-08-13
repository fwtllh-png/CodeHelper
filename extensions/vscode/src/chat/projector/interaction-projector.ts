import { isUnknownEvent, type DecodedEvent } from "../../protocol/decode.js";
import { projectEditPlan } from "../../edits/model.js";
import { stringify } from "./helpers.js";
import type { MutableTurn } from "./model.js";
export function projectInteraction(event: DecodedEvent, turn: MutableTurn): boolean {
  if (isUnknownEvent(event)) return false;
  switch (event.kind) {
    case "approval.required":
      turn.reasoningActive = false;
      turn.status = "awaiting_approval";
      turn.approvals.set(event.data.request_id, {
        requestId: event.data.request_id,
        turnId: event.turn_id,
        itemId: event.item_id,
        tool: event.data.tool,
        arguments: stringify(event.data.arguments),
        resources: [
          ...event.data.resources.map((resource) =>
            `${resource.access}:${resource.path ?? resource.id ?? resource.kind}`),
          ...(event.data.network === undefined
            ? []
            : [`network:${event.data.network.protocol}://${event.data.network.host} ` +
              `(${event.data.network.mode})`]),
        ],
        allowedScopes: [...event.data.allowed_scopes],
        expiresAt: event.data.expires_at,
        ...(event.data.reason === undefined ? {} : { reason: event.data.reason }),
        ...(event.data.grant_preview === undefined
          ? {}
          : {
              grantPreview: {
                kind: event.data.grant_preview.kind,
                key: event.data.grant_preview.key,
                summary: event.data.grant_preview.summary,
              },
            }),
        ...(event.data.source === undefined
          ? {}
          : {
              source: {
                kind: "agent" as const,
                agentId: event.data.source.agent_id,
                agentPath: event.data.source.agent_path,
                parentPath: event.data.source.parent_path,
                role: event.data.source.role,
              },
            }),
        ...(event.data.edit_plan === undefined
          ? {}
          : { editPlan: projectEditPlan(event.data.edit_plan) }),
      });
      turn.timeline.push({
        id: `approval:${event.data.request_id}`,
        sequence: event.sequence,
        kind: "approval",
        requestId: event.data.request_id,
      });
      return true;
    case "approval.resolved": {
      const approval = turn.approvals.get(event.data.request_id);
      if (approval !== undefined) approval.resolved = event.data.decision;
      turn.status = "running";
      return true;
    }
    case "input.required":
      turn.reasoningActive = false;
      turn.status = "awaiting_input";
      turn.inputs.set(event.data.request_id, {
        requestId: event.data.request_id,
        turnId: event.turn_id,
        itemId: event.item_id,
        prompt: event.data.prompt,
        options: [...(event.data.options ?? [])],
        expiresAt: event.data.expires_at,
      });
      turn.timeline.push({
        id: `input:${event.data.request_id}`,
        sequence: event.sequence,
        kind: "input",
        requestId: event.data.request_id,
      });
      return true;
    case "input.resolved": {
      const input = turn.inputs.get(event.data.request_id);
      if (input !== undefined) input.resolved = event.data.answer ?? "";
      turn.status = "running";
      return true;
    }
    default:
      return false;
  }
}
